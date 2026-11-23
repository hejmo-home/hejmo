package server_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Ticket 199: a browser that held a device session which has ended sees "This
// device is no longer paired" instead of the password sign-in.

// unpairedPersonalDevice is a personal device paired for Mia whose device session a parent has
// removed again. The parent's box is returned for further steps.
func unpairedPersonalDevice(t *testing.T) (*box, *screen, string) {
	t.Helper()
	b, secret := configuredBox(t)
	if r := b.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("login = %d", r.status)
	}
	personalDevice := pairedPersonalDevice(t, b, personNamed(t, b, "Mia"), "Mia's tablet")
	id := personalDeviceIDRe.FindStringSubmatch(b.get("/personal-devices").body)[1]
	if r := b.post("/personal-devices/"+id+"/revoke", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("revoke = %d", r.status)
	}
	return b, personalDevice, secret
}

func TestUnpairedPersonalDeviceShowsNoLongerPairedInsteadOfSignIn(t *testing.T) {
	t.Parallel()
	_, personalDevice, _ := unpairedPersonalDevice(t)
	de := personalDevice.get("/login", "de")
	if de.status != http.StatusOK {
		t.Fatalf("GET /login = %d", de.status)
	}
	for _, want := range []string{"data-unpaired", "Dieses Gerät ist nicht mehr gekoppelt", "Zeig dieses Gerät deinen Eltern.", `href="/login?account=1"`, "Gelöscht ist nichts."} {
		if !strings.Contains(de.body, want) {
			t.Errorf("de page misses %q\n%s", want, de.body)
		}
	}
	for _, not := range []string{`name="password"`, `name="code"`, "data-signed-out", "dieses Handy", "das Handy", "deinem Handy", "Mia", `<a class="brand"`, `class="frame-nav"`} {
		if strings.Contains(de.body, not) {
			t.Errorf("de page shows %q", not)
		}
	}
	if !strings.Contains(de.body, `<span class="brand">Hejmo</span>`) {
		t.Errorf("the brand is not an unlinked span")
	}
	en := personalDevice.get("/login", "en")
	for _, want := range []string{"This device is no longer paired", "Show this device to your parents."} {
		if !strings.Contains(en.body, want) {
			t.Errorf("en page misses %q", want)
		}
	}
	if strings.Contains(strings.ReplaceAll(en.body, "Settings › Personal devices", ""), "personal_device") {
		t.Errorf("en page says personal device:\n%s", en.body)
	}
}

func TestUnpairedPageKnowsNothingOfTheHousehold(t *testing.T) {
	t.Parallel()
	_, personalDevice, _ := unpairedPersonalDevice(t)
	body := personalDevice.get("/login", "de").body
	for _, who := range []string{"Mama", "Papa", "Sebastian", "Mia", "Pasing"} {
		if strings.Contains(body, who) {
			t.Errorf("page names %q", who)
		}
	}
}

func TestFreshBrowserStillGetsTheSignIn(t *testing.T) {
	t.Parallel()
	b, _, _ := unpairedPersonalDevice(t)
	r := newScreen(b).get("/login", "de")
	if !strings.Contains(r.body, `name="password"`) || strings.Contains(r.body, "data-unpaired") {
		t.Fatalf("fresh browser did not get the plain sign-in:\n%s", r.body)
	}
}

func TestEndedAdultBrowserSessionKeepsTheSignedOutNotice(t *testing.T) {
	t.Parallel()
	b, secret := configuredBox(t)
	adult := newScreen(b)
	if r := adult.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("login = %d", r.status)
	}
	b.now = b.now.Add(40 * 24 * time.Hour)
	r := adult.get("/login", "de")
	if !strings.Contains(r.body, "data-signed-out") || !strings.Contains(r.body, `name="password"`) || strings.Contains(r.body, "data-unpaired") {
		t.Fatalf("ended adult session did not get today's notice:\n%s", r.body)
	}
}

func TestUnpairedPersonalDeviceRunOutSessionShowsThePageToo(t *testing.T) {
	t.Parallel()
	b, secret := configuredBox(t)
	personalDevice := pairedPersonalDevice(t, signedInFrom(t, b, secret), personNamed(t, b, "Mia"), "Personal device")
	b.now = b.now.Add(400 * 24 * time.Hour)
	if r := personalDevice.get("/login", "de"); !strings.Contains(r.body, "data-unpaired") {
		t.Fatalf("run-out session did not show the page:\n%s", r.body)
	}
}

func signedInFrom(t *testing.T, b *box, secret string) *box {
	t.Helper()
	if r := b.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("login = %d", r.status)
	}
	return b
}

func TestAdultSignInFromTheUnpairedPageHasNoNoticeAndBackComesBack(t *testing.T) {
	t.Parallel()
	_, personalDevice, _ := unpairedPersonalDevice(t)
	r := personalDevice.get("/login?account=1", "de")
	if !strings.Contains(r.body, `name="password"`) || strings.Contains(r.body, "data-signed-out") || strings.Contains(r.body, "data-unpaired") {
		t.Fatalf("account=1 page:\n%s", r.body)
	}
	if again := personalDevice.get("/login", "de"); !strings.Contains(again.body, "data-unpaired") {
		t.Fatalf("going back from the sign-in did not show the page again")
	}
}

func TestSuccessfulSignInForgetsTheUnpairedMarker(t *testing.T) {
	t.Parallel()
	b, personalDevice, secret := unpairedPersonalDevice(t)
	if r := personalDevice.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("adult sign-in on the personal device = %d", r.status)
	}
	personalDevice.post("/logout", url.Values{})
	if r := personalDevice.get("/login", "de"); strings.Contains(r.body, "data-unpaired") || !strings.Contains(r.body, `name="password"`) {
		t.Fatalf("after an adult's own sign-in and sign-out the page still shows:\n%s", r.body)
	}
}

func TestOwnSignOutOnTheDeviceForgetsTheMarker(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	personalDevice := pairedPersonalDevice(t, b, personNamed(t, b, "Mama"), "Personal device")
	personalDevice.post("/logout", url.Values{})
	if r := personalDevice.get("/login", "de"); strings.Contains(r.body, "data-unpaired") {
		t.Fatalf("a device that signed out itself shows the unpaired page:\n%s", r.body)
	}
}

func TestPairingAgainShowsThePairedHomeInsteadOfTheUnpairedPage(t *testing.T) {
	t.Parallel()
	b, personalDevice, _ := unpairedPersonalDevice(t)
	path := showCode(t, b)
	personalDevice.get(path, "en")
	confirmPersonalDevice(t, b, personNamed(t, b, "Mia"), "Mia's tablet")
	if r := personalDevice.get(path, "en"); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("re-pairing = %d %q", r.status, r.location)
	}
	if r := personalDevice.get("/", "de"); r.status != http.StatusOK {
		t.Fatalf("paired again, GET / = %d", r.status)
	}
}

func TestPairingSetsTheDeviceMarker(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	path := showCode(t, b)
	req, _ := http.NewRequest(http.MethodGet, b.srv.URL+path, nil)
	ph := newScreen(b)
	ph.get(path, "en")
	confirmPersonalDevice(t, b, personNamed(t, b, "Mia"), "Personal device")
	res, err := ph.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	var found bool
	for _, c := range res.Cookies() {
		if c.Name == "hejmo_device" {
			found = true
			if c.Value != "1" || !c.HttpOnly || c.Path != "/" {
				t.Errorf("marker = %+v, want value 1, HttpOnly, path /", c)
			}
		}
	}
	if !found {
		t.Error("pairing sets no marker cookie")
	}
}

func TestPasskeyErrorOnAnUnpairedDeviceShowsTheSignIn(t *testing.T) {
	t.Parallel()
	_, personalDevice, _ := unpairedPersonalDevice(t)
	for _, v := range []string{"failed", "cancelled", "gone"} {
		r := personalDevice.get("/login?passkey="+v, "de")
		if strings.Contains(r.body, "data-unpaired") || !strings.Contains(r.body, `name="password"`) {
			t.Errorf("passkey=%s on an unpaired device did not show the sign-in:\n%s", v, r.body)
		}
	}
}

var signInLinkRe = regexp.MustCompile(`href="(/login\?[^"]*account=1[^"]*)"`)

func TestUnpairedPageLinksToTheSignInWithTheNextPage(t *testing.T) {
	t.Parallel()
	_, personalDevice, _ := unpairedPersonalDevice(t)
	r := personalDevice.get("/login?next=/tasks", "de")
	m := signInLinkRe.FindStringSubmatch(r.body)
	if m == nil {
		t.Fatalf("no sign-in link on the unpaired page:\n%s", r.body)
	}
	u, err := url.Parse(html.UnescapeString(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	if q := u.Query(); u.Path != "/login" || q.Get("account") != "1" || q.Get("next") != "/tasks" {
		t.Errorf("sign-in link = %q, want /login with account=1 and next=/tasks", m[1])
	}
}

func TestPairingAgainAfterASecondRevokeShowsThePageAgain(t *testing.T) {
	t.Parallel()
	b, personalDevice, _ := unpairedPersonalDevice(t)
	path := showCode(t, b)
	personalDevice.get(path, "en")
	confirmPersonalDevice(t, b, personNamed(t, b, "Mia"), "Mia's tablet")
	personalDevice.get(path, "en")
	id := personalDeviceIDRe.FindStringSubmatch(b.get("/personal-devices").body)[1]
	if r := b.post("/personal-devices/"+id+"/revoke", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("revoke = %d", r.status)
	}
	if r := personalDevice.get("/login", "de"); !strings.Contains(r.body, "data-unpaired") {
		t.Fatalf("second revoke did not show the page:\n%s", r.body)
	}
}

// A personal device re-paired for another person (#198) ends its device session the same
// way a removal does, so it shows the unpaired page too.
func TestPersonalDeviceRepairedForAnotherPersonShowsTheUnpairedPage(t *testing.T) {
	t.Parallel()
	f := newRepairFixture(t)
	if r := f.b.post(f.repairAction, url.Values{"person": {f.mia.ID}}); r.status != http.StatusOK {
		t.Fatalf("repair = %d\n%s", r.status, r.body)
	}
	got := f.personalDevice.get("/login", "en")
	if got.status != http.StatusOK || !strings.Contains(got.body, "data-unpaired") {
		t.Errorf("re-paired personal device = %d, want the unpaired page\n%s", got.status, got.body)
	}
	if strings.Contains(got.body, `name="password"`) {
		t.Error("re-paired personal device shows the sign-in")
	}
}
