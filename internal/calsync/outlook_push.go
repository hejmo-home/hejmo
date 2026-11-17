package calsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/graph"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// pushOutlook writes every queued object of an Outlook calendar. It returns the
// first problem after trying the others; a problem that makes the others
// pointless (the sign-in ended) stops it at once. The caller holds the lock.
func (s *Service) pushOutlook(ctx context.Context, sess *graph.Session, cal store.SyncedCalendar, h store.Household) error {
	pending, err := s.db.PendingObjects(cal.ID)
	if err != nil || len(pending) == 0 {
		return err
	}
	opts, err := s.patchOptions(h)
	if err != nil {
		return err
	}
	var first error
	for _, po := range pending {
		if err := s.pushOutlookObject(ctx, sess, cal, h, po, opts.Audience); err != nil {
			if outlookAborts(err) {
				return err
			}
			if first == nil {
				first = err
			}
		}
	}
	return first
}

func (s *Service) pushOutlookObject(ctx context.Context, sess *graph.Session, cal store.SyncedCalendar, h store.Household, po store.PendingObject, audience func(calendar.Event) (string, string)) error {
	content, err := s.db.ObjectEvents(h.ID, cal.ID, po.URL)
	if err != nil {
		return err
	}
	isNew := strings.HasPrefix(po.URL, store.NewOutlookPrefix)
	if len(content.Events) == 0 {
		return s.pushOutlookDelete(ctx, sess, cal, h, po, isNew)
	}
	master, overrides := splitObject(content)
	if isNew {
		return s.createOutlook(ctx, sess, cal, po, master, overrides, audience)
	}
	remote, err := sess.GetEvent(ctx, po.URL)
	if errors.Is(err, graph.ErrNotFound) {
		// Deleted outside while changed here: the box's change stands.
		return s.recreateOutlook(ctx, sess, cal, h, po, master, overrides, audience)
	}
	if err != nil {
		return err
	}
	if remote.ChangeKey != po.ETag && po.ETag != "" {
		if !boxWinsAt(remote.LastModified, po.ChangedAt) {
			return s.outlookOutsideWins(ctx, sess, cal, h, po, remote, i18n.Msg("sync.conflict.outside_won", "title", master.Title), master.Title)
		}
		if err := s.db.LogConflict(h.ID, cal.ID, i18n.Msg("sync.conflict.box_won", "title", master.Title), describe(remote), s.now()); err != nil {
			return err
		}
	}
	return s.updateOutlook(ctx, sess, cal, po, master, overrides, audience)
}

func splitObject(c store.ObjectContent) (calendar.Event, []calendar.Event) {
	var master calendar.Event
	var overrides []calendar.Event
	for _, e := range c.Events {
		if e.SeriesID == "" {
			master = e
		} else {
			overrides = append(overrides, e)
		}
	}
	return master, overrides
}

// boxWinsAt decides a conflict by the later change (ADR 0012): Outlook's last
// modification against the time the box last changed the object. A tie, or no
// date, goes to the box: its change is the one the family just made.
func boxWinsAt(lastModified string, changedAt time.Time) bool {
	t, err := time.Parse(time.RFC3339, lastModified)
	return err != nil || !t.After(changedAt)
}

// describe names an Outlook event for the log's losing version.
func describe(e graph.Event) string {
	when := ""
	if e.Start != nil {
		when = e.Start.DateTime
	}
	return fmt.Sprintf("%s (%s)", e.Subject, when)
}

func (s *Service) outlookBody(e calendar.Event, audience func(calendar.Event) (string, string)) (map[string]any, string, error) {
	prop, line := audience(e)
	body, err := graph.BodyOf(e, line)
	return body, prop, err
}

func (s *Service) createOutlook(ctx context.Context, sess *graph.Session, cal store.SyncedCalendar, po store.PendingObject, master calendar.Event, overrides []calendar.Event, audience func(calendar.Event) (string, string)) error {
	body, prop, err := s.outlookBody(master, audience)
	if err != nil {
		return err
	}
	created, err := sess.CreateEvent(ctx, cal.URL, body)
	if err != nil {
		return err
	}
	if err := s.db.OutlookCreated(cal.ID, po.URL, created.ID, po.Rev, created.ChangeKey); err != nil {
		return err
	}
	return s.afterWrite(ctx, sess, created.ID, prop, master, overrides, audience)
}

func (s *Service) recreateOutlook(ctx context.Context, sess *graph.Session, cal store.SyncedCalendar, h store.Household, po store.PendingObject, master calendar.Event, overrides []calendar.Event, audience func(calendar.Event) (string, string)) error {
	if err := s.createOutlook(ctx, sess, cal, po, master, overrides, audience); err != nil {
		return err
	}
	return s.db.LogConflict(h.ID, cal.ID, i18n.Msg("sync.conflict.deleted_outside", "title", master.Title), "", s.now())
}

func (s *Service) updateOutlook(ctx context.Context, sess *graph.Session, cal store.SyncedCalendar, po store.PendingObject, master calendar.Event, overrides []calendar.Event, audience func(calendar.Event) (string, string)) error {
	body, prop, err := s.outlookBody(master, audience)
	if err != nil {
		return err
	}
	updated, err := sess.UpdateEvent(ctx, po.URL, body)
	if err != nil {
		return err
	}
	if err := s.db.PushDone(cal.ID, po.URL, po.Rev, updated.ChangeKey, ""); err != nil {
		return err
	}
	return s.afterWrite(ctx, sess, po.URL, prop, master, overrides, audience)
}

// afterWrite sets the hidden audience extension and writes the changed and
// removed occurrences of a series.
func (s *Service) afterWrite(ctx context.Context, sess *graph.Session, id, audienceProp string, master calendar.Event, overrides []calendar.Event, audience func(calendar.Event) (string, string)) error {
	if audienceProp != "" {
		if err := sess.SetAudience(ctx, id, audienceProp); err != nil {
			return err
		}
	}
	if len(overrides) == 0 && len(master.ExDates) == 0 {
		return nil
	}
	from := master.Start.AddDate(0, 0, -1)
	to := s.now().AddDate(outlookFutureYears, 0, 0)
	instances, err := sess.Instances(ctx, id, from, to)
	if err != nil {
		return err
	}
	find := func(start time.Time) string {
		for _, in := range instances {
			var at time.Time
			if in.Type == "exception" {
				at, _ = time.Parse(time.RFC3339, in.OriginalStart)
			} else if in.Start != nil {
				at, _, _ = parseStart(in)
			}
			if at.Equal(start) {
				return in.ID
			}
		}
		return ""
	}
	for _, o := range overrides {
		inst := find(o.RecurrenceID)
		if inst == "" {
			continue
		}
		body, _, err := s.outlookBody(o, audience)
		if err != nil {
			return err
		}
		if _, err := sess.UpdateEvent(ctx, inst, body); err != nil {
			return err
		}
	}
	for _, x := range master.ExDates {
		if inst := find(x); inst != "" {
			if err := sess.DeleteEvent(ctx, inst); err != nil && !errors.Is(err, graph.ErrNotFound) {
				return err
			}
		}
	}
	return nil
}

func parseStart(e graph.Event) (time.Time, string, error) {
	it, err := graph.ToEvent(e, time.UTC)
	return it.Event.Start, it.UnknownZone, err
}

func (s *Service) pushOutlookDelete(ctx context.Context, sess *graph.Session, cal store.SyncedCalendar, h store.Household, po store.PendingObject, isNew bool) error {
	if isNew || po.ETag == "" {
		return s.db.PushDeleted(cal.ID, po.URL, po.Rev) // never left the box
	}
	remote, err := sess.GetEvent(ctx, po.URL)
	if errors.Is(err, graph.ErrNotFound) {
		return s.db.PushDeleted(cal.ID, po.URL, po.Rev)
	}
	if err != nil {
		return err
	}
	if remote.ChangeKey != po.ETag && !boxWinsAt(remote.LastModified, po.ChangedAt) {
		return s.outlookOutsideWins(ctx, sess, cal, h, po, remote, i18n.Msg("sync.conflict.deleted_on_box", "title", remote.Subject), "")
	}
	if err := sess.DeleteEvent(ctx, po.URL); err != nil && !errors.Is(err, graph.ErrNotFound) {
		return err
	}
	return s.db.PushDeleted(cal.ID, po.URL, po.Rev)
}

// outlookOutsideWins makes the box say what Outlook says and keeps the box's
// version in the log.
func (s *Service) outlookOutsideWins(ctx context.Context, sess *graph.Session, cal store.SyncedCalendar, h store.Household, po store.PendingObject, remote graph.Event, msg i18n.Message, boxTitle string) error {
	at := s.now()
	zone, err := time.LoadLocation(h.Timezone)
	if err != nil {
		zone = time.UTC
	}
	var items []graph.Item
	if remote.Type == "seriesMaster" {
		from, to := at.AddDate(-1, 0, 0), at.AddDate(outlookFutureYears, 0, 0)
		instances, err := sess.Instances(ctx, remote.ID, from, to)
		if err != nil {
			return err
		}
		if items, err = graph.SeriesItems(remote, instances, zone, from, to); err != nil {
			return err
		}
	} else {
		it, err := graph.ToEvent(remote, zone)
		if err != nil {
			return err
		}
		items = []graph.Item{it}
	}
	names, err := s.audienceNames(h.ID)
	if err != nil {
		return err
	}
	change := store.ObjectChange{URL: po.URL, ETag: remote.ChangeKey}
	for _, it := range items {
		s.readAudience(&it.Event, names)
		change.Items = append(change.Items, store.SyncItem{Key: it.Key, SeriesKey: it.SeriesKey, Event: it.Event})
	}
	return s.db.ResolveToOutside(h.ID, cal.ID, change, po.Rev, store.Conflict{Message: msg, Detail: boxTitle}, at)
}
