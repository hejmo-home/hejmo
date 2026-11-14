package caldav_test

import (
	"context"
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
