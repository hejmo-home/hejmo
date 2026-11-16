package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Ticket 199: a browser that held a device session which has ended sees "This
// device is no longer paired" instead of the password sign-in.

// unpairedPhone is a phone paired for Mia whose device session a parent has
// removed again. The parent's box is returned for further steps.
func unpairedPhone(t *testing.T) (*box, *screen, string) {
	t.Helper()
	b, secret := configuredBox(t)
	if r := b.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("login = %d", r.status)
	}
	phone := pairedPhone(t, b, personNamed(t, b, "Mia"), "Mia's phone")
	id := phoneIDRe.FindStringSubmatch(b.get("/phones").body)[1]
	if r := b.post("/phones/"+id+"/revoke", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("revoke = %d", r.status)
	}
	return b, phone, secret
}

func TestUnpairedPhoneShowsNoLongerPairedInsteadOfSignIn(t *testing.T) {
	_, phone, _ := unpairedPhone(t)
	de := phone.get("/login", "de")
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
	en := phone.get("/login", "en")
	for _, want := range []string{"This device is no longer paired", "Show this device to your parents."} {
		if !strings.Contains(en.body, want) {
			t.Errorf("en page misses %q", want)
		}
	}
	if strings.Contains(strings.ReplaceAll(en.body, "Settings › Phones", ""), "phone") {
		t.Errorf("en page says phone:\n%s", en.body)
	}
}

func TestUnpairedPageKnowsNothingOfTheHousehold(t *testing.T) {
	_, phone, _ := unpairedPhone(t)
	body := phone.get("/login", "de").body
	for _, who := range []string{"Mama", "Papa", "Sebastian", "Mia", "Pasing"} {
		if strings.Contains(body, who) {
			t.Errorf("page names %q", who)
		}
	}
}

func TestFreshBrowserStillGetsTheSignIn(t *testing.T) {
	b, _, _ := unpairedPhone(t)
	r := newScreen(b).get("/login", "de")
	if !strings.Contains(r.body, `name="password"`) || strings.Contains(r.body, "data-unpaired") {
		t.Fatalf("fresh browser did not get the plain sign-in:\n%s", r.body)
	}
}

func TestEndedAdultBrowserSessionKeepsTheSignedOutNotice(t *testing.T) {
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

func TestUnpairedPhoneRunOutSessionShowsThePageToo(t *testing.T) {
	b, secret := configuredBox(t)
	phone := pairedPhone(t, signedInFrom(t, b, secret), personNamed(t, b, "Mia"), "Phone")
	b.now = b.now.Add(400 * 24 * time.Hour)
	if r := phone.get("/login", "de"); !strings.Contains(r.body, "data-unpaired") {
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
	_, phone, _ := unpairedPhone(t)
	r := phone.get("/login?account=1", "de")
	if !strings.Contains(r.body, `name="password"`) || strings.Contains(r.body, "data-signed-out") || strings.Contains(r.body, "data-unpaired") {
		t.Fatalf("account=1 page:\n%s", r.body)
	}
	if again := phone.get("/login", "de"); !strings.Contains(again.body, "data-unpaired") {
		t.Fatalf("going back from the sign-in did not show the page again")
	}
}

func TestSuccessfulSignInForgetsTheUnpairedMarker(t *testing.T) {
	b, phone, secret := unpairedPhone(t)
	if r := phone.post("/login", loginForm(b, secret, "correct horse battery")); r.status != http.StatusSeeOther {
		t.Fatalf("adult sign-in on the phone = %d", r.status)
	}
	phone.post("/logout", url.Values{})
	if r := phone.get("/login", "de"); strings.Contains(r.body, "data-unpaired") || !strings.Contains(r.body, `name="password"`) {
		t.Fatalf("after an adult's own sign-in and sign-out the page still shows:\n%s", r.body)
	}
}

func TestOwnSignOutOnTheDeviceForgetsTheMarker(t *testing.T) {
	b := signedInBox(t)
	phone := pairedPhone(t, b, personNamed(t, b, "Mama"), "Phone")
	phone.post("/logout", url.Values{})
	if r := phone.get("/login", "de"); strings.Contains(r.body, "data-unpaired") {
		t.Fatalf("a device that signed out itself shows the unpaired page:\n%s", r.body)
	}
}

func TestPairingAgainForgetsTheUnpairedPage(t *testing.T) {
	b, phone, _ := unpairedPhone(t)
	path := showCode(t, b)
	phone.get(path, "en")
	confirmPhone(t, b, personNamed(t, b, "Mia"), "Mia's phone")
	if r := phone.get(path, "en"); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("re-pairing = %d %q", r.status, r.location)
	}
	if r := phone.get("/", "de"); r.status != http.StatusOK {
		t.Fatalf("paired again, GET / = %d", r.status)
	}
}

func TestPairingSetsTheDeviceMarker(t *testing.T) {
	b := signedInBox(t)
	path := showCode(t, b)
	req, _ := http.NewRequest(http.MethodGet, b.srv.URL+path, nil)
	ph := newScreen(b)
	ph.get(path, "en")
	confirmPhone(t, b, personNamed(t, b, "Mia"), "Phone")
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
	_, phone, _ := unpairedPhone(t)
	for _, v := range []string{"failed", "cancelled", "gone"} {
		r := phone.get("/login?passkey="+v, "de")
		if strings.Contains(r.body, "data-unpaired") || !strings.Contains(r.body, `name="password"`) {
			t.Errorf("passkey=%s on an unpaired device did not show the sign-in:\n%s", v, r.body)
		}
	}
}

func TestUnpairedPageLinksToTheSignInWithTheNextPage(t *testing.T) {
	_, phone, _ := unpairedPhone(t)
	r := phone.get("/login?next=/tasks", "de")
	if !strings.Contains(r.body, `href="/login?account=1&amp;next=%2ftasks"`) && !strings.Contains(r.body, `href="/login?account=1&amp;next=/tasks"`) && !strings.Contains(r.body, `href="/login?account=1&next=%2Ftasks"`) {
		t.Errorf("sign-in link drops next:\n%s", r.body)
	}
}

func TestPairingAgainAfterASecondRevokeShowsThePageAgain(t *testing.T) {
	b, phone, _ := unpairedPhone(t)
	path := showCode(t, b)
	phone.get(path, "en")
	confirmPhone(t, b, personNamed(t, b, "Mia"), "Mia's phone")
	phone.get(path, "en")
	id := phoneIDRe.FindStringSubmatch(b.get("/phones").body)[1]
	if r := b.post("/phones/"+id+"/revoke", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("revoke = %d", r.status)
	}
	if r := phone.get("/login", "de"); !strings.Contains(r.body, "data-unpaired") {
		t.Fatalf("second revoke did not show the page:\n%s", r.body)
	}
}
