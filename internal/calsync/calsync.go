// Package calsync keeps synced calendars up to date: it fetches feeds on a
// schedule or on request, stores what changed, and records failures in the
// sync log. A failed sync never touches the last good data (ADR 0012).
package calsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/icsfeed"
	"github.com/hejmo-home/hejmo/internal/store"
)

// StaleAfter is how long a calendar may go without a successful sync before
// parents are told (and displays show an info symbol).
const StaleAfter = 24 * time.Hour

// State is the sync state of a calendar as the API reports it.
type State string

// States.
const (
	OK    State = "ok"
	Stale State = "stale"
)

// StateAt decides whether a calendar is stale: 24 hours without a success,
// counted from the last success or, if there never was one, from the start of
// syncing. One success clears it.
func StateAt(now, startedAt, lastSuccess time.Time) State {
	ref := lastSuccess
	if ref.IsZero() {
		ref = startedAt
	}
	if !ref.IsZero() && now.Sub(ref) >= StaleAfter {
		return Stale
	}
	return OK
}

// Backoff is the wait before retrying after the n-th failure in a row: it
// starts at five minutes (never more than the interval), doubles each time and
// stops growing at six hours or the interval, whichever is longer.
func Backoff(interval time.Duration, failures int) time.Duration {
	base := min(5*time.Minute, interval)
	limit := max(6*time.Hour, interval)
	d := base
	for i := 1; i < failures && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

// Fetcher gets the bytes of a feed.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

const maxFeedBytes = 16 << 20

// HTTPFetcher fetches feeds over HTTP(S).
type HTTPFetcher struct {
	Client *http.Client // nil: a client with a 30 second timeout
}

// Fetch implements Fetcher.
func (f HTTPFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Hejmo calendar sync")
	req.Header.Set("Accept", "text/calendar, */*;q=0.1")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the calendar server answered %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFeedBytes {
		return nil, errors.New("the calendar feed is larger than 16 MiB")
	}
	return body, nil
}

// Service syncs calendars.
type Service struct {
	db     *store.DB
	fetch  Fetcher
	now    func() time.Time
	logger *slog.Logger
	mu     sync.Mutex // one sync at a time
}

// New returns a Service. A nil fetcher uses HTTP, a nil clock time.Now.
func New(db *store.DB, fetch Fetcher, now func() time.Time) *Service {
	if fetch == nil {
		fetch = HTTPFetcher{}
	}
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, fetch: fetch, now: now, logger: slog.Default()}
}

// SetLogger sets where unexpected errors go.
func (s *Service) SetLogger(l *slog.Logger) { s.logger = l }

// Sync syncs one calendar now. A failure is recorded in the sync log and
// returned; the calendar's events stay as they were.
func (s *Service) Sync(ctx context.Context, householdID, calendarID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cal, err := s.db.SyncedCalendar(householdID, calendarID)
	if err != nil {
		return err
	}
	h, err := s.db.Household(householdID)
	if err != nil {
		return err
	}
	at := s.now()
	interval := time.Duration(cal.Sync.IntervalMinutes) * time.Minute

	fail := func(cause error) error {
		next := at.Add(Backoff(interval, cal.Sync.Failures+1))
		if err := s.db.FailSync(householdID, calendarID, cause.Error(), at, next); err != nil {
			return errors.Join(cause, err)
		}
		return cause
	}
	feedURL, err := icsfeed.NormalizeURL(cal.URL)
	if err != nil {
		return fail(err)
	}
	body, err := s.fetch.Fetch(ctx, feedURL)
	if err != nil {
		return fail(err)
	}
	zone, err := time.LoadLocation(h.Timezone)
	if err != nil {
		zone = time.UTC
	}
	res, err := icsfeed.Parse(body, icsfeed.Options{Zone: zone, Now: at})
	if err != nil {
		return fail(err)
	}
	items := make([]store.SyncItem, len(res.Items))
	for i, it := range res.Items {
		items[i] = store.SyncItem{Key: it.Key, SeriesKey: it.SeriesKey, Event: it.Event}
	}
	if err := s.db.CompleteSync(householdID, calendarID, items, res.Notes, at, at.Add(interval)); err != nil {
		return fail(fmt.Errorf("could not store the events: %w", err))
	}
	return nil
}

// RunDue syncs every calendar whose time has come.
func (s *Service) RunDue(ctx context.Context) {
	due, err := s.db.DueCalendars(s.now())
	if err != nil {
		s.logger.Error("calendar sync: list due calendars", "err", err)
		return
	}
	for _, c := range due {
		if ctx.Err() != nil {
			return
		}
		if err := s.Sync(ctx, c.HouseholdID, c.ID); err != nil {
			s.logger.Warn("calendar sync failed", "calendar", c.ID, "err", err)
		}
	}
}

// Run is the scheduler: it checks for due calendars every tick until ctx ends.
func (s *Service) Run(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		s.RunDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
