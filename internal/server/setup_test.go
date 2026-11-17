package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
	"rsc.io/qr"
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
	for _, want := range []string{"Dein Konto", "Heimatort", "Eltern-PIN", `lang="de"`, `name="password"`, `value="DE-BY"`, "Konto anlegen"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("setup page misses %q", want)
		}
	}
}

func TestSetupWizardSpeaksEnglish(t *testing.T) {
	b := newBox(t)
	r := b.do(http.MethodGet, "/setup", nil, map[string]string{"Accept-Language": "en-GB,en;q=0.9"})
	for _, want := range []string{"Your account", "Home location", "The town gives the weather and the sunrise and sunset, the state the public holidays.", "Parent PIN", `lang="en"`, "Create account"} {
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
	if page.status != http.StatusOK {
		t.Fatalf("GET /setup/totp = %d\n%s", page.status, page.body)
	}
	secret := secretOn(t, page.body)
	// On a phone the page opens the authenticator app instead; the QR code is for another phone.
	phone := b.do(http.MethodGet, "/setup/totp", nil, map[string]string{"User-Agent": iPhone})
	if !strings.Contains(phone.body, `href="otpauth://totp/Hejmo:mama`) || strings.Contains(phone.body, "data-totp-secret") {
		t.Errorf("phone page lacks the app link or shows the key:\n%s", phone.body)
	}

	r = b.do(http.MethodPost, "/setup/totp", url.Values{"code": {"000000"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("wrong code = %d, want 422", r.status)
	}
	if !strings.Contains(r.body, "That code is wrong") {
		t.Error("wrong code shows no error message")
	}
	code, _ := auth.TOTPCode(secret, b.now)
	if r := b.post("/setup/totp", url.Values{"code": {code}}); r.status != http.StatusOK || !strings.Contains(r.body, `action="/"`) {
		t.Fatalf("right code = %d, want 200 with the recovery codes and a way on\n%s", r.status, r.body)
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
		"no home":        func(f url.Values) { f.Del("place_id") },
		"unknown place":  func(f url.Values) { f.Set("place_id", "42") },
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
			if !strings.Contains(r.body, `class="ferr"`) {
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

var qrModuleRe = regexp.MustCompile(`M(\d+) (\d+)h1v1h-1z`)

// Acceptance (#68): the one-time code setup page shows a QR code that encodes
// exactly the page's otpauth URI, served inline, with a text alternative in both
// locales; key and link remain.
func TestSetupTOTPPageShowsQRCodeOfTheOtpauthURI(t *testing.T) {
	b := newBox(t)
	b.post("/setup", validSetupForm())

	page := b.get("/setup/totp")
	secret := secretOn(t, page.body)
	uri := auth.TOTPURI(secret, "mama")

	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	_, svg, ok := strings.Cut(page.body, `<div class="qr"><svg`) // not the chevron of the language switcher
	if !ok {
		t.Fatalf("no QR code on the page\n%s", page.body)
	}
	svg, _, ok = strings.Cut(svg, "</svg>")
	if !ok {
		t.Fatalf("unterminated QR code on the page\n%s", page.body)
	}
	got := map[[2]int]bool{}
	for _, m := range qrModuleRe.FindAllStringSubmatch(svg, -1) {
		x, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatal(err)
		}
		y, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatal(err)
		}
		got[[2]int{x - 4, y - 4}] = true // 4 modules quiet zone
	}
	dark := 0
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) != got[[2]int{x, y}] {
				t.Fatalf("module (%d,%d) differs from the QR code of %q", x, y, uri)
			}
			if code.Black(x, y) {
				dark++
			}
		}
	}
	if dark != len(got) {
		t.Fatalf("QR has %d dark modules, want %d", len(got), dark)
	}

	en := b.do(http.MethodGet, "/setup/totp", nil, map[string]string{"Accept-Language": "en"})
	for _, want := range []string{secret, "Type the key", `aria-label="QR code of the one-time code key`} {
		if !strings.Contains(en.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	de := b.do(http.MethodGet, "/setup/totp", nil, map[string]string{"Accept-Language": "de-DE"})
	if !strings.Contains(de.body, `aria-label="QR-Code des Einmalcode-Schlüssels`) {
		t.Errorf("German page misses the QR text alternative:\n%s", de.body)
	}
}
