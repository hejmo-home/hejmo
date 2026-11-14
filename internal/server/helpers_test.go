package server_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

type box struct {
	t       *testing.T
	srv     *httptest.Server // HTTPS once the box has its certificate, else plain HTTP
	handler http.Handler
	pki     *pki.Store
	db      *store.DB
	client  *http.Client
	now     time.Time
}

// newBox returns a box whose HTTPS is set up, served over TLS like in production.
func newBox(t *testing.T) *box {
	t.Helper()
	b := newFreshBox(t)
	if err := b.pki.Provision("127.0.0.1", b.now); err != nil {
		t.Fatal(err)
	}
	b.srv.Close()
	b.srv = b.serveTLS()
	b.client = b.trustingClient()
	return b
}

// newFreshBox returns a box that has not set up HTTPS yet, reachable over plain HTTP.
func newFreshBox(t *testing.T) *box {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	certs, err := pki.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &box{t: t, db: db, pki: certs, now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	b.handler = server.New(server.Options{DB: db, PKI: certs, Now: func() time.Time { return b.now }})
	b.srv = httptest.NewServer(b.handler)
	t.Cleanup(func() { b.srv.Close() })
	b.client = b.trustingClient()
	return b
}

// serveTLS serves the box over HTTPS with its own certificate.
func (b *box) serveTLS() *httptest.Server {
	srv := httptest.NewUnstartedServer(b.handler)
	cert, err := b.pki.GetCertificate(nil)
	if err != nil {
		b.t.Fatal(err)
	}
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{*cert}}
	srv.StartTLS()
	b.t.Cleanup(srv.Close)
	return srv
}

// bareClient trusts the box's CA but keeps no cookies, like an API client.
func (b *box) bareClient() *http.Client {
	c := b.trustingClient()
	c.Jar, c.CheckRedirect = nil, nil
	return c
}

// trustingClient trusts the box's CA once it exists, keeps cookies and does not follow redirects.
func (b *box) trustingClient() *http.Client {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(b.pki.CAPEM())
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:           jar,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type resp struct {
	status   int
	location string
	body     string
}

func (b *box) do(method, path string, form url.Values, header map[string]string) resp {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, b.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", b.srv.URL)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), string(raw)}
}

func (b *box) get(path string) resp { return b.do(http.MethodGet, path, nil, nil) }

func (b *box) post(path string, form url.Values) resp { return b.do(http.MethodPost, path, form, nil) }

func (b *box) logout() {
	jar, _ := cookiejar.New(nil)
	b.client.Jar = jar
}

// plainServer serves the same box over HTTP, as the box's port 80 does.
func (b *box) plainServer() *httptest.Server {
	srv := httptest.NewServer(b.handler)
	b.t.Cleanup(srv.Close)
	return srv
}

func pemToDER(t *testing.T, p []byte) []byte {
	t.Helper()
	block, _ := pem.Decode(p)
	if block == nil {
		t.Fatal("not PEM")
	}
	return block.Bytes
}

// serverWithHTTPSPort rebuilds the box's handler with a public HTTPS port.
func serverWithHTTPSPort(b *box, port string) http.Handler {
	return server.New(server.Options{DB: b.db, PKI: b.pki, HTTPSPort: port, Now: func() time.Time { return b.now }})
}

func validSetupForm() url.Values {
	return url.Values{
		"home_name": {"Pasing"},
		"region":    {"DE-BY"},
		"name":      {"Mama"},
		"username":  {"mama"},
		"password":  {"correct horse battery"},
		"pin":       {"2468"},
	}
}

var secretRe = regexp.MustCompile(`data-totp-secret="([A-Z2-7]+)"`)

func secretOn(t *testing.T, page string) string {
	t.Helper()
	m := secretRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no TOTP secret on page:\n%s", page)
	}
	return m[1]
}
