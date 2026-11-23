package server_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestParentsManagePauseDaysThroughTheAPI(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	status, p, _ := tb.parent.call(http.MethodPost, "/tasks/pauses", map[string]any{"from": "2026-11-25", "to": "2026-11-26", "person_ids": []string{tb.miaID}})
	if status != http.StatusCreated || p["from"] != "2026-11-25" || p["whole_family"] != false || len(p["person_ids"].([]any)) != 1 {
		t.Fatalf("create = %d %v", status, p)
	}
	id := p["id"].(string)
	status, whole, _ := tb.parent.call(http.MethodPost, "/tasks/pauses", map[string]any{"from": "2026-12-19", "to": "2027-01-03"})
	if status != http.StatusCreated || whole["whole_family"] != true || len(whole["person_ids"].([]any)) != 0 {
		t.Fatalf("whole family = %d %v", status, whole)
	}
	status, _, list := tb.child.call(http.MethodGet, "/tasks/pauses", nil)
	if status != http.StatusOK || len(list) != 2 || list[0].(map[string]any)["id"] != id {
		t.Fatalf("list = %d %v", status, list)
	}
	status, got, _ := tb.parent.call(http.MethodPatch, "/tasks/pauses/"+id, map[string]any{"to": "2026-11-28"})
	if status != http.StatusOK || got["to"] != "2026-11-28" || got["from"] != "2026-11-25" || len(got["person_ids"].([]any)) != 1 {
		t.Fatalf("patch = %d %v", status, got)
	}
	status, got, _ = tb.parent.call(http.MethodPatch, "/tasks/pauses/"+id, map[string]any{"person_ids": []string{}})
	if status != http.StatusOK || got["whole_family"] != true {
		t.Fatalf("to the whole family = %d %v", status, got)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/tasks/pauses/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/tasks/pauses/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("second delete = %d", status)
	}
	if status, _, _ := tb.parent.call(http.MethodPatch, "/tasks/pauses/"+id, map[string]any{"to": "2026-11-28"}); status != http.StatusNotFound {
		t.Fatalf("patch of a removed pause = %d", status)
	}
}

func TestPauseInputIsValidatedAtTheFieldAndOverlapsMerge(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	for name, c := range map[string]struct {
		body  map[string]any
		field string
	}{
		"order":          {map[string]any{"from": "2026-11-10", "to": "2026-11-09"}, "to"},
		"long":           {map[string]any{"from": "2026-01-01", "to": "2027-01-03"}, "to"},
		"unknown person": {map[string]any{"from": "2026-11-10", "to": "2026-11-10", "person_ids": []string{"00000000-0000-4000-8000-000000000000"}}, "people"},
	} {
		status, body, _ := tb.parent.call(http.MethodPost, "/tasks/pauses", c.body)
		if status != http.StatusUnprocessableEntity || body["field"] != c.field {
			t.Errorf("%s = %d %v", name, status, body)
		}
	}
	// Past days are fine.
	if status, _, _ := tb.parent.call(http.MethodPost, "/tasks/pauses", map[string]any{"from": "2020-01-01", "to": "2020-01-02"}); status != http.StatusCreated {
		t.Errorf("past = %d", status)
	}
	_, a, _ := tb.parent.call(http.MethodPost, "/tasks/pauses", map[string]any{"from": "2026-11-10", "to": "2026-11-12", "person_ids": []string{tb.miaID}})
	_, b, _ := tb.parent.call(http.MethodPost, "/tasks/pauses", map[string]any{"from": "2026-11-12", "to": "2026-11-14", "person_ids": []string{tb.miaID}})
	if b["id"] != a["id"] || b["from"] != "2026-11-10" || b["to"] != "2026-11-14" {
		t.Errorf("merged = %v (first %v)", b, a)
	}
}

func TestChildrenReadButNeverChangePauseDays(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	_, p, _ := tb.parent.call(http.MethodPost, "/tasks/pauses", map[string]any{"from": "2026-11-25", "to": "2026-11-26"})
	id := p["id"].(string)
	if status, _, _ := tb.child.call(http.MethodPost, "/tasks/pauses", map[string]any{"from": "2026-11-25", "to": "2026-11-26"}); status != http.StatusForbidden {
		t.Errorf("child create = %d", status)
	}
	if status, _, _ := tb.child.call(http.MethodPatch, "/tasks/pauses/"+id, map[string]any{"to": "2026-11-27"}); status != http.StatusForbidden {
		t.Errorf("child patch = %d", status)
	}
	if status, _, _ := tb.child.call(http.MethodDelete, "/tasks/pauses/"+id, nil); status != http.StatusForbidden {
		t.Errorf("child delete = %d", status)
	}
}

func TestThePersonCarriesTheStreaksSwitchInTheAPI(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	_, mia, _ := tb.parent.call(http.MethodGet, "/people/"+tb.miaID, nil)
	_, mama, _ := tb.parent.call(http.MethodGet, "/people/"+tb.mamaID, nil)
	if mia["streaks_shown"] != true || mama["streaks_shown"] != false {
		t.Fatalf("defaults by role: Mia %v, Mama %v", mia["streaks_shown"], mama["streaks_shown"])
	}
	status, got, _ := tb.parent.call(http.MethodPatch, "/people/"+tb.miaID, map[string]any{"streaks_shown": false})
	if status != http.StatusOK || got["streaks_shown"] != false || got["name"] != "Mia" {
		t.Fatalf("patch = %d %v", status, got)
	}
	_, again, _ := tb.parent.call(http.MethodGet, "/people/"+tb.miaID, nil)
	if again["streaks_shown"] != false {
		t.Errorf("not stored: %v", again)
	}
}

func TestMCPPauseToolsDoWhatTheRESTFunctionsDo(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	c := tb.parent
	c.switchMCP(t, true)
	s := c.mcpSession(t, c.newMCPToken(t, "Claude", allAreas))
	out, failed := callTool(s, "create_pause", map[string]any{"from": "2026-11-25", "to": "2026-11-26", "person_ids": []string{tb.miaID}})
	if failed || !strings.Contains(out, "2026-11-25") {
		t.Fatalf("create_pause = %v %s", failed, out)
	}
	_, _, list := c.call(http.MethodGet, "/tasks/pauses", nil)
	if len(list) != 1 {
		t.Fatalf("REST sees %v", list)
	}
	id := list[0].(map[string]any)["id"].(string)
	if out, failed := callTool(s, "list_pauses", nil); failed || !strings.Contains(out, id) {
		t.Errorf("list_pauses = %v %s", failed, out)
	}
	if out, failed := callTool(s, "update_pause", map[string]any{"id": id, "to": "2026-11-30"}); failed || !strings.Contains(out, "2026-11-30") {
		t.Errorf("update_pause = %v %s", failed, out)
	}
	if out, failed := callTool(s, "create_pause", map[string]any{"from": "2026-11-26", "to": "2026-11-25"}); !failed {
		t.Errorf("an invalid pause was accepted: %s", out)
	}
	if out, failed := callTool(s, "delete_pause", map[string]any{"id": id}); failed {
		t.Errorf("delete_pause: %s", out)
	}
	if _, _, list := c.call(http.MethodGet, "/tasks/pauses", nil); len(list) != 0 {
		t.Errorf("left: %v", list)
	}
}
