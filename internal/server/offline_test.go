package server_test

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 201: offline mode and "Connections to the outside" (Q62 b, Q141 to
// Q146). The design is docs/design/preview-201.html.

func offlineOn(t *testing.T, b *box) {
	t.Helper()
	if err := b.db.SetOffline(true, b.now); err != nil {
		t.Fatal(err)
	}
}

// ---- the setup wizard (Q142)

func TestWizardAsksWhetherTheBoxStaysOfflineRightAfterChoosingTheHomeNetwork(t *testing.T) {
	b := newFreshBox(t)
	r := getEN(b, "/setup/https?way=own")
	if r.status != http.StatusOK {
		t.Fatalf("status = %d", r.status)
	}
	mustContain(t, r.body, "Should the box stay fully offline?", `action="/setup/offline"`, `name="offline" value="on"`, `name="offline" value="off"`,
		"Yes, fully offline", "No, only what is needed may go out", "In return:", "Settings › Connections to the outside")
	mustNotContain(t, r.body, " checked") // neither answer is preselected: both ways are equal
	if !strings.Contains(r.body, "Step 1 of 4") {
		t.Errorf("the question is part of step 1:\n%s", r.body)
	}
}

func TestWizardDoesNotAskAboutAPublicName(t *testing.T) {
	b := newFreshBox(t)
	for _, way := range []string{"", "?way=public", "?way=domain"} {
		r := getEN(b, "/setup/https"+way)
		mustNotContain(t, r.body, "Should the box stay fully offline?")
	}
}

func TestWizardNeedsAnAnswer(t *testing.T) {
	b := newFreshBox(t)
	r := b.do(http.MethodPost, "/setup/offline", url.Values{}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", r.status)
	}
	mustContain(t, r.body, `id="err-offline"`, "Choose one of the two answers.", `aria-invalid="true"`)
	if box, _ := b.db.OutboundBox(); box.Asked {
		t.Error("no answer was given, yet the question counts as answered")
	}
}

func TestAnsweringYesSwitchesOfflineModeOnAndGoesOnToTheHomeNetworkPage(t *testing.T) {
	b := newFreshBox(t)
	r := b.post("/setup/offline", url.Values{"offline": {"on"}})
	if r.status != http.StatusSeeOther || r.location != "/setup/https?way=own" {
		t.Fatalf("answer = %d %q", r.status, r.location)
	}
	box, _ := b.db.OutboundBox()
	if !box.Asked || !box.Offline {
		t.Errorf("box = %+v", box)
	}
	page := getEN(b, "/setup/https?way=own").body
	mustNotContain(t, page, "Should the box stay fully offline?")
	mustContain(t, page, `action="/setup/https"`)
}

func TestAnsweringNoLeavesTheBoxOnline(t *testing.T) {
	b := newFreshBox(t)
	if r := b.post("/setup/offline", url.Values{"offline": {"off"}}); r.status != http.StatusSeeOther {
		t.Fatalf("answer = %d", r.status)
	}
	if box, _ := b.db.OutboundBox(); !box.Asked || box.Offline {
		t.Errorf("box = %+v", box)
	}
	if r := b.post("/setup/offline", url.Values{"offline": {"maybe"}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("a made-up answer = %d, want 422", r.status)
	}
}

func TestChoosingAnotherWayAfterYesTakesTheAnswerBack(t *testing.T) {
	b := newFreshBox(t)
	b.post("/setup/offline", url.Values{"offline": {"on"}})
	getEN(b, "/setup/https") // "Choose another way"
	if box, _ := b.db.OutboundBox(); box.Offline || box.Asked {
		t.Errorf("box = %+v: a public name needs the internet", box)
	}
}

// ---- the page "Connections to the outside"

func TestOnlyParentsSeeAndChangeTheConnections(t *testing.T) {
	b := signedInBox(t)
	child := childSession(t, b)
	for _, path := range []string{"/settings/connections", "/settings/connections/online", "/settings/connections/offline"} {
		if r := getEN(child, path); r.status != http.StatusForbidden && r.status != http.StatusSeeOther {
			t.Errorf("child GET %s = %d", path, r.status)
		}
	}
	if r := child.post("/settings/connections", url.Values{"offline": {"on"}}); r.status == http.StatusSeeOther && strings.Contains(r.location, "connections") {
		t.Errorf("a child's POST reached the page: %d %s", r.status, r.location)
	}
	if box, _ := b.db.OutboundBox(); box.Offline {
		t.Error("a child switched offline mode on")
	}
}

func TestTheConnectionsPageListsEveryConnectionWithItsSwitchAndTheDNSRowWithout(t *testing.T) {
	b := signedInBox(t)
	r := getEN(b, "/settings/connections")
	if r.status != http.StatusOK {
		t.Fatalf("status = %d", r.status)
	}
	mustContain(t, r.body, "<h1>Connections to the outside</h1>", `class="crumbs"`, `<a href="/settings">Settings</a>`,
		`name="offline"`, `name="weather" checked`, `name="update" checked`, `name="calendars" checked`, `name="ics" checked`, `name="recipes" checked`, `name="images" checked`,
		"Goes to: api.open-meteo.com", "Last contact:", "Never", "DNS service", "Not in use: the box issues its own certificate",
		"What offline mode does not cover", "NTP", "Save")
	mustNotContain(t, r.body, `name="dns"`, "place search", "Place search", " disabled")
	if strings.Contains(r.body, `name="offline" checked`) {
		t.Error("offline mode is on by default")
	}
	// A box without a public name has no way to leave: no proof line while online.
	mustNotContain(t, r.body, "No connection to the outside since")
}

func TestASingleSwitchTurnsOffOnlyThatConnection(t *testing.T) {
	b := signedInBox(t)
	form := url.Values{"weather": {"on"}, "update": {"on"}, "ics": {"on"}, "recipes": {"on"}, "images": {"on"}} // calendars unticked
	if r := b.post("/settings/connections", form); r.status != http.StatusSeeOther || r.location != "/settings/connections" {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
	if got := b.db.Outbound().Verdict(outbound.Calendars); got != outbound.VerdictSwitchedOff {
		t.Errorf("calendars verdict = %v", got)
	}
	if got := b.db.Outbound().Verdict(outbound.Weather); got != outbound.VerdictAllowed {
		t.Errorf("weather verdict = %v", got)
	}
	page := getEN(b, "/settings/connections").body
	mustContain(t, page, "Off: calendar connections are paused", `name="weather" checked`)
	mustNotContain(t, page, `name="calendars" checked`)
	mustContain(t, page, "Connections saved.")
}

func TestSwitchingOnOfflineModeLocksEverythingAndShowsTheProof(t *testing.T) {
	b := signedInBox(t)
	b.now = time.Date(2026, 11, 16, 21, 4, 12, 0, time.UTC)
	r := b.post("/settings/connections", url.Values{"offline": {"on"}, "weather": {"on"}, "update": {"on"}, "calendars": {"on"}, "ics": {"on"}, "recipes": {"on"}})
	if r.status != http.StatusSeeOther || r.location != "/settings/connections" {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
	box, _ := b.db.OutboundBox()
	if !box.Offline {
		t.Fatal("offline mode is not on")
	}
	page := getEN(b, "/settings/connections").body
	mustContain(t, page, `name="offline" checked`, "No connection to the outside since", "Off, locked: offline mode is on", "Updates without the internet",
		`name="update_reminder_days"`, `value="90"`, "No refused attempts yet", "Offline mode is on. The box no longer goes outside.")
	for _, s := range []string{"weather", "update", "calendars", "ics", "recipes", "images"} {
		if !regexp.MustCompile(`name="` + s + `"[^>]* disabled`).MatchString(page) {
			t.Errorf("the switch of %s is not locked", s)
		}
	}
	mustNotContain(t, page, `name="weather" checked`)
	// What was on before stays in the database: the locked rows show the state.
	if svcs, _ := b.db.OutboundServices(); !svcs[outbound.Weather].Enabled {
		t.Error("offline mode changed the weather switch")
	}
}

func TestEveryRefusedAttemptIsListedNewestFirstWithTimeServiceAndTarget(t *testing.T) {
	b := signedInBox(t)
	offlineOn(t, b)
	led := b.db.Outbound()
	led.Refused(time.Date(2026, 11, 16, 22, 0, 0, 0, time.UTC), outbound.Weather, "api.open-meteo.com")
	led.Refused(time.Date(2026, 11, 17, 3, 30, 2, 0, time.UTC), outbound.Update, "api.github.com")
	page := getEN(b, "/settings/connections").body
	mustContain(t, page, `<ol class="refused">`, "Refused attempts", "to api.github.com · refused", "to api.open-meteo.com · refused")
	if strings.Index(page, "api.github.com · refused") > strings.Index(page, "api.open-meteo.com · refused") {
		t.Error("the newest attempt must come first")
	}
	mustNotContain(t, page, "No refused attempts yet")
}

func TestTheUpdateReminderDaysAreSavedWhileOffline(t *testing.T) {
	b := signedInBox(t)
	offlineOn(t, b)
	post := func(days string) resp {
		return b.do(http.MethodPost, "/settings/connections", url.Values{"offline": {"on"}, "update_reminder_days": {days}}, map[string]string{"Accept-Language": "en"})
	}
	if r := post("30"); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	if box, _ := b.db.OutboundBox(); box.ReminderDays != 30 {
		t.Errorf("days = %d", box.ReminderDays)
	}
	r := post("3")
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("3 days = %d, want 422", r.status)
	}
	mustContain(t, r.body, `id="err-update_reminder_days"`)
	if box, _ := b.db.OutboundBox(); box.ReminderDays != 30 {
		t.Errorf("an invalid value was stored: %d", box.ReminderDays)
	}
}

// ---- switching off (Q141)

func TestSwitchingOffAsksFirstWhatGoesOutsideAgain(t *testing.T) {
	b := signedInBox(t)
	connectedBox(t, b)
	b.post("/calendars", url.Values{"name": {"School holidays"}, "url": {"https://www.example.org/holidays.ics"}, "interval": {"360"}, "whole_family": {"1"}})
	if err := b.db.SetServiceEnabled(outbound.Images, false); err != nil {
		t.Fatal(err)
	}
	offlineOn(t, b)

	r := b.post("/settings/connections", url.Values{"update_reminder_days": {"90"}}) // the switch is unticked
	if r.status != http.StatusSeeOther || r.location != "/settings/connections/online" {
		t.Fatalf("unticking = %d %q, want the confirmation page", r.status, r.location)
	}
	if box, _ := b.db.OutboundBox(); !box.Offline {
		t.Fatal("offline mode went off without asking")
	}
	page := getEN(b, "/settings/connections/online").body
	mustContain(t, page, "Switch off offline mode?", "Goes outside again at once", "Only when you use it", "Stays off",
		"api.open-meteo.com", "caldav.example.org", "School holidays", "www.example.org", "Web images", "Was already off before offline mode",
		`action="/settings/connections/online"`, "Yes, switch off offline mode", "No, stay offline", "Connections to the outside")

	r = b.post("/settings/connections/online", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/settings/connections" {
		t.Fatalf("confirm = %d %q", r.status, r.location)
	}
	if box, _ := b.db.OutboundBox(); box.Offline {
		t.Error("offline mode is still on")
	}
	v := b.db.Outbound()
	if v.Verdict(outbound.Weather) != outbound.VerdictAllowed || v.Verdict(outbound.Images) != outbound.VerdictSwitchedOff {
		t.Error("the switches must come back as they were before offline mode")
	}
}

func TestABoxSetUpOfflineComesBackWithEverySwitchOn(t *testing.T) {
	b := signedInBox(t)
	if err := b.db.AnswerOfflineQuestion(true, b.now); err != nil {
		t.Fatal(err)
	}
	b.post("/settings/connections/online", url.Values{})
	for _, s := range outbound.Services {
		if b.db.Outbound().Verdict(s) != outbound.VerdictAllowed {
			t.Errorf("%s is not on", s)
		}
	}
}

// ---- a public name (Q143)

func publicBox(t *testing.T) *box {
	t.Helper()
	b := signedInBox(t)
	b.public.name = "familie-maier.dedyn.io"
	err := b.db.SavePublicHTTPS(store.PublicHTTPS{Name: "familie-maier.dedyn.io", Zone: "dedyn.io", Provider: "desec", Issuer: "letsencrypt", LANIP: "192.168.178.40", Token: []byte("sealed")})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOfflineModeWithAPublicNameLeadsToTheChangePageAndSwitchesNothing(t *testing.T) {
	b := publicBox(t)
	r := b.post("/settings/connections", url.Values{"offline": {"on"}})
	if r.status != http.StatusSeeOther || r.location != "/settings/connections/offline" {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
	if box, _ := b.db.OutboundBox(); box.Offline {
		t.Fatal("offline mode went on although the public name needs the internet")
	}
	pairedDisplay(t, b, "Kitchen", "eink_bw")
	page := getEN(b, "/settings/connections/offline").body
	mustContain(t, page, "<h1>Switch on offline mode</h1>", "familie-maier.dedyn.io", "deSEC", "What the change costs", "New address", "192.168.178.40",
		"Every device trusts the box once more", "Kitchen", "Passkeys only work for the old name", "Mama",
		"Delete the DNS records at deSEC", `name="delete_dns" value="1" checked`, "Change and go offline", "No, change nothing", `href="/settings/connections"`)
}

func TestTheChangePageIsOnlyForABoxWithAPublicName(t *testing.T) {
	b := signedInBox(t)
	if r := b.get("/settings/connections/offline"); r.status != http.StatusSeeOther || r.location != "/settings/connections" {
		t.Errorf("GET = %d %q", r.status, r.location)
	}
	if r := b.post("/settings/connections/offline", url.Values{}); r.status != http.StatusSeeOther {
		t.Errorf("POST = %d", r.status)
	}
	if box, _ := b.db.OutboundBox(); box.Offline {
		t.Error("the change page switched offline mode on for a box without a public name")
	}
}

func TestChangingToTheHomeNetworkDeletesTheRecordsFirstThenGoesOffline(t *testing.T) {
	b := publicBox(t)
	r := b.post("/settings/connections/offline", url.Values{"delete_dns": {"1"}})
	if r.status != http.StatusSeeOther || r.location != "http://192.168.178.40/ca" {
		t.Fatalf("change = %d %q, want the trust page of the new address", r.status, r.location)
	}
	if b.public.deleted != 1 || b.public.cleared != 1 {
		t.Errorf("deleted %d times, cleared %d times, want 1 and 1", b.public.deleted, b.public.cleared)
	}
	if box, _ := b.db.OutboundBox(); !box.Offline {
		t.Error("offline mode is not on")
	}
	if !b.pki.Ready() {
		t.Error("the box has no certificate of its own CA")
	}
}

func TestADNSServiceThatRefusesChangesNothing(t *testing.T) {
	b := publicBox(t)
	b.public.deleteErr = errors.New("token no longer valid")
	r := b.do(http.MethodPost, "/settings/connections/offline", url.Values{"delete_dns": {"1"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", r.status)
	}
	mustContain(t, r.body, "Nothing was changed.", `id="err-dns"`, "deSEC refused to delete", "Try again", "untick the box", `name="delete_dns" value="1" checked`)
	if b.public.cleared != 0 {
		t.Error("the public name was dropped although the records are still there")
	}
	if box, _ := b.db.OutboundBox(); box.Offline {
		t.Error("offline mode went on")
	}
	// Unticked: the family deletes the records itself later.
	r = b.post("/settings/connections/offline", url.Values{})
	if r.status != http.StatusSeeOther || b.public.cleared != 1 || b.public.deleted != 1 {
		t.Errorf("without the tick: %d, cleared %d, deleted %d", r.status, b.public.cleared, b.public.deleted)
	}
	if box, _ := b.db.OutboundBox(); !box.Offline {
		t.Error("offline mode is not on")
	}
}

// ---- the notices (Q144 addition)

func TestParentsSeeTheOfflineNoticeOnHomeAndHubAndChildrenDoNot(t *testing.T) {
	b := signedInBox(t)
	if home := getEN(b, "/").body; strings.Contains(home, "offline-note") {
		t.Error("the notice shows although offline mode is off")
	}
	offlineOn(t, b)
	for _, path := range []string{"/", "/settings"} {
		page := getEN(b, path).body
		mustContain(t, page, `class="offline-note"`, "Offline mode is on.", `<a href="/settings/connections">Change</a>`)
	}
	mustContain(t, getEN(b, "/settings").body, "<strong>Connections to the outside</strong>", "<b>Offline mode on.</b>")
	child := childSession(t, b)
	if r := getEN(child, "/"); strings.Contains(r.body, "offline-note") {
		t.Errorf("a child sees the notice:\n%s", r.body)
	}
}

func TestTheHubRowSaysTheStateInWordsAndComesAfterHousehold(t *testing.T) {
	b := signedInBox(t)
	page := getEN(b, "/settings").body
	mustContain(t, page, "<b>Offline mode off.</b>")
	if strings.Index(page, `href="/settings/connections"`) < strings.Index(page, `href="/settings/household"`) {
		t.Error("the row belongs after Household")
	}
}

func TestAPageWhoseFeatureNeedsTheOutsideShowsTheNoticeInsteadOfTheForm(t *testing.T) {
	b := signedInBox(t)
	offlineOn(t, b)
	page := getEN(b, "/connections/new").body
	mustContain(t, page, `class="offline-note"`, "Offline mode is on.", "Connecting a calendar and subscribing to a calendar link need a connection to the internet.", "Set up calendar apps", `href="/caldav"`)
	mustNotContain(t, page, `action="/connections"`, `name="password"`)

	// Only calendar services off, offline mode off: the same notice for that one connection.
	b2 := signedInBox(t)
	if err := b2.db.SetServiceEnabled(outbound.Calendars, false); err != nil {
		t.Fatal(err)
	}
	page = getEN(b2, "/connections/new").body
	mustContain(t, page, `class="offline-note"`, "Calendar services are off.", `href="/settings/connections#calendars"`)
	mustNotContain(t, page, `action="/connections"`)
}

func TestConnectingIsRefusedWhileTheConnectionIsOff(t *testing.T) {
	b := signedInBox(t)
	offlineOn(t, b)
	b.sync.SetDialer(nil) // the real dialer reaches nothing: the gate stops it first
	r := b.post("/connections", url.Values{"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {"u"}, "password": {"pw"}})
	if r.status != http.StatusSeeOther || r.location != "/connections/new" {
		t.Fatalf("connect = %d %q, want back to the page with the notice", r.status, r.location)
	}
	h, _ := b.db.HomeHousehold()
	if conns, _ := b.db.Connections(h.ID); len(conns) != 0 {
		t.Errorf("a connection was stored: %v", conns)
	}
}

func TestPausedCalendarsKeepTheirRowsWithAWordTagAndAreNotProblems(t *testing.T) {
	b := signedInBox(t)
	id := connectedBox(t, b)
	b.post("/calendars", url.Values{"name": {"Waste"}, "url": {"https://www.example.org/waste.ics"}, "interval": {"360"}, "whole_family": {"1"}})
	offlineOn(t, b)
	b.now = b.now.Add(30 * time.Hour) // a day without a sync would otherwise be a warning

	page := getEN(b, "/calendars").body
	mustContain(t, page, `class="offline-note"`, "Offline mode is on.", "Subscribed calendars", `class="tag paused"`, "paused, offline mode")
	if n := strings.Count(page, "paused, offline mode"); n < 2 {
		t.Errorf("%d paused tags, want one per connection and one per subscribed calendar", n)
	}
	mustNotContain(t, page, `class="flag problem"`, "data-sync-stale", `action="/connections/new"`)

	conn := getEN(b, "/connections/"+id).body
	mustContain(t, conn, `class="offline-note"`, "This connection is paused.", `class="tag paused"`, "Calendars on the box")
	mustNotContain(t, conn, `action="/connections/`+id+`/credentials"`)
}

func TestSyncingAPausedCalendarNowAsksNothingOfTheServer(t *testing.T) {
	b := signedInBox(t)
	b.post("/calendars", url.Values{"name": {"Waste"}, "url": {"https://www.example.org/waste.ics"}, "interval": {"360"}, "whole_family": {"1"}})
	offlineOn(t, b)
	h, _ := b.db.HomeHousehold()
	cals, _ := b.db.SyncedCalendars(h.ID)
	r := b.post("/calendars/"+cals[0].ID+"/sync", url.Values{})
	if r.status != http.StatusSeeOther {
		t.Fatalf("sync now = %d", r.status)
	}
	if page := getEN(b, "/calendars/"+cals[0].ID).body; strings.Contains(page, "flag problem") {
		t.Errorf("a paused calendar is shown as a problem:\n%s", page)
	}
	attempts, _ := b.db.RefusedAttempts(10)
	if len(attempts) != 1 || attempts[0].Service != outbound.ICS {
		t.Errorf("refused attempts = %+v", attempts)
	}
}

func TestTheImageFromTheWebFieldGivesWayToTheNotice(t *testing.T) {
	b := signedInBox(t)
	if err := b.db.SetServiceEnabled(outbound.Images, false); err != nil {
		t.Fatal(err)
	}
	page := getEN(b, "/settings/lists/library/new").body
	mustContain(t, page, `class="offline-note"`, "Web images are off.", `href="/settings/connections#images"`)
	mustNotContain(t, page, `data-fetch-url`)
}

// ---- the reminder (Q145)

// signInLater moves the clock and signs in again: sessions end.
func signInLater(t *testing.T, b *box, secret string, to time.Time) {
	t.Helper()
	b.now = to
	b.logout()
	if r := b.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("login = %d", r.status)
	}
}

func TestAfterNinetyDaysWithoutAnUpdateParentsSeeTheReminder(t *testing.T) {
	b, secret := configuredBox(t)
	signInLater(t, b, secret, b.now)
	offlineOn(t, b)
	installed, _ := b.db.OutboundBox()
	if installed.ReminderDays != 90 {
		t.Fatalf("default = %d", installed.ReminderDays)
	}
	signInLater(t, b, secret, installed.UpdatedAt.AddDate(0, 0, 10))
	if page := getEN(b, "/").body; strings.Contains(page, "Not updated for") {
		t.Error("the reminder shows on the tenth day")
	}
	signInLater(t, b, secret, installed.UpdatedAt.AddDate(0, 0, 91))
	page := getEN(b, "/").body
	mustContain(t, page, `class="notice lead-in"`, "Not updated for 91 days.", "Update without the internet", "install.md#update-without-the-internet")
	child := childSession(t, b)
	if r := getEN(child, "/"); strings.Contains(r.body, "Not updated for") {
		t.Error("a child sees the reminder")
	}
	// Online, the box updates itself: no reminder.
	if err := b.db.SetOffline(false, b.now); err != nil {
		t.Fatal(err)
	}
	if page := getEN(b, "/").body; strings.Contains(page, "Not updated for") {
		t.Error("the reminder shows while the box can update itself")
	}
}

func TestTheReminderUsesTheDaysTheParentsChose(t *testing.T) {
	b, secret := configuredBox(t)
	signInLater(t, b, secret, b.now)
	offlineOn(t, b)
	if err := b.db.SetUpdateReminderDays(30); err != nil {
		t.Fatal(err)
	}
	installed, _ := b.db.OutboundBox()
	signInLater(t, b, secret, installed.UpdatedAt.AddDate(0, 0, 31))
	mustContain(t, getEN(b, "/").body, "Not updated for 31 days.")
}

// ---- the host update script (Q145)

func (b *box) updateSettings() string {
	data, _ := os.ReadFile(filepath.Join(b.updateDir, "update-settings"))
	return string(data)
}

func TestTheHostUpdateScriptIsToldToStayOfflineThroughTheSettingsFile(t *testing.T) {
	b := signedInBox(t)
	b.post("/settings/connections", url.Values{"offline": {"on"}, "weather": {"on"}, "update": {"on"}, "calendars": {"on"}, "ics": {"on"}, "recipes": {"on"}, "images": {"on"}})
	if got := b.updateSettings(); got != "mode=on\noffline=yes\n" {
		t.Errorf("settings file while offline = %q", got)
	}
	b.post("/settings/connections/online", url.Values{})
	if got := b.updateSettings(); got != "mode=on\n" {
		t.Errorf("settings file after = %q", got)
	}
	// The update check is a connection of its own.
	b.post("/settings/connections", url.Values{"weather": {"on"}, "calendars": {"on"}, "ics": {"on"}, "recipes": {"on"}, "images": {"on"}})
	if got := b.updateSettings(); got != "mode=on\noffline=yes\n" {
		t.Errorf("settings file with the update check off = %q", got)
	}
}

func TestTheBoxWritesTheOfflineKeyAtStartToo(t *testing.T) {
	b := signedInBox(t)
	offlineOn(t, b)
	_ = server.New(server.Options{DB: b.db, PKI: b.pki, Public: b.public, UpdateDir: b.updateDir, Now: func() time.Time { return b.now }}) // a new server over the same database, as after a restart
	_ = os.Remove(filepath.Join(b.updateDir, "update-settings"))
	_ = server.New(server.Options{DB: b.db, PKI: b.pki, Public: b.public, UpdateDir: b.updateDir, Now: func() time.Time { return b.now }})
	if got := b.updateSettings(); got != "mode=on\noffline=yes\n" {
		t.Errorf("settings file after start = %q", got)
	}
}

// ---- the API (ADR 0015)

func TestTheAPIShowsAndChangesTheConnections(t *testing.T) {
	c := signedInAPI(t)
	status, got, _ := c.call(http.MethodGet, "/outbound", nil)
	if status != http.StatusOK || got["offline"] != false || got["update_reminder_days"] != float64(90) {
		t.Fatalf("get = %d %v", status, got)
	}
	conns, _ := got["connections"].([]any)
	if len(conns) != 7 {
		t.Fatalf("connections = %v", got["connections"])
	}
	status, got, _ = c.call(http.MethodPatch, "/outbound", map[string]any{"connections": map[string]any{"weather": false}})
	if status != http.StatusOK || c.b.db.Outbound().Verdict(outbound.Weather) != outbound.VerdictSwitchedOff {
		t.Fatalf("patch = %d %v", status, got)
	}
	status, got, _ = c.call(http.MethodPatch, "/outbound", map[string]any{"offline": true})
	if status != http.StatusOK || got["offline"] != true || got["offline_since"] == nil {
		t.Fatalf("offline = %d %v", status, got)
	}
	// While offline the single switches are locked.
	status, _, _ = c.call(http.MethodPatch, "/outbound", map[string]any{"connections": map[string]any{"weather": true}})
	if status != http.StatusConflict {
		t.Errorf("changing a locked switch = %d, want 409", status)
	}
	if status, _, _ = c.call(http.MethodPatch, "/outbound", map[string]any{"connections": map[string]any{"nonsense": true}}); status != http.StatusUnprocessableEntity {
		t.Errorf("unknown connection = %d, want 422", status)
	}
	if status, _, _ = c.call(http.MethodPatch, "/outbound", map[string]any{"update_reminder_days": 3}); status != http.StatusUnprocessableEntity {
		t.Errorf("3 days = %d, want 422", status)
	}
	status, got, _ = c.call(http.MethodPatch, "/outbound", map[string]any{"offline": false})
	if status != http.StatusOK || got["offline"] != false {
		t.Fatalf("back online = %d %v", status, got)
	}
}

func TestTheAPIWillNotGoOfflineWithAPublicNameAndOffersTheChange(t *testing.T) {
	c := signedInAPI(t)
	c.b.public.name = "familie-maier.dedyn.io"
	_ = c.b.db.SavePublicHTTPS(store.PublicHTTPS{Name: "familie-maier.dedyn.io", Zone: "dedyn.io", Provider: "desec", Issuer: "letsencrypt", LANIP: "192.168.178.40", Token: []byte("x")})
	status, got, _ := c.call(http.MethodPatch, "/outbound", map[string]any{"offline": true})
	if status != http.StatusConflict {
		t.Fatalf("patch = %d %v, want 409", status, got)
	}
	status, got, _ = c.call(http.MethodGet, "/outbound", nil)
	if got["public_name"] != "familie-maier.dedyn.io" {
		t.Errorf("public_name = %v", got["public_name"])
	}
	c.b.public.deleteErr = errors.New("no")
	if status, got, _ = c.call(http.MethodPost, "/outbound/offline", map[string]any{"delete_dns": true}); status != http.StatusBadGateway {
		t.Fatalf("refused deletion = %d %v, want 502", status, got)
	}
	c.b.public.deleteErr = nil
	status, got, _ = c.call(http.MethodPost, "/outbound/offline", map[string]any{"delete_dns": true})
	if status != http.StatusOK || got["offline"] != true || got["public_name"] != nil {
		t.Fatalf("change = %d %v", status, got)
	}
}

// ---- nothing comes from outside

func TestPagesLoadNothingFromOutside(t *testing.T) {
	b := signedInBox(t)
	offlineOn(t, b)
	ext := regexp.MustCompile(`(?i)(<(?:script|img|link|iframe|source|video|audio)\b[^>]*\b(?:src|href)="(?:https?:)?//|url\((?:"|')?https?://|@import\s+(?:url\()?["']?https?://)`)
	for _, path := range []string{"/", "/settings", "/settings/connections", "/calendars", "/calendar", "/settings/household", "/connections/new", "/display"} {
		body := b.get(path).body
		if m := ext.FindString(body); m != "" {
			t.Errorf("%s loads something from outside: %s", path, m)
		}
	}
}

func TestTheAPIRefusesWhatNeedsAConnectionThatIsOff(t *testing.T) {
	c := signedInAPI(t)
	status, cal, _ := c.call(http.MethodPost, "/calendars", map[string]any{"kind": "ics", "name": "Waste", "url": "https://www.example.org/waste.ics", "audience": map[string]any{"people": []string{}, "groups": []string{}}})
	if status != http.StatusCreated {
		t.Fatalf("add = %d %v", status, cal)
	}
	if status, _, _ = c.call(http.MethodPatch, "/outbound", map[string]any{"offline": true}); status != http.StatusOK {
		t.Fatalf("offline = %d", status)
	}
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/calendar-connections", map[string]any{"provider": "caldav", "url": "https://caldav.example.org", "username": "u", "password": "p"}},
		{http.MethodPost, "/calendars", map[string]any{"kind": "ics", "name": "More", "url": "https://www.example.org/more.ics", "audience": map[string]any{"people": []string{}, "groups": []string{}}}},
		{http.MethodPost, "/calendars/" + cal["id"].(string) + "/sync", nil},
	} {
		if status, got, _ := c.call(call.method, call.path, call.body); status != http.StatusConflict {
			t.Errorf("%s %s = %d %v, want 409", call.method, call.path, status, got)
		}
	}
	_, list, _ := c.call(http.MethodGet, "/outbound", nil)
	if refused, _ := list["refused"].([]any); len(refused) == 0 {
		t.Errorf("the refused sync is not on the list: %v", list["refused"])
	}
}

func TestAPausedCalendarSaysSoInTheAPI(t *testing.T) {
	c := signedInAPI(t)
	status, cal, _ := c.call(http.MethodPost, "/calendars", map[string]any{"kind": "ics", "name": "Waste", "url": "https://www.example.org/waste.ics", "audience": map[string]any{"people": []string{}, "groups": []string{}}})
	if status != http.StatusCreated {
		t.Fatalf("add = %d", status)
	}
	if sync, _ := cal["sync"].(map[string]any); sync["paused"] != false {
		t.Errorf("a calendar of a connection that is on is not paused: %v", sync)
	}
	c.call(http.MethodPatch, "/outbound", map[string]any{"offline": true})
	c.b.now = c.b.now.Add(48 * time.Hour) // without the pause, two days without a sync would be stale
	_, _, list := c.call(http.MethodGet, "/calendars", nil)
	for _, raw := range list {
		cc := raw.(map[string]any)
		if cc["id"] != cal["id"] {
			continue
		}
		sync := cc["sync"].(map[string]any)
		if sync["paused"] != true || sync["state"] != "ok" {
			t.Errorf("sync = %v, want paused and not stale", sync)
		}
	}
}

// The manual update of the install guide ("Update without the internet"): the
// box does not know its image, so the guide has a step that writes the status
// file the nightly updater would write. This runs that very command.
func TestTheManualUpdateOfTheGuideEndsTheReminderAndShowsTheNewVersion(t *testing.T) {
	for _, g := range []struct{ file, heading, olderBoxes string }{
		{"install.md", "## Update without the internet", "run the install script once first"},
		{"install.de.md", "## Update ohne Internet", "führe zuerst einmal das Installationsskript aus"},
	} {
		t.Run(g.file, func(t *testing.T) { manualUpdateOfTheGuide(t, g.file, g.heading, g.olderBoxes) })
	}
}

func manualUpdateOfTheGuide(t *testing.T, file, heading, olderBoxes string) {
	guide, err := os.ReadFile(filepath.Join("..", "..", "docs", file))
	if err != nil {
		t.Fatal(err)
	}
	chapter := string(guide)
	chapter = chapter[strings.Index(chapter, heading):]
	chapter = chapter[:strings.Index(chapter[3:], "\n## ")+3]
	if !strings.Contains(chapter, olderBoxes) {
		t.Errorf("%s has no note that a box installed before offline mode runs the install script once", file)
	}
	cmd := regexp.MustCompile("(?m)^\\s*sudo (sh -c '.*update-status')$").FindStringSubmatch(chapter)
	if cmd == nil {
		t.Fatalf("%s has no step that writes the update-status file:\n%s", file, chapter)
	}

	b, secret := configuredBox(t)
	signInLater(t, b, secret, b.now)
	offlineOn(t, b)
	installed, _ := b.db.OutboundBox()
	signInLater(t, b, secret, installed.UpdatedAt.AddDate(0, 0, 91))
	mustContain(t, getEN(b, "/").body, "Not updated for 91 days.")

	// The guide's command, with the box's data folder and today's date swapped in.
	bin := t.TempDir()
	fakeDate := "#!/bin/sh\necho " + b.now.UTC().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "date"), []byte(fakeDate), 0o755); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("sh", "-c", strings.TrimSuffix(strings.TrimPrefix(strings.ReplaceAll(cmd[1], "/opt/hejmo/data", b.updateDir), "sh -c '"), "'"))
	run.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("the guide's command failed: %v\n%s", err, out)
	}

	page := getEN(b, "/").body
	if strings.Contains(page, "Not updated for") {
		t.Errorf("the reminder stays after the manual update:\n%s", page)
	}
	mustContain(t, getEN(b, "/settings/connections").body, "0.2.4")
}

func TestTheHouseholdSettingsPageSaysWhatOfflineModeSwitchesOff(t *testing.T) {
	b := signedInBox(t)
	mustNotContain(t, getEN(b, "/settings/household").body, `class="offline-note"`)
	offlineOn(t, b)
	mustContain(t, getEN(b, "/settings/household").body, `class="offline-note"`, "Offline mode is on.")
}

// Review of ticket 18: a Google-only household must see every host the box asks
// (the token service and the calendar list too) and its sync interval.
func TestTheCalendarRowNamesGoogleHostsAndTheSyncInterval(t *testing.T) {
	b := signedInBox(t)
	h, _ := b.db.HomeHousehold()
	conn, err := b.db.CreateGoogleConnection(h.ID, "mama@gmail.com", "refresh-1", b.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.AddGoogleCalendar(h.ID, conn.ID, store.NewGoogleCalendar{Name: "Mama", CalendarID: "mama@gmail.com", Color: "#2F5E7A", IntervalMinutes: 15, Now: b.now}); err != nil {
		t.Fatal(err)
	}
	r := getEN(b, "/settings/connections")
	mustContain(t, r.body, "apidata.googleusercontent.com", "oauth2.googleapis.com", "www.googleapis.com", "every 15 minutes")
}
