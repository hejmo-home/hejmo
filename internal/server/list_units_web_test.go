package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Ticket 165: Settings, Lists, Units.

func unitForm(on ...string) url.Values {
	f := url.Values{}
	for _, u := range on {
		f.Set("unit_"+u, "on")
	}
	return f
}

func TestTheUnitsPageGroupsThemAndShowsTheCountryPreset(t *testing.T) {
	l := newLibBox(t)
	page := l.en("/settings/lists").body
	for _, want := range []string{`id="units"`, `class="steps unit-set"`, "Counting", "Weight", "Liquid", "Always on", "Preset for <b>Germany</b>", `name="unit_dozen" aria-label="dozen" checked`, `name="unit_g"`, `name="unit_kg"`, `name="unit_oz"`, `name="unit_pint_uk"`, "28.35 g", `name="reset"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the units page lacks %q", want)
		}
	}
	if strings.Contains(page, `name="unit_oz" aria-label="oz" checked`) || strings.Contains(page, `name="unit_piece"`) {
		t.Errorf("oz is off in Germany, and piece has no switch")
	}
	if d := l.de("/settings/lists").body; !strings.Contains(d, "Voreingestellt für <b>Deutschland</b>") || !strings.Contains(d, "Gewicht") || !strings.Contains(d, "Flüssigkeit") || !strings.Contains(d, "Immer an") {
		t.Errorf("German:\n%s", d)
	}
}

func TestParentsSwitchUnitsOnAndOffAndTheAddFormOffersOnlyThose(t *testing.T) {
	l := newLibBox(t)
	r := l.postEN("/settings/lists/units", unitForm("dozen", "g", "kg", "ml", "l", "oz", "lb"))
	if r.status != http.StatusSeeOther || r.location != "/settings/lists#units" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	on, custom, _ := l.db.ActiveUnits(l.h.ID)
	if !custom || strings.Join(on, ",") != "piece,dozen,g,kg,ml,l,oz,lb" {
		t.Errorf("stored %v %v", on, custom)
	}
	page := l.en(r.location).body
	if !strings.Contains(page, "Units saved.") || !strings.Contains(page, `name="unit_pack" aria-label="pack"`) || strings.Contains(page, `name="unit_pack" aria-label="pack" checked`) {
		t.Errorf("pack is off now:\n%s", page)
	}
	s := shop{l, ""}
	ls, _ := l.db.Lists(l.h.ID)
	s.id = ls[0].ID
	frag := s.en(s.path("/quantity?article=sugar")).body
	if !strings.Contains(frag, `value="oz"`) || strings.Contains(frag, `value="pack"`) || strings.Contains(frag, `value="bunch"`) {
		t.Errorf("only the units that are on:\n%s", frag)
	}
	if strings.Contains(frag, "(UK)") || strings.Contains(frag, "(US)") {
		t.Errorf("no region labels: only one system's units are on")
	}
}

func TestEachKindKeepsAUnitAndNothingIsSavedOtherwise(t *testing.T) {
	l := newLibBox(t)
	r := l.postEN("/settings/lists/units", unitForm("dozen", "g", "kg"))
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-unit-volume"`) || !strings.Contains(r.body, "At least one unit for liquids must stay on.") {
		t.Errorf("no liquid unit = %d\n%s", r.status, r.body)
	}
	if !strings.Contains(r.body, `name="unit_kg" aria-label="kg" checked`) {
		t.Errorf("what was chosen stays on the page")
	}
	if _, custom, _ := l.db.ActiveUnits(l.h.ID); custom {
		t.Errorf("a refused change stores nothing")
	}
	if r := l.postEN("/settings/lists/units", unitForm("l")); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "At least one weight unit must stay on.") {
		t.Errorf("no weight unit = %d", r.status)
	}
}

func TestResetBringsBackThePresetOfTheCountry(t *testing.T) {
	l := newLibBox(t)
	l.postEN("/settings/lists/units", unitForm("g", "l", "oz"))
	f := unitForm("g", "l", "oz")
	f.Set("reset", "1")
	r := l.postEN("/settings/lists/units", f)
	if r.status != http.StatusSeeOther {
		t.Fatalf("reset = %d", r.status)
	}
	if _, custom, _ := l.db.ActiveUnits(l.h.ID); custom {
		t.Errorf("the units follow the country again")
	}
	if page := l.en(r.location).body; !strings.Contains(page, "follow the country of your home again") {
		t.Errorf("the line:\n%s", page)
	}
}

func TestOnlyParentsChangeUnits(t *testing.T) {
	l := newLibBox(t)
	l.logout()
	if r := l.postEN("/settings/lists/units", unitForm("g", "l")); r.status == http.StatusSeeOther && !strings.HasPrefix(r.location, "/login") {
		t.Errorf("signed out: %d %q", r.status, r.location)
	}
}
