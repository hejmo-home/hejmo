package server_test

import (
	"encoding/base64"
	"fmt"
	"html"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/web"
)

// TestWriteDesignPreview37 writes the self-contained preview page for ticket
// 6.6 (tasks on the display): the family view with each person's task progress
// anchored at the bottom of the column (ADR 0016), and a personal view with its
// large checkboxes. Since the design pass (Opus) the templates and the
// stylesheet carry its markup, so regenerating gives the same design for the
// example family; the committed docs/design/preview-37.html is the designer's
// reference with its extra edge cases (eight people, phone, long titles). Every frame is an iframe with the real stylesheet and the
// real templates, built from the real view models.
// Run: HEJMO_WRITE_PREVIEW_37=../../docs/design/preview-37.html go test ./internal/server -run WriteDesignPreview37
func TestWriteDesignPreview37(t *testing.T) {
	t.Parallel()
	out := os.Getenv("HEJMO_WRITE_PREVIEW_37")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_37 to the output path to write the preview")
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

	d := func(day, h, m int) time.Time { return time.Date(2026, 11, day, h, m, 0, 0, berlinTZ) }
	people := []familyview.Person{
		{ID: "mama", Name: "Mama", Slot: 1, Shape: "circle-filled"}, {ID: "papa", Name: "Papa", Slot: 2, Shape: "circle-empty"},
		{ID: "seb", Name: "Sebastian", Slot: 3, Shape: "square-filled"}, {ID: "mia", Name: "Mia", Slot: 4, Shape: "square-empty"}}
	occ := func(title string, s, e time.Time, who ...string) calendar.Occurrence {
		return calendar.Occurrence{EventID: title, CalendarID: "c", Title: title, Start: s, End: e, People: who}
	}
	occs := []calendar.Occurrence{
		occ("Dentist", d(14, 9, 0), d(14, 10, 0), "mama"),
		occ("Team meeting", d(14, 10, 0), d(14, 11, 0), "papa"),
		occ("Football", d(14, 15, 0), d(14, 16, 30), "seb", "mia"),
		occ("Piano", d(16, 15, 0), d(16, 16, 0), "mia"),
		occ("Swimming", d(18, 16, 30), d(18, 17, 30), "mia"),
	}
	cals := map[string]calendar.Audience{"c": {}}
	// Mia has three of five tasks done, Sebastian all of his, Papa none of his;
	// Mama has no tasks today and shows no bar.
	progress := map[string]familyview.Progress{"mia": {Done: 3, Total: 5}, "seb": {Done: 2, Total: 2}, "papa": {Done: 0, Total: 1}}
	family := familyview.Build(familyview.Input{Now: d(14, 16, 20), Lang: "en", Region: "DE-BY", People: people, Calendars: cals, Occurrences: occs, TaskProgress: progress})
	familyMarkup, err := server.RenderFamilyView("en", family)
	if err != nil {
		t.Fatal(err)
	}
	mia := familyview.BuildPersonal(familyview.PersonalInput{
		Now: d(14, 16, 20), Person: people[3], Calendars: cals, Occurrences: occs,
		Tasks: []familyview.Task{
			{ID: "1", Title: "Brush teeth", Emoji: "🪥", Part: "morning", Done: true},
			{ID: "2", Title: "Pack school bag", Emoji: "🎒", Part: "morning", Done: true},
			{ID: "3", Title: "Feed the cat", Emoji: "🐈", Done: true},
			{ID: "4", Title: "Tidy room", Emoji: "🧸"},
			{ID: "5", Title: "Practise piano", Emoji: "🎹", Part: "evening"},
		},
	})
	personalMarkup, err := server.RenderPersonalView("en", mia)
	if err != nil {
		t.Fatal(err)
	}

	var body strings.Builder
	frame := func(caption, theme string, w, h int, markup string) {
		doc := fmt.Sprintf(`<!doctype html><html lang="en" data-theme="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>%s</style></head><body><div class="family-page"><div data-view>%s</div></div></body></html>`, theme, style, markup)
		fmt.Fprintf(&body, `<figure><figcaption>%s (%dx%d)</figcaption><iframe title="%s" width="%d" height="%d" srcdoc="%s"></iframe></figure>`+"\n", caption, w, h, caption, w, h, html.EscapeString(doc))
	}
	themes := [][2]string{{"Light", "light"}, {"Dark", "dark"}, {"Black and white", "bw"}}
	section := func(title string, w, h int, markup string) {
		fmt.Fprintf(&body, "<h2>%s</h2><div class=\"row\">\n", title)
		for _, th := range themes {
			frame(th[0], th[1], w, h, markup)
		}
		body.WriteString("</div>\n")
	}
	section("Family view, landscape 1024x768: today's tasks in words at the bottom of each column, the whole block opens the personal view; Mama has no tasks today and shows no block", 1024, 768, familyMarkup)
	section("Family view, portrait 768x1024", 768, 1024, familyMarkup)
	section("Mia's personal view, landscape 1024x768: tasks first, grouped by part of the day, with large checkboxes (touch targets of at least 44 px)", 1024, 768, personalMarkup)
	section("Mia's personal view, portrait 768x1024", 768, 1024, personalMarkup)

	page := `<!doctype html>
<!-- Generated by TestWriteDesignPreview37 from the real view models, templates and stylesheet (ticket 6.6). Stylesheet and font are inlined copies of web/static at generation time and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 6.6 tasks on the display</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif}
h1,h2{color:#fff}h1{font-size:22px}h2{font-size:18px;margin:20px 0 8px}
figure{margin:0 0 28px}figcaption{color:#fff;margin-bottom:6px;font-weight:700}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 6.6 tasks on the display: example family (Mama, Papa, Sebastian, Mia), Saturday 14/11/2026, 16:20</h1>
` + body.String() + `</body></html>
`
	if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}
