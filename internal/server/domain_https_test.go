package server_test

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/publiccert"
)

// Ticket 57: the wizard's third way, the family's own domain at Cloudflare,
// deSEC, Hetzner or INWX.

func domainForm(provider string) url.Values {
	f := url.Values{
		"name": {"box.familie-mueller.de"}, "provider": {provider}, "way": {"domain"}, "issuer": {"letsencrypt"},
		"lan_ip": {"192.168.178.20"}, "email": {"papa@example.org"},
	}
	switch provider {
	case "inwx":
		f.Set("username", "mueller-familie")
		f.Set("password", "geheim-pw-4711")
	default:
		f.Set("token", "tok-4711")
	}
	return f
}

// guideOf returns the markup of one provider's guide.
func guideOf(t *testing.T, body, provider string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<form class="guide" data-provider="` + provider + `".*?</form>`).FindString(body)
	if m == "" {
		t.Fatalf("no guide for %s in:\n%s", provider, body)
	}
	return m
}

func TestWizardChoiceOffersThreeWays(t *testing.T) {
	b := newFreshBox(t)
	r := b.do(http.MethodGet, "/setup/https", nil, map[string]string{"Accept-Language": "de"})
	for _, want := range []string{"Es gibt drei Wege.", `value="public" checked`, `value="domain"`, `value="own"`, "Mit kostenlosem Namen (empfohlen)", "Mit eurer eigenen Domain", "Cloudflare, deSEC, Hetzner oder INWX"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("choice page misses %q", want)
		}
	}
	if strings.Index(r.body, `value="public"`) > strings.Index(r.body, `value="domain"`) || strings.Index(r.body, `value="domain"`) > strings.Index(r.body, `value="own"`) {
		t.Error("the ways are not in the order public, domain, own")
	}
	en := b.do(http.MethodGet, "/setup/https", nil, map[string]string{"Accept-Language": "en"})
	if !strings.Contains(en.body, "three ways") {
		t.Error("English lead does not say three ways")
	}
}

func TestOwnDomainPageHasOneGuidePerProviderAndNoneChosen(t *testing.T) {
	b := newFreshBox(t)
	r := b.do(http.MethodGet, "/setup/https?way=domain", nil, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusOK {
		t.Fatalf("status %d", r.status)
	}
	for _, want := range []string{"<h1>Eigene Domain einrichten</h1>", "Wo liegt eure Domain?", `class="hint pick-first"`, `<a href="/setup/https">Anderen Weg wählen</a>`, `<div class="connect">`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	radios := regexp.MustCompile(`<input type="radio" name="dns" value="(\w+)"( checked)?`).FindAllStringSubmatch(r.body, -1)
	var order []string
	for _, m := range radios {
		order = append(order, m[1])
		if m[2] != "" {
			t.Errorf("provider %s is preselected", m[1])
		}
	}
	if strings.Join(order, ",") != "cloudflare,desec,hetzner,inwx" {
		t.Errorf("provider cards = %v", order)
	}
	fields := map[string][]string{
		"cloudflare": {"name", "token", "zone_token", "lan_ip", "issuer", "email"},
		"desec":      {"name", "token", "lan_ip", "issuer", "email"},
		"hetzner":    {"name", "token", "lan_ip", "issuer", "email"},
		"inwx":       {"name", "username", "password", "twofa", "otp_secret", "lan_ip", "issuer", "email"},
	}
	for provider, names := range fields {
		g := guideOf(t, r.body, provider)
		for _, want := range []string{`action="/setup/https/public"`, `<input type="hidden" name="provider" value="` + provider + `">`, "data-busy", "Mit „Zertifikat holen“", `class="howto"`} {
			if !strings.Contains(g, want) {
				t.Errorf("%s guide misses %q", provider, want)
			}
		}
		for _, n := range names {
			if !strings.Contains(g, `name="`+n+`"`) {
				t.Errorf("%s guide has no field %q", provider, n)
			}
		}
		have := regexp.MustCompile(`name="(\w+)"`).FindAllStringSubmatch(g, -1)
		for _, h := range have {
			if h[1] != "provider" && h[1] != "way" && !slices.Contains(names, h[1]) && h[1] != "eab_key_id" && h[1] != "eab_hmac" {
				t.Errorf("%s guide has a field %q it should not have", provider, h[1])
			}
		}
		if strings.Count(g, `class="actions"`) != 1 || !strings.Contains(g, `<a class="link" href="/setup/https">Anderen Weg wählen</a>`) {
			t.Errorf("%s guide has no actions with the other-way link", provider)
		}
	}
	if !strings.Contains(guideOf(t, r.body, "inwx"), `role="switch" name="twofa"`) || strings.Contains(guideOf(t, r.body, "inwx"), `name="twofa" checked`) {
		t.Error("the INWX switch is missing or on by default")
	}
	for _, p := range []string{"cloudflare", "hetzner", "inwx"} {
		if !strings.Contains(guideOf(t, r.body, p), `class="why"`) {
			t.Errorf("%s guide has no why box", p)
		}
	}
	if strings.Contains(guideOf(t, r.body, "desec"), `class="why"`) {
		t.Error("deSEC needs no why box")
	}
	// The dedyn.io page stays as it is: one provider, no cards.
	pub := b.do(http.MethodGet, "/setup/https?way=public", nil, map[string]string{"Accept-Language": "de"})
	if strings.Contains(pub.body, `name="dns"`) || !strings.Contains(pub.body, `placeholder="familie.dedyn.io"`) {
		t.Error("the dedyn.io page changed")
	}
}

func TestOwnDomainFormsHandTheirCredentialsToTheManager(t *testing.T) {
	cloudflare := domainForm("cloudflare")
	cloudflare.Set("zone_token", "zone-4711")
	inwxOn := domainForm("inwx")
	inwxOn.Set("twofa", "on")
	inwxOn.Set("otp_secret", " jbsw y3dp ehpk 3pxp ")
	inwxOff := domainForm("inwx")
	inwxOff.Set("otp_secret", "123456") // typed, then switched off: not sent on
	for name, c := range map[string]struct {
		form url.Values
		want publiccert.Request
	}{
		"hetzner":    {domainForm("hetzner"), publiccert.Request{Name: "box.familie-mueller.de", Provider: "hetzner", Token: "tok-4711"}},
		"desec":      {domainForm("desec"), publiccert.Request{Name: "box.familie-mueller.de", Provider: "desec", Token: "tok-4711"}},
		"cloudflare": {cloudflare, publiccert.Request{Name: "box.familie-mueller.de", Provider: "cloudflare", Token: "tok-4711", ZoneToken: "zone-4711"}},
		"inwx on":    {inwxOn, publiccert.Request{Name: "box.familie-mueller.de", Provider: "inwx", Username: "mueller-familie", Password: "geheim-pw-4711", TwoFA: true, OTPSecret: "jbsw y3dp ehpk 3pxp"}},
		"inwx off":   {inwxOff, publiccert.Request{Name: "box.familie-mueller.de", Provider: "inwx", Username: "mueller-familie", Password: "geheim-pw-4711"}},
	} {
		t.Run(name, func(t *testing.T) {
			b := newFreshBox(t)
			if r := b.post("/setup/https/public", c.form); r.status != http.StatusSeeOther || r.location != "/setup/https/check" {
				t.Fatalf("POST = %d %q", r.status, r.location)
			}
			got := b.public.requests()
			c.want.Issuer, c.want.LANIP, c.want.Email = "letsencrypt", "192.168.178.20", "papa@example.org"
			if len(got) != 1 || got[0] != c.want {
				t.Fatalf("manager got %+v, want %+v", got, c.want)
			}
		})
	}
}

// After a failure the secrets are never filled in again; the page opens at
// the provider's own guide with the error inside it.
func TestOwnDomainFailureComesBackInTheSameGuideWithoutSecrets(t *testing.T) {
	type c struct {
		form    url.Values
		secrets []string
		errs    []string // fields that ask to enter the secret again
		text    string   // the most likely cause, English
	}
	inwx := domainForm("inwx")
	inwx.Set("twofa", "on")
	inwx.Set("otp_secret", "JBSWY3DPEHPK3PXP")
	cf := domainForm("cloudflare")
	cf.Set("zone_token", "zone-4711")
	for provider, tc := range map[string]c{
		"hetzner":    {domainForm("hetzner"), []string{"tok-4711"}, []string{"token"}, "Hetzner"},
		"desec":      {domainForm("desec"), []string{"tok-4711"}, []string{"token"}, "deSEC"},
		"cloudflare": {cf, []string{"tok-4711", "zone-4711"}, []string{"token", "zone_token"}, "Zone"},
		"inwx":       {inwx, []string{"geheim-pw-4711", "JBSWY3DPEHPK3PXP"}, []string{"password", "otp_secret"}, "INWX"},
	} {
		t.Run(provider, func(t *testing.T) {
			b := newFreshBox(t)
			b.public.failure = errors.New("provider says no")
			r := b.do(http.MethodPost, "/setup/https/public", tc.form, map[string]string{"Accept-Language": "en", "Origin": b.srv.URL})
			if r.status != http.StatusBadGateway {
				t.Fatalf("status %d", r.status)
			}
			g := guideOf(t, r.body, provider)
			if !strings.Contains(r.body, `name="dns" value="`+provider+`" checked`) {
				t.Errorf("the %s card is not checked", provider)
			}
			if strings.Count(r.body, ` checked`) < 1 || strings.Count(regexp.MustCompile(`name="dns" value="\w+" checked`).FindAllString(r.body, -1)[0], "checked") != 1 {
				t.Error("more than one card checked")
			}
			if !strings.Contains(g, `class="error"`) || !strings.Contains(g, tc.text) {
				t.Errorf("the guide shows no error with %q:\n%s", tc.text, g)
			}
			for _, s := range tc.secrets {
				if strings.Contains(r.body, s) {
					t.Errorf("secret %q is echoed", s)
				}
			}
			for _, f := range tc.errs {
				if !strings.Contains(g, `id="err-`+f+`"`) {
					t.Errorf("no re-enter error at %q", f)
				}
			}
			if strings.Contains(r.body, "provider says no") {
				t.Error("the provider's text is shown")
			}
			for _, kept := range []string{`value="box.familie-mueller.de"`, `value="192.168.178.20"`, `value="papa@example.org"`} {
				if !strings.Contains(g, kept) {
					t.Errorf("%s not kept", kept)
				}
			}
			if provider == "inwx" && (!strings.Contains(g, `value="mueller-familie"`) || !strings.Contains(g, `name="twofa" checked`)) {
				t.Error("INWX user name or switch state not kept")
			}
			// The other guides stay empty.
			for _, other := range []string{"cloudflare", "desec", "hetzner", "inwx"} {
				if other != provider && strings.Contains(guideOf(t, r.body, other), `class="error"`) {
					t.Errorf("the error shows in the %s guide too", other)
				}
			}
		})
	}
}

func TestInwxFailureWithTwoFactorOffOnlyAsksForThePasswordAgain(t *testing.T) {
	b := newFreshBox(t)
	b.public.failure = errors.New("login refused")
	r := b.do(http.MethodPost, "/setup/https/public", domainForm("inwx"), map[string]string{"Accept-Language": "de", "Origin": b.srv.URL})
	g := guideOf(t, r.body, "inwx")
	if !strings.Contains(g, `id="err-password"`) || strings.Contains(g, `id="err-otp_secret"`) {
		t.Errorf("fields flagged wrongly:\n%s", g)
	}
	if !strings.Contains(g, "Gib das Passwort bitte noch einmal ein. Die Box behält es nach einem Fehler nicht.") {
		t.Error("password text missing")
	}
	if strings.Contains(g, `name="twofa" checked`) {
		t.Error("switch turned on")
	}
}

func TestInwxSecretInTheWrongFormIsShownAtTheField(t *testing.T) {
	b := newFreshBox(t)
	b.public.failure = &publiccert.InvalidError{Field: "otp_secret"}
	f := domainForm("inwx")
	f.Set("twofa", "on")
	f.Set("otp_secret", "123456")
	r := b.do(http.MethodPost, "/setup/https/public", f, map[string]string{"Accept-Language": "de", "Origin": b.srv.URL})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", r.status)
	}
	g := guideOf(t, r.body, "inwx")
	for _, want := range []string{`id="err-otp_secret"`, "wohl der 6-stellige Code", `name="twofa" checked`, `value="mueller-familie"`} {
		if !strings.Contains(g, want) {
			t.Errorf("misses %q", want)
		}
	}
	if strings.Contains(r.body, "123456") {
		t.Error("the wrong secret is echoed")
	}
	if strings.Contains(g, `id="err-password"`) {
		t.Error("the password was not at fault")
	}
	// Missing user name or password: at their field.
	for field, text := range map[string]string{"username": "Benutzernamen", "password": "Passwort"} {
		b.public.failure = &publiccert.InvalidError{Field: field}
		r := b.do(http.MethodPost, "/setup/https/public", domainForm("inwx"), map[string]string{"Accept-Language": "de", "Origin": b.srv.URL})
		if g := guideOf(t, r.body, "inwx"); !strings.Contains(g, `id="err-`+field+`"`) || !strings.Contains(g, text) {
			t.Errorf("%s: no error at its field", field)
		}
	}
}

func TestTakenNameIsRefusedAtTheNameFieldAndTheTokenStaysUntouched(t *testing.T) {
	b := newFreshBox(t)
	b.public.failure = &publiccert.NameTakenError{Name: "familie-mueller.de", Address: "203.0.113.7", Suggestion: "box.familie-mueller.de"}
	f := domainForm("hetzner")
	f.Set("name", "familie-mueller.de")
	r := b.do(http.MethodPost, "/setup/https/public", f, map[string]string{"Accept-Language": "de", "Origin": b.srv.URL})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", r.status)
	}
	g := guideOf(t, r.body, "hetzner")
	for _, want := range []string{`id="err-name"`, `<span class="host">familie-mueller.de</span>`, `<span class="host">203.0.113.7</span>`, `<span class="host">box.familie-mueller.de</span>`, "überschreibt sie nicht", `value="familie-mueller.de"`} {
		if !strings.Contains(g, want) {
			t.Errorf("misses %q:\n%s", want, g)
		}
	}
	if strings.Contains(g, `id="err-token"`) || strings.Contains(g, `class="error"`) {
		t.Error("the token is flagged or a page-level error shows although only the name is wrong")
	}
	if strings.Contains(r.body, "tok-4711") {
		t.Error("token echoed")
	}
	// What the provider returned is escaped.
	b.public.failure = &publiccert.NameTakenError{Name: "x.de", Address: "<script>alert(1)</script>", Suggestion: "box.x.de"}
	r = b.do(http.MethodPost, "/setup/https/public", f, map[string]string{"Accept-Language": "de", "Origin": b.srv.URL})
	if strings.Contains(r.body, "<script>alert(1)") {
		t.Error("an address is not escaped")
	}
}

func TestOwnDomainPageHasNoTextOverflowTraps(t *testing.T) {
	b := newFreshBox(t)
	b.public.failure = &publiccert.NameTakenError{Name: "familie-schmidt-oberhuber-mueller.de", Address: "203.0.113.7", Suggestion: "box.familie-schmidt-oberhuber-mueller.de"}
	r := b.do(http.MethodPost, "/setup/https/public", domainForm("hetzner"), map[string]string{"Accept-Language": "de", "Origin": b.srv.URL})
	css := b.get("/static/hejmo.css").body
	for _, want := range []string{`input[name="dns"]:checked`, ".pick-first", ".switchset", "overflow-wrap: anywhere"} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet misses %q", want)
		}
	}
	_ = r
}

// Every text the own-domain page asks for exists in both languages (a missing
// key would show as the key itself).
func TestOwnDomainPageHasEveryTextInBothLanguages(t *testing.T) {
	b := newFreshBox(t)
	for _, lang := range []string{"de", "en"} {
		pages := []string{b.do(http.MethodGet, "/setup/https?way=domain", nil, map[string]string{"Accept-Language": lang}).body, b.do(http.MethodGet, "/setup/https", nil, map[string]string{"Accept-Language": lang}).body}
		for _, failure := range []error{errors.New("x"), &publiccert.NameTakenError{Name: "a.de", Address: "203.0.113.7", Suggestion: "box.a.de"}, &publiccert.InvalidError{Field: "otp_secret"}, &publiccert.InvalidError{Field: "username"}} {
			b.public.failure = failure
			for _, p := range []string{"cloudflare", "desec", "hetzner", "inwx"} {
				f := domainForm(p)
				f.Set("twofa", "on")
				f.Set("zone_token", "z")
				pages = append(pages, b.do(http.MethodPost, "/setup/https/public", f, map[string]string{"Accept-Language": lang, "Origin": b.srv.URL}).body)
			}
		}
		for _, body := range pages {
			if m := regexp.MustCompile(`setup\.https\.[a-z_.0-9]+`).FindString(body); m != "" {
				t.Errorf("%s: page shows the key %q instead of a text", lang, m)
			}
			if strings.Contains(body, "{name}") || strings.Contains(body, "{suggestion}") || strings.Contains(body, "{provider}") || strings.Contains(body, "((") {
				t.Errorf("%s: a placeholder is left in the page", lang)
			}
		}
	}
}
