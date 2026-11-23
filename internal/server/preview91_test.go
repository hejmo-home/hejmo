package server_test

import (
	"encoding/base64"
	"fmt"
	"html"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
	"github.com/hejmo-home/hejmo/web"
)

// TestWriteDesignPreview91 writes the self-contained preview page for ticket
// 7.2 (#91, routines): the personal view, the tasks page, the routine pages,
// the task detail page and a child's tasks page, rendered by boxes holding the
// example family from the real templates, with the real stylesheet. It
// replaces the hand-written design preview.
// Run: HEJMO_WRITE_PREVIEW_91=../../docs/design/preview-91.html go test ./internal/server -run WriteDesignPreview91
func TestWriteDesignPreview91(t *testing.T) {
	t.Parallel()
	out := os.Getenv("HEJMO_WRITE_PREVIEW_91")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_91 to the output path to write the preview")
	}
	css, err := web.Static.ReadFile("static/hejmo.css")
	if err != nil {
		t.Fatal(err)
	}
	font, err := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")
	if err != nil {
		t.Fatal(err)
	}
	de := map[string]string{"Accept-Language": "de"}
	scripts := regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	displayDoc := func(page string) string {
		i, j := strings.Index(page, "<body"), strings.LastIndex(page, "</body>")
		if i < 0 || j < 0 {
			t.Fatalf("no body in the page:\n%s", page)
		}
		return scripts.ReplaceAllString(page[i:j], "") + "</body>"
	}
	webDoc := func(page string) string { return `<body class="web">` + frameOf(t, page) + "</body>" }

	// Monday 16/11/2026, early morning.
	f := newFamilyBox(t, "eink_color")
	f.now = f.at(16, 7, 20)
	day := "2026-11-16"
	add := func(who, title, emoji string, stars int, part task.PartOfDay, routine string, weekdays ...string) task.Task {
		t.Helper()
		k := task.Task{PersonID: f.people[who].ID, Title: title, Emoji: emoji, Repeat: task.Daily, Date: "2026-11-14", Stars: stars, PartOfDay: part, RoutineID: routine}
		if len(weekdays) > 0 {
			k.Repeat, k.Weekdays = task.Weekly, weekdays
		}
		k, err := f.db.AddTask(f.householdID, k)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	tick := func(k task.Task, done bool) {
		t.Helper()
		if err := f.db.SetTaskDone(f.householdID, k.ID, day, done, "", f.at(16, 7, 10)); err != nil {
			t.Fatal(err)
		}
	}
	routine := func(who, name string, part task.PartOfDay) task.Routine {
		t.Helper()
		r, err := f.db.CreateRoutine(f.householdID, task.Routine{PersonID: f.people[who].ID, Name: name, Part: part})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	mon2fri := []string{"MO", "TU", "WE", "TH", "FR"}
	morning := routine("Mia", "Morgenroutine", task.Morning)
	teeth := add("Mia", "Zähne putzen", "🪥", 1, "", morning.ID)
	dressed := add("Mia", "Anziehen", "👕", 1, "", morning.ID)
	breakfast := add("Mia", "Frühstücken", "🥣", 1, "", morning.ID)
	bag := add("Mia", "Schulranzen packen", "🎒", 2, "", morning.ID, mon2fri...)
	evening := routine("Mia", "Abendroutine", task.Evening)
	add("Mia", "Baden", "🛁", 1, "", evening.ID)
	add("Mia", "Zähne putzen", "🪥", 1, "", evening.ID)
	add("Mia", "Vorlesen", "📖", 0, "", evening.ID)
	add("Mia", "Katze füttern", "🐈", 1, "", "")
	piano := add("Mia", "Klavier üben, mindestens eine Viertelstunde", "🎹", 1, "", "", "MO", "WE", "FR")
	_ = piano
	add("Mia", "Blumen gießen", "🌱", 1, task.Morning, "", "SA")
	add("Papa", "Altpapier rausbringen", "📦", 0, "", "", "MO")
	sebEvening := routine("Sebastian", "Abendroutine", task.Evening)
	add("Sebastian", "Schulsachen für morgen herrichten", "🎒", 1, "", sebEvening.ID)
	add("Sebastian", "Zähne putzen", "🪥", 1, "", sebEvening.ID)
	add("Sebastian", "Licht aus um 20:30", "💡", 0, "", sebEvening.ID)
	cat := add("Sebastian", "Katze füttern", "🐈", 1, "", "")
	tick(cat, true)
	tick(teeth, true)
	tick(dressed, true)

	pv := displayDoc(f.display.get("/display/person/"+f.people["Mia"].ID, "de").body)
	pvEn := displayDoc(f.display.get("/display/person/"+f.people["Mia"].ID, "en").body)
	tick(breakfast, true)
	tick(bag, true)
	pvDone := displayDoc(f.display.get("/display/person/"+f.people["Mia"].ID, "de").body)
	tick(breakfast, false)
	tick(bag, false)

	tasksPage := webDoc(f.do("GET", "/tasks", nil, de).body)
	newPage := webDoc(f.do("GET", "/routines/new", nil, de).body)
	newErr := f.do("POST", "/routines", url.Values{"person": {f.people["Mia"].ID}, "part": {"morning"}, "name": {"Morgenroutine"}}, de)
	if newErr.status != 422 {
		t.Fatalf("duplicate = %d", newErr.status)
	}
	newErrDoc := webDoc(newErr.body)

	created := f.do("POST", "/routines", url.Values{"person": {f.people["Sebastian"].ID}, "part": {"morning"}, "name": {"Morgenroutine"}}, de)
	if created.status != 303 {
		t.Fatalf("create = %d", created.status)
	}
	emptyDoc := webDoc(f.do("GET", created.location, nil, de).body)

	filled := webDoc(f.do("GET", "/routines/"+morning.ID, nil, de).body)
	existing := strings.Replace(strings.Replace(filled, `value="new" checked`, `value="new"`, 1), `value="existing"`, `value="existing" checked`, 1)
	danger := strings.Replace(filled, `<details class="danger">`, `<details class="danger" open>`, 1)
	rErr := f.do("POST", "/routines/"+morning.ID+"/tasks", url.Values{"source": {"new"}, "title": {""}, "emoji": {"🧦"}, "stars": {"1"}, "repeat": {"daily"}}, de)
	if rErr.status != 422 {
		t.Fatalf("blank title = %d", rErr.status)
	}
	rErrDoc := webDoc(rErr.body)
	taskDoc := webDoc(f.do("GET", "/tasks/"+breakfast.ID, nil, de).body)
	phone := pairedPhone(t, f.box, f.people["Mia"], "Mias Handy")
	childDoc := webDoc(phone.get("/tasks", "de").body)

	if r := f.post("/routines/"+morning.ID+"/move", url.Values{"task": {breakfast.ID}, "direction": {"up"}}); r.status != 303 {
		t.Fatalf("move = %d", r.status)
	}
	movedDoc := webDoc(f.do("GET", "/routines/"+morning.ID, nil, de).body)

	// A box with a long routine and long titles (Jonas), and one with eight people.
	j := newFamilyBox(t, "eink_color")
	j.now = j.at(16, 15, 30)
	jonas, err := j.db.AddPerson(j.householdID, store.Person{Name: "Maximilian-Alexander", Role: store.RoleChild})
	if err != nil {
		t.Fatal(err)
	}
	j.people["Maximilian-Alexander"] = jonas
	jr := func(name string, part task.PartOfDay) task.Routine {
		r, err := j.db.CreateRoutine(j.householdID, task.Routine{PersonID: jonas.ID, Name: name, Part: part})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	step := func(r task.Routine, title, emoji string, stars int, done bool) {
		k, err := j.db.AddTask(j.householdID, task.Task{PersonID: jonas.ID, Title: title, Emoji: emoji, Repeat: task.Daily, Date: "2026-11-14", Stars: stars, RoutineID: r.ID})
		if err != nil {
			t.Fatal(err)
		}
		if done {
			if err := j.db.SetTaskDone(j.householdID, k.ID, day, true, "", j.at(16, 7, 10)); err != nil {
				t.Fatal(err)
			}
		}
	}
	school := jr("Schulmorgen", task.Morning)
	for _, s := range [][2]string{{"Zähne putzen", "🪥"}, {"Anziehen", "👕"}, {"Frühstücken", "🥣"}, {"Schulranzen packen", "🎒"}} {
		step(school, s[0], s[1], 1, true)
	}
	after := jr("Nachmittagsroutine mit Hausaufgaben und Trompetenübung", task.AfterSchool)
	step(after, "Schulranzen ausräumen und Brotdose in die Spülmaschine stellen", "🎒", 1, true)
	step(after, "Hausaufgaben im Mathematikheft", "✏️", 2, true)
	step(after, "Lesetagebuch: zehn Minuten lesen", "📚", 1, false)
	step(after, "Trompete üben", "🎺", 2, false)
	step(after, "Fahrradhelm und Turnbeutel an die Garderobe hängen", "🚲", 1, false)
	step(after, "Zimmer aufräumen", "🧸", 1, false)
	step(after, "Wäschekorb ins Badezimmer bringen", "🧺", 1, false)
	pvJonas := displayDoc(j.display.get("/display/person/"+jonas.ID, "de").body)

	e := newFamilyBox(t, "eink_color")
	for _, n := range []string{"Lotta", "Ben", "Emma", "Jonas"} {
		if _, err := e.db.AddPerson(e.householdID, store.Person{Name: n, Role: store.RoleChild}); err != nil {
			t.Fatal(err)
		}
	}
	new8 := webDoc(e.do("GET", "/routines/new", nil, de).body)

	type fig struct {
		caption, theme, lang, anchor string
		w, h                         int
		doc                          string
	}
	docs := map[string]string{}
	var order []string
	var body strings.Builder
	section := func(title, note string, figs ...fig) {
		fmt.Fprintf(&body, `<h2>%s</h2><p class="note">%s</p><div class="row">`, html.EscapeString(title), html.EscapeString(note))
		for _, g := range figs {
			id := fmt.Sprintf("d%d", len(order))
			docs[id] = g.doc
			order = append(order, id)
			lang := g.lang
			if lang == "" {
				lang = "de"
			}
			fmt.Fprintf(&body, `<figure><figcaption>%s</figcaption><iframe data-doc="%s" data-theme="%s" data-lang="%s" data-anchor="%s" width="%d" height="%d" title="%s"></iframe></figure>`,
				html.EscapeString(g.caption), id, g.theme, lang, html.EscapeString(g.anchor), g.w, g.h, html.EscapeString(title+": "+g.caption))
		}
		body.WriteString("</div>")
	}
	l, d, bw := "light", "dark", "bw"
	section("1. Mias persönliche Ansicht am Montagmorgen: Routinen als Kästen in ihrer Reihenfolge",
		"Aufgaben zuerst (ADR 0016). Jede Tageszeit hat ihr Wort und ihr Symbol (Sonnenaufgang, Sonne, Mond). Eine Routine ist ein Kasten in Mias Farbe, oben ihr Name und ihr Stand in Worten. Die nächste offene Aufgabe hat einen dicken Rahmen. Der Kopf zählt alle Aufgaben von heute.",
		fig{"Hell, 1024 x 768", l, "", "", 1024, 768, pv}, fig{"Dunkel, 1024 x 768", d, "", "", 1024, 768, pv}, fig{"Schwarz-Weiß, 1024 x 768", bw, "", "", 1024, 768, pv},
		fig{"Hell, 768 x 1024", l, "", "", 768, 1024, pv}, fig{"Hell, 375 x 812", l, "", "", 375, 812, pv}, fig{"Schwarz-Weiß, 375 x 812 (gescrollt)", bw, "", ".part[data-part='evening']", 375, 812, pv})
	section("2. Morgenroutine geschafft: 4 von 4", "Der Kasten bekommt Mias Farbton als Fläche und ein Häkchen vor „4 von 4 erledigt“.",
		fig{"Hell, 1024 x 768", l, "", "", 1024, 768, pvDone}, fig{"Schwarz-Weiß, 1024 x 768", bw, "", "", 1024, 768, pvDone}, fig{"Hell, 375 x 812", l, "", "", 375, 812, pvDone})
	section("3. Grenzfall: lange Titel, Routine mit sieben Aufgaben nach der Schule", "Lange Titel brechen um, Silbentrennung nur bei Wörtern ab 12 Buchstaben.",
		fig{"Schwarz-Weiß, 1024 x 768 (gescrollt)", bw, "", ".part[data-part='afterschool']", 1024, 768, pvJonas}, fig{"Hell, 375 x 812 (gescrollt)", l, "", ".part[data-part='afterschool']", 375, 812, pvJonas})
	section("4. Dieselbe Ansicht auf Englisch", "Morning, After school, Evening, Any time today; „2 of 4 done“.", fig{"Hell, 1024 x 768", l, "en", "", 1024, 768, pvEn})
	section("5. Aufgaben (Web, Eltern): Routinen als Karten pro Person", "Pro Person zuerst ihre Routinen als Karten, darunter „Weitere Aufgaben von …“.",
		fig{"Hell, 1024 x 768 (gescrollt)", l, "", "#mia", 1024, 768, tasksPage}, fig{"Dunkel, 1024 x 768", d, "", "", 1024, 768, tasksPage}, fig{"Schwarz-Weiß, 1024 x 768", bw, "", "", 1024, 768, tasksPage},
		fig{"Hell, 375 x 812", l, "", "", 375, 812, tasksPage})
	section("6. Neue Routine", "Person als Chips (eine Person, keine „Ganze Familie“), Tageszeit als Segment, Name unter „Weitere Optionen“.",
		fig{"Hell, 1024 x 768", l, "", "", 1024, 768, newPage}, fig{"Dunkel, 1024 x 768", d, "", "", 1024, 768, newPage}, fig{"Schwarz-Weiß, 1024 x 768", bw, "", "", 1024, 768, newPage}, fig{"Hell, 375 x 812", l, "", "", 375, 812, newPage})
	section("7. Neue Routine mit acht Personen", "Acht Chips brechen in Zeilen um.", fig{"Hell, 375 x 812 (gescrollt)", l, "", "fieldset.chips", 375, 812, new8})
	section("8. Fehler: Mia hat schon eine Morgenroutine", "Der Fehler steht am Feld Tageszeit, nennt die vorhandene Routine mit Link.",
		fig{"Hell, 1024 x 768", l, "", "", 1024, 768, newErrDoc}, fig{"Schwarz-Weiß, 375 x 812 (gescrollt)", bw, "", "fieldset.seg", 375, 812, newErrDoc})
	section("9. Gerade angelegt: leere Routine", "Erfolgszeile, Fakten, leerer Zustand, Formular für die erste Aufgabe.",
		fig{"Hell, 1024 x 768", l, "", "", 1024, 768, emptyDoc}, fig{"Hell, 375 x 812 (gescrollt)", l, "", "#add", 375, 812, emptyDoc})
	section("10. Seite der Routine: Reihenfolge, Aufgabe hinzufügen", "Nummeriert, mit „Hoch“ und „Runter“ in Worten.",
		fig{"Hell, 1024 x 768", l, "", "", 1024, 768, filled}, fig{"Dunkel, 1024 x 768", d, "", "", 1024, 768, filled}, fig{"Schwarz-Weiß, 1024 x 768", bw, "", "", 1024, 768, filled},
		fig{"Hell, 375 x 812", l, "", "", 375, 812, filled}, fig{"Hell, 375 x 812 (gescrollt)", l, "", "#add", 375, 812, filled})
	section("11. Vorhandene Aufgabe von Mia übernehmen", "Nur Aufgaben ohne Routine stehen zur Wahl.",
		fig{"Hell, 1024 x 768 (gescrollt)", l, "", "#add", 1024, 768, existing}, fig{"Hell, 375 x 812 (gescrollt)", l, "", "#add", 375, 812, existing})
	section("12. Nach „Hoch“: Erfolgszeile nennt die neue Stelle", "", fig{"Hell, 1024 x 768", l, "", "", 1024, 768, movedDoc}, fig{"Schwarz-Weiß, 375 x 812", bw, "", "", 375, 812, movedDoc})
	section("13. Fehler am Feld: Titel fehlt", "", fig{"Hell, 1024 x 768 (gescrollt)", l, "", "#add", 1024, 768, rErrDoc}, fig{"Hell, 375 x 812 (gescrollt)", l, "", "#add", 375, 812, rErrDoc})
	section("14. Routine auflösen fragt zuerst und nennt jede Aufgabe", "Auflösen löscht keine Aufgabe.",
		fig{"Hell, 1024 x 768 (gescrollt)", l, "", "details.danger", 1024, 768, danger}, fig{"Hell, 375 x 812 (gescrollt)", l, "", "details.danger", 375, 812, danger})
	section("15. Aufgabe in einer Routine (Detailseite)", "Ein Kasten sagt, zu welcher Routine die Aufgabe gehört; „Aus der Routine nehmen“ sagt vorher, was passiert.",
		fig{"Hell, 1024 x 768", l, "", "", 1024, 768, taskDoc}, fig{"Schwarz-Weiß, 768 x 1024", bw, "", "", 768, 1024, taskDoc}, fig{"Hell, 375 x 812", l, "", "", 375, 812, taskDoc})
	section("16. Kinderhandy: Mias Aufgaben-Seite", "Dieselben großen Kästchen und Routinen-Kästen wie auf dem Display.",
		fig{"Hell, 375 x 812", l, "", "", 375, 812, childDoc}, fig{"Schwarz-Weiß, 375 x 812 (gescrollt)", bw, "", ".part[data-part='evening']", 375, 812, childDoc}, fig{"Hell, 768 x 1024", l, "", "", 768, 1024, childDoc})

	var tpl strings.Builder
	for _, id := range order {
		fmt.Fprintf(&tpl, `<template id="doc-%s">%s</template>`+"\n", id, docs[id])
	}
	doc := `<!doctype html>
<!-- Generated by TestWriteDesignPreview91 from the real templates (ticket 7.2, #91). The stylesheet and font are inlined copies of web/static and do not follow later changes. -->
<html lang="de"><head><meta charset="utf-8"><title>Hejmo preview 7.2 routines</title>
<style>body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:980px}.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}iframe{border:0;background:#fff;display:block}</style></head><body>
<h1>Hejmo 7.2 Routinen, aus den echten Vorlagen gerendert (Beispielfamilie: Mama, Papa, Sebastian, Mia), Montag 16/11/2026: 1024 x 768, Hochformat 768 x 1024 und Handy 375 px, hell, dunkel und schwarz-weiß</h1>
<noscript><p>Diese Seite braucht JavaScript nur, um die Seiten in ihre Rahmen zu setzen.</p></noscript>
` + body.String() + `
<script type="text/plain" id="css">` + string(css) + `</script>
<script type="text/plain" id="font">` + base64.StdEncoding.EncodeToString(font) + `</script>
` + tpl.String() + `<script>
(function () {
  var font = Uint8Array.from(atob(document.getElementById("font").textContent.trim()), function (c) { return c.charCodeAt(0); });
  var fontUrl = URL.createObjectURL(new Blob([font], { type: "font/ttf" }));
  var css = document.getElementById("css").textContent.replace("/static/fonts/Figtree-wght.ttf", fontUrl);
  document.querySelectorAll("iframe[data-doc]").forEach(function (f) {
    var inner = document.getElementById("doc-" + f.getAttribute("data-doc")).innerHTML;
    f.addEventListener("load", function () { var a = f.getAttribute("data-anchor"); var el = a && f.contentDocument.querySelector(a); if (el) { var x = window.scrollX, y = window.scrollY; el.scrollIntoView(); window.scrollTo(x, y); } });
    f.srcdoc = '<!doctype html><html lang="' + f.getAttribute("data-lang") + '" data-theme="' + f.getAttribute("data-theme") + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + css + '</style></head>' + inner + '</html>';
  });
})();
</script>
</body></html>
`
	if err := os.WriteFile(out, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}
