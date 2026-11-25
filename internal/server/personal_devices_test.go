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
	joinURLRe          = regexp.MustCompile(`data-join-url="([^"]+)"`)
	pairingIDRe        = regexp.MustCompile(`data-pairing-id="([^"]+)"`)
	personalDeviceIDRe = regexp.MustCompile(`href="/personal-devices/([0-9a-f-]{36})"`)
)

// joinPath is the path a personal device opens after scanning the QR code on page. With
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
	if !strings.HasPrefix(u.Path, "/personal-devices/join/") {
		t.Fatalf("join address %q does not lead to /personal-devices/join/", u)
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

// showCode has the signed-in parent show a QR code and returns the path the personal device opens.
func showCode(t *testing.T, b *box) string {
	t.Helper()
	r := b.post("/personal-devices/pairings", url.Values{})
	if r.status != http.StatusOK {
		t.Fatalf("POST /personal-devices/pairings = %d\n%s", r.status, r.body)
	}
	return joinPath(t, r.body)
}

// confirmPersonalDevice is the parent's confirmation of the one waiting personal device.
func confirmPersonalDevice(t *testing.T, b *box, person store.Person, name string) resp {
	t.Helper()
	page := b.get("/personal-devices").body
	m := pairingIDRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no waiting personal device on the personal devices page:\n%s", page)
	}
	return b.post("/personal-devices/pairings/"+m[1]+"/confirm", url.Values{"person": {person.ID}, "name": {name}})
}

// pairedPersonalDevice pairs a personal device for person and returns it, signed in.
func pairedPersonalDevice(t *testing.T, b *box, person store.Person, name string) *screen {
	t.Helper()
	return pairedPersonalDeviceAs(t, b, person, name, "")
}

// pairedPersonalDeviceAs is pairedPersonalDevice for a device that sends the User-Agent ua: the pairing stores
// its kind of screen (phone, tablet).
func pairedPersonalDeviceAs(t *testing.T, b *box, person store.Person, name, ua string) *screen {
	t.Helper()
	path := showCode(t, b)
	ph := newScreen(b)
	ph.ua = ua
	if r := ph.get(path, "en"); r.status != http.StatusOK {
		t.Fatalf("personal device opens the code = %d\n%s", r.status, r.body)
	}
	if r := confirmPersonalDevice(t, b, person, name); r.status != http.StatusSeeOther {
		t.Fatalf("confirm = %d\n%s", r.status, r.body)
	}
	if r := ph.get(path, "en"); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("personal device collects its session = %d %q", r.status, r.location)
	}
	return ph
}

// 5.13: Given a QR code shown by a parent, when a personal device opens it and the parent
// confirms, then the personal device has a device session for that account.
func TestPersonalDeviceOpensTheQRCodeAndGetsADeviceSessionAfterTheParentConfirms(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	r := b.post("/personal-devices/pairings", url.Values{})
	for _, want := range []string{"<svg", `role="img"`, "data-join-url"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("QR page misses %q", want)
		}
	}
	path := joinPath(t, r.body)
	personalDevice := newScreen(b)

	waiting := personalDevice.get(path, "en")
	if waiting.status != http.StatusOK || !strings.Contains(waiting.body, `data-state="waiting"`) || !strings.Contains(waiting.body, `http-equiv="refresh"`) {
		t.Fatalf("personal device before confirmation = %d\n%s", waiting.status, waiting.body)
	}
	if got := personalDevice.get("/", "en"); got.status != http.StatusSeeOther {
		t.Fatalf("personal device is signed in before the parent confirmed: %d", got.status)
	}
	if again := personalDevice.get(path, "en"); again.status != http.StatusOK || !strings.Contains(again.body, `data-state="waiting"`) {
		t.Fatalf("personal device still waiting = %d", again.status)
	}

	// The parent sees the waiting personal device and chooses the account.
	page := b.get("/personal-devices").body
	if !strings.Contains(page, "data-pairing-id") {
		t.Fatalf("parent does not see the waiting personal device:\n%s", page)
	}
	mama := personNamed(t, b, "Mama")
	if c := confirmPersonalDevice(t, b, mama, "Mama's tablet"); c.status != http.StatusSeeOther || c.location != "/personal-devices" {
		t.Fatalf("confirm = %d %q", c.status, c.location)
	}
	if done := personalDevice.get(path, "en"); done.status != http.StatusSeeOther || done.location != "/" {
		t.Fatalf("personal device after confirmation = %d %q, want 303 to /", done.status, done.location)
	}
	me := personalDevice.get("/api/v1/me", "en")
	if me.status != http.StatusOK || !strings.Contains(me.body, `"Mama"`) {
		t.Fatalf("personal device is not signed in as Mama: %d %s", me.status, me.body)
	}
	if list := b.get("/personal-devices").body; !strings.Contains(list, "Mama&#39;s tablet") || !personalDeviceIDRe.MatchString(list) {
		t.Fatalf("personal devices page does not list the personal device:\n%s", list)
	}
}

// 5.13: Given an expired or used pairing code, then pairing is refused.
func TestExpiredUnknownAndUsedPairingCodesAreRefused(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	refused := func(what, path string, personalDevice *screen) {
		t.Helper()
		signedIn := personalDevice.get("/", "en").status == http.StatusOK // the personal device that paired legitimately stays signed in
		r := personalDevice.get(path, "en")
		if r.status != http.StatusGone || !strings.Contains(r.body, `data-state="refused"`) {
			t.Errorf("%s: %d, want 410 with the refusal page\n%s", what, r.status, r.body)
		}
		if got := personalDevice.get("/", "en"); (got.status == http.StatusOK) != signedIn {
			t.Errorf("%s: personal device got a session (%d)", what, got.status)
		}
	}

	refused("unknown code", "/personal-devices/join/not-a-code", newScreen(b))

	expired := showCode(t, b)
	b.now = b.now.Add(store.PairingCodeLifetime + time.Minute)
	refused("expired code", expired, newScreen(b))

	// A used code: a second personal device cannot use it, neither while the first waits nor afterwards.
	path := showCode(t, b)
	first := newScreen(b)
	first.get(path, "en")
	refused("code of another personal device, waiting", path, newScreen(b))
	confirmPersonalDevice(t, b, personNamed(t, b, "Mia"), "Mia's tablet")
	first.get(path, "en")
	refused("code of another personal device, paired", path, newScreen(b))
	refused("used code, same personal device", path, first)
}

func TestACodeConfirmedTooLateIsRefusedToo(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	path := showCode(t, b)
	personalDevice := newScreen(b)
	personalDevice.get(path, "en")
	page := b.get("/personal-devices").body
	id := pairingIDRe.FindStringSubmatch(page)[1]
	b.now = b.now.Add(store.PairingCodeLifetime + time.Minute)
	r := b.post("/personal-devices/pairings/"+id+"/confirm", url.Values{"person": {personNamed(t, b, "Mia").ID}, "name": {"Mia"}})
	if r.status != http.StatusNotFound {
		t.Fatalf("confirm after expiry = %d, want 404", r.status)
	}
}

func TestConfirmingNeedsANameAndAPerson(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	path := showCode(t, b)
	newScreen(b).get(path, "en")
	id := pairingIDRe.FindStringSubmatch(b.get("/personal-devices").body)[1]
	mia := personNamed(t, b, "Mia")
	if r := b.post("/personal-devices/pairings/"+id+"/confirm", url.Values{"person": {mia.ID}, "name": {" "}}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-name"`) {
		t.Errorf("blank name = %d\n%s", r.status, r.body)
	}
	if r := b.post("/personal-devices/pairings/"+id+"/confirm", url.Values{"person": {"nobody"}, "name": {"Personal device"}}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-person"`) {
		t.Errorf("unknown person = %d\n%s", r.status, r.body)
	}
}

// 5.13: Given a revoked device session, when the personal device loads the app, then it is signed out.
func TestRevokedPersonalDeviceIsSignedOut(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	personalDevice := pairedPersonalDevice(t, b, personNamed(t, b, "Mama"), "Old tablet")
	if r := personalDevice.get("/", "en"); r.status != http.StatusOK {
		t.Fatalf("personal device before revoking = %d", r.status)
	}
	id := personalDeviceIDRe.FindStringSubmatch(b.get("/personal-devices").body)[1]
	if r := b.post("/personal-devices/"+id+"/revoke", url.Values{}); r.status != http.StatusSeeOther || r.location != "/personal-devices" {
		t.Fatalf("revoke = %d %q", r.status, r.location)
	}
	if r := personalDevice.get("/", "en"); r.status != http.StatusSeeOther || r.location != "/login" {
		t.Fatalf("revoked personal device loads the app = %d %q, want 303 to /login", r.status, r.location)
	}
	if r := personalDevice.get("/api/v1/me", "en"); r.status != http.StatusUnauthorized {
		t.Fatalf("revoked personal device calls the API = %d, want 401", r.status)
	}
	if personalDeviceIDRe.MatchString(b.get("/personal-devices").body) {
		t.Error("revoked personal device is still listed")
	}
	if r := b.post("/personal-devices/"+id+"/revoke", url.Values{}); r.status != http.StatusNotFound {
		t.Fatalf("second revoke = %d, want 404", r.status)
	}
}

func TestSigningOutOnThePersonalDeviceRemovesIt(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	personalDevice := pairedPersonalDevice(t, b, personNamed(t, b, "Mama"), "Personal device")
	req, _ := http.NewRequest(http.MethodPost, b.srv.URL+"/logout", nil)
	req.Header.Set("Origin", b.srv.URL)
	res, err := personalDevice.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if personalDeviceIDRe.MatchString(b.get("/personal-devices").body) {
		t.Error("a personal device that signed out is still listed")
	}
}

func TestPersonalDeviceSessionIsLongLived(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	personalDevice := pairedPersonalDevice(t, b, personNamed(t, b, "Mama"), "Personal device")
	b.now = b.now.Add(300 * 24 * time.Hour)
	if r := personalDevice.get("/", "en"); r.status != http.StatusOK {
		t.Fatalf("personal device after 300 days = %d, want still signed in", r.status)
	}
	b.now = b.now.Add(70 * 24 * time.Hour)
	if r := personalDevice.get("/", "en"); r.status != http.StatusSeeOther {
		t.Fatalf("personal device after 370 days = %d, want signed out", r.status)
	}
}

// ADR 0015: a child's personal device acts with the child's role.
func TestChildsPersonalDeviceHasTheChildsRoleOnly(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	personalDevice := pairedPersonalDevice(t, b, personNamed(t, b, "Mia"), "Mia's tablet")
	if r := personalDevice.get("/personal-devices", "en"); r.status != http.StatusForbidden {
		t.Errorf("child's personal device opens /personal-devices = %d, want 403", r.status)
	}
	if r := personalDevice.get("/api/v1/personal-devices", "en"); r.status != http.StatusForbidden {
		t.Errorf("child's personal device lists personal devices = %d, want 403", r.status)
	}
	if nav := navTargets(t, personalDevice.get("/", "en").body); strings.Contains(strings.Join(nav, " "), "/personal-devices") {
		t.Errorf("child's navigation offers the personal devices screen: %v", nav)
	}
}

func TestOnlyParentsSeeThePersonalDevicesScreen(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	b.logout()
	if r := b.get("/personal-devices"); r.status != http.StatusSeeOther {
		t.Fatalf("signed out = %d, want redirect to login", r.status)
	}
	if r := b.post("/personal-devices/pairings", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("signed out creates a pairing = %d", r.status)
	}
}

func TestPersonalDevicesScreenIsTranslated(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	for lang, want := range map[string]string{"en": "Add a device", "de": "Gerät hinzufügen"} {
		r := b.do(http.MethodGet, "/personal-devices", nil, map[string]string{"Accept-Language": lang})
		if r.status != http.StatusOK || !strings.Contains(r.body, want) {
			t.Errorf("%s: personal devices screen misses %q (%d)", lang, want, r.status)
		}
	}
}

// 5.13: With the box's own CA the QR flow points personal devices to installing the CA first.
func TestWithTheBoxsOwnCAThePersonalDeviceGetsTheTrustPageFirst(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	r := b.post("/personal-devices/pairings", url.Values{})
	raw := html.UnescapeString(joinURLRe.FindStringSubmatch(r.body)[1])
	u, _ := url.Parse(raw)
	if u.Scheme != "http" || u.Path != "/ca" || !strings.HasPrefix(u.Query().Get("continue"), "https://") {
		t.Fatalf("QR address = %q, want the plain-HTTP trust page with a continue address", raw)
	}
	page := newScreen(b).get("/ca?"+u.RawQuery, "en")
	if !strings.Contains(page.body, `href="`+u.Query().Get("continue")+`"`) {
		t.Errorf("trust page does not lead on to the pairing:\n%s", page.body)
	}
	foreign := newScreen(b).get("/ca?continue="+url.QueryEscape("https://evil.example/personal-devices/join/x"), "en")
	// The address may stay in the language switcher's own "next" path (the
	// switcher only ever leads back to this box); cut that input out, then
	// the address must appear nowhere else.
	body := regexp.MustCompile(`<input type="hidden" name="next" value="[^"]*">`).ReplaceAllString(foreign.body, "")
	if strings.Contains(body, "evil.example") {
		t.Error("trust page leads on to a foreign address")
	}
}

func TestWithAPublicCertificateTheQRCodeLeadsStraightToThePairing(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	b.public.name = "mia.dedyn.io"
	r := b.post("/personal-devices/pairings", url.Values{})
	raw := html.UnescapeString(joinURLRe.FindStringSubmatch(r.body)[1])
	if !strings.HasPrefix(raw, "https://mia.dedyn.io/personal-devices/join/") {
		t.Fatalf("QR address = %q", raw)
	}
}

// 5.13: The web interface is installable (valid manifest, icons).
func TestWebInterfaceIsInstallable(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	b := signedInBox(t)
	for lang, want := range map[string]string{"en": "QR code to pair a device", "de": "QR-Code zum Koppeln eines Geräts"} {
		r := b.do(http.MethodPost, "/personal-devices/pairings", url.Values{}, map[string]string{"Accept-Language": lang})
		if !strings.Contains(r.body, `aria-label="`+want+`"`) {
			t.Errorf("%s: QR code misses the label %q (raw key shown?)", lang, want)
		}
	}
}

var verificationRe = regexp.MustCompile(`data-verification="([0-9 ]+)"`)

// Review of #118: the code travels over plain HTTP with the box's own CA, so
// the personal device shows a number the parent compares before confirming. A personal device that
// is not the one in the parent's hand shows another number.
func TestPersonalDeviceAndParentSeeTheSameVerificationNumber(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	path := showCode(t, b)
	personalDevice := newScreen(b)
	waiting := personalDevice.get(path, "en")
	onPersonalDevice := verificationRe.FindStringSubmatch(waiting.body)
	if onPersonalDevice == nil {
		t.Fatalf("waiting personal device shows no verification number:\n%s", waiting.body)
	}
	if again := verificationRe.FindStringSubmatch(personalDevice.get(path, "en").body); again == nil || again[1] != onPersonalDevice[1] {
		t.Errorf("number changes between requests: %v vs %v", again, onPersonalDevice)
	}
	onParent := verificationRe.FindStringSubmatch(b.get("/personal-devices").body)
	if onParent == nil || onParent[1] != onPersonalDevice[1] {
		t.Fatalf("parent sees %v, personal device shows %v", onParent, onPersonalDevice)
	}
	if len(strings.ReplaceAll(onPersonalDevice[1], " ", "")) != 6 {
		t.Errorf("verification number %q is not six digits", onPersonalDevice[1])
	}
	// Another personal device cannot take over the code, and is not shown a number.
	if other := newScreen(b).get(path, "en"); other.status != http.StatusGone || verificationRe.MatchString(other.body) {
		t.Errorf("second personal device = %d, want refused without a number", other.status)
	}
}

func TestVerificationNumberIsInTheAPIToo(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, shown, _ := c.call(http.MethodPost, "/personal-devices/pairings", nil)
	path := joinPath(t, `data-join-url="`+shown["url"].(string)+`"`)
	personalDevice := newScreen(c.b)
	onPersonalDevice := verificationRe.FindStringSubmatch(personalDevice.get(path, "en").body)
	_, _, waiting := c.call(http.MethodGet, "/personal-devices/pairings", nil)
	if onPersonalDevice == nil || len(waiting) != 1 || waiting[0].(map[string]any)["verification"] != onPersonalDevice[1] {
		t.Fatalf("API waiting = %v, personal device shows %v", waiting, onPersonalDevice)
	}
}

// Review of #118: a personal device whose device session ran out is shown as expired.
func TestPersonalDeviceWithAnExpiredSessionIsListedAsExpired(t *testing.T) {
	t.Parallel()
	b, secret := configuredBox(t)
	b.post("/login", loginForm(b, secret, "correct horse battery"))
	pairedPersonalDevice(t, b, personNamed(t, b, "Mama"), "Old tablet")
	if strings.Contains(b.get("/personal-devices").body, `class="flag warn"`) {
		t.Fatal("fresh personal device is shown as expired")
	}
	b.now = b.now.Add(store.PersonalDeviceSessionLifetime + 24*time.Hour)
	b.post("/login", loginForm(b, secret, "correct horse battery"))
	if !strings.Contains(b.get("/personal-devices").body, `class="flag warn"`) {
		t.Fatal("personal device with an expired session is not shown as expired")
	}
}

// Review of #118: the dark scheme gets its own status bar colour.
func TestThemeColourFollowsTheColourScheme(t *testing.T) {
	t.Parallel()
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

// Ticket 149: the offline line follows the household's time and date format.
// The box hands both to offline.js as data attributes, in either page language,
// so the script needs no request while offline.
func TestOfflineScriptGetsTheHouseholdTimeAndDateFormat(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	for _, lang := range []string{"en", "de"} {
		r := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": lang})
		if !strings.Contains(r.body, `data-time-format="24h" data-date-format="dmy"`) {
			t.Errorf("%s: defaults not on the offline script tag", lang)
		}
	}
	if r := b.post("/settings/household", url.Values{"date_format": {"mdy"}, "time_format": {"12h"}, "temperature_unit": {"c"}}); r.status != http.StatusSeeOther {
		t.Fatalf("settings = %d", r.status)
	}
	for _, lang := range []string{"en", "de"} {
		r := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": lang})
		tag := regexp.MustCompile(`<script src="/static/offline.js"[^>]*>`).FindString(r.body)
		if !strings.Contains(tag, `data-time-format="12h"`) || !strings.Contains(tag, `data-date-format="mdy"`) {
			t.Errorf("%s: offline script tag = %q", lang, tag)
		}
	}
}

// Q147 (#203): the page says what a personal device is.
func TestPersonalDevicesPageSaysWhatAPersonalDeviceIs(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	for lang, want := range map[string][]string{
		"en": {"<h1>Personal devices</h1>", "Phones, tablets and other devices that belong to one person."},
		"de": {"<h1>Persönliche Geräte</h1>", "Handys, Tablets und andere Geräte, die zu einer Person gehören."},
	} {
		page := b.do(http.MethodGet, "/personal-devices", nil, map[string]string{"Accept-Language": lang}).body
		for _, w := range want {
			if !strings.Contains(page, w) {
				t.Errorf("%s: page lacks %q", lang, w)
			}
		}
	}
}

// Q149 (#203): the pairing page asks first whether the device should hang on the wall and show
// everyone the day, and then sends the parent to display pairing.
func TestPairingPageAsksFirstWhetherTheDeviceIsADisplay(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	notice := regexp.MustCompile(`(?s)<p class="notice">(.*?)</p>`)
	for lang, question := range map[string]string{
		"en": "Should the device hang on the wall and show everyone the day? Then pair it as a display.",
		"de": "Soll das Gerät an der Wand hängen und allen den Tag zeigen? Dann koppele es als Display.",
	} {
		page := b.do(http.MethodGet, "/personal-devices", nil, map[string]string{"Accept-Language": lang}).body
		var para string
		for _, m := range notice.FindAllStringSubmatch(page, -1) {
			if strings.Contains(m[1], question) {
				para = m[1]
			}
		}
		if para == "" {
			t.Errorf("%s: the question about displays is missing", lang)
			continue
		}
		if !strings.Contains(para, `<a href="/displays">`) {
			t.Errorf("%s: the question does not link to display pairing: %s", lang, para)
		}
		if i := strings.Index(page, question); i > strings.Index(page, `action="/personal-devices/pairings"`) {
			t.Errorf("%s: the question comes after the QR code button, not first", lang)
		}
	}
}
