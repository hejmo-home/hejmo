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
	s := weather.New(weather.WithBaseURL(srv.URL), weather.WithClock(func() time.Time { return now }))
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
		s := weather.New(weather.WithBaseURL(srv.URL))
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
	s := weather.New(weather.WithBaseURL(good.URL), weather.WithClock(func() time.Time { return now }))
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
			s := weather.New(weather.WithBaseURL(srv.URL), weather.WithClock(func() time.Time { return t0 }))
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
	s := weather.New(weather.WithBaseURL(srv.URL))
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

// The home name is turned into coordinates by Open-Meteo's geocoding (no key).
func TestGeocodeFindsTheHomeByName(t *testing.T) {
	var q url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		_, _ = w.Write([]byte(`{"results":[{"latitude":48.15,"longitude":11.46,"name":"Pasing"}]}`))
	}))
	defer srv.Close()
	s := weather.New(weather.WithGeocodingURL(srv.URL))
	lat, lon, err := s.Geocode(context.Background(), "Pasing", "DE")
	if err != nil || lat != 48.15 || lon != 11.46 {
		t.Fatalf("geocode = %v %v %v", lat, lon, err)
	}
	if q.Get("name") != "Pasing" || q.Get("countryCode") != "DE" || q.Get("count") != "1" {
		t.Errorf("query = %v", q)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer empty.Close()
	if _, _, err := weather.New(weather.WithGeocodingURL(empty.URL)).Geocode(context.Background(), "Nowhere", ""); err == nil {
		t.Fatal("unknown place found")
	}
	if weather.DefaultGeocodingURL != "https://geocoding-api.open-meteo.com/v1/search" {
		t.Errorf("default geocoding = %q", weather.DefaultGeocodingURL)
	}
}

type fakeHome struct {
	mu            sync.Mutex
	lat, lon      float64
	name, country string
	stored        int
	err           error
}

func (h *fakeHome) Home() (lat, lon float64, name, country string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lat, h.lon, h.name, h.country, h.err
}
func (h *fakeHome) SetCoordinates(lat, lon float64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lat, h.lon, h.stored = lat, lon, h.stored+1
	return nil
}

// A home without coordinates is looked up once and the answer is kept; the
// weather then follows from the coordinates.
func TestKeepLooksUpTheHomeOnceAndRefreshesItsWeather(t *testing.T) {
	var geoHits atomic.Int32
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geoHits.Add(1)
		_, _ = w.Write([]byte(`{"results":[{"latitude":48.15,"longitude":11.46}]}`))
	}))
	defer geo.Close()
	fc, _, _ := openMeteo(t, sunny, 200)
	s := weather.New(weather.WithBaseURL(fc.URL), weather.WithGeocodingURL(geo.URL))
	home := &fakeHome{name: "Pasing", country: "DE"}
	for i := 0; i < 2; i++ {
		if err := s.Keep(context.Background(), home); err != nil {
			t.Fatal(err)
		}
	}
	if geoHits.Load() != 1 || home.stored != 1 {
		t.Errorf("geocoded %d times, stored %d", geoHits.Load(), home.stored)
	}
	if r, ok := s.Latest(48.15, 11.46); !ok || r.TempC != 18.4 {
		t.Fatalf("latest = %+v %v", r, ok)
	}
}

func TestKeepReportsFailuresAndDoesNothingWithoutAHome(t *testing.T) {
	s := weather.New(weather.WithBaseURL("http://127.0.0.1:1"))
	if err := s.Keep(context.Background(), &fakeHome{err: weather.ErrNoHome}); err != nil {
		t.Fatalf("no home must not be an error: %v", err)
	}
	if err := s.Keep(context.Background(), &fakeHome{lat: 1, lon: 2}); err == nil {
		t.Fatal("unreachable Open-Meteo not reported")
	}
}

// Only "no home yet" is quiet; a real database error is reported.
func TestKeepReportsDatabaseErrors(t *testing.T) {
	s := weather.New(weather.WithBaseURL("http://127.0.0.1:1"))
	if err := s.Keep(context.Background(), &fakeHome{err: errors.New("disk I/O error")}); err == nil {
		t.Fatal("database error swallowed")
	}
}

// A place that is not found is not searched again every round.
func TestKeepBacksOffAfterNoPlaceFound(t *testing.T) {
	var geoHits atomic.Int32
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geoHits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer geo.Close()
	now := t0
	s := weather.New(weather.WithGeocodingURL(geo.URL), weather.WithClock(func() time.Time { return now }))
	home := &fakeHome{name: "Nowhereville"}
	if err := s.Keep(context.Background(), home); err == nil {
		t.Fatal("first failed search not reported")
	}
	for i := 0; i < 3; i++ {
		if err := s.Keep(context.Background(), home); err != nil {
			t.Fatalf("round during back-off: %v", err)
		}
	}
	if geoHits.Load() != 1 {
		t.Fatalf("searched %d times during back-off", geoHits.Load())
	}
	home.mu.Lock()
	home.name = "Pasing" // the location was corrected
	home.mu.Unlock()
	_ = s.Keep(context.Background(), home)
	if geoHits.Load() != 2 {
		t.Fatalf("corrected name not searched, hits %d", geoHits.Load())
	}
	now = now.Add(weather.NoPlaceBackoff + time.Minute)
	home.mu.Lock()
	home.name = "Nowhereville"
	home.mu.Unlock()
	_ = s.Keep(context.Background(), home)
	if geoHits.Load() != 3 {
		t.Fatalf("not searched again after back-off, hits %d", geoHits.Load())
	}
}

// Run does not wait a whole interval for the first reading when the home is
// created after the box started.
func TestRunRetriesSoonWhileThereIsNoReading(t *testing.T) {
	fc, _, _ := openMeteo(t, sunny, 200)
	s := weather.New(weather.WithBaseURL(fc.URL), weather.WithRetry(5*time.Millisecond))
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
