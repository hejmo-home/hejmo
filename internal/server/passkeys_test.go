package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/passkey/passkeytest"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 95 (7.6, ADR 0018): passkeys. A passkey is valid for the box name,
// here the own CA's name; the test box is reached by its IP address unless a
// request says otherwise with Host. The authenticator is a virtual one: no real
// credentials are involved.
const boxName = "hejmo.local"

// asBrowserOn sends the request as a browser that reached the box under host
// does, and that is the same browser as everything else in the test: the Go
// client keeps cookies per Host header, so the cookies of the test server's
// address are carried over by hand.
func (b *box) asBrowserOn(host string, req *http.Request) resp {
	b.t.Helper()
	req.Host = host
	u, _ := url.Parse(b.srv.URL)
	for _, c := range b.client.Jar.Cookies(u) {
		req.AddCookie(c)
	}
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	b.client.Jar.SetCookies(u, res.Cookies())
	out, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), string(out)}
}

// jsonAt sends a JSON request as a browser on host would.
func (b *box) jsonAt(host, method, path string, body any) resp {
	b.t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, b.srv.URL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://"+host)
	return b.asBrowserOn(host, req)
}

// at is a request of a browser that reached the box under host.
func (b *box) at(host, method, path string, form url.Values) resp {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, b.srv.URL+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://"+host)
	}
	return b.asBrowserOn(host, req)
}

type ceremonyJSON struct {
	Ceremony string          `json:"ceremony"`
	Options  json.RawMessage `json:"options"`
}

func begin(t *testing.T, b *box, host, path string) (ceremonyJSON, resp) {
	t.Helper()
	r := b.jsonAt(host, http.MethodPost, path, nil)
	var c ceremonyJSON
	if r.status == http.StatusOK {
		decodeJSON(t, r.body, &c)
	}
	return c, r
}

// addPasskeyAt creates a passkey on the authenticator for the signed-in (or
// wizard) person of the browser and returns the box's answer.
func addPasskeyAt(t *testing.T, b *box, dev *passkeytest.Authenticator, host, name string) resp {
	t.Helper()
	c, r := begin(t, b, host, "/account/passkeys/begin")
	if r.status != http.StatusOK {
		t.Fatalf("begin registration = %d %s", r.status, r.body)
	}
	cred, err := dev.Create(c.Options, "https://"+host)
	if err != nil {
		t.Fatal(err)
	}
	return b.jsonAt(host, http.MethodPost, "/account/passkeys/finish", map[string]any{"ceremony": c.Ceremony, "credential": json.RawMessage(cred), "name": name})
}

// signInWithPasskey runs the sign-in ceremony in a browser on host.
func signInWithPasskey(t *testing.T, b *box, dev *passkeytest.Authenticator, host string) resp {
	t.Helper()
	c, r := begin(t, b, host, "/login/passkey/begin")
	if r.status != http.StatusOK {
		t.Fatalf("begin sign-in = %d %s", r.status, r.body)
	}
	cred, err := dev.Get(c.Options, "https://"+host)
	if err != nil {
		t.Fatal(err)
	}
	return b.jsonAt(host, http.MethodPost, "/login/passkey/finish", map[string]any{"ceremony": c.Ceremony, "credential": json.RawMessage(cred)})
}

func redirectOf(t *testing.T, r resp) string {
	t.Helper()
	var out struct{ Redirect string }
	decodeJSON(t, r.body, &out)
	return out.Redirect
}

// mamaWithPasskey returns a box where Mama has signed in with password and code
// and created a passkey called "Mac" under the box name; she is signed out again.
func mamaWithPasskey(t *testing.T) (*box, string, *passkeytest.Authenticator) {
	t.Helper()
	b, secret, _ := setupWithCodes(t)
	if r := loginWith(b, "mama", "correct horse battery", totpNow(b, secret)); r.status != http.StatusSeeOther {
		t.Fatalf("sign-in = %d %s", r.status, r.body)
	}
	dev := passkeytest.New()
	if r := addPasskeyAt(t, b, dev, boxName, "Mac"); r.status != http.StatusOK {
		t.Fatalf("create passkey = %d %s", r.status, r.body)
	}
	b.logout()
	return b, secret, dev
}

// 7.6: Given a parent with a passkey, when they sign in with it, then no
// password or one-time code is asked.
func TestParentSignsInWithAPasskeyWithoutPasswordOrOneTimeCode(t *testing.T) {
	t.Parallel()
	b, _, dev := mamaWithPasskey(t)
	if r := b.get("/"); r.status != http.StatusSeeOther {
		t.Fatalf("signed out: GET / = %d", r.status)
	}
	r := signInWithPasskey(t, b, dev, boxName)
	if r.status != http.StatusOK || redirectOf(t, r) != "/" {
		t.Fatalf("passkey sign-in = %d %s", r.status, r.body)
	}
	if home := b.at(boxName, http.MethodGet, "/", nil); home.status != http.StatusOK {
		t.Fatalf("not signed in after the passkey: %d", home.status)
	}
	if !strings.Contains(b.get("/account").body, "Damit bist du gerade angemeldet") {
		t.Error("Mein Konto does not say which passkey the parent is signed in with")
	}
}

// 7.6: a passkey sign-in sends the parent where they wanted to go.
func TestPasskeySignInLeadsToTheRequestedPage(t *testing.T) {
	t.Parallel()
	b, _, dev := mamaWithPasskey(t)
	c, _ := begin(t, b, boxName, "/login/passkey/begin")
	cred, _ := dev.Get(c.Options, "https://"+boxName)
	r := b.jsonAt(boxName, http.MethodPost, "/login/passkey/finish", map[string]any{"ceremony": c.Ceremony, "credential": json.RawMessage(cred), "next": "/tasks"})
	if redirectOf(t, r) != "/tasks" {
		t.Fatalf("redirect = %q, want /tasks", redirectOf(t, r))
	}
	c, _ = begin(t, b, boxName, "/login/passkey/begin")
	cred, _ = dev.Get(c.Options, "https://"+boxName)
	r = b.jsonAt(boxName, http.MethodPost, "/login/passkey/finish", map[string]any{"ceremony": c.Ceremony, "credential": json.RawMessage(cred), "next": "https://evil.example/"})
	if redirectOf(t, r) != "/" {
		t.Fatalf("an outside address was followed: %q", redirectOf(t, r))
	}
}

// 7.6: the passkey is remembered on the browser as "used", never by name.
func TestPasskeySignInMakesThePasskeyTheMainActionOnThisDevice(t *testing.T) {
	t.Parallel()
	b, _, dev := mamaWithPasskey(t)
	login := b.at(boxName, http.MethodGet, "/login", nil).body
	for _, want := range []string{`data-passkey-signin`, `class="or"`, `class="signin-pk"`, `autocomplete="username webauthn"`} {
		if !strings.Contains(login, want) {
			t.Errorf("login page lacks %q\n%s", want, login)
		}
	}
	if strings.Contains(login, `<details class="more"><summary>`) && strings.Contains(login, `class="button pk"`) {
		t.Error("the passkey is the main action on a device that never used one")
	}
	r := signInWithPasskey(t, b, dev, boxName)
	if r.status != http.StatusOK {
		t.Fatalf("sign-in = %d %s", r.status, r.body)
	}
	b.at(boxName, http.MethodPost, "/logout", url.Values{}) // signing out leaves the flag of the browser
	login = b.at(boxName, http.MethodGet, "/login", nil).body
	if !strings.Contains(login, `class="button pk"`) || !strings.Contains(login, "Mit Passwort und Einmalcode anmelden") {
		t.Errorf("after a passkey sign-in the passkey is not the main action\n%s", login)
	}
	if strings.Contains(login, "Mama") || strings.Contains(login, `value="mama"`) {
		t.Error("the page names who signed in last")
	}
}

// 7.6: Given the box reached by IP address, then no passkey is offered and
// password sign-in works.
func TestNoPasskeyIsOfferedOverAnIPAddressAndPasswordSignInWorks(t *testing.T) {
	t.Parallel()
	b, secret, _ := mamaWithPasskey(t)
	login := b.get("/login").body // the test box is at 127.0.0.1
	if strings.Contains(login, "data-passkey-signin") {
		t.Error("the sign-in page offers a passkey over an IP address")
	}
	if !strings.Contains(login, "Passkeys gehen nur unter dem Namen der Box") || !strings.Contains(login, `href="https://hejmo.local/login"`) {
		t.Errorf("no notice with the way to the box name\n%s", login)
	}
	if r := b.jsonAt("127.0.0.1", http.MethodPost, "/login/passkey/begin", nil); r.status != http.StatusConflict || !strings.Contains(r.body, `"name"`) {
		t.Errorf("sign-in with a passkey over the IP address = %d %s, want 409", r.status, r.body)
	}
	if r := loginWith(b, "mama", "correct horse battery", totpNow(b, secret)); r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("password sign-in over the IP address = %d %q", r.status, r.location)
	}
	if r := b.jsonAt("127.0.0.1", http.MethodPost, "/account/passkeys/begin", nil); r.status != http.StatusConflict || !strings.Contains(r.body, `"name"`) {
		t.Errorf("creating a passkey over the IP address = %d %s, want 409", r.status, r.body)
	}
	account := b.get("/account").body
	if strings.Contains(account, "data-passkey-add") || !strings.Contains(account, "Passkeys gelten nur für") || !strings.Contains(account, `href="https://hejmo.local/account"`) {
		t.Errorf("Mein Konto over an IP address offers to create a passkey or lacks the way to the box name\n%s", account)
	}
	if !strings.Contains(account, `href="/account/passkeys/`) {
		t.Error("the passkeys are not listed over an IP address")
	}
	// Under another name than the box name: also none.
	if r := b.jsonAt("fritz.box", http.MethodPost, "/account/passkeys/begin", nil); r.status != http.StatusConflict {
		t.Errorf("registration under another name = %d, want 409", r.status)
	}
}

// 7.6: Given a revoked passkey, then it cannot sign in.
func TestARevokedPasskeyCannotSignIn(t *testing.T) {
	t.Parallel()
	b, secret, dev := mamaWithPasskey(t)
	loginWith(b, "mama", "correct horse battery", totpNow(b, secret))
	acc, _ := b.db.AccountByUsername("mama")
	list, _ := b.db.Passkeys(acc.PersonID)
	if len(list) != 1 {
		t.Fatalf("%d passkeys", len(list))
	}
	if r := b.at(boxName, http.MethodPost, "/account/passkeys/"+list[0].ID+"/remove", url.Values{}); r.status != http.StatusSeeOther || r.location != "/account" {
		t.Fatalf("remove = %d %q", r.status, r.location)
	}
	b.logout()
	r := signInWithPasskey(t, b, dev, boxName)
	if r.status != http.StatusUnauthorized || !strings.Contains(r.body, "passkey_gone") {
		t.Fatalf("sign-in with a revoked passkey = %d %s, want 401 passkey_gone", r.status, r.body)
	}
	if home := b.get("/"); home.status != http.StatusSeeOther {
		t.Fatalf("a revoked passkey signed in: %d", home.status)
	}
	if page := b.at(boxName, http.MethodGet, "/login?passkey=gone", nil).body; !strings.Contains(page, "Dieser Passkey gilt nicht mehr") || !strings.Contains(page, "Anderen Passkey wählen") {
		t.Errorf("the page after a refused passkey does not say why\n%s", page)
	}
}

// 7.6: a passkey of an earlier box name signs nobody in.
func TestAPasskeyOfAnEarlierBoxNameDoesNotSignIn(t *testing.T) {
	t.Parallel()
	b, secret, _ := mamaWithPasskey(t)
	b.public.name = "familie-mueller.dedyn.io" // the box has a public name now
	other := passkeytest.New()                 // a device that holds nothing for the new name
	c, r := begin(t, b, "familie-mueller.dedyn.io", "/login/passkey/begin")
	if r.status != http.StatusOK || !strings.Contains(string(c.Options), `"rpId":"familie-mueller.dedyn.io"`) {
		t.Fatalf("the sign-in does not ask for the new box name: %d %s", r.status, r.body)
	}
	if _, err := other.Get(c.Options, "https://familie-mueller.dedyn.io"); err == nil {
		t.Fatal("virtual authenticator answered without a passkey")
	}
	// The old name no longer works as the box name.
	if r := b.jsonAt(boxName, http.MethodPost, "/login/passkey/begin", nil); r.status != http.StatusConflict {
		t.Errorf("the old name is still accepted: %d", r.status)
	}
	loginWith(b, "mama", "correct horse battery", totpNow(b, secret))
	page := b.at("familie-mueller.dedyn.io", http.MethodGet, "/account", nil).body
	for _, want := range []string{"Funktioniert nicht mehr: gilt für hejmo.local", `class="flag warn"`, "Die Box heißt jetzt", "Neuen Passkey für dieses Gerät anlegen"} {
		if !strings.Contains(page, want) {
			t.Errorf("Mein Konto after a change of the box name lacks %q\n%s", want, page)
		}
	}
}

// 7.6: Given a reset of one-time codes, then a passkey sign-in still works and
// asks to set up new one-time codes.
func TestPasskeySignInAfterAResetStillWorksAndAsksForNewOneTimeCodes(t *testing.T) {
	t.Parallel()
	b, mamaSecret, _ := setupWithCodes(t)
	papa, papaSecret := secondParent(t, b)
	loginWith(b, "papa", "papa's long password", totpNow(b, papaSecret))
	dev := passkeytest.New()
	if r := addPasskeyAt(t, b, dev, boxName, "Mac im Arbeitszimmer"); r.status != http.StatusOK {
		t.Fatalf("Papa's passkey = %d %s", r.status, r.body)
	}
	b.logout()
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	own := url.Values{"password": {"correct horse battery"}, "code": {totpNow(b, mamaSecret)}}
	if r := b.at(boxName, http.MethodPost, "/account/reset/"+papa.ID, own); r.status != http.StatusSeeOther || r.location != "/account" {
		t.Fatalf("reset = %d %q\n%s", r.status, r.location, r.body)
	}
	b.logout()

	r := signInWithPasskey(t, b, dev, boxName)
	if r.status != http.StatusOK || redirectOf(t, r) != "/setup/totp" {
		t.Fatalf("passkey sign-in after the reset = %d %s, want to be led to the new one-time codes", r.status, r.body)
	}
	if home := b.get("/"); home.status != http.StatusSeeOther {
		t.Fatalf("after the reset a passkey session reaches the home page: %d", home.status)
	}
	setup := b.get("/setup/totp")
	if setup.status != http.StatusOK {
		t.Fatalf("setup page = %d", setup.status)
	}
	for _, want := range []string{"Mama hat deine Einmalcodes am", "Mac im Arbeitszimmer", "Neue Einmalcodes einrichten"} {
		if !strings.Contains(setup.body, want) {
			t.Errorf("setup after a reset lacks %q\n%s", want, setup.body)
		}
	}
	// He sets the codes up and is a normal signed-in parent afterwards.
	code := totpNow(b, secretOn(t, setup.body))
	if r := b.post("/setup/totp", url.Values{"code": {code}}); r.status != http.StatusOK {
		t.Fatalf("confirm new codes = %d\n%s", r.status, r.body)
	}
	if home := b.get("/"); home.status != http.StatusOK {
		t.Fatalf("after setting up new codes: %d", home.status)
	}
}

// ADR 0018 review item, product owner Q9: the page that resets another
// parent's one-time codes lists that parent's passkeys with checkboxes, none
// ticked; the ticked ones are removed with the reset.
func TestResetPageListsTheOtherParentsPasskeysAndRemovesTheTickedOnes(t *testing.T) {
	t.Parallel()
	b, mamaSecret, _ := setupWithCodes(t)
	papa, papaSecret := secondParent(t, b)
	loginWith(b, "papa", "papa's long password", totpNow(b, papaSecret))
	personalDevice, mac := passkeytest.New(), passkeytest.New()
	addPasskeyAt(t, b, personalDevice, boxName, "iPhone")
	addPasskeyAt(t, b, mac, boxName, "Mac im Arbeitszimmer")
	list, _ := b.db.Passkeys(papa.ID)
	b.logout()
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))

	page := b.at(boxName, http.MethodGet, "/account/reset/"+papa.ID, nil)
	if page.status != http.StatusOK {
		t.Fatalf("reset page = %d", page.status)
	}
	boxes := regexp.MustCompile(`<input type="checkbox" name="remove_passkey" value="([^"]+)"([^>]*)>`).FindAllStringSubmatch(page.body, -1)
	if len(boxes) != 2 {
		t.Fatalf("%d checkboxes, want one per passkey of Papa\n%s", len(boxes), page.body)
	}
	for _, m := range boxes {
		if strings.Contains(m[2], "checked") {
			t.Error("a passkey is ticked in advance")
		}
	}
	for _, want := range []string{`class="pk-pick"`, "<b>iPhone</b>", "<b>Mac im Arbeitszimmer</b>", "Lag ein Passkey von Papa auf dem verlorenen Gerät?"} {
		if !strings.Contains(page.body, want) {
			t.Errorf("reset page lacks %q", want)
		}
	}

	// A wrong confirmation changes nothing and keeps the tick.
	bad := url.Values{"password": {"wrong horse battery"}, "code": {"000000"}, "remove_passkey": {list[0].ID}}
	r := b.at(boxName, http.MethodPost, "/account/reset/"+papa.ID, bad)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `value="`+list[0].ID+`" checked`) || !strings.Contains(r.body, "Es wurde nichts zurückgesetzt") {
		t.Fatalf("wrong confirmation = %d\n%s", r.status, r.body)
	}
	if left, _ := b.db.Passkeys(papa.ID); len(left) != 2 {
		t.Fatal("a wrong confirmation removed a passkey")
	}

	ok := url.Values{"password": {"correct horse battery"}, "code": {totpNow(b, mamaSecret)}, "remove_passkey": {list[0].ID}}
	if r := b.at(boxName, http.MethodPost, "/account/reset/"+papa.ID, ok); r.status != http.StatusSeeOther || r.location != "/account" {
		t.Fatalf("reset = %d %q\n%s", r.status, r.location, r.body)
	}
	if left, _ := b.db.Passkeys(papa.ID); len(left) != 1 || left[0].ID != list[1].ID {
		t.Fatalf("Papa's passkeys after = %+v, want only the unticked one", left)
	}
	done := b.at(boxName, http.MethodGet, "/account", nil).body
	if line := successLine(done); !strings.Contains(line, "Die Einmalcodes von Papa wurden zurückgesetzt") || !strings.Contains(line, "Papas Passkey „iPhone“ wurde entfernt") {
		t.Errorf("success line = %q", line)
	}
	if !strings.Contains(done, "Einmalcodes zurückgesetzt am") {
		t.Error("Papa's row does not say the codes were reset")
	}
	if !strings.Contains(done, `href="/personal-devices"`) || !strings.Contains(done, `href="/caldav"`) {
		t.Error("the reminder about paired personal devices and calendar apps is gone")
	}
	// The passkey that was removed cannot sign in; the other one still can.
	b.logout()
	if r := signInWithPasskey(t, b, personalDevice, boxName); r.status != http.StatusUnauthorized {
		t.Errorf("the removed passkey signed in: %d", r.status)
	}
	if r := signInWithPasskey(t, b, mac, boxName); r.status != http.StatusOK {
		t.Errorf("the other passkey does not sign in: %d %s", r.status, r.body)
	}
}

// ADR 0018: a parent with a passkey confirms the reset with it, instead of
// password and one-time code.
func TestResetIsConfirmedWithTheParentsOwnPasskey(t *testing.T) {
	t.Parallel()
	b, mamaSecret, _ := setupWithCodes(t)
	papa, _ := secondParent(t, b)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	dev := passkeytest.New()
	addPasskeyAt(t, b, dev, boxName, "Mac")

	page := b.at(boxName, http.MethodGet, "/account/reset/"+papa.ID, nil).body
	if !strings.Contains(page, "data-passkey-confirm") || !strings.Contains(page, "Stattdessen mit Passwort und Einmalcode bestätigen") {
		t.Fatalf("the reset page offers no passkey confirmation\n%s", page)
	}
	assertion := func() url.Values {
		c, r := begin(t, b, boxName, "/account/passkeys/assert/begin")
		if r.status != http.StatusOK {
			t.Fatalf("begin confirmation = %d %s", r.status, r.body)
		}
		cred, err := dev.Get(c.Options, "https://"+boxName)
		if err != nil {
			t.Fatal(err)
		}
		return url.Values{"ceremony": {c.Ceremony}, "assertion": {string(cred)}}
	}
	// A confirmation that was made for another ceremony or is garbage resets nothing.
	if r := b.at(boxName, http.MethodPost, "/account/reset/"+papa.ID, url.Values{"ceremony": {"nope"}, "assertion": {"{}"}}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("a made-up confirmation = %d", r.status)
	}
	if acc, _ := b.db.AccountByPerson(papa.ID); acc.TOTPReset {
		t.Fatal("a made-up confirmation reset the codes")
	}
	form := assertion()
	if r := b.at(boxName, http.MethodPost, "/account/reset/"+papa.ID, form); r.status != http.StatusSeeOther || r.location != "/account" {
		t.Fatalf("reset with a passkey = %d %q\n%s", r.status, r.location, r.body)
	}
	if acc, _ := b.db.AccountByPerson(papa.ID); !acc.TOTPReset || acc.TOTPResetBy == "" {
		t.Fatalf("the codes were not reset: %+v", acc)
	}
	// The same assertion cannot be used twice.
	papa2, _ := secondParentNamed(t, b, "Oma", "oma")
	if r := b.at(boxName, http.MethodPost, "/account/reset/"+papa2.ID, form); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("an assertion was used twice: %d", r.status)
	}
	// Over an IP address the password fields are shown directly.
	ip := b.get("/account/reset/" + papa2.ID).body
	if strings.Contains(ip, "data-passkey-confirm") || !strings.Contains(ip, `name="password"`) || strings.Contains(ip, "Stattdessen mit Passwort") {
		t.Errorf("over an IP address the reset page offers the passkey or hides the password\n%s", ip)
	}
}

// secondParentNamed adds a further parent with a confirmed account.
func secondParentNamed(t *testing.T, b *box, name, username string) (store.Person, string) {
	t.Helper()
	h, _ := b.db.HomeHousehold()
	p, err := b.db.AddPerson(h.ID, store.Person{Name: name, Role: store.RoleParent, Color: "#4E7D52", Shape: "square-filled"})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword(username + "'s long password")
	sec := auth.NewTOTPSecret()
	if err := b.db.CreateAccount(store.Account{PersonID: p.ID, Username: username, PasswordHash: hash, TOTPSecret: sec}); err != nil {
		t.Fatal(err)
	}
	_ = b.db.ConfirmTOTP(p.ID)
	return p, sec
}

// ADR 0018: the setup wizard offers a passkey right after the account.
func TestWizardOffersAPasskeyAfterTheAccountAndLeadsOnToTheOneTimeCode(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	r := b.at(boxName, http.MethodPost, "/setup", validSetupForm())
	if r.status != http.StatusSeeOther || r.location != "/setup/passkey" {
		t.Fatalf("account created under the box name = %d %q, want the passkey offer", r.status, r.location)
	}
	offer := b.at(boxName, http.MethodGet, "/setup/passkey", nil)
	for _, want := range []string{"Schneller anmelden mit Passkey?", `class="pk-why"`, `action="/account/passkeys"`, "Passkey anlegen", `href="/setup/totp"`, "Jetzt nicht, weiter zum Einmalcode", "Konto angelegt. Du bist als Mama angemeldet.", `<span class="host">hejmo.local</span>`, `name="name"`, "Einrichtung: Schritt 2 von 4"} {
		if !strings.Contains(offer.body, want) {
			t.Errorf("the offer lacks %q\n%s", want, offer.body)
		}
	}
	dev := passkeytest.New()
	fin := addPasskeyAt(t, b, dev, boxName, "Mac")
	if fin.status != http.StatusOK || redirectOf(t, fin) != "/setup/totp" {
		t.Fatalf("create in the wizard = %d %s", fin.status, fin.body)
	}
	setup := b.at(boxName, http.MethodGet, "/setup/totp", nil)
	if line := successLine(setup.body); !strings.Contains(line, "Passkey „Mac“ angelegt") {
		t.Errorf("one-time code step shows %q", line)
	}
	if !strings.Contains(setup.body, "Der Einmalcode ist dein Weg zurück") {
		t.Errorf("the one-time code step does not say why it is still needed\n%s", setup.body)
	}
	// The wizard goes on as before: the code is still set up.
	code := totpNow(b, secretOn(t, setup.body))
	if r := b.post("/setup/totp", url.Values{"code": {code}}); r.status != http.StatusOK || len(recoveryCodesOn(t, r.body)) != 10 {
		t.Fatalf("one-time code after the passkey = %d", r.status)
	}
	b.logout()
	if r := signInWithPasskey(t, b, dev, boxName); r.status != http.StatusOK || redirectOf(t, r) != "/" {
		t.Fatalf("sign-in with the wizard's passkey = %d %s", r.status, r.body)
	}
}

func TestWizardSkipsTheOfferOverAnIPAddress(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	if r := b.post("/setup", validSetupForm()); r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		t.Fatalf("account created over the IP address = %d %q, want straight on to the one-time code", r.status, r.location)
	}
	if r := b.get("/setup/passkey"); r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		t.Fatalf("offer over the IP address = %d %q", r.status, r.location)
	}
}

func TestOfferWithoutJavaScriptSaysNothingWasCreated(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	b.at(boxName, http.MethodPost, "/setup", validSetupForm())
	r := b.at(boxName, http.MethodPost, "/account/passkeys", url.Values{"name": {"Mac"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `data-state="fail"`) || !strings.Contains(r.body, "Es wurde kein Passkey angelegt") || !strings.Contains(r.body, "Nochmal versuchen") {
		t.Fatalf("the form without a script = %d\n%s", r.status, r.body)
	}
}

// Mein Konto: list, detail page, rename and remove.
func TestMeinKontoListsRenamesAndRemovesPasskeys(t *testing.T) {
	t.Parallel()
	b, secret, _ := mamaWithPasskey(t)
	loginWith(b, "mama", "correct horse battery", totpNow(b, secret))
	acc, _ := b.db.AccountByUsername("mama")
	page := b.at(boxName, http.MethodGet, "/account", nil)
	for _, want := range []string{"<h1>Mein Konto</h1>", "Angemeldet als Mama, Benutzername mama.", "<b>Mac</b>", "noch nicht benutzt", "data-passkey-add", "Weiteren Passkey anlegen", "Passkeys gelten nur für", "Eingerichtet am"} {
		if !strings.Contains(page.body, want) {
			t.Errorf("Mein Konto lacks %q\n%s", want, page.body)
		}
	}
	if strings.Contains(page.body, ">Einmalcodes<") && strings.Contains(page.body, `href="/account"`) {
		t.Error("the old page is still linked")
	}
	list, _ := b.db.Passkeys(acc.PersonID)
	detail := b.at(boxName, http.MethodGet, "/account/passkeys/"+list[0].ID, nil)
	for _, want := range []string{"Passkey „Mac“", "Funktioniert.", "Gilt für", `name="name"`, "Passkey entfernen", `<details class="danger"`, "Nein, nichts ändern"} {
		if !strings.Contains(detail.body, want) {
			t.Errorf("detail page lacks %q\n%s", want, detail.body)
		}
	}
	if r := b.at(boxName, http.MethodPost, "/account/passkeys/"+list[0].ID, url.Values{"name": {"  "}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("an empty name = %d", r.status)
	}
	if r := b.at(boxName, http.MethodPost, "/account/passkeys/"+list[0].ID, url.Values{"name": {"Mac im Arbeitszimmer"}}); r.status != http.StatusSeeOther || r.location != "/account/passkeys/"+list[0].ID {
		t.Fatalf("rename = %d %q", r.status, r.location)
	}
	if got := b.get("/account/passkeys/" + list[0].ID).body; !strings.Contains(got, "Mac im Arbeitszimmer") || successLine(got) == "" {
		t.Errorf("after renaming: no success line or name\n%s", got)
	}
	// Removing names what stays.
	if !strings.Contains(b.get("/account/passkeys/"+list[0].ID).body, "Passwort und Einmalcode bleiben") {
		t.Error("the confirmation does not say what stays")
	}
	r := b.at(boxName, http.MethodPost, "/account/passkeys/"+list[0].ID+"/remove", url.Values{})
	if r.status != http.StatusSeeOther || r.location != "/account" {
		t.Fatalf("remove = %d %q", r.status, r.location)
	}
	after := b.get("/account").body
	if line := successLine(after); !strings.Contains(line, "Passkey „Mac im Arbeitszimmer“ entfernt") {
		t.Errorf("success line = %q", line)
	}
	if !strings.Contains(after, "Noch kein Passkey") {
		t.Error("the empty state is missing")
	}
	if r := b.get("/account/passkeys/" + list[0].ID); r.status != http.StatusNotFound {
		t.Errorf("a removed passkey's page = %d", r.status)
	}
}

// A parent cannot see, rename or remove another parent's passkey.
func TestPasskeysOfAnotherPersonAreNotReachable(t *testing.T) {
	t.Parallel()
	b, mamaSecret, _ := setupWithCodes(t)
	papa, papaSecret := secondParent(t, b)
	loginWith(b, "papa", "papa's long password", totpNow(b, papaSecret))
	addPasskeyAt(t, b, passkeytest.New(), boxName, "Papas")
	list, _ := b.db.Passkeys(papa.ID)
	b.logout()
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	if r := b.get("/account/passkeys/" + list[0].ID); r.status != http.StatusNotFound {
		t.Errorf("Mama sees Papa's passkey: %d", r.status)
	}
	if r := b.at(boxName, http.MethodPost, "/account/passkeys/"+list[0].ID+"/remove", url.Values{}); r.status != http.StatusNotFound {
		t.Errorf("Mama removed Papa's passkey: %d", r.status)
	}
	if left, _ := b.db.Passkeys(papa.ID); len(left) != 1 {
		t.Fatal("Papa's passkey is gone")
	}
	if strings.Contains(b.get("/account").body, "Papas") {
		t.Error("Papa's passkey is listed for Mama")
	}
}

// A passkey cannot be created or used without a person who is allowed.
func TestPasskeyEndpointsNeedASignedInParent(t *testing.T) {
	t.Parallel()
	b, _, _ := setupWithCodes(t)
	if r := b.jsonAt(boxName, http.MethodPost, "/account/passkeys/begin", nil); r.status != http.StatusUnauthorized {
		t.Errorf("registration signed out = %d", r.status)
	}
	if r := b.jsonAt(boxName, http.MethodPost, "/account/passkeys/finish", map[string]any{"ceremony": "x", "credential": map[string]any{}}); r.status != http.StatusUnauthorized {
		t.Errorf("finish signed out = %d", r.status)
	}
	c, _ := begin(t, b, boxName, "/login/passkey/begin")
	if r := b.jsonAt(boxName, http.MethodPost, "/login/passkey/finish", map[string]any{"ceremony": c.Ceremony, "credential": map[string]any{"id": "x"}}); r.status != http.StatusUnauthorized && r.status != http.StatusBadRequest {
		t.Errorf("sign-in with garbage = %d", r.status)
	}
	if r := b.jsonAt(boxName, http.MethodPost, "/login/passkey/finish", map[string]any{"ceremony": "unknown", "credential": map[string]any{}}); r.status != http.StatusBadRequest {
		t.Errorf("sign-in with an unknown ceremony = %d", r.status)
	}
	// A cross-site post is refused.
	req, _ := http.NewRequest(http.MethodPost, b.srv.URL+"/login/passkey/begin", nil)
	req.Header.Set("Origin", "https://evil.example")
	if r := b.asBrowserOn(boxName, req); r.status != http.StatusForbidden {
		t.Errorf("cross-site sign-in begin = %d", r.status)
	}
}

// A ceremony works once and only for the person who began it.
func TestARegistrationCeremonyWorksOnceAndOnlyForItsPerson(t *testing.T) {
	t.Parallel()
	b, mamaSecret, _ := setupWithCodes(t)
	_, papaSecret := secondParent(t, b)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	c, _ := begin(t, b, boxName, "/account/passkeys/begin")
	dev := passkeytest.New()
	cred, _ := dev.Create(c.Options, "https://"+boxName)
	b.logout()
	loginWith(b, "papa", "papa's long password", totpNow(b, papaSecret))
	if r := b.jsonAt(boxName, http.MethodPost, "/account/passkeys/finish", map[string]any{"ceremony": c.Ceremony, "credential": json.RawMessage(cred), "name": "Stolen"}); r.status != http.StatusBadRequest {
		t.Fatalf("another person finished Mama's ceremony: %d %s", r.status, r.body)
	}
	b.logout()
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	if r := b.jsonAt(boxName, http.MethodPost, "/account/passkeys/finish", map[string]any{"ceremony": c.Ceremony, "credential": json.RawMessage(cred), "name": "Mac"}); r.status != http.StatusBadRequest {
		t.Fatalf("a ceremony that was tried by someone else still worked: %d", r.status)
	}
}

// The sign-in page states of the preview (docs 5 to 8).
func TestSignInPageStates(t *testing.T) {
	t.Parallel()
	b, _, _ := mamaWithPasskey(t)
	def := b.at(boxName, http.MethodGet, "/login", nil).body
	if !strings.Contains(def, `<p class="or">oder</p>`) || !strings.Contains(def, "Mit Fingerabdruck, Gesicht oder Geräte-PIN, ohne Passwort und Einmalcode") {
		t.Errorf("default sign-in lacks the passkey way\n%s", def)
	}
	cancelled := b.at(boxName, http.MethodGet, "/login?passkey=cancelled", nil).body
	if !strings.Contains(cancelled, "Anmeldung mit Passkey abgebrochen. Versuche es noch einmal oder melde dich mit Passwort und Einmalcode an.") {
		t.Errorf("cancelled\n%s", cancelled)
	}
	gone := b.at(boxName, http.MethodGet, "/login?passkey=gone", nil).body
	if !strings.Contains(gone, `role="alert"`) || !strings.Contains(gone, "Anderen Passkey wählen") {
		t.Errorf("gone\n%s", gone)
	}
	if strings.Contains(b.at(boxName, http.MethodGet, "/login?passkey=%3Cscript%3E", nil).body, "<script>") {
		t.Error("the passkey parameter is shown unescaped")
	}
	en := b.do(http.MethodGet, "/login", nil, map[string]string{"Host": boxName, "Accept-Language": "en"}).body
	if !strings.Contains(en, "Sign in with a passkey") {
		t.Errorf("English sign-in lacks the passkey button\n%s", en)
	}
	// No passkey exists on a box that nobody added one to: no notice over an IP address, no button anywhere useless.
	fresh, _, _ := setupWithCodes(t)
	if p := fresh.get("/login").body; strings.Contains(p, "Passkeys gehen nur unter dem Namen der Box") {
		t.Error("a box without passkeys advertises them over an IP address")
	}
}

// ADR 0015: everything a page can do is possible through the REST API.
func TestPasskeysThroughTheAPI(t *testing.T) {
	t.Parallel()
	b, secret, _ := mamaWithPasskey(t)
	loginWith(b, "mama", "correct horse battery", totpNow(b, secret))
	c := newAPIClient(t, b)
	c.cookie = true
	status, _, list := c.call(http.MethodGet, "/me/passkeys", nil)
	if status != http.StatusOK || len(list) != 1 {
		t.Fatalf("list = %d %v", status, list)
	}
	pk := list[0].(map[string]any)
	if pk["name"] != "Mac" || pk["box_name"] != boxName || pk["works"] != true || pk["last_used_at"] != nil {
		t.Fatalf("passkey = %v", pk)
	}
	id := pk["id"].(string)
	if status, body, _ := c.call(http.MethodPatch, "/me/passkeys/"+id, map[string]any{"name": "Büro-Mac"}); status != http.StatusOK || body["name"] != "Büro-Mac" {
		t.Fatalf("rename = %d %v", status, body)
	}
	if status, _, _ := c.call(http.MethodPatch, "/me/passkeys/"+id, map[string]any{"name": " "}); status != http.StatusUnprocessableEntity {
		t.Fatalf("empty name = %d", status)
	}
	// Creating one: begin and finish, under the box name.
	c.host = boxName
	status, begun, _ := c.call(http.MethodPost, "/me/passkeys/registration", nil)
	if status != http.StatusOK || begun["ceremony"] == nil {
		t.Fatalf("begin = %d %v", status, begun)
	}
	opts, _ := json.Marshal(begun["options"])
	dev := passkeytest.New()
	cred, err := dev.Create(opts, "https://"+boxName)
	if err != nil {
		t.Fatal(err)
	}
	status, made, _ := c.call(http.MethodPost, "/me/passkeys", map[string]any{"ceremony": begun["ceremony"], "credential": json.RawMessage(cred), "name": "iPad"})
	if status != http.StatusCreated || made["name"] != "iPad" {
		t.Fatalf("finish = %d %v", status, made)
	}
	c.host = ""
	if status, body, _ := c.call(http.MethodPost, "/me/passkeys/registration", nil); status != http.StatusConflict {
		t.Fatalf("begin over the IP address = %d %v, want 409", status, body)
	}
	if status, _, _ := c.call(http.MethodDelete, "/me/passkeys/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/me/passkeys/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("delete again = %d", status)
	}
	if status, _, list := c.call(http.MethodGet, "/me/passkeys", nil); status != http.StatusOK || len(list) != 1 {
		t.Fatalf("list after = %d %v", status, list)
	}
	// A token without the scope for people cannot manage passkeys, and no token
	// creates one: a passkey signs in for good.
	tok := c.withToken(c.newToken("read"))
	if status, _, _ := tok.call(http.MethodDelete, "/me/passkeys/"+made["id"].(string), nil); status != http.StatusForbidden {
		t.Fatalf("read-only token deleted a passkey: %d", status)
	}
	full := c.withToken(c.newToken("write"))
	full.host = boxName
	if status, body, _ := full.call(http.MethodPost, "/me/passkeys/registration", nil); status != http.StatusForbidden {
		t.Fatalf("a token began a passkey = %d %v, want 403", status, body)
	}
}

// The reset through the API can remove passkeys in the same step as well.
func TestResetThroughTheAPIRemovesTickedPasskeys(t *testing.T) {
	t.Parallel()
	b, mamaSecret, _ := setupWithCodes(t)
	papa, papaSecret := secondParent(t, b)
	loginWith(b, "papa", "papa's long password", totpNow(b, papaSecret))
	addPasskeyAt(t, b, passkeytest.New(), boxName, "iPhone")
	list, _ := b.db.Passkeys(papa.ID)
	b.logout()
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	c := newAPIClient(t, b)
	c.cookie = true
	body := map[string]any{"password": "correct horse battery", "code": totpNow(b, mamaSecret), "remove_passkeys": []string{list[0].ID}}
	if status, out, _ := c.call(http.MethodPost, "/people/"+papa.ID+"/reset-otp", body); status != http.StatusNoContent {
		t.Fatalf("reset = %d %v", status, out)
	}
	if left, _ := b.db.Passkeys(papa.ID); len(left) != 0 {
		t.Fatalf("passkeys after = %+v", left)
	}
}

// ADR 0017, T23: a paired personal device never creates a passkey, through the API
// either, before or after a reset of its parent's one-time codes.
func TestPairedPersonalDeviceCannotCreateAPasskeyThroughTheAPI(t *testing.T) {
	t.Parallel()
	b, mamaSecret, _ := setupWithCodes(t)
	papa, _ := secondParent(t, b)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	personalDevice := pairedPersonalDevice(t, b, papa, "Papa's tablet")
	call := func(path string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, b.srv.URL+"/api/v1"+path, strings.NewReader(`{"ceremony":"x","credential":{}}`))
		req.Host = boxName
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://"+boxName)
		u, _ := url.Parse(b.srv.URL)
		for _, c := range personalDevice.client.Jar.Cookies(u) { // the jar keys cookies by Host, as asBrowserOn does
			req.AddCookie(c)
		}
		res, err := personalDevice.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	check := func(when string) {
		t.Helper()
		for _, path := range []string{"/me/passkeys/registration", "/me/passkeys"} {
			if got := call(path); got != http.StatusForbidden {
				t.Errorf("%s: personal device POST %s = %d, want 403", when, path, got)
			}
		}
	}
	check("before a reset")
	own := url.Values{"password": {"correct horse battery"}, "code": {totpNow(b, mamaSecret)}}
	if r := b.post("/account/reset/"+papa.ID, own); r.status != http.StatusSeeOther {
		t.Fatalf("reset = %d", r.status)
	}
	check("after a reset")
	if n, _ := b.db.PasskeyCount(); n != 0 {
		t.Errorf("%d passkeys exist, want none", n)
	}
}

// ADR 0018, threat model T23: after Mama resets Papa's codes, a password alone
// signs Papa in only as far as the new one-time codes. It must not create a
// passkey: that would be a way in without a one-time code. The wizard and the
// invitation (pending, never reset) are the cases that may.
func TestPasswordOnlySessionAfterAResetCannotCreateAPasskey(t *testing.T) {
	t.Parallel()
	b, mamaSecret, _ := setupWithCodes(t)
	papa, _ := secondParent(t, b)
	loginWith(b, "mama", "correct horse battery", totpNow(b, mamaSecret))
	own := url.Values{"password": {"correct horse battery"}, "code": {totpNow(b, mamaSecret)}}
	if r := b.at(boxName, http.MethodPost, "/account/reset/"+papa.ID, own); r.status != http.StatusSeeOther {
		t.Fatalf("reset = %d", r.status)
	}
	b.logout()
	// Papa signs in with the password only; his codes are reset.
	if r := b.at(boxName, http.MethodPost, "/login", url.Values{"username": {"papa"}, "password": {"papa's long password"}}); r.status >= 400 {
		t.Fatalf("password-only sign-in after the reset = %d %s", r.status, r.body)
	}
	if _, r := begin(t, b, boxName, "/account/passkeys/begin"); r.status != http.StatusForbidden {
		t.Fatalf("begin after a reset = %d %s, want 403", r.status, r.body)
	}
	if n, _ := b.db.PasskeyCount(); n != 0 {
		t.Errorf("%d passkeys exist, want none", n)
	}
}

// Control for the test above: the wizard's pending session (never reset) may.
func TestWizardSessionStillMayBeginAPasskey(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	if r := b.at(boxName, http.MethodPost, "/setup", validSetupForm()); r.status != http.StatusSeeOther {
		t.Fatalf("setup = %d", r.status)
	}
	if _, r := begin(t, b, boxName, "/account/passkeys/begin"); r.status != http.StatusOK {
		t.Fatalf("begin in the wizard = %d %s, want 200", r.status, r.body)
	}
}
