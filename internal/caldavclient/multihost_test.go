package caldavclient_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/caldavclient/caldavtest"
)

// fakeHost is one CalDAV host of a fake multi-host server (the way iCloud
// answers from caldav.icloud.com and from p123-caldav.icloud.com). It records
// whether credentials arrived.
type fakeHost struct {
	srv  *httptest.Server
	mu   sync.Mutex
	auth int // requests that carried credentials
	hits int
}

func (h *fakeHost) sawCredentials() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.auth > 0
}

func (h *fakeHost) requests() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hits
}

func startFakeHost(t *testing.T, handler http.HandlerFunc) *fakeHost {
	t.Helper()
	h := new(fakeHost)
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.hits++
		if _, _, ok := r.BasicAuth(); ok {
			h.auth++
		}
		h.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

// multiHostClient returns a client whose requests for the given host names
// are all dialed to the matching fake host, whatever port the address names.
func multiHostClient(t *testing.T, serverURL string, hosts map[string]*fakeHost) *caldavclient.Client {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		name, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		h, ok := hosts[name]
		if !ok {
			return nil, fmt.Errorf("no fake host %q", name)
		}
		return (&net.Dialer{}).DialContext(ctx, network, h.srv.Listener.Addr().String())
	}}
	t.Cleanup(tr.CloseIdleConnections)
	c, err := caldavclient.NewClient(serverURL, caldavtest.User, caldavtest.Password, &http.Client{Transport: tr})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func multistatus(w http.ResponseWriter, responses ...string) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	fmt.Fprint(w, `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">`+strings.Join(responses, "")+`</D:multistatus>`)
}

func response(href, props string) string {
	return `<D:response><D:href>` + href + `</D:href><D:propstat><D:prop>` + props + `</D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`
}

const (
	home = "http://p123-caldav.icloud.example/123/calendars/"
	cal  = "http://p123-caldav.icloud.example/123/calendars/family/"
)

// iCloud's shape: the entry host knows the principal (a relative address) and
// points to a calendar home on another host of the same domain, which holds
// the calendars and their objects.
func icloudLikeHosts(t *testing.T) map[string]*fakeHost {
	t.Helper()
	entry := startFakeHost(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			multistatus(w, response("/", `<D:current-user-principal><D:href>/123/principal/</D:href></D:current-user-principal>`))
		case "/123/principal/":
			multistatus(w, response("/123/principal/", `<C:calendar-home-set><D:href>`+home+`</D:href></C:calendar-home-set>`))
		default:
			http.NotFound(w, r)
		}
	})
	partition := startFakeHost(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/123/calendars/":
			multistatus(w,
				response("/123/calendars/", `<D:resourcetype><D:collection/></D:resourcetype>`),
				response("/123/calendars/family/", `<D:resourcetype><D:collection/><C:calendar/></D:resourcetype><D:displayname>Family</D:displayname><C:supported-calendar-component-set><C:comp name="VEVENT"/></C:supported-calendar-component-set>`))
		case "/123/calendars/family/":
			multistatus(w,
				response("/123/calendars/family/", `<D:resourcetype><D:collection/><C:calendar/></D:resourcetype>`),
				response("/123/calendars/family/a.ics", `<D:resourcetype/><D:getetag>"v1"</D:getetag><D:getcontentlength>10</D:getcontentlength><D:getcontenttype>text/calendar</D:getcontenttype>`))
		default:
			http.NotFound(w, r)
		}
	})
	return map[string]*fakeHost{"caldav.icloud.example": entry, "p123-caldav.icloud.example": partition}
}

func TestDiscoveryFollowsTheCalendarHomeToAnotherHostOfTheSameDomain(t *testing.T) {
	hosts := icloudLikeHosts(t)
	c := multiHostClient(t, "http://caldav.icloud.example/", hosts)

	cals, err := c.Calendars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cals) != 1 || cals[0].Name != "Family" || cals[0].URL != cal {
		t.Fatalf("Calendars() = %+v, want Family at %s", cals, cal)
	}

	objs, err := c.List(context.Background(), cals[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].URL != cal+"a.ics" || objs[0].ETag != "v1" {
		t.Fatalf("List() = %+v", objs)
	}
	if !hosts["p123-caldav.icloud.example"].sawCredentials() {
		t.Error("the partition host never got credentials, so it cannot have answered")
	}
}

func TestRequestsFollowARedirectToAnotherHostOfTheSameDomain(t *testing.T) {
	hosts := icloudLikeHosts(t)
	// The entry host now only redirects discovery to the partition host.
	hosts["caldav.icloud.example"] = startFakeHost(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://p123-caldav.icloud.example"+r.URL.Path, http.StatusTemporaryRedirect)
	})
	c := multiHostClient(t, "http://caldav.icloud.example/", hosts)

	objs, err := c.List(context.Background(), cal)
	if err != nil || len(objs) != 1 {
		t.Fatalf("List() = %+v, %v", objs, err)
	}

	// The calendar address on the entry host redirects to the partition host.
	objs, err = c.List(context.Background(), "http://caldav.icloud.example/123/calendars/family/")
	if err != nil || len(objs) != 1 {
		t.Fatalf("List() through a redirect = %+v, %v", objs, err)
	}
	if !hosts["p123-caldav.icloud.example"].sawCredentials() {
		t.Error("credentials did not reach the host the redirect led to")
	}
}

func TestRedirectToAForeignDomainIsRefusedAndCredentialsStayHome(t *testing.T) {
	foreign := startFakeHost(t, func(w http.ResponseWriter, r *http.Request) {
		multistatus(w, response("/", `<D:current-user-principal><D:href>/x/</D:href></D:current-user-principal>`))
	})
	entry := startFakeHost(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example"+r.URL.Path, http.StatusTemporaryRedirect)
	})
	c := multiHostClient(t, "http://caldav.icloud.example/", map[string]*fakeHost{
		"caldav.icloud.example": entry, "evil.example": foreign,
	})

	_, err := c.Calendars(context.Background())
	if err == nil || !strings.Contains(err.Error(), "evil.example") || !strings.Contains(err.Error(), "icloud.example") {
		t.Fatalf("Calendars() error = %v, want a clear refusal naming both domains", err)
	}
	if foreign.requests() != 0 {
		t.Errorf("the foreign host got %d requests, want none", foreign.requests())
	}
}

func TestCalendarHomeOnAForeignDomainIsRefusedAndCredentialsStayHome(t *testing.T) {
	foreign := startFakeHost(t, func(w http.ResponseWriter, r *http.Request) {
		multistatus(w)
	})
	entry := startFakeHost(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			multistatus(w, response("/", `<D:current-user-principal><D:href>/123/principal/</D:href></D:current-user-principal>`))
		default:
			multistatus(w, response(r.URL.Path, `<C:calendar-home-set><D:href>http://evil.example/123/calendars/</D:href></C:calendar-home-set>`))
		}
	})
	c := multiHostClient(t, "http://caldav.icloud.example/", map[string]*fakeHost{
		"caldav.icloud.example": entry, "evil.example": foreign,
	})

	_, err := c.Calendars(context.Background())
	if err == nil || !strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("Calendars() error = %v, want a refusal naming the foreign host", err)
	}
	// The same for an address handed to List or Fetch.
	if _, err := c.List(context.Background(), "http://evil.example/123/calendars/family/"); err == nil {
		t.Error("List() on a foreign domain succeeded")
	}
	if _, err := c.Fetch(context.Background(), "http://evil.example/c/", []string{"http://evil.example/c/a.ics"}); err == nil {
		t.Error("Fetch() on a foreign domain succeeded")
	}
	if foreign.requests() != 0 {
		t.Errorf("the foreign host got %d requests, want none", foreign.requests())
	}
}
