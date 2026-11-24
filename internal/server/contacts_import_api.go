package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/hejmo-home/hejmo/internal/contactimport"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/vcard"
)

// The REST side of the contact import (ticket 300, ADR 0015): upload a file, read what it brings, confirm with
// the choices, or cancel. The same rules as the web interface; the file waits an hour for the parent who uploaded it.

type importCardJSON struct {
	Card      int               `json:"card"`
	Name      string            `json:"name"`
	FullName  string            `json:"full_name"`
	Numbers   []contactLineJSON `json:"numbers"`
	Emails    []contactLineJSON `json:"emails"`
	Photo     bool              `json:"photo"`
	PhotoLink bool              `json:"photo_link"`
}

type importReasonJSON struct {
	Kind      string `json:"kind"`
	Value     string `json:"value"`
	FileValue string `json:"file_value"`
}

type importDupJSON struct {
	Key         string             `json:"key"`
	InFile      bool               `json:"in_file"`
	ContactID   *string            `json:"contact_id"`
	Cards       []importCardJSON   `json:"cards"`
	Reasons     []importReasonJSON `json:"reasons"`
	Preselect   *string            `json:"preselect"`
	Choices     []string           `json:"choices"`
	NewName     string             `json:"new_name"`
	PhotoChoice bool               `json:"photo_choice"`
	Adds        importAddsJSON     `json:"adds"`
}

type importAddsJSON struct {
	Numbers   []contactLineJSON `json:"numbers"`
	Emails    []contactLineJSON `json:"emails"`
	FullName  string            `json:"full_name"`
	Address   bool              `json:"address"`
	Birthday  bool              `json:"birthday"`
	BirthYear bool              `json:"birth_year"`
	Note      bool              `json:"note"`
	Photo     bool              `json:"photo"`
}

type importNewJSON struct {
	Key        string         `json:"key"`
	Name       string         `json:"name"`
	NameChange *string        `json:"name_change"`
	Card       importCardJSON `json:"card"`
}

type importSameJSON struct {
	ContactID string `json:"contact_id"`
	Name      string `json:"name"`
}

type importUnreadJSON struct {
	Card   int    `json:"card"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

type contactImportJSON struct {
	ID         string             `json:"id"`
	FileName   string             `json:"file_name"`
	UploadedBy string             `json:"uploaded_by"`
	UploadedAt string             `json:"uploaded_at"`
	ExpiresAt  string             `json:"expires_at"`
	Cards      int                `json:"cards"`
	Duplicates []importDupJSON    `json:"duplicates"`
	New        []importNewJSON    `json:"new"`
	Same       []importSameJSON   `json:"same"`
	Unreadable []importUnreadJSON `json:"unreadable"`
}

func cardJSON(c vcard.Card) importCardJSON {
	return importCardJSON{Card: c.Number, Name: c.Name(), FullName: c.Full, Numbers: linesJSON(c.Numbers), Emails: linesJSON(c.Emails), Photo: len(c.Photo) > 0, PhotoLink: c.PhotoLink && len(c.Photo) == 0}
}

func (s *server) importToJSON(p principal, st importState) contactImportJSON {
	loc := p.location()
	out := contactImportJSON{ID: st.imp.ID, FileName: st.imp.FileName, UploadedBy: st.imp.UploadedBy, UploadedAt: rfc3339(st.imp.UploadedAt, loc),
		ExpiresAt: rfc3339(st.imp.UploadedAt.Add(importTTL), loc), Cards: st.plan.Cards,
		Duplicates: []importDupJSON{}, New: []importNewJSON{}, Same: []importSameJSON{}, Unreadable: []importUnreadJSON{}}
	for _, d := range st.plan.Dups {
		j := importDupJSON{Key: d.Key, InFile: d.InFile, Preselect: strPtr(d.Preselect), Choices: d.Choices, NewName: d.NewName, PhotoChoice: d.Merge.PhotoChoice,
			Cards: []importCardJSON{}, Reasons: []importReasonJSON{},
			Adds: importAddsJSON{Numbers: linesJSON(d.Merge.AddNumbers), Emails: linesJSON(d.Merge.AddEmails), FullName: d.Merge.AddFull, Address: d.Merge.AddAddress,
				Birthday: d.Merge.AddBirthday, BirthYear: d.Merge.AddYear, Note: d.Merge.AddNote, Photo: d.Merge.AddPhoto}}
		if d.Existing != nil {
			j.ContactID = strPtr(d.Existing.ID)
		}
		for _, c := range d.Cards {
			j.Cards = append(j.Cards, cardJSON(c))
		}
		for _, m := range d.Matches {
			j.Reasons = append(j.Reasons, importReasonJSON{Kind: m.Kind, Value: m.Hejmo.Value, FileValue: m.File})
		}
		out.Duplicates = append(out.Duplicates, j)
	}
	for _, n := range st.plan.News {
		j := importNewJSON{Key: n.Key, Name: n.Name, Card: cardJSON(n.Card)}
		if n.Note != nil {
			j.NameChange = strPtr(n.Note.Kind)
		}
		out.New = append(out.New, j)
	}
	for _, x := range st.plan.Same {
		out.Same = append(out.Same, importSameJSON{ContactID: x.ID, Name: x.Name})
	}
	for _, u := range st.plan.Unread {
		out.Unreadable = append(out.Unreadable, importUnreadJSON{Card: u.Card.Number, Line: u.Card.Line, Reason: string(u.Problem)})
	}
	return out
}

func (s *server) apiCreateContactImport(w http.ResponseWriter, r *http.Request, p principal) {
	s.importSweep()
	name, data, f := s.readUpload(w, r, "en")
	if f != nil {
		writeFieldError(w, f.status, "file", f.text)
		return
	}
	_, photos, f := s.checkUpload(r.Context(), "en", name, data, anyReadable)
	if f != nil {
		writeFieldError(w, f.status, "file", f.text)
		return
	}
	imp, err := s.db.AddContactImport(p.household.ID, p.person.ID, name, data, photos, s.now())
	if s.storeError(w, "add contact import", err) {
		return
	}
	st, err := s.readImport(p, imp)
	if err != nil {
		s.fail(w, "read contact import", err)
		return
	}
	writeJSON(w, http.StatusCreated, s.importToJSON(p, st))
}

// apiImport finds the uploaded file of the caller; it writes the answer when there is none.
func (s *server) apiImport(w http.ResponseWriter, r *http.Request, p principal) (importState, bool) {
	st, found, expired, err := s.loadImport(p, r.PathValue("id"))
	switch {
	case err != nil:
		s.fail(w, "contact import", err)
	case !found:
		writeError(w, http.StatusNotFound, "not found")
	case expired:
		writeError(w, http.StatusGone, "the file waited longer than an hour and was deleted")
	default:
		return st, true
	}
	return importState{}, false
}

func (s *server) apiGetContactImport(w http.ResponseWriter, r *http.Request, p principal) {
	if st, ok := s.apiImport(w, r, p); ok {
		writeJSON(w, http.StatusOK, s.importToJSON(p, st))
	}
}

func (s *server) apiDeleteContactImport(w http.ResponseWriter, r *http.Request, p principal) {
	if _, ok := s.apiImport(w, r, p); !ok {
		return
	}
	if err := s.db.DeleteContactImport(p.household.ID, r.PathValue("id")); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "delete contact import", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type contactImportConfirmIn struct {
	// Duplicates maps the key of a possible duplicate to merge, new or skip; where it has no entry the
	// preselection counts, and a duplicate without one is refused.
	Duplicates map[string]string `json:"duplicates"`
	// Photos maps the key of a duplicate whose both sides have a photo to keep (the default) or file.
	Photos map[string]string `json:"photos"`
	// Skip lists the keys of new contacts that are not taken over.
	Skip []string `json:"skip"`
	// Groups are the ids of the groups the new contacts come into.
	Groups []string `json:"groups"`
}

type contactImportResultJSON struct {
	FileName  string        `json:"file_name"`
	Created   []contactJSON `json:"created"`
	Completed []contactJSON `json:"completed"`
}

func (s *server) apiConfirmContactImport(w http.ResponseWriter, r *http.Request, p principal) {
	var in contactImportConfirmIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	st, ok := s.apiImport(w, r, p)
	if !ok {
		return
	}
	dec := contactimport.Decisions{Dup: in.Duplicates, Photo: in.Photos, Skip: map[string]bool{}, Groups: in.Groups}
	for _, k := range in.Skip {
		dec.Skip[k] = true
	}
	for key := range in.Duplicates {
		if !slices.ContainsFunc(st.plan.Dups, func(d contactimport.Dup) bool { return d.Key == key }) {
			writeFieldError(w, http.StatusUnprocessableEntity, "duplicates", "duplicates: no possible duplicate "+key)
			return
		}
	}
	if missing := st.plan.Missing(dec); len(missing) > 0 {
		writeFieldError(w, http.StatusUnprocessableEntity, "duplicates", "duplicates: choose merge, new or skip for "+strings.Join(missing, ", "))
		return
	}
	for _, g := range in.Groups {
		if _, err := s.db.ContactGroup(p.household.ID, g); err != nil {
			writeFieldError(w, http.StatusUnprocessableEntity, "groups", "groups: unknown group "+g)
			return
		}
	}
	res, ids, err := s.applyImport(p, st, dec, s.i18n.T(s.langOf(r), "birthdays.calendar_name"))
	var taken *store.NameTakenError
	if errors.As(err, &taken) {
		writeFieldError(w, http.StatusConflict, "name", "name: taken by "+taken.Existing+" while the file waited; read the file again")
		return
	}
	if s.storeError(w, "apply contact import", err) {
		return
	}
	out := contactImportResultJSON{FileName: st.imp.FileName, Created: []contactJSON{}, Completed: []contactJSON{}}
	for i, e := range res.Entries {
		c, err := s.db.Contact(p.household.ID, ids[i])
		if err != nil {
			s.fail(w, "contact", err)
			return
		}
		if e.Completed {
			out.Completed = append(out.Completed, s.contactToJSON(p, c))
		} else {
			out.Created = append(out.Created, s.contactToJSON(p, c))
		}
	}
	writeJSON(w, http.StatusOK, out)
}
