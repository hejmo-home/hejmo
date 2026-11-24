package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 293: the savings goal on the child's reward page, the tasks pages and
// the parents' goal page (preview-293).

type goalWeb struct {
	rewardWeb
	kino, eis string
}

func newGoalWeb(t *testing.T) goalWeb {
	t.Helper()
	rw := newRewardWeb(t)
	g := goalWeb{rewardWeb: rw}
	g.kino = rw.create(t, "Kino mit Popcorn", "30", url.Values{"limit_period": {"m"}, "limit_count": {"1"}})
	g.eis = rw.create(t, "Eis aus der Eisdiele", "8", url.Values{"limit_period": {"w"}, "limit_count": {"1"}})
	rw.create(t, "Länger aufbleiben", "15", nil)
	rw.stars(t, rw.mia, 12)
	return g
}

func (g goalWeb) choose(t *testing.T, id string) resp {
	t.Helper()
	r := g.miaScreen.post("/tasks/rewards/"+id+"/goal", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/tasks/rewards" {
		t.Fatalf("choose = %d %q\n%s", r.status, r.location, r.body)
	}
	return g.miaScreen.get("/tasks/rewards", "de")
}

func count(s, sub string) int { return strings.Count(s, sub) }

func TestAChildChoosesASavingsGoalAndSeesItAboveTheGroups(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	before := g.miaScreen.get("/tasks/rewards", "de").body
	hasAll(t, "no goal yet", before, `class="hint g-pick"`, "Tippe bei einer Belohnung auf „Darauf sparen“.", `<form class="rwd-act" method="post" action="/tasks/rewards/`+g.kino+`/goal">`, "Darauf sparen")
	hasNone(t, "no goal yet", before, `class="rwd-goal"`, "Dein Sparziel")

	page := g.choose(t, g.kino).body
	hasAll(t, "goal chosen", page, `<p class="msg ok" role="status" data-success>`, "Dein Sparziel ist jetzt <b>Kino mit Popcorn</b>. Es steht auch auf dem Bildschirm zu Hause.",
		`<section class="rwd-goal"`, "Dein Sparziel", `class="rwd goal"`, `aria-label="12 von 30 Sternen"`, `style="--pct:40"`, "<b>12 von 30 Sternen</b> · noch 18", `class="g-acts"`,
		`action="/tasks/rewards/goal/clear"`, "Kein Sparziel mehr", "Es steht auch auf dem Bildschirm zu Hause, in deiner Spalte.", "einmal pro Monat")
	hasNone(t, "goal chosen", page, `class="hint g-pick"`)
	if n := count(page, "<h3>Kino mit Popcorn</h3>"); n != 1 {
		t.Errorf("the goal card is repeated in its group (%d)", n)
	}
	if i, j, k := strings.Index(page, `class="rwd-bal"`), strings.Index(page, `class="rwd-goal"`), strings.Index(page, `class="rwd-group`); !(i < j && j < k) {
		t.Errorf("the goal sits right after the balance and before the groups: %d %d %d", i, j, k)
	}
	if i, j := strings.Index(page, "data-success"), strings.Index(page, "<h1>"); !(i > 0 && i < j) {
		t.Errorf("the success line comes before the heading")
	}
	// The other cards offer the button; the goal itself does not.
	if n := count(page, `class="rwd-act"`); n != 1 {
		t.Errorf("save buttons = %d, want 1 (Eis is affordable and needs none, Kino is the goal)", n)
	}
	if got := g.miaScreen.get("/tasks/rewards", "en").body; !strings.Contains(got, "Your savings goal") || !strings.Contains(got, "<b>12 of 30 stars</b> · 18 to go") || !strings.Contains(got, "No savings goal") {
		t.Errorf("English:\n%s", got)
	}
}

func TestSwitchingAndClearingTheGoalNameWhatChangedAndKeepTheStars(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	g.choose(t, g.kino)
	other := g.rewardWeb.create(t, "Ausflug", "60", nil)
	page := g.choose(t, other).body
	hasAll(t, "switched", page, "Dein Sparziel ist jetzt <b>Ausflug</b>.", "Für „Kino mit Popcorn“ sparst du nicht mehr. Deine Sterne bleiben alle.")
	if n := count(page, "<h3>Kino mit Popcorn</h3>"); n != 1 {
		t.Errorf("the old goal is back in its group (%d)", n)
	}
	r := g.miaScreen.post("/tasks/rewards/goal/clear", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/tasks/rewards" {
		t.Fatalf("clear = %d %q", r.status, r.location)
	}
	page = g.miaScreen.get("/tasks/rewards", "de").body
	hasAll(t, "cleared", page, "Du hast kein Sparziel mehr. Deine Sterne bleiben alle.", `class="hint g-pick"`)
	hasNone(t, "cleared", page, `class="rwd-goal"`)
	if st, _ := g.stars(); st != 12 {
		t.Errorf("stars = %d", st)
	}
}

func (g goalWeb) stars() (int, error) {
	h, _ := g.b.db.HomeHousehold()
	acc, err := g.b.db.StarAccount(h.ID, g.mia, 1)
	return acc.Balance, err
}

func TestAChildCannotChooseARewardNotMeantForThemOrChangeAnotherChildsGoal(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	forSeb := g.rewardWeb.create(t, "Nur Sebastian", "6", url.Values{"whole_family": {""}, "person": {g.sebastian}})
	if r := g.miaScreen.post("/tasks/rewards/"+forSeb+"/goal", url.Values{}); r.status != http.StatusNotFound {
		t.Errorf("a reward not meant for Mia = %d, want 404", r.status)
	}
	if r := g.miaScreen.post("/tasks/rewards/goal/"+g.sebastian, url.Values{"reward": {g.kino}}); r.status != http.StatusForbidden {
		t.Errorf("a child at the parents' goal page = %d, want 403", r.status)
	}
	if r := g.miaScreen.get("/tasks/rewards/goal/"+g.mia, "de"); r.status != http.StatusForbidden {
		t.Errorf("a child opening the parents' goal page = %d, want 403", r.status)
	}
	h, _ := g.b.db.HomeHousehold()
	if got, _ := g.b.db.Goal(h.ID, g.mia); got.Active() {
		t.Errorf("a goal was set: %+v", got)
	}
}

func TestAnAffordableGoalSaysEnoughStarsAndALimitSaysWhenItIsBackWhileSavingGoesOn(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	g.rewardWeb.stars(t, g.mia, 20) // 32
	page := g.choose(t, g.kino).body
	hasAll(t, "affordable goal", page, `class="rwd goal ok"`, "<b>Genug Sterne</b>")
	hasNone(t, "affordable goal", page, "· noch")
	// A used-up limit: the line says when it is back and that saving goes on.
	g.redeem(t, g.kino, g.mia, "confirmed", g.b.now.Add(-time.Hour))
	h, _ := g.b.db.HomeHousehold()
	if err := g.b.db.SetGoal(h.ID, g.mia, g.kino); err != nil { // redeeming ended it; choose it again
		t.Fatal(err)
	}
	g.rewardWeb.stars(t, g.mia, -30) // 2
	page = g.miaScreen.get("/tasks/rewards", "de").body
	hasAll(t, "limited goal", page, `class="rwd-state later"`, "<b>Diesen Monat schon eingelöst.</b> Wieder ab ", "Du kannst trotzdem weiter sparen.", "<b>2 von 30 Sternen</b> · noch 28")
	// Enough stars but the limit is used up: the card says so and offers no
	// request (POST /ask would answer 409).
	g.rewardWeb.stars(t, g.mia, 78) // 80
	page = g.miaScreen.get("/tasks/rewards", "de").body
	card := goalCard(t, page)
	hasAll(t, "limited goal with enough stars", card, "<b>Genug Sterne</b>", "<b>Diesen Monat schon eingelöst.</b>")
	hasNone(t, "limited goal with enough stars", card, `class="rwd-ask"`, "Eltern fragen")
}

func TestARedemptionAnArchivedRewardAndAChangedAudienceEndTheGoalWithAKindLine(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	g.choose(t, g.kino)
	g.rewardWeb.stars(t, g.mia, 30)
	g.redeem(t, g.kino, g.mia, "confirmed", g.b.now)
	page := g.miaScreen.get("/tasks/rewards", "de").body
	hasAll(t, "redeemed", page, `<p class="msg ok" role="status" data-success>`, "<b>Geschafft!</b>", "„Kino mit Popcorn“", "eingelöst", "Such dir ein neues Sparziel aus, wenn du magst.")
	hasNone(t, "redeemed", page, `class="rwd-goal"`)
	if i, j := strings.Index(page, "data-success"), strings.Index(page, "<h1>"); !(i > 0 && i < j) {
		t.Errorf("the line comes before the heading")
	}

	// Choosing a new goal clears the line.
	if page = g.choose(t, g.eis).body; strings.Contains(page, "Geschafft!") {
		t.Error("the redeemed line stays after a new goal")
	}

	// Archived: a calm notice.
	if r := g.b.post("/tasks/rewards/"+g.eis+"/archive", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("archive = %d", r.status)
	}
	page = g.miaScreen.get("/tasks/rewards", "de").body
	hasAll(t, "archived", page, `<div class="notice g-ended" role="status">`, "<b>„Eis aus der Eisdiele“ gibt es gerade nicht mehr.</b> Deine Sterne bleiben alle. Such dir ein neues Sparziel aus, wenn du magst.")
	hasNone(t, "archived", page, `class="rwd-goal"`, "Geschafft!")

	// The line lasts 7 days.
	if _, err := g.b.db.SQL().Exec(`UPDATE savings_goals SET ended_at = ?`, "2020-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if page = g.miaScreen.get("/tasks/rewards", "de").body; strings.Contains(page, "g-ended") {
		t.Error("the notice stays after 7 days")
	}

	// Audience change: no longer meant for Mia.
	g.choose(t, g.kino)
	if r := g.b.post("/tasks/rewards/"+g.kino, g.form("Kino mit Popcorn", "30", url.Values{"whole_family": {""}, "person": {g.sebastian}})); r.status != http.StatusSeeOther {
		t.Fatalf("change audience = %d\n%s", r.status, r.body)
	}
	hasAll(t, "audience", g.miaScreen.get("/tasks/rewards", "de").body, `class="notice g-ended"`, "<b>„Kino mit Popcorn“ gibt es gerade nicht mehr.</b>")
}

func TestTheChildsTasksPageNamesTheGoalInWords(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	g.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": g.mia, "title": "Brush teeth", "repeat": "daily"})
	if got := g.miaScreen.get("/tasks", "de").body; !strings.Contains(got, "Du hast 12 Sterne. Schau, wofür sie reichen.") {
		t.Fatalf("without a goal the line is unchanged:\n%s", got)
	}
	g.choose(t, g.kino)
	if got := g.miaScreen.get("/tasks", "de").body; !strings.Contains(got, `Du hast 12 Sterne. Sparziel: <b class="g-inline">Kino mit Popcorn</b>, noch 18 Sterne.`) {
		t.Errorf("goal line:\n%s", got)
	}
	g.rewardWeb.stars(t, g.mia, 20)
	if got := g.miaScreen.get("/tasks", "de").body; !strings.Contains(got, `Du hast 32 Sterne. Sparziel: <b class="g-inline">Kino mit Popcorn</b>, genug Sterne.`) {
		t.Errorf("reached:\n%s", got)
	}
}

func TestTheParentsStarCardsShowEachGoalAndTheWayToChangeIt(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	h, _ := g.b.db.HomeHousehold()
	if err := g.b.db.SetGoal(h.ID, g.sebastian, g.kino); err != nil {
		t.Fatal(err)
	}
	g.rewardWeb.stars(t, g.sebastian, 12)
	g.rewardWeb.create(t, "Nur Mia", "6", url.Values{"whole_family": {""}, "person": {g.mia}})
	page := g.b.get("/tasks").body
	hasAll(t, "star cards", page, `<ul class="cards stars">`, `class="st-head"`, `<span class="marker" data-person=`, `data-shape=`, `class="stars-balance"`,
		`class="goal-line"`, `<span class="k">Sparziel:</span>`, `class="art-sym sm"`, "<b>Kino mit Popcorn</b> · <b>12 von 30</b>, noch 18", `class="g-link" href="/tasks/rewards/goal/`+g.sebastian+`">Sparziel ändern</a>`,
		`class="goal-line none"`, "Noch kein Sparziel. In Mias Spalte stehen nur die Sterne.", `href="/tasks/rewards/goal/`+g.mia+`">Sparziel wählen</a>`,
		"Jede Person kann eine Belohnung als Sparziel haben; es steht in ihrer Spalte auf dem Bildschirm.")
	hasNone(t, "star cards", page, `class="swatch" type="color"`)
	if n := count(page, `class="goal-line`); n != 2 {
		t.Errorf("goal lines = %d, want one per card", n)
	}
}

func TestAPersonWithNoRewardMeantForThemGetsNoGoalLine(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	rw.create(t, "Nur Sebastian", "6", url.Values{"whole_family": {""}, "person": {rw.sebastian}})
	rw.stars(t, rw.sebastian, 3)
	rw.stars(t, rw.mia, 3)
	page := rw.b.get("/tasks").body
	if strings.Count(page, `class="goal-line`) != 1 || !strings.Contains(page, `href="/tasks/rewards/goal/`+rw.sebastian+`"`) || strings.Contains(page, `/tasks/rewards/goal/`+rw.mia) {
		t.Errorf("only Sebastian has a reward meant for him:\n%s", page)
	}
}

func TestAParentChoosesAGoalForSomeoneOnTheGoalPage(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	path := "/tasks/rewards/goal/" + g.mia
	page := g.b.get(path)
	if page.status != http.StatusOK {
		t.Fatalf("status = %d\n%s", page.status, page.body)
	}
	hasAll(t, "goal page", page.body, `class="crumbs"`, "Sparziel für Mia", "<h1>Sparziel für Mia</h1>", "Mia hat <b>12 Sterne</b>. Das Sparziel steht in Mias Spalte auf dem Bildschirm und in Mias Ansicht",
		"Mia kann es auf dem eigenen Gerät auch selbst ändern.", `<form class="steps goal-form" method="post" action="`+path+`">`, `<fieldset class="choice goals">`, "<legend>Worauf spart Mia?</legend>",
		`<label class="none"><input type="radio" name="reward" value="" checked>`, "<b>Kein Sparziel</b>", "In Mias Spalte stehen dann nur die Sterne.", `class="price"`, "genug Sterne", "noch 18 Sterne", "einmal pro Monat",
		`<button class="button" type="submit">`, "Sparziel speichern")
	// Sorted by price and then name, with "no goal" first.
	order := []string{"Kein Sparziel", "Eis aus der Eisdiele", "Länger aufbleiben", "Kino mit Popcorn"}
	last := -1
	for _, name := range order {
		i := strings.Index(page.body, "<b>"+name+"</b>")
		if i < last {
			t.Errorf("%q comes too early", name)
		}
		last = i
	}
	r := g.b.post(path, url.Values{"reward": {g.kino}})
	if r.status != http.StatusSeeOther || r.location != "/tasks#stars" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	hasAll(t, "after saving", g.b.get("/tasks").body, "Mias Sparziel ist jetzt <b>Kino mit Popcorn</b>.")
	page = g.b.get(path)
	if !strings.Contains(page.body, `value="`+g.kino+`" checked`) {
		t.Errorf("the current goal is not checked:\n%s", page.body)
	}
	if r := g.b.post(path, url.Values{"reward": {""}}); r.status != http.StatusSeeOther || r.location != "/tasks#stars" {
		t.Fatalf("clear = %d %q", r.status, r.location)
	}
	hasAll(t, "cleared", g.b.get("/tasks").body, "Mia hat kein Sparziel mehr.")
}

func TestTheGoalPageNamesAnArchivedRewardAtTheFieldAndLeavesItOut(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	path := "/tasks/rewards/goal/" + g.mia
	g.b.post("/tasks/rewards/"+g.eis+"/archive", url.Values{})
	r := g.b.post(path, url.Values{"reward": {g.eis}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d\n%s", r.status, r.body)
	}
	hasAll(t, "error", r.body, `<fieldset class="choice goals" aria-invalid="true"`, `class="ferr"`, "„Eis aus der Eisdiele“ wurde gerade archiviert und ist kein Sparziel mehr. Wähle ein anderes oder „Kein Sparziel“.")
	if strings.Contains(r.body, "<b>Eis aus der Eisdiele</b>") {
		t.Error("the archived reward must be left out of the list")
	}
	i := strings.Index(r.body, `<fieldset class="choice goals"`)
	if j := strings.Index(r.body[i:], `class="ferr"`); j < 0 || j > strings.Index(r.body[i:], "</fieldset>") {
		t.Error("the error belongs inside the fieldset")
	}
	h, _ := g.b.db.HomeHousehold()
	if got, _ := g.b.db.Goal(h.ID, g.mia); got.Active() {
		t.Errorf("nothing is saved on error: %+v", got)
	}
}

func TestTheGoalPageForSomeoneWithoutARewardOffersTheNewRewardButton(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	page := rw.b.get("/tasks/rewards/goal/" + rw.mia)
	hasAll(t, "empty", page.body, `<div class="empty" data-empty>`, "Für Mia gibt es noch keine Belohnung.", `href="/tasks/rewards/new"`, "Neue Belohnung")
	hasNone(t, "empty", page.body, `class="choice goals"`)
	if r := rw.b.get("/tasks/rewards/goal/nobody"); r.status != http.StatusNotFound {
		t.Errorf("unknown person = %d", r.status)
	}
}

func TestTheGoalPageMentionsTheOwnDeviceOnlyForSomeoneWithOne(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	rw.create(t, "Eis", "8", nil)
	if got := rw.b.get("/tasks/rewards/goal/" + rw.sebastian).body; strings.Contains(got, "auf dem eigenen Gerät") {
		t.Errorf("Sebastian has no device of his own:\n%s", got)
	}
}

var _ = store.RoleChild

// goalCard is the savings goal section of the child's page.
func goalCard(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, `<section class="rwd-goal"`)
	if i < 0 {
		t.Fatalf("no goal card:\n%s", page)
	}
	j := strings.Index(page[i:], "</section>")
	return page[i : i+j]
}

func (g goalWeb) ask(t *testing.T, reward string) {
	t.Helper()
	if r := g.miaScreen.post("/tasks/rewards/"+reward+"/ask", url.Values{}); r.status != http.StatusSeeOther || r.location != "/tasks/rewards" {
		t.Fatalf("ask = %d %q\n%s", r.status, r.location, r.body)
	}
}

func TestAnAffordableGoalCardOffersTheAskButton(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	g.rewardWeb.stars(t, g.mia, 18) // 30
	card := goalCard(t, g.choose(t, g.kino).body)
	hasAll(t, "goal card", card, `<form class="rwd-ask" method="post" action="/tasks/rewards/`+g.kino+`/ask">`, "Eltern fragen", "<b>Genug Sterne</b>")
}

func TestAskingForTheGoalKeepsTheCardOnEnoughStarsAndSaysItWaits(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	g.rewardWeb.stars(t, g.mia, 18) // 30 = the price of the goal
	g.choose(t, g.kino)
	g.ask(t, g.kino)
	page := g.miaScreen.get("/tasks/rewards", "de").body
	card := goalCard(t, page)
	hasAll(t, "asked goal card", card, `class="rwd goal ok"`, "<b>Genug Sterne</b>", `class="rwd-state wait"`, "Wartet auf Mama")
	hasNone(t, "asked goal card", card, "von 30 Sternen", "· noch", `class="rwd-ask"`, "Eltern fragen")
	// The request stands under "Deine Anfragen" as well.
	hasAll(t, "requests", page, `class="rwd-group asks"`, "Deine Anfragen", `class="rwd asked"`)
	if n := count(page, "<h3>Kino mit Popcorn</h3>"); n != 2 {
		t.Errorf("Kino is on the page %d times, want 2 (goal card and request)", n)
	}
}

func TestTheGoalCardCountsTheWholeBalanceAndOffersNoAskWhileOtherRequestsSetStarsAside(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	g.rewardWeb.stars(t, g.mia, 18) // 30
	g.choose(t, g.kino)
	g.ask(t, g.eis) // 8 stars set aside, 22 free
	card := goalCard(t, g.miaScreen.get("/tasks/rewards", "de").body)
	hasAll(t, "goal card", card, `class="rwd goal ok"`, "<b>Genug Sterne</b>")
	hasNone(t, "goal card", card, "von 30 Sternen", `class="rwd-ask"`)
	// Below the price the progress is the plain balance, not the free stars.
	g.rewardWeb.stars(t, g.mia, -10) // 20, still 8 set aside
	card = goalCard(t, g.miaScreen.get("/tasks/rewards", "de").body)
	hasAll(t, "goal card below the price", card, `aria-label="20 von 30 Sternen"`, `style="--pct:66"`, "<b>20 von 30 Sternen</b> · noch 10")
}

func TestTheGoalCardKeepsSayingItWaitsWhenTheBalanceFallsBelowThePrice(t *testing.T) {
	t.Parallel()
	g := newGoalWeb(t)
	g.rewardWeb.stars(t, g.mia, 18) // 30 = the price of the goal
	g.choose(t, g.kino)
	g.ask(t, g.kino)
	g.rewardWeb.stars(t, g.mia, -10) // 20
	card := goalCard(t, g.miaScreen.get("/tasks/rewards", "de").body)
	hasAll(t, "asked goal card below the price", card, "von 30 Sternen", "Wartet auf")
	hasNone(t, "asked goal card below the price", card, `class="rwd-ask"`)
}
