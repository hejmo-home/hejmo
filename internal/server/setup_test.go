package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Acceptance (Platform): Given a running box without configuration, when the
// web UI is opened, then the setup wizard starts (home location, first parent
// account, parent PIN).
func TestUnconfiguredBoxStartsSetupWizard(t *testing.T) {
	b := newBox(t)
	if r := b.get("/"); r.status != http.StatusSeeOther || r.location != "/setup" {
		t.Fatalf("GET / = %d %q, want 303 to /setup", r.status, r.location)
	}
	r := b.do(http.MethodGet, "/setup", nil, map[string]string{"Accept-Language": "de-DE"})
	for _, want := range []string{"Hejmo einrichten", "Heimatort", "Erstes Elternkonto", "Eltern-PIN", `lang="de"`, `name="password"`, `value="DE-BY"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("setup page misses %q", want)
		}
	}
}

func TestSetupWizardSpeaksEnglish(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodGet, "/setup", nil, map[string]string{"Accept-Language": "en-GB,en;q=0.9"})
	for _, want := range []string{"Set up Hejmo", "Home location", "First parent account", "Parent PIN", `lang="en"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("setup page misses %q", want)
		}
	}
}

func TestSetupSavesAndConfirmsTOTP(t *testing.T) {
	b := newBox(t)

	r := b.post("/setup", validSetupForm())
	if r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		t.Fatalf("POST /setup = %d %q, want 303 to /setup/totp\n%s", r.status, r.location, r.body)
	}
	if b.db.Configured() {
		t.Fatal("configured before TOTP confirmation")
	}
	h, err := b.db.HomeHousehold()
	if err != nil || h.HomeName != "Pasing" || h.Region != "DE-BY" {
		t.Fatalf("household = %+v, %v", h, err)
	}
	people, _ := b.db.People(h.ID)
	if len(people) != 1 || people[0].Name != "Mama" || people[0].Role != store.RoleParent {
		t.Fatalf("people = %+v", people)
	}
	pinHash, _ := b.db.DisplayPINHash(h.ID)
	if !auth.VerifyPIN(pinHash, "2468") {
		t.Error("display PIN not stored")
	}

	page := b.get("/setup/totp")
	if page.status != http.StatusOK || !strings.Contains(page.body, "otpauth://totp/Hejmo:mama") {
		t.Fatalf("GET /setup/totp = %d\n%s", page.status, page.body)
	}
	secret := secretOn(t, page.body)

	if r := b.post("/setup/totp", url.Values{"code": {"000000"}}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("wrong code = %d, want 422", r.status)
	}
	code, _ := auth.TOTPCode(secret, b.now)
	if r := b.post("/setup/totp", url.Values{"code": {code}}); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("right code = %d %q, want 303 to /", r.status, r.location)
	}
	if !b.db.Configured() {
		t.Fatal("not configured after setup")
	}
	if r := b.get("/"); r.status != http.StatusOK || !strings.Contains(r.body, "Mama") {
		t.Fatalf("home after setup = %d\n%s", r.status, r.body)
	}
	if r := b.get("/setup"); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("GET /setup on configured box = %d %q", r.status, r.location)
	}
	if r := b.post("/setup", validSetupForm()); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("POST /setup on configured box = %d %q", r.status, r.location)
	}
}

func TestSetupRejectsInvalidInput(t *testing.T) {
	cases := map[string]func(url.Values){
		"short password": func(f url.Values) { f.Set("password", "short") },
		"bad PIN":        func(f url.Values) { f.Set("pin", "12a") },
		"no name":        func(f url.Values) { f.Set("name", " ") },
		"no username":    func(f url.Values) { f.Set("username", "") },
		"unknown region": func(f url.Values) { f.Set("region", "XX-YY") },
		"no home":        func(f url.Values) { f.Set("home_name", "") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b := newBox(t)
			form := validSetupForm()
			mutate(form)
			r := b.post("/setup", form)
			if r.status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", r.status)
			}
			if !strings.Contains(r.body, `class="error"`) {
				t.Error("no error message shown")
			}
			if _, err := b.db.HomeHousehold(); err == nil {
				t.Error("invalid setup stored a household")
			}
		})
	}
}

func TestUnfinishedSetupCanStartOver(t *testing.T) {
	b := newBox(t)
	b.post("/setup", validSetupForm())
	b.logout()

	form := validSetupForm()
	form.Set("name", "Papa")
	form.Set("username", "papa")
	if r := b.post("/setup", form); r.status != http.StatusSeeOther {
		t.Fatalf("restart setup = %d\n%s", r.status, r.body)
	}
	h, _ := b.db.HomeHousehold()
	people, _ := b.db.People(h.ID)
	if len(people) != 1 || people[0].Name != "Papa" {
		t.Fatalf("people after restart = %+v", people)
	}
}

func TestTOTPStepNeedsTheSetupSession(t *testing.T) {
	b := newBox(t)
	b.post("/setup", validSetupForm())
	b.logout()
	if r := b.get("/setup/totp"); r.status != http.StatusSeeOther || r.location != "/setup" {
		t.Fatalf("GET /setup/totp without session = %d %q", r.status, r.location)
	}
}

func TestFormsRejectForeignOrigin(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodPost, "/setup", validSetupForm(), map[string]string{"Origin": "http://evil.example"})
	if r.status != http.StatusForbidden {
		t.Fatalf("foreign origin = %d, want 403", r.status)
	}
	r = b.do(http.MethodPost, "/setup", validSetupForm(), map[string]string{"Origin": ""})
	if r.status != http.StatusForbidden {
		t.Fatalf("missing origin = %d, want 403", r.status)
	}
}
