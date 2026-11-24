package i18n_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/i18n"
)

func TestShipsGermanAndEnglish(t *testing.T) {
	b := i18n.MustLoad()
	got := b.Languages()
	if len(got) != 2 || got[0] != "de" || got[1] != "en" {
		t.Fatalf("Languages() = %v, want [de en]", got)
	}
}

func TestCataloguesHaveTheSameKeys(t *testing.T) {
	b := i18n.MustLoad()
	if missing := b.MissingKeys(); len(missing) > 0 {
		t.Fatalf("keys missing per language: %v", missing)
	}
}

func TestTranslate(t *testing.T) {
	b := i18n.MustLoad()
	if got := b.T("de", "setup.title"); got != "Dein Konto" {
		t.Errorf("de setup.title = %q", got)
	}
	if got := b.T("en", "setup.title"); got != "Your account" {
		t.Errorf("en setup.title = %q", got)
	}
}

func TestUnknownLanguageFallsBackToDefault(t *testing.T) {
	b := i18n.MustLoad()
	if got := b.T("fr", "setup.title"); got != "Dein Konto" {
		t.Errorf("fr setup.title = %q, want German fallback", got)
	}
}

func TestUnknownKeyIsVisible(t *testing.T) {
	b := i18n.MustLoad()
	if got := b.T("en", "no.such.key"); got != "no.such.key" {
		t.Errorf("unknown key = %q, want the key itself", got)
	}
}

func TestMatchAcceptLanguage(t *testing.T) {
	b := i18n.MustLoad()
	cases := map[string]string{
		"":                        "de",
		"en-GB,en;q=0.9":          "en",
		"de-AT":                   "de",
		"fr-FR,fr;q=0.9,en;q=0.8": "en",
		"fr, de;q=0.5, en;q=0.7":  "en",
		"es":                      "de",
		"EN":                      "en",
		"en;q=0, de;q=0.1":        "de",
	}
	for header, want := range cases {
		if got := b.Match(header); got != want {
			t.Errorf("Match(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestFormatFillsParameters(t *testing.T) {
	b := i18n.MustLoad()
	m := i18n.Message{Key: "sync.note.no_start", Params: map[string]string{"label": "Choir"}}
	if got := b.Format("en", m); got != `Skipped event "Choir": it has no start time.` {
		t.Errorf("en = %q", got)
	}
	if got := b.Format("de", m); got == m.Key || !strings.Contains(got, "Choir") {
		t.Errorf("de = %q", got)
	}
	if got := b.Format("en", i18n.Message{}); got != "" {
		t.Errorf("empty message = %q, want empty", got)
	}
}

// ADR 0017: German texts address the user as "du". "eure"/"euer" stay only
// where the whole family is meant; every such text is listed here with its key.
var wholeFamilyTexts = map[string]bool{
	"displays.none":                            true, // "euren Tag": the family's day on the wall
	"connect.legend":                           true, // "eure Kalender": the family's calendars
	"connect.lead":                             true, // "eure Termine": the family's events, kept by several people
	"setup.welcome.lead":                       true, // "eure Box": the family's box
	"setup.welcome.step.2":                     true, // "Euer Ort": the family's town
	"setup.https.way.public.cost":              true, // "eure Box"
	"setup.https.way.domain.cost":              true, // "eure Box"
	"setup.https.way.own.means":                true, // "bei euch zu Hause"
	"setup.https.way.domain.means":             true, // "eurer Domain": the family's domain
	"setup.https.public.lan_ip.hint":           true, // "eure Geräte"
	"setup.https.rebind.why":                   true, // "bei euch zu Hause"
	"setup.totp.lead":                          true, // "eure Box"
	"firststeps.lead":                          true, // "eurem Familienleben"
	"firststeps.settled":                       true, // "eure Familie"
	"firststeps.close.hint":                    true, // "euren Tag"
	"mcp.intro.off":                            true, // "eurer Hejmo-Box": the family's box
	"mcp.intro.on":                             true, // "eurer Hejmo-Box"
	"mcp.switch.off":                           true, // "eure Daten": the family's data
	"mcp.area.events.desc":                     true, // "eure anderen Kalender": the family's calendars
	"mcp.explain.what":                         true, // "eure Hejmo-Box"
	"mcp.explain.know3":                        true, // "eurem Heimnetz": the family's home network
	"flash.mcp_off":                            true, // "eure Daten"
	"firststeps.display.text":                  true, // "euren Tag"
	"setup.https.domain.cloudflare.sub":        true, // "eure Domain": the family's domain
	"setup.https.domain.legend":                true, // "eure Domain": the family's domain
	"setup.https.way.domain":                   true, // "eure Domain": the family's domain
	"setup.https.error.name_taken":             true, // "eure Domain": the family's domain
	"setup.https.domain.name.text":             true, // "eure Domain": the family's domain
	"setup.https.domain.hetzner.token.text":    true, // "eure Domain": the family's domain
	"setup.https.public.failed.hetzner":        true, // "eure Domain": the family's domain
	"setup.https.domain.other":                 true, // "eure Domain": the family's domain
	"setup.https.public.failed.cloudflare":     true, // "eure Domain": the family's domain
	"setup.https.domain.lead":                  true, // "eure Domain": the family's domain
	"setup.https.domain.name.hint":             true, // "eure Domain": the family's domain
	"setup.https.domain.cloudflare.token.text": true, // "eure Domain": the family's domain
	"setup.https.domain.why.hetzner.text":      true, // "eure Domain": the family's domain
	"food.why.title":                           true, // "eurer Box": the family's box (preview 163)
	"theme.auto.parent":                        true, // "eures Zuhauses": the family's home
	"theme.auto.child":                         true, // "eures Zuhauses"
	"theme.where.personal_device":              true, // "behalten ihr Design": everyone else
	"theme.where.browser":                      true, // "behalten ihr Design"
	"art.sug.diet":                             true, // "bei euch": the family
	"art.fits.nobody":                          true,
	"art.fits.all":                             true,
	"artf.kind.hint":                           true,
	"lib.own.empty":                            true, // "was ihr oft kauft", "eure Lieblingsmarke": the family
	"lists.set.hide.none":                      true,
	"artf.url.hint":                            true, // "eurer Box": the family's box
	"art.copy.hint":                            true, // "eure Marke"
	"art.hide.off.diet":                        true,
	"artf.err.url.private":                     true, // "euer eigenes Netz": the family's network
	"lists.set.empty":                          true,
	"lib.lead":                                 true, // "wenn ihr etwas auf eine Liste setzt"
	"art.units.hint":                           true, // "bis ihr eine andere Einheit nehmt"
	"saver.hint.title":                         true, // "eure Fotos": the family's photos (preview 178)
	"displays.saver.on":                        true, // "euren Alben": the family's albums
	"photos.full.hint":                         true, // "ihr nicht mehr braucht": the family's photos
	"photos.up.wait":                           true, // "Wartet": the state of a file, not an imperative
	"google.s.warn.text":                       true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.replace.text_many":                 true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.s1.hint":                           true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.reg.lead":                          true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.why.text":                          true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.s.warn.title":                      true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.s4.hint":                           true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.error.client_rejected":             true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.notice.seven":                      true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.fact.app.own":                      true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.replace.text_none":                 true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.signin.lead":                       true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"google.replace.text_one_a":                true, // "eure App-Registrierung", "ihr": the family that owns the app registration at Google (preview 18)
	"connect.outlook.appid":                    true, // "eurer eigenen App-Registrierung": the family's
	"connect.outlook.appid.hint":               true, // "wenn ihr ... eine eigene angelegt habt": the family
	"outlook.fail.consent.ask":                 true, // "Schick ihr": the IT
	"contact.family":                           true, // "Aus eurer Familie": the people of the household (ticket 296)
	"contact.lead":                             true, // "Eure Familie und alle, die ihr erreichen wollt": the household's address book
	"contact.fam.lead":                         true, // "Gehört zu eurer Familie."
	"contact.country.hint.none":                true, // "im Land eures Zuhauses"
	"contact.error.name.taken_person":          true, // "aus eurer Familie"
	"hub.contacts.hint":                        true, // "eurer Familie und von allen, die ihr erreichen wollt"
	"hub.contacts.state":                       true, // "aus eurer Familie"
	"contact.f.rels.hint":                      true, // "Für wen in eurer Familie": the people of the household (ticket 298)
	"contact.f.rels.kids":                      true, // "legt ihr extra fest": the family sets what children see (ticket 298)
	"contact.others.empty":                     true, // "alle, die ihr anrufen wollt": the family's contacts (ticket 300, preview 2)
	"cimp.keep":                                true, // "eurer Box": the household's box (ticket 300)
	"cimp.k.hejmo.person":                      true, // "aus eurer Familie": the people of the household (ticket 300)
	"cimp.name.taken.person":                   true, // "aus eurer Familie": the people of the household (ticket 300)
	"cimp.keep.locked.name":                    true, // "aus eurer Familie": the people of the household (ticket 300)
	"cimp.keep.locked.birthday":                true, // "aus eurer Familie": the people of the household (ticket 300)
	"cimp.keep.locked.emails":                  true, // "aus eurer Familie": the people of the household (ticket 300)
}

func TestGermanLocaleAddressesTheUserAsDu(t *testing.T) {
	raw, err := os.ReadFile("locales/de.json")
	if err != nil {
		t.Fatal(err)
	}
	cat := map[string]string{}
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatal(err)
	}
	// Plural address (ihr/euch/euer...) and plural imperatives that are not "du".
	plural := regexp.MustCompile(`(?i:\b(ihr|euch|euer\w*|eur\w*)\b)|\b(Öffnet|Gebt|Wählt|Prüft|Tragt|Schaut|Klickt|Scannt|Nehmt|Macht|Legt|Stellt|Sagt|Wartet|Versucht|Nutzt|Benutzt)\b`)
	for key, text := range cat {
		if wholeFamilyTexts[key] {
			continue
		}
		// "ihr eigenes Zertifikat" and "ihr Zertifikat" (the certificate of the box) refer to the box, not to the reader.
		clean := strings.NewReplacer("ihr eigenes", "", "ihr Zertifikat", "").Replace(text)
		if m := plural.FindString(clean); m != "" {
			t.Errorf("de %s uses %q instead of du: %s", key, m, text)
		}
	}
	for key := range wholeFamilyTexts {
		if _, ok := cat[key]; !ok {
			t.Errorf("allow-list names unknown key %s", key)
		}
	}
}

// The 12-hour example keeps its space non-breaking, so "2:30 PM" never breaks across lines.
func TestTwelveHourExampleDoesNotBreak(t *testing.T) {
	b := i18n.MustLoad()
	for _, lang := range []string{"de", "en"} {
		if got := b.T(lang, "settings.time_format.12h"); !strings.Contains(got, "(2:30 PM)") {
			t.Errorf("%s settings.time_format.12h = %q, want 2:30 PM with a non-breaking space", lang, got)
		}
	}
}

// Ticket #135: the connect page and the calendars page have all their texts in
// both languages, every text the template asks for exists, and Apple's own menu
// names appear per locale.
func TestConnectTextsExistInBothLanguages(t *testing.T) {
	b := i18n.MustLoad()
	keys := []string{
		"connect.title", "connect.lead", "connect.legend", "connect.why.title", "connect.newtab.sr", "connect.newtab.after", "connect.done",
		"connect.icloud.name", "connect.icloud.sub", "connect.icloud.heading", "connect.icloud.why", "connect.icloud.link.url", "connect.icloud.link.label",
		"connect.icloud.step1.title", "connect.icloud.step1.text", "connect.icloud.step1.hint",
		"connect.icloud.step2.title", "connect.icloud.step2.text", "connect.icloud.notfound.summary", "connect.icloud.notfound.text", "connect.icloud.notfound.help",
		"connect.icloud.help.url", "connect.icloud.help.label", "connect.icloud.step3.title", "connect.icloud.step3.text", "connect.icloud.step4.title", "connect.icloud.step4.text",
		"connect.icloud.username", "connect.icloud.password", "connect.icloud.password.hint", "connect.icloud.submit",
		"connect.caldav.name", "connect.caldav.sub", "connect.caldav.heading", "connect.caldav.why", "connect.caldav.step1.title", "connect.caldav.step1.text",
		"connect.caldav.step1.hint", "connect.caldav.help.url", "connect.caldav.help.label", "connect.caldav.step2.title", "connect.caldav.step2.text", "connect.caldav.step3.title",
		"connect.caldav.url", "connect.caldav.url.hint", "connect.caldav.username", "connect.caldav.password", "connect.caldav.more", "connect.caldav.more.sum", "connect.caldav.single", "connect.caldav.submit",
		"connections.title", "connections.about", "connections.none", "connections.connect", "calendars.empty",
		"connections.success.icloud.none", "connections.success.icloud.one", "connections.success.icloud.many",
		"connections.success.caldav.none", "connections.success.caldav.one", "connections.success.caldav.many",
		"connections.error.login", "connections.error.login_icloud", "connections.error.login_caldav", "connections.error.server", "connections.error.server_icloud", "connections.error.server_saved",
	}
	for _, lang := range []string{"de", "en"} {
		for _, k := range keys {
			if got := b.T(lang, k); got == k || strings.TrimSpace(got) == "" {
				t.Errorf("%s lacks %s", lang, k)
			}
		}
	}
	for _, k := range []string{"connections.hint", "connections.field.url", "connections.field.username", "connections.field.password", "connections.add", "connections.submit"} {
		if got := b.T("de", k); got != k {
			t.Errorf("%s should be gone, found %q", k, got)
		}
	}
	// Apple's own menu names, per locale.
	for lang, names := range map[string][]string{
		"de": {"Anmelden und Sicherheit", "App-spezifische Passwörter", "Apple Account"},
		"en": {"Sign-In and Security", "App-Specific Passwords", "Apple Account"},
	} {
		all := b.T(lang, "connect.icloud.step2.text") + b.T(lang, "connect.icloud.step2.title") + b.T(lang, "connect.icloud.why") + b.T(lang, "connect.icloud.username")
		for _, n := range names {
			if !strings.Contains(strings.ToLower(all), strings.ToLower(n)) {
				t.Errorf("%s texts never name Apple's %q", lang, n)
			}
		}
	}
	// No address in a text, except the two link targets and the hint about how an address starts.
	address := regexp.MustCompile(`(?i)https?://[a-z0-9]|\bappleid\.apple\.com|\baccount\.apple\.com`)
	for _, lang := range []string{"de", "en"} {
		for _, k := range keys {
			if strings.HasSuffix(k, ".url") || strings.HasSuffix(k, ".label") {
				continue
			}
			if m := address.FindString(b.T(lang, k)); m != "" {
				t.Errorf("%s %s holds a plain-text address %q", lang, k, m)
			}
		}
	}
}

// A find-and-replace once left "telenumber_x" in English texts. No contact text shows an identifier-like word
// (lower case words joined by an underscore) to a person.
func TestEnglishContactTextsHoldNoStrayIdentifiers(t *testing.T) {
	raw, err := os.ReadFile("locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	cat := map[string]string{}
	if err := json.Unmarshal(raw, &cat); err != nil {
		t.Fatal(err)
	}
	ident := regexp.MustCompile(`[a-z]+_[a-z]+`)
	placeholder := regexp.MustCompile(`\{[^}]*\}`)
	for key, text := range cat {
		if !strings.HasPrefix(key, "contact.") {
			continue
		}
		plain := placeholder.ReplaceAllString(text, "") // {name} placeholders are no words
		if w := ident.FindString(plain); w != "" {
			t.Errorf("en %s holds the identifier-like word %q: %s", key, w, text)
		}
	}
}
