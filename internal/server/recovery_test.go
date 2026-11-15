package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/server"
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
	if !strings.Contains(r.body, "data-otp-reset") || !strings.Contains(r.body, `href="/caldav"`) {
		t.Fatalf("the success page does not remind to revoke paired devices:\n%s", r.body)
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

func TestParentCannotResetTheirOwnCodes(t *testing.T) {
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

func TestChildrenCannotUseTheOneTimeCodePagesOrEndpoints(t *testing.T) {
	b, _, _ := setupWithCodes(t)
	papa, _ := secondParent(t, b)
	mia := childSession(t, b)
	if r := mia.get("/otp"); r.status != http.StatusForbidden {
		t.Fatalf("child GET /otp = %d, want 403", r.status)
	}
	form := url.Values{"password": {"x"}, "code": {"1"}}
	if r := mia.post("/otp/reset/"+papa.ID, form); r.status != http.StatusForbidden {
		t.Fatalf("child reset = %d, want 403", r.status)
	}
	if r := mia.post("/otp/recovery-codes", url.Values{}); r.status != http.StatusForbidden {
		t.Fatalf("child new codes = %d, want 403", r.status)
	}
	if acc, _ := b.db.AccountByPerson(papa.ID); !acc.TOTPConfirmed {
		t.Fatal("a child reset a parent's codes")
	}
	c := newAPIClient(t, mia)
	c.cookie = true
	if status, _, _ := c.call("POST", "/people/"+papa.ID+"/reset-otp", map[string]any{"password": "x", "code": "1"}); status != http.StatusForbidden {
		t.Fatalf("child API reset = %d, want 403", status)
	}
	if status, _, _ := c.call("POST", "/me/recovery-codes", map[string]any{}); status != http.StatusForbidden {
		t.Fatalf("child API new codes = %d, want 403", status)
	}
}

func TestResetOfAnUnknownOrNonParentTargetIsNotFound(t *testing.T) {
	b, mamaSecret, _ := setupWithCodes(t)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	h, _ := b.db.HomeHousehold()
	mia, _ := b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild})
	form := func() url.Values {
		return url.Values{"password": {"correct horse battery"}, "code": {totpNow(b, mamaSecret)}}
	}
	for _, id := range []string{"no-such-person", mia.ID} {
		if r := b.post("/otp/reset/"+id, form()); r.status != http.StatusNotFound {
			t.Errorf("web reset of %q = %d, want 404", id, r.status)
		}
	}
	c := newAPIClient(t, b)
	c.cookie = true
	for _, id := range []string{"no-such-person", mia.ID} {
		if status, _, _ := c.call("POST", "/people/"+id+"/reset-otp", map[string]any{"password": "correct horse battery", "code": totpNow(b, mamaSecret)}); status != http.StatusNotFound {
			t.Errorf("API reset of %q = %d, want 404", id, status)
		}
	}
}

// The reset confirmation also accepts an unused recovery code, which is then spent.
func TestRecoveryCodeConfirmsAResetAndIsSpent(t *testing.T) {
	b, mamaSecret, codes := setupWithCodes(t)
	papa, _ := secondParent(t, b)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	r := b.post("/otp/reset/"+papa.ID, url.Values{"password": {"correct horse battery"}, "code": {codes[0]}})
	if r.status != http.StatusOK {
		t.Fatalf("reset with a recovery code = %d\n%s", r.status, r.body)
	}
	if acc, _ := b.db.AccountByPerson(papa.ID); acc.TOTPConfirmed {
		t.Fatal("Papa's codes were not reset")
	}
	b.logout()
	if r := loginWith(b, "mama", "correct horse battery", codes[0]); r.status != http.StatusUnauthorized {
		t.Fatalf("recovery code used again = %d, want 401", r.status)
	}
}

// After five failed confirmations the web form and the API both answer 429,
// even with the right password and code.
func TestResetConfirmationIsRateLimitedOnWebAndAPI(t *testing.T) {
	b, mamaSecret, _ := setupWithCodes(t)
	papa, _ := secondParent(t, b)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	bad := url.Values{"password": {"wrong horse battery"}, "code": {"000000"}}
	for i := 0; i < 5; i++ {
		if r := b.post("/otp/reset/"+papa.ID, bad); r.status != http.StatusUnprocessableEntity {
			t.Fatalf("failed attempt %d = %d, want 422", i+1, r.status)
		}
	}
	good := url.Values{"password": {"correct horse battery"}, "code": {totpNow(b, mamaSecret)}}
	r := b.post("/otp/reset/"+papa.ID, good)
	if r.status != http.StatusTooManyRequests || !strings.Contains(r.body, "15") {
		t.Fatalf("web after the limit = %d, want 429\n%s", r.status, r.body)
	}
	c := newAPIClient(t, b)
	c.cookie = true
	if status, _, _ := c.call("POST", "/people/"+papa.ID+"/reset-otp", map[string]any{"password": "correct horse battery", "code": totpNow(b, mamaSecret)}); status != http.StatusTooManyRequests {
		t.Fatalf("API after the limit = %d, want 429", status)
	}
	if acc, _ := b.db.AccountByPerson(papa.ID); !acc.TOTPConfirmed {
		t.Fatal("a limited attempt reset the codes")
	}
}

// The error appears at the form that failed, not at page level.
func TestResetErrorShowsAtTheFailingParent(t *testing.T) {
	b, mamaSecret, _ := setupWithCodes(t)
	papa, _ := secondParent(t, b)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	r := b.post("/otp/reset/"+papa.ID, url.Values{"password": {"wrong horse battery"}, "code": {"000000"}})
	form := r.body[strings.Index(r.body, `action="/otp/reset/`+papa.ID):]
	form = form[:strings.Index(form, "</form>")]
	if !strings.Contains(form, `role="alert"`) {
		t.Fatalf("no error inside the form of the failing parent:\n%s", r.body)
	}
	if n := strings.Count(r.body, `role="alert"`); n != 1 {
		t.Fatalf("%d errors on the page, want 1", n)
	}
}

func TestRecoveryCodeCountReadsRightForOne(t *testing.T) {
	b, mamaSecret, _ := setupWithCodes(t)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	mama, _ := b.db.AccountByUsername("mama")
	_ = b.db.ReplaceRecoveryCodes(mama.PersonID, []string{auth.HashRecoveryCode("AAAAA-BBBBB")})
	if page := b.get("/otp").body; strings.Contains(page, "1 recovery codes") || strings.Contains(page, "1 Wiederherstellungscodes") {
		t.Fatalf("grammar of count 1:\n%s", page)
	}
}

// Parity: the same through the REST API, contract-checked against openapi.yaml.
func TestRecoveryAndResetThroughTheAPI(t *testing.T) {
	b, mamaSecret, _ := setupWithCodes(t)
	papa, _ := secondParent(t, b)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	c := newAPIClient(t, b)
	c.cookie = true

	status, out, _ := c.call("POST", "/me/recovery-codes", map[string]any{})
	codes, _ := out["codes"].([]any)
	if status != http.StatusCreated || len(codes) != 10 {
		t.Fatalf("POST /me/recovery-codes = %d %v", status, out)
	}
	if status, _, _ := c.call("POST", "/people/"+papa.ID+"/reset-otp", map[string]any{"password": "nope nope nope", "code": totpNow(b, mamaSecret)}); status != http.StatusUnprocessableEntity {
		t.Fatalf("reset with wrong password = %d, want 422", status)
	}
	if status, _, _ := c.call("POST", "/people/"+papa.ID+"/reset-otp", map[string]any{"password": "correct horse battery", "code": totpNow(b, mamaSecret)}); status != http.StatusNoContent {
		t.Fatalf("reset = %d, want 204", status)
	}
	if acc, _ := b.db.AccountByPerson(papa.ID); acc.TOTPConfirmed {
		t.Fatal("Papa's codes were not reset")
	}
	mama, _ := b.db.AccountByUsername("mama")
	if status, _, _ := c.call("POST", "/people/"+mama.PersonID+"/reset-otp", map[string]any{"password": "correct horse battery", "code": totpNow(b, mamaSecret)}); status != http.StatusForbidden {
		t.Fatalf("own reset = %d, want 403", status)
	}
}

// Review round 3: how long a sign-in takes must not tell whether the password
// is right, so a recovery-shaped code costs the same number of verifies in
// every case (counted through a seam, not timed).
func TestRecoveryCodeSignInDoesTheSameWorkWhateverTheOutcome(t *testing.T) {
	b, _, codes := setupWithCodes(t)
	verifies := func(user, password, code string) (int, int) {
		stop := server.CountRecoveryVerifies()
		r := loginWith(b, user, password, code)
		return stop(), r.status
	}
	wrongPw, _ := verifies("mama", "wrong horse battery", "ZZZZZ-ZZZZZ")
	if wrongPw == 0 {
		t.Fatal("wrong password did no recovery verifies: the work would differ from a right password")
	}
	cases := []struct{ name, user, pw, code string }{
		{"right password, wrong code", "mama", "correct horse battery", "ZZZZZ-ZZZZZ"},
		{"right password, valid code", "mama", "correct horse battery", codes[0]},
		{"wrong password, valid code", "mama", "wrong horse battery", codes[1]},
		{"unknown account", "nobody", "whatever it is", "ZZZZZ-ZZZZZ"},
	}
	for _, c := range cases {
		if got, _ := verifies(c.user, c.pw, c.code); got != wrongPw {
			t.Errorf("%s: %d verifies, want %d (same as wrong password)", c.name, got, wrongPw)
		}
	}
	// Fewer codes left: still the same number.
	if got, _ := verifies("mama", "correct horse battery", "ZZZZZ-ZZZZZ"); got != wrongPw {
		t.Errorf("with nine codes left: %d verifies, want %d", got, wrongPw)
	}
	// A wrong password must not spend the code.
	if r := loginWith(b, "mama", "correct horse battery", codes[1]); r.status != http.StatusSeeOther {
		t.Errorf("code spent by a sign-in with the wrong password: %d", r.status)
	}
}

// Same for the reset confirmation: a recovery-shaped code costs the same
// number of verifies whether or not the password is right.
func TestResetConfirmationDoesTheSameWorkWhateverTheOutcome(t *testing.T) {
	b, mamaSecret, codes := setupWithCodes(t)
	papa, _ := secondParent(t, b)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	verifies := func(password, code string) int {
		stop := server.CountRecoveryVerifies()
		b.post("/otp/reset/"+papa.ID, url.Values{"password": {password}, "code": {code}})
		return stop()
	}
	wrongPw := verifies("wrong horse battery", "ZZZZZ-ZZZZZ")
	if wrongPw == 0 {
		t.Fatal("wrong password did no recovery verifies: the work would differ from a right password")
	}
	for _, c := range []struct{ name, pw, code string }{
		{"right password, wrong code", "correct horse battery", "ZZZZZ-ZZZZZ"},
		{"wrong password, valid code", "wrong horse battery", codes[0]},
		{"right password, valid code", "correct horse battery", codes[0]},
	} {
		if got := verifies(c.pw, c.code); got != wrongPw {
			t.Errorf("%s: %d verifies, want %d (same as wrong password)", c.name, got, wrongPw)
		}
	}
}

// A person whose codes were reset may only set new ones up: a still-valid API
// token must not mint recovery codes for the account in that state.
func TestPendingAccountCannotMintRecoveryCodesWithAToken(t *testing.T) {
	b, secret, _ := setupWithCodes(t)
	loginWith(b, "mama", "correct horse battery", totpNow(b, secret))
	session := newAPIClient(t, b)
	session.cookie = true
	token := session.withToken(session.newToken("write"))
	// Both answers are checked against openapi.yaml by call.
	if status, body, _ := token.call("POST", "/me/recovery-codes", nil); status != http.StatusCreated {
		t.Fatalf("confirmed account = %d %v, want 201", status, body)
	}
	mama, _ := b.db.AccountByUsername("mama")
	if err := b.db.ResetOTP(mama.PersonID); err != nil {
		t.Fatal(err)
	}
	if status, body, _ := token.call("POST", "/me/recovery-codes", nil); status != http.StatusForbidden {
		t.Fatalf("account pending re-setup = %d %v, want 403", status, body)
	}
}

// The wizard ends on the recovery codes; the welcome line follows on the home page.
func TestSetupWelcomeFollowsTheRecoveryCodes(t *testing.T) {
	b := newBox(t)
	b.post("/setup", validSetupForm())
	secret := secretOn(t, b.get("/setup/totp").body)
	code, _ := auth.TOTPCode(secret, b.now)
	r := b.post("/setup/totp", url.Values{"code": {code}})
	if len(recoveryCodesOn(t, r.body)) != 10 {
		t.Fatalf("no recovery codes after setup\n%s", r.body)
	}
	if m := successRe.FindStringSubmatch(b.get("/").body); m == nil || !strings.Contains(m[1], "Willkommen") {
		t.Fatalf("home shows no welcome line after the recovery codes: %v", m)
	}
}

func TestRecoveryCodesPageAndResetFormsUseSharedPatterns(t *testing.T) {
	b, secret, _ := setupWithCodes(t)
	loginWith(b, "mama", "correct horse battery", totpNow(b, secret))
	secondParent(t, b)
	r := b.post("/otp/recovery-codes", url.Values{})
	if !successRe.MatchString(r.body) {
		t.Fatalf("recovery codes page shows no success line\n%s", r.body)
	}
	if !strings.Contains(b.get("/otp").body, "oder einem Wiederherstellungscode") {
		t.Fatal("the reset intro does not mention that a recovery code works")
	}
}
