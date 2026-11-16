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
	"sync/atomic"
	"testing"
	"time"

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
	return &picture.Fetcher{Public: func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") }, Timeout: 3 * time.Second}
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
	f := &picture.Fetcher{Timeout: 2 * time.Second}
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
	f := &picture.Fetcher{Timeout: 2 * time.Second}
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
	f := &picture.Fetcher{Public: func(netip.Addr) bool { return true }, Timeout: time.Second}
	if _, err := f.Fetch(context.Background(), "http://127.0.0.1:1/a.jpg"); !errors.Is(err, picture.ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}
