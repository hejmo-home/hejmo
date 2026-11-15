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

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/web"
)

// TestWritePreview71 writes the self-contained preview page for the product
// owner's approval of ticket 5.16 (shared form patterns). Every frame is a
// real page of the box, rendered by the real templates for the example family
// (Mama, Papa, Sebastian, Mia); nothing is hand-written HTML. Each frame is an
// iframe, so the viewport widths trigger the real media queries.
// Run: HEJMO_WRITE_PREVIEW=../../docs/design/preview-71.html go test ./internal/server -run WritePreview71
func TestWritePreview71(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	js, _ := web.Static.ReadFile("static/form.js")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")

	b, ids := patternBox(t)
	b.feeds.set("https://example.org/school.ics", feedOf(), nil)
	h, _ := b.db.HomeHousehold()
	_ = ids
	d := newScreen(b)
	b.post("/displays", url.Values{"code": {codeOn(t, d.get("/display", "en").body)}, "name": {"Kitchen"}, "room": {""}, "type": {"eink_bw"}})
	b.post("/calendars", url.Values{"name": {"School holidays"}, "url": {"https://example.org/school.ics"}, "color": {"#2F5E7A"}, "interval": {"60"}, "people": {ids["Sebastian"], ids["Mia"]}})
	for _, tk := range []url.Values{
		{"person": {ids["Mia"]}, "title": {"Brush teeth"}, "emoji": {"🪥"}, "repeat": {"daily"}},
		{"person": {ids["Sebastian"]}, "title": {"Empty the dishwasher"}, "emoji": {"🍽️"}, "repeat": {"weekly"}, "weekdays": {"MO", "TH"}},
	} {
		b.post("/tasks", tk)
	}
	tasks, _ := b.db.Tasks(h.ID)
	_ = store.RoleParent

	page := func(path string) string {
		r := getEN(b, path)
		if r.status != http.StatusOK && r.status != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d", path, r.status)
		}
		return r.body
	}
	// After a saved change: the real redirect carries the success line.
	saved := func(path string, form url.Values, to string) string {
		if r := b.post(path, form); r.status != http.StatusSeeOther {
			t.Fatalf("POST %s = %d", path, r.status)
		}
		return page(to)
	}
	failed := func(path string, form url.Values) string {
		r := b.do(http.MethodPost, path, form, map[string]string{"Accept-Language": "en"})
		if r.status != http.StatusUnprocessableEntity {
			t.Fatalf("POST %s = %d", path, r.status)
		}
		return r.body
	}
	tasksSaved := saved("/tasks", url.Values{"person": {ids["Mia"]}, "title": {"Read a book"}, "emoji": {"📖"}, "repeat": {"daily"}}, "/tasks")
	tasksError := failed("/tasks", url.Values{"person": {ids["Sebastian"]}, "title": {""}, "emoji": {"🎒"}, "repeat": {"weekly"}})
	calendarsError := failed("/calendars", url.Values{"name": {"Swimming"}, "url": {"not an address"}, "color": {"#4E7D52"}, "interval": {"60"}, "people": {ids["Mia"]}})
	settingsSaved := saved("/settings/household", url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}}, "/settings/household")
	displaysSaved := saved("/displays/"+firstDisplay(t, b)+"/settings", url.Values{"show_tomorrow": {"on"}}, "/displays")
	detail := strings.Replace(page("/tasks/"+tasks[0].ID), `<details class="danger">`, `<details class="danger" open>`, 1)

	type frame struct {
		theme string
		w, h  int
	}
	land := func(themes ...string) []frame {
		var fs []frame
		for _, th := range themes {
			fs = append(fs, frame{th, 1280, 800})
		}
		return fs
	}
	portrait := frame{"light", 768, 1024}
	phoneLight, phoneDark := frame{"light", 375, 812}, frame{"dark", 375, 812}
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
		// Design gate: every page is shown at 1280 px and at 375 px, light and dark.
		frames = append(frames, phoneLight, phoneDark)
		if title != "" {
			fmt.Fprintf(&body, "<h2>%s</h2><p class=\"note\">%s</p>", title, note)
		}
		body.WriteString("<div class=\"row\">\n")
		for _, f := range frames {
			label := map[string]string{"light": "Light", "dark": "Dark", "bw": "Black and white"}[f.theme]
			fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" data-anchor="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", label, f.w, f.h, idx, f.theme, anchor, f.w, f.h, title)
		}
		body.WriteString("</div>\n")
	}
	tasksForm := page("/tasks")
	calendarsForm := page("/calendars")
	displaysPage := page("/displays")
	fresh := signedInBox(t)
	emptyTasks := getEN(fresh, "/tasks").body
	emptyDisplays := getEN(fresh, "/displays").body
	add("1. Tasks: success line after a saved change, calm rows",
		"After adding a task the page shows the success line (once). Rows open a detail page; the only action on a row is the everyday one, marking it done. There is no delete button on the list.",
		"", tasksSaved, land("light", "dark", "bw")...)
	add("2. New task: person chips (one choice), segmented control, weekday chips, More options",
		"The person is one chip with the person marker; how often is a segmented control; the date or the weekdays appear only when they apply; the emoji sits under More options. In black and white the marker is a shape and a selected chip has a thick border and a tick.",
		"#new", tasksForm, append(land("light", "dark", "bw"), portrait)...)
	add("3. Error at the field, entries stay filled in",
		"No title: the error is under the title and the field is marked (it takes the focus in the real page). Sebastian, weekly and the emoji stay as typed; More options opens by itself when the error is inside it.",
		"#new", tasksError, land("light", "bw")...)
	add("4. Calendars: Whole family chip is selected by default",
		"\"Whole family\" stores no audience, so people added later are included; it works without JavaScript. Colour and update interval sit under More options.",
		"#add", calendarsForm, land("light", "dark", "bw")...)
	add("5. Calendars: error at the address, a person chosen",
		"Mia is chosen, so Whole family looks unselected (the server then ignores it). The bad address shows its error under the field; name and Mia stay filled in.",
		"#add", calendarsError, land("light", "bw")...)
	add("6. Settings: segmented controls and success line",
		"Time format and temperature unit are segmented controls; saving shows the success line.",
		"", settingsSaved, land("light", "dark", "bw")...)
	add("7. Displays: switch with its state in words",
		"The tomorrow block is a switch with the state written under the name (it follows the switch without JavaScript in current browsers).",
		"", displaysSaved, land("light", "dark", "bw")...)
	add("8. Displays: choice cards for the display type",
		"The display type is a choice card with a one-line explanation.",
		"#new", displaysPage, land("light", "bw")...)
	add("9. Task detail: deleting behind a confirmation",
		"The danger disclosure is closed by default; it explains what happens and the red button confirms (shown open here). Revoking a token, removing a calendar and unpairing a display use the same confirmation on their lists.",
		"", detail, land("light", "bw")...)
	add("10. Empty states: what it is for, then the first action",
		"Tasks and displays on a fresh box.",
		"", emptyTasks, land("light", "bw")...)
	add("", "", "", emptyDisplays, land("light", "bw")...)

	var jsDocs strings.Builder
	for i, h := range docs {
		h = regexp.MustCompile(`(?s)<script src="[^"]*"[^>]*></script>`).ReplaceAllString(h, "")
		h = regexp.MustCompile(`(?s)<link rel="stylesheet"[^>]*>`).ReplaceAllString(h, "")
		h = strings.ReplaceAll(h, " autofocus", "") // focus would scroll the frame
		bodyAt := strings.Index(h, "<body")
		inner := h[bodyAt:strings.LastIndex(h, "</body>")]
		fmt.Fprintf(&jsDocs, "<template id=\"doc%d\">%s</template>\n", i, inner)
	}
	page71 := `<!doctype html>
<!-- Generated by TestWritePreview71 from the box's real pages and templates (ticket 5.16). The stylesheet, script and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 5.16 shared form patterns</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 5.16 shared form patterns: preview with the example family (Mama, Papa, Sebastian, Mia): 1280 px and 375 px, light and dark</h1>
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
	if err := os.WriteFile(out, []byte(page71), 0o644); err != nil {
		t.Fatal(err)
	}
}

func firstDisplay(t *testing.T, b *box) string {
	t.Helper()
	h, _ := b.db.HomeHousehold()
	ds, err := b.db.Displays(h.ID)
	if err != nil || len(ds) == 0 {
		t.Fatalf("no display: %v", err)
	}
	return ds[0].ID
}
