package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/nameday"
	"github.com/hejmo-home/hejmo/internal/store"
)

// REST endpoints of the address book (ticket 296), for parents. MCP and CardDAV follow with #304 and #299.

type contactLineJSON struct {
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	Value string `json:"value"`
}

type contactJSON struct {
	ID       string            `json:"id"`
	PersonID *string           `json:"person_id"`
	Name     string            `json:"name"`
	FullName string            `json:"full_name"`
	Numbers  []contactLineJSON `json:"numbers"`
	Emails   []contactLineJSON `json:"emails"`
	Address  contactAddress    `json:"address"`
	Birthday *contactBirthday  `json:"birthday"`
	// ShowInBirthdays is whether the birthday is in the generated calendar Birthdays.
	ShowInBirthdays bool `json:"show_in_birthdays"`
	// ForChildren is whether the contact is on the children's page "Wichtige Nummern" (ticket 301).
	ForChildren bool                  `json:"for_children"`
	Note        string                `json:"note"`
	Groups      []string              `json:"groups"`
	Relations   []contactRelationJSON `json:"relations"`
	Photo       *contactPhotoJSON     `json:"photo"`
	CreatedBy   *string               `json:"created_by"`
	CreatedAt   string                `json:"created_at"`
	UpdatedBy   *string               `json:"updated_by"`
	UpdatedAt   string                `json:"updated_at"`
	// NameDay is the choice of the name day and what it makes of the contact while name days are on (ticket 318).
	NameDay contactNameDay `json:"name_day"`
}

// contactNameDay is what a contact says about its name day: the mode (auto, entry, name, none), the entry id or the
// other name that goes with it, and, while the household has name days on and the contact has one, the next day.
type contactNameDay struct {
	Mode     string               `json:"mode"`
	Entry    *string              `json:"entry"`
	Name     *string              `json:"name"`
	Resolved *contactNameDayFound `json:"resolved"`
}

type contactNameDayFound struct {
	Date  string `json:"date"`  // the next day it falls on, YYYY-MM-DD
	Name  string `json:"name"`  // the name of the name day, as the list writes it
	Saint string `json:"saint"` // the saint or feast
	Entry string `json:"entry"` // the id of the entry in the list
}

// contactNameDayIn is the name day of a contact written by the API; an omitted name_day keeps what the contact has.
type contactNameDayIn struct {
	Mode  string  `json:"mode"`
	Entry *string `json:"entry"`
	Name  *string `json:"name"`
}

type contactPhotoJSON struct {
	Origin string  `json:"origin"`
	By     *string `json:"by"`
	Device string  `json:"device"`
	At     string  `json:"at"`
}

// contactRelationJSON is a relation (ticket 298): a word plus the people it is for, or the whole family (no people).
type contactRelationJSON struct {
	Word        string   `json:"word"`
	WholeFamily bool     `json:"whole_family"`
	People      []string `json:"people"`
}

type contactAddress struct {
	Street   string `json:"street"`
	Postcode string `json:"postcode"`
	City     string `json:"city"`
	Country  string `json:"country"`
}

type contactBirthday struct {
	Day   int  `json:"day"`
	Month int  `json:"month"`
	Year  *int `json:"year"`
}

type contactGroupJSON struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Shipped  *string  `json:"shipped_key"`
	Contacts []string `json:"contacts"`
}

func linesJSON(in []contact.Line) []contactLineJSON {
	out := make([]contactLineJSON, 0, len(in))
	for _, l := range in {
		out = append(out, contactLineJSON{Kind: string(l.Kind), Label: l.Label, Value: l.Value})
	}
	return out
}

func relationsJSON(in []store.Relation) []contactRelationJSON {
	out := make([]contactRelationJSON, 0, len(in))
	for _, r := range in {
		out = append(out, contactRelationJSON{Word: r.Word, WholeFamily: r.WholeFamily, People: append([]string{}, r.PersonIDs...)})
	}
	return out
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *server) contactToJSON(p principal, c store.Contact) contactJSON {
	loc := p.location()
	out := contactJSON{
		ID: c.ID, PersonID: strPtr(c.PersonID), Name: c.HejmoName, FullName: c.FullName,
		Numbers: linesJSON(c.Numbers), Emails: linesJSON(c.Emails), Address: contactAddress{c.Street, c.Postcode, c.City, c.Country},
		ShowInBirthdays: !c.HideBirthday, ForChildren: c.ForChildren, Note: c.Note, Groups: append([]string{}, c.GroupIDs...), Relations: relationsJSON(c.Relations), CreatedBy: strPtr(c.CreatedBy), CreatedAt: rfc3339(c.CreatedAt, loc),
		UpdatedBy: strPtr(c.UpdatedBy), UpdatedAt: rfc3339(c.UpdatedAt, loc),
	}
	out.NameDay = contactNameDay{Mode: c.NameDayMode, Entry: strPtr(c.NameDayEntry), Name: strPtr(c.NameDayName)}
	if cal, ok := p.household.NameDayCalendar(); ok {
		if info := ndOf(c, cal, s.householdNow(p.household)); info.Res.Found {
			out.NameDay.Resolved = &contactNameDayFound{Date: info.Occ.Date.Format("2006-01-02"), Name: info.Res.Entry.Name, Saint: info.Res.Entry.Saint, Entry: info.Res.Entry.ID}
		}
	}
	if c.HasPhoto {
		out.Photo = &contactPhotoJSON{Origin: c.PhotoOrigin, By: strPtr(c.PhotoBy), Device: c.PhotoDevice, At: rfc3339(c.PhotoAt, loc)}
	}
	if c.BdayDay != 0 {
		b := &contactBirthday{Day: c.BdayDay, Month: c.BdayMonth}
		if c.BdayYear != 0 {
			y := c.BdayYear
			b.Year = &y
		}
		out.Birthday = b
	}
	return out
}

func (s *server) apiListContacts(w http.ResponseWriter, r *http.Request, p principal) {
	all, err := s.db.Contacts(p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return
	}
	q, g := strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("group")
	out := []contactJSON{}
	for _, c := range all {
		if (g != "" && !slices.Contains(c.GroupIDs, g)) || !ctMatches(c, q) {
			continue
		}
		out = append(out, s.contactToJSON(p, c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiGetContact(w http.ResponseWriter, r *http.Request, p principal) {
	c, err := s.db.Contact(p.household.ID, r.PathValue("id"))
	if s.storeError(w, "contact", err) {
		return
	}
	writeJSON(w, http.StatusOK, s.contactToJSON(p, c))
}

// contactIn is what the API takes: everything a parent can set. An omitted part is empty.
type contactIn struct {
	Name     string            `json:"name"`
	FullName string            `json:"full_name"`
	Numbers  []contactLineJSON `json:"numbers"`
	Emails   []contactLineJSON `json:"emails"`
	Address  contactAddress    `json:"address"`
	Birthday *contactBirthday  `json:"birthday"`
	// ShowInBirthdays defaults to true when omitted.
	ShowInBirthdays *bool `json:"show_in_birthdays"`
	// ForChildren shows the contact on the children's page; omitted means false, so a replace switches it off.
	ForChildren bool     `json:"for_children"`
	Note        string   `json:"note"`
	Groups      []string `json:"groups"`
	// Relations replace the contact's relations; a person's contact has none (422).
	Relations []contactRelationJSON `json:"relations"`
	// NameDay chooses the name day; omitted it stays as it is (ticket 318).
	NameDay *contactNameDayIn `json:"name_day"`
}

func jsonLines(in []contactLineJSON, number bool) ([]contact.Line, string) {
	var out []contact.Line
	for _, l := range in {
		k := contact.Kind(l.Kind)
		if !contact.KindValid(k) || (!number && k == contact.Cell) {
			return nil, "kind: unknown kind " + l.Kind
		}
		out = append(out, contact.Line{Kind: k, Label: l.Label, Value: l.Value})
	}
	return out, ""
}

func (in contactIn) draft(isPerson bool) (ctDraft, string) {
	d := ctDraft{Name: in.Name, Full: in.FullName, Street: in.Address.Street, Postcode: in.Address.Postcode, City: in.Address.City, Country: in.Address.Country,
		Note: in.Note, GroupIDs: in.Groups, IsPerson: isPerson, HideBirthday: in.ShowInBirthdays != nil && !*in.ShowInBirthdays, ForChildren: in.ForChildren}
	for _, r := range in.Relations {
		d.Rels = append(d.Rels, ctRelDraft{Word: r.Word, Whole: r.WholeFamily, PersonIDs: r.People})
	}
	var bad string
	if d.Numbers, bad = jsonLines(in.Numbers, true); bad != "" {
		return d, "numbers." + bad
	}
	if d.Emails, bad = jsonLines(in.Emails, false); bad != "" {
		return d, "emails." + bad
	}
	if n := in.NameDay; n != nil {
		d.NameDayMode = n.Mode
		if n.Entry != nil {
			d.NameDayEntry = *n.Entry
		}
		if n.Name != nil {
			d.NameDayName = *n.Name
		}
	}
	if b := in.Birthday; b != nil {
		d.Day, d.Month = b.Day, b.Month
		if b.Year != nil {
			d.Year = *b.Year
		}
	}
	return d, ""
}

// saveContactAPI checks and stores a contact of the API; id is "" for a new one.
func (s *server) saveContactAPI(w http.ResponseWriter, r *http.Request, p principal, id string) {
	var in contactIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	all, err := s.db.Contacts(p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return
	}
	isPerson := false
	if id != "" {
		i := slices.IndexFunc(all, func(c store.Contact) bool { return c.ID == id })
		if i < 0 {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		isPerson = all[i].IsPerson()
		if isPerson {
			in.Name = all[i].HejmoName
		}
	}
	if isPerson && len(in.Relations) > 0 {
		writeFieldError(w, http.StatusUnprocessableEntity, "relations", "relations: the contact of a person has none")
		return
	}
	d, bad := in.draft(isPerson)
	if bad != "" {
		writeError(w, http.StatusUnprocessableEntity, bad)
		return
	}
	problems := checkContact(d, s.householdNow(p.household), nameTakenIn(all, id), func(string) bool { return false })
	if len(problems) == 0 {
		switch d.NameDayMode {
		case "", nameday.ModeAuto, nameday.ModeNone, nameday.ModeEntry, nameday.ModeName:
			problems = ctEnv{s: s, lang: s.langOf(r), p: p}.nameDayProblems(d)
		default:
			writeFieldError(w, http.StatusUnprocessableEntity, "name_day", "name_day: unknown mode "+d.NameDayMode)
			return
		}
	}
	if len(problems) > 0 {
		writeFieldError(w, http.StatusUnprocessableEntity, problems[0].Field, problems[0].String())
		return
	}
	var saved store.Contact
	if id == "" {
		saved, err = s.db.CreateContact(p.household.ID, p.person.ID, d.input(s.householdNow(p.household), s.i18n.T(s.langOf(r), "birthdays.calendar_name")))
	} else {
		saved, err = s.db.UpdateContact(p.household.ID, id, p.person.ID, d.input(s.householdNow(p.household), s.i18n.T(s.langOf(r), "birthdays.calendar_name")))
	}
	var taken *store.NameTakenError
	if errors.As(err, &taken) {
		writeFieldError(w, http.StatusUnprocessableEntity, "name", "name: taken by "+taken.Existing)
		return
	}
	if s.storeError(w, "save contact", err) {
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	writeJSON(w, status, s.contactToJSON(p, saved))
}

func (s *server) apiCreateContact(w http.ResponseWriter, r *http.Request, p principal) {
	s.saveContactAPI(w, r, p, "")
}

func (s *server) apiUpdateContact(w http.ResponseWriter, r *http.Request, p principal) {
	s.saveContactAPI(w, r, p, r.PathValue("id"))
}

func (s *server) apiDeleteContact(w http.ResponseWriter, r *http.Request, p principal) {
	if s.storeError(w, "delete contact", s.db.DeleteContact(p.household.ID, r.PathValue("id"))) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) groupToJSON(g store.ContactGroup, all []store.Contact) contactGroupJSON {
	out := contactGroupJSON{ID: g.ID, Name: g.Name, Shipped: strPtr(g.ShippedKey), Contacts: []string{}}
	for _, c := range all {
		if slices.Contains(c.GroupIDs, g.ID) {
			out.Contacts = append(out.Contacts, c.ID)
		}
	}
	return out
}

func (s *server) apiListContactGroups(w http.ResponseWriter, r *http.Request, p principal) {
	groups, err := s.db.ContactGroups(p.household.ID)
	if err != nil {
		s.fail(w, "contact groups", err)
		return
	}
	all, err := s.db.Contacts(p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return
	}
	out := []contactGroupJSON{}
	for _, g := range groups {
		out = append(out, s.groupToJSON(g, all))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiCreateContactGroup(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if s.apiGroupNameTaken(w, p, in.Name, "") {
		return
	}
	g, err := s.db.CreateContactGroup(p.household.ID, p.person.ID, in.Name)
	if s.storeError(w, "create contact group", err) {
		return
	}
	writeJSON(w, http.StatusCreated, s.groupToJSON(g, nil))
}

// apiGroupNameTaken answers 422 when the name clashes with one of the groups of Hejmo (in either language) or an own one.
func (s *server) apiGroupNameTaken(w http.ResponseWriter, p principal, name, exceptID string) bool {
	groups, err := s.db.ContactGroups(p.household.ID)
	if err != nil {
		s.fail(w, "contact groups", err)
		return true
	}
	e := ctEnv{s: s, lang: "en", p: p, groups: groups}
	if text := e.groupNameProblem(strings.TrimSpace(name), exceptID); text != "" {
		writeFieldError(w, http.StatusUnprocessableEntity, "name", "name: not a free group name of 1 to 40 characters")
		return true
	}
	return false
}

func (s *server) apiRenameContactGroup(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	id := r.PathValue("id")
	if _, err := s.db.ContactGroup(p.household.ID, id); s.storeError(w, "contact group", err) {
		return
	}
	if s.apiGroupNameTaken(w, p, in.Name, id) {
		return
	}
	if s.storeError(w, "rename contact group", s.db.RenameContactGroup(p.household.ID, id, in.Name)) {
		return
	}
	g, err := s.db.ContactGroup(p.household.ID, id)
	if s.storeError(w, "contact group", err) {
		return
	}
	all, err := s.db.Contacts(p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return
	}
	writeJSON(w, http.StatusOK, s.groupToJSON(g, all))
}

func (s *server) apiDeleteContactGroup(w http.ResponseWriter, r *http.Request, p principal) {
	if s.storeError(w, "delete contact group", s.db.DeleteContactGroup(p.household.ID, r.PathValue("id"))) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
