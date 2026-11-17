package server

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/graph"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Outlook sign-in (ticket 177, ADR 0020). A parent starts a device-code
// sign-in, signs in at Microsoft on the phone or computer, and the box notices:
// every view of the sign-in page (or of its API twin) asks Microsoft once when
// the interval has passed, so no background task is needed and a closed page
// stops asking. The state lives in memory: a code is good for 15 minutes.

const (
	flowWaiting  = "waiting"
	flowOK       = "ok"
	flowExpired  = "expired"
	flowDeclined = "declined"
	flowConsent  = "consent"
	flowWrong    = "wrong_account"
	flowFailed   = "error"
	flowDown     = "down"

	outlookFlowKeep = time.Hour
)

type outlookFlow struct {
	mu                   sync.Mutex
	id, household        string
	ownApp, app          string // the family's own app ID (stored with the connection), the one in use
	reconnect            string // the connection being signed in again, or ""
	signIn               graph.SignIn
	state                string
	started              time.Time
	lastPolled, nextPoll time.Time
	account, other       string // the signed-in account; for a wrong account, the one that signed in
	connectionID         string
	existing             bool // the account was already connected: its sign-in was renewed
}

type outlookFlows struct {
	mu sync.Mutex
	m  map[string]*outlookFlow
}

func (o *outlookFlows) put(f *outlookFlow, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.m == nil {
		o.m = map[string]*outlookFlow{}
	}
	for id, old := range o.m {
		if now.Sub(old.started) > outlookFlowKeep {
			delete(o.m, id)
		}
	}
	o.m[f.id] = f
}

func (o *outlookFlows) get(id, household string) *outlookFlow {
	o.mu.Lock()
	defer o.mu.Unlock()
	if f := o.m[id]; f != nil && f.household == household {
		return f
	}
	return nil
}

var (
	errOutlookNoApp = errors.New("no app registration")
	errOutlookDown  = errors.New("microsoft did not answer")
)

// startOutlookFlow asks Microsoft for a code. reconnectID names the connection
// that signs in again; then its own app ID is the one in use.
func (s *server) startOutlookFlow(ctx context.Context, p principal, ownApp, reconnectID string) (*outlookFlow, error) {
	g := s.sync.Graph()
	if reconnectID != "" {
		conn, err := s.db.Connection(p.household.ID, reconnectID)
		if err != nil {
			return nil, err
		}
		if conn.Kind != "outlook" {
			return nil, store.ErrNotFound
		}
		ownApp = conn.ClientID
	}
	account := ""
	if reconnectID != "" {
		if conn, err := s.db.Connection(p.household.ID, reconnectID); err == nil {
			account = conn.Username
		}
	}
	app := g.AppID(ownApp)
	if app == "" {
		return nil, errOutlookNoApp
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	si, err := g.StartSignIn(ctx, app)
	switch {
	case errors.Is(err, graph.ErrUnknownApp):
		return nil, &inputError{"client_id", "Microsoft does not know this app ID"}
	case err != nil:
		s.logger.Warn("outlook sign-in could not start", "err", err)
		return nil, errOutlookDown
	}
	f := &outlookFlow{id: store.NewID(), household: p.household.ID, ownApp: strings.TrimSpace(ownApp), app: app, reconnect: reconnectID, account: account,
		signIn: si, state: flowWaiting, started: s.now(), nextPoll: s.now()}
	s.outlook.put(f, s.now())
	return f, nil
}

// pollOutlookFlow asks Microsoft whether the person has signed in, if the
// interval since the last question has passed, and settles the flow.
func (s *server) pollOutlookFlow(ctx context.Context, p principal, f *outlookFlow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := s.now()
	if f.state != flowWaiting {
		return
	}
	if now.After(f.signIn.ExpiresAt) {
		f.state = flowExpired
		return
	}
	if now.Before(f.nextPoll) {
		return
	}
	g := s.sync.Graph()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tok, err := g.PollSignIn(ctx, f.app, f.signIn)
	f.lastPolled, f.nextPoll = now, now.Add(f.signIn.Interval)
	switch {
	case err == nil:
		s.finishOutlookFlow(ctx, p, f, tok)
	case errors.Is(err, graph.ErrPending):
	case errors.Is(err, graph.ErrSlowDown):
		f.signIn.Interval += 5 * time.Second
		f.nextPoll = now.Add(f.signIn.Interval)
	case errors.Is(err, graph.ErrDeclined):
		f.state = flowDeclined
	case errors.Is(err, graph.ErrExpired):
		f.state = flowExpired
	case errors.Is(err, graph.ErrConsent):
		f.state = flowConsent
	default:
		s.logger.Warn("outlook sign-in poll failed", "err", err)
		// A network error is no verdict on the code: ask again next time.
	}
}

// finishOutlookFlow names the account and stores the sign-in: a new connection,
// the renewed sign-in of one the account already has, or, when reconnecting, of
// the connection being reconnected (only for the same account).
func (s *server) finishOutlookFlow(ctx context.Context, p principal, f *outlookFlow, tok graph.Token) {
	refresh := tok.Refresh
	sess := s.sync.Graph().Session(f.app, tok.Refresh, func(r string) { refresh = r })
	me, err := sess.Me(ctx)
	if err != nil {
		s.logger.Warn("outlook sign-in: cannot read the account", "err", err)
		f.state = flowFailed
		return
	}
	now := s.now()
	if f.reconnect != "" {
		conn, err := s.db.Connection(p.household.ID, f.reconnect)
		if err != nil {
			f.state = flowFailed
			return
		}
		if !strings.EqualFold(conn.Username, me.Address) {
			f.state, f.account, f.other = flowWrong, conn.Username, me.Address
			return
		}
		if err := s.db.Reconnected(p.household.ID, conn.ID, refresh, now); err != nil {
			s.logger.Error("outlook reconnect", "err", err)
			f.state = flowFailed
			return
		}
		f.state, f.account, f.connectionID, f.existing = flowOK, conn.Username, conn.ID, true
		return
	}
	f.account = me.Address
	if conn, err := s.db.FindOutlookConnection(p.household.ID, me.Address); err == nil {
		if err := s.db.Reconnected(p.household.ID, conn.ID, refresh, now); err != nil {
			s.logger.Error("outlook reconnect", "err", err)
			f.state = flowFailed
			return
		}
		f.state, f.connectionID, f.existing = flowOK, conn.ID, true
		return
	}
	conn, err := s.db.CreateOutlookConnection(p.household.ID, store.NewOutlookConnection{Account: me.Address, ClientID: f.ownApp, RefreshToken: refresh, Now: now})
	if err != nil {
		s.logger.Error("outlook connection", "err", err)
		f.state = flowFailed
		return
	}
	f.state, f.connectionID = flowOK, conn.ID
}

// outlookGuide is what the connect page needs to show the Outlook guide.
func (s *server) outlookGuide(extra map[string]any, own string) map[string]any {
	configured := s.sync.Graph().DefaultClientID != ""
	extra["OutlookConfigured"] = configured
	extra["OutlookApp"] = own
	return extra
}

func (s *server) handleOutlookStart(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	own := strings.TrimSpace(r.PostFormValue("client_id"))
	f, err := s.startOutlookFlow(r.Context(), p, own, "")
	var bad *inputError
	switch {
	case errors.Is(err, errOutlookNoApp):
		s.renderConnectNew(w, r, p, http.StatusUnprocessableEntity, "outlook", map[string]any{"Error": "outlook.error.no_app", "OutlookApp": own, "OutlookOpen": true})
		return
	case errors.As(err, &bad):
		s.renderConnectNew(w, r, p, http.StatusUnprocessableEntity, "outlook", map[string]any{"Error": "outlook.error.unknown_app", "ErrorField": "client_id", "OutlookApp": own, "OutlookOpen": true})
		return
	case errors.Is(err, errOutlookDown):
		s.renderConnectNew(w, r, p, http.StatusBadGateway, "outlook", map[string]any{"Error": "outlook.error.down", "OutlookApp": own, "OutlookOpen": own != ""})
		return
	case err != nil:
		s.fail(w, "outlook sign-in", err)
		return
	}
	http.Redirect(w, r, "/connections/outlook/"+f.id, http.StatusSeeOther)
}

func (s *server) handleOutlookReconnect(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	conn, err := s.db.Connection(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && conn.Kind != "outlook") {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "connection", err)
		return
	}
	f, err := s.startOutlookFlow(r.Context(), p, "", conn.ID)
	if err != nil {
		key := "outlook.error.down"
		if errors.Is(err, errOutlookNoApp) {
			key = "outlook.error.no_app"
		}
		stored, serr := s.storedCalendars(p, conn)
		if serr != nil {
			s.fail(w, "synced calendars", serr)
			return
		}
		s.renderSelection(w, r, p, http.StatusBadGateway, conn, stored, nil, key, "", "")
		return
	}
	http.Redirect(w, r, "/connections/outlook/"+f.id, http.StatusSeeOther)
}

func (s *server) handleOutlookFlowNew(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	old := s.outlook.get(r.PathValue("flow"), p.household.ID)
	if old == nil {
		http.Redirect(w, r, "/connections/new?service=outlook", http.StatusSeeOther)
		return
	}
	f, err := s.startOutlookFlow(r.Context(), p, old.ownApp, old.reconnect)
	if err != nil {
		old.mu.Lock()
		old.state = flowDown
		old.mu.Unlock()
		s.renderOutlookFlow(w, r, p, http.StatusBadGateway, old)
		return
	}
	http.Redirect(w, r, "/connections/outlook/"+f.id, http.StatusSeeOther)
}

func (s *server) handleOutlookFlow(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	f := s.outlook.get(r.PathValue("flow"), p.household.ID)
	if f == nil {
		http.Redirect(w, r, "/connections/new?service=outlook", http.StatusSeeOther)
		return
	}
	s.pollOutlookFlow(r.Context(), p, f)
	s.renderOutlookFlow(w, r, p, http.StatusOK, f)
}

// spaced writes a code letter by letter, so a screen reader spells it.
func spaced(code string) string {
	return strings.Join(strings.Split(code, ""), " ")
}

// connectionTarget is where a finished sign-in leads: Choose calendars of the connection.
func connectionTarget(f *outlookFlow) string {
	q := "new"
	if f.reconnect != "" || f.existing {
		q = "again"
	}
	return "/connections/" + f.connectionID + "?connected=" + q
}

func (s *server) renderOutlookFlow(w http.ResponseWriter, r *http.Request, p principal, status int, f *outlookFlow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	lang := s.langOf(r)
	clock := func(t time.Time) string { return datefmt.Clock(p.household.TimeFormat, t.In(p.location())) }
	uri := f.signIn.VerificationURI
	host := strings.TrimPrefix(strings.TrimPrefix(uri, "https://"), "http://")
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "State": f.state, "Reconnect": f.reconnect != "", "Account": f.account, "Other": f.other,
		"Code": f.signIn.UserCode, "CodeSpaced": spaced(f.signIn.UserCode), "VerifyURL": uri, "VerifyHost": host,
		"ValidUntil": clock(f.signIn.ExpiresAt), "NewAction": "/connections/outlook/" + f.id + "/new", "AppID": f.app, "Scopes": strings.Join(graph.Scopes, ", "),
		"CancelHref": "/connections/new?service=outlook", "ConnectionID": f.reconnect,
	}
	if !f.lastPolled.IsZero() {
		data["LastPolled"] = datefmt.ClockSeconds(p.household.TimeFormat, f.lastPolled.In(p.location()))
	} else {
		data["LastPolled"] = datefmt.ClockSeconds(p.household.TimeFormat, s.now().In(p.location()))
	}
	if f.reconnect != "" {
		data["CancelHref"] = "/calendars"
	}
	switch f.state {
	case flowWaiting:
		data["Refresh"] = intervalSeconds(f.signIn.Interval)
	case flowOK:
		data["Refresh"] = "2;url=" + connectionTarget(f)
	}
	_ = lang
	s.render(w, r, status, "outlook_signin.html", data)
}

func intervalSeconds(d time.Duration) string {
	n := int(d.Seconds())
	if n < 3 {
		n = 3
	}
	return itoa(n)
}

func itoa(n int) string { return strconv.Itoa(n) }

// ---- the reconnect notice (Q37) ----

type reconnectItem struct {
	ID, Account, Since string
}

// reconnectNotice is what the calendars page and the parents' home show when a
// Microsoft sign-in has ended: every account on its own, never a count.
type reconnectNotice struct {
	Items []reconnectItem
	Intro template.HTML // the sentence above the list, for several accounts; for one, the whole text
	Many  bool
	Who   string // who may sign in again
}

// boldedText formats a message in which the named values stand out as the
// notice's host name does. Everything else is escaped.
func (s *server) boldedText(r *http.Request, key string, kv ...string) template.HTML {
	args := make([]string, 0, len(kv))
	marks := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		mark := "\x00" + kv[i] + "\x00"
		args = append(args, kv[i], mark)
		marks[mark] = kv[i+1]
	}
	text := html.EscapeString(s.say(r, i18n.Msg(key, args...)))
	for mark, v := range marks {
		text = strings.Replace(text, html.EscapeString(mark), `<b class="host">`+html.EscapeString(v)+`</b>`, 1)
	}
	return template.HTML(text)
}

func (s *server) reconnectNotice(r *http.Request, p principal) (*reconnectNotice, error) {
	if p.person.Role != store.RoleParent {
		return nil, nil
	}
	need, err := s.db.ReauthConnections(p.household.ID)
	if err != nil || len(need) == 0 {
		return nil, err
	}
	n := &reconnectNotice{Who: s.say(r, i18n.Msg("outlook.reconnect.who"))}
	for _, c := range need {
		n.Items = append(n.Items, reconnectItem{ID: c.ID, Account: c.Username, Since: p.dateTimeSeconds(c.ReauthSince)})
	}
	if len(need) == 1 {
		n.Intro = s.boldedText(r, "outlook.reconnect.one", "account", need[0].Username, "since", p.dateTimeSeconds(need[0].ReauthSince))
		return n, nil
	}
	n.Many = true
	n.Intro = template.HTML(html.EscapeString(s.say(r, i18n.Msg("outlook.reconnect.many", "count", s.countWord(r, len(need))))))
	return n, nil
}

func (s *server) countWord(r *http.Request, n int) string {
	if n >= 2 && n <= 10 {
		return s.i18n.T(s.langOf(r), "outlook.count."+itoa(n))
	}
	return itoa(n)
}

// ---- API ----

type signInJSON struct {
	ID              string   `json:"id"`
	State           string   `json:"state"`
	UserCode        string   `json:"user_code,omitempty"`
	VerificationURI string   `json:"verification_uri,omitempty"`
	ExpiresAt       string   `json:"expires_at,omitempty"`
	IntervalSeconds int      `json:"interval_seconds,omitempty"`
	LastPolledAt    string   `json:"last_polled_at,omitempty"`
	Account         string   `json:"account,omitempty"`
	OtherAccount    string   `json:"other_account,omitempty"`
	ConnectionID    string   `json:"connection_id,omitempty"`
	AppID           string   `json:"app_id,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
}

func apiState(state string) string {
	switch state {
	case flowOK:
		return "signed_in"
	case flowConsent:
		return "consent_required"
	case flowDown:
		return "error"
	}
	return state
}

func signInOf(f *outlookFlow) signInJSON {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := signInJSON{ID: f.id, State: apiState(f.state), Account: f.account, OtherAccount: f.other, ConnectionID: f.connectionID}
	if f.state == flowWaiting {
		j.UserCode, j.VerificationURI = f.signIn.UserCode, f.signIn.VerificationURI
		j.ExpiresAt, j.IntervalSeconds = f.signIn.ExpiresAt.UTC().Format(time.RFC3339), int(f.signIn.Interval.Seconds())
	}
	if !f.lastPolled.IsZero() {
		j.LastPolledAt = f.lastPolled.UTC().Format(time.RFC3339)
	}
	if f.state == flowConsent {
		j.AppID, j.Scopes = f.app, graph.Scopes
	}
	return j
}

func (s *server) apiStartSignIn(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		ClientID     string `json:"client_id"`
		ConnectionID string `json:"connection_id"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}
	f, err := s.startOutlookFlow(r.Context(), p, in.ClientID, in.ConnectionID)
	var bad *inputError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "connection not found")
	case errors.Is(err, errOutlookNoApp):
		writeError(w, http.StatusUnprocessableEntity, "client_id: no app registration is configured; give your own app ID")
	case errors.As(err, &bad):
		writeError(w, http.StatusUnprocessableEntity, "client_id: Microsoft does not know this app ID")
	case errors.Is(err, errOutlookDown):
		writeError(w, http.StatusBadGateway, "Microsoft did not answer")
	case err != nil:
		s.fail(w, "outlook sign-in", err)
	default:
		writeJSON(w, http.StatusCreated, signInOf(f))
	}
}

func (s *server) apiGetSignIn(w http.ResponseWriter, r *http.Request, p principal) {
	f := s.outlook.get(r.PathValue("id"), p.household.ID)
	if f == nil {
		writeError(w, http.StatusNotFound, "sign-in not found")
		return
	}
	s.pollOutlookFlow(r.Context(), p, f)
	writeJSON(w, http.StatusOK, signInOf(f))
}

// ---- discovery and the connection page ----

// rediscoverOutlook lists the account's calendars, marking those already on the box.
func (s *server) rediscoverOutlook(ctx context.Context, p principal, conn store.Connection) ([]discoveredJSON, error) {
	ctx, cancel := context.WithTimeout(ctx, discoverTimeoutSec*time.Second)
	defer cancel()
	found, err := s.sync.DiscoverOutlook(ctx, p.household.ID, conn.ID)
	if err != nil {
		if errors.Is(err, graph.ErrReauth) {
			return nil, &inputError{"reauth", "the Microsoft sign-in has ended"}
		}
		return nil, &inputError{"server", "could not read calendars from Microsoft: " + err.Error()}
	}
	synced, err := s.db.SyncedCalendars(p.household.ID)
	if err != nil {
		return nil, err
	}
	have := map[string]string{}
	for _, c := range calendarsOf(synced, conn.ID) {
		have[c.URL] = c.ID
	}
	out := make([]discoveredJSON, 0, len(found))
	for _, f := range found {
		id, added := have[f.ID]
		out = append(out, discoveredJSON{URL: f.ID, Name: f.Name, Added: added, CalendarID: id, ReadOnly: f.Locked, SharedBy: f.SharedBy})
	}
	return out, nil
}

// outlookCard is what a card of Choose calendars says about who may change the
// calendar, in words.
type outlookCard struct{ Kicker, Access, AccessKind string }

func (s *server) outlookCards(r *http.Request, found []discoveredJSON, synced []store.SyncedCalendar) map[string]outlookCard {
	byURL := map[string]store.SyncedCalendar{}
	for _, c := range synced {
		byURL[c.URL] = c
	}
	out := map[string]outlookCard{}
	for _, f := range found {
		c := outlookCard{Kicker: s.say(r, i18n.Msg("outlook.card.source")), Access: s.say(r, i18n.Msg("outlook.acc.rw")), AccessKind: "rw"}
		locked, sharedBy := f.ReadOnly, f.SharedBy
		if st, ok := byURL[f.URL]; ok && f.fromStore {
			locked, sharedBy = st.SourceLocked, st.SourceOwner
		}
		switch {
		case sharedBy != "":
			c.Kicker = s.say(r, i18n.Msg("outlook.card.shared_by", "who", sharedBy))
			c.Access, c.AccessKind = s.say(r, i18n.Msg("outlook.acc.shared", "who", sharedByName(sharedBy))), "ro"
		case locked:
			c.Access, c.AccessKind = s.say(r, i18n.Msg("outlook.acc.locked")), "ro"
		default:
			if st, ok := byURL[f.URL]; ok && st.ReadOnly {
				c.Access, c.AccessKind = s.say(r, i18n.Msg("outlook.acc.box_ro")), "ro"
			}
		}
		out[f.URL] = c
	}
	return out
}

// sharedByName is the name in "Name (address)".
func sharedByName(s string) string {
	if i := strings.LastIndex(s, " ("); i > 0 {
		return s[:i]
	}
	return s
}

// outlookFacts are the facts at the top of an Outlook connection's page.
func (s *server) outlookFacts(r *http.Request, p principal, conn store.Connection, last time.Time) []factView {
	login := factView{Label: s.say(r, i18n.Msg("outlook.fact.login"))}
	if conn.ReauthSince.IsZero() {
		login.Value = s.say(r, i18n.Msg("outlook.fact.login_ok", "when", p.dateTimeSeconds(conn.RenewedAt)))
	} else {
		login.Flag = &flagView{Kind: "problem", Text: s.say(r, i18n.Msg("outlook.fact.login_ended", "when", p.dateTimeSeconds(conn.ReauthSince)))}
	}
	app := s.say(r, i18n.Msg("outlook.fact.app_project"))
	if conn.ClientID != "" {
		app = s.say(r, i18n.Msg("outlook.fact.app_own", "id", conn.ClientID))
	}
	lastFact := factView{Label: s.say(r, i18n.Msg("connections.fact.last")), Value: s.say(r, i18n.Msg("sync.never"))}
	if !last.IsZero() {
		lastFact.Value = p.when(last)
	}
	return []factView{
		{Label: s.say(r, i18n.Msg("connections.fact.service")), Value: s.say(r, i18n.Msg("connections.provider.outlook"))},
		{Label: s.say(r, i18n.Msg("outlook.fact.account")), Value: conn.Username},
		login,
		{Label: s.say(r, i18n.Msg("outlook.fact.app")), Value: app},
		lastFact,
	}
}

// outlookDisconnect lists what a disconnect removes: every calendar by name and by its name in Outlook.
type outlookGone struct{ Name, Source string }

func outlookGoneOf(cals []store.SyncedCalendar) []outlookGone {
	var out []outlookGone
	for _, c := range cals {
		out = append(out, outlookGone{c.Name, c.SourceName})
	}
	return out
}

// outlookInvite is what the page of an invitation says: why it is read-only, the
// link to the event in Outlook, and the facts only Outlook knows.
type outlookInviteView struct {
	Organizer, Response, WebLink, Calendar string
	Who                                    []personChip
	Whole                                  bool
}

func (s *server) outlookInvite(r *http.Request, c *calendarContext, master calendar.Event, occ calendar.Occurrence) outlookInviteView {
	inv := master.Invitation
	v := outlookInviteView{Organizer: inv.Organizer, WebLink: inv.WebLink, Calendar: c.calendarName(occ.CalendarID)}
	if inv.Response != "" {
		v.Response = s.say(r, i18n.Msg("outlook.response."+inv.Response))
		if strings.HasPrefix(v.Response, "outlook.response.") {
			v.Response = ""
		}
	}
	if cal, err := s.db.SyncedCalendar(c.p.household.ID, occ.CalendarID); err == nil {
		if conn, err := s.db.Connection(c.p.household.ID, cal.ConnectionID); err == nil {
			v.Calendar = s.say(r, i18n.Msg("outlook.invite.calendar", "name", v.Calendar, "account", conn.Username))
		}
	}
	people, _, whole := audienceOf(calendar.Audience{People: occ.People, Groups: occ.Groups}, c.chips, c.groups)
	v.Who, v.Whole = people, whole
	return v
}

// internalLink is [text](/path), a link inside the box, in an escaped text.
var internalLink = regexp.MustCompile(`\[([^\]]+)\]\((/[^\s)]*)\)`)

// withHost is rich with named values (an address) shown as the bold,
// breakable host of the Outlook pages. Pairs: placeholder, value.
func withHost(text string, kv ...string) template.HTML {
	text = string(rich(text))
	for i := 0; i+1 < len(kv); i += 2 {
		text = strings.Replace(text, html.EscapeString(kv[i]), `<b class="host">`+html.EscapeString(kv[i+1])+`</b>`, 1)
	}
	text = internalLink.ReplaceAllString(text, `<a href="$2">$1</a>`)
	return template.HTML(text) // #nosec G203 -- rich escapes the text, the values are escaped above
}
