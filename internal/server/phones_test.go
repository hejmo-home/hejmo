package server_test

import (
	"html"
	"image/png"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

var (
	joinURLRe   = regexp.MustCompile(`data-join-url="([^"]+)"`)
	pairingIDRe = regexp.MustCompile(`data-pairing-id="([^"]+)"`)
	phoneIDRe   = regexp.MustCompile(`data-phone-id="([^"]+)"`)
)

// joinPath is the path a phone opens after scanning the QR code on page. With
// the box's own CA the code leads through the trust page first.
func joinPath(t *testing.T, page string) string {
	t.Helper()
	m := joinURLRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no QR code with a join address on page:\n%s", page)
	}
	u, err := url.Parse(html.UnescapeString(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	if u.Path == "/ca" {
		if u, err = url.Parse(u.Query().Get("continue")); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.HasPrefix(u.Path, "/phones/join/") {
		t.Fatalf("join address %q does not lead to /phones/join/", u)
	}
	return u.Path
}

func personNamed(t *testing.T, b *box, name string) store.Person {
	t.Helper()
	h, err := b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	people, err := b.db.People(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range people {
		if p.Name == name {
			return p
		}
	}
	p, err := b.db.AddPerson(h.ID, store.Person{Name: name, Role: store.RoleChild, Color: "#9A7420", Shape: "square-empty"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// showCode has the signed-in parent show a QR code and returns the path the phone opens.
func showCode(t *testing.T, b *box) string {
	t.Helper()
	r := b.post("/phones/pairings", url.Values{})
	if r.status != http.StatusOK {
		t.Fatalf("POST /phones/pairings = %d\n%s", r.status, r.body)
	}
	return joinPath(t, r.body)
}

// confirmPhone is the parent's confirmation of the one waiting phone.
func confirmPhone(t *testing.T, b *box, person store.Person, name string) resp {
	t.Helper()
	page := b.get("/phones").body
	m := pairingIDRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no waiting phone on the phones page:\n%s", page)
	}
	return b.post("/phones/pairings/"+m[1]+"/confirm", url.Values{"person": {person.ID}, "name": {name}})
}

// pairedPhone pairs a phone for person and returns it, signed in.
func pairedPhone(t *testing.T, b *box, person store.Person, name string) *screen {
	t.Helper()
	path := showCode(t, b)
	ph := newScreen(b)
	if r := ph.get(path, "en"); r.status != http.StatusOK {
		t.Fatalf("phone opens the code = %d\n%s", r.status, r.body)
	}
	if r := confirmPhone(t, b, person, name); r.status != http.StatusSeeOther {
		t.Fatalf("confirm = %d\n%s", r.status, r.body)
	}
	if r := ph.get(path, "en"); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("phone collects its session = %d %q", r.status, r.location)
	}
	return ph
}

// 5.13: Given a QR code shown by a parent, when a phone opens it and the parent
// confirms, then the phone has a device session for that account.
func TestPhoneOpensTheQRCodeAndGetsADeviceSessionAfterTheParentConfirms(t *testing.T) {
	b := signedInBox(t)
	r := b.post("/phones/pairings", url.Values{})
	for _, want := range []string{"<svg", `role="img"`, "data-join-url"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("QR page misses %q", want)
		}
	}
	path := joinPath(t, r.body)
	phone := newScreen(b)

	waiting := phone.get(path, "en")
	if waiting.status != http.StatusOK || !strings.Contains(waiting.body, `data-state="waiting"`) || !strings.Contains(waiting.body, `http-equiv="refresh"`) {
		t.Fatalf("phone before confirmation = %d\n%s", waiting.status, waiting.body)
	}
	if got := phone.get("/", "en"); got.status != http.StatusSeeOther {
		t.Fatalf("phone is signed in before the parent confirmed: %d", got.status)
	}
	if again := phone.get(path, "en"); again.status != http.StatusOK || !strings.Contains(again.body, `data-state="waiting"`) {
		t.Fatalf("phone still waiting = %d", again.status)
	}

	// The parent sees the waiting phone and chooses the account.
	page := b.get("/phones").body
	if !strings.Contains(page, "data-pairing-id") {
		t.Fatalf("parent does not see the waiting phone:\n%s", page)
	}
	mama := personNamed(t, b, "Mama")
	if c := confirmPhone(t, b, mama, "Mama's phone"); c.status != http.StatusSeeOther || c.location != "/phones" {
		t.Fatalf("confirm = %d %q", c.status, c.location)
	}
	if done := phone.get(path, "en"); done.status != http.StatusSeeOther || done.location != "/" {
		t.Fatalf("phone after confirmation = %d %q, want 303 to /", done.status, done.location)
	}
	me := phone.get("/api/v1/me", "en")
	if me.status != http.StatusOK || !strings.Contains(me.body, `"Mama"`) {
		t.Fatalf("phone is not signed in as Mama: %d %s", me.status, me.body)
	}
	if list := b.get("/phones").body; !strings.Contains(list, "Mama&#39;s phone") || !strings.Contains(list, "data-phone-id") {
		t.Fatalf("phones page does not list the phone:\n%s", list)
	}
}

// 5.13: Given an expired or used pairing code, then pairing is refused.
func TestExpiredUnknownAndUsedPairingCodesAreRefused(t *testing.T) {
	b := signedInBox(t)
	refused := func(what, path string, phone *screen) {
		t.Helper()
		signedIn := phone.get("/", "en").status == http.StatusOK // the phone that paired legitimately stays signed in
		r := phone.get(path, "en")
		if r.status != http.StatusGone || !strings.Contains(r.body, `data-state="refused"`) {
			t.Errorf("%s: %d, want 410 with the refusal page\n%s", what, r.status, r.body)
		}
		if got := phone.get("/", "en"); (got.status == http.StatusOK) != signedIn {
			t.Errorf("%s: phone got a session (%d)", what, got.status)
		}
	}

	refused("unknown code", "/phones/join/not-a-code", newScreen(b))

	expired := showCode(t, b)
	b.now = b.now.Add(store.PairingCodeLifetime + time.Minute)
	refused("expired code", expired, newScreen(b))

	// A used code: a second phone cannot use it, neither while the first waits nor afterwards.
	path := showCode(t, b)
	first := newScreen(b)
	first.get(path, "en")
	refused("code of another phone, waiting", path, newScreen(b))
	confirmPhone(t, b, personNamed(t, b, "Mia"), "Mia's phone")
	first.get(path, "en")
	refused("code of another phone, paired", path, newScreen(b))
	refused("used code, same phone", path, first)
}

func TestACodeConfirmedTooLateIsRefusedToo(t *testing.T) {
	b := signedInBox(t)
	path := showCode(t, b)
	phone := newScreen(b)
	phone.get(path, "en")
	page := b.get("/phones").body
	id := pairingIDRe.FindStringSubmatch(page)[1]
	b.now = b.now.Add(store.PairingCodeLifetime + time.Minute)
	r := b.post("/phones/pairings/"+id+"/confirm", url.Values{"person": {personNamed(t, b, "Mia").ID}, "name": {"Mia"}})
	if r.status != http.StatusNotFound {
		t.Fatalf("confirm after expiry = %d, want 404", r.status)
	}
}

func TestConfirmingNeedsANameAndAPerson(t *testing.T) {
	b := signedInBox(t)
	path := showCode(t, b)
	newScreen(b).get(path, "en")
	id := pairingIDRe.FindStringSubmatch(b.get("/phones").body)[1]
	mia := personNamed(t, b, "Mia")
	if r := b.post("/phones/pairings/"+id+"/confirm", url.Values{"person": {mia.ID}, "name": {" "}}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-name"`) {
		t.Errorf("blank name = %d\n%s", r.status, r.body)
	}
	if r := b.post("/phones/pairings/"+id+"/confirm", url.Values{"person": {"nobody"}, "name": {"Phone"}}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-person"`) {
		t.Errorf("unknown person = %d\n%s", r.status, r.body)
	}
}

// 5.13: Given a revoked device session, when the phone loads the app, then it is signed out.
func TestRevokedPhoneIsSignedOut(t *testing.T) {
	b := signedInBox(t)
	phone := pairedPhone(t, b, personNamed(t, b, "Mama"), "Old phone")
	if r := phone.get("/", "en"); r.status != http.StatusOK {
		t.Fatalf("phone before revoking = %d", r.status)
	}
	id := phoneIDRe.FindStringSubmatch(b.get("/phones").body)[1]
	if r := b.post("/phones/"+id+"/revoke", url.Values{}); r.status != http.StatusSeeOther || r.location != "/phones" {
		t.Fatalf("revoke = %d %q", r.status, r.location)
	}
	if r := phone.get("/", "en"); r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("revoked phone loads the app = %d %q, want 303 to /login", r.status, r.location)
	}
	if r := phone.get("/api/v1/me", "en"); r.status != http.StatusUnauthorized {
		t.Fatalf("revoked phone calls the API = %d, want 401", r.status)
	}
	if strings.Contains(b.get("/phones").body, "data-phone-id") {
		t.Error("revoked phone is still listed")
	}
	if r := b.post("/phones/"+id+"/revoke", url.Values{}); r.status != http.StatusNotFound {
		t.Fatalf("second revoke = %d, want 404", r.status)
	}
}

func TestSigningOutOnThePhoneRemovesIt(t *testing.T) {
	b := signedInBox(t)
	phone := pairedPhone(t, b, personNamed(t, b, "Mama"), "Phone")
	req, _ := http.NewRequest(http.MethodPost, b.srv.URL+"/logout", nil)
	req.Header.Set("Origin", b.srv.URL)
	res, err := phone.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if strings.Contains(b.get("/phones").body, "data-phone-id") {
		t.Error("a phone that signed out is still listed")
	}
}

func TestPhoneSessionIsLongLived(t *testing.T) {
	b := signedInBox(t)
	phone := pairedPhone(t, b, personNamed(t, b, "Mama"), "Phone")
	b.now = b.now.Add(300 * 24 * time.Hour)
	if r := phone.get("/", "en"); r.status != http.StatusOK {
		t.Fatalf("phone after 300 days = %d, want still signed in", r.status)
	}
	b.now = b.now.Add(70 * 24 * time.Hour)
	if r := phone.get("/", "en"); r.status != http.StatusSeeOther {
		t.Fatalf("phone after 370 days = %d, want signed out", r.status)
	}
}

// ADR 0015: a child's phone acts with the child's role.
func TestChildsPhoneHasTheChildsRoleOnly(t *testing.T) {
	b := signedInBox(t)
	phone := pairedPhone(t, b, personNamed(t, b, "Mia"), "Mia's phone")
	if r := phone.get("/phones", "en"); r.status != http.StatusForbidden {
		t.Errorf("child's phone opens /phones = %d, want 403", r.status)
	}
	if r := phone.get("/api/v1/phones", "en"); r.status != http.StatusForbidden {
		t.Errorf("child's phone lists phones = %d, want 403", r.status)
	}
	if nav := navTargets(t, phone.get("/", "en").body); strings.Contains(strings.Join(nav, " "), "/phones") {
		t.Errorf("child's navigation offers the phones screen: %v", nav)
	}
}

func TestOnlyParentsSeeThePhonesScreen(t *testing.T) {
	b := signedInBox(t)
	b.logout()
	if r := b.get("/phones"); r.status != http.StatusSeeOther {
		t.Fatalf("signed out = %d, want redirect to login", r.status)
	}
	if r := b.post("/phones/pairings", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("signed out creates a pairing = %d", r.status)
	}
}

func TestPhonesScreenIsTranslated(t *testing.T) {
	b := signedInBox(t)
	for lang, want := range map[string]string{"en": "Add a phone", "de": "Handy hinzufügen"} {
		r := b.do(http.MethodGet, "/phones", nil, map[string]string{"Accept-Language": lang})
		if r.status != http.StatusOK || !strings.Contains(r.body, want) {
			t.Errorf("%s: phones screen misses %q (%d)", lang, want, r.status)
		}
	}
}

// 5.13: With the box's own CA the QR flow points phones to installing the CA first.
func TestWithTheBoxsOwnCAThePhoneGetsTheTrustPageFirst(t *testing.T) {
	b := signedInBox(t)
	r := b.post("/phones/pairings", url.Values{})
	raw := html.UnescapeString(joinURLRe.FindStringSubmatch(r.body)[1])
	u, _ := url.Parse(raw)
	if u.Scheme != "http" || u.Path != "/ca" || !strings.HasPrefix(u.Query().Get("continue"), "https://") {
		t.Fatalf("QR address = %q, want the plain-HTTP trust page with a continue address", raw)
	}
	page := newScreen(b).get("/ca?"+u.RawQuery, "en")
	if !strings.Contains(page.body, `href="`+u.Query().Get("continue")+`"`) {
		t.Errorf("trust page does not lead on to the pairing:\n%s", page.body)
	}
	foreign := newScreen(b).get("/ca?continue="+url.QueryEscape("https://evil.example/phones/join/x"), "en")
	// The address may stay in the language switcher's own "next" path (the
	// switcher only ever leads back to this box); no link may lead there.
	if strings.Contains(foreign.body, `href="https://evil.example`) {
		t.Error("trust page leads on to a foreign address")
	}
}

func TestWithAPublicCertificateTheQRCodeLeadsStraightToThePairing(t *testing.T) {
	b := signedInBox(t)
	b.public.name = "mia.dedyn.io"
	r := b.post("/phones/pairings", url.Values{})
	raw := html.UnescapeString(joinURLRe.FindStringSubmatch(r.body)[1])
	if !strings.HasPrefix(raw, "https://mia.dedyn.io/phones/join/") {
		t.Fatalf("QR address = %q", raw)
	}
}

// 5.13: The web interface is installable (valid manifest, icons).
func TestWebInterfaceIsInstallable(t *testing.T) {
	b := signedInBox(t)
	b.logout()
	m := b.get("/manifest.webmanifest")
	if m.status != http.StatusOK {
		t.Fatalf("manifest = %d", m.status)
	}
	var man struct {
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
		StartURL  string `json:"start_url"`
		Scope     string `json:"scope"`
		Display   string `json:"display"`
		Icons     []struct {
			Src, Sizes, Type, Purpose string
		} `json:"icons"`
	}
	decodeJSON(t, m.body, &man)
	if man.Name != "Hejmo" || man.ShortName == "" || man.StartURL != "/" || man.Scope != "/" || man.Display != "standalone" {
		t.Errorf("manifest = %+v", man)
	}
	sizes := map[string]bool{}
	for _, ic := range man.Icons {
		res, err := b.client.Get(b.srv.URL + ic.Src)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/png" {
			t.Errorf("icon %s: %d %v %q", ic.Src, res.StatusCode, err, res.Header.Get("Content-Type"))
			continue
		}
		w := img.Bounds().Dx()
		if want := ic.Sizes; want != itoa(w)+"x"+itoa(img.Bounds().Dy()) {
			t.Errorf("icon %s is %dx%d, manifest says %s", ic.Src, w, img.Bounds().Dy(), want)
		}
		sizes[ic.Sizes+" "+ic.Purpose] = true
	}
	for _, want := range []string{"192x192 any", "512x512 any", "512x512 maskable"} {
		if !sizes[want] {
			t.Errorf("manifest has no icon %q (has %v)", want, sizes)
		}
	}
	login := b.get("/login").body
	for _, want := range []string{`rel="manifest" href="/manifest.webmanifest"`, `rel="apple-touch-icon"`, `name="theme-color"`} {
		if !strings.Contains(login, want) {
			t.Errorf("page head misses %q", want)
		}
	}
}

func TestServiceWorkerIsServedFromTheRootScope(t *testing.T) {
	b := signedInBox(t)
	b.logout()
	req, _ := http.NewRequest(http.MethodGet, b.srv.URL+"/sw.js", nil)
	res, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") ||
		res.Header.Get("Service-Worker-Allowed") != "/" || res.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("sw.js = %d %q allowed=%q cache=%q", res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Service-Worker-Allowed"), res.Header.Get("Cache-Control"))
	}
}

// 5.13: Given no connection, then the app shows the last view read-only with
// the timestamp and offers no changes. The service worker and offline.js do
// this in the browser; the box hands both the texts and the scripts.
func TestOfflineViewGetsItsTextsInBothLanguagesAndItsScripts(t *testing.T) {
	b := signedInBox(t)
	for lang, want := range map[string]string{"en": "As of {time}, not connected", "de": "Stand {time}, nicht verbunden"} {
		r := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": lang})
		if !strings.Contains(r.body, `data-offline-text="`+want+`"`) || !strings.Contains(r.body, `/static/offline.js`) {
			t.Errorf("%s: framed page misses the offline text %q or script", lang, want)
		}
	}
	for _, path := range []string{"/static/offline.js", "/sw.js"} {
		if r := b.get(path); r.status != http.StatusOK || len(r.body) == 0 {
			t.Errorf("%s = %d", path, r.status)
		}
	}
	// The service worker keeps pages only for a signed-in person and forgets them on sign-out.
	sw := b.get("/sw.js").body
	for _, want := range []string{"fetch", "caches", "/login"} {
		if !strings.Contains(sw, want) {
			t.Errorf("service worker does not mention %q", want)
		}
	}
}

// Review of #118: the QR code carries a translated accessible label.
func TestQRCodeHasATranslatedAccessibleLabel(t *testing.T) {
	b := signedInBox(t)
	for lang, want := range map[string]string{"en": "QR code to pair a phone", "de": "QR-Code zum Koppeln eines Handys"} {
		r := b.do(http.MethodPost, "/phones/pairings", url.Values{}, map[string]string{"Accept-Language": lang})
		if !strings.Contains(r.body, `aria-label="`+want+`"`) {
			t.Errorf("%s: QR code misses the label %q (raw key shown?)", lang, want)
		}
	}
}

var verificationRe = regexp.MustCompile(`data-verification="([0-9 ]+)"`)

// Review of #118: the code travels over plain HTTP with the box's own CA, so
// the phone shows a number the parent compares before confirming. A phone that
// is not the one in the parent's hand shows another number.
func TestPhoneAndParentSeeTheSameVerificationNumber(t *testing.T) {
	b := signedInBox(t)
	path := showCode(t, b)
	phone := newScreen(b)
	waiting := phone.get(path, "en")
	onPhone := verificationRe.FindStringSubmatch(waiting.body)
	if onPhone == nil {
		t.Fatalf("waiting phone shows no verification number:\n%s", waiting.body)
	}
	if again := verificationRe.FindStringSubmatch(phone.get(path, "en").body); again == nil || again[1] != onPhone[1] {
		t.Errorf("number changes between requests: %v vs %v", again, onPhone)
	}
	onParent := verificationRe.FindStringSubmatch(b.get("/phones").body)
	if onParent == nil || onParent[1] != onPhone[1] {
		t.Fatalf("parent sees %v, phone shows %v", onParent, onPhone)
	}
	if len(strings.ReplaceAll(onPhone[1], " ", "")) != 6 {
		t.Errorf("verification number %q is not six digits", onPhone[1])
	}
	// Another phone cannot take over the code, and is not shown a number.
	if other := newScreen(b).get(path, "en"); other.status != http.StatusGone || verificationRe.MatchString(other.body) {
		t.Errorf("second phone = %d, want refused without a number", other.status)
	}
}

func TestVerificationNumberIsInTheAPIToo(t *testing.T) {
	c := signedInAPI(t)
	_, shown, _ := c.call(http.MethodPost, "/phones/pairings", nil)
	path := joinPath(t, `data-join-url="`+shown["url"].(string)+`"`)
	phone := newScreen(c.b)
	onPhone := verificationRe.FindStringSubmatch(phone.get(path, "en").body)
	_, _, waiting := c.call(http.MethodGet, "/phones/pairings", nil)
	if onPhone == nil || len(waiting) != 1 || waiting[0].(map[string]any)["verification"] != onPhone[1] {
		t.Fatalf("API waiting = %v, phone shows %v", waiting, onPhone)
	}
}

// Review of #118: a phone whose device session ran out is shown as expired.
func TestPhoneWithAnExpiredSessionIsListedAsExpired(t *testing.T) {
	b, secret := configuredBox(t)
	b.post("/login", loginForm(b, secret, "correct horse battery"))
	pairedPhone(t, b, personNamed(t, b, "Mama"), "Old phone")
	if strings.Contains(b.get("/phones").body, "data-expired") {
		t.Fatal("fresh phone is shown as expired")
	}
	b.now = b.now.Add(store.PhoneSessionLifetime + 24*time.Hour)
	b.post("/login", loginForm(b, secret, "correct horse battery"))
	if !strings.Contains(b.get("/phones").body, "data-expired") {
		t.Fatal("phone with an expired session is not shown as expired")
	}
}

// Review of #118: the dark scheme gets its own status bar colour.
func TestThemeColourFollowsTheColourScheme(t *testing.T) {
	b := signedInBox(t)
	b.logout()
	head := b.get("/login").body
	for _, want := range []string{
		`name="theme-color" content="#F6F3EE" media="(prefers-color-scheme: light)"`,
		`name="theme-color" content="#1B1A18" media="(prefers-color-scheme: dark)"`,
	} {
		if !strings.Contains(head, want) {
			t.Errorf("page head misses %q", want)
		}
	}
}
