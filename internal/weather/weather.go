// Package weather fetches the current weather for the home location from
// Open-Meteo (no API key) and keeps the last reading. The box fetches; a
// display never calls outside (ADR 0008, 0016). This is the only outgoing
// connection of the weather feature.
package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// DefaultBaseURL is Open-Meteo's forecast endpoint.
const DefaultBaseURL = "https://api.open-meteo.com/v1/forecast"

// MaxAge is how long the last reading is still shown when refreshing fails;
// after that the chip hides.
const MaxAge = 3 * time.Hour

// Icon names the weather symbol of a reading.
type Icon string

// Icons.
const (
	Clear        Icon = "clear"
	ClearNight   Icon = "clear-night"
	PartlyCloudy Icon = "partly-cloudy"
	Cloudy       Icon = "cloudy"
	Fog          Icon = "fog"
	Rain         Icon = "rain"
	Snow         Icon = "snow"
	Thunderstorm Icon = "thunderstorm"
)

// Reading is the current weather at a location.
type Reading struct {
	TempC float64
	Icon  Icon
}

// defaultRetry is how soon Run asks again while the box has no reading and
// nothing failed (for example before the household exists).
const defaultRetry = time.Minute

// ErrNoHome is what a Home returns while there is no household yet.
var ErrNoHome = errors.New("weather: no home yet")

type entry struct {
	lat, lon float64
	reading  Reading
	at       time.Time
}

// Service fetches and caches the weather of one location (the home).
type Service struct {
	base string
	http *http.Client
	now  func() time.Time
	// retry is the wait of Run while there is no reading and no failure.
	retry time.Duration

	mu   sync.Mutex
	last *entry
}

// Option configures a Service.
type Option func(*Service)

// WithBaseURL points the service at another endpoint (tests).
func WithBaseURL(u string) Option { return func(s *Service) { s.base = u } }

// WithClock sets the clock.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithRetry sets how soon Run asks again while there is no reading (tests).
func WithRetry(d time.Duration) Option { return func(s *Service) { s.retry = d } }

// New returns a service for Open-Meteo.
func New(opts ...Option) *Service {
	s := &Service{base: DefaultBaseURL, http: &http.Client{Timeout: 15 * time.Second}, now: time.Now, retry: defaultRetry}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Latest returns the last reading for the location, if there is one that is
// not older than MaxAge.
func (s *Service) Latest(lat, lon float64) (Reading, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.last
	if e == nil || e.lat != lat || e.lon != lon || s.now().Sub(e.at) > MaxAge {
		return Reading{}, false
	}
	return e.reading, true
}

// Refresh asks Open-Meteo for the current weather. On failure the last
// reading is kept.
func (s *Service) Refresh(ctx context.Context, lat, lon float64) error {
	q := url.Values{
		"latitude":  {strconv.FormatFloat(lat, 'f', -1, 64)},
		"longitude": {strconv.FormatFloat(lon, 'f', -1, 64)},
		"current":   {"temperature_2m,weather_code,is_day"},
	}
	var body struct {
		Current *struct {
			Temp *float64 `json:"temperature_2m"`
			Code int      `json:"weather_code"`
			Day  int      `json:"is_day"`
		} `json:"current"`
	}
	if err := s.getJSON(ctx, s.base+"?"+q.Encode(), &body); err != nil {
		return err
	}
	if body.Current == nil || body.Current.Temp == nil {
		return fmt.Errorf("weather: answer without current weather")
	}
	s.mu.Lock()
	s.last = &entry{lat, lon, Reading{*body.Current.Temp, iconFor(body.Current.Code, body.Current.Day == 1)}, s.now()}
	s.mu.Unlock()
	return nil
}

// iconFor maps a WMO weather code to an icon.
func iconFor(code int, day bool) Icon {
	switch {
	case code == 0, code == 1:
		if day {
			return Clear
		}
		return ClearNight
	case code == 2:
		return PartlyCloudy
	case code == 3:
		return Cloudy
	case code == 45, code == 48:
		return Fog
	case code >= 51 && code <= 67, code >= 80 && code <= 82:
		return Rain
	case code >= 71 && code <= 77, code == 85, code == 86:
		return Snow
	case code >= 95:
		return Thunderstorm
	}
	return Cloudy
}

// Home is the household's home location, as the weather needs it.
type Home interface {
	// Home returns the coordinates (both zero: no place picked yet). ErrNoHome
	// means there is no household yet; any other error is a real failure.
	Home() (lat, lon float64, err error)
}

// Keep does one round for the home: it refreshes the weather at the stored
// coordinates. A box without a home yet (ErrNoHome), or whose home has no
// location yet (set up before the place list; a parent picks one in Settings),
// is not an error: nothing is looked up by name.
func (s *Service) Keep(ctx context.Context, h Home) error {
	lat, lon, err := h.Home()
	if errors.Is(err, ErrNoHome) {
		return nil
	}
	if err != nil {
		return err
	}
	if lat == 0 && lon == 0 {
		return nil
	}
	return s.Refresh(ctx, lat, lon)
}

// hasReading reports whether there is any reading yet.
func (s *Service) hasReading() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last != nil
}

// Run calls Keep now and then every interval until ctx ends; failures go to
// onErr. While there is no reading and nothing failed (no household yet, or no
// place picked yet) it asks again sooner, so a display paired right after
// setup does not wait a whole interval for its chip.
func (s *Service) Run(ctx context.Context, interval time.Duration, h Home, onErr func(error)) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		err := s.Keep(ctx, h)
		if err != nil && ctx.Err() == nil && onErr != nil {
			onErr(err)
		}
		var soon <-chan time.Time
		var timer *time.Timer
		if err == nil && !s.hasReading() {
			timer = time.NewTimer(s.retry)
			soon = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-soon:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// getJSON reads a JSON answer from Open-Meteo.
func (s *Service) getJSON(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("weather: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("weather: Open-Meteo answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into); err != nil {
		return fmt.Errorf("weather: unreadable answer: %w", err)
	}
	return nil
}
