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
	"io"
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
	// Err is set by Fetch when the server sent the object but it cannot be
	// read as iCalendar. The other objects of the same Fetch are unaffected.
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

// Client talks to one CalDAV server with one set of credentials.
type Client struct {
	base *url.URL
	dav  *davcal.Client
	auth *authClient
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
	auth := &authClient{hc: hc, user: user, password: password}
	dav, err := davcal.NewClient(auth, base.String())
	if err != nil {
		return nil, err
	}
	return &Client{base: base, dav: dav, auth: auth}, nil
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

// fetchBatch is how many objects one multiget asks for, so that the first
// sync of a calendar with years of events is not one huge request.
const fetchBatch = 100

// Fetch downloads the given objects, fetchBatch at a time. An object that was
// deleted since it was listed is left out. An object the server sent but that
// cannot be read comes back with Err set. Neither fails the others.
func (c *Client) Fetch(ctx context.Context, calendarURL string, objectURLs []string) ([]Object, error) {
	var out []Object
	for batch := range slices.Chunk(objectURLs, fetchBatch) {
		got, err := c.multiget(ctx, calendarURL, batch)
		if err != nil {
			if IsUnauthorized(err) || ctx.Err() != nil {
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
		obj := Object{URL: c.abs(o.Path), ETag: o.ETag}
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
// left out; one that cannot be read, or that the server refuses to give,
// carries the reason in Err.
func (c *Client) fetchEach(ctx context.Context, objectURLs []string) ([]Object, error) {
	var out []Object
	for _, u := range objectURLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.abs(c.route(u)), nil)
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
			obj.Err = fmt.Errorf("the server answered %s", res.Status)
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
