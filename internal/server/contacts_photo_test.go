package server_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Ticket 297 (preview-297.html): photos of contacts, added by a parent from the camera or the gallery.

// photoPost posts the contact form as a browser does with a photo: multipart, the photo in the part "photo".
func (c ctBox) photoPost(path string, f url.Values, name string, data []byte) resp {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range f {
		for _, v := range vs {
			_ = mw.WriteField(k, v)
		}
	}
	if data != nil {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="photo"; filename=%q`, name))
		h.Set("Content-Type", "application/octet-stream")
		part, _ := mw.CreatePart(h)
		_, _ = part.Write(data)
	}
	_ = mw.Close()
	return c.rawPost(path, mw.FormDataContentType(), &buf)
}

func (c ctBox) rawPost(path, ctype string, body *bytes.Buffer) resp {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.srv.URL+path, body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Origin", c.srv.URL)
	req.Header.Set("Accept-Language", "de")
	res, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), out.String()}
}

// getPhoto reads GET /contacts/{id}/photo: status, content type, cache header and body.
func (c ctBox) getPhoto(path string) (int, http.Header, []byte) {
	c.t.Helper()
	res, err := c.client.Get(c.srv.URL + path)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(res.Body)
	return res.StatusCode, res.Header, out.Bytes()
}

func jpegSide(t *testing.T, data []byte) (int, int) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a JPEG: %v", err)
	}
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

var photoTokenRe = regexp.MustCompile(`name="picture_token" value="([0-9a-f]+)"`)

func phHas(t *testing.T, what, page string, want ...string) {
	t.Helper()
	ctHas(t, what, squash(page), want...)
}

func phHasNot(t *testing.T, what, page string, unwanted ...string) {
	t.Helper()
	ctHasNot(t, what, squash(page), unwanted...)
}

func formWith(name string, extra url.Values) url.Values {
	f := contactForm(name, "", nil, nil)
	for k, vs := range extra {
		f[k] = vs
	}
	return f
}

// Acceptance: photo upload for parents, without script: the photo goes in with Save.
func TestAParentAddsAContactPhotoWithSaveAndItIsSquareAndAtMost512(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := c.photoPost("/contacts/new", formWith("Oma Gerda", nil), "IMG_1.jpg", testJPEG(t, 600, 1200))
	m := regexp.MustCompile(`^/contacts/([0-9a-f-]{36})$`).FindStringSubmatch(r.location)
	if r.status != http.StatusSeeOther || m == nil {
		t.Fatalf("add = %d %q\n%s", r.status, r.location, r.body)
	}
	id := m[1]
	page := c.page("/contacts/"+id, "de").body
	phHas(t, "contact page", page, `<div class="ct-head"><span class="ct-photo page"><img src="/contacts/`+id+`/photo?v=`, `alt="Foto von Oma Gerda" width="512" height="512"`,
		`<p class="ph-link"><a href="/contacts/`+id+`/edit#photo">`, "Foto ändern")
	status, header, body := c.getPhoto("/contacts/" + id + "/photo")
	if status != http.StatusOK || header.Get("Content-Type") != "image/jpeg" || !strings.Contains(header.Get("Cache-Control"), "private") {
		t.Fatalf("photo = %d %q %q", status, header.Get("Content-Type"), header.Get("Cache-Control"))
	}
	if w, h := jpegSide(t, body); w != 512 || h != 512 {
		t.Errorf("stored %d x %d, want 512 x 512", w, h)
	}
	if bytes.Contains(body, []byte("Exif")) {
		t.Error("the photo carries metadata")
	}
	// The success line names the photo.
	if !strings.Contains(page, "„Oma Gerda“ gespeichert, mit neuem Foto.") {
		t.Errorf("no success line with the new photo:\n%s", page)
	}
	phHas(t, "English", c.page("/contacts/"+id, "en").body, `alt="Photo of Oma Gerda"`, "Change photo")
	// The version in the address changes with the photo, so a changed photo is not served from the cache.
	v1 := regexp.MustCompile(`photo\?v=([0-9a-z]+)`).FindStringSubmatch(page)
	if v1 == nil {
		t.Fatalf("no version on the photo address:\n%s", page)
	}
}

func TestUploadPrepareShowsTheCroppedPhotoKeepsEverythingTypedAndStoresNothingUntilSave(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, formWith("Oma Gerda", url.Values{"full_name": {"Gerda Müller"}}))
	f := formWith("Oma Gerda", url.Values{"full_name": {"Gerda Maier"}, "note": {"Hat den Schlüssel."}, "picture": {"upload"}})
	r := c.photoPost("/contacts/"+id+"/edit", f, "Familie.jpg", testJPEG(t, 3024, 4032))
	if r.status != http.StatusOK {
		t.Fatalf("prepare = %d\n%s", r.status, r.body)
	}
	tok := photoTokenRe.FindStringSubmatch(r.body)
	if tok == nil {
		t.Fatalf("no picture_token:\n%s", r.body)
	}
	phHas(t, "prepared form", r.body, `enctype="multipart/form-data"`, `<fieldset id="photo" class="ph-card">`, `<div class="pic-result" role="status">`,
		`alt="Neues Foto von Oma Gerda" width="132" height="132"`, "Neues Foto, noch nicht gespeichert", "So erscheint es überall, wo „Oma Gerda“ steht.",
		"3024 × 4032 auf 512 × 512 Pixel", "Ort und Kameradaten entfernt", "Hejmo speichert es mit „Speichern“.", "Neues Foto verwerfen",
		`value="Gerda Maier"`, "Hat den Schlüssel.", `<span class="dirty">Noch nicht gespeichert</span>`)
	phHasNot(t, "prepared form", r.body, `class="dirty" hidden`)
	// Nothing is stored yet.
	if status, _, _ := c.getPhoto("/contacts/" + id + "/photo"); status != http.StatusNotFound {
		t.Errorf("photo before Save = %d, want 404", status)
	}
	// The prepared photo can be looked at.
	if status, _, body := c.getPhoto("/settings/lists/library/picture/" + tok[1]); status != http.StatusOK || len(body) == 0 {
		t.Errorf("pending photo = %d", status)
	}
	// Save applies the token.
	save := formWith("Oma Gerda", url.Values{"full_name": {"Gerda Maier"}, "picture_token": {tok[1]}})
	if r := c.photoPost("/contacts/"+id+"/edit", save, "", nil); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	status, _, body := c.getPhoto("/contacts/" + id + "/photo")
	if status != http.StatusOK {
		t.Fatalf("photo after Save = %d", status)
	}
	if w, h := jpegSide(t, body); w != 512 || h != 512 {
		t.Errorf("stored %d x %d", w, h)
	}
	phHas(t, "origin line", c.page("/contacts/"+id, "de").body, "Foto von Mama hinzugefügt am ")
	phHas(t, "English origin line", c.page("/contacts/"+id, "en").body, "Photo added by Mama on ")
	// The token is used up: a second post of the same form asks for the photo again instead of saving without it.
	if r := c.photoPost("/contacts/"+id+"/edit", save, "", nil); r.status != http.StatusUnprocessableEntity {
		t.Errorf("save with a used token = %d, want 422", r.status)
	}
}

func TestAFailedSaveKeepsThePreparedPhotoAndSaysSo(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := c.photoPost("/contacts/new", formWith("Mia Neu", url.Values{"picture": {"upload"}}), "a.jpg", testJPEG(t, 800, 600))
	tok := photoTokenRe.FindStringSubmatch(r.body)
	if r.status != http.StatusOK || tok == nil {
		t.Fatalf("prepare = %d\n%s", r.status, r.body)
	}
	phHas(t, "new contact", r.body, "Hejmo speichert es mit „Kontakt anlegen“.")
	// Another field is wrong: the name is empty.
	bad := formWith("", url.Values{"picture_token": {tok[1]}})
	r = c.photoPost("/contacts/new", bad, "", nil)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("bad save = %d", r.status)
	}
	phHas(t, "refused form", r.body, "Das neue Foto ist vorbereitet und wird mit gespeichert.", `name="picture_token" value="`+tok[1]+`"`, `<div class="pic-result"`)
	// Fixing the name saves the contact with the photo.
	ok := formWith("Mia Neu", url.Values{"picture_token": {tok[1]}})
	r = c.photoPost("/contacts/new", ok, "", nil)
	m := regexp.MustCompile(`^/contacts/([0-9a-f-]{36})$`).FindStringSubmatch(r.location)
	if r.status != http.StatusSeeOther || m == nil {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if status, _, _ := c.getPhoto("/contacts/" + m[1] + "/photo"); status != http.StatusOK {
		t.Errorf("photo = %d", status)
	}
}

func TestAParentChangesAndRemovesAContactPhotoAndRemovalWaitsForSave(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := c.photoPost("/contacts/new", formWith("Oma Gerda", nil), "a.jpg", testJPEG(t, 400, 400))
	id := regexp.MustCompile(`/contacts/([0-9a-f-]{36})$`).FindStringSubmatch(r.location)[1]
	_, _, first := c.getPhoto("/contacts/" + id + "/photo")
	// The form shows it and offers to change and remove it.
	form := c.page("/contacts/"+id+"/edit", "de").body
	phHas(t, "form with a photo", form, `<div class="ph-now"><span class="ct-photo page"><img src="/contacts/`+id+`/photo?v=`, "Foto gespeichert", "Von Mama hinzugefügt am ",
		"Neues Foto aufnehmen", "Anderes aus der Galerie", "Anderes Foto auswählen", `name="picture" value="remove" formnovalidate`, "Foto entfernen")
	// Change.
	if r := c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", nil), "b.jpg", testJPEG(t, 900, 300)); r.status != http.StatusSeeOther {
		t.Fatalf("change = %d\n%s", r.status, r.body)
	}
	_, _, second := c.getPhoto("/contacts/" + id + "/photo")
	if bytes.Equal(first, second) {
		t.Error("the photo was not changed")
	}
	// "Foto entfernen" shows what will happen and removes nothing yet.
	r = c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", url.Values{"picture": {"remove"}}), "", nil)
	if r.status != http.StatusOK {
		t.Fatalf("remove = %d", r.status)
	}
	phHas(t, "marked for removal", r.body, `<div class="ph-gone" role="status">`, "Das Foto wird beim Speichern entfernt.", "Danach zeigt Hejmo überall die Anfangsbuchstaben „OG“.",
		`name="photo_gone" value="1"`, `name="picture" value="keep" formnovalidate`, "Foto doch behalten", `<span class="ct-photo page none" aria-hidden="true">OG</span>`)
	if status, _, _ := c.getPhoto("/contacts/" + id + "/photo"); status != http.StatusOK {
		t.Errorf("removed before Save: %d", status)
	}
	// "Foto doch behalten" takes it back.
	r = c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", url.Values{"picture": {"keep"}, "photo_gone": {"1"}}), "", nil)
	phHasNot(t, "kept", r.body, `class="ph-gone"`, `name="photo_gone"`)
	phHas(t, "kept", r.body, "Foto gespeichert")
	// Save with the mark removes it.
	if r := c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", url.Values{"photo_gone": {"1"}}), "", nil); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if status, _, _ := c.getPhoto("/contacts/" + id + "/photo"); status != http.StatusNotFound {
		t.Errorf("photo after removal = %d, want 404", status)
	}
	page := c.page("/contacts/"+id, "de").body
	phHas(t, "page without photo", page, `<span class="ct-photo page none" aria-hidden="true">OG</span>`, "Foto hinzufügen")
	phHasNot(t, "page without photo", page, "Foto von Mama hinzugefügt")
	// A new photo can be discarded again: back to what was saved.
	c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", nil), "c.jpg", testJPEG(t, 300, 300))
	r = c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", url.Values{"picture": {"upload"}}), "d.jpg", testJPEG(t, 300, 300))
	tok := photoTokenRe.FindStringSubmatch(r.body)[1]
	r = c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", url.Values{"picture": {"remove"}, "picture_token": {tok}}), "", nil)
	phHasNot(t, "discarded", r.body, `class="pic-result"`, `name="picture_token"`, `class="ph-gone"`)
	phHas(t, "discarded", r.body, "Foto gespeichert")
}

func TestAPersonsContactCanHaveAPhotoToo(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	mia := c.addPerson(t, "Mia", "child")
	r := c.photoPost("/contacts/"+mia+"/edit", url.Values{"name": {"Mia"}}, "mia.jpg", testJPEG(t, 640, 480))
	if r.status != http.StatusSeeOther {
		t.Fatalf("person's photo = %d\n%s", r.status, r.body)
	}
	if status, _, _ := c.getPhoto("/contacts/" + mia + "/photo"); status != http.StatusOK {
		t.Errorf("photo = %d", status)
	}
	person := c.page("/people/"+mia, "de").body
	phHas(t, "person page", person, `<ul class="list-rows ct-row">`, `<span class="ct-photo"><img src="/contacts/`+mia+`/photo?v=`, `alt="" width="512" height="512"`)
	page := c.page("/contacts/"+mia, "de").body
	phHas(t, "contact of a person", page, `<span class="marker"`, `alt="Foto von Mia"`)
}

func TestTheOverviewHasAPhotoOrInitialsInEveryRowFamilyRowsIncluded(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, formWith("Oma Gerda", nil))
	withPhoto := regexp.MustCompile(`/contacts/([0-9a-f-]{36})$`).FindStringSubmatch(c.photoPost("/contacts/new", formWith("Kita Sonnenschein", nil), "k.jpg", testJPEG(t, 100, 100)).location)[1]
	c.add(t, formWith("Kinderarzt", nil))
	c.add(t, formWith("Anna (Mama von Lukas)", nil))
	c.add(t, formWith("Ärztlicher Bereitschaftsdienst", nil))
	page := c.page("/contacts", "de").body
	rowOfS := func(n string) string { return squash(rowOf(page, n)) }
	_ = rowOfS
	for name, want := range map[string]string{
		"Oma Gerda":                      `<span class="ct-photo none" aria-hidden="true">OG</span><span class="t">`,
		"Kinderarzt":                     `<span class="ct-photo none" aria-hidden="true">K</span><span class="t">`,
		"Anna (Mama von Lukas)":          `<span class="ct-photo none" aria-hidden="true">AM</span><span class="t">`,
		"Ärztlicher Bereitschaftsdienst": `<span class="ct-photo none" aria-hidden="true">ÄB</span><span class="t">`,
		"Kita Sonnenschein":              `<span class="ct-photo"><img src="/contacts/` + withPhoto + `/photo?v=`,
		"Mama":                           `<span class="ct-photo none" aria-hidden="true">M</span><span class="t">`,
	} {
		row := squash(rowOf(page, name))
		if row == "" {
			t.Fatalf("no row for %s\n%s", name, page)
		}
		if !strings.Contains(row, want) {
			t.Errorf("row of %s lacks %q:\n%s", name, want, row)
		}
	}
	phHas(t, "photo row", rowOf(page, "Kita Sonnenschein"), `alt="" width="512" height="512" loading="lazy"></span><span class="t">`)
	phHas(t, "initials are not read out", rowOf(page, "Oma Gerda"), `aria-hidden="true"`)
}

func TestTheFormHasAPhotoCardAfterNameWithTheStateInWords(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	form := c.page("/contacts/new", "de").body
	phHas(t, "new form", form, `enctype="multipart/form-data"`, `<fieldset id="photo" class="ph-card"><legend>Foto <span class="tag hidden-tag">freiwillig</span></legend>`,
		`<span class="ct-photo page none" aria-hidden="true"><svg class="icon"`, "<b>Noch kein Foto</b>", "Ohne Foto zeigt Hejmo die Anfangsbuchstaben des Namens in Hejmo.",
		`<label class="button secondary touch">`, `accept="image/*" capture="environment"`, "Foto aufnehmen", "Aus der Galerie wählen",
		`<label class="button secondary mouse">`, "Foto auswählen", `aria-describedby="hint-photo"`, `<p class="hint" id="hint-photo">`, "JPEG, PNG, WebP, GIF oder HEIC, höchstens 15 MB",
		"Es bleibt auf der Box.")
	if strings.Index(form, `id="photo"`) < strings.Index(form, `id="name"`) || strings.Index(form, `id="photo"`) > strings.Index(form, `id="numbers"`) {
		t.Error("the photo card is not between Name and Numbers")
	}
	id := c.add(t, formWith("Kinderarzt", nil))
	phHas(t, "one letter", c.page("/contacts/"+id+"/edit", "de").body, "Ohne Foto zeigt Hejmo den Anfangsbuchstaben „K“.", `>K</span>`)
	id2 := c.add(t, formWith("Oma Gerda", nil))
	phHas(t, "two letters", c.page("/contacts/"+id2+"/edit", "de").body, "Ohne Foto zeigt Hejmo die Anfangsbuchstaben „OG“.")
	phHas(t, "English", c.page("/contacts/"+id2+"/edit", "en").body, `<legend>Photo <span class="tag hidden-tag">optional</span></legend>`, "Take photo", "Choose from gallery", "Choose photo", "the initials “OG”")
	phHas(t, "contact page without photo", c.page("/contacts/"+id2, "de").body, `<div class="ct-head"><span class="ct-photo page none" aria-hidden="true">OG</span><h1>Oma Gerda</h1>`,
		`<a href="/contacts/`+id2+`/edit#photo">`, "Foto hinzufügen")
}

func TestPhotoFieldErrorsNameTheFileAndKeepWhatWasSaved(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, formWith("Kinderarzt", nil))
	up := url.Values{"name": {"Kinderarzt"}, "picture": {"upload"}}
	r := c.photoPost("/contacts/"+id+"/edit", up, "Mietvertrag Praxis.pdf", []byte("%PDF-1.4 not a picture"))
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("pdf = %d\n%s", r.status, r.body)
	}
	phHas(t, "not an image", r.body, `<fieldset id="photo" class="ph-card invalid">`, `<p class="ferr" id="err-photo" role="alert">`,
		"„Mietvertrag Praxis.pdf“ ist kein Foto. Wähle ein Bild: JPEG, PNG, WebP, GIF oder HEIC.", `aria-invalid="true" aria-describedby="err-photo hint-photo"`)
	big := bytes.Repeat([]byte{0}, 16<<20)
	r = c.photoPost("/contacts/"+id+"/edit", up, "IMG_4031.png", big)
	phHas(t, "too large", r.body, "„IMG_4031.png“ ist 16,0 MB groß. Hejmo nimmt Fotos bis 15 MB. Mach ein neues Foto oder wähle ein kleineres.")
	if r.status != http.StatusUnprocessableEntity {
		t.Errorf("too large = %d", r.status)
	}
	r = c.photoPost("/contacts/"+id+"/edit", up, "pano.png", hugePNG())
	phHas(t, "too many pixels", r.body, "„pano.png“ hat zu viele Pixel. Mach ein kleineres Foto")
	phHas(t, "English", c.photoPostEN(t, "/contacts/"+id+"/edit", up, "doc.pdf", []byte("%PDF")).body, "“doc.pdf” is not a photo. Choose a picture: JPEG, PNG, WebP, GIF or HEIC.")
	// Nothing was stored, and a good photo saved before stays after a refused one.
	if status, _, _ := c.getPhoto("/contacts/" + id + "/photo"); status != http.StatusNotFound {
		t.Errorf("photo = %d", status)
	}
	c.photoPost("/contacts/"+id+"/edit", url.Values{"name": {"Kinderarzt"}}, "ok.jpg", testJPEG(t, 200, 200))
	c.photoPost("/contacts/"+id+"/edit", up, "bad.pdf", []byte("nope"))
	if status, _, _ := c.getPhoto("/contacts/" + id + "/photo"); status != http.StatusOK {
		t.Errorf("the saved photo is gone after a refused upload: %d", status)
	}
}

func (c ctBox) photoPostEN(t *testing.T, path string, f url.Values, name string, data []byte) resp {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range f {
		for _, v := range vs {
			_ = mw.WriteField(k, v)
		}
	}
	part, _ := mw.CreateFormFile("photo", name)
	_, _ = part.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", c.srv.URL)
	req.Header.Set("Accept-Language", "en")
	res, err := c.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), out.String()}
}

// hugePNG claims 6000 x 5000 pixels in its header and holds nothing more.
func hugePNG() []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(kind string, data []byte) {
		_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(kind)
		b.Write(data)
		sum := crc32.NewIEEE()
		sum.Write([]byte(kind))
		sum.Write(data)
		_ = binary.Write(&b, binary.BigEndian, sum.Sum32())
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 6000)
	binary.BigEndian.PutUint32(ihdr[4:], 5000)
	ihdr[8], ihdr[9] = 8, 2
	chunk("IHDR", ihdr)
	chunk("IEND", nil)
	return b.Bytes()
}

// Acceptance: only parents may upload, change or remove; others are refused.
func TestOnlyParentsSeeAndChangeContactPhotos(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := c.photoPost("/contacts/new", formWith("Oma Gerda", nil), "a.jpg", testJPEG(t, 300, 300))
	id := regexp.MustCompile(`/contacts/([0-9a-f-]{36})$`).FindStringSubmatch(r.location)[1]

	signedOut, _ := configuredBox(t)
	if r := signedOut.get("/contacts/" + id + "/photo"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Errorf("signed out photo = %d %q", r.status, r.location)
	}
	kid := childSession(t, c.box)
	kc := ctBox{peopleBox{box: kid}}
	if status, _, _ := kc.getPhoto("/contacts/" + id + "/photo"); status != http.StatusForbidden {
		t.Errorf("child GET photo = %d, want 403", status)
	}
	if r := kc.photoPost("/contacts/"+id+"/edit", url.Values{"name": {"Oma Gerda"}}, "x.jpg", testJPEG(t, 300, 300)); r.status != http.StatusForbidden {
		t.Errorf("child upload = %d, want 403", r.status)
	}
	if r := kc.photoPost("/contacts/"+id+"/edit", url.Values{"name": {"Oma Gerda"}, "photo_gone": {"1"}}, "", nil); r.status != http.StatusForbidden {
		t.Errorf("child removal = %d, want 403", r.status)
	}
	if r := kc.photoPost("/contacts/new", url.Values{"name": {"Neu"}}, "x.jpg", testJPEG(t, 300, 300)); r.status != http.StatusForbidden {
		t.Errorf("child new contact with a photo = %d, want 403", r.status)
	}
	if status, _, _ := c.getPhoto("/contacts/" + id + "/photo"); status != http.StatusOK {
		t.Errorf("the parent's photo changed: %d", status)
	}
	if status, _, _ := c.getPhoto("/contacts/nope/photo"); status != http.StatusNotFound {
		t.Errorf("unknown contact = %d", status)
	}
}

func TestContactPhotosOverRESTAreCheckedAgainstTheContract(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, formWith("Oma Gerda", nil))
	if status, got, _ := c.api.call(http.MethodGet, "/contacts/"+id, nil); status != http.StatusOK || got["photo"] != nil {
		t.Fatalf("contact without a photo = %d %v", status, got)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "oma.jpg")
	_, _ = part.Write(testJPEG(t, 640, 480))
	_ = mw.Close()
	body, status, _ := c.api.send(http.MethodPut, "/contacts/"+id+"/photo", mw.FormDataContentType(), buf.Bytes())
	if status != http.StatusOK || !strings.Contains(body, `"photo"`) || !strings.Contains(body, `"origin":"manual"`) {
		t.Fatalf("PUT photo = %d %s", status, body)
	}
	raw, status, ctype := c.api.callRaw(http.MethodGet, "/contacts/"+id+"/photo", nil)
	if status != http.StatusOK || ctype != "image/jpeg" {
		t.Fatalf("GET photo = %d %s", status, ctype)
	}
	if w, h := jpegSide(t, []byte(raw)); w != 480 || h != 480 { // 640 x 480 cut to a square, never scaled up
		t.Errorf("stored %d x %d, want 480 x 480", w, h)
	}
	if status, got, _ := c.api.call(http.MethodGet, "/contacts/"+id, nil); status != http.StatusOK || got["photo"] == nil || got["photo"].(map[string]any)["by"] == nil {
		t.Errorf("contact with a photo = %d %v", status, got)
	}
	// Not a picture: 422; no multipart: 422.
	var bad bytes.Buffer
	bw := multipart.NewWriter(&bad)
	p2, _ := bw.CreateFormFile("file", "x.pdf")
	_, _ = p2.Write([]byte("%PDF"))
	_ = bw.Close()
	if _, status, _ := c.api.send(http.MethodPut, "/contacts/"+id+"/photo", bw.FormDataContentType(), bad.Bytes()); status != http.StatusUnprocessableEntity {
		t.Errorf("PDF = %d, want 422", status)
	}
	// A save of the contact through PUT /contacts/{id} leaves the photo alone.
	if status, got, _ := c.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": "Oma Gerda", "full_name": "Gerda Müller"}); status != http.StatusOK || got["photo"] == nil {
		t.Errorf("replace = %d %v", status, got)
	}
	if _, status, _ := c.api.callRaw(http.MethodDelete, "/contacts/"+id+"/photo", nil); status != http.StatusNoContent {
		t.Errorf("DELETE photo = %d", status)
	}
	if _, status, _ := c.api.callRaw(http.MethodGet, "/contacts/"+id+"/photo", nil); status != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", status)
	}
	if _, status, _ := c.api.callRaw(http.MethodDelete, "/contacts/"+id+"/photo", nil); status != http.StatusNoContent {
		t.Errorf("DELETE twice = %d", status)
	}
	// Children get 403 on all three.
	kc := newAPIClient(t, childSession(t, c.box))
	kc.cookie = true
	if _, status, _ := kc.callRaw(http.MethodGet, "/contacts/"+id+"/photo", nil); status != http.StatusForbidden {
		t.Errorf("child GET = %d", status)
	}
	if _, status, _ := kc.callRaw(http.MethodDelete, "/contacts/"+id+"/photo", nil); status != http.StatusForbidden {
		t.Errorf("child DELETE = %d", status)
	}
	if _, status, _ := kc.send(http.MethodPut, "/contacts/"+id+"/photo", mw.FormDataContentType(), buf.Bytes()); status != http.StatusForbidden {
		t.Errorf("child PUT = %d", status)
	}
}

// Review of #314: a prepared photo that is gone (expired or evicted) is never dropped without a word.
func TestSavingWithAPreparedPhotoThatIsGoneStopsAndAsksForTheChoiceAgain(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, formWith("Oma Gerda", nil))
	r := c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", url.Values{"full_name": {"Gerda Neu"}, "picture_token": {"0123456789abcdef"}}), "", nil)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("save with an unknown token = %d, want 422\n%s", r.status, r.body)
	}
	phHas(t, "form", r.body, `<fieldset id="photo" class="ph-card invalid">`, "Das vorbereitete Foto ist nicht mehr da. Wähle das Foto noch einmal aus.", `value="Gerda Neu"`)
	phHasNot(t, "form", r.body, "Das neue Foto ist vorbereitet und wird mit gespeichert.", `name="picture_token"`)
	if c.page("/contacts/"+id, "de").body == "" || strings.Contains(c.page("/contacts/"+id, "de").body, "Gerda Neu") {
		t.Error("the contact was saved although its photo was gone")
	}
	phHas(t, "English", c.photoPostEN(t, "/contacts/"+id+"/edit", url.Values{"name": {"Oma Gerda"}, "picture_token": {"0123456789abcdef"}}, "", nil).body, "The prepared photo is no longer there. Choose the photo again.")
	// A new photo in the same post replaces the gone one and saves.
	r = c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", url.Values{"picture_token": {"0123456789abcdef"}}), "n.jpg", testJPEG(t, 300, 300))
	if r.status != http.StatusSeeOther {
		t.Errorf("save with a new photo and a gone token = %d", r.status)
	}
}

// Review of #314: the photo address changes with every change and the version-less REST address is never cached.
func TestThePhotoAddressChangesWithEveryChangeAndTheRESTAddressIsNotCached(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := c.photoPost("/contacts/new", formWith("Oma Gerda", nil), "a.jpg", testJPEG(t, 400, 400))
	id := regexp.MustCompile(`/contacts/([0-9a-f-]{36})$`).FindStringSubmatch(r.location)[1]
	version := func() string {
		m := regexp.MustCompile(`photo\?v=([0-9a-z]+)`).FindStringSubmatch(c.page("/contacts/"+id, "de").body)
		if m == nil {
			t.Fatal("no version on the photo address")
		}
		return m[1]
	}
	v1 := version()
	c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", nil), "b.jpg", testJPEG(t, 500, 500))
	v2 := version()
	c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", nil), "c.jpg", testJPEG(t, 300, 300))
	if v3 := version(); v1 == v2 || v2 == v3 || v1 == v3 {
		t.Errorf("versions %s, %s, %s: two changes gave the same address", v1, v2, v3)
	}
	_, header, _ := c.getPhoto("/contacts/" + id + "/photo")
	if cc := header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") || strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control without ?v= = %q, want no-cache", cc)
	}
	_, header, _ = c.getPhoto("/contacts/" + id + "/photo?v=" + v2)
	if cc := header.Get("Cache-Control"); !strings.Contains(cc, "max-age=86400") {
		t.Errorf("Cache-Control with ?v= = %q, want max-age", cc)
	}
}

// Review of #314: a form over the limit is not read; the form comes back with what is saved and says so.
func TestAFormOverTheLimitComesBackWithTheSavedValuesAndSaysItWasNotRead(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	id := c.add(t, formWith("Oma Gerda", url.Values{"full_name": {"Gerda Müller"}}))
	r := c.photoPost("/contacts/"+id+"/edit", formWith("Oma Gerda", url.Values{"full_name": {"Nie gelesen"}, "picture": {"upload"}}), "riesig.jpg", bytes.Repeat([]byte{0}, 41<<20))
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("too big = %d", r.status)
	}
	phHas(t, "form", r.body, "Die anderen Eingaben wurden nicht gelesen.", `value="Gerda Müller"`, `name="name"`)
	phHasNot(t, "form", r.body, "Nie gelesen")
}

// Review of #314: a list shows many photos; the ones in rows load when they come near.
func TestPhotosInRowsLoadLazilyAndThePageOnesDoNot(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	r := c.photoPost("/contacts/new", formWith("Oma Gerda", nil), "a.jpg", testJPEG(t, 400, 400))
	id := regexp.MustCompile(`/contacts/([0-9a-f-]{36})$`).FindStringSubmatch(r.location)[1]
	phHas(t, "overview", c.page("/contacts", "de").body, `<img src="/contacts/`+id+`/photo?v=`)
	if !regexp.MustCompile(`<img src="/contacts/` + id + `/photo\?v=[0-9a-z]+" alt="" width="512" height="512" loading="lazy">`).MatchString(c.page("/contacts", "de").body) {
		t.Error("the photo in an overview row has no loading=\"lazy\"")
	}
	phHasNot(t, "contact page", c.page("/contacts/"+id, "de").body, `loading="lazy"`)
}
