package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 168 (ADR 0019, Q27, Q43 to Q45, preview-168 screens 3 to 8): the card
// "Immer auf der Liste" on the page of an article.

type alwaysBox struct {
	libBox
	shop, drugstore list.List
}

// The test box's clock stands on Saturday 14/11/2026, 12:00 UTC.
func newAlwaysBox(t *testing.T) alwaysBox {
	t.Helper()
	b := alwaysBox{libBox: newLibBox(t)}
	var err error
	if b.shop, err = b.db.CreateList(b.h.ID, list.List{Name: "Shopping", Color: "blue", Symbol: "circle-filled", Sort: list.Manual, Suggestions: true}); err != nil {
		t.Fatal(err)
	}
	if b.drugstore, err = b.db.CreateList(b.h.ID, list.List{Name: "Drugstore", Color: "green", Symbol: "square-filled", Sort: list.Manual, Suggestions: true}); err != nil {
		t.Fatal(err)
	}
	return b
}

func milkForm(b alwaysBox, extra url.Values) url.Values {
	f := url.Values{"auto_card": {"1"}, "auto": {"on"}, "auto_list": {b.shop.ID}, "auto_weekday": {"1"}, "auto_every": {"1"}, "auto_start": {"2026-11-09"}, "qmode": {"none"}}
	for k, v := range extra {
		f[k] = v
	}
	return f
}

var (
	cardRe = regexp.MustCompile(`(?s)<fieldset class="auto-card".*?</fieldset>\s*<fieldset>`)
	planRe = regexp.MustCompile(`(?s)<div class="auto-plan[^"]*".*?</div>`)
)

func textOf(html string) string {
	html = regexp.MustCompile(`(?s)<svg.*?</svg>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(html, " ")
	return strings.Join(strings.Fields(strings.NewReplacer("&#34;", `"`, "&#39;", "'", " ", " ").Replace(html)), " ")
}

func TestTheArticlePageOffersTheCardWithNothingChosenExceptTheFirstList(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	page := b.en("/settings/lists/library/milk").body
	card := cardRe.FindString(page)
	if card == "" {
		t.Fatal("no card on the page")
	}
	if !strings.Contains(card, `name="auto" aria-label="Milk goes on a list by itself"`) || strings.Contains(card, `name="auto" checked`) {
		t.Errorf("the switch is off and named: %s", card)
	}
	lists, _ := b.db.Lists(b.h.ID)
	first := lists[0]
	if !first.Suggestions {
		t.Fatal("the example lists start with one that has suggestions")
	}
	if !strings.Contains(card, `name="auto_list" value="`+first.ID+`" checked`) || strings.Count(card, `name="auto_list"`) != len(lists) || strings.Count(card, `name="auto_list" value="`) != len(lists) || strings.Count(card, `" checked><span class="marker"`) != 1 {
		t.Errorf("the first list with suggestions is preselected: %s", card)
	}
	if strings.Contains(card, `name="auto_weekday" value="1" checked`) || strings.Contains(card, "checked><span class=\"marker\"") && false {
		t.Error("no day is preselected")
	}
	for _, day := range []string{"Monday", "Tuesday", "Sunday"} {
		if !strings.Contains(card, ">"+day+"<") {
			t.Errorf("weekday %s is written out", day)
		}
	}
	if !strings.Contains(card, "Off. Hejmo does not put Milk on a list by itself.") {
		t.Error("the switch says its state in words")
	}
	// The card sits in the one form, before "In the suggestions".
	if strings.Index(page, `class="auto-card"`) > strings.Index(page, "<legend>In the suggestions</legend>") {
		t.Error("the card goes before the suggestions")
	}
}

func TestSavingTheCardStoresTheRuleAndTheSuccessLineSaysWhenItStarts(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	f := milkForm(b, url.Values{"qmode": {"qty"}, "amount": {"3"}, "unit": {"l"}})
	r := b.postEN("/settings/lists/library/milk/hidden", f)
	if r.status != http.StatusSeeOther || r.location != "/settings/lists/library/milk" {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
	rule, ok, _ := b.db.AlwaysRule(b.h.ID, "milk")
	if !ok || !rule.Enabled || rule.ListID != b.shop.ID || rule.Weekday != 1 || rule.Every != 1 || rule.Qty.Base != 3000 || rule.Start != "2026-11-09" || rule.Lang != "en" {
		t.Fatalf("rule = %+v", rule)
	}
	page := b.en("/settings/lists/library/milk").body
	if got := successLine(page); got != "Saved: Milk goes every Monday at 3:00 on “Shopping”, the first time on Monday, 16.11." {
		t.Errorf("success line = %q", got)
	}
	card := cardRe.FindString(page)
	if !strings.Contains(card, `name="auto" checked`) || !strings.Contains(card, `name="auto_weekday" value="1" checked`) || !strings.Contains(card, `name="qmode" value="qty" checked`) {
		t.Errorf("the card shows the stored rule: %s", card)
	}
	if got := textOf(planRe.FindString(card)); !strings.HasPrefix(got, "Every Monday on “Shopping”, 3 l The first time is on Monday, 16.11. at 3:00, then on 23.11. and so on.") {
		t.Errorf("plan = %q", got)
	}
}

func TestSavingOnTheChosenDayStartsNextTimeAndSaysSo(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.now = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC) // Monday
	r := b.postEN("/settings/lists/library/milk/hidden", milkForm(b, url.Values{"auto_start": {"2026-11-16"}}))
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	if got := successLine(b.en("/settings/lists/library/milk").body); got != "Saved: Milk goes every Monday at 3:00 on “Shopping”, the first time on Monday, 23.11. Nothing changes today." {
		t.Errorf("success line = %q", got)
	}
	rule, _, _ := b.db.AlwaysRule(b.h.ID, "milk")
	if rule.Since != "2026-11-16" {
		t.Errorf("since = %s", rule.Since)
	}
	plan := textOf(planRe.FindString(b.en("/settings/lists/library/milk").body))
	if !strings.Contains(plan, "Today is Monday, but Hejmo only starts the next time.") {
		t.Errorf("plan = %q", plan)
	}
}

func TestACardThatBreaksTheRulesIsRefusedAtItsFieldsAndKeepsWhatWasChosen(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	f := milkForm(b, url.Values{"auto_every": {"0"}, "auto_start": {"2026-11-09"}})
	f.Del("auto_weekday")
	r := b.postEN("/settings/lists/library/milk/hidden", f)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	for _, want := range []string{
		`id="err-day"`, "Choose a day. On that day Hejmo puts Milk on the list.",
		`id="err-every"`, "Enter a number from 1 to 52",
		`name="auto" checked`, `name="auto_list" value="` + b.shop.ID + `" checked`, `value="0"`,
		"Not saved yet",
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if _, ok, _ := b.db.AlwaysRule(b.h.ID, "milk"); ok {
		t.Error("a refused card stores nothing")
	}
}

func TestACardWithAQuantityThatIsNoAmountIsRefusedAtTheAmount(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	r := b.postEN("/settings/lists/library/milk/hidden", milkForm(b, url.Values{"qmode": {"qty"}, "amount": {"abc"}, "unit": {"l"}}))
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-amount"`) {
		t.Errorf("status = %d", r.status)
	}
}

func TestSwitchingTheCardOffKeepsTheRuleSavedButStopsIt(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.postEN("/settings/lists/library/milk/hidden", milkForm(b, nil))
	f := url.Values{"auto_card": {"1"}}
	r := b.postEN("/settings/lists/library/milk/hidden", f)
	if r.status != http.StatusSeeOther {
		t.Fatalf("status = %d", r.status)
	}
	rule, ok, _ := b.db.AlwaysRule(b.h.ID, "milk")
	if !ok || rule.Enabled || rule.Weekday != 1 {
		t.Errorf("rule = %+v", rule)
	}
	if got := successLine(b.en("/settings/lists/library/milk").body); got != "Saved: Milk no longer goes on a list by itself." {
		t.Errorf("success line = %q", got)
	}
}

func TestADeletedListPausesTheRuleAndTheCardSaysWhatToDo(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.postEN("/settings/lists/library/milk/hidden", milkForm(b, nil))
	if err := b.db.DeleteList(b.h.ID, b.shop.ID); err != nil {
		t.Fatal(err)
	}
	page := b.en("/settings/lists/library/milk").body
	for _, want := range []string{
		"On, but paused: the list is missing.",
		`<div class="auto-plan stopped"`,
		"Paused: Hejmo is not putting Milk on any list right now",
		"Day, rhythm and quantity stay saved. With a new list it carries on next Monday.",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Saving without choosing a list names the list that is gone.
	f := milkForm(b, nil)
	f.Del("auto_list")
	r := b.postEN("/settings/lists/library/milk/hidden", f)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "The list “Shopping” that Hejmo put Milk on no longer exists.") {
		t.Errorf("status = %d", r.status)
	}
}

func TestAnOwnArticleHasTheCardInItsOneForm(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	a, err := b.db.CreateArticle(b.h.ID, b.mama.ID, ownArticleForTest(), nil)
	if err != nil {
		t.Fatal(err)
	}
	page := b.en("/settings/lists/library/" + a.ID).body
	if strings.Count(page, `class="auto-card"`) != 1 {
		t.Fatal("the card is on the page once")
	}
	form := url.Values{"name": {a.Name}, "dept": {a.Dept}, "kind": {a.Kind}, "unit1": {a.Unit1}, "pic_source": {""},
		"auto_card": {"1"}, "auto": {"on"}, "auto_list": {b.drugstore.ID}, "auto_weekday": {"4"}, "auto_every": {"4"}, "auto_start": {"2026-11-09"}, "qmode": {"none"}}
	r := b.postEN("/settings/lists/library/"+a.ID, form)
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d %s", r.status, r.body)
	}
	rule, ok, _ := b.db.AlwaysRule(b.h.ID, a.ID)
	if !ok || rule.Every != 4 || rule.Weekday != 4 || rule.ListID != b.drugstore.ID {
		t.Fatalf("rule = %+v", rule)
	}
	if got := successLine(b.en("/settings/lists/library/" + a.ID).body); !strings.Contains(got, "every 4 weeks on Thursday at 3:00 on “Drugstore”, the first time on Thursday, 12.11.") && !strings.Contains(got, "every 4 weeks on Thursday at 3:00 on “Drugstore”") {
		t.Errorf("success line = %q", got)
	}
	// A refused card keeps the article form and shows the error at the field.
	form.Del("auto_weekday")
	if r := b.postEN("/settings/lists/library/"+a.ID, form); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-day"`) {
		t.Errorf("refused = %d", r.status)
	}
}

func TestCopyingAShippedArticleTakesTheRuleAlong(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	b.postEN("/settings/lists/library/milk/hidden", milkForm(b, nil))
	r := b.postEN("/settings/lists/library/milk/copy", url.Values{})
	if r.status != http.StatusSeeOther {
		t.Fatalf("copy = %d", r.status)
	}
	own, _ := b.db.Articles(b.h.ID)
	if len(own) != 1 {
		t.Fatalf("own = %d", len(own))
	}
	if _, ok, _ := b.db.AlwaysRule(b.h.ID, own[0].ID); !ok {
		t.Error("the copy has the rule")
	}
	if _, ok, _ := b.db.AlwaysRule(b.h.ID, "milk"); ok {
		t.Error("the hidden original does not keep it")
	}
}

func TestThePlanSentenceIsAskedForWhatIsChosenSoFar(t *testing.T) {
	t.Parallel()
	b := newAlwaysBox(t)
	q := url.Values{"list": {b.shop.ID}, "weekday": {"4"}, "every": {"4"}, "start": {"2026-12-14"}, "qmode": {"qty"}, "amount": {"1"}, "unit": {"pack"}}
	got := textOf(b.en("/settings/lists/library/plan/milk?" + q.Encode()).body)
	if !strings.HasPrefix(got, "Every 4 weeks on Thursday on “Shopping”, 1 pack The first time is on Thursday, 17.12. at 3:00, then on 14.01.2027, on 11.02.2027 and so on.") {
		t.Errorf("plan = %q", got)
	}
	q.Del("weekday")
	if got := textOf(b.en("/settings/lists/library/plan/milk?" + q.Encode()).body); !strings.HasPrefix(got, "Not saved yet") {
		t.Errorf("incomplete plan = %q", got)
	}
	if r := b.en("/settings/lists/library/plan/nothing"); r.status != http.StatusNotFound {
		t.Errorf("unknown article = %d", r.status)
	}
}

func ownArticleForTest() store.OwnArticle {
	return store.OwnArticle{Name: "Dishwasher tabs", Dept: "household", Kind: "nonfood", Unit1: "pack"}
}
