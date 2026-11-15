package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/web"
)

// Ticket 134: the language switcher in the header.

var htmlLangRe = regexp.MustCompile(`<html lang="([a-z]+)"`)

func pageLang(t *testing.T, body string) string {
	t.Helper()
	m := htmlLangRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no lang attribute on <html>:\n%s", body)
	}
	return m[1]
}

func langForm(code, next string) url.Values { return url.Values{"lang": {code}, "next": {next}} }

func TestSwitchingBeforeSignInSetsCookieAndShowsPageInNewLanguage(t *testing.T) {
	b, _ := configuredBox(t)
	r := b.post("/language", langForm("en", "/login"))
	if r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("POST /language = %d %q, want 303 to /login", r.status, r.location)
	}
	page := b.do(http.MethodGet, "/login", nil, map[string]string{"Accept-Language": "de"})
	if got := pageLang(t, page.body); got != "en" {
		t.Fatalf("page lang = %q, want en", got)
	}
	if !strings.Contains(page.body, "Language changed to English. This device remembers your choice.") {
		t.Fatalf("no success line:\n%s", page.body)
	}
	if !strings.Contains(page.body, "Sign in") {
		t.Fatalf("login page is not English:\n%s", page.body)
	}
	// The line is shown once.
	if again := b.get("/login"); strings.Contains(again.body, "This device remembers") {
		t.Fatal("success line shown twice")
	}
}

func TestLanguageCookieAttributes(t *testing.T) {
	b, _ := configuredBox(t)
	req, _ := http.NewRequest(http.MethodPost, b.srv.URL+"/language", strings.NewReader(langForm("en", "/").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", b.srv.URL)
	noRedirect := b.bareClient()
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var c *http.Cookie
	for _, k := range res.Cookies() {
		if k.Name == "hejmo_lang" {
			c = k
		}
	}
	if c == nil || c.Value != "en" || c.Path != "/" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 365*24*3600 || !c.Secure {
		t.Fatalf("cookie over HTTPS = %+v", c)
	}
	// The trust page runs over plain HTTP, so the cookie cannot be Secure there.
	plain := b.plainServer()
	req, _ = http.NewRequest(http.MethodPost, plain.URL+"/language", strings.NewReader(langForm("de", "/ca").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", plain.URL)
	res2, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	for _, k := range res2.Cookies() {
		if k.Name == "hejmo_lang" && k.Secure {
			t.Fatalf("cookie over HTTP is Secure: %+v", k)
		}
	}
}

func TestLanguageRejectsForeignNextAndUnknownCodes(t *testing.T) {
	b, _ := configuredBox(t)
	for _, next := range []string{"https://evil.example/", "//evil.example/x", `/\evil.example`, "evil", ""} {
		if r := b.post("/language", langForm("en", next)); r.status != http.StatusSeeOther || r.location != "/" {
			t.Errorf("next %q: %d %q, want 303 to /", next, r.status, r.location)
		}
	}
	b.logout()
	for _, code := range []string{"fr", "", "EN", "../de", "de;x"} {
		if r := b.post("/language", langForm(code, "/login")); r.status != http.StatusBadRequest {
			t.Errorf("code %q: status %d, want 400", code, r.status)
		}
	}
	page := b.do(http.MethodGet, "/login", nil, map[string]string{"Accept-Language": "fr"})
	if got := pageLang(t, page.body); got != "de" {
		t.Fatalf("lang after refused codes = %q, want de", got)
	}
}

func TestLanguageKeepsAQueryOnTheNextPath(t *testing.T) {
	b := signedInBox(t)
	if r := b.post("/language", langForm("en", "/calendar?view=week")); r.location != "/calendar?view=week" {
		t.Fatalf("location = %q", r.location)
	}
}

func TestSignedInSwitchIsSavedOnTheAccount(t *testing.T) {
	b, secret := configuredBox(t)
	b.post("/login", loginForm(b, secret, "correct horse battery"))
	b.post("/language", langForm("en", "/"))
	acc, err := b.db.AccountByUsername("mama")
	if err != nil || acc.Language != "en" {
		t.Fatalf("account language = %q, %v", acc.Language, err)
	}
	page := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "de"})
	if pageLang(t, page.body) != "en" || !strings.Contains(page.body, "Language changed to English and saved for your account. Displays and the children&#39;s phones keep their language.") {
		t.Fatalf("home after switch:\n%s", page.body)
	}
	// On another device without the cookie, the account's language applies.
	b.logout()
	r := b.post("/login", loginForm(b, secret, "correct horse battery"))
	if r.status != http.StatusSeeOther {
		t.Fatalf("login = %d", r.status)
	}
	other := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "de"})
	if got := pageLang(t, other.body); got != "en" {
		t.Fatalf("lang on a new device = %q, want en", got)
	}
	if got := other.body; !strings.Contains(got, "Sign out") {
		t.Fatalf("not English:\n%s", got)
	}
}

func TestAccountLanguageCannotBeSavedShowsError(t *testing.T) {
	b := signedInBox(t)
	if _, err := b.db.SQL().Exec(`CREATE TRIGGER no_lang BEFORE UPDATE OF language ON accounts BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	r := b.post("/language", langForm("en", "/"))
	if r.status != http.StatusSeeOther {
		t.Fatalf("status = %d", r.status)
	}
	page := b.get("/")
	if pageLang(t, page.body) != "en" {
		t.Fatalf("the device still switches:\n%s", page.body)
	}
	if !strings.Contains(page.body, `role="alert"`) || !strings.Contains(page.body, "Language changed on this device only: it could not be saved for your account. Try again later.") {
		t.Fatalf("no error line:\n%s", page.body)
	}
	if strings.Contains(page.body, "Language changed to English and saved") {
		t.Fatal("success line shown with the error")
	}
}

func TestLanguagePrecedence(t *testing.T) {
	b, secret := configuredBox(t)
	get := func(cookie, accept string) string {
		t.Helper()
		h := map[string]string{}
		if cookie != "" {
			h["Cookie"] = "hejmo_lang=" + cookie
		}
		if accept != "" {
			h["Accept-Language"] = accept
		}
		return pageLang(t, b.do(http.MethodGet, "/login", nil, h).body)
	}
	for _, tc := range []struct{ cookie, accept, want string }{
		{"", "", "de"},
		{"", "en-GB,en;q=0.9", "en"},
		{"", "fr-FR,fr;q=0.9", "de"},
		{"de", "en", "de"},
		{"en", "de", "en"},
		{"xx", "en", "en"}, // an unknown cookie value is ignored
	} {
		if got := get(tc.cookie, tc.accept); got != tc.want {
			t.Errorf("cookie %q, Accept-Language %q: lang = %q, want %q", tc.cookie, tc.accept, got, tc.want)
		}
	}
	// Account beats cookie.
	b.post("/login", loginForm(b, secret, "correct horse battery"))
	acc, _ := b.db.AccountByUsername("mama")
	if err := b.db.SetAccountLanguage(acc.PersonID, "en"); err != nil {
		t.Fatal(err)
	}
	page := b.do(http.MethodGet, "/", nil, map[string]string{"Cookie": "hejmo_lang=de", "Accept-Language": "de"})
	if got := pageLang(t, page.body); got != "en" {
		t.Fatalf("account en vs cookie de: lang = %q, want en", got)
	}
}

func TestContentLanguageFollowsTheChoice(t *testing.T) {
	b, _ := configuredBox(t)
	req, _ := http.NewRequest(http.MethodGet, b.srv.URL+"/login", nil)
	req.Header.Set("Cookie", "hejmo_lang=en")
	res, err := b.bareClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if got := res.Header.Get("Content-Language"); got != "en" {
		t.Fatalf("Content-Language = %q", got)
	}
}

func TestSignInCopiesTheCookieChoiceToAnAccountWithNone(t *testing.T) {
	b, secret := configuredBox(t)
	b.post("/language", langForm("en", "/login"))
	b.post("/login", loginForm(b, secret, "correct horse battery"))
	acc, _ := b.db.AccountByUsername("mama")
	if acc.Language != "en" {
		t.Fatalf("account language after sign-in = %q, want en", acc.Language)
	}
	// An account that has a language keeps it.
	b.logout()
	_ = b.db.SetAccountLanguage(acc.PersonID, "de")
	b.post("/language", langForm("en", "/login")) // cookie en, but sign-in must not overwrite
	b.logout()
	b.client.Jar.SetCookies(mustURL(t, b.srv.URL), []*http.Cookie{{Name: "hejmo_lang", Value: "en", Path: "/"}})
	b.post("/login", loginForm(b, secret, "correct horse battery"))
	if acc, _ = b.db.AccountByUsername("mama"); acc.Language != "de" {
		t.Fatalf("account language overwritten: %q", acc.Language)
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSwitcherMarkup(t *testing.T) {
	b, secret := configuredBox(t)
	login := b.do(http.MethodGet, "/login", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{
		`<div class="frame-tools">`, `<details class="lang">`,
		`src="/static/flags/de.svg"`, `<span class="vh">Sprache: </span>Deutsch`,
		`<form class="lang-menu" method="post" action="/language">`, `<input type="hidden" name="next" value="/login">`,
		`<button type="submit" name="lang" value="en" lang="en"><img class="flag" src="/static/flags/gb.svg" alt="">English</button>`,
	} {
		if !strings.Contains(login, want) {
			t.Errorf("login page lacks %s", want)
		}
	}
	if strings.Contains(login, `value="de" lang="de"`) {
		t.Error("the menu offers the active language")
	}
	if strings.Contains(login, `class="signout"`) {
		t.Error("sign-out shown before sign-in")
	}
	english := b.do(http.MethodGet, "/login", nil, map[string]string{"Accept-Language": "en"}).body
	if !strings.Contains(english, `src="/static/flags/gb.svg"`) || !strings.Contains(english, `<span class="vh">Language: </span>English`) || !strings.Contains(english, `value="de" lang="de"><img class="flag" src="/static/flags/de.svg" alt="">Deutsch`) {
		t.Errorf("English switcher wrong:\n%s", english)
	}
	b.post("/login", loginForm(b, secret, "correct horse battery"))
	home := b.get("/calendar?view=week").body
	if !strings.Contains(home, `<form class="signout" method="post" action="/logout">`) || !strings.Contains(home, `name="next" value="/calendar?view=week"`) {
		t.Errorf("signed-in switcher wrong:\n%s", home)
	}
	if strings.Index(home, `class="lang"`) > strings.Index(home, `class="signout"`) {
		t.Error("the switcher must come before sign-out")
	}
}

func TestSwitcherOnSetupAndTrustPagesButNotOnDisplays(t *testing.T) {
	fresh := newFreshBox(t) // no HTTPS yet: the first setup step
	if r := fresh.get("/setup/https"); r.status != http.StatusOK || !strings.Contains(r.body, `<details class="lang">`) {
		t.Errorf("/setup/https: %d, switcher missing", r.status)
	}
	unconfigured := newBox(t) // HTTPS set up, first-run wizard still to do
	for _, path := range []string{"/ca", "/setup"} {
		if r := unconfigured.get(path); r.status != http.StatusOK || !strings.Contains(r.body, `<details class="lang">`) {
			t.Errorf("%s: %d, switcher missing", path, r.status)
		}
	}
	b := signedInBox(t)
	if r := b.get("/otp"); r.status != http.StatusOK || !strings.Contains(r.body, `<details class="lang">`) {
		t.Errorf("/otp: %d, switcher missing", r.status)
	}
	if r := b.get("/display"); strings.Contains(r.body, `class="lang"`) || strings.Contains(r.body, "/language") {
		t.Errorf("display page carries the switcher:\n%s", r.body)
	}
}

// 375 px: the frame must not widen the page. The browser measure (document
// scrollWidth equals the viewport) needs a real layout engine; this checks the
// rules that guarantee it: one explicit minimum-width column on every frame,
// and the narrow bar that drops the visible language name next to sign-out.
func TestFrameCannotOverflowNarrowScreens(t *testing.T) {
	css, err := web.Static.ReadFile("static/hejmo.css")
	if err != nil {
		t.Fatal(err)
	}
	s := string(css)
	if !regexp.MustCompile(`\.frame \{[^}]*grid-template-columns: minmax\(0, 1fr\);`).MatchString(s) {
		t.Error(".frame lacks an explicit minmax(0, 1fr) column")
	}
	if !regexp.MustCompile(`@media \(max-width: 479px\) \{\s*\.frame-tools:has\(\.signout\) \.lang-name \{`).MatchString(s) {
		t.Error("no narrow rule hiding the language name next to sign-out")
	}
}

func TestFlagsAreServed(t *testing.T) {
	b, _ := configuredBox(t)
	for _, f := range []string{"de", "gb"} {
		r := b.get("/static/flags/" + f + ".svg")
		if r.status != http.StatusOK || !strings.Contains(r.body, "<svg") {
			t.Errorf("flag %s: %d", f, r.status)
		}
	}
}

// Text a handler builds (not only the template's) follows the page's language:
// a German browser that switched to English gets no German on the page.
func TestHandlerBuiltTextFollowsTheSwitchedLanguage(t *testing.T) {
	b := signedInBox(t)
	b.post("/language", langForm("en", "/otp"))
	de := map[string]string{"Accept-Language": "de"}
	page := b.do(http.MethodGet, "/otp", nil, de)
	if pageLang(t, page.body) != "en" {
		t.Fatalf("page language:\n%s", page.body)
	}
	if !strings.Contains(page.body, "Recovery codes left: ") || strings.Contains(page.body, "Wiederherstellungscodes") {
		t.Errorf("/otp still has German handler text:\n%s", page.body)
	}
	cals := b.do(http.MethodGet, "/calendars", nil, de)
	if cals.status != http.StatusOK {
		t.Fatalf("/calendars = %d", cals.status)
	}
	if strings.Contains(cals.body, "Ganzer Haushalt") || strings.Contains(cals.body, "Nicht zugeordnet") {
		t.Errorf("/calendars still has German handler text")
	}
	// Holiday names on the week page are built by the handler too.
	week := b.do(http.MethodGet, "/calendar?week=2026-12-25", nil, de)
	if week.status != http.StatusOK {
		t.Fatalf("/calendar = %d", week.status)
	}
	if !strings.Contains(week.body, "Christmas Day") || strings.Contains(week.body, "1. Weihnachtstag") {
		t.Errorf("holiday name is not English:\n%s", week.body)
	}
	b.post("/language", langForm("de", "/calendar"))
	if wde := b.do(http.MethodGet, "/calendar?week=2026-12-25", nil, map[string]string{"Accept-Language": "en"}); !strings.Contains(wde.body, "1. Weihnachtstag") {
		t.Errorf("holiday name is not German:\n%s", wde.body)
	}
}

// A signed-in page reads the language several times (frame, handler-built
// texts, headers); without the per-request memo each of those is a lookup.
func TestPageLanguageIsLookedUpOncePerRequest(t *testing.T) {
	b := signedInBox(t)
	for _, path := range []string{"/", "/otp", "/calendars", "/calendar?week=2026-12-25"} {
		stop := server.CountLanguageLookups(t)
		page := b.get(path)
		n := stop()
		if page.status != http.StatusOK {
			t.Fatalf("GET %s = %d", path, page.status)
		}
		if n != 1 {
			t.Errorf("GET %s: language lookups for one request = %d, want 1", path, n)
		}
	}
}
