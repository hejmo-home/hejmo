package kiosk_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hejmo-home/hejmo/internal/kiosk"
)

// fakeFreeKiosk records what a FreeKiosk REST API would receive.
type fakeFreeKiosk struct {
	mu   sync.Mutex
	key  string
	got  []string // "METHOD path body"
	fail bool
}

func (f *fakeFreeKiosk) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.key != "" && r.Header.Get("X-Api-Key") != f.key {
		http.Error(w, `{"success":false}`, http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.got = append(f.got, r.Method+" "+r.URL.Path+" "+string(body))
	if f.fail {
		http.Error(w, `{"success":false}`, http.StatusInternalServerError)
		return
	}
	_, _ = w.Write([]byte(`{"success":true}`))
}

func TestFreeKioskSwitchesTheFrontLightOffWithBrightnessZero(t *testing.T) {
	fake := &fakeFreeKiosk{key: "k-123"}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if err := kiosk.NewFreeKiosk(srv.URL, "k-123").FrontLightOff(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.got) != 1 || fake.got[0] != `POST /api/brightness {"value":0}` {
		t.Fatalf("got %q", fake.got)
	}
}

func TestFreeKioskWithoutKeySendsNoHeader(t *testing.T) {
	fake := &fakeFreeKiosk{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if err := kiosk.NewFreeKiosk(srv.URL+"/", "").FrontLightOff(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.got) != 1 {
		t.Fatalf("got %q", fake.got)
	}
}

func TestFreeKioskReportsRefusalAndFailure(t *testing.T) {
	fake := &fakeFreeKiosk{key: "right"}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if err := kiosk.NewFreeKiosk(srv.URL, "wrong").FrontLightOff(context.Background()); err == nil {
		t.Fatal("wrong key accepted")
	}
	fake.key, fake.fail = "", true
	if err := kiosk.NewFreeKiosk(srv.URL, "").FrontLightOff(context.Background()); err == nil {
		t.Fatal("server error not reported")
	}
	srv.Close()
	if err := kiosk.NewFreeKiosk(srv.URL, "").FrontLightOff(context.Background()); err == nil {
		t.Fatal("unreachable kiosk not reported")
	}
}

func TestValidURLAcceptsOnlyHTTP(t *testing.T) {
	for u, ok := range map[string]bool{"http://192.168.1.20:8080": true, "https://tablet.lan:8080/": true, "ftp://x": false, "tablet": false, "": false, "http://": false} {
		if kiosk.ValidURL(u) != ok {
			t.Errorf("ValidURL(%q) = %v", u, !ok)
		}
	}
}
