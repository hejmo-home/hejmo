package auth_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

func TestRolePermissions(t *testing.T) {
	cases := []struct {
		role store.Role
		perm auth.Permission
		want bool
	}{
		{store.RoleParent, auth.Perm("people", auth.Write), true},
		{store.RoleParent, auth.Perm("events", auth.Delete), true},
		{store.RoleChild, auth.Perm("people", auth.Read), true},
		{store.RoleChild, auth.Perm("events", auth.Read), true},
		{store.RoleChild, auth.Perm("people", auth.Write), false},
		// Acceptance (People and permissions): a child profile may not delete an event.
		{store.RoleChild, auth.Perm("events", auth.Delete), false},
		{store.RoleChild, auth.Perm("events", auth.Write), false},
		{store.RoleChild, auth.Perm("tasks", auth.Complete), true},
		{store.RoleChild, auth.Perm("lists", auth.Complete), true},
		{store.RoleChild, auth.Perm("settings", auth.Write), false},
		// Device and box management is parent only, even to read.
		{store.RoleChild, auth.Perm("displays", auth.Read), false},
		{store.RoleChild, auth.Perm("tokens", auth.Read), false},
		{store.RoleChild, auth.Perm("settings", auth.Read), false},
		{store.RoleParent, auth.Perm("displays", auth.Read), true},
		{"", auth.Perm("events", auth.Read), false},
	}
	for _, c := range cases {
		if got := auth.RoleAllows(c.role, c.perm); got != c.want {
			t.Errorf("RoleAllows(%s, %s) = %v, want %v", c.role, c.perm, got, c.want)
		}
	}
}

func TestTokenScopes(t *testing.T) {
	cases := []struct {
		scopes string
		perm   auth.Permission
		want   bool
	}{
		{"read", auth.Perm("events", auth.Read), true},
		// Acceptance (Platform): a read-only token may not create an event.
		{"read", auth.Perm("events", auth.Write), false},
		{"read", auth.Perm("tasks", auth.Complete), false},
		{"write", auth.Perm("events", auth.Write), true},
		{"write", auth.Perm("events", auth.Delete), true},
		{"lists", auth.Perm("lists", auth.Write), true},
		{"lists", auth.Perm("lists", auth.Read), true},
		{"lists", auth.Perm("events", auth.Read), false},
		{"lists:read", auth.Perm("lists", auth.Write), false},
		{"read lists", auth.Perm("lists", auth.Write), true},
		{"", auth.Perm("events", auth.Read), false},
	}
	for _, c := range cases {
		if got := auth.ScopesAllow(c.scopes, c.perm); got != c.want {
			t.Errorf("ScopesAllow(%q, %s) = %v, want %v", c.scopes, c.perm, got, c.want)
		}
	}
}

func TestValidateScopes(t *testing.T) {
	for _, ok := range []string{"read", "write", "read lists", "events:read", "lists:complete"} {
		if err := auth.ValidateScopes(ok); err != nil {
			t.Errorf("ValidateScopes(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "admin", "events:fly", "nothing:read"} {
		if err := auth.ValidateScopes(bad); err == nil {
			t.Errorf("ValidateScopes(%q) accepted", bad)
		}
	}
}

// A device token for a personal device's calendar app carries the scope "calendars": it
// reaches the box's CalDAV server and nothing in the REST API (ADR 0012).
func TestCalendarsScopeCoversOnlyCalendars(t *testing.T) {
	if err := auth.ValidateScopes("calendars"); err != nil {
		t.Fatalf("calendars is a valid scope: %v", err)
	}
	for _, p := range []auth.Permission{auth.Perm("calendars", auth.Read), auth.Perm("calendars", auth.Write), auth.Perm("calendars", auth.Delete)} {
		if !auth.ScopesAllow("calendars", p) {
			t.Errorf("scope calendars should allow %s", p)
		}
	}
	for _, p := range []auth.Permission{auth.Perm("events", auth.Read), auth.Perm("people", auth.Read), auth.Perm("tokens", auth.Write)} {
		if auth.ScopesAllow("calendars", p) {
			t.Errorf("scope calendars must not allow %s", p)
		}
	}
}

// Ticket 7.7 (ADR 0018): an MCP token's rights are three-way choices per area.
func TestMCPScopesFromAreaChoices(t *testing.T) {
	got, err := auth.MCPScopes(map[string]auth.MCPLevel{"events": auth.MCPRead, "lists": auth.MCPWrite, "tasks": auth.MCPNone, "meals": auth.MCPRead})
	if err != nil || got != "events:read lists meals:read" {
		t.Fatalf("auth.MCPScopes = %q, %v", got, err)
	}
	if err := auth.ValidateScopes(got); err != nil {
		t.Errorf("the scopes are not valid: %v", err)
	}
	// Whatever is chosen, an MCP token never reaches tokens, settings, people or displays.
	for _, res := range []string{"tokens", "settings", "people", "displays", "calendars"} {
		if auth.ScopesAllow(got, auth.Perm(res, auth.Read)) {
			t.Errorf("scopes %q allow reading %s", got, res)
		}
	}
	if auth.ScopesAllow(got, auth.Perm("events", auth.Write)) || !auth.ScopesAllow(got, auth.Perm("lists", auth.Delete)) {
		t.Errorf("scopes %q mix up read only and read and change", got)
	}
}

func TestMCPScopesNeedOneAreaAndKnowOnlyOurAreas(t *testing.T) {
	if _, err := auth.MCPScopes(map[string]auth.MCPLevel{"events": auth.MCPNone, "lists": auth.MCPNone, "tasks": auth.MCPNone, "meals": auth.MCPNone}); err == nil {
		t.Error("a token that may do nothing was accepted")
	}
	if _, err := auth.MCPScopes(map[string]auth.MCPLevel{}); err == nil {
		t.Error("no choice was accepted")
	}
	if _, err := auth.MCPScopes(map[string]auth.MCPLevel{"events": auth.MCPRead, "settings": auth.MCPWrite}); err == nil {
		t.Error("an area outside MCP was accepted")
	}
	if _, err := auth.MCPScopes(map[string]auth.MCPLevel{"events": "sometimes"}); err == nil {
		t.Error("an unknown level was accepted")
	}
}

func TestMCPAccessReadsScopesBackAsAreaLevels(t *testing.T) {
	got := auth.MCPAccess("events:read lists meals:read")
	want := map[string]auth.MCPLevel{"events": auth.MCPRead, "lists": auth.MCPWrite, "tasks": auth.MCPNone, "meals": auth.MCPRead}
	for area, level := range want {
		if got[area] != level {
			t.Errorf("%s = %q, want %q", area, got[area], level)
		}
	}
}

// Ticket 163 (ADR 0019): diet and allergies are health data. Only an explicit
// scope of the resource reaches them; the broad scopes and children do not.
func TestHealthDataNeedsAnExplicitScopeAndAParent(t *testing.T) {
	read, write := auth.Perm("health", auth.Read), auth.Perm("health", auth.Write)
	for _, scopes := range []string{"read", "write", "people", "people:read"} {
		if auth.ScopesAllow(scopes, read) || auth.ScopesAllow(scopes, write) {
			t.Errorf("scopes %q reach health data", scopes)
		}
	}
	if !auth.ScopesAllow("health:read", read) || auth.ScopesAllow("health:read", write) {
		t.Error("health:read is read only")
	}
	if !auth.ScopesAllow("events health", write) {
		t.Error("the resource scope allows everything on health")
	}
	if err := auth.ValidateScopes("health:read"); err != nil {
		t.Error(err)
	}
	if !auth.RoleAllows(store.RoleParent, read) || !auth.RoleAllows(store.RoleParent, write) {
		t.Error("parents read and change")
	}
	for _, p := range []auth.Permission{read, write} {
		if auth.RoleAllows(store.RoleChild, p) {
			t.Errorf("a child may %s", p)
		}
	}
}

func TestMCPHealthAreaIsReadOnlyAndNeverImplied(t *testing.T) {
	if _, err := auth.MCPScopes(map[string]auth.MCPLevel{"events": auth.MCPRead, "health": auth.MCPWrite}); err == nil {
		t.Error("an MCP token got change rights on health data")
	}
	s, err := auth.MCPScopes(map[string]auth.MCPLevel{"events": auth.MCPWrite, "health": auth.MCPRead})
	if err != nil || s != "events health:read" {
		t.Errorf("scopes = %q, %v", s, err)
	}
	s, _ = auth.MCPScopes(map[string]auth.MCPLevel{"events": auth.MCPWrite})
	if auth.MCPAccess(s)["health"] != auth.MCPNone {
		t.Error("health is none unless granted")
	}
	if auth.MCPAccess("health:read")["health"] != auth.MCPRead {
		t.Error("health:read reads back as read")
	}
}
