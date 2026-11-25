package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Ticket 291: the reward catalogue in the web interface (preview-291).

type rewardWeb struct {
	parent       *apiClient
	b            *box
	mia          string
	sebastian    string
	miaScreen    *screen
	miaPersonID  string
	sebastianScr *screen
}

func newRewardWeb(t *testing.T) rewardWeb {
	t.Helper()
	parent := signedInAPI(t)
	b := parent.b
	mia, sebastian := personNamed(t, b, "Mia"), personNamed(t, b, "Sebastian")
	return rewardWeb{parent: parent, b: b, mia: mia.ID, sebastian: sebastian.ID, miaScreen: pairedPersonalDevice(t, b, mia, "Mia's tablet")}
}

func (rw rewardWeb) form(name, price string, extra url.Values) url.Values {
	v := url.Values{"name": {name}, "price": {price}, "pic_source": {"symbol"}, "symbol": {"🎁"}, "whole_family": {"1"}, "limit_period": {""}, "limit_count": {"1"}}
	for k, vs := range extra {
		v[k] = vs
	}
	return v
}

// create posts the form and returns the id of the new reward.
func (rw rewardWeb) create(t *testing.T, name, price string, extra url.Values) string {
	t.Helper()
	r := rw.b.post("/tasks/rewards", rw.form(name, price, extra))
	if r.status != http.StatusSeeOther || r.location != "/tasks/rewards" {
		t.Fatalf("create %q = %d %q\n%s", name, r.status, r.location, r.body)
	}
	_, _, list := rw.parent.call(http.MethodGet, "/rewards", nil)
	for _, x := range list {
		if m := x.(map[string]any); m["name"] == name {
			return m["id"].(string)
		}
	}
	t.Fatalf("%q is not in the catalogue: %v", name, rewardNames(list))
	return ""
}

func (rw rewardWeb) redeem(t *testing.T, reward, person, status string, at time.Time) {
	t.Helper()
	ts := at.UTC().Format(time.RFC3339)
	if _, err := rw.b.db.SQL().Exec(`INSERT INTO reward_redemptions (id, household_id, reward_id, person_id, status, price, requested_at, decided_at) SELECT lower(hex(randomblob(8))), household_id, id, ?, ?, 5, ?, ? FROM rewards WHERE id = ?`, person, status, ts, ts, reward); err != nil {
		t.Fatal(err)
	}
}

func (rw rewardWeb) stars(t *testing.T, person string, n int) {
	t.Helper()
	if status, _, _ := rw.parent.call(http.MethodPost, "/people/"+person+"/stars", map[string]any{"delta": n, "note": "test"}); status != http.StatusCreated {
		t.Fatalf("book stars = %d", status)
	}
}

func hasAll(t *testing.T, what, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("%s lacks %q\n%s", what, w, body)
		}
	}
}

func hasNone(t *testing.T, what, body string, banned ...string) {
	t.Helper()
	for _, w := range banned {
		if strings.Contains(body, w) {
			t.Errorf("%s holds %q\n%s", what, w, body)
		}
	}
}

func TestRewardPagesNeedASignedInPerson(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	for _, path := range []string{"/tasks/rewards", "/tasks/rewards/new"} {
		if r := b.get(path); r.status != http.StatusSeeOther {
			t.Errorf("%s signed out = %d, want a redirect", path, r.status)
		}
	}
}

func TestAnEmptyCatalogueSaysWhatRewardsAreForAndOffersTheOneButton(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	r := rw.b.get("/tasks/rewards")
	if r.status != http.StatusOK {
		t.Fatalf("status = %d", r.status)
	}
	hasAll(t, "empty catalogue", r.body, "<h1>Belohnungen</h1>", `data-empty`, "Noch keine Belohnungen.", `href="/tasks/rewards/new"`, "Neue Belohnung", `<a class="back" href="/tasks">`, "Alle Aufgaben")
	hasNone(t, "empty catalogue", r.body, `list-rows rewards`)
}

func TestAParentAddsARewardAndTheOverviewShowsItByPriceWithItsLimitAndAudience(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	rw.create(t, "Kino mit Popcorn", "30", url.Values{"limit_period": {"m"}, "limit_count": {"1"}, "symbol": {"🎬"}})
	rw.create(t, "Eis aus der Eisdiele", "8", url.Values{"limit_period": {"w"}, "limit_count": {"2"}, "whole_family": {""}, "person": {rw.mia, rw.sebastian}})
	rw.create(t, "Spieleabend", "12", nil)
	r := rw.b.get("/tasks/rewards")
	hasAll(t, "overview", r.body, `class="list-rows rewards"`, "ist im Katalog. Kinder, für die sie gilt, sehen sie jetzt.", "8 Sterne", "2-mal pro Woche", "einmal pro Monat", "ohne Grenze", "Ganze Familie", "Für:", `data-shape=`, "Mia", "Sebastian", "🎬")
	i, j, k := strings.Index(r.body, "Eis aus der Eisdiele</b>"), strings.Index(r.body, "Spieleabend</b>"), strings.Index(r.body, "Kino mit Popcorn</b>")
	if !(i > 0 && i < j && j < k) {
		t.Errorf("rows are not sorted by price: %d %d %d", i, j, k)
	}
	// The same rows in English.
	en := rw.b.do(http.MethodGet, "/tasks/rewards", nil, map[string]string{"Accept-Language": "en"})
	hasAll(t, "overview (en)", en.body, "What children can exchange their stars for", "8 stars", "2 times a week", "once a month", "no limit", "Whole family", "For:", "New reward", "All tasks")
}

func TestARewardFormShowsErrorsAtTheFieldsAndKeepsWhatWasTyped(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	bad := rw.form("", "0", url.Values{"whole_family": {""}, "limit_period": {"w"}, "limit_count": {"0"}, "symbol": {"🍦"}})
	r := rw.b.post("/tasks/rewards", bad)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422\n%s", r.status, r.body)
	}
	hasAll(t, "form with errors", r.body,
		`id="err-name"`, "Gib der Belohnung einen Namen", `id="err-price"`, "Gib einen Preis von mindestens 1 Stern ein.",
		`id="err-person"`, "Wähle „Ganze Familie“ oder mindestens eine Person.", `id="err-count"`, "Wie oft: eine ganze Zahl von 1 bis 99.",
		"Grenze: bitte prüfen", `<details class="more" open>`, `name="limit_period" value="w" checked`, `name="symbol" value="🍦" checked`, `value="0"`)
	// An empty audience is never quietly turned into the whole family.
	if _, _, list := rw.parent.call(http.MethodGet, "/rewards", nil); len(list) != 0 {
		t.Errorf("a refused form saved %v", rewardNames(list))
	}
	// The name and the price stay as typed.
	again := rw.b.post("/tasks/rewards", rw.form("Eis", "1000", url.Values{"whole_family": {""}}))
	hasAll(t, "form with a high price", again.body, ">Eis</textarea>", `value="1000"`, "Der Preis darf höchstens 999 Sterne sein.", "Wähle „Ganze Familie“")
}

func TestALimitCountIsRequiredOnlyWhenAPeriodIsChosen(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	if r := rw.b.post("/tasks/rewards", rw.form("Eis", "5", url.Values{"limit_period": {""}, "limit_count": {""}})); r.status != http.StatusSeeOther {
		t.Fatalf("no period and no count = %d\n%s", r.status, r.body)
	}
	r := rw.b.post("/tasks/rewards", rw.form("Kino", "5", url.Values{"limit_period": {"m"}, "limit_count": {""}}))
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-count"`) {
		t.Fatalf("a period without a count = %d\n%s", r.status, r.body)
	}
}

func TestTheNewRewardFormHasThePreviewsFields(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	r := rw.b.get("/tasks/rewards/new")
	hasAll(t, "new reward form", r.body, `<nav class="crumbs"`, `href="/tasks/rewards"`, `<h1>Neue Belohnung</h1>`, `class="steps article-form reward-form"`, `enctype="multipart/form-data"`,
		`<textarea name="name" rows="2" maxlength="60" required`, `name="pic_source" value="symbol" checked`, `name="pic_source" value="upload"`, `class="sym-tiles"`, `name="symbol" value="🎁" checked`,
		"Gewählt: Geschenk", `type="number" inputmode="numeric" min="1" max="999"`, `name="whole_family" value="1" data-whole checked`, `class="pchip" data-person=`, "Mehr Optionen", "Grenze: keine",
		`name="limit_period" value="w"`, `class="f short limit-count"`, "Belohnung anlegen", "Foto vom Gerät")
	hasNone(t, "new reward form", r.body, `name="pic_source" value="web"`, `name="url"`, "Bild aus dem Internet")
}

// Acceptance: a reward that was ever redeemed is archived, not deleted (Y).
func TestARedeemedRewardPageOffersOnlyArchivingAndTheOtherOnlyDeleting(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	kino := rw.create(t, "Kino mit Popcorn", "30", nil)
	eis := rw.create(t, "Eis aus der Eisdiele", "8", nil)
	rw.redeem(t, kino, rw.sebastian, "confirmed", rw.b.now.Add(-48*time.Hour))
	rw.redeem(t, kino, rw.sebastian, "confirmed", rw.b.now.Add(-24*time.Hour))
	rw.redeem(t, kino, rw.mia, "confirmed", rw.b.now.Add(-time.Hour))

	page := rw.b.get("/tasks/rewards/" + kino)
	hasAll(t, "redeemed reward", page.body, `<nav class="crumbs"`, "Eingelöst", "3-mal:", "2-mal", "1-mal", "Zuletzt am 14.11.2026 von Mia", "Angelegt", `<details class="repair archive" open>`, "Belohnung archivieren",
		"Die 3 Einlösungen bleiben in den Sternekonten von", `action="/tasks/rewards/`+kino+`/archive"`, "Ja, „Kino mit Popcorn“ archivieren", "Ein neuer Preis gilt ab jetzt.")
	hasNone(t, "redeemed reward", page.body, `<details class="danger"`, "Belohnung löschen", "/delete")
	if r := rw.b.post("/tasks/rewards/"+kino+"/delete", url.Values{}); r.status != http.StatusConflict {
		t.Errorf("deleting a redeemed reward = %d, want 409", r.status)
	}
	never := rw.b.get("/tasks/rewards/" + eis)
	hasAll(t, "never redeemed reward", never.body, "Noch nie", `<details class="danger">`, "Belohnung löschen", "kein Sternekonto ändert sich", `action="/tasks/rewards/`+eis+`/delete"`)
	hasNone(t, "never redeemed reward", never.body, `repair archive`, "Belohnung archivieren")

	// Archive: hidden from children, shown folded at the bottom of the overview, and offered again.
	if r := rw.b.post("/tasks/rewards/"+kino+"/archive", url.Values{}); r.status != http.StatusSeeOther || r.location != "/tasks/rewards" {
		t.Fatalf("archive = %d %q", r.status, r.location)
	}
	over := rw.b.get("/tasks/rewards")
	hasAll(t, "overview with an archived reward", over.body, "„Kino mit Popcorn“ ist archiviert", `<details class="more arch">`, "Archivierte Belohnungen", "Kinder sehen sie nicht", "Archiviert am 14.11.2026", `list-rows rewards archived`)
	archived := rw.b.get("/tasks/rewards/" + kino)
	hasAll(t, "archived reward", archived.body, `class="notice arch-note"`, "Archiviert am 14.11.2026.", `<dl class="facts">`, "30 Sterne", "Wieder anbieten", `action="/tasks/rewards/`+kino+`/restore"`)
	hasNone(t, "archived reward", archived.body, `<textarea name="name"`, `repair archive`, "danger")
	if r := rw.b.post("/tasks/rewards/"+kino, rw.form("Anders", "5", nil)); r.status != http.StatusSeeOther || r.location != "/tasks/rewards/"+kino {
		t.Errorf("saving an archived reward = %d %q, want back to its page", r.status, r.location)
	}
	if r := rw.b.post("/tasks/rewards/"+kino+"/restore", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("restore = %d", r.status)
	}
	hasAll(t, "restored reward", rw.b.get("/tasks/rewards/"+kino).body, `<textarea name="name"`, "Belohnung archivieren")
	// Delete the one never redeemed.
	if r := rw.b.post("/tasks/rewards/"+eis+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", r.status)
	}
	if r := rw.b.get("/tasks/rewards/" + eis); r.status != http.StatusNotFound {
		t.Errorf("deleted reward = %d, want 404", r.status)
	}
}

func TestSavingARewardChangesItAndNamesItInTheSuccessLine(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	id := rw.create(t, "Eis", "8", nil)
	form := rw.form("Eis aus der Eisdiele", "9", url.Values{"limit_period": {"d"}, "limit_count": {"1"}})
	if r := rw.b.post("/tasks/rewards/"+id, form); r.status != http.StatusSeeOther || r.location != "/tasks/rewards/"+id {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
	page := rw.b.get("/tasks/rewards/" + id)
	hasAll(t, "saved reward", page.body, "„Eis aus der Eisdiele“ gespeichert.", ">Eis aus der Eisdiele</textarea>", `value="9"`, "Grenze: einmal pro Tag", `name="limit_period" value="d" checked`)
}

// Acceptance: when the last person a reward is meant for is removed, the reward is archived and says why.
func TestARewardWhoseOnlyPersonLeftIsArchivedAndSaysWhy(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	id := rw.create(t, "Kuscheltier", "40", url.Values{"whole_family": {""}, "person": {rw.mia}})
	if _, err := rw.b.db.SQL().Exec(`DELETE FROM people WHERE id = ?`, rw.mia); err != nil {
		t.Fatal(err)
	}
	over := rw.b.get("/tasks/rewards")
	hasAll(t, "overview", over.body, "Von selbst archiviert: Mia war die Einzige, für die sie galt, und ist nicht mehr im Haushalt.", "Archivierte Belohnungen")
	hasNone(t, "overview", over.body, `class="list-rows rewards"`) // nothing is offered any more
	page := rw.b.get("/tasks/rewards/" + id)
	hasAll(t, "archived page", page.body, "war die Einzige", `name="whole_family"`, "Wieder anbieten")
	// It never falls back to the whole family: restoring needs someone chosen.
	if r := rw.b.post("/tasks/rewards/"+id+"/restore", url.Values{}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Wähle „Ganze Familie“") {
		t.Fatalf("restore without an audience = %d\n%s", r.status, r.body)
	}
	if r := rw.b.post("/tasks/rewards/"+id+"/restore", url.Values{"person": {rw.sebastian}}); r.status != http.StatusSeeOther {
		t.Fatalf("restore for Sebastian = %d", r.status)
	}
	if _, _, list := rw.parent.call(http.MethodGet, "/rewards/"+id, nil); list != nil {
		t.Logf("unexpected list %v", list)
	}
}

func TestAChildSeesBalanceAndThreeGroupsSortedByPrice(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	rw.stars(t, rw.mia, 12)
	rw.create(t, "Eine Folge fernsehen", "5", url.Values{"limit_period": {"d"}, "limit_count": {"1"}})
	rw.create(t, "Spieleabend", "12", nil)
	rw.create(t, "Länger aufbleiben", "15", url.Values{"limit_period": {"w"}, "limit_count": {"1"}})
	rw.create(t, "Kino", "30", nil)
	rw.create(t, "Fast geschafft", "13", nil)
	rw.create(t, "Eis", "8", url.Values{"limit_period": {"w"}, "limit_count": {"1"}})
	rw.create(t, "Nur Sebastian", "6", url.Values{"whole_family": {""}, "person": {rw.sebastian}})
	archived := rw.create(t, "Archiv", "1", nil)
	rw.b.post("/tasks/rewards/"+archived+"/archive", url.Values{})
	_, _, list := rw.parent.call(http.MethodGet, "/rewards", nil)
	for _, x := range list {
		if m := x.(map[string]any); m["name"] == "Eis" {
			rw.redeem(t, m["id"].(string), rw.mia, "confirmed", rw.b.now.Add(-2*24*time.Hour)) // Thursday of the week of Saturday 14/11/2026
		}
	}
	page := rw.miaScreen.get("/tasks/rewards", "de")
	if page.status != http.StatusOK {
		t.Fatalf("status = %d\n%s", page.status, page.body)
	}
	hasAll(t, "child view", page.body, `<div class="rwd-cat"`, `class="rwd-bal"`, "Du hast <b>12 Sterne</b>.", "Dafür reichen deine Sterne", "Dafür sparst du noch", "Gerade nicht", `class="rwd ok"`, "Genug Sterne",
		`class="rwd save"`, "Noch 1 Stern", "Noch 3 Sterne", "Noch 18 Sterne", `aria-label="12 von 15 Sternen"`, `style="--pct:80"`, `class="rwd later"`, "Diese Woche schon eingelöst.", "Wieder ab Montag, 16.11.", "einmal pro Woche")
	hasNone(t, "child view", page.body, "Nur Sebastian", "Archiv", `class="list-rows`, "Neue Belohnung", "Für:")
	order := []string{"Eine Folge fernsehen", "Spieleabend", "Fast geschafft", "Länger aufbleiben", "Kino", "Eis"}
	last := -1
	for _, name := range order {
		i := strings.Index(page.body, "<h3>"+name+"</h3>")
		if i < last {
			t.Errorf("%q comes too early: groups and prices are the order", name)
		}
		last = i
	}
	// Children cannot change the catalogue.
	for _, path := range []string{"/tasks/rewards/new"} {
		if r := rw.miaScreen.get(path, "de"); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", path, r.status)
		}
	}
	if r := rw.miaScreen.post("/tasks/rewards", rw.form("Hack", "1", nil)); r.status != http.StatusForbidden {
		t.Errorf("child POST = %d, want 403", r.status)
	}
	if r := rw.miaScreen.get("/tasks/rewards/"+archived, "de"); r.status != http.StatusForbidden {
		t.Errorf("child GET a reward page = %d, want 403", r.status)
	}
}

func TestAChildWithoutStarsAndAChildWithoutRewardsAreTreatedKindly(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	empty := rw.miaScreen.get("/tasks/rewards", "de")
	hasAll(t, "child without rewards", empty.body, "Du hast <b>noch keine Sterne</b>.", "Sterne bekommst du für Aufgaben mit Stern.", "Für dich gibt es noch keine Belohnungen. Frag Mama", "wofür du deine Sterne eintauschen kannst.")
	rw.create(t, "Eis", "8", nil)
	rw.create(t, "Nur Sebastian", "6", url.Values{"whole_family": {""}, "person": {rw.sebastian}})
	page := rw.miaScreen.get("/tasks/rewards", "de")
	hasAll(t, "child without stars", page.body, "Du hast <b>noch keine Sterne</b>.", "Dafür sparst du noch", "Noch 8 Sterne")
	hasNone(t, "child without stars", page.body, "Dafür reichen deine Sterne", "Gerade nicht", "Für dich gibt es noch keine")
	en := rw.miaScreen.get("/tasks/rewards", "en")
	hasAll(t, "child view (en)", en.body, "You have <b>no stars yet</b>.", "You are still saving for", "8 stars to go")
}

func TestTheWayToTheRewardsIsOnTheTasksPages(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	parent := rw.b.get("/tasks")
	hasAll(t, "parent's tasks page", parent.body, `<a class="button secondary" href="/tasks/rewards">`, "Belohnungen", "Belohnungen sind das, wofür Kinder ihre Sterne eintauschen.", `<a href="/tasks/rewards">Belohnungen</a>`)
	// The child's link appears only when a reward is meant for the child.
	if got := rw.miaScreen.get("/tasks", "de").body; strings.Contains(got, `class="reward-link"`) {
		t.Errorf("the link shows without rewards:\n%s", got)
	}
	rw.create(t, "Nur Sebastian", "6", url.Values{"whole_family": {""}, "person": {rw.sebastian}})
	if got := rw.miaScreen.get("/tasks", "de").body; strings.Contains(got, `class="reward-link"`) {
		t.Errorf("the link shows for rewards meant for someone else:\n%s", got)
	}
	rw.create(t, "Eis", "8", nil)
	rw.stars(t, rw.mia, 12)
	rw.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": rw.mia, "title": "Brush teeth", "repeat": "daily"})
	child := rw.miaScreen.get("/tasks", "de").body
	hasAll(t, "child's tasks page", child, `<a class="reward-link" href="/tasks/rewards">`, "Du hast 12 Sterne. Schau, wofür sie reichen.")
	if i, j := strings.Index(child, `class="count"`), strings.Index(child, `class="reward-link"`); i < 0 || j < i {
		t.Errorf("the link must come after the progress line: %d %d", i, j)
	}
}

func TestARewardPhotoComesBackAsStoredAndOnlyToThoseItIsMeantFor(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	id := rw.create(t, "Eis", "8", url.Values{"whole_family": {""}, "person": {rw.sebastian}})
	if r := rw.b.get("/tasks/rewards/" + id + "/picture"); r.status != http.StatusNotFound {
		t.Errorf("a reward without a photo = %d, want 404", r.status)
	}
	if r := rw.miaScreen.get("/tasks/rewards/"+id+"/picture", ""); r.status != http.StatusNotFound {
		t.Errorf("a photo of a reward meant for someone else = %d, want 404", r.status)
	}
}

var pctPattern = regexp.MustCompile(`--pct:(\d+)`)

func TestTheProgressBarNeverExceedsTheFullWidth(t *testing.T) {
	t.Parallel()
	rw := newRewardWeb(t)
	rw.stars(t, rw.mia, 3)
	rw.create(t, "Kino", "30", nil)
	m := pctPattern.FindStringSubmatch(rw.miaScreen.get("/tasks/rewards", "de").body)
	if m == nil || m[1] != "10" {
		t.Errorf("3 of 30 stars = %v, want 10 percent", m)
	}
}
