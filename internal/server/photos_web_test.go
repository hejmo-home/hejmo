package server_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// postFiles posts a multipart form like the photos page does without script.
func (b *box) postFiles(path string, fields map[string]string, files map[string][]byte) resp {
	b.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for name, data := range files {
		part, _ := mw.CreateFormFile("file", name)
		part.Write(data)
	}
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, b.srv.URL+path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", b.srv.URL)
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	var out bytes.Buffer
	out.ReadFrom(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), out.String()}
}

func photoBoxWith(t *testing.T) (*box, *apiClient) {
	t.Helper()
	c := signedInAPI(t)
	return c.b, c
}

var albumHrefRe = regexp.MustCompile(`/photos/albums/([0-9a-f-]{36})`)

func firstAlbumID(t *testing.T, page string) string {
	t.Helper()
	m := albumHrefRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no album link on the page:\n%s", page)
	}
	return m[1]
}

func TestSettingsHubOffersPhotosAfterDisplaysAndNamesItsState(t *testing.T) {
	b, c := photoBoxWith(t)
	hub := b.get("/settings").body
	if i, j := strings.Index(hub, `href="/displays"`), strings.Index(hub, `href="/photos"`); i < 0 || j < i || j > strings.Index(hub, `href="/phones"`) {
		t.Fatalf("the hub has the photos row at %d, displays at %d, want it after Displays and before Phones", j, i)
	}
	if !strings.Contains(hub, "Noch keine Fotos.") {
		t.Errorf("the hub row does not say that there are no photos yet:\n%s", hub)
	}
	c.upload(defaultAlbum(t, c), "a.jpg", jpegPhoto(t, 640, 480))
	newAlbum(t, c, "Urlaub")
	if hub := b.get("/settings").body; !strings.Contains(hub, "1 Foto in 2 Alben, 0 GB von 2 GB.") {
		t.Errorf("the hub row does not name its state in words:\n%s", hub)
	}
}

func TestPhotosPageWithoutPhotosExplainsAndOffersTheFirstAction(t *testing.T) {
	b, _ := photoBoxWith(t)
	r := b.get("/photos")
	if r.status != http.StatusOK {
		t.Fatalf("GET /photos = %d", r.status)
	}
	for _, want := range []string{"Noch keine Fotos", "Fotos auswählen", "Unsere Fotos", "Noch leer", "Das reicht für etwa", `href="/displays"`, "Zu den Displays",
		"Welche Alben ein Display zeigt, stellst du beim Display ein.", "JPEG, PNG oder HEIC, vom Handy oder Computer"} {
		if !strings.Contains(r.body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Contains(strings.ToLower(r.body), "iphone") {
		t.Error("the page names a device")
	}
}

func TestPhotosPageNamesStorageAlbumsAndTheDisplaysThatShowThem(t *testing.T) {
	f := newFamilyBox(t, "screen")
	c := newAPIClient(t, f.box)
	c.cookie = true
	def := defaultAlbum(t, c)
	uploaded(t, c, def, "a.jpg")
	r := f.get("/photos")
	for _, want := range []string{"GB von 2 GB belegt", "1 Foto", "Gezeigt auf", "„Hallway“", `href="/displays/`, `#bildschirmschoner"`, `aria-label="Einstellungen des Displays „Hallway“"`} {
		if !strings.Contains(r.body, want) {
			t.Errorf("the page lacks %q:\n%s", want, r.body)
		}
	}
	// A display's name is a link of its own, not inside the album's link.
	for _, part := range strings.Split(r.body, `<a class="main"`)[1:] {
		if strings.Contains(strings.SplitN(part, "</a>", 2)[0], `class="dn"`) {
			t.Error("a display link sits inside the album link")
		}
	}
	en := getEN(f.box, "/photos").body
	for _, want := range []string{"Our photos", "Shown on", "To the displays", "Which albums a display shows is set on the display."} {
		if !strings.Contains(en, want) {
			t.Errorf("English page lacks %q", want)
		}
	}
}

func TestUploadWithoutScriptAddsPhotosAndNamesWhatWasRefused(t *testing.T) {
	b, c := photoBoxWith(t)
	def := defaultAlbum(t, c)
	r := b.postFiles("/photos/upload", map[string]string{"album": def}, map[string][]byte{"IMG_1.jpg": jpegPhoto(t, 640, 480), "Reiseplan Gardasee.pdf": []byte("%PDF-1.7")})
	if r.status != http.StatusOK {
		t.Fatalf("upload = %d", r.status)
	}
	for _, want := range []string{"1 Foto zu „Unsere Fotos“ hinzugefügt.", "1 Datei nicht hinzugefügt:", "Reiseplan Gardasee.pdf", "kein Foto. Es gehen JPEG, PNG und HEIC."} {
		if !strings.Contains(r.body, want) {
			t.Errorf("the answer lacks %q:\n%s", want, r.body)
		}
	}
	if r := b.postFiles("/photos/upload", map[string]string{"album": def}, map[string][]byte{"x.pdf": []byte("no")}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("only refused files = %d, want 422", r.status)
	}
}

func TestFullStoragePageSaysSoAndLocksUploads(t *testing.T) {
	b, c := photoBoxWith(t)
	def := defaultAlbum(t, c)
	h, _ := b.db.HomeHousehold()
	if _, err := b.db.AddPhoto(h.ID, store.Photo{AlbumID: def, Name: "huge.jpg", Bytes: 2 << 30}); err != nil {
		t.Fatal(err)
	}
	r := b.get("/photos")
	for _, want := range []string{"storage full", "Voll: Die Box nimmt keine neuen Fotos an.", "löscht nie selbst Fotos", `aria-disabled="true"`, "Gerade ist kein Platz für neue Fotos."} {
		if !strings.Contains(r.body, want) {
			t.Errorf("the full page lacks %q", want)
		}
	}
	if strings.Contains(r.body, `type="file"`) {
		t.Error("a full box still offers the file field")
	}
	if r := b.postFiles("/photos/upload", map[string]string{"album": def}, map[string][]byte{"a.jpg": jpegPhoto(t, 100, 100)}); !strings.Contains(r.body, "a.jpg") || r.status != http.StatusUnprocessableEntity {
		t.Errorf("a refused photo = %d; its name must be listed", r.status)
	}
}

func TestAlbumPageHidesPhotosAndMovesAndDeletesThroughASelection(t *testing.T) {
	b, c := photoBoxWith(t)
	def := defaultAlbum(t, c)
	other := newAlbum(t, c, "Oma und Opa")
	p1, p2, p3 := uploaded(t, c, def, "IMG_1.jpg"), uploaded(t, c, def, "IMG_2.jpg"), uploaded(t, c, def, "IMG_3.jpg")

	page := b.get("/photos/albums/" + def)
	for _, want := range []string{"3 Fotos", "Neueste zuerst", `name="photo"`, `form="selection"`, "Auf Displays", `role="switch"`, "Fotos auswählen"} {
		if !strings.Contains(page.body, want) {
			t.Errorf("the album page lacks %q", want)
		}
	}
	// The switch hides a photo on the displays.
	if r := b.post("/photos/"+p1+"/hidden", url.Values{"album": {def}}); r.status != http.StatusSeeOther || !strings.HasSuffix(r.location, "/photos/albums/"+def+"#photo-"+p1) {
		t.Fatalf("hide = %d %s", r.status, r.location)
	}
	page = b.get("/photos/albums/" + def)
	if !strings.Contains(page.body, "Ausgeblendet") || !strings.Contains(page.body, "1 ausgeblendet") && !strings.Contains(page.body, "davon 1 ausgeblendet") {
		t.Errorf("a hidden photo is not marked in words:\n%s", page.body)
	}
	if r := b.post("/photos/"+p1+"/hidden", url.Values{"album": {def}, "shown": {"1"}}); r.status != http.StatusSeeOther {
		t.Fatalf("show again = %d", r.status)
	}
	if p, _ := b.db.Photo(householdIDOf(b), p1); p.Hidden {
		t.Error("the photo is still hidden")
	}
	// Move two photos.
	if r := b.post("/photos/albums/"+def+"/selection", url.Values{"photo": {p1, p2}, "target": {other}, "do": {"move"}}); r.status != http.StatusSeeOther {
		t.Fatalf("move = %d", r.status)
	}
	if got := b.get("/photos/albums/" + other).body; !strings.Contains(got, "2 Fotos") {
		t.Errorf("the target album does not hold the two photos:\n%s", got)
	}
	// Delete one.
	if r := b.post("/photos/albums/"+def+"/selection", url.Values{"photo": {p3}, "do": {"delete"}}); r.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", r.status)
	}
	if _, status, _ := c.callRaw(http.MethodGet, "/photos/"+p3+"/image", nil); status != http.StatusNotFound {
		t.Errorf("the deleted photo is served: %d", status)
	}
	// The next page says what happened.
	if got := b.get("/photos/albums/" + def).body; !strings.Contains(got, "1 Foto gelöscht.") {
		t.Errorf("no success line after the delete:\n%s", got)
	}
}

func householdIDOf(b *box) string {
	h, _ := b.db.HomeHousehold()
	return h.ID
}

func TestDeletingAnAlbumNamesItsPhotosAndEveryAffectedDisplay(t *testing.T) {
	f := newFamilyBox(t, "screen")
	c := newAPIClient(t, f.box)
	c.cookie = true
	trip := newAlbum(t, c, "Urlaub Gardasee 2026")
	uploaded(t, c, trip, "1.jpg")
	uploaded(t, c, trip, "2.jpg")
	page := f.get("/photos/albums/" + trip).body
	for _, want := range []string{"Album löschen", "<b>2 Fotos</b>", "werden von der Box gelöscht", "Diese Displays zeigen das Album danach nicht mehr:", "„Hallway“", "zeigt weiter alle anderen Alben", "Ja, „Urlaub Gardasee 2026“ mit 2 Fotos löschen"} {
		if !strings.Contains(page, want) {
			t.Errorf("the delete box lacks %q:\n%s", want, page)
		}
	}
	// A display that chose only this album goes back to all albums.
	_, d, _ := c.call(http.MethodGet, "/displays", nil)
	_ = d
	_, _, list := c.call(http.MethodGet, "/displays", nil)
	id := list[0].(map[string]any)["id"].(string)
	c.call(http.MethodPatch, "/displays/"+id, map[string]any{"screensaver": map[string]any{"album_ids": []string{trip}}})
	if page := f.get("/photos/albums/" + trip).body; !strings.Contains(page, "zeigt danach wieder alle Alben") {
		t.Errorf("the box does not say what the display shows afterwards:\n%s", page)
	}
	if r := f.post("/photos/albums/"+trip+"/delete", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("delete = %d", r.status)
	}
	if got := f.get("/photos").body; !strings.Contains(got, "Album „Urlaub Gardasee 2026“ gelöscht.") || strings.Contains(got, "Urlaub Gardasee 2026</b>") {
		t.Errorf("after the delete:\n%s", got)
	}
}

func TestNewAlbumNeedsAFreeName(t *testing.T) {
	b, c := photoBoxWith(t)
	if r := b.post("/photos/albums", url.Values{"album_name": {"Oma und Opa"}}); r.status != http.StatusSeeOther || !strings.Contains(r.location, "/photos/albums/") {
		t.Fatalf("create = %d %s", r.status, r.location)
	}
	r := b.post("/photos/albums", url.Values{"album_name": {"oma und opa"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "Ein Album mit diesem Namen gibt es schon.") || !strings.Contains(r.body, `value="oma und opa"`) {
		t.Errorf("duplicate = %d, want 422 with the error at the field and the typed name kept:\n%s", r.status, r.body)
	}
	_ = c
	id := firstAlbumID(t, b.get("/photos").body)
	if r := b.post("/photos/albums/"+id+"/rename", url.Values{"album_name": {"Unsere schönsten Fotos"}}); r.status != http.StatusSeeOther {
		t.Errorf("rename = %d", r.status)
	}
}

func TestPhotoLimitCanBeChangedAndIsChecked(t *testing.T) {
	b, _ := photoBoxWith(t)
	if r := b.post("/photos/limit", url.Values{"photo_limit": {"5"}}); r.status != http.StatusSeeOther {
		t.Fatalf("set limit = %d", r.status)
	}
	if got := b.get("/photos").body; !strings.Contains(got, "0 GB von 5 GB belegt") {
		t.Errorf("the page still shows the old limit:\n%s", got)
	}
	if r := b.post("/photos/limit", url.Values{"photo_limit": {"0"}}); r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, "ganze Zahl ab 1") {
		t.Errorf("limit 0 = %d", r.status)
	}
}

func TestOnlyAdultsSeeThePhotoPages(t *testing.T) {
	b, _ := photoBoxWith(t)
	kid := childSession(t, b)
	for _, path := range []string{"/photos", "/photos/albums/" + firstAlbumID(t, b.get("/photos").body)} {
		if r := kid.get(path); r.status != http.StatusForbidden {
			t.Errorf("a child opens %s = %d, want 403", path, r.status)
		}
	}
	if r := kid.post("/photos/albums", url.Values{"album_name": {"x"}}); r.status != http.StatusForbidden {
		t.Errorf("a child creates an album = %d, want 403", r.status)
	}
}

func TestNothingOnThePhotoPagesNamesADevice(t *testing.T) {
	// The browser converts photos from any phone or computer: no text says iPhone.
	for _, lang := range []string{"de", "en"} {
		raw, err := os.ReadFile(fmt.Sprintf("../i18n/locales/%s.json", lang))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if (strings.Contains(line, `"photos.`) || strings.Contains(line, `"saver.`) || strings.Contains(line, `"displays.saver`)) && strings.Contains(strings.ToLower(line), "iphone") {
				t.Errorf("%s: %s", lang, line)
			}
		}
	}
}

// Folder albums (Q72, Q83): every subfolder of the box's photo folder is an
// album, a photo lying directly in the folder is in the default album, and an
// album from a folder is changed only in the folder.
func TestSubfoldersBecomeAlbumsAndLooseFilesGoToTheDefaultAlbum(t *testing.T) {
	b, c := photoBoxWith(t)
	root := filepath.Join(b.photoDir, "photos")
	writeFile := func(rel string, data []byte) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("lose.jpg", jpegPhoto(t, 800, 600))
	writeFile("Weihnachten 2025/a.jpg", jpegPhoto(t, 2200, 1650))
	writeFile("Weihnachten 2025/b.jpg", jpegPhoto(t, 640, 480))
	writeFile("Weihnachten 2025/notiz.txt", []byte("keine Datei für Fotos"))
	if err := os.MkdirAll(filepath.Join(root, "Sommerfest"), 0o755); err != nil {
		t.Fatal(err)
	}

	list := albums(t, c)
	byName := map[string]map[string]any{}
	for _, a := range list {
		byName[a["name"].(string)] = a
	}
	if len(list) != 3 || byName["Weihnachten 2025"]["source"] != "folder" || byName["Weihnachten 2025"]["photo_count"].(float64) != 2 || byName["Sommerfest"]["photo_count"].(float64) != 0 {
		t.Fatalf("albums = %v", list)
	}
	if byName["Unsere Fotos"]["photo_count"].(float64) != 1 {
		t.Errorf("the loose photo is not in the default album: %v", byName["Unsere Fotos"])
	}
	xmas := byName["Weihnachten 2025"]["id"].(string)
	_, _, ps := c.call(http.MethodGet, "/albums/"+xmas+"/photos", nil)
	if w := ps[0].(map[string]any)["width"].(float64); w > 2048 {
		t.Errorf("a folder photo was kept at %v pixels, want it shrunk to display size", w)
	}

	// The folder album is changed only in the folder.
	page := b.get("/photos/albums/" + xmas).body
	if !strings.Contains(page, "Dieses Album kommt aus dem Ordner „Weihnachten 2025“ auf der Box.") || strings.Contains(page, `name="photo"`) || strings.Contains(page, "Album löschen") || strings.Contains(page, `type="file"`) {
		t.Errorf("the folder album page offers changes:\n%s", page)
	}
	if !strings.Contains(page, `role="switch"`) {
		t.Error("a folder photo cannot be hidden")
	}
	pid := ps[0].(map[string]any)["id"].(string)
	if status, _, _ := c.call(http.MethodPatch, "/photos/"+pid, map[string]any{"hidden": true}); status != http.StatusOK {
		t.Errorf("hiding a folder photo = %d", status)
	}
	for name, status := range map[string]int{
		"rename": firstStatus(c.call(http.MethodPatch, "/albums/"+xmas, map[string]any{"name": "Neu"})),
		"delete": firstStatus(c.call(http.MethodDelete, "/albums/"+xmas, nil)),
		"move":   firstStatus(c.call(http.MethodPatch, "/photos/"+pid, map[string]any{"album_id": defaultAlbum(t, c)})),
		"remove": firstStatus(c.call(http.MethodDelete, "/photos/"+pid, nil)),
		"upload": func() int { s, _ := c.upload(xmas, "x.jpg", jpegPhoto(t, 100, 100)); return s }(),
	} {
		if status != http.StatusUnprocessableEntity {
			t.Errorf("%s on a folder album = %d, want 422", name, status)
		}
	}
	if r := b.post("/photos/albums/"+xmas+"/rename", url.Values{"album_name": {"Neu"}}); r.status != http.StatusForbidden {
		t.Errorf("web rename of a folder album = %d, want 403", r.status)
	}

	// A photo that leaves the folder and a folder that goes are forgotten.
	if err := os.Remove(filepath.Join(root, "Weihnachten 2025", "b.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "Sommerfest")); err != nil {
		t.Fatal(err)
	}
	list = albums(t, c)
	if len(list) != 2 {
		t.Fatalf("after the folder lost an album: %v", list)
	}
	for _, a := range list {
		if a["name"] == "Weihnachten 2025" && a["photo_count"].(float64) != 1 {
			t.Errorf("the album still holds the removed photo: %v", a)
		}
	}
}

func firstStatus(status int, _ map[string]any, _ []any) int { return status }

func TestFolderPhotosWaitWhenThereIsNoSpaceAndTheBoxDeletesNothing(t *testing.T) {
	b, c := photoBoxWith(t)
	h := householdIDOf(b)
	def := defaultAlbum(t, c)
	if _, err := b.db.AddPhoto(h, store.Photo{AlbumID: def, Name: "huge.jpg", Bytes: 2<<30 - 10}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(b.photoDir, "photos", "Sommerfest 2026")
	os.MkdirAll(dir, 0o755)
	for i := range 3 {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("p%d.jpg", i)), jpegPhoto(t, 640, 480), 0o644)
	}
	page := b.get("/photos").body
	if !strings.Contains(page, "3 Fotos aus dem Ordner „Sommerfest 2026“ warten, bis wieder Platz ist.") {
		t.Errorf("the waiting photos are not named:\n%s", page)
	}
	// The API names the same waiting folder, with the keys of the contract.
	_, st, _ := c.call(http.MethodGet, "/photo-storage", nil)
	waiting, _ := st["waiting"].([]any)
	if len(waiting) != 1 {
		t.Fatalf("waiting = %v, want one folder", st["waiting"])
	}
	if w, _ := waiting[0].(map[string]any); w["folder"] != "Sommerfest 2026" || w["count"] != float64(3) {
		t.Errorf("waiting folder = %v, want folder and count", waiting[0])
	}
	// Room again: the photos are read.
	if status, _, _ := c.call(http.MethodPatch, "/photo-storage", map[string]any{"limit_gb": 3}); status != http.StatusOK {
		t.Fatal("raise limit")
	}
	for _, a := range albums(t, c) {
		if a["name"] == "Sommerfest 2026" && a["photo_count"].(float64) != 3 {
			t.Errorf("the waiting photos were not read when room came: %v", a)
		}
	}
}
