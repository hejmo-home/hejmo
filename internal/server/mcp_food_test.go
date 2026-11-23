package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// Ticket 163 (ADR 0019): MCP gets diet and allergies only with the explicit
// area "health", and then at most read only.

func TestMCPGivesDietAndAllergiesOnlyWithTheHealthGrant(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	c := tb.parent
	c.switchMCP(t, true)
	c.call(http.MethodPut, "/people/"+tb.miaID+"/food", map[string]any{"diet": "vegetarian", "allergens": []string{"peanuts"}, "other": []string{"Kiwi"}})

	// Every other area, even changing all of them, does not reach it.
	without := c.mcpSession(t, c.newMCPToken(t, "Alles andere", allAreas))
	for _, n := range toolNames(t, without) {
		if strings.Contains(n, "diet") || strings.Contains(n, "allerg") {
			t.Errorf("a token without the health area sees tool %q", n)
		}
	}
	if _, failed := callTool(without, "get_diet_and_allergies", map[string]any{"id": tb.miaID}); !failed {
		t.Error("a token without the health area read diet and allergies")
	}

	with := c.mcpSession(t, c.newMCPToken(t, "Mit Gesundheit", map[string]string{"events": "read", "health": "read"}))
	names := toolNames(t, with)
	if !hasName(names, "get_diet_and_allergies") || !hasName(names, "list_diets_and_allergies") {
		t.Fatalf("tools = %v", names)
	}
	out, failed := callTool(with, "get_diet_and_allergies", map[string]any{"id": tb.miaID})
	if failed || !strings.Contains(out, "vegetarian") || !strings.Contains(out, "peanuts") || !strings.Contains(out, "Kiwi") || !strings.Contains(out, "nicht automatisch geprüft") {
		t.Errorf("get = %v %s", failed, out)
	}
	if out, failed := callTool(with, "list_diets_and_allergies", nil); failed || !strings.Contains(out, "Kiwi") {
		t.Errorf("list = %v %s", failed, out)
	}
	for _, n := range names {
		if strings.Contains(n, "set_") && (strings.Contains(n, "diet") || strings.Contains(n, "allerg")) {
			t.Errorf("an assistant can change health data with %q", n)
		}
	}
}

func TestMCPTokenCannotBeGivenChangeRightsOnHealthDataAndDefaultsToNone(t *testing.T) {
	t.Parallel()
	c := newTaskBox(t).parent
	if status, _, _ := c.call(http.MethodPost, "/mcp/tokens", map[string]any{"name": "Böse", "areas": map[string]string{"events": "read", "health": "write"}}); status != http.StatusUnprocessableEntity {
		t.Errorf("health write = %d, want 422", status)
	}
	_, tok, _ := c.call(http.MethodPost, "/mcp/tokens", map[string]any{"name": "Nur Termine", "areas": map[string]string{"events": "write"}})
	if areas := tok["areas"].(map[string]any); areas["health"] != "none" {
		t.Errorf("areas = %v, want health none", areas)
	}
}
