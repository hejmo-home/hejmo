// Package caldav reads calendars from a CalDAV server (iCloud, Nextcloud,
// Radicale and others). It is a thin adapter over go-webdav (ADR 0012): it
// speaks in absolute URLs and raw iCalendar text and knows nothing of the
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
}

func (e *ForeignHostError) Error() string {
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
		if registrable(host) == domain {
			host = u.Scheme + "://" + host
		}
		return &ForeignHostError{Host: host, Domain: domain}
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
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", from.String(), strings.NewReader(fmt.Sprintf(propfindBody, prop)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "0")
	res, err := c.auth.Do(req)
	if err != nil {
		return nil, err
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
			at := from
			if res.Request != nil && res.Request.URL != nil {
				at = res.Request.URL
			}
			target := at.ResolveReference(ref)
			if err := c.auth.permit(target); err != nil {
				return nil, err
			}
			return target, nil
		}
	}
	return nil, fmt.Errorf("the server does not say where the %s is", strings.TrimPrefix(strings.TrimPrefix(prop, "c:"), "d:"))
}

// Calendars discovers the server's event calendars of the signed-in user.
func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	principal, err := c.findHref(ctx, c.base, "d:current-user-principal")
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

type listAnswer struct {
	Responses []struct {
		Href      string `xml:"DAV: href"`
		Propstats []struct {
			Status string `xml:"DAV: status"`
			Prop   struct {
				ETag string `xml:"DAV: getetag"`
			} `xml:"DAV: prop"`
		} `xml:"DAV: propstat"`
	} `xml:"DAV: response"`
}

// List returns the objects of a calendar with their ETags, without their data.
//
// It does not use go-webdav's directory listing: that decodes resourcetype of
// every member and fails the whole listing when one answers 404 for it, which
// iCloud does for some members. Here only the 200 propstats are read, and a
// member without an ETag, or a collection, is skipped.
func (c *Client) List(ctx context.Context, calendarURL string) ([]Object, error) {
	origin, _, err := c.origin(calendarURL)
	if err != nil {
		return nil, err
	}
	dir := abs(origin, c.route(calendarURL))
	req, err := http.NewRequestWithContext(ctx, "REPORT", dir, strings.NewReader(listBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "1")
	res, err := c.auth.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus {
		return nil, &StatusError{res.StatusCode, res.Status}
	}
	var ans listAnswer
	if err := xml.NewDecoder(res.Body).Decode(&ans); err != nil {
		return nil, fmt.Errorf("reading the list of events: %w", err)
	}
	// Resolve against the address that finally answered, which may be on
	// another host than the one asked after a redirect.
	at, _ := url.Parse(dir)
	if res.Request != nil && res.Request.URL != nil {
		at = res.Request.URL
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
		etag := ""
		for _, ps := range r.Propstats {
			if strings.Contains(ps.Status, " 200") && strings.TrimSpace(ps.Prop.ETag) != "" {
				etag = unquoteETag(ps.Prop.ETag)
				break
			}
		}
		if etag == "" {
			continue // not an object we can track
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
			// One object the multiget cannot take (go-webdav decodes every
			// object and fails the whole answer on the first it cannot read,
			// or on a 404), so ask for each on its own.
			if got, err = c.fetchEach(ctx, batch); err != nil {
				return nil, err
			}
		}
		out = append(out, got...)
	}
	return out, nil
}

func (c *Client) multiget(ctx context.Context, calendarURL string, objectURLs []string) ([]Object, error) {
	origin, dav, err := c.origin(calendarURL)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(objectURLs))
	for i, u := range objectURLs {
		paths[i] = c.route(u)
	}
	got, err := dav.MultiGetCalendar(ctx, c.route(calendarURL), &davcal.CalendarMultiGet{
		Paths:       paths,
		CompRequest: davcal.CalendarCompRequest{Name: ical.CompCalendar, AllProps: true, AllComps: true},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Object, 0, len(got))
	for _, o := range got {
		var buf bytes.Buffer
		obj := Object{URL: abs(origin, o.Path), ETag: o.ETag}
		if err := ical.NewEncoder(&buf).Encode(o.Data); err != nil {
			obj.Err = err
		} else {
			obj.Data = buf.Bytes()
		}
		out = append(out, obj)
	}
	return out, nil
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
