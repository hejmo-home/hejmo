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

// outlookPush is what writing one Outlook calendar needs, passed around as one.
type outlookPush struct {
	*Service
	sess     *graph.Session
	cal      store.SyncedCalendar
	h        store.Household
	audience func(calendar.Event) (prop, line string)
	zone     *time.Location // the household's: the times of Outlook's answers
}

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
	zone, err := time.LoadLocation(h.Timezone)
	if err != nil {
		zone = time.UTC
	}
	p := &outlookPush{Service: s, sess: sess, cal: cal, h: h, audience: opts.Audience, zone: zone}
	var first error
	for _, po := range pending {
		if err := p.object(ctx, po); err != nil {
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

func (p *outlookPush) object(ctx context.Context, po store.PendingObject) error {
	content, err := p.db.ObjectEvents(p.h.ID, p.cal.ID, po.URL)
	if err != nil {
		return err
	}
	isNew := strings.HasPrefix(po.URL, store.NewOutlookPrefix)
	if len(content.Events) == 0 {
		return p.delete(ctx, po, isNew)
	}
	master, overrides := splitObject(content)
	if isNew {
		return p.create(ctx, po, master, overrides)
	}
	remote, err := p.sess.GetEvent(ctx, po.URL)
	if errors.Is(err, graph.ErrNotFound) {
		// Deleted outside while changed here: the box's change stands.
		return p.recreate(ctx, po, master, overrides)
	}
	if err != nil {
		return err
	}
	if remote.ChangeKey != po.ETag && po.ETag != "" {
		if !boxWinsAt(remote.LastModified, po.ChangedAt) {
			return p.outsideWins(ctx, po, remote, i18n.Msg("sync.conflict.outside_won", "title", master.Title), master.Title)
		}
		if err := p.db.LogConflict(p.h.ID, p.cal.ID, i18n.Msg("sync.conflict.box_won", "title", master.Title), describe(remote), p.now()); err != nil {
			return err
		}
	}
	return p.update(ctx, po, remote, master, overrides)
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

func (p *outlookPush) body(e calendar.Event) (map[string]any, string, error) {
	prop, line := p.audience(e)
	body, err := graph.BodyOf(e, line)
	return body, prop, err
}

func (p *outlookPush) create(ctx context.Context, po store.PendingObject, master calendar.Event, overrides []calendar.Event) error {
	body, prop, err := p.body(master)
	if err != nil {
		return err
	}
	created, err := p.sess.CreateEvent(ctx, p.cal.URL, body)
	if err != nil {
		return err
	}
	if err := p.db.OutlookCreated(p.cal.ID, po.URL, created.ID, po.Rev, created.ChangeKey); err != nil {
		return err
	}
	return p.afterWrite(ctx, created.ID, po.Rev, prop, master, overrides)
}

func (p *outlookPush) recreate(ctx context.Context, po store.PendingObject, master calendar.Event, overrides []calendar.Event) error {
	if err := p.create(ctx, po, master, overrides); err != nil {
		return err
	}
	return p.db.LogConflict(p.h.ID, p.cal.ID, i18n.Msg("sync.conflict.deleted_outside", "title", master.Title), "", p.now())
}

// update writes what the box changed against remote, the event as Outlook has
// it now (CODING_STANDARDS 2).
func (p *outlookPush) update(ctx context.Context, po store.PendingObject, remote graph.Event, master calendar.Event, overrides []calendar.Event) error {
	changes, prop, err := p.changes(master, remote)
	if err != nil {
		return err
	}
	changeKey := remote.ChangeKey
	if len(changes) > 0 {
		updated, err := p.sess.UpdateEvent(ctx, po.URL, changes)
		if err != nil {
			return err
		}
		changeKey = updated.ChangeKey
	} else {
		// The audience line is part of the notes: nothing to PATCH means the
		// audience is unchanged, and writing it again would only bump the
		// change key for nothing.
		prop = ""
	}
	if err := p.db.PushDone(p.cal.ID, po.URL, po.Rev, changeKey, ""); err != nil {
		return err
	}
	return p.afterWrite(ctx, po.URL, po.Rev, prop, master, overrides)
}

func (p *outlookPush) changes(e calendar.Event, remote graph.Event) (map[string]any, string, error) {
	prop, line := p.audience(e)
	changes, err := graph.ChangesOf(e, line, remote, p.zone)
	return changes, prop, err
}

// afterWrite sets the hidden audience extension and writes the changed and
// removed occurrences of a series. These writes change the event's change key
// again, so the key kept is read last: a second edit of the box before the
// next import must not look like a change made outside.
func (p *outlookPush) afterWrite(ctx context.Context, id string, rev int, audienceProp string, master calendar.Event, overrides []calendar.Event) error {
	wrote, err := p.writeParts(ctx, id, audienceProp, master, overrides)
	if err != nil || !wrote {
		return err
	}
	now, err := p.sess.GetEvent(ctx, id)
	if err != nil {
		return err
	}
	return p.db.PushDone(p.cal.ID, id, rev, now.ChangeKey, "")
}

func (p *outlookPush) writeParts(ctx context.Context, id, audienceProp string, master calendar.Event, overrides []calendar.Event) (bool, error) {
	sess := p.sess
	wrote := false
	if audienceProp != "" {
		if err := sess.SetAudience(ctx, id, audienceProp); err != nil {
			return wrote, err
		}
		wrote = true
	}
	if len(overrides) == 0 && len(master.ExDates) == 0 {
		return wrote, nil
	}
	from := master.Start.AddDate(0, 0, -1)
	to := p.now().AddDate(outlookFutureYears, 0, 0)
	instances, err := sess.Instances(ctx, id, from, to)
	if err != nil {
		return wrote, err
	}
	find := func(start time.Time) (graph.Event, bool) {
		for _, in := range instances {
			var at time.Time
			if in.Type == "exception" {
				at, _ = time.Parse(time.RFC3339, in.OriginalStart)
			} else if in.Start != nil {
				at, _, _ = parseStart(in)
			}
			if at.Equal(start) {
				return in, true
			}
		}
		return graph.Event{}, false
	}
	for _, o := range overrides {
		inst, ok := find(o.RecurrenceID)
		if !ok {
			continue
		}
		changes, _, err := p.changes(o, inst)
		if err != nil {
			return wrote, err
		}
		if len(changes) == 0 {
			continue
		}
		if _, err := sess.UpdateEvent(ctx, inst.ID, changes); err != nil {
			return wrote, err
		}
		wrote = true
	}
	for _, x := range master.ExDates {
		if inst, ok := find(x); ok {
			if err := sess.DeleteEvent(ctx, inst.ID); err != nil && !errors.Is(err, graph.ErrNotFound) {
				return wrote, err
			}
			wrote = true
		}
	}
	return wrote, nil
}

func parseStart(e graph.Event) (time.Time, string, error) {
	it, err := graph.ToEvent(e, time.UTC)
	return it.Event.Start, it.UnknownZone, err
}

func (p *outlookPush) delete(ctx context.Context, po store.PendingObject, isNew bool) error {
	if isNew || po.ETag == "" {
		return p.db.PushDeleted(p.cal.ID, po.URL, po.Rev) // never left the box
	}
	remote, err := p.sess.GetEvent(ctx, po.URL)
	if errors.Is(err, graph.ErrNotFound) {
		return p.db.PushDeleted(p.cal.ID, po.URL, po.Rev)
	}
	if err != nil {
		return err
	}
	if remote.ChangeKey != po.ETag && !boxWinsAt(remote.LastModified, po.ChangedAt) {
		return p.outsideWins(ctx, po, remote, i18n.Msg("sync.conflict.deleted_on_box", "title", remote.Subject), "")
	}
	if err := p.sess.DeleteEvent(ctx, po.URL); err != nil && !errors.Is(err, graph.ErrNotFound) {
		return err
	}
	return p.db.PushDeleted(p.cal.ID, po.URL, po.Rev)
}

// outsideWins makes the box say what Outlook says and keeps the box's
// version in the log.
func (p *outlookPush) outsideWins(ctx context.Context, po store.PendingObject, remote graph.Event, msg i18n.Message, boxTitle string) error {
	at := p.now()
	zone := p.zone
	var items []graph.Item
	if remote.Type == "seriesMaster" {
		from, to := at.AddDate(-1, 0, 0), at.AddDate(outlookFutureYears, 0, 0)
		instances, err := p.sess.Instances(ctx, remote.ID, from, to)
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
	names, err := p.audienceNames(p.h.ID)
	if err != nil {
		return err
	}
	change := store.ObjectChange{URL: po.URL, ETag: remote.ChangeKey}
	for _, it := range items {
		p.readAudience(&it.Event, names)
		change.Items = append(change.Items, store.SyncItem{Key: it.Key, SeriesKey: it.SeriesKey, Event: it.Event})
	}
	return p.db.ResolveToOutside(p.h.ID, p.cal.ID, change, po.Rev, store.Conflict{Message: msg, Detail: boxTitle}, at)
}
