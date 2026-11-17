package calsync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/graph"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Outlook calendars (ADR 0020): events come from Microsoft Graph's calendarView
// delta; a changed occurrence refreshes its whole series; box changes go out
// event by event. The import window is the last 12 months and the next three
// years, moved forward by a fresh start every 30 days.
const (
	outlookFutureYears = 3
	outlookRebaseline  = 30 * 24 * time.Hour
)

// SetGraph sets how the service talks to Microsoft (tests point it at a fake).
func (s *Service) SetGraph(c *graph.Client) { s.graph = c }

// Graph is the client for Microsoft Graph and the sign-in.
func (s *Service) Graph() *graph.Client {
	if s.graph == nil {
		s.graph = graph.New()
	}
	return s.graph
}

// errNoApp means neither the family nor the project has an app registration.
var errNoApp = errors.New("no app registration configured")

// withOutlook runs fn with a session of an Outlook connection. A sign-in that
// Microsoft ended is remembered on the connection, and while it is, nothing
// goes to Microsoft.
func (s *Service) withOutlook(ctx context.Context, householdID, connectionID string, fn func(*graph.Session, store.Connection) error) error {
	conn, err := s.db.Connection(householdID, connectionID)
	if err != nil {
		return err
	}
	if conn.Kind != "outlook" {
		return store.ErrNotFound
	}
	if !conn.ReauthSince.IsZero() {
		return fmt.Errorf("%w: signed out since %s", graph.ErrReauth, conn.ReauthSince.Format(time.RFC3339))
	}
	_, token, err := s.db.ConnectionCredentials(householdID, connectionID)
	if err != nil {
		return err
	}
	h, err := s.db.Household(householdID)
	if err != nil {
		return err
	}
	zone, err := time.LoadLocation(h.Timezone)
	if err != nil {
		zone = time.UTC
	}
	g := s.Graph()
	clientID := g.AppID(conn.ClientID)
	if clientID == "" {
		return errNoApp
	}
	sess := g.Session(clientID, token, func(refresh string) {
		if err := s.db.SaveRefreshToken(conn.ID, refresh, s.now()); err != nil {
			s.logger.Error("outlook: keeping the new sign-in failed", "err", err)
		}
	}).InZone(zone)
	err = fn(sess, conn)
	if errors.Is(err, graph.ErrReauth) {
		_ = s.db.MarkReauth(conn.ID, s.now())
	}
	return err
}

// OutlookFound is a calendar of an Outlook account as parents are shown it.
type OutlookFound struct {
	ID, Name string
	// Locked: Outlook does not let the account change it (shared with the
	// account, birthdays, holidays). SharedBy names who shared it, "Name (address)".
	Locked   bool
	SharedBy string
}

// DiscoverOutlook lists the calendars of a connection's account. It stores nothing.
func (s *Service) DiscoverOutlook(ctx context.Context, householdID, connectionID string) ([]OutlookFound, error) {
	var out []OutlookFound
	err := s.withOutlook(ctx, householdID, connectionID, func(sess *graph.Session, conn store.Connection) error {
		cals, err := sess.Calendars(ctx)
		if err != nil {
			return err
		}
		for _, c := range cals {
			f := OutlookFound{ID: c.ID, Name: c.Name}
			// Shared with the account: someone else's address and not the
			// account's to share. The address alone is no signal, for personal
			// accounts can name an outlook_…@outlook.com owner of their own.
			if !c.CanShare && !strings.EqualFold(c.OwnerAddress, conn.Username) && c.OwnerAddress != "" {
				f.SharedBy = c.OwnerName + " (" + c.OwnerAddress + ")"
				if c.OwnerName == "" {
					f.SharedBy = c.OwnerAddress
				}
			}
			f.Locked = !c.CanEdit || f.SharedBy != ""
			out = append(out, f)
		}
		return nil
	})
	return out, err
}

// CountOutlook counts the events of one Outlook calendar without downloading them.
func (s *Service) CountOutlook(ctx context.Context, householdID, connectionID, calendarID string) (int, error) {
	n := 0
	err := s.withOutlook(ctx, householdID, connectionID, func(sess *graph.Session, _ store.Connection) error {
		var err error
		n, err = sess.CountEvents(ctx, calendarID, s.now().AddDate(-1, 0, 0))
		return err
	})
	return n, err
}

// outlookMessage is the sync log message for a failure talking to Microsoft.
func outlookMessage(err error) i18n.Message {
	var status *graph.StatusError
	switch {
	case errors.Is(err, graph.ErrReauth):
		return i18n.Msg("sync.fail.outlook_signin")
	case errors.Is(err, errNoApp):
		return i18n.Msg("sync.fail.outlook_app")
	case errors.As(err, &status) && status.Code == http.StatusForbidden:
		return i18n.Msg("sync.fail.write_refused")
	}
	return MessageOf(err)
}

func outlookAborts(err error) bool {
	return errors.Is(err, graph.ErrReauth) || errors.Is(err, errNoApp) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (s *Service) syncOutlook(ctx context.Context, cal store.SyncedCalendar, h store.Household, at time.Time, run *runInfo, fail func(i18n.Message, error) error) error {
	var pushErr error
	err := s.withOutlook(ctx, h.ID, cal.ConnectionID, func(sess *graph.Session, _ store.Connection) error {
		// Box changes go out first, so the import below never overwrites them.
		if !cal.ReadOnly {
			if pushErr = s.pushOutlook(ctx, sess, cal, h); pushErr != nil && outlookAborts(pushErr) {
				return pushErr
			}
		}
		return s.importOutlook(ctx, sess, cal, h, at, run)
	})
	var bad *storeFailure
	switch {
	case errors.As(err, &bad):
		return fail(i18n.Msg("sync.fail.store", "detail", bad.err.Error()), bad.err)
	case err != nil:
		return fail(outlookMessage(err), err)
	}
	if pushErr != nil {
		return fail(pushMessageOutlook(pushErr), pushErr)
	}
	return nil
}

func pushMessageOutlook(err error) i18n.Message {
	if m := outlookMessage(err); m.Key != "sync.fail.fetch" {
		return m
	}
	return i18n.Msg("sync.fail.push", "detail", err.Error())
}

// storeFailure marks an error of the box's own database, which is told apart
// from a failure of Microsoft.
type storeFailure struct{ err error }

func (e *storeFailure) Error() string { return e.err.Error() }
func (e *storeFailure) Unwrap() error { return e.err }

func (s *Service) importOutlook(ctx context.Context, sess *graph.Session, cal store.SyncedCalendar, h store.Household, at time.Time, run *runInfo) error {
	st := func(err error) error { return &storeFailure{err} }
	pending, err := s.db.PendingObjects(cal.ID)
	if err != nil {
		return st(err)
	}
	waiting, discard := map[string]bool{}, map[string]bool{}
	for _, p := range pending {
		if cal.ReadOnly {
			discard[p.URL] = true
		} else {
			waiting[p.URL] = true
		}
	}
	known, err := s.db.RemoteObjects(cal.ID, cal.ReadOnly)
	if err != nil {
		return st(err)
	}
	prev, err := s.db.RemoteState(cal.ID)
	if err != nil {
		return st(err)
	}
	zone, err := time.LoadLocation(h.Timezone)
	if err != nil {
		zone = time.UTC
	}
	from, to := at.AddDate(-1, 0, 0), at.AddDate(outlookFutureYears, 0, 0)

	baseline, _ := time.Parse(time.RFC3339, prev.CTag)
	link := prev.Token
	if baseline.IsZero() || at.Sub(baseline) > outlookRebaseline || len(discard) > 0 {
		link = ""
	}
	res, err := sess.Delta(ctx, cal.URL, link, from, to)
	if errors.Is(err, graph.ErrDeltaExpired) && link != "" {
		link = ""
		res, err = sess.Delta(ctx, cal.URL, "", from, to)
	}
	if err != nil {
		return err
	}
	full := link == ""
	if full {
		baseline = at
	}

	singles := map[string]graph.Event{}
	refresh := map[string]bool{}
	var deleted []string
	refreshAll := false
	for _, ev := range res.Events {
		switch {
		case ev.Removed != nil || ev.IsCancelled:
			if _, ok := known[ev.ID]; ok {
				deleted = append(deleted, ev.ID)
			} else if ev.SeriesMasterID != "" {
				refresh[ev.SeriesMasterID] = true
			} else {
				refreshAll = true // an occurrence of some series went away
			}
		case ev.Type == "occurrence" || ev.Type == "exception":
			refresh[ev.SeriesMasterID] = true
		case ev.Type == "seriesMaster":
			refresh[ev.ID] = true
		default:
			singles[ev.ID] = ev
		}
	}
	if full {
		for obj := range known {
			if _, ok := singles[obj]; !ok && !refresh[obj] && !waiting[obj] {
				deleted = append(deleted, obj)
			}
		}
	} else if refreshAll {
		series, err := s.db.SeriesObjects(cal.ID)
		if err != nil {
			return st(err)
		}
		for _, id := range series {
			refresh[id] = true
		}
	}
	deleted = dedupe(deleted)

	ids, err := s.audienceNames(h.ID)
	if err != nil {
		return st(err)
	}
	var changes []store.ObjectChange
	var notes []i18n.Message
	zones := map[string]bool{}
	convert := func(items []itemLike, id, changeKey string) {
		c := store.ObjectChange{URL: id, ETag: changeKey, DropPending: cal.ReadOnly}
		for _, it := range items {
			if it.zone != "" && !zones[it.zone] {
				zones[it.zone] = true
				notes = append(notes, i18n.Msg("sync.note.unknown_zone", "zone", it.zone))
			}
			s.readAudience(&it.event, ids)
			c.Items = append(c.Items, store.SyncItem{Key: it.key, SeriesKey: it.seriesKey, Event: it.event})
		}
		changes = append(changes, c)
	}
	for id, ev := range singles {
		if waiting[id] {
			continue
		}
		if etag, ok := known[id]; ok && etag == ev.ChangeKey && !discard[id] {
			continue
		}
		it, err := graph.ToEvent(ev, zone)
		if err != nil {
			notes = append(notes, i18n.Msg("sync.note.bad_object", "object", id, "detail", err.Error()))
			continue
		}
		convert([]itemLike{like(it)}, id, ev.ChangeKey)
	}
	ordered := make([]string, 0, len(refresh))
	for id := range refresh {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		if waiting[id] {
			continue
		}
		master, err := sess.GetEvent(ctx, id)
		if errors.Is(err, graph.ErrNotFound) || (err == nil && master.IsCancelled) {
			if _, ok := known[id]; ok {
				deleted = append(deleted, id)
			}
			continue
		}
		if err != nil {
			return err
		}
		instances, err := sess.Instances(ctx, id, from, to)
		if err != nil {
			return err
		}
		items, err := graph.SeriesItems(master, instances, zone, from, to)
		if err != nil {
			notes = append(notes, i18n.Msg("sync.note.bad_object", "object", id, "detail", err.Error()))
			continue
		}
		var ls []itemLike
		for _, it := range items {
			ls = append(ls, like(it))
		}
		convert(ls, id, master.ChangeKey)
	}
	run.listed, run.fetched = len(known)+len(changes)-len(deleted), len(changes)
	run.skipped = skipped(notes)
	interval := time.Duration(cal.Sync.IntervalMinutes) * time.Minute
	// The baseline time travels in the CTag column: when the delta link began.
	done, err := s.db.CompleteObjectSyncWithState(h.ID, cal.ID, store.RemoteState{CTag: baseline.UTC().Format(time.RFC3339), Token: res.Link}, changes, deleted, notes, at, at.Add(interval))
	if err != nil {
		return st(fmt.Errorf("could not store the events: %w", err))
	}
	run.done = done
	return nil
}

type itemLike struct {
	key, seriesKey, zone string
	event                calendar.Event
}

func like(it graph.Item) itemLike {
	return itemLike{it.Key, it.SeriesKey, it.UnknownZone, it.Event}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// audienceNames maps the names of people and groups to their ids. A name two
// of them share is left out: the line cannot say which one.
func (s *Service) audienceNames(householdID string) (map[string]string, error) {
	people, err := s.db.People(householdID)
	if err != nil {
		return nil, err
	}
	groups, err := s.db.Groups(householdID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	dup := map[string]bool{}
	add := func(name, id string) {
		if _, ok := out[name]; ok {
			dup[name] = true
		}
		out[name] = id
	}
	for _, p := range people {
		add(p.Name, "person:"+p.ID)
	}
	for _, g := range groups {
		add(g.Name, "group:"+g.ID)
	}
	for n := range dup {
		delete(out, n)
	}
	return out, nil
}

// readAudience reads the readable audience line ("For: Mia, Sebastian") from
// the end of the notes, sets the audience from it and takes the line out of
// the description again.
func (s *Service) readAudience(e *calendar.Event, names map[string]string) {
	desc := e.Description
	i := strings.LastIndex(desc, "\n")
	last := desc[i+1:]
	for _, head := range s.audienceLines() {
		if head == "" || !strings.HasPrefix(last, head) {
			continue
		}
		for _, n := range strings.Split(strings.TrimSpace(strings.TrimPrefix(last, head)), ", ") {
			id, ok := names[strings.TrimSpace(n)]
			switch {
			case !ok:
			case strings.HasPrefix(id, "person:"):
				e.People = append(e.People, strings.TrimPrefix(id, "person:"))
			default:
				e.Groups = append(e.Groups, strings.TrimPrefix(id, "group:"))
			}
		}
		if i < 0 {
			e.Description = ""
		} else {
			e.Description = strings.TrimRight(desc[:i], " \t\r\n")
		}
		return
	}
}
