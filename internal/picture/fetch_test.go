package picture_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/picture"
)

// Ticket 164, threat model "download of web images" (server-side request
// forgery): the box fetches an address a user typed. It may only reach public
// addresses over http(s), however the address is written, redirected or
// resolved.

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

// loopbackIsPublic lets the tests reach their own server on 127.0.0.1, and
// nothing else: 127.0.0.2 stays private to prove the check runs.
func loopbackIsPublic() *picture.Fetcher {
	return &picture.Fetcher{Gate: outbound.AllowAll(), Public: func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") }, Timeout: 3 * time.Second}
}

func jpegHandler(t *testing.T) http.HandlerFunc {
	body := encodeJPEG(t, gradient(800, 600))
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(body)
	}
}

func TestAPublicImageIsDownloadedOnceAndStoredScaledDown(t *testing.T) {
	var requests int
	var seen *http.Request
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { requests++; seen = r; jpegHandler(t)(w, r) })
	res, err := loopbackIsPublic().Fetch(context.Background(), srv.URL+"/media/hafer.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, res.JPEG); w != 512 || h != 384 {
		t.Errorf("result %d x %d", w, h)
	}
	if requests != 1 {
		t.Errorf("%d requests, want 1", requests)
	}
	if res.Host != "127.0.0.1" {
		t.Errorf("host = %q", res.Host)
	}
	if seen.Header.Get("Cookie") != "" || seen.Header.Get("Referer") != "" || seen.Header.Get("Authorization") != "" {
		t.Errorf("the request carries more than it should: %v", seen.Header)
	}
	if !strings.Contains(seen.Header.Get("User-Agent"), "Hejmo") {
		t.Errorf("user agent = %q", seen.Header.Get("User-Agent"))
	}
}

func TestAddressesInTheOwnNetworkAreRefusedWhateverTheirSpelling(t *testing.T) {
	hits := 0
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { hits++; jpegHandler(t)(w, r) })
	port := srv.Listener.Addr().(*net.TCPAddr).Port
	f := &picture.Fetcher{Gate: outbound.AllowAll(), Timeout: 2 * time.Second}
	for _, u := range []string{
		fmt.Sprintf("http://127.0.0.1:%d/a.jpg", port),
		fmt.Sprintf("http://localhost:%d/a.jpg", port),
		fmt.Sprintf("http://[::1]:%d/a.jpg", port),
		fmt.Sprintf("http://[::ffff:127.0.0.1]:%d/a.jpg", port),
		fmt.Sprintf("http://0.0.0.0:%d/a.jpg", port),
		fmt.Sprintf("http://127.1:%d/a.jpg", port),
		fmt.Sprintf("http://2130706433:%d/a.jpg", port),
		fmt.Sprintf("http://0x7f.0.0.1:%d/a.jpg", port),
		fmt.Sprintf("http://017700000001:%d/a.jpg", port),
	} {
		if _, err := f.Fetch(context.Background(), u); err == nil {
			t.Errorf("%s was fetched", u)
		}
	}
	if hits != 0 {
		t.Errorf("the server in the own network was asked %d times", hits)
	}
}

func TestThePrivateAddressIsNamedInTheError(t *testing.T) {
	f := &picture.Fetcher{Gate: outbound.AllowAll(), Timeout: 2 * time.Second}
	for u, addr := range map[string]string{
		"http://192.168.178.20/bilder/birne.jpg":   "192.168.178.20",
		"http://10.0.0.5/x.jpg":                    "10.0.0.5",
		"http://172.16.4.4/x.jpg":                  "172.16.4.4",
		"http://169.254.169.254/latest/meta-data/": "169.254.169.254",
		"http://100.64.0.1/x.jpg":                  "100.64.0.1",
		"https://[fe80::1]/x.jpg":                  "fe80::1",
		"https://[fd00::1234]/x.jpg":               "fd00::1234",
		"http://[::ffff:192.168.1.1]/x.jpg":        "192.168.1.1",
		"http://224.0.0.1/x.jpg":                   "224.0.0.1",
		"http://[2002:c0a8:0101::1]/x.jpg":         "2002:c0a8:101::1", // 6to4 of 192.168.1.1
	} {
		_, err := f.Fetch(context.Background(), u)
		var private *picture.PrivateError
		if !errors.As(err, &private) {
			t.Errorf("%s: err = %v, want PrivateError", u, err)
			continue
		}
		if private.Addr != addr {
			t.Errorf("%s: names %q, want %q", u, private.Addr, addr)
		}
	}
}

func TestWhichAddressesAreCalledPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "93.184.216.34": true, "2606:4700:4700::1111": true, "172.32.0.1": true, "100.63.255.255": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "172.31.255.255": false, "192.168.0.1": false,
		"169.254.1.1": false, "100.64.0.1": false, "100.127.255.255": false, "0.0.0.0": false, "255.255.255.255": false,
		"224.0.0.1": false, "192.0.0.1": false, "192.0.2.1": false, "198.18.0.1": false, "198.51.100.1": false, "203.0.113.1": false, "240.0.0.1": false,
		"::1": false, "::": false, "fe80::1": false, "fc00::1": false, "fd12::1": false, "ff02::1": false, "::ffff:10.0.0.1": false, "::ffff:8.8.8.8": true,
		"64:ff9b::7f00:1": false, "::7f00:1": false, "::808:808": false, "64:ff9b:1::1": false, "64:ff9b::808:808": true, "2001:db8::1": false,
	} {
		if got := picture.IsPublic(netip.MustParseAddr(addr)); got != want {
			t.Errorf("IsPublic(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestOnlyHTTPAndHTTPSAddressesWithoutPasswordsAreTaken(t *testing.T) {
	f := loopbackIsPublic()
	for _, u := range []string{"ftp://example.org/a.jpg", "file:///etc/passwd", "gopher://example.org/", "javascript:alert(1)", "//example.org/a.jpg", "example.org/a.jpg", ""} {
		if _, err := f.Fetch(context.Background(), u); !errors.Is(err, picture.ErrScheme) {
			t.Errorf("%q: err = %v, want ErrScheme", u, err)
		}
	}
	for _, u := range []string{"http://user:secret@127.0.0.1/a.jpg", "http:///a.jpg", "http://exa mple.org/"} {
		if _, err := f.Fetch(context.Background(), u); !errors.Is(err, picture.ErrAddress) {
			t.Errorf("%q: err = %v, want ErrAddress", u, err)
		}
	}
}

func TestARedirectIntoTheOwnNetworkIsRefused(t *testing.T) {
	var privateHits int
	private := serve(t, func(w http.ResponseWriter, r *http.Request) { privateHits++ })
	_, port, _ := net.SplitHostPort(private.Listener.Addr().String())
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.2:"+port+"/geheim.jpg", http.StatusFound)
	})
	_, err := loopbackIsPublic().Fetch(context.Background(), srv.URL+"/bild.jpg")
	var p *picture.PrivateError
	if !errors.As(err, &p) || p.Addr != "127.0.0.2" {
		t.Errorf("err = %v, want PrivateError for 127.0.0.2", err)
	}
	if privateHits != 0 {
		t.Error("the redirect target was asked")
	}
}

func TestARedirectToAPublicImageIsFollowedButNotForever(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/real.jpg", http.StatusMovedPermanently)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			jpegHandler(t)(w, r)
		}
	})
	if _, err := loopbackIsPublic().Fetch(context.Background(), srv.URL+"/start"); err != nil {
		t.Errorf("one redirect: %v", err)
	}
	if _, err := loopbackIsPublic().Fetch(context.Background(), srv.URL+"/loop"); !errors.Is(err, picture.ErrTooManyRedirects) {
		t.Errorf("a loop: err = %v, want ErrTooManyRedirects", err)
	}
}

func TestAWebPageAnErrorPageAndTextAreNamedForWhatTheyAre(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/shop":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<!doctype html><title>Shop</title>"))
		case "/liar":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("<html><body>nein</body></html>"))
		case "/text":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("hallo"))
		case "/gone":
			http.NotFound(w, r)
		}
	})
	f := loopbackIsPublic()
	if _, err := f.Fetch(context.Background(), srv.URL+"/shop"); !errors.Is(err, picture.ErrWebPage) {
		t.Errorf("shop: %v", err)
	}
	if _, err := f.Fetch(context.Background(), srv.URL+"/liar"); !errors.Is(err, picture.ErrWebPage) {
		t.Errorf("liar: %v", err)
	}
	if _, err := f.Fetch(context.Background(), srv.URL+"/text"); !errors.Is(err, picture.ErrNotAnImage) {
		t.Errorf("text: %v", err)
	}
	var st *picture.StatusError
	if _, err := f.Fetch(context.Background(), srv.URL+"/gone"); !errors.As(err, &st) || st.Code != 404 {
		t.Errorf("gone: %v", err)
	}
}

func TestADownloadOverTheLimitIsStoppedByItsDeclaredSizeAndWhileItStreams(t *testing.T) {
	var streamed atomic.Int64
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		if r.URL.Path == "/declared" {
			w.Header().Set("Content-Length", fmt.Sprint(40<<20))
			w.WriteHeader(200)
			return
		}
		chunk := make([]byte, 1<<20)
		for i := 0; i < 40; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			streamed.Add(1)
		}
	})
	f := loopbackIsPublic()
	var big *picture.TooLargeError
	if _, err := f.Fetch(context.Background(), srv.URL+"/declared"); !errors.As(err, &big) || big.Bytes != 40<<20 {
		t.Errorf("declared: err = %v", err)
	}
	if _, err := f.Fetch(context.Background(), srv.URL+"/stream"); !errors.As(err, &big) {
		t.Errorf("stream: err = %v", err)
	}
	if streamed.Load() > 20 {
		t.Errorf("the box read %d MB before it stopped", streamed.Load())
	}
}

func TestASlowServerIsGivenUpOn(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	})
	f := loopbackIsPublic()
	f.Timeout = 200 * time.Millisecond
	start := time.Now()
	if _, err := f.Fetch(context.Background(), srv.URL+"/slow"); !errors.Is(err, picture.ErrTimeout) {
		t.Errorf("err = %v, want ErrTimeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
	if picture.DefaultTimeout != 10*time.Second {
		t.Errorf("the default is %v, want the 10 seconds of the design", picture.DefaultTimeout)
	}
}

func TestAnUnreachableHostIsNotTheUsersFault(t *testing.T) {
	f := &picture.Fetcher{Gate: outbound.AllowAll(), Public: func(netip.Addr) bool { return true }, Timeout: time.Second}
	if _, err := f.Fetch(context.Background(), "http://127.0.0.1:1/a.jpg"); !errors.Is(err, picture.ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

func TestACancelledDownloadStopsWaitingForTheDecodeSlot(t *testing.T) {
	sent := make(chan struct{})
	var once sync.Once
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		jpegHandler(t)(w, r)
		once.Do(func() { close(sent) })
	})
	release := picture.Hold() // another picture is being decoded
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	f := loopbackIsPublic()
	f.Timeout = 30 * time.Second
	done := make(chan error, 1)
	go func() {
		_, err := f.Fetch(ctx, srv.URL+"/media/hafer.jpg")
		done <- err
	}()
	select {
	case <-sent: // the whole picture is on its way, so the fetch is waiting for the slot
	case <-time.After(2 * time.Second):
		t.Fatal("the server was never asked for the picture")
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, picture.ErrTimeout) {
			t.Errorf("err = %v, want ErrTimeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled download kept waiting for the decode slot")
	}
}

// Ticket 201: a web image is a connection to the outside like any other; with
// web images switched off, or offline mode on, nothing is downloaded and the
// attempt is recorded with the address (without its query).
type gatePolicy struct{ v outbound.Verdict }

func (p gatePolicy) Verdict(outbound.Service) outbound.Verdict { return p.v }

type gateLedger struct {
	mu      sync.Mutex
	refused []string
}

func (l *gateLedger) Refused(_ time.Time, s outbound.Service, target string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refused = append(l.refused, string(s)+" "+target)
}
func (l *gateLedger) Contacted(time.Time, outbound.Service, string) {}

func TestAWebImageIsRefusedWhileItsConnectionIsOff(t *testing.T) {
	var requests atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); jpegHandler(t)(w, r) })
	led := &gateLedger{}
	f := loopbackIsPublic()
	f.Gate = outbound.New(gatePolicy{outbound.VerdictOffline}, led)

	_, err := f.Fetch(context.Background(), srv.URL+"/media/hafer.jpg?token=secret")
	if !errors.Is(err, outbound.ErrRefused) {
		t.Fatalf("Fetch = %v, want refused", err)
	}
	if requests.Load() != 0 {
		t.Error("the server was contacted")
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	if len(led.refused) != 1 || led.refused[0] != "images "+host+"/media/hafer.jpg" {
		t.Errorf("refused = %v, want the address without its query", led.refused)
	}
}

func TestADownloadCanAskForALargerMaximum(t *testing.T) {
	body := encodeJPEG(t, gradient(2400, 1200))
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(body)
	})
	f := loopbackIsPublic()
	res, err := f.FetchMax(context.Background(), srv.URL+"/a.jpg", picture.RecipeMaxSide)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, res.JPEG); w != 1600 || h != 800 {
		t.Errorf("result %d x %d, want 1600 x 800", w, h)
	}
	res, err = f.Fetch(context.Background(), srv.URL+"/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, res.JPEG); w != 512 || h != 256 {
		t.Errorf("Fetch result %d x %d, want 512 x 256", w, h)
	}
}

// Ticket 289: the picture of a recipe page is fetched as the connection "recipes", from names only.

// switchedOff is a Policy that refuses one connection and allows the rest.
type switchedOff struct{ off outbound.Service }

func (p switchedOff) Verdict(s outbound.Service) outbound.Verdict {
	if s == p.off {
		return outbound.VerdictSwitchedOff
	}
	return outbound.VerdictAllowed
}

type pathLedger struct {
	mu      sync.Mutex
	targets []string
}

func (l *pathLedger) Refused(time.Time, outbound.Service, string) {}
func (l *pathLedger) Contacted(_ time.Time, _ outbound.Service, target string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.targets = append(l.targets, target)
}

func TestTheServiceDecidesWhichSwitchStopsTheDownload(t *testing.T) {
	srv := serve(t, jpegHandler(t))
	for _, c := range []struct {
		name    string
		service outbound.Service // the zero value is the web images
		off     outbound.Service
		refused bool
	}{
		{"web images, images off", "", outbound.Images, true},
		{"web images, recipes off", "", outbound.Recipes, false},
		{"recipe picture, recipes off", outbound.Recipes, outbound.Recipes, true},
		{"recipe picture, images off", outbound.Recipes, outbound.Images, false},
	} {
		f := loopbackIsPublic()
		f.Gate = outbound.New(switchedOff{c.off}, nil)
		f.Service = c.service
		_, err := f.Fetch(context.Background(), srv.URL+"/a.jpg")
		if got := errors.Is(err, outbound.ErrRefused); got != c.refused || (!c.refused && err != nil) {
			t.Errorf("%s: err = %v, want refused = %v", c.name, err, c.refused)
		}
	}
}

func TestNumberAddressesAreRefusedWhenAskedTo(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "http://93.184.216.34/pic.jpg", http.StatusFound)
			return
		}
		jpegHandler(t)(w, r)
	})
	f := loopbackIsPublic()
	f.NoIPLiteral = true
	f.Public = func(netip.Addr) bool { return true } // a public number address: only the rule against numbers stops it
	var priv *picture.PrivateError
	for _, u := range []string{srv.URL + "/a.jpg", "http://93.184.216.34/a.jpg", "http://[2606:2800:220:1::1]/a.jpg", srv.URL + "/hop"} {
		if _, err := f.Fetch(context.Background(), u); !errors.As(err, &priv) {
			t.Errorf("%s: err = %v, want PrivateError", u, err)
		}
	}
	// the zero Fetcher keeps taking public number addresses (the library's picture field)
	g := loopbackIsPublic()
	if _, err := g.Fetch(context.Background(), srv.URL+"/a.jpg"); err != nil {
		t.Errorf("without the rule: %v", err)
	}
}

func TestARecipePictureLeavesNoPathInTheLedger(t *testing.T) {
	srv := serve(t, jpegHandler(t))
	for _, c := range []struct {
		service  outbound.Service
		wantPath bool
	}{{"", true}, {outbound.Recipes, false}} {
		l := &pathLedger{}
		f := loopbackIsPublic()
		f.Gate = outbound.New(switchedOff{"none"}, l)
		f.Service = c.service
		if _, err := f.Fetch(context.Background(), srv.URL+"/secret/pic.jpg"); err != nil {
			t.Fatal(err)
		}
		if got := len(l.targets) == 1 && strings.Contains(l.targets[0], "/secret/pic.jpg"); got != c.wantPath {
			t.Errorf("service %q: ledger %v, path recorded = %v, want %v", c.service, l.targets, got, c.wantPath)
		}
	}
}
