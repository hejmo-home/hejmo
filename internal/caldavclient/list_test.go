package caldavclient_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
)

// iCloud reports properties it does not have for some members of a calendar
// collection in a 404 propstat. These fake servers answer the way it does:
// resourcetype (and more) sits in a 404 propstat for some members.
const (
	multistatusOpen = `<?xml version="1.0" encoding="UTF-8"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`
	okEtag          = `<d:propstat><d:prop><d:getetag>"%s"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>`
	noResourcetype  = `<d:propstat><d:prop><d:resourcetype/></d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat>`
	noEtag          = `<d:propstat><d:prop><d:getetag/></d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat>`
)

func member(href, propstats string) string {
	return "<d:response><d:href>" + href + "</d:href>" + propstats + "</d:response>"
}

// iCloudLikeCalendar answers the listing of /cal/ for both PROPFIND (Depth 1)
// and a calendar-query REPORT. It records the REPORT body.
func iCloudLikeCalendar(t *testing.T, report *string, hrefOverride string) *httptest.Server {
	t.Helper()
	body := multistatusOpen +
		member("/cal/", `<d:propstat><d:prop><d:resourcetype><d:collection/><c:calendar/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>`) +
		member("/cal/a.ics", fmt.Sprintf(okEtag, "ea")+noResourcetype) +
		member("/cal/b%20b.ics", noResourcetype+fmt.Sprintf(okEtag, "eb")) +
		member("/cal/nothing.ics", noResourcetype+noEtag) +
		member("/cal/sub/", `<d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype><d:getetag>"es"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>`)
	if hrefOverride != "" {
		body += member(hrefOverride, fmt.Sprintf(okEtag, "ex"))
	}
	body += "</d:multistatus>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cal/" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "PROPFIND", "REPORT":
			if r.Method == "REPORT" && report != nil {
				b, _ := io.ReadAll(r.Body)
				*report = string(b) + "|Depth=" + r.Header.Get("Depth")
			}
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, body)
		default:
			http.Error(w, "unexpected "+r.Method, http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListToleratesMembersWithAMissingResourcetype(t *testing.T) {
	var report string
	srv := iCloudLikeCalendar(t, &report, "")
	c, err := caldavclient.NewClient(srv.URL, "u", "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	objs, err := c.List(context.Background(), srv.URL+"/cal/")
	if err != nil {
		t.Fatalf("List() = %v, want the .ics objects despite resourcetype in a 404 propstat", err)
	}
	got := map[string]string{}
	for _, o := range objs {
		got[o.URL] = o.ETag
	}
	if len(got) != 2 || got[srv.URL+"/cal/a.ics"] != "ea" || got[srv.URL+"/cal/b%20b.ics"] != "eb" {
		t.Fatalf("List() = %+v, want a.ics and b b.ics with their ETags only", objs)
	}
}

// Only calendar objects come back and the listing needs nothing but getetag.
// No time range is asked for: the import window is applied to the occurrences
// when the objects are read, so a series whose first event is older than the
// window but recurs into it is still listed.
func TestListAsksForCalendarObjectsOnly(t *testing.T) {
	var report string
	srv := iCloudLikeCalendar(t, &report, "")
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	if _, err := c.List(context.Background(), srv.URL+"/cal/"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"calendar-query", "getetag", "VCALENDAR", "VEVENT", "|Depth=1"} {
		if !strings.Contains(report, want) {
			t.Errorf("REPORT = %s, missing %q", report, want)
		}
	}
	for _, not := range []string{"resourcetype", "time-range"} {
		if strings.Contains(report, not) {
			t.Errorf("REPORT = %s, must not contain %q", report, not)
		}
	}
}

func TestListRefusesAnObjectOnAForeignHost(t *testing.T) {
	srv := iCloudLikeCalendar(t, nil, "http://evil.example.net/cal/x.ics")
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	_, err := c.List(context.Background(), srv.URL+"/cal/")
	var foreign *caldavclient.ForeignHostError
	if !errors.As(err, &foreign) {
		t.Fatalf("List() = %v, want a ForeignHostError", err)
	}
}

func TestListReportsAMissingCalendarAsAStatusError(t *testing.T) {
	srv := iCloudLikeCalendar(t, nil, "")
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	_, err := c.List(context.Background(), srv.URL+"/gone/")
	var se *caldavclient.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusNotFound {
		t.Fatalf("List() = %v, want a StatusError 404", err)
	}
}
