package server_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/googleauth"
)

// googlePages returns the pages of the Google connection (ticket 18) in the
// states a crawl does not reach, for the rendered layout check: long names of
// accounts, calendars and the app, eight people, the failures, the fallback.
// The box is put back as it was.
func googlePages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	g := newGoogleFake(t, f.box)
	parent := siteOf("app", f.box, f.client)
	var pages []layoutPage
	const account = "konstantin-alexander.mueller-luedenscheidt@gmail.com"
	grab := func(name, method, path string, form url.Values, want int) resp {
		t.Helper()
		out := map[string]string{}
		var last resp
		for _, lang := range []string{"de", "en"} {
			last = f.do(method, path, form, map[string]string{"Accept-Language": lang})
			if last.status != want {
				t.Fatalf("%s %s %s = %d, want %d\n%s", name, method, path, last.status, want, last.body)
			}
			out[lang] = last.body
		}
		pages = append(pages, layoutPage{site: parent, path: "/google-layout/" + name, html: out})
		return last
	}
	// Routes are matched by path: give the pages the path of the route they show.
	route := func(path string) {
		pages[len(pages)-1].path = path + "?state=" + pages[len(pages)-1].path[len("/google-layout/"):]
	}

	grab("guide", http.MethodGet, "/connections/new?service=google", nil, http.StatusOK)
	route("/connections/new")
	grab("guide-error", http.MethodPost, "/connections/google/app", url.Values{"action": {"save"}, "client_id": {"318046275519-k7q2m9xlb4tn0c8e1rvd6hf3ys5pjw2a-und-noch-viel-mehr-text-ohne-leerzeichen"}, "client_secret": {gSecret}}, http.StatusUnprocessableEntity)
	route("/connections/new")
	if r := f.post("/connections/google/app", url.Values{"action": {"save"}, "client_id": {gClientID}, "client_secret": {gSecret}}); r.status != http.StatusSeeOther {
		t.Fatalf("register = %d\n%s", r.status, r.body)
	}
	grab("signin", http.MethodGet, "/connections/google/signin", nil, http.StatusOK)
	route("/connections/google/signin")
	grab("signin-fallback", http.MethodGet, "/connections/google/signin?way=fallback", nil, http.StatusOK)
	route("/connections/google/signin")
	grab("paste-error", http.MethodPost, "/connections/google/paste", url.Values{"address": {"https://accounts.google.com/signin/oauth/consent?authuser=0&part=AJi8hAPrQ-eine-sehr-lange-adresse-ohne-leerzeichen-die-nicht-abgeschnitten-werden-darf"}}, http.StatusUnprocessableEntity)
	route("/connections/google/paste")
	grab("callback-unknown", http.MethodGet, "/connections/google/callback?state=hj1.aHR0cHM6Ly94.not-made-here&code=x", nil, http.StatusBadRequest)
	route("/connections/google/callback")

	// A sign-in with a long account name and calendars with long names.
	g.set(func(g *googleFake) {
		g.account = account
		g.custom = []map[string]any{
			{"id": account, "summary": account, "accessRole": "owner", "primary": true},
			{"id": "oma.hildegard@gmail.com", "summary": "Termine von Oma Hildegard aus der Seniorenresidenz am Starnberger See", "accessRole": "reader"},
			{"id": "addressbook#contacts@group.v.calendar.google.com", "summary": "Geburtstage", "accessRole": "reader"},
			{"id": "fam123@group.calendar.google.com", "summary": "Familie Maier-Schmidt – Termine, Geburtstage, Urlaube und Schulveranstaltungen", "accessRole": "writer"},
		}
	})
	start := f.do(http.MethodPost, "/connections/google/start", url.Values{}, nil)
	state, _ := g.hand(start.location, "layout-code")
	cb := f.do(http.MethodGet, googleCallback(state, "layout-code"), nil, nil)
	if cb.status != http.StatusSeeOther {
		t.Fatalf("callback = %d %q\n%s", cb.status, cb.location, cb.body)
	}
	id := cb.location[len("/connections/"):]
	grab("choose", http.MethodGet, "/connections/"+id, nil, http.StatusOK)
	route("/connections/{id}")
	cal := func(email string) string { return googleauth.CalendarURL(googleauth.CalDAVBase, email) }
	if r := f.post("/connections/"+id+"/calendars", url.Values{
		"calendar": {cal(account), cal("oma.hildegard@gmail.com")}, "shown": {cal(account), cal("oma.hildegard@gmail.com")}, "interval": {"15"},
		"name_" + cal(account): {"Konstantin"}, "name_" + cal("oma.hildegard@gmail.com"): {"Oma Hildegard"}, "people_" + cal(account): f.ids, "whole_family": {"1"},
	}); r.status != http.StatusSeeOther {
		t.Fatalf("choose calendars = %d\n%s", r.status, r.body)
	}
	grab("connection", http.MethodGet, "/connections/"+id, nil, http.StatusOK)
	route("/connections/{id}")
	grab("registered", http.MethodGet, "/connections/new?service=google", nil, http.StatusOK)
	route("/connections/new")
	grab("resignin", http.MethodGet, "/connections/google/signin?connection="+id, nil, http.StatusOK)
	route("/connections/google/signin")

	// A sign-in with another account than the connection's.
	g.set(func(g *googleFake) { g.account = "papa.mit-sehr-langem-namen@gmail.com"; g.custom = nil })
	wrong := map[string]string{}
	for _, lang := range []string{"de", "en"} { // a state works once: one sign-in for each language
		start = f.do(http.MethodPost, "/connections/google/start", url.Values{"connection": {id}}, nil)
		state, _ = g.hand(start.location, "wrong-"+lang)
		r := f.do(http.MethodGet, googleCallback(state, "wrong-"+lang), nil, map[string]string{"Accept-Language": lang})
		if r.status != http.StatusConflict {
			t.Fatalf("wrong account = %d\n%s", r.status, r.body)
		}
		wrong[lang] = r.body
	}
	pages = append(pages, layoutPage{site: parent, path: "/google-layout/wrong-account", html: wrong})
	route("/connections/google/callback")

	// Google stops accepting the sign-in: the notices on Start and on the connections.
	g.set(func(g *googleFake) { g.failToken, g.account = "invalid_grant", account })
	f.box.now = f.box.now.Add(2 * time.Hour) // the access token ran out; the same day, so no day note is pruned
	cals, _ := f.db.SyncedCalendars(f.householdID)
	for _, c := range cals {
		if c.ConnectionID == id {
			_ = f.sync.Sync(t.Context(), f.householdID, c.ID)
		}
	}
	grab("notice-home", http.MethodGet, "/", nil, http.StatusOK)
	route("/")
	grab("notice-calendars", http.MethodGet, "/calendars", nil, http.StatusOK)
	route("/calendars")
	grab("notice-connection", http.MethodGet, "/connections/"+id, nil, http.StatusOK)
	route("/connections/{id}")

	if err := f.db.DeleteConnection(f.householdID, id); err != nil {
		t.Fatal(err)
	}
	f.box.now = f.box.now.Add(-2 * time.Hour)
	return pages
}
