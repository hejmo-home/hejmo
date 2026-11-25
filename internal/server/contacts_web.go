package server

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The address book (ticket 296, ADR 0021): the pages of parents.

const contactsPath = "/contacts"

// kidsFilter is the value of ?g= of the chip "Für Kinder sichtbar": no group id looks like it.
const kidsFilter = "kids"

// ctEnv is what the pages of the address book share: the language, the household, the people with their markers and the groups.
type ctEnv struct {
	s      *server
	lang   string
	p      principal
	people []store.Person
	chips  map[string]personChip
	groups []store.ContactGroup
}

func (s *server) contactEnv(w http.ResponseWriter, r *http.Request, p principal) (ctEnv, bool) {
	people, ok := s.peopleOf(w, p)
	if !ok {
		return ctEnv{}, false
	}
	groups, err := s.db.ContactGroups(p.household.ID)
	if err != nil {
		s.fail(w, "contact groups", err)
		return ctEnv{}, false
	}
	e := ctEnv{s: s, lang: s.langOf(r), p: p, people: people, chips: map[string]personChip{}, groups: groups}
	for _, c := range chipsOf(people) {
		e.chips[c.ID] = c
	}
	return e, true
}

func (e ctEnv) t(key string, kv ...string) string {
	if len(kv) == 0 {
		return e.s.i18n.T(e.lang, key)
	}
	return e.s.i18n.Format(e.lang, i18n.Msg(key, kv...))
}

// groupName is the name of a group as the pages show it: the five of Hejmo are in the locales.
func (e ctEnv) groupName(g store.ContactGroup) string {
	if g.ShippedKey != "" {
		return e.t("contact.group." + g.ShippedKey)
	}
	return g.Name
}

func (e ctEnv) groupByID(id string) (store.ContactGroup, bool) {
	i := slices.IndexFunc(e.groups, func(g store.ContactGroup) bool { return g.ID == id })
	if i < 0 {
		return store.ContactGroup{}, false
	}
	return e.groups[i], true
}

// kindWord is what a number or an e-mail address is, in words: the kind, or the label the household chose.
func (e ctEnv) kindWord(l contact.Line, email bool) string {
	if l.Kind == contact.Custom {
		return l.Label
	}
	if email {
		return e.t("contact.kind.email." + string(l.Kind))
	}
	return e.t("contact.kind." + string(l.Kind))
}

// factLabel is the label of an e-mail address on the contact page: "E-Mail privat", "E-Mail Verein".
func (e ctEnv) factLabel(l contact.Line, email bool) string {
	if !email {
		return e.kindWord(l, false)
	}
	if l.Kind == contact.Custom {
		return e.t("contact.fact.email.custom", "label", l.Label)
	}
	return e.t("contact.fact.email." + string(l.Kind))
}

func (e ctEnv) month(m int) string { return e.t("contact.month." + strconv.Itoa(m)) }

// birthday writes the birthday in the household's format: "14.03.1956 · 70 Jahre", or "14.03. · Jahr unbekannt".
func (e ctEnv) birthday(c store.Contact, withAge bool) string {
	if c.BdayDay == 0 {
		return ""
	}
	if c.BdayYear == 0 {
		return datefmt.Short(e.p.household.DateFormat, time.Date(2000, time.Month(c.BdayMonth), c.BdayDay, 0, 0, 0, 0, time.UTC)) + " · " + e.t("contact.year_unknown")
	}
	out := datefmt.Date(e.p.household.DateFormat, time.Date(c.BdayYear, time.Month(c.BdayMonth), c.BdayDay, 0, 0, 0, 0, time.UTC))
	if !withAge {
		return out
	}
	if age := contact.Age(c.BdayDay, c.BdayMonth, c.BdayYear, e.s.householdNow(e.p.household)); age >= 0 {
		key := "contact.age.many"
		if age == 1 {
			key = "contact.age.one"
		}
		out += " · " + e.t(key, "n", strconv.Itoa(age))
	}
	return out
}

// ctNum is a number with its kind in words.
type ctNum struct{ Label, Value string }

// ctRow is a contact in the overview.
type ctRow struct {
	Href, Name, Full string
	Person           bool
	Slot             int
	Shape            string
	Nums             []ctNum
	Groups           []string
	Rels             []string // how the contact is related to the family, one sentence each (ticket 298)
	Photo            ctPhotoView
	// Kids is "on" for a contact the children see, "wait" for one switched on that has no number yet (ticket 301).
	Kids  string
	Fresh string // "Gerade übernommen" or "Gerade ergänzt" right after an import (ticket 300), for one page load
}

func (e ctEnv) row(c store.Contact, withGroups bool) ctRow {
	row := ctRow{Href: contactsPath + "/" + c.ID, Name: c.HejmoName, Photo: photoViewOf(c)}
	if c.FullName != "" && !strings.EqualFold(c.FullName, c.HejmoName) {
		row.Full = c.FullName
	}
	if chip, ok := e.chips[c.PersonID]; ok && c.IsPerson() {
		row.Person, row.Slot, row.Shape = true, chip.Slot, chip.Shape
	}
	for _, l := range c.Numbers {
		row.Nums = append(row.Nums, ctNum{e.kindWord(l, false), l.Value})
	}
	row.Rels = e.relSentences(c, "")
	switch {
	case c.ForChildren && len(c.Numbers) > 0:
		row.Kids = "on"
	case c.ForChildren:
		row.Kids = "wait"
	}
	if withGroups && !c.IsPerson() {
		for _, id := range c.GroupIDs {
			if g, ok := e.groupByID(id); ok {
				row.Groups = append(row.Groups, e.groupName(g))
			}
		}
	}
	return row
}

// matches reports whether a contact holds the search text in a name, a number, an e-mail address or the word of a relation.
func ctMatches(c store.Contact, q string) bool {
	q = library.Fold(q)
	if q == "" {
		return true
	}
	if strings.Contains(library.Fold(c.HejmoName), q) || strings.Contains(library.Fold(c.FullName), q) {
		return true
	}
	for _, r := range c.Relations {
		if strings.Contains(library.Fold(r.Word), q) {
			return true
		}
	}
	digits := onlyDigits(q)
	for _, l := range c.Numbers {
		if strings.Contains(library.Fold(l.Value), q) || (len(digits) >= 3 && strings.Contains(onlyDigits(l.Value), digits)) {
			return true
		}
	}
	for _, l := range c.Emails {
		if strings.Contains(library.Fold(l.Value), q) {
			return true
		}
	}
	return false
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *server) handleContacts(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	all, err := s.db.Contacts(p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	g := r.URL.Query().Get("g")
	group, hasGroup := e.groupByID(g)
	kidsOnly := g == kidsFilter // the last chip: the contacts the children see
	if !hasGroup && !kidsOnly {
		g = ""
	}
	var others, family []store.Contact
	for _, c := range all {
		if c.IsPerson() {
			family = append(family, c)
		} else {
			others = append(others, c)
		}
	}
	data := map[string]any{"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Q": q, "G": g, "HasOthers": len(others) > 0}
	filterHref := func(g string) string {
		v := url.Values{}
		if q != "" {
			v.Set("q", q)
		}
		if g != "" {
			v.Set("g", g)
		}
		if len(v) == 0 {
			return contactsPath
		}
		return "?" + v.Encode()
	}
	filters := []map[string]any{{"Href": filterHref(""), "Text": e.t("contact.filter.all"), "Current": g == ""}}
	for _, grp := range e.groups {
		filters = append(filters, map[string]any{"Href": filterHref(grp.ID), "Text": e.groupName(grp), "Current": g == grp.ID})
	}
	filters = append(filters, map[string]any{"Href": filterHref(kidsFilter), "Text": e.t("contact.filter.kids"), "Current": kidsOnly})
	data["Filters"] = filters

	keep := func(list []store.Contact) []store.Contact {
		var out []store.Contact
		for _, c := range list {
			if (hasGroup && !slices.Contains(c.GroupIDs, group.ID)) || (kidsOnly && !c.ForChildren) || !ctMatches(c, q) {
				continue
			}
			out = append(out, c)
		}
		return out
	}
	shownOthers, shownFamily := keep(others), keep(family)
	sortFurther(e.lang, shownOthers)
	var otherRows, familyRows []ctRow
	fresh := s.takeFresh(w, r)
	for _, c := range shownOthers {
		row := e.row(c, true)
		switch fresh[c.ID] {
		case "new":
			row.Fresh = e.t("cimp.tag.new")
		case "completed":
			row.Fresh = e.t("cimp.tag.completed")
		}
		otherRows = append(otherRows, row)
	}
	for _, c := range shownFamily { // in the order of people
		row := e.row(c, false)
		if fresh[c.ID] == "completed" {
			row.Fresh = e.t("cimp.tag.completed")
		}
		familyRows = append(familyRows, row)
	}
	data["Others"], data["Family"] = otherRows, familyRows
	filtering := q != "" || hasGroup || kidsOnly
	switch {
	case len(otherRows)+len(familyRows) == 0 && q != "":
		data["NoMatch"] = true
		data["NoneText"] = e.t("contact.search.none", "q", q)
		data["NewHref"] = contactsPath + "/new?" + url.Values{"name": {q}}.Encode()
		data["NewText"] = e.t("contact.search.new", "q", q)
		data["ClearHref"], data["ClearText"], data["ResultTitle"] = filterHref0(g), e.t("contact.result.clear"), e.t("contact.result.title")
	case len(otherRows)+len(familyRows) == 0 && kidsOnly:
		data["NoMatch"] = true
		data["NoneText"] = e.t("contact.filter.kids.none")
		data["ClearHref"], data["ClearText"], data["ResultTitle"] = contactsPath, e.t("contact.filter.clear"), e.t("contact.filter.kids")
	case len(otherRows)+len(familyRows) == 0 && hasGroup:
		data["NoMatch"] = true
		data["NoneText"] = e.t("contact.filter.none", "group", e.groupName(group))
		data["ClearHref"], data["ClearText"], data["ResultTitle"] = contactsPath, e.t("contact.filter.clear"), e.groupName(group)
	}
	switch {
	case kidsOnly:
		data["OthersTitle"] = e.t("contact.others.in", "group", e.t("contact.filter.kids"))
	case hasGroup:
		data["OthersTitle"] = e.t("contact.others.in", "group", e.groupName(group))
	default:
		data["OthersTitle"] = e.t("contact.others")
	}
	data["Filtering"] = filtering
	data["OthersEmpty"] = len(others) == 0
	s.importSweep()
	s.render(w, r, http.StatusOK, "contacts.html", data)
}

// filterHref0 is the overview with the group filter but without the search.
func filterHref0(g string) string {
	if g == "" {
		return contactsPath
	}
	return "?" + url.Values{"g": {g}}.Encode()
}

// ctFact is one line of a contact's page.
type ctFact struct {
	Label, Value string
	Tel          template.URL // tel: with the digits and a leading + only, so safe for a link
	Mail         string
	Kind         string // call, mail
}

func (s *server) handleContact(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	c, err := s.db.Contact(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "contact", err)
		return
	}
	data := map[string]any{"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "ID": c.ID, "Name": c.HejmoName, "Note": c.Note,
		"Photo": photoViewOf(c), "PhotoAlt": e.t("contact.photo.alt", "name", c.HejmoName), "HasPhoto": c.HasPhoto}
	data["PhotoLink"] = e.t("contact.photo.link.add")
	if c.HasPhoto {
		data["PhotoLink"] = e.t("contact.photo.link.change")
	}
	if c.FullName != "" && !strings.EqualFold(c.FullName, c.HejmoName) {
		data["Full"] = c.FullName
	}
	if c.IsPerson() {
		chip := e.chips[c.PersonID]
		data["IsPerson"], data["Slot"], data["Shape"] = true, chip.Slot, chip.Shape
		data["PersonHref"] = "/people/" + c.PersonID
	}
	var facts []ctFact
	for _, l := range c.Numbers {
		facts = append(facts, ctFact{Label: e.kindWord(l, false), Value: l.Value, Tel: template.URL(contact.TelHref(l.Value)), Kind: "call"})
	}
	data["NoNumber"] = len(c.Numbers) == 0
	for _, l := range c.Emails {
		facts = append(facts, ctFact{Label: e.factLabel(l, true), Value: l.Value, Mail: "mailto:" + l.Value, Kind: "mail"})
	}
	data["Facts"] = facts
	data["Rels"] = e.relItems(c)
	var adr []string
	if c.Street != "" {
		adr = append(adr, c.Street)
	}
	if line := strings.TrimSpace(c.Postcode + " " + c.City); line != "" {
		adr = append(adr, line)
	}
	if c.Country != "" {
		adr = append(adr, c.Country)
	}
	data["Address"] = adr
	data["Birthday"] = e.birthday(c, true)
	data["NameDay"] = e.nameDayFact(c, s.householdNow(p.household))
	var groups []string
	for _, id := range c.GroupIDs {
		if g, ok := e.groupByID(id); ok {
			groups = append(groups, e.groupName(g))
		}
	}
	data["Groups"] = groups
	data["KidsFact"] = e.kidsFact(c)
	hint := e.photoHint(c)
	if !c.IsPerson() {
		if hint != "" {
			hint = e.createdHint(c) + " · " + hint
		} else {
			hint = e.createdHint(c)
		}
		data["DeleteExplain"] = e.relDeleteExplain(c)
		data["DeleteConfirm"] = e.t("contact.delete.confirm", "name", c.HejmoName)
	}
	data["Hint"] = hint
	s.render(w, r, http.StatusOK, "contact.html", data)
}

// createdHint says who made a contact and who changed it last, with the time of the change to the second. A
// contact taken over from a contact file, or completed from one, names the file (ticket 300).
func (e ctEnv) createdHint(c store.Contact) string {
	day := func(t time.Time) string { return e.p.day(t) }
	out := e.t("contact.hint.created", "name", c.CreatedByName, "date", day(c.CreatedAt))
	switch {
	case c.CreatedFile != "" && c.CreatedByName != "":
		out = e.t("contact.hint.created_file", "file", c.CreatedFile, "name", c.CreatedByName, "date", day(c.CreatedAt))
	case c.CreatedFile != "":
		out = e.t("contact.hint.created_file.anon", "file", c.CreatedFile, "date", day(c.CreatedAt))
	case c.CreatedByName == "":
		out = e.t("contact.hint.created.anon", "date", day(c.CreatedAt))
	}
	if c.UpdatedAt.After(c.CreatedAt) || c.UpdatedFile != "" {
		when := e.p.dateTimeSeconds(c.UpdatedAt)
		var changed string
		switch {
		case c.UpdatedFile != "" && c.UpdatedByName != "":
			changed = e.t("contact.hint.changed_file", "file", c.UpdatedFile, "name", c.UpdatedByName, "when", when)
		case c.UpdatedFile != "":
			changed = e.t("contact.hint.changed_file.anon", "file", c.UpdatedFile, "when", when)
		case c.UpdatedByName == "":
			changed = e.t("contact.hint.changed.anon", "when", when)
		default:
			changed = e.t("contact.hint.changed", "name", c.UpdatedByName, "when", when)
		}
		out += " · " + changed
	}
	return out
}

func (s *server) handleContactDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, err := s.db.Contact(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "contact", err)
		return
	}
	if c.IsPerson() { // a person's contact goes with the person
		http.Error(w, "the contact of a person goes with the person", http.StatusUnprocessableEntity)
		return
	}
	if err := s.db.DeleteContact(p.household.ID, c.ID); err != nil {
		s.fail(w, "delete contact", err)
		return
	}
	s.redirectSavedNamed(w, r, contactsPath, "flash.contact_deleted", c.HejmoName)
}

// personContactRow is the row "Kontaktdaten" of a person's page.
type ctPersonRow struct {
	Href, Title string
	Lines       []string
	Empty       bool
	Photo       ctPhotoView
}

func (s *server) personContactRow(r *http.Request, p principal, personID string) (ctPersonRow, bool) {
	c, err := s.db.Contact(p.household.ID, personID)
	if err != nil {
		return ctPersonRow{}, false
	}
	e := ctEnv{s: s, lang: s.langOf(r), p: p}
	row := ctPersonRow{Href: contactsPath + "/" + c.ID, Photo: photoViewOf(c)}
	if c.FullName == "" && len(c.Numbers) == 0 && len(c.Emails) == 0 && c.Street == "" && c.City == "" && c.Postcode == "" && c.BdayDay == 0 {
		row.Empty, row.Title = true, e.t("contact.person.empty")
		row.Lines = []string{e.t("contact.person.empty.hint")}
		return row, true
	}
	row.Title = c.FullName
	if row.Title == "" {
		row.Title = c.HejmoName
	}
	if len(c.Numbers) == 0 {
		row.Lines = append(row.Lines, e.t("contact.nonumber"))
	}
	for _, l := range c.Numbers {
		row.Lines = append(row.Lines, e.kindWord(l, false)+" "+l.Value)
	}
	if c.BdayDay != 0 {
		row.Lines = append(row.Lines, e.t("contact.person.birthday", "date", e.birthday(c, true)))
	}
	return row, true
}

// contactSubpage answers the two-part paths below /contacts: /contacts/groups/{id} and /contacts/{id}/edit,
// and the POST /contacts/{id}/delete.
func (s *server) contactSubpage(w http.ResponseWriter, r *http.Request) {
	first, second := r.PathValue("first"), r.PathValue("second")
	post := r.Method == http.MethodPost
	switch {
	case first == "groups":
		if second == "edit" || second == "delete" { // no group has these ids
			http.NotFound(w, r)
			return
		}
		r.SetPathValue("id", second)
		if post {
			s.handleContactGroupRename(w, r)
		} else {
			s.handleContactGroup(w, r)
		}
	case second == "edit":
		r.SetPathValue("id", first)
		if post {
			s.handleContactSave(w, r)
		} else {
			s.handleContactEdit(w, r)
		}
	case second == "photo" && !post:
		r.SetPathValue("id", first)
		s.handleContactPhoto(w, r)
	case second == "delete" && post:
		r.SetPathValue("id", first)
		s.handleContactDelete(w, r)
	default:
		http.NotFound(w, r)
	}
}
