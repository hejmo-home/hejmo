package server

import (
	"context"
	"errors"
	"html"
	"html/template"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The Google connection (ADR 0013, ticket 18). The family's own OAuth client
// (the "app registration"), the sign-in through the relay page or the pasted
// fallback address, and what comes of a callback are decided here, once, for the
// web pages and the REST API alike.

const (
	googleCallbackPath = "/connections/google/callback"
	googleClientSuffix = ".apps.googleusercontent.com"
	googleExchangeWait = 30 * time.Second
)

// googleOrigin is how the browser reached the box: the address the relay page
// has to go back to.
func googleOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// relayUsable says whether the relay page may send the browser back to this
// address: over https to any host, over http only to an address in the home
// network (the relay page refuses anything else, ADR 0013).
func relayUsable(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err == nil && ip.IsPrivate()
}

// homeIPOrigin is the address of the box in the home network under which the
// relay page works, from the address the browser connected to; "" if that is
// not an address in the home network (Docker's own bridge addresses do not count).
func homeIPOrigin(r *http.Request) string {
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return ""
	}
	host, port, err := net.SplitHostPort(local.String())
	if err != nil {
		return ""
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsPrivate() || netip.MustParsePrefix("172.16.0.0/12").Contains(ip) {
		return ""
	}
	hostPort := host
	if !(port == "80" || port == "443") && r.TLS == nil {
		hostPort = net.JoinHostPort(host, port)
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
		hostPort = host
		if port != "443" {
			hostPort = net.JoinHostPort(host, port)
		}
	}
	return scheme + "://" + hostPort
}

// googleProblem is what went wrong when a sign-in came back; the empty value is success.
type googleProblem string

const (
	gpStateUnknown googleProblem = "state_unknown" // not made by this box, or by another browser's cookie-less path
	gpStateExpired googleProblem = "state_expired"
	gpStateUsed    googleProblem = "state_used"
	gpCancelled    googleProblem = "cancelled"
	gpCodeRejected googleProblem = "code_rejected"
	gpScopeMissing googleProblem = "scope_missing"
	gpClient       googleProblem = "client_rejected"
	gpWrongAccount googleProblem = "wrong_account"
	gpNotCallback  googleProblem = "not_a_callback"
	gpUnreachable  googleProblem = "unreachable"
	gpNoApp        googleProblem = "no_app"
)

// googleOutcome is the result of finishing a sign-in.
type googleOutcome struct {
	Problem googleProblem
	// Conn is the connection that was created or signed in again.
	Conn        store.Connection
	Reconnected bool
	// The login the callback belonged to (known once the state was accepted): the connection it
	// was for, and for a wrong account that account and the nonce under which its token is kept.
	ForConnection string
	Account       string
	Nonce         string
}

// googleStart is a sign-in that was started: where to send the browser.
type googleStart struct {
	URL, RedirectURI, Way string
	Expires               time.Time
}

// googleClient returns the client for Google with the household's app registration
// (client ID and secret), or store.ErrNotFound if the family has not entered one.
func (s *server) googleClient(p principal) (*googleauth.Client, string, string, error) {
	id, secret, err := s.db.GoogleAppSecret(p.household.ID)
	if err != nil {
		return nil, "", "", err
	}
	return s.sync.GoogleClient(), id, secret, nil
}

// startGoogleLogin makes the state and the PKCE pair and stores them, and
// returns the address at Google. connectionID names a connection that signs in
// again. way is "relay" or "fallback"; relay is only possible when the relay
// page may come back to this box (relayUsable). Nothing is created before this
// is called, so reloading the page that offers it uses nothing up.
func (s *server) startGoogleLogin(r *http.Request, p principal, connectionID, way string) (googleStart, error) {
	client, clientID, _, err := s.googleClient(p)
	if err != nil {
		return googleStart{}, err
	}
	origin := googleOrigin(r)
	if way != "fallback" && relayUsable(origin) {
		way = "relay"
	} else {
		way = "fallback"
	}
	redirect := googleauth.RelayRedirectURI
	if way == "fallback" {
		redirect = googleauth.FallbackRedirectURI
	}
	hint := ""
	if connectionID != "" {
		conn, err := s.db.Connection(p.household.ID, connectionID)
		if err != nil || conn.Kind != "google" {
			return googleStart{}, store.ErrNotFound
		}
		hint = conn.Username
	}
	now := s.now()
	tok, err := googleauth.NewStateToken(s.db.Sealer(), now)
	if err != nil {
		return googleStart{}, err
	}
	verifier, err := googleauth.NewVerifier()
	if err != nil {
		return googleStart{}, err
	}
	expires := now.Add(googleauth.StateLifetime)
	if err := s.db.CreateGoogleLogin(p.household.ID, store.GoogleLogin{
		Nonce: tok.Nonce, PersonID: p.person.ID, Verifier: verifier, RedirectURI: redirect, ConnectionID: connectionID, ExpiresAt: expires,
	}, now); err != nil {
		return googleStart{}, err
	}
	return googleStart{
		URL:         client.AuthURL(clientID, redirect, googleauth.State(origin, tok.Token), googleauth.Challenge(verifier), hint),
		RedirectURI: redirect, Way: way, Expires: expires,
	}, nil
}

// finishGoogleLogin takes what Google (or the parent, pasting) sent back and
// ends the sign-in: it checks the state (signed by the box, not expired, never
// used before), exchanges the code with the PKCE verifier and stores the refresh
// token sealed, as a new connection or on the one that signed in again.
func (s *server) finishGoogleLogin(ctx context.Context, p principal, cb googleauth.Callback) (googleOutcome, error) {
	now := s.now()
	_, token, err := googleauth.ParseState(cb.State)
	if err != nil {
		return googleOutcome{Problem: gpStateUnknown}, nil
	}
	nonce, err := googleauth.VerifyStateToken(s.db.Sealer(), token, now)
	switch {
	case errors.Is(err, googleauth.ErrStateExpired):
		return googleOutcome{Problem: gpStateExpired}, nil
	case err != nil:
		return googleOutcome{Problem: gpStateUnknown}, nil
	}
	login, err := s.db.TakeGoogleLogin(p.household.ID, nonce, now)
	switch {
	case errors.Is(err, store.ErrUsed):
		return googleOutcome{Problem: gpStateUsed}, nil
	case errors.Is(err, store.ErrExpired):
		return googleOutcome{Problem: gpStateExpired}, nil
	case errors.Is(err, store.ErrNotFound):
		return googleOutcome{Problem: gpStateUnknown}, nil
	case err != nil:
		return googleOutcome{}, err
	}
	out := googleOutcome{ForConnection: login.ConnectionID, Nonce: nonce}
	if cb.Error != "" {
		out.Problem = gpCodeRejected
		if cb.Error == "access_denied" {
			out.Problem = gpCancelled
		}
		return out, nil
	}
	if cb.Code == "" {
		out.Problem = gpNotCallback
		return out, nil
	}
	if cb.Scope != "" && !strings.Contains(" "+cb.Scope+" ", " "+googleauth.Scope+" ") {
		out.Problem = gpScopeMissing
		return out, nil
	}
	client, clientID, clientSecret, err := s.googleClient(p)
	if errors.Is(err, store.ErrNotFound) {
		out.Problem = gpNoApp
		return out, nil
	} else if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, googleExchangeWait)
	defer cancel()
	tok, err := client.Exchange(ctx, clientID, clientSecret, cb.Code, login.Verifier, login.RedirectURI, now)
	switch {
	case errors.Is(err, googleauth.ErrInvalidGrant), errors.Is(err, googleauth.ErrNoRefreshToken):
		out.Problem = gpCodeRejected
		return out, nil
	case errors.Is(err, googleauth.ErrInvalidClient):
		out.Problem = gpClient
		return out, nil
	case errors.Is(err, googleauth.ErrScopeMissing):
		out.Problem = gpScopeMissing
		return out, nil
	case err != nil:
		s.logger.Warn("google sign-in: token exchange", "err", err)
		out.Problem = gpUnreachable
		return out, nil
	}
	cals, err := client.Calendars(ctx, tok.Access)
	if err != nil {
		s.logger.Warn("google sign-in: calendar list", "err", err)
		out.Problem = gpUnreachable
		return out, nil
	}
	account := googleAccount(cals)
	if account == "" {
		out.Problem = gpUnreachable
		return out, nil
	}
	out.Account = account
	conns, err := s.db.Connections(p.household.ID)
	if err != nil {
		return out, err
	}
	var existing *store.Connection
	for i, c := range conns {
		if c.Kind == "google" && (c.ID == login.ConnectionID || (login.ConnectionID == "" && strings.EqualFold(c.Username, account))) {
			existing = &conns[i]
		}
	}
	switch {
	case existing != nil && !strings.EqualFold(existing.Username, account):
		// Another Google account than the one that asked: not thrown away, offered as a new connection.
		if err := s.db.StashGoogleToken(p.household.ID, nonce, account, tok.Refresh, now); err != nil {
			return out, err
		}
		out.Problem = gpWrongAccount
		return out, nil
	case existing != nil:
		if err := s.db.SetGoogleRefreshToken(p.household.ID, existing.ID, tok.Refresh, now); err != nil {
			return out, err
		}
		out.Conn, out.Reconnected = *existing, true
	default:
		conn, err := s.db.CreateGoogleConnection(p.household.ID, account, tok.Refresh, now)
		if err != nil {
			return out, err
		}
		out.Conn = conn
	}
	s.sync.RememberGoogleToken(out.Conn.ID, tok)
	if out.Reconnected {
		s.syncConnectionNow(ctx, p, out.Conn)
	}
	return out, nil
}

// adoptStashedGoogle adds the account of a sign-in that was for another
// connection as a connection of its own.
func (s *server) adoptStashedGoogle(p principal, nonce string) (store.Connection, error) {
	account, token, err := s.db.TakeStashedGoogleToken(p.household.ID, nonce)
	if err != nil {
		return store.Connection{}, err
	}
	conns, err := s.db.Connections(p.household.ID)
	if err != nil {
		return store.Connection{}, err
	}
	for _, c := range conns {
		if c.Kind == "google" && strings.EqualFold(c.Username, account) {
			return c, s.db.SetGoogleRefreshToken(p.household.ID, c.ID, token, s.now())
		}
	}
	return s.db.CreateGoogleConnection(p.household.ID, account, token, s.now())
}

// syncConnectionNow has the calendars of a connection sync at once, within a
// bounded time: they were waiting for a working sign-in.
func (s *server) syncConnectionNow(ctx context.Context, p principal, conn store.Connection) {
	sctx, stop := context.WithTimeout(ctx, passwordSyncTimeoutSec*time.Second)
	defer stop()
	all, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		return
	}
	for _, c := range calendarsOf(all, conn.ID) {
		if sctx.Err() != nil {
			return
		}
		_ = s.sync.Sync(sctx, p.household.ID, c.ID)
	}
}

// googleAccount is the Google account of a calendar list: the id of the
// primary calendar is the account's address.
func googleAccount(cals []googleauth.Calendar) string {
	for _, c := range cals {
		if c.Primary {
			return c.ID
		}
	}
	return ""
}

// googleDiscovered turns the calendar list of an account into the calendars
// the parent can tick. The account's own calendar comes first; for each the
// access Google gives says whether the box may change it, and why not.
func googleDiscovered(cals []googleauth.Calendar, account string) []discoveredJSON {
	out := make([]discoveredJSON, 0, len(cals))
	add := func(c googleauth.Calendar) {
		d := discoveredJSON{URL: googleauth.CalendarURL(googleauth.CalDAVBase, c.ID), Name: c.Name()}
		switch {
		case strings.HasSuffix(c.ID, "#contacts@group.v.calendar.google.com"):
			d.Access, d.Reason = "read_only", "birthdays"
		case !c.Writable() && strings.HasSuffix(c.ID, "@group.v.calendar.google.com"):
			d.Access, d.Reason = "read_only", "google"
		case !c.Writable():
			d.Access, d.Reason = "read_only", "shared"
		default:
			d.Access, d.Reason = "read_write", "writable"
		}
		google := strings.HasSuffix(c.ID, "@group.v.calendar.google.com") // Google's own calendars: birthdays, holidays
		if c.AccessRole != "owner" && !google {
			d.Shared = true
			if strings.Contains(c.ID, "@") && !strings.HasSuffix(c.ID, "calendar.google.com") { // the id of a person's own calendar is their account
				d.SharedBy = c.ID
			}
		}
		if d.Name == "" {
			d.Name = c.ID
		}
		out = append(out, d)
	}
	for _, c := range cals {
		if c.Primary {
			add(c)
		}
	}
	for _, c := range cals {
		if !c.Primary {
			add(c)
		}
	}
	return out
}

// googleCalendarsOf lists the calendars of a Google connection as discovered ones.
func (s *server) googleCalendarsOf(ctx context.Context, p principal, conn store.Connection) ([]discoveredJSON, error) {
	ctx, cancel := context.WithTimeout(ctx, discoverTimeoutSec*time.Second)
	defer cancel()
	cals, err := s.sync.GoogleCalendars(ctx, p.household, conn)
	switch {
	case errors.Is(err, googleauth.ErrInvalidGrant):
		return nil, &inputError{"signin", "Google no longer accepts the sign-in: sign in again"}
	case errors.Is(err, googleauth.ErrInvalidClient):
		return nil, &inputError{"client", "Google does not accept the client ID or client secret"}
	case err != nil:
		return nil, &inputError{"server", "could not read calendars from Google: " + err.Error()}
	}
	return s.markAddedList(p, conn, googleDiscovered(cals, conn.Username))
}

// googleAccessFor finds the access Google gives one calendar of a connection
// (for a calendar that is about to be added).
func (s *server) googleAccessFor(ctx context.Context, p principal, conn store.Connection, address string) (discoveredJSON, error) {
	found, err := s.googleCalendarsOf(ctx, p, conn)
	if err != nil {
		return discoveredJSON{}, err
	}
	for _, f := range found {
		if caldavclient.AddressKey(f.URL) == caldavclient.AddressKey(address) {
			return f, nil
		}
	}
	return discoveredJSON{}, &inputError{"calendar", "url must be a calendar of this Google account"}
}

// googleAppFacts is what the connect page and the API say about the family's app registration.
type googleAppView struct {
	Registered bool
	ClientID   string
	By, Since  string
	Accounts   []string // Google accounts already connected
	// AccountsHTML lists them as names that may wrap, separated by commas and ended by a full stop.
	AccountsHTML template.HTML
}

func (s *server) googleAppView(r *http.Request, p principal) (googleAppView, error) {
	var v googleAppView
	app, err := s.db.GoogleApp(p.household.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return v, nil
	case err != nil:
		return v, err
	}
	v.Registered, v.ClientID, v.Since = true, app.ClientID, p.day(app.CreatedAt)
	if by, err := s.db.Person(p.household.ID, app.CreatedBy); err == nil {
		v.By = by.Name
	}
	conns, err := s.db.Connections(p.household.ID)
	if err != nil {
		return v, err
	}
	var names []string
	for _, c := range conns {
		if c.Kind == "google" {
			v.Accounts = append(v.Accounts, c.Username)
			names = append(names, `<b class="host">`+html.EscapeString(c.Username)+`</b>`)
		}
	}
	v.AccountsHTML = template.HTML(strings.Join(names, ", ") + ".") // #nosec G203 -- the accounts are escaped
	return v, nil
}

// checkGoogleApp checks what a parent typed for the app registration without
// saving it: the field it belongs to and the problem.
func checkGoogleApp(clientID, secret string) *inputError {
	clientID = strings.TrimSpace(clientID)
	switch {
	case clientID == "" || !strings.HasSuffix(clientID, googleClientSuffix) || len(clientID) == len(googleClientSuffix):
		return &inputError{"client_id", "the client ID ends with " + googleClientSuffix}
	case strings.ContainsAny(clientID, " \t\r\n/"):
		return &inputError{"client_id", "the client ID has no spaces"}
	case strings.TrimSpace(secret) == "":
		return &inputError{"client_secret", "the client secret is required"}
	}
	return nil
}

// signedInAgainWho names the Google accounts that have to sign in again after a new app registration.
func (s *server) googleAccountsOf(p principal) []string {
	conns, err := s.db.Connections(p.household.ID)
	if err != nil {
		return nil
	}
	var out []string
	for _, c := range conns {
		if c.Kind == "google" {
			out = append(out, c.Username)
		}
	}
	return out
}
