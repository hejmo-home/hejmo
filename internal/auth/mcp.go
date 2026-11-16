package auth

import (
	"fmt"
	"strings"
)

// MCPLevel is how far an MCP token may go in one area (ADR 0018, ticket 7.7).
type MCPLevel string

// The three choices per area: nothing, read only, or read and change (which
// includes adding, ticking off and deleting).
const (
	MCPNone  MCPLevel = "none"
	MCPRead  MCPLevel = "read"
	MCPWrite MCPLevel = "write"
)

// Valid reports whether l is one of the three choices.
func (l MCPLevel) Valid() bool { return l == MCPNone || l == MCPRead || l == MCPWrite }

// MCPAreas are the resources an MCP token can be given, in the order the
// token page shows them. Tasks cover routines, templates and star bookings.
// "health" is diet and allergies: set apart, read only at most (ADR 0019).
var MCPAreas = []string{"events", "lists", "tasks", "meals", HealthResource}

// MCPScopes turns the choice per area into the token scopes: read only is
// "<area>:read", read and change is "<area>" (every action). Areas left out
// or set to none get no scope, and the broad scopes "read" and "write" are
// never used, so tokens, settings, people and displays stay out of reach.
func MCPScopes(levels map[string]MCPLevel) (string, error) {
	for area, l := range levels {
		if !isMCPArea(area) {
			return "", fmt.Errorf("auth: %q is not an MCP area", area)
		}
		if !l.Valid() {
			return "", fmt.Errorf("auth: unknown MCP level %q for %s", l, area)
		}
		if area == HealthResource && l == MCPWrite {
			return "", fmt.Errorf("auth: an MCP token never changes health data")
		}
	}
	var scopes []string
	for _, area := range MCPAreas {
		switch levels[area] {
		case MCPRead:
			scopes = append(scopes, area+":read")
		case MCPWrite:
			scopes = append(scopes, area)
		}
	}
	if len(scopes) == 0 {
		return "", fmt.Errorf("auth: an MCP token needs at least one area")
	}
	return strings.Join(scopes, " "), nil
}

// MCPAccess reads a token's scopes back as the level per area.
func MCPAccess(scopes string) map[string]MCPLevel {
	out := map[string]MCPLevel{}
	for _, area := range MCPAreas {
		switch {
		case ScopesAllow(scopes, Perm(area, Write)):
			out[area] = MCPWrite
		case ScopesAllow(scopes, Perm(area, Read)):
			out[area] = MCPRead
		default:
			out[area] = MCPNone
		}
	}
	return out
}

func isMCPArea(a string) bool {
	for _, k := range MCPAreas {
		if k == a {
			return true
		}
	}
	return false
}
