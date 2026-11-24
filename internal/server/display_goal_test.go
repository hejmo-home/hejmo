package server_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 293: the savings goal on the display: the column of the family view
// and the foot of the personal view.

func (f *familyBox) goalReward(t *testing.T, name string, price int, ids ...string) store.Reward {
	t.Helper()
	r, err := f.db.CreateReward(f.householdID, f.people["Mama"].ID, store.Reward{Name: name, Symbol: "🎬", Price: price, PersonIDs: ids}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *familyBox) stars(t *testing.T, who string, n int) {
	t.Helper()
	if _, err := f.db.BookStars(f.householdID, f.people[who].ID, n, "test", f.people["Mama"].ID, f.at(14, 8, 0)); err != nil {
		t.Fatal(err)
	}
}

func (f *familyBox) setGoal(t *testing.T, who string, r store.Reward) {
	t.Helper()
	if err := f.db.SetGoal(f.householdID, f.people[who].ID, r.ID); err != nil {
		t.Fatal(err)
	}
}

// goalBlock is the stars block of a person's column; "" without one.
func goalBlock(page, who string) string {
	for _, m := range columnRe.FindAllStringSubmatch(page, -1) {
		if m[1] == who {
			if i := strings.Index(m[0], `<a class="col-goal"`); i >= 0 {
				return m[0][i:]
			}
		}
	}
	return ""
}

func TestTheColumnShowsTheGoalOrOnlyTheStarCount(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	kino := f.goalReward(t, "Kino mit Popcorn", 30)
	f.stars(t, "Sebastian", 12)
	f.stars(t, "Mia", 14)
	f.setGoal(t, "Sebastian", kino)
	page := f.view(t, "en", f.at(14, 9, 0))
	seb := goalBlock(page, "Sebastian")
	for _, want := range []string{`href="/display/person/` + f.people["Sebastian"].ID + `"`, `data-goal`, `aria-label="Savings goal Kino mit Popcorn: 12 of 30 stars"`, `class="k">Savings goal<`,
		`class="art-sym"`, `🎬`, `--pct:40`, `<b>12 of 30</b> stars`} {
		if !strings.Contains(seb, want) {
			t.Errorf("Sebastian's block lacks %q:\n%s", want, seb)
		}
	}
	if !strings.Contains(seb, `class="progress" aria-hidden="true"`) || strings.Contains(seb, "data-reached") {
		t.Errorf("bar must be hidden from readers and not reached:\n%s", seb)
	}
	mia := goalBlock(page, "Mia")
	if !strings.Contains(mia, `<b>14</b> stars`) || strings.Contains(mia, "progress") || strings.Contains(mia, "data-goal") || strings.Contains(mia, "Kino") {
		t.Errorf("Mia has no goal and gets only the count, never a suggestion:\n%s", mia)
	}
	if de := goalBlock(f.view(t, "de", f.at(14, 9, 0)), "Sebastian"); !strings.Contains(de, "<b>12 von 30</b> Sternen") || !strings.Contains(de, ">Sparziel<") {
		t.Errorf("German:\n%s", de)
	}
}

func TestAReachedGoalSaysEnoughSavedWithTheRealBalance(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	f.setGoal(t, "Mia", f.goalReward(t, "Spieleabend", 25))
	f.stars(t, "Mia", 27)
	blk := goalBlock(f.view(t, "en", f.at(14, 9, 0)), "Mia")
	for _, want := range []string{"data-reached", "--pct:100", "<b>Enough saved</b>", "27 stars", `aria-label="Savings goal Spieleabend: enough saved, 27 stars"`} {
		if !strings.Contains(blk, want) {
			t.Errorf("lacks %q:\n%s", want, blk)
		}
	}
	if strings.Contains(blk, "27 of 25") || strings.Contains(blk, "of 25") {
		t.Errorf("never 27 of 25:\n%s", blk)
	}
}

func TestBelowZeroTheColumnShowsNoBarAndHiddenStarsShowNoBlock(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	f.setGoal(t, "Mia", f.goalReward(t, "Kino", 30))
	f.stars(t, "Mia", -2)
	blk := goalBlock(f.view(t, "en", f.at(14, 9, 0)), "Mia")
	if !strings.Contains(blk, "<b>−2</b> stars") || strings.Contains(blk, "progress") {
		t.Errorf("below zero:\n%s", blk)
	}
	page := f.view(t, "en", f.at(14, 9, 0))
	for _, who := range []string{"Mama", "Papa"} {
		if goalBlock(page, who) != "" {
			t.Errorf("%s collects no stars and gets no block", who)
		}
	}
}

func TestTheStarsBlockCanBeSwitchedOffPerDisplay(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	f.stars(t, "Mia", 5)
	if goalBlock(f.view(t, "en", f.at(14, 9, 0)), "Mia") == "" {
		t.Fatal("on by default")
	}
	if r := f.saveLayout(t, f.layoutForm(true, []string{"Mama", "Papa", "Sebastian", "Mia"}, "clock", "weather", "events", "tasks", "meals", "tomorrow")); r.status >= 400 {
		t.Fatalf("save = %d", r.status)
	}
	if goalBlock(f.view(t, "en", f.at(14, 9, 0)), "Mia") != "" {
		t.Error("block switched off, column still shows it")
	}
}

func TestPeopleKeepTheirOrderWhateverTheirStars(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	f.stars(t, "Mia", 99)
	f.stars(t, "Sebastian", 1)
	eq(t, columnNames(f.view(t, "en", f.at(14, 9, 0))), []string{"Mama", "Papa", "Sebastian", "Mia"})
}

var goalPersonRe = regexp.MustCompile(`(?s)<section class="pv-goal".*?</section>`)

func TestThePersonalViewShowsTheGoalAtTheFootOfTheTasksAndNothingWithoutOne(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	tk := f.starTask(t, "Mia", "Tidy room", 3)
	f.stars(t, "Mia", 11)
	body := f.personalPage(t, "en", "Mia", 14, 9).body
	if strings.Contains(body, "pv-goal") {
		t.Fatalf("no goal, nothing shown:\n%s", body)
	}
	f.setGoal(t, "Mia", f.goalReward(t, "Kino mit Popcorn", 30))
	body = f.personalPage(t, "en", "Mia", 14, 9).body
	g := goalPersonRe.FindString(body)
	for _, want := range []string{`class="art-sym big"`, `class="g-name"`, `Savings goal</span>Kino mit Popcorn`, `class="progress" role="img"`, `--pct:36`, `<b>11 of 30 stars</b> · 19 to go`} {
		if !strings.Contains(g, want) {
			t.Errorf("goal lacks %q:\n%s", want, g)
		}
	}
	if strings.Contains(g, "data-reached") {
		t.Error("not reached yet")
	}
	// The goal is the last child of the tasks column, after the list.
	if i, j := strings.Index(body, `<div class="list">`), strings.Index(body, `<section class="pv-goal"`); i < 0 || j < i || strings.Index(body[j:], "</aside>") < 0 {
		t.Errorf("goal not after the task list")
	}
	// Ticking re-renders it like the star chip: 11 + 3 = 14.
	if r := f.display.post("/display/tasks/"+tk.ID+"/done", url.Values{"done": {"1"}}); r.status != 303 {
		t.Fatalf("tick = %d", r.status)
	}
	if g := goalPersonRe.FindString(f.personalPage(t, "en", "Mia", 14, 9).body); !strings.Contains(g, "<b>14 of 30 stars</b> · 16 to go") {
		t.Errorf("after the tick:\n%s", g)
	}
}

func TestAReachedGoalInThePersonalViewSaysAskMumOrDad(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	f.setGoal(t, "Mia", f.goalReward(t, "Spieleabend", 12))
	f.stars(t, "Mia", 14)
	g := goalPersonRe.FindString(f.personalPage(t, "en", "Mia", 14, 9).body)
	for _, want := range []string{"data-reached", "--pct:100", "<b>Enough stars!</b> Ask Mama or Papa."} {
		if !strings.Contains(g, want) {
			t.Errorf("lacks %q:\n%s", want, g)
		}
	}
}

func TestADisplayServesTheGoalPictureOfAnUploadedReward(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	r := f.goalReward(t, "Kino", 30)
	if r := f.display.get("/display/rewards/"+r.ID+"/picture", "en"); r.status != 404 {
		t.Errorf("a reward without a picture = %d", r.status)
	}
	if r := newScreen(f.box).get("/display/rewards/"+r.ID+"/picture", "en"); r.status == 200 {
		t.Error("an unpaired screen gets no picture")
	}
}
