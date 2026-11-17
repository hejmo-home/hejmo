// Package googleauth is the box's side of signing in at Google (ADR 0013): the
// authorisation address with PKCE, the state that carries the box's address
// through Google and the relay page, the exchange of the code and the renewal
// of the access token, and the list of the account's calendars. It knows
// nothing of the box's database; every request goes through the HTTP client it
// is given, which the box takes from the gate to the outside.
package googleauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The fixed values (ADR 0013, ticket 18): public addresses, no credentials.
const (
	// RelayRedirectURI is the static page in repository hejmo-home/hejmo-home.github.io.
	RelayRedirectURI = "https://hejmo-home.github.io/oauth/"
	// FallbackRedirectURI is where Google sends the browser when the relay page cannot be used: the
	// browser shows "cannot connect" and the parent pastes the address. Google needs an exact match.
	FallbackRedirectURI = "http://127.0.0.1:8099/"
	// Scope gives the Calendar API and the CalDAV API.
	Scope = "https://www.googleapis.com/auth/calendar"
	// CalDAVBase is where Google's CalDAV API lives; a calendar is BASE/<id>/events.
	CalDAVBase = "https://apidata.googleusercontent.com/caldav/v2/"
	// StateLifetime is how long a sign-in may take.
	StateLifetime = 15 * time.Minute
)

// Endpoints are the addresses of Google's services; tests point them at a fake.
type Endpoints struct{ Auth, Token, CalendarList string }

// DefaultEndpoints are Google's own.
var DefaultEndpoints = Endpoints{ // #nosec G101 -- public addresses of Google, no credential
	Auth:         "https://accounts.google.com/o/oauth2/v2/auth",
	Token:        "https://oauth2.googleapis.com/token",
	CalendarList: "https://www.googleapis.com/calendar/v3/users/me/calendarList",
}

// Errors the caller tells apart.
var (
	ErrInvalidGrant   = errors.New("googleauth: Google rejects the code or the refresh token (invalid_grant)")
	ErrInvalidClient  = errors.New("googleauth: Google rejects the client ID or secret (invalid_client)")
	ErrScopeMissing   = errors.New("googleauth: the calendar access was not granted")
	ErrNoRefreshToken = errors.New("googleauth: Google sent no refresh token")
	ErrUnauthorized   = errors.New("googleauth: Google refuses the access token")
	ErrStateUnknown   = errors.New("googleauth: unknown state")
	ErrStateExpired   = errors.New("googleauth: expired state")
	ErrNotACallback   = errors.New("googleauth: not an address that holds a sign-in")
)

// Client talks to Google. A nil HTTP client refuses every request: the box
// must bring one from the gate.
type Client struct {
	hc *http.Client
	ep Endpoints
}

// Option changes a Client.
type Option func(*Client)

// WithEndpoints points the client at other addresses (tests).
func WithEndpoints(e Endpoints) Option {
	return func(c *Client) {
		if e.Auth != "" {
			c.ep.Auth = e.Auth
		}
		if e.Token != "" {
			c.ep.Token = e.Token
		}
		if e.CalendarList != "" {
			c.ep.CalendarList = e.CalendarList
		}
	}
}

// New returns a Client that sends through hc.
func New(hc *http.Client, opts ...Option) *Client {
	c := &Client{hc: hc, ep: DefaultEndpoints}
	for _, o := range opts {
		o(c)
	}
	return c
}

var b64 = base64.RawURLEncoding

// NewVerifier returns a PKCE code verifier (RFC 7636): 32 random bytes, 43 characters.
func NewVerifier() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return b64.EncodeToString(raw), nil
}

// Challenge is the S256 code challenge of a verifier.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return b64.EncodeToString(sum[:])
}

// Signer seals and opens values with the box's key (*secret.Sealer).
type Signer interface {
	Seal([]byte) ([]byte, error)
	Open([]byte) ([]byte, error)
}

// StateToken is a fresh, signed, short-lived state value. Nonce is what the
// box stores to let it be used once.
type StateToken struct{ Token, Nonce string }

// NewStateToken makes a state token valid for StateLifetime: random bytes and
// the expiry, sealed with the box's key, so a value the box did not make, or
// that was changed, does not open.
func NewStateToken(s Signer, now time.Time) (StateToken, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return StateToken{}, err
	}
	plain := append(nonce, strconv.FormatInt(now.Add(StateLifetime).Unix(), 10)...)
	sealed, err := s.Seal(plain)
	if err != nil {
		return StateToken{}, err
	}
	return StateToken{Token: b64.EncodeToString(sealed), Nonce: hex.EncodeToString(plain[:16])}, nil
}

// VerifyStateToken opens a token and returns its nonce: ErrStateUnknown if the
// box did not make it, ErrStateExpired if it is older than StateLifetime.
func VerifyStateToken(s Signer, token string, now time.Time) (string, error) {
	sealed, err := b64.DecodeString(token)
	if err != nil {
		return "", ErrStateUnknown
	}
	plain, err := s.Open(sealed)
	if err != nil || len(plain) <= 16 {
		return "", ErrStateUnknown
	}
	expires, err := strconv.ParseInt(string(plain[16:]), 10, 64)
	if err != nil {
		return "", ErrStateUnknown
	}
	if now.Unix() > expires {
		return "", ErrStateExpired
	}
	return hex.EncodeToString(plain[:16]), nil
}

// State is the value sent to Google as `state`: "hj1.", the box's address (so
// the relay page knows where to go back to) and the signed token.
func State(origin, token string) string {
	return "hj1." + b64.EncodeToString([]byte(origin)) + "." + token
}

// ParseState splits a state into the box's address and the token.
func ParseState(state string) (origin, token string, err error) {
	parts := strings.Split(state, ".")
	if len(parts) != 3 || parts[0] != "hj1" || parts[1] == "" || parts[2] == "" {
		return "", "", ErrStateUnknown
	}
	raw, err := b64.DecodeString(parts[1])
	if err != nil {
		return "", "", ErrStateUnknown
	}
	return string(raw), parts[2], nil
}

// AuthURL is the address that sends the browser to Google's sign-in. Offline
// access and the consent prompt make Google hand out a refresh token each time.
func (c *Client) AuthURL(clientID, redirectURI, state, challenge, loginHint string) string {
	q := url.Values{
		"client_id": {clientID}, "redirect_uri": {redirectURI}, "response_type": {"code"}, "scope": {Scope},
		"state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"access_type": {"offline"}, "prompt": {"consent"},
	}
	if loginHint != "" {
		q.Set("login_hint", loginHint)
	}
	return c.ep.Auth + "?" + q.Encode()
}

// Callback is what Google sent back: a code, or an error such as access_denied.
type Callback struct{ Code, State, Error, Scope string }

// CallbackFromQuery reads the parameters of the callback.
func CallbackFromQuery(v url.Values) Callback {
	return Callback{Code: v.Get("code"), State: v.Get("state"), Error: v.Get("error"), Scope: v.Get("scope")}
}

// ParseCallbackURL reads the address a parent copied from the browser's
// address bar on the fallback way. It must be the fallback address with a
// state and either a code or an error; anything else is ErrNotACallback.
func ParseCallbackURL(raw string) (Callback, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host != "127.0.0.1:8099" || (u.Path != "/" && u.Path != "") {
		return Callback{}, ErrNotACallback
	}
	cb := CallbackFromQuery(u.Query())
	if cb.State == "" || (cb.Code == "" && cb.Error == "") {
		return Callback{}, ErrNotACallback
	}
	return cb, nil
}

// Tokens are what Google hands out. Refresh is empty after a renewal.
type Tokens struct {
	Access, Refresh string
	Expires         time.Time
}

type tokenReply struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int64  `json:"expires_in"`
	Scope     string `json:"scope"`
	Error     string `json:"error"`
	Detail    string `json:"error_description"`
}

// Exchange trades the code for tokens (PKCE: the verifier proves it is the same sign-in).
func (c *Client) Exchange(ctx context.Context, clientID, clientSecret, code, verifier, redirectURI string, now time.Time) (Tokens, error) {
	tok, scope, err := c.token(ctx, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirectURI},
		"client_id": {clientID}, "client_secret": {clientSecret},
	}, now)
	if err != nil {
		return Tokens{}, err
	}
	if !strings.Contains(" "+scope+" ", " "+Scope+" ") {
		return Tokens{}, ErrScopeMissing
	}
	if tok.Refresh == "" {
		return Tokens{}, ErrNoRefreshToken
	}
	return tok, nil
}

// Refresh gets a new access token with the stored refresh token. A refresh
// token Google no longer accepts is ErrInvalidGrant.
func (c *Client) Refresh(ctx context.Context, clientID, clientSecret, refreshToken string, now time.Time) (Tokens, error) {
	tok, _, err := c.token(ctx, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {clientID}, "client_secret": {clientSecret},
	}, now)
	return tok, err
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	if c.hc == nil {
		return nil, errors.New("googleauth: no HTTP client")
	}
	req.Header.Set("User-Agent", "Hejmo calendar sync")
	return c.hc.Do(req)
}

func (c *Client) token(ctx context.Context, form url.Values, now time.Time) (Tokens, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.ep.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.do(req)
	if err != nil {
		return Tokens{}, "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Tokens{}, "", err
	}
	var rep tokenReply
	_ = json.Unmarshal(body, &rep)
	switch {
	case rep.Error == "invalid_grant":
		return Tokens{}, "", ErrInvalidGrant
	case rep.Error == "invalid_client" || rep.Error == "unauthorized_client":
		return Tokens{}, "", ErrInvalidClient
	case res.StatusCode != http.StatusOK || rep.Access == "":
		return Tokens{}, "", fmt.Errorf("googleauth: the token service answered %s %s", res.Status, rep.Error)
	}
	return Tokens{Access: rep.Access, Refresh: rep.Refresh, Expires: now.Add(time.Duration(rep.ExpiresIn) * time.Second)}, rep.Scope, nil
}

// Calendar is one entry of the account's calendar list.
type Calendar struct {
	ID         string `json:"id"`
	Summary    string `json:"summary"`
	Override   string `json:"summaryOverride"`
	AccessRole string `json:"accessRole"`
	Primary    bool   `json:"primary"`
}

// Name is the name the account owner sees for the calendar.
func (c Calendar) Name() string {
	if strings.TrimSpace(c.Override) != "" {
		return strings.TrimSpace(c.Override)
	}
	return strings.TrimSpace(c.Summary)
}

// Writable says whether the box may change events in it: only an owner or a
// writer may; a reader or free/busy reader may not.
func (c Calendar) Writable() bool { return c.AccessRole == "owner" || c.AccessRole == "writer" }

// Calendars lists every calendar of the account (all pages).
func (c *Client) Calendars(ctx context.Context, accessToken string) ([]Calendar, error) {
	var out []Calendar
	page := ""
	for range 20 {
		q := url.Values{"minAccessRole": {"reader"}, "showHidden": {"false"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.ep.CalendarList+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		res, err := c.do(req)
		if err != nil {
			return nil, err
		}
		body, rerr := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		switch {
		case rerr != nil:
			return nil, rerr
		case res.StatusCode == http.StatusUnauthorized:
			return nil, ErrUnauthorized
		case res.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("googleauth: the calendar list answered %s", res.Status)
		}
		var list struct {
			Items []Calendar `json:"items"`
			Next  string     `json:"nextPageToken"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("googleauth: the calendar list is not readable: %w", err)
		}
		out = append(out, list.Items...)
		if page = list.Next; page == "" {
			break
		}
	}
	return out, nil
}

// CalendarURL is the CalDAV address of a calendar under base.
func CalendarURL(base, calendarID string) string {
	return strings.TrimSuffix(base, "/") + "/" + strings.ReplaceAll(url.PathEscape(calendarID), "%40", "@") + "/events"
}

// CalendarIDOf is the reverse: the calendar id in a CalDAV address made by CalendarURL.
func CalendarIDOf(address string) (string, bool) {
	u, err := url.Parse(address)
	if err != nil {
		return "", false
	}
	segs := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(segs) < 4 || segs[len(segs)-1] != "events" {
		return "", false
	}
	id, err := url.PathUnescape(segs[len(segs)-2])
	return id, err == nil && id != ""
}
