package server_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// jpegPhoto is a w x h JPEG with a location in its EXIF data.
func jpegPhoto(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: 120, A: 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	seg := append([]byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x00"), []byte("GPS-48.139N-11.461E")...)
	out := []byte{0xff, 0xd8, 0xff, 0xe1}
	out = binary.BigEndian.AppendUint16(out, uint16(len(seg)+2))
	out = append(out, seg...)
	return append(out, raw[2:]...)
}

// upload sends one file to an album the way the web interface does.
func (c *apiClient) upload(albumID, filename string, data []byte) (int, map[string]any) {
	c.b.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	hdr.Set("Content-Type", "application/octet-stream")
	part, _ := mw.CreatePart(hdr)
	part.Write(data)
	mw.Close()
	raw, status, _ := c.send(http.MethodPost, "/albums/"+albumID+"/photos", mw.FormDataContentType(), body.Bytes())
	return status, decodeObject(c.b.t, raw)
}

func decodeObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	var obj map[string]any
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, raw)
		}
	}
	return obj
}

func albums(t *testing.T, c *apiClient) []map[string]any {
	t.Helper()
	status, _, list := c.call(http.MethodGet, "/albums", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /albums = %d", status)
	}
	var out []map[string]any
	for _, a := range list {
		out = append(out, a.(map[string]any))
	}
	return out
}

func defaultAlbum(t *testing.T, c *apiClient) string {
	t.Helper()
	for _, a := range albums(t, c) {
		if a["default"] == true {
			return a["id"].(string)
		}
	}
	t.Fatal("no default album")
	return ""
}

func newAlbum(t *testing.T, c *apiClient, name string) string {
	t.Helper()
	status, body, _ := c.call(http.MethodPost, "/albums", map[string]any{"name": name})
	if status != http.StatusCreated {
		t.Fatalf("POST /albums %q = %d %v", name, status, body)
	}
	return body["id"].(string)
}

func uploaded(t *testing.T, c *apiClient, album, name string) string {
	t.Helper()
	status, body := c.upload(album, name, jpegPhoto(t, 640, 480))
	if status != http.StatusCreated {
		t.Fatalf("upload %s = %d %v", name, status, body)
	}
	return body["id"].(string)
}

func TestTheDefaultAlbumExistsFromTheStart(t *testing.T) {
	c := signedInAPI(t)
	list := albums(t, c)
	if len(list) != 1 || list[0]["default"] != true || list[0]["name"] != "Unsere Fotos" || list[0]["source"] != "upload" {
		t.Fatalf("albums = %v, want only the default album Unsere Fotos", list)
	}
	if status, _, _ := c.call(http.MethodDelete, "/albums/"+list[0]["id"].(string), nil); status != http.StatusUnprocessableEntity {
		t.Errorf("deleting the default album = %d, want 422", status)
	}
}

func TestUploadKeepsAShrunkCopyWithoutLocation(t *testing.T) {
	c := signedInAPI(t)
	album := defaultAlbum(t, c)
	status, p := c.upload(album, "IMG_0001.jpg", jpegPhoto(t, 4000, 3000))
	if status != http.StatusCreated || p["name"] != "IMG_0001.jpg" || p["hidden"] != false || p["album_id"] != album {
		t.Fatalf("upload = %d %v", status, p)
	}
	if p["width"].(float64) != 2048 || p["height"].(float64) != 1536 {
		t.Errorf("stored size = %vx%v, want 2048x1536 (display size)", p["width"], p["height"])
	}
	raw, status, ctype := c.callRaw(http.MethodGet, "/photos/"+p["id"].(string)+"/image", nil)
	if status != http.StatusOK || ctype != "image/jpeg" {
		t.Fatalf("image = %d %s", status, ctype)
	}
	if strings.Contains(raw, "GPS-48") || strings.Contains(raw, "Exif") {
		t.Error("the stored photo still carries its location")
	}
	thumb, status, _ := c.callRaw(http.MethodGet, "/photos/"+p["id"].(string)+"/image?size=thumbnail", nil)
	if status != http.StatusOK || len(thumb) >= len(raw) {
		t.Errorf("thumbnail = %d, %d bytes vs %d", status, len(thumb), len(raw))
	}
	// The original file is not kept anywhere in the box's data directory.
	_ = filepath.WalkDir(c.b.photoDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if data, _ := os.ReadFile(path); bytes.Contains(data, []byte("GPS-48")) {
				t.Errorf("%s still holds the location", path)
			}
		}
		return nil
	})
}

func TestSomethingThatIsNoPhotoIsRefusedWithAReason(t *testing.T) {
	c := signedInAPI(t)
	album := defaultAlbum(t, c)
	for name, data := range map[string][]byte{"Reiseplan.pdf": []byte("%PDF-1.7"), "foto.heic": append([]byte{0, 0, 0, 24}, []byte("ftypheic")...)} {
		status, body := c.upload(album, name, data)
		if status != http.StatusUnprocessableEntity || body["error"] == nil {
			t.Errorf("%s = %d %v, want 422 with a reason", name, status, body)
		}
	}
	if l := albums(t, c); l[0]["photo_count"].(float64) != 0 {
		t.Errorf("a refused file was stored: %v", l[0])
	}
}

func TestFullStorageRefusesNewPhotosAndDeletesNothing(t *testing.T) {
	c := signedInAPI(t)
	album := defaultAlbum(t, c)
	keep := uploaded(t, c, album, "first.jpg")
	h, _ := c.b.db.HomeHousehold()
	used, _, _ := c.b.db.PhotoUsage(h.ID)
	if _, err := c.b.db.AddPhoto(h.ID, store.Photo{AlbumID: album, Name: "huge.jpg", Bytes: 2<<30 - used - 10}); err != nil {
		t.Fatal(err)
	}
	status, body := c.upload(album, "second.jpg", jpegPhoto(t, 640, 480))
	if status != http.StatusInsufficientStorage || !strings.Contains(fmt.Sprint(body["error"]), "full") {
		t.Errorf("upload into full storage = %d %v, want 507 saying it is full", status, body)
	}
	if _, status, _ := c.callRaw(http.MethodGet, "/photos/"+keep+"/image", nil); status != http.StatusOK {
		t.Errorf("an existing photo vanished: %d", status)
	}
	status, st, _ := c.call(http.MethodGet, "/photo-storage", nil)
	if status != http.StatusOK || st["full"] != true || st["limit_gb"].(float64) != 2 {
		t.Errorf("storage = %d %v", status, st)
	}
	// Raising the limit makes room again.
	if status, st, _ := c.call(http.MethodPatch, "/photo-storage", map[string]any{"limit_gb": 4}); status != http.StatusOK || st["full"] != false {
		t.Fatalf("raise limit = %d %v", status, st)
	}
	if status, _ := c.upload(album, "third.jpg", jpegPhoto(t, 640, 480)); status != http.StatusCreated {
		t.Errorf("upload after raising the limit = %d", status)
	}
	if status, _, _ := c.call(http.MethodPatch, "/photo-storage", map[string]any{"limit_gb": 0}); status != http.StatusUnprocessableEntity {
		t.Errorf("limit 0 = %d, want 422", status)
	}
}

func TestPhotoCanBeHiddenFromDisplays(t *testing.T) {
	c := signedInAPI(t)
	album := defaultAlbum(t, c)
	id := uploaded(t, c, album, "a.jpg")
	status, p, _ := c.call(http.MethodPatch, "/photos/"+id, map[string]any{"hidden": true})
	if status != http.StatusOK || p["hidden"] != true {
		t.Fatalf("hide = %d %v", status, p)
	}
	if a := albums(t, c)[0]; a["hidden_count"].(float64) != 1 || a["photo_count"].(float64) != 1 {
		t.Errorf("album = %v, want 1 photo of which 1 hidden", a)
	}
	_, _, list := c.call(http.MethodGet, "/albums/"+album+"/photos", nil)
	if len(list) != 1 || list[0].(map[string]any)["hidden"] != true {
		t.Errorf("photos = %v", list)
	}
}

func TestAlbumsAreMadeRenamedMovedAndDeleted(t *testing.T) {
	c := signedInAPI(t)
	def := defaultAlbum(t, c)
	trip := newAlbum(t, c, "Urlaub Gardasee 2026")
	if status, body, _ := c.call(http.MethodPost, "/albums", map[string]any{"name": "urlaub gardasee 2026"}); status != http.StatusUnprocessableEntity || body["field"] != "album_name" {
		t.Errorf("duplicate name = %d %v, want 422 on album_name", status, body)
	}
	p1, p2 := uploaded(t, c, def, "1.jpg"), uploaded(t, c, def, "2.jpg")
	if status, _, _ := c.call(http.MethodPatch, "/photos/"+p1, map[string]any{"album_id": trip}); status != http.StatusOK {
		t.Fatalf("move = %d", status)
	}
	_, _, inTrip := c.call(http.MethodGet, "/albums/"+trip+"/photos", nil)
	if len(inTrip) != 1 || inTrip[0].(map[string]any)["id"] != p1 {
		t.Errorf("the album holds %v", inTrip)
	}
	if status, a, _ := c.call(http.MethodPatch, "/albums/"+trip, map[string]any{"name": "Gardasee"}); status != http.StatusOK || a["name"] != "Gardasee" {
		t.Errorf("rename = %d %v", status, a)
	}
	if status, _, _ := c.call(http.MethodDelete, "/photos/"+p2, nil); status != http.StatusNoContent {
		t.Errorf("delete photo = %d", status)
	}
	if _, status, _ := c.callRaw(http.MethodGet, "/photos/"+p2+"/image", nil); status != http.StatusNotFound {
		t.Errorf("a deleted photo is still served: %d", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/albums/"+trip, nil); status != http.StatusNoContent {
		t.Errorf("delete album = %d", status)
	}
	if _, status, _ := c.callRaw(http.MethodGet, "/photos/"+p1+"/image", nil); status != http.StatusNotFound {
		t.Errorf("a photo of a deleted album is still served: %d", status)
	}
	if status, _, _ := c.call(http.MethodGet, "/albums/"+trip, nil); status != http.StatusNotFound {
		t.Errorf("deleted album = %d, want 404", status)
	}
	if entries, _ := os.ReadDir(filepath.Join(c.b.photoDir, "photo-files")); len(entries) != 0 {
		t.Errorf("files of deleted photos are left: %d", len(entries))
	}
}

func TestOnlyAdultsManagePhotos(t *testing.T) {
	c := signedInAPI(t)
	album := defaultAlbum(t, c)
	id := uploaded(t, c, album, "a.jpg")
	kidBox := childSession(t, c.b)
	kid := newAPIClient(t, kidBox)
	kid.cookie = true
	for _, req := range [][2]string{{http.MethodGet, "/albums"}, {http.MethodGet, "/photo-storage"}, {http.MethodGet, "/albums/" + album + "/photos"}, {http.MethodDelete, "/photos/" + id}, {http.MethodPatch, "/photos/" + id}} {
		if status, _, _ := kid.call(req[0], req[1], map[string]any{"hidden": true}); status != http.StatusForbidden {
			t.Errorf("a child's %s %s = %d, want 403", req[0], req[1], status)
		}
	}
	if status, _ := kid.upload(album, "kid.jpg", jpegPhoto(t, 100, 100)); status != http.StatusForbidden {
		t.Errorf("a child's upload = %d, want 403", status)
	}
}

func TestADisplayReadsItsPhotosAndNobodyElseCan(t *testing.T) {
	c := signedInAPI(t)
	anon := newAPIClient(t, c.b)
	_, opened := openAPIPairing(anon)
	token := opened["token"].(string)
	if status, _, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": opened["code"], "name": "Küche", "type": "screen"}); status != http.StatusCreated {
		t.Fatal("pairing failed")
	}
	d := anon.withToken(token)
	def := defaultAlbum(t, c)
	shown, hidden := uploaded(t, c, def, "a.jpg"), uploaded(t, c, def, "b.jpg")
	c.call(http.MethodPatch, "/photos/"+hidden, map[string]any{"hidden": true})

	status, saver, _ := d.call(http.MethodGet, "/display/saver", nil)
	photos, _ := saver["photos"].([]any)
	if status != http.StatusOK || saver["active"] != true || len(photos) != 1 || photos[0] != shown || saver["interval_seconds"].(float64) != 60 {
		t.Fatalf("saver = %d %v, want the shown photo only, every 60 seconds", status, saver)
	}
	raw, status, ctype := d.callRaw(http.MethodGet, "/display/photos/"+shown, nil)
	if status != http.StatusOK || ctype != "image/jpeg" || len(raw) == 0 {
		t.Errorf("display photo = %d %s", status, ctype)
	}
	if _, status, _ := anon.callRaw(http.MethodGet, "/display/photos/"+shown, nil); status != http.StatusUnauthorized {
		t.Errorf("a photo without a display token = %d, want 401", status)
	}
	if _, status, _ := anon.callRaw(http.MethodGet, "/display/saver", nil); status != http.StatusUnauthorized {
		t.Errorf("the saver without a display token = %d, want 401", status)
	}
}

func TestDisplaySettingsCarryTheScreensaver(t *testing.T) {
	c := signedInAPI(t)
	anon := newAPIClient(t, c.b)
	_, opened := openAPIPairing(anon)
	_, kitchen, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": opened["code"], "name": "Küche", "type": "screen"})
	_, opened = openAPIPairing(anon)
	_, hall, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": opened["code"], "name": "Flur", "type": "eink_bw"})
	k, _ := kitchen["screensaver"].(map[string]any)
	h, _ := hall["screensaver"].(map[string]any)
	if k["enabled"] != true || k["idle_minutes"].(float64) != 10 || k["interval_minutes"].(float64) != 1 || len(k["album_ids"].([]any)) != 0 {
		t.Errorf("a new screen's screensaver = %v, want on, 10 minutes, 1 minute, all albums", k)
	}
	if h["interval_minutes"].(float64) != 15 {
		t.Errorf("a new E-Ink display changes the photo every %v minutes, want 15", h["interval_minutes"])
	}
	trip := newAlbum(t, c, "Urlaub")
	id := kitchen["id"].(string)
	status, d, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"screensaver": map[string]any{"idle_minutes": 5, "album_ids": []string{trip}}})
	sv, _ := d["screensaver"].(map[string]any)
	if status != http.StatusOK || sv["idle_minutes"].(float64) != 5 || sv["interval_minutes"].(float64) != 1 || sv["enabled"] != true || len(sv["album_ids"].([]any)) != 1 {
		t.Fatalf("patch = %d %v", status, d)
	}
	for name, bad := range map[string]map[string]any{"idle 0": {"idle_minutes": 0}, "interval 241": {"interval_minutes": 241}, "unknown album": {"album_ids": []string{"7d3c6a0e-0000-4000-8000-000000000000"}}} {
		if status, _, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"screensaver": bad}); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
	// The album now says where it is shown, and no longer the other display.
	for _, a := range albums(t, c) {
		shownOn := names(a["displays"])
		switch a["name"] {
		case "Urlaub":
			if strings.Join(shownOn, ",") != "Küche,Flur" && strings.Join(shownOn, ",") != "Küche" {
				t.Errorf("Urlaub is shown on %v", shownOn)
			}
		case "Unsere Fotos":
			if strings.Join(shownOn, ",") != "Flur" {
				t.Errorf("Unsere Fotos is shown on %v, want only Flur (Küche chose Urlaub)", shownOn)
			}
		}
	}
	// Deleting the album sends the kitchen back to all albums.
	c.call(http.MethodDelete, "/albums/"+trip, nil)
	_, _, list := c.call(http.MethodGet, "/displays", nil)
	for _, d := range list {
		if m := d.(map[string]any); m["name"] == "Küche" && len(m["screensaver"].(map[string]any)["album_ids"].([]any)) != 0 {
			t.Errorf("after deleting its only album the kitchen shows %v", m["screensaver"])
		}
	}
}

func names(v any) []string {
	var out []string
	for _, d := range v.([]any) {
		out = append(out, d.(map[string]any)["name"].(string))
	}
	return out
}
