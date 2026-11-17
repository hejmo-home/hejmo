package calsync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
)

// GoogleDialer connects to Google's CalDAV API with an access token.
type GoogleDialer func(accessToken string) (Remote, error)

// googleDialer connects to Google's CalDAV API through the calendar services connection of g.
func googleDialer(g *outbound.Gate) GoogleDialer {
	return func(accessToken string) (Remote, error) {
		c, err := caldavclient.NewBearerClient(googleauth.CalDAVBase, accessToken, g.Client(outbound.Calendars, 30*time.Second))
		if err != nil {
			return nil, err
		}
		return c, nil
	}
}

// SetGoogle sets how the service talks to Google: the client for the token
// service and how it connects to the CalDAV API (tests fake both).
func (s *Service) SetGoogle(auth *googleauth.Client, dial GoogleDialer) {
	s.google, s.googleDial, s.ownGoogle = auth, dial, true
}

// accessToken is an access token with the time it runs out.
type accessToken struct {
	token   string
	expires time.Time
}

// googleTokens keeps access tokens per connection, so a sync does not ask
// Google for a new one every time. They live in memory only.
type googleTokens struct {
	mu sync.Mutex
	by map[string]accessToken
}

func (t *googleTokens) get(connID string, now time.Time) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	a, ok := t.by[connID]
	return a.token, ok && now.Add(time.Minute).Before(a.expires)
}

func (t *googleTokens) set(connID string, a accessToken) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.by == nil {
		t.by = map[string]accessToken{}
	}
	t.by[connID] = a
}

func (t *googleTokens) forget(connID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.by, connID)
}

// remoteFor connects to the server of a connection: a CalDAV server with its
// password, or Google with an access token renewed from the stored refresh
// token. On failure the message is for the sync log. A refresh token that
// Google rejects (invalid_grant) marks the connection "sign in again" and is
// logged; nothing else is changed. While a connection waits for a new sign-in
// Google is not asked again.
func (s *Service) remoteFor(ctx context.Context, h store.Household, conn store.Connection, at time.Time) (Remote, i18n.Message, error) {
	if conn.Kind != "google" {
		user, password, err := s.db.ConnectionCredentials(h.ID, conn.ID)
		if err != nil {
			return nil, i18n.Msg("sync.fail.credentials", "detail", err.Error()), err
		}
		remote, err := s.dial(conn.URL, user, password)
		if err != nil {
			return nil, i18n.Msg("sync.fail.address", "detail", err.Error()), err
		}
		return remote, i18n.Message{}, nil
	}
	if !conn.SignInNeededAt.IsZero() {
		return nil, i18n.Msg("sync.fail.google_signin"), fmt.Errorf("%w: waiting for a new sign-in", googleauth.ErrInvalidGrant)
	}
	token, ok := s.tokens.get(conn.ID, at)
	if !ok {
		clientID, clientSecret, err := s.db.GoogleAppSecret(h.ID)
		if err != nil {
			return nil, i18n.Msg("sync.fail.google_app"), err
		}
		_, refresh, err := s.db.ConnectionCredentials(h.ID, conn.ID)
		if err != nil {
			return nil, i18n.Msg("sync.fail.credentials", "detail", err.Error()), err
		}
		tok, err := s.google.Refresh(ctx, clientID, clientSecret, refresh, at)
		switch {
		case errors.Is(err, googleauth.ErrInvalidGrant):
			s.tokens.forget(conn.ID)
			if merr := s.db.MarkSignInNeeded(h.ID, conn.ID, at); merr != nil {
				err = errors.Join(err, merr)
			}
			return nil, i18n.Msg("sync.fail.google_signin"), err
		case errors.Is(err, googleauth.ErrInvalidClient):
			return nil, i18n.Msg("sync.fail.google_client"), err
		case err != nil:
			return nil, MessageOf(err), err
		}
		token = tok.Access
		s.tokens.set(conn.ID, accessToken{token, tok.Expires})
		_ = s.db.MarkTokenRenewed(h.ID, conn.ID, at)
	}
	remote, err := s.googleDial(token)
	if err != nil {
		return nil, i18n.Msg("sync.fail.address", "detail", err.Error()), err
	}
	return remote, i18n.Message{}, nil
}
