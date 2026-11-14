package calsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"

	"github.com/hejmo-home/hejmo/internal/caldav"
	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/icsfeed"
	"github.com/hejmo-home/hejmo/internal/store"
)

// pushTimeout bounds one background push.
const pushTimeout = 60 * time.Second

// Push writes the box's queued changes of a two-way CalDAV calendar to its
// server now. Changes that cannot be written stay queued for the next push or
// sync; the failure is recorded in the sync log and returned.
func (s *Service) Push(ctx context.Context, householdID, calendarID string) error {
	mu := s.lock(calendarID)
	mu.Lock()
	defer mu.Unlock()
	cal, err := s.db.SyncedCalendar(householdID, calendarID)
	if err != nil {
		return err
	}
	if cal.Kind != "caldav" || cal.ReadOnly {
		return nil
	}
	h, err := s.db.Household(householdID)
	if err != nil {
		return err
	}
	at := s.now()
	interval := time.Duration(cal.Sync.IntervalMinutes) * time.Minute
	fail := func(msg i18n.Message, cause error) error {
		if err := s.db.FailSync(householdID, calendarID, msg, at, at.Add(Backoff(interval, cal.Sync.Failures+1))); err != nil {
			return errors.Join(cause, err)
		}
		return cause
	}
	pending, err := s.db.PendingObjects(cal.ID)
	if err != nil {
		return fail(i18n.Msg("sync.fail.store", "detail", err.Error()), err)
	}
	if len(pending) == 0 {
		return nil
	}
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
	if err := s.pushPending(ctx, cal, h, remote); err != nil {
		return fail(pushMessage(err), err)
	}
	return nil
}

// PushSoon pushes in the background, for the request that made the change not
// to wait for the calendar server. Wait waits for such pushes.
func (s *Service) PushSoon(householdID, calendarID string) {
	s.pushes.Add(1)
	go func() {
		defer s.pushes.Done()
		ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
		defer cancel()
		if err := s.Push(ctx, householdID, calendarID); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.logger.Warn("calendar push failed", "calendar", calendarID, "err", err)
		}
	}()
}

// Wait returns when the pushes started by PushSoon have finished.
func (s *Service) Wait() { s.pushes.Wait() }

// pushPending writes every queued object of a calendar. It returns the first
// problem after trying the others; a problem that makes the others pointless
// (wrong password, foreign host) stops it at once. The caller holds the lock.
func (s *Service) pushPending(ctx context.Context, cal store.SyncedCalendar, h store.Household, remote Remote) error {
	pending, err := s.db.PendingObjects(cal.ID)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	opts, err := s.patchOptions(h)
	if err != nil {
		return err
	}
	var first error
	for _, po := range pending {
		if err := s.pushObject(ctx, cal, h, remote, po, opts); err != nil {
			if abortsSync(err) {
				return err
			}
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// abortsSync says whether err makes it pointless to go on with the server.
func abortsSync(err error) bool {
	var foreign *caldavclient.ForeignHostError
	var status *caldavclient.StatusError
	return errors.As(err, &foreign) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &status) && status.Code == http.StatusUnauthorized)
}

// remoteMessage is the sync log message for a failure talking to the server.
func remoteMessage(err error) i18n.Message {
	if caldavclient.IsUnauthorized(err) {
		return i18n.Msg("sync.fail.login")
	}
	var foreign *caldavclient.ForeignHostError
	if errors.As(err, &foreign) && foreign.Downgrade {
		return i18n.Msg("sync.fail.downgrade", "host", foreign.Host)
	}
	if errors.As(err, &foreign) {
		return i18n.Msg("sync.fail.foreign_host", "host", foreign.Host, "domain", foreign.Domain)
	}
	return MessageOf(err)
}

// pushMessage is the sync log message for a failed write.
func pushMessage(err error) i18n.Message {
	var status *caldavclient.StatusError
	if errors.As(err, &status) && status.Code == http.StatusForbidden {
		return i18n.Msg("sync.fail.write_refused")
	}
	var f *Failure
	var foreign *caldavclient.ForeignHostError
	if caldavclient.IsUnauthorized(err) || errors.As(err, &foreign) || errors.As(err, &f) {
		return remoteMessage(err)
	}
	return i18n.Msg("sync.fail.push", "detail", err.Error())
}

// patchOptions says how box events are written into iCalendar text.
func (s *Service) patchOptions(h store.Household) (caldav.PatchOptions, error) {
	zone, err := time.LoadLocation(h.Timezone)
	if err != nil {
		zone = time.UTC
	}
	people, err := s.db.People(h.ID)
	if err != nil {
		return caldav.PatchOptions{}, err
	}
	groups, err := s.db.Groups(h.ID)
	if err != nil {
		return caldav.PatchOptions{}, err
	}
	names := map[string]string{}
	for _, p := range people {
		names[p.ID] = p.Name
	}
	for _, g := range groups {
		names[g.ID] = g.Name
	}
	audience := func(e calendar.Event) (string, string) {
		if len(e.People) == 0 && len(e.Groups) == 0 {
			return "", ""
		}
		var who []string
		for _, id := range append(append([]string{}, e.People...), e.Groups...) {
			if n := names[id]; n != "" {
				who = append(who, n)
			}
		}
		sort.Strings(who)
		return icsfeed.FormatAudience(sortedCopy(e.People), sortedCopy(e.Groups)),
			s.bundle.Format(s.lang, i18n.Msg("sync.audience_line", "names", strings.Join(who, ", ")))
	}
	return caldav.PatchOptions{Zone: zone, Now: s.now(), AudienceLines: s.audienceLines(), Audience: audience}, nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// audienceLines are the beginnings of the readable audience line in every
// language, so lines written in any of them are recognised on import.
func (s *Service) audienceLines() []string {
	var out []string
	for _, lang := range s.bundle.Languages() {
		head, _, _ := strings.Cut(s.bundle.T(lang, "sync.audience_line"), "{names}")
		if head != "" {
			out = append(out, head)
		}
	}
	return out
}

// importOptions are the options for reading objects of a CalDAV calendar.
func (s *Service) importOptions(zone *time.Location, at time.Time) icsfeed.Options {
	return icsfeed.Options{Zone: zone, Now: at, AudienceLines: s.audienceLines()}
}

// audienceIDs are the ids of the household's people and groups.
func (s *Service) audienceIDs(householdID string) (map[string]bool, error) {
	people, err := s.db.People(householdID)
	if err != nil {
		return nil, err
	}
	groups, err := s.db.Groups(householdID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, p := range people {
		out[p.ID] = true
	}
	for _, g := range groups {
		out[g.ID] = true
	}
	return out, nil
}

// keepKnown drops audience entries that name nobody in this household (an
// event written by another box).
func keepKnown(in []string, known map[string]bool) []string {
	var out []string
	for _, id := range in {
		if known[id] {
			out = append(out, id)
		}
	}
	return out
}

// pushObject writes one queued object: a create, a change or a delete.
func (s *Service) pushObject(ctx context.Context, cal store.SyncedCalendar, h store.Household, remote Remote, po store.PendingObject, opts caldav.PatchOptions) error {
	content, err := s.db.ObjectEvents(h.ID, cal.ID, po.URL)
	if err != nil {
		return err
	}
	if len(content.Events) == 0 {
		return s.pushDelete(ctx, cal, h, remote, po)
	}
	obj := objectOf(content)
	raw, err := caldav.Patch(po.Raw, obj, opts)
	if err != nil {
		return fmt.Errorf("writing %s: %w", po.URL, err)
	}
	if po.ETag != "" && raw == po.Raw {
		return s.db.PushDone(cal.ID, po.URL, po.Rev, po.ETag, po.Raw) // the server already says so
	}
	etag, err := remote.Put(ctx, po.URL, raw, po.ETag)
	if caldavclient.IsConflict(err) {
		return s.resolvePut(ctx, cal, h, remote, po, obj, raw, opts)
	}
	if err != nil {
		return err
	}
	return s.finishPut(ctx, cal, remote, po, etag, raw)
}

// finishPut records a successful write. A server that does not report the new
// ETag is asked for it, and its own text is kept.
func (s *Service) finishPut(ctx context.Context, cal store.SyncedCalendar, remote Remote, po store.PendingObject, etag, raw string) error {
	if etag == "" {
		got, err := remote.Fetch(ctx, cal.URL, []string{po.URL})
		if err != nil {
			return err
		}
		if len(got) == 1 && got[0].Err == nil {
			etag, raw = got[0].ETag, string(got[0].Data)
		}
	}
	return s.db.PushDone(cal.ID, po.URL, po.Rev, etag, raw)
}

func objectOf(c store.ObjectContent) caldav.Object {
	o := caldav.Object{UID: c.UID}
	for _, e := range c.Events {
		if e.SeriesID == "" {
			o.Master = e
		} else {
			o.Overrides = append(o.Overrides, e)
		}
	}
	if o.UID == "" {
		o.UID = o.Master.ID
	}
	return o
}

func (s *Service) pushDelete(ctx context.Context, cal store.SyncedCalendar, h store.Household, remote Remote, po store.PendingObject) error {
	if po.ETag == "" {
		return s.db.PushDeleted(cal.ID, po.URL, po.Rev) // never left the box
	}
	err := remote.Delete(ctx, po.URL, po.ETag)
	switch {
	case err == nil || caldavclient.IsNotFound(err):
		return s.db.PushDeleted(cal.ID, po.URL, po.Rev)
	case caldavclient.IsConflict(err):
		return s.resolveDelete(ctx, cal, h, remote, po)
	}
	return err
}

// current fetches the server's version of an object; nil means it is gone.
func current(ctx context.Context, cal store.SyncedCalendar, remote Remote, address string) (*caldavclient.Object, error) {
	got, err := remote.Fetch(ctx, cal.URL, []string{address})
	if err != nil {
		return nil, err
	}
	for i := range got {
		if caldavclient.AddressKey(got[i].URL) == caldavclient.AddressKey(address) {
			return &got[i], nil
		}
	}
	return nil, nil
}

// boxWins decides a conflict by the later change (ADR 0012): LAST-MODIFIED
// outside against the time the box last changed the object. A tie, or an
// object without a date, goes to the box: its change is the one the family
// just made.
func boxWins(raw []byte, changedAt time.Time) bool {
	lm := lastModified(raw)
	return lm.IsZero() || !lm.After(changedAt)
}

// lastModified is the latest LAST-MODIFIED (else DTSTAMP) of an object's events.
func lastModified(raw []byte) time.Time {
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return time.Time{}
	}
	var latest time.Time
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		p := c.Props.Get(ical.PropLastModified)
		if p == nil {
			p = c.Props.Get(ical.PropDateTimeStamp)
		}
		if p == nil {
			continue
		}
		if t, err := time.Parse("20060102T150405Z", strings.TrimSpace(p.Value)); err == nil && t.After(latest) {
			latest = t
		}
	}
	return latest
}

func titleOf(c store.ObjectContent) string {
	for _, e := range c.Events {
		if e.SeriesID == "" {
			return e.Title
		}
	}
	return ""
}

// resolvePut settles a write the server refused because its version is not the
// one the box knew.
func (s *Service) resolvePut(ctx context.Context, cal store.SyncedCalendar, h store.Household, remote Remote, po store.PendingObject, obj caldav.Object, boxText string, opts caldav.PatchOptions) error {
	at := s.now()
	theirs, err := current(ctx, cal, remote, po.URL)
	if err != nil {
		return err
	}
	title := obj.Master.Title
	if theirs == nil {
		// Deleted outside while changed here: nothing of theirs to lose, so the
		// box's change stands and the event is written back.
		raw, err := caldav.Patch("", obj, opts)
		if err != nil {
			return err
		}
		etag, err := remote.Put(ctx, po.URL, raw, "")
		if err != nil {
			return err
		}
		if err := s.finishPut(ctx, cal, remote, po, etag, raw); err != nil {
			return err
		}
		return s.db.LogConflict(h.ID, cal.ID, i18n.Msg("sync.conflict.deleted_outside", "title", title), po.Raw, at)
	}
	if theirs.Err != nil {
		return fmt.Errorf("the server's version of %s cannot be read, so the conflict cannot be settled: %w", po.URL, theirs.Err)
	}
	if boxWins(theirs.Data, po.ChangedAt) {
		raw, err := caldav.Patch(string(theirs.Data), obj, opts)
		if err != nil {
			return err
		}
		etag, err := remote.Put(ctx, po.URL, raw, theirs.ETag)
		if err != nil {
			return err // changed again meanwhile: the next sync settles it
		}
		if err := s.finishPut(ctx, cal, remote, po, etag, raw); err != nil {
			return err
		}
		return s.db.LogConflict(h.ID, cal.ID, i18n.Msg("sync.conflict.box_won", "title", title), string(theirs.Data), at)
	}
	return s.outsideWins(h, cal, po, theirs, i18n.Msg("sync.conflict.outside_won", "title", title), boxText, at)
}

// resolveDelete settles a delete the server refused because its version changed.
func (s *Service) resolveDelete(ctx context.Context, cal store.SyncedCalendar, h store.Household, remote Remote, po store.PendingObject) error {
	at := s.now()
	theirs, err := current(ctx, cal, remote, po.URL)
	if err != nil {
		return err
	}
	if theirs == nil {
		return s.db.PushDeleted(cal.ID, po.URL, po.Rev)
	}
	if theirs.Err != nil {
		return fmt.Errorf("the server's version of %s cannot be read, so the conflict cannot be settled: %w", po.URL, theirs.Err)
	}
	title := titleFromRaw(po.Raw, theirs.Data)
	if boxWins(theirs.Data, po.ChangedAt) {
		if err := remote.Delete(ctx, po.URL, theirs.ETag); err != nil && !caldavclient.IsNotFound(err) {
			return err
		}
		if err := s.db.PushDeleted(cal.ID, po.URL, po.Rev); err != nil {
			return err
		}
		return s.db.LogConflict(h.ID, cal.ID, i18n.Msg("sync.conflict.box_won", "title", title), string(theirs.Data), at)
	}
	return s.outsideWins(h, cal, po, theirs, i18n.Msg("sync.conflict.deleted_on_box", "title", title), po.Raw, at)
}

// titleFromRaw names an object for the log from the first text that has a title.
func titleFromRaw(texts ...any) string {
	for _, t := range texts {
		var b []byte
		switch v := t.(type) {
		case string:
			b = []byte(v)
		case []byte:
			b = v
		}
		if res, err := icsfeed.Parse(b, icsfeed.Options{Now: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)}); err == nil {
			for _, it := range res.Items {
				if it.SeriesKey == "" && it.Event.Title != "" {
					return it.Event.Title
				}
			}
		}
	}
	return ""
}

// outsideWins makes the box say what the server says and keeps the box's
// version in the log.
func (s *Service) outsideWins(h store.Household, cal store.SyncedCalendar, po store.PendingObject, theirs *caldavclient.Object, msg i18n.Message, losing string, at time.Time) error {
	zone, err := time.LoadLocation(h.Timezone)
	if err != nil {
		zone = time.UTC
	}
	res, err := icsfeed.Parse(theirs.Data, s.importOptions(zone, at))
	if err != nil {
		return err
	}
	ids, err := s.audienceIDs(h.ID)
	if err != nil {
		return err
	}
	change := store.ObjectChange{URL: po.URL, ETag: theirs.ETag, Raw: string(theirs.Data)}
	for _, it := range res.Items {
		it.Event.People, it.Event.Groups = keepKnown(it.Event.People, ids), keepKnown(it.Event.Groups, ids)
		change.Items = append(change.Items, store.SyncItem{Key: it.Key, SeriesKey: it.SeriesKey, Event: it.Event})
	}
	return s.db.ResolveToOutside(h.ID, cal.ID, change, po.Rev, store.Conflict{Message: msg, Detail: losing}, at)
}
