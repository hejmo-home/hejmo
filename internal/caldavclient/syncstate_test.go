package caldavclient_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
)

// syncServer answers the way a server with RFC 6578 does: PROPFIND on the
// calendar reports its CTag and sync token, and a sync-collection REPORT
// answers with the members that changed since a token and the ones that are
// gone (a response with a 404 status), and the new token.
func syncServer(t *testing.T, reportStatus int, reportBody *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cal/" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case "PROPFIND":
			multistatus(w, response("/cal/", `<CS:getctag xmlns:CS="http://calendarserver.org/ns/">ct-7</CS:getctag><D:sync-token>tok-7</D:sync-token>`))
		case "REPORT":
			b, _ := io.ReadAll(r.Body)
			if reportBody != nil {
				*reportBody = string(b) + "|Depth=" + r.Header.Get("Depth")
			}
			if reportStatus != http.StatusMultiStatus {
				http.Error(w, "no", reportStatus)
				return
			}
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusMultiStatus)
			io.WriteString(w, `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:">`+
				`<D:response><D:href>/cal/new.ics</D:href><D:propstat><D:prop><D:getetag>"n1"</D:getetag></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`+
				`<D:response><D:href>/cal/b%20b.ics</D:href><D:propstat><D:prop><D:getetag>"b2"</D:getetag></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`+
				`<D:response><D:href>/cal/old.ics</D:href><D:status>HTTP/1.1 404 Not Found</D:status></D:response>`+
				`<D:sync-token>tok-8</D:sync-token></D:multistatus>`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStateReportsCTagAndSyncToken(t *testing.T) {
	srv := syncServer(t, http.StatusMultiStatus, nil)
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	st, err := c.State(context.Background(), srv.URL+"/cal/")
	if err != nil {
		t.Fatal(err)
	}
	if st.CTag != "ct-7" || st.SyncToken != "tok-7" {
		t.Fatalf("State() = %+v, want ct-7 and tok-7", st)
	}
}

// A server without either property is not an error: the sync lists ETags.
func TestStateOfAServerWithoutCTagOrToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		multistatus(w, response("/cal/", `<D:displayname>Family</D:displayname>`))
	}))
	t.Cleanup(srv.Close)
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	st, err := c.State(context.Background(), srv.URL+"/cal/")
	if err != nil || st != (caldavclient.State{}) {
		t.Fatalf("State() = %+v, %v, want empty and no error", st, err)
	}
}

func TestChangesReadsChangedGoneAndTheNewToken(t *testing.T) {
	var report string
	srv := syncServer(t, http.StatusMultiStatus, &report)
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	got, err := c.Changes(context.Background(), srv.URL+"/cal/", "tok-7")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sync-collection", "<d:sync-token>tok-7</d:sync-token>", "<d:sync-level>1</d:sync-level>", "getetag"} {
		if !strings.Contains(report, want) {
			t.Errorf("REPORT = %s, missing %q", report, want)
		}
	}
	if got.Token != "tok-8" {
		t.Errorf("Token = %q, want tok-8", got.Token)
	}
	etags := map[string]string{}
	for _, o := range got.Changed {
		etags[o.URL] = o.ETag
	}
	if len(etags) != 2 || etags[srv.URL+"/cal/new.ics"] != "n1" || etags[srv.URL+"/cal/b%20b.ics"] != "b2" {
		t.Errorf("Changed = %+v, want new.ics and b b.ics with their ETags", got.Changed)
	}
	if len(got.Deleted) != 1 || got.Deleted[0] != srv.URL+"/cal/old.ics" {
		t.Errorf("Deleted = %v, want old.ics", got.Deleted)
	}
}

// A server that has no sync-collection, or no longer knows the token, makes
// the caller fall back to the ETag listing: that is ErrSyncUnavailable, not a
// failure of the sync.
func TestChangesRefusedByTheServerMeansSyncUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusConflict, http.StatusBadRequest, http.StatusNotImplemented, http.StatusUnsupportedMediaType, http.StatusMethodNotAllowed} {
		srv := syncServer(t, status, nil)
		c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
		_, err := c.Changes(context.Background(), srv.URL+"/cal/", "stale")
		if !errors.Is(err, caldavclient.ErrSyncUnavailable) {
			t.Errorf("status %d: Changes() = %v, want ErrSyncUnavailable", status, err)
		}
	}
}

// A server fault is a failure: the caller retries, it does not silently list.
func TestChangesServerErrorFails(t *testing.T) {
	srv := syncServer(t, http.StatusServiceUnavailable, nil)
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	_, err := c.Changes(context.Background(), srv.URL+"/cal/", "tok")
	var se *caldavclient.StatusError
	if err == nil || errors.Is(err, caldavclient.ErrSyncUnavailable) || !errors.As(err, &se) || se.Code != 503 {
		t.Fatalf("Changes() = %v, want a StatusError 503", err)
	}
}

func TestChangesRefusesAMemberOnAForeignHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"><D:response><D:href>http://evil.example.net/cal/x.ics</D:href><D:propstat><D:prop><D:getetag>"x"</D:getetag></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response><D:sync-token>t</D:sync-token></D:multistatus>`)
	}))
	t.Cleanup(srv.Close)
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	_, err := c.Changes(context.Background(), srv.URL+"/cal/", "tok")
	var foreign *caldavclient.ForeignHostError
	if !errors.As(err, &foreign) {
		t.Fatalf("Changes() = %v, want a ForeignHostError", err)
	}
}

// RFC 6578 3.6: a server that cannot send all the changes answers with the
// ones it has, a token to carry on from, and 507 on the collection itself.
func TestChangesAnswerCutShortIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMultiStatus)
		io.WriteString(w, `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:">`+
			`<D:response><D:href>/cal/new.ics</D:href><D:propstat><D:prop><D:getetag>"n1"</D:getetag></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response>`+
			`<D:response><D:href>/cal/</D:href><D:status>HTTP/1.1 507 Insufficient Storage</D:status></D:response>`+
			`<D:sync-token>tok-8</D:sync-token></D:multistatus>`)
	}))
	t.Cleanup(srv.Close)
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	got, err := c.Changes(context.Background(), srv.URL+"/cal/", "tok-7")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || got.Token != "tok-8" || len(got.Changed) != 1 {
		t.Fatalf("Changes() = %+v, want the partial answer marked truncated", got)
	}
}

func TestChangesCompleteAnswerIsNotTruncated(t *testing.T) {
	srv := syncServer(t, http.StatusMultiStatus, nil)
	c, _ := caldavclient.NewClient(srv.URL, "u", "p", nil)
	got, err := c.Changes(context.Background(), srv.URL+"/cal/", "tok-7")
	if err != nil || got.Truncated {
		t.Fatalf("Changes() = %+v, %v, want a complete answer", got, err)
	}
}
