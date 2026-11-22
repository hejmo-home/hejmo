package server_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Ticket 168, preview-168 screens 17 to 23: the list page and the display say
// why an entry is there.

func inLiters(float64, unit.Kind) string { return "l" }

// mondayList stands on Monday 16/11/2026 after 03:00 with milk (1 l already on
// the list), eggs and toast (open without quantity) ruled to come.
func mondayList(t *testing.T) alwaysBox {
	t.Helper()
	b := newAlwaysBox(t)
	b.now = time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)
	if _, err := b.db.AddItem(b.h.ID, b.shop.ID, store.Item{Text: "Milk", ArticleID: "milk", Qty: list.NewQuantity(1, "l")}, inLiters); err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.AddEntry(b.h.ID, b.shop.ID, "Sliced bread"); err != nil {
		t.Fatal(err)
	}
	b.rule("milk", b.shop.ID, 1, 1, list.NewQuantity(3, "l"))
	if _, err := b.db.SaveAlwaysRule(b.h.ID, always.Rule{ArticleID: "eggs", ListID: b.shop.ID, Weekday: 1, Every: 2, Start: "2026-11-16", Qty: list.NewQuantity(1, "dozen"), Lang: "en"}, "2026-11-01"); err != nil {
		t.Fatal(err)
	}
	b.rule("toast", b.shop.ID, 1, 1, list.Quantity{})
	b.tickRunner()
	return b
}

func TestTheListPageSaysWhatCameByItselfAndWhyUnderTheEntries(t *testing.T) {
	t.Parallel()
	b := mondayList(t)
	page := b.en("/lists/" + b.shop.ID).body
	got := textOf(page)
	for _, want := range []string{
		"Came by itself today at 3:00: Eggs (1 dozen). Milk was already on the list: 3 l added, now 4 l. Sliced bread was already on the list and stays as it is.",
		"1 l already entered + 3 l always (every Monday)",
		"Comes by itself every other Monday",
		"Comes by itself on this list: Milk, Eggs and Sliced bread",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page lacks %q\n%s", want, got)
		}
	}
	if !strings.Contains(page, `<div class="auto-note" role="status">`) || !strings.Contains(page, `<details class="more auto-rules">`) {
		t.Error("the note and the details for parents")
	}
	if strings.Count(page, `class="auto-src"`) != 2 {
		t.Errorf("only entries Hejmo put on carry the line: %d", strings.Count(page, `class="auto-src"`))
	}
	// Changing the quantity by hand ends the split.
	es, _ := b.db.Entries(b.h.ID, b.shop.ID)
	if _, err := b.db.SetQuantity(b.h.ID, b.shop.ID, es[0].ID, list.NewQuantity(5, "l"), "l", inLiters); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(textOf(b.en("/lists/"+b.shop.ID).body), "already entered") {
		t.Error("the split goes after a manual change")
	}
}

func TestTheTilesShowTheShortLineAndAChildSeesNoRules(t *testing.T) {
	t.Parallel()
	b := mondayList(t)
	if err := b.db.SetPersonListView(b.h.ID, b.mama.ID, list.Tiles); err != nil {
		t.Fatal(err)
	}
	page := b.en("/lists/" + b.shop.ID).body
	if !strings.Contains(page, `<span class="tile-auto">`) {
		t.Fatalf("no tile line:\n%s", page)
	}
	got := textOf(page)
	for _, want := range []string{"3 l of it every Monday", "Every other Monday"} {
		if !strings.Contains(got, want) {
			t.Errorf("tiles lack %q", want)
		}
	}
}

func TestInTheHolidayTheListSaysUntilWhenAndWhatComesAgain(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.rule("milk", b.shop.ID, 1, 1, list.Quantity{})
	b.rule("toast", b.shop.ID, 1, 1, list.Quantity{})
	if err := b.db.SetAlwaysSettings(b.h.ID, always.Settings{At: "03:00", Holiday: always.Holiday{From: "2026-11-14", To: "2026-11-22"}}); err != nil {
		t.Fatal(err)
	}
	page := b.en("/lists/" + b.shop.ID).body
	want := "Holiday until Sunday, 22.11.: Hejmo is not putting anything on this list by itself right now. On Monday, 23.11. Milk and Sliced bread come again."
	if got := textOf(page); !strings.Contains(got, want) {
		t.Errorf("page lacks %q\n%s", want, got)
	}
	if !strings.Contains(page, `<div class="auto-note holiday" role="status">`) {
		t.Error("the holiday line has its own look")
	}
}

func TestTheDisplayCarriesTheNoteAndTheLines(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	id, _ := f.onlyList(t, "Shopping", nil)
	l, _ := f.db.List(f.householdID, id)
	if _, err := f.db.UpdateList(f.householdID, list.List{ID: id, Name: l.Name, Color: l.Color, Symbol: l.Symbol, Sort: l.Sort, Suggestions: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.AddItem(f.householdID, id, store.Item{Text: "Milk", ArticleID: "milk", Qty: list.NewQuantity(1, "l")}, inLiters); err != nil {
		t.Fatal(err)
	}
	r := always.Rule{ArticleID: "milk", ListID: id, Weekday: 1, Every: 1, Start: "2026-11-09", Qty: list.NewQuantity(3, "l"), Lang: "en"}
	if _, err := f.db.SaveAlwaysRule(f.householdID, r, "2026-11-01"); err != nil {
		t.Fatal(err)
	}
	f.now = f.at(16, 8, 0)
	f.runAlways(t)
	page := f.display.get("/display/lists/"+id, "en").body
	got := textOf(page)
	for _, want := range []string{"Milk was already on the list: 3 l added, now 4 l.", "3 l of it every Monday"} {
		if !strings.Contains(got, want) {
			t.Errorf("display lacks %q\n%s", want, got)
		}
	}
	displays, _ := f.db.Displays(f.householdID)
	if err := f.db.SetDisplayListView(f.householdID, displays[0].ID, list.Rows); err != nil {
		t.Fatal(err)
	}
	if got := textOf(f.display.get("/display/lists/"+id, "en").body); !strings.Contains(got, "1 l already entered + 3 l always (every Monday)") {
		t.Errorf("the list view of a display has the split:\n%s", got)
	}
	if strings.Contains(page, `class="auto-rules"`) {
		t.Error("a display never offers the rules")
	}
}

// runAlways runs the rules once, at the box's clock.
func (f *familyBox) runAlways(t *testing.T) {
	t.Helper()
	server.NewAlwaysRunner(server.AlwaysOptions{DB: f.db, Now: func() time.Time { return f.now }}).Tick(context.Background())
}

// ADR 0020: the rule keeps running for an article hidden for an allergy; the
// entry carries the allergy warning (ADR 0019).
func TestTheRuleKeepsRunningForAnArticleHiddenForAnAllergyAndTheEntryCarriesTheWarning(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.person("Mia", "child", "omnivore", "peanuts")
	if err := b.db.SetLibrarySettings(b.h.ID, store.LibrarySettings{HideAllergen: true, Everyone: true, People: []string{}, ConfirmedBy: b.mama.ID}); err != nil {
		t.Fatal(err)
	}
	b.now = time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)
	b.rule("peanut-butter", b.shop.ID, 1, 1, list.Quantity{})
	b.tickRunner()
	page := b.en("/lists/" + b.shop.ID).body
	if !strings.Contains(page, "Peanut butter") {
		t.Fatalf("the hidden article is on the list:\n%s", page)
	}
	if got := textOf(page); !strings.Contains(got, "Contains Peanuts") || !strings.Contains(got, "Mia") {
		t.Errorf("the entry lacks the allergy warning:\n%s", got)
	}
}
