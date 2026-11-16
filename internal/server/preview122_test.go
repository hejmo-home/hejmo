package server_test

import (
	"encoding/base64"
	"fmt"
	"html"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/web"
)

// TestWriteDesignPreview122 writes the self-contained preview page for ticket
// 7.x #122 (change a task's star value after creation): the task detail page
// in its states, rendered by a box holding the example family from the real
// templates, with the real stylesheet. It replaces the hand-written preview.
// Run: HEJMO_WRITE_PREVIEW_122=../../docs/design/preview-122.html go test ./internal/server -run WriteDesignPreview122
func TestWriteDesignPreview122(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW_122")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_122 to the output path to write the preview")
	}
	css, err := web.Static.ReadFile("static/hejmo.css")
	if err != nil {
		t.Fatal(err)
	}
	font, err := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")
	if err != nil {
		t.Fatal(err)
	}

	f := newFamilyBox(t, "screen")
	f.now = f.at(15, 10, 0) // Sunday 15/11/2026
	maxi, err := f.db.AddPerson(f.householdID, store.Person{Name: "Maximilian-Alexander", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	f.people["Maximilian-Alexander"] = maxi
	de := map[string]string{"Accept-Language": "de"}
	// post keeps the success line for the next page; the helpers below read a
	// page afterwards so that a line does not leak into another scenario.
	post := func(path string, v url.Values) {
		t.Helper()
		if r := f.post(path, v); r.status != 303 {
			t.Fatalf("POST %s = %d\n%s", path, r.status, r.body)
		}
	}
	consume := func() { f.get("/tasks") }
	add := func(who, title, emoji, repeat, part, stars string, days ...string) string {
		t.Helper()
		v := url.Values{"person": {f.people[who].ID}, "title": {title}, "emoji": {emoji}, "repeat": {repeat}, "part": {part}, "stars": {stars}, "weekdays": days}
		post("/tasks", v)
		consume()
		tasks, err := f.db.Tasks(f.householdID)
		if err != nil {
			t.Fatal(err)
		}
		return tasks[len(tasks)-1].ID
	}
	page := func(status int, path string) string {
		t.Helper()
		r := f.do("GET", path, nil, de)
		if r.status != status {
			t.Fatalf("GET %s = %d\n%s", path, r.status, r.body)
		}
		return frameOf(t, r.body)
	}
	set := func(id, stars string) { post("/tasks/"+id+"/stars", url.Values{"stars": {stars}}) }
	tick := func(id string, done string) { post("/tasks/"+id+"/done", url.Values{"done": {done}}); consume() }

	// Scenarios, each a page of the real detail page.
	bag := add("Mia", "Schultasche packen", "🎒", "daily", "morning", "2")
	def := page(200, "/tasks/"+bag)

	tick(bag, "1")
	set(bag, "3")
	savedTicked := page(200, "/tasks/"+bag) // success line, notice for the 2 stars credited
	tick(bag, "0")
	consume()

	r := f.do("POST", "/tasks/"+bag+"/stars", url.Values{"stars": {"120"}}, de)
	if r.status != 422 {
		t.Fatalf("error page = %d", r.status)
	}
	errPage := frameOf(t, r.body)

	one := add("Mia", "Zähne putzen", "🪥", "daily", "morning", "1")
	tick(one, "1")
	creditedOne := page(200, "/tasks/"+one)

	zero := add("Sebastian", "Zimmer aufräumen", "🧸", "daily", "", "2")
	tick(zero, "1")
	set(zero, "0")
	tick(zero, "0")
	set(zero, "0")
	consume()
	tick(zero, "1") // credited for 0
	creditedZero := page(200, "/tasks/"+zero)

	bins := add("Papa", "Müll rausbringen", "🗑️", "weekly", "evening", "0", "SA")
	set(bins, "0")
	savedAdult := page(200, "/tasks/"+bins) // not due on Sunday, saved as 0

	long := add("Maximilian-Alexander", "Hausaufgabenheft kontrollieren und Schultasche für Donnerstag packen", "📚", "daily", "evening", "3")
	tick(long, "1")
	set(long, "5")
	longPage := page(200, "/tasks/"+long)

	type fig struct {
		caption, theme string
		w, h           int
		doc            string
	}
	var docs []string
	figures := func(figs ...fig) string {
		var b strings.Builder
		b.WriteString(`<div class="row">`)
		for _, g := range figs {
			docs = append(docs, g.doc)
			fmt.Fprintf(&b, `<figure class="frame-fig"><figcaption>%s (%dx%d)</figcaption><iframe data-doc="%d" data-theme="%s" width="%d" height="%d" title="%s"></iframe></figure>`,
				html.EscapeString(g.caption), g.w, g.h, len(docs)-1, g.theme, g.w, g.h, html.EscapeString(g.caption))
		}
		b.WriteString("</div>")
		return b.String()
	}
	themes := []struct{ key, label string }{{"light", "Hell"}, {"dark", "Dunkel"}, {"bw", "Schwarz-Weiß"}}
	three := func(label string, w, h int, doc string) string {
		var fs []fig
		for _, th := range themes {
			fs = append(fs, fig{th.label + ": " + label, th.key, w, h, doc})
		}
		return figures(fs...)
	}

	var body strings.Builder
	body.WriteString(`<h1>Hejmo #122: change a task's star value after creation (rendered from the real templates)</h1>
<p class="note">Every page below is the real detail page of a task, served by a box holding the example family (Mama, Papa, Sebastian, Mia and Maximilian-Alexander for the long name), Sunday 15/11/2026, with the real stylesheet. The page changes the task detail page and fixes the frame width on narrow screens; both wait for the product owner's approval of this layout.</p>`)
	body.WriteString(`<h2>1. Task detail page, landscape 1024x768</h2><p class="note">Today still open, worth 2 stars. Saving is the one primary action; ticking off is secondary. The hint under the field says that credited stars stay.</p>` + three("offen", 1024, 768, def))
	body.WriteString(`<h2>2. Saved, today already ticked off with the old value (1024x768)</h2><p class="note">Ticked off for 2 stars, now set to 3: success line on top, notice under the field.</p>` + three("gespeichert, heute abgehakt", 1024, 768, savedTicked))
	body.WriteString(`<h2>3. Error at the field (1024x768)</h2>` + three("120 ist zu groß", 1024, 768, errPage))
	body.WriteString(`<h2>4. Portrait 768x1024</h2>` + figures(
		fig{"Hell: offen", "light", 768, 1024, def}, fig{"Dunkel: heute mit 1 Stern abgehakt", "dark", 768, 1024, creditedOne}, fig{"Schwarz-Weiß: Fehler", "bw", 768, 1024, errPage}))
	body.WriteString(`<h2>5. Phone 375 px</h2><p class="note">The frame is no wider than the screen; long titles and names wrap.</p>` + figures(
		fig{"Hell: langer Titel und Name, mit 3 abgehakt, jetzt 5", "light", 375, 1180, longPage},
		fig{"Dunkel: Fehler", "dark", 375, 1000, errPage},
		fig{"Schwarz-Weiß: Erwachsenen-Aufgabe auf 0, heute nicht fällig", "bw", 375, 1000, savedAdult},
		fig{"Hell: heute für 0 Sterne abgehakt", "light", 375, 1000, creditedZero}))

	var tpl strings.Builder
	for i, d := range docs {
		fmt.Fprintf(&tpl, `<template id="doc%d">%s</template>`+"\n", i, d)
	}
	doc := `<!doctype html>
<!-- Generated by TestWriteDesignPreview122 from the real templates (ticket #122). The stylesheet and font are inlined copies of web/static and do not follow later changes. -->
<html lang="de"><head><meta charset="utf-8"><title>Hejmo preview #122 star value of a task</title>
<style>body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:24px;margin:0 0 8px}h2{font-size:19px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:1100px;line-height:1.45}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}</style></head><body>
` + body.String() + `
<script type="text/plain" id="css">` + string(css) + `</script>
<script type="text/plain" id="font">` + base64.StdEncoding.EncodeToString(font) + `</script>
` + tpl.String() + `<script>
(function () {
  var font = Uint8Array.from(atob(document.getElementById("font").textContent.trim()), function (c) { return c.charCodeAt(0); });
  var fontUrl = URL.createObjectURL(new Blob([font], { type: "font/ttf" }));
  var css = document.getElementById("css").textContent.replace("/static/fonts/Figtree-wght.ttf", fontUrl);
  document.querySelectorAll("iframe[data-doc]").forEach(function (f) {
    var inner = document.getElementById("doc" + f.getAttribute("data-doc")).innerHTML;
    f.srcdoc = '<!doctype html><html lang="de" data-theme="' + f.getAttribute("data-theme") + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + css + '</style></head><body class="web">' + inner + '</body></html>';
  });
})();
</script>
</body></html>
`
	if err := os.WriteFile(out, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// frameOf cuts the frame (navigation and main) out of a rendered page.
func frameOf(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, `<div class="frame framed`)
	j := strings.LastIndex(page, "</body>")
	if i < 0 || j < 0 {
		t.Fatalf("cannot find the frame in the page:\n%s", page)
	}
	return page[i:j]
}
