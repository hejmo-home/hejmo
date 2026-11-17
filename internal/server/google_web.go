package server

import (
	"errors"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The web pages of the Google connection (ticket 18, preview-18): the guide on
// "Connect a calendar", the page "Sign in at Google" in its variants, and the
// places Google leads back to.

const (
	googleSignInPath = "/connections/google/signin"
	googleNewPath    = "/connections/new?service=google"
)

// googleConsole is the page at Google's Cloud Console of each step of the guide.
var googleConsole = map[string]string{
	"create":   "https://console.cloud.google.com/projectcreate",
	"caldav":   "https://console.cloud.google.com/apis/library/caldav.googleapis.com",
	"calendar": "https://console.cloud.google.com/apis/library/calendar-json.googleapis.com",
	"overview": "https://console.cloud.google.com/auth/overview",
	"branding": "https://console.cloud.google.com/auth/branding",
	"scopes":   "https://console.cloud.google.com/auth/scopes",
	"audience": "https://console.cloud.google.com/auth/audience",
	"client":   "https://console.cloud.google.com/auth/clients/create",
	"access":   "https://myaccount.google.com/connections",
}

// googleValues are the values a parent types in at Google, and the page of Google they go to.
var googleValues = map[string]string{
	"domain": "hejmo-home.github.io", "scope": googleauth.Scope,
	"relay": googleauth.RelayRedirectURI, "fallback": googleauth.FallbackRedirectURI,
}

// googleData adds what every page of the Google connection shows: the fixed
// values to copy and the pages at Google.
func googleData(data map[string]any) map[string]any {
	data["GConsole"], data["GValues"] = googleConsole, googleValues
	return data
}

// googleGate sends a parent whose connection to calendar services is off back to
// the page that says why: nothing is asked of Google.
func (s *server) googleGate(w http.ResponseWriter, r *http.Request) bool {
	if s.gate.Allowed(outbound.Calendars) {
		return true
	}
	http.Redirect(w, r, "/connections/new", http.StatusSeeOther)
	return false
}

// handleGoogleApp saves the app registration (the OAuth client of the family)
// and goes on to the sign-in. Without new values (a registration exists and the
// parent only goes on) it just goes on.
func (s *server) handleGoogleApp(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok || !s.googleGate(w, r) {
		return
	}
	_ = r.ParseForm()
	connection := r.PostFormValue("connection")
	next := googleSignInPath
	if connection != "" {
		next += "?" + url.Values{"connection": {connection}}.Encode()
	}
	// "Continue" with nothing typed in goes on with the registration there is; Enter in a field
	// of "Enter another app registration" submits the first button, so typed values win.
	if r.PostFormValue("action") == "continue" && strings.TrimSpace(r.PostFormValue("client_id")) == "" && r.PostFormValue("client_secret") == "" {
		if _, err := s.db.GoogleApp(p.household.ID); err == nil {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
	}
	clientID, secret := strings.TrimSpace(r.PostFormValue("client_id")), r.PostFormValue("client_secret")
	if bad := checkGoogleApp(clientID, secret); bad != nil {
		extra := map[string]any{
			"Error": "google.error." + bad.Field, "ErrorField": bad.Field, "GClientID": clientID, "GGuide": true, "GReplace": true,
		}
		if bad.Field == "client_id" { // the end of the ID in bold
			extra["ErrorText"] = rich(s.i18n.T(s.langOf(r), "google.error.client_id"))
		}
		s.renderConnectNew(w, r, p, http.StatusUnprocessableEntity, "google", extra)
		return
	}
	if _, err := s.db.SetGoogleApp(p.household.ID, p.person.ID, clientID, secret, s.now()); err != nil {
		s.fail(w, "google app", err)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// googleSignIn is what the page "Sign in at Google" shows.
type googleSignIn struct {
	Way          string // relay or fallback
	Forced       bool   // the relay page cannot come back to this address: only the fallback works
	Origin       string // how the browser reached the box
	HomeIP       string // the same page under the box's address in the home network, when known
	ConnectionID string // set when a connection signs in again
	Account      string // that connection's Google account
	Problem      googleProblem
	Wrong        string // the account that was chosen instead, with Nonce, to offer it as a new connection
	Nonce        string
	Address      string // what was pasted
	AddressError string // locale key of the error at the pasted address
}

func (s *server) renderGoogleSignIn(w http.ResponseWriter, r *http.Request, p principal, status int, in googleSignIn) {
	origin := googleOrigin(r)
	if in.Way != "fallback" && !relayUsable(origin) {
		in.Way, in.Forced = "fallback", true
	}
	if in.Way == "" {
		in.Way = "relay"
	}
	in.Origin = origin
	if in.Forced {
		in.HomeIP = homeIPOrigin(r)
		if in.HomeIP != "" {
			in.HomeIP += googleSignInPath
			if in.ConnectionID != "" {
				in.HomeIP += "?" + url.Values{"connection": {in.ConnectionID}}.Encode()
			}
		}
	}
	title, lead := "google.signin.title", "google.signin.lead"
	if in.Way == "fallback" {
		lead = "google.signin.lead_fallback"
	}
	if in.ConnectionID != "" {
		title, lead = "google.resignin.title", "google.resignin.lead"
	}
	lang := s.langOf(r)
	data := googleData(map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "S": in,
		"Title": s.i18n.T(lang, title), "LeadHTML": accountText(s.i18n.T(lang, lead), in.Account),
		"OutNote": s.noteFor(r, outbound.Calendars, "outside.note.connect"),
	})
	if in.AddressError != "" {
		data["Error"], data["ErrorField"] = in.AddressError, "address"
	}
	if in.Problem != "" {
		data["ProblemTitle"], data["ProblemText"] = s.problemText(lang, in)
	}
	s.render(w, r, status, "google_signin.html", data)
}

// accountText is a translated sentence with the Google account in {account}
// set as a name that may wrap, and the other marks of rich text applied.
func accountText(text, account string) template.HTML {
	out := strings.ReplaceAll(string(rich(text)), "{account}", `<b class="host">`+html.EscapeString(account)+`</b>`)
	return template.HTML(out) // #nosec G203 -- rich escapes the text, the account is escaped here
}

func (s *server) problemText(lang string, in googleSignIn) (string, template.HTML) {
	key := map[googleProblem]string{
		gpStateUnknown: "stale", gpStateExpired: "stale", gpStateUsed: "used", gpCancelled: "cancelled",
		gpCodeRejected: "code", gpScopeMissing: "scope", gpWrongAccount: "account", gpNotCallback: "unreachable", gpUnreachable: "unreachable", gpNoApp: "unreachable",
	}[in.Problem]
	if key == "" {
		key = "unreachable"
	}
	title := s.i18n.T(lang, "google.problem."+key+".title")
	text := s.i18n.T(lang, "google.problem."+key+".text")
	text = strings.NewReplacer("{wrong}", in.Wrong, "{account}", in.Account).Replace(text)
	return strings.NewReplacer("{wrong}", in.Wrong, "{account}", in.Account).Replace(title), template.HTML(html.EscapeString(text)) // #nosec G203 -- escaped
}

// handleGoogleSignIn shows the page "Sign in at Google". Looking at it creates
// nothing: the state and the PKCE pair are made when "Open Google" is pressed.
func (s *server) handleGoogleSignIn(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if _, err := s.db.GoogleApp(p.household.ID); errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, googleNewPath, http.StatusSeeOther)
		return
	} else if err != nil {
		s.fail(w, "google app", err)
		return
	}
	in := googleSignIn{Way: r.URL.Query().Get("way")}
	if id := r.URL.Query().Get("connection"); id != "" {
		conn, err := s.db.Connection(p.household.ID, id)
		if err != nil || conn.Kind != "google" {
			http.NotFound(w, r)
			return
		}
		in.ConnectionID, in.Account = conn.ID, conn.Username
	}
	s.renderGoogleSignIn(w, r, p, http.StatusOK, in)
}

// handleGoogleStart is "Open Google": it makes the state and the PKCE pair and
// sends the browser to Google.
func (s *server) handleGoogleStart(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok || !s.googleGate(w, r) {
		return
	}
	start, err := s.startGoogleLogin(r, p, r.PostFormValue("connection"), r.PostFormValue("way"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Redirect(w, r, googleNewPath, http.StatusSeeOther)
		return
	case err != nil:
		s.fail(w, "google sign-in", err)
		return
	}
	http.Redirect(w, r, start.URL, http.StatusSeeOther)
}

// finished answers a finished sign-in: to the connection's page, or with the
// reason in words and what to do next.
func (s *server) finishedGoogle(w http.ResponseWriter, r *http.Request, p principal, out googleOutcome, pasted string) {
	switch out.Problem {
	case "":
		if out.Reconnected {
			s.redirectSaved(w, r, "/connections/"+out.Conn.ID, "flash.google_signed_in")
			return
		}
		http.Redirect(w, r, "/connections/"+out.Conn.ID, http.StatusSeeOther)
	case gpClient:
		app, _ := s.db.GoogleApp(p.household.ID)
		s.renderConnectNew(w, r, p, http.StatusUnprocessableEntity, "google", map[string]any{
			"Error": "google.error.client_rejected", "ErrorField": "client_secret", "GClientID": app.ClientID, "GGuide": true, "GReplace": true,
		})
	default:
		in := googleSignIn{Problem: out.Problem, ConnectionID: out.ForConnection, Wrong: out.Account, Nonce: out.Nonce}
		if out.ForConnection != "" {
			if conn, err := s.db.Connection(p.household.ID, out.ForConnection); err == nil {
				in.Account = conn.Username
			}
		}
		status := http.StatusBadRequest
		if out.Problem == gpWrongAccount {
			status = http.StatusConflict
		}
		s.renderGoogleSignIn(w, r, p, status, in)
	}
}

// handleGoogleCallback is where the relay page sends the browser back to, with
// the code and the state Google handed out.
func (s *server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok || !s.googleGate(w, r) {
		return
	}
	out, err := s.finishGoogleLogin(r.Context(), p, googleauth.CallbackFromQuery(r.URL.Query()))
	if err != nil {
		s.fail(w, "google sign-in", err)
		return
	}
	s.finishedGoogle(w, r, p, out, "")
}

// handleGooglePaste finishes a sign-in on the fallback way: the parent pastes
// the address from the browser's address bar.
func (s *server) handleGooglePaste(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok || !s.googleGate(w, r) {
		return
	}
	address := strings.TrimSpace(r.PostFormValue("address"))
	fieldError := func(key string) {
		in := googleSignIn{Way: "fallback", Address: address, AddressError: key, ConnectionID: r.PostFormValue("connection")}
		if in.ConnectionID != "" {
			if conn, err := s.db.Connection(p.household.ID, in.ConnectionID); err == nil {
				in.Account = conn.Username
			} else {
				in.ConnectionID = ""
			}
		}
		s.renderGoogleSignIn(w, r, p, http.StatusUnprocessableEntity, in)
	}
	cb, err := googleauth.ParseCallbackURL(address)
	if err != nil {
		fieldError("google.paste.error.none")
		return
	}
	out, err := s.finishGoogleLogin(r.Context(), p, cb)
	if err != nil {
		s.fail(w, "google sign-in", err)
		return
	}
	switch out.Problem {
	case gpStateUnknown, gpStateExpired, gpStateUsed:
		fieldError("google.paste.error.old")
	case gpCancelled:
		fieldError("google.paste.error.cancelled")
	default:
		s.finishedGoogle(w, r, p, out, address)
	}
}

// handleGoogleAdopt adds the account a sign-in ended up with as a new
// connection, when it was not the account the connection asked for.
func (s *server) handleGoogleAdopt(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	conn, err := s.adoptStashedGoogle(p, r.PostFormValue("nonce"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Redirect(w, r, googleSignInPath, http.StatusSeeOther)
	case err != nil:
		s.fail(w, "google sign-in", err)
	default:
		http.Redirect(w, r, "/connections/"+conn.ID, http.StatusSeeOther)
	}
}

// googleNotice says which Google connections have to sign in again, for the
// notices on Start and on the calendar connections.
type googleNotice struct {
	Account, Since string
	ConnectionID   string
	SevenDays      bool // the sign-in lasted exactly seven days: the app is still on "Testing"
	Lead, Text     template.HTML
	Reasons        string
	Who            string
}

// googleNotices lists the Google connections that must sign in again.
func (s *server) googleNotices(r *http.Request, p principal) []googleNotice {
	conns, err := s.db.Connections(p.household.ID)
	if err != nil {
		return nil
	}
	lang := s.langOf(r)
	var out []googleNotice
	for _, c := range conns {
		if c.Kind != "google" || c.SignInNeededAt.IsZero() {
			continue
		}
		n := googleNotice{Account: c.Username, ConnectionID: c.ID, Since: p.dateTimeSeconds(c.SignInNeededAt)}
		lived := c.SignInNeededAt.Sub(c.SignedInAt)
		n.SevenDays = !c.SignedInAt.IsZero() && lived >= 7*24*time.Hour-time.Hour && lived <= 7*24*time.Hour+12*time.Hour
		n.Text = accountText(s.i18n.Format(lang, i18n.Msg("google.notice.text", "since", n.Since)), c.Username)
		out = append(out, n)
	}
	return out
}

// googleNoticesOf is googleNotices for one connection.
func (s *server) googleNoticesOf(r *http.Request, p principal, connectionID string) []googleNotice {
	var out []googleNotice
	for _, n := range s.googleNotices(r, p) {
		if n.ConnectionID == connectionID {
			out = append(out, n)
		}
	}
	return out
}

// googleHomeNotices are the notices on the parents' Start; children and
// displays see none.
func (s *server) googleHomeNotices(r *http.Request, person store.Person, h store.Household) []googleNotice {
	if person.Role != store.RoleParent {
		return nil
	}
	return s.googleNotices(r, principal{household: h, person: person, scopes: "write"})
}

// googleFacts are the facts of a Google connection's page.
func (s *server) googleFacts(r *http.Request, p principal, conn store.Connection, last factView) ([]factView, error) {
	app, err := s.googleAppView(r, p)
	if err != nil {
		return nil, err
	}
	signin := factView{Label: s.say(r, i18n.Msg("google.fact.signin"))}
	if conn.SignInNeededAt.IsZero() {
		when := conn.TokenRenewedAt
		if when.IsZero() {
			when = conn.SignedInAt
		}
		signin.Value = s.say(r, i18n.Msg("google.fact.signin.valid", "when", p.dateTimeSeconds(when)))
	} else {
		signin.Flag = &flagView{Kind: "problem", Text: s.say(r, i18n.Msg("google.flag.signin"))}
	}
	if last.Flag == nil && strings.HasPrefix(last.Value, s.say(r, i18n.Msg("sync.never"))) {
		last.Value = s.say(r, i18n.Msg("google.fact.last.never"))
	}
	return []factView{
		{Label: s.say(r, i18n.Msg("connections.fact.service")), Value: s.serviceName(r, conn)},
		{Label: s.say(r, i18n.Msg("google.fact.account")), Value: conn.Username, Host: true},
		signin,
		{Label: s.say(r, i18n.Msg("google.fact.app")), Value: s.say(r, i18n.Msg("google.fact.app.own"))},
		{Label: s.say(r, i18n.Msg("google.fact.client")), Value: app.ClientID, Host: true},
		{Label: s.say(r, i18n.Msg("google.fact.secret")), Value: s.say(r, i18n.Msg("google.fact.secret.value"))},
		last,
	}, nil
}

// googleCard fills in where a calendar of a Google account comes from and what
// the box may do with it, for its card on Choose calendars.
func (s *server) googleCard(r *http.Request, c *selectionCalendar, found []discoveredJSON) {
	var d discoveredJSON
	for _, f := range found {
		if f.URL == c.URL {
			d = f
		}
	}
	switch {
	case d.SharedBy != "":
		c.Source = s.say(r, i18n.Msg("google.card.shared_by", "owner", d.SharedBy))
	case d.Shared:
		c.Source = s.say(r, i18n.Msg("google.card.shared"))
	default:
		c.Source = s.say(r, i18n.Msg("google.card.source"))
	}
	c.Access = d.Access
	if d.Access == "read_only" {
		c.AccessLabel = s.say(r, i18n.Msg("google.acc.ro.label"))
		c.AccessText = s.say(r, i18n.Msg("google.acc.ro."+d.Reason))
		return
	}
	c.AccessLabel, c.AccessText = s.say(r, i18n.Msg("google.acc.rw.label")), s.say(r, i18n.Msg("google.acc.rw.text"))
}
