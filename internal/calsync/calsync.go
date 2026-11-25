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
	"net/url"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/graph"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/icsfeed"
	"github.com/hejmo-home/hejmo/internal/outbound"
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
	var refused *outbound.RefusedError
	if errors.As(err, &refused) {
		if refused.Verdict == outbound.VerdictOffline {
			return i18n.Msg("sync.fail.offline")
		}
		return i18n.Msg("sync.fail.switched_off")
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
	Client *http.Client // nil: a client that refuses every connection (the service gives it one from the gate)
}

// Fetch implements Fetcher.
func (f HTTPFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	client := f.Client
	if client == nil {
		client = outbound.New(outbound.Closed{}, nil).Client(outbound.ICS, 30*time.Second)
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
	// google talks to Google's token service, googleDial to its CalDAV API; tokens keeps the access tokens.
	google     *googleauth.Client
	googleDial GoogleDialer
	tokens     googleTokens
	gate       *outbound.Gate
	// ownFetch and ownDial say that a test or caller set its own; the gate then
	// does not replace them.
	ownFetch, ownDial, ownGoogle bool
	locks                        sync.Map // calendar id -> *sync.Mutex: one sync per calendar at a time
	pushes                       sync.WaitGroup
	bundle                       *i18n.Bundle
	lang                         string // language of the readable audience line
	graph                        *graph.Client
}

func (s *Service) lock(calendarID string) *sync.Mutex {
	m, _ := s.locks.LoadOrStore(calendarID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// New returns a Service. A nil fetcher uses HTTP, a nil clock time.Now.
func New(db *store.DB, fetch Fetcher, now func() time.Time) *Service {
	own := fetch != nil
	if fetch == nil {
		fetch = HTTPFetcher{}
	}
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, fetch: fetch, now: now, logger: slog.Default(), dial: dialer(nil), ownFetch: own, bundle: i18n.MustLoad(), lang: i18n.Default}
}

// SetDialer sets how the service connects to CalDAV servers (tests count or
// fake the traffic).
func (s *Service) SetDialer(d Dialer) { s.dial, s.ownDial = d, true }

// SetGate sends the service's connections through the gate of the outside
// connections (ticket 201): CalDAV servers are the calendar services, feeds
// the ICS subscriptions. A fetcher or dialer set by the caller stays. Without a
// gate every connection is refused.
func (s *Service) SetGate(g *outbound.Gate) {
	s.gate = g
	if !s.ownFetch {
		s.fetch = HTTPFetcher{Client: g.Client(outbound.ICS, 30*time.Second)}
	}
	if !s.ownDial {
		s.dial = dialer(g)
	}
	if !s.ownGoogle {
		s.google, s.googleDial = googleauth.New(g.Client(outbound.Calendars, 30*time.Second)), googleDialer(g)
	}
}

// ServiceOf is the connection a calendar kind belongs to: the calendars one
// for CalDAV, Google and Outlook, the subscription one for the rest. The sync and the
// pages use this one mapping.
func ServiceOf(kind string) outbound.Service {
	if kind == "caldav" || kind == "google" || kind == "outlook" { // Microsoft Graph is a calendar service too (ADR 0024)
		return outbound.Calendars
	}
	return outbound.ICS
}

// refuse is the check before a calendar is synced or pushed: if its connection
// is off, the attempt is recorded and the calendar stays as it is, paused and
// not failed.
func (s *Service) refuse(kind, rawURL string) error {
	host := rawURL
	if kind == "outlook" {
		host = s.Graph().Host() // rawURL is a Graph calendar ID, not an address
	} else if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return s.gate.Check(ServiceOf(kind), host)
}

// SetLogger sets where unexpected errors go.
func (s *Service) SetLogger(l *slog.Logger) { s.logger = l }

// runInfo is what one sync run reports in the log. It holds numbers and the
// key of a failure, never an event's title, description or address.
type runInfo struct {
	calendar        string
	started         bool // the calendar was found, so there is a run to report
	listed, fetched int  // objects the server listed, objects downloaded
	skipped         int  // events or objects the box could not read
	done            store.SyncRun
	result, failure string
}

// skipped counts the notes that say something was left out, not the ones that
// only remark on how it was read.
func skipped(notes []i18n.Message) int {
	n := 0
	for _, m := range notes {
		if m.Key != "sync.note.unknown_zone" {
			n++
		}
	}
	return n
}

// Sync syncs one calendar now. A failure is recorded in the sync log and
// returned; the calendar's events stay as they were. Every run writes one
// INFO line (see runInfo).
func (s *Service) Sync(ctx context.Context, householdID, calendarID string) error {
	mu := s.lock(calendarID)
	mu.Lock()
	defer mu.Unlock()
	run := runInfo{calendar: calendarID, result: "ok"}
	began := time.Now()
	err := s.sync(ctx, householdID, calendarID, &run)
	if run.started {
		attrs := []any{"calendar", run.calendar, "listed", run.listed, "fetched", run.fetched, "new", run.done.New, "changed", run.done.Changed,
			"removed", run.done.Removed, "skipped", run.skipped, "duration", time.Since(began).Round(time.Millisecond).String(), "result", run.result}
		if run.failure != "" {
			attrs = append(attrs, "failure", run.failure)
		}
		s.logger.Info("calendar sync", attrs...)
	}
	return err
}

func (s *Service) sync(ctx context.Context, householdID, calendarID string, run *runInfo) error {
	cal, err := s.db.SyncedCalendar(householdID, calendarID)
	if err != nil {
		return err
	}
	run.started = true
	h, err := s.db.Household(householdID)
	if err != nil {
		return err
	}
	at := s.now()
	interval := time.Duration(cal.Sync.IntervalMinutes) * time.Minute

	if err := s.refuse(cal.Kind, cal.URL); err != nil {
		run.result, run.failure = "paused", "sync.paused"
		return err
	}
	fail := func(msg i18n.Message, cause error) error {
		msg, next := failed(msg, cause, at, interval, cal.Sync.Failures)
		run.result, run.failure = "failed", msg.Key
		if err := s.db.FailSync(householdID, calendarID, msg, at, next); err != nil {
			return errors.Join(cause, err)
		}
		return cause
	}
	if cal.Kind == "caldav" || cal.Kind == "google" {
		return s.syncCalDAV(ctx, cal, h, at, run, fail)
	}
	if cal.Kind == "outlook" {
		return s.syncOutlook(ctx, cal, h, at, run, fail)
	}
	feedURL, err := icsfeed.NormalizeURL(cal.URL)
	if err != nil {
		return fail(i18n.Msg("sync.fail.address", "detail", err.Error()), err)
	}
	body, err := s.fetch.Fetch(ctx, feedURL)
	if err != nil {
		return fail(MessageOf(err), err)
	}
	run.fetched = 1
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
	run.listed, run.skipped = len(items), skipped(res.Notes)
	done, err := s.db.CompleteSync(householdID, calendarID, items, res.Notes, at, at.Add(interval))
	if err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), fmt.Errorf("could not store the events: %w", err))
	}
	run.done = done
	return nil
}

// RunDue syncs every calendar whose time has come.
func (s *Service) RunDue(ctx context.Context) {
	if err := s.db.PruneGoogleLogins(s.now()); err != nil {
		s.logger.Error("calendar sync: prune Google sign-ins", "err", err)
	}
	due, err := s.db.DueCalendars(s.now())
	if err != nil {
		s.logger.Error("calendar sync: list due calendars", "err", err)
		return
	}
	for _, c := range due {
		if ctx.Err() != nil {
			return
		}
		if !s.gate.Allowed(ServiceOf(c.Kind)) {
			continue // paused (offline mode or switched off): not tried, not failed
		}
		// Sync already wrote the run's one INFO line and stored the failure; the
		// raw error can hold the feed address (often with a secret token) or an
		// event title, so it is not logged.
		_ = s.Sync(ctx, c.HouseholdID, c.ID)
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
