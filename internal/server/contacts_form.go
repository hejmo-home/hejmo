package server

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

// ctLineForm is a number or e-mail line as the form holds it: the value of the kind select, what was typed as an own
// label, and the value.
type ctLineForm struct{ Kind, Own, Value string }

// ctForm is everything the contact form posts, as typed.
type ctForm struct {
	Name, Full                      string
	Phones, Emails                  []ctLineForm
	Street, Postcode, City, Country string
	Day, Month, Year                string
	Groups                          []string
	NewGroup, Note                  string
}

const ownKind = "own"        // the select option "Eigene Bezeichnung …"
const labelPrefix = "label:" // a saved own label, as an option of its own

func parseLines(r *http.Request, base string) []ctLineForm {
	kinds, own, values := r.PostForm[base+"_label"], r.PostForm[base+"_own"], r.PostForm[base]
	n := max(len(kinds), len(own), len(values))
	var out []ctLineForm
	for i := 0; i < n; i++ {
		l := ctLineForm{}
		if i < len(kinds) {
			l.Kind = kinds[i]
		}
		if i < len(own) {
			l.Own = own[i]
		}
		if i < len(values) {
			l.Value = values[i]
		}
		out = append(out, l)
	}
	return out
}

func parseContactForm(r *http.Request) ctForm {
	_ = r.ParseForm()
	f := ctForm{
		Name: r.PostFormValue("name"), Full: r.PostFormValue("full_name"),
		Phones: parseLines(r, "phone"), Emails: parseLines(r, "email"),
		Street: r.PostFormValue("street"), Postcode: r.PostFormValue("postcode"), City: r.PostFormValue("city"), Country: r.PostFormValue("country"),
		Day: r.PostFormValue("bday_day"), Month: r.PostFormValue("bday_month"), Year: r.PostFormValue("bday_year"),
		Groups: r.PostForm["group"], NewGroup: r.PostFormValue("new_group"), Note: strings.ReplaceAll(r.PostFormValue("note"), "\r\n", "\n"),
	}
	return f
}

// lineOf turns what the form holds for a line into a line of the contact.
func (l ctLineForm) line(phone bool) contact.Line {
	out := contact.Line{Value: l.Value}
	switch {
	case l.Kind == ownKind:
		out.Kind, out.Label = contact.Custom, l.Own
	case strings.HasPrefix(l.Kind, labelPrefix):
		out.Kind, out.Label = contact.Custom, strings.TrimPrefix(l.Kind, labelPrefix)
	case contact.KindValid(contact.Kind(l.Kind)) && l.Kind != string(contact.Custom) && (phone || l.Kind != string(contact.Cell)):
		out.Kind = contact.Kind(l.Kind)
	case phone:
		out.Kind = contact.Cell
	default:
		out.Kind = contact.Home
	}
	return out
}

func atoiOrZero(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0
}

// draft turns the form into a draft the rules can check.
func (f ctForm) draft(isPerson bool) ctDraft {
	d := ctDraft{Name: f.Name, Full: f.Full, Street: f.Street, Postcode: f.Postcode, City: f.City, Country: f.Country, Note: f.Note,
		NewGroup: f.NewGroup, GroupIDs: f.Groups, IsPerson: isPerson}
	for _, l := range f.Phones {
		d.Phones = append(d.Phones, l.line(true))
	}
	for _, l := range f.Emails {
		d.Emails = append(d.Emails, l.line(false))
	}
	var ok1, ok2, ok3 bool
	d.Day, ok1 = atoiOrZero(f.Day)
	d.Month, ok2 = atoiOrZero(f.Month)
	d.Year, ok3 = atoiOrZero(f.Year)
	d.BdayNotNumbers = !(ok1 && ok2 && ok3)
	return d
}

// ctFormOf is the form of a stored contact.
func ctFormOf(c store.Contact) ctForm {
	f := ctForm{Name: c.HejmoName, Full: c.FullName, Street: c.Street, Postcode: c.Postcode, City: c.City, Country: c.Country, Note: c.Note, Groups: c.GroupIDs}
	for _, l := range c.Phones {
		f.Phones = append(f.Phones, lineFormOf(l))
	}
	for _, l := range c.Emails {
		f.Emails = append(f.Emails, lineFormOf(l))
	}
	if c.BdayDay != 0 {
		f.Day, f.Month = strconv.Itoa(c.BdayDay), strconv.Itoa(c.BdayMonth)
	}
	if c.BdayYear != 0 {
		f.Year = strconv.Itoa(c.BdayYear)
	}
	return f
}

func lineFormOf(l contact.Line) ctLineForm {
	if l.Kind == contact.Custom {
		return ctLineForm{Kind: labelPrefix + l.Label, Value: l.Value}
	}
	return ctLineForm{Kind: string(l.Kind), Value: l.Value}
}

// ctOption is an option of a kind select.
type ctOption struct {
	Value, Text string
	Selected    bool
}

// ctLineView is a line of the form with its options and its errors.
type ctLineView struct {
	N                               int
	ID                              string
	Options                         []ctOption
	Own, Value                      string
	OwnSelected                     bool
	Blank                           bool
	Err, OwnErr                     string
	AriaKind, AriaValue, AriaRemove string
}

func (e ctEnv) lineViews(lines []ctLineForm, phone bool, errs map[string]string) []ctLineView {
	base, kinds := "email", contact.EmailKinds
	if phone {
		base, kinds = "phone", contact.PhoneKinds
	}
	if len(lines) == 0 {
		lines = []ctLineForm{{}}
	}
	var out []ctLineView
	for i, l := range lines {
		n := i + 1
		v := ctLineView{N: n, ID: base + strconv.Itoa(n), Own: l.Own, Value: l.Value, Blank: len(lines) == 1 && strings.TrimSpace(l.Value) == "",
			Err: errs[base+strconv.Itoa(n)], OwnErr: errs[base+"_own"+strconv.Itoa(n)]}
		sel := l.Kind
		if sel == "" {
			sel = string(kinds[0])
		}
		for _, k := range kinds {
			text := e.kindWord(contact.Line{Kind: k}, !phone)
			v.Options = append(v.Options, ctOption{Value: string(k), Text: text, Selected: sel == string(k)})
		}
		if label, ok := strings.CutPrefix(sel, labelPrefix); ok {
			v.Options = append(v.Options, ctOption{Value: sel, Text: label, Selected: true})
		}
		v.OwnSelected = sel == ownKind
		v.Options = append(v.Options, ctOption{Value: ownKind, Text: e.t("contact.kind.own"), Selected: v.OwnSelected})
		ks := "contact.aria.phone.kind"
		vs, rs := "contact.aria.phone", "contact.aria.phone.remove"
		if !phone {
			ks, vs, rs = "contact.aria.email.kind", "contact.aria.email", "contact.aria.email.remove"
		}
		v.AriaKind, v.AriaValue, v.AriaRemove = e.t(ks, "n", strconv.Itoa(n)), e.t(vs, "n", strconv.Itoa(n)), e.t(rs, "n", strconv.Itoa(n))
		out = append(out, v)
	}
	return out
}

// lineTemplate is an empty line with {n} where its number goes: the page holds it in a template element and the
// script adds a line from it without a round trip. The page without the script adds the line on the server.
func (e ctEnv) lineTemplate(phone bool) ctLineView {
	base := "email"
	if phone {
		base = "phone"
	}
	v := e.lineViews(nil, phone, nil)[0]
	v.Blank, v.N, v.ID = false, 0, base+"{n}"
	ks, vs, rs := "contact.aria.phone.kind", "contact.aria.phone", "contact.aria.phone.remove"
	if !phone {
		ks, vs, rs = "contact.aria.email.kind", "contact.aria.email", "contact.aria.email.remove"
	}
	v.AriaKind, v.AriaValue, v.AriaRemove = e.t(ks, "n", "{n}"), e.t(vs, "n", "{n}"), e.t(rs, "n", "{n}")
	return v
}

// ctErrLink is a link of the line above the form to a wrong part of it.
type ctErrLink struct{ Anchor, Text, Sep string }

// problemText is what a problem says to the person, with what to do.
func (e ctEnv) problemText(pr ctProblem, d ctDraft) string {
	switch pr.Field + "." + pr.Code {
	case "name.required":
		return e.t("contact.error.name.required")
	case "name.long":
		return e.t("contact.error.name.long")
	case "name.taken_person":
		return e.t("contact.error.name.taken_person", "name", strings.TrimSpace(d.Name), "existing", pr.Arg, "examples", e.nameExamples(d))
	case "name.taken":
		return e.t("contact.error.name.taken", "name", strings.TrimSpace(d.Name), "existing", pr.Arg, "examples", e.nameExamples(d))
	case "full_name.long":
		return e.t("contact.error.full.long")
	case "phone.char", "email.char":
		return e.t("contact.error.phone.char", "char", pr.Arg)
	case "phone.digits":
		return e.t("contact.error.phone.digits")
	case "email.at":
		return e.t("contact.error.email.at")
	case "email.dot":
		return e.t("contact.error.email.dot")
	case "email.space":
		return e.t("contact.error.email.space")
	case "phone_own.label_required", "email_own.label_required":
		return e.t("contact.error.label.required")
	case "phone_own.label_long", "email_own.label_long":
		return e.t("contact.error.label.long")
	case "phones.many":
		return e.t("contact.error.phones.many")
	case "emails.many":
		return e.t("contact.error.emails.many")
	case "birthday.pair":
		return e.t("contact.error.birthday.pair")
	case "birthday.year":
		return e.t("contact.error.birthday.year", "max", strconv.Itoa(e.s.householdNow(e.p.household).Year()))
	case "birthday.future":
		return e.t("contact.error.birthday.future")
	case "birthday.date":
		if d.Day != 0 && d.Month >= 1 && d.Month <= 12 {
			if d.Year != 0 && d.Day <= contact.DaysIn(d.Month, 0) { // only the year makes it impossible: 29 February
				return e.t("contact.error.birthday.date.year", "day", strconv.Itoa(d.Day), "month", e.month(d.Month), "year", strconv.Itoa(d.Year))
			}
			return e.t("contact.error.birthday.date", "day", strconv.Itoa(d.Day), "month", e.month(d.Month))
		}
		return e.t("contact.error.birthday.nan")
	case "note.long":
		return e.t("contact.error.note.long")
	case "new_group.long":
		return e.t("contact.error.group.long")
	case "new_group.taken":
		return e.t("contact.error.group.taken", "name", pr.Arg)
	}
	return pr.String()
}

// nameExamples are two names that would not clash: the name with a hint in brackets, and the full name when it differs.
func (e ctEnv) nameExamples(d ctDraft) string {
	name, full := strings.TrimSpace(d.Name), strings.TrimSpace(d.Full)
	a := name + " (" + e.t("contact.error.name.suffix") + ")"
	if full != "" && !strings.EqualFold(full, name) {
		return e.t("contact.error.name.examples2", "a", a, "b", full)
	}
	return e.t("contact.error.name.examples1", "a", a)
}

// anchorText is the name of a part of the form in the line above it.
func (e ctEnv) anchorText(pr ctProblem) string {
	switch {
	case pr.Anchor == "name" && pr.Field == "full_name":
		return e.t("contact.link.full")
	case pr.Anchor == "name":
		return e.t("contact.link.name")
	case pr.Field == "phone" || pr.Field == "phone_own":
		return e.t("contact.link.phone", "n", strconv.Itoa(pr.N))
	case pr.Field == "email" || pr.Field == "email_own":
		return e.t("contact.link.email", "n", strconv.Itoa(pr.N))
	case pr.Field == "phones":
		return e.t("contact.link.phones")
	case pr.Field == "emails":
		return e.t("contact.link.emails")
	case pr.Field == "birthday":
		return e.t("contact.link.birthday")
	case pr.Field == "note":
		return e.t("contact.link.note")
	}
	return e.t("contact.link.group")
}

// ctFormState is what a render of the form needs besides the form itself.
type ctFormState struct {
	Contact  *store.Contact // nil for a new contact
	Problems []ctProblem
	Draft    ctDraft
	Status   int
}

// groupChoices are the chips of the groups, ticked as the form has them.
func (e ctEnv) groupChoices(f ctForm, c *store.Contact) (chips []map[string]any, fam string) {
	for _, g := range e.groups {
		if c != nil && c.IsPerson() && g.ShippedKey == store.FamilyGroup {
			fam = e.groupName(g)
			continue
		}
		chips = append(chips, map[string]any{"ID": g.ID, "Name": e.groupName(g), "Checked": slices.Contains(f.Groups, g.ID)})
	}
	return chips, fam
}

func (s *server) renderContactForm(w http.ResponseWriter, r *http.Request, e ctEnv, f ctForm, st ctFormState) {
	p := e.p
	errs := map[string]string{}
	var links []ctErrLink
	seen := map[string]bool{}
	for _, pr := range st.Problems {
		key := pr.Field
		if pr.N > 0 {
			key += strconv.Itoa(pr.N)
		}
		if _, ok := errs[key]; !ok {
			errs[key] = e.problemText(pr, st.Draft)
		}
		if !seen[pr.Anchor] {
			seen[pr.Anchor] = true
			links = append(links, ctErrLink{Anchor: pr.Anchor, Text: e.anchorText(pr)})
		}
	}
	for i := range links {
		if i < len(links)-1 {
			links[i].Sep = ", "
		}
	}
	// A line of the form is wrong in its value or in its own label: the errors are found by field and line.
	lineErrs := map[string]string{}
	for k, v := range errs {
		lineErrs[k] = v
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "F": f, "Errs": errs, "ErrLinks": links,
		"Phones": e.lineViews(f.Phones, true, lineErrs), "Emails": e.lineViews(f.Emails, false, lineErrs),
		"Months": e.months(f.Month), "MDY": p.household.DateFormat == store.DateMDY, "MaxYear": s.householdNow(p.household).Year(),
		"NoteCount": contact.Runes(strings.TrimSpace(f.Note)),
		"BdayErr":   errs["birthday"] != "", "NewGroupOpen": f.NewGroup != "" || errs["new_group"] != "",
		"MorePhones": len(f.Phones) < contact.MaxLines, "MoreEmails": len(f.Emails) < contact.MaxLines,
		"CountryHint": e.countryHint(), "PhoneTpl": e.lineTemplate(true), "EmailTpl": e.lineTemplate(false),
	}
	if len(links) > 0 {
		key := "contact.error.summary.many"
		if len(links) == 1 {
			key = "contact.error.summary.one"
		}
		data["ErrLead"] = e.t(key, "n", strconv.Itoa(len(links)))
		data["ErrEnd"] = e.t("contact.error.summary.end")
	}
	chips, fam := e.groupChoices(f, st.Contact)
	data["GroupChips"], data["FamChip"] = chips, fam
	if c := st.Contact; c != nil {
		data["Editing"] = true
		data["CT"] = map[string]string{"ID": c.ID, "Name": c.HejmoName}
		data["Action"] = contactsPath + "/" + c.ID + "/edit"
		data["Title"] = e.t("contact.edit.title")
		if c.IsPerson() {
			chip := e.chips[c.PersonID]
			data["IsPerson"], data["Slot"], data["Shape"] = true, chip.Slot, chip.Shape
			data["PersonName"] = c.HejmoName
			data["PersonHref"] = "/people/" + c.PersonID
			data["PersonLink"] = e.t("contact.person.link", "name", c.HejmoName)
			data["FamHint"] = e.t("contact.group.fam.hint", "name", c.HejmoName)
		}
	} else {
		data["Action"] = contactsPath + "/new"
		data["Title"] = e.t("contact.new.title")
	}
	s.render(w, r, st.Status, "contact_form.html", data)
}

func (e ctEnv) months(sel string) []ctOption {
	out := []ctOption{{Value: "", Text: e.t("contact.month.none"), Selected: sel == "" || sel == "0"}}
	for m := 1; m <= 12; m++ {
		out = append(out, ctOption{Value: strconv.Itoa(m), Text: e.month(m), Selected: sel == strconv.Itoa(m)})
	}
	return out
}

func (e ctEnv) countryHint() string {
	code, _, _ := strings.Cut(e.p.household.Region, "-")
	if name := e.s.i18n.T(e.lang, "setup.country."+code); code != "" && name != "setup.country."+code {
		return e.t("contact.country.hint", "country", name)
	}
	return e.t("contact.country.hint.none")
}

// nameTaken answers whether a Hejmo name is in use, ignoring case.
func nameTakenIn(all []store.Contact, exceptID string) func(string) *store.NameTakenError {
	return func(name string) *store.NameTakenError {
		for _, c := range all {
			if c.ID != exceptID && strings.EqualFold(c.HejmoName, name) {
				return &store.NameTakenError{Existing: c.HejmoName, ExistingIsPerson: c.IsPerson()}
			}
		}
		return nil
	}
}

// groupTakenIn answers whether a group name is in use, among the own groups and the names of the five of Hejmo.
func (e ctEnv) groupTaken(name string) bool {
	for _, g := range e.groups {
		if strings.EqualFold(e.groupName(g), name) {
			return true
		}
		if g.ShippedKey != "" {
			for _, lang := range e.s.i18n.Languages() {
				if strings.EqualFold(e.s.i18n.T(lang, "contact.group."+g.ShippedKey), name) {
					return true
				}
			}
		}
	}
	return false
}

func (s *server) handleContactNew(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	s.renderContactForm(w, r, e, ctForm{Name: strings.TrimSpace(r.URL.Query().Get("name"))}, ctFormState{Status: http.StatusOK})
}

// handleContactCreate and handleContactSave answer the contact form. A button "Weitere Nummer" or "Entfernen"
// (the page without JavaScript) shows the form again with the line added or removed and nothing checked.
func (s *server) handleContactCreate(w http.ResponseWriter, r *http.Request) {
	s.saveContact(w, r, "")
}

func (s *server) handleContactSave(w http.ResponseWriter, r *http.Request) {
	s.saveContact(w, r, r.PathValue("id"))
}

func (s *server) saveContact(w http.ResponseWriter, r *http.Request, id string) {
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
	var cur *store.Contact
	if id != "" {
		i := slices.IndexFunc(all, func(c store.Contact) bool { return c.ID == id })
		if i < 0 {
			http.NotFound(w, r)
			return
		}
		cur = &all[i]
	}
	f := parseContactForm(r)
	if cur != nil && cur.IsPerson() {
		f.Name = cur.HejmoName
	}
	if more, remove := r.PostFormValue("more"), r.PostFormValue("remove"); more != "" || remove != "" {
		switch more {
		case "phone":
			if len(f.Phones) < contact.MaxLines {
				f.Phones = append(f.Phones, ctLineForm{})
			}
		case "email":
			if len(f.Emails) < contact.MaxLines {
				f.Emails = append(f.Emails, ctLineForm{})
			}
		}
		for _, base := range []struct {
			prefix string
			lines  *[]ctLineForm
		}{{"phone", &f.Phones}, {"email", &f.Emails}} {
			if n, err := strconv.Atoi(strings.TrimPrefix(remove, base.prefix)); err == nil && strings.HasPrefix(remove, base.prefix) && n >= 1 && n <= len(*base.lines) {
				*base.lines = slices.Delete(slices.Clone(*base.lines), n-1, n)
			}
		}
		s.renderContactForm(w, r, e, f, ctFormState{Contact: cur, Status: http.StatusOK})
		return
	}
	d := f.draft(cur != nil && cur.IsPerson())
	problems := checkContact(d, s.householdNow(p.household), nameTakenIn(all, id), e.groupTaken)
	if len(problems) > 0 {
		s.renderContactForm(w, r, e, f, ctFormState{Contact: cur, Problems: problems, Draft: d, Status: http.StatusUnprocessableEntity})
		return
	}
	var saved store.Contact
	if cur == nil {
		saved, err = s.db.CreateContact(p.household.ID, p.person.ID, d.input())
	} else {
		saved, err = s.db.UpdateContact(p.household.ID, id, p.person.ID, d.input())
	}
	var taken *store.NameTakenError
	var fe *store.FieldError
	switch {
	case errors.As(err, &taken): // made between the check and the save
		code := "taken"
		if taken.ExistingIsPerson {
			code = "taken_person"
		}
		s.renderContactForm(w, r, e, f, ctFormState{Contact: cur, Draft: d, Status: http.StatusUnprocessableEntity,
			Problems: []ctProblem{{Anchor: "name", Field: "name", Code: code, Arg: taken.Existing}}})
		return
	case errors.As(err, &fe):
		s.renderContactForm(w, r, e, f, ctFormState{Contact: cur, Draft: d, Status: http.StatusUnprocessableEntity,
			Problems: []ctProblem{{Anchor: "name", Field: fe.Field, Code: fe.Msg}}})
		return
	case err != nil:
		s.fail(w, "save contact", err)
		return
	}
	s.redirectSavedNamed(w, r, contactsPath+"/"+saved.ID, "flash.contact_saved", saved.HejmoName)
}

func (s *server) handleContactEdit(w http.ResponseWriter, r *http.Request) {
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
	s.renderContactForm(w, r, e, ctFormOf(c), ctFormState{Contact: &c, Status: http.StatusOK})
}
