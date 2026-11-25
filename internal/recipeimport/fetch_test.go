package recipeimport_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/recipeimport"
)

func loopback() *recipeimport.Fetcher {
	return &recipeimport.Fetcher{Gate: outbound.AllowAll(), Public: func(a netip.Addr) bool { return a.IsLoopback() }, AllowIPLiteral: true}
}

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestFetchReadsPage(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html>ok</html>"))
	})
	p, err := loopback().Fetch(context.Background(), s.URL+"/r")
	if err != nil || !strings.Contains(string(p.Body), "ok") || p.URL.Path != "/r" {
		t.Fatalf("Fetch: %v %+v", err, p)
	}
}

func TestFetchLatin1(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		w.Write([]byte("Gem\xfcse"))
	})
	p, err := loopback().Fetch(context.Background(), s.URL)
	if err != nil || !strings.Contains(string(p.Body), "Gemüse") {
		t.Fatalf("latin1: %v %q", err, p.Body)
	}
}

func TestFetchErrors(t *testing.T) {
	big := strings.Repeat("a", recipeimport.MaxBytes+10)
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/404":
			http.NotFound(w, r)
		case "/410":
			w.WriteHeader(410)
		case "/403":
			w.WriteHeader(403)
		case "/pdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF"))
		case "/txt":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("Mehl"))
		case "/big":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(big))
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		case "/loop":
			http.Redirect(w, r, "/loop", 302)
		case "/home":
			http.Redirect(w, r, "http://192.168.178.20/x", 302)
		}
	})
	f := loopback()
	// /slow has its own short timeout: under -race the 5 MB of /big may take longer than that.
	slow := loopback()
	slow.Timeout = 100 * time.Millisecond
	var st *recipeimport.StatusError
	var nh *recipeimport.NotHTMLError
	var priv *recipeimport.PrivateError
	for path, check := range map[string]func(error) bool{
		"/404":  func(e error) bool { return errors.As(e, &st) && st.Code == 404 },
		"/410":  func(e error) bool { return errors.As(e, &st) && st.Code == 410 },
		"/403":  func(e error) bool { return errors.As(e, &st) && st.Code == 403 },
		"/pdf":  func(e error) bool { return errors.As(e, &nh) && nh.Type == "application/pdf" },
		"/txt":  func(e error) bool { return errors.As(e, &nh) && nh.Type == "text/plain" },
		"/big":  func(e error) bool { return errors.Is(e, recipeimport.ErrTooLarge) },
		"/slow": func(e error) bool { return errors.Is(e, recipeimport.ErrTimeout) },
		"/loop": func(e error) bool { return errors.Is(e, recipeimport.ErrTooManyRedirects) },
		"/home": func(e error) bool { return errors.As(e, &priv) && priv.Addr == "192.168.178.20" },
	} {
		fetcher := f
		if path == "/slow" {
			fetcher = slow
		}
		if _, err := fetcher.Fetch(context.Background(), s.URL+path); err == nil || !check(err) {
			t.Errorf("%s: err = %v", path, err)
		}
	}
}

func TestFetchRefusesAddresses(t *testing.T) {
	f := &recipeimport.Fetcher{Gate: outbound.AllowAll()}
	var priv *recipeimport.PrivateError
	for _, u := range []string{"http://192.168.178.20/x", "http://127.0.0.1/x", "http://93.184.216.34/x", "http://[::1]/x", "http://localhost/x"} {
		if _, err := f.Fetch(context.Background(), u); !errors.As(err, &priv) {
			t.Errorf("%s: err = %v, want PrivateError", u, err)
		}
	}
	if _, err := f.Fetch(context.Background(), "ftp://x.de/a"); !errors.Is(err, recipeimport.ErrScheme) {
		t.Errorf("ftp: %v", err)
	}
	if _, err := f.Fetch(context.Background(), "https://user:pw@x.de/"); !errors.Is(err, recipeimport.ErrAddress) {
		t.Errorf("userinfo: %v", err)
	}
}

func TestFetchClassifiesNames(t *testing.T) {
	err := recipeimport.Classify(context.Background(), &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true})
	if !errors.Is(err, recipeimport.ErrUnknownHost) {
		t.Errorf("dns: %v", err)
	}
}

func TestFetchGate(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("x")) })
	f := loopback()
	f.Gate = outbound.New(outbound.Closed{}, nil)
	if _, err := f.Fetch(context.Background(), s.URL); !errors.Is(err, outbound.ErrRefused) {
		t.Errorf("closed gate: %v", err)
	}
}

// Only the switch "recipes" stops the page: the fetcher counts as that connection, not as another.
type onlyOff struct{ off outbound.Service }

func (p onlyOff) Verdict(s outbound.Service) outbound.Verdict {
	if s == p.off {
		return outbound.VerdictSwitchedOff
	}
	return outbound.VerdictAllowed
}

func TestFetchCountsAsTheRecipesConnection(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<p>x</p>"))
	})
	f := loopback()
	f.Gate = outbound.New(onlyOff{outbound.Recipes}, nil)
	if _, err := f.Fetch(context.Background(), s.URL); !errors.Is(err, outbound.ErrRefused) {
		t.Errorf("recipes off: %v", err)
	}
	f.Gate = outbound.New(onlyOff{outbound.Images}, nil)
	if _, err := f.Fetch(context.Background(), s.URL); err != nil {
		t.Errorf("only images off: %v", err)
	}
}
