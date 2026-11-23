package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Ticket 292: asking for and redeeming rewards in the web interface (preview-292).

type redeemWeb struct {
	rewardWeb
	eis, spiel string
}

func newRedeemWeb(t *testing.T) redeemWeb {
	t.Helper()
	rw := newRewardWeb(t)
	rw.stars(t, rw.mia, 12)
	return redeemWeb{rw, rw.create(t, "Eis aus der Eisdiele", "8", nil), rw.create(t, "Spieleabend", "5", nil)}
}

// asks makes Mia ask for a reward and returns the id of her request.
func (rw redeemWeb) ask(t *testing.T, reward string) string {
	t.Helper()
	if r := rw.miaScreen.post("/tasks/rewards/"+reward+"/ask", url.Values{}); r.status != http.StatusSeeOther || r.location != "/tasks/rewards" {
		t.Fatalf("ask = %d %q\n%s", r.status, r.location, r.body)
	}
	_, _, list := rw.parent.call(http.MethodGet, "/rewards/requests", nil)
	for _, x := range list {
		if m := x.(map[string]any); m["reward_id"] == reward {
			return m["id"].(string)
		}
	}
	t.Fatalf("no open request for %s: %v", reward, list)
	return ""
}

func TestAChildAsksAndSeesTheRequestWithReservedAndFreeStars(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	before := rw.miaScreen.get("/tasks/rewards", "de")
	hasAll(t, "child view before", before.body, `<form class="rwd-ask" method="post" action="/tasks/rewards/`+rw.eis+`/ask">`, `class="button ask"`, "Eltern fragen")
	hasNone(t, "child view before", before.body, "Deine Anfragen")
	rw.ask(t, rw.eis)
	page := rw.miaScreen.get("/tasks/rewards", "de")
	hasAll(t, "child view after", page.body, `class="rwd-group asks"`, "Deine Anfragen", `class="rwd asked"`, `class="rwd-state wait"`, "Wartet auf Mama", `class="rwd-when"`, "Gefragt heute um",
		"Zurücknehmen", "8 davon sind für deine Anfrage vorgemerkt. Frei sind <b>4 Sterne</b>.")
	// The free stars decide the groups: Spieleabend (5) no longer fits into 4.
	hasAll(t, "child groups", page.body, "Dafür sparst du noch", "Noch 1 Stern")
	if n := strings.Count(page.body, "Eltern fragen"); n != 0 {
		t.Errorf("%d ask buttons left, want none", n)
	}
}

func TestAChildWithdrawsAndTheStarsAreFreeAgainButNotAfterTheParentDecided(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	id := rw.ask(t, rw.eis)
	if r := rw.miaScreen.post("/tasks/rewards/requests/"+id+"/withdraw", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("withdraw = %d", r.status)
	}
	page := rw.miaScreen.get("/tasks/rewards", "de")
	hasAll(t, "after withdrawing", page.body, `data-success`, "Zurückgenommen:", "Eis aus der Eisdiele", "Deine 8 Sterne sind wieder frei.")
	id = rw.ask(t, rw.eis)
	if r := rw.b.post("/tasks/rewards/requests/"+id+"/confirm", url.Values{"back": {"rewards"}}); r.status != http.StatusSeeOther {
		t.Fatalf("confirm = %d\n%s", r.status, r.body)
	}
	late := rw.miaScreen.post("/tasks/rewards/requests/"+id+"/withdraw", url.Values{})
	hasAll(t, "too late", late.body, `class="notice top"`, "Zu spät zum Zurücknehmen:", "Mama hat schon Ja gesagt.")
	hasNone(t, "too late", late.body, `class="ferr"`)
}

func TestTheStartPageShowsRequestsOldestFirstAndNothingWhenThereAreNone(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	if page := rw.b.get("/"); strings.Contains(page.body, `class="asks"`) {
		t.Fatalf("a section without requests:\n%s", page.body)
	}
	rw.ask(t, rw.eis)
	home := rw.b.get("/")
	hasAll(t, "start page", home.body, `<section class="asks"`, "Anfragen zum Einlösen", "Sterne werden erst abgezogen, wenn du Ja sagst.", `<ul class="ask-rows">`, `class="ask-row" data-person=`,
		"möchte <b class=\"rn\">Eis aus der Eisdiele</b>", `class="ask-meta"`, "8 Sterne", "gefragt heute um", `class="ask-bal"`, "Mia hat <b>12 Sterne</b>, danach <b>4</b>.", `class="ask-acts"`,
		"Ja, einlösen</button>", `<details class="ask-no">`, "Warum nicht? (freiwillig, Mia sieht es)", `maxlength="120"`, "Mia behält die Sterne und kann wieder fragen.", "Anfrage von Mia ablehnen")
	rewards := rw.b.get("/tasks/rewards")
	hasAll(t, "rewards page", rewards.body, `<section class="asks"`, "möchte <b class=\"rn\">Eis aus der Eisdiele</b>")
	if strings.Index(rewards.body, `class="lead"`) > strings.Index(rewards.body, `<section class="asks"`) {
		t.Error("the requests come after the lead")
	}
}

func TestConfirmingNamesRewardChildAndNewBalanceAndBooksTheStarLogLine(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	id := rw.ask(t, rw.eis)
	r := rw.b.post("/tasks/rewards/requests/"+id+"/confirm", url.Values{"back": {"home"}})
	if r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("confirm = %d %q", r.status, r.location)
	}
	home := rw.b.get("/")
	hasAll(t, "start page", home.body, `data-success`, "„Eis aus der Eisdiele“</b> für Mia eingelöst. Mia hat jetzt <b>4 Sterne</b>.")
	hasNone(t, "start page", home.body, `class="asks"`)
	tasks := rw.b.get("/tasks")
	hasAll(t, "star card", tasks.body, `class="rd"`, "Eingelöst: 🎁 Eis aus der Eisdiele", "bestätigt von Mama", "−8")
	// The second decision books nothing and says who decided.
	again := rw.b.post("/tasks/rewards/requests/"+id+"/confirm", url.Values{"back": {"home"}})
	hasAll(t, "second decision", again.body, `class="notice top"`, "Schon entschieden:", "Mama hat <b>„Eis aus der Eisdiele“</b> für Mia", "Es wurde nichts doppelt abgezogen.")
	if _, st, _ := rw.parent.call(http.MethodGet, "/people/"+rw.mia+"/stars", nil); st["balance"] != float64(4) {
		t.Fatalf("balance = %v, want 4", st["balance"])
	}
}

func TestAConfirmThatCannotBookShowsTheReasonInTheRowAndKeepsTheRequest(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	id := rw.ask(t, rw.eis)
	rw.stars(t, rw.mia, -7) // five are left
	r := rw.b.post("/tasks/rewards/requests/"+id+"/confirm", url.Values{"back": {"home"}})
	hasAll(t, "refused", r.body, `class="ferr"`, `role="alert"`, "Nicht eingelöst: Mia hat inzwischen nur noch 5 Sterne, „Eis aus der Eisdiele“ kostet 8. Die Anfrage bleibt offen.")
	if r.status != http.StatusConflict {
		t.Errorf("status = %d, want 409", r.status)
	}
	// The next page load has no Ja button, a warning says why.
	home := rw.b.get("/")
	hasAll(t, "start page", home.body, `class="flag warn"`, "Mia hat gerade nur 5 Sterne, es fehlen 3. Die Anfrage bleibt offen; Ja geht, sobald es reicht.", "Anfrage von Mia ablehnen")
	hasNone(t, "start page", home.body, "Ja, einlösen")
}

func TestDecliningShowsTheNoteAndKeepsTheStars(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	id := rw.ask(t, rw.eis)
	r := rw.b.post("/tasks/rewards/requests/"+id+"/decline", url.Values{"back": {"home"}, "note": {"Erst am Wochenende"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("decline = %d", r.status)
	}
	page := rw.miaScreen.get("/tasks/rewards", "de")
	hasAll(t, "child view", page.body, `class="rwd answer"`, `class="rwd-state no"`, "<b>Nein</b>", `class="rwd-said"`, "Mama hat Nein gesagt: <q>Erst am Wochenende</q> Deine Sterne bleiben.")
	hasNone(t, "child view", page.body, `class="rwd asked"`)
	if _, st, _ := rw.parent.call(http.MethodGet, "/people/"+rw.mia+"/stars", nil); st["balance"] != float64(12) {
		t.Fatalf("balance = %v", st["balance"])
	}
}

func TestADirectRedemptionBoxOnTheRewardPageChoosesOnePersonAndExplainsWhoCannot(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	rw.stars(t, rw.sebastian, 3)
	page := rw.b.get("/tasks/rewards/" + rw.eis)
	hasAll(t, "reward page", page.body, `<details class="more redeem">`, "Jetzt für jemanden einlösen", `<fieldset class="chips">`, `name="person" value="`+rw.mia+`"`, "12 Sterne, danach 4",
		`value="`+rw.sebastian+`" disabled`, "nur 3 Sterne, es fehlen 5", `<button class="button secondary" type="submit">Jetzt einlösen</button>`)
	// No choice: the error sits at the chips and the box stays open.
	r := rw.b.post("/tasks/rewards/"+rw.eis+"/redeem", url.Values{})
	hasAll(t, "no choice", r.body, `<details class="more redeem" open>`, `class="ferr"`, "Bitte wähle, für wen du einlöst.")
	if r.status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", r.status)
	}
	ok := rw.b.post("/tasks/rewards/"+rw.eis+"/redeem", url.Values{"person": {rw.mia}})
	if ok.status != http.StatusSeeOther {
		t.Fatalf("redeem = %d\n%s", ok.status, ok.body)
	}
	hasAll(t, "after redeeming", rw.b.get("/tasks/rewards/"+rw.eis).body, `data-success`, "„Eis aus der Eisdiele“</b> für Mia eingelöst. Mia hat jetzt <b>4 Sterne</b>.")
	hasAll(t, "star card", rw.b.get("/tasks").body, "Eingelöst: 🎁 Eis aus der Eisdiele", "von Mama direkt eingelöst")
}

func TestArchivingOrDeletingARewardNamesTheOpenRequestAndTheChildSeesItIsGone(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	rw.ask(t, rw.eis)
	hasAll(t, "delete box", rw.b.get("/tasks/rewards/"+rw.eis).body, "Die offene Anfrage von Mia wird abgelehnt. Mia sieht: „Diese Belohnung gibt es nicht mehr.“ Ihre Sterne bleiben.")
	if r := rw.b.post("/tasks/rewards/"+rw.eis+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", r.status)
	}
	page := rw.miaScreen.get("/tasks/rewards", "de")
	hasAll(t, "child view", page.body, `class="rwd answer"`, "Diese Belohnung gibt es nicht mehr.", "Deine Sterne bleiben.")
}

func TestTheBelowZeroSwitchIsUnderMoreOptionsAndSaysWhatItDoes(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	page := rw.b.get("/tasks/rewards")
	hasAll(t, "rewards page", page.body, `<details class="more below-zero">`, "Mehr Optionen", "Sterne unter null: erlaubt", `role="switch" name="below_zero"`, "checked", "Einlösen selbst braucht immer genug Sterne und bringt nie unter null.")
	r := rw.b.post("/tasks/rewards/settings", url.Values{})
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	after := rw.b.get("/tasks/rewards")
	hasAll(t, "after saving", after.body, "Gespeichert: Sterne fallen nicht mehr unter null. Wiederöffnen zieht höchstens bis null ab.", "Sterne unter null: nicht erlaubt")
	if r := rw.miaScreen.post("/tasks/rewards/settings", url.Values{"below_zero": {"1"}}); r.status != http.StatusForbidden {
		t.Errorf("a child changing the switch = %d, want 403", r.status)
	}
}

func TestANegativeBalanceNamesTheReopenedTaskAndTheRewardForChildAndParent(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	_, k, _ := rw.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": rw.mia, "title": "Zimmer aufräumen", "repeat": "daily", "stars": 3})
	task := k["id"].(string)
	rw.b.post("/tasks/"+task+"/done", url.Values{"done": {"1"}}) // 15 stars
	id := rw.ask(t, rw.eis)
	if r := rw.b.post("/tasks/rewards/requests/"+id+"/confirm", url.Values{"back": {"home"}}); r.status != http.StatusSeeOther {
		t.Fatalf("confirm = %d\n%s", r.status, r.body)
	}
	rw.stars(t, rw.mia, -7) // nothing is left of the 7
	rw.b.post("/tasks/"+task+"/done", url.Values{"done": {"0"}})
	child := rw.miaScreen.get("/tasks/rewards", "de")
	hasAll(t, "child view", child.body, `class="rwd-bal below"`, "Du hast <b>−3 Sterne</b>.", "<b>„Zimmer aufräumen“</b> wurde wieder geöffnet, nachdem du deine Sterne schon für <b>„Eis aus der Eisdiele“</b> eingelöst hattest. Deine nächsten 3 Sterne gleichen das aus.")
	parent := rw.b.get("/tasks")
	hasAll(t, "star card", parent.body, `class="stars-below"`, `class="flag warn"`, "Unter null: „Zimmer aufräumen“ wurde wieder geöffnet, nachdem die Sterne eingelöst waren. Die nächsten 3 Sterne gleichen das aus.", "Wieder geöffnet: Zimmer aufräumen")
}

func TestWithBelowZeroOffReopeningStopsAtZeroAndTheTaskPageSaysSo(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	rw.stars(t, rw.mia, -12)
	_, k, _ := rw.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": rw.mia, "title": "Zimmer aufräumen", "repeat": "daily", "stars": 3})
	task := k["id"].(string)
	rw.b.post("/tasks/"+task+"/done", url.Values{"done": {"1"}})
	rw.b.post("/tasks/rewards/settings", url.Values{}) // switch off
	rw.stars(t, rw.mia, -2)                            // one star is left
	page := rw.b.get("/tasks/" + task)
	hasAll(t, "task page", page.body, "höchstens bis 0")
	rw.b.post("/tasks/"+task+"/done", url.Values{"done": {"0"}})
	if _, st, _ := rw.parent.call(http.MethodGet, "/people/"+rw.mia+"/stars", nil); st["balance"] != float64(0) {
		t.Fatalf("balance = %v, want 0", st["balance"])
	}
}

func TestTheChildsTasksPageNamesEachOpenRequest(t *testing.T) {
	t.Parallel()
	rw := newRedeemWeb(t)
	_, _, _ = rw.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": rw.mia, "title": "Katze füttern", "repeat": "daily", "stars": 2})
	rw.ask(t, rw.eis)
	page := rw.miaScreen.get("/tasks", "de")
	hasAll(t, "child tasks page", page.body, `class="asked-line"`, "Du hast gefragt: „Eis aus der Eisdiele“. Wartet auf Mama.")
}
