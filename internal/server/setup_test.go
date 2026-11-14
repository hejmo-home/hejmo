package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/server"
)

type fakeSetup struct{ configured bool }

func (f fakeSetup) Configured() bool { return f.configured }

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Acceptance (Platform): Given a running box without configuration, when the
// web UI is opened, then the setup wizard starts (home location, first parent
// account, parent PIN).
func TestUnconfiguredBoxStartsSetupWizard(t *testing.T) {
	srv := httptest.NewServer(server.New(server.Options{Setup: fakeSetup{configured: false}}))
	defer srv.Close()
	client := &http.Client{CheckRedirect: noRedirect}

	res, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/setup" {
		t.Fatalf("GET / = %d %q, want 303 to /setup", res.StatusCode, res.Header.Get("Location"))
	}

	body := get(t, srv.URL+"/setup", "de-DE,de;q=0.9")
	for _, want := range []string{"Hejmo einrichten", "Heimatort", "Erstes Elternkonto", "Eltern-PIN", `lang="de"`} {
		if !strings.Contains(body, want) {
			t.Errorf("setup page misses %q", want)
		}
	}
}

func TestSetupWizardSpeaksEnglish(t *testing.T) {
	srv := httptest.NewServer(server.New(server.Options{Setup: fakeSetup{configured: false}}))
	defer srv.Close()

	body := get(t, srv.URL+"/setup", "en-GB,en;q=0.9")
	for _, want := range []string{"Set up Hejmo", "Home location", "First parent account", "Parent PIN", `lang="en"`} {
		if !strings.Contains(body, want) {
			t.Errorf("setup page misses %q", want)
		}
	}
}

func TestConfiguredBoxSkipsSetupWizard(t *testing.T) {
	srv := httptest.NewServer(server.New(server.Options{Setup: fakeSetup{configured: true}}))
	defer srv.Close()
	client := &http.Client{CheckRedirect: noRedirect}

	for _, path := range []string{"/", "/setup"} {
		res, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusSeeOther && res.Header.Get("Location") == "/setup" {
			t.Errorf("GET %s redirects to setup on a configured box", path)
		}
		if path == "/setup" && res.StatusCode != http.StatusSeeOther {
			t.Errorf("GET /setup = %d, want 303 away from the wizard", res.StatusCode)
		}
	}
}

func get(t *testing.T, url, acceptLanguage string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Accept-Language", acceptLanguage)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", url, res.StatusCode)
	}
	b, _ := io.ReadAll(res.Body)
	return string(b)
}
