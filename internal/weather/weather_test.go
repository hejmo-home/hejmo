package weather_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/weather"
)

// openMeteo fakes the Open-Meteo forecast endpoint.
func openMeteo(t *testing.T, body string, status int) (*httptest.Server, *atomic.Int32, *url.Values) {
	t.Helper()
	var hits atomic.Int32
	var last url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		last = r.URL.Query()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &last
}

const sunny = `{"current":{"temperature_2m":18.4,"weather_code":0,"is_day":1}}`

var t0 = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func TestRefreshReadsTemperatureAndIconFromOpenMeteo(t *testing.T) {
	srv, _, q := openMeteo(t, sunny, 200)
	now := t0
	s := weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL(srv.URL), weather.WithClock(func() time.Time { return now }))
	if _, ok := s.Latest(48.15, 11.46); ok {
		t.Fatal("reading before any refresh")
	}
	if err := s.Refresh(context.Background(), 48.15, 11.46); err != nil {
		t.Fatal(err)
	}
	r, ok := s.Latest(48.15, 11.46)
	if !ok || r.TempC != 18.4 || r.Icon != weather.Clear {
		t.Fatalf("latest = %+v, %v", r, ok)
	}
	if (*q).Get("latitude") != "48.15" || (*q).Get("longitude") != "11.46" || (*q).Get("api_key") != "" || (*q).Get("apikey") != "" {
		t.Errorf("query = %v", *q)
	}
}

func TestIconsFollowTheWMOCodeAndDaylight(t *testing.T) {
	for _, c := range []struct {
		code, day int
		want      weather.Icon
	}{
		{0, 1, weather.Clear}, {0, 0, weather.ClearNight}, {2, 1, weather.PartlyCloudy}, {3, 1, weather.Cloudy},
		{45, 1, weather.Fog}, {53, 1, weather.Rain}, {63, 1, weather.Rain}, {81, 1, weather.Rain},
		{73, 1, weather.Snow}, {86, 1, weather.Snow}, {95, 1, weather.Thunderstorm}, {99, 0, weather.Thunderstorm},
	} {
		body := `{"current":{"temperature_2m":1,"weather_code":` + itoa(c.code) + `,"is_day":` + itoa(c.day) + `}}`
		srv, _, _ := openMeteo(t, body, 200)
		s := weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL(srv.URL))
		if err := s.Refresh(context.Background(), 1, 2); err != nil {
			t.Fatal(err)
		}
		if r, _ := s.Latest(1, 2); r.Icon != c.want {
			t.Errorf("code %d day %d = %q, want %q", c.code, c.day, r.Icon, c.want)
		}
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// Open-Meteo unreachable: the last value stays for a while, then the chip hides.
func TestLastValueStaysWhileOpenMeteoIsUnreachableThenHides(t *testing.T) {
	good, _, _ := openMeteo(t, sunny, 200)
	now := t0
	s := weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL(good.URL), weather.WithClock(func() time.Time { return now }))
	if err := s.Refresh(context.Background(), 1, 2); err != nil {
		t.Fatal(err)
	}
	good.Close() // unreachable now
	if err := s.Refresh(context.Background(), 1, 2); err == nil {
		t.Fatal("refresh against a closed server succeeded")
	}
	now = t0.Add(weather.MaxAge - time.Minute)
	if r, ok := s.Latest(1, 2); !ok || r.TempC != 18.4 {
		t.Fatalf("last value lost: %+v %v", r, ok)
	}
	now = t0.Add(weather.MaxAge + time.Minute)
	if _, ok := s.Latest(1, 2); ok {
		t.Fatal("stale value still shown")
	}
}

func TestErrorAnswersAndGarbageKeepTheLastValue(t *testing.T) {
	for name, tc := range map[string]struct {
		body   string
		status int
	}{"500": {`oops`, 500}, "garbage": {`not json`, 200}, "no current": {`{}`, 200}} {
		t.Run(name, func(t *testing.T) {
			var cur atomic.Value
			cur.Store(sunny)
			status := 200
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(cur.Load().(string)))
			}))
			defer srv.Close()
			s := weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL(srv.URL), weather.WithClock(func() time.Time { return t0 }))
			if err := s.Refresh(context.Background(), 1, 2); err != nil {
				t.Fatal(err)
			}
			cur.Store(tc.body)
			status = tc.status
			if err := s.Refresh(context.Background(), 1, 2); err == nil {
				t.Error("bad answer not reported")
			}
			if r, ok := s.Latest(1, 2); !ok || r.TempC != 18.4 {
				t.Errorf("last value lost: %+v %v", r, ok)
			}
		})
	}
}

// The reading belongs to the location it was fetched for.
func TestReadingIsOnlyForItsLocation(t *testing.T) {
	srv, _, _ := openMeteo(t, sunny, 200)
	s := weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL(srv.URL))
	_ = s.Refresh(context.Background(), 1, 2)
	if _, ok := s.Latest(3, 4); ok {
		t.Fatal("reading for another location")
	}
}

// The box only talks to Open-Meteo for this feature: the default endpoint is
// Open-Meteo's own, over HTTPS, without a key.
func TestDefaultEndpointIsOpenMeteoOverHTTPS(t *testing.T) {
	if got := weather.DefaultBaseURL; got != "https://api.open-meteo.com/v1/forecast" {
		t.Fatalf("default = %q", got)
	}
}

type fakeHome struct {
	mu       sync.Mutex
	lat, lon float64
	err      error
}

func (h *fakeHome) Home() (lat, lon float64, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lat, h.lon, h.err
}

// The weather follows the stored coordinates of the home, which come from a
// place picked in the list on the box (ADR 0023).
func TestKeepRefreshesTheWeatherAtTheStoredCoordinates(t *testing.T) {
	fc, hits, q := openMeteo(t, sunny, 200)
	s := weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL(fc.URL))
	if err := s.Keep(context.Background(), &fakeHome{lat: 48.15, lon: 11.46}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 || q.Get("latitude") != "48.15" || q.Get("longitude") != "11.46" {
		t.Errorf("hits %d, query %v", hits.Load(), *q)
	}
	if r, ok := s.Latest(48.15, 11.46); !ok || r.TempC != 18.4 {
		t.Fatalf("latest = %+v %v", r, ok)
	}
}

// A home without coordinates (a box set up before the picker, until a parent
// picks a place) is not looked up by name any more: nothing goes out.
func TestKeepDoesNothingWhileTheHomeHasNoLocation(t *testing.T) {
	fc, hits, _ := openMeteo(t, sunny, 200)
	s := weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL(fc.URL))
	for i := 0; i < 2; i++ {
		if err := s.Keep(context.Background(), &fakeHome{}); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("%d requests for a home without location", hits.Load())
	}
}

func TestKeepReportsFailuresAndDoesNothingWithoutAHome(t *testing.T) {
	s := weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL("http://127.0.0.1:1"))
	if err := s.Keep(context.Background(), &fakeHome{err: weather.ErrNoHome}); err != nil {
		t.Fatalf("no home must not be an error: %v", err)
	}
	if err := s.Keep(context.Background(), &fakeHome{lat: 1, lon: 2}); err == nil {
		t.Fatal("unreachable Open-Meteo not reported")
	}
}

// Only "no home yet" is quiet; a real database error is reported.
func TestKeepReportsDatabaseErrors(t *testing.T) {
	s := weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL("http://127.0.0.1:1"))
	if err := s.Keep(context.Background(), &fakeHome{err: errors.New("disk I/O error")}); err == nil {
		t.Fatal("database error swallowed")
	}
}

// Run does not wait a whole interval for the first reading when the home is
// created after the box started.
func TestRunRetriesSoonWhileThereIsNoReading(t *testing.T) {
	fc, _, _ := openMeteo(t, sunny, 200)
	s := weather.New(weather.WithGate(outbound.AllowAll()), weather.WithBaseURL(fc.URL), weather.WithRetry(5*time.Millisecond))
	home := &fakeHome{err: weather.ErrNoHome}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx, time.Hour, home, nil); close(done) }()
	time.Sleep(30 * time.Millisecond)
	home.mu.Lock()
	home.err, home.lat, home.lon = nil, 48.15, 11.46
	home.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := s.Latest(48.15, 11.46); ok {
			cancel()
			<-done
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("no reading although the home appeared")
}
