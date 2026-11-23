package server

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"golang.org/x/text/collate"
	textlang "golang.org/x/text/language"

	"github.com/hejmo-home/hejmo/internal/store"
)

// The contact groups (ticket 296): the five of Hejmo and the household's own.

const contactGroupsPath = "/contacts/groups"

// membersOf lists the names of the contacts in a group, A to Z.
func (e ctEnv) membersOf(all []store.Contact, g store.ContactGroup) []store.Contact {
	var out []store.Contact
	for _, c := range all {
		if slices.Contains(c.GroupIDs, g.ID) {
			out = append(out, c)
		}
	}
	coll := collate.New(textlang.Make(e.lang))
	slices.SortStableFunc(out, func(a, b store.Contact) int { return coll.CompareString(a.HejmoName, b.HejmoName) })
	return out
}

func ctNames(list []store.Contact) []string {
	var out []string
	for _, c := range list {
		out = append(out, c.HejmoName)
	}
	return out
}

type ctGroupRow struct{ Href, Name, Meta string }

func (s *server) renderContactGroups(w http.ResponseWriter, r *http.Request, e ctEnv, status int, name, errText string) {
	all, err := s.db.Contacts(e.p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return
	}
	var rows []ctGroupRow
	var shipped []string
	for _, g := range e.groups {
		if g.ShippedKey != "" {
			shipped = append(shipped, e.groupName(g))
			continue
		}
		meta := e.t("contact.groups.empty")
		if m := e.membersOf(all, g); len(m) > 0 {
			meta = s.joinWords(e.lang, ctNames(m))
		}
		rows = append(rows, ctGroupRow{Href: contactGroupsPath + "/" + g.ID, Name: g.Name, Meta: meta})
	}
	data := map[string]any{
		"Person": e.p.person, "SyncStale": s.syncStale(e.p.household.ID), "Rows": rows,
		"ShippedText": e.t("contact.groups.shipped", "names", strings.Join(shipped, ", ")), "Name": name, "Err": errText,
	}
	s.render(w, r, status, "contact_groups.html", data)
}

func (s *server) handleContactGroups(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	s.renderContactGroups(w, r, e, http.StatusOK, "", "")
}

func (s *server) handleContactGroupCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if text := e.groupNameProblem(name, ""); text != "" {
		s.renderContactGroups(w, r, e, http.StatusUnprocessableEntity, name, text)
		return
	}
	g, err := s.db.CreateContactGroup(p.household.ID, p.person.ID, name)
	if err != nil {
		var fe *store.FieldError
		if errors.As(err, &fe) {
			s.renderContactGroups(w, r, e, http.StatusUnprocessableEntity, name, e.t("contact.error.group.taken", "name", name))
			return
		}
		s.fail(w, "create contact group", err)
		return
	}
	s.redirectSavedNamed(w, r, contactGroupsPath, "flash.contact_group_created", g.Name)
}

// groupNameProblem says what is wrong with a group name, or "". exceptID is the group being renamed.
func (e ctEnv) groupNameProblem(name, exceptID string) string {
	switch {
	case name == "":
		return e.t("contact.error.group.required")
	case len([]rune(name)) > 40:
		return e.t("contact.error.group.long")
	}
	e2 := e
	e2.groups = slices.DeleteFunc(slices.Clone(e.groups), func(g store.ContactGroup) bool { return g.ID == exceptID })
	if e2.groupTaken(name) {
		return e.t("contact.error.group.taken", "name", name)
	}
	return ""
}

// ownGroup loads the group of the path; the groups of Hejmo have no page of their own.
func (s *server) ownGroup(w http.ResponseWriter, r *http.Request, e ctEnv) (store.ContactGroup, bool) {
	g, ok := e.groupByID(r.PathValue("id"))
	if !ok || g.ShippedKey != "" {
		http.NotFound(w, r)
		return store.ContactGroup{}, false
	}
	return g, true
}

func (s *server) renderContactGroup(w http.ResponseWriter, r *http.Request, e ctEnv, g store.ContactGroup, status int, name, errText string) {
	all, err := s.db.Contacts(e.p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return
	}
	members := e.membersOf(all, g)
	type link struct{ Href, Name string }
	var links []link
	for _, c := range members {
		links = append(links, link{contactsPath + "/" + c.ID, c.HejmoName})
	}
	if name == "" {
		name = g.Name
	}
	created := e.p.day(g.CreatedAt)
	if g.CreatedByName != "" {
		created = e.t("contact.group.created", "name", g.CreatedByName, "date", created)
	} else {
		created = e.t("contact.group.created.anon", "date", created)
	}
	explain := e.t("contact.group.delete.explain", "name", g.Name)
	if len(members) == 0 {
		explain = e.t("contact.group.delete.explain.none", "name", g.Name)
	}
	data := map[string]any{
		"Person": e.p.person, "SyncStale": s.syncStale(e.p.household.ID), "ID": g.ID, "GroupName": g.Name, "Name": name, "Err": errText,
		"Members": links, "Created": created,
		"DeleteExplain": explain, "DeleteConfirm": e.t("contact.group.delete.confirm", "name", g.Name),
		"Stays": ctNames(members),
	}
	s.render(w, r, status, "contact_group.html", data)
}

func (s *server) handleContactGroup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	if g, ok := s.ownGroup(w, r, e); ok {
		s.renderContactGroup(w, r, e, g, http.StatusOK, "", "")
	}
}

func (s *server) handleContactGroupRename(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	g, ok := s.ownGroup(w, r, e)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if text := e.groupNameProblem(name, g.ID); text != "" {
		s.renderContactGroup(w, r, e, g, http.StatusUnprocessableEntity, name, text)
		return
	}
	if err := s.db.RenameContactGroup(p.household.ID, g.ID, name); err != nil {
		s.fail(w, "rename contact group", err)
		return
	}
	s.redirectSavedNamed(w, r, contactGroupsPath, "flash.contact_group_saved", name)
}

func (s *server) handleContactGroupDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	g, ok := s.ownGroup(w, r, e)
	if !ok {
		return
	}
	if err := s.db.DeleteContactGroup(p.household.ID, g.ID); err != nil {
		s.fail(w, "delete contact group", err)
		return
	}
	s.redirectSavedNamed(w, r, contactGroupsPath, "flash.contact_group_deleted", g.Name)
}
