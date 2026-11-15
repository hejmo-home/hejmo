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

// A device token for a phone's calendar app carries the scope "calendars": it
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
