package server_test

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 291: the reward catalogue over REST, contract-checked against api/openapi.yaml.

type rewardBox struct {
	taskBox
	sebastianID string
	sebastian   *apiClient
}

func newRewardBox(t *testing.T) rewardBox {
	t.Helper()
	tb := newTaskBox(t)
	_, seb, _ := tb.parent.call(http.MethodPost, "/people", map[string]any{"name": "Sebastian", "role": "child"})
	const secret = "hejmo_test_sebastian_token"
	if _, err := tb.parent.b.db.CreateAPIToken(store.APIToken{Name: "Sebastian", Hash: auth.HashToken(secret), PersonID: seb["id"].(string), Scopes: "write"}); err != nil {
		t.Fatal(err)
	}
	return rewardBox{tb, seb["id"].(string), tb.parent.withToken(secret)}
}

func (rb rewardBox) create(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	if _, ok := body["person_ids"]; !ok {
		body["whole_family"] = true
	}
	status, got, _ := rb.parent.call(http.MethodPost, "/rewards", body)
	if status != http.StatusCreated {
		t.Fatalf("create %v = %d %v", body, status, got)
	}
	return got
}

func (rb rewardBox) book(t *testing.T, person string, stars int) {
	t.Helper()
	if status, _, _ := rb.parent.call(http.MethodPost, "/people/"+person+"/stars", map[string]any{"delta": stars, "note": "test"}); status != http.StatusCreated {
		t.Fatalf("book = %d", status)
	}
}

func (rb rewardBox) redeem(t *testing.T, reward, person, status string, at time.Time) {
	t.Helper()
	ts := at.UTC().Format(time.RFC3339)
	if _, err := rb.parent.b.db.SQL().Exec(`INSERT INTO reward_redemptions (id, household_id, reward_id, person_id, status, price, requested_at, decided_at) SELECT lower(hex(randomblob(8))), household_id, id, ?, ?, 5, ?, ? FROM rewards WHERE id = ?`,
		person, status, ts, ts, reward); err != nil {
		t.Fatal(err)
	}
}

func rewardNames(list []any) []string {
	var out []string
	for _, x := range list {
		out = append(out, x.(map[string]any)["name"].(string))
	}
	return out
}

func TestAParentCreatesChangesAndReadsARewardOverRest(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	got := rb.create(t, map[string]any{"name": "  Eis aus der Eisdiele ", "symbol": "🍦", "price": 8, "limit_period": "week", "limit_count": 1, "person_ids": []string{rb.miaID}})
	if got["name"] != "Eis aus der Eisdiele" || got["symbol"] != "🍦" || got["price"] != float64(8) || got["limit_period"] != "week" || got["limit_count"] != float64(1) ||
		got["whole_family"] != false || got["archived"] != false || got["redeemed"] != float64(0) || got["picture"] != nil || got["picture_source"] != "symbol" {
		t.Fatalf("created = %v", got)
	}
	if ids := got["person_ids"].([]any); len(ids) != 1 || ids[0] != rb.miaID {
		t.Fatalf("person_ids = %v", ids)
	}
	id := got["id"].(string)
	status, patched, _ := rb.parent.call(http.MethodPatch, "/rewards/"+id, map[string]any{"price": 10, "limit_period": "none", "whole_family": true})
	if status != http.StatusOK || patched["price"] != float64(10) || patched["limit_period"] != "none" || patched["limit_count"] != nil || patched["whole_family"] != true || patched["name"] != "Eis aus der Eisdiele" {
		t.Fatalf("patched = %d %v", status, patched)
	}
	if status, one, _ := rb.parent.call(http.MethodGet, "/rewards/"+id, nil); status != http.StatusOK || one["id"] != id {
		t.Fatalf("get = %d %v", status, one)
	}
}

func TestARewardWithABadFieldIsRefusedWithTheField(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	for field, body := range map[string]string{
		"name":         `{"name":"","price":5,"whole_family":true}`,
		"price":        `{"name":"x","price":0,"whole_family":true}`,
		"limit_period": `{"name":"x","price":5,"whole_family":true,"limit_period":"year","limit_count":1}`,
		"limit_count":  `{"name":"x","price":5,"whole_family":true,"limit_period":"week"}`,
		"symbol":       `{"name":"x","price":5,"whole_family":true,"symbol":"ab"}`,
		"person":       `{"name":"x","price":5}`,
	} {
		status, raw := rawCallBody(t, rb.parent, http.MethodPost, "/rewards", body)
		if status != http.StatusUnprocessableEntity || !strings.Contains(raw, `"field":"`+field+`"`) {
			t.Errorf("%s: %d %s", field, status, raw)
		}
	}
	if status := rawCall(t, rb.parent, http.MethodPost, "/rewards", `{"name":"x","price":5,"whole_family":true,"person_ids":["`+rb.miaID+`"]}`); status != http.StatusUnprocessableEntity {
		t.Errorf("both the whole family and people = %d, want 422", status)
	}
	if status := rawCall(t, rb.parent, http.MethodPost, "/rewards", `{"name":"x","price":5,"person_ids":["00000000-0000-4000-8000-000000000000"]}`); status != http.StatusUnprocessableEntity {
		t.Errorf("a person outside the household = %d, want 422", status)
	}
}

func TestAChildCannotChangeTheCatalogue(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	r := rb.create(t, map[string]any{"name": "Kino", "price": 30})
	id := r["id"].(string)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/rewards"}, {http.MethodPatch, "/rewards/" + id}, {http.MethodDelete, "/rewards/" + id},
		{http.MethodPost, "/rewards/" + id + "/archive"}, {http.MethodPost, "/rewards/" + id + "/restore"},
	} {
		if _, status, _ := rb.child.send(c.method, c.path, "application/json", []byte(`{"name":"x","price":1,"whole_family":true}`)); status != http.StatusForbidden {
			t.Errorf("%s %s as a child = %d, want 403", c.method, c.path, status)
		}
	}
}

// Acceptance: children see the rewards meant for them and what they can afford.
func TestAChildSeesTheirRewardsSortedWithWhatTheyCanAfford(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	rb.create(t, map[string]any{"name": "Kino", "price": 30})
	rb.create(t, map[string]any{"name": "Eis", "price": 8})
	rb.create(t, map[string]any{"name": "Affe", "price": 8, "person_ids": []string{rb.miaID}})
	rb.create(t, map[string]any{"name": "Nur Sebastian", "price": 5, "person_ids": []string{rb.sebastianID}})
	hidden := rb.create(t, map[string]any{"name": "Archiv", "price": 1})
	rb.parent.call(http.MethodPost, "/rewards/"+hidden["id"].(string)+"/archive", nil)
	rb.book(t, rb.miaID, 10)

	status, _, list := rb.child.call(http.MethodGet, "/rewards", nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d", status)
	}
	if got := rewardNames(list); strings.Join(got, ",") != "Affe,Eis,Kino" {
		t.Fatalf("mia sees %v, want Affe,Eis,Kino (by price, then name; no archived, none for others)", got)
	}
	afford := map[string]bool{}
	for _, x := range list {
		m := x.(map[string]any)
		afford[m["name"].(string)] = m["affordable"].(bool)
		if m["limit_reached"] != false || m["available_again"] != nil {
			t.Errorf("%v: no limit, so not reached", m)
		}
	}
	if !afford["Affe"] || !afford["Eis"] || afford["Kino"] {
		t.Errorf("affordable with 10 stars = %v", afford)
	}
	_, _, seb := rb.sebastian.call(http.MethodGet, "/rewards", nil)
	if got := rewardNames(seb); strings.Join(got, ",") != "Nur Sebastian,Eis,Kino" {
		t.Errorf("sebastian sees %v", got)
	}
	// A reward meant for someone else is not found by a child.
	_, _, all := rb.parent.call(http.MethodGet, "/rewards", nil)
	if len(all) != 5 {
		t.Fatalf("a parent sees every reward, archived ones too: %v", rewardNames(all))
	}
	for _, x := range all {
		m := x.(map[string]any)
		if m["name"] == "Nur Sebastian" {
			if status, _, _ := rb.child.call(http.MethodGet, "/rewards/"+m["id"].(string), nil); status != http.StatusNotFound {
				t.Errorf("another child's reward = %d, want 404", status)
			}
		}
		if m["name"] == "Archiv" {
			if status, _, _ := rb.child.call(http.MethodGet, "/rewards/"+m["id"].(string), nil); status != http.StatusNotFound {
				t.Errorf("an archived reward = %d, want 404", status)
			}
		}
	}
}

// Acceptance: limits count confirmed redemptions per person and period.
func TestTheLimitCountsConfirmedRedemptionsPerPersonInTheCurrentWeek(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t) // the box clock is Saturday 14/11/2026, 12:00 UTC
	r := rb.create(t, map[string]any{"name": "Eis", "price": 3, "limit_period": "week", "limit_count": 1})
	id := r["id"].(string)
	rb.book(t, rb.miaID, 50)
	rb.book(t, rb.sebastianID, 50)
	rb.redeem(t, id, rb.miaID, "confirmed", time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)) // Tuesday of this week
	rb.redeem(t, id, rb.miaID, "declined", time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC))
	rb.redeem(t, id, rb.sebastianID, "confirmed", time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)) // Sunday, last week

	_, one, _ := rb.child.call(http.MethodGet, "/rewards/"+id, nil)
	if one["limit_reached"] != true || !strings.HasPrefix(one["available_again"].(string), "2026-11-16T00:00:00") {
		t.Errorf("mia: reached = %v, again = %v", one["limit_reached"], one["available_again"])
	}
	_, one, _ = rb.sebastian.call(http.MethodGet, "/rewards/"+id, nil)
	if one["limit_reached"] != false || one["available_again"] != nil {
		t.Errorf("sebastian redeemed last week only: %v", one)
	}
	_, parent, _ := rb.parent.call(http.MethodGet, "/rewards/"+id, nil)
	if parent["redeemed"] != float64(2) {
		t.Errorf("redeemed = %v, want 2 confirmed", parent["redeemed"])
	}
}

func TestTheRedeemedCountStillIncludesRedemptionsOfAPersonWhoLeft(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	id := rb.create(t, map[string]any{"name": "Eis", "price": 3, "whole_family": true})["id"].(string)
	rb.redeem(t, id, rb.miaID, "confirmed", rb.parent.b.now)
	rb.redeem(t, id, rb.sebastianID, "confirmed", rb.parent.b.now)
	if _, err := rb.parent.b.db.SQL().Exec(`DELETE FROM people WHERE id = ?`, rb.miaID); err != nil {
		t.Fatal(err)
	}
	_, one, _ := rb.parent.call(http.MethodGet, "/rewards/"+id, nil)
	if one["redeemed"] != float64(2) {
		t.Errorf("redeemed after mia left = %v, want 2", one["redeemed"])
	}
	_, _, list := rb.parent.call(http.MethodGet, "/rewards", nil)
	if len(list) != 1 || list[0].(map[string]any)["redeemed"] != float64(2) {
		t.Errorf("list = %v, want redeemed 2", list)
	}
}

// Acceptance: a reward that was ever redeemed is archived, not deleted (Y).
func TestARedeemedRewardIsRefusedDeletionAndCanBeArchivedAndOfferedAgain(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	r := rb.create(t, map[string]any{"name": "Kino", "price": 30})
	id := r["id"].(string)
	rb.redeem(t, id, rb.miaID, "confirmed", rb.parent.b.now)
	status, body, _ := rb.parent.call(http.MethodDelete, "/rewards/"+id, nil)
	if status != http.StatusConflict {
		t.Fatalf("delete of a redeemed reward = %d %v, want 409", status, body)
	}
	status, arch, _ := rb.parent.call(http.MethodPost, "/rewards/"+id+"/archive", nil)
	if status != http.StatusOK || arch["archived"] != true || arch["archived_at"] == nil {
		t.Fatalf("archive = %d %v", status, arch)
	}
	if status, _, _ := rb.parent.call(http.MethodPatch, "/rewards/"+id, map[string]any{"price": 5}); status != http.StatusConflict {
		t.Errorf("changing an archived reward = %d, want 409", status)
	}
	_, _, list := rb.child.call(http.MethodGet, "/rewards", nil)
	if len(list) != 0 {
		t.Errorf("a child sees %v of an archived reward", rewardNames(list))
	}
	status, back, _ := rb.parent.call(http.MethodPost, "/rewards/"+id+"/restore", nil)
	if status != http.StatusOK || back["archived"] != false {
		t.Fatalf("restore = %d %v", status, back)
	}
}

func TestANeverRedeemedRewardIsDeleted(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	id := rb.create(t, map[string]any{"name": "Eis", "price": 3})["id"].(string)
	if status, _, _ := rb.parent.call(http.MethodDelete, "/rewards/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", status)
	}
	if status, _, _ := rb.parent.call(http.MethodGet, "/rewards/"+id, nil); status != http.StatusNotFound {
		t.Errorf("get after delete = %d", status)
	}
	if status, _, _ := rb.parent.call(http.MethodDelete, "/rewards/"+id, nil); status != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", status)
	}
}

func TestARewardWhoseAudienceLeftIsArchivedAndNeedsAnAudienceToComeBack(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	id := rb.create(t, map[string]any{"name": "Kuscheltier", "price": 40, "person_ids": []string{rb.miaID}})["id"].(string)
	if _, err := rb.parent.b.db.SQL().Exec(`DELETE FROM people WHERE id = ?`, rb.miaID); err != nil {
		t.Fatal(err)
	}
	_, got, _ := rb.parent.call(http.MethodGet, "/rewards/"+id, nil)
	if got["archived"] != true || got["archived_reason"] != "audience_gone" || got["whole_family"] != false {
		t.Fatalf("reward = %v", got)
	}
	if status, _, _ := rb.parent.call(http.MethodPost, "/rewards/"+id+"/restore", nil); status != http.StatusUnprocessableEntity {
		t.Errorf("restore without an audience = %d, want 422", status)
	}
	status, back, _ := rb.parent.call(http.MethodPost, "/rewards/"+id+"/restore", map[string]any{"person_ids": []string{rb.sebastianID}})
	if status != http.StatusOK || back["archived"] != false || back["archived_reason"] != nil || back["whole_family"] != false {
		t.Fatalf("restore with an audience = %d %v", status, back)
	}
}

func TestARewardPictureIsAnUploadReencodedAt512Px(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	id := rb.create(t, map[string]any{"name": "Kino", "price": 30})["id"].(string)
	img := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	for x := 0; x < 1200; x++ {
		img.Set(x, 3, color.RGBA{200, 30, 30, 255})
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	raw, status, _ := rb.parent.send(http.MethodPut, "/rewards/"+id+"/picture", "image/jpeg", buf.Bytes())
	if status != http.StatusOK || !strings.Contains(raw, `"picture_source":"upload"`) {
		t.Fatalf("put picture = %d %s", status, raw)
	}
	pic, status, ctype := rb.child.send(http.MethodGet, "/rewards/"+id+"/picture", "", nil)
	if status != http.StatusOK || !strings.Contains(ctype, "image/jpeg") {
		t.Fatalf("get picture = %d %s", status, ctype)
	}
	cfg, _, err := image.DecodeConfig(strings.NewReader(pic))
	if err != nil || cfg.Width != 512 || cfg.Height != 341 {
		t.Fatalf("stored = %v %v, want 512 wide", cfg, err)
	}
	if _, status, _ := rb.parent.send(http.MethodPut, "/rewards/"+id+"/picture", "image/jpeg", []byte("not an image")); status != http.StatusUnprocessableEntity {
		t.Errorf("not an image = %d, want 422", status)
	}
	status, got, _ := rb.parent.call(http.MethodPatch, "/rewards/"+id, map[string]any{"remove_picture": true, "symbol": "🎬"})
	if status != http.StatusOK || got["picture"] != nil || got["picture_source"] != "symbol" || got["symbol"] != "🎬" {
		t.Errorf("remove picture = %d %v", status, got)
	}
}

// Acceptance: REST/OpenAPI and MCP under the tasks area.
func TestMCPToolsManageTheRewardCatalogueUnderTheTasksArea(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	c := rb.parent
	c.switchMCP(t, true)
	s := c.mcpSession(t, c.newMCPToken(t, "Claude", allAreas))
	for _, tool := range server.MCPTools() {
		switch tool.Name {
		case "list_rewards", "get_reward", "create_reward", "update_reward", "archive_reward", "restore_reward", "delete_reward":
			if tool.Permission.Resource != "tasks" {
				t.Errorf("%s needs %s, want the tasks area", tool.Name, tool.Permission)
			}
		}
	}
	out, failed := callTool(s, "create_reward", map[string]any{"name": "Kino", "price": 30, "whole_family": true, "limit_period": "month", "limit_count": 1})
	if failed || !strings.Contains(out, "Kino") {
		t.Fatalf("create_reward = %v %s", failed, out)
	}
	_, _, list := c.call(http.MethodGet, "/rewards", nil)
	if len(list) != 1 {
		t.Fatalf("REST sees %v", rewardNames(list))
	}
	id := list[0].(map[string]any)["id"].(string)
	if out, failed := callTool(s, "list_rewards", nil); failed || !strings.Contains(out, "Kino") {
		t.Errorf("list_rewards = %v %s", failed, out)
	}
	if out, failed := callTool(s, "get_reward", map[string]any{"id": id}); failed || !strings.Contains(out, "Kino") {
		t.Errorf("get_reward = %v %s", failed, out)
	}
	if out, failed := callTool(s, "update_reward", map[string]any{"id": id, "price": 35}); failed || !strings.Contains(out, "35") {
		t.Errorf("update_reward = %v %s", failed, out)
	}
	if out, failed := callTool(s, "archive_reward", map[string]any{"id": id}); failed || !strings.Contains(out, `"archived":true`) {
		t.Errorf("archive_reward = %v %s", failed, out)
	}
	if out, failed := callTool(s, "restore_reward", map[string]any{"id": id}); failed || !strings.Contains(out, `"archived":false`) {
		t.Errorf("restore_reward = %v %s", failed, out)
	}
	rb.redeem(t, id, rb.miaID, "confirmed", c.b.now)
	if out, failed := callTool(s, "delete_reward", map[string]any{"id": id}); !failed {
		t.Errorf("delete_reward of a redeemed reward must fail: %s", out)
	}
}
