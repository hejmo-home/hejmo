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

// TestWriteDesignPreview38 writes the self-contained preview page for the
// product owner's approval of ticket 6.7 (the tomorrow tile states, ADR 0016).
// Every frame is an iframe with the real stylesheet and the real template,
// rendered from the real view model with the example family.
// Run: HEJMO_WRITE_PREVIEW=../../docs/design/preview-38.html go test ./internal/server -run WriteDesignPreview38
func TestWriteDesignPreview38(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW to the output path to write the preview")
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
		occ("Dentist", d(15, 9, 0), d(15, 10, 0), "mama"),
		occ("Football", d(15, 15, 0), d(15, 16, 30), "seb", "mia"),
		occ("Pick up Mia", d(15, 16, 45), d(15, 17, 15), "papa"),
	}
	bin := func(title string) calendar.Occurrence {
		return calendar.Occurrence{EventID: title, CalendarID: "waste", Title: title, AllDay: true, Start: d(16, 0, 0), End: d(17, 0, 0)}
	}
	type state struct {
		name  string
		now   time.Time
		waste []calendar.Occurrence
		notes map[string]string
	}
	states := []state{
		{"Hidden: no collection tomorrow and no note (18:00)", d(15, 18, 0), nil, nil},
		{"One collection, before 18:00: not highlighted", d(15, 9, 0), []calendar.Occurrence{bin("Paper bin")}, nil},
		{"One collection from 18:00: highlighted (the reminder)", d(15, 18, 0), []calendar.Occurrence{bin("Paper bin")}, nil},
		{"Two collections and the parent's note (18:00): the note is the last line", d(15, 18, 0), []calendar.Occurrence{bin("Paper bin"), bin("Residual waste")},
			map[string]string{"2026-11-16": "Mia: swimming bag"}},
		{"Four collections and a note: three lines, the rest as +N more, the note stays", d(15, 18, 0),
			[]calendar.Occurrence{bin("Paper bin"), bin("Residual waste"), bin("Organic waste"), bin("Yellow bag")}, map[string]string{"2026-11-16": "Mia: swimming bag"}},
		{"Note only (18:00): not highlighted; tomorrow's note replaced today's", d(15, 18, 0), nil,
			map[string]string{"2026-11-15": "Sebastian: football kit", "2026-11-16": "Mia: swimming bag"}},
	}
	build := func(s state) familyview.View {
		v := familyview.Build(familyview.Input{Now: s.now, Lang: "en", Region: "DE-BY", People: people, Occurrences: occs,
			Waste: s.waste, DayNotes: s.notes, TomorrowLabel: "Tomorrow", MoreFormat: "+%d more"})
		v.Columns[0].Progress = &familyview.Progress{Done: 3, Total: 5}
		if s.name[0] == 'O' || s.name[0] == 'T' { // a meal tile next to it shows how the two tiles share the width
			v.Meal = &familyview.Tile{Label: "Dinner", Lines: []string{"Pasta bake"}}
		}
		return v
	}

	var body strings.Builder
	frame := func(caption, theme string, w, h int, v familyview.View) {
		view, err := server.RenderFamilyView("en", v)
		if err != nil {
			t.Fatal(err)
		}
		doc := fmt.Sprintf(`<!doctype html><html lang="en" data-theme="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>%s</style></head><body><div class="family-page"><div data-view>%s</div></div></body></html>`, theme, style, view)
		fmt.Fprintf(&body, `<figure><figcaption>%s (%dx%d)</figcaption><iframe title="%s" width="%d" height="%d" srcdoc="%s"></iframe></figure>`+"\n", caption, w, h, caption, w, h, html.EscapeString(doc))
	}
	body.WriteString("<h2>Light, 1024x768: the six states of the tomorrow tile</h2><div class=\"row\">\n")
	for _, s := range states {
		frame(s.name, "light", 1024, 768, build(s))
	}
	for _, th := range [][2]string{{"Dark", "dark"}, {"Black and white", "bw"}} {
		fmt.Fprintf(&body, "</div><h2>%s, 1024x768: the reminder, the longest tile and a note only</h2><div class=\"row\">\n", th[0])
		for _, i := range []int{2, 4, 5} {
			frame(th[0]+": "+states[i].name, th[1], 1024, 768, build(states[i]))
		}
	}
	body.WriteString("</div><h2>Portrait 768x1024: the reminder with a note, in light, dark and black and white</h2><div class=\"row\">\n")
	for _, th := range [][2]string{{"Light", "light"}, {"Dark", "dark"}, {"Black and white", "bw"}} {
		frame(th[0], th[1], 768, 1024, build(states[3]))
	}
	body.WriteString("</div>\n")

	page := `<!doctype html>
<!-- Generated by TestWriteDesignPreview38 from the real view model, template and stylesheet (ticket 6.7, ADR 0016). Stylesheet and font are inlined copies of web/static at generation time and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 6.7 tomorrow tile</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif}
h1,h2{color:#fff}h1{font-size:22px}h2{font-size:18px;margin:20px 0 8px}
figure{margin:0 0 28px}figcaption{color:#fff;margin-bottom:6px;font-weight:700;max-width:1024px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 6.7 tomorrow tile and waste collection reminder: example family (Mama, Papa, Sebastian, Mia), Sunday 15/11/2026</h1>
` + body.String() + `</body></html>
`
	if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}
