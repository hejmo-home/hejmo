package store_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 7.7 (ADR 0018): MCP is off by default and a parent switches it on.
func TestMCPIsOffByDefaultAndSwitchesOnAndOff(t *testing.T) {
	db, h := householdForDisplays(t)
	got, err := db.Household(h.ID)
	if err != nil || got.MCPEnabled {
		t.Fatalf("new household = %+v, %v; MCP must be off", got, err)
	}
	if err := db.SetMCPEnabled(h.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.HomeHousehold(); !got.MCPEnabled {
		t.Error("MCP did not switch on")
	}
	if err := db.SetMCPEnabled(h.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ = db.Household(h.ID); got.MCPEnabled {
		t.Error("MCP did not switch off")
	}
	if err := db.SetMCPEnabled("nope", true); err == nil {
		t.Error("an unknown household was accepted")
	}
}

// A token is made for MCP or it is not; the flag survives every lookup.
func TestAPITokenRemembersItWasCreatedForMCP(t *testing.T) {
	db, h, mama, _, _ := householdWithPapa(t)
	if _, err := db.CreateAPIToken(store.APIToken{Name: "plain", Hash: "h1", PersonID: mama.ID, Scopes: "lists"}); err != nil {
		t.Fatal(err)
	}
	mcp, err := db.CreateAPIToken(store.APIToken{Name: "assistant", Hash: "h2", PersonID: mama.ID, Scopes: "lists:read", MCP: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := db.APITokenByHash("h1"); got.MCP {
		t.Error("an ordinary token reads as MCP")
	}
	if got, _ := db.APITokenByHash("h2"); !got.MCP {
		t.Error("an MCP token reads as ordinary by hash")
	}
	all, _ := db.APITokens(h.ID)
	flags := map[string]bool{}
	for _, tok := range all {
		flags[tok.Name] = tok.MCP
	}
	if len(all) != 2 || flags["plain"] || !flags["assistant"] {
		t.Errorf("list = %+v", flags)
	}
	if got, err := db.APITokenByID(h.ID, mcp.ID); err != nil || !got.MCP {
		t.Errorf("by id = %+v, %v", got, err)
	}
}
