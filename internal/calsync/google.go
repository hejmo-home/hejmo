package calsync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

// googleAccess returns a valid access token of a Google connection, renewed
// from the stored refresh token when the last one ran out. On failure the
// message is for the sync log. A refresh token that Google rejects
// (invalid_grant) marks the connection "sign in again"; while it waits for a
// new sign-in Google is not asked again.
func (s *Service) googleAccess(ctx context.Context, h store.Household, conn store.Connection, at time.Time) (string, i18n.Message, error) {
	if !conn.SignInNeededAt.IsZero() {
		return "", i18n.Msg("sync.fail.google_signin"), fmt.Errorf("%w: waiting for a new sign-in", googleauth.ErrInvalidGrant)
	}
	if token, ok := s.tokens.get(conn.ID, at); ok {
		return token, i18n.Message{}, nil
	}
	clientID, clientSecret, err := s.db.GoogleAppSecret(h.ID)
	if err != nil {
		return "", i18n.Msg("sync.fail.google_app"), err
	}
	_, refresh, err := s.db.ConnectionCredentials(h.ID, conn.ID)
	if err != nil {
		return "", i18n.Msg("sync.fail.credentials", "detail", err.Error()), err
	}
	tok, err := s.google.Refresh(ctx, clientID, clientSecret, refresh, at)
	switch {
	case errors.Is(err, googleauth.ErrInvalidGrant):
		s.tokens.forget(conn.ID)
		if merr := s.db.MarkSignInNeeded(h.ID, conn.ID, at); merr != nil {
			err = errors.Join(err, merr)
		}
		return "", i18n.Msg("sync.fail.google_signin"), err
	case errors.Is(err, googleauth.ErrInvalidClient):
		return "", i18n.Msg("sync.fail.google_client"), err
	case err != nil:
		return "", MessageOf(err), err
	}
	s.tokens.set(conn.ID, accessToken{tok.Access, tok.Expires})
	_ = s.db.MarkTokenRenewed(h.ID, conn.ID, at)
	return tok.Access, i18n.Message{}, nil
}

// remoteFor connects to the server of a connection: a CalDAV server with its
// password, or Google's CalDAV API with an access token (see googleAccess). On
// failure the message is for the sync log.
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
	token, msg, err := s.googleAccess(ctx, h, conn, at)
	if err != nil {
		return nil, msg, err
	}
	remote, err := s.googleDial(token)
	if err != nil {
		return nil, i18n.Msg("sync.fail.address", "detail", err.Error()), err
	}
	return remote, i18n.Message{}, nil
}

// GoogleClient is the client for Google's token service and calendar list, so
// the web interface can finish a sign-in with the same gate and fakes.
func (s *Service) GoogleClient() *googleauth.Client { return s.google }

// RememberGoogleToken keeps the access token of a sign-in that just finished,
// so the first sync does not ask Google for another.
func (s *Service) RememberGoogleToken(connectionID string, t googleauth.Tokens) {
	s.tokens.set(connectionID, accessToken{t.Access, t.Expires})
}

// GoogleCalendars lists the calendars of a Google connection's account.
func (s *Service) GoogleCalendars(ctx context.Context, h store.Household, conn store.Connection) ([]googleauth.Calendar, error) {
	token, _, err := s.googleAccess(ctx, h, conn, s.now())
	if err != nil {
		return nil, err
	}
	cals, err := s.google.Calendars(ctx, token)
	if errors.Is(err, googleauth.ErrUnauthorized) { // the token ran out early: renew once
		s.tokens.forget(conn.ID)
		if token, _, err = s.googleAccess(ctx, h, conn, s.now()); err != nil {
			return nil, err
		}
		cals, err = s.google.Calendars(ctx, token)
	}
	return cals, err
}

// CountConnectionEvents asks the server of a stored connection how many events
// a calendar has in the import window, as CountEvents does for a CalDAV server.
func (s *Service) CountConnectionEvents(ctx context.Context, h store.Household, conn store.Connection, calendarURL string) (int, error) {
	remote, _, err := s.remoteFor(ctx, h, conn, s.now())
	if err != nil {
		return 0, err
	}
	return remote.CountEvents(ctx, calendarURL, s.now().AddDate(-1, 0, 0))
}

// googleRefused says whether Google's CalDAV API refused the access token of a
// Google connection, and forgets that token so the next run renews it. A refused
// token is not a wrong password: the refresh token decides whether a new sign-in is needed.
func (s *Service) googleRefused(conn store.Connection, err error) bool {
	var status *caldavclient.StatusError
	// Only 401 says the token is bad: Google also answers 403 for quota, rate
	// limits and missing calendar permissions, and then the token is fine.
	if conn.Kind != "google" || !errors.As(err, &status) || status.Code != http.StatusUnauthorized {
		return false
	}
	s.tokens.forget(conn.ID)
	return true
}

// googleMessage is the sync log message for a failure of a Google connection
// that has no password and is not a refused token: Google answers 403 for quota,
// rate limits and missing calendar permissions, which neither a new password
// nor a read-only calendar helps with.
func googleMessage(conn store.Connection, err error) (i18n.Message, bool) {
	var status *caldavclient.StatusError
	if conn.Kind != "google" || !errors.As(err, &status) || status.Code != http.StatusForbidden {
		return i18n.Message{}, false
	}
	return i18n.Msg("sync.fail.google_forbidden"), true
}
