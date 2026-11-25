package server_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/starter"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 280 (preview-280.html, ADR 0021): own photos for recipes. Parents add them on the recipe page, the first
// is the cover, children see but cannot change, lists load a small file only, the files lie on the box.

// rphUpload posts photos to a recipe as the page does without script: several files in the part "file".
func rphUpload(b *box, path string, files ...[]byte) resp {
	b.t.Helper()
	m := map[string][]byte{}
	for i, f := range files {
		m[fmt.Sprintf("IMG_%02d.jpg", i)] = f
	}
	return b.postFiles(path, nil, m)
}

var rphIDRe = regexp.MustCompile(`/photos/([0-9a-f-]{36})`)

func rphIDs(page string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range rphIDRe.FindAllStringSubmatch(page, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func (rb recipeBox) photos(t *testing.T, ref string) []store.RecipePhoto {
	t.Helper()
	list, err := rb.b.db.RecipePhotos(rb.hid, ref)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func (rb recipeBox) addPhotos(t *testing.T, ref string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		r := rphUpload(rb.b, "/meals/recipes/"+ref+"/photos", jpegPhoto(t, 800+i, 600))
		if r.status != http.StatusSeeOther {
			t.Fatalf("upload %d = %d\n%s", i, r.status, rcpShown(r.body))
		}
	}
}

func TestTheFirstPhotoBecomesTheCoverAndSavesAtOnceWithASuccessLine(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	up := rphUpload(rb.b, "/meals/recipes/"+r.ID+"/photos", jpegPhoto(t, 2400, 1800))
	if up.status != http.StatusSeeOther || !strings.HasPrefix(up.location, "/meals/recipes/"+r.ID+"/photos") {
		t.Fatalf("upload = %d %s", up.status, up.location)
	}
	page := rb.b.do(http.MethodGet, up.location, nil, rcpDE)
	rcpHas(t, "photos page", page.body, "1 Foto hinzugefügt, verkleinert, Aufnahmeort und Kameradaten entfernt. Es ist jetzt das Titelbild.",
		`class="rph-card is-cover is-new"`, "Steht oben im Rezept und in der Rezeptliste.", "Hinzugefügt von Mama am", `<span class="tag">Neu</span>`, "Danach zeigt das Rezept wieder das Symbol seiner Kategorie.")
	rb.addPhotos(t, r.ID, 1)
	again := rphUpload(rb.b, "/meals/recipes/"+r.ID+"/photos", jpegPhoto(t, 600, 800), jpegPhoto(t, 700, 500))
	page = rb.b.do(http.MethodGet, again.location, nil, rcpDE)
	rcpHas(t, "second upload", page.body, "2 Fotos hinzugefügt, verkleinert, Aufnahmeort und Kameradaten entfernt. Das Titelbild bleibt, wie es war.", "4 Fotos")
	list := rb.photos(t, r.ID)
	if len(list) != 4 || !list[0].IsCover || list[0].Width != 1600 || list[0].Height != 1200 {
		t.Errorf("photos = %+v: the first stays the cover, shrunk to 1600", list)
	}
}

func TestTheRecipePageShowsTheCoverInsteadOfTheSymbolAndTheGridOfAllPhotos(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	plain := rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE)
	rcpHas(t, "without photos", plain.body, `<span class="art-sym big"`, `href="#fotos">`, "Foto hinzufügen", `class="rph-none"`, `class="rph-add"`, `capture="environment"`, "Fotos auswählen", "Aus der Galerie wählen")
	rcpLacks(t, "without photos", plain.body, "rcp-cover", "Titelbild wählen oder Fotos entfernen")
	rb.addPhotos(t, r.ID, 1)
	one := rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE)
	rcpHas(t, "one photo", one.body, `class="rcp-head has-cover"`, `class="rcp-cover"`, "Weiteres Foto hinzufügen", "Titelbild wählen oder Fotos entfernen", `<span class="tag cover-tag">Titelbild</span>`,
		"JPEG, PNG oder HEIC, bis 15 MB je Foto, höchstens 12 Fotos pro Rezept", `alt="Foto von „Erdnuss-Curry mit Süßkartoffeln“, hinzugefügt von Mama am`)
	rcpLacks(t, "one photo", one.body, `<span class="art-sym big"`, "Alle 1 Fotos")
	rb.addPhotos(t, r.ID, 2)
	three := rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE)
	rcpHas(t, "three photos", three.body, "Alle 3 Fotos ansehen", "Tipp auf ein Foto, dann siehst du es groß.")
	grid := rcpBetween(t, three.body, `<ul class="rph-grid">`, "</ul>")
	if n := strings.Count(grid, `loading="lazy"`); n != 3 || !strings.Contains(grid, "?size=medium") || strings.Count(grid, "<small class=\"by\">") != 3 {
		t.Errorf("grid = %s", grid)
	}
	if order := strings.Index(three.body, `id="tip"`); order < 0 || order > strings.Index(three.body, `id="fotos"`) {
		t.Errorf("the section Fotos comes after the tips")
	}
}

func TestChildrenSeeThePhotosButCannotAddOrRemoveAnyAndGetNoPhotosPage(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	plain := rb.child.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE)
	rcpLacks(t, "child, no photos", plain.body, `id="fotos"`, "Foto hinzufügen", "rph-add")
	rb.addPhotos(t, r.ID, 2)
	ph := rb.photos(t, r.ID)
	page := rb.child.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE)
	rcpHas(t, "child", page.body, `class="rcp-cover"`, "Alle 2 Fotos ansehen", `class="rph-grid"`)
	rcpLacks(t, "child", page.body, "rph-add", `type="file"`, "rph-manage", "Foto hinzufügen", "Weiteres Foto")
	base := "/meals/recipes/" + r.ID + "/photos"
	for _, c := range []struct {
		name string
		got  resp
	}{
		{"photos page", rb.child.get(base)},
		{"upload", rphUpload(rb.child, base, jpegPhoto(t, 100, 100))},
		{"cover", rb.child.post(base+"/"+ph[1].ID+"/cover", url.Values{})},
		{"delete", rb.child.post(base+"/"+ph[1].ID+"/delete", url.Values{})},
	} {
		if c.got.status != http.StatusForbidden && c.got.status != http.StatusNotFound {
			t.Errorf("child %s = %d, want 403 or 404", c.name, c.got.status)
		}
	}
	if len(rb.photos(t, r.ID)) != 2 || !rb.photos(t, r.ID)[0].IsCover {
		t.Error("a child changed the photos")
	}
	for _, size := range []string{"", "?size=medium", "?size=small"} {
		if got := rb.child.get(base + "/" + ph[0].ID + size); got.status != http.StatusOK {
			t.Errorf("child reads the photo%s = %d", size, got.status)
		}
	}
}

func TestPhotoFilesAreJPEGsOfThreeSizesWithoutLocation(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	rb.addPhotos(t, r.ID, 1)
	ph := rb.photos(t, r.ID)[0]
	url := "/meals/recipes/" + r.ID + "/photos/" + ph.ID
	for size, want := range map[string]int{"": 800, "?size=medium": 640, "?size=small": 168} {
		res := rb.b.get(url + size)
		w, _ := jpegSide(t, []byte(res.body))
		if res.status != http.StatusOK || (size != "" && w != want) || bytes.Contains([]byte(res.body), []byte("GPS-")) {
			t.Errorf("size %q = %d, width %d", size, res.status, w)
		}
	}
	if res := rb.b.get(url + "?size=huge"); res.status != http.StatusBadRequest {
		t.Errorf("an unknown size = %d, want 400: openapi.yaml allows medium and small only", res.status)
	}
	files, _ := filepath.Glob(filepath.Join(rb.b.photoDir, "recipe-photos", ph.ID+"*.jpg"))
	if len(files) != 3 {
		t.Errorf("files on the box = %v: the photos are files, not blobs in SQLite", files)
	}
}

func TestListsLoadOnlyTheSmallFileAndLazily(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	pf := rb.add(t, recipe.Recipe{Name: "Pfannkuchen", Category: recipe.Breakfast})
	rb.addPhotos(t, r.ID, 2)
	list := rb.b.do(http.MethodGet, "/meals/recipes", nil, rcpDE).body
	rcpHas(t, "list", list, `<span class="art-sym rcp-pic" aria-hidden="true"><img src="/meals/recipes/`+r.ID+`/photos/`, "?size=small", `loading="lazy" decoding="async"`, `alt=""`)
	for _, src := range regexp.MustCompile(`src="(/meals/recipes/[^"]*)"`).FindAllStringSubmatch(list, -1) {
		if !strings.HasSuffix(src[1], "?size=small") {
			t.Errorf("the list loads %s: only the small size may be in a list", src[1])
		}
	}
	if !strings.Contains(list, `<span class="art-sym rcp-pic" aria-hidden="true">🥣</span>`) {
		t.Errorf("a recipe without photo keeps its symbol in the same box (%s)", pf.ID)
	}
	if strings.Contains(rb.child.do(http.MethodGet, "/meals/recipes", nil, rcpDE).body, "?size=small") == false {
		t.Error("children see the covers in the list")
	}
	st := rb.b.do(http.MethodGet, "/meals/recipes/starter", nil, rcpDE).body
	rcpHas(t, "starter list", st, "rcp-pic")
}

func TestChoosingTheCoverAndRemovingItFollowTheRule(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	rb.addPhotos(t, r.ID, 3)
	base := "/meals/recipes/" + r.ID + "/photos"
	ph := rb.photos(t, r.ID) // cover first, then newest first
	page := rb.b.get(base).body
	rcpHas(t, "page", page, "Als Titelbild nehmen", "Neues Titelbild wird das nächste Foto: von Mama,", "Ja, dieses Foto entfernen", "Nein, nichts ändern")
	cov := rb.b.post(base+"/"+ph[2].ID+"/cover", url.Values{})
	if cov.status != http.StatusSeeOther {
		t.Fatalf("cover = %d", cov.status)
	}
	rcpHas(t, "after cover", rb.b.do(http.MethodGet, cov.location, nil, rcpDE).body, "Neues Titelbild gespeichert.")
	if now := rb.photos(t, r.ID); !now[0].IsCover || now[0].ID != ph[2].ID {
		t.Fatalf("cover = %+v", now[0])
	}
	del := rb.b.post(base+"/"+ph[2].ID+"/delete", url.Values{})
	rcpHas(t, "after removing the cover", rb.b.do(http.MethodGet, del.location, nil, rcpDE).body, "Foto entfernt. Neues Titelbild: das Foto von Mama vom ")
	left := rb.photos(t, r.ID)
	if len(left) != 2 || !left[0].IsCover || left[0].ID != ph[1].ID {
		t.Errorf("the newest remaining photo is the cover: %+v", left)
	}
	if _, err := os.Stat(filepath.Join(rb.b.photoDir, "recipe-photos", ph[2].ID+".jpg")); !os.IsNotExist(err) {
		t.Errorf("the file of a removed photo stays: %v", err)
	}
	for _, p := range left {
		rb.b.post(base+"/"+p.ID+"/delete", url.Values{})
	}
	gone := rb.b.do(http.MethodGet, base, nil, rcpDE)
	rcpHas(t, "no photo left", gone.body, "Noch kein Foto. Das Rezept zeigt das Symbol seiner Kategorie, bis ihr eins hinzufügt.")
	if rb.b.post(base+"/nope/delete", url.Values{}).status != http.StatusNotFound {
		t.Error("an unknown photo is not found")
	}
}

func TestAtMostTwelvePhotosAndTheFullPageSaysSo(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	rb.addPhotos(t, r.ID, 11)
	over := rphUpload(rb.b, "/meals/recipes/"+r.ID+"/photos", jpegPhoto(t, 300, 200), jpegPhoto(t, 310, 200))
	if over.status != http.StatusOK {
		t.Fatalf("12th and 13th = %d", over.status)
	}
	rcpHas(t, "refusal", over.body, "1 Foto nicht hinzugefügt:", "das Rezept hat schon 12 Fotos", "1 Foto hinzugefügt, verkleinert", "12 Fotos")
	if len(rb.photos(t, r.ID)) != 12 {
		t.Errorf("photos = %d", len(rb.photos(t, r.ID)))
	}
	page := rb.b.get("/meals/recipes/" + r.ID + "/photos").body
	rcpHas(t, "full page", page, "12 Fotos, mehr gehen nicht. Entferne unten eins", `aria-disabled="true"`)
	if strings.Contains(rcpBetween(t, page, `<section class="upload`, "</section>"), `type="file"`) {
		t.Error("the full page still has file inputs")
	}
	rec := rb.b.get("/meals/recipes/" + r.ID).body
	rcpHas(t, "full recipe page", rec, `class="rph-full"`, "Alle 12 Fotos ansehen")
	rcpHas(t, "delete box", rec, "Auch die 12 Fotos verschwinden.")
}

func TestFilesThatCannotBeUsedAreNamedWithReasonAndWhatWorkedIsSaved(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	base := "/meals/recipes/" + r.ID + "/photos"
	pdf := rb.b.postFiles(base, nil, map[string][]byte{"Einkaufszettel.pdf": []byte("%PDF-1.4 hello"), "ok.jpg": jpegPhoto(t, 400, 300),
		"IMG_9.HEIC": append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'h', 'e', 'i', 'c'}, make([]byte, 40)...)})
	if pdf.status != http.StatusOK {
		t.Fatalf("status = %d", pdf.status)
	}
	rcpHas(t, "partial", pdf.body, `class="refused"`, "2 Fotos nicht hinzugefügt:", "<b>„Einkaufszettel.pdf“</b>: kein Foto. Hejmo nimmt JPEG, PNG oder HEIC.", "„IMG_9.HEIC“", "1 Foto hinzugefügt, verkleinert", "Es ist jetzt das Titelbild.")
	big := rb.b.postFiles(base, nil, map[string][]byte{"IMG_4471.jpg": append(jpegPhoto(t, 50, 50), make([]byte, 16<<20)...)})
	if big.status != http.StatusUnprocessableEntity {
		t.Fatalf("too big = %d", big.status)
	}
	rcpHas(t, "too big", big.body, "<b>„IMG_4471.jpg“</b>: 16,0 MB, größer als 15 MB. Verkleinere es auf dem Gerät und versuch es noch einmal.")
	if len(rb.photos(t, r.ID)) != 1 {
		t.Error("a refused file was stored")
	}
	if none := rb.b.postFiles(base, nil, nil); none.status != http.StatusUnprocessableEntity {
		t.Errorf("no file = %d", none.status)
	}
}

func TestStarterRecipesGetPhotosOfTheirOwnPerHousehold(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	ref := starter.All()[0].Recipe("de").ID
	if !strings.HasPrefix(ref, "starter-") {
		t.Fatalf("starter id = %q", ref)
	}
	rb.addPhotos(t, ref, 1)
	page := rb.b.do(http.MethodGet, "/meals/recipes/"+ref, nil, rcpDE).body
	rcpHas(t, "starter page", page, "Ein Startrezept von Hejmo. Es lässt sich nicht ändern. Eigene Fotos kannst du trotzdem hinzufügen; sie bleiben bei euch.", `class="rcp-cover"`, `class="rph-add"`)
	rcpLacks(t, "starter page", page, "Rezept ändern", "Rezept löschen")
	child := rb.child.do(http.MethodGet, "/meals/recipes/"+ref, nil, rcpDE).body
	rcpHas(t, "starter page, child", child, `class="rcp-cover"`, "Ein Startrezept von Hejmo. Es lässt sich nicht ändern.")
	rcpLacks(t, "starter page, child", child, "Eigene Fotos kannst du", "rph-add")
	if rb.b.get("/meals/recipes/"+ref+"/photos").status != http.StatusOK {
		t.Error("the photos page of a starter recipe")
	}
	rcpHas(t, "starter list", rb.b.get("/meals/recipes/starter").body, "?size=small")
	if got := rphUpload(rb.b, "/meals/recipes/starter-does-not-exist/photos", jpegPhoto(t, 90, 90)); got.status != http.StatusNotFound {
		t.Errorf("an unknown starter recipe = %d", got.status)
	}
}

func TestDeletingARecipeDeletesItsPhotosAndFilesAndTheDangerBoxSaysSo(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	rb.addPhotos(t, r.ID, 1)
	rcpHas(t, "danger box", rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE).body, "Auch das Foto verschwindet.")
	rb.addPhotos(t, r.ID, 1)
	rcpHas(t, "danger box", rb.b.do(http.MethodGet, "/meals/recipes/"+r.ID, nil, rcpDE).body, "Auch die 2 Fotos verschwinden.")
	ids := []string{}
	for _, p := range rb.photos(t, r.ID) {
		ids = append(ids, p.ID)
	}
	if got := rb.b.post("/meals/recipes/"+r.ID+"/delete", url.Values{}); got.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", got.status)
	}
	if len(rb.photos(t, r.ID)) != 0 {
		t.Error("the rows stay")
	}
	left, _ := filepath.Glob(filepath.Join(rb.b.photoDir, "recipe-photos", "*"))
	if len(left) != 0 {
		t.Errorf("files stay on the box: %v (%v)", left, ids)
	}
}

func TestThePlanBoxAndSuggestionsShowTheCoverInTheirSymbolBox(t *testing.T) {
	t.Parallel()
	rb := newRecipeBox(t)
	r := rb.curry(t)
	rb.addPhotos(t, r.ID, 1)
	sugg := rb.b.do(http.MethodGet, "/meals/suggest?q=Erdnuss&mealtime=dinner", nil, rcpDE)
	if sugg.status != http.StatusOK || !strings.Contains(sugg.body, `<span class="art-sym" aria-hidden="true"><img src="/meals/recipes/`+r.ID+`/photos/`) || !strings.Contains(sugg.body, "?size=small") {
		t.Errorf("suggestion = %d without the cover:\n%s", sugg.status, rcpShown(sugg.body))
	}
}

// ---- the REST API, contract-checked

func apiMultipart(t *testing.T, files map[string][]byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, data := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
		h.Set("Content-Type", "application/octet-stream")
		part, _ := mw.CreatePart(h)
		_, _ = part.Write(data)
	}
	_ = mw.Close()
	return mw.FormDataContentType(), buf.Bytes()
}

func TestRecipePhotosOverTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	id := r["id"].(string)
	ct, body := apiMultipart(t, map[string][]byte{"a.jpg": jpegPhoto(t, 1000, 700)})
	raw, status, _ := c.send(http.MethodPost, "/recipes/"+id+"/photos", ct, body)
	if status != http.StatusCreated || !strings.Contains(raw, `"is_cover":true`) {
		t.Fatalf("upload = %d %s", status, raw)
	}
	ct, body = apiMultipart(t, map[string][]byte{"b.jpg": jpegPhoto(t, 500, 700), "list.pdf": []byte("%PDF-1.4")})
	raw, status, _ = c.send(http.MethodPost, "/recipes/"+id+"/photos", ct, body)
	if status != http.StatusCreated || !strings.Contains(raw, `"refused":[{"name":"list.pdf"`) {
		t.Fatalf("second upload = %d %s", status, raw)
	}
	status, _, list := c.call(http.MethodGet, "/recipes/"+id+"/photos", nil)
	if status != http.StatusOK || len(list) != 2 || list[0].(map[string]any)["is_cover"] != true {
		t.Fatalf("list = %d %v", status, list)
	}
	second := list[1].(map[string]any)["id"].(string)
	if _, got, _ := c.call(http.MethodPost, "/recipes/"+id+"/photos/"+second+"/cover", nil); got["is_cover"] != true {
		t.Errorf("cover = %v", got)
	}
	img, st, ctype := c.callRaw(http.MethodGet, "/recipes/"+id+"/photos/"+second+"/image?size=small", nil)
	if st != http.StatusOK || ctype != "image/jpeg" || len(img) == 0 {
		t.Errorf("image = %d %s", st, ctype)
	}
	if body, st, ctype := c.callRaw(http.MethodGet, "/recipes/"+id+"/photos/"+second+"/image?size=huge", nil); st != http.StatusBadRequest || ctype != "application/json" || !strings.Contains(body, `"error"`) {
		t.Errorf("an unknown size = %d %s %q, want 400 with the JSON error of openapi.yaml", st, ctype, body)
	}
	if status, _, _ := c.call(http.MethodDelete, "/recipes/"+id+"/photos/"+second, nil); status != http.StatusNoContent {
		t.Errorf("delete = %d", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/recipes/"+id+"/photos/"+second, nil); status != http.StatusNotFound {
		t.Errorf("delete twice = %d", status)
	}
	ct, body = apiMultipart(t, map[string][]byte{"x.pdf": []byte("%PDF-1.4")})
	if _, status, _ = c.send(http.MethodPost, "/recipes/"+id+"/photos", ct, body); status != http.StatusUnprocessableEntity {
		t.Errorf("only a PDF = %d", status)
	}
	if _, status, _ = c.send(http.MethodPost, "/recipes/nope/photos", ct, body); status != http.StatusNotFound {
		t.Errorf("unknown recipe = %d", status)
	}
	ct, body = apiMultipart(t, map[string][]byte{"s.jpg": jpegPhoto(t, 200, 200)})
	if _, status, _ = c.send(http.MethodPost, "/recipes/starter-"+strings.TrimPrefix(starter.All()[0].Recipe("en").ID, "starter-")+"/photos", ct, body); status != http.StatusCreated {
		t.Errorf("starter recipe photo = %d", status)
	}
}

func TestChildrenReadRecipePhotosOverTheAPIButCannotChangeThem(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, r := createRecipe(t, c, curryBody())
	id := r["id"].(string)
	ct, body := apiMultipart(t, map[string][]byte{"a.jpg": jpegPhoto(t, 300, 300)})
	raw, _, _ := c.send(http.MethodPost, "/recipes/"+id+"/photos", ct, body)
	pid := regexp.MustCompile(`"id":"([^"]+)"`).FindStringSubmatch(raw)[1]
	kid := c.withToken(c.newToken("meals:read"))
	if status, _, list := kid.call(http.MethodGet, "/recipes/"+id+"/photos", nil); status != http.StatusOK || len(list) != 1 {
		t.Errorf("read = %d %v", status, list)
	}
	if _, status, _ := kid.callRaw(http.MethodGet, "/recipes/"+id+"/photos/"+pid+"/image", nil); status != http.StatusOK {
		t.Errorf("image = %d", status)
	}
	if _, status, _ := kid.send(http.MethodPost, "/recipes/"+id+"/photos", ct, body); status != http.StatusForbidden {
		t.Errorf("add = %d", status)
	}
	if status, _, _ := kid.call(http.MethodPost, "/recipes/"+id+"/photos/"+pid+"/cover", nil); status != http.StatusForbidden {
		t.Errorf("cover = %d", status)
	}
	if status, _, _ := kid.call(http.MethodDelete, "/recipes/"+id+"/photos/"+pid, nil); status != http.StatusForbidden {
		t.Errorf("delete = %d", status)
	}
}
