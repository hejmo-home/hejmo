package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

type box struct {
	t      *testing.T
	srv    *httptest.Server
	db     *store.DB
	client *http.Client
	now    time.Time
	feeds  *fakeFeeds
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

func newBox(t *testing.T) *box {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	b := &box{t: t, db: db, now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), feeds: &fakeFeeds{feeds: map[string]string{}, errs: map[string]error{}}}
	now := func() time.Time { return b.now }
	b.srv = httptest.NewServer(server.New(server.Options{DB: db, Now: now, Sync: calsync.New(db, b.feeds, now)}))
	t.Cleanup(b.srv.Close)
	jar, _ := cookiejar.New(nil)
	b.client = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return b
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
