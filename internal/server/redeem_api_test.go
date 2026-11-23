package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// Ticket 292: asking for and redeeming rewards over REST, contract-checked
// against api/openapi.yaml.

func (rb rewardBox) balance(t *testing.T, person string) float64 {
	t.Helper()
	_, acc, _ := rb.parent.call(http.MethodGet, "/people/"+person+"/stars", nil)
	return acc["balance"].(float64)
}

func (rb rewardBox) ask(t *testing.T, reward string) (int, map[string]any) {
	t.Helper()
	status, got, _ := rb.sebastian.call(http.MethodPost, "/rewards/"+reward+"/ask", nil)
	return status, got
}

func TestAChildAsksAndNothingIsBookedUntilAParentConfirms(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	eis := rb.create(t, map[string]any{"name": "Eis", "symbol": "🍦", "price": 8})["id"].(string)
	rb.book(t, rb.sebastianID, 12)
	status, req := rb.ask(t, eis)
	if status != http.StatusCreated || req["status"] != "open" || req["name"] != "Eis" || req["price"] != float64(8) || req["person_id"] != rb.sebastianID || req["reward_id"] != eis {
		t.Fatalf("ask = %d %v", status, req)
	}
	if rb.balance(t, rb.sebastianID) != 12 {
		t.Fatal("asking books nothing")
	}
	// The parent sees it, the child sees their own.
	if _, _, list := rb.parent.call(http.MethodGet, "/rewards/requests", nil); len(list) != 1 {
		t.Fatalf("parent list = %v", list)
	}
	if _, _, list := rb.sebastian.call(http.MethodGet, "/rewards/requests", nil); len(list) != 1 {
		t.Fatalf("child list = %v", list)
	}
	id := req["id"].(string)
	status, done, _ := rb.parent.call(http.MethodPost, "/rewards/requests/"+id+"/confirm", nil)
	if status != http.StatusOK || done["status"] != "confirmed" || done["decided_by"] != rb.mamaID {
		t.Fatalf("confirm = %d %v", status, done)
	}
	if rb.balance(t, rb.sebastianID) != 4 {
		t.Fatalf("balance = %v, want 4", rb.balance(t, rb.sebastianID))
	}
}

func TestAnAskNeedsFreeStarsAndIsRefusedTwiceWithTheReason(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	eis := rb.create(t, map[string]any{"name": "Eis", "price": 8})["id"].(string)
	spiel := rb.create(t, map[string]any{"name": "Spiel", "price": 5})["id"].(string)
	rb.book(t, rb.sebastianID, 12)
	rb.ask(t, eis)
	status, got := rb.ask(t, spiel)
	if status != http.StatusConflict || got["code"] != "stars" || got["missing"] != float64(1) {
		t.Fatalf("ask with 4 free stars for 5 = %d %v", status, got)
	}
	if status, got := rb.ask(t, eis); status != http.StatusConflict || got["code"] != "asked" {
		t.Fatalf("ask twice = %d %v", status, got)
	}
}

func TestASecondDecisionBooksNothingAndSaysWhoDecidedAndWhen(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	eis := rb.create(t, map[string]any{"name": "Eis", "price": 8})["id"].(string)
	rb.book(t, rb.sebastianID, 12)
	_, req := rb.ask(t, eis)
	id := req["id"].(string)
	rb.parent.call(http.MethodPost, "/rewards/requests/"+id+"/confirm", nil)
	status, got, _ := rb.parent.call(http.MethodPost, "/rewards/requests/"+id+"/confirm", nil)
	red, _ := got["redemption"].(map[string]any)
	if status != http.StatusConflict || got["code"] != "decided" || red["status"] != "confirmed" || red["decided_by"] != rb.mamaID || red["decided_at"] == nil {
		t.Fatalf("second confirm = %d %v", status, got)
	}
	if status, got, _ := rb.parent.call(http.MethodPost, "/rewards/requests/"+id+"/decline", map[string]any{"note": "x"}); status != http.StatusConflict || got["code"] != "decided" {
		t.Fatalf("decline after confirm = %d %v", status, got)
	}
	if status, got, _ := rb.sebastian.call(http.MethodPost, "/rewards/requests/"+id+"/withdraw", nil); status != http.StatusConflict || got["code"] != "decided" {
		t.Fatalf("withdraw after confirm = %d %v", status, got)
	}
	if rb.balance(t, rb.sebastianID) != 4 {
		t.Fatalf("balance = %v, want 4: booked once", rb.balance(t, rb.sebastianID))
	}
}

func TestConfirmingRefusesWhenTheStarsDroppedAndKeepsTheRequestOpen(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	eis := rb.create(t, map[string]any{"name": "Eis", "price": 8})["id"].(string)
	rb.book(t, rb.sebastianID, 9)
	_, req := rb.ask(t, eis)
	rb.book(t, rb.sebastianID, -4)
	status, got, _ := rb.parent.call(http.MethodPost, "/rewards/requests/"+req["id"].(string)+"/confirm", nil)
	if status != http.StatusConflict || got["code"] != "stars" || got["missing"] != float64(3) {
		t.Fatalf("confirm with 5 stars = %d %v", status, got)
	}
	if _, _, list := rb.parent.call(http.MethodGet, "/rewards/requests", nil); len(list) != 1 {
		t.Fatalf("the request must stay open: %v", list)
	}
}

func TestDecliningCarriesANoteTheChildSeesAndAChildCanWithdraw(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	eis := rb.create(t, map[string]any{"name": "Eis", "price": 8})["id"].(string)
	rb.book(t, rb.sebastianID, 12)
	_, req := rb.ask(t, eis)
	status, got, _ := rb.parent.call(http.MethodPost, "/rewards/requests/"+req["id"].(string)+"/decline", map[string]any{"note": "Erst am Wochenende"})
	if status != http.StatusOK || got["status"] != "declined" || got["note"] != "Erst am Wochenende" || got["decline_reason"] != "parent" {
		t.Fatalf("decline = %d %v", status, got)
	}
	_, req = rb.ask(t, eis)
	status, got, _ = rb.sebastian.call(http.MethodPost, "/rewards/requests/"+req["id"].(string)+"/withdraw", nil)
	if status != http.StatusOK || got["status"] != "withdrawn" {
		t.Fatalf("withdraw = %d %v", status, got)
	}
	if rb.balance(t, rb.sebastianID) != 12 {
		t.Fatal("stars stay")
	}
}

func TestOnlyAChildAsksAndOnlyAParentDecides(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	eis := rb.create(t, map[string]any{"name": "Eis", "price": 8})["id"].(string)
	rb.book(t, rb.sebastianID, 12)
	_, req := rb.ask(t, eis)
	id := req["id"].(string)
	if status, _, _ := rb.parent.call(http.MethodPost, "/rewards/"+eis+"/ask", nil); status != http.StatusForbidden {
		t.Errorf("a parent asking = %d, want 403", status)
	}
	for _, path := range []string{"/rewards/requests/" + id + "/confirm", "/rewards/requests/" + id + "/decline", "/rewards/" + eis + "/redeem"} {
		if status, _, _ := rb.sebastian.call(http.MethodPost, path, map[string]any{"person_id": rb.sebastianID}); status != http.StatusForbidden {
			t.Errorf("a child at %s = %d, want 403", path, status)
		}
	}
	if status, _, _ := rb.child.call(http.MethodPost, "/rewards/requests/"+id+"/withdraw", nil); status != http.StatusNotFound {
		t.Errorf("withdrawing somebody else's request = %d, want 404", status)
	}
}

func TestADirectRedemptionBooksAtOnceOrConfirmsTheOpenRequest(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	kino := rb.create(t, map[string]any{"name": "Kino", "price": 30})["id"].(string)
	rb.book(t, rb.sebastianID, 70)
	status, got, _ := rb.parent.call(http.MethodPost, "/rewards/"+kino+"/redeem", map[string]any{"person_id": rb.sebastianID})
	if status != http.StatusCreated || got["status"] != "confirmed" || got["direct"] != true || rb.balance(t, rb.sebastianID) != 40 {
		t.Fatalf("direct = %d %v", status, got)
	}
	_, req := rb.ask(t, kino)
	status, got, _ = rb.parent.call(http.MethodPost, "/rewards/"+kino+"/redeem", map[string]any{"person_id": rb.sebastianID})
	if status != http.StatusCreated || got["id"] != req["id"] || got["direct"] != false || rb.balance(t, rb.sebastianID) != 10 {
		t.Fatalf("direct with an open request = %d %v (balance %v)", status, got, rb.balance(t, rb.sebastianID))
	}
	if status, got, _ := rb.parent.call(http.MethodPost, "/rewards/"+kino+"/redeem", map[string]any{"person_id": rb.miaID}); status != http.StatusConflict || got["code"] != "stars" || got["missing"] != float64(30) {
		t.Fatalf("direct without stars = %d %v", status, got)
	}
	if status, _, _ := rb.parent.call(http.MethodPost, "/rewards/"+kino+"/redeem", map[string]any{"person_id": "00000000-0000-4000-8000-000000000000"}); status != http.StatusUnprocessableEntity {
		t.Errorf("a person outside the household = %d, want 422", status)
	}
}

func TestArchivingARewardDeclinesItsOpenRequests(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	eis := rb.create(t, map[string]any{"name": "Eis", "price": 8})["id"].(string)
	rb.book(t, rb.sebastianID, 12)
	_, req := rb.ask(t, eis)
	rb.parent.call(http.MethodPost, "/rewards/"+eis+"/archive", nil)
	_, _, items := rb.sebastian.call(http.MethodGet, "/rewards/requests", nil)
	if len(items) != 1 || items[0].(map[string]any)["id"] != req["id"] || items[0].(map[string]any)["status"] != "declined" || items[0].(map[string]any)["decline_reason"] != "reward_gone" {
		t.Fatalf("child list = %v", items)
	}
}

func TestBelowZeroIsAHouseholdSettingOnByDefault(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	_, s, _ := rb.parent.call(http.MethodGet, "/settings", nil)
	if s["below_zero"] != true {
		t.Fatalf("settings = %v", s)
	}
	status, s, _ := rb.parent.call(http.MethodPatch, "/settings", map[string]any{"below_zero": false})
	if status != http.StatusOK || s["below_zero"] != false {
		t.Fatalf("patch = %d %v", status, s)
	}
}

func TestMCPListsConfirmsDeclinesAndRedeemsForAParent(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	c := rb.parent
	c.switchMCP(t, true)
	eis := rb.create(t, map[string]any{"name": "Eis", "price": 8})["id"].(string)
	rb.book(t, rb.sebastianID, 20)
	_, req := rb.ask(t, eis)
	sess := c.mcpSession(t, c.newMCPToken(t, "Alles", allAreas))
	names := toolNames(t, sess)
	for _, want := range []string{"list_reward_requests", "confirm_reward_request", "decline_reward_request", "redeem_reward"} {
		if !hasName(names, want) {
			t.Errorf("tool %s missing in %v", want, names)
		}
	}
	if out, failed := callTool(sess, "list_reward_requests", nil); failed || !strings.Contains(out, "Eis") {
		t.Errorf("list = %v %s", failed, out)
	}
	if out, failed := callTool(sess, "confirm_reward_request", map[string]any{"id": req["id"]}); failed || !strings.Contains(out, "confirmed") {
		t.Errorf("confirm = %v %s", failed, out)
	}
	if out, failed := callTool(sess, "redeem_reward", map[string]any{"id": eis, "person_id": rb.sebastianID}); failed || !strings.Contains(out, "\"direct\": true") && !strings.Contains(out, "\"direct\":true") {
		t.Errorf("redeem = %v %s", failed, out)
	}
}

// Acceptance: two parents confirming the same request at once book it once.
func TestParallelConfirmationsOverRESTBookOnceAndTheRestSeeItDecided(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	eis := rb.create(t, map[string]any{"name": "Eis", "price": 8})["id"].(string)
	rb.book(t, rb.sebastianID, 12)
	_, req := rb.ask(t, eis)
	id := req["id"].(string)
	const n = 6
	codes := make(chan int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			<-start
			status, _, _ := rb.parent.call(http.MethodPost, "/rewards/requests/"+id+"/confirm", nil)
			codes <- status
		}()
	}
	close(start)
	ok, conflict := 0, 0
	for i := 0; i < n; i++ {
		switch s := <-codes; s {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Errorf("unexpected status %d", s)
		}
	}
	if ok != 1 || conflict != n-1 {
		t.Fatalf("%d confirmed, %d conflicts; want 1 and %d", ok, conflict, n-1)
	}
	if got := rb.balance(t, rb.sebastianID); got != 4 {
		t.Fatalf("balance = %v, want 4", got)
	}
}
