package server_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/web"
)

var (
	widthQueryRe   = regexp.MustCompile(`@media \(((?:min|max)-width: \d+px)\)`)
	pickErrorRe    = regexp.MustCompile(`(?s)<p class="ferr" id="err-home" role="alert">.*?</p>\n  `)
	pickButtonRe   = regexp.MustCompile(`(<li role="option"[^>]*>)<button [^>]*>(.*?)</button></li>`)
	invalidFieldRe = regexp.MustCompile(` aria-invalid="true" aria-describedby="err-home"`)
)

// typing is a page as it looks while the parent types, with the list open: in
// a browser the script updates the list and nothing is submitted, so the error
// of the submit without JavaScript is left out. The rows lose the submit
// buttons they have without JavaScript (the script replaces them with plain rows).
func typing(page string) string {
	page = pickErrorRe.ReplaceAllString(page, "")
	page = invalidFieldRe.ReplaceAllString(page, "")
	page = strings.Replace(page, `place-pick invalid"`, `place-pick"`, 1)
	return pickButtonRe.ReplaceAllString(page, "$1$2</li>")
}

// TestWriteDesignPreview86 writes the self-contained preview page for the
// home location (ADR 0020): the setup wizard and Settings → Household
// rendered by the real templates of a box that holds the example family, in
// light, dark and black and white, 1024 px wide, in portrait (768 px) and on a
// phone (375 px), plus the German phone pages (375 px, light and black and
// white). The places come from the list on the box.
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
	// media queries become container queries on the .device that holds each
	// one, and the line-break rules of body.web apply to .device.
	style = widthQueryRe.ReplaceAllString(style, "@container ($1)")
	style = strings.ReplaceAll(style, "body.web { overflow-wrap", "body.web, .device { overflow-wrap")
	style = strings.ReplaceAll(style, "body.web { -webkit-hyphens", "body.web, .device { -webkit-hyphens")

	// The setup frames use the wizard shell: everything inside <body>.
	wizardFrame := func(r resp) string {
		t.Helper()
		if !strings.Contains(r.body, `<div class="wizard">`) {
			t.Fatalf("no wizard in the page (status %d):\n%s", r.status, r.body)
		}
		return `<div class="wizard-pad">` + "\n" + strings.ReplaceAll(inlineFlags(t, bodyOf(t, r.body)), `action="`, `data-action="`)
	}
	// The Settings frames start at the page frame.
	settingsFrame := func(r resp) string {
		t.Helper()
		i, j := strings.Index(r.body, `<div class="frame framed`), strings.LastIndex(r.body, "</body>")
		if i < 0 || j < 0 {
			t.Fatalf("no frame in the page (status %d):\n%s", r.status, r.body)
		}
		// The preview is static: forms must not leave it, scripts are not copied.
		return strings.ReplaceAll(scriptTagRe.ReplaceAllString(r.body[i:j], ""), `action="`, `data-action="`)
	}

	// The setup wizard of a new box.
	fresh := newBox(t)
	setupTyping := wizardFrame(fresh.do(http.MethodPost, "/setup", typedSetup("Neustadt"), en))
	setupTyping = typing(setupTyping)
	setupChosen := wizardFrame(fresh.do(http.MethodPost, "/setup", pickedSetup("München", idMuenchen), en))
	setupNone := wizardFrame(fresh.do(http.MethodPost, "/setup", typedSetup("Pasingg"), en))
	freshDE := newBox(t)
	setupTypingDE := typing(wizardFrame(freshDE.do(http.MethodPost, "/setup", typedSetup("Neustadt"), de)))

	// Settings → Household of the configured example family (Mama, Papa, Sebastian, Mia).
	f := newFamilyBox(t, "screen")
	f.now = f.at(14, 12, 0)      // the sun times of the status box are those of this day
	f.get("/settings/household") // takes the "Display paired." line of the fixture's setup, which is not part of the design
	settingsInUse := settingsFrame(f.do(http.MethodGet, "/settings/household", nil, en))
	form := func(extra url.Values) url.Values {
		v := url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}}
		for k, vs := range extra {
			v[k] = vs
		}
		return v
	}
	// "Change" pressed and a new place typed: the old one stays in use until Save.
	settingsChanging := typing(settingsFrame(f.do(http.MethodPost, "/settings/household", form(url.Values{"home_name": {"Obermen"}}), en)))
	// German: a place picked and not saved yet, a place not found with the keep row.
	settingsPendingDE := settingsFrame(f.do(http.MethodPost, "/settings/household", form(url.Values{"home_name": {"Schmedeswurther"}, "place_id": {idSchmedeswurth}}), de))
	settingsNoneDE := settingsFrame(f.do(http.MethodPost, "/settings/household", form(url.Values{"home_name": {"Garmisch-Partenkirchn"}}), de))
	// A box set up before the list of places: a name, no place yet.
	f.withoutLocation(t)
	settingsMissing := typing(settingsFrame(f.do(http.MethodGet, "/settings/household", nil, en)))
	// Saving a place: the success line names it.
	if r := f.post("/settings/household", form(url.Values{"place_id": {idObermenzing}})); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	settingsSaved := settingsFrame(f.do(http.MethodGet, "/settings/household", nil, en))
	if r := f.post("/settings/household", form(url.Values{"place_id": {idSchmedeswurth}})); r.status != http.StatusSeeOther {
		t.Fatalf("save (de) = %d\n%s", r.status, r.body)
	}
	settingsSavedDE := settingsFrame(f.do(http.MethodGet, "/settings/household", nil, de))

	views := []struct{ title, html string }{
		{"Setup wizard: typing; up to five places from the list on the box, name in bold and region below; none chosen yet", setupTyping},
		{"Setup wizard: place chosen, confirmed in words with a tick and “Change”; the state is taken from the place", setupChosen},
		{"Setup wizard: no place found, the error is at the field, what was typed is kept", setupNone},
		{"Settings → Household: the home location in use, with today’s sunrise and sunset", settingsInUse},
		{"Settings → Household: “Change” pressed, typing a new place; the old one stays in use until Save", settingsChanging},
		{"Settings → Household: box set up before this setting, no place chosen yet; the list is already open for the saved name", settingsMissing},
		{"Settings → Household: saved, the success line and the box name the new place", settingsSaved},
	}
	themes := []struct{ key, label string }{{"light", "Light"}, {"dark", "Dark"}, {"bw", "Black and white"}}
	germanViews := []struct{ title, html string }{
		{"Deutsch: Einrichtung, beim Tippen 5 Orte mit langen Namen, keiner gewählt", setupTypingDE},
		{"Deutsch: Einstellungen → Haushalt, neuer Ort gewählt, noch nicht gespeichert (Schmedeswurtherwesterdeich, ein langes Wort)", settingsPendingDE},
		{"Deutsch: Einstellungen → Haushalt, kein Ort gefunden (langer Text am Feld); der alte Ort bleibt in Verwendung und lässt sich behalten", settingsNoneDE},
		{"Deutsch: Einstellungen → Haushalt, gespeichert (Erfolgszeile und Kasten mit langem Ortsnamen)", settingsSavedDE},
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
<!-- Generated by TestWriteDesignPreview86 from the real templates (home location picker, ADR 0020, ticket 86, design pass 3). The stylesheet and font are inlined copies of web/static and do not follow later changes; width media queries are container queries here, and the line-break rules of body.web apply to .device. -->
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
.device > .wizard-pad { padding: var(--margin); }
@container (max-width: 599px) { .device > .wizard-pad { padding: var(--gap); } }
</style></head><body>
<h1 style="font:700 26px system-ui;margin:16px 8px">Hejmo home location: place picker with suggestions from the list on the box (example family Mama, Papa, Sebastian, Mia)</h1>
` + body.String() + `</body></html>
`
	if err := os.WriteFile(out, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
}
