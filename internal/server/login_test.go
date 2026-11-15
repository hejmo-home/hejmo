package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
)

// configuredBox runs the setup wizard and returns the parent's TOTP secret.
func configuredBox(t *testing.T, opts ...boxOption) (*box, string) {
	t.Helper()
	b := newBox(t, opts...)
	b.post("/setup", validSetupForm())
	secret := secretOn(t, b.get("/setup/totp").body)
	code, _ := auth.TOTPCode(secret, b.now)
	if r := b.post("/setup/totp", url.Values{"code": {code}}); r.status != http.StatusOK {
		t.Fatalf("setup failed: %d", r.status)
	}
	b.logout()
	return b, secret
}

func loginForm(b *box, secret, password string) url.Values {
	code, _ := auth.TOTPCode(secret, b.now)
	return url.Values{"username": {"mama"}, "password": {password}, "code": {code}}
}

func TestSignedOutVisitorIsSentToLogin(t *testing.T) {
	b, _ := configuredBox(t)
	if r := b.get("/"); r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("GET / signed out = %d %q, want 303 to /login", r.status, r.location)
	}
	r := b.do(http.MethodGet, "/login", nil, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusOK || !strings.Contains(r.body, `name="code"`) {
		t.Fatalf("login page = %d\n%s", r.status, r.body)
	}
}

func TestLoginWithPasswordAndTOTP(t *testing.T) {
	b, secret := configuredBox(t)
	r := b.post("/login", loginForm(b, secret, "correct horse battery"))
	if r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("login = %d %q\n%s", r.status, r.location, r.body)
	}
	if r := b.get("/"); r.status != http.StatusOK || !strings.Contains(r.body, "Mama") {
		t.Fatalf("home after login = %d", r.status)
	}
	if r := b.post("/logout", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("logout = %d", r.status)
	}
	if r := b.get("/"); r.location != "/login" {
		t.Fatalf("after logout GET / = %d %q", r.status, r.location)
	}
}

func TestLoginRejectsWrongCredentials(t *testing.T) {
	b, secret := configuredBox(t)
	wrongPassword := loginForm(b, secret, "wrong horse battery")
	wrongCode := loginForm(b, secret, "correct horse battery")
	wrongCode.Set("code", "000000")
	unknownUser := loginForm(b, secret, "correct horse battery")
	unknownUser.Set("username", "papa")

	for name, form := range map[string]url.Values{"password": wrongPassword, "code": wrongCode, "user": unknownUser} {
		r := b.post("/login", form)
		if r.status != http.StatusUnauthorized || !strings.Contains(r.body, `class="ferr"`) {
			t.Errorf("wrong %s = %d, want 401 with error", name, r.status)
		}
	}
	if r := b.get("/"); r.location != "/login" {
		t.Fatal("signed in despite wrong credentials")
	}
}

func TestLoginIsRateLimited(t *testing.T) {
	b, secret := configuredBox(t)
	bad := loginForm(b, secret, "wrong horse battery")
	for i := 0; i < 5; i++ {
		b.post("/login", bad)
	}
	r := b.post("/login", loginForm(b, secret, "correct horse battery"))
	if r.status != http.StatusTooManyRequests {
		t.Fatalf("6th attempt = %d, want 429", r.status)
	}
}
