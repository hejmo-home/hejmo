package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	webcal "github.com/emersion/go-webdav/caldav"

	"github.com/hejmo-home/hejmo/internal/auth"
	hcal "github.com/hejmo-home/hejmo/internal/caldav"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The box as CalDAV server (ADR 0012): personal devices subscribe to the family
// calendar and to every calendar shared through the box, and write events
// back. Addresses:
//
//	/dav/                              root, names the signed-in person's principal
//	/dav/{person}/                     principal
//	/dav/{person}/calendars/           calendar home set
//	/dav/{person}/calendars/{id}/      one calendar
//	/dav/{person}/calendars/{id}/{event}.ics   one event with its series
const davPrefix = "/dav"

// davSession is who a CalDAV request came from, handed to the backend
// through the request context.
type davSession struct {
	person store.Person
	house  store.Household
	req    *http.Request
	scopes string // the token's scopes: which lists the person may see
}

type davKey struct{}

func (s *server) routeCalDAV(mux *http.ServeMux) {
	backend := &davBackend{s: s}
	handler := &webcal.Handler{Backend: backend, Prefix: davPrefix}
	mux.Handle(davPrefix+"/", s.davAuth(handler))
	redirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, davPrefix+"/", http.StatusPermanentRedirect)
	}
	mux.HandleFunc(davPrefix, redirect)
	mux.HandleFunc("/.well-known/caldav", redirect)
}

func davChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Hejmo", charset="UTF-8"`)
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}

// davResource says which resource an address belongs to: the task lists below
// the calendar home are tasks, any other calendar is calendars, and the root,
// the principal and the home set itself ("") belong to both.
func davResource(p string) string {
	_, rest, found := strings.Cut(p, "/calendars/")
	if !found {
		return ""
	}
	first, _, _ := strings.Cut(rest, "/")
	switch {
	case first == "":
		return ""
	case strings.HasPrefix(first, taskListPrefix):
		return "tasks"
	}
	return "calendars"
}

// davAction is what a CalDAV request does: read, write or delete.
func davAction(method string) auth.Action {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, "PROPFIND", "REPORT":
		return auth.Read
	case http.MethodDelete:
		return auth.Delete
	}
	return auth.Write
}

// scopedTo reports whether a token carries a scope of this resource: ADR 0012
// opens CalDAV to calendars scopes only, ADR 0018 task lists to tasks scopes
// only, not to plain read or write tokens.
func scopedTo(scopes, resource string) bool {
	for _, sc := range strings.Fields(scopes) {
		if res, _, _ := strings.Cut(sc, ":"); res == resource {
			return true
		}
	}
	return false
}

// canRead says whether a token's scopes cover reading a resource.
func canRead(scopes, resource string) bool {
	return scopedTo(scopes, resource) && auth.ScopesAllow(scopes, auth.Perm(resource, auth.Read))
}

// davAllows decides whether a token may make a request. Addresses that belong
// to both resources are open for reading to a token of either; changing them
// is calendar business.
func davAllows(scopes, method, path string) (bool, auth.Permission) {
	action := davAction(method)
	res := davResource(path)
	if res == "" {
		if action == auth.Read {
			for _, r := range []string{"calendars", "tasks"} {
				if canRead(scopes, r) {
					return true, auth.Perm(r, auth.Read)
				}
			}
			return false, auth.Perm("calendars", auth.Read)
		}
		res = "calendars"
	}
	perm := auth.Perm(res, action)
	return scopedTo(scopes, res) && auth.ScopesAllow(scopes, perm), perm
}

// davAuth signs a personal device in with HTTP Basic: the person's username and a device
// token with token scope calendars as the password. Browser sessions do not
// count, so CalDAV is not open to cross-site requests.
func (s *server) davAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			http.Error(w, "CalDAV is served over HTTPS only", http.StatusForbidden)
			return
		}
		user, secret, ok := r.BasicAuth()
		if !ok || !s.db.Configured() {
			davChallenge(w)
			return
		}
		acc, err := s.db.AccountByUsername(user)
		if err != nil {
			davChallenge(w)
			return
		}
		token, err := s.db.APITokenByHash(auth.HashToken(secret))
		if err != nil || token.PersonID != acc.PersonID || token.MCP {
			davChallenge(w)
			return
		}
		house, err := s.db.HomeHousehold()
		if err != nil {
			s.fail(w, "household", err)
			return
		}
		person, err := s.db.Person(house.ID, acc.PersonID)
		if err != nil {
			davChallenge(w)
			return
		}
		allowed, perm := davAllows(token.Scopes, r.Method, r.URL.Path)
		// Children have no account; a token held by one is refused all the same.
		if person.Role != store.RoleParent || !auth.RoleAllows(person.Role, perm) || !allowed {
			http.Error(w, "forbidden: needs "+perm.String(), http.StatusForbidden)
			return
		}
		s.touchToken(token)
		// Only the signed-in person's own tree exists.
		if rest, ok := strings.CutPrefix(r.URL.Path, davPrefix+"/"); ok && rest != "" {
			if first, _, _ := strings.Cut(rest, "/"); first != person.ID {
				http.NotFound(w, r)
				return
			}
		}
		sess := &davSession{person: person, house: house, req: r, scopes: token.Scopes}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), davKey{}, sess)))
	})
}

// davBackend serves the box's calendars to go-webdav's CalDAV handler.
type davBackend struct {
	s  *server
	mu sync.Mutex // one CalDAV write at a time; edits through the REST API are not covered
}

func httpError(code int, msg string) error { return webdav.NewHTTPError(code, errors.New(msg)) }

func (b *davBackend) session(ctx context.Context) (*davSession, error) {
	sess, ok := ctx.Value(davKey{}).(*davSession)
	if !ok {
		return nil, httpError(http.StatusUnauthorized, "not signed in")
	}
	return sess, nil
}

func (b *davBackend) principalPath(sess *davSession) string {
	return davPrefix + "/" + sess.person.ID + "/"
}

func (b *davBackend) homePath(sess *davSession) string { return b.principalPath(sess) + "calendars/" }

func (b *davBackend) calendarPath(sess *davSession, c store.Calendar) string {
	return b.homePath(sess) + c.ID + "/"
}

func (b *davBackend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return "", err
	}
	return b.principalPath(sess), nil
}

func (b *davBackend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return "", err
	}
	return b.homePath(sess), nil
}

func (b *davBackend) CreateCalendar(context.Context, *webcal.Calendar) error {
	return httpError(http.StatusForbidden, "calendars are managed in the box's web interface")
}

// served lists the calendars personal devices may see: the family calendar always and
// every calendar shared through the box.
func (b *davBackend) served(sess *davSession) ([]store.Calendar, error) {
	// The family calendar is created on first use, so it is always there to serve.
	if err := b.s.db.EnsureFamilyCalendar(sess.house.ID); err != nil {
		return nil, err
	}
	all, err := b.s.db.Calendars(sess.house.ID)
	if err != nil {
		return nil, err
	}
	var out []store.Calendar
	for _, c := range all {
		if c.Shared {
			out = append(out, c)
		}
	}
	return out, nil
}

func (b *davBackend) toCalendar(sess *davSession, c store.Calendar) webcal.Calendar {
	return webcal.Calendar{Path: b.calendarPath(sess, c), Name: c.Name, SupportedComponentSet: []string{ical.CompEvent}}
}

func (b *davBackend) ListCalendars(ctx context.Context) ([]webcal.Calendar, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return nil, err
	}
	var out []webcal.Calendar
	if canRead(sess.scopes, "calendars") {
		cals, err := b.served(sess)
		if err != nil {
			return nil, err
		}
		for _, c := range cals {
			out = append(out, b.toCalendar(sess, c))
		}
	}
	if canRead(sess.scopes, "tasks") {
		lists, err := b.taskLists(sess)
		if err != nil {
			return nil, err
		}
		out = append(out, lists...)
	}
	return out, nil
}

// address is a parsed CalDAV path below the principal.
type address struct {
	calendar string // "" for the principal and the home set
	object   string // event id, "" for a calendar
}

// parsePath reads /dav/{person}/calendars/{calendar}/{event}.ics.
func parsePath(sess *davSession, p string) (address, bool) {
	rest, ok := strings.CutPrefix(p, davPrefix+"/"+sess.person.ID+"/calendars/")
	if !ok {
		return address{}, false
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] != "":
		return address{calendar: parts[0]}, true
	case len(parts) == 2 && !strings.HasSuffix(rest, "/"):
		id, isICS := strings.CutSuffix(parts[1], ".ics")
		return address{calendar: parts[0], object: id}, isICS && id != ""
	}
	return address{}, false
}

// calendarAt resolves a path to a served calendar (404 for unknown or unshared ones).
func (b *davBackend) calendarAt(sess *davSession, p string) (store.Calendar, address, error) {
	addr, ok := parsePath(sess, p)
	if !ok {
		return store.Calendar{}, address{}, httpError(http.StatusNotFound, "no such resource")
	}
	cals, err := b.served(sess)
	if err != nil {
		return store.Calendar{}, address{}, err
	}
	for _, c := range cals {
		if c.ID == addr.calendar {
			return c, addr, nil
		}
	}
	return store.Calendar{}, address{}, httpError(http.StatusNotFound, "no such calendar")
}

func (b *davBackend) GetCalendar(ctx context.Context, p string) (*webcal.Calendar, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return nil, err
	}
	if isTaskPath(p) {
		return b.getTaskList(sess, p)
	}
	c, addr, err := b.calendarAt(sess, p)
	if err != nil {
		return nil, err
	}
	if addr.object != "" {
		return nil, httpError(http.StatusNotFound, "not a calendar")
	}
	out := b.toCalendar(sess, c)
	return &out, nil
}

// object renders an event with its series as a CalDAV resource.
func (b *davBackend) object(sess *davSession, c store.Calendar, o hcal.Object) (webcal.CalendarObject, error) {
	cal, err := o.Calendar()
	if err != nil {
		return webcal.CalendarObject{}, err
	}
	tag, err := hcal.ETag(cal)
	if err != nil {
		return webcal.CalendarObject{}, err
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return webcal.CalendarObject{}, err
	}
	return webcal.CalendarObject{
		Path: b.calendarPath(sess, c) + o.Master.ID + ".ics", ModTime: o.ModTime(),
		ContentLength: int64(buf.Len()), ETag: tag, Data: cal,
	}, nil
}

func householdZone(h store.Household) *time.Location {
	if loc, err := time.LoadLocation(h.Timezone); err == nil {
		return loc
	}
	return time.UTC
}

func (b *davBackend) objects(sess *davSession, c store.Calendar) ([]webcal.CalendarObject, error) {
	events, err := b.s.db.CalendarEvents(sess.house.ID, c.ID)
	if err != nil {
		return nil, err
	}
	var out []webcal.CalendarObject
	for _, o := range hcal.Group(events) {
		co, err := b.object(sess, c, o)
		if err != nil {
			return nil, err
		}
		out = append(out, co)
	}
	return out, nil
}

// errNameTaken says an event id belongs to something else than the calendar
// or kind of event the address names.
var errNameTaken = errors.New("the name is taken by another event")

// stored loads the object with this id from calendar c: nil if there is none,
// errNameTaken if the id belongs to another calendar or is a changed occurrence.
func (b *davBackend) stored(sess *davSession, c store.Calendar, id string) (*hcal.Object, error) {
	series, err := b.s.db.Series(sess.house.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if series[0].ID != id || series[0].CalendarID != c.ID {
		return nil, errNameTaken
	}
	return &hcal.Object{UID: id, Master: series[0], Overrides: series[1:]}, nil
}

func (b *davBackend) GetCalendarObject(ctx context.Context, p string, _ *webcal.CalendarCompRequest) (*webcal.CalendarObject, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return nil, err
	}
	if isTaskPath(p) {
		return b.getTodo(sess, p)
	}
	c, addr, err := b.calendarAt(sess, p)
	if err != nil {
		return nil, err
	}
	if addr.object == "" {
		return nil, httpError(http.StatusNotFound, "not an event")
	}
	o, err := b.stored(sess, c, addr.object)
	if errors.Is(err, errNameTaken) || o == nil && err == nil {
		return nil, httpError(http.StatusNotFound, "no such event")
	}
	if err != nil {
		return nil, err
	}
	co, err := b.object(sess, c, *o)
	return &co, err
}

func (b *davBackend) ListCalendarObjects(ctx context.Context, p string, _ *webcal.CalendarCompRequest) ([]webcal.CalendarObject, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return nil, err
	}
	if isTaskPath(p) {
		return b.listTodos(sess, p)
	}
	c, _, err := b.calendarAt(sess, p)
	if err != nil {
		return nil, err
	}
	return b.objects(sess, c)
}

func (b *davBackend) QueryCalendarObjects(ctx context.Context, p string, q *webcal.CalendarQuery) ([]webcal.CalendarObject, error) {
	all, err := b.ListCalendarObjects(ctx, p, &q.CompRequest)
	if err != nil {
		return nil, err
	}
	return webcal.Filter(q, all)
}

// validName limits event names to what is safe in an address and an id.
var validName = regexp.MustCompile(`^[A-Za-z0-9._@=+-]{1,128}$`)

func (b *davBackend) PutCalendarObject(ctx context.Context, p string, cal *ical.Calendar, opts *webcal.PutCalendarObjectOptions) (*webcal.CalendarObject, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return nil, err
	}
	if isTaskPath(p) {
		return b.putTodo(sess, p, cal, opts)
	}
	c, addr, err := b.calendarAt(sess, p)
	if err != nil {
		return nil, err
	}
	if addr.object == "" || !validName.MatchString(addr.object) || strings.Trim(addr.object, ".") == "" {
		return nil, httpError(http.StatusBadRequest, "event names may use letters, digits and . _ @ = + -")
	}
	if c.ReadOnly {
		return nil, httpError(http.StatusForbidden, "this calendar is read-only")
	}
	parsed, err := hcal.Parse(cal, householdZone(sess.house))
	if err != nil {
		return nil, httpError(http.StatusBadRequest, err.Error())
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	existing, err := b.stored(sess, c, addr.object)
	if errors.Is(err, errNameTaken) {
		return nil, httpError(http.StatusConflict, err.Error())
	}
	if err != nil {
		return nil, err
	}
	if err := checkPreconditions(existing, opts.IfMatch, opts.IfNoneMatch); err != nil {
		return nil, err
	}
	ch := changeFor(c.ID, addr.object, existing, parsed)
	if err := b.s.db.ApplyChange(sess.house.ID, ch); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			return nil, httpError(http.StatusBadRequest, err.Error())
		}
		return nil, err
	}
	b.s.sync.PushSoon(sess.house.ID, c.ID)
	saved, err := b.stored(sess, c, addr.object)
	if err != nil || saved == nil {
		return nil, fmt.Errorf("reload saved event: %v", err)
	}
	co, err := b.object(sess, c, *saved)
	return &co, err
}

// checkPreconditions applies If-Match and If-None-Match against the stored event.
func checkPreconditions(existing *hcal.Object, ifMatch, ifNoneMatch webdav.ConditionalMatch) error {
	if existing == nil {
		return checkETag("", false, ifMatch, ifNoneMatch)
	}
	cal, err := existing.Calendar()
	if err != nil {
		return err
	}
	have, err := hcal.ETag(cal)
	if err != nil {
		return err
	}
	return checkETag(have, true, ifMatch, ifNoneMatch)
}

// checkETag applies If-Match and If-None-Match against the ETag of what is
// stored (exists says whether anything is).
func checkETag(have string, exists bool, ifMatch, ifNoneMatch webdav.ConditionalMatch) error {
	failed := httpError(http.StatusPreconditionFailed, "the resource changed in the meantime")
	if ifNoneMatch.IsSet() && exists {
		return failed
	}
	if !ifMatch.IsSet() {
		return nil
	}
	if !exists {
		return failed
	}
	if ifMatch.IsWildcard() {
		return nil
	}
	want, err := ifMatch.ETag()
	if err != nil {
		return httpError(http.StatusBadRequest, "unreadable If-Match")
	}
	if have != want {
		return failed
	}
	return nil
}

// changeFor turns the object a personal device saved into the edit that makes the box
// equal to it: the series' audience is kept (personal devices do not show it), changed
// occurrences are matched by their original start.
func changeFor(calendarID, id string, existing *hcal.Object, in hcal.Object) calendar.Change {
	master := in.Master
	master.ID, master.CalendarID, master.SeriesID = id, calendarID, ""
	if existing != nil {
		master.People, master.Groups = existing.Master.People, existing.Master.Groups
	}
	var ch calendar.Change
	add := func(e calendar.Event, known bool) {
		if known {
			ch.Update = append(ch.Update, e)
		} else {
			ch.Create = append(ch.Create, e)
		}
	}
	add(master, existing != nil)

	oldByStart := map[int64]calendar.Event{}
	if existing != nil {
		for _, o := range existing.Overrides {
			oldByStart[o.RecurrenceID.Unix()] = o
		}
	}
	if master.RRule != "" {
		for _, o := range in.Overrides {
			o.CalendarID, o.SeriesID = calendarID, id
			old, known := oldByStart[o.RecurrenceID.Unix()]
			if known {
				o.ID, o.People, o.Groups = old.ID, old.People, old.Groups
				delete(oldByStart, o.RecurrenceID.Unix())
			} else {
				o.ID, o.People, o.Groups = store.NewID(), master.People, master.Groups
			}
			add(o, known)
		}
	}
	for _, gone := range oldByStart {
		ch.Delete = append(ch.Delete, gone.ID)
	}
	return ch
}

func (b *davBackend) DeleteCalendarObject(ctx context.Context, p string) error {
	sess, err := b.session(ctx)
	if err != nil {
		return err
	}
	if isTaskPath(p) {
		return b.deleteTodo(sess, p)
	}
	c, addr, err := b.calendarAt(sess, p)
	if err != nil {
		return err
	}
	if addr.object == "" {
		return httpError(http.StatusForbidden, "calendars are managed in the box's web interface")
	}
	if c.ReadOnly {
		return httpError(http.StatusForbidden, "this calendar is read-only")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	existing, err := b.stored(sess, c, addr.object)
	if errors.Is(err, errNameTaken) || existing == nil && err == nil {
		return httpError(http.StatusNotFound, "no such event")
	}
	if err != nil {
		return err
	}
	if m := webdav.ConditionalMatch(sess.req.Header.Get("If-Match")); m.IsSet() {
		if err := checkPreconditions(existing, m, ""); err != nil {
			return err
		}
	}
	if err := b.s.db.ApplyChange(sess.house.ID, calendar.Change{Delete: []string{existing.Master.ID}}); err != nil {
		return err
	}
	b.s.sync.PushSoon(sess.house.ID, c.ID)
	return nil
}
