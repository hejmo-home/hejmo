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

	"github.com/hejmo-home/hejmo/web"
)

// TestWritePreview49 writes the self-contained preview page for the product
// owner's approval of ticket 5.12 (calendar in the web interface). Every frame
// is a real page of the box, rendered by the real templates for the example
// family (Mama, Papa, Sebastian, Mia); nothing is hand-written HTML. Each frame
// is an iframe, so the viewport widths trigger the real media queries.
// Run: HEJMO_WRITE_PREVIEW=../../docs/design/preview-49.html go test ./internal/server -run WritePreview49
func TestWritePreview49(t *testing.T) {
	t.Parallel()
	out := os.Getenv("HEJMO_WRITE_PREVIEW")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	js, _ := web.Static.ReadFile("static/form.js")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")

	f := calendarBox(t)
	f.now = time.Date(2026, 11, 16, 9, 0, 0, 0, berlinTZ) // Monday of the sample week
	ev := func(title, date, from, to string, who ...string) {
		v := eventForm(title, date, from, to)
		if len(who) > 0 {
			v.Del("whole_family")
			for _, w := range who {
				v.Add("people", f.people[w].ID)
			}
		}
		f.postEvent(t, v)
	}
	form := func(title, date, from, to string, who ...string) {
		v := eventForm(title, date, from, to)
		v.Set("repeat", "weekly")
		v.Del("whole_family")
		for _, w := range who {
			v.Add("people", f.people[w].ID)
		}
		f.postEvent(t, v)
	}
	form("Swimming", "2026-11-03", "15:00", "16:00", "Mia")
	form("Piano", "2026-11-04", "16:00", "17:00", "Sebastian")
	form("Choir", "2026-11-05", "18:00", "19:30", "Mama")
	ev("Parents' evening", "2026-11-16", "19:30", "21:00", "Mama", "Papa")
	ev("Dentist", "2026-11-18", "08:30", "09:15", "Sebastian")
	allDay := func(title, from, to string, who ...string) {
		v := url.Values{"title": {title}, "all_day": {"on"}, "start_date": {from}, "end_date": {to}, "repeat": {"none"}, "whole_family": {"1"}}
		if len(who) > 0 {
			v.Del("whole_family")
			for _, w := range who {
				v.Add("people", f.people[w].ID)
			}
		}
		f.postEvent(t, v)
	}
	allDay("Hut weekend", "2026-11-20", "2026-11-22")
	allDay("School trip", "2026-11-17", "2026-11-19", "Mia")
	allDay("Grandma's birthday", "2026-11-18", "2026-11-18")
	f.feeds.set("https://example.org/school.ics", feedOf("UID:concert\r\nSUMMARY:School concert\r\nDTSTART:20261119T150000Z\r\nDTEND:20261119T163000Z"), nil)
	f.post("/calendars", url.Values{"name": {"School"}, "url": {"https://example.org/school.ics"}, "color": {"#2F5E7A"}, "interval": {"60"}, "people": {f.people["Mia"].ID, f.people["Sebastian"].ID}})

	page := func(path string) string {
		r := getEN(f.box, path)
		if r.status != http.StatusOK && r.status != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d", path, r.status)
		}
		return r.body
	}
	page("/calendar") // shows the success line of the last change once
	week := page("/calendar")
	newForm := page("/calendar/new?date=2026-11-19")
	path, occ := occurrenceOf(t, linkTo(t, week, "Swimming"))
	edit := page(path + "?occurrence=" + url.QueryEscape(occ))
	danger := strings.Replace(edit, `<details class="danger">`, `<details class="danger" open>`, 1)
	search := page("/calendar/search?q=swim")
	bad := eventForm("", "2026-11-19", "15:00", "14:00")
	badPage := func() string {
		r := f.do(http.MethodPost, "/calendar/events", bad, map[string]string{"Accept-Language": "en"})
		return r.body
	}()
	fresh := signedInBox(t)
	emptyWeek := getEN(fresh, "/calendar").body

	type frame struct {
		theme string
		w, h  int
	}
	three := func(w, h int) []frame { return []frame{{"light", w, h}, {"dark", w, h}, {"bw", w, h}} }
	var body strings.Builder
	var docs []string
	add := func(title, note, anchor, html string, frames ...frame) {
		idx := len(docs)
		docs = append(docs, html)
		fmt.Fprintf(&body, "<h2>%s</h2><p class=\"note\">%s</p><div class=\"row\">\n", title, note)
		for _, fr := range frames {
			label := map[string]string{"light": "Light", "dark": "Dark", "bw": "Black and white"}[fr.theme]
			fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" data-anchor="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", label, fr.w, fr.h, idx, fr.theme, anchor, fr.w, fr.h, title)
		}
		body.WriteString("</div>\n")
	}
	add("1. The week: day columns, multi-day events as continuous bars",
		"Monday to Sunday with today marked. The hut weekend (Fri to Sun, whole family) and Mia's school trip (Tue to Thu) are one bar each and sit in their own rows. Timed events sit in their day, with the markers of their people; in black and white the marker is a shape. The school concert comes from a read-only calendar: no link, marked as read-only. Weekly events repeat (the repeat sign). \"+\" adds an event on that day.",
		"", week, append(three(1024, 768), frame{"light", 768, 1024}, frame{"bw", 768, 1024})...)
	add("2. The week on a phone",
		"Below 760 px the days stack and the bars become entries inside every day they cover.",
		"", week, frame{"light", 375, 812}, frame{"dark", 375, 812})
	add("3. New event: person chips, repeat, More options",
		"Title, start and end, who it is for (the same chips as the other pages; \"Whole family\" is the default), the repeat. Place, notes and time zone sit under More options. \"All day\" hides the times.",
		"", newForm, append(three(1024, 768), frame{"light", 768, 1024})...)
	add("4. Edit a series: the scope is a choice",
		"Changing an event of a series asks which events change: only this, this and following, or all. Earlier events stay as they are with \"this and following\".",
		"", edit, append(three(1024, 768), frame{"light", 768, 1024})...)
	add("5. Delete behind a confirmation",
		"Deleting is only on the event's page, closed by default; it says what happens and asks the same scope (shown open here).",
		"details.danger", danger, frame{"light", 1024, 768}, frame{"bw", 1024, 768})
	add("6. Error at the field, entries stay",
		"The end is before the start and the title is empty: errors sit under their fields, everything else stays as typed.",
		"", badPage, frame{"light", 1024, 768}, frame{"bw", 1024, 768})
	add("7. Search",
		"Search finds the words of the title, place and notes by their beginning, ignoring capitals and accents (\"swim\" finds Swimming; \"schwimm\" finds Schwimmen). Events of read-only calendars are found too but have no link.",
		"", search, frame{"light", 1024, 768}, frame{"bw", 1024, 768})
	add("8. Empty week", "A week without events says so.", "", emptyWeek, frame{"light", 1024, 768}, frame{"bw", 1024, 768})

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
<!-- Generated by TestWritePreview49 from the box's real pages and templates (ticket 5.12). The stylesheet, script and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 5.12 calendar in the web interface</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 5.12 calendar in the web interface: preview with the example family (Mama, Papa, Sebastian, Mia): 1024 x 768 and portrait, light, dark and black and white</h1>
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
	if err := os.WriteFile(out, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}
