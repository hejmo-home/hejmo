package kiosk_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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
	// status is the body of GET /api/status; empty means a plain success.
	status string
}

func (f *fakeFreeKiosk) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.key != "" && r.Header.Get("X-Api-Key") != f.key {
		http.Error(w, `{"success":false}`, http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/status" && f.status != "" {
		_, _ = w.Write([]byte(f.status))
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

func TestFreeKioskSetsTheBrightnessInPercent(t *testing.T) {
	fake := &fakeFreeKiosk{key: "k-123"}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	a := kiosk.NewFreeKiosk(srv.URL+"/", "k-123")
	for _, p := range []int{0, 40} {
		if err := a.SetBrightness(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{`POST /api/brightness {"value":0}`, `POST /api/brightness {"value":40}`}; !reflect.DeepEqual(fake.got, want) {
		t.Fatalf("got %q, want %q", fake.got, want)
	}
	if err := a.SetBrightness(context.Background(), 101); err == nil {
		t.Fatal("a brightness above 100 was sent")
	}
}

func TestFreeKioskSwitchesTheScreenOffAndOn(t *testing.T) {
	fake := &fakeFreeKiosk{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	a := kiosk.NewFreeKiosk(srv.URL, "")
	if err := a.ScreenOff(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.ScreenOn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"POST /api/screen/off ", "POST /api/screen/on "}; !reflect.DeepEqual(fake.got, want) {
		t.Fatalf("got %q, want %q", fake.got, want)
	}
}

func TestFreeKioskHandsBrightnessToTheLightSensor(t *testing.T) {
	fake := &fakeFreeKiosk{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if err := kiosk.NewFreeKiosk(srv.URL, "").EnableAutoBrightness(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.got) != 1 || !strings.HasPrefix(fake.got[0], "POST /api/autoBrightness/enable ") {
		t.Fatalf("got %q", fake.got)
	}
}

func TestFreeKioskStatusReportsTheLightSensorAndWhatIsSet(t *testing.T) {
	fake := &fakeFreeKiosk{key: "k", status: `{"success":true,"data":{"screen":{"on":true,"brightness":40},"sensors":{"light":150.5,"proximity":5},"autoBrightness":{"enabled":false}}}`}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	st, err := kiosk.NewFreeKiosk(srv.URL, "k").Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := (kiosk.Status{HasLightSensor: true, Brightness: 40, ScreenOn: true}); st != want {
		t.Fatalf("status = %+v, want %+v", st, want)
	}
	fake.status = `{"success":true,"data":{"screen":{"on":false,"brightness":0},"sensors":{"proximity":5},"autoBrightness":{"enabled":true}}}`
	st, err = kiosk.NewFreeKiosk(srv.URL, "k").Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := (kiosk.Status{AutoBrightness: true}); st != want {
		t.Fatalf("status without a sensor = %+v, want %+v", st, want)
	}
	fake.status = `not json`
	if _, err := kiosk.NewFreeKiosk(srv.URL, "k").Status(context.Background()); err == nil {
		t.Fatal("an unreadable answer was accepted")
	}
}

func TestFreeKioskWithoutKeySendsNoHeader(t *testing.T) {
	fake := &fakeFreeKiosk{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	if err := kiosk.NewFreeKiosk(srv.URL+"/", "").SetBrightness(context.Background(), 10); err != nil {
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
	if err := kiosk.NewFreeKiosk(srv.URL, "wrong").SetBrightness(context.Background(), 10); err == nil {
		t.Fatal("wrong key accepted")
	}
	fake.key, fake.fail = "", true
	if err := kiosk.NewFreeKiosk(srv.URL, "").ScreenOff(context.Background()); err == nil {
		t.Fatal("server error not reported")
	}
	srv.Close()
	if err := kiosk.NewFreeKiosk(srv.URL, "").SetBrightness(context.Background(), 10); err == nil {
		t.Fatal("unreachable kiosk not reported")
	}
	if _, err := kiosk.NewFreeKiosk(srv.URL, "").Status(context.Background()); err == nil {
		t.Fatal("unreachable kiosk status not reported")
	}
}

func TestValidURLAcceptsOnlyHTTP(t *testing.T) {
	for u, ok := range map[string]bool{"http://192.168.1.20:8080": true, "https://tablet.lan:8080/": true, "ftp://x": false, "tablet": false, "": false, "http://": false} {
		if kiosk.ValidURL(u) != ok {
			t.Errorf("ValidURL(%q) = %v", u, !ok)
		}
	}
}
