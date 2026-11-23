package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #203 (Q147-Q151): "phone" (German "Handy") is not the name of a domain concept any more; the
// concept is the personal device. Page templates, the locales, the OpenAPI contract and the
// route patterns must not bring the old term back. A phone may still be named where it is
// hardware: the device kind keys iphone and android_phone, the iPhone brand, and the words of
// the proposed device names ("Mia's phone"), which are listed here.
var oldTerm = regexp.MustCompile(`(?i)\b(phones?|handys?|android-handy)\b|phones?[_-]|[_-]phones?\b|[a-z]Phones?\b|\bPhones?[A-Z]`)

// Keys whose texts may name a phone as hardware or as an example.
var phoneWordKeys = map[string]string{
	"device.android_phone":                "device kind",
	"passkey.this.android_phone":          "device kind",
	"passkey.on.this.android_phone":       "device kind",
	"passkey.have.android_phone":          "device kind",
	"passkey.offer.how.android_phone":     "device kind",
	"passkey.waiting.android_phone":       "device kind",
	"personal_devices.default_name.phone": "proposed name by the kind of screen (Q148)",
	"personal_devices.name":               "example name",
	"personal_devices.intro":              "what a personal device is (Q147)",
	"hub.personal_devices.hint":           "what a personal device is (Q147)",
}

// routeTable matches a route given as a method and a path in a table.
var routeTable = regexp.MustCompile(`\{"(GET|POST|PUT|PATCH|DELETE)"`)

func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOldPhoneTermIsGoneFromLocalesTemplatesContractAndRoutes(t *testing.T) {
	t.Parallel()
	for _, lang := range []string{"en", "de"} {
		var m map[string]string
		if err := json.Unmarshal([]byte(repoFile(t, "internal", "i18n", "locales", lang+".json")), &m); err != nil {
			t.Fatal(err)
		}
		for k, v := range m {
			if _, ok := phoneWordKeys[k]; ok {
				continue
			}
			if loc := strings.ReplaceAll(strings.ReplaceAll(k, "iphone", ""), "android_phone", ""); oldTerm.MatchString(loc) {
				t.Errorf("%s: key %q uses the old term", lang, k)
			}
			if oldTerm.MatchString(strings.ReplaceAll(v, "iPhone", "")) {
				t.Errorf("%s: %q says phone: %q", lang, k, v)
			}
		}
		for k := range phoneWordKeys {
			if _, ok := m[k]; !ok {
				t.Errorf("%s: the allow-list names unknown key %q", lang, k)
			}
		}
	}
	templates, err := filepath.Glob(filepath.Join("..", "..", "web", "templates", "*.html"))
	if err != nil || len(templates) == 0 {
		t.Fatalf("no templates: %v", err)
	}
	for _, f := range templates {
		b, _ := os.ReadFile(f)
		if oldTerm.MatchString(strings.ReplaceAll(string(b), "iPhone", "")) || oldTerm.MatchString(filepath.Base(f)) {
			t.Errorf("%s uses the old term", f)
		}
	}
	// The kind of device (#243) is the value "phone", "tablet" or "device": the one place the
	// word is data, not wording.
	spec := strings.ReplaceAll(strings.ReplaceAll(repoFile(t, "api", "openapi.yaml"), "iPhone", ""), "enum: [phone, tablet, device]", "")
	if oldTerm.MatchString(spec) {
		t.Error("api/openapi.yaml uses the old term")
	}
	// Every file that registers routes, not only server.go and api.go.
	files, err := filepath.Glob(filepath.Join("..", "..", "internal", "server", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no server files: %v", err)
	}
	routes := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "HandleFunc(") && !routeTable.MatchString(line) {
				continue
			}
			routes++
			if oldTerm.MatchString(line) {
				t.Errorf("%s:%d: route uses the old term: %s", filepath.Base(f), i+1, strings.TrimSpace(line))
			}
		}
	}
	if routes < 100 {
		t.Errorf("only %d route lines found: the scan misses the routes", routes)
	}
}
