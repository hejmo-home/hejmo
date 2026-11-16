package caldavclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
)

// wellKnownServer is a CalDAV server that is entered as a bare host: it keeps
// everything under /dav/ and sends /.well-known/caldav there with the given
// redirect status (RFC 6764). Go's HTTP client would turn a followed 301, 302
// or 303 into a GET, which this server refuses for PROPFIND.
func wellKnownServer(t *testing.T, status int) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/.well-known/caldav":
			http.Redirect(w, r, "/dav/", status)
		case "/dav/":
			if r.Method != "PROPFIND" {
				http.Error(w, "PROPFIND only", http.StatusMethodNotAllowed)
				return
			}
			multistatus(w, response("/dav/", `<D:current-user-principal><D:href>/dav/me/</D:href></D:current-user-principal>`))
		case "/dav/me/":
			multistatus(w, response("/dav/me/", `<C:calendar-home-set><D:href>/dav/me/cals/</D:href></C:calendar-home-set>`))
		case "/dav/me/cals/":
			multistatus(w,
				response("/dav/me/cals/", `<D:resourcetype><D:collection/></D:resourcetype>`),
				response("/dav/me/cals/family/", `<D:resourcetype><D:collection/><C:calendar/></D:resourcetype><D:displayname>Family</D:displayname><C:supported-calendar-component-set><C:comp name="VEVENT"/></C:supported-calendar-component-set>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// A server entered as a bare host is found through /.well-known/caldav, and
// the redirect keeps PROPFIND whatever its status.
func TestCalendarsFollowWellKnownRedirectKeepingPropfind(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		srv, seen := wellKnownServer(t, status)
		c, err := caldavclient.NewClient(srv.URL, "u", "p", nil)
		if err != nil {
			t.Fatal(err)
		}
		cals, err := c.Calendars(context.Background())
		if err != nil {
			t.Fatalf("status %d: Calendars() = %v, want the calendar behind the redirect (requests: %v)", status, err, *seen)
		}
		if len(cals) != 1 || cals[0].Name != "Family" || cals[0].URL != srv.URL+"/dav/me/cals/family/" {
			t.Fatalf("status %d: Calendars() = %+v, want Family", status, cals)
		}
		if (*seen)[0] != "PROPFIND /.well-known/caldav" || (*seen)[1] != "PROPFIND /dav/" {
			t.Fatalf("status %d: requests = %v, want PROPFIND at /.well-known/caldav, then PROPFIND at /dav/", status, *seen)
		}
	}
}

// A server without a well-known address is still found at the address given.
func TestCalendarsFallBackWhenThereIsNoWellKnown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			multistatus(w, response("/", `<D:current-user-principal><D:href>/me/</D:href></D:current-user-principal>`))
		case "/me/":
			multistatus(w, response("/me/", `<C:calendar-home-set><D:href>/me/cals/</D:href></C:calendar-home-set>`))
		case "/me/cals/":
			multistatus(w, response("/me/cals/family/", `<D:resourcetype><D:collection/><C:calendar/></D:resourcetype><D:displayname>Family</D:displayname>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	cals, err := c.Calendars(context.Background())
	if err != nil || len(cals) != 1 {
		t.Fatalf("Calendars() = %+v, %v, want the calendar found at the address given", cals, err)
	}
}

// An address that names a path is used as given: no well-known guess.
func TestCalendarsDoNotTryWellKnownForAnAddressWithAPath(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	c, _ := caldavclient.NewClient(srv.URL+"/dav/", "u", "p", nil)
	_, _ = c.Calendars(context.Background())
	if len(seen) != 1 || seen[0] != "/dav/" {
		t.Fatalf("requests = %v, want only /dav/", seen)
	}
}

// A redirect to another domain is refused before credentials go there.
func TestWellKnownRedirectToAForeignHostIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example.net/dav/", http.StatusMovedPermanently)
	}))
	t.Cleanup(srv.Close)
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	_, err := c.Calendars(context.Background())
	var foreign *caldavclient.ForeignHostError
	if !asForeign(err, &foreign) {
		t.Fatalf("Calendars() = %v, want a ForeignHostError", err)
	}
}

func asForeign(err error, target **caldavclient.ForeignHostError) bool { return errors.As(err, target) }

// A proxy may forbid /.well-known while the server answers at the address
// given; only a refused login ends the discovery.
func TestCalendarsFallBackWhenWellKnownIsForbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/caldav":
			http.Error(w, "no", http.StatusForbidden)
		case "/":
			multistatus(w, response("/", `<D:current-user-principal><D:href>/me/</D:href></D:current-user-principal>`))
		case "/me/":
			multistatus(w, response("/me/", `<C:calendar-home-set><D:href>/me/cals/</D:href></C:calendar-home-set>`))
		case "/me/cals/":
			multistatus(w, response("/me/cals/family/", `<D:resourcetype><D:collection/><C:calendar/></D:resourcetype><D:displayname>Family</D:displayname>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	cals, err := c.Calendars(context.Background())
	if err != nil || len(cals) != 1 {
		t.Fatalf("Calendars() = %+v, %v, want the calendar found at the address given", cals, err)
	}
}
