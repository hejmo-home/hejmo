package server_test

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// screen is a display: a browser of its own with its own cookies, not signed in.
type screen struct {
	b      *box
	client *http.Client
}

func newScreen(b *box) *screen { return &screen{b: b, client: b.trustingClient()} }

func (s *screen) get(path string, lang string) resp {
	s.b.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.b.srv.URL+path, nil)
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	res, err := s.client.Do(req)
	if err != nil {
		s.b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), string(raw)}
}

var (
	codeRe    = regexp.MustCompile(`data-pairing-code="([0-9]{6})"`)
	pairURLRe = regexp.MustCompile(`data-pair-url="([^"]+)"`)
	pollRe    = regexp.MustCompile(`data-poll-seconds="([0-9]+)"`)
)

func codeOn(t *testing.T, page string) string {
	t.Helper()
	m := codeRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no pairing code on page:\n%s", page)
	}
	return m[1]
}

// signedInBox returns a configured box with the parent signed in.
func signedInBox(t *testing.T, opts ...boxOption) *box {
	t.Helper()
	b, secret := configuredBox(t, opts...)
	if r := b.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("login = %d", r.status)
	}
	return b
}

func pairForm(code string) url.Values {
	return url.Values{"code": {code}, "name": {"Hallway"}, "type": {"eink_bw"}}
}

// 5.2: Given an unpaired display, when it starts, then it shows a QR code and a six-digit code.
func TestUnpairedDisplayShowsQRCodeAndSixDigitCode(t *testing.T) {
	b := signedInBox(t)
	d := newScreen(b)
	r := d.get("/display", "en")
	if r.status != http.StatusOK {
		t.Fatalf("GET /display = %d", r.status)
	}
	code := codeOn(t, r.body)
	for _, want := range []string{`data-state="pairing"`, "<svg", `role="img"`, code} {
		if !strings.Contains(r.body, want) {
			t.Errorf("pairing screen misses %q", want)
		}
	}
	// The QR code leads a parent's phone to the confirmation form with the code filled in.
	m := pairURLRe.FindStringSubmatch(r.body)
	if m == nil || !strings.HasPrefix(m[1], b.srv.URL+"/displays") || !strings.Contains(m[1], "code="+code) {
		t.Fatalf("pair url = %v, want %s/displays?code=%s", m, b.srv.URL, code)
	}
	// A display keeps its code while it reloads.
	if again := d.get("/display", "en"); codeOn(t, again.body) != code {
		t.Fatal("code changed between two loads")
	}
	// Two displays get two codes.
	if other := newScreen(b).get("/display", "en"); codeOn(t, other.body) == code {
		t.Fatal("two displays share a code")
	}
}

func TestPairingScreenIsGermanOnRequestAndNeverCached(t *testing.T) {
	d := newScreen(signedInBox(t))
	req, _ := http.NewRequest(http.MethodGet, d.b.srv.URL+"/display", nil)
	req.Header.Set("Accept-Language", "de-DE")
	res, err := d.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `lang="de"`) || !strings.Contains(string(body), "Code") {
		t.Fatalf("not German:\n%s", body)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", res.Header.Get("Cache-Control"))
	}
	for _, c := range res.Cookies() {
		if c.Name == "hejmo_display" && (!c.HttpOnly || !c.Secure) {
			t.Fatalf("display cookie not HttpOnly and Secure: %+v", c)
		}
	}
}

func TestBoxWithoutSetupSendsDisplaysToSetup(t *testing.T) {
	r := newScreen(newBox(t)).get("/display", "")
	if r.status != http.StatusSeeOther || r.location != "/setup" {
		t.Fatalf("GET /display = %d %q, want 303 to /setup", r.status, r.location)
	}
}

// 5.2: Given a confirmed code, then the display receives a token and shows the family view placeholder.
func TestConfirmedCodePairsTheDisplay(t *testing.T) {
	b := signedInBox(t)
	d := newScreen(b)
	code := codeOn(t, d.get("/display", "en").body)

	if r := b.post("/displays", pairForm(code)); r.status != http.StatusSeeOther || r.location != "/displays" {
		t.Fatalf("confirm = %d %q\n%s", r.status, r.location, r.body)
	}
	r := d.get("/display", "en")
	for _, want := range []string{`data-state="paired"`, `data-family`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("family view misses %q:\n%s", want, r.body)
		}
	}
	if strings.Contains(r.body, "<svg") || codeRe.MatchString(r.body) {
		t.Error("paired display still shows a pairing code or QR code")
	}
	if st := d.get("/display/state", ""); !strings.Contains(st.body, `"state":"paired"`) {
		t.Fatalf("state = %s", st.body)
	}
	// The display's own browser holds the token; nobody else gets the family view.
	if other := newScreen(b).get("/display", "en"); !strings.Contains(other.body, `data-state="pairing"`) {
		t.Fatal("a browser without the token shows the family view")
	}
}

func TestParentSeesPairedDisplaysAndOpensTheQRLinkWithCodePrefilled(t *testing.T) {
	b := signedInBox(t)
	d := newScreen(b)
	code := codeOn(t, d.get("/display", "en").body)

	page := b.get("/displays?code=" + code)
	if page.status != http.StatusOK || !strings.Contains(page.body, `value="`+code+`"`) {
		t.Fatalf("form = %d, code not prefilled:\n%s", page.status, page.body)
	}
	for _, want := range []string{`name="name"`, `name="type"`, `value="eink_bw"`, `value="eink_color"`, `value="screen"`} {
		if !strings.Contains(page.body, want) {
			t.Errorf("form misses %s", want)
		}
	}
	b.post("/displays", pairForm(code))
	list := b.get("/displays")
	for _, want := range []string{"Hallway"} {
		if !strings.Contains(list.body, want) {
			t.Errorf("display list misses %q", want)
		}
	}
}

// 5.2: Given a wrong or expired code, then pairing is refused.
func TestWrongOrExpiredCodeIsRefused(t *testing.T) {
	b := signedInBox(t)
	d := newScreen(b)
	code := codeOn(t, d.get("/display", "en").body)
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}

	r := b.post("/displays", pairForm(wrong))
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `role="alert"`) {
		t.Fatalf("wrong code = %d, want 422 with an error\n%s", r.status, r.body)
	}
	if !strings.Contains(d.get("/display", "en").body, `data-state="pairing"`) {
		t.Fatal("display paired by a wrong code")
	}

	b.now = b.now.Add(11 * time.Minute)
	if r := b.post("/displays", pairForm(code)); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("expired code = %d, want 422", r.status)
	}
	// The waiting display notices and shows a new code.
	if st := d.get("/display/state", ""); !strings.Contains(st.body, `"state":"unpaired"`) {
		t.Fatalf("state after expiry = %s", st.body)
	}
	fresh := codeOn(t, d.get("/display", "en").body)
	if r := b.post("/displays", pairForm(fresh)); r.status != http.StatusSeeOther {
		t.Fatalf("new code = %d, want 303", r.status)
	}
}

func TestGuessingCodesIsLimited(t *testing.T) {
	b := signedInBox(t)
	d := newScreen(b)
	code := codeOn(t, d.get("/display", "en").body)
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}
	for i := 0; i < 5; i++ {
		b.post("/displays", pairForm(wrong))
	}
	if r := b.post("/displays", pairForm(code)); r.status != http.StatusTooManyRequests {
		t.Fatalf("after 5 wrong codes = %d, want 429", r.status)
	}
	b.now = b.now.Add(16 * time.Minute)
	fresh := codeOn(t, d.get("/display", "en").body)
	if r := b.post("/displays", pairForm(fresh)); r.status != http.StatusSeeOther {
		t.Fatalf("after the wait = %d, want 303", r.status)
	}
}

func TestConfirmingNeedsNameAndKnownType(t *testing.T) {
	b := signedInBox(t)
	code := codeOn(t, newScreen(b).get("/display", "en").body)
	for _, form := range []url.Values{
		{"code": {code}, "name": {" "}, "type": {"screen"}},
		{"code": {code}, "name": {"Hallway"}, "type": {"hologram"}},
	} {
		if r := b.post("/displays", form); r.status != http.StatusUnprocessableEntity {
			t.Fatalf("POST %v = %d, want 422", form, r.status)
		}
	}
}

// 5.2: Given a paired display, when its token is revoked in the web interface,
// then it shows the pairing screen again within one minute.
func TestRevokedDisplayShowsPairingScreenAgainWithinAMinute(t *testing.T) {
	b := signedInBox(t)
	d := newScreen(b)
	code := codeOn(t, d.get("/display", "en").body)
	b.post("/displays", pairForm(code))
	paired := d.get("/display", "en")
	m := pollRe.FindStringSubmatch(paired.body)
	if m == nil {
		t.Fatalf("paired page does not say how often it asks the box:\n%s", paired.body)
	}
	if secs, _ := time.ParseDuration(m[1] + "s"); secs <= 0 || secs > 30*time.Second {
		t.Fatalf("poll every %v, must be at most 30s to notice a revocation within a minute", secs)
	}
	if !strings.Contains(paired.body, "/static/display.js") {
		t.Fatal("paired page does not load the display script")
	}
	if js := d.get("/static/display.js", ""); js.status != http.StatusOK || !strings.Contains(js.body, "/display/state") {
		t.Fatalf("display.js = %d", js.status)
	}

	id := regexp.MustCompile(`/displays/([0-9a-f-]{36})/unpair`).FindStringSubmatch(b.get("/displays").body)
	if id == nil {
		t.Fatal("no unpair button on the displays page")
	}
	if r := b.post("/displays/"+id[1]+"/unpair", url.Values{}); r.status != http.StatusSeeOther || r.location != "/displays" {
		t.Fatalf("unpair = %d %q", r.status, r.location)
	}

	// The next poll tells the display; the reload shows a fresh pairing screen.
	if st := d.get("/display/state", ""); !strings.Contains(st.body, `"state":"unpaired"`) {
		t.Fatalf("state after revoke = %s", st.body)
	}
	again := d.get("/display", "en")
	if !strings.Contains(again.body, `data-state="pairing"`) || strings.Contains(again.body, "Hallway") {
		t.Fatalf("revoked display still shows the family view:\n%s", again.body)
	}
	if strings.Contains(b.get("/displays").body, "Hallway") {
		t.Fatal("revoked display still listed")
	}
	// It can be paired again.
	if r := b.post("/displays", pairForm(codeOn(t, again.body))); r.status != http.StatusSeeOther {
		t.Fatalf("re-pair = %d", r.status)
	}
}

func TestDisplaysAreManagedByParentsOnly(t *testing.T) {
	b := newBox(t)
	if r := b.get("/displays"); r.status != http.StatusSeeOther || r.location != "/setup" {
		t.Fatalf("unconfigured = %d %q", r.status, r.location)
	}
	b, _ = configuredBox(t)
	if r := b.get("/displays"); r.status != http.StatusSeeOther || r.location != "/login?next=%2Fdisplays" {
		t.Fatalf("signed out = %d %q, want 303 to /login", r.status, r.location)
	}
	if r := b.post("/displays", pairForm("123456")); r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("signed-out confirm = %d %q", r.status, r.location)
	}
}

func TestDisplayLinkIsOnTheSettingsHub(t *testing.T) {
	b := signedInBox(t)
	if r := b.get("/settings"); !strings.Contains(r.body, `href="/displays"`) {
		t.Fatal("no link to the displays page")
	}
}

// A client that keeps opening pairing screens is slowed down, and the
// waiting rows it leaves behind stay bounded.
func TestOpeningManyPairingScreensIsLimited(t *testing.T) {
	b := signedInBox(t)
	refused := 0
	for i := 0; i < 40; i++ {
		if r := newScreen(b).get("/display", "en"); r.status == http.StatusTooManyRequests {
			refused++
		} else if r.status != http.StatusOK {
			t.Fatalf("open %d = %d", i, r.status)
		}
	}
	if refused == 0 {
		t.Fatal("40 pairing screens opened from one address, none refused")
	}
	b.now = b.now.Add(11 * time.Minute)
	if r := newScreen(b).get("/display", "en"); r.status != http.StatusOK {
		t.Fatalf("after the wait = %d, want 200", r.status)
	}
}

func TestEvictedWaitingDisplayGetsANewPairingScreen(t *testing.T) {
	b := signedInBox(t)
	first := newScreen(b)
	first.get("/display", "en")
	for i := 0; i < 30; i++ {
		b.now = b.now.Add(2 * time.Minute)
		newScreen(b).get("/display", "en")
	}
	if r := first.get("/display", "en"); r.status != http.StatusOK || !strings.Contains(r.body, "data-pairing-code") {
		t.Fatalf("evicted display = %d, want a fresh pairing screen", r.status)
	}
}

func TestSignInFromTheQRLinkReturnsToTheDisplaysPageWithTheCode(t *testing.T) {
	b, secret := configuredBox(t)
	code := codeOn(t, newScreen(b).get("/display", "en").body)
	r := b.get("/displays?code=" + code)
	if r.status != http.StatusSeeOther || r.location != "/login?next=%2Fdisplays%3Fcode%3D"+code {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
	form := loginForm(b, secret, "correct horse battery")
	form.Set("next", "/displays?code="+code)
	if r := b.post("/login", form); r.status != http.StatusSeeOther || r.location != "/displays?code="+code {
		t.Fatalf("login = %d %q, want 303 back to the displays page", r.status, r.location)
	}
	for _, bad := range []string{"https://evil.example/", "//evil.example/", `/\evil.example`, "/\t/evil.example/", "/ /evil.example/", "/\x00x"} {
		b.logout()
		form.Set("next", bad)
		if r := b.post("/login", form); r.location != "/" {
			t.Fatalf("next=%q redirected to %q, want /", bad, r.location)
		}
	}
}

var checkedTypeRe = regexp.MustCompile(`<input type="radio" name="type" value="([a-z_]+)" checked>`)

// ADR 0017: the type cards start with one choice, as the old select did.
func TestFreshPairingFormHasADisplayTypeChecked(t *testing.T) {
	b := signedInBox(t)
	for _, path := range []string{"/displays", "/displays?code=123456"} {
		m := checkedTypeRe.FindStringSubmatch(getEN(b, path).body)
		if m == nil || m[1] != "eink_bw" {
			t.Errorf("GET %s: checked type = %v, want eink_bw", path, m)
		}
	}
}

// A form error is shown at its field: the code, the name or the type.
func TestPairingErrorsAreShownAtTheWrongField(t *testing.T) {
	b := signedInBox(t)
	d := newScreen(b)
	code := codeOn(t, d.get("/display", "en").body)
	for _, c := range []struct {
		name  string
		edit  func(url.Values)
		field string
	}{
		{"no name", func(v url.Values) { v.Set("name", " ") }, "name"},
		{"no type", func(v url.Values) { v.Del("type") }, "type"},
		{"wrong code", func(v url.Values) { v.Set("code", "000000") }, "code"},
	} {
		form := pairForm(code)
		c.edit(form)
		r := b.do(http.MethodPost, "/displays", form, map[string]string{"Accept-Language": "en"})
		if r.status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d", c.name, r.status)
			continue
		}
		if !strings.Contains(r.body, `id="err-`+c.field+`"`) {
			t.Errorf("%s: no error at %q:\n%s", c.name, c.field, r.body)
		}
		if n := strings.Count(r.body, `class="ferr"`); n != 1 {
			t.Errorf("%s: %d field errors, want 1", c.name, n)
		}
	}
}

func (s *screen) post(path string, form url.Values) resp {
	s.b.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.b.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", s.b.srv.URL)
	res, err := s.client.Do(req)
	if err != nil {
		s.b.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), string(raw)}
}
