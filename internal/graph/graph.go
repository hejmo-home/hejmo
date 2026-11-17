// Package graph talks to Microsoft Graph v1.0 and the Microsoft identity
// platform for Outlook calendars (ADR 0020). It uses net/http and
// golang.org/x/oauth2 with hand-written types, not the Graph SDK. Sign-in is
// the device-code flow of a public client: the box holds no secret.
package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// DefaultClientID is the app ID of the Hejmo project's app registration. It is
// set at build time (-ldflags "-X .../internal/graph.DefaultClientID=...") or
// by the box's configuration; empty means the project has not created the
// registration yet, and a family must enter its own.
var DefaultClientID = ""

// Scopes are what the box asks Microsoft for. Calendars.ReadWrite is needed to
// write back; offline_access keeps the sign-in; User.Read names the account.
var Scopes = []string{"Calendars.ReadWrite", "offline_access", "User.Read"}

// Defaults for the two Microsoft hosts.
const (
	DefaultAuthBase  = "https://login.microsoftonline.com"
	DefaultGraphBase = "https://graph.microsoft.com/v1.0"
	// Tenant "common" accepts personal and work or school accounts.
	tenant = "common"
)

// Errors the sign-in and the calls tell apart.
var (
	ErrPending      = errors.New("graph: sign-in is waiting for the person")
	ErrSlowDown     = errors.New("graph: asked too often")
	ErrDeclined     = errors.New("graph: the person declined")
	ErrExpired      = errors.New("graph: the code expired")
	ErrConsent      = errors.New("graph: an administrator must approve the app")
	ErrUnknownApp   = errors.New("graph: Microsoft does not know this app ID")
	ErrReauth       = errors.New("graph: the sign-in is no longer valid")
	ErrDeltaExpired = errors.New("graph: the delta link is no longer valid")
	ErrNotFound     = errors.New("graph: not found")
)

// StatusError is an unexpected answer from Graph.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string { return fmt.Sprintf("graph: %d %s", e.Code, e.Body) }

// Client holds the addresses and the HTTP client; the zero value talks to
// Microsoft.
type Client struct {
	AuthBase, GraphBase string
	HTTP                *http.Client
	// DefaultClientID is the project's app ID, used when a family entered none.
	DefaultClientID string
}

// New returns a client for Microsoft with the project's default app ID.
func New() *Client { return &Client{DefaultClientID: DefaultClientID} }

func (c *Client) auth() string {
	if c.AuthBase != "" {
		return strings.TrimRight(c.AuthBase, "/")
	}
	return DefaultAuthBase
}

func (c *Client) graph() string {
	if c.GraphBase != "" {
		return strings.TrimRight(c.GraphBase, "/")
	}
	return DefaultGraphBase
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// AppID is the app ID to use: the family's own, else the project's. Empty
// means none is configured.
func (c *Client) AppID(own string) string {
	if own = strings.TrimSpace(own); own != "" {
		return own
	}
	return c.DefaultClientID
}

func (c *Client) config(clientID string) *oauth2.Config {
	return &oauth2.Config{ClientID: clientID, Scopes: Scopes, Endpoint: oauth2.Endpoint{
		DeviceAuthURL: c.auth() + "/" + tenant + "/oauth2/v2.0/devicecode",
		TokenURL:      c.auth() + "/" + tenant + "/oauth2/v2.0/token",
		AuthStyle:     oauth2.AuthStyleInParams,
	}}
}

func (c *Client) ctx(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, c.http())
}

// SignIn is a device code Microsoft handed out.
type SignIn struct {
	DeviceCode, UserCode, VerificationURI string
	ExpiresAt                             time.Time
	Interval                              time.Duration
}

// Token is what a finished sign-in gives; only the refresh token is kept.
type Token struct{ Refresh, Access string }

// StartSignIn asks Microsoft for a device code.
func (c *Client) StartSignIn(ctx context.Context, clientID string) (SignIn, error) {
	res, err := c.config(clientID).DeviceAuth(c.ctx(ctx))
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && hasCode(re.Body, 700016, 700020, 90002) {
			return SignIn{}, ErrUnknownApp
		}
		return SignIn{}, err
	}
	iv := time.Duration(res.Interval) * time.Second
	if iv <= 0 {
		iv = 5 * time.Second
	}
	return SignIn{DeviceCode: res.DeviceCode, UserCode: res.UserCode, VerificationURI: res.VerificationURI, ExpiresAt: res.Expiry, Interval: iv}, nil
}

type tokenReply struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
	Codes        []int  `json:"error_codes"`
}

func hasCode(body []byte, want ...int) bool {
	var r tokenReply
	if json.Unmarshal(body, &r) != nil {
		return false
	}
	for _, c := range r.Codes {
		for _, w := range want {
			if c == w {
				return true
			}
		}
	}
	return false
}

// PollSignIn asks once whether the person has signed in. It returns
// ErrPending while they have not, and ErrDeclined, ErrExpired or ErrConsent
// when the sign-in cannot succeed.
func (c *Client) PollSignIn(ctx context.Context, clientID string, s SignIn) (Token, error) {
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {clientID}, "device_code": {s.DeviceCode}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config(clientID).Endpoint.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http().Do(req)
	if err != nil {
		return Token{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var r tokenReply
	_ = json.Unmarshal(body, &r)
	if res.StatusCode == http.StatusOK && r.RefreshToken != "" {
		return Token{Refresh: r.RefreshToken, Access: r.AccessToken}, nil
	}
	switch {
	case r.Error == "authorization_pending":
		return Token{}, ErrPending
	case r.Error == "slow_down":
		return Token{}, ErrSlowDown
	case r.Error == "authorization_declined" || hasCode(body, 65004):
		return Token{}, ErrDeclined
	case r.Error == "expired_token" || r.Error == "bad_verification_code":
		return Token{}, ErrExpired
	case r.Error == "consent_required" || hasCode(body, 65001, 90094, 65002, 500011):
		return Token{}, ErrConsent
	}
	return Token{}, &StatusError{Code: res.StatusCode, Body: r.Error}
}

// Account is the signed-in Microsoft account.
type Account struct{ Name, Address string }

// Session is one signed-in account. It refreshes its access token itself and
// reports a rotated refresh token through saved, so the box never loses the
// sign-in. It is safe for one sync at a time.
type Session struct {
	c        *Client
	clientID string
	saved    func(refresh string)

	mu      sync.Mutex
	refresh string
	tok     *oauth2.Token
}

// Session opens a session from a stored refresh token.
func (c *Client) Session(clientID, refresh string, saved func(refresh string)) *Session {
	return &Session{c: c, clientID: clientID, refresh: refresh, saved: saved}
}

func (s *Session) token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok != nil && s.tok.Valid() {
		return s.tok.AccessToken, nil
	}
	t, err := s.c.config(s.clientID).TokenSource(s.c.ctx(ctx), &oauth2.Token{RefreshToken: s.refresh}).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && (re.ErrorCode == "invalid_grant" || re.ErrorCode == "interaction_required" || re.ErrorCode == "consent_required" || re.ErrorCode == "unauthorized_client") {
			return "", fmt.Errorf("%w: %s", ErrReauth, re.ErrorCode)
		}
		return "", err
	}
	s.tok = t
	if t.RefreshToken != "" && t.RefreshToken != s.refresh {
		s.refresh = t.RefreshToken
		if s.saved != nil {
			s.saved(t.RefreshToken)
		}
	}
	return t.AccessToken, nil
}

// do calls Graph. target is a path below the Graph base, or a full link Graph
// handed out (only accepted below the Graph base, so the token goes nowhere else).
func (s *Session) do(ctx context.Context, method, target string, in, out any) error {
	full := target
	if !strings.HasPrefix(target, "http") {
		full = s.c.graph() + target
	} else if !strings.HasPrefix(target, s.c.graph()+"/") {
		return fmt.Errorf("graph: refusing to follow a link outside %s", s.c.graph())
	}
	tok, err := s.token(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	// Immutable ids stay the same when an event moves between calendars; the
	// body comes as plain text; times keep the event's own zone.
	req.Header.Set("Prefer", `IdType="ImmutableId", outlook.body-content-type="text", odata.maxpagesize=100`)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.c.http().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case res.StatusCode == http.StatusGone:
		return ErrDeltaExpired
	case res.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%w: Graph refused the token", ErrReauth)
	case res.StatusCode >= 300:
		return &StatusError{Code: res.StatusCode, Body: truncate(string(data), 200)}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Me names the signed-in account.
func (s *Session) Me(ctx context.Context) (Account, error) {
	var me struct {
		DisplayName       string `json:"displayName"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := s.do(ctx, http.MethodGet, "/me", nil, &me); err != nil {
		return Account{}, err
	}
	addr := me.Mail
	if addr == "" {
		addr = me.UserPrincipalName
	}
	return Account{Name: me.DisplayName, Address: addr}, nil
}

// Calendar is a calendar of the account, own or shared with it.
type Calendar struct {
	ID, Name, OwnerName, OwnerAddress string
	CanEdit                           bool
}

// Calendars lists the account's calendars.
func (s *Session) Calendars(ctx context.Context) ([]Calendar, error) {
	var out []Calendar
	next := "/me/calendars"
	for pages := 0; next != "" && pages < 20; pages++ {
		var page struct {
			Value []struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				CanEdit bool   `json:"canEdit"`
				Owner   struct {
					Name    string `json:"name"`
					Address string `json:"address"`
				} `json:"owner"`
			} `json:"value"`
			Next string `json:"@odata.nextLink"`
		}
		if err := s.do(ctx, http.MethodGet, next, nil, &page); err != nil {
			return nil, err
		}
		for _, v := range page.Value {
			out = append(out, Calendar{ID: v.ID, Name: v.Name, CanEdit: v.CanEdit, OwnerName: v.Owner.Name, OwnerAddress: v.Owner.Address})
		}
		next = page.Next
	}
	return out, nil
}

// DeltaResult is everything a delta run returned and the link to continue from.
type DeltaResult struct {
	Events []Event
	Link   string
}

// Delta lists the changes of a calendar's events in the window. With an empty
// link it starts over (every event in the window); with the link of an earlier
// run it returns only what changed since, removals included.
func (s *Session) Delta(ctx context.Context, calendarID, link string, from, to time.Time) (DeltaResult, error) {
	next := link
	if next == "" {
		q := url.Values{"startDateTime": {from.UTC().Format(time.RFC3339)}, "endDateTime": {to.UTC().Format(time.RFC3339)}}
		next = "/me/calendars/" + url.PathEscape(calendarID) + "/calendarView/delta?" + q.Encode()
	}
	var out DeltaResult
	for pages := 0; pages < 500; pages++ {
		var page struct {
			Value []Event `json:"value"`
			Next  string  `json:"@odata.nextLink"`
			Delta string  `json:"@odata.deltaLink"`
		}
		if err := s.do(ctx, http.MethodGet, next, nil, &page); err != nil {
			return DeltaResult{}, err
		}
		out.Events = append(out.Events, page.Value...)
		if page.Next == "" {
			out.Link = page.Delta
			return out, nil
		}
		next = page.Next
	}
	return DeltaResult{}, errors.New("graph: delta did not end")
}

// GetEvent reads one event (a series master has its recurrence).
func (s *Session) GetEvent(ctx context.Context, id string) (Event, error) {
	var e Event
	err := s.do(ctx, http.MethodGet, "/me/events/"+url.PathEscape(id), nil, &e)
	return e, err
}

// Instances lists the occurrences and exceptions of a series in the window.
func (s *Session) Instances(ctx context.Context, masterID string, from, to time.Time) ([]Event, error) {
	q := url.Values{"startDateTime": {from.UTC().Format(time.RFC3339)}, "endDateTime": {to.UTC().Format(time.RFC3339)}}
	var out []Event
	next := "/me/events/" + url.PathEscape(masterID) + "/instances?" + q.Encode()
	for pages := 0; next != "" && pages < 100; pages++ {
		var page struct {
			Value []Event `json:"value"`
			Next  string  `json:"@odata.nextLink"`
		}
		if err := s.do(ctx, http.MethodGet, next, nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Value...)
		next = page.Next
	}
	return out, nil
}

// CreateEvent writes a new event into a calendar.
func (s *Session) CreateEvent(ctx context.Context, calendarID string, body map[string]any) (Event, error) {
	var e Event
	err := s.do(ctx, http.MethodPost, "/me/calendars/"+url.PathEscape(calendarID)+"/events", body, &e)
	return e, err
}

// UpdateEvent changes the named fields of an event.
func (s *Session) UpdateEvent(ctx context.Context, id string, body map[string]any) (Event, error) {
	var e Event
	err := s.do(ctx, http.MethodPatch, "/me/events/"+url.PathEscape(id), body, &e)
	return e, err
}

// DeleteEvent removes an event (or one occurrence of a series).
func (s *Session) DeleteEvent(ctx context.Context, id string) error {
	return s.do(ctx, http.MethodDelete, "/me/events/"+url.PathEscape(id), nil, nil)
}

// AudienceExtension is the name of the hidden open extension that carries an
// event's audience (who it is for) on the box's side of an Outlook event.
const AudienceExtension = "com.hejmo.audience"

// SetAudience writes the hidden audience extension of an event: it is created,
// or changed if it already exists.
func (s *Session) SetAudience(ctx context.Context, eventID, value string) error {
	ext := map[string]any{"@odata.type": "microsoft.graph.openTypeExtension", "extensionName": AudienceExtension, "audience": value}
	err := s.do(ctx, http.MethodPost, "/me/events/"+url.PathEscape(eventID)+"/extensions", ext, nil)
	var se *StatusError
	if errors.As(err, &se) && (se.Code == http.StatusConflict || se.Code == http.StatusBadRequest) {
		return s.do(ctx, http.MethodPatch, "/me/events/"+url.PathEscape(eventID)+"/extensions/"+AudienceExtension, map[string]any{"audience": value}, nil)
	}
	return err
}

// CountEvents counts a calendar's events the way Outlook keeps them: a series
// is one event. Birthdays and holidays count as they are.
func (s *Session) CountEvents(ctx context.Context, calendarID string, since time.Time) (int, error) {
	q := url.Values{"$count": {"true"}, "$top": {"1"}, "$select": {"id"},
		"$filter": {"type eq 'seriesMaster' or end/dateTime ge '" + since.UTC().Format("2006-01-02T15:04:05") + "'"}}
	var out struct {
		Count int `json:"@odata.count"`
	}
	err := s.do(ctx, http.MethodGet, "/me/calendars/"+url.PathEscape(calendarID)+"/events?"+q.Encode(), nil, &out)
	return out.Count, err
}
