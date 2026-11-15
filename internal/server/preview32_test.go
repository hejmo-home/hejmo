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
	add := func(l list.List, done map[string]bool, texts ...string) {
		for _, s := range texts {
			e, _ := b.db.AddEntry(h.ID, l.ID, s)
			if done[s] {
				_ = b.db.SetEntryDone(h.ID, l.ID, e.ID, true)
			}
		}
	}
	add(shopping, map[string]bool{"Oat milk": true, "Football socks for Sebastian": true},
		"Oat milk", "Bananas", "Football socks for Sebastian", "Coffee for Papa", "Birthday card for Mama", "Pasta")
	add(todos, nil, "Book the dentist for Mia", "Pack the swimming bag", "Call the plumber")
	packing, _ := b.db.CreateList(h.ID, list.List{Name: "Holiday packing", Color: "plum", Symbol: "triangle-filled", Sort: list.Alphabetical})
	add(packing, map[string]bool{"Sun hats": true}, "Swimsuits", "Sun hats", "Passports", "Charger", "Ärmelschoner")
	_, _ = b.db.CreateList(h.ID, list.List{Name: "Gift ideas", Color: "ochre", Symbol: "diamond-filled", Sort: list.Manual})

	page := func(c interface{ get(string) resp }, path string) string {
		r := c.get(path)
		if r.status != http.StatusOK && r.status != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d", path, r.status)
		}
		return r.body
	}
	parent := enGetter{b}
	child := screenGetter{cp.screen}
	failed := b.do(http.MethodPost, "/lists", url.Values{"name": {""}, "color": {"teal"}, "symbol": {"square-filled"}, "sort": {"alphabetical"}}, map[string]string{"Accept-Language": "en"})
	if failed.status != http.StatusUnprocessableEntity {
		t.Fatalf("failed create = %d", failed.status)
	}
	open := func(s, marker string) string {
		return strings.Replace(s, marker, strings.Replace(marker, "<details", "<details open", 1), 1)
	}
	detail := page(parent, "/lists/"+shopping.ID)
	detail = open(detail, `<details class="danger">`)

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
	var body strings.Builder
	var docs []string
	section := func(title, note, anchor, html string, frames ...frame) {
		idx := len(docs)
		docs = append(docs, html)
		fmt.Fprintf(&body, "<h2>%s</h2><p class=\"note\">%s</p><div class=\"row\">\n", title, note)
		for _, f := range frames {
			label := map[string]string{"light": "Light", "dark": "Dark", "bw": "Black and white"}[f.theme]
			fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" data-anchor="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", label, f.w, f.h, idx, f.theme, anchor, f.w, f.h, title)
		}
		body.WriteString("</div>\n")
	}
	section("1. Lists overview", "Lists in the navigation between Tasks and Settings. Each list wears its colour; in black and white its symbol replaces the colour.", "",
		page(parent, "/lists"), append(desk("light", "dark", "bw"), portrait...)...)
	section("2. A manual list: Shopping", "Ticked entries are struck through and keep their place. Entries move up and down by hand; the add field and the settings are for parents. Completed entries can be hidden, or deleted behind a confirmation (open here).", "",
		detail, append(desk("light", "dark", "bw"), portrait...)...)
	section("3. An alphabetical list: Holiday packing", "A new entry appears at its alphabetical place (ignoring case, with ä like a). No move buttons.", "", page(parent, "/lists/"+packing.ID), desk("light", "bw")...)
	section("4. Completed entries hidden", "\"Hide completed entries\" is a link, so the choice needs no setting.", "", page(parent, "/lists/"+shopping.ID+"?completed=hide"), desk("light", "bw")...)
	newForm := strings.Replace(page(parent, "/lists"), `<details class="more">`, `<details class="more" open>`, 1)
	section("5. New list: colour, symbol and sorting under More options", "Defaults are the next free colour and symbol. Colour is named as well as shown; the symbol chooser shows the shapes in every theme.", "#new", newForm, desk("light", "dark", "bw")...)
	section("6. Error at the name", "The error is under the field; chosen colour, symbol and sorting stay (More options opens by itself when the error is inside it).", "#new", failed.body, desk("light", "bw")...)
	section("7. Mia's phone", "A child sees the lists and ticks entries off. There is no add field, no move, remove or delete.", "", page(child, "/lists/"+shopping.ID), desk("light", "bw")...)

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
<!-- Generated by TestWritePreview32 from the box's real pages and templates (ticket 6.1). The stylesheet, script and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="en"><head><meta charset="utf-8"><title>Hejmo preview 6.1 lists</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 6.1 lists: preview with the example family (Mama, Papa, Sebastian, Mia): 1024 x 768 and portrait, light, dark and black and white</h1>
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
