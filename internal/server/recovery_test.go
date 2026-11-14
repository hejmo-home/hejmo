package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

var recoveryRe = regexp.MustCompile(`data-recovery-code="([A-Z2-9]{5}-[A-Z2-9]{5})"`)

func recoveryCodesOn(t *testing.T, page string) []string {
	t.Helper()
	var codes []string
	for _, m := range recoveryRe.FindAllStringSubmatch(page, -1) {
		codes = append(codes, m[1])
	}
	return codes
}

// setupWithCodes runs the wizard and returns the parent's secret and recovery codes.
func setupWithCodes(t *testing.T) (*box, string, []string) {
	t.Helper()
	b := newBox(t)
	b.post("/setup", validSetupForm())
	secret := secretOn(t, b.get("/setup/totp").body)
	code, _ := auth.TOTPCode(secret, b.now)
	r := b.post("/setup/totp", url.Values{"code": {code}})
	codes := recoveryCodesOn(t, r.body)
	b.logout()
	return b, secret, codes
}

func loginWith(b *box, user, password, code string) resp {
	return b.post("/login", url.Values{"username": {user}, "password": {password}, "code": {code}})
}

// 5.20: when one-time codes are set up, the box shows 10 recovery codes.
func TestSetupShowsTenRecoveryCodesOnce(t *testing.T) {
	b := newBox(t)
	b.post("/setup", validSetupForm())
	secret := secretOn(t, b.get("/setup/totp").body)
	code, _ := auth.TOTPCode(secret, b.now)
	r := b.post("/setup/totp", url.Values{"code": {code}})
	codes := recoveryCodesOn(t, r.body)
	if len(codes) != 10 {
		t.Fatalf("setup shows %d recovery codes, want 10\n%s", len(codes), r.body)
	}
	if page := b.get("/otp").body; len(recoveryCodesOn(t, page)) != 0 || strings.Contains(page, codes[0]) {
		t.Fatal("recovery codes are shown again after the first time")
	}
}

// 5.20: a recovery code signs in once; the code is spent.
func TestRecoveryCodeSignsInOnceAndNeedsThePassword(t *testing.T) {
	b, _, codes := setupWithCodes(t)
	if r := loginWith(b, "mama", "wrong horse battery", codes[0]); r.status != http.StatusUnauthorized {
		t.Fatalf("recovery code with wrong password = %d, want 401", r.status)
	}
	if r := loginWith(b, "mama", "correct horse battery", strings.ToLower(codes[0])); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("recovery code sign-in = %d %q\n%s", r.status, r.location, r.body)
	}
	if r := b.get("/"); r.status != http.StatusOK {
		t.Fatalf("not signed in after recovery code: %d", r.status)
	}
	b.logout()
	if r := loginWith(b, "mama", "correct horse battery", codes[0]); r.status != http.StatusUnauthorized {
		t.Fatalf("spent recovery code = %d, want 401", r.status)
	}
	if r := loginWith(b, "mama", "correct horse battery", codes[1]); r.status != http.StatusSeeOther {
		t.Fatalf("second recovery code = %d, want 303", r.status)
	}
}

// 5.20: recovery codes are stored hashed.
func TestRecoveryCodesAreStoredHashed(t *testing.T) {
	b, _, codes := setupWithCodes(t)
	rows, err := b.db.SQL().Query(`SELECT code_hash FROM recovery_codes`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var h string
		_ = rows.Scan(&h)
		n++
		if !strings.HasPrefix(h, "$argon2id$") {
			t.Errorf("stored value %q is not an Argon2id hash", h)
		}
		for _, c := range codes {
			if strings.Contains(h, c) || strings.Contains(h, strings.ReplaceAll(c, "-", "")) {
				t.Errorf("stored value contains a code")
			}
		}
	}
	if n != 10 {
		t.Fatalf("%d stored codes, want 10", n)
	}
}

func TestNewSetOfRecoveryCodesVoidsTheOldOne(t *testing.T) {
	b, secret, old := setupWithCodes(t)
	loginWith(b, "mama", "correct horse battery", func() string { c, _ := auth.TOTPCode(secret, b.now); return c }())
	r := b.post("/otp/recovery-codes", url.Values{})
	fresh := recoveryCodesOn(t, r.body)
	if r.status != http.StatusOK || len(fresh) != 10 {
		t.Fatalf("new set = %d with %d codes\n%s", r.status, len(fresh), r.body)
	}
	b.logout()
	if r := loginWith(b, "mama", "correct horse battery", old[0]); r.status != http.StatusUnauthorized {
		t.Fatalf("code of the old set = %d, want 401", r.status)
	}
	if r := loginWith(b, "mama", "correct horse battery", fresh[0]); r.status != http.StatusSeeOther {
		t.Fatalf("code of the new set = %d, want 303", r.status)
	}
}

// secondParent adds Papa with a confirmed account and returns his secret.
func secondParent(t *testing.T, b *box) (store.Person, string) {
	t.Helper()
	h, _ := b.db.HomeHousehold()
	papa, err := b.db.AddPerson(h.ID, store.Person{Name: "Papa", Role: store.RoleParent, Color: "#2F5E7A", Shape: "circle-empty"})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("papa's long password")
	sec := auth.NewTOTPSecret()
	if err := b.db.CreateAccount(store.Account{PersonID: papa.ID, Username: "papa", PasswordHash: hash, TOTPSecret: sec}); err != nil {
		t.Fatal(err)
	}
	_ = b.db.ConfirmTOTP(papa.ID)
	_ = b.db.ReplaceRecoveryCodes(papa.ID, []string{auth.HashRecoveryCode("AAAAA-BBBBB")})
	return papa, sec
}

func totpNow(b *box, secret string) string { c, _ := auth.TOTPCode(secret, b.now); return c }

// 5.20: when parent A resets parent B's one-time codes, B sets them up again at the next sign-in.
func TestParentResetsAnotherParentsOneTimeCodes(t *testing.T) {
	b, mamaSecret, _ := setupWithCodes(t)
	papa, papaSecret := secondParent(t, b)
	// Papa is signed in on a browser when Mama resets him.
	loginWith(b, "papa", "papa's long password", totpNow(b, papaSecret))
	papaJar := b.client.Jar
	b.logout()
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))

	if page := b.get("/otp").body; !strings.Contains(page, "/otp/reset/"+papa.ID) || strings.Contains(page, "/otp/reset/"+papa.ID+"x") {
		t.Fatalf("the page offers no reset for the other parent:\n%s", page)
	}
	wrong := url.Values{"password": {"wrong horse battery"}, "code": {totpNow(b, mamaSecret)}}
	if r := b.post("/otp/reset/"+papa.ID, wrong); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("reset with a wrong password = %d, want 422", r.status)
	}
	if acc, _ := b.db.AccountByPerson(papa.ID); !acc.TOTPConfirmed {
		t.Fatal("reset went through without the right password")
	}
	own := url.Values{"password": {"correct horse battery"}, "code": {totpNow(b, mamaSecret)}}
	r := b.post("/otp/reset/"+papa.ID, own)
	if r.status != http.StatusOK || !strings.Contains(r.body, "Papa") {
		t.Fatalf("reset = %d\n%s", r.status, r.body)
	}

	// Papa's browser session ended; his password alone leads to a new setup.
	b.client.Jar = papaJar
	if r := b.get("/"); r.status != http.StatusSeeOther {
		t.Fatalf("Papa's old session still works: %d", r.status)
	}
	b.logout()
	r = loginWith(b, "papa", "papa's long password", "")
	if r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		t.Fatalf("sign-in after reset = %d %q, want 303 to /setup/totp\n%s", r.status, r.location, r.body)
	}
	// Nothing else is reachable until the new codes are confirmed.
	if r := b.get("/"); r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("home before re-setup = %d %q", r.status, r.location)
	}
	if r := b.get("/api/v1/me"); r.status != http.StatusUnauthorized {
		t.Fatalf("API before re-setup = %d", r.status)
	}
	page := b.get("/setup/totp")
	newSecret := secretOn(t, page.body)
	if newSecret == papaSecret {
		t.Fatal("the old secret was kept")
	}
	if r := b.post("/setup/totp", url.Values{"code": {totpNow(b, papaSecret)}}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("old secret accepted at re-setup: %d", r.status)
	}
	r = b.post("/setup/totp", url.Values{"code": {totpNow(b, newSecret)}})
	codes := recoveryCodesOn(t, r.body)
	if r.status != http.StatusOK || len(codes) != 10 {
		t.Fatalf("re-setup = %d with %d codes", r.status, len(codes))
	}
	if r := b.get("/"); r.status != http.StatusOK {
		t.Fatalf("home after re-setup = %d", r.status)
	}
	b.logout()
	if r := loginWith(b, "papa", "papa's long password", totpNow(b, papaSecret)); r.status != http.StatusUnauthorized {
		t.Fatal("old one-time codes still sign in")
	}
	if r := loginWith(b, "papa", "papa's long password", codes[0]); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("new recovery code = %d %q", r.status, r.location)
	}
}

func TestParentCannotResetTheirOwnCodesAndChildrenCannotReset(t *testing.T) {
	b, mamaSecret, _ := setupWithCodes(t)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	mama, _ := b.db.AccountByUsername("mama")
	own := url.Values{"password": {"correct horse battery"}, "code": {totpNow(b, mamaSecret)}}
	if r := b.post("/otp/reset/"+mama.PersonID, own); r.status != http.StatusForbidden {
		t.Fatalf("own reset = %d, want 403", r.status)
	}
	if acc, _ := b.db.AccountByPerson(mama.PersonID); !acc.TOTPConfirmed {
		t.Fatal("own codes were reset")
	}
}

// Parity: the same through the REST API.
func TestRecoveryAndResetThroughTheAPI(t *testing.T) {
	b, mamaSecret, _ := setupWithCodes(t)
	papa, _ := secondParent(t, b)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))

	call := func(path string, body any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, b.srv.URL+"/api/v1"+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", b.srv.URL)
		res, err := b.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	status, out := call("/me/recovery-codes", map[string]any{})
	codes, _ := out["codes"].([]any)
	if status != http.StatusCreated || len(codes) != 10 {
		t.Fatalf("POST /me/recovery-codes = %d %v", status, out)
	}
	if status, _ := call("/people/"+papa.ID+"/reset-otp", map[string]any{"password": "nope nope nope", "code": totpNow(b, mamaSecret)}); status != http.StatusUnprocessableEntity {
		t.Fatalf("reset with wrong password = %d, want 422", status)
	}
	if status, _ := call("/people/"+papa.ID+"/reset-otp", map[string]any{"password": "correct horse battery", "code": totpNow(b, mamaSecret)}); status != http.StatusNoContent {
		t.Fatalf("reset = %d, want 204", status)
	}
	if acc, _ := b.db.AccountByPerson(papa.ID); acc.TOTPConfirmed {
		t.Fatal("Papa's codes were not reset")
	}
	mama, _ := b.db.AccountByUsername("mama")
	if status, _ := call("/people/"+mama.PersonID+"/reset-otp", map[string]any{"password": "correct horse battery", "code": totpNow(b, mamaSecret)}); status != http.StatusForbidden {
		t.Fatalf("own reset = %d, want 403", status)
	}
}
