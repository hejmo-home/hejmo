package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
)

// Ticket 167 (docs/design/preview-167.html): sharing lists with Apple
// Reminders, in the web interface. A new box has the lists Shopping and To-dos.

type sharingBox struct {
	libBox
	lists []list.List
}

func newSharingBox(t *testing.T) sharingBox {
	t.Helper()
	l := newLibBox(t)
	ls, err := l.db.Lists(l.h.ID)
	if err != nil || len(ls) < 2 {
		t.Fatalf("%v %v", ls, err)
	}
	return sharingBox{l, ls}
}

func (s sharingBox) form(on ...list.List) url.Values {
	f := url.Values{}
	for _, l := range s.lists {
		f.Add("seen", url.Values{"id": {l.ID}, "name": {l.Name}, "color": {l.Color}, "symbol": {l.Symbol}}.Encode())
	}
	for _, l := range on {
		f.Add("share", l.ID)
	}
	return f
}

func TestSettingsListsNamesTheListsInRemindersAndThoseOnlyInHejmo(t *testing.T) {
	t.Parallel()
	s := newSharingBox(t)
	page := s.en("/settings/lists")
	for _, want := range []string{`href="/settings/lists/sharing"`, "Share with other apps", "In Apple Reminders: none", "Only in Hejmo: Einkaufen and To-dos"} {
		if !strings.Contains(page.body, want) {
			t.Errorf("missing %q\n%s", want, page.body)
		}
	}
	if err := s.db.SetListsShared(s.h.ID, map[string]bool{s.lists[0].ID: true}); err != nil {
		t.Fatal(err)
	}
	page = s.de("/settings/lists")
	if !strings.Contains(page.body, "In Apple Erinnerungen: Einkaufen") || !strings.Contains(page.body, "Nur in Hejmo: To-dos") {
		t.Errorf("de\n%s", page.body)
	}
	if !strings.Contains(page.body, "welche Listen auch in Apple Erinnerungen stehen") {
		t.Error("the lead names Erinnerungen")
	}
	// The units form stays below the row, unchanged.
	if strings.Index(page.body, `href="/settings/lists/sharing"`) > strings.Index(page.body, `id="units"`) {
		t.Error("the row belongs above the units")
	}
}

func TestSharingPageHasOneSwitchPerListAllOffAtFirstAndATrailOfCrumbs(t *testing.T) {
	t.Parallel()
	s := newSharingBox(t)
	if _, err := s.db.AddEntry(s.h.ID, s.lists[0].ID, "Milch"); err != nil {
		t.Fatal(err)
	}
	page := s.en("/settings/lists/sharing")
	if page.status != http.StatusOK {
		t.Fatal(page.status)
	}
	for _, want := range []string{
		`<form method="post" action="/settings/lists/sharing"`, "Which lists?", `name="share" value="` + s.lists[0].ID + `"`,
		"On. In Reminders as “Einkaufen”, 1 open entry.", "Off. Only in Hejmo.", `aria-label="Einkaufen in Reminders"`,
		`>Save</button>`, `href="/settings">Settings</a>`, `href="/settings/lists">Lists</a>`,
		"Set up an iPhone or iPad", `href="/settings/lists/sharing/setup"`, "Android: DAVx5",
		"What you can do in Reminders", "What stays in Hejmo", "How “Einkaufen” would look in Reminders",
	} {
		if !strings.Contains(page.body, want) {
			t.Errorf("missing %q\n%s", want, page.body)
		}
	}
	if strings.Contains(page.body, " checked") {
		t.Error("new lists are off")
	}
	if strings.Contains(page.body, "back") && strings.Contains(page.body, `class="back"`) {
		t.Error("a trail, not a back link")
	}
}

func TestSavingSharesTheTickedListsAndTheSuccessLineNamesThem(t *testing.T) {
	t.Parallel()
	s := newSharingBox(t)
	r := s.postEN("/settings/lists/sharing", s.form(s.lists[0], s.lists[1]))
	if r.status != http.StatusSeeOther || r.location != "/settings/lists/sharing" {
		t.Fatalf("%d %s", r.status, r.location)
	}
	page := s.en(r.location)
	if !strings.Contains(page.body, "Saved. “Einkaufen” and “To-dos” appear in Reminders") {
		t.Errorf("success line\n%s", page.body)
	}
	for _, l := range s.lists {
		if got, _ := s.db.List(s.h.ID, l.ID); !got.Shared {
			t.Errorf("%s not shared", l.Name)
		}
	}
	// Unticking switches a list off; nothing is lost in Hejmo.
	s.postEN("/settings/lists/sharing", s.form(s.lists[1]))
	if got, _ := s.db.List(s.h.ID, s.lists[0].ID); got.Shared {
		t.Error("still shared")
	}
	page = s.en("/settings/lists/sharing")
	if !strings.Contains(page.body, "Saved. “To-dos” appears in Reminders") {
		t.Errorf("one list\n%s", page.body)
	}
	s.postEN("/settings/lists/sharing", s.form())
	if page = s.en("/settings/lists/sharing"); !strings.Contains(page.body, "Saved. No list is in Reminders any more.") {
		t.Errorf("none\n%s", page.body)
	}
}

func TestAListDeletedMeanwhileShowsTheErrorAtItsRowAndStoresNothing(t *testing.T) {
	t.Parallel()
	s := newSharingBox(t)
	form := s.form(s.lists[0], s.lists[1])
	if err := s.db.DeleteList(s.h.ID, s.lists[1].ID); err != nil {
		t.Fatal(err)
	}
	r := s.postEN("/settings/lists/sharing", form)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("%d", r.status)
	}
	for _, want := range []string{"To-dos", `class="card-row invalid"`, `aria-invalid="true"`, `role="alert"`, "This list was just deleted"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("missing %q\n%s", want, r.body)
		}
	}
	// The switches stay as they were set, and nothing is stored.
	if !strings.Contains(r.body, `value="`+s.lists[0].ID+`" checked`) {
		t.Error("the other switches keep their state")
	}
	if got, _ := s.db.List(s.h.ID, s.lists[0].ID); got.Shared {
		t.Error("stored although one list is gone")
	}
}

func TestSharingPageWithoutListsSaysSoAndKeepsTheSetup(t *testing.T) {
	t.Parallel()
	s := newSharingBox(t)
	for _, l := range s.lists {
		_ = s.db.DeleteList(s.h.ID, l.ID)
	}
	page := s.en("/settings/lists/sharing")
	for _, want := range []string{"You have no list yet.", `href="/lists"`, "Set up an iPhone or iPad"} {
		if !strings.Contains(page.body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(page.body, ">Save</button>") || strings.Contains(page.body, "What you can do in Reminders") {
		t.Error("no form and no explanation without lists")
	}
}

func TestSharingPageShowsDevicesAndFlagsTokensWithoutTheListsScope(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	c.newToken("calendars tasks lists")
	c.newToken("calendars tasks") // before 0.2
	page := c.b.do(http.MethodGet, "/settings/lists/sharing", nil, map[string]string{"Accept-Language": "en"})
	if page.status != http.StatusOK || strings.Count(page.body, `href="/caldav/tokens/`) != 2 {
		t.Fatalf("%d\n%s", page.status, page.body)
	}
	for _, want := range []string{"Fetches lists.", "Sees no lists: set up before version 0.2"} {
		if !strings.Contains(page.body, want) {
			t.Errorf("missing %q\n%s", want, page.body)
		}
	}
	_ = h
}

func TestOnlyParentsSeeAndSaveSharing(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	kid := childSession(t, b)
	for _, path := range []string{"/settings/lists/sharing", "/settings/lists/sharing/setup"} {
		if r := kid.get(path); r.status != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403", path, r.status)
		}
	}
	if r := kid.post("/settings/lists/sharing", url.Values{}); r.status != http.StatusForbidden {
		t.Errorf("POST = %d, want 403", r.status)
	}
}

func TestSetupPageHasTheStepsTheCAFirstAndTheHomeNetworkHint(t *testing.T) {
	t.Parallel()
	s := newSharingBox(t)
	_ = s.db.SetListsShared(s.h.ID, map[string]bool{s.lists[0].ID: true, s.lists[1].ID: true})
	page := s.de("/settings/lists/sharing/setup")
	if page.status != http.StatusOK {
		t.Fatal(page.status)
	}
	for _, want := range []string{
		`<ol class="howto">`, `class="profile-steps"`, `action="/caldav/profile"`, `name="next" value="/settings/lists/sharing"`, `name="profile_name"`,
		"Profil für iPhone laden", "„Einkaufen“ und „To-dos“", `data-ca-profile-first`, `class="hint home-only"><b>Nur im Heimnetz:</b>`,
		`href="/settings/lists/sharing">Mit anderen Apps teilen</a>`, `href="/settings/lists">Listen</a>`,
	} {
		if !strings.Contains(page.body, want) {
			t.Errorf("missing %q\n%s", want, page.body)
		}
	}
	if strings.Index(page.body, "data-ca-profile-first") > strings.Index(page.body, `<ol class="howto">`) {
		t.Error("the CA notice comes first")
	}
	if !strings.Contains(page.body, `class="crumbs"`) || !strings.Contains(page.body, "Mit anderen Apps teilen") {
		t.Error("trail")
	}
}

func TestSetupPageWithoutSharedListsSaysNoneYet(t *testing.T) {
	t.Parallel()
	s := newSharingBox(t)
	page := s.en("/settings/lists/sharing/setup")
	if !strings.Contains(page.body, "the lists you switched on: none yet. Switch them on under") {
		t.Errorf("\n%s", page.body)
	}
}

func TestSetupPageOverPlainHTTPHasNoFieldAndNoButtonButTheHTTPSAddress(t *testing.T) {
	t.Parallel()
	c := plainBoxSignedIn(t)
	page := c.b.do(http.MethodGet, "/settings/lists/sharing/setup", nil, map[string]string{"Accept-Language": "en"})
	if page.status != http.StatusOK {
		t.Fatal(page.status)
	}
	if strings.Contains(page.body, `name="profile_name"`) || strings.Contains(page.body, "Load the profile") {
		t.Error("no field and no button over HTTP")
	}
	if !strings.Contains(page.body, "only offered over HTTPS") || !strings.Contains(page.body, `href="https://`) {
		t.Errorf("\n%s", page.body)
	}
}

func TestSetupPostWithoutANameShowsTheErrorAtTheFieldOfTheSetupPage(t *testing.T) {
	t.Parallel()
	s := newSharingBox(t)
	r := s.postEN("/caldav/profile", url.Values{"profile_name": {"  "}, "next": {"/settings/lists/sharing"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("%d", r.status)
	}
	for _, want := range []string{`class="f invalid"`, `aria-invalid="true"`, `id="err-profile_name"`, "for example “iPhone Mama”", `class="howto"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("missing %q\n%s", want, r.body)
		}
	}
	if toks, _ := s.db.APITokens(s.h.ID); len(toks) != 0 {
		t.Errorf("tokens %+v", toks)
	}
}

func TestSharingLists_APIReadsAndSwitches(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	ls, _ := c.b.db.Lists(h.ID)
	status, body, _ := c.call(http.MethodPatch, "/list-sharing", map[string]any{"lists": map[string]bool{ls[0].ID: true}})
	if status != http.StatusOK {
		t.Fatalf("%d %s", status, body)
	}
	status, body, _ = c.call(http.MethodGet, "/list-sharing", nil)
	rows, _ := body["lists"].([]any)
	if status != http.StatusOK || len(rows) != len(ls) || rows[0].(map[string]any)["shared"] != true || rows[1].(map[string]any)["shared"] != false {
		t.Fatalf("%d %v", status, body)
	}
	if status, body, _ = c.call(http.MethodGet, "/lists/"+ls[0].ID, nil); status != http.StatusOK || body["shared"] != true {
		t.Errorf("list shared %v", body)
	}
	if status, _, _ = c.call(http.MethodPatch, "/list-sharing", map[string]any{"lists": map[string]bool{"gone": true}}); status != http.StatusUnprocessableEntity {
		t.Errorf("a list that is gone = %d, want 422", status)
	}
}
