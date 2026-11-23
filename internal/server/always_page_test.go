package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/server"
)

// Ticket 168, preview-168 screens 1, 2, 9 to 16: Settings, Lists, Immer auf der Liste.

func (b alwaysBox) rule(article, listID string, weekday, every int, qty list.Quantity) {
	b.t.Helper()
	r := always.Rule{ArticleID: article, ListID: listID, Weekday: weekday, Every: every, Start: "2026-11-09", Qty: qty, Lang: "en"}
	if _, err := b.db.SaveAlwaysRule(b.h.ID, r, "2026-11-01"); err != nil {
		b.t.Fatal(err)
	}
}

func (b alwaysBox) tickRunner() {
	b.t.Helper()
	server.NewAlwaysRunner(server.AlwaysOptions{DB: b.db, Now: func() time.Time { return b.now }}).Tick(context.Background())
}

func TestWithoutRulesTheOverviewSaysHowAndLeadsToTheLibrary(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	page := b.en("/settings/lists/always").body
	got := textOf(page)
	if !strings.Contains(got, "Nothing goes on a list by itself yet. Open an article in the library, such as milk, and switch on “Always on the list” there. To the article library") {
		t.Errorf("page = %s", got)
	}
	if strings.Contains(page, "auto-house") {
		t.Error("the empty page has no form")
	}
}

func TestTheOverviewNamesWhatComesWhenAndWhatIsNotRunning(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t) // Saturday 14/11/2026, before 03:00 Berlin has passed: 12:00 UTC, so today's time is over
	b.rule("milk", b.shop.ID, 1, 1, list.NewQuantity(3, "l"))
	b.rule("toast", b.shop.ID, 1, 1, list.Quantity{})
	b.rule("eggs", b.shop.ID, 1, 2, list.NewQuantity(1, "dozen"))
	b.rule("bananas", b.drugstore.ID, 6, 1, list.Quantity{})
	b.rule("butter", b.shop.ID, 5, 1, list.Quantity{})
	_ = b.db.DisableAlwaysRule(b.h.ID, "butter")
	_ = b.db.DeleteList(b.h.ID, b.drugstore.ID)
	page := b.en("/settings/lists/always").body
	got := textOf(page)
	for _, want := range []string{
		"Coming by themselves",
		"Milk Every Monday on “Shopping” · 3 l Next time: Monday, 16.11.",
		"Sliced bread Every Monday on “Shopping” · without quantity",
		"Eggs Every other Monday on “Shopping” · 1 dozen Next time: Monday, 23.11.",
		"The next two weeks",
		"Monday, 16.11. Shopping: Milk (3 l), Sliced bread",
		"Monday, 23.11. Shopping: Milk (3 l), Sliced bread, Eggs (1 dozen)",
		"Not right now",
		"Bananas Paused: list deleted The list “Drugstore” no longer exists. Choose another one and it carries on every Saturday.",
		"Butter Off Switched off. Still saved: every Friday on “Shopping” · without quantity",
		"For all articles",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page lacks %q\n%s", want, got)
		}
	}
	if !strings.Contains(page, `name="at" value="03:00"`) {
		t.Error("the time is 3:00 by default")
	}
}

func TestSavingTheTimeAndAHolidayWithTheSentencesThatSayWhatFallsOut(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.rule("milk", b.shop.ID, 1, 1, list.NewQuantity(3, "l"))
	b.rule("toast", b.shop.ID, 1, 1, list.Quantity{})
	b.rule("eggs", b.shop.ID, 1, 2, list.Quantity{})
	f := url.Values{"at": {"07:30"}, "holiday": {"on"}, "holiday_from": {"2026-12-19"}, "holiday_to": {"2027-01-03"}}
	r := b.postEN("/settings/lists/always", f)
	if r.status != http.StatusSeeOther || r.location != "/settings/lists/always" {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
	s, _ := b.db.AlwaysSettings(b.h.ID)
	if s.At != "07:30" || s.Holiday.From != "2026-12-19" || s.Holiday.To != "2027-01-03" {
		t.Fatalf("settings = %+v", s)
	}
	page := b.en("/settings/lists/always").body
	got := textOf(page)
	for _, want := range []string{
		"Saved: holiday from Saturday, 19.12. to Sunday, 03.01.2027. Until then everything comes as set.",
		"Holiday planned: From Saturday, 19.12. to Sunday, 03.01.2027 nothing comes by itself. Change the holiday",
		"From Saturday, 19.12. to Sunday, 03.01.2027 nothing comes by itself Drops out: Milk and Sliced bread on 21.12. and 28.12., Eggs on 21.12.",
		"Hejmo does not catch anything up. After that it carries on on Monday, 04.01.2027, and the holiday switches itself off. Until 19.12. everything comes as set.",
		"On. From 19.12. to 03.01.2027 Hejmo puts nothing on the lists by itself.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page lacks %q\n%s", want, got)
		}
	}
	for _, want := range []string{`name="holiday" checked`, `name="holiday_from" value="2026-12-19"`, `name="at" value="07:30"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Switching it off and saving clears the dates.
	b.postEN("/settings/lists/always", url.Values{"at": {"07:30"}})
	if s, _ := b.db.AlwaysSettings(b.h.ID); s.Holiday.On() {
		t.Errorf("holiday = %+v", s.Holiday)
	}
}

func TestAHolidayThatRunsNowSaysUntilWhenAndWhatCarriesOn(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.rule("milk", b.shop.ID, 1, 1, list.Quantity{})
	b.postEN("/settings/lists/always", url.Values{"at": {"03:00"}, "holiday": {"on"}, "holiday_from": {"2026-11-14"}, "holiday_to": {"2026-11-22"}})
	got := textOf(b.en("/settings/lists/always").body)
	for _, want := range []string{
		"Saved: holiday until Sunday, 22.11. Hejmo is not putting anything on the lists by itself right now.",
		"Holiday until Sunday, 22.11.: Hejmo is not putting anything on the lists by itself right now. On Monday, 23.11. it carries on.",
		"Next time: Monday, 23.11. (after the holiday)",
		"Holiday is on, until Sunday, 22.11.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page lacks %q\n%s", want, got)
		}
	}
}

func TestTheHolidayAndTheTimeAreRefusedAtTheirFields(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.rule("milk", b.shop.ID, 1, 1, list.Quantity{})
	for name, tc := range map[string]struct {
		f    url.Values
		want string
	}{
		"before":      {url.Values{"at": {"03:00"}, "holiday": {"on"}, "holiday_from": {"2026-12-19"}, "holiday_to": {"2026-12-03"}}, `id="err-bis"`},
		"no last day": {url.Values{"at": {"03:00"}, "holiday": {"on"}, "holiday_from": {"2026-12-19"}}, "Choose the last day of the holiday."},
		"no first":    {url.Values{"at": {"03:00"}, "holiday": {"on"}, "holiday_to": {"2026-12-19"}}, "Choose the first day of the holiday."},
		"over":        {url.Values{"at": {"03:00"}, "holiday": {"on"}, "holiday_from": {"2026-11-01"}, "holiday_to": {"2026-11-10"}}, "That day is already over."},
		"no time":     {url.Values{"at": {""}}, "Choose a time."},
	} {
		r := b.postEN("/settings/lists/always", tc.f)
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, tc.want) {
			t.Errorf("%s: status %d, lacks %q", name, r.status, tc.want)
		}
	}
	r := b.postEN("/settings/lists/always", url.Values{"at": {"03:00"}, "holiday": {"on"}, "holiday_from": {"2026-12-19"}, "holiday_to": {"2026-12-03"}})
	if !strings.Contains(r.body, "The last day is before the first. Choose Saturday, 19.12. or later.") || !strings.Contains(r.body, `value="2026-12-03"`) {
		t.Error("the error names the day to choose and keeps what was typed")
	}
	if s, _ := b.db.AlwaysSettings(b.h.ID); s.Holiday.On() {
		t.Error("a refused form stores nothing")
	}
}

func TestTheHolidaySentenceIsAskedForWhatIsTyped(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.rule("milk", b.shop.ID, 1, 1, list.Quantity{})
	got := textOf(b.en("/settings/lists/always/plan?from=2026-12-19&to=2027-01-03").body)
	if !strings.HasPrefix(got, "From Saturday, 19.12. to Sunday, 03.01.2027 nothing comes by itself Drops out: Milk on 21.12. and 28.12.") {
		t.Errorf("plan = %q", got)
	}
	if got := textOf(b.en("/settings/lists/always/plan?from=2026-12-19").body); !strings.HasPrefix(got, "Not saved yet") {
		t.Errorf("incomplete = %q", got)
	}
}

func TestTodaysRunShowsInTheNextTwoWeeks(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.now = time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC) // Monday after 03:00 Berlin
	if _, err := b.db.AddEntry(b.h.ID, b.shop.ID, "Sliced bread"); err != nil {
		t.Fatal(err)
	}
	b.rule("milk", b.shop.ID, 1, 1, list.NewQuantity(3, "l"))
	b.rule("eggs", b.shop.ID, 1, 1, list.NewQuantity(1, "dozen"))
	b.rule("toast", b.shop.ID, 1, 1, list.Quantity{})
	b.tickRunner()
	got := textOf(b.en("/settings/lists/always").body)
	for _, want := range []string{
		"Monday, 16.11. Today Shopping: Milk (3 l) put on, Eggs (1 dozen) put on Sliced bread was already on the list and stayed as it was",
		"Monday, 23.11. Shopping: Milk (3 l), Eggs (1 dozen), Sliced bread",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page lacks %q\n%s", want, got)
		}
	}
}

func TestSettingsListsHasTheRowWithNamesAndTheNextTime(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.rule("milk", b.shop.ID, 1, 1, list.Quantity{})
	b.rule("toast", b.shop.ID, 1, 1, list.Quantity{})
	b.rule("bananas", b.drugstore.ID, 6, 1, list.Quantity{})
	b.rule("butter", b.shop.ID, 5, 1, list.Quantity{})
	_ = b.db.DisableAlwaysRule(b.h.ID, "butter")
	_ = b.db.DeleteList(b.h.ID, b.drugstore.ID)
	got := textOf(b.en("/settings/lists").body)
	for _, want := range []string{
		"Always on the list Milk and Sliced bread come by themselves Next time: Monday, 16.11. at 3:00, Milk and Sliced bread on “Shopping” Paused: Bananas (list deleted). Off: Butter",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page lacks %q\n%s", want, got)
		}
	}
	b.postEN("/settings/lists/always", url.Values{"at": {"03:00"}, "holiday": {"on"}, "holiday_from": {"2026-11-14"}, "holiday_to": {"2026-11-22"}})
	got = textOf(b.en("/settings/lists").body)
	if !strings.Contains(got, "Always on the list Holiday until Sunday, 22.11.: nothing comes by itself right now Milk and Sliced bread come by themselves Next time: Monday, 23.11. at 3:00,") {
		t.Errorf("row in the holiday: %s", got)
	}
}

func TestTheLibraryHasAFourthFilterWithNamesAndATagOnEveryRow(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.rule("milk", b.shop.ID, 1, 1, list.Quantity{})
	b.rule("toast", b.shop.ID, 1, 2, list.Quantity{})
	b.rule("bananas", b.drugstore.ID, 6, 1, list.Quantity{})
	b.rule("butter", b.shop.ID, 5, 1, list.Quantity{})
	_ = b.db.DisableAlwaysRule(b.h.ID, "butter")
	_ = b.db.DeleteList(b.h.ID, b.drugstore.ID)
	page := b.en("/settings/lists/library?f=auto").body
	if !strings.Contains(page, `<a href="?f=auto" aria-current="true">Always on the list</a>`) {
		t.Error("the filter is the fourth and current")
	}
	got := textOf(page)
	for _, want := range []string{
		"4 articles with “Always on the list”: 2 come by themselves, Bananas is paused, Butter is off.",
		"Vegetarian · from Hejmo Every Monday on “Shopping”",
		"Vegetarian · from Hejmo Every other Monday on “Shopping”",
		"Paused: list deleted",
		"Always on the list: off",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page lacks %q\n%s", want, got)
		}
	}
	if !strings.Contains(page, `<p class="results-head auto-res">`) || !strings.Contains(page, `<span class="tag auto">`) {
		t.Error("bold result sentence and the tag")
	}
	if all := b.en("/settings/lists/library?q=milk").body; !strings.Contains(all, `<span class="tag auto">`) {
		t.Error("the tag is on every row, not only in the filter")
	}
	if none := newAlwaysBox(t).en("/settings/lists/library?f=auto").body; !strings.Contains(none, "No article has “Always on the list” set up.") {
		t.Error("an empty filter says so")
	}
}
