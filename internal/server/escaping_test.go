package server_test

import (
	"net/url"
	"strings"
	"testing"
)

// Threat model T7: hostile text in a title must reach the page escaped, never as markup.
const hostileTitle = `<script>alert(1)</script>`

func requireEscaped(t *testing.T, where, page string) {
	t.Helper()
	if strings.Contains(page, "<script>alert(1)") {
		t.Errorf("%s: hostile title rendered as markup", where)
	}
	if !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("%s: escaped hostile title not found", where)
	}
}

func TestHostileEventTitleIsEscapedOnTheFamilyViewAndDisplayPage(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "screen")
	f.event(t, hostileTitle, f.at(14, 15, 0), f.at(14, 16, 0), false, "Mia")
	requireEscaped(t, "GET /display", f.view(t, "en", f.at(14, 13, 0)))
	r := f.display.get("/display/view", "en")
	if r.status != 200 {
		t.Fatalf("GET /display/view = %d", r.status)
	}
	requireEscaped(t, "GET /display/view", r.body)
}

func TestHostileTaskTitleIsEscapedOnTheTasksPage(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	if r := b.post("/tasks", url.Values{"person": {tb.miaID}, "title": {hostileTitle}, "repeat": {"daily"}}); r.status != 303 {
		t.Fatalf("create = %d\n%s", r.status, r.body)
	}
	requireEscaped(t, "GET /tasks", b.get("/tasks").body)
}
