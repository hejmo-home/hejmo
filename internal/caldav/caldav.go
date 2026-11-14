// Package caldav reads calendars from a CalDAV server (iCloud, Nextcloud,
// Radicale and others). It is a thin adapter over go-webdav (ADR 0012): it
// speaks in absolute URLs and raw iCalendar text and knows nothing of the
// box's calendars or database.
package caldav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	davcal "github.com/emersion/go-webdav/caldav"
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

// Client talks to one CalDAV server with one set of credentials.
type Client struct {
	base *url.URL
	dav  *davcal.Client
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
	dav, err := davcal.NewClient(&authClient{hc: hc, user: user, password: password}, base.String())
	if err != nil {
		return nil, err
	}
	return &Client{base: base, dav: dav}, nil
}

// authClient adds basic auth and turns refusals into StatusError.
type authClient struct {
	hc             *http.Client
	user, password string
}

func (a *authClient) Do(req *http.Request) (*http.Response, error) {
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

func (c *Client) abs(p string) string {
	u := *c.base
	u.RawQuery, u.Fragment = "", ""
	ref, err := url.Parse(p)
	if err != nil {
		return p
	}
	return u.ResolveReference(ref).String()
}

// route turns an address into what go-webdav expects: a path on the server.
func (c *Client) route(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return address
	}
	return u.EscapedPath()
}

// Calendars discovers the server's event calendars of the signed-in user.
func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	principal, err := c.dav.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	home, err := c.dav.FindCalendarHomeSet(ctx, principal)
	if err != nil {
		return nil, err
	}
	found, err := c.dav.FindCalendars(ctx, home)
	if err != nil {
		return nil, err
	}
	var out []Calendar
	for _, f := range found {
		if len(f.SupportedComponentSet) > 0 && !slices.Contains(f.SupportedComponentSet, ical.CompEvent) {
			continue // a task list or notes
		}
		out = append(out, Calendar{URL: c.abs(f.Path), Name: nameOf(f.Name, f.Path)})
	}
	return out, nil
}

// Calendar checks one calendar address given by hand and names it.
func (c *Client) Calendar(ctx context.Context, calendarURL string) (Calendar, error) {
	parent := path.Dir(strings.TrimSuffix(c.route(calendarURL), "/")) + "/"
	found, err := c.dav.FindCalendars(ctx, parent)
	if err != nil {
		return Calendar{}, err
	}
	want := strings.TrimSuffix(c.route(calendarURL), "/")
	for _, f := range found {
		if strings.TrimSuffix(f.Path, "/") == want {
			return Calendar{URL: c.abs(f.Path), Name: nameOf(f.Name, f.Path)}, nil
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

// List returns the objects of a calendar with their ETags, without their data.
func (c *Client) List(ctx context.Context, calendarURL string) ([]Object, error) {
	dir := c.route(calendarURL)
	infos, err := c.dav.ReadDir(ctx, dir, false)
	if err != nil {
		return nil, err
	}
	var out []Object
	for _, fi := range infos {
		if fi.IsDir || strings.TrimSuffix(fi.Path, "/") == strings.TrimSuffix(dir, "/") {
			continue
		}
		out = append(out, Object{URL: c.abs(fi.Path), ETag: fi.ETag})
	}
	return out, nil
}

// Fetch downloads the given objects (at most one request).
func (c *Client) Fetch(ctx context.Context, calendarURL string, objectURLs []string) ([]Object, error) {
	if len(objectURLs) == 0 {
		return nil, nil
	}
	paths := make([]string, len(objectURLs))
	for i, u := range objectURLs {
		paths[i] = c.route(u)
	}
	got, err := c.dav.MultiGetCalendar(ctx, c.route(calendarURL), &davcal.CalendarMultiGet{
		Paths:       paths,
		CompRequest: davcal.CalendarCompRequest{Name: ical.CompCalendar, AllProps: true, AllComps: true},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Object, 0, len(got))
	for _, o := range got {
		var buf bytes.Buffer
		if err := ical.NewEncoder(&buf).Encode(o.Data); err != nil {
			return nil, fmt.Errorf("object %s: %w", o.Path, err)
		}
		out = append(out, Object{URL: c.abs(o.Path), ETag: o.ETag, Data: buf.Bytes()})
	}
	return out, nil
}
