package calsync

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/icsfeed"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Remote is a CalDAV server as the sync needs it.
type Remote interface {
	Calendars(ctx context.Context) ([]caldavclient.Calendar, error)
	Calendar(ctx context.Context, calendarURL string) (caldavclient.Calendar, error)
	List(ctx context.Context, calendarURL string) ([]caldavclient.Object, error)
	State(ctx context.Context, calendarURL string) (caldavclient.State, error)
	Changes(ctx context.Context, calendarURL, token string) (caldavclient.Changes, error)
	Fetch(ctx context.Context, calendarURL string, objectURLs []string) ([]caldavclient.Object, error)
	CountEvents(ctx context.Context, calendarURL string, since time.Time) (int, error)
}

// Dialer connects to a CalDAV server with one set of credentials.
type Dialer func(serverURL, user, password string) (Remote, error)

func dialCalDAV(serverURL, user, password string) (Remote, error) {
	c, err := caldavclient.NewClient(serverURL, user, password, nil)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Discover signs in to a CalDAV server and lists its event calendars; with
// single, serverURL is itself the address of one calendar. It stores nothing.
func (s *Service) Discover(ctx context.Context, serverURL, user, password string, single bool) ([]caldavclient.Calendar, error) {
	remote, err := s.dial(serverURL, user, password)
	if err != nil {
		return nil, err
	}
	if single {
		c, err := remote.Calendar(ctx, serverURL)
		if err != nil {
			return nil, err
		}
		return []caldavclient.Calendar{c}, nil
	}
	return remote.Calendars(ctx)
}

// CountEvents asks a CalDAV server how many events a calendar has in the
// import window (the past 12 months and everything later; a series counts
// once) without downloading them. It stores nothing.
func (s *Service) CountEvents(ctx context.Context, serverURL, user, password, calendarURL string) (int, error) {
	remote, err := s.dial(serverURL, user, password)
	if err != nil {
		return 0, err
	}
	return remote.CountEvents(ctx, calendarURL, s.now().AddDate(-1, 0, 0))
}

// syncCalDAV brings one CalDAV calendar up to date by ETag: it lists the
// server's objects, downloads only those that are new or changed, and drops
// those that are gone. The caller holds the calendar's lock.
func (s *Service) syncCalDAV(ctx context.Context, cal store.SyncedCalendar, h store.Household, at time.Time, run *runInfo, fail func(i18n.Message, error) error) error {
	conn, err := s.db.Connection(h.ID, cal.ConnectionID)
	if err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), err)
	}
	user, password, err := s.db.ConnectionCredentials(h.ID, cal.ConnectionID)
	if err != nil {
		return fail(i18n.Msg("sync.fail.credentials", "detail", err.Error()), err)
	}
	remote, err := s.dial(conn.URL, user, password)
	if err != nil {
		return fail(i18n.Msg("sync.fail.address", "detail", err.Error()), err)
	}
	failRemote := func(err error) error {
		if caldavclient.IsUnauthorized(err) {
			return fail(i18n.Msg("sync.fail.login"), err)
		}
		var foreign *caldavclient.ForeignHostError
		if errors.As(err, &foreign) && foreign.Downgrade {
			return fail(i18n.Msg("sync.fail.downgrade", "host", foreign.Host), err)
		}
		if errors.As(err, &foreign) {
			return fail(i18n.Msg("sync.fail.foreign_host", "host", foreign.Host, "domain", foreign.Domain), err)
		}
		return fail(MessageOf(err), err)
	}

	known, err := s.db.RemoteObjects(cal.ID)
	if err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), err)
	}
	prev, err := s.db.RemoteState(cal.ID)
	if err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), err)
	}
	// The server's state is read before anything else, so a change made while
	// this run goes on shows up in the next one and is never skipped.
	state, err := remote.State(ctx, cal.URL)
	if err != nil {
		if caldavclient.IsUnauthorized(err) || isForeign(err) {
			return failRemote(err)
		}
		state = caldavclient.State{} // not asked for or not answered: list the ETags
	}

	var toFetch, deleted []string
	etags := map[string]string{}
	byPath := map[string]string{} // normalised address -> listed address
	next := caldavclient.State{CTag: state.CTag, SyncToken: state.SyncToken}
	listing := func() error {
		listed, err := remote.List(ctx, cal.URL)
		if err != nil {
			return err
		}
		run.listed = len(listed)
		present := map[string]bool{}
		for _, o := range listed {
			present[o.URL] = true
			etags[o.URL] = o.ETag
			byPath[caldavclient.AddressKey(o.URL)] = o.URL
			if etag, ok := known[o.URL]; !ok || etag != o.ETag || o.ETag == "" {
				toFetch = append(toFetch, o.URL)
			}
		}
		for u := range known {
			if !present[u] {
				deleted = append(deleted, u)
			}
		}
		return nil
	}
	delta := func(token string) error {
		ch, err := remote.Changes(ctx, cal.URL, token)
		if err != nil {
			return err
		}
		keep := map[string]string{} // normalised address -> address the box holds
		for u := range known {
			keep[caldavclient.AddressKey(u)] = u
		}
		present := maps.Clone(known)
		for _, u := range ch.Deleted {
			if held, ok := keep[caldavclient.AddressKey(u)]; ok {
				deleted = append(deleted, held)
				delete(present, held)
			}
		}
		for _, o := range ch.Changed {
			addr := o.URL
			if held, ok := keep[caldavclient.AddressKey(o.URL)]; ok {
				addr = held
			}
			present[addr] = o.ETag
			etags[addr] = o.ETag
			byPath[caldavclient.AddressKey(addr)] = addr
			if etag, ok := known[addr]; !ok || etag != o.ETag || o.ETag == "" {
				toFetch = append(toFetch, addr)
			}
		}
		run.listed = len(present)
		next.SyncToken = ch.Token
		return nil
	}
	switch {
	case prev.CTag != "" && state.CTag == prev.CTag:
		run.listed = len(known) // nothing changed since the last run
	case prev.Token != "" && state.SyncToken != "":
		err := delta(prev.Token)
		if errors.Is(err, caldavclient.ErrSyncUnavailable) {
			// No sync-collection, or the token is not known any more.
			toFetch, deleted = nil, nil
			clear(etags)
			clear(byPath)
			err = listing()
		}
		if err != nil {
			return failRemote(err)
		}
	default:
		if err := listing(); err != nil {
			return failRemote(err)
		}
	}

	zone, err := time.LoadLocation(h.Timezone)
	if err != nil {
		zone = time.UTC
	}
	var fetched []caldavclient.Object
	if len(toFetch) > 0 {
		if fetched, err = remote.Fetch(ctx, cal.URL, toFetch); err != nil {
			return failRemote(err)
		}
	}
	run.fetched = len(fetched)
	var changes []store.ObjectChange
	var notes []i18n.Message
	for _, o := range fetched {
		addr, ok := byPath[caldavclient.AddressKey(o.URL)]
		if !ok {
			continue // not asked for
		}
		change := store.ObjectChange{URL: addr, ETag: etags[addr], Raw: string(o.Data)}
		var res icsfeed.Result
		err := o.Err
		if err == nil {
			res, err = icsfeed.Parse(o.Data, icsfeed.Options{Zone: zone, Now: at})
		}
		if err != nil {
			// Keep the object's last good events (ADR 0012) and remember its
			// ETag, so it is not downloaded and noted again until the server
			// changes it.
			notes = append(notes, i18n.Msg("sync.note.bad_object", "object", addr, "detail", err.Error()))
			change.KeepEvents = true
			changes = append(changes, change)
			continue
		}
		for _, it := range res.Items {
			change.Items = append(change.Items, store.SyncItem{Key: it.Key, SeriesKey: it.SeriesKey, Event: it.Event})
		}
		notes = append(notes, res.Notes...)
		changes = append(changes, change)
	}
	interval := time.Duration(cal.Sync.IntervalMinutes) * time.Minute
	run.skipped = skipped(notes)
	done, err := s.db.CompleteObjectSyncWithState(h.ID, cal.ID, store.RemoteState{CTag: next.CTag, Token: next.SyncToken}, changes, deleted, notes, at, at.Add(interval))
	if err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), fmt.Errorf("could not store the events: %w", err))
	}
	run.done = done
	return nil
}

// isForeign says whether err is the refusal to contact a host outside the
// server's domain.
func isForeign(err error) bool {
	var foreign *caldavclient.ForeignHostError
	return errors.As(err, &foreign)
}
