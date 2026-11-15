package server_test

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/web"
)

// TestWriteDesignPreview90 writes the self-contained preview page for ticket
// 7.1 (stars and star log): Mia's personal view with her balance and the web
// tasks page with star values, the star log and the booking form. Both are the
// real templates: the personal view from its view model, the tasks page served
// by a box holding the example family.
// Run: HEJMO_WRITE_PREVIEW_90=../../docs/design/preview-90.html go test ./internal/server -run WriteDesignPreview90
func TestWriteDesignPreview90(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW_90")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_90 to the output path to write the preview")
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

	// Personal view of Mia, view model built like the box does.
	d := func(day, h, m int) time.Time { return time.Date(2026, 11, day, h, m, 0, 0, berlinTZ) }
	mia := familyview.Person{ID: "mia", Name: "Mia", Slot: 4, Shape: "square-empty"}
	occ := func(title string, s, e time.Time) calendar.Occurrence {
		return calendar.Occurrence{EventID: title, CalendarID: "c", Title: title, Start: s, End: e, People: []string{"mia"}}
	}
	view := familyview.BuildPersonal(familyview.PersonalInput{
		Now: d(14, 16, 20), Person: mia, Calendars: map[string]calendar.Audience{"c": {}},
		Occurrences: []calendar.Occurrence{occ("Football", d(14, 15, 0), d(14, 16, 30)), occ("Piano", d(16, 15, 0), d(16, 16, 0)), occ("Swimming", d(18, 16, 30), d(18, 17, 30))},
		Tasks: []familyview.Task{
			{ID: "1", Title: "Brush teeth", Emoji: "🪥", Part: "morning", Done: true, Stars: 1},
			{ID: "2", Title: "Pack school bag", Emoji: "🎒", Part: "morning", Done: true, Stars: 2},
			{ID: "3", Title: "Tidy room", Emoji: "🧸", Stars: 3},
			{ID: "4", Title: "Feed the cat", Emoji: "🐈", Stars: 1},
		},
		Stars: familyview.Stars{Shown: true, Balance: 12},
	})
	personal, err := server.RenderPersonalView("en", view)
	if err != nil {
		t.Fatal(err)
	}

	// The tasks page, served by a box with the example family.
	f := newFamilyBox(t, "screen")
	f.now = f.at(14, 16, 20)
	post := func(path string, v url.Values) {
		if r := f.post(path, v); r.status != 303 {
			t.Fatalf("POST %s = %d\n%s", path, r.status, r.body)
		}
	}
	add := func(who, title, emoji, part string, stars string) {
		post("/tasks", url.Values{"person": {f.people[who].ID}, "title": {title}, "emoji": {emoji}, "repeat": {"daily"}, "part": {part}, "stars": {stars}})
	}
	add("Mia", "Brush teeth", "🪥", "morning", "1")
	add("Mia", "Pack school bag", "🎒", "morning", "2")
	add("Mia", "Tidy room", "🧸", "", "3")
	add("Sebastian", "Feed the cat", "🐈", "", "")
	add("Papa", "Take out the bins", "🗑️", "evening", "")
	tasks, err := f.db.Tasks(f.householdID)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range tasks {
		if k.Title == "Brush teeth" || k.Title == "Pack school bag" || k.Title == "Feed the cat" {
			post("/tasks/"+k.ID+"/done", url.Values{"done": {"1"}})
		}
	}
	f.now = f.at(14, 16, 45)
	post("/tasks/stars", url.Values{"person": {f.people["Mia"].ID}, "delta": {"10"}, "note": {"weekend chores"}})
	post("/tasks/stars", url.Values{"person": {f.people["Mia"].ID}, "delta": {"-5"}, "note": {"redeemed: ice cream"}})
	page := f.do("GET", "/tasks", nil, map[string]string{"Accept-Language": "en"})
	if page.status != 200 {
		t.Fatalf("tasks page = %d", page.status)
	}
	i := strings.Index(page.body, `<div class="frame framed`)
	j := strings.LastIndex(page.body, "</body>")
	if i < 0 || j < 0 {
		t.Fatalf("cannot find the frame in the page:\n%s", page.body)
	}
	tasksPage := page.body[i:j]

	var body strings.Builder
	themes := []struct{ key, label string }{{"light", "Light"}, {"dark", "Dark"}, {"bw", "Black and white"}}
	fig := func(caption, theme string, w, h int, inner string, class string) {
		fmt.Fprintf(&body, `<figure class="frame-fig"><figcaption>%s (%dx%d)</figcaption><div class="shell" style="width:%dpx"><div class="%s" data-theme="%s" style="height:%dpx">%s</div></div></figure>`+"\n", caption, w, h, w, class, theme, h, inner)
	}
	body.WriteString(`<h2 class="sec">Personal view of Mia on a display: star balance in the header, star value on each task (1024x768, landscape)</h2><div class="row-of">` + "\n")
	for _, th := range themes {
		fig(th.label, th.key, 1024, 768, `<div class="family-page" style="height:100%">`+personal+`</div>`, "screen")
	}
	body.WriteString(`</div><h2 class="sec">Personal view, portrait (768x1024)</h2><div class="row-of">` + "\n")
	for _, th := range themes {
		fig(th.label, th.key, 768, 1024, `<div class="family-page" style="height:100%">`+personal+`</div>`, "screen")
	}
	body.WriteString(`</div><h2 class="sec">Web interface, tasks page for a parent: star value on tasks, star accounts with log, booking by hand, star field in the new-task form (1024 px wide)</h2><div class="row-of">` + "\n")
	for _, th := range themes {
		fig(th.label, th.key, 1024, 2000, tasksPage, "device")
	}
	body.WriteString(`</div><h2 class="sec">Web interface, tasks page in portrait (768 px wide)</h2><div class="row-of">` + "\n")
	for _, th := range themes {
		fig(th.label, th.key, 768, 2300, tasksPage, "device")
	}
	body.WriteString("</div>\n")

	html := `<!doctype html>
<!-- Generated by TestWriteDesignPreview90 from the real view model and templates (ticket 7.1). The stylesheet and font are inlined copies of web/static and do not follow later changes. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 7.1 stars and star log</title>
<style>` + style + `
.device { container-type: inline-size; width: 100%; overflow: hidden; background: var(--background); color: var(--text); }
.screen { background: var(--background); color: var(--text); padding: var(--margin); box-sizing: border-box; overflow: hidden; }
.shell { border: 1px solid #888; flex: none; overflow: hidden; }
.frame-fig { margin: 0; } .frame-fig figcaption { font: 14px system-ui, sans-serif; margin: 0 0 6px; color: #fff; }
.row-of { display: flex; gap: 24px; align-items: flex-start; flex-wrap: nowrap; overflow-x: auto; margin-bottom: 32px; padding: 8px; background: #888; }
.sec { color: #111; font: 700 20px system-ui, sans-serif; margin: 24px 8px 8px; }
body { margin: 0; background: #fff; color: #111; font-family: Figtree, system-ui, sans-serif; padding: 0; }
</style></head><body>
<h1 style="font:700 26px system-ui;margin:16px 8px">Hejmo 7.1 stars and star log: preview with the example family (Mama, Papa, Sebastian, Mia), Saturday 14/11/2026</h1>
` + body.String() + `</body></html>
`
	if err := os.WriteFile(out, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
}
