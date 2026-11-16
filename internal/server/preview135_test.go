package server_test

import (
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/web"
)

// TestWritePreview135 writes the self-contained preview page for ticket #135
// (guide for connecting iCloud and other CalDAV servers): the real calendars
// page and the real connect page, rendered by the real templates. Run:
// HEJMO_WRITE_PREVIEW_135=$PWD/docs/design/preview-135.html go test ./internal/server -run WritePreview135
func TestWritePreview135(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW_135")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_135 to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")
	style := strings.Replace(string(css), `url("/static/fonts/Figtree-wght.ttf")`, "url(data:font/ttf;base64,"+base64.StdEncoding.EncodeToString(font)+")", 1)

	type screen struct{ title, note, lang, body string }
	var screens []screen
	// inner is the page without scripts and stylesheet links, as the iframe gets it.
	inner := func(page string) string {
		page = regexp.MustCompile(`(?s)<script[^>]*></script>`).ReplaceAllString(page, "")
		start := strings.Index(page, "<body")
		start += strings.Index(page[start:], ">") + 1
		return page[start:strings.LastIndex(page, "</body>")]
	}
	get := func(b *box, lang, path string) string {
		return inner(b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang}).body)
	}
	post := func(b *box, lang string, form url.Values) string {
		return inner(b.do(http.MethodPost, "/connections", form, map[string]string{"Accept-Language": lang}).body)
	}
	long := "konstantin-alexander.mueller-luedenscheidt@icloud.com"

	b := signedInAPI(t).b
	screens = append(screens, screen{"Kalender-Seite: noch keine Verbindung", "Ein Satz und eine Aktion pro Abschnitt; die Anleitung steht auf der eigenen Seite. Ab 900 px stehen der Zurück-Link und der Inhalt rechts neben der Navigation.", "de", get(b, "de", "/calendars")})
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return hostedRemote{found: []caldavclient.Calendar{{URL: flowCalendar, Name: "Familie"}, {URL: flowCalendar2, Name: "Sebastian Fußballtraining FC Pasing U11"}}}, nil
	})
	success := post(b, "de", url.Values{"provider": {"icloud"}, "username": {long}, "password": {"abcd-efgh-ijkl-mnop"}})
	screens = append(screens, screen{"Kalender-Seite: mit einer Verbindung", "Eine Karte pro Verbindung (Dienst fett, Konto darunter), „Kalender auswählen“ als Zweitknopf, darunter „Weiteren Kalender verbinden“.", "de", get(b, "de", "/calendars")})
	screens = append(screens, screen{"Kalender verbinden: iCloud (Standard)", "iCloud ist vorausgewählt. Vier nummerierte Schritte; die Felder stehen im Schritt, in dem du ihren Wert bekommst.", "de", get(b, "de", "/connections/new")})
	screens = append(screens, screen{"iCloud: „App-spezifische Passwörter“ nicht gefunden (aufgeklappt)", "Die Hilfe sitzt unter dem Schritt, in dem man hängen bleibt.", "de", strings.Replace(get(b, "de", "/connections/new"), `<details class="more">`, `<details class="more" open>`, 1)})
	bad := signedInAPI(t).b
	bad.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return errRemote{err: &caldavclient.StatusError{Code: http.StatusUnauthorized}}, nil
	})
	screens = append(screens, screen{"iCloud: Passwort abgelehnt (Fehler am Feld, lange Adresse)", "Der Fehler steht am Passwortfeld, die E-Mail-Adresse bleibt stehen, das Passwort ist leer.", "de", post(bad, "de", url.Values{"provider": {"icloud"}, "username": {long}, "password": {"x"}})})
	screens = append(screens, screen{"Kalender verbinden: anderer Kalender-Server", "Eigene kurze Anleitung; keine Beispieladresse zum Abtippen.", "de", get(b, "de", "/connections/new?service=caldav")})
	down := signedInAPI(t).b
	down.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return downRemote{errRemote{err: &caldavclient.StatusError{Code: http.StatusNotFound}}}, nil
	})
	screens = append(screens, screen{"Anderer Server: Adresse antwortet nicht", "Fehler am Adressfeld, Eingaben bleiben stehen (außer dem Passwort).", "de", post(down, "de", url.Values{"provider": {"caldav"}, "url": {"https://cloud.familie-schmidt-oberhuber.de/remote.php/da"}, "username": {"mama"}, "password": {"x"}})})
	screens = append(screens, screen{"Nach dem Verbinden: Erfolgszeile", "Die Erfolgszeile sagt, wie viele Kalender gefunden wurden und was als Nächstes zu tun ist; der Zurück-Link sitzt im Rahmen.", "de", success})
	screens = append(screens, screen{"English: iCloud", "Both locales: the same structure in English (Apple's own English menu names).", "en", get(b, "en", "/connections/new")})

	var body strings.Builder
	var tpl strings.Builder
	sizes := []struct {
		theme string
		w, h  int
	}{{"light", 1024, 768}, {"dark", 1024, 768}, {"bw", 1024, 768}, {"light", 768, 1024}, {"bw", 768, 1024}, {"light", 375, 812}, {"dark", 375, 812}, {"bw", 375, 812}}
	names := map[string]string{"light": "Hell", "dark": "Dunkel", "bw": "Schwarz-weiß"}
	for i, s := range screens {
		fmt.Fprintf(&tpl, `<template id="s%d" data-lang="%s">%s</template>`+"\n", i, s.lang, s.body)
		fmt.Fprintf(&body, `<h2>%d. %s</h2><p class="note">%s</p><div class="row">`+"\n", i, html.EscapeString(s.title), html.EscapeString(s.note))
		for _, z := range sizes {
			fmt.Fprintf(&body, `<figure><figcaption>%s, %d × %d</figcaption><iframe data-doc="%d" data-theme="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", names[z.theme], z.w, z.h, i, z.theme, z.w, z.h, html.EscapeString(s.title))
		}
		body.WriteString("</div>\n")
	}
	page := `<!doctype html>
<!-- Generated by TestWritePreview135 from the real templates (ticket #135). The stylesheet and the font are inlined copies of web/static and do not follow later changes. No external requests; the links to Apple and Nextcloud are only followed when clicked. -->
<html lang="de"><head><meta charset="utf-8"><title>Hejmo preview #135 Kalender verbinden: iCloud und CalDAV</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:980px;line-height:1.4}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo #135 Kalender verbinden: Anleitung für iCloud und andere CalDAV-Server. Hell, dunkel und schwarz-weiß; 1024 × 768, 768 × 1024, 375 px Handy.</h1>
<p class="note">Erzeugt aus den echten Vorlagen (TestWritePreview135).</p>
` + body.String() + tpl.String() + `<template id="css">` + html.EscapeString(style) + `</template>
<script>
(function () {
  var css = document.getElementById("css").content.textContent;
  document.querySelectorAll("iframe[data-doc]").forEach(function (f) {
    var t = document.getElementById("s" + f.dataset.doc);
    f.srcdoc = '<!doctype html><html lang="' + t.dataset.lang + '" data-theme="' + f.dataset.theme + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + css + '</style></head><body class="web">' + t.innerHTML + '</body></html>';
  });
})();
</script>
</body></html>
`
	if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}
