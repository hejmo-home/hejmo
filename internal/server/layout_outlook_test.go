package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

// addOutlook puts the Outlook pages (ticket 177) into the layout fixture: an
// account with long names, all eight people, a shared calendar and an
// invitation; a second account whose sign-in expired, with a long address; and
// the sign-in page in every state. It returns the pages that no link leads to.
func (f *layoutFixture) addOutlook(t *testing.T) []string {
	t.Helper()
	fake := withGraph(t, f.box, "5f3c1a2e-8b7d-4c9e-a1f0-2d6b9e4c7a31")
	fake.Account, fake.Name = "mama.maier@outlook.de", "Mama Maier"
	fake.AddCalendar("o1", "Familie Maier-Schmidt – alles Gemeinsame")
	fake.AddShared("o2", "Kalender von Papa", "Papa Maier", "papa.maier@outlook.de")
	zone := map[string]any{"timeZone": "W. Europe Standard Time"}
	at := func(dt string) map[string]any { return map[string]any{"dateTime": dt, "timeZone": zone["timeZone"]} }
	fake.Put("o1", map[string]any{"subject": "Elternabend Klasse 3b – Ausflug ins Deutsche Museum und Schwimmkurs", "start": at("2026-11-19T19:30:00"), "end": at("2026-11-19T21:00:00"),
		"location":       map[string]any{"displayName": "Aula der Grundschule an der Pasinger Fabrik"},
		"attendees":      []map[string]any{{"emailAddress": map[string]any{"name": "Papa", "address": "papa.maier@outlook.de"}}},
		"organizer":      map[string]any{"emailAddress": map[string]any{"name": "Huber-Schwarzenbach, Annemarie (Klassenleitung 3b)", "address": "h@example.org"}},
		"responseStatus": map[string]any{"response": "accepted"}, "webLink": "https://outlook.live.com/owa/?itemid=elternabend"})

	flow := func(form url.Values) string {
		t.Helper()
		r := f.do(http.MethodPost, "/connections/outlook", form, nil)
		if r.status != http.StatusSeeOther {
			t.Fatalf("start Outlook sign-in = %d\n%s", r.status, r.body)
		}
		return r.location
	}
	// Account one, through the pages.
	first := flow(url.Values{})
	f.get(first)
	fake.SetDeviceState("approved")
	f.now = f.now.Add(10 * time.Second)
	r := f.get(first)
	m := regexp.MustCompile(`url=/connections/([0-9a-f-]{36})\?`).FindStringSubmatch(r.body)
	if m == nil {
		t.Fatalf("Outlook sign-in did not finish:\n%s", r.body)
	}
	conn := m[1]
	form := url.Values{"interval": {"5"}, "calendar": {"o1", "o2"}, "shown": {"o1", "o2"}, "name_o1": {"Wochenendplanung der Familie Maier-Schmidt"}, "name_o2": {"Papa Arbeit"}, "color_o1": {"#2F5E7A"}, "color_o2": {"#B4583A"},
		"people_o1": f.ids, "people_o2": f.ids[:2]}
	if r := f.post("/connections/"+conn+"/calendars", form); r.status != http.StatusSeeOther {
		t.Fatalf("choose Outlook calendars = %d\n%s", r.status, r.body)
	}
	// Account two: a long address and a sign-in that Microsoft ended.
	h := f.householdID
	long, err := f.db.CreateOutlookConnection(h, store.NewOutlookConnection{Account: "konstantin-alexander.mueller-luedenscheidt@outlook.de", RefreshToken: "x", Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.AddOutlookCalendar(h, long.ID, store.NewOutlookCalendar{Name: "Opa Konstantin", RemoteID: "o9", IntervalMinutes: 5, Now: f.now}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.MarkReauth(long.ID, f.now.Add(-15*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.db.MarkReauth(conn, f.now.Add(-5*time.Hour)); err != nil { // the notice for two accounts
		t.Fatal(err)
	}
	paths := []string{}
	// The sign-in page in each state.
	states := []struct {
		device string
		form   url.Values
	}{{"pending", nil}, {"expired", nil}, {"declined", nil}, {"consent", nil}}
	for _, s := range states {
		p := flow(url.Values{})
		fake.SetDeviceState(s.device)
		f.get(p)
		paths = append(paths, p)
	}
	// A reconnect: waiting, and with another account signed in.
	rec := f.do(http.MethodPost, "/connections/"+long.ID+"/reconnect", url.Values{}, nil)
	f.get(rec.location)
	paths = append(paths, rec.location)
	rec2 := f.do(http.MethodPost, "/connections/"+long.ID+"/reconnect", url.Values{}, nil)
	fake.SetDeviceState("approved")
	f.now = f.now.Add(10 * time.Second)
	f.get(rec2.location)
	paths = append(paths, rec2.location)
	// The invitation's page.
	cals, _ := f.db.SyncedCalendars(h)
	for _, c := range cals {
		if c.Kind != "outlook" || c.SourceName != "Familie Maier-Schmidt – alles Gemeinsame" {
			continue
		}
		evs, _ := f.db.CalendarEvents(h, c.ID)
		for _, e := range evs {
			if strings.HasPrefix(e.Title, "Elternabend Klasse 3b") {
				paths = append(paths, "/calendar/events/"+e.ID)
			}
		}
	}
	return paths
}
