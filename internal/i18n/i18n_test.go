package i18n_test

import (
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
	if got := b.T("de", "setup.title"); got != "Hejmo einrichten" {
		t.Errorf("de setup.title = %q", got)
	}
	if got := b.T("en", "setup.title"); got != "Set up Hejmo" {
		t.Errorf("en setup.title = %q", got)
	}
}

func TestUnknownLanguageFallsBackToDefault(t *testing.T) {
	b := i18n.MustLoad()
	if got := b.T("fr", "setup.title"); got != "Hejmo einrichten" {
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
