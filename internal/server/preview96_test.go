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
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/web"
)

// TestWritePreview96 writes the self-contained preview page for ticket 96
// (the MCP server's page "KI-Assistenten (MCP)"): every frame is a real page of
// the box, rendered by the real templates in German for the sample family, at
// 1024 x 768, 768 x 1024 and 375 px wide, in light, dark and black-and-white.
// Run: HEJMO_WRITE_PREVIEW_96=$PWD/docs/design/preview-96.html go test ./internal/server -run WritePreview96
func TestWritePreview96(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW_96")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_96 to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	formJS, _ := web.Static.ReadFile("static/form.js")
	copyJS, _ := web.Static.ReadFile("static/token-copy.js")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")

	de := map[string]string{"Accept-Language": "de"}
	local, _ := time.LoadLocation("Europe/Berlin")
	moment := func(day, h, m, s int) string {
		return time.Date(2026, 11, day, h, m, s, 0, local).UTC().Format(time.RFC3339)
	}
	// A box of the sample family, its clock at 15/11/2026, 07:42:30 in Munich.
	sampleBox := func() (*box, map[string]string) {
		b := signedInBox(t)
		b.now = time.Date(2026, 11, 15, 7, 42, 30, 0, local)
		h, _ := b.db.HomeHousehold()
		for _, p := range []struct{ name, role string }{{"Papa", "parent"}, {"Sebastian", "child"}, {"Mia", "child"}} {
			if _, err := b.db.AddPerson(h.ID, store.Person{Name: p.name, Role: store.Role(p.role)}); err != nil {
				t.Fatal(err)
			}
		}
		people, _ := b.db.People(h.ID)
		ids := map[string]string{}
		for _, p := range people {
			ids[p.Name] = p.ID
		}
		return b, ids
	}
	sql := func(b *box, query string, args ...any) {
		t.Helper()
		if _, err := b.db.SQL().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	page := func(b *box, path string) string {
		t.Helper()
		r := b.do(http.MethodGet, path, nil, de)
		if r.status != http.StatusOK {
			t.Fatalf("%s = %d\n%s", path, r.status, r.body)
		}
		return r.body
	}
	post := func(b *box, path string, form url.Values, want int) resp {
		t.Helper()
		r := b.do(http.MethodPost, path, form, de)
		if r.status != want {
			t.Fatalf("POST %s = %d, want %d\n%s", path, r.status, want, r.body)
		}
		return r
	}
	switchOn := func(b *box, on bool) {
		form := url.Values{}
		if on {
			form.Set("mcp", "on")
		}
		post(b, "/mcp", form, http.StatusSeeOther)
	}
	token := func(b *box, ids map[string]string, owner, name string, scopes string, created, used string) string {
		t.Helper()
		h, _ := b.db.HomeHousehold()
		tok, err := b.db.CreateAPIToken(store.APIToken{Name: name, Hash: auth.HashToken("preview-" + name), PersonID: ids[owner], Scopes: scopes, MCP: true})
		if err != nil {
			t.Fatal(err)
		}
		_ = h
		sql(b, `UPDATE api_tokens SET created_at = ? WHERE id = ?`, created, tok.ID)
		if used != "" {
			sql(b, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, used, tok.ID)
		}
		return tok.ID
	}
	open := func(html string) string {
		return strings.ReplaceAll(html, `<details class="danger">`, `<details class="danger" open>`)
	}

	// ---- a box without tokens: the first visit, the first token ----
	b0, _ := sampleBox()
	hub := page(b0, "/settings")
	firstVisit := page(b0, "/mcp")
	switchOn(b0, true)
	justOn := page(b0, "/mcp")
	newForm := page(b0, "/mcp/tokens/new")
	bad := post(b0, "/mcp/tokens", url.Values{"name": {""}, "events": {"none"}, "lists": {"none"}, "tasks": {"none"}, "meals": {"none"}}, http.StatusUnprocessableEntity).body
	once := post(b0, "/mcp/tokens", url.Values{"name": {"Claude auf Mamas Laptop"}, "events": {"write"}, "lists": {"write"}, "tasks": {"read"}, "meals": {"write"}}, http.StatusOK).body
	secret := mcpSecretRe.FindStringSubmatch(once)[1]
	once = strings.ReplaceAll(once, secret, "hjm_9XcR4tLw2PqZ7kVn3HbE8sYd")

	// ---- the box of the family with three tokens ----
	b, ids := sampleBox()
	switchOn(b, true)
	created := moment(4, 19, 20, 11)
	a1 := token(b, ids, "Mama", "Claude auf Mamas Laptop", "events lists tasks:read meals", created, moment(15, 7, 42, 13))
	b2 := token(b, ids, "Papa", "Claude Desktop auf dem Arbeitslaptop von Papa (Büro im Dachgeschoss)", "events:read meals:read", created, moment(14, 21, 5, 44))
	c3 := token(b, ids, "Papa", "Einkaufshelfer Papa", "lists", created, "")
	withTokens := page(b, "/mcp")
	detail := page(b, "/mcp/tokens/"+b2)
	revokeAsks := open(page(b, "/mcp/tokens/"+c3))
	switchOn(b, false)
	switchedOff := page(b, "/mcp")
	tokenWhileOff := page(b, "/mcp/tokens/"+a1)
	switchOn(b, true)
	post(b, "/mcp/tokens/"+c3+"/revoke", url.Values{}, http.StatusSeeOther)
	afterRevoke := page(b, "/mcp")

	// ---- the page ----
	type frame struct {
		theme string
		w, h  int
	}
	var frames []frame
	for _, size := range [][2]int{{1024, 768}, {768, 1024}, {375, 812}} {
		for _, th := range []string{"light", "dark", "bw"} {
			frames = append(frames, frame{th, size[0], size[1]})
		}
	}
	var body strings.Builder
	var docs []string
	docOf := map[string]int{}
	host := regexp.MustCompile(`https://127\.0\.0\.1(:[0-9]+)?`)
	notice := regexp.MustCompile(`(?s)\s*<p class="notice" role="status">.*?</p>`)
	add := func(title, note, html string, ownCA bool) {
		if ownCA { // the box of this state has its own certificate and the name hejmo.local
			html = host.ReplaceAllString(html, "https://hejmo.local")
		} else { // the box of the sample family has a public name and certificate
			html = host.ReplaceAllString(html, "https://hejmo.familie-maier.de")
			html = notice.ReplaceAllString(html, "")
		}
		idx, seen := docOf[html]
		if !seen {
			idx = len(docs)
			docOf[html] = idx
			docs = append(docs, html)
		}
		fmt.Fprintf(&body, "<h2>%s</h2><p class=\"note\">%s</p><div class=\"row\">\n", title, note)
		for _, f := range frames {
			label := map[string]string{"light": "Hell", "dark": "Dunkel", "bw": "Schwarz-Weiß"}[f.theme]
			fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", label, f.w, f.h, idx, f.theme, f.w, f.h, title)
		}
		body.WriteString("</div>\n")
	}
	add("1. Einstellungen: neue Zeile „KI-Assistenten (MCP)“ mit Zustand in Worten", "Nach „Handys“, weil es wie diese ein Zugang von außen ist. Der Hinweis beginnt mit dem Zustand („Aus.“ oder „An.“), damit man ihn sieht, ohne die Seite zu öffnen. Nur für Eltern.", hub, false)
	add("2. MCP aus, erster Besuch: erst erklären, dann der Schalter", "Eltern, die MCP nicht kennen, lesen zuerst in einfachen Worten, wofür es ist, was ein Assistent darf und nie darf, dass Daten an den Anbieter gehen, und finden zwei Links. Der Schalter ist aus; die Adresse erscheint erst bei „An“. Einzige Hauptaktion: Speichern.", firstVisit, false)
	add("3. MCP gerade eingeschaltet, noch kein Token (Box mit eigenem Zertifikat)", "Erfolgszeile sagt den nächsten Schritt. Die Adresse steht jetzt da; bei eigenem Zertifikat der Hinweis mit Link zur Vertrauensseite. Speichern wird zweitrangig, die Hauptaktion ist „Erstes MCP-Token erstellen“. Die Erklärung bleibt offen, bis es ein Token gibt.", justOn, true)
	add("4. MCP an, drei Tokens als ruhige Zeilen", "Jede Zeile: Name, für wen (Marker und Name), was es liest und ändert in Worten (Bereiche beim Namen, keine Zahlen), zuletzt benutzt mit Sekunden. Ein nie benutztes Token sagt, was zu prüfen ist. Die Erklärung ist jetzt unter „Was ist MCP?“ eingeklappt.", withTokens, false)
	add("5. Neues MCP-Token: Name und pro Bereich „Nein / Nur lesen / Lesen und ändern“", "Vorauswahl überall „Nur lesen“: sicher und sofort nützlich. Jeder Bereich sagt in einer Zeile, was dazugehört. Wem das Token gehört, steht als Satz mit Marker; eine Personenwahl gibt es nicht (immer die angemeldete Person, nie ein Kind).", newForm, false)
	add("6. Neues MCP-Token: Fehler am Feld, Eingaben bleiben", "Ohne Namen und mit überall „Nein“: Fehler direkt am Namen und am Bereichsblock (roter Rahmen), alles Gewählte bleibt. Ein schon vergebener Name bekommt denselben Platz.", bad, false)
	add("7. Neues MCP-Token: einmal sichtbar, mit allem, was der Assistent braucht", "Muster aus #111: Erfolgszeile mit Namen, Token in Festschrift (l/1/O/0 unterscheidbar), Kopieren, MCP-Adresse, was es darf, Link zur Anleitung, dann „Fertig“.", strings.ReplaceAll(once, " hidden>", ">"), false)
	add("8. MCP-Token: was es darf, in Worten; Entziehen nur hier", "Fakten: für wen, liest und ändert, liest nur, darf nicht (Bereiche beim Namen), erstellt, zuletzt benutzt mit Sekunden. Rechte sind fest; der Hinweis sagt, wie man sie ändert. Langer Name bricht um.", detail, false)
	add("9. MCP-Token: Entziehen fragt zuerst, nennt das Token", "Roter Rahmen, was passiert und was bleibt, der Name auf dem roten Knopf, „Nein, nichts ändern“. Hier ein Token, das noch nie benutzt wurde.", revokeAsks, false)
	add("10. Nach dem Entziehen: Erfolgszeile nennt das Token", "Zurück auf der Übersicht; die Zeile ist weg.", afterRevoke, false)
	add("11. MCP ausgeschaltet, Tokens bleiben: jede Zeile sagt, dass sie gerade nicht geht", "Ausschalten braucht keine Bestätigung (nichts geht verloren). Die Tokens bleiben gespeichert, jede Zeile hat den Hinweis; „Neues MCP-Token“ gibt es erst wieder bei „An“. Speichern ist wieder die Hauptaktion.", switchedOff, false)
	add("12. MCP-Token, während MCP aus ist", "Der Zustand steht als Hinweis bei den Fakten, mit Link zum Schalter.", tokenWhileOff, false)

	var jsDocs strings.Builder
	for i, h := range docs {
		h = regexp.MustCompile(`(?s)<script src="[^"]*"[^>]*></script>`).ReplaceAllString(h, "")
		h = regexp.MustCompile(`(?s)<link rel="stylesheet"[^>]*>`).ReplaceAllString(h, "")
		h = strings.ReplaceAll(h, " autofocus", "")
		bodyAt := strings.Index(h, "<body")
		inner := h[bodyAt:strings.LastIndex(h, "</body>")]
		fmt.Fprintf(&jsDocs, "<template id=\"doc%d\">%s</template>\n", i, inner)
	}
	doc := `<!doctype html>
<!-- Generated by TestWritePreview96 from the box's real pages and templates (ticket 96, MCP server). The stylesheet, scripts and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="de"><head><meta charset="utf-8"><title>Hejmo preview 96: KI-Assistenten (MCP)</title>
<style>

body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 96 KI-Assistenten (MCP): Vorschau mit der Beispielfamilie: 1024 x 768, 768 x 1024 und 375 px, hell, dunkel und Schwarz-Weiß</h1>
<noscript><p>Diese Seite braucht JavaScript nur, um die echten Seiten in ihre Rahmen zu setzen.</p></noscript>
` + body.String() + `
<template id="css">` + string(css) + `</template>
<template id="js">` + string(formJS) + `</template>
<template id="js2">` + string(copyJS) + `</template>
` + jsDocs.String() + `
<script>
(function () {
  var font = Uint8Array.from(atob("` + base64.StdEncoding.EncodeToString(font) + `"), function (c) { return c.charCodeAt(0); });
  var fontUrl = URL.createObjectURL(new Blob([font], { type: "font/ttf" }));
  var css = document.getElementById("css").innerHTML.replace("/static/fonts/Figtree-wght.ttf", fontUrl).replace(/&amp;/g, "&").replace(/&gt;/g, ">").replace(/&lt;/g, "<");
  var js = document.getElementById("js").textContent;
  document.querySelectorAll("iframe[data-doc]").forEach(function (f) {
    var inner = document.getElementById("doc" + f.getAttribute("data-doc")).innerHTML;
    f.srcdoc = '<!doctype html><html lang="de" data-theme="' + f.getAttribute("data-theme") + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + css + '</style></head>' + inner + '<script>' + js + '<\/script></html>';
  });
})();
</script>
</body></html>
`
	if err := os.WriteFile(out, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}
