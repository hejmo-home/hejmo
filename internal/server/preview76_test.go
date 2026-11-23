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

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/web"
)

// TestWritePreview76 writes the self-contained preview page for the product
// owner's approval of ticket 5.21 (people in the web interface). Every frame
// is a real page of the box, rendered by the real templates for the example
// family (Mama, Papa, Sebastian, Mia and four more children: eight people, the household maximum); nothing is hand-written HTML. Each frame
// is an iframe, so the viewport sizes trigger the real media queries.
// Run: HEJMO_WRITE_PREVIEW=../../docs/design/preview-76.html go test ./internal/server -run WritePreview76
func TestWritePreview76(t *testing.T) {
	t.Parallel()
	out := os.Getenv("HEJMO_WRITE_PREVIEW")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	js, _ := web.Static.ReadFile("static/form.js")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")

	p := newPeopleBox(t)
	p.add(t, "Papa", "parent")
	p.add(t, "Sebastian", "child")
	mia := p.add(t, "Mia", "child")
	var more []string
	for _, name := range []string{"Jonas", "Lotta", "Ben", "Emma"} {
		more = append(more, p.add(t, name, "child")["id"].(string))
	}
	sebastian := p.byName(t, "Sebastian")["id"].(string)
	// The family strip shows markers of whoever an event is for.
	if r := p.post("/groups", url.Values{"name": {"School children"}, "people": {sebastian, mia["id"].(string)}}); r.status != http.StatusSeeOther {
		t.Fatalf("group = %d", r.status)
	}

	page := func(path string) string {
		r := p.en(path)
		if r.status != http.StatusOK {
			t.Fatalf("%s = %d", path, r.status)
		}
		return r.body
	}
	open := func(h string) string {
		return strings.Replace(h, `<details class="more">`, `<details class="more" open>`, 1)
	}
	// After a saved change the real redirect carries the success line.
	if r := p.post("/people/"+mia["id"].(string)+"/move", url.Values{"direction": {"up"}}); r.status != http.StatusSeeOther {
		t.Fatalf("move = %d", r.status)
	}
	overview := page("/people")
	addForm := open(overview)
	edit := page("/people/" + mia["id"].(string))
	papa := p.byName(t, "Papa")
	clash := p.do(http.MethodPost, "/people/"+mia["id"].(string), url.Values{"name": {"Mia"}, "color": {papa["color"].(string)}, "shape": {papa["shape"].(string)}}, map[string]string{"Accept-Language": "en"})
	if clash.status != http.StatusUnprocessableEntity {
		t.Fatalf("clash = %d", clash.status)
	}
	groupPage := page("/groups/new")
	h, _ := p.db.HomeHousehold()
	cal, _ := p.db.FamilyCalendar(h.ID)
	day := func(hour, min int) time.Time { return time.Date(2026, 11, 14, hour, min, 0, 0, berlinTZ) }
	for _, e := range []calendar.Event{
		{Title: "Swimming", Start: day(16, 0), End: day(17, 0), People: []string{mia["id"].(string)}},
		{Title: "Football", Start: day(15, 30), End: day(17, 0), People: []string{sebastian}},
		{Title: "Parents' evening", Start: day(19, 0), End: day(21, 0), People: []string{p.byName(t, "Mama")["id"].(string), papa["id"].(string)}},
		{Title: "Visit to Oma", Start: day(0, 0), End: day(0, 0).AddDate(0, 0, 1), AllDay: true, People: []string{mia["id"].(string), sebastian}},
		{Title: "Birthday party", Start: day(14, 0), End: day(15, 0), People: []string{p.byName(t, "Mama")["id"].(string), papa["id"].(string), sebastian, mia["id"].(string), more[0], more[1], more[2], more[3]}},
		{Title: "Choir", Start: day(17, 30), End: day(18, 30), People: []string{more[0], more[2]}},
		{Title: "Judo", Start: day(16, 0), End: day(17, 0), People: []string{more[1], more[3]}},
	} {
		e.CalendarID = cal.ID
		if _, err := p.db.CreateEvent(h.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	family := p.display.get("/display", "en").body
	if n := strings.Count(family, `<section class="column"`); n != 8 {
		t.Fatalf("%d columns on the display, want 8", n)
	}

	// Ben wears Mama's colour (his marker, the plus, is free with it) and Emma
	// Mama's marker (her colour, raspberry, is free with it): the edit pages show
	// taken options faded and the pairs that would be refused disabled. Taken
	// after the family view, so the displays keep the example family.
	ben, emma := more[2], more[3]
	mama := p.byName(t, "Mama")
	for _, x := range []struct{ id, name, color, shape string }{
		{ben, "Ben", mama["color"].(string), p.byName(t, "Ben")["shape"].(string)},
		{emma, "Emma", p.byName(t, "Emma")["color"].(string), mama["shape"].(string)},
	} {
		if r := p.save(x.id, url.Values{"name": {x.name}, "color": {x.color}, "shape": {x.shape}}); r.status != http.StatusSeeOther {
			t.Fatalf("save %s = %d\n%s", x.name, r.status, r.body)
		}
	}
	p.en("/people") // shows the "Person saved" line once
	benPage, emmaPage := page("/people/"+ben), page("/people/"+emma)

	type frame struct {
		theme string
		w, h  int
		focus string // selector of the choice that shows its tooltip, as it does on keyboard focus
	}
	desk := func(themes ...string) []frame {
		var fs []frame
		for _, th := range themes {
			fs = append(fs, frame{th, 1024, 768, ""})
		}
		return fs
	}
	portrait := []frame{{"light", 768, 1024, ""}, {"bw", 768, 1024, ""}}
	phone := []frame{{"light", 375, 812, ""}, {"dark", 375, 812, ""}}
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
			fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" data-anchor="%s" data-focus="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", label, f.w, f.h, idx, f.theme, anchor, strings.ReplaceAll(f.focus, "'", "&#39;"), f.w, f.h, title)
		}
		body.WriteString("</div>\n")
	}
	add("1. People: rows with marker, role and order buttons; subgroups; success line",
		"Mia was just moved up one place (the success line shows once). Each person is a calm row that opens their page; the only buttons on a row are the everyday ones, up and down, which set the order of the columns on the displays. Subgroups sit below with their members.",
		"", overview, append(append(desk("light", "dark", "bw"), portrait...), phone...)...)
	add("2. Add a person: name and adult or child; colour and marker under More options",
		"One primary action. The box suggests the first free colour and marker, so the defaults never clash. More options is shown open here.",
		"#new", addForm, append(append(desk("light", "dark", "bw"), portrait[0]), phone...)...)
	add("3. Change a person: name, colour (eight palette colours) and marker (eight shapes)",
		"The colour chips carry the colour name, the marker chips show the shape, so nothing relies on colour alone. In black and white every colour is black and only the marker tells people apart. The new eighth colour (raspberry) and marker (star) and the seventh (indigo, plus) join the choice; a household of up to eight people (two parents, six children) gets eight different colours and eight different shapes.",
		"", edit, append(append(desk("light", "dark", "bw"), portrait[0]), phone...)...)
	add("4. The same colour with the same marker is refused",
		"Mia was given Papa's colour and marker. The error sits at the marker choice, and what was typed stays.",
		"#err-shape", clash.body, append(desk("light", "bw"), phone...)...)
	add("5. Taken colours and markers are faded, pairs that would be refused are disabled",
		"Ben wears Mama's terracotta: the markers Mama and the others wear with it (filled circle) are disabled and cannot be chosen; every colour and marker another person uses is faded but still readable, also in black and white; the free ones keep full strength. Picking a colour updates which markers are disabled and the other way round (with JavaScript); without it the server still refuses the pair with the field error, as in frame 4.",
		"fieldset.look", benPage, append(desk("light", "bw"), phone[0])...)
	add("5b. The same for a colour: Emma wears Mama's filled circle",
		"With the filled circle selected, terracotta (Mama's colour with that marker) is disabled.",
		"fieldset.look", emmaPage, desk("light", "dark")...)
	add("5c. Tooltip: every colour and marker names who uses it, on hover and on focus",
		"Here the keyboard focus is on Mama's colour: the bubble reads Terracotta: Mama. The same text is the title (hover) and the accessible name of the choice; free options say free (German: frei).",
		"fieldset.look", benPage, frame{"light", 1024, 768, "label[title^='Terracotta']"}, frame{"bw", 1024, 768, "label[title^='Filled circle']"})
	add("5d. New subgroup: name and person chips",
		"Subgroups are chosen with the same chips as everywhere else.",
		"", groupPage, append(desk("light", "dark"), phone[0])...)
	add("6. Result on the display: eight people, eight columns, eight colours and eight shapes",
		"The family view at 1024 x 768 in light, dark and black and white, and in portrait. Eight columns do not fit side by side: six are shown and the rest scroll sideways (ADR 0016), here from the start. The Birthday party card lists all eight people: in black and white the eight shapes are told apart at 9 px on the card and at 16 px in each column header.",
		"", family, append(desk("light", "dark", "bw"), portrait...)...)
	add("7. The same family view scrolled to the last two columns (slots 7 and 8)",
		"Scrolled sideways to Emma (slot 8, raspberry, star) next to Ben (slot 7, indigo, plus).",
		`section.column[data-person='8']`, family, desk("light", "dark", "bw")...)
	add("8. Landscape 1280 x 800 with eight people",
		"A wider display still shows six columns before the sideways scroll starts.",
		"", family, frame{"light", 1280, 800, ""}, frame{"bw", 1280, 800, ""})

	var jsDocs strings.Builder
	for i, h := range docs {
		h = regexp.MustCompile(`(?s)<script src="[^"]*"[^>]*></script>`).ReplaceAllString(h, "")
		h = regexp.MustCompile(`(?s)<link rel="stylesheet"[^>]*>`).ReplaceAllString(h, "")
		h = strings.ReplaceAll(h, " autofocus", "") // focus would scroll the frame
		bodyAt := strings.Index(h, "<body")
		inner := h[bodyAt:strings.LastIndex(h, "</body>")]
		fmt.Fprintf(&jsDocs, "<template id=\"doc%d\">%s</template>\n", i, inner)
	}
	pageHTML := `<!doctype html>
<!-- Generated by TestWritePreview76 from the box's real pages and templates (ticket 5.21). The stylesheet, script and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 5.21 people</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 5.21 people in the web interface: preview with the example family (Mama, Papa, Sebastian, Mia and four more children: eight people): 1024 x 768 and portrait, light, dark and black and white; 375 px for phones</h1>
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
  var js = document.getElementById("js").innerHTML.replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");
  document.querySelectorAll("iframe[data-doc]").forEach(function (f) {
    var inner = document.getElementById("doc" + f.getAttribute("data-doc")).innerHTML;
    f.addEventListener("load", function () {
      var a = f.getAttribute("data-anchor");
      var el = a && f.contentDocument.querySelector(a);
      if (el) el.scrollIntoView();
      var focus = f.getAttribute("data-focus");
      var fl = focus && f.contentDocument.querySelector(focus);
      if (fl) { fl.setAttribute("data-tip-open", ""); }
    });
    f.srcdoc = '<!doctype html><html lang="en" data-theme="' + f.getAttribute("data-theme") + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + css + '</style></head>' + inner + '<script>' + js + '<\/script></html>';
  });
})();
</script>
</body></html>
`
	if err := os.WriteFile(out, []byte(pageHTML), 0o644); err != nil {
		t.Fatal(err)
	}
}
