package caldavclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
)

// Google's CalDAV API takes an OAuth access token, never a password (ticket 18).
func TestBearerClientSendsTheAccessTokenAndNoPassword(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path == "/old/" {
			http.Redirect(w, r, "/caldav/v2/mama@gmail.com/events/", http.StatusMovedPermanently)
			return
		}
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><d:multistatus xmlns:d="DAV:"/>`))
	}))
	defer srv.Close()
	c, err := caldavclient.NewBearerClient(srv.URL+"/caldav/v2/", "ya29.the-access-token", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.State(context.Background(), srv.URL+"/caldav/v2/mama@gmail.com/events/")
	_, _ = c.State(context.Background(), srv.URL+"/old/") // a redirect keeps the token
	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 3 {
		t.Fatalf("requests seen: %v", seen)
	}
	for _, a := range seen {
		if a != "Bearer ya29.the-access-token" {
			t.Errorf("Authorization = %q", a)
		}
	}
}

func TestBearerClientRefusesAHostOutsideTheServersDomain(t *testing.T) {
	c, err := caldavclient.NewBearerClient("https://apidata.googleusercontent.com/caldav/v2/", "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.State(context.Background(), "https://evil.example/caldav/v2/x/events/"); err == nil {
		t.Fatal("sent the token to another domain")
	}
}
