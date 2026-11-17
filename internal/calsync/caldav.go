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
	"github.com/hejmo-home/hejmo/internal/outbound"
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
	// Put writes an object (a new one with an empty etag, else replacing that
	// version) and returns its new ETag; Delete removes one. A refusal because
	// the server's version is another is caldavclient.IsConflict.
	Put(ctx context.Context, objectURL, raw, etag string) (string, error)
	Delete(ctx context.Context, objectURL, etag string) error
	CountEvents(ctx context.Context, calendarURL string, since time.Time) (int, error)
}

// Dialer connects to a CalDAV server with one set of credentials.
type Dialer func(serverURL, user, password string) (Remote, error)

// dialer connects to CalDAV servers through the calendar services connection of g.
func dialer(g *outbound.Gate) Dialer {
	return func(serverURL, user, password string) (Remote, error) {
		c, err := caldavclient.NewClient(serverURL, user, password, g.Client(outbound.Calendars, 30*time.Second))
		if err != nil {
			return nil, err
		}
		return c, nil
	}
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
	remote, msg, err := s.remoteFor(ctx, h, conn, at)
	if err != nil {
		return fail(msg, err)
	}
	failRemote := func(err error) error { return fail(remoteMessage(err), err) }

	// Box changes go out first, so the import below never overwrites them.
	var pushErr error
	if !cal.ReadOnly {
		if pushErr = s.pushPending(ctx, cal, h, remote); pushErr != nil && abortsSync(pushErr) {
			return failRemote(pushErr)
		}
	}
	pending, err := s.db.PendingObjects(cal.ID)
	if err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), err)
	}
	// Two-way: objects with queued box changes are left alone by the import.
	// Read-only: nothing will ever push them, so the outside version wins and
	// the queued changes are dropped.
	waiting := map[string]bool{} // skipped by the import
	discard := map[string]bool{} // imported over the queued changes
	for _, p := range pending {
		if cal.ReadOnly {
			discard[caldavclient.AddressKey(p.URL)] = true
		} else {
			waiting[caldavclient.AddressKey(p.URL)] = true
		}
	}

	known, err := s.db.RemoteObjects(cal.ID, cal.ReadOnly)
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

	knownByKey := map[string]string{} // normalised address -> address as stored
	for u := range known {
		knownByKey[caldavclient.AddressKey(u)] = u
	}
	var toFetch, deleted []string
	skippedWaiting := false
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
			key := caldavclient.AddressKey(o.URL)
			if waiting[key] {
				skippedWaiting = true
				continue // the box has changes for it that are not written yet
			}
			addr, isKnown := knownByKey[key]
			if !isKnown {
				addr = o.URL
			}
			present[addr] = true
			etags[addr] = o.ETag
			byPath[key] = addr
			if !isKnown || known[addr] != o.ETag || o.ETag == "" || discard[key] {
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
		keep := knownByKey
		present := maps.Clone(known)
		for _, u := range ch.Deleted {
			if held, ok := keep[caldavclient.AddressKey(u)]; ok {
				deleted = append(deleted, held)
				delete(present, held)
			}
		}
		for _, o := range ch.Changed {
			key := caldavclient.AddressKey(o.URL)
			if waiting[key] {
				skippedWaiting = true
				continue // the box has changes for it that are not written yet
			}
			addr := o.URL
			if held, ok := keep[caldavclient.AddressKey(o.URL)]; ok {
				addr = held
			}
			present[addr] = o.ETag
			etags[addr] = o.ETag
			byPath[caldavclient.AddressKey(addr)] = addr
			if etag, ok := known[addr]; !ok || etag != o.ETag || o.ETag == "" || discard[key] {
				toFetch = append(toFetch, addr)
			}
		}
		run.listed = len(present)
		next.SyncToken = ch.Token
		if ch.Truncated {
			// The server sent only part of the changes: carry on from the new
			// token, but do not claim the calendar is up to date, or the next
			// run would stop at the unchanged CTag.
			next.CTag = ""
		}
		return nil
	}
	switch {
	case prev.CTag != "" && state.CTag == prev.CTag && len(discard) == 0:
		run.listed = len(known) // nothing changed since the last run
	case prev.Token != "" && state.SyncToken != "" && len(discard) == 0:
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
	ids, err := s.audienceIDs(h.ID)
	if err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), err)
	}
	var fetched []caldavclient.Object
	if len(toFetch) > 0 {
		if fetched, err = remote.Fetch(ctx, cal.URL, toFetch); err != nil {
			return failRemote(err)
		}
	}
	run.fetched = len(fetched)
	if len(fetched) < len(toFetch) || skippedWaiting {
		// The server left out objects it had named, or an object was left
		// alone for queued box changes: forget its state, so the next run
		// lists the ETags and looks at them again.
		next = caldavclient.State{}
	}
	var changes []store.ObjectChange
	var notes []i18n.Message
	for _, o := range fetched {
		addr, ok := byPath[caldavclient.AddressKey(o.URL)]
		if !ok {
			continue // not asked for
		}
		change := store.ObjectChange{URL: addr, ETag: etags[addr], Raw: string(o.Data), DropPending: cal.ReadOnly}
		var res icsfeed.Result
		err := o.Err
		if err == nil {
			res, err = icsfeed.Parse(o.Data, s.importOptions(zone, at))
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
			it.Event.People, it.Event.Groups = keepKnown(it.Event.People, ids), keepKnown(it.Event.Groups, ids)
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
	if pushErr != nil {
		// Reading went well; the failed write is logged and tried again.
		return fail(pushMessage(pushErr), pushErr)
	}
	return nil
}

// isForeign says whether err is the refusal to contact a host outside the
// server's domain.
func isForeign(err error) bool {
	var foreign *caldavclient.ForeignHostError
	return errors.As(err, &foreign)
}
