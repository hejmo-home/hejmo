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
// owner's approval of ticket 6.3 (meal plan and cut-off times), in its design
// pass: the generated page has the sections of the committed
// docs/design/preview-34.html (parent week, German on a personal device, error, error
// scrolled to the field, empty week, child, Settings > Household, time picker
// open). Every frame is a real page of the box, rendered by the real templates
// for the example family (Mama, Papa, Sebastian, Mia). Each frame is an iframe
// at the real size of a reference screen (1024 x 768, portrait 768 x 1024 and
// phone 375 x 812), so the viewport triggers the real media queries, and
// carries the language of its page (data-lang).
// The committed file was edited by hand after it was generated, so do not
// overwrite it; write to a temporary path and compare:
// HEJMO_WRITE_PREVIEW_34=/tmp/preview-34.html go test ./internal/server -run WritePreview34
func TestWritePreview34(t *testing.T) {
	t.Parallel()
	out := os.Getenv("HEJMO_WRITE_PREVIEW_34")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_34 to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	js, _ := web.Static.ReadFile("static/form.js")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")

	// planWeek signs in Mama on a box whose clock stands at Saturday 14/11/2026
	// and plans the week of 09/11 with the given dishes (day, mealtime, text).
	planWeek := func(dishes [][3]string) (*box, url.Values) {
		b, _ := patternBox(t)
		week := url.Values{"week": {"2026-11-09"}}
		for _, m := range dishes {
			week.Set(mealField(m[0], m[1]), m[2])
		}
		if r := b.post("/meals", week); r.status != http.StatusSeeOther {
			t.Fatalf("plan the week = %d", r.status)
		}
		return b, week
	}
	b, week := planWeek([][3]string{
		{"2026-11-09", "breakfast", "Porridge with apple"}, {"2026-11-09", "dinner", "Spaghetti bolognese"},
		{"2026-11-10", "breakfast", "Muesli"}, {"2026-11-10", "lunch", "Vegetable soup"}, {"2026-11-10", "snack", "Apple slices"}, {"2026-11-10", "dinner", "Pasta bake"},
		{"2026-11-11", "dinner", "Fish fingers and mash"},
		{"2026-11-12", "lunch", "Leftover pasta bake"}, {"2026-11-12", "dinner", "Papa's vegetable curry with rice, naan bread and Mia's mango chutney"},
		{"2026-11-14", "breakfast", "Pancakes (Mia's wish)"}, {"2026-11-14", "lunch", "Leftover curry"}, {"2026-11-14", "snack", "Fruit and yoghurt"}, {"2026-11-14", "dinner", "Sebastian's burgers"},
		{"2026-11-15", "breakfast", "Rolls and eggs"}, {"2026-11-15", "lunch", "Roast chicken with potatoes"}, {"2026-11-15", "dinner", "Bread and cheese"},
	})
	german, _ := planWeek([][3]string{
		{"2026-11-09", "breakfast", "Haferbrei mit Apfel"}, {"2026-11-09", "dinner", "Spaghetti Bolognese mit Parmesan und grünem Salat"},
		{"2026-11-10", "breakfast", "Müsli"}, {"2026-11-10", "lunch", "Kartoffel-Lauch-Suppe"}, {"2026-11-10", "snack", "Apfelschnitze"}, {"2026-11-10", "dinner", "Nudelauflauf"},
		{"2026-11-11", "dinner", "Fischstäbchen mit Kartoffelbrei"},
		{"2026-11-12", "lunch", "Reste vom Nudelauflauf"}, {"2026-11-12", "dinner", "Papas Gemüsecurry mit Reis"},
		{"2026-11-14", "breakfast", "Pfannkuchen (Mias Wunsch)"}, {"2026-11-14", "lunch", "Reste vom Curry"}, {"2026-11-14", "snack", "Obst und Joghurt"}, {"2026-11-14", "dinner", "Sebastians Burger"},
		{"2026-11-15", "breakfast", "Semmeln und Eier"}, {"2026-11-15", "lunch", "Brathähnchen mit Kartoffeln"}, {"2026-11-15", "dinner", "Brotzeit"},
	})
	page := func(b *box, lang, path string) string {
		r := b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang})
		if r.status != http.StatusOK {
			t.Fatalf("%s = %d", path, r.status)
		}
		return r.body
	}
	plan := page(b, "en", "/meals?week=2026-11-09")
	failedPlan := url.Values{}
	for k, v := range week {
		failedPlan[k] = v
	}
	failedPlan.Set(mealField("2026-11-11", "dinner"), "Fish fingers and mash with peas, carrots, a big green salad, lemon wedges, tartar sauce and ketchup for everyone who wants some, plus the rest of yesterday's pasta bake for Papa, and ice cream for dessert if the weather is nice")
	r := b.do(http.MethodPost, "/meals", failedPlan, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("too long = %d", r.status)
	}
	failed := r.body
	empty := page(b, "en", "/meals?week=2026-11-23") // a week nobody planned yet
	child := pairedPersonalDevice(t, b, personNamed(t, b, "Mia"), "Mia's tablet").get("/meals?week=2026-11-09", "en").body
	// The household page as it looks after a save (with its own success line), and
	// as it looks when opened (for the time picker).
	if r := b.post("/settings/household", url.Values{"time_format": {"24h"}, "temperature_unit": {"c"},
		"cutoff_breakfast": {"10:00"}, "cutoff_breakfast_on": {"on"}, "cutoff_lunch": {"13:30"}, "cutoff_lunch_on": {"on"},
		"cutoff_snack": {"16:30"}, "cutoff_snack_on": {"on"}, "cutoff_dinner": {"20:00"}}); r.status != http.StatusSeeOther {
		t.Fatalf("save the household = %d", r.status)
	}
	settings := page(b, "en", "/settings/household")
	pickerPage := page(b, "en", "/settings/household")
	page(german, "de", "/") // shows (and so clears) the success line of planning the week
	germanPlan := page(german, "de", "/meals?week=2026-11-09")

	type frame struct {
		theme, lang string
		w, h        int
	}
	size := func(w, h int, themes ...string) []frame {
		var fs []frame
		for _, th := range themes {
			fs = append(fs, frame{th, "en", w, h})
		}
		return fs
	}
	land := func(themes ...string) []frame { return size(1024, 768, themes...) }
	port := func(themes ...string) []frame { return size(768, 1024, themes...) }
	personalDevice := func(themes ...string) []frame { return size(375, 812, themes...) }
	german34 := func(fs []frame) []frame {
		for i := range fs {
			fs[i].lang = "de"
		}
		return fs
	}
	// The documents are numbered in this order, whatever the order of the sections.
	docs := []string{plan, failed, empty, child, settings, pickerPage, germanPlan}
	const planDoc, failedDoc, emptyDoc, childDoc, settingsDoc, pickerDoc, germanDoc = 0, 1, 2, 3, 4, 5, 6
	var body strings.Builder
	add := func(title, note, anchor, open string, doc int, frames ...frame) {
		fmt.Fprintf(&body, "<h2>%s</h2><p class=\"note\">%s</p><div class=\"row\">\n", escapeHTML(title), escapeHTML(note))
		for _, f := range frames {
			label := map[string]string{"light": "Light", "dark": "Dark", "bw": "Black and white"}[f.theme]
			shape := "landscape"
			switch {
			case f.w == 375:
				shape = "personal_device"
			case f.h > f.w:
				shape = "portrait"
			}
			if f.lang == "de" {
				shape += " (German)"
			}
			openAttr := ""
			if open != "" {
				openAttr = fmt.Sprintf(` data-open="%s"`, open)
			}
			fmt.Fprintf(&body, `<figure><figcaption>%s, %s %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" data-lang="%s" data-anchor="%s"%s width="%d" height="%d" title="%s"></iframe></figure>`+"\n",
				label, shape, f.w, f.h, doc, f.theme, f.lang, anchor, openAttr, f.w, f.h, escapeHTML(title))
		}
		body.WriteString("</div>\n")
	}
	cat := func(lists ...[]frame) []frame {
		var all []frame
		for _, l := range lists {
			all = append(all, l...)
		}
		return all
	}
	add("1. Meal plan of the week (parent): a card per day, a row per mealtime, one save button that stays in reach",
		"Sample week of the example family. Each row is the mealtime in words with a small icon, then the dish; a long dish wraps instead of being cut off (Thursday). Today (Saturday) has a thick outline and the label \"Today\". The week heading names the calendar week. \"Save week 46\" sits in a bar at the bottom of the screen while the days scroll, and says \"Changes not saved yet\" once something is typed. Enter saves. An empty field removes that meal. Every filled field is also an all-day event \"Dinner: Pasta bake\" in the generated, read-only meal-plan calendar.",
		"", "", planDoc, cat(land("light", "dark", "bw"), port("light", "dark", "bw"), personalDevice("light", "bw"))...)
	add("2. German on a personal device: long words and dishes",
		"The same week in German at 375 px: \"Mittagessen\" and \"Abendessen\" fit the label column; inside a narrow card the mealtime moves above the dish.",
		"", "", germanDoc, cat(german34(personalDevice("light")), german34(port("light")))...)
	add("3. Too long a meal: error at the field, everything typed stays",
		"A meal is one line of at most 200 characters (the browser already stops typing at 200; this is what the box answers to longer text). Nothing is saved. A line above the week says so and links to the field, because the field may be far down; the field itself shows the error and keeps the whole text.",
		"", "", failedDoc, cat(land("light", "bw"), port("light"))...)
	add("3b. The same error, scrolled to the field",
		"In the box the field with the error takes the focus (autofocus), so the page opens scrolled to it. The field shows the whole text, wrapped, and the message under it.",
		"#meal_2026-11-11_dinner", "", failedDoc, cat(land("light"), port("bw"), personalDevice("light"))...)
	add("4. A week nobody planned yet",
		"One sentence says what to do; the same fields and the same single button. \"Back to this week\" appears whenever another week is shown.",
		"", "", emptyDoc, land("light", "bw")...)
	add("5. A child (personal device or browser): read only",
		"Children see the plan, not the fields: the dish is large, the mealtime has a word and an icon, an unplanned mealtime is left out, and a day without anything says \"Nothing planned.\" The navigation has no Calendar and no Settings for children.",
		"", "", childDoc, cat(land("light", "bw"), port("light"), personalDevice("light"))...)
	add("6. Settings > Household: when meals disappear from displays",
		"One switch row per mealtime with its state in words (ADR 0017). On: \"Disappears from displays at a set time\" and the time in a capsule. Off: \"Shown all day\" and no time field. Defaults: breakfast 10:00, lunch 13:30, snack 16:30 on; dinner off (ADR 0016). The page is shown scrolled to the section. The \"Back to Settings\" link now sits above the content, not beside the navigation.",
		"fieldset.cutoffs", "", settingsDoc, cat(land("light", "dark", "bw"), port("light", "bw"), personalDevice("light"))...)
	add("7. Time picker on a computer and on displays (shared for every time field)",
		"Tapping the capsule opens a panel below it: the chosen time large, hours 00 to 23 and minutes in 5-minute steps as tap targets, \"Done\" closes it. No wheel and no scrolling, so it works with a mouse and on E-Ink. Typing into the capsule still works. On iPhone, iPad and Android the capsule is the system time field and opens the system picker (iOS wheel), so nothing is shown here for them. With the 12-hour format the hours are 1 to 12 with an AM / PM segmented control below.",
		"fieldset.cutoffs", `input[name=cutoff_breakfast]`, pickerDoc, cat(land("light", "bw"), port("bw"))...)

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
<!-- Design pass on ticket 6.3. Generated by TestWritePreview34 from the box's real pages and templates. The stylesheet, script and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 6.3 meal plan</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 6.3 shared meal plan: design pass with the example family (Mama, Papa, Sebastian, Mia): 1024 x 768, portrait 768 x 1024 and phone 375 x 812, light, dark and black and white</h1>
<noscript><p>This page needs JavaScript only to place the pages into their frames.</p></noscript>
` + body.String() + `
<template id="css">` + string(css) + `</template>
<template id="js">` + string(js) + `</template>
` + jsDocs.String() + `
<script>
(function () {
  var font = Uint8Array.from(atob("` + base64.StdEncoding.EncodeToString(font) + `"), function (c) { return c.charCodeAt(0); });
  var fontUrl = URL.createObjectURL(new Blob([font], { type: "font/ttf" }));
  var css = document.getElementById("css").innerHTML.replace("/static/fonts/Figtree-wght.ttf", fontUrl).replace(/&amp;/g, "&").replace(/&gt;/g, ">").replace(/&lt;/g, "<");
  var js = document.getElementById("js").innerHTML.replace(/&amp;/g, "&").replace(/&gt;/g, ">").replace(/&lt;/g, "<");
  document.querySelectorAll("iframe[data-doc]").forEach(function (f) {
    var inner = document.getElementById("doc" + f.getAttribute("data-doc")).innerHTML;
    f.addEventListener("load", function () {
      var a = f.getAttribute("data-anchor");
      var o = f.getAttribute("data-open");
      var cap = o && f.contentDocument.querySelector(o);
      if (cap) cap.click(); // the time picker, opened as a click on the capsule does
      var el = a && f.contentDocument.querySelector(a);
      if (el) el.scrollIntoView();
    });
    f.srcdoc = '<!doctype html><html lang="' + (f.getAttribute("data-lang") || "en") + '" data-theme="' + f.getAttribute("data-theme") + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + css + '</style></head>' + inner + '<script>' + js + '<\/script></html>';
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

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func escapeHTML(s string) string { return htmlEscaper.Replace(s) }
