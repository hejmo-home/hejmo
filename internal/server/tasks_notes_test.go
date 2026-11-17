package server_test

import (
	"net/http"
	"testing"
)

// ADR 0018: a task has an optional free-text field "notes", set when creating
// a task and changed from CalDAV clients.
func TestTaskNotesAreCreatedAndListedThroughTheAPI(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	status, k, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "Pack swim bag", "repeat": "once", "date": "2026-11-20", "notes": "Towel and goggles"})
	if status != http.StatusCreated || k["notes"] != "Towel and goggles" {
		t.Fatalf("create = %d %v", status, k)
	}
	if got := taskState(t, tb.parent, k["id"].(string), "")["notes"]; got != "Towel and goggles" {
		t.Fatalf("listed notes = %v", got)
	}
	plain := tb.newDaily(t, tb.miaID)
	if got := taskState(t, tb.parent, plain, "")["notes"]; got != "" {
		t.Fatalf("notes of a task without = %v, want empty", got)
	}
}
