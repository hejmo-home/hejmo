package calsync

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldav"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/icsfeed"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Remote is a CalDAV server as the sync needs it.
type Remote interface {
	Calendars(ctx context.Context) ([]caldav.Calendar, error)
	Calendar(ctx context.Context, calendarURL string) (caldav.Calendar, error)
	List(ctx context.Context, calendarURL string) ([]caldav.Object, error)
	Fetch(ctx context.Context, calendarURL string, objectURLs []string) ([]caldav.Object, error)
}

// Dialer connects to a CalDAV server with one set of credentials.
type Dialer func(serverURL, user, password string) (Remote, error)

func dialCalDAV(serverURL, user, password string) (Remote, error) {
	c, err := caldav.NewClient(serverURL, user, password, nil)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Discover signs in to a CalDAV server and lists its event calendars; with
// single, serverURL is itself the address of one calendar. It stores nothing.
func (s *Service) Discover(ctx context.Context, serverURL, user, password string, single bool) ([]caldav.Calendar, error) {
	remote, err := s.dial(serverURL, user, password)
	if err != nil {
		return nil, err
	}
	if single {
		c, err := remote.Calendar(ctx, serverURL)
		if err != nil {
			return nil, err
		}
		return []caldav.Calendar{c}, nil
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
	failRemote := func(err error) error {
		if caldav.IsUnauthorized(err) {
			return fail(i18n.Msg("sync.fail.login"), err)
		}
		return fail(MessageOf(err), err)
	}

	listed, err := remote.List(ctx, cal.URL)
	if err != nil {
		return failRemote(err)
	}
	known, err := s.db.RemoteObjects(cal.ID)
	if err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), err)
	}
	var toFetch []string
	present := map[string]bool{}
	etags := map[string]string{}
	byPath := map[string]string{} // normalised address -> listed address
	for _, o := range listed {
		present[o.URL] = true
		etags[o.URL] = o.ETag
		byPath[normal(o.URL)] = o.URL
		if etag, ok := known[o.URL]; !ok || etag != o.ETag || o.ETag == "" {
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
	fetched, err := remote.Fetch(ctx, cal.URL, toFetch)
	if err != nil {
		return failRemote(err)
	}
	var changes []store.ObjectChange
	var notes []i18n.Message
	for _, o := range fetched {
		addr, ok := byPath[normal(o.URL)]
		if !ok {
			continue // not asked for
		}
		change := store.ObjectChange{URL: addr, ETag: etags[addr], Raw: string(o.Data)}
		res, err := icsfeed.Parse(o.Data, icsfeed.Options{Zone: zone, Now: at})
		if err != nil {
			// Remember the object anyway, so it is not downloaded again until it changes.
			notes = append(notes, i18n.Msg("sync.note.bad_object", "object", addr, "detail", err.Error()))
		} else {
			for _, it := range res.Items {
				change.Items = append(change.Items, store.SyncItem{Key: it.Key, SeriesKey: it.SeriesKey, Event: it.Event})
			}
			notes = append(notes, res.Notes...)
		}
		changes = append(changes, change)
	}
	interval := time.Duration(cal.Sync.IntervalMinutes) * time.Minute
	if err := s.db.CompleteObjectSync(h.ID, cal.ID, changes, deleted, notes, at, at.Add(interval)); err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), fmt.Errorf("could not store the events: %w", err))
	}
	return nil
}

// normal compares addresses by their decoded path, so servers that escape
// differently between listing and download still match.
func normal(address string) string {
	u, err := url.Parse(address)
	if err != nil {
		return address
	}
	return u.Path
}
