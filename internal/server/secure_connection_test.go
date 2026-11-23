package server_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/server"
)

// Ticket 216 (decision Q92, preview 57 frames 0, 1/2, 14 to 18): the VPN line,
// the names the box certificate covers, the router name, and the settings page
// "Sichere Verbindung".

// fakeResolver stands in for the router's DNS: the names it knows per address.
type fakeResolver struct {
	mu    sync.Mutex
	names map[string][]string
	err   error
	asked []string
}

func (f *fakeResolver) LookupAddr(_ context.Context, addr string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, addr)
	return f.names[addr], f.err
}

func (f *fakeResolver) lookups() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

const (
	lanAddress = "192.168.178.40"
	guideDE    = "https://github.com/hejmo-home/hejmo/blob/main/docs/install.de.md#von-unterwegs-wireguard-mit-der-fritzbox"
	guideEN    = "https://github.com/hejmo-home/hejmo/blob/main/docs/install.md#away-from-home-wireguard-with-the-fritzbox"
)

var (
	certNamesRe = regexp.MustCompile(`(?s)<ul class="certnames">(.*?)</ul>`)
	liRe        = regexp.MustCompile(`<li>(.*?)</li>`)
	ownCardRe   = regexp.MustCompile(`(?s)<label><input type="radio" name="way" value="own">.*?</label>`)
)

// certNamesOn returns the names of the first ul.certnames of a page.
func certNamesOn(t *testing.T, body string) []string {
	t.Helper()
	m := certNamesRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no ul.certnames on the page:\n%s", body)
	}
	var out []string
	for _, li := range liRe.FindAllStringSubmatch(m[1], -1) {
		out = append(out, li[1])
	}
	return out
}

// ownPage is the wizard's page "Nur im Heimnetz" as a browser on the LAN address sees it.
// The wizard asks whether the box stays offline before this page (ticket 201), so the helper answers "no" first.
func ownPage(b *box, lang string) resp {
	b.t.Helper()
	if box, _ := b.db.OutboundBox(); !box.Asked {
		if err := b.db.AnswerOfflineQuestion(false, b.now); err != nil {
			b.t.Fatal(err)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, b.srv.URL+"/setup/https?way=own", nil)
	req.Header.Set("Accept-Language", lang)
	return b.asBrowserOn(lanAddress, req)
}

func ownPost(b *box, lang string, form url.Values) resp {
	b.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, b.srv.URL+"/setup/https", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://"+lanAddress)
	req.Header.Set("Accept-Language", lang)
	return b.asBrowserOn(lanAddress, req)
}

func routerBox(t *testing.T, names ...string) (*box, *fakeResolver) {
	t.Helper()
	r := &fakeResolver{names: map[string][]string{lanAddress: names}}
	return newFreshBox(t, withResolver(r)), r
}

// Acceptance: the card "Nur im Heimnetz" has a fourth line "Unterwegs?" with a
// link to the WireGuard chapter; the link opens a new tab; no other card has it.
func TestOwnCACardHasTheAwayLineWithAGuideLinkAndNoOtherCardDoes(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	for lang, c := range map[string]struct{ label, text, link, url string }{
		"de": {"Unterwegs?", "Mit einem VPN wie WireGuard (z.\u00a0B. in der FRITZ!Box) bist du auch von unterwegs im Heimnetz.", "Anleitung für WireGuard", guideDE},
		"en": {"Away from home?", "With a VPN such as WireGuard (for example in the FRITZ!Box) you are in the home network even when you are away.", "Guide for WireGuard", guideEN},
	} {
		body := b.do(http.MethodGet, "/setup/https", nil, map[string]string{"Accept-Language": lang}).body
		card := ownCardRe.FindString(body)
		if card == "" {
			t.Fatalf("%s: no card for the own CA:\n%s", lang, body)
		}
		for _, want := range []string{`<span class="k">` + c.label + `</span>`, c.text, `<a href="` + c.url + `" target="_blank" rel="noopener">` + c.link + `</a>`} {
			if !strings.Contains(card, want) {
				t.Errorf("%s: the card misses %q:\n%s", lang, want, card)
			}
		}
		// It is the fourth line, after "Du brauchst" and "Dafür".
		if lines := strings.Count(card, "<span><span class=\"k\">"); lines != 3 {
			t.Errorf("%s: the card has %d keyed lines, want 3 (need, cost, away)", lang, lines)
		}
		if strings.Count(body, c.url) != 1 {
			t.Errorf("%s: the guide is linked %d times on the choice page, want once (only the own CA card)", lang, strings.Count(body, c.url))
		}
	}
}

// Acceptance: the page "Nur im Heimnetz" lists the names (ul.certnames); the
// first point of ul.homeonly names the VPN way with the link.
func TestOwnCAPageListsTheNamesAndNamesTheVPNWayFirst(t *testing.T) {
	t.Parallel()
	b, _ := routerBox(t)
	r := ownPage(b, "de")
	if r.status != http.StatusOK {
		t.Fatalf("status %d", r.status)
	}
	if got, want := certNamesOn(t, r.body), []string{"hejmo.local", lanAddress}; !slices.Equal(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
	for _, want := range []string{"Das Zertifikat gilt für diese Adressen:", "Öffne die Box unter einer dieser Adressen. Unter einem anderen Namen warnt der Browser."} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	m := regexp.MustCompile(`(?s)<ul class="homeonly">\s*<li>(.*?)</li>`).FindStringSubmatch(r.body)
	if m == nil || !strings.Contains(m[1], "unterwegs über ein VPN wie WireGuard.") || !strings.Contains(m[1], `<a href="`+guideDE+`" target="_blank" rel="noopener">Anleitung für WireGuard</a>`) {
		t.Errorf("the first point of ul.homeonly = %v", m)
	}
	if strings.Contains(r.body, `class="addr"`) {
		t.Error("the single address of before is still there")
	}
	// The wizard step is one form: names, "More options", what it means, the buttons.
	if f := regexp.MustCompile(`(?s)<form method="post" action="/setup/https" class="homeform">.*?</form>`).FindString(r.body); !strings.Contains(f, `name="router_name"`) || !strings.Contains(f, `class="actions"`) || !strings.Contains(f, "Anderen Weg wählen") {
		t.Errorf("no homeform with the field and the buttons:\n%s", r.body)
	}
}

// Acceptance: "More options" has the optional router name, empty when the box
// finds none (frame 2).
func TestRouterNameFieldIsEmptyAndClosedWhenTheRouterKnowsNoName(t *testing.T) {
	t.Parallel()
	b, resolver := routerBox(t)
	body := ownPage(b, "de").body
	if got := resolver.lookups(); !slices.Equal(got, []string{lanAddress}) {
		t.Errorf("reverse lookups = %v, want one for the LAN address", got)
	}
	for _, want := range []string{`<details class="more">`, `<span class="sum">Kein Name im Router</span>`, `Name im Router (freiwillig)`,
		`name="router_name" type="text" value=""`, `placeholder="hejmo.fritz.box"`, `autocomplete="off"`,
		`Die Box hat in deinem Router keinen Namen gefunden. Bei der FRITZ!Box steht er unter Heimnetz › Netzwerk beim Gerät der Box, zum Beispiel hejmo.fritz.box.`,
		`Wozu? Über ein VPN findet das Gerät hejmo.local oft nicht, den Namen im Router aber schon.`,
		`aria-describedby="hint-router_name hint-router_name2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if got := certNamesOn(t, body); len(got) != 2 {
		t.Errorf("names = %v, want two", got)
	}
}

// Acceptance: the box asks the router for its name (reverse lookup of the LAN
// address, fake resolver) and prefills the field; the name is in the list and
// "More options" is open and names it (frame 14).
func TestRouterNameIsPrefilledByAReverseLookupOfTheBoxAddress(t *testing.T) {
	t.Parallel()
	b, resolver := routerBox(t, "Hejmo.Fritz.Box.")
	body := ownPage(b, "de").body
	if got := resolver.lookups(); !slices.Equal(got, []string{lanAddress}) {
		t.Errorf("reverse lookups = %v", got)
	}
	for _, want := range []string{`<details class="more" open>`, `<span class="sum">Name im Router: hejmo.fritz.box</span>`, `name="router_name" type="text" value="hejmo.fritz.box"`,
		`Die Box hat diesen Namen in deinem Router gefunden. Lass ihn stehen, dann öffnest du die Box auch über VPN ohne Warnung. Brauchst du ihn nicht, mach das Feld leer.`} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if got, want := certNamesOn(t, body), []string{"hejmo.local", lanAddress, "hejmo.fritz.box"}; !slices.Equal(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}

func TestReverseLookupSkipsWhatACertificateCannotCarryAndSurvivesFailure(t *testing.T) {
	t.Parallel()
	// The first usable name wins; "hejmo.local" is covered anyway, "localhost" and an address are no router names.
	b, _ := routerBox(t, "localhost.", "hejmo.local.", "wohn zimmer.lan.", "box.lan.", "other.lan.")
	if got := certNamesOn(t, ownPage(b, "de").body); !slices.Equal(got, []string{"hejmo.local", lanAddress, "box.lan"}) {
		t.Errorf("names = %v", got)
	}

	failing := &fakeResolver{err: errors.New("no such host"), names: map[string][]string{lanAddress: {"hejmo.fritz.box."}}}
	f := newFreshBox(t, withResolver(failing))
	r := ownPage(f, "de")
	if got := certNamesOn(t, r.body); r.status != http.StatusOK || len(got) != 2 || !strings.Contains(r.body, `name="router_name" type="text" value=""`) {
		t.Errorf("a failing lookup = %d, want the page with an empty field:\n%s", r.status, r.body)
	}
}

// Without the public certificate service only the own CA is on offer, and the page
// of the choice is already that page: it asks the router too.
func TestOwnCAOnlyBoxAsksTheRouterToo(t *testing.T) {
	t.Parallel()
	b := newFreshBox(t)
	resolver := &fakeResolver{names: map[string][]string{lanAddress: {"hejmo.fritz.box."}}}
	h := server.New(server.Options{DB: b.db, PKI: b.pki, Now: func() time.Time { return b.now }, Resolver: resolver})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	if err := b.db.AnswerOfflineQuestion(false, b.now); err != nil { // the wizard asks this first (ticket 201)
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/setup/https", nil)
	req.Host = lanAddress
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if body := string(raw); !strings.Contains(body, `name="router_name" type="text" value="hejmo.fritz.box"`) || !strings.Contains(body, "Die Box hat diesen Namen in deinem Router gefunden.") {
		t.Errorf("the own-CA-only page did not ask the router:\n%s", body)
	}
}

func TestReverseLookupOnlyAsksAboutAddressesOfTheHomeNetwork(t *testing.T) {
	t.Parallel()
	resolver := &fakeResolver{names: map[string][]string{"8.8.8.8": {"dns.google."}}}
	b := newFreshBox(t, withResolver(resolver))
	for _, host := range []string{"8.8.8.8", "hejmo.local", "box.example.org"} {
		req, _ := http.NewRequest(http.MethodGet, b.srv.URL+"/setup/https?way=own", nil)
		if r := b.asBrowserOn(host, req); r.status != http.StatusOK {
			t.Fatalf("%s: status %d", host, r.status)
		}
	}
	if got := resolver.lookups(); len(got) != 0 {
		t.Errorf("lookups for %v: a public address or a name must not go to a DNS server", got)
	}
}

func TestOwnCAPageIsGermanAndEnglish(t *testing.T) {
	t.Parallel()
	b, _ := routerBox(t, "hejmo.fritz.box.")
	en := ownPage(b, "en").body
	for _, want := range []string{"The certificate is valid for these addresses:", "Name in the router: hejmo.fritz.box", "Name in the router (optional)", "The box found this name in your router.", guideEN} {
		if !strings.Contains(en, want) {
			t.Errorf("English page misses %q", want)
		}
	}
}

// Acceptance: the certificate covers the entered router name; an empty field
// means none, as before.
func TestCreatingTheCertificateWithARouterName(t *testing.T) {
	t.Parallel()
	b, _ := routerBox(t)
	r := ownPost(b, "de", url.Values{"router_name": {" Hejmo.Fritz.Box "}})
	if r.status != http.StatusSeeOther || r.location != "/ca" {
		t.Fatalf("POST = %d %q\n%s", r.status, r.location, r.body)
	}
	if got, want := b.pki.Names(), []string{"hejmo.local", lanAddress, "hejmo.fritz.box"}; !slices.Equal(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
	if b.pki.RouterName() != "hejmo.fritz.box" {
		t.Errorf("router name = %q", b.pki.RouterName())
	}
}

func TestCreatingTheCertificateWithoutARouterNameStaysAsBefore(t *testing.T) {
	t.Parallel()
	b, _ := routerBox(t)
	if r := ownPost(b, "de", url.Values{"router_name": {""}}); r.status != http.StatusSeeOther || r.location != "/ca" {
		t.Fatalf("POST = %d %q", r.status, r.location)
	}
	if got := b.pki.Names(); !slices.Equal(got, []string{"hejmo.local", lanAddress}) || b.pki.RouterName() != "" {
		t.Errorf("names = %v router = %q", got, b.pki.RouterName())
	}
}

// Acceptance (name validation): the error is at the field, in words, and what
// was typed stays; nothing is created.
func TestRefusedRouterNameComesBackAtTheFieldAndCreatesNothing(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ typed, want string }{
		{"hejmo", "Der Name braucht einen Punkt, zum Beispiel hejmo.fritz.box. So zeigt ihn die FRITZ!Box beim Gerät der Box."},
		{"hejmo box.fritz.box", "Nur Buchstaben a bis z, Ziffern, Bindestrich und Punkte."},
		{"höjmo.fritz.box", "Nur Buchstaben a bis z, Ziffern, Bindestrich und Punkte."},
		{lanAddress, "Die Adresse 192.168.178.40 gilt schon. Trag hier einen Namen ein."},
		{"hejmo.local", "Dieser Name gilt schon."},
	} {
		b, _ := routerBox(t)
		r := ownPost(b, "de", url.Values{"router_name": {c.typed}})
		if r.status != http.StatusUnprocessableEntity {
			t.Errorf("%q: status %d, want 422", c.typed, r.status)
			continue
		}
		for _, want := range []string{`<div class="f invalid">`, `value="` + c.typed + `"`, `aria-invalid="true"`, `aria-describedby="err-router_name hint-router_name hint-router_name2"`,
			`<p class="ferr" id="err-router_name" role="alert">`, c.want, `<details class="more" open>`} {
			if !strings.Contains(r.body, want) {
				t.Errorf("%q: page misses %q", c.typed, want)
			}
		}
		if b.pki.Ready() {
			t.Errorf("%q: a certificate was created for a refused name", c.typed)
		}
	}
}

// Acceptance: the hub has the row "Sichere Verbindung" after "Haushalt", with
// the way in words as its state.
func TestSettingsHubHasTheRowSichereVerbindungAfterHaushalt(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	body := b.do(http.MethodGet, "/settings", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{`<a class="hub-row" href="/settings/https">`, `<strong>Sichere Verbindung</strong>`, `<b>Nur im Heimnetz.</b> Für welche Adressen das Zertifikat der Box gilt, Name im Router und der Weg von unterwegs über ein VPN.`} {
		if !strings.Contains(body, want) {
			t.Errorf("hub misses %q", want)
		}
	}
	h, https, account := strings.Index(body, `href="/settings/household"`), strings.Index(body, `href="/settings/https"`), strings.Index(body, `href="/account"`)
	if !(h < https && https < account) {
		t.Errorf("the row is not between Haushalt and Mein Konto (%d, %d, %d)", h, https, account)
	}
	b.public.name = "mia.dedyn.io"
	if body := b.do(http.MethodGet, "/settings", nil, map[string]string{"Accept-Language": "de"}).body; !strings.Contains(body, "<b>Mit öffentlichem Namen.</b>") {
		t.Errorf("hub of a box with a public name:\n%s", body)
	}
}

// Acceptance: the page has breadcrumbs (#182), the facts and the three tiles
// (frame 16).
func TestSettingsPageShowsFactsAndTheTwoTiles(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	r := b.do(http.MethodGet, "/settings/https", nil, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusOK {
		t.Fatalf("status %d", r.status)
	}
	wantTrail(t, "Sichere Verbindung", r.body, part("Einstellungen", "/settings"), part("Sichere Verbindung", ""))
	if h := headingText(r.body); h != "Sichere Verbindung" {
		t.Errorf("h1 = %q", h)
	}
	for _, want := range []string{`<dl class="facts">`, "<dt>Weg</dt>", "Nur im Heimnetz. Die Box stellt ihr Zertifikat selbst aus.", "<dt>Gilt für</dt>", "<dt>Gültig bis</dt>", "2036"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if got, want := certNamesOn(t, r.body), []string{"hejmo.local", "127.0.0.1"}; !slices.Equal(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
	if n := strings.Count(r.body, `class="tile netblock"`); n != 2 {
		t.Errorf("%d tiles, want 2 (from away, router name)", n)
	}
	away := regexp.MustCompile(`(?s)<section class="tile netblock">\s*<h2>Von unterwegs</h2>.*?</section>`).FindString(r.body)
	for _, want := range []string{"Mit einem VPN wie WireGuard (z.\u00a0B. in der FRITZ!Box) bist du auch von unterwegs im Heimnetz.", `<a href="` + guideDE + `" target="_blank" rel="noopener">Anleitung für WireGuard</a>`,
		"Über ein VPN findet das Gerät hejmo.local oft nicht. Öffne die Box dann unter 127.0.0.1 oder unter dem Namen im Router (unten)."} {
		if !strings.Contains(away, want) {
			t.Errorf("the tile \"Von unterwegs\" misses %q:\n%s", want, away)
		}
	}
}

// Acceptance: the form states the cost first, before the one button.
func TestRouterNameFormStatesTheCostBeforeTheButton(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	body := b.do(http.MethodGet, "/settings/https", nil, map[string]string{"Accept-Language": "de"}).body
	form := regexp.MustCompile(`(?s)<form method="post" action="/settings/https" class="tile netblock">.*?</form>`).FindString(body)
	if form == "" {
		t.Fatalf("no form:\n%s", body)
	}
	why, button := strings.Index(form, `<p class="why">`), strings.Index(form, `<button class="button" type="submit">Namen eintragen</button>`)
	if why < 0 || button < 0 || why > button || strings.Count(form, "<button") != 1 {
		t.Errorf("the cost box is not before the one button:\n%s", form)
	}
	for _, want := range []string{"<h2>Name im Router</h2>", "Danach muss jedes Gerät der Box noch einmal vertrauen.", "Ein neuer Name braucht ein neues Zertifikat der Box.", `placeholder="hejmo.fritz.box"`,
		"Bei der FRITZ!Box steht er unter Heimnetz › Netzwerk beim Gerät der Box. Dort kannst du ihn auch ändern, zum Beispiel in „hejmo“."} {
		if !strings.Contains(form, want) {
			t.Errorf("form misses %q", want)
		}
	}
}

// Acceptance: the form re-issues the name-constrained CA; the answer is the
// page itself (it comes over the old connection), with the success line and the
// one next step "Der Box neu vertrauen" (frame 18).
func TestEnteringTheRouterNameMakesANewCAAndLeadsToTrustingTheBoxAgain(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	before := b.pki.CAFingerprint()
	r := postDE(b, "/settings/https", url.Values{"router_name": {"hejmo.fritz.box"}})
	if r.status != http.StatusOK {
		t.Fatalf("POST = %d %q, want the page itself (a redirect would meet the new certificate)\n%s", r.status, r.location, r.body)
	}
	if b.pki.CAFingerprint() == before {
		t.Error("the CA did not change")
	}
	if got, want := b.pki.Names(), []string{"hejmo.local", "127.0.0.1", "hejmo.fritz.box"}; !slices.Equal(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
	if got := successLine(r.body); got != "„hejmo.fritz.box“ ist eingetragen. Das Zertifikat gilt jetzt auch für diesen Namen." {
		t.Errorf("success line = %q", got)
	}
	if got, want := certNamesOn(t, r.body), []string{"hejmo.local", "127.0.0.1", "hejmo.fritz.box"}; !slices.Equal(got, want) {
		t.Errorf("the facts show %v, want %v", got, want)
	}
	for _, want := range []string{"<h2>Jetzt jedes Gerät der Box neu vertrauen</h2>", `<a class="button" href="http://127.0.0.1/ca">Der Box neu vertrauen</a>`,
		"Die Seite geht auch ohne Vertrauen auf: http://127.0.0.1/ca. Öffne sie auf jedem weiteren Gerät.",
		"Öffne die Box dann unter 127.0.0.1 oder hejmo.fritz.box."} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if strings.Contains(r.body, `action="/settings/https"`) {
		t.Error("the form is still there: it comes back only when the page is opened again")
	}
	if strings.Index(r.body, "Jetzt jedes Gerät der Box neu vertrauen") > strings.Index(r.body, "<h2>Von unterwegs</h2>") {
		t.Error("the next step is not before \"Von unterwegs\"")
	}

	// Opened again: the form is back, with the name in the field.
	again := b.do(http.MethodGet, "/settings/https", nil, map[string]string{"Accept-Language": "de"}).body
	if !strings.Contains(again, `name="router_name" type="text" value="hejmo.fritz.box"`) || strings.Contains(again, "neu vertrauen</h2>") || successLine(again) != "" {
		t.Errorf("the page opened again:\n%s", again)
	}
	if !strings.Contains(again, "Öffne die Box dann unter 127.0.0.1 oder hejmo.fritz.box.") {
		t.Errorf("the hint of \"Von unterwegs\" does not name the router name:\n%s", again)
	}
}

// postDE posts a form with the German texts.
func postDE(b *box, path string, form url.Values) resp {
	b.t.Helper()
	return b.do(http.MethodPost, path, form, map[string]string{"Accept-Language": "de"})
}

func TestNewCAKeepsTheBoxServingAndTheOldNamesCovered(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	if r := postDE(b, "/settings/https", url.Values{"router_name": {"hejmo.fritz.box"}}); r.status != http.StatusOK {
		t.Fatalf("POST = %d", r.status)
	}
	// The CA download of the trust page is the new one.
	pem := b.get("/ca/hejmo-ca.crt")
	if pem.status != http.StatusOK || pem.body != string(b.pki.CAPEM()) {
		t.Errorf("the trust page does not serve the new CA (%d)", pem.status)
	}
}

// Acceptance: field errors keep the input; the CA stays as it was.
func TestSettingsFormErrorsStayAtTheFieldAndKeepTheInput(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ typed, want string }{
		{"hejmo", "Der Name braucht einen Punkt, zum Beispiel hejmo.fritz.box. So zeigt ihn die FRITZ!Box beim Gerät der Box."},
		{"hejmo box.fritz.box", "Nur Buchstaben a bis z, Ziffern, Bindestrich und Punkte."},
		{"hejmo.fritz.box?", "Nur Buchstaben a bis z, Ziffern, Bindestrich und Punkte."},
		{"127.0.0.1", "Die Adresse 127.0.0.1 gilt schon. Trag hier einen Namen ein."},
		{"hejmo.local", "Dieser Name gilt schon."},
		{"", "Trag den Namen ein, den dein Router für die Box zeigt, zum Beispiel hejmo.fritz.box."},
	} {
		b := signedInBox(t)
		before := b.pki.CAFingerprint()
		r := postDE(b, "/settings/https", url.Values{"router_name": {c.typed}})
		if r.status != http.StatusUnprocessableEntity {
			t.Errorf("%q: status %d, want 422", c.typed, r.status)
			continue
		}
		for _, want := range []string{`<div class="f invalid">`, `value="` + c.typed + `"`, `aria-invalid="true"`, ` autofocus`, `<p class="ferr" id="err-router_name" role="alert">`, c.want, `action="/settings/https"`} {
			if !strings.Contains(r.body, want) {
				t.Errorf("%q: page misses %q", c.typed, want)
			}
		}
		if successLine(r.body) != "" || strings.Contains(r.body, "neu vertrauen</h2>") {
			t.Errorf("%q: a refused name shows the success state", c.typed)
		}
		if b.pki.CAFingerprint() != before {
			t.Errorf("%q: the CA changed", c.typed)
		}
	}
}

func TestSettingsPageIsEnglishToo(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	body := getEN(b, "/settings/https").body
	for _, want := range []string{"<h1>Secure connection</h1>", "Home network only. The box issues its own certificate.", "<h2>Away from home</h2>", guideEN, "<h2>Name in the router</h2>",
		"After this, every device has to trust the box again.", "Enter name"} {
		if !strings.Contains(body, want) {
			t.Errorf("English page misses %q", want)
		}
	}
	r := postEN(b, "/settings/https", url.Values{"router_name": {"hejmo.fritz.box"}})
	if got := successLine(r.body); got != "“hejmo.fritz.box” is entered. The certificate is now valid for that name too." || !strings.Contains(r.body, "Trust the box again") {
		t.Errorf("English result: %q\n%s", got, r.body)
	}
}

// Acceptance: public ways show no router-name form: weg, name and "Von unterwegs".
func TestPublicNameShowsNoRouterNameForm(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	b.public.name = "mia.dedyn.io"
	body := b.do(http.MethodGet, "/settings/https", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{"Mit öffentlichem Namen. Alle Geräte vertrauen dem Zertifikat sofort.", "<dt>Name</dt>", "mia.dedyn.io", "<h2>Von unterwegs</h2>", guideDE} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	for _, not := range []string{`name="router_name"`, `action="/settings/https"`, "Name im Router", `class="certnames"`} {
		if strings.Contains(body, not) {
			t.Errorf("a public name shows %q", not)
		}
	}
	before := b.pki.CAFingerprint()
	if r := postDE(b, "/settings/https", url.Values{"router_name": {"hejmo.fritz.box"}}); r.status != http.StatusSeeOther || r.location != "/settings/https" || b.pki.CAFingerprint() != before {
		t.Errorf("POST with a public name = %d %q, the CA changed: %v", r.status, r.location, b.pki.CAFingerprint() != before)
	}
}

func TestOnlyParentsOpenTheSecureConnectionPage(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	child := childSession(t, b)
	if r := child.get("/settings/https"); r.status != http.StatusForbidden {
		t.Errorf("GET as a child = %d, want 403", r.status)
	}
	before := b.pki.CAFingerprint()
	if r := child.post("/settings/https", url.Values{"router_name": {"hejmo.fritz.box"}}); r.status != http.StatusForbidden || b.pki.CAFingerprint() != before {
		t.Errorf("POST as a child = %d", r.status)
	}
	b.logout()
	if r := b.get("/settings/https"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Errorf("GET signed out = %d %q", r.status, r.location)
	}
	if r := b.post("/settings/https", url.Values{"router_name": {"hejmo.fritz.box"}}); r.status == http.StatusOK {
		t.Errorf("POST signed out = %d", r.status)
	}
}

// ADR 0015: everything the page does is also possible through the API.
func TestAPIReportsTheNamesAndSetsTheRouterName(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, body, _ := c.call(http.MethodGet, "/https", nil)
	names, _ := body["names"].([]any)
	if status != http.StatusOK || body["mode"] != "own-ca" || len(names) != 2 || names[0] != "hejmo.local" || body["router_name"] != nil || body["expires_at"] == nil {
		t.Fatalf("GET /https = %d %v", status, body)
	}
	before := c.b.pki.CAFingerprint()
	status, body, _ = c.call(http.MethodPatch, "/https", map[string]any{"router_name": "Hejmo.Fritz.Box"})
	names, _ = body["names"].([]any)
	if status != http.StatusOK || body["router_name"] != "hejmo.fritz.box" || len(names) != 3 || names[2] != "hejmo.fritz.box" {
		t.Fatalf("PATCH /https = %d %v", status, body)
	}
	if c.b.pki.CAFingerprint() == before {
		t.Error("the CA did not change")
	}
	for _, bad := range []string{"hejmo", "hejmo box.lan", "hejmo.local", "hejmo.fritz.box", "", "127.0.0.1"} {
		if status, body, _ := c.call(http.MethodPatch, "/https", map[string]any{"router_name": bad}); status != http.StatusUnprocessableEntity || !strings.HasPrefix(body["error"].(string), "router_name: ") {
			t.Errorf("PATCH %q = %d %v, want 422", bad, status, body)
		}
	}
	c.b.public.name = "mia.dedyn.io"
	if status, _, _ := c.call(http.MethodPatch, "/https", map[string]any{"router_name": "hejmo.lan"}); status != http.StatusConflict {
		t.Errorf("PATCH with a public name = %d, want 409", status)
	}
}

func TestAPIRouterNameNeedsTheSettingsWriteScope(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	read := c.withToken(c.newToken("settings:read"))
	if status, _, _ := read.call(http.MethodGet, "/https", nil); status != http.StatusOK {
		t.Errorf("GET with settings:read = %d", status)
	}
	if status, _, _ := read.call(http.MethodPatch, "/https", map[string]any{"router_name": "hejmo.lan"}); status != http.StatusForbidden {
		t.Errorf("PATCH with settings:read = %d, want 403", status)
	}
}
