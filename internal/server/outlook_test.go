package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/graph"
	"github.com/hejmo-home/hejmo/internal/graph/graphtest"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 177 (ADR 0020, preview 177): Outlook is a calendar connection: signed
// in by device code, synced through Microsoft Graph. Everything runs against a
// fake Graph and a fake token endpoint.

func withGraph(t *testing.T, b *box, defaultApp string) *graphtest.Server {
	t.Helper()
	fake := graphtest.New()
	t.Cleanup(fake.Close)
	b.sync.SetGraph(&graph.Client{AuthBase: fake.AuthBase(), GraphBase: fake.GraphBase(), DefaultClientID: defaultApp})
	return fake
}

func outlookBox(t *testing.T) (*box, *graphtest.Server) {
	t.Helper()
	b := signedInBox(t)
	fake := withGraph(t, b, "app-1")
	fake.AddCalendar("cal-1", "Kalender")
	fake.AddShared("cal-2", "Kalender von Papa", "Papa Maier", "papa@example.org")
	return b, fake
}

var flowRe = regexp.MustCompile(`^/connections/outlook/([^/?]+)$`)

// startSignIn posts the guide's form and returns the sign-in page's address.
func startSignIn(t *testing.T, b *box, form url.Values) string {
	t.Helper()
	r := b.do(http.MethodPost, "/connections/outlook", form, de)
	if r.status != http.StatusSeeOther || !flowRe.MatchString(r.location) {
		t.Fatalf("start sign-in = %d %q\n%s", r.status, r.location, r.body)
	}
	return r.location
}

// later moves the box's clock, so the next view of the sign-in page asks Microsoft again.
func later(b *box, d time.Duration) { b.now = b.now.Add(d) }

// connectOutlook signs in through the pages and returns the connection id.
func connectOutlook(t *testing.T, b *box, fake *graphtest.Server) string {
	t.Helper()
	flow := startSignIn(t, b, url.Values{})
	b.do(http.MethodGet, flow, nil, de)
	fake.SetDeviceState("approved")
	later(b, 10*time.Second)
	r := b.do(http.MethodGet, flow, nil, de)
	m := regexp.MustCompile(`url=(/connections/[^"]+)"`).FindStringSubmatch(r.body)
	if r.status != http.StatusOK || m == nil {
		t.Fatalf("signed-in page = %d, no way on:\n%s", r.status, r.body)
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(m[1], "/connections/"), "?")
	return id
}

func TestOutlookIsAChoiceCardWithTheGuide(t *testing.T) {
	b, _ := outlookBox(t)
	r := b.do(http.MethodGet, "/connections/new?service=outlook", nil, de)
	for _, want := range []string{`value="outlook" checked`, "Outlook.com, Hotmail, Live und Microsoft 365", "Warum bei Microsoft anmelden?",
		"Hejmo sieht dein Microsoft-Passwort nie", "Anmeldecode holen", "Mit einem Arbeits- oder Schulkonto?", "App-Registrierung: Hejmo-Projekt",
		`name="client_id"`, `action="/connections/outlook"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("German connect page misses %q", want)
		}
	}
	// No code is fetched by looking at the page: it would run out before anyone typed it.
	if got := getEN(b, "/connections/new?service=outlook").body; !strings.Contains(got, "Get sign-in code") || !strings.Contains(got, "Why sign in at Microsoft?") {
		t.Errorf("English connect page:\n%s", got)
	}
	wantTrail(t, "/connections/new", r.body, part("Einstellungen", "/settings"), part("Kalender-Verbindungen", "/calendars"), part("Kalender verbinden", ""))
}

func TestWithoutAnAppRegistrationTheGuideSaysSoAndKeepsTheOwnOneOpen(t *testing.T) {
	b := signedInBox(t)
	withGraph(t, b, "") // the project has not created its registration yet
	page := b.do(http.MethodGet, "/connections/new?service=outlook", nil, de).body
	if !strings.Contains(page, "keine App-Registrierung") || !strings.Contains(page, `class="more" open`) && !strings.Contains(page, `<details class="more" open>`) {
		t.Errorf("not-configured state missing:\n%s", page)
	}
	r := b.do(http.MethodPost, "/connections/outlook", url.Values{}, de)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "keine App-Registrierung") {
		t.Fatalf("start without any app id = %d\n%s", r.status, r.body)
	}
}

func TestStartingTheSignInShowsTheCodeTheLinkAndTheTimeItRunsOut(t *testing.T) {
	b, _ := outlookBox(t)
	flow := startSignIn(t, b, url.Values{})
	r := b.do(http.MethodGet, flow, nil, de)
	for _, want := range []string{"Bei Microsoft anmelden", "B7KQ2M9XL", `aria-label="Code: B 7 K Q 2 M 9 X L"`, "Code kopieren", "microsoft.com/devicelogin",
		`href="https://microsoft.com/devicelogin"`, `rel="noopener"`, "Gilt 15 Minuten, bis", "Hejmo wartet auf deine Anmeldung bei Microsoft.", "Zuletzt nachgefragt:", `data-state="waiting"`, "Abbrechen"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("sign-in page misses %q\n%s", want, r.body)
		}
	}
	if !strings.Contains(r.body, `http-equiv="refresh"`) {
		t.Error("the waiting page does not move on by itself")
	}
	wantTrail(t, flow, r.body, part("Einstellungen", "/settings"), part("Kalender-Verbindungen", "/calendars"), part("Kalender verbinden", "/connections/new"), part("Bei Microsoft anmelden", ""))
	if strings.Contains(r.body, "<canvas") || strings.Contains(r.body, "qr") {
		t.Error("the page shows a QR code, the design has none")
	}
}

func TestTheFamilysOwnAppIdIsUsedAndKeptOnTheConnection(t *testing.T) {
	b, fake := outlookBox(t)
	fake.Set(func(s *graphtest.Server) { s.KnownClients = []string{"family-app"} })
	r := b.do(http.MethodPost, "/connections/outlook", url.Values{"client_id": {"unknown-app"}}, de)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Microsoft kennt diese App-ID nicht") || !strings.Contains(r.body, `aria-invalid="true"`) || !strings.Contains(r.body, `value="unknown-app"`) {
		t.Fatalf("unknown app id = %d\n%s", r.status, r.body)
	}
	flow := startSignIn(t, b, url.Values{"client_id": {"family-app"}})
	b.do(http.MethodGet, flow, nil, de)
	fake.SetDeviceState("approved")
	later(b, 10*time.Second)
	b.do(http.MethodGet, flow, nil, de)
	h, _ := b.db.HomeHousehold()
	conns, _ := b.db.Connections(h.ID)
	if len(conns) != 1 || conns[0].ClientID != "family-app" || conns[0].Username != "mama@example.org" {
		t.Fatalf("connections = %+v", conns)
	}
}

func TestMicrosoftBeingDownKeepsYouOnTheGuideWithoutACode(t *testing.T) {
	b, fake := outlookBox(t)
	fake.Set(func(s *graphtest.Server) { s.DeviceCodeDown = true })
	r := b.do(http.MethodPost, "/connections/outlook", url.Values{}, de)
	if r.status != http.StatusBadGateway || !strings.Contains(r.body, "Microsoft antwortet gerade nicht, deshalb gibt es noch keinen Code") || strings.Contains(r.body, "B7KQ2M9XL") {
		t.Fatalf("Microsoft down = %d\n%s", r.status, r.body)
	}
}

func TestApprovingTheSignInCreatesTheConnectionAndMovesOnToChoosingCalendars(t *testing.T) {
	b, fake := outlookBox(t)
	id := connectOutlook(t, b, fake)
	h, _ := b.db.HomeHousehold()
	conns, _ := b.db.Connections(h.ID)
	if len(conns) != 1 || conns[0].Kind != "outlook" || conns[0].ID != id {
		t.Fatalf("connections = %+v", conns)
	}
	page := b.do(http.MethodGet, "/connections/"+id+"?connected=new", nil, de).body
	for _, want := range []string{"mama@example.org", "Mit Outlook verbunden. 2 Kalender gefunden", "Kalender auswählen", "Was die Box in Outlook ändern darf", "Deine eigenen Kalender:",
		"Kalender in Outlook", "Lesen und ändern:", "Geteilt von Papa Maier (papa@example.org)", "Nur lesen:", "Papa Maier hat ihn mit dir geteilt",
		"Anmeldung bei Microsoft", "Neu bei Microsoft anmelden", "Hejmo-Projekt", `Nur in Hejmo. In Outlook heißt er weiter <q>Kalender</q>`} {
		if !strings.Contains(page, want) {
			t.Errorf("Choose calendars misses %q\n%s", want, page)
		}
	}
	if strings.Contains(page, "refresh-") || strings.Contains(page, "Passwort ändern") {
		t.Error("the page shows a token or offers to change a password")
	}
	wantTrail(t, "/connections/"+id, page, part("Einstellungen", "/settings"), part("Kalender-Verbindungen", "/calendars"), part("mama@example.org", ""))
}

func TestTheSignInPageNamesTheAccountOnceSignedIn(t *testing.T) {
	b, fake := outlookBox(t)
	flow := startSignIn(t, b, url.Values{})
	b.do(http.MethodGet, flow, nil, de)
	fake.SetDeviceState("approved")
	later(b, 10*time.Second)
	r := b.do(http.MethodGet, flow, nil, de)
	for _, want := range []string{"Angemeldet als mama@example.org.", "Hejmo sucht deine Kalender. Gleich geht es weiter.", `data-state="ok"`, `http-equiv="refresh"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("signed-in page misses %q\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, "B7KQ2M9XL") {
		t.Error("the code stays on the page after it was used")
	}
}

func TestAFailedSignInReplacesTheCodeWithOneSentenceAndOneButton(t *testing.T) {
	for _, c := range []struct{ state, want string }{
		{"expired", "Der Code ist abgelaufen."},
		{"declined", "Microsoft hat Hejmo keinen Zugriff gegeben."},
		{"consent", "Deine Firma oder Schule muss Hejmo erst freigeben."},
	} {
		b, fake := outlookBox(t)
		flow := startSignIn(t, b, url.Values{})
		fake.SetDeviceState(c.state)
		later(b, 10*time.Second)
		r := b.do(http.MethodGet, flow, nil, de)
		if !strings.Contains(r.body, c.want) || !strings.Contains(r.body, "Neuen Code holen") || strings.Contains(r.body, "B7KQ2M9XL") || strings.Contains(r.body, `http-equiv="refresh"`) {
			t.Errorf("%s: page\n%s", c.state, r.body)
		}
		if c.state == "consent" && (!strings.Contains(r.body, "Calendars.ReadWrite") || !strings.Contains(r.body, "app-1") || !strings.Contains(r.body, "Oder verbinde ein privates Microsoft-Konto")) {
			t.Errorf("admin consent page misses what the IT needs:\n%s", r.body)
		}
		// One click gets a new code.
		again := b.do(http.MethodPost, flow+"/new", url.Values{}, de)
		fake.SetDeviceState("pending")
		if again.status != http.StatusSeeOther || again.location == flow || !strings.Contains(b.do(http.MethodGet, again.location, nil, de).body, "B7KQ2M9XL") {
			t.Errorf("%s: new code = %d %q", c.state, again.status, again.location)
		}
	}
}

func chooseOutlook(t *testing.T, b *box, id string, calendars ...string) resp {
	t.Helper()
	form := url.Values{"interval": {"5"}}
	for _, c := range calendars {
		form.Add("calendar", c)
		form.Add("shown", c)
		form.Set("name_"+c, "Name "+c)
	}
	return b.do(http.MethodPost, "/connections/"+id+"/calendars", form, de)
}

func TestChoosingCalendarsSyncsThemAndSharedOnesAreReadOnly(t *testing.T) {
	b, fake := outlookBox(t)
	fake.Put("cal-1", map[string]any{"subject": "Zahnarzt", "start": map[string]any{"dateTime": "2026-11-20T08:30:00", "timeZone": "W. Europe Standard Time"}, "end": map[string]any{"dateTime": "2026-11-20T09:00:00", "timeZone": "W. Europe Standard Time"}})
	id := connectOutlook(t, b, fake)
	if r := chooseOutlook(t, b, id, "cal-1", "cal-2"); r.status != http.StatusSeeOther {
		t.Fatalf("choose = %d\n%s", r.status, r.body)
	}
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	if len(cals) != 2 || cals[0].Kind != "outlook" || cals[0].ReadOnly || cals[0].SourceName != "Kalender" {
		t.Fatalf("calendars = %+v", cals)
	}
	if !cals[1].ReadOnly || !cals[1].SourceLocked || cals[1].SourceOwner != "Papa Maier (papa@example.org)" {
		t.Fatalf("shared = %+v", cals[1])
	}
	evs, _ := b.db.CalendarEvents(h.ID, cals[0].ID)
	if len(evs) != 1 || evs[0].Title != "Zahnarzt" {
		t.Fatalf("events = %+v", evs)
	}
	// A parent cannot make a shared calendar two-way.
	if r := b.do(http.MethodPost, "/calendars/"+cals[1].ID+"/read-only", url.Values{"read_only": {"0"}}, de); r.status == http.StatusSeeOther {
		again, _ := b.db.SyncedCalendar(h.ID, cals[1].ID)
		if !again.ReadOnly {
			t.Error("a shared Outlook calendar was made two-way")
		}
	}
}

// expire signs the account out at Microsoft and lets the next sync find out.
func expire(t *testing.T, b *box, fake *graphtest.Server) {
	t.Helper()
	fake.Set(func(s *graphtest.Server) { s.RevokeRefresh = true })
	later(b, time.Hour)
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	_ = b.sync.Sync(t.Context(), h.ID, cals[0].ID)
}

func TestAnExpiredSignInShowsReconnectOutlookToParentsWithAccountAndTime(t *testing.T) {
	b, fake := outlookBox(t)
	id := connectOutlook(t, b, fake)
	chooseOutlook(t, b, id, "cal-1")
	expire(t, b, fake)
	for _, path := range []string{"/calendars", "/"} {
		page := b.do(http.MethodGet, path, nil, de).body
		for _, want := range []string{"Outlook neu verbinden", "mama@example.org", "ist abgelaufen", "kommen keine neuen Termine aus diesem Konto auf die Box; die bisherigen bleiben sichtbar", `action="/connections/` + id + `/reconnect"`, `class="notice act reconnect"`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s misses %q\n%s", path, want, page)
			}
		}
	}
	if page := b.do(http.MethodGet, "/", nil, de).body; !strings.Contains(page, "Neu anmelden kann sich, wer das Passwort dieses Microsoft-Kontos kennt.") {
		t.Error("the parents' home does not say who has to sign in")
	}
	api := newAPIClient(t, b)
	api.cookie = true
	if status, _, conns := api.call(http.MethodGet, "/calendar-connections", nil); status != http.StatusOK || len(conns) != 1 || conns[0].(map[string]any)["reauth_since"] == nil {
		t.Errorf("the API does not say the sign-in ended: %d %v", status, conns)
	}
	list := b.do(http.MethodGet, "/calendars", nil, de).body
	if !strings.Contains(list, "Anmeldung abgelaufen: Outlook neu verbinden") {
		t.Errorf("the connection row carries no problem flag in words:\n%s", list)
	}
	// The sign-in works again after reconnecting: the notice goes, the old calendars stay.
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	if len(cals) != 1 || cals[0].Name != "Name cal-1" {
		t.Fatalf("calendars = %+v", cals)
	}
}

func TestSeveralExpiredAccountsAreListedOneByOneAndChildrenDoNotSeeThem(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	fake := withGraph(t, f.box, "app-1")
	fake.AddCalendar("cal-1", "Kalender")
	h, _ := f.db.HomeHousehold()
	for _, acct := range []string{"mama@example.org", "opa@example.org"} {
		c, err := f.db.CreateOutlookConnection(h.ID, store.NewOutlookConnection{Account: acct, RefreshToken: "x", Now: f.now})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.db.MarkReauth(c.ID, f.now); err != nil {
			t.Fatal(err)
		}
	}
	page := f.page("de", "/calendars").body
	if !strings.Contains(page, "Bei zwei Microsoft-Konten ist die Anmeldung abgelaufen.") || !strings.Contains(page, "mama@example.org") || !strings.Contains(page, "opa@example.org") ||
		strings.Count(page, "Neu verbinden") != 2 || strings.Contains(page, "+1") {
		t.Errorf("two expired accounts:\n%s", page)
	}
	mia := childOf(t, f, "Mia")
	if home := mia.do(http.MethodGet, "/", nil, de).body; strings.Contains(home, "Outlook neu verbinden") || strings.Contains(home, "opa@example.org") {
		t.Errorf("a child sees the notice:\n%s", home)
	}
}

func TestReconnectingNeedsTheSameAccountAndKeepsWhatWasChosen(t *testing.T) {
	b, fake := outlookBox(t)
	id := connectOutlook(t, b, fake)
	chooseOutlook(t, b, id, "cal-1")
	expire(t, b, fake)
	fake.Set(func(s *graphtest.Server) { s.RevokeRefresh = false; s.Account = "papa@example.org" })
	r := b.do(http.MethodPost, "/connections/"+id+"/reconnect", url.Values{}, de)
	if r.status != http.StatusSeeOther || !flowRe.MatchString(r.location) {
		t.Fatalf("reconnect = %d %q", r.status, r.location)
	}
	flow := r.location
	page := b.do(http.MethodGet, flow, nil, de).body
	if !strings.Contains(page, "Outlook neu verbinden") || !strings.Contains(page, "Namen, Farben und für wen die Kalender sind, bleiben, wie sie sind.") || !strings.Contains(page, "mama@example.org") || !strings.Contains(page, "B7KQ2M9XL") ||
		!strings.Contains(page, "Anderes Konto verwenden") {
		t.Fatalf("reconnect page:\n%s", page)
	}
	fake.SetDeviceState("approved")
	later(b, 10*time.Second)
	wrong := b.do(http.MethodGet, flow, nil, de).body
	for _, want := range []string{"Das war ein anderes Microsoft-Konto.", "papa@example.org", "Neu verbinden geht nur mit mama@example.org", "Neuen Code holen"} {
		if !strings.Contains(wrong, want) {
			t.Errorf("wrong account page misses %q\n%s", want, wrong)
		}
	}
	h, _ := b.db.HomeHousehold()
	if need, _ := b.db.ReauthConnections(h.ID); len(need) != 1 {
		t.Fatalf("a wrong account cleared the notice")
	}
	// The right account reconnects.
	fake.Set(func(s *graphtest.Server) { s.Account = "mama@example.org" })
	again := b.do(http.MethodPost, flow+"/new", url.Values{}, de)
	b.do(http.MethodGet, again.location, nil, de)
	fake.SetDeviceState("approved")
	later(b, 10*time.Second)
	done := b.do(http.MethodGet, again.location, nil, de)
	if done.status != http.StatusOK || !strings.Contains(done.body, "Angemeldet als mama@example.org.") {
		t.Fatalf("reconnected page = %d\n%s", done.status, done.body)
	}
	if need, _ := b.db.ReauthConnections(h.ID); len(need) != 0 {
		t.Fatalf("still needing sign-in: %+v", need)
	}
	conns, _ := b.db.Connections(h.ID)
	if len(conns) != 1 || conns[0].ID != id {
		t.Fatalf("reconnecting made another connection: %+v", conns)
	}
	if page := b.do(http.MethodGet, "/calendars", nil, de).body; strings.Contains(page, `class="notice act reconnect"`) {
		t.Error("the notice stays after reconnecting")
	}
}

func TestAnInvitationWithAttendeesIsReadOnlyWithALinkToOutlook(t *testing.T) {
	b, fake := outlookBox(t)
	fake.Put("cal-1", map[string]any{"subject": "Elternabend", "start": map[string]any{"dateTime": "2026-11-19T19:30:00", "timeZone": "W. Europe Standard Time"}, "end": map[string]any{"dateTime": "2026-11-19T21:00:00", "timeZone": "W. Europe Standard Time"},
		"attendees": []map[string]any{{"emailAddress": map[string]any{"name": "Papa", "address": "papa@example.org"}}},
		"organizer": map[string]any{"emailAddress": map[string]any{"name": "Huber, Annemarie", "address": "h@example.org"}}, "responseStatus": map[string]any{"response": "accepted"},
		"webLink": "https://outlook.live.com/owa/?itemid=elternabend"})
	id := connectOutlook(t, b, fake)
	chooseOutlook(t, b, id, "cal-1")
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	evs, _ := b.db.CalendarEvents(h.ID, cals[0].ID)
	if len(evs) != 1 {
		t.Fatalf("events = %+v", evs)
	}
	page := b.do(http.MethodGet, "/calendar/events/"+evs[0].ID, nil, de).body
	for _, want := range []string{"Einladung mit Teilnehmern: nur in Outlook änderbar", "Eine Änderung hier würde allen Teilnehmern eine Nachricht schicken", "In Outlook öffnen",
		`href="https://outlook.live.com/owa/?itemid=elternabend"`, "Huber, Annemarie", "Zugesagt (in Outlook)"} {
		if !strings.Contains(page, want) {
			t.Errorf("invitation page misses %q\n%s", want, page)
		}
	}
	if strings.Contains(page, "data-event-form") || strings.Contains(page, "/delete") {
		t.Error("an invitation offers to change or delete it")
	}
	c := newAPIClient(t, b)
	c.cookie = true
	if status, _, _ := c.call(http.MethodPatch, "/events/"+evs[0].ID, map[string]any{"title": "Neu"}); status != http.StatusUnprocessableEntity {
		t.Errorf("changing an invitation through the API = %d, want 422", status)
	}
	// ADR 0015: the API says what the page says, so a client need not learn it from the 422.
	status, _, list := c.call(http.MethodGet, "/events?from=2026-11-01T00:00:00Z&to=2026-12-01T00:00:00Z", nil)
	if status != http.StatusOK || len(list) != 1 {
		t.Fatalf("events = %d %v", status, list)
	}
	inv, _ := list[0].(map[string]any)["invitation"].(map[string]any)
	if inv["organizer"] != "Huber, Annemarie" || inv["response"] != "accepted" || inv["web_link"] != "https://outlook.live.com/owa/?itemid=elternabend" {
		t.Errorf("occurrence invitation = %v", list[0])
	}
}

func TestDisconnectingNamesEveryCalendarAndDeletesNothingInOutlook(t *testing.T) {
	b, fake := outlookBox(t)
	fake.Put("cal-1", map[string]any{"subject": "Zahnarzt", "start": map[string]any{"dateTime": "2026-11-20T08:30:00", "timeZone": "W. Europe Standard Time"}, "end": map[string]any{"dateTime": "2026-11-20T09:00:00", "timeZone": "W. Europe Standard Time"}})
	id := connectOutlook(t, b, fake)
	chooseOutlook(t, b, id, "cal-1", "cal-2")
	page := b.do(http.MethodGet, "/connections/"+id, nil, de).body
	for _, want := range []string{"Verbindung trennen", "Diese Kalender und ihre Termine verschwinden von der Box und von den Displays. In Outlook wird nichts gelöscht.",
		"<strong>Name cal-1</strong> (in Outlook „Kalender“)", "<strong>Name cal-2</strong> (in Outlook „Kalender von Papa“)", "account.live.com/consent/Manage", "myapps.microsoft.com", "Ja, „mama@example.org“ trennen", "Nein, nichts ändern"} {
		if !strings.Contains(page, want) {
			t.Errorf("disconnect section misses %q\n%s", want, page)
		}
	}
	if r := b.do(http.MethodPost, "/connections/"+id+"/delete", url.Values{}, de); r.status != http.StatusSeeOther {
		t.Fatalf("disconnect = %d", r.status)
	}
	h, _ := b.db.HomeHousehold()
	if cals, _ := b.db.SyncedCalendars(h.ID); len(cals) != 0 {
		t.Fatalf("calendars stayed: %+v", cals)
	}
	if len(fake.Events("cal-1")) != 1 {
		t.Fatal("disconnecting deleted something in Outlook")
	}
	if fake.Count("DELETE") != 0 {
		t.Fatal("disconnecting sent a delete to Microsoft")
	}
}

func TestOutlookSignInThroughTheAPI(t *testing.T) {
	c := signedInAPI(t)
	fake := withGraph(t, c.b, "app-1")
	fake.AddCalendar("cal-1", "Kalender")
	fake.AddShared("cal-2", "Kalender von Papa", "Papa Maier", "papa@example.org")
	status, in, _ := c.call(http.MethodPost, "/calendar-connections/outlook/sign-ins", map[string]any{})
	if status != http.StatusCreated || in["user_code"] != "B7KQ2M9XL" || in["verification_uri"] != "https://microsoft.com/devicelogin" || in["state"] != "waiting" || in["id"] == "" {
		t.Fatalf("start = %d %v", status, in)
	}
	path := "/calendar-connections/outlook/sign-ins/" + in["id"].(string)
	if status, got, _ := c.call(http.MethodGet, path, nil); status != http.StatusOK || got["state"] != "waiting" || got["last_polled_at"] == nil {
		t.Fatalf("waiting = %d %v", status, got)
	}
	fake.SetDeviceState("approved")
	later(c.b, 10*time.Second)
	status, got, _ := c.call(http.MethodGet, path, nil)
	if status != http.StatusOK || got["state"] != "signed_in" || got["account"] != "mama@example.org" || got["connection_id"] == nil {
		t.Fatalf("signed in = %d %v", status, got)
	}
	status, _, conns := c.call(http.MethodGet, "/calendar-connections", nil)
	if status != http.StatusOK || len(conns) != 1 || conns[0].(map[string]any)["kind"] != "outlook" || strings.Contains(jsonOf(conns[0].(map[string]any)), "refresh-") {
		t.Fatalf("connections = %d %v", status, conns)
	}
	connID := got["connection_id"].(string)
	status, found, _ := c.call(http.MethodGet, "/calendar-connections/"+connID+"/calendars", nil)
	if status != http.StatusOK || len(found["calendars"].([]any)) != 2 {
		t.Fatalf("discover = %d %v", status, found)
	}
	if shared := calendarNamed(t, found, "Kalender von Papa"); shared["read_only"] != true || shared["shared_by"] != "Papa Maier (papa@example.org)" {
		t.Errorf("shared calendar = %v", shared)
	}
	if status, _, _ := c.call(http.MethodGet, "/calendar-connections/outlook/sign-ins/nope", nil); status != http.StatusNotFound {
		t.Errorf("unknown sign-in = %d, want 404", status)
	}
}

func TestEventCountsOfAnOutlookConnection(t *testing.T) {
	b, fake := outlookBox(t)
	fake.Put("cal-1", map[string]any{"subject": "Zahnarzt", "start": map[string]any{"dateTime": "2026-11-20T08:30:00", "timeZone": "UTC"}, "end": map[string]any{"dateTime": "2026-11-20T09:00:00", "timeZone": "UTC"}})
	id := connectOutlook(t, b, fake)
	status, counts := eventCounts(t, b, id)
	if status != http.StatusOK || countOf(t, counts, "cal-1").Count != 1 || countOf(t, counts, "cal-2").State != "empty" {
		t.Fatalf("counts = %d %+v", status, counts)
	}
}
