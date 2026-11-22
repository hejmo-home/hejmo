package server_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/weather"
)

// clock is a settable time source that is safe to move while a stream reads it.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.t = t }

// liveBox is a family box whose box also runs a second handler on the same
// database with its own clock and live-update interval; the display talks to it.
type liveBox struct {
	*familyBox
	clock *clock
	url   string
}

func newLiveBox(t *testing.T, interval time.Duration) *liveBox {
	t.Helper()
	f := newFamilyBox(t, "eink_bw")
	c := &clock{t: f.at(14, 8, 0)}
	// The weather reading ages with the live box's clock, not the family box's.
	f.weather = weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL(f.meteo.srv.URL), weather.WithClock(c.now))
	h := server.New(server.Options{DB: f.db, PKI: f.pki, Now: c.now, LiveInterval: interval, Weather: f.weather})
	srv := httptest.NewUnstartedServer(h)
	cert, err := f.pki.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{*cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return &liveBox{familyBox: f, clock: c, url: srv.URL}
}

func (l *liveBox) get(path string) (int, http.Header, string) {
	l.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, l.url+path, nil)
	req.Header.Set("Accept-Language", "en")
	res, err := l.display.client.Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	defer res.Body.Close()
	var sb strings.Builder
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		sb.WriteString(sc.Text() + "\n")
	}
	return res.StatusCode, res.Header, sb.String()
}

// stream is an open /display/events connection; versions delivers each
// announced view version.
type stream struct {
	versions chan string
	cancel   func()
}

func (l *liveBox) open(t *testing.T) *stream { return l.openPath(t, "/display/events") }

func (l *liveBox) openPath(t *testing.T, path string) *stream {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, l.url+path, nil)
	req.Header.Set("Accept-Language", "en")
	res, err := l.display.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("GET %s = %d %q", path, res.StatusCode, res.Header.Get("Content-Type"))
	}
	s := &stream{versions: make(chan string, 100), cancel: func() { res.Body.Close() }}
	t.Cleanup(s.cancel)
	go func() {
		sc := bufio.NewScanner(res.Body)
		ev := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && ev == "view":
				s.versions <- strings.TrimPrefix(line, "data: ")
			case line == "":
				ev = ""
			}
		}
		close(s.versions)
	}()
	return s
}

func (s *stream) next(t *testing.T, within time.Duration) (string, bool) {
	t.Helper()
	select {
	case v, ok := <-s.versions:
		return v, ok
	case <-time.After(within):
		return "", false
	}
}

func (s *stream) first(t *testing.T) string {
	t.Helper()
	v, ok := s.next(t, 5*time.Second)
	if !ok || v == "" {
		t.Fatal("no view version announced on connect")
	}
	return v
}

var versionRe = regexp.MustCompile(`data-version="([0-9a-f]+)"`)

// 5.5: Given an E-Ink display, when an event is changed, then the change is visible within 10 seconds.
// Runs with the box's default interval, so it checks the real promise.
func TestLiveChangedEventIsAnnouncedAndFetchedWithin10Seconds(t *testing.T) {
	t.Parallel()
	l := newLiveBox(t, 0)
	_, _, page := l.get("/display")
	pageVersion := versionRe.FindStringSubmatch(page)
	if pageVersion == nil {
		t.Fatalf("page carries no data-version:\n%s", page)
	}
	s := l.open(t)
	if v := s.first(t); v != pageVersion[1] {
		t.Fatalf("announced %q on connect, page shows %q", v, pageVersion[1])
	}
	start := time.Now()
	l.event(t, "Dentist", l.at(14, 9, 0), l.at(14, 10, 0), false, "Mama")
	v, ok := s.next(t, 10*time.Second)
	if !ok || v == pageVersion[1] {
		t.Fatalf("no new version within 10 seconds (got %q, ok=%v)", v, ok)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v", d)
	}
	code, hdr, body := l.get("/display/view")
	if code != 200 || !strings.Contains(body, "Dentist") {
		t.Fatalf("GET /display/view = %d, body lacks the event:\n%s", code, body)
	}
	if hdr.Get("X-Hejmo-View") != v {
		t.Fatalf("view version %q, announced %q", hdr.Get("X-Hejmo-View"), v)
	}
}

// 5.5: Given an E-Ink display, when nothing changes, then no new image is transmitted.
func TestLiveNothingIsSentWhenNothingChanges(t *testing.T) {
	t.Parallel()
	l := newLiveBox(t, 10*time.Millisecond)
	s := l.open(t)
	s.first(t)
	if v, ok := s.next(t, 400*time.Millisecond); ok {
		t.Fatalf("announced %q although nothing changed", v)
	}
}

// 5.5 (from PR #64): the 18:00 block and the day rollover update without a reload.
func TestLiveEveningAndMidnightAreAnnounced(t *testing.T) {
	t.Parallel()
	l := newLiveBox(t, 10*time.Millisecond)
	l.showTomorrow(t, true)
	l.event(t, "Tomorrow school", l.at(15, 8, 0), l.at(15, 9, 0), false, "Mia")
	s := l.open(t)
	seen := s.first(t)
	for _, step := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"18:00 tomorrow block", l.at(14, 18, 0), "data-tomorrow"},
		{"midnight rollover", l.at(15, 0, 0), "Tomorrow school"},
	} {
		l.clock.set(step.at)
		v, ok := s.next(t, 2*time.Second)
		if !ok || v == seen {
			t.Fatalf("%s: no new version announced", step.name)
		}
		seen = v
		if _, _, body := l.get("/display/view"); !strings.Contains(body, step.want) {
			t.Fatalf("%s: view lacks %q:\n%s", step.name, step.want, body)
		}
	}
}

// ADR 0016: the clock chip is a partial update the display ticks itself. A new
// minute alone must not change the view version, or every display would
// repaint its whole view once a minute.
func TestLiveClockMinuteKeepsTheVersion(t *testing.T) {
	t.Parallel()
	l := newLiveBox(t, 10*time.Millisecond)
	s := l.open(t)
	first := s.first(t)
	l.clock.set(l.at(14, 8, 1))
	if v, ok := s.next(t, 400*time.Millisecond); ok {
		t.Fatalf("announced %q for a new minute alone", v)
	}
	code, hdr, body := l.get("/display/view")
	if code != 200 || !strings.Contains(body, "08:01") || !strings.Contains(body, "data-clock-zone=") {
		t.Fatalf("view lacks the current clock or its zone:\n%s", body)
	}
	if hdr.Get("X-Hejmo-View") != first {
		t.Fatalf("view version %q after a minute, was %q", hdr.Get("X-Hejmo-View"), first)
	}
}

// Shutdown must not wait for open streams: they end when the box is told to stop.
func TestLiveShutdownEndsOpenStreams(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_bw")
	done := make(chan struct{})
	c := &clock{t: f.at(14, 8, 0)}
	h := server.New(server.Options{DB: f.db, PKI: f.pki, Now: c.now, LiveInterval: 10 * time.Millisecond, Done: done})
	srv := httptest.NewUnstartedServer(h)
	cert, err := f.pki.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{*cert}}
	srv.StartTLS()
	defer srv.Close()
	l := &liveBox{familyBox: f, clock: c, url: srv.URL}
	l.open(t).first(t)

	close(done)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Config.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with a stream open: %v", err)
	}
}

// 5.5: Given a lost connection, then the display reconnects and catches up.
func TestLiveReconnectAnnouncesTheCurrentVersion(t *testing.T) {
	t.Parallel()
	l := newLiveBox(t, 10*time.Millisecond)
	s := l.open(t)
	old := s.first(t)
	s.cancel()
	l.event(t, "Dentist", l.at(14, 9, 0), l.at(14, 10, 0), false, "Mama") // while the display is cut off
	again := l.open(t)
	if v := again.first(t); v == old {
		t.Fatal("reconnected stream announced the stale version")
	}
}

// A display that is not paired gets neither the stream nor the view.
func TestLiveIsOnlyForPairedDisplays(t *testing.T) {
	t.Parallel()
	l := newLiveBox(t, 10*time.Millisecond)
	stranger := newScreen(l.box)
	for _, path := range []string{"/display/events", "/display/view"} {
		req, _ := http.NewRequest(http.MethodGet, l.url+path, nil)
		res, err := stranger.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("GET %s = %d, want 403", path, res.StatusCode)
		}
	}
}

// 5.8 (round 1 bug): the weather chip appears, changes its icon and hides on
// the display without a reload: each is a new view version, announced and fetched.
func TestLiveWeatherChipAppearsChangesIconAndHides(t *testing.T) {
	t.Parallel()
	l := newLiveBox(t, 10*time.Millisecond)
	s := l.open(t)
	seen := s.first(t)
	announced := func(name string) string {
		t.Helper()
		v, ok := s.next(t, 2*time.Second)
		if !ok || v == seen {
			t.Fatalf("%s: no new version announced", name)
		}
		seen = v
		return v
	}
	view := func(name, want string, present bool) {
		t.Helper()
		_, hdr, body := l.get("/display/view")
		if hdr.Get("X-Hejmo-View") != seen {
			t.Errorf("%s: view version %q, announced %q", name, hdr.Get("X-Hejmo-View"), seen)
		}
		if strings.Contains(body, want) != present {
			t.Errorf("%s: want %q present=%v:\n%s", name, want, present, body)
		}
	}

	l.homeWeather(t) // a reading appears
	announced("reading appears")
	view("reading appears", `data-icon="clear"`, true)

	l.meteo.set(`{"current":{"temperature_2m":9.0,"weather_code":61,"is_day":1}}`, 200)
	if err := l.weather.Refresh(context.Background(), 48.15, 11.46); err != nil {
		t.Fatal(err)
	}
	announced("icon changes")
	view("icon changes", `data-icon="rain"`, true)

	l.clock.set(l.clock.now().Add(weather.MaxAge + time.Minute)) // the reading goes stale
	announced("reading goes stale")
	view("reading goes stale", "data-weather", false)
}
