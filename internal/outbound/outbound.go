// Package outbound is the one place through which the box connects to
// anything outside the home network (ticket 201, decision Q144). Code that
// needs the outside takes a client from a Gate, one per connection (weather,
// calendar services, and so on). The Gate looks at the household's switches
// before every request: in offline mode, or with that connection switched
// off, it refuses, records the attempt (time, connection, target) and never
// opens a socket. When a request goes through it records the last contact.
//
// A test (bypass_test.go) fails if any other package builds its own client,
// transport or dialer, so there is no second way out.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Service is one connection to the outside that parents can switch.
type Service string

// The connections of the page "Connections to the outside", in its order.
const (
	Weather   Service = "weather"   // Open-Meteo, the weather of the home
	Update    Service = "update"    // the nightly check for a new version (made by the host script)
	Calendars Service = "calendars" // iCloud, Google, CalDAV servers
	ICS       Service = "ics"       // subscribed calendars
	Recipes   Service = "recipes"   // importing a recipe from a web page
	Images    Service = "images"    // a picture from a web address
	DNS       Service = "dns"       // the DNS service of a public name and its certificate
	// Other is a connection that did not say which it belongs to (a library
	// using the default client). It has no switch: only offline mode stops it.
	Other Service = "other"
)

// Services lists the connections that have a switch, in display order.
var Services = []Service{Weather, Update, Calendars, ICS, Recipes, Images, DNS}

// Valid reports whether s is a connection with a switch.
func (s Service) Valid() bool {
	for _, v := range Services {
		if v == s {
			return true
		}
	}
	return false
}

// Verdict is the Gate's answer for one connection.
type Verdict int

// Verdicts.
const (
	VerdictAllowed     Verdict = iota
	VerdictOffline             // offline mode is on
	VerdictSwitchedOff         // this connection is switched off
)

// Policy tells the Gate what the household has switched on. An implementation
// that cannot tell must answer VerdictOffline (fail closed).
type Policy interface {
	Verdict(Service) Verdict
}

// Ledger keeps the proof: refused attempts and the last contact per connection.
type Ledger interface {
	Refused(at time.Time, s Service, target string)
	Contacted(at time.Time, s Service, target string)
}

// Closed is the Policy that refuses everything.
type Closed struct{}

// Verdict implements Policy.
func (Closed) Verdict(Service) Verdict { return VerdictOffline }

// ErrRefused is what a refused attempt matches with errors.Is.
var ErrRefused = errors.New("outbound: the connection to the outside is switched off")

// ErrNotHome is the error of a client for the home network that was pointed outside.
var ErrNotHome = errors.New("outbound: not an address in the home network")

// RefusedError says which connection was refused, where it wanted to go and why.
type RefusedError struct {
	Service Service
	Target  string
	Verdict Verdict
}

func (e *RefusedError) Error() string {
	why := "switched off"
	if e.Verdict == VerdictOffline {
		why = "offline mode is on"
	}
	return fmt.Sprintf("outbound: %s to %s refused: %s", e.Service, e.Target, why)
}

// Is makes errors.Is(err, ErrRefused) true.
func (e *RefusedError) Is(target error) bool { return target == ErrRefused }

// Gate is the box's door to the outside. A Gate without a policy (the zero
// value, a nil *Gate, New(nil, ...)) fails closed: it refuses everything.
// Tests that need a connection use AllowAll; the bypass check (bypass_test.go)
// fails on that, on nil-Gate conversions and on Gate literals in the box's own code.
type Gate struct {
	policy Policy
	ledger Ledger
	now    func() time.Time
}

// New returns a Gate that asks policy before every connection and writes to ledger.
func New(policy Policy, ledger Ledger) *Gate {
	return &Gate{policy: policy, ledger: ledger, now: time.Now}
}

type allowAll struct{}

func (allowAll) Verdict(Service) Verdict { return VerdictAllowed }

// AllowAll returns a Gate that lets everything through and keeps no record.
// It is for tests only; the bypass check rejects it anywhere else.
func AllowAll() *Gate { return &Gate{policy: allowAll{}, now: time.Now} }

// SetClock sets the clock the ledger entries are stamped with (tests).
func (g *Gate) SetClock(now func() time.Time) { g.now = now }

func (g *Gate) clock() time.Time {
	if g == nil || g.now == nil {
		return time.Now()
	}
	return g.now()
}

// Verdict is the answer for s now, without recording anything.
func (g *Gate) Verdict(s Service) Verdict {
	if g == nil || g.policy == nil {
		return VerdictOffline // fail closed
	}
	return g.policy.Verdict(s)
}

// Allowed reports whether s may connect now. Background jobs ask it to skip a
// round that would only be refused; it records nothing.
func (g *Gate) Allowed(s Service) bool { return g.Verdict(s) == VerdictAllowed }

// Check is the gate for work that is not an HTTP request through Client (a DNS
// provider's API, say). It returns a *RefusedError and records the attempt if s
// may not connect.
func (g *Gate) Check(s Service, target string) error {
	if v := g.Verdict(s); v != VerdictAllowed {
		if g != nil && g.ledger != nil {
			g.ledger.Refused(g.clock(), s, target)
		}
		return &RefusedError{Service: s, Target: target, Verdict: v}
	}
	return nil
}

// Contacted records that s reached target (a connection made outside Client).
func (g *Gate) Contacted(s Service, target string) {
	if g != nil && g.ledger != nil {
		g.ledger.Contacted(g.clock(), s, target)
	}
}

// ClientConfig tunes a client from NewClient.
type ClientConfig struct {
	Timeout time.Duration
	// AllowAddr, if set, is asked about every address the client is about to
	// connect to, after the name is resolved; an error stops the connection.
	AllowAddr func(netip.Addr) error
	// NoKeepAlive closes the connection after each request.
	NoKeepAlive bool
	// ResponseHeaderTimeout and TLSHandshakeTimeout default to none.
	ResponseHeaderTimeout, TLSHandshakeTimeout time.Duration
	// MaxHeaderBytes limits the answer's headers; zero keeps the default.
	MaxHeaderBytes int64
	// CheckRedirect is http.Client's.
	CheckRedirect func(*http.Request, []*http.Request) error
	// RecordPath adds the path (never the query) of the address to the target
	// in the ledger, for connections whose address a parent typed in.
	RecordPath bool
}

// Client returns a client for connection s with the given timeout.
func (g *Gate) Client(s Service, timeout time.Duration) *http.Client {
	return g.NewClient(s, ClientConfig{Timeout: timeout})
}

// NewClient returns a client for connection s.
func (g *Gate) NewClient(s Service, c ClientConfig) *http.Client {
	tr := baseTransport(c)
	return &http.Client{Timeout: c.Timeout, CheckRedirect: c.CheckRedirect, Transport: &gated{g: g, s: s, base: tr, path: c.RecordPath}}
}

func baseTransport(c ClientConfig) *http.Transport {
	tr := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		TLSHandshakeTimeout:    c.TLSHandshakeTimeout,
		ResponseHeaderTimeout:  c.ResponseHeaderTimeout,
		DisableKeepAlives:      c.NoKeepAlive,
		MaxResponseHeaderBytes: c.MaxHeaderBytes,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           10,
		IdleConnTimeout:        30 * time.Second,
	}
	if c.AllowAddr != nil {
		tr.Proxy = nil // an environment proxy would bypass the check
		tr.DialContext = controlledDialer(c.Timeout, c.AllowAddr)
	}
	return tr
}

func controlledDialer(timeout time.Duration, allow func(netip.Addr) error) func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("outbound: odd address %q", address)
			}
			return allow(ap.Addr().Unmap())
		},
	}
	return d.DialContext
}

// gated is the transport of a client for one connection: it asks the Gate for
// every request, also for each hop of a redirect.
type gated struct {
	g    *Gate
	s    Service
	base http.RoundTripper
	path bool
}

func (t *gated) RoundTrip(req *http.Request) (*http.Response, error) {
	target := req.URL.Host
	if t.path {
		target += req.URL.Path
	}
	if err := t.g.Check(t.s, target); err != nil {
		return nil, err
	}
	res, err := t.base.RoundTrip(req)
	if err == nil {
		t.g.Contacted(t.s, target)
	}
	return res, err
}

type serviceKey struct{}

// WithService marks requests made with ctx as belonging to connection s, for
// libraries that use the default client.
func WithService(ctx context.Context, s Service) context.Context {
	return context.WithValue(ctx, serviceKey{}, s)
}

// InstallDefault gates the default HTTP transport, so a library that falls
// back to http.DefaultClient is stopped too: requests to the outside count as
// the connection named by WithService, else as Other. It returns the function
// that restores the old transport.
func InstallDefault(g *Gate) (restore func()) {
	old := http.DefaultTransport
	http.DefaultTransport = &strayGate{g: g, base: old}
	return func() { http.DefaultTransport = old }
}

type strayGate struct {
	g    *Gate
	base http.RoundTripper
}

func (t *strayGate) RoundTrip(req *http.Request) (*http.Response, error) {
	if CheckHome(req.URL.String()) == nil {
		return t.base.RoundTrip(req)
	}
	s, ok := req.Context().Value(serviceKey{}).(Service)
	if !ok {
		s = Other
	}
	if err := t.g.Check(s, req.URL.Host); err != nil {
		return nil, err
	}
	res, err := t.base.RoundTrip(req)
	if err == nil {
		t.g.Contacted(s, req.URL.Host)
	}
	return res, err
}

// Local returns a client that reaches only the home network (the display
// tablets' kiosk apps, the box itself): an address outside it is refused with
// ErrNotHome, before any name is looked up.
func Local(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &homeOnly{base: baseTransport(ClientConfig{Timeout: timeout, AllowAddr: func(a netip.Addr) error {
		if !homeAddr(a) {
			return ErrNotHome
		}
		return nil
	}})}}
}

type homeOnly struct{ base http.RoundTripper }

func (t *homeOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := CheckHome(req.URL.String()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

func homeAddr(a netip.Addr) bool {
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified()
}

// CheckHome reports whether the address in rawURL is in the home network: an
// address of a private, loopback or link-local range, "localhost", a name
// ending in .local, .lan, .home.arpa or .fritz.box, or a name without a dot.
// Other names (tablet.home, say) do not count, offline mode or not. It looks at
// the text only and resolves nothing.
func CheckHome(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return ErrNotHome
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if a, err := netip.ParseAddr(h); err == nil {
		if homeAddr(a.Unmap()) {
			return nil
		}
		return ErrNotHome
	}
	if h == "localhost" || !strings.Contains(h, ".") {
		return nil
	}
	for _, suffix := range []string{".local", ".lan", ".home.arpa", ".fritz.box"} {
		if strings.HasSuffix(h, suffix) {
			return nil
		}
	}
	return ErrNotHome
}
