package calsync_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/caldavclient/caldavtest"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/store"
)

// fakeServer is a CalDAV server as the sync sees it, scripted: objects with
// ETags and data, a CTag and a sync token that move with every change, and a
// log of what the sync asked for. It does not need Docker.
type fakeServer struct {
	objects  map[string]fakeObject // by address
	version  int
	history  []fakeChange // by version, for sync-collection
	ctagOn   bool
	tokenOn  bool
	syncOn   bool // answers sync-collection
	asked    []string
	fetched  []string
	stateErr error
	pageSize int      // sync-collection answers this many changes at a time (0: all)
	hidden   []string // addresses the multiget leaves out of its answer
}

type fakeObject struct{ etag, data string }

type fakeChange struct {
	version int
	url     string
	gone    bool
}

func newFakeServer() *fakeServer {
	return &fakeServer{objects: map[string]fakeObject{}, ctagOn: true, tokenOn: true, syncOn: true}
}

func (f *fakeServer) put(url, uid, title string) {
	f.version++
	f.objects[url] = fakeObject{etag: "v" + string(rune('a'+f.version)), data: caldavtest.Event(uid, title, "20261120T150000", "20261120T160000")}
	f.history = append(f.history, fakeChange{version: f.version, url: url})
}

func (f *fakeServer) remove(url string) {
	f.version++
	delete(f.objects, url)
	f.history = append(f.history, fakeChange{version: f.version, url: url, gone: true})
}

func (f *fakeServer) count(what string) int {
	n := 0
	for _, a := range f.asked {
		if a == what {
			n++
		}
	}
	return n
}

func (f *fakeServer) stamp() string { return string(rune('0' + f.version)) }

type fakeRemote struct{ f *fakeServer }

func (r fakeRemote) Calendars(context.Context) ([]caldavclient.Calendar, error) { return nil, nil }
func (r fakeRemote) Calendar(context.Context, string) (caldavclient.Calendar, error) {
	return caldavclient.Calendar{}, nil
}
func (r fakeRemote) Put(context.Context, string, string, string) (string, error) {
	return "", errors.New("fake server: read-only in these tests")
}
func (r fakeRemote) Delete(context.Context, string, string) error {
	return errors.New("fake server: read-only in these tests")
}
func (r fakeRemote) CountEvents(context.Context, string, time.Time) (int, error) { return 0, nil }

func (r fakeRemote) State(context.Context, string) (caldavclient.State, error) {
	r.f.asked = append(r.f.asked, "state")
	var st caldavclient.State
	if r.f.ctagOn {
		st.CTag = "ctag-" + r.f.stamp()
	}
	if r.f.tokenOn {
		st.SyncToken = "token-" + r.f.stamp()
	}
	return st, r.f.stateErr
}

func (r fakeRemote) List(context.Context, string) ([]caldavclient.Object, error) {
	r.f.asked = append(r.f.asked, "list")
	var out []caldavclient.Object
	for u, o := range r.f.objects {
		out = append(out, caldavclient.Object{URL: u, ETag: o.etag})
	}
	return out, nil
}

func (r fakeRemote) Changes(_ context.Context, _ string, token string) (caldavclient.Changes, error) {
	r.f.asked = append(r.f.asked, "changes")
	if !r.f.syncOn || !strings.HasPrefix(token, "token-") {
		return caldavclient.Changes{}, caldavclient.ErrSyncUnavailable
	}
	since := int(token[len("token-")] - '0')
	out := caldavclient.Changes{Token: "token-" + r.f.stamp()}
	latest := map[string]fakeChange{}
	for _, c := range r.f.history {
		if c.version > since {
			latest[c.url] = c
		}
	}
	var pending []fakeChange
	for _, c := range latest {
		pending = append(pending, c)
	}
	slices.SortFunc(pending, func(a, b fakeChange) int { return a.version - b.version })
	if r.f.pageSize > 0 && len(pending) > r.f.pageSize {
		pending = pending[:r.f.pageSize]
		out.Token = "token-" + string(rune('0'+pending[len(pending)-1].version))
		out.Truncated = true
	}
	for _, c := range pending {
		if c.gone {
			out.Deleted = append(out.Deleted, c.url)
		} else {
			out.Changed = append(out.Changed, caldavclient.Object{URL: c.url, ETag: r.f.objects[c.url].etag})
		}
	}
	return out, nil
}

func (r fakeRemote) Fetch(_ context.Context, _ string, urls []string) ([]caldavclient.Object, error) {
	r.f.asked = append(r.f.asked, "fetch")
	r.f.fetched = append(r.f.fetched, urls...)
	var out []caldavclient.Object
	for _, u := range urls {
		if o, ok := r.f.objects[u]; ok && !slices.Contains(r.f.hidden, u) {
			out = append(out, caldavclient.Object{URL: u, ETag: o.etag, Data: []byte(o.data)})
		}
	}
	return out, nil
}

type fakeFixture struct {
	*fakeServer
	db    *store.DB
	h     store.Household
	cal   store.SyncedCalendar
	svc   *calsync.Service
	clock time.Time
}

func newFakeFixture(t *testing.T) *fakeFixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetSecrets(box)
	h, err := db.CreateHousehold(store.Household{Name: "Home", Region: "DE-BY", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	fx := &fakeFixture{fakeServer: newFakeServer(), db: db, h: h, clock: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	conn, err := db.CreateCalDAVConnection(h.ID, store.NewCalDAVConnection{URL: "https://cal.example.org/", Username: "mama", Password: "pw", Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	fx.cal, err = db.AddCalDAVCalendar(h.ID, conn.ID, store.NewCalDAVCalendar{Name: "Family", RemoteURL: "https://cal.example.org/family/", IntervalMinutes: 5, Now: fx.clock})
	if err != nil {
		t.Fatal(err)
	}
	fx.svc = calsync.New(db, nil, func() time.Time { return fx.clock })
	fx.svc.SetDialer(func(string, string, string) (calsync.Remote, error) { return fakeRemote{fx.fakeServer}, nil })
	return fx
}

func (fx *fakeFixture) sync(t *testing.T) {
	t.Helper()
	fx.asked, fx.fetched = nil, nil
	fx.clock = fx.clock.Add(5 * time.Minute)
	if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err != nil {
		t.Fatal(err)
	}
}

func (fx *fakeFixture) titles(t *testing.T) []string {
	t.Helper()
	evs, err := fx.db.EventsBetween(fx.h.ID, fx.clock.AddDate(-1, 0, 0), fx.clock.AddDate(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, e.Title)
	}
	slices.Sort(out)
	return out
}

const (
	swim  = "https://cal.example.org/family/swim.ics"
	piano = "https://cal.example.org/family/piano.ics"
)

func TestFirstRunListsETagsAndRemembersTheServersState(t *testing.T) {
	fx := newFakeFixture(t)
	fx.put(swim, "swim@test", "Swimming")
	fx.put(piano, "piano@test", "Piano")
	fx.sync(t)
	if got := fx.titles(t); !slices.Equal(got, []string{"Piano", "Swimming"}) {
		t.Fatalf("events = %v", got)
	}
	if fx.count("list") != 1 || fx.count("changes") != 0 {
		t.Fatalf("asked = %v, want one listing and no sync-collection on the first run", fx.asked)
	}
	if st, _ := fx.db.RemoteState(fx.cal.ID); st.CTag != "ctag-2" || st.Token != "token-2" {
		t.Fatalf("state = %+v, want the CTag and token from before the listing", st)
	}
}

// Nothing changed: the CTag is the same, so the server is asked nothing more.
func TestUnchangedCTagEndsTheRunWithoutListing(t *testing.T) {
	fx := newFakeFixture(t)
	fx.put(swim, "swim@test", "Swimming")
	fx.sync(t)
	fx.sync(t)
	if fx.count("list") != 0 || fx.count("changes") != 0 || fx.count("fetch") != 0 {
		t.Fatalf("asked = %v, want only the state", fx.asked)
	}
	if got := fx.titles(t); !slices.Equal(got, []string{"Swimming"}) {
		t.Fatalf("events = %v, want them kept", got)
	}
	cal, _ := fx.db.SyncedCalendar(fx.h.ID, fx.cal.ID)
	if !cal.Sync.LastSuccess.Equal(fx.clock) {
		t.Fatalf("LastSuccess = %v, want the run to count as a success at %v", cal.Sync.LastSuccess, fx.clock)
	}
}

func TestChangedCTagDownloadsOnlyWhatSyncCollectionReports(t *testing.T) {
	fx := newFakeFixture(t)
	fx.put(swim, "swim@test", "Swimming")
	fx.put(piano, "piano@test", "Piano")
	fx.sync(t)

	fx.put(swim, "swim@test", "Swimming lesson")
	fx.remove(piano)
	fx.put("https://cal.example.org/family/new.ics", "new@test", "Dentist")
	fx.sync(t)

	if fx.count("list") != 0 || fx.count("changes") != 1 {
		t.Fatalf("asked = %v, want sync-collection and no listing", fx.asked)
	}
	slices.Sort(fx.fetched)
	if want := []string{"https://cal.example.org/family/new.ics", swim}; !slices.Equal(fx.fetched, want) {
		t.Fatalf("downloaded = %v, want %v", fx.fetched, want)
	}
	if got := fx.titles(t); !slices.Equal(got, []string{"Dentist", "Swimming lesson"}) {
		t.Fatalf("events = %v", got)
	}
	if st, _ := fx.db.RemoteState(fx.cal.ID); st.CTag != "ctag-5" || st.Token != "token-5" {
		t.Fatalf("state = %+v, want the new CTag and token", st)
	}
	// And the new token carries on.
	fx.put(swim, "swim@test", "Swimming gala")
	fx.sync(t)
	if fx.count("changes") != 1 || len(fx.fetched) != 1 {
		t.Fatalf("asked = %v, downloaded = %v, want one sync-collection and one object", fx.asked, fx.fetched)
	}
}

// A server that has no sync-collection, or forgot the token, is listed.
func TestSyncCollectionRefusedFallsBackToTheETagListing(t *testing.T) {
	fx := newFakeFixture(t)
	fx.put(swim, "swim@test", "Swimming")
	fx.put(piano, "piano@test", "Piano")
	fx.sync(t)

	fx.syncOn = false
	fx.put(swim, "swim@test", "Swimming lesson")
	fx.remove(piano)
	fx.sync(t)
	if fx.count("changes") != 1 || fx.count("list") != 1 {
		t.Fatalf("asked = %v, want sync-collection tried, then the listing", fx.asked)
	}
	if got := fx.titles(t); !slices.Equal(got, []string{"Swimming lesson"}) {
		t.Fatalf("events = %v", got)
	}
	if st, _ := fx.db.RemoteState(fx.cal.ID); st.Token != "token-4" {
		t.Fatalf("state = %+v, want the token from before the listing", st)
	}
}

// A server with neither CTag nor token is listed on every run, as before.
func TestServerWithoutCTagOrTokenIsListedEveryRun(t *testing.T) {
	fx := newFakeFixture(t)
	fx.ctagOn, fx.tokenOn = false, false
	fx.put(swim, "swim@test", "Swimming")
	fx.sync(t)
	fx.sync(t)
	if fx.count("list") != 1 || fx.count("changes") != 0 || len(fx.fetched) != 0 {
		t.Fatalf("asked = %v, downloaded = %v, want a listing and nothing downloaded", fx.asked, fx.fetched)
	}
	fx.put(swim, "swim@test", "Swimming lesson")
	fx.sync(t)
	if got := fx.titles(t); !slices.Equal(got, []string{"Swimming lesson"}) {
		t.Fatalf("events = %v", got)
	}
}

// A CTag without a token still saves the listing when nothing changed.
func TestCTagAloneShortCircuitsAndAChangeIsListed(t *testing.T) {
	fx := newFakeFixture(t)
	fx.tokenOn = false
	fx.put(swim, "swim@test", "Swimming")
	fx.sync(t)
	fx.sync(t)
	if fx.count("list") != 0 || fx.count("changes") != 0 {
		t.Fatalf("asked = %v, want only the state", fx.asked)
	}
	fx.put(swim, "swim@test", "Swimming lesson")
	fx.sync(t)
	if fx.count("list") != 1 || fx.count("changes") != 0 {
		t.Fatalf("asked = %v, want a listing", fx.asked)
	}
}

// RFC 6578 3.6: a server may answer a sync-collection with only part of the
// changes (507 on the collection) and a token to carry on from. The part that
// came is applied, and the rest must arrive on later runs although the CTag of
// the whole calendar has not moved since.
func TestTruncatedSyncCollectionDeliversTheRestOnLaterRuns(t *testing.T) {
	fx := newFakeFixture(t)
	fx.sync(t)
	fx.put(swim, "swim@test", "Swimming")
	fx.put(piano, "piano@test", "Piano")
	fx.pageSize = 1
	fx.sync(t)
	if got := fx.titles(t); !slices.Equal(got, []string{"Swimming"}) {
		t.Fatalf("events after the partial answer = %v, want the first change", got)
	}
	for i := 0; i < 3; i++ {
		fx.sync(t)
	}
	if got := fx.titles(t); !slices.Equal(got, []string{"Piano", "Swimming"}) {
		t.Fatalf("events = %v, want the second change to arrive", got)
	}
}

// A State that cannot be read for a reason other than the login or a foreign
// host does not stop the sync: the ETags are listed and no state is kept.
func TestStateFailureFallsBackToTheETagListing(t *testing.T) {
	fx := newFakeFixture(t)
	fx.stateErr = errors.New("PROPFIND: connection reset")
	fx.put(swim, "swim@test", "Swimming")
	fx.sync(t)
	if fx.count("list") != 1 || fx.count("changes") != 0 {
		t.Fatalf("asked = %v, want the listing", fx.asked)
	}
	if got := fx.titles(t); !slices.Equal(got, []string{"Swimming"}) {
		t.Fatalf("events = %v", got)
	}
	if st, _ := fx.db.RemoteState(fx.cal.ID); st != (store.RemoteState{}) {
		t.Fatalf("state = %+v, want none stored", st)
	}
	fx.sync(t)
	if fx.count("list") != 1 || len(fx.fetched) != 0 {
		t.Fatalf("asked = %v, downloaded = %v, want a listing and nothing downloaded", fx.asked, fx.fetched)
	}
}

// A refused login or a foreign host found while reading the state fails the
// run at once, with the same log entry as anywhere else in the sync.
func TestStateFailureOfLoginOrForeignHostFailsTheRun(t *testing.T) {
	cases := []struct {
		name string
		err  error
		key  string
	}{
		{"unauthorized", &caldavclient.StatusError{Code: 401, Status: "401 Unauthorized"}, "sync.fail.login"},
		{"foreign host", refusal("https://cal.example.org", "https://evil.test/cal/"), "sync.fail.foreign_host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFakeFixture(t)
			fx.put(swim, "swim@test", "Swimming")
			fx.stateErr = tc.err
			fx.asked = nil
			fx.clock = fx.clock.Add(5 * time.Minute)
			if err := fx.svc.Sync(context.Background(), fx.h.ID, fx.cal.ID); err == nil {
				t.Fatal("sync succeeded")
			}
			if fx.count("list") != 0 || fx.count("changes") != 0 {
				t.Fatalf("asked = %v, want nothing after the state", fx.asked)
			}
			log, _ := fx.db.SyncLog(fx.h.ID, 10)
			if len(log) != 1 || log[0].Kind != "failure" || log[0].Message.Key != tc.key {
				t.Fatalf("log = %+v, want a failure %s", log, tc.key)
			}
		})
	}
}

// An object the report names but the multiget leaves out must be asked for
// again: the state of this run is not kept, so the next run lists.
func TestObjectMissingFromTheMultigetIsRetriedOnTheNextRun(t *testing.T) {
	fx := newFakeFixture(t)
	fx.put(swim, "swim@test", "Swimming")
	fx.sync(t)
	fx.put(piano, "piano@test", "Piano")
	fx.hidden = []string{piano}
	fx.sync(t)
	fx.hidden = nil
	fx.sync(t)
	if got := fx.titles(t); !slices.Equal(got, []string{"Piano", "Swimming"}) {
		t.Fatalf("events = %v, want the left-out object to arrive", got)
	}
}
