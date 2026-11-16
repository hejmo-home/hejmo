// Package caldavclient reads calendars from a CalDAV server (iCloud, Nextcloud,
// Radicale and others). It finds calendars with go-webdav and lists and
// fetches objects with its own calendar-query and calendar-multiget REPORTs
// (ADR 0012), because real servers answer in ways go-webdav's reader refuses.
// It speaks in absolute URLs and raw iCalendar text and knows nothing of the
// box's calendars or database.
package caldavclient

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	davcal "github.com/emersion/go-webdav/caldav"
	"golang.org/x/net/publicsuffix"
)

// Calendar is one calendar collection on the server.
type Calendar struct {
	URL  string // absolute address
	Name string
}

// Object is one calendar object resource (an .ics file holding one event
// together with its changed occurrences).
type Object struct {
	URL  string // absolute address
	ETag string
	Data []byte // iCalendar text, set by Fetch only
	// Err is set by Fetch when the server answered 2xx but the object cannot
	// be read as iCalendar. The other objects of the same Fetch are
	// unaffected. A refusal by the server is never an Err: Fetch fails.
	Err error
}

// StatusError is a refusal by the server, such as 401 for a wrong password.
type StatusError struct {
	Code   int
	Status string
}

func (e *StatusError) Error() string { return "the server answered " + e.Status }

// IsUnauthorized reports whether the server refused the user name or password.
func IsUnauthorized(err error) bool {
	var s *StatusError
	return errors.As(err, &s) && (s.Code == http.StatusUnauthorized || s.Code == http.StatusForbidden)
}

// ForeignHostError is the refusal to talk to a host outside the domain of the
// configured server. Credentials are never sent to such a host.
type ForeignHostError struct {
	Host   string // the host that was refused
	Domain string // the registrable domain of the configured server
	// Downgrade is set when Host is on the server's own domain but was
	// reached over http while the server is configured with https. The
	// password is never sent over a connection less safe than the one the
	// family chose.
	Downgrade bool
}

func (e *ForeignHostError) Error() string {
	if e.Downgrade {
		return fmt.Sprintf("refused to contact %s over http: the server is configured with https, and the password is never sent unencrypted", e.Host)
	}
	return fmt.Sprintf("refused to contact %s: it is not part of %s, the domain of the configured server, and the password is only sent there", e.Host, e.Domain)
}

// Client talks to one CalDAV server with one set of credentials. Servers such
// as iCloud answer from several hosts of one domain (caldav.icloud.com names
// p123-caldav.icloud.com as the calendar home), so the client follows
// redirects and hrefs to other hosts, but only within the registrable domain
// of the configured address.
type Client struct {
	base *url.URL
	auth *authClient

	mu  sync.Mutex
	dav map[string]*davcal.Client // by scheme://host
}

// NewClient returns a Client for the server at serverURL. A nil hc uses an
// HTTP client with a 30 second timeout.
func NewClient(serverURL, user, password string, hc *http.Client) (*Client, error) {
	base, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("not a web address")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	auth := &authClient{base: base, user: user, password: password}
	cp := *hc // do not change the caller's client
	prev := hc.CheckRedirect
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := auth.permit(req.URL); err != nil {
			return err
		}
		if req.Context().Value(manualRedirect{}) != nil {
			return http.ErrUseLastResponse
		}
		if prev != nil {
			if err := prev(req, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		// Go drops credentials when a redirect leaves the host's own domain
		// tree; the host is vetted above, so send them again.
		req.SetBasicAuth(user, password)
		return nil
	}
	auth.hc = &cp
	c := &Client{base: base, auth: auth, dav: map[string]*davcal.Client{}}
	if _, _, err := c.origin(""); err != nil {
		return nil, err
	}
	return c, nil
}

// origin returns the host an address lives on and the go-webdav client for it.
// An address without a host (a plain path) lives on the configured server.
// It fails with a ForeignHostError before anything is sent to a host outside
// the configured domain.
func (c *Client) origin(address string) (*url.URL, *davcal.Client, error) {
	o := c.base
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		o = u
	}
	if err := c.auth.permit(o); err != nil {
		return nil, nil, err
	}
	root := &url.URL{Scheme: o.Scheme, Host: o.Host}
	c.mu.Lock()
	defer c.mu.Unlock()
	dav, ok := c.dav[root.String()]
	if !ok {
		var err error
		if dav, err = davcal.NewClient(c.auth, root.String()); err != nil {
			return nil, nil, err
		}
		c.dav[root.String()] = dav
	}
	return root, dav, nil
}

// authClient adds basic auth and turns refusals into StatusError. It sends
// nothing to a host outside the domain of base.
type authClient struct {
	hc             *http.Client
	base           *url.URL
	user, password string
}

// registrable returns the registrable domain of a host ("icloud.com" for
// "p123-caldav.icloud.com"). An IP address, a name such as localhost, or a
// public suffix has no domain above itself and stands for itself.
func registrable(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if net.ParseIP(host) != nil {
		return host
	}
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return d
	}
	return host
}

// CheckSameDomain says whether u may be contacted with the credentials of the
// server at base: the same registrable domain, and never a step down from
// https to http. A move from http to https is allowed. The error is a
// *ForeignHostError. The sync, discovery and the server's check of a ticked
// calendar all use this one rule.
//
// An unknown top-level domain counts as a public suffix, so on a home network
// nas.fritz.box and cal.fritz.box are the same domain. That is intended.
func CheckSameDomain(base, u *url.URL) error {
	domain := registrable(base.Hostname())
	upgrade := base.Scheme == "http" && u.Scheme == "https"
	if registrable(u.Hostname()) != domain || (u.Scheme != base.Scheme && !upgrade) || (u.Scheme != "http" && u.Scheme != "https") {
		host := u.Hostname()
		return &ForeignHostError{Host: host, Domain: domain, Downgrade: registrable(host) == domain && (u.Scheme == "http" || u.Scheme == "https")}
	}
	return nil
}

// permit says whether credentials may be sent to u.
func (a *authClient) permit(u *url.URL) error { return CheckSameDomain(a.base, u) }

func (a *authClient) Do(req *http.Request) (*http.Response, error) {
	if err := a.permit(req.URL); err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.user, a.password)
	req.Header.Set("User-Agent", "Hejmo calendar sync")
	res, err := a.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		res.Body.Close()
		return nil, &StatusError{res.StatusCode, res.Status}
	}
	return res, nil
}

var _ webdav.HTTPClient = (*authClient)(nil)

// abs makes p, an address or a path on origin, absolute.
func abs(origin *url.URL, p string) string {
	ref, err := url.Parse(p)
	if err != nil {
		return p
	}
	return origin.ResolveReference(ref).String()
}

// route turns an address into what go-webdav expects: a path on the server.
func (c *Client) route(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return address
	}
	return u.EscapedPath()
}

const propfindBody = `<?xml version="1.0" encoding="utf-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop><%s/></d:prop></d:propfind>`

type hrefProp struct {
	Href string `xml:"DAV: href"`
}

type multistatus struct {
	Responses []struct {
		Propstats []struct {
			Prop struct {
				Principal hrefProp `xml:"DAV: current-user-principal"`
				Home      hrefProp `xml:"urn:ietf:params:xml:ns:caldav calendar-home-set"`
			} `xml:"DAV: prop"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

// findHref asks the resource at "from" for a property that holds an address
// (current-user-principal or calendar-home-set) and returns it resolved
// against where the answer came from. go-webdav keeps only the path of such
// an address, but a server may name another host of its domain here. The host
// is checked before the caller sends anything to it.
func (c *Client) findHref(ctx context.Context, from *url.URL, prop string) (*url.URL, error) {
	var (
		res *http.Response
		at  = from
	)
	// A redirect for PROPFIND is followed by hand: Go's client turns a
	// followed 301, 302 or 303 into a GET, and a server that redirects
	// /.well-known/caldav (RFC 6764) means the same PROPFIND at the new address.
	ctx = context.WithValue(ctx, manualRedirect{}, true)
	for hops := 0; ; hops++ {
		req, err := http.NewRequestWithContext(ctx, "PROPFIND", at.String(), strings.NewReader(fmt.Sprintf(propfindBody, prop)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
		req.Header.Set("Depth", "0")
		res, err = c.auth.Do(req)
		if err != nil {
			return nil, err
		}
		loc := res.Header.Get("Location")
		if res.StatusCode < 300 || res.StatusCode > 399 || loc == "" {
			break
		}
		res.Body.Close()
		if hops >= 5 {
			return nil, errors.New("stopped after 5 redirects")
		}
		ref, err := url.Parse(strings.TrimSpace(loc))
		if err != nil {
			return nil, fmt.Errorf("the server redirected to a malformed address: %w", err)
		}
		at = at.ResolveReference(ref)
		if err := c.auth.permit(at); err != nil {
			return nil, err
		}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus {
		return nil, &StatusError{res.StatusCode, res.Status}
	}
	var ms multistatus
	if err := xml.NewDecoder(res.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("reading the answer for %s: %w", prop, err)
	}
	for _, r := range ms.Responses {
		for _, ps := range r.Propstats {
			href := ps.Prop.Principal.Href
			if prop == "c:calendar-home-set" {
				href = ps.Prop.Home.Href
			}
			if strings.TrimSpace(href) == "" {
				continue
			}
			ref, err := url.Parse(strings.TrimSpace(href))
			if err != nil {
				return nil, fmt.Errorf("the server named a malformed address: %w", err)
			}
			// Resolve against the address that finally answered, which may
			// be on another host than "from" after a redirect.
			target := at.ResolveReference(ref)
			if err := c.auth.permit(target); err != nil {
				return nil, err
			}
			return target, nil
		}
	}
	return nil, fmt.Errorf("the server does not say where the %s is", strings.TrimPrefix(strings.TrimPrefix(prop, "c:"), "d:"))
}

// manualRedirect marks a request whose redirects the caller follows itself.
type manualRedirect struct{}

// principal finds the signed-in user's principal. A server entered as a bare
// host (no path) is asked at /.well-known/caldav first (RFC 6764), then at the
// address itself; an address with a path is used as given.
func (c *Client) principal(ctx context.Context) (*url.URL, error) {
	if p := strings.Trim(c.base.Path, "/"); p != "" {
		return c.findHref(ctx, c.base, "d:current-user-principal")
	}
	known := c.base.ResolveReference(&url.URL{Path: "/.well-known/caldav"})
	principal, err := c.findHref(ctx, known, "d:current-user-principal")
	if err == nil {
		return principal, nil
	}
	var status *StatusError
	var foreign *ForeignHostError
	if errors.As(err, &foreign) || (errors.As(err, &status) && status.Code == http.StatusUnauthorized) {
		return nil, err
	}
	return c.findHref(ctx, c.base, "d:current-user-principal")
}

// Calendars discovers the server's event calendars of the signed-in user.
func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	principal, err := c.principal(ctx)
	if err != nil {
		return nil, err
	}
	home, err := c.findHref(ctx, principal, "c:calendar-home-set")
	if err != nil {
		return nil, err
	}
	origin, dav, err := c.origin(home.String())
	if err != nil {
		return nil, err
	}
	found, err := dav.FindCalendars(ctx, home.EscapedPath())
	if err != nil {
		return nil, err
	}
	var out []Calendar
	for _, f := range found {
		if len(f.SupportedComponentSet) > 0 && !slices.Contains(f.SupportedComponentSet, ical.CompEvent) {
			continue // a task list or notes
		}
		out = append(out, Calendar{URL: abs(origin, f.Path), Name: nameOf(f.Name, f.Path)})
	}
	return out, nil
}

// Calendar checks one calendar address given by hand and names it.
func (c *Client) Calendar(ctx context.Context, calendarURL string) (Calendar, error) {
	origin, dav, err := c.origin(calendarURL)
	if err != nil {
		return Calendar{}, err
	}
	parent := path.Dir(strings.TrimSuffix(c.route(calendarURL), "/")) + "/"
	found, err := dav.FindCalendars(ctx, parent)
	if err != nil {
		return Calendar{}, err
	}
	want := strings.TrimSuffix(c.route(calendarURL), "/")
	for _, f := range found {
		if strings.TrimSuffix(f.Path, "/") == want {
			return Calendar{URL: abs(origin, f.Path), Name: nameOf(f.Name, f.Path)}, nil
		}
	}
	return Calendar{}, errors.New("that address is not a calendar")
}

func nameOf(display, p string) string {
	if strings.TrimSpace(display) != "" {
		return strings.TrimSpace(display)
	}
	n := path.Base(strings.TrimSuffix(p, "/"))
	if u, err := url.PathUnescape(n); err == nil {
		n = u
	}
	return n
}

// listBody asks for the ETag of every event object in the calendar. Nothing
// else is requested, so a server has no property to report as missing. No time
// range is asked for: the import window (ADR 0012) is applied to occurrences
// when the objects are read, so a series that began before the window but
// recurs into it is never lost here.
const listBody = `<?xml version="1.0" encoding="utf-8"?>` +
	`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` +
	`<d:prop><d:getetag/></d:prop>` +
	`<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT"/></c:comp-filter></c:filter>` +
	`</c:calendar-query>`

type reportAnswer struct {
	Responses []struct {
		Href      string `xml:"DAV: href"`
		Status    string `xml:"DAV: status"` // of the whole member, when it has no propstat
		Propstats []struct {
			Status string `xml:"DAV: status"`
			Prop   struct {
				ETag         string `xml:"DAV: getetag"`
				CalendarData string `xml:"urn:ietf:params:xml:ns:caldav calendar-data"`
			} `xml:"DAV: prop"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

// report sends a REPORT with the given body to a calendar and reads the
// multistatus answer tolerantly (go-webdav's reader fails on a 404 propstat and
// decodes calendar data). It returns the address that finally answered, which
// may be on another host than the one asked after a redirect, for resolving
// the hrefs in the answer.
func (c *Client) report(ctx context.Context, dir, body, what string) (*reportAnswer, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, "REPORT", dir, strings.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "1")
	res, err := c.auth.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus {
		return nil, nil, &StatusError{res.StatusCode, res.Status}
	}
	var ans reportAnswer
	if err := xml.NewDecoder(res.Body).Decode(&ans); err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", what, err)
	}
	at, _ := url.Parse(dir)
	if res.Request != nil && res.Request.URL != nil {
		at = res.Request.URL
	}
	return &ans, at, nil
}

// List returns the objects of a calendar with their ETags, without their data.
//
// It does not use go-webdav's directory listing: that decodes resourcetype of
// every member and fails the whole listing when one answers 404 for it, which
// iCloud does for some members. Here only the 2xx propstats are read. A
// collection is skipped, and so is a member the server reports as gone (404 or
// 410 for the whole response). A member without an ETag stays in the list with
// an empty one: the sync downloads such an object on every run instead of
// taking it for deleted.
func (c *Client) List(ctx context.Context, calendarURL string) ([]Object, error) {
	return c.query(ctx, calendarURL, listBody, "the list of events")
}

// countBody asks which event objects have an occurrence at or after a moment
// (a time-range without an end), so the server decides about series: an
// object holding a series is one answer however often it recurs.
const countBody = `<?xml version="1.0" encoding="utf-8"?>` +
	`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` +
	`<d:prop><d:getetag/></d:prop>` +
	`<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT"><c:time-range start="%s"/></c:comp-filter></c:comp-filter></c:filter>` +
	`</c:calendar-query>`

// CountEvents says how many events a calendar has at or after since: event
// objects with an occurrence in that range, a series counting once. It is
// what the import window (ADR 0012) will bring in, asked of the server
// without downloading anything. A server that cannot answer a time-range
// query fails the call.
func (c *Client) CountEvents(ctx context.Context, calendarURL string, since time.Time) (int, error) {
	body := fmt.Sprintf(countBody, since.UTC().Format("20060102T150405Z"))
	objects, err := c.query(ctx, calendarURL, body, "the count of events")
	return len(objects), err
}

// query sends a calendar-query that asks for ETags and returns the member
// objects of the answer, read as List documents.
func (c *Client) query(ctx context.Context, calendarURL, body, what string) ([]Object, error) {
	origin, _, err := c.origin(calendarURL)
	if err != nil {
		return nil, err
	}
	dir := abs(origin, c.route(calendarURL))
	ans, at, err := c.report(ctx, dir, body, what)
	if err != nil {
		return nil, err
	}
	self := AddressKey(dir)
	var out []Object
	for _, r := range ans.Responses {
		href, err := url.Parse(strings.TrimSpace(r.Href))
		if err != nil || strings.TrimSpace(r.Href) == "" {
			continue
		}
		target := at.ResolveReference(href)
		if err := c.auth.permit(target); err != nil {
			return nil, err
		}
		if strings.HasSuffix(target.Path, "/") || AddressKey(target.String()) == self {
			continue // a collection
		}
		if code := statusCode(r.Status); code == http.StatusNotFound || code == http.StatusGone {
			continue
		}
		etag := ""
		for _, ps := range r.Propstats {
			if statusCode(ps.Status)/100 == 2 && strings.TrimSpace(ps.Prop.ETag) != "" {
				etag = unquoteETag(ps.Prop.ETag)
				break
			}
		}
		out = append(out, Object{URL: target.String(), ETag: etag})
	}
	return out, nil
}

// unquoteETag drops the quotes around an ETag, as go-webdav does for the ETags
// Fetch returns, so a listed ETag and a fetched one are the same string. An
// ETag that is not quoted (or a weak one) is kept as the server sent it.
func unquoteETag(s string) string {
	s = strings.TrimSpace(s)
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return s
}

// fetchBatch is how many objects one multiget asks for, so that the first
// sync of a calendar with years of events is not one huge request.
const fetchBatch = 100

// Fetch downloads the given objects, fetchBatch at a time. An object that was
// deleted since it was listed (404, 410) is left out. An object the server
// sent but that cannot be read comes back with Err set. Neither fails the
// others. Any other refusal (429, 5xx, ...) fails Fetch with a StatusError, so
// the caller retries instead of remembering a version it never got.
func (c *Client) Fetch(ctx context.Context, calendarURL string, objectURLs []string) ([]Object, error) {
	var out []Object
	for batch := range slices.Chunk(objectURLs, fetchBatch) {
		got, err := c.multiget(ctx, calendarURL, batch)
		if err != nil {
			var foreign *ForeignHostError
			if IsUnauthorized(err) || errors.As(err, &foreign) || ctx.Err() != nil {
				return nil, err
			}
			// The multiget as a whole failed (a server that cannot take it, or
			// an answer that is no multistatus), so ask for each object on its
			// own.
			if got, err = c.fetchEach(ctx, batch); err != nil {
				return nil, err
			}
		}
		out = append(out, got...)
	}
	return out, nil
}

// multigetOpen starts the body of a calendar-multiget that asks for the ETag
// and the calendar data of the objects whose hrefs follow. The data is
// requested whole, as the server stores it.
const multigetOpen = `<?xml version="1.0" encoding="utf-8"?>` +
	`<c:calendar-multiget xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` +
	`<d:prop><d:getetag/><c:calendar-data/></d:prop>`

// multiget downloads objects with one calendar-multiget REPORT and returns the
// server's calendar data as it was sent. It does not go through go-webdav:
// that decodes every object and the text then has to be encoded again, and
// go-ical's encoder refuses what real servers hold (iCloud keeps old events
// with two STATUS lines and invitations with several METHOD lines). Whether
// the text can be read is decided by readable, which is as liberal as the
// decoder.
func (c *Client) multiget(ctx context.Context, calendarURL string, objectURLs []string) ([]Object, error) {
	origin, _, err := c.origin(calendarURL)
	if err != nil {
		return nil, err
	}
	dir := abs(origin, c.route(calendarURL))
	var body strings.Builder
	body.WriteString(multigetOpen)
	for _, u := range objectURLs {
		body.WriteString("<d:href>")
		if err := xml.EscapeText(&body, []byte(c.route(u))); err != nil {
			return nil, err
		}
		body.WriteString("</d:href>")
	}
	body.WriteString("</c:calendar-multiget>")
	ans, at, err := c.report(ctx, dir, body.String(), "the events")
	if err != nil {
		return nil, err
	}
	var out []Object
	for _, r := range ans.Responses {
		href, err := url.Parse(strings.TrimSpace(r.Href))
		if err != nil || strings.TrimSpace(r.Href) == "" {
			continue
		}
		target := at.ResolveReference(href)
		if err := c.auth.permit(target); err != nil {
			return nil, err
		}
		var (
			ok    bool
			obj   = Object{URL: target.String()}
			gone  bool
			other *StatusError
		)
		refused := func(status string) {
			if code := statusCode(status); code/100 != 2 && other == nil {
				other = &StatusError{code, strings.TrimSpace(strings.TrimPrefix(status, "HTTP/1.1 "))}
			}
		}
		// Only the status of the whole response says the object is gone. A
		// 404 propstat says that one property is missing.
		if code := statusCode(r.Status); code == http.StatusNotFound || code == http.StatusGone {
			gone = true
		} else if len(r.Propstats) == 0 {
			refused(r.Status)
		}
		for _, ps := range r.Propstats {
			if code := statusCode(ps.Status); code/100 != 2 {
				if code != http.StatusNotFound {
					refused(ps.Status)
				}
				continue
			}
			if tag := strings.TrimSpace(ps.Prop.ETag); tag != "" {
				obj.ETag = unquoteETag(tag)
			}
			if strings.TrimSpace(ps.Prop.CalendarData) != "" {
				obj.Data, ok = []byte(crlf(ps.Prop.CalendarData)), true
			}
		}
		switch {
		case ok:
			obj.Err = readable(obj.Data)
		case gone:
			continue
		case other != nil:
			return nil, other
		default:
			obj.Err = errors.New("the server sent no calendar data")
		}
		out = append(out, obj)
	}
	return out, nil
}

// statusCode reads the code of a status line such as "HTTP/1.1 404 Not Found".
// A missing or malformed status counts as 200, as most servers leave it out
// only when all is well.
func statusCode(status string) int {
	f := strings.Fields(status)
	if len(f) >= 2 {
		if n, err := strconv.Atoi(f[1]); err == nil {
			return n
		}
	}
	return http.StatusOK
}

// crlf gives calendar data the line ends iCalendar prescribes. An XML parser
// turns CR LF into LF, so the text a server sent as CR LF comes back with LF.
func crlf(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// readable says whether text is iCalendar at all. It only decodes: cardinality
// rules (one STATUS, one METHOD, ...) are not checked, so what real servers
// store is accepted. The reading of single properties is as tolerant (the
// first of several wins).
func readable(data []byte) error {
	_, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	return err
}

// fetchEach downloads objects one request each. An object that is gone is
// left out; one that answers 2xx but cannot be read carries the reason in Err.
// Any other status fails the whole call: it says nothing about the object.
func (c *Client) fetchEach(ctx context.Context, objectURLs []string) ([]Object, error) {
	var out []Object
	for _, u := range objectURLs {
		origin, _, err := c.origin(u)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, abs(origin, c.route(u)), nil)
		if err != nil {
			return nil, err
		}
		res, err := c.auth.Do(req)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			return nil, err
		}
		obj := Object{URL: u, ETag: res.Header.Get("ETag"), Data: data}
		switch {
		case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
			continue
		case res.StatusCode/100 != 2:
			return nil, &StatusError{res.StatusCode, res.Status}
		default:
			if _, err := ical.NewDecoder(bytes.NewReader(data)).Decode(); err != nil {
				obj.Err = err
			}
		}
		out = append(out, obj)
	}
	return out, nil
}

// AddressKey identifies an address by host and decoded path, ignoring the
// case of the host and a trailing slash, so servers that escape or end
// addresses differently between listing and download still match.
func AddressKey(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return address
	}
	return strings.ToLower(u.Host) + strings.TrimSuffix(u.Path, "/")
}

// State is what a calendar collection reports about its own version: the
// CTag (calendarserver.org, a string that changes whenever anything in the
// collection does) and the sync token of RFC 6578 (what Changes starts from).
// Either is empty where the server offers none.
type State struct {
	CTag      string
	SyncToken string
}

const stateBody = `<?xml version="1.0" encoding="utf-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:cs="http://calendarserver.org/ns/"><d:prop><cs:getctag/><d:sync-token/></d:prop></d:propfind>`

// State asks a calendar for its CTag and sync token. A server that has
// neither answers with an empty State, not an error.
func (c *Client) State(ctx context.Context, calendarURL string) (State, error) {
	origin, _, err := c.origin(calendarURL)
	if err != nil {
		return State{}, err
	}
	dir := abs(origin, c.route(calendarURL))
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", dir, strings.NewReader(stateBody))
	if err != nil {
		return State{}, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "0")
	res, err := c.auth.Do(req)
	if err != nil {
		return State{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus {
		return State{}, &StatusError{res.StatusCode, res.Status}
	}
	var ans struct {
		Responses []struct {
			Propstats []struct {
				Status string `xml:"DAV: status"`
				Prop   struct {
					CTag      string `xml:"http://calendarserver.org/ns/ getctag"`
					SyncToken string `xml:"DAV: sync-token"`
				} `xml:"DAV: prop"`
			} `xml:"DAV: propstat"`
		} `xml:"DAV: response"`
	}
	if err := xml.NewDecoder(res.Body).Decode(&ans); err != nil {
		return State{}, fmt.Errorf("reading the state of the calendar: %w", err)
	}
	var st State
	for _, r := range ans.Responses {
		for _, ps := range r.Propstats {
			if statusCode(ps.Status)/100 != 2 {
				continue
			}
			if v := strings.TrimSpace(ps.Prop.CTag); v != "" {
				st.CTag = v
			}
			if v := strings.TrimSpace(ps.Prop.SyncToken); v != "" {
				st.SyncToken = v
			}
		}
	}
	return st, nil
}

// ErrSyncUnavailable says the server cannot answer a sync-collection REPORT:
// it does not offer RFC 6578, or it no longer knows the token. The caller
// lists the ETags instead.
var ErrSyncUnavailable = errors.New("the server cannot report changes since a sync token")

// Changes is the answer to a sync-collection REPORT: the objects that are new
// or changed since the token (address and ETag, no data), the addresses of
// the ones that are gone, and the token to continue from.
type Changes struct {
	Token   string
	Changed []Object
	Deleted []string
	// Truncated says the server answered with only part of the changes (RFC
	// 6578 3.6: 507 on the collection itself); Token is where to carry on.
	Truncated bool
}

const changesBody = `<?xml version="1.0" encoding="utf-8"?>` +
	`<d:sync-collection xmlns:d="DAV:"><d:sync-token>%s</d:sync-token><d:sync-level>1</d:sync-level>` +
	`<d:prop><d:getetag/></d:prop></d:sync-collection>`

// Changes asks a calendar what changed since a sync token (RFC 6578). A
// refusal other than a wrong login or a server fault (the REPORT is not
// known, the token is not valid) is ErrSyncUnavailable.
func (c *Client) Changes(ctx context.Context, calendarURL, token string) (Changes, error) {
	origin, _, err := c.origin(calendarURL)
	if err != nil {
		return Changes{}, err
	}
	dir := abs(origin, c.route(calendarURL))
	var esc bytes.Buffer
	xml.EscapeText(&esc, []byte(token))
	req, err := http.NewRequestWithContext(ctx, "REPORT", dir, strings.NewReader(fmt.Sprintf(changesBody, esc.String())))
	if err != nil {
		return Changes{}, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "0")
	res, err := c.auth.Do(req)
	if err != nil {
		var se *StatusError
		if errors.As(err, &se) && se.Code == http.StatusForbidden {
			return Changes{}, fmt.Errorf("%w: %v", ErrSyncUnavailable, err) // valid-sync-token precondition
		}
		return Changes{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus {
		if code := res.StatusCode; code >= 400 && code < 500 && code != http.StatusUnauthorized && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests || code == http.StatusNotImplemented {
			return Changes{}, fmt.Errorf("%w: %s", ErrSyncUnavailable, res.Status)
		}
		return Changes{}, &StatusError{res.StatusCode, res.Status}
	}
	var ans struct {
		reportAnswer
		Token string `xml:"DAV: sync-token"`
	}
	if err := xml.NewDecoder(res.Body).Decode(&ans); err != nil {
		return Changes{}, fmt.Errorf("reading the changes: %w", err)
	}
	if strings.TrimSpace(ans.Token) == "" {
		return Changes{}, fmt.Errorf("%w: the answer has no new token", ErrSyncUnavailable)
	}
	at, _ := url.Parse(dir)
	if res.Request != nil && res.Request.URL != nil {
		at = res.Request.URL
	}
	self := AddressKey(dir)
	out := Changes{Token: strings.TrimSpace(ans.Token)}
	for _, r := range ans.Responses {
		href, err := url.Parse(strings.TrimSpace(r.Href))
		if err != nil || strings.TrimSpace(r.Href) == "" {
			continue
		}
		target := at.ResolveReference(href)
		if err := c.auth.permit(target); err != nil {
			return Changes{}, err
		}
		if AddressKey(target.String()) == self && statusCode(r.Status) == http.StatusInsufficientStorage {
			out.Truncated = true // the server has more changes than it sent
		}
		if strings.HasSuffix(target.Path, "/") || AddressKey(target.String()) == self {
			if AddressKey(target.String()) == self && statusCode(r.Status) == http.StatusInsufficientStorage {
				out.Truncated = true // the server has more changes than it sent
			}
			continue // a collection
		}
		if code := statusCode(r.Status); code == http.StatusNotFound || code == http.StatusGone {
			out.Deleted = append(out.Deleted, target.String())
			continue
		}
		etag := ""
		for _, ps := range r.Propstats {
			if statusCode(ps.Status)/100 == 2 && strings.TrimSpace(ps.Prop.ETag) != "" {
				etag = unquoteETag(ps.Prop.ETag)
				break
			}
		}
		out.Changed = append(out.Changed, Object{URL: target.String(), ETag: etag})
	}
	return out, nil
}
