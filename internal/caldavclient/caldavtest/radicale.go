// Package caldavtest runs a real Radicale CalDAV server in a container for
// tests (ADR 0012). Tests that need it are skipped where Docker is missing
// unless HEJMO_REQUIRE_RADICALE=1 is set (CI does), which turns the skip into
// a failure. They fail loudly if Docker is there but Radicale does not come up.
package caldavtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Image is the Radicale container image the tests run, pinned to a release so
// a new upstream image cannot break CI unannounced.
const Image = "tomsquest/docker-radicale:3.9.1.1"

// pulled makes the first Start download the image once, so a cold runner does
// not spend the startup deadline on the download.
var (
	pullOnce sync.Once
	pullErr  error
	pullOut  []byte
)

// User and Password sign in to the test server.
const (
	User     = "mama"
	Password = "test-app-password"
)

// Server is a running Radicale.
type Server struct {
	// URL is the server root, e.g. http://127.0.0.1:49152/.
	URL string
}

const radicaleConfig = `[server]
hosts = 0.0.0.0:5232

[auth]
type = htpasswd
htpasswd_filename = /config/users
htpasswd_encryption = plain

[rights]
type = owner_only

[storage]
filesystem_folder = /data/collections
`

// RequireEnv is the environment variable that turns a missing container
// runtime from a skip into a failure.
const RequireEnv = "HEJMO_REQUIRE_RADICALE"

// unavailable skips the test, or fails it when RequireEnv is 1.
func unavailable(t *testing.T, reason string) {
	t.Helper()
	if os.Getenv(RequireEnv) == "1" {
		t.Fatalf("%s (%s=1, so the test must not be skipped)", reason, RequireEnv)
	}
	t.Skip(reason)
}

// Start runs Radicale for the duration of the test.
func Start(t *testing.T) *Server {
	t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		unavailable(t, "docker not found: Radicale tests need a container runtime")
	}
	if err := exec.Command(docker, "info").Run(); err != nil {
		unavailable(t, "docker daemon not reachable: Radicale tests need a container runtime")
	}
	pullOnce.Do(func() {
		pullOut, pullErr = exec.Command(docker, "pull", "--quiet", Image).CombinedOutput()
	})
	if pullErr != nil {
		t.Fatalf("pulling %s: %v\n%s", Image, pullErr, pullOut)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"config": radicaleConfig, "users": User + ":" + Password + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command(docker, "run", "-d", "-p", "127.0.0.1::5232", "-v", dir+":/config:ro", Image).CombinedOutput()
	if err != nil {
		t.Fatalf("starting Radicale: %v\n%s", err, out)
	}
	id := strings.TrimSpace(string(out))
	// No --rm: the container must outlive a crash so its log can be read; the
	// cleanup below removes it.
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := exec.Command(docker, "logs", "--tail", "30", id).CombinedOutput()
			t.Logf("Radicale log:\n%s", logs)
		}
		exec.Command(docker, "rm", "-f", id).Run()
	})
	port, err := exec.Command(docker, "port", id, "5232/tcp").Output()
	if err != nil {
		t.Fatalf("Radicale port: %v (container is %s)", err, state(docker, id))
	}
	// "127.0.0.1:49152" (possibly followed by an IPv6 line)
	addr := strings.TrimSpace(strings.SplitN(string(port), "\n", 2)[0])
	s := &Server{URL: "http://" + addr + "/"}

	deadline := time.Now().Add(60 * time.Second)
	for {
		res, err := http.Get(s.URL)
		if err == nil {
			res.Body.Close()
			if res.StatusCode < 500 {
				return s
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Radicale did not start in time (last error: %v)", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// CalendarURL is the address of the test user's calendar called name.
func (s *Server) CalendarURL(name string) string { return s.URL + User + "/" + name + "/" }

// MakeCalendar creates a calendar collection with a display name.
func (s *Server) MakeCalendar(t *testing.T, name, displayName string) string {
	t.Helper()
	body := `<?xml version="1.0" encoding="utf-8"?>
<C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:set><D:prop>
<D:displayname>` + displayName + `</D:displayname>
<C:supported-calendar-component-set><C:comp name="VEVENT"/></C:supported-calendar-component-set>
</D:prop></D:set></C:mkcalendar>`
	s.do(t, "MKCALENDAR", s.CalendarURL(name), body, "application/xml", http.StatusCreated)
	return s.CalendarURL(name)
}

// Put stores an event object (a complete VCALENDAR text) and returns its URL.
func (s *Server) Put(t *testing.T, calendarURL, file, ics string) string {
	t.Helper()
	u := calendarURL + file
	s.do(t, http.MethodPut, u, ics, "text/calendar; charset=utf-8", http.StatusCreated, http.StatusNoContent)
	return u
}

// Tamper sits between the client and Radicale and makes objects unreadable on
// the wire: multiget answers and plain downloads of the objects named in Break
// (file names) carry text that is not iCalendar. Radicale refuses to store such
// objects, but other servers hand them out. Reports counts multiget requests.
type Tamper struct {
	Reports atomic.Int32

	mu       sync.Mutex
	broken   []string
	refuse   int      // status answered for REPORTs and for downloads of refused files
	refusing []string // file names whose download is refused
	writes   int      // status answered for PUT and DELETE
	seen     map[string]int
	after    string // Retry-After header sent with the refusals of PUT and DELETE
	state    int    // status answered for the PROPFIND of a calendar's state (Depth 0)
	stateAt  string // Retry-After header sent with those refusals
}

// RefuseState makes every PROPFIND with Depth 0 (the read of a calendar's
// CTag and sync token) answer with status and a Retry-After header, until it
// is called with 0: a server that throttles before the import lists anything.
func (tp *Tamper) RefuseState(status int, retryAfter string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.state, tp.stateAt = status, retryAfter
}

// RefuseWritesWait is RefuseWrites with a Retry-After header (seconds or an
// HTTP date) on every refusal: a server that throttles.
func (tp *Tamper) RefuseWritesWait(status int, retryAfter string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.writes, tp.after = status, retryAfter
}

// Count returns how many requests with this method reached the Tamper, refused
// ones included.
func (tp *Tamper) Count(method string) int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.seen[method]
}

// RefuseWrites makes every PUT and DELETE answer with status until it is
// called with 0: a server that is down for writes.
func (tp *Tamper) RefuseWrites(status int) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.writes, tp.after = status, ""
}

// Refuse makes every REPORT, and the download of the named objects, answer
// with status (503 for an overloaded server, 429 for a throttled one) until
// Refuse(0) is called. Other downloads work. This is a temporary failure of
// the server, not unreadable data.
func (tp *Tamper) Refuse(status int, files ...string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.refuse, tp.refusing = status, files
}

// refused returns the status to answer instead of asking the server.
func (tp *Tamper) refused(req *http.Request) int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.writes != 0 && (req.Method == http.MethodPut || req.Method == http.MethodDelete) {
		return tp.writes
	}
	if tp.state != 0 && req.Method == "PROPFIND" && req.Header.Get("Depth") == "0" {
		return tp.state
	}
	switch {
	case tp.refuse == 0:
		return 0
	case req.Method == "REPORT":
		return tp.refuse
	case req.Method == http.MethodGet && slices.Contains(tp.refusing, path.Base(req.URL.Path)):
		return tp.refuse
	}
	return 0
}

// Break makes exactly these objects unreadable from now on.
func (tp *Tamper) Break(files ...string) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.broken = files
}

// Client is an HTTP client that goes through the Tamper.
func (tp *Tamper) Client() *http.Client { return &http.Client{Transport: tamper{tp}} }

type tamper struct{ *Tamper }

func (tp *Tamper) isBroken(f string) bool {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return slices.Contains(tp.broken, f)
}

var (
	responseBlock = regexp.MustCompile(`(?s)<(\w+:)?response>.*?</(\w+:)?response>`)
	hrefOf        = regexp.MustCompile(`<(?:\w+:)?href>([^<]*)</`)
	calendarData  = regexp.MustCompile(`(?s)(<(?:\w+:)?calendar-data[^>]*>).*?(</(?:\w+:)?calendar-data>)`)
)

func (tr tamper) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.mu.Lock()
	if tr.seen == nil {
		tr.seen = map[string]int{}
	}
	tr.seen[req.Method]++
	after := tr.after
	if req.Method == "PROPFIND" {
		after = tr.stateAt
	}
	tr.mu.Unlock()
	if status := tr.refused(req); status != 0 {
		header := http.Header{}
		if after != "" && (req.Method == http.MethodPut || req.Method == http.MethodDelete || req.Method == "PROPFIND") {
			header.Set("Retry-After", after)
		}
		return &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header:     header,
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	}
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return res, err
	}
	if req.Method == http.MethodGet && res.StatusCode == http.StatusOK {
		if tr.isBroken(path.Base(req.URL.Path)) {
			res.Body.Close()
			res.Body = io.NopCloser(strings.NewReader("this is not a calendar"))
			res.Header.Del("Content-Length")
		}
	}
	if req.Method != "REPORT" {
		return res, nil
	}
	tr.Reports.Add(1)
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return nil, err
	}
	out := responseBlock.ReplaceAllStringFunc(string(body), func(block string) string {
		if m := hrefOf.FindStringSubmatch(block); m != nil && tr.isBroken(path.Base(m[1])) {
			return calendarData.ReplaceAllString(block, "${1}this is not a calendar${2}")
		}
		return block
	})
	res.Body = io.NopCloser(strings.NewReader(out))
	res.ContentLength = int64(len(out))
	res.Header.Del("Content-Length")
	return res, nil
}

// Get returns the text of an object, or "" if there is none.
func (s *Server) Get(t *testing.T, objectURL string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, objectURL, nil)
	req.SetBasicAuth(User, Password)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusNotFound {
		return ""
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", objectURL, res.StatusCode)
	}
	return string(b)
}

// Delete removes an object.
func (s *Server) Delete(t *testing.T, objectURL string) {
	t.Helper()
	s.do(t, http.MethodDelete, objectURL, "", "", http.StatusNoContent, http.StatusOK)
}

// Event builds a minimal VCALENDAR with one timed event in Europe/Berlin.
func Event(uid, summary, start, end string) string {
	return fmt.Sprintf("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Hejmo test//EN\r\nBEGIN:VEVENT\r\nUID:%s\r\nDTSTAMP:20261114T100000Z\r\nDTSTART;TZID=Europe/Berlin:%s\r\nDTEND;TZID=Europe/Berlin:%s\r\nSUMMARY:%s\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n",
		uid, start, end, summary)
}

func (s *Server) do(t *testing.T, method, url, body, contentType string, want ...int) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(User, Password)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	for _, w := range want {
		if res.StatusCode == w {
			return
		}
	}
	t.Fatalf("%s %s: status %d, body %s", method, url, res.StatusCode, b)
}

// state describes whether the container is running, for failure messages.
func state(docker, id string) string {
	out, err := exec.Command(docker, "inspect", "--format", "{{.State.Status}} (exit {{.State.ExitCode}}) {{.State.Error}}", id).CombinedOutput()
	if err != nil {
		return "unknown: " + strings.TrimSpace(string(out))
	}
	return strings.TrimSpace(string(out))
}
