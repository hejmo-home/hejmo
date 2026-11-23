package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/store/storetest"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Ticket 168 (Q27, Q43 to Q45): the scheduler puts articles on lists at the
// household's time on the weekday of a due week.

type alwaysFixture struct {
	db     *store.DB
	h      store.Household
	shop   list.List
	now    time.Time
	runner *server.AlwaysRunner
}

// 23/11/2026 is a Monday; the household's zone is Europe/Berlin (UTC+1 in winter).
func newAlwaysFixture(t *testing.T) *alwaysFixture {
	t.Helper()
	db, err := store.Open(storetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h, err := db.CreateHousehold(store.Household{Name: "Example", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPerson(h.ID, store.Person{Name: "Mama", Role: store.RoleParent}); err != nil {
		t.Fatal(err)
	}
	shop, err := db.CreateList(h.ID, list.List{Name: "Einkauf", Color: "green", Symbol: "square-filled", Sort: list.Manual, Suggestions: true})
	if err != nil {
		t.Fatal(err)
	}
	f := &alwaysFixture{db: db, h: h, shop: shop, now: time.Date(2026, 11, 23, 1, 59, 0, 0, time.UTC)}
	f.runner = server.NewAlwaysRunner(server.AlwaysOptions{DB: db, Now: func() time.Time { return f.now }})
	return f
}

func (f *alwaysFixture) rule(t *testing.T, article string, weekday, every int, qty list.Quantity) {
	t.Helper()
	r := always.Rule{ArticleID: article, ListID: f.shop.ID, Weekday: weekday, Every: every, Start: "2026-11-16", Qty: qty, Lang: "de"}
	if _, err := f.db.SaveAlwaysRule(f.h.ID, r, "2026-11-01"); err != nil {
		t.Fatal(err)
	}
}

func (f *alwaysFixture) tick(at time.Time) {
	f.now = at
	f.runner.Tick(context.Background())
}

func (f *alwaysFixture) entries(t *testing.T) []list.Entry {
	t.Helper()
	es, err := f.db.Entries(f.h.ID, f.shop.ID)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

var monday3 = time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC) // 03:00 in Berlin

func TestOnTheDayAtTheHouseholdTimeTheArticleGoesOnTheListOnce(t *testing.T) {
	t.Parallel()
	f := newAlwaysFixture(t)
	f.rule(t, "milk", 1, 1, list.NewQuantity(3, "l"))
	f.tick(f.now)
	if len(f.entries(t)) != 0 {
		t.Fatal("before the time nothing comes")
	}
	f.tick(monday3)
	es := f.entries(t)
	if len(es) != 1 || es[0].Text != "Milch" || es[0].ArticleID != "milk" || es[0].Qty.Base != 3000 || !es[0].Origin.Auto {
		t.Fatalf("entries = %+v", es)
	}
	f.tick(monday3.Add(5 * time.Hour))
	if got := f.entries(t); len(got) != 1 || got[0].Qty.Base != 3000 {
		t.Errorf("the rules run once a day: %+v", got)
	}
	runs, _ := f.db.AlwaysRuns(f.h.ID, "2026-11-23")
	if len(runs) != 1 || runs[0].Action != "new" || runs[0].Text != "Milch" {
		t.Errorf("runs = %+v", runs)
	}
}

func TestAnOpenEntryGetsTheRulesQuantityAddedAndATickedOneDoesNotCount(t *testing.T) {
	t.Parallel()
	f := newAlwaysFixture(t)
	f.rule(t, "milk", 1, 1, list.NewQuantity(3, "l"))
	f.rule(t, "toast", 1, 1, list.Quantity{})
	if _, err := f.db.AddItem(f.h.ID, f.shop.ID, store.Item{Text: "Milch", ArticleID: "milk", Qty: list.NewQuantity(1, "l")}, func(float64, unit.Kind) string { return "l" }); err != nil {
		t.Fatal(err)
	}
	bread, _ := f.db.AddEntry(f.h.ID, f.shop.ID, "Toastbrot")
	_ = bread
	f.tick(monday3)
	es := f.entries(t)
	if len(es) != 2 {
		t.Fatalf("entries = %+v", es)
	}
	if es[0].Qty.Base != 4000 || es[0].Origin.Prior != 1000 || es[0].Origin.Part != 3000 {
		t.Errorf("milk = %+v", es[0])
	}
	if es[1].Text != "Toastbrot" || es[1].Origin.Auto {
		t.Errorf("toast stays as it is: %+v", es[1])
	}
	runs, _ := f.db.AlwaysRuns(f.h.ID, "2026-11-23")
	if len(runs) != 2 || runs[0].Action != "added" || runs[0].Total.Base != 4000 || runs[1].Action != "exists" {
		t.Errorf("runs = %+v", runs)
	}
}

func TestARuleOnlyRunsInItsWeeksAndNeverCatchesUp(t *testing.T) {
	t.Parallel()
	f := newAlwaysFixture(t)
	f.rule(t, "eggs", 1, 2, list.Quantity{}) // every second Monday from 16/11: 16/11, 30/11
	f.tick(monday3)
	if len(f.entries(t)) != 0 {
		t.Error("23/11 is not a week of the rule")
	}
	// The box was off on Monday 30/11 and starts on Wednesday: Monday is not caught up.
	f.tick(time.Date(2026, 12, 2, 8, 0, 0, 0, time.UTC))
	if len(f.entries(t)) != 0 {
		t.Error("a missed day is not caught up")
	}
}

func TestTheHolidayPausesEverythingAndSwitchesItselfOffAfterItsLastDay(t *testing.T) {
	t.Parallel()
	f := newAlwaysFixture(t)
	f.rule(t, "milk", 1, 1, list.Quantity{})
	_ = f.db.SetAlwaysSettings(f.h.ID, always.Settings{At: "03:00", Holiday: always.Holiday{From: "2026-11-21", To: "2026-11-29"}})
	f.tick(monday3)
	if len(f.entries(t)) != 0 {
		t.Fatal("nothing comes during the holiday")
	}
	f.tick(time.Date(2026, 11, 30, 2, 0, 0, 0, time.UTC))
	s, _ := f.db.AlwaysSettings(f.h.ID)
	if s.Holiday.On() {
		t.Errorf("the holiday is still on after its last day: %+v", s)
	}
	if es := f.entries(t); len(es) != 1 {
		t.Errorf("the Monday after the holiday runs again: %+v", es)
	}
}

func TestTheRuleKeepsRunningForAHiddenArticle(t *testing.T) {
	t.Parallel()
	f := newAlwaysFixture(t)
	f.rule(t, "milk", 1, 1, list.Quantity{})
	if err := f.db.SetArticleHidden(f.h.ID, "milk", true); err != nil {
		t.Fatal(err)
	}
	f.tick(monday3)
	if len(f.entries(t)) != 1 {
		t.Error("hidden articles still come (Q45)")
	}
}

func TestAPausedOrSwitchedOffRuleDoesNothingAndTheTimeIsConfigurable(t *testing.T) {
	t.Parallel()
	f := newAlwaysFixture(t)
	f.rule(t, "milk", 1, 1, list.Quantity{})
	f.rule(t, "eggs", 1, 1, list.Quantity{})
	_ = f.db.DisableAlwaysRule(f.h.ID, "eggs")
	_ = f.db.SetAlwaysSettings(f.h.ID, always.Settings{At: "07:00"})
	f.tick(monday3)
	if len(f.entries(t)) != 0 {
		t.Fatal("the time is 07:00 now")
	}
	f.tick(time.Date(2026, 11, 23, 6, 0, 0, 0, time.UTC))
	if es := f.entries(t); len(es) != 1 || es[0].Text != "Milch" {
		t.Errorf("entries = %+v", es)
	}
	_ = f.db.DeleteList(f.h.ID, f.shop.ID)
}
