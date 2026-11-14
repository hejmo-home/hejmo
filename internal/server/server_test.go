package server_test

import (
	"net/http"
	"net/url"
	"testing"
)

func TestHealthzReportsOK(t *testing.T) {
	b := newBox(t)
	res, err := http.Get(b.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("healthz = %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
}

func TestHealthzRejectsNonGet(t *testing.T) {
	b := newBox(t)
	if r := b.post("/healthz", url.Values{}); r.status != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", r.status)
	}
}
