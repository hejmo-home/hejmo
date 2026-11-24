package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/badge"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/task"
)

// insertBadge stores a badge dated at, as if it had been earned then: a second
// connection to the file, because the store only awards what the history earns.
func insertBadge(t *testing.T, b *box, household, person string, k badge.Key, at time.Time) {
	t.Helper()
	if _, err := b.db.SQL().Exec(`INSERT OR IGNORE INTO badges (household_id, person_id, key, earned_at, quiet) VALUES (?, ?, ?, ?, 0)`,
		household, person, string(k), at.UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
}

// Ticket 295: badges. Mia's morning routine (see newWeekBox) has two steps due
// Monday to Friday; each is worth a star. Wednesday 11 November 2026 is "today".

func (w *weekBox) bookStars(t *testing.T, who string, n int, at time.Time) {
	t.Helper()
	if _, err := w.db.BookStars(w.householdID, w.people[who].ID, n, "bonus", w.people["Mama"].ID, at); err != nil {
		t.Fatal(err)
	}
}

func (w *weekBox) badgeKeys(t *testing.T, who string) []string {
	t.Helper()
	list, err := w.db.Badges(w.householdID, w.people[who].ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range list {
		out = append(out, string(b.Key))
	}
	return out
}

func badgeSection(t *testing.T, page, open string) string {
	t.Helper()
	i := strings.Index(page, open)
	if i < 0 {
		t.Fatalf("no %q on the page:\n%s", open, page)
	}
	j := strings.Index(page[i:], "</section>")
	if j < 0 {
		t.Fatalf("%q is not closed:\n%s", open, page[i:])
	}
	return page[i : i+j]
}

func TestThePersonalViewHasTheBadgesAtTheFootNewOnesFirst(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(9, 8, 0)
	w.tick(t, "2026-11-09", true) // first routine, first star: Monday
	w.now = w.at(11, 7, 0)
	w.bookStars(t, "Mia", 100, w.now) // 100 stars collected: today
	page := w.personalPage(t, "de", "Mia", 11, 8).body
	foot := badgeSection(t, page, `<section class="pv-badges"`)
	for _, want := range []string{`aria-label="Abzeichen"`, `<div class="bdg-new">`, "Neues Abzeichen!", "100 Sterne gesammelt", `<ul class="bdg-chips">`, "Erster Stern", "Erste Routine geschafft", `<svg class="seal"`} {
		if !strings.Contains(foot, want) {
			t.Errorf("the badges block lacks %q\n%s", want, foot)
		}
	}
	// New ones in the band, the others as chips, nobody twice.
	band := foot[strings.Index(foot, `<div class="bdg-new">`):strings.Index(foot, `<ul class="bdg-chips">`)]
	if !strings.Contains(band, "100 Sterne gesammelt") || strings.Contains(band, "Erster Stern") {
		t.Errorf("the band holds the wrong badges:\n%s", band)
	}
	if !strings.Contains(page, `<div class="days">`) || strings.Index(page, `<div class="days">`) > strings.Index(page, `class="pv-badges"`) {
		t.Error("the days are not in div.days before the badges")
	}
	en := w.personalPage(t, "en", "Mia", 11, 8).body
	if !strings.Contains(en, "New badge!") || !strings.Contains(en, "100 stars collected") || !strings.Contains(en, `aria-label="Badges"`) {
		t.Errorf("English block:\n%s", badgeSection(t, en, `<section class="pv-badges"`))
	}
}

func TestSeveralNewBadgesAreAllNamedInTheBand(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(11, 7, 0)
	w.bookStars(t, "Mia", 600, w.now) // first star, 100 and 500 at once
	foot := badgeSection(t, w.personalPage(t, "de", "Mia", 11, 8).body, `<section class="pv-badges"`)
	if !strings.Contains(foot, "Neue Abzeichen!") {
		t.Errorf("several new badges need the plural heading\n%s", foot)
	}
	band := foot[strings.Index(foot, `<div class="bdg-new">`):]
	for _, name := range []string{"Erster Stern", "100 Sterne gesammelt", "500 Sterne gesammelt"} {
		if !strings.Contains(band, name) {
			t.Errorf("the band lacks %q\n%s", name, band)
		}
	}
	if strings.Contains(foot, "+") {
		t.Error("the block counts with a plus instead of naming the badges")
	}
}

func TestABadgeIsNewTodayAndYesterdayOnly(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(9, 8, 0)
	w.bookStars(t, "Mia", 1, w.now)                                                             // first star on Monday
	if !strings.Contains(w.personalPage(t, "en", "Mia", 10, 8).body, `<div class="bdg-new">`) { // Tuesday: yesterday
		t.Error("a badge of yesterday is not new")
	}
	body := w.personalPage(t, "en", "Mia", 11, 8).body // Wednesday: two days ago
	if strings.Contains(body, `<div class="bdg-new">`) || !strings.Contains(body, `<ul class="bdg-chips">`) {
		t.Errorf("a badge of two days ago is still new or gone\n%s", badgeSection(t, body, `<section class="pv-badges"`))
	}
}

func TestThePersonalViewStaysAsItIsWithoutBadgesOrWithTheSwitchOff(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	if strings.Contains(w.personalPage(t, "en", "Mia", 11, 8).body, "pv-badges") {
		t.Error("an empty badges box")
	}
	mia := w.people["Mia"]
	if err := w.db.SetStreaksShown(w.householdID, mia.ID, false); err != nil {
		t.Fatal(err)
	}
	w.now = w.at(11, 7, 0)
	w.bookStars(t, "Mia", 100, w.now)
	if keys := w.badgeKeys(t, "Mia"); len(keys) == 0 {
		t.Fatal("no badge was awarded in the background while the switch is off")
	}
	if body := w.personalPage(t, "en", "Mia", 11, 8).body; strings.Contains(body, "pv-badges") || strings.Contains(body, "100 stars collected") {
		t.Error("badges show while the switch is off")
	}
	// Switched on again: shown, but nothing is celebrated as new.
	if err := w.db.SetStreaksShown(w.householdID, mia.ID, true); err != nil {
		t.Fatal(err)
	}
	body := w.personalPage(t, "en", "Mia", 11, 8).body
	if !strings.Contains(body, "100 stars collected") || strings.Contains(body, `<div class="bdg-new">`) {
		t.Errorf("after switching on:\n%s", body)
	}
}

func TestTheFamilyViewShowsNoBadges(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(11, 7, 0)
	w.bookStars(t, "Mia", 600, w.now)
	body := w.view(t, "en", w.at(11, 8, 0))
	for _, not := range []string{"pv-badges", "bdg-", "badge", "stars collected"} {
		if strings.Contains(body, not) {
			t.Errorf("the family view shows %q", not)
		}
	}
}

func TestAChildSeesTheWayToTheBadgesAndWhichAreNew(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	kid := pairedPersonalDevice(t, w.box, w.people["Mia"], "Mias Handy")
	w.now = w.at(11, 7, 0)
	w.bookStars(t, "Mia", 100, w.now)
	page := kid.get("/tasks", "en").body
	for _, want := range []string{`<a class="reward-link badge-link" href="/tasks/badges">`, `<b>Badges</b>`, `New: <b class="g-inline">First star</b> and <b class="g-inline">100 stars collected</b>`} {
		if !strings.Contains(page, want) {
			t.Errorf("tasks page lacks %q", want)
		}
	}
	w.now = w.at(13, 7, 0) // two days later: no longer new, the newest is named
	page = kid.get("/tasks", "en").body
	if strings.Contains(page, "New: ") || !strings.Contains(page, `Latest: <b class="g-inline">First star</b>`) {
		t.Errorf("after the days are over:\n%s", page)
	}
	w.now = w.at(14, 7, 0) // Saturday, nothing due: the way to the badges stays
	if page = kid.get("/tasks", "en").body; !strings.Contains(page, `href="/tasks/badges"`) {
		t.Error("no way to the badges on a day without tasks")
	}
	if err := w.db.SetStreaksShown(w.householdID, w.people["Mia"].ID, false); err != nil {
		t.Fatal(err)
	}
	if page := kid.get("/tasks", "en").body; strings.Contains(page, "badge-link") {
		t.Error("the link to the badges shows while the switch is off")
	}
}

func TestTheTickThatEarnsABadgeSaysSoWithItsSeal(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	kid := pairedPersonalDevice(t, w.box, w.people["Mia"], "Mias Handy")
	w.now = w.at(11, 7, 0)
	r := kid.post("/tasks/"+w.steps[0].ID+"/done", url.Values{"done": {"1"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("tick = %d", r.status)
	}
	page := kid.get(r.location, "en").body
	for _, want := range []string{`class="msg ok bdg-won-msg"`, `<b>New badge: First star!</b> It stays with you forever.`, `<svg class="seal" width="40" height="40"`} {
		if !strings.Contains(page, want) {
			t.Errorf("tasks page lacks %q\n%s", want, page)
		}
	}
	if again := kid.get("/tasks", "en").body; strings.Contains(again, "bdg-won-msg") {
		t.Error("the line stays after being shown")
	}
	// The second step completes the routine: the next badge. A tick that earns nothing gets the usual line.
	r = kid.post("/tasks/"+w.steps[1].ID+"/done", url.Values{"done": {"1"}})
	if page := kid.get(r.location, "en").body; !strings.Contains(page, "<b>New badge: First routine done!</b>") {
		t.Errorf("the tick that completes the routine says nothing\n%s", page)
	}
	r = kid.post("/tasks/"+w.steps[1].ID+"/done", url.Values{"done": {"0"}})
	r = kid.post("/tasks/"+w.steps[1].ID+"/done", url.Values{"done": {"1"}})
	if page := kid.get(r.location, "en").body; strings.Contains(page, "bdg-won-msg") {
		t.Errorf("the same badge is celebrated twice\n%s", page)
	}
}

func TestATickWhileTheSwitchIsOffCelebratesNothing(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	if err := w.db.SetStreaksShown(w.householdID, w.people["Mia"].ID, false); err != nil {
		t.Fatal(err)
	}
	kid := pairedPersonalDevice(t, w.box, w.people["Mia"], "Mias Handy")
	w.now = w.at(11, 7, 0)
	r := kid.post("/tasks/"+w.steps[0].ID+"/done", url.Values{"done": {"1"}})
	page := kid.get(r.location, "en").body
	if strings.Contains(page, "bdg-won-msg") || strings.Contains(page, "New badge") {
		t.Errorf("a badge is celebrated while the switch is off\n%s", page)
	}
	if len(w.badgeKeys(t, "Mia")) == 0 {
		t.Error("nothing was awarded in the background")
	}
}

func TestEveryWayToTickAwardsTheBadgeOnce(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(11, 7, 0)
	// A display tick.
	if r := w.display.post("/display/tasks/"+w.steps[0].ID+"/done", url.Values{"done": {"1"}, "day": {"2026-11-11"}}); r.status != http.StatusSeeOther {
		t.Fatalf("display tick = %d", r.status)
	}
	if keys := w.badgeKeys(t, "Mia"); len(keys) != 1 || keys[0] != "first_star" {
		t.Fatalf("after the display tick: %v", keys)
	}
	// The REST API (a parent), twice.
	for range 2 {
		if r := w.do(http.MethodPut, "/api/tasks/"+w.steps[1].ID+"/done", nil, map[string]string{"Content-Type": "application/json"}); r.status >= 500 {
			t.Fatalf("api tick = %d", r.status)
		}
	}
}

func TestTheBadgesPageOfAChild(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	kid := pairedPersonalDevice(t, w.box, w.people["Mia"], "Mias Handy")
	w.now = w.at(9, 8, 0)
	w.bookStars(t, "Mia", 1, w.now) // first star: Monday
	w.now = w.at(11, 7, 0)
	w.bookStars(t, "Mia", 99, w.now) // 100 stars: today
	r := kid.get("/tasks/badges", "en")
	if r.status != http.StatusOK {
		t.Fatalf("GET /tasks/badges = %d", r.status)
	}
	page := r.body
	for _, want := range []string{`class="back" href="/tasks"`, "All tasks", "<h1>Badges</h1>", `<div class="bdg-page" data-person="`,
		"You get each badge once.", "Your badges", `<li class="bdg-card">`, "100 stars collected", `<span class="bdg-pill">New</span>`, "Got it today",
		"Got it Monday 09.11.", "First star", "You can still get these", "No hurry: nothing is lost if a day does not work out.", `<li class="bdg-card todo">`, `<svg class="seal todo"`, "A whole week done"} {
		if !strings.Contains(page, want) {
			t.Errorf("badges page lacks %q\n%s", want, page)
		}
	}
	// What cannot be reached is not offered: Mia has a routine and star tasks but no reward.
	if strings.Contains(page, "First reward") || strings.Contains(page, "Savings goal reached") {
		t.Error("a reward badge is offered although no reward is meant for Mia")
	}
	// Earned badges are not listed as open, and there is no button and no progress.
	open := page[strings.Index(page, `class="bdg-group more"`):]
	if strings.Contains(open, "100 stars collected") || strings.Contains(open, "<button") || strings.Contains(page, `class="progress"`) {
		t.Errorf("the open group lists earned badges or has a button/progress\n%s", open)
	}
	de := kid.get("/tasks/badges", "de").body
	for _, want := range []string{"Deine Abzeichen", "Diese kannst du noch bekommen", "Bekommen heute", "Bekommen Montag 09.11.", "Ohne Eile:", ">Neu</span>"} {
		if !strings.Contains(de, want) {
			t.Errorf("German page lacks %q", want)
		}
	}
}

func TestTheBadgesPageSaysHowTheFirstOneComesAndOffersWhatIsReachable(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_color")
	kid := pairedPersonalDevice(t, f.box, f.people["Sebastian"], "Sebastians Handy")
	page := kid.get("/tasks/badges", "en").body
	if !strings.Contains(page, `<p class="bdg-empty">`) || !strings.Contains(page, "You have no badge yet. Once there are routines") {
		t.Errorf("empty state without anything reachable:\n%s", page)
	}
	if strings.Contains(page, `bdg-group more`) {
		t.Error("an open group without reachable badges")
	}
	if _, err := f.db.AddTask(f.householdID, taskWithStars(f.people["Sebastian"].ID)); err != nil {
		t.Fatal(err)
	}
	page = kid.get("/tasks/badges", "en").body
	for _, want := range []string{"as soon as you have earned your first star", "First star", "100 stars collected", "500 stars collected"} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, not := range []string{"First routine done", "First reward"} {
		if strings.Contains(page, not) {
			t.Errorf("page offers %q without anything to reach it", not)
		}
	}
}

func TestAChildWithAllTenSeesASuccessLineInsteadOfTheOpenGroup(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	kid := pairedPersonalDevice(t, w.box, w.people["Mia"], "Mias Handy")
	w.now = w.at(11, 7, 0)
	// Everything earned by history that exists directly in the store: fake it with the store's own rules.
	for _, k := range badge.Catalogue {
		insertBadge(t, w.box, w.householdID, w.people["Mia"].ID, k, w.now.AddDate(0, 0, -20))
	}
	page := kid.get("/tasks/badges", "en").body
	if !strings.Contains(page, `class="msg ok bdg-all"`) || !strings.Contains(page, "You have every badge there is. Well done!") || strings.Contains(page, `bdg-group more`) {
		t.Errorf("all ten:\n%s", page)
	}
	if strings.Count(page, `<li class="bdg-card">`) != 10 {
		t.Errorf("want ten cards, got %d", strings.Count(page, `<li class="bdg-card">`))
	}
}

func TestTheBadgesPageIsAQuietNoticeWhileTheSwitchIsOff(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.bookStars(t, "Mia", 100, w.at(11, 7, 0))
	if err := w.db.SetStreaksShown(w.householdID, w.people["Mia"].ID, false); err != nil {
		t.Fatal(err)
	}
	kid := pairedPersonalDevice(t, w.box, w.people["Mia"], "Mias Handy")
	r := kid.get("/tasks/badges", "en")
	if r.status != http.StatusOK || !strings.Contains(r.body, `<p class="notice top">Badges are switched off for you at the moment. Mama or Papa can switch them back on.`) {
		t.Fatalf("child, switch off: %d\n%s", r.status, r.body)
	}
	if strings.Contains(r.body, "bdg-card") || strings.Contains(r.body, "100 stars collected") {
		t.Error("badges shown while the switch is off")
	}
	p := w.page("en", "/tasks/badges/"+w.people["Mia"].ID)
	if p.status != http.StatusOK || !strings.Contains(p.body, `class="notice top"`) || !strings.Contains(p.body, `href="/people/`+w.people["Mia"].ID+`"`) || strings.Contains(p.body, "bdg-card") {
		t.Errorf("parent, switch off: %d\n%s", p.status, p.body)
	}
}

func TestParentsSeeThePageOfOnePersonAndOnlyThat(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(11, 7, 0)
	w.bookStars(t, "Mia", 1, w.now)
	p := w.page("en", "/tasks/badges/"+w.people["Mia"].ID)
	for _, want := range []string{`<span class="marker" data-person=`, "Mia&#39;s badges", "Already earned", "Not earned yet", `href="/people/`, "Mia earns each badge once", "Only what Mia can reach"} {
		if !strings.Contains(p.body, want) {
			t.Errorf("parents' page lacks %q\n%s", want, p.body)
		}
	}
	if strings.Contains(p.body, "<form") && strings.Contains(p.body, "badge") && strings.Contains(p.body, `name="key"`) {
		t.Error("parents can award badges by hand")
	}
	if r := w.page("en", "/tasks/badges/nobody"); r.status != http.StatusNotFound {
		t.Errorf("unknown person = %d", r.status)
	}
	kid := pairedPersonalDevice(t, w.box, w.people["Mia"], "Mias Handy")
	if r := kid.get("/tasks/badges/"+w.people["Sebastian"].ID, "en"); r.status != http.StatusForbidden {
		t.Errorf("a child opens another person's badges: %d", r.status)
	}
	if r := w.page("en", "/tasks/badges"); r.status != http.StatusNotFound {
		t.Errorf("a parent has no badges of their own: %d", r.status)
	}
}

func TestParentsTasksPageHasOneBadgeLinePerPersonWhoseSwitchIsOn(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	w.now = w.at(9, 8, 0)
	w.bookStars(t, "Mia", 1, w.now)
	w.now = w.at(11, 7, 0)
	w.bookStars(t, "Mia", 99, w.now)
	page := w.page("en", "/tasks").body
	mia := badgeSection(t, page, `<section aria-label="Mia"`)
	for _, want := range []string{`class="bdg-line"`, "Badges:", `<span class="nm"><b>100 stars collected</b> <span class="bdg-pill">new</span> ·</span>`, `<span class="nm"><b>First star</b></span>`, `href="/tasks/badges/` + w.people["Mia"].ID + `"`, "View Mia&#39;s badges"} {
		if !strings.Contains(mia, want) {
			t.Errorf("Mia's group lacks %q\n%s", want, mia)
		}
	}
	if strings.Index(mia, "100 stars collected") > strings.Index(mia, "First star") {
		t.Error("new badges do not come first")
	}
	if strings.Contains(mia, "+") && strings.Contains(mia, "bdg-line") && strings.Contains(mia[strings.Index(mia, "bdg-line"):], "+1") {
		t.Error("the line counts instead of naming")
	}
	// Without badges: "none yet". A person whose switch is off has no line at all.
	if err := w.db.SetStreaksShown(w.householdID, w.people["Mia"].ID, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(badgeSection(t, w.page("en", "/tasks").body, `<section aria-label="Mia"`), "bdg-line") {
		t.Error("a badge line for a person whose switch is off")
	}
	if err := w.db.SetStreaksShown(w.householdID, w.people["Mia"].ID, true); err != nil {
		t.Fatal(err)
	}
}

func TestTheBadgesAPIIsReadOnlyAndHonoursTheSwitch(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	rb.book(t, rb.miaID, 100)
	path := "/people/" + rb.miaID + "/badges"
	status, got, _ := rb.parent.call(http.MethodGet, path, nil)
	if status != http.StatusOK || got["person_id"] != rb.miaID || got["streaks_shown"] != true {
		t.Fatalf("GET badges = %d %v", status, got)
	}
	list, _ := got["badges"].([]any)
	if len(list) != 2 {
		t.Fatalf("badges = %v", got["badges"])
	}
	first, second := list[0].(map[string]any), list[1].(map[string]any)
	if first["key"] != "first_star" || second["key"] != "stars_100" || second["name"] == "" || second["how"] == "" || first["new"] != true ||
		!strings.HasPrefix(first["earned_at"].(string), "2026-11-14T") {
		t.Fatalf("badge fields = %v", list)
	}
	// Everyone may read it, a child included.
	if status, _, _ := rb.sebastian.call(http.MethodGet, path, nil); status != http.StatusOK {
		t.Errorf("a child reads badges = %d", status)
	}
	if status, _, _ := rb.parent.call(http.MethodGet, "/people/00000000-0000-4000-8000-000000000000/badges", nil); status != http.StatusNotFound {
		t.Errorf("unknown person = %d", status)
	}
	// No way to write.
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(m, rb.parent.b.srv.URL+"/api/v1"+path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+rb.parent.token)
		res, err := rb.parent.b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed && res.StatusCode != http.StatusNotFound {
			t.Errorf("%s badges = %d, want no write endpoint", m, res.StatusCode)
		}
	}
	// Switch off: hidden, but still stored.
	if status, _, _ := rb.parent.call(http.MethodPatch, "/people/"+rb.miaID, map[string]any{"streaks_shown": false}); status != http.StatusOK {
		t.Fatalf("switch off = %d", status)
	}
	status, got, _ = rb.parent.call(http.MethodGet, path, nil)
	if list, _ := got["badges"].([]any); status != http.StatusOK || got["streaks_shown"] != false || list == nil || len(list) != 0 {
		t.Fatalf("switch off: %d %v", status, got)
	}
	h, err := rb.parent.b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	if stored, _ := rb.parent.b.db.Badges(h.ID, rb.miaID); len(stored) != 2 {
		t.Errorf("badges not stored while hidden: %v", stored)
	}
}

func TestThePersonPageSaysBadgesStayStoredWhileTheSwitchIsOff(t *testing.T) {
	t.Parallel()
	w := newWeekBox(t)
	page := w.do(http.MethodGet, "/people/"+w.people["Mia"].ID, nil, map[string]string{"Accept-Language": "en"}).body
	if !strings.Contains(page, "Stars stay visible, and Mia&#39;s badges stay stored.") {
		t.Errorf("English off text:\n%s", page)
	}
	de := w.do(http.MethodGet, "/people/"+w.people["Mia"].ID, nil, map[string]string{"Accept-Language": "de"}).body
	if !strings.Contains(de, "Sterne bleiben sichtbar, und Mias Abzeichen bleiben gespeichert.") {
		t.Errorf("German off text:\n%s", de)
	}
}

func taskWithStars(person string) task.Task {
	return task.Task{PersonID: person, Title: "Tidy room", Repeat: task.Daily, Date: "2026-11-01", Stars: 2}
}

func TestMCPReadsBadgesUnderTheTasksAreaAndCannotWriteThem(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	c := rb.parent
	c.switchMCP(t, true)
	s := c.mcpSession(t, c.newMCPToken(t, "Claude", allAreas))
	var names []string
	for _, tool := range server.MCPTools() {
		if strings.Contains(tool.Name, "badge") {
			names = append(names, tool.Name)
			if tool.Permission.Resource != "tasks" || tool.Method != http.MethodGet {
				t.Errorf("%s: %s %s, want a read in the tasks area", tool.Name, tool.Method, tool.Permission)
			}
		}
	}
	if len(names) != 1 || names[0] != "get_badges" {
		t.Fatalf("badge tools = %v, want only get_badges", names)
	}
	rb.book(t, rb.miaID, 100)
	if out, failed := callTool(s, "get_badges", map[string]any{"id": rb.miaID}); failed || !strings.Contains(out, "stars_100") || !strings.Contains(out, "first_star") {
		t.Errorf("get_badges = %v %s", failed, out)
	}
}
