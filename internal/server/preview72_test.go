package server_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/web"
)

// previewRemote is a CalDAV server of the example family for the preview: it
// finds six calendars and counts events (or fails to, for one of them).
type previewRemote struct {
	calsync.Remote
	found  []caldavclient.Calendar
	counts map[string]int
}

func (p previewRemote) Calendars(context.Context) ([]caldavclient.Calendar, error) {
	return p.found, nil
}
func (p previewRemote) List(context.Context, string) ([]caldavclient.Object, error) { return nil, nil }
func (p previewRemote) Fetch(context.Context, string, []string) ([]caldavclient.Object, error) {
	return nil, nil
}
func (p previewRemote) CountEvents(_ context.Context, address string, _ time.Time) (int, error) {
	n, ok := p.counts[address]
	if !ok {
		return 0, context.DeadlineExceeded
	}
	return n, nil
}

var countCellRe = regexp.MustCompile(`<span class="hint count" data-count-for="([^"]*)" data-state="loading">[^<]*</span>`)

// TestWritePreview72 writes the self-contained preview page for the product
// owner's approval of ticket 5.17 (Choose calendars and calendar management).
// Every frame is a real page of the box, rendered by the real templates for the
// example family (Mama, Papa, Sebastian, Mia). The event counts, which the
// page loads after it is shown, are filled in from the real answer of the
// count endpoint, since the preview runs no scripts of the box.
// Run: HEJMO_WRITE_PREVIEW=../../docs/design/preview-72.html go test ./internal/server -run WritePreview72
func TestWritePreview72(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	js, _ := web.Static.ReadFile("static/form.js")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")

	b, ids := patternBox(t)
	h, _ := b.db.HomeHousehold()
	b.now = time.Date(2026, 11, 14, 20, 46, 26, 0, time.UTC) // 21:46:26 in Munich
	base := "https://caldav.example.org/mama/"
	cal := func(slug string) string { return base + slug + "/" }
	remote := previewRemote{
		found: []caldavclient.Calendar{
			{URL: cal("family"), Name: "Family"}, {URL: cal("work"), Name: "Mama's work"}, {URL: cal("school"), Name: "Sebastian's school"},
			{URL: cal("choir"), Name: "Choir"}, {URL: cal("piano"), Name: "Mia's piano"}, {URL: cal("swim"), Name: "Swimming club"},
		},
		counts: map[string]int{cal("family"): 38, cal("work"): 112, cal("school"): 24, cal("choir"): 12, cal("piano"): 0},
	}
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) { return remote, nil })
	if r := b.post("/connections", url.Values{"provider": {"caldav"}, "url": {"https://caldav.example.org"}, "username": {"mama@example.org"}, "password": {"preview-password-1"}}); r.status != http.StatusOK {
		t.Fatalf("connect = %d", r.status)
	}
	conns, _ := b.db.Connections(h.ID)
	connID := conns[0].ID
	form := url.Values{"interval": {"5"}, "shown": {cal("family"), cal("work"), cal("school"), cal("choir"), cal("piano"), cal("swim")},
		"calendar":              {cal("family"), cal("work"), cal("school")},
		"name_" + cal("family"): {"Family"}, "name_" + cal("work"): {"Mama's work"}, "name_" + cal("school"): {"Sebastian's school"},
		"people_" + cal("work"): {mamaID(t, b)}, "people_" + cal("school"): {ids["Sebastian"], ids["Mia"]},
	}
	if r := b.post("/connections/"+connID+"/calendars", form); r.status != http.StatusSeeOther {
		t.Fatalf("choose = %d\n%s", r.status, r.body)
	}
	synced, _ := b.db.SyncedCalendars(h.ID)
	runs := map[string][4]int{cal("family"): {38, 2, 1, 0}, cal("work"): {112, 0, 0, 0}, cal("school"): {24, 3, 0, 1}}
	for _, c := range synced {
		r := runs[c.URL]
		if _, err := b.db.SQL().Exec(`UPDATE calendar_sync SET last_success_at = ?, last_attempt_at = ?, last_run_events = ?, last_run_new = ?, last_run_changed = ?, last_run_removed = ? WHERE calendar_id = ?`,
			b.now.Format(time.RFC3339), b.now.Format(time.RFC3339), r[0], r[1], r[2], r[3], c.ID); err != nil {
			t.Fatal(err)
		}
	}
	school := synced[2]
	// The school calendar's last run failed at 21:46:26; its last success was three hours earlier.
	if _, err := b.db.SQL().Exec(`UPDATE calendar_sync SET last_success_at = ? WHERE calendar_id = ?`, b.now.Add(-3*time.Hour).Format(time.RFC3339), school.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.db.FailSync(h.ID, school.ID, i18n.Msg("sync.fail.fetch", "detail", "the server did not answer"), b.now, b.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	page := func(path string) string {
		r := getEN(b, path)
		if r.status != http.StatusOK && r.status != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d", path, r.status)
		}
		return r.body
	}
	// "Sync now": the real redirect carries the success line. The run's numbers
	// are those of the sample (the preview's fake server holds no events).
	if r := b.post("/calendars/"+synced[0].ID+"/sync", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("sync now = %d", r.status)
	}
	if _, err := b.db.SQL().Exec(`UPDATE calendar_sync SET last_run_events = 38, last_run_new = 2, last_run_changed = 1, last_run_removed = 0 WHERE calendar_id = ?`, synced[0].ID); err != nil {
		t.Fatal(err)
	}
	calendarsPage := page("/calendars")

	// Choose calendars with the counts as the endpoint answers them.
	counts := getEN(b, "/api/v1/calendar-connections/"+connID+"/event-counts")
	var answers []countJSON
	if err := json.Unmarshal([]byte(counts.body), &answers); err != nil || len(answers) != 6 {
		t.Fatalf("counts: %v\n%s", err, counts.body)
	}
	byURL := map[string]countJSON{}
	for _, a := range answers {
		byURL[a.URL] = a
	}
	choose := countCellRe.ReplaceAllStringFunc(page("/connections/"+connID), func(m string) string {
		a := byURL[countCellRe.FindStringSubmatch(m)[1]]
		return fmt.Sprintf(`<span class="hint count" data-count-for="%s" data-state="%s">%s</span>`, countCellRe.FindStringSubmatch(m)[1], a.State, a.Text)
	})
	// The page while the counts are still loading.
	loading := strings.ReplaceAll(page("/connections/"+connID), "The number of events is shown when JavaScript is on.", "Counting events…")

	off := url.Values{"interval": {"5"}, "shown": form["shown"], "calendar": {cal("family"), cal("school")}, "name_" + cal("family"): {"Family"}, "name_" + cal("school"): {"Sebastian's school"}}
	confirm := b.do(http.MethodPost, "/connections/"+connID+"/calendars", off, map[string]string{"Accept-Language": "en"})
	if confirm.status != http.StatusOK {
		t.Fatalf("confirmation = %d", confirm.status)
	}

	type frame struct {
		theme string
		w, h  int
	}
	// Design gate: every page is shown at 1280 px and at 375 px, light and dark.
	var frames []frame
	for _, size := range [][2]int{{1280, 800}, {375, 812}} {
		for _, th := range []string{"light", "dark"} {
			frames = append(frames, frame{th, size[0], size[1]})
		}
	}
	var body strings.Builder
	var docs []string
	docOf := map[string]int{}
	add := func(title, note, anchor, html string) {
		idx, seen := docOf[html]
		if !seen {
			idx = len(docs)
			docOf[html] = idx
			docs = append(docs, html)
		}
		fmt.Fprintf(&body, "<h2>%s</h2><p class=\"note\">%s</p><div class=\"row\">\n", title, note)
		anchor = strings.ReplaceAll(anchor, `"`, "&quot;")
		for _, f := range frames {
			label := map[string]string{"light": "Light", "dark": "Dark", "bw": "Black and white"}[f.theme]
			fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" data-anchor="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", label, f.w, f.h, idx, f.theme, anchor, f.w, f.h, title)
		}
		body.WriteString("</div>\n")
	}
	add("1. Choose calendars: one switch per calendar, with the event count",
		"Every found calendar has the same switch, the ones already synced too (Family, Mama's work, Sebastian's school are on). Under each name: how many events the calendar has in the last 12 months and later. The 2016 piano calendar shows \"No events in the last 12 months or later\" (highlighted), the swimming club's server did not answer in time (\"unknown\"). Name and audience (person chips, Whole family selected by default) stay editable; the colour is picked automatically and sits under More options.",
		"", choose)
	add("2. Choose calendars, scrolled to the calendars that are not synced yet",
		"Mia's piano has no event in the last 12 months or later (only one from 2016): the count says so, highlighted, before anyone switches it on. The swimming club's server did not answer in time: the count is unknown and the page works regardless.",
		`[data-count-for$="/piano/"]`, choose)
	add("3. Choose calendars while the counts load",
		"The page is shown at once; each count says \"Counting events\" until the server answers.", "", loading)
	add("4. Switching a synced calendar off asks first",
		"Mama's work was switched off. Nothing has been saved: the box says its events disappear from the box and the displays and that nothing is deleted at the source.", "", confirm.body)
	add("5. Calendars: Sync now shows a success line; rows show last sync with seconds, event count and the last run's changes",
		"After Sync now: time (21:46:26), event count and new, changed and removed. Calendars are grouped by person. The log below is called Sync problems and says where successful runs show.", "", calendarsPage)

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
<!-- Generated by TestWritePreview72 from the box's real pages and templates (ticket 5.17). The stylesheet, script and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 5.17 Choose calendars and calendar management</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 5.17 Choose calendars and calendar management: preview with the example family (Mama, Papa, Sebastian, Mia): 1280 px and 375 px, light and dark</h1>
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

// mamaID is the signed-in parent of the pattern box.
func mamaID(t *testing.T, b *box) string {
	t.Helper()
	h, _ := b.db.HomeHousehold()
	people, err := b.db.People(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range people {
		if p.Role == store.RoleParent && p.Name != "Papa" {
			return p.ID
		}
	}
	t.Fatal("no parent")
	return ""
}
