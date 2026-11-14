package caldav_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldav"
	"github.com/hejmo-home/hejmo/internal/caldav/caldavtest"
)

func client(t *testing.T, srv *caldavtest.Server, password string) *caldav.Client {
	t.Helper()
	c, err := caldav.NewClient(srv.URL, caldavtest.User, password, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDiscoversCalendarsFromTheServerRoot(t *testing.T) {
	srv := caldavtest.Start(t)
	srv.MakeCalendar(t, "family", "Family")
	srv.MakeCalendar(t, "school", "School")

	cals, err := client(t, srv, caldavtest.Password).Calendars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range cals {
		got[c.Name] = c.URL
	}
	if len(got) != 2 || got["Family"] != srv.CalendarURL("family") || got["School"] != srv.CalendarURL("school") {
		t.Fatalf("Calendars() = %+v", cals)
	}
}

func TestWrongPasswordIsReportedAsRefusedLogin(t *testing.T) {
	srv := caldavtest.Start(t)
	_, err := client(t, srv, "wrong").Calendars(context.Background())
	if !caldav.IsUnauthorized(err) {
		t.Fatalf("err = %v, want an unauthorized error", err)
	}
}

func TestSingleCalendarAddressWorksWithoutDiscovery(t *testing.T) {
	srv := caldavtest.Start(t)
	srv.MakeCalendar(t, "family", "Family")

	cal, err := client(t, srv, caldavtest.Password).Calendar(context.Background(), srv.CalendarURL("family"))
	if err != nil {
		t.Fatal(err)
	}
	if cal.URL != srv.CalendarURL("family") || cal.Name != "Family" {
		t.Fatalf("Calendar() = %+v", cal)
	}
}

func TestListsEtagsAndFetchesOnlyWhatIsAsked(t *testing.T) {
	srv := caldavtest.Start(t)
	cal := srv.MakeCalendar(t, "family", "Family")
	a := srv.Put(t, cal, "a.ics", caldavtest.Event("a@test", "Swimming", "20261120T150000", "20261120T160000"))
	srv.Put(t, cal, "b.ics", caldavtest.Event("b@test", "Piano", "20261121T150000", "20261121T160000"))

	c := client(t, srv, caldavtest.Password)
	objs, err := c.List(context.Background(), cal)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 || objs[0].ETag == "" || objs[1].ETag == "" || objs[0].ETag == objs[1].ETag {
		t.Fatalf("List() = %+v", objs)
	}
	got, err := c.Fetch(context.Background(), cal, []string{a})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].URL != a || !strings.Contains(string(got[0].Data), "SUMMARY:Swimming") || got[0].ETag == "" {
		t.Fatalf("Fetch() = %+v", got)
	}
}

func TestAddressKeyIgnoresCaseOfHostTrailingSlashAndEscaping(t *testing.T) {
	a := caldav.AddressKey("https://Cal.Example.org/mama/family/")
	for _, same := range []string{"https://cal.example.org/mama/family", "https://cal.example.org/mama/fam%69ly/"} {
		if got := caldav.AddressKey(same); got != a {
			t.Errorf("AddressKey(%q) = %q, want %q", same, got, a)
		}
	}
	if caldav.AddressKey("https://cal.example.org/mama/school/") == a {
		t.Error("different calendars share a key")
	}
}

func tamperedClient(t *testing.T, srv *caldavtest.Server, broken ...string) (*caldav.Client, *caldavtest.Tamper) {
	t.Helper()
	tp := new(caldavtest.Tamper)
	tp.Break(broken...)
	c, err := caldav.NewClient(srv.URL, caldavtest.User, caldavtest.Password, tp.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c, tp
}

func TestFetchKeepsGoodObjectsWhenOneCannotBeDecoded(t *testing.T) {
	srv := caldavtest.Start(t)
	cal := srv.MakeCalendar(t, "family", "Family")
	good := srv.Put(t, cal, "good.ics", caldavtest.Event("good@test", "Swimming", "20261120T150000", "20261120T160000"))
	bad := srv.Put(t, cal, "bad.ics", caldavtest.Event("bad@test", "Piano", "20261121T150000", "20261121T160000"))

	c, _ := tamperedClient(t, srv, "bad.ics")
	got, err := c.Fetch(context.Background(), cal, []string{good, bad})
	if err != nil {
		t.Fatalf("Fetch() = %v, want the good object despite the bad one", err)
	}
	byURL := map[string]caldav.Object{}
	for _, o := range got {
		byURL[o.URL] = o
	}
	if o := byURL[good]; o.Err != nil || !strings.Contains(string(o.Data), "SUMMARY:Swimming") {
		t.Fatalf("good object = %+v", o)
	}
	if o, ok := byURL[bad]; !ok || o.Err == nil {
		t.Fatalf("bad object = %+v, want it reported with an error", o)
	}
}

func TestFetchSkipsAnObjectDeletedAfterListing(t *testing.T) {
	srv := caldavtest.Start(t)
	cal := srv.MakeCalendar(t, "family", "Family")
	good := srv.Put(t, cal, "good.ics", caldavtest.Event("good@test", "Swimming", "20261120T150000", "20261120T160000"))
	gone := srv.Put(t, cal, "gone.ics", caldavtest.Event("gone@test", "Piano", "20261121T150000", "20261121T160000"))
	srv.Delete(t, gone)

	got, err := client(t, srv, caldavtest.Password).Fetch(context.Background(), cal, []string{good, gone})
	if err != nil {
		t.Fatalf("Fetch() = %v, want the deleted object skipped", err)
	}
	if len(got) != 1 || got[0].URL != good || got[0].Err != nil {
		t.Fatalf("Fetch() = %+v", got)
	}
}

func TestFetchAsksForObjectsInBatches(t *testing.T) {
	srv := caldavtest.Start(t)
	cal := srv.MakeCalendar(t, "family", "Family")
	var urls []string
	for i := range 230 {
		urls = append(urls, srv.Put(t, cal, fmt.Sprintf("e%03d.ics", i), caldavtest.Event(fmt.Sprintf("e%d@test", i), "Event", "20261120T150000", "20261120T160000")))
	}
	c, tp := tamperedClient(t, srv)
	got, err := c.Fetch(context.Background(), cal, urls)
	if err != nil || len(got) != len(urls) {
		t.Fatalf("Fetch() = %d objects, %v", len(got), err)
	}
	if n := tp.Reports.Load(); n != 3 {
		t.Fatalf("multiget requests = %d, want 3 (100 + 100 + 30)", n)
	}
}
