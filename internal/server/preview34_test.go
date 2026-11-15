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

// TestWritePreview34 writes the self-contained preview page for the product
// owner's approval of ticket 6.3 (meal plan and cut-off times). Every frame is
// a real page of the box, rendered by the real templates for the example
// family (Mama, Papa, Sebastian, Mia); nothing is hand-written HTML. Each frame
// is an iframe at the real size of a reference display (1024 x 768, and
// portrait 768 x 1024), so the viewport triggers the real media queries.
// Run: HEJMO_WRITE_PREVIEW_34=../../docs/design/preview-34.html go test ./internal/server -run WritePreview34
func TestWritePreview34(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW_34")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_34 to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	js, _ := web.Static.ReadFile("static/form.js")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")

	b, _ := patternBox(t) // Mama (signed in), Papa, Sebastian, Mia; the clock stands at Saturday 14/11/2026
	week := url.Values{"week": {"2026-11-09"}}
	set := func(day, mealtime, text string) { week.Set(mealField(day, mealtime), text) }
	for _, m := range [][3]string{
		{"2026-11-09", "breakfast", "Porridge with apple"}, {"2026-11-09", "dinner", "Spaghetti bolognese"},
		{"2026-11-10", "breakfast", "Muesli"}, {"2026-11-10", "lunch", "Vegetable soup"}, {"2026-11-10", "snack", "Apple slices"}, {"2026-11-10", "dinner", "Pasta bake"},
		{"2026-11-11", "dinner", "Fish fingers and mash"},
		{"2026-11-12", "lunch", "Leftover pasta bake"}, {"2026-11-12", "dinner", "Papa's vegetable curry"},
		{"2026-11-13", "dinner", "Pizza night"},
		{"2026-11-14", "breakfast", "Pancakes (Mia's wish)"}, {"2026-11-14", "lunch", "Leftover curry"}, {"2026-11-14", "snack", "Fruit and yoghurt"}, {"2026-11-14", "dinner", "Sebastian's burgers"},
		{"2026-11-15", "breakfast", "Rolls and eggs"}, {"2026-11-15", "lunch", "Roast chicken"}, {"2026-11-15", "dinner", "Bread and cheese"},
	} {
		set(m[0], m[1], m[2])
	}
	if r := b.post("/meals", week); r.status != http.StatusSeeOther {
		t.Fatalf("plan the week = %d", r.status)
	}
	page := func(path string) string {
		r := getEN(b, path)
		if r.status != http.StatusOK {
			t.Fatalf("%s = %d", path, r.status)
		}
		return r.body
	}
	plan := page("/meals")
	empty := page("/meals?week=2026-11-23") // a week nobody planned yet
	bad := week
	bad.Set(mealField("2026-11-11", "dinner"), strings.Repeat("Fish fingers and mash, ", 12))
	r := b.do(http.MethodPost, "/meals", bad, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("too long = %d", r.status)
	}
	failed := r.body
	child := pairedPhone(t, b, personNamed(t, b, "Mia"), "Mia's phone").get("/meals?week=2026-11-09", "en").body
	settings := page("/settings")

	type frame struct {
		theme string
		w, h  int
	}
	land := func(themes ...string) []frame {
		var fs []frame
		for _, th := range themes {
			fs = append(fs, frame{th, 1024, 768})
		}
		return fs
	}
	port := func(themes ...string) []frame {
		var fs []frame
		for _, th := range themes {
			fs = append(fs, frame{th, 768, 1024})
		}
		return fs
	}
	var body strings.Builder
	var docs []string
	docOf := map[string]int{}
	add := func(title, note, anchor, html string, frames ...frame) {
		idx, ok := docOf[html]
		if !ok {
			idx = len(docs)
			docOf[html] = idx
			docs = append(docs, html)
		}
		fmt.Fprintf(&body, "<h2>%s</h2><p class=\"note\">%s</p><div class=\"row\">\n", title, note)
		for _, f := range frames {
			label := map[string]string{"light": "Light", "dark": "Dark", "bw": "Black and white"}[f.theme]
			shape := "landscape"
			if f.h > f.w {
				shape = "portrait"
			}
			fmt.Fprintf(&body, `<figure><figcaption>%s, %s %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" data-anchor="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", label, shape, f.w, f.h, idx, f.theme, anchor, f.w, f.h, title)
		}
		body.WriteString("</div>\n")
	}
	add("1. Meal plan of the week: a card per day, a field per mealtime",
		"Sample week of the example family. Today (Saturday) has a thick outline and a label. The week is saved with one button; an empty field removes that meal. Every filled field is also an all-day event \"Dinner: Pasta bake\" in the generated, read-only calendar (shared through the box by default).",
		"", plan, append(land("light", "dark", "bw"), port("light", "dark", "bw")...)...)
	add("2. Too long a meal: error at the field, everything typed stays",
		"A meal is one line of at most 200 characters. Nothing is saved until all fields are valid.",
		"", failed, append(land("light", "bw"), port("light")...)...)
	add("3. A week nobody planned yet",
		"A short hint, the same fields, and the same single button.",
		"", empty, land("light", "bw")...)
	add("4. A child's phone or a child's browser: read only",
		"Children see the plan (read), not the fields. Only planned meals are listed.",
		"", child, append(land("light", "bw"), port("light")...)...)
	add("5. Settings: cut-off time per mealtime",
		"From these times a mealtime is no longer shown on displays (ADR 0016). The defaults are breakfast 10:00, lunch 13:30, snack 16:30; dinner has none and shows for the rest of the day. Leave a field empty for none. The page is shown scrolled to the new section.",
		"fieldset.cutoffs", settings, append(land("light", "dark", "bw"), port("light", "bw")...)...)

	var jsDocs strings.Builder
	for i, h := range docs {
		h = regexp.MustCompile(`(?s)<script src="[^"]*"[^>]*></script>`).ReplaceAllString(h, "")
		h = regexp.MustCompile(`(?s)<link rel="[^"]*"[^>]*>`).ReplaceAllString(h, "")
		h = strings.ReplaceAll(h, " autofocus", "") // focus would scroll the frame
		bodyAt := strings.Index(h, "<body")
		inner := h[bodyAt:strings.LastIndex(h, "</body>")]
		fmt.Fprintf(&jsDocs, "<template id=\"doc%d\">%s</template>\n", i, inner)
	}
	page34 := `<!doctype html>
<!-- Generated by TestWritePreview34 from the box's real pages and templates (ticket 6.3). The stylesheet, script and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 6.3 meal plan</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 6.3 shared meal plan: preview with the example family (Mama, Papa, Sebastian, Mia): 1024 x 768 and portrait 768 x 1024, light, dark and black and white</h1>
<noscript><p>This page needs JavaScript only to place the real pages into their frames.</p></noscript>
` + body.String() + `
<template id="css">` + string(css) + `</template>
<template id="js">` + string(js) + `</template>
` + jsDocs.String() + `
<script>
(function () {
  var font = Uint8Array.from(atob("` + base64.StdEncoding.EncodeToString(font) + `"), function (c) { return c.charCodeAt(0); });
  var fontUrl = URL.createObjectURL(new Blob([font], { type: "font/ttf" }));
  var css = document.getElementById("css").innerHTML.replace("/static/fonts/Figtree-wght.ttf", fontUrl).replace(/&amp;/g, "&").replace(/&gt;/g, ">").replace(/&lt;/g, "<");
  var js = document.getElementById("js").textContent;
  document.querySelectorAll("iframe[data-doc]").forEach(function (f) {
    var inner = document.getElementById("doc" + f.getAttribute("data-doc")).innerHTML;
    f.addEventListener("load", function () {
      var a = f.getAttribute("data-anchor");
      var el = a && f.contentDocument.querySelector(a);
      if (el) el.scrollIntoView();
    });
    f.srcdoc = '<!doctype html><html lang="en" data-theme="' + f.getAttribute("data-theme") + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + css + '</style></head>' + inner + '<script>' + js + '<\/script></html>';
  });
})();
</script>
</body></html>
`
	for _, external := range []string{`src="http`, `href="http`, "url(http", "@import"} {
		if strings.Contains(page34, external) {
			t.Fatalf("the preview makes an external request: %s", external)
		}
	}
	if err := os.WriteFile(out, []byte(page34), 0o644); err != nil {
		t.Fatal(err)
	}
}
