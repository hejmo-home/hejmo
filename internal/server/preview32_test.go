package server_test

import (
	"encoding/base64"
	"fmt"
	html_ "html"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/web"
)

// TestWritePreview32 writes the self-contained preview page for the product
// owner's approval of ticket 6.1 (lists in the web interface). Every frame is
// a real page of the box, rendered by the real templates for the example
// family (Mama, Papa, Sebastian, Mia); nothing is hand-written HTML.
// Run: HEJMO_WRITE_PREVIEW_32=../../docs/design/preview-32.html go test ./internal/server -run WritePreview32
func TestWritePreview32(t *testing.T) {
	out := os.Getenv("HEJMO_WRITE_PREVIEW_32")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_32 to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	js, _ := web.Static.ReadFile("static/form.js")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")

	cp := newChildPhone(t)
	b := cp.parent.b
	h, _ := b.db.HomeHousehold()
	lists, _ := b.db.Lists(h.ID)
	shopping, todos := lists[0], lists[1]
	shopping.Name = "Shopping"
	_, _ = b.db.UpdateList(h.ID, shopping)
	entryIDs := map[string]string{}
	add := func(l list.List, done map[string]bool, texts ...string) {
		for _, s := range texts {
			e, err := b.db.AddEntry(h.ID, l.ID, s)
			if err != nil {
				t.Fatal(err)
			}
			entryIDs[s] = e.ID
			if done[s] {
				_ = b.db.SetEntryDone(h.ID, l.ID, e.ID, true)
			}
		}
	}
	add(shopping, map[string]bool{"Oat milk": true, "Football socks for Sebastian": true},
		"Oat milk", "Bananas", "Football socks for Sebastian", "Coffee for Papa", "Birthday card for Mama",
		"Sunscreen for sensitive children's skin, factor 50, the big bottle")
	add(todos, nil, "Book the dentist for Mia", "Pack the swimming bag", "Call the plumber")
	packing, _ := b.db.CreateList(h.ID, list.List{Name: "Holiday packing", Color: "plum", Symbol: "triangle-filled", Sort: list.Alphabetical})
	add(packing, map[string]bool{"Sun hats": true}, "Swimsuits", "Sun hats", "Passports", "Charger", "Ärmelschoner")
	// A list name holds 60 characters at most (list.MaxName): this one is as long as it gets, to show wrapping.
	party, err := b.db.CreateList(h.ID, list.List{Name: "Sebastian's birthday party: things to borrow from neighbours", Color: "terracotta", Symbol: "circle-filled", Sort: list.Manual})
	partyDone := map[string]bool{}
	partyTexts := []string{"Trestle table", "Folding chairs", "Paddling pool", "Fairy lights", "Cool box"}
	for _, s := range partyTexts {
		partyDone[s] = true
	}
	add(party, partyDone, partyTexts...)
	if err != nil {
		t.Fatal(err)
	}
	gifts, _ := b.db.CreateList(h.ID, list.List{Name: "Gift ideas", Color: "ochre", Symbol: "diamond-filled", Sort: list.Manual})

	page := func(c interface{ get(string) resp }, path string) string {
		r := c.get(path)
		if r.status != http.StatusOK && r.status != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d", path, r.status)
		}
		return r.body
	}
	parent := enGetter{b}
	child := screenGetter{cp.screen}
	open := func(s, marker string) string {
		return strings.Replace(s, marker, strings.Replace(marker, "<details", "<details open", 1), 1)
	}

	type frame struct {
		theme string
		w, h  int
	}
	desk := func(themes ...string) []frame {
		var fs []frame
		for _, th := range themes {
			fs = append(fs, frame{th, 1024, 768})
		}
		return fs
	}
	portrait := []frame{{"light", 768, 1024}, {"bw", 768, 1024}}
	phone := func(themes ...string) []frame {
		var fs []frame
		for _, th := range themes {
			fs = append(fs, frame{th, 375, 812})
		}
		return fs
	}
	all := func() []frame {
		return append(append(desk("light", "dark", "bw"), portrait...), phone("light", "bw")...)
	}
	var body strings.Builder
	var docs []string
	section := func(title, note, anchor, html string, frames ...frame) {
		idx := len(docs)
		docs = append(docs, html)
		fmt.Fprintf(&body, "<h2>%s</h2><p class=\"note\">%s</p><div class=\"row\">\n", title, note)
		for _, f := range frames {
			label := map[string]string{"light": "Light", "dark": "Dark", "bw": "Black and white"}[f.theme]
			fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" data-anchor="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", label, f.w, f.h, idx, f.theme, anchor, f.w, f.h, html_.EscapeString(title))
		}
		body.WriteString("</div>\n")
	}

	_ = page(parent, "/lists") // shows and clears the success line left over from pairing Mia's phone
	if r := b.post("/lists/"+shopping.ID+"/entries", url.Values{"text": {"Pasta"}}); r.status != http.StatusSeeOther {
		t.Fatalf("add = %d", r.status)
	}
	detail := open(page(parent, "/lists/"+shopping.ID), `<details class="danger">`) // shows the success line of the add
	overview := page(parent, "/lists")

	section("1. Lists overview", "Lists is an everyday screen between Tasks and Settings. Each list is one calm row that opens it, with its state in words (\"All 5 ticked off\", \"No entries yet\"); a long name wraps. In black and white the symbol replaces the colour. Adding a list sits below, with its choices under More options; the summary on the closed disclosure says what is chosen.", "",
		overview, all()...)
	section("2. A list sorted by hand: Shopping, right after adding an entry", "One add row on top (the only primary action), a success line naming the entry, and the field ready for the next one. Open entries come first; ticked-off entries move into their own group below and keep their place in the order, so unticking puts them back. Deleting the ticked-off entries says how many and asks first (open here). Name, colour, symbol and order sit under one disclosure; deleting the list is last.", "",
		detail, all()...)

	moved := func(direction string) {
		if r := b.post("/lists/"+shopping.ID+"/entries/"+entryIDs["Bananas"]+"/move", url.Values{"direction": {direction}}); r.status != http.StatusSeeOther {
			t.Fatalf("move %s = %d", direction, r.status)
		}
		_ = page(parent, "/lists/"+shopping.ID+"?edit=1")
	}
	moved("down") // so that the frame can show "Bananas" moved up
	if r := b.post("/lists/"+shopping.ID+"/entries/"+entryIDs["Bananas"]+"/move", url.Values{"direction": {"up"}}); r.status != http.StatusSeeOther {
		t.Fatalf("move up = %d", r.status)
	}
	section("3. Sort or remove entries", "Moving and removing are rare, so they leave the everyday rows: \"Sort or remove entries\" opens this view. Buttons say what they do in words. Up and down count only the open entries, so the first open entry has no Up. A single entry is removed straight away with a success line (ADR 0015 lets displays do this without the PIN). Done goes back to the list.", "",
		page(parent, "/lists/"+shopping.ID+"?edit=1"), append(desk("light", "bw"), phone("light")...)...)

	section("4. A list sorted A to Z: Holiday packing", "A new entry appears at its alphabetical place (ignoring case, with ä like a). There is nothing to sort by hand, so the link only says \"Remove entries\".", "",
		page(parent, "/lists/"+packing.ID), desk("light", "bw")...)
	section("5. Ticked-off entries hidden", "\"Hide\" on the group hides the ticked-off entries; the page says how many are hidden and how to show them. The choice stays while ticking, adding and removing.", "",
		page(parent, "/lists/"+shopping.ID+"?ticked=hide"), append(desk("light", "bw"), phone("light")...)...)

	newForm := strings.Replace(page(parent, "/lists"), `<details class="more">`, `<details class="more" open>`, 1)
	section("6. New list: colour, symbol and order under More options", "Defaults are the next free colour and symbol. Colour and symbol are named, not only shown; in black and white the colour chips show only their names, because every colour prints black there.", "#new",
		newForm, append(desk("light", "dark", "bw"), phone("light")...)...)

	failed := b.do(http.MethodPost, "/lists", url.Values{"name": {""}, "color": {"teal"}, "symbol": {"square-filled"}, "sort": {"alphabetical"}}, map[string]string{"Accept-Language": "en"})
	if failed.status != http.StatusUnprocessableEntity {
		t.Fatalf("failed create = %d", failed.status)
	}
	section("7. Error at the name", "The error is under the field. The chosen colour, symbol and order stay, and the closed More options says so (\"Teal, filled square, A to Z\").", "#new",
		failed.body, append(desk("light", "bw"), phone("light")...)...)

	section("8. Mia's phone", "A child reads and ticks off; there is no add row, no sorting, removing or deleting, and no Settings in the navigation. The large boxes work for children who cannot read well yet.", "",
		page(child, "/lists/"+shopping.ID), append(phone("light", "bw"), desk("light")...)...)

	section("9. An empty list", "What to do next, in one sentence.", "",
		page(parent, "/lists/"+gifts.ID), append(desk("light"), phone("light")...)...)

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
<!-- Generated by TestWritePreview32 from the box's real pages and templates (ticket 6.1, with the design pass of the Opus designer). The stylesheet, script and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 6.1 lists</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 6.1 lists: preview with the example family (Mama, Papa, Sebastian, Mia): 1024 x 768, portrait and 375 px phone, light, dark and black and white</h1>
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

type enGetter struct{ b *box }

func (g enGetter) get(path string) resp { return getEN(g.b, path) }

type screenGetter struct{ s *screen }

func (g screenGetter) get(path string) resp { return g.s.get(path, "en") }
