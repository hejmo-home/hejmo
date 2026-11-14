package caldavclient_test

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
)

// Real iCloud calendars hold objects that break go-ical's encoder: old events
// with two STATUS lines, invitations with several METHOD lines. The client
// must hand the server's text out as it is, not decode and re-encode it.
const (
	icsNormal = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\nUID:normal@test\r\nDTSTAMP:20261114T100000Z\r\nDTSTART:20261120T150000Z\r\nDTEND:20261120T160000Z\r\nSUMMARY:Swimming\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	icsTwoStatus = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\nUID:twostatus@test\r\nDTSTAMP:20261114T100000Z\r\nDTSTART:20261121T150000Z\r\nDTEND:20261121T160000Z\r\nSTATUS:CONFIRMED\r\nSTATUS:CONFIRMED\r\nSUMMARY:Piano & \"scales\" <b>\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

	icsThreeMethod = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:PUBLISH\r\nMETHOD:PUBLISH\r\nMETHOD:PUBLISH\r\nPRODID:-//Test//EN\r\nBEGIN:VEVENT\r\nUID:threemethod@test\r\nDTSTAMP:20261114T100000Z\r\nDTSTART:20261122T150000Z\r\nDTEND:20261122T160000Z\r\nSUMMARY:Party\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
)

// multigetServer answers calendar-multiget REPORTs on /cal/ the way iCloud
// does: calendar-data is the stored text, escaped as XML text. objects maps a
// file name to its text; a name in gone answers 404 for that member.
func multigetServer(t *testing.T, objects map[string]string, extra string, reports *atomic.Int32) *httptest.Server {
	t.Helper()
	hrefRe := regexp.MustCompile(`<(?:\w+:)?href[^>]*>([^<]*)</`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet { // the per-object fallback
			if text, ok := objects[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]; ok {
				io.WriteString(w, text)
				return
			}
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
			return
		}
		if r.Method != "REPORT" || r.URL.Path != "/cal/" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusMethodNotAllowed)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "calendar-multiget") {
			http.Error(w, "not a multiget", http.StatusBadRequest)
			return
		}
		if reports != nil {
			reports.Add(1)
		}
		var out strings.Builder
		out.WriteString(multistatusOpen)
		for _, m := range hrefRe.FindAllStringSubmatch(string(b), -1) {
			name := m[1][strings.LastIndex(m[1], "/")+1:]
			text, ok := objects[name]
			if !ok {
				out.WriteString("<d:response><d:href>" + m[1] + "</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response>")
				continue
			}
			var esc strings.Builder
			xml.EscapeText(&esc, []byte(text))
			out.WriteString(member(m[1], fmt.Sprintf(`<d:propstat><d:prop><d:getetag>"e-%s"</d:getetag><c:calendar-data>%s</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>`, name, esc.String())+noResourcetype))
		}
		out.WriteString(extra)
		out.WriteString("</d:multistatus>")
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, out.String())
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fetchByName(t *testing.T, srvURL string, names ...string) (map[string]caldavclient.Object, error) {
	t.Helper()
	c, err := caldavclient.NewClient(srvURL, "u", "p", nil)
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, n := range names {
		urls = append(urls, srvURL+"/cal/"+n)
	}
	got, err := c.Fetch(context.Background(), srvURL+"/cal/", urls)
	byName := map[string]caldavclient.Object{}
	for _, o := range got {
		byName[o.URL[strings.LastIndex(o.URL, "/")+1:]] = o
	}
	return byName, err
}

func TestFetchHandsOutObjectsWithRepeatedPropertiesAsTheServerSentThem(t *testing.T) {
	srv := multigetServer(t, map[string]string{"normal.ics": icsNormal, "twostatus.ics": icsTwoStatus, "threemethod.ics": icsThreeMethod}, "", nil)
	got, err := fetchByName(t, srv.URL, "normal.ics", "twostatus.ics", "threemethod.ics")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"normal.ics": icsNormal, "twostatus.ics": icsTwoStatus, "threemethod.ics": icsThreeMethod}
	if len(got) != 3 {
		t.Fatalf("Fetch() = %+v, want 3 objects", got)
	}
	for name, text := range want {
		o := got[name]
		if o.Err != nil {
			t.Errorf("%s: Err = %v, want it readable", name, o.Err)
		}
		if string(o.Data) != text {
			t.Errorf("%s: Data = %q, want the server's text %q", name, o.Data, text)
		}
		if o.ETag != "e-"+name {
			t.Errorf("%s: ETag = %q", name, o.ETag)
		}
	}
}

func TestFetchStillReportsTextThatIsNoCalendar(t *testing.T) {
	srv := multigetServer(t, map[string]string{"normal.ics": icsNormal, "bad.ics": "this is not a calendar"}, "", nil)
	got, err := fetchByName(t, srv.URL, "normal.ics", "bad.ics")
	if err != nil {
		t.Fatal(err)
	}
	if got["normal.ics"].Err != nil || got["bad.ics"].Err == nil {
		t.Fatalf("Fetch() = %+v, want the bad object with an Err and the good one without", got)
	}
}

func TestFetchLeavesOutObjectsAnsweredWith404Or410(t *testing.T) {
	srv := multigetServer(t, map[string]string{"normal.ics": icsNormal}, "", nil)
	got, err := fetchByName(t, srv.URL, "normal.ics", "deleted.ics")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["normal.ics"].Err != nil {
		t.Fatalf("Fetch() = %+v, want only the existing object", got)
	}

	extra := "<d:response><d:href>/cal/g.ics</d:href><d:status>HTTP/1.1 410 Gone</d:status></d:response>" +
		"<d:response><d:href>/cal/h.ics</d:href><d:propstat><d:prop><c:calendar-data/></d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat></d:response>"
	srv2 := multigetServer(t, map[string]string{"normal.ics": icsNormal}, extra, nil)
	if got, err = fetchByName(t, srv2.URL, "normal.ics"); err != nil || len(got) != 1 {
		t.Fatalf("Fetch() = %+v, %v; want 410 and a 404 propstat left out", got, err)
	}
}

func TestFetchRefusesAnObjectOnAForeignHost(t *testing.T) {
	extra := member("http://evil.example.net/cal/x.ics", `<d:propstat><d:prop><d:getetag>"x"</d:getetag><c:calendar-data>`+icsNormal+`</c:calendar-data></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>`)
	srv := multigetServer(t, map[string]string{"normal.ics": icsNormal}, extra, nil)
	_, err := fetchByName(t, srv.URL, "normal.ics")
	var foreign *caldavclient.ForeignHostError
	if !errors.As(err, &foreign) {
		t.Fatalf("Fetch() = %v, want a ForeignHostError", err)
	}
}

func TestFetchFailsOnAnotherRefusalInsideTheAnswer(t *testing.T) {
	// The member answers 503 inside the multistatus; the per-object fallback
	// then meets the same refusal on its own download.
	extra := "<d:response><d:href>/cal/x.ics</d:href><d:status>HTTP/1.1 503 Service Unavailable</d:status></d:response>"
	srv := multigetServer(t, map[string]string{"normal.ics": icsNormal}, extra, nil)
	_, err := fetchByName(t, srv.URL, "normal.ics", "x.ics")
	var se *caldavclient.StatusError
	if !errors.As(err, &se) || se.Code != http.StatusServiceUnavailable {
		t.Fatalf("Fetch() = %v, want a StatusError 503", err)
	}
}

func TestFetchBatchesStayAtOneHundredObjects(t *testing.T) {
	objects := map[string]string{}
	var names []string
	for i := range 230 {
		n := fmt.Sprintf("e%03d.ics", i)
		objects[n] = icsNormal
		names = append(names, n)
	}
	var reports atomic.Int32
	srv := multigetServer(t, objects, "", &reports)
	got, err := fetchByName(t, srv.URL, names...)
	if err != nil || len(got) != 230 {
		t.Fatalf("Fetch() = %d objects, %v", len(got), err)
	}
	if n := reports.Load(); n != 3 {
		t.Fatalf("multiget requests = %d, want 3", n)
	}
}

// When the multiget itself fails, each object is downloaded on its own.
func TestFetchFallsBackToOneRequestPerObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "REPORT" {
			http.Error(w, "broken", http.StatusInternalServerError)
			return
		}
		io.WriteString(w, map[string]string{"/cal/a.ics": icsTwoStatus, "/cal/b.ics": icsThreeMethod}[r.URL.Path])
	}))
	t.Cleanup(srv.Close)
	got, err := fetchByName(t, srv.URL, "a.ics", "b.ics")
	if err != nil || len(got) != 2 || got["a.ics"].Err != nil || got["b.ics"].Err != nil || string(got["b.ics"].Data) != icsThreeMethod {
		t.Fatalf("Fetch() = %+v, %v", got, err)
	}
}
