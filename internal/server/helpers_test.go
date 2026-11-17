package server_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/weather"
)

type box struct {
	t       *testing.T
	srv     *httptest.Server // HTTPS once the box has its certificate, else plain HTTP
	handler http.Handler
	pki     *pki.Store
	db      *store.DB
	client  *http.Client
	now     time.Time
	feeds   *fakeFeeds
	sync    *calsync.Service
	public  *fakePublic
	weather *weather.Service // fetches from meteo, never from the internet
	meteo   *fakeMeteo
	// updateDir stands in for the data volume that the host script shares with the box.
	updateDir string
	// photoDir stands in for the data volume: the photo folder and the stored photos.
	photoDir string
}

// fakeMeteo stands in for Open-Meteo.
type fakeMeteo struct {
	mu     sync.Mutex
	body   string
	status int
	srv    *httptest.Server
}

func (m *fakeMeteo) set(body string, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.body, m.status = body, status
}

// fakePublic stands in for the public certificate manager: what it does
// (DNS-01, CAs) is tested in internal/publiccert.
type fakePublic struct {
	mu      sync.Mutex
	name    string
	status  publiccert.Status
	got     []publiccert.Request
	failure error
	cleared int
	// noCert makes a ready manager have no certificate to serve: the setup is
	// stored but the certificate could not be obtained.
	noCert bool
	// warnOnFailure makes a failed Setup leave a warning, as a failed order may.
	warnOnFailure bool
	// gate, if set, holds Setup back until it is closed.
	gate chan struct{}
	// ctxErr and ctxDeadline describe the context the last Setup ended with.
	ctxErr      error
	ctxDeadline bool
}

func (f *fakePublic) Ready() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.name != "" }
func (f *fakePublic) HasCertificate() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.name != "" && !f.noCert
}
func (f *fakePublic) Name() string { f.mu.Lock(); defer f.mu.Unlock(); return f.name }
func (f *fakePublic) Status() publiccert.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.status
	st.Name = f.name
	return st
}
func (f *fakePublic) Clear() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.name, f.status, f.cleared = "", publiccert.Status{}, f.cleared+1
	return nil
}
func (f *fakePublic) Setup(ctx context.Context, r publiccert.Request) error {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, r)
	f.ctxErr = ctx.Err()
	_, f.ctxDeadline = ctx.Deadline()
	if f.failure != nil {
		if f.warnOnFailure {
			f.status.Warning, f.status.RenewalError = true, f.failure.Error()
		}
		return f.failure
	}
	f.name = r.Name
	f.status.Issuer = r.Issuer
	return nil
}
func (f *fakePublic) requests() []publiccert.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]publiccert.Request(nil), f.got...)
}

// fakeFeeds serves ICS feeds (and failures) to the box's calendar sync.
type fakeFeeds struct {
	mu    sync.Mutex
	feeds map[string]string
	errs  map[string]error
	asked []string
}

func (f *fakeFeeds) Fetch(_ context.Context, url string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, url)
	if err := f.errs[url]; err != nil {
		return nil, err
	}
	return []byte(f.feeds[url]), nil
}

func (f *fakeFeeds) set(url, body string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feeds[url] = body
	if err == nil {
		delete(f.errs, url)
	} else {
		f.errs[url] = err
	}
}

// boxOption changes how a test box is built.
type boxOption func(*boxConfig)

type boxConfig struct{ noHostUpdater bool }

// withoutHostUpdater makes the box run like a plain Docker Compose install,
// without the host updater of ADR 0018.
func withoutHostUpdater(c *boxConfig) { c.noHostUpdater = true }

// newBox returns a box whose HTTPS is set up, served over TLS like in production.
func newBox(t *testing.T, opts ...boxOption) *box {
	t.Helper()
	b := newFreshBox(t, opts...)
	if err := b.pki.Provision("127.0.0.1", b.now); err != nil {
		t.Fatal(err)
	}
	b.srv.Close()
	b.srv = b.serveTLS()
	b.client = b.trustingClient()
	return b
}

// newFreshBox returns a box that has not set up HTTPS yet, reachable over plain HTTP.
func newFreshBox(t *testing.T, opts ...boxOption) *box {
	t.Helper()
	var cfg boxConfig
	for _, o := range opts {
		o(&cfg)
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	keys, err := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetSecrets(keys)
	feeds := &fakeFeeds{feeds: map[string]string{}, errs: map[string]error{}}
	certs, err := pki.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &box{t: t, db: db, pki: certs, feeds: feeds, public: &fakePublic{}, updateDir: t.TempDir(), photoDir: t.TempDir(), now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	now := func() time.Time { return b.now }
	b.sync = calsync.New(db, feeds, now)
	b.meteo = &fakeMeteo{body: `{"current":{"temperature_2m":18.4,"weather_code":0,"is_day":1}}`, status: 200}
	b.meteo.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.meteo.mu.Lock()
		defer b.meteo.mu.Unlock()
		w.WriteHeader(b.meteo.status)
		_, _ = w.Write([]byte(b.meteo.body))
	}))
	t.Cleanup(b.meteo.srv.Close)
	b.weather = weather.New(weather.WithBaseURL(b.meteo.srv.URL), weather.WithClock(now))
	updateDir := b.updateDir
	if cfg.noHostUpdater {
		updateDir, b.updateDir = "", ""
	}
	b.handler = server.New(server.Options{DB: db, PKI: certs, Public: b.public, Now: now, Sync: b.sync, Weather: b.weather, UpdateDir: updateDir, PhotoDir: b.photoDir})
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
		if k == "Host" {
			req.Host = v
		}
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
	return server.New(server.Options{DB: b.db, PKI: b.pki, Public: b.public, HTTPSPort: port, Now: func() time.Time { return b.now }})
}

// GeoNames ids of places of the list on the box (internal/places).
const (
	idMuenchen      = "2867714"
	idPasing        = "2855334" // a district of München
	idObermenzing   = "2859782" // another one
	idSchmedeswurth = "13394048"
	idWien          = "2761369"
)

func validSetupForm() url.Values {
	return url.Values{
		"region":   {"DE-BY"},
		"place_id": {idPasing}, // the place the parent picked from the list
		"name":     {"Mama"},
		"username": {"mama"},
		"password": {"correct horse battery"},
		"pin":      {"2468"},
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

func itoa(n int) string { return strconv.Itoa(n) }

func decodeJSON(t *testing.T, body string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, body)
	}
}
