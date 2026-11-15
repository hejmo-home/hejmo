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

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/web"
)

// TestWritePreview108 writes the self-contained preview page for ticket 7.x
// (signed-in devices, issue #108). Every frame is a real page of the box,
// rendered by the real templates for the example family; nothing is
// hand-written HTML. Each frame is an iframe, so the viewport sizes trigger
// the real media queries.
// Run: HEJMO_WRITE_PREVIEW=../../docs/design/preview-108.html go test ./internal/server -run WritePreview108
func TestWritePreview108(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")
	utc := func(m, d, h, min int) time.Time { return time.Date(2026, time.Month(m), d, h, min, 0, 0, time.UTC) }

	s := newSessionsBox(t)
	// The family's browsers: Papa also has a Windows PC with Edge.
	mustSession(t, s.db, "papa-windows", s.papaPerson.ID, "windows|edge", utc(10, 20, 8, 0), utc(12, 31, 0, 0))
	s.db.TouchSession("papa-windows", utc(11, 13, 17, 20))
	s.now = utc(11, 14, 12, 0)
	s.mama.get("/", "de") // Mama's browser is in use right now

	page := func(br *browser, path, lang string) string {
		t.Helper()
		r := br.get(path, lang)
		if r.status != http.StatusOK {
			t.Fatalf("%s = %d\n%s", path, r.status, r.body)
		}
		return r.body
	}
	open := func(h string) string {
		return strings.Replace(h, `<details class="danger">`, `<details class="danger" open>`, 1)
	}
	androidID := s.idOf(t, "android_phone|chrome")
	ownID := s.idOf(t, "mac|firefox")

	hub := page(s.mama, "/settings", "de")
	list := page(s.mama, "/sessions", "de")
	detail := page(s.mama, "/sessions/"+androidID, "de")
	own := page(s.mama, "/sessions/"+ownID, "de")
	listEN := page(s.mama, "/sessions", "en")
	detailEN := open(page(s.mama, "/sessions/"+androidID, "en"))

	// Mama ends Papa's Android phone: the real redirect carries the success line.
	if r := s.mama.post("/sessions/"+androidID+"/end", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("end = %d", r.status)
	}
	ended := page(s.mama, "/sessions", "de")
	gone := page(s.mama, "/sessions?gone=1", "de")
	refused := page(s.papa, "/login", "de") // the lost phone's next request, as it lands on the sign-in page
	if r := s.papa.get("/", "de"); r.status != http.StatusSeeOther {
		t.Fatalf("refused request = %d", r.status)
	}

	// Many sign-ins, a long name and a session from before the box kept devices.
	many := newSessionsBox(t)
	mustSession(t, many.db, "many-papa-win", many.papaPerson.ID, "windows|edge", utc(10, 20, 8, 0), utc(12, 31, 0, 0))
	many.db.TouchSession("many-papa-win", utc(11, 13, 17, 20))
	h, _ := many.db.HomeHousehold()
	sebastian, err := many.db.AddPerson(h.ID, store.Person{Name: "Sebastian", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ hash, who, device string }{
		{"m-ipad", "mama", "ipad|safari"},
		{"m-unknown", "mama", ""},
		{"p-tablet", "papa", "android_tablet|samsung"},
		{"p-linux", "papa", "linux|firefox"},
		{"s-chromebook", "sebastian", "chromebook|chrome"},
	} {
		person := map[string]string{"mama": mamaID(t, many.box), "papa": many.papaPerson.ID, "sebastian": sebastian.ID}[x.who]
		created := utc(10, 16, 8, 0)
		if x.device != "" {
			created = utc(11, 4, 9, 30)
		}
		mustSession(t, many.db, x.hash, person, x.device, created, utc(12, 31, 0, 0))
	}
	many.db.TouchSession("m-ipad", utc(11, 9, 20, 41))
	many.db.TouchSession("p-tablet", utc(11, 14, 5, 58))
	many.db.TouchSession("p-linux", utc(11, 1, 9, 3))
	many.db.TouchSession("s-chromebook", utc(11, 14, 12, 15))
	if _, err := many.db.SQL().Exec(`UPDATE sessions SET last_seen_at = NULL, device = '' WHERE token_hash = 'm-unknown'`); err != nil {
		t.Fatal(err)
	}
	if _, err := many.db.SQL().Exec(`UPDATE people SET name = 'Maximilian-Konstantin Wittelsbacher-Oberhuber' WHERE id = ?`, many.papaPerson.ID); err != nil {
		t.Fatal(err)
	}
	many.now = utc(11, 14, 12, 0)
	manyList := page(many.mama, "/sessions", "de")

	// Only this browser.
	solo, secret := configuredBox(t)
	fresh(t, solo)
	soloBrowser := newBrowser(solo, uaMacFirefox)
	soloBrowser.signIn(t, "mama", mamaPassword, secret, utc(11, 2, 7, 14))
	solo.now = utc(11, 14, 12, 0)
	only := page(soloBrowser, "/sessions", "de")

	type frame struct {
		theme string
		w, h  int
	}
	desk := func(themes ...string) []frame {
		var fs []frame
		for _, th := range themes {
			fs = append(fs, frame{th, 1024, 768})
		}
		return fs
	}
	portrait, phone, phoneDark := frame{"light", 768, 1024}, frame{"light", 375, 812}, frame{"dark", 375, 812}
	var body strings.Builder
	var docs []string
	docOf := map[string]int{}
	section := func(title, note, html string, frames ...frame) {
		idx, ok := docOf[html]
		if !ok {
			idx = len(docs)
			docOf[html] = idx
			docs = append(docs, html)
		}
		fmt.Fprintf(&body, "<h2>%s</h2><p class=\"note\">%s</p><div class=\"row\">\n", title, note)
		for _, f := range frames {
			label := map[string]string{"light": "Light", "dark": "Dark", "bw": "Black and white"}[f.theme]
			fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", label, f.w, f.h, idx, f.theme, f.w, f.h, title)
		}
		body.WriteString("</div>\n")
	}
	section("1. Settings: new row &quot;Signed-in devices&quot;", "A parent finds the list where every other setup page lives: one calm row under Settings, next to one-time codes (account security). Only parents see it.", hub, append(desk("light", "bw"), phone)...)
	section("2. Signed-in devices: the list", "Grouped by person (marker and name, household order). Each row says device and browser in words, when it was last used and since when it is signed in. The browser you are using carries the word tag. Rows open a detail page; nothing is ended from the list. Paired phones are not duplicated here: one line points to Phones.", list,
		append(desk("light", "dark", "bw"), portrait, phone, phoneDark)...)
	section("3. Detail page of another browser (confirmation closed)", "Facts in words, one action. The destructive action is a closed disclosure, as on every detail page (ADR 0017).", detail, append(desk("light", "bw"), phone)...)
	section("4. Detail page: confirmation open", "The confirmation says what happens (signed out at once, sign-in page next time, password and code needed) and what stays (paired phones, calendar apps). The red button is the confirmation.", open(detail),
		append(desk("light", "dark", "bw"), portrait, phone)...)
	section("5. After ending: success line", "Back on the list, the success line names device and person; the row is gone.", ended, append(desk("light", "bw"), phone)...)
	section("6. Detail page of this browser", "You cannot end your own sign-in here; the page says so and offers Abmelden, the existing sign-out.", own, desk("light")[0], phone)
	section("7. Edge: only this browser is signed in", "The list is never empty (you are signed in); one sentence says what will appear here.", only, desk("light")[0], phone)
	section("8. Edge: already ended or expired", "Another parent ended it first, or it expired while the page was open: a calm notice, no error.", gone, desk("light")[0], phone)
	section("9. Edge: many sign-ins, long name, unknown device", "Long names wrap; sign-ins made before this ticket show &quot;Unbekanntes Gerät&quot; and &quot;noch nicht erfasst&quot;. No counts, every sign-in is a row.", manyList, append(desk("light", "bw"), portrait, phone)...)
	section("10. The lost phone: next request", "The ended browser is refused and lands on the sign-in page with one calm sentence (shown whenever a session cookie came along that the box no longer knows).", refused, phone, desk("bw")[0])
	section("11. English", "Same screens in English.", listEN, desk("light")[0], phone)
	section("12. English confirmation", "", detailEN, phone)

	var jsDocs strings.Builder
	for i, h := range docs {
		h = regexp.MustCompile(`(?s)<script src="[^"]*"[^>]*></script>`).ReplaceAllString(h, "")
		h = regexp.MustCompile(`(?s)<link rel="stylesheet"[^>]*>`).ReplaceAllString(h, "")
		bodyAt := strings.Index(h, "<body")
		inner := h[bodyAt:strings.LastIndex(h, "</body>")]
		lang := "de"
		if strings.Contains(h, `<html lang="en"`) {
			lang = "en"
		}
		fmt.Fprintf(&jsDocs, "<template id=\"doc%d\" data-lang=\"%s\">%s</template>\n", i, lang, inner)
	}
	pageHTML := `<!doctype html>
<!-- Generated by TestWritePreview108 from the box's real pages and templates (issue #108). The stylesheet and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="de"><head><meta charset="utf-8"><title>Hejmo preview #108 Signed-in devices</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo #108 Signed-in devices: a parent ends another browser's sign-in. Example family Mama, Papa, Sebastian, Mia; 1024 x 768, 768 x 1024 and 375 x 812; light, dark and black and white</h1>
<noscript><p>This page needs JavaScript only to place the real pages into their frames.</p></noscript>
` + body.String() + `
<template id="css">` + string(css) + `</template>
` + jsDocs.String() + `
<script>
(function () {
  var font = Uint8Array.from(atob("` + base64.StdEncoding.EncodeToString(font) + `"), function (c) { return c.charCodeAt(0); });
  var fontUrl = URL.createObjectURL(new Blob([font], { type: "font/ttf" }));
  var css = document.getElementById("css").innerHTML.replace("/static/fonts/Figtree-wght.ttf", fontUrl).replace(/&amp;/g, "&").replace(/&gt;/g, ">").replace(/&lt;/g, "<");
  document.querySelectorAll("iframe[data-doc]").forEach(function (f) {
    var doc = document.getElementById("doc" + f.getAttribute("data-doc"));
    f.srcdoc = '<!doctype html><html lang="' + doc.getAttribute("data-lang") + '" data-theme="' + f.getAttribute("data-theme") + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + css + '</style></head>' + doc.innerHTML + '</html>';
  });
})();
</script>
</body></html>
`
	if err := os.WriteFile(out, []byte(pageHTML), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustSession(t *testing.T, db *store.DB, hash, personID, device string, created, expires time.Time) {
	t.Helper()
	if err := db.CreateSession(hash, personID, device, created, expires); err != nil {
		t.Fatal(err)
	}
}
