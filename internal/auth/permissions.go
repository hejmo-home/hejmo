package auth

import (
	"fmt"
	"strings"

	"github.com/hejmo-home/hejmo/internal/store"
)

// Action is what someone wants to do with a resource.
type Action string

// Actions. Complete means ticking off (tasks, list items) without editing.
const (
	Read     Action = "read"
	Write    Action = "write"
	Delete   Action = "delete"
	Complete Action = "complete"
)

// Resources known to the permission model.
var Resources = []string{"people", "events", "calendars", "tasks", "lists", "meals", "displays", "settings", "tokens", "health"}

// Permission is a resource plus an action, e.g. events:write.
type Permission struct {
	Resource string
	Action   Action
}

// Perm builds a Permission.
func Perm(resource string, action Action) Permission { return Permission{resource, action} }

func (p Permission) String() string { return p.Resource + ":" + string(p.Action) }

// RoleAllows applies the V1 role model: parents may do everything; children
// may read (except tokens, settings and displays, which are device and box
// management) and tick off tasks and list items, nothing else.
func RoleAllows(role store.Role, p Permission) bool {
	switch role {
	case store.RoleParent:
		return true
	case store.RoleChild:
		if p.Action == Read {
			return p.Resource != "tokens" && p.Resource != "settings" && p.Resource != "displays" && p.Resource != "health"
		}
		return p.Action == Complete && (p.Resource == "tasks" || p.Resource == "lists")
	}
	return false
}

// HealthResource is diet and allergies (ADR 0019): health data. The broad
// scopes "read" and "write" never reach it; only its own scope does.
const HealthResource = "health"

// ScopesAllow checks a token's space-separated scopes:
//
//	read             read everything (except health data)
//	write            everything (except health data)
//	<res>            everything on one resource, e.g. lists
//	<res>:<action>   one action on one resource
func ScopesAllow(scopes string, p Permission) bool {
	for _, s := range strings.Fields(scopes) {
		switch {
		case s == "write" && p.Resource != HealthResource:
			return true
		case s == "read" && p.Action == Read && p.Resource != HealthResource:
			return true
		case s == p.Resource:
			return true
		case s == p.String():
			return true
		}
	}
	return false
}

// ValidateScopes rejects unknown scopes so typos never widen or silently drop rights.
func ValidateScopes(scopes string) error {
	fields := strings.Fields(scopes)
	if len(fields) == 0 {
		return fmt.Errorf("auth: at least one scope required")
	}
	for _, s := range fields {
		if s == "read" || s == "write" {
			continue
		}
		res, action, hasAction := strings.Cut(s, ":")
		if !knownResource(res) {
			return fmt.Errorf("auth: unknown scope %q", s)
		}
		if hasAction {
			switch Action(action) {
			case Read, Write, Delete, Complete:
			default:
				return fmt.Errorf("auth: unknown action in scope %q", s)
			}
		}
	}
	return nil
}

func knownResource(r string) bool {
	for _, k := range Resources {
		if k == r {
			return true
		}
	}
	return false
}
