package server_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestHealthzReportsOK(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	res, err := b.bareClient().Get(b.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("healthz = %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
}

func TestHealthzRejectsNonGet(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	if r := b.post("/healthz", url.Values{}); r.status != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", r.status)
	}
}

// Figtree is served by the box itself (no outside font CDN).
func TestServesFigtreeFromTheBox(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	for path, magic := range map[string]string{
		"/static/fonts/Figtree-wght.ttf": "\x00\x01\x00\x00",
		"/static/fonts/OFL.txt":          "Copyright",
	} {
		res, err := b.bareClient().Get(b.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), magic) {
			t.Errorf("GET %s = %d, body starts %q", path, res.StatusCode, body[:min(len(body), 12)])
		}
	}
}
