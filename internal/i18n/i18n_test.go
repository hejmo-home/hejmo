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
	"displays.none":                  true, // "euren Tag": the family's day on the wall
	"setup.welcome.lead":             true, // "eure Box": the family's box
	"setup.welcome.step.2":           true, // "Euer Ort": the family's town
	"setup.step.home":                true, // "Euer Zuhause"
	"setup.https.way.public.hint":    true, // "eure Box"
	"setup.https.way.own.hint":       true, // "euer Zuhause", "mit dem ihr die Box öffnet"
	"setup.https.public.lan_ip.hint": true, // "eure Geräte"
	"setup.https.rebind.why":         true, // "bei euch zu Hause"
	"setup.https.check.ok.hint":      true, // "Öffnet ihr die Box": the family opens it
	"setup.totp.lead":                true, // "eure Box"
	"firststeps.family.text":         true, // "bei euch wohnen"
	"firststeps.calendars.text":      true, // "Ihr könnt auch nur den Kalender der Box nutzen"
	"firststeps.lead.1":              true, // "euren Tag"
	"firststeps.lead.2":              true, // "euren Tag"
	"firststeps.lead.3":              true, // "euren Tag"
	"firststeps.devices.text":        true, // "euren Tag"
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
		// "ihr eigenes Zertifikat" refers to the box, not to the reader.
		clean := strings.ReplaceAll(text, "ihr eigenes", "")
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
