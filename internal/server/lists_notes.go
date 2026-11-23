package server

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Notes on list entries (ticket 167, Q42): a note is written in Hejmo's editor
// or in Apple Reminders, shown small under the entry and kept when the entry
// is ticked off.

// cleanNote is a note as it is stored: trimmed, with line breaks as \n.
func cleanNote(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
}

// noteOutcome is the line after saving a note alone.
func noteOutcome(l list.List, e list.Entry, note string) outcome {
	action := "saved"
	if note == "" {
		action = "removed"
	}
	return outcome{Kind: "note", Action: action, EntryID: e.ID, Text: e.Text, Shared: l.Shared}
}

// saveNote stores the note an editor posted after its quantity was changed.
// into is the entry that holds the quantity now. When the quantity did not
// change, the line says the note was saved; it returns whether the note
// changed.
func (s *server) saveNote(p principal, l list.List, entryID, note string, merged bool) (list.Entry, bool, error) {
	es, err := s.db.Entries(p.household.ID, l.ID)
	if err != nil {
		return list.Entry{}, false, err
	}
	at := slices.IndexFunc(es, func(e list.Entry) bool { return e.ID == entryID })
	if at < 0 {
		return list.Entry{}, false, store.ErrNotFound
	}
	e := es[at]
	if (merged && note == "") || e.Note == note {
		return e, false, nil // a merged entry brought its note along
	}
	if err := s.db.SetEntryNote(p.household.ID, l.ID, entryID, note); err != nil {
		return e, false, err
	}
	e.Note = note
	return e, true, nil
}

// noteEditor is the editor of a list without quantities: the note alone.
func (s *server) noteEditor(lang, base string, e list.Entry, hide bool, post *editPost, errKey string) *editorView {
	note, errText := e.Note, ""
	if post != nil && post.EntryID == e.ID {
		note = post.Note
		if errKey != "" {
			errText = s.i18n.T(lang, errKey)
		}
	}
	ed := &editorView{Anchor: e.ID, Action: base + "/entries/" + e.ID + "/note", Hide: hide, NoteOnly: true, Cancel: base + "#e-" + e.ID, Note: newNoteField(e.ID, note, errText)}
	if hide {
		ed.Cancel = base + "?ticked=hide#e-" + e.ID
	}
	return ed
}

// handleEntryNote saves the note of an entry on a list without quantities.
func (s *server) handleEntryNote(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	l, ok := s.listPage(w, r, p)
	if !ok {
		return
	}
	id, note := r.PathValue("entry"), cleanNote(r.PostFormValue("note"))
	if err := list.ValidateNote(note); err != nil {
		post := &editPost{EntryID: id, Note: note, NoteSet: true}
		s.renderList(w, r, p, http.StatusUnprocessableEntity, l, false, map[string]any{"Text": "", "EditPost": post}, "lists.note.error", "edit-note")
		return
	}
	es, at, ok := s.entryAt(w, r, p, l.ID, id)
	if !ok {
		return
	}
	if es[at].Done {
		s.renderList(w, r, p, http.StatusConflict, l, false, map[string]any{"Text": ""}, "lq.err.gone", "")
		return
	}
	switch err := s.db.SetEntryNote(p.household.ID, l.ID, id, note); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "set note", err)
	default:
		http.Redirect(w, r, s.resultURL(r, l.ID, noteOutcome(l, es[at], note)), http.StatusSeeOther)
	}
}
