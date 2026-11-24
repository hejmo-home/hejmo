package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/server"
)

// Ticket 293: the savings goal over REST, contract-checked against api/openapi.yaml.

func goalPath(person string) string { return "/people/" + person + "/savings-goal" }

func TestAGoalIsSetReadSwitchedAndClearedOverRest(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	kino := rb.create(t, map[string]any{"name": "Kino", "price": 30})["id"].(string)
	eis := rb.create(t, map[string]any{"name": "Eis", "price": 8})["id"].(string)
	rb.book(t, rb.miaID, 12)

	status, got, _ := rb.parent.call(http.MethodGet, goalPath(rb.miaID), nil)
	if status != http.StatusOK || got["reward_id"] != nil || got["person_id"] != rb.miaID || got["ended"] != nil {
		t.Fatalf("no goal yet = %d %v", status, got)
	}
	status, got, _ = rb.parent.call(http.MethodPut, goalPath(rb.miaID), map[string]any{"reward_id": kino})
	if status != http.StatusOK || got["reward_id"] != kino || got["name"] != "Kino" || got["price"] != float64(30) || got["balance"] != float64(12) || got["reached"] != false {
		t.Fatalf("set = %d %v", status, got)
	}
	status, got, _ = rb.parent.call(http.MethodPut, goalPath(rb.miaID), map[string]any{"reward_id": eis})
	if status != http.StatusOK || got["reward_id"] != eis || got["reached"] != true {
		t.Fatalf("switch = %d %v", status, got)
	}
	if _, got, _ = rb.parent.call(http.MethodGet, goalPath(rb.miaID), nil); got["reward_id"] != eis {
		t.Errorf("read = %v", got)
	}
	if status, _, _ = rb.parent.call(http.MethodDelete, goalPath(rb.miaID), nil); status != http.StatusNoContent {
		t.Fatalf("clear = %d", status)
	}
	if _, got, _ = rb.parent.call(http.MethodGet, goalPath(rb.miaID), nil); got["reward_id"] != nil {
		t.Errorf("after clear = %v", got)
	}
}

func TestAChildChangesOnlyTheirOwnGoalAndOnlyToARewardMeantForThem(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	whole := rb.create(t, map[string]any{"name": "Kino", "price": 30})["id"].(string)
	forMia := rb.create(t, map[string]any{"name": "Kuscheltier", "price": 40, "person_ids": []string{rb.miaID}})["id"].(string)

	if status, got, _ := rb.sebastian.call(http.MethodPut, goalPath(rb.sebastianID), map[string]any{"reward_id": whole}); status != http.StatusOK || got["reward_id"] != whole {
		t.Fatalf("own goal = %d %v", status, got)
	}
	if status, _, _ := rb.sebastian.call(http.MethodPut, goalPath(rb.miaID), map[string]any{"reward_id": whole}); status != http.StatusForbidden {
		t.Errorf("another child's goal = %d, want 403", status)
	}
	if status, _, _ := rb.sebastian.call(http.MethodDelete, goalPath(rb.miaID), nil); status != http.StatusForbidden {
		t.Errorf("clearing another child's goal = %d, want 403", status)
	}
	status, got, _ := rb.sebastian.call(http.MethodPut, goalPath(rb.sebastianID), map[string]any{"reward_id": forMia})
	if status != http.StatusUnprocessableEntity || got["field"] != "reward_id" {
		t.Errorf("a reward not meant for them = %d %v", status, got)
	}
	if _, got, _ := rb.sebastian.call(http.MethodGet, goalPath(rb.sebastianID), nil); got["reward_id"] != whole {
		t.Errorf("the refused change left the goal alone: %v", got)
	}
	if status, _, _ := rb.sebastian.call(http.MethodDelete, goalPath(rb.sebastianID), nil); status != http.StatusNoContent {
		t.Errorf("own clear = %d", status)
	}
	if status, _, _ := rb.parent.call(http.MethodGet, goalPath("00000000-0000-0000-0000-000000000000"), nil); status != http.StatusNotFound {
		t.Errorf("unknown person = %d", status)
	}
}

func TestAnArchivedRewardIsNoGoalAndArchivingEndsOne(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	kino := rb.create(t, map[string]any{"name": "Kino", "price": 30})["id"].(string)
	old := rb.create(t, map[string]any{"name": "Alt", "price": 5})["id"].(string)
	rb.parent.call(http.MethodPost, "/rewards/"+old+"/archive", nil)
	status, got, _ := rb.parent.call(http.MethodPut, goalPath(rb.miaID), map[string]any{"reward_id": old})
	if status != http.StatusUnprocessableEntity || got["field"] != "reward_id" {
		t.Fatalf("archived = %d %v", status, got)
	}
	rb.parent.call(http.MethodPut, goalPath(rb.miaID), map[string]any{"reward_id": kino})
	rb.parent.call(http.MethodPost, "/rewards/"+kino+"/archive", nil)
	_, got, _ = rb.parent.call(http.MethodGet, goalPath(rb.miaID), nil)
	ended, _ := got["ended"].(map[string]any)
	if got["reward_id"] != nil || ended == nil || ended["reason"] != "archived" || ended["name"] != "Kino" {
		t.Errorf("after archiving = %v", got)
	}
}

func TestMCPToolsSetReadAndClearTheSavingsGoalUnderTheTasksArea(t *testing.T) {
	t.Parallel()
	rb := newRewardBox(t)
	c := rb.parent
	c.switchMCP(t, true)
	s := c.mcpSession(t, c.newMCPToken(t, "Claude", allAreas))
	for _, tool := range server.MCPTools() {
		switch tool.Name {
		case "get_savings_goal", "set_savings_goal", "clear_savings_goal":
			if tool.Permission.Resource != "tasks" {
				t.Errorf("%s needs %s, want the tasks area", tool.Name, tool.Permission)
			}
		}
	}
	id := rb.create(t, map[string]any{"name": "Kino", "price": 30})["id"].(string)
	if out, failed := callTool(s, "set_savings_goal", map[string]any{"id": rb.miaID, "reward_id": id}); failed || !strings.Contains(out, "Kino") {
		t.Fatalf("set_savings_goal = %v %s", failed, out)
	}
	if out, failed := callTool(s, "get_savings_goal", map[string]any{"id": rb.miaID}); failed || !strings.Contains(out, id) {
		t.Errorf("get_savings_goal = %v %s", failed, out)
	}
	if _, failed := callTool(s, "clear_savings_goal", map[string]any{"id": rb.miaID}); failed {
		t.Error("clear_savings_goal failed")
	}
	if _, got, _ := c.call(http.MethodGet, goalPath(rb.miaID), nil); got["reward_id"] != nil {
		t.Errorf("REST sees %v", got)
	}
}
