package calsync

import (
	"context"
	"fmt"
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
	Fetch(ctx context.Context, calendarURL string, objectURLs []string) ([]caldavclient.Object, error)
	// Put writes an object (a new one with an empty etag, else replacing that
	// version) and returns its new ETag; Delete removes one. A refusal because
	// the server's version is another is caldavclient.IsConflict.
	Put(ctx context.Context, objectURL, raw, etag string) (string, error)
	Delete(ctx context.Context, objectURL, etag string) error
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

// syncCalDAV brings one CalDAV calendar up to date by ETag: it lists the
// server's objects, downloads only those that are new or changed, and drops
// those that are gone. The caller holds the calendar's lock.
func (s *Service) syncCalDAV(ctx context.Context, cal store.SyncedCalendar, h store.Household, at time.Time, fail func(i18n.Message, error) error) error {
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

	listed, err := remote.List(ctx, cal.URL)
	if err != nil {
		return failRemote(err)
	}
	known, err := s.db.RemoteObjects(cal.ID, cal.ReadOnly)
	if err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), err)
	}
	knownByKey := map[string]string{} // normalised address -> address as stored
	for u := range known {
		knownByKey[caldavclient.AddressKey(u)] = u
	}
	var toFetch []string
	present := map[string]bool{}
	etags := map[string]string{}
	byPath := map[string]string{} // normalised address -> address as stored, else as listed
	for _, o := range listed {
		key := caldavclient.AddressKey(o.URL)
		if waiting[key] {
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
	var deleted []string
	for u := range known {
		if !present[u] {
			deleted = append(deleted, u)
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
	fetched, err := remote.Fetch(ctx, cal.URL, toFetch)
	if err != nil {
		return failRemote(err)
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
	if err := s.db.CompleteObjectSync(h.ID, cal.ID, changes, deleted, notes, at, at.Add(interval)); err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), fmt.Errorf("could not store the events: %w", err))
	}
	if pushErr != nil {
		// Reading went well; the failed write is logged and tried again.
		return fail(pushMessage(pushErr), pushErr)
	}
	return nil
}
