package server

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// unitBox is a server with only what the units need: a household in a country.
func unitBox(t *testing.T, region string) (*server, store.Household) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "hejmo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h, err := db.CreateHousehold(store.Household{Name: "Home", HomeName: "Home", Region: region, Timezone: "Europe/London"})
	if err != nil {
		t.Fatal(err)
	}
	return &server{db: db, i18n: i18n.MustLoad()}, h
}

func TestTheCountryOfTheHomeSetsTheUnits(t *testing.T) {
	for region, want := range map[string]string{
		"DE-BY":  "piece,dozen,pack,bunch,g,kg,ml,l",
		"AT-9":   "piece,dozen,pack,bunch,g,kg,ml,l",
		"CH-ZH":  "piece,dozen,pack,bunch,g,kg,ml,l",
		"":       "piece,dozen,pack,bunch,g,kg,ml,l",
		"GB-ENG": "piece,dozen,pack,bunch,g,kg,ml,l,oz,lb,floz_uk,pint_uk,gallon_uk",
		"US-CA":  "piece,dozen,pack,bunch,oz,lb,floz_us,cup_us,pint_us,quart_us,gallon_us",
	} {
		s, h := unitBox(t, region)
		if got := strings.Join(s.activeUnits(h), ","); got != want {
			t.Errorf("%q: %s", region, got)
		}
	}
}

func TestOneSystemsUnitsCarryNoRegionLabelAndBothSystemsDo(t *testing.T) {
	s, h := unitBox(t, "GB-ENG")
	c := &itemCtx{s: s, p: principal{household: h}, lang: "en", active: s.activeUnits(h)}
	if got := c.qtyWords(list.NewQuantity(3, "pint_uk")); got != "3 pints" {
		t.Errorf("UK only: %q", got)
	}
	if got := c.qtyWords(list.NewQuantity(1, "pint_uk")); got != "1 pint" {
		t.Errorf("one: %q", got)
	}
	if got := c.qtyWords(list.NewQuantity(2, "pack")); got != "2 packs" {
		t.Errorf("%q", got)
	}
	if got := c.qtyWords(list.NewQuantity(2, "dozen")); got != "2 dozen" {
		t.Errorf("%q", got)
	}
	both := &itemCtx{s: s, p: principal{household: h}, lang: "en", active: append(slices.Clone(c.active), "pint_us", "floz_us")}
	if got := both.qtyWords(list.NewQuantity(3, "pint_uk")); got != "3 pints (UK)" {
		t.Errorf("both: %q", got)
	}
	if got := both.qtyWords(list.NewQuantity(3, "pint_us")); got != "3 pints (US)" {
		t.Errorf("both: %q", got)
	}
	if got := both.qtyWords(list.NewQuantity(2, "cup_us")); got != "2 cups" {
		t.Errorf("a cup has no UK twin: %q", got)
	}
	de := &itemCtx{s: s, p: principal{household: h}, lang: "de", active: both.active}
	if got := de.qtyWords(list.NewQuantity(3, "gallon_uk")); got != "3 Gallonen" {
		t.Errorf("only one gallon is on: %q", got)
	}
	if got := de.qtyWords(list.NewQuantity(1.5, "pint_uk")); got != "1,5 Pints (GB)" {
		t.Errorf("German, both on: %q", got)
	}
}

func TestMilkInTheUKOffersPintsFirstAndNoLabels(t *testing.T) {
	s, h := unitBox(t, "GB-ENG")
	c := &itemCtx{s: s, p: principal{household: h}, lang: "en", active: s.activeUnits(h)}
	// A default unit that is off gives way to the unit of its kind with the nearest size.
	got := c.offered(&articleItem{Article: libraryArticle("milk", []string{"l", "ml"})})
	if strings.Join(got, ",") != "l,ml" {
		t.Errorf("metric stays on in the UK: %v", got)
	}
	c.active = []string{"piece", "g", "kg", "ml", "pint_uk", "gallon_uk"}
	got = c.offered(&articleItem{Article: libraryArticle("milk", []string{"l", "ml"})})
	if strings.Join(got, ",") != "pint_uk,ml" {
		t.Errorf("litre gives way to the pint: %v", got)
	}
}

func libraryArticle(id string, units []string) library.Article {
	return library.Article{ID: id, De: id, En: id, Units: units}
}

// Every unit of ADR 0019 reads in words in both languages, in the singular and the plural.
func TestEveryUnitReadsInBothLanguagesWithAndWithoutALabel(t *testing.T) {
	s, h := unitBox(t, "DE-BY")
	all := unit.All
	for _, lang := range []string{"de", "en"} {
		c := &itemCtx{s: s, p: principal{household: h}, lang: lang, active: all}
		for _, k := range all {
			one, many := c.qtyWords(list.NewQuantity(1, k)), c.qtyWords(list.NewQuantity(2, k))
			for _, got := range []string{one, many, c.chip(k, "").Label} {
				if strings.Contains(got, "unit.") || strings.TrimSpace(got) == "" {
					t.Errorf("%s %s: %q is not words", lang, k, got)
				}
			}
			if !strings.HasPrefix(one, "1 ") || !strings.HasPrefix(many, "2 ") {
				t.Errorf("%s %s: %q, %q", lang, k, one, many)
			}
			for _, key := range []string{"unit." + k + ".one", "unit." + k + ".other", "unit." + k + ".long"} {
				if s.i18n.T(lang, key) == key {
					t.Errorf("%s lacks %s", lang, key)
				}
			}
		}
	}
	// The plural in German and English.
	de := &itemCtx{s: s, p: principal{household: h}, lang: "de", active: unit.DefaultActive}
	en := &itemCtx{s: s, p: principal{household: h}, lang: "en", active: unit.DefaultActive}
	for _, c := range []struct {
		ctx  *itemCtx
		q    list.Quantity
		want string
	}{
		{de, list.NewQuantity(1, "pack"), "1 Packung"}, {de, list.NewQuantity(2, "pack"), "2 Packungen"}, {de, list.NewQuantity(0.5, "pack"), "0,5 Packungen"},
		{de, list.NewQuantity(6, "piece"), "6 Stück"}, {de, list.NewQuantity(2, "dozen"), "2 Dutzend"}, {de, list.NewQuantity(1500, "g"), "1500 g"},
		{en, list.NewQuantity(2, "pack"), "2 packs"}, {en, list.NewQuantity(1, "bunch"), "1 bunch"}, {en, list.NewQuantity(2, "bunch"), "2 bunches"},
		{en, list.NewQuantity(1.5, "l"), "1.5 l"},
	} {
		if got := c.ctx.qtyWords(c.q); got != c.want {
			t.Errorf("%s: %q, want %q", c.ctx.lang, got, c.want)
		}
	}
}
