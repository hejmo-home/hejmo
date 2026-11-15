package caldavclient_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
)

// The quirk corpus (testdata/quirks, see its README) replays provider answers
// that fakes and Radicale never give. Every quirk runs through discovery, list
// and multiget as far as its expect.json asks for.

type quirkRoute struct {
	host, method, bodyHas, path string
	status                      int
	body                        []byte
	location                    string
}

type quirkObject struct {
	URL  string `json:"url"`
	ETag string `json:"etag"`
	Data string `json:"data"`
}

type quirkExpect struct {
	Server    string `json:"server"`
	Calendars []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"calendars"`
	Calendar string        `json:"calendar"`
	List     []quirkObject `json:"list"`
	// Count is the number of events the event-count query must report
	// (nil: the quirk says nothing about it).
	Count *int `json:"count"`
	Fetch    []quirkObject `json:"fetch"`
}

var icsPlaceholder = regexp.MustCompile(`\{\{ics:([^}]+)\}\}`)

func loadQuirkRoutes(t *testing.T, dir string) []quirkRoute {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "routes"))
	if err != nil {
		t.Fatal(err)
	}
	var routes []quirkRoute
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if len(f) < 5 {
			t.Fatalf("routes: %q needs HOST METHOD PATH STATUS BODYFILE", line)
		}
		status, err := strconv.Atoi(f[3])
		if err != nil {
			t.Fatalf("routes: %q: %v", line, err)
		}
		r := quirkRoute{host: f[0], path: f[2], status: status}
		r.method, r.bodyHas, _ = strings.Cut(f[1], ":")
		if f[4] != "-" {
			body, err := os.ReadFile(filepath.Join(dir, f[4]))
			if err != nil {
				t.Fatal(err)
			}
			r.body = icsPlaceholder.ReplaceAllFunc(body, func(m []byte) []byte {
				ics, err := os.ReadFile(filepath.Join(dir, string(icsPlaceholder.FindSubmatch(m)[1])))
				if err != nil {
					t.Fatal(err)
				}
				var esc strings.Builder
				xml.EscapeText(&esc, ics)
				return []byte(esc.String())
			})
		}
		for _, extra := range f[5:] {
			if v, ok := strings.CutPrefix(extra, "Location="); ok {
				r.location = v
			}
		}
		routes = append(routes, r)
	}
	return routes
}

func TestProviderQuirks(t *testing.T) {
	dirs, err := filepath.Glob("testdata/quirks/*/routes")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no quirks found (%v)", err)
	}
	for _, routesFile := range dirs {
		dir := filepath.Dir(routesFile)
		t.Run(filepath.Base(dir), func(t *testing.T) { runQuirk(t, dir) })
	}
}

func runQuirk(t *testing.T, dir string) {
	var want quirkExpect
	raw, err := os.ReadFile(filepath.Join(dir, "expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	routes := loadQuirkRoutes(t, dir)

	hosts := map[string]*fakeHost{}
	for _, r := range routes {
		if hosts[r.host] != nil {
			continue
		}
		host := r.host
		hosts[host] = startFakeHost(t, func(w http.ResponseWriter, req *http.Request) {
			body, _ := io.ReadAll(req.Body)
			for _, r := range routes {
				if r.host != host || r.method != req.Method || r.path != req.URL.Path || !strings.Contains(string(body), r.bodyHas) {
					continue
				}
				if r.location != "" {
					w.Header().Set("Location", r.location)
				}
				w.Header().Set("Content-Type", "application/xml; charset=utf-8")
				w.WriteHeader(r.status)
				w.Write(r.body)
				return
			}
			http.NotFound(w, req)
		})
	}
	c := multiHostClient(t, want.Server, hosts)
	ctx := context.Background()

	if want.Calendars != nil {
		got, err := c.Calendars(ctx)
		if err != nil {
			t.Fatalf("discovery: %v", err)
		}
		if len(got) != len(want.Calendars) {
			t.Fatalf("discovery = %+v, want %+v", got, want.Calendars)
		}
		for i, w := range want.Calendars {
			if got[i].Name != w.Name || got[i].URL != w.URL {
				t.Errorf("calendar %d = %+v, want %+v", i, got[i], w)
			}
		}
	}
	if want.Calendar == "" && len(want.Calendars) > 0 {
		want.Calendar = want.Calendars[0].URL
	}
	if want.List != nil {
		got, err := c.List(ctx, want.Calendar)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != len(want.List) {
			t.Fatalf("list = %+v, want %+v", got, want.List)
		}
		for i, w := range want.List {
			if got[i].URL != w.URL || got[i].ETag != w.ETag {
				t.Errorf("listed object %d = {%s %q}, want {%s %q}", i, got[i].URL, got[i].ETag, w.URL, w.ETag)
			}
		}
	}
	if want.Count != nil {
		got, err := c.CountEvents(ctx, want.Calendar, time.Date(2025, 11, 14, 12, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if got != *want.Count {
			t.Errorf("count = %d, want %d", got, *want.Count)
		}
	}
	if want.Fetch != nil {
		var urls []string
		for _, w := range want.Fetch {
			urls = append(urls, w.URL)
		}
		got, err := c.Fetch(ctx, want.Calendar, urls)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		byURL := map[string]caldavclient.Object{}
		for _, o := range got {
			byURL[o.URL] = o
		}
		for _, w := range want.Fetch {
			o, ok := byURL[w.URL]
			if !ok {
				t.Errorf("fetch: %s missing from %+v", w.URL, got)
				continue
			}
			text, err := os.ReadFile(filepath.Join(dir, w.Data))
			if err != nil {
				t.Fatal(err)
			}
			if o.Err != nil {
				t.Errorf("%s: Err = %v, want it readable", w.URL, o.Err)
			}
			if string(o.Data) != string(text) {
				t.Errorf("%s: Data = %q, want the provider's text %q", w.URL, o.Data, text)
			}
			if o.ETag != w.ETag {
				t.Errorf("%s: ETag = %q, want %q", w.URL, o.ETag, w.ETag)
			}
		}
	}
}
