package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
)

// Ticket 145: the same invitation through the REST API, contract-checked.
func TestInvitationsWorkThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, papa, _ := c.call(http.MethodPost, "/people", map[string]any{"name": "Papa", "role": "parent"})
	_, mia, _ := c.call(http.MethodPost, "/people", map[string]any{"name": "Mia", "role": "child"})
	path := "/people/" + papa["id"].(string) + "/invitation"

	if status, _, _ := c.call(http.MethodGet, path, nil); status != http.StatusNotFound {
		t.Fatalf("no invitation yet = %d, want 404", status)
	}
	status, inv, _ := c.call(http.MethodPost, path, nil)
	if status != http.StatusCreated || inv["state"] != "open" {
		t.Fatalf("create = %d %v", status, inv)
	}
	link, _ := inv["url"].(string)
	tok := inviteTokenRe.FindString(link)
	if !strings.HasPrefix(link, "http://") || tok == "" || inv["expires_at"] == "" || inv["created_by"] != "" && inv["created_by"] == nil {
		t.Fatalf("create answer = %v", inv)
	}
	if _, err := c.b.db.InvitationByHash(auth.HashInvitationToken(tok)); err != nil {
		t.Fatalf("not stored by hash: %v", err)
	}
	status, got, _ := c.call(http.MethodGet, path, nil)
	if status != http.StatusOK || got["state"] != "open" || got["url"] != nil || got["expires_at"] != inv["expires_at"] {
		t.Fatalf("get = %d %v (the link must not come back)", status, got)
	}
	// a new link replaces the old one
	_, second, _ := c.call(http.MethodPost, path, nil)
	if second["url"] == inv["url"] {
		t.Fatal("same link again")
	}
	if old, err := c.b.db.InvitationByHash(auth.HashInvitationToken(tok)); err != nil || old.WithdrawnAt.IsZero() {
		t.Errorf("old link still works: %+v, %v", old, err)
	}
	if status, _, _ := c.call(http.MethodDelete, path, nil); status != http.StatusNoContent {
		t.Fatalf("withdraw = %d", status)
	}
	if status, got, _ := c.call(http.MethodGet, path, nil); status != http.StatusOK || got["state"] != "withdrawn" {
		t.Errorf("after withdrawing = %d %v", status, got)
	}
	if status, _, _ := c.call(http.MethodDelete, path, nil); status != http.StatusNotFound {
		t.Errorf("withdrawing twice = %d, want 404", status)
	}
	// only adults, only people without account
	if status, _, _ := c.call(http.MethodPost, "/people/"+mia["id"].(string)+"/invitation", nil); status != http.StatusUnprocessableEntity {
		t.Errorf("inviting a child = %d, want 422", status)
	}
	_, me, _ := c.call(http.MethodGet, "/me", nil)
	if status, _, _ := c.call(http.MethodPost, "/people/"+me["id"].(string)+"/invitation", nil); status != http.StatusConflict {
		t.Errorf("inviting someone who has an account = %d, want 409", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/people/00000000-0000-4000-8000-000000000000/invitation", nil); status != http.StatusNotFound {
		t.Errorf("unknown person = %d, want 404", status)
	}
}

func TestInvitationAPIStatesAndRights(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, papa, _ := c.call(http.MethodPost, "/people", map[string]any{"name": "Papa", "role": "parent"})
	path := "/people/" + papa["id"].(string) + "/invitation"
	_, inv, _ := c.call(http.MethodPost, path, nil)
	// a read-only token sees the state but neither creates nor withdraws
	ro := c.withToken(c.newToken("read"))
	if status, got, _ := ro.call(http.MethodGet, path, nil); status != http.StatusOK || got["state"] != "open" {
		t.Errorf("read token GET = %d %v", status, got)
	}
	if status, _, _ := ro.call(http.MethodPost, path, nil); status != http.StatusForbidden {
		t.Errorf("read token POST = %d, want 403", status)
	}
	if status, _, _ := ro.call(http.MethodDelete, path, nil); status != http.StatusForbidden {
		t.Errorf("read token DELETE = %d, want 403", status)
	}
	if status, _, _ := newAPIClient(t, c.b).call(http.MethodGet, path, nil); status != http.StatusUnauthorized {
		t.Errorf("anonymous GET = %d, want 401", status)
	}
	// expiry and acceptance show as states
	c.b.now = c.b.now.Add(8 * 24 * time.Hour)
	if _, got, _ := c.call(http.MethodGet, path, nil); got["state"] != "expired" {
		t.Errorf("after 8 days = %v", got)
	}
	_, fresh, _ := c.call(http.MethodPost, path, nil)
	e := &inviteEnv{b: c.b, papa: personNamed(t, c.b, "Papa")}
	e.acceptAndSetUpCodes(inviteTokenRe.FindString(fresh["url"].(string)))
	e.signInMama()
	c2 := newAPIClient(t, c.b)
	c2.cookie = true
	if status, got, _ := c2.call(http.MethodGet, path, nil); status != http.StatusOK || got["state"] != "used" || got["accepted_at"] == nil {
		t.Errorf("accepted = %d %v", status, got)
	}
	_ = inv
}
