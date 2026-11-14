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

	"github.com/hejmo-home/hejmo/internal/i18n"
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

// Failure is an error with a translatable message for the sync log. Errors
// that are not a Failure are shown as their raw text inside a generic message.
type Failure struct{ Msg i18n.Message }

func (f *Failure) Error() string { return fmt.Sprintf("%s %v", f.Msg.Key, f.Msg.Params) }

func failure(key string, kv ...string) error { return &Failure{i18n.Msg(key, kv...)} }

// MessageOf returns the translatable message of err: its own if err is a
// Failure, else key "sync.fail.fetch" with the error text as detail.
func MessageOf(err error) i18n.Message {
	var f *Failure
	if errors.As(err, &f) {
		return f.Msg
	}
	return i18n.Msg("sync.fail.fetch", "detail", err.Error())
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
		return nil, failure("sync.fail.status", "status", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFeedBytes {
		return nil, failure("sync.fail.too_large")
	}
	return body, nil
}

// Service syncs calendars.
type Service struct {
	db     *store.DB
	fetch  Fetcher
	now    func() time.Time
	logger *slog.Logger
	dial   Dialer
	locks  sync.Map // calendar id -> *sync.Mutex: one sync per calendar at a time
	pushes sync.WaitGroup
	bundle *i18n.Bundle
	lang   string // language of the readable audience line
}

func (s *Service) lock(calendarID string) *sync.Mutex {
	m, _ := s.locks.LoadOrStore(calendarID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// New returns a Service. A nil fetcher uses HTTP, a nil clock time.Now.
func New(db *store.DB, fetch Fetcher, now func() time.Time) *Service {
	if fetch == nil {
		fetch = HTTPFetcher{}
	}
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, fetch: fetch, now: now, logger: slog.Default(), dial: dialCalDAV, bundle: i18n.MustLoad(), lang: i18n.Default}
}

// SetDialer sets how the service connects to CalDAV servers (tests count or
// fake the traffic).
func (s *Service) SetDialer(d Dialer) { s.dial = d }

// SetLogger sets where unexpected errors go.
func (s *Service) SetLogger(l *slog.Logger) { s.logger = l }

// Sync syncs one calendar now. A failure is recorded in the sync log and
// returned; the calendar's events stay as they were.
func (s *Service) Sync(ctx context.Context, householdID, calendarID string) error {
	mu := s.lock(calendarID)
	mu.Lock()
	defer mu.Unlock()
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

	fail := func(msg i18n.Message, cause error) error {
		next := at.Add(Backoff(interval, cal.Sync.Failures+1))
		if err := s.db.FailSync(householdID, calendarID, msg, at, next); err != nil {
			return errors.Join(cause, err)
		}
		return cause
	}
	if cal.Kind == "caldav" {
		return s.syncCalDAV(ctx, cal, h, at, fail)
	}
	feedURL, err := icsfeed.NormalizeURL(cal.URL)
	if err != nil {
		return fail(i18n.Msg("sync.fail.address", "detail", err.Error()), err)
	}
	body, err := s.fetch.Fetch(ctx, feedURL)
	if err != nil {
		return fail(MessageOf(err), err)
	}
	zone, err := time.LoadLocation(h.Timezone)
	if err != nil {
		zone = time.UTC
	}
	res, err := icsfeed.Parse(body, icsfeed.Options{Zone: zone, Now: at})
	if err != nil {
		return fail(i18n.Msg("sync.fail.not_ics", "detail", err.Error()), err)
	}
	items := make([]store.SyncItem, len(res.Items))
	for i, it := range res.Items {
		items[i] = store.SyncItem{Key: it.Key, SeriesKey: it.SeriesKey, Event: it.Event}
	}
	if err := s.db.CompleteSync(householdID, calendarID, items, res.Notes, at, at.Add(interval)); err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), fmt.Errorf("could not store the events: %w", err))
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
