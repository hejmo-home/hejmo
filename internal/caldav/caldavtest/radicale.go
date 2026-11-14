// Package caldavtest runs a real Radicale CalDAV server in a container for
// tests (ADR 0012). Tests that need it are skipped where Docker is missing,
// and fail loudly if it is there but Radicale does not come up.
package caldavtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

// Start runs Radicale for the duration of the test.
func Start(t *testing.T) *Server {
	t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not found: Radicale tests need a container runtime")
	}
	if err := exec.Command(docker, "info").Run(); err != nil {
		t.Skip("docker daemon not reachable: Radicale tests need a container runtime")
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
