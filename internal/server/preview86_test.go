package server_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/web"
)

// TestWriteDesignPreview86 writes the self-contained preview page for the
// home location (Q59): the setup wizard and Settings → Household, rendered
// by the real templates of a box that holds the example family, in light, dark
// and black and white, 1024 px wide, in portrait (768 px) and on a phone (375 px), plus the German phone pages (375 px, light and black and white).
// Run: HEJMO_WRITE_PREVIEW_86=$PWD/docs/design/preview-86.html go test ./internal/server -run WriteDesignPreview86
func TestWriteDesignPreview86(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW_86")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_86 to the output path to write the preview")
	}
	css, err := web.Static.ReadFile("static/hejmo.css")
	if err != nil {
		t.Fatal(err)
	}
	font, err := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")
	if err != nil {
		t.Fatal(err)
	}
	style := strings.Replace(string(css), `url("/static/fonts/Figtree-wght.ttf")`, "url(data:font/ttf;base64,"+base64.StdEncoding.EncodeToString(font)+")", 1)
	// The frames sit side by side on one page at their own widths: the frame's
	// media queries become container queries on the .device that holds each one.
	for _, q := range []string{"(min-width: 900px)", "(max-width: 599px)"} {
		style = strings.ReplaceAll(style, "@media "+q, "@container "+q)
	}

	de := map[string]string{"Accept-Language": "de"}
	frameOf := func(r resp) string {
		t.Helper()
		i, j := strings.Index(r.body, `<div class="frame framed`), strings.LastIndex(r.body, "</body>")
		if i < 0 || j < 0 {
			t.Fatalf("no frame in the page (status %d):\n%s", r.status, r.body)
		}
		// The preview is static: forms must not leave it, scripts are not copied.
		return strings.ReplaceAll(r.body[i:j], `action="`, `data-action="`)
	}

	// The setup wizard of a new box: several places match (five long names), and none does.
	fresh := newBox(t)
	fresh.geo.set("Neustadt", `{"results":[`+
		`{"name":"Neustadt an der Weinstraße","latitude":49.35,"longitude":8.14,"country":"Germany","admin1":"Rhineland-Palatinate"},`+
		`{"name":"Neustadt bei Coburg","latitude":50.32,"longitude":11.12,"country":"Germany","admin1":"Bavaria","admin2":"Coburg"},`+
		`{"name":"Neustadt an der Aisch","latitude":49.58,"longitude":10.61,"country":"Germany","admin1":"Bavaria","admin2":"Middle Franconia"},`+
		`{"name":"Neustadt an der Donau","latitude":48.8,"longitude":11.77,"country":"Germany","admin1":"Bavaria","admin2":"Kelheim"},`+
		`{"name":"Neustadt in Holstein","latitude":54.11,"longitude":10.81,"country":"Germany","admin1":"Schleswig-Holstein"}]}`)
	setupMany := frameOf(fresh.do(http.MethodPost, "/setup", unconfirmedSetup("Neustadt"), en))
	setupNone := frameOf(fresh.do(http.MethodPost, "/setup", unconfirmedSetup("Nirgendwo"), en))

	// Settings → Household of the configured example family (Mama, Papa, Sebastian, Mia).
	f := newFamilyBox(t, "screen")
	f.now = f.at(14, 12, 0)      // the sun times of the status box are those of this day
	f.get("/settings/household") // takes the "Display paired." line of the fixture's setup, which is not part of the design
	settings := frameOf(f.do(http.MethodGet, "/settings/household", nil, en))
	typed := settingsForm("Pasing-Obermenzing") // as typed by a family that keeps 24-hour time and Celsius
	typed.Set("time_format", "24h")
	typed.Set("temperature_unit", "c")
	// A new name: one place found, to save (nothing is stored yet).
	f.geo.set("Pasing-Obermenzing", `{"results":[{"name":"Pasing-Obermenzing","latitude":48.16,"longitude":11.45,"country":"Germany","admin1":"Bavaria","admin2":"Upper Bavaria"}]}`)
	settingsPending := frameOf(f.do(http.MethodPost, "/settings/household", typed, en))
	// The German pages show the longer name of the district as the place in use.
	storedHome := func(name string) {
		t.Helper()
		if _, err := f.db.SQL().Exec(`UPDATE households SET home_name = ?`, name); err != nil {
			t.Fatal(err)
		}
	}
	storedHome("Pasing-Obermenzing")
	// The same two screens in German for the phone (Landkreise, long words): a very long place name.
	f.geo.set("Schmedeswurtherwesterdeich", `{"results":[{"name":"Schmedeswurtherwesterdeich","latitude":54.07,"longitude":9.03,"country":"Deutschland","admin1":"Schleswig-Holstein","admin2":"Kreis Dithmarschen"}]}`)
	typedDE := settingsForm("Schmedeswurtherwesterdeich")
	typedDE.Set("time_format", "24h")
	typedDE.Set("temperature_unit", "c")
	settingsPendingDE := frameOf(f.do(http.MethodPost, "/settings/household", typedDE, de))
	storedHome("Pasing")
	// The search cannot be reached: the old place stays in use.
	f.geo.setDown(true)
	settingsDown := frameOf(f.do(http.MethodPost, "/settings/household", typed, en))
	storedHome("Pasing-Obermenzing")
	downDE := settingsForm("Garmisch-Partenkirchen")
	downDE.Set("time_format", "24h")
	downDE.Set("temperature_unit", "c")
	settingsDownDE := frameOf(f.do(http.MethodPost, "/settings/household", downDE, de))
	storedHome("Pasing")
	f.geo.setDown(false)
	// A box set up before the home had a location.
	f.withoutLocation(t)
	settingsMissing := frameOf(f.do(http.MethodGet, "/settings/household", nil, en))
	// Saving the found place: the success line names it.
	save := typed
	save.Set("place_for", "Pasing-Obermenzing")
	save.Set("place", "48.16,11.45")
	if r := f.do(http.MethodPost, "/settings/household", save, en); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	settingsSaved := frameOf(f.do(http.MethodGet, "/settings/household", nil, en))
	saveDE := typedDE
	saveDE.Set("place_for", "Schmedeswurtherwesterdeich")
	saveDE.Set("place", "54.07,9.03")
	saveDE.Set("place_label", "Schmedeswurtherwesterdeich, Kreis Dithmarschen, Schleswig-Holstein, Deutschland")
	if r := f.do(http.MethodPost, "/settings/household", saveDE, de); r.status != http.StatusSeeOther {
		t.Fatalf("save (de) = %d\n%s", r.status, r.body)
	}
	settingsSavedDE := frameOf(f.do(http.MethodGet, "/settings/household", nil, de))

	// The setup wizard in German: several places with long names and Landkreise.
	freshDE := newBox(t)
	freshDE.geo.set("Neustadt", `{"results":[`+
		`{"name":"Neustadt an der Waldnaab","latitude":49.73,"longitude":12.17,"country":"Deutschland","admin1":"Bayern","admin2":"Landkreis Neustadt an der Waldnaab"},`+
		`{"name":"Bad Neustadt an der Saale","latitude":50.32,"longitude":10.21,"country":"Deutschland","admin1":"Bayern","admin2":"Landkreis Rhön-Grabfeld"},`+
		`{"name":"Titisee-Neustadt","latitude":47.92,"longitude":8.21,"country":"Deutschland","admin1":"Baden-Württemberg","admin2":"Landkreis Breisgau-Hochschwarzwald"},`+
		`{"name":"Neustadt am Rübenberge","latitude":52.5,"longitude":9.46,"country":"Deutschland","admin1":"Niedersachsen","admin2":"Region Hannover"},`+
		`{"name":"Neustadt an der Weinstraße","latitude":49.35,"longitude":8.14,"country":"Deutschland","admin1":"Rheinland-Pfalz"}]}`)
	setupManyDE := frameOf(freshDE.do(http.MethodPost, "/setup", unconfirmedSetup("Neustadt"), de))

	views := []struct{ title, html string }{
		{"Setup wizard: several places match, the parent chooses (five long names, none preselected)", setupMany},
		{"Setup wizard: no place found, field error, what was typed is kept", setupNone},
		{"Settings → Household: the home with its location in use", settings},
		{"Settings → Household: a new name, one place found, to confirm (nothing saved yet)", settingsPending},
		{"Settings → Household: place search unreachable, field error, typed name kept, old location still in use", settingsDown},
		{"Settings → Household: box set up before this setting, name but no location yet", settingsMissing},
		{"Settings → Household: saved, the success line names the new place", settingsSaved},
	}
	themes := []struct{ key, label string }{{"light", "Light"}, {"dark", "Dark"}, {"bw", "Black and white"}}
	germanViews := []struct{ title, html string }{
		{"Deutsch: Einrichtung, mehrere Orte passen (lange Namen und Landkreise)", setupManyDE},
		{"Deutsch: Einstellungen → Haushalt, ein Ort gefunden (Schmedeswurtherwesterdeich, ein langes Wort)", settingsPendingDE},
		{"Deutsch: Einstellungen → Haushalt, Ortssuche nicht erreichbar (langer Fehlertext am Feld)", settingsDownDE},
		{"Deutsch: Einstellungen → Haushalt, gespeichert (Erfolgszeile mit langem Ortsnamen)", settingsSavedDE},
	}
	var body strings.Builder
	for _, w := range []struct {
		px    int
		title string
	}{{1024, "1024 px wide"}, {768, "Portrait, 768 px wide"}, {375, "Phone, 375 px wide"}} {
		fmt.Fprintf(&body, `<h2 class="sec">%s</h2>`+"\n", w.title)
		for _, v := range views {
			fmt.Fprintf(&body, `<h3 class="sec">%s</h3><div class="row-of">`+"\n", v.title)
			for _, th := range themes {
				fmt.Fprintf(&body, `<figure class="frame-fig"><figcaption>%s (%d px)</figcaption><div class="shell" style="width:%dpx"><div class="device" data-theme="%s">%s</div></div></figure>`+"\n", th.label, w.px, w.px, th.key, v.html)
			}
			body.WriteString("</div>\n")
		}
	}
	// German phone frames: lang="de" on the device so words hyphenate in German.
	body.WriteString(`<h2 class="sec">Phone, 375 px wide, German: long words, place names and texts (lang="de", hyphenation)</h2>` + "\n")
	for _, v := range germanViews {
		fmt.Fprintf(&body, `<h3 class="sec">%s</h3><div class="row-of">`+"\n", v.title)
		for _, th := range []struct{ key, label string }{{"light", "Light"}, {"bw", "Black and white"}} {
			fmt.Fprintf(&body, `<figure class="frame-fig"><figcaption>%s (375 px), Deutsch</figcaption><div class="shell" style="width:375px"><div class="device" lang="de" data-theme="%s">%s</div></div></figure>`+"\n", th.label, th.key, v.html)
		}
		body.WriteString("</div>\n")
	}
	html := `<!doctype html>
<!-- Generated by TestWriteDesignPreview86 from the real templates (home location, Q59, with the design pass of #86). Design pass 2 (15/11/2026, margins and line breaks): shared block "Page padding and line breaks" added to the stylesheet copy, frames padded like body.web at their own width (container queries), German phone frames added. The stylesheet and font are inlined copies of web/static and do not follow later changes. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview: home location</title>
<style>` + style + `
.device { container-type: inline-size; width: 100%; overflow: hidden; background: var(--background); color: var(--text); }
.device > .frame { padding: var(--margin); }
@container (max-width: 599px) { .device > .frame { padding: var(--gap); } }
.shell { border: 1px solid #888; flex: none; overflow: hidden; }
.frame-fig { margin: 0; } .frame-fig figcaption { font: 14px system-ui, sans-serif; margin: 0 0 6px; color: #fff; }
.row-of { display: flex; gap: 24px; align-items: flex-start; flex-wrap: nowrap; overflow-x: auto; margin-bottom: 32px; padding: 8px; background: #888; }
.sec { color: #111; font: 700 20px system-ui, sans-serif; margin: 24px 8px 8px; }
h3.sec { font-size: 16px; }
body { margin: 0; background: #fff; color: #111; font-family: Figtree, system-ui, sans-serif; padding: 0; }
</style></head><body>
<h1 style="font:700 26px system-ui;margin:16px 8px">Hejmo home location: preview with the example family (Mama, Papa, Sebastian, Mia)</h1>
` + body.String() + `</body></html>
`
	if err := os.WriteFile(out, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
}
