package server_test

import (
	"net/http"
	"strings"
	"testing"

	ical "github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Lists shared with Apple Reminders over CalDAV (ticket 167). Fake credentials
// only: the tokens are made by the test box. The box's clock and language are
// the defaults (German titles: "1,5 kg").

type davListBox struct {
	c      *apiClient
	h      store.Household
	shop   list.List // shared
	packen list.List // not shared
	ph     *phone
	path   string // the shared list's collection
}

func newDavListBox(t *testing.T) davListBox {
	t.Helper()
	c := signedInAPI(t)
	h, _ := familyOf(t, c)
	mk := func(name string) list.List {
		l, err := c.b.db.CreateList(h.ID, list.List{Name: name, Color: "green", Symbol: "square-filled", Sort: list.Manual, Suggestions: true})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	lb := davListBox{c: c, h: h, shop: mk("Einkaufen"), packen: mk("Packen")}
	if err := c.b.db.SetListsShared(h.ID, map[string]bool{lb.shop.ID: true}); err != nil {
		t.Fatal(err)
	}
	lb.ph = c.phone(t, "calendars tasks lists")
	lb.path = lb.ph.calendarNamed("Einkaufen").Path
	return lb
}

func (lb davListBox) add(t *testing.T, it store.Item, units ...string) list.Entry {
	t.Helper()
	res, err := lb.c.b.db.AddItem(lb.h.ID, lb.shop.ID, it, func(base float64, kind unit.Kind) string {
		return unit.ShowUnit(base, kind, units, unit.DefaultActive)
	})
	if err != nil {
		t.Fatal(err)
	}
	return res.Entry
}

func (lb davListBox) put(t *testing.T, name string, lines ...string) int {
	t.Helper()
	status, _, body := lb.ph.raw("PUT", lb.path+name+".ics", todoICS(name, lines...), map[string]string{"Content-Type": icsType})
	if status >= 300 {
		t.Logf("PUT %s: %d %s", name, status, body)
	}
	return status
}

func (lb davListBox) entries(t *testing.T) []list.Entry {
	t.Helper()
	es, err := lb.c.b.db.Entries(lb.h.ID, lb.shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestOnlySharedListsAreOfferedAsCollectionsOfReminders(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	cals, _ := lb.ph.calendars()
	for _, cal := range cals {
		if cal.Name == "Packen" {
			t.Fatal("a list that is not shared is offered")
		}
	}
	got := lb.ph.calendarNamed("Einkaufen")
	if len(got.SupportedComponentSet) != 1 || got.SupportedComponentSet[0] != "VTODO" {
		t.Fatalf("components %v", got.SupportedComponentSet)
	}
	if status, _, _ := lb.ph.raw("PROPFIND", "/dav/"+lb.c.mama(t)+"/calendars/lists-"+lb.packen.ID+"/", "", map[string]string{"Depth": "0"}); status != http.StatusNotFound {
		t.Errorf("unshared list = %d, want 404", status)
	}
}

func (c *apiClient) mama(t *testing.T) string {
	t.Helper()
	people, _ := c.b.db.People(mustHousehold(t, c).ID)
	for _, p := range people {
		if p.Name == "Mama" {
			return p.ID
		}
	}
	t.Fatal("no Mama")
	return ""
}

func TestAnEntryIsAReminderTitledWithItsQuantityAndCarryingItsNote(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	flour := lb.add(t, store.Item{Text: "Weizenmehl", ArticleID: "wheat-flour", Qty: list.NewQuantity(1.5, "kg")}, "kg")
	lb.add(t, store.Item{Text: "Bananen"})
	cheese := lb.add(t, store.Item{Text: "Käse"})
	_ = lb.c.b.db.SetEntryNote(lb.h.ID, lb.shop.ID, flour.ID, "Type 405")
	_ = lb.c.b.db.SetEntryDone(lb.h.ID, lb.shop.ID, cheese.ID, true)
	todos := lb.ph.todos(lb.path)
	f := todos["Weizenmehl · 1,5 kg"]
	if f == nil || todos["Bananen"] == nil || todos["Käse"] == nil {
		t.Fatalf("titles: %v", todos)
	}
	if d, _ := f.Props.Text(ical.PropDescription); d != "Type 405" {
		t.Errorf("DESCRIPTION = %q", d)
	}
	if s, _ := todos["Käse"].Props.Text(ical.PropStatus); s != "COMPLETED" {
		t.Errorf("a ticked entry is completed, got %q", s)
	}
	if s, _ := todos["Bananen"].Props.Text(ical.PropStatus); s != "NEEDS-ACTION" {
		t.Errorf("status %q", s)
	}
}

func TestListsNeedTheListsScopeAndOldTokensAreNotWidened(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	old := lb.c.phone(t, "calendars tasks")
	cals, _ := old.calendars()
	for _, cal := range cals {
		if cal.Name == "Einkaufen" {
			t.Fatal("a token from before 0.2 sees lists")
		}
	}
	if status, _, _ := old.raw("GET", lb.path, "", nil); status != http.StatusForbidden {
		t.Errorf("old token on a list = %d, want 403", status)
	}
	only := lb.c.phone(t, "lists")
	cals, err := only.calendars()
	if err != nil || len(cals) != 1 || cals[0].Name != "Einkaufen" {
		t.Fatalf("lists only: %v %v", cals, err)
	}
}

func TestChildrenGetNoListsInReminders(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	mia, _ := lb.c.b.db.AddPerson(lb.h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	_ = lb.c.b.db.CreateAccount(store.Account{PersonID: mia.ID, Username: "mia", PasswordHash: "x", TOTPSecret: "S"})
	_, _ = lb.c.b.db.CreateAPIToken(store.APIToken{Name: "kid", Hash: auth.HashToken("hejmo_kid"), PersonID: mia.ID, Scopes: "calendars tasks lists"})
	if status, _, _ := lb.c.phoneWith(t, "mia", "hejmo_kid").raw("PROPFIND", "/dav/", "", map[string]string{"Depth": "0"}); status != http.StatusForbidden {
		t.Errorf("a child's token = %d, want 403", status)
	}
}

func TestTickingInRemindersTicksInHejmoAndBack(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	e := lb.add(t, store.Item{Text: "Bananen"})
	if lb.put(t, e.ID, "SUMMARY:Bananen", "STATUS:COMPLETED") >= 300 {
		t.FailNow()
	}
	if es := lb.entries(t); !es[0].Done {
		t.Fatal("not ticked in Hejmo")
	}
	if lb.put(t, e.ID, "SUMMARY:Bananen", "STATUS:NEEDS-ACTION") >= 300 {
		t.FailNow()
	}
	if es := lb.entries(t); es[0].Done {
		t.Fatal("not unticked in Hejmo")
	}
	_ = lb.c.b.db.SetEntryDone(lb.h.ID, lb.shop.ID, e.ID, true)
	if s, _ := lb.ph.todos(lb.path)["Bananen"].Props.Text(ical.PropStatus); s != "COMPLETED" {
		t.Errorf("ticked in Hejmo = %q in Reminders", s)
	}
}

func TestANewReminderBecomesAnEntryMatchedToAnArticleWithItsQuantityAndNote(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	status := lb.put(t, "new-1", "SUMMARY:Milch 2 l", "DESCRIPTION:laktosefrei", "DUE;VALUE=DATE:20261201", "PRIORITY:1", "BEGIN:VALARM", "TRIGGER:-PT1H", "END:VALARM", "X-APPLE-SORT-ORDER:5")
	if status >= 300 {
		t.FailNow()
	}
	es := lb.entries(t)
	if len(es) != 1 {
		t.Fatalf("%+v", es)
	}
	e := es[0]
	if e.ID != "new-1" || e.Text != "Milch" || e.ArticleID != "milk" || e.Qty.Unit != "l" || e.Qty.Base != 2000 || e.Note != "laktosefrei" {
		t.Fatalf("%+v", e)
	}
	// The date and the priority are not stored: the reminder goes out without them.
	d := lb.ph.todos(lb.path)["Milch · 2 l"]
	if d == nil || d.Props.Get(ical.PropDue) != nil || d.Props.Get("PRIORITY") != nil {
		t.Fatalf("%v", lb.ph.todos(lb.path))
	}
}

func TestANewReminderWithoutAnArticleStaysFreeTextAndABareNumberTakesTheFirstDefaultUnit(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	lb.put(t, "a", "SUMMARY:Zeltstangen")
	lb.put(t, "b", "SUMMARY:Eier 6")
	lb.put(t, "c", "SUMMARY:Käse 2 Scheiben")
	by := map[string]list.Entry{}
	for _, e := range lb.entries(t) {
		by[e.ID] = e
	}
	if a := by["a"]; a.Text != "Zeltstangen" || a.ArticleID != "" || !a.Qty.None() {
		t.Errorf("free text %+v", a)
	}
	if b := by["b"]; b.ArticleID != "eggs" || b.Qty.Unit != "dozen" || b.Qty.Base != 72 {
		t.Errorf("a bare number takes the first default unit (dozen): %+v", b)
	}
	if c := by["c"]; c.Text != "Käse 2 Scheiben" || !c.Qty.None() {
		t.Errorf("an unknown unit stays in the name: %+v", c)
	}
}

func TestADuplicateReminderIsAddedUpAndLeavesTheNextSync(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	lb.add(t, store.Item{Text: "Weizenmehl", ArticleID: "wheat-flour", Qty: list.NewQuantity(1, "kg")}, "kg", "g")
	if lb.put(t, "dup", "SUMMARY:Weizenmehl · 500 g") >= 300 {
		t.FailNow()
	}
	es := lb.entries(t)
	if len(es) != 1 || es[0].Qty.Base != 1500 {
		t.Fatalf("%+v", es)
	}
	if got := lb.ph.todos(lb.path); len(got) != 1 || got["Weizenmehl · 1,5 kg"] == nil {
		t.Fatalf("the second reminder goes at the next sync: %v", got)
	}
}

func TestEditingTheTitleInRemindersChangesTextAndQuantityAndKeepsTheNote(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	e := lb.add(t, store.Item{Text: "Weizenmehl", ArticleID: "wheat-flour", Qty: list.NewQuantity(1, "kg")}, "kg")
	_ = lb.c.b.db.SetEntryNote(lb.h.ID, lb.shop.ID, e.ID, "Type 405")
	lb.put(t, e.ID, "SUMMARY:Weizenmehl - 2 kg", "DESCRIPTION:Type 405")
	got := lb.entries(t)[0]
	if got.Qty.Base != 2000 || got.Note != "Type 405" || got.Text != "Weizenmehl" || got.ArticleID != "wheat-flour" {
		t.Fatalf("%+v", got)
	}
	// Removing the note in Reminders removes it in Hejmo; a long note is cut to 500 characters.
	lb.put(t, e.ID, "SUMMARY:Weizenmehl - 2 kg")
	if n := lb.entries(t)[0].Note; n != "" {
		t.Errorf("note %q", n)
	}
	lb.put(t, e.ID, "SUMMARY:Weizenmehl - 2 kg", "DESCRIPTION:"+strings.Repeat("x", 600))
	if n := lb.entries(t)[0].Note; len([]rune(n)) != list.MaxNote {
		t.Errorf("note of %d characters", len([]rune(n)))
	}
}

func TestDeletingAReminderDeletesTheEntryButNotTheList(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	e := lb.add(t, store.Item{Text: "Bananen"})
	if status, _, _ := lb.ph.raw("DELETE", lb.path+e.ID+".ics", "", nil); status >= 300 {
		t.Fatalf("delete = %d", status)
	}
	if len(lb.entries(t)) != 0 {
		t.Error("entry stays")
	}
	if status, _, _ := lb.ph.raw("DELETE", lb.path, "", nil); status != http.StatusForbidden {
		t.Errorf("deleting the list = %d, want 403", status)
	}
	if _, err := lb.c.b.db.List(lb.h.ID, lb.shop.ID); err != nil {
		t.Errorf("the list is gone: %v", err)
	}
	if status, _, _ := lb.ph.raw("MKCALENDAR", lb.path+"x/", "", nil); status < 400 {
		t.Errorf("creating a list = %d, want refused", status)
	}
}

func TestAReminderNameThatBelongsToAnotherListIsRefused(t *testing.T) {
	t.Parallel()
	lb := newDavListBox(t)
	other, _ := lb.c.b.db.AddEntry(lb.h.ID, lb.packen.ID, "Zelt")
	if status := lb.put(t, other.ID, "SUMMARY:Zelt"); status != http.StatusConflict {
		t.Errorf("status %d, want 409", status)
	}
	var _ caldav.Calendar
}
