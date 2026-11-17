package calsync_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/outbound"
)

func TestHTTPFetcher(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.ics":
			if r.Header.Get("User-Agent") == "" {
				t.Error("no user agent")
			}
			_, _ = w.Write([]byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n"))
		case "/huge.ics":
			_, _ = w.Write([]byte(strings.Repeat("x", 17<<20)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := calsync.HTTPFetcher{Client: outbound.AllowAll().Client(outbound.ICS, 30*time.Second)}
	ctx := context.Background()

	if body, err := f.Fetch(ctx, srv.URL+"/ok.ics"); err != nil || !strings.HasPrefix(string(body), "BEGIN:VCALENDAR") {
		t.Fatalf("ok = %q, %v", body, err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/missing.ics"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404 error = %v", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/huge.ics"); err == nil {
		t.Fatal("oversized feed accepted")
	} else if m := calsync.MessageOf(err); m.Key != "sync.fail.too_large" {
		t.Fatalf("oversized message = %+v", m)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/missing.ics"); calsync.MessageOf(err).Key != "sync.fail.status" || !strings.Contains(calsync.MessageOf(err).Params["status"], "404") {
		t.Fatalf("404 message = %+v", calsync.MessageOf(err))
	}
	if _, err := f.Fetch(ctx, "http://127.0.0.1:1/x.ics"); err == nil {
		t.Fatal("unreachable server accepted")
	}
}
