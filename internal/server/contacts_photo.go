package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Contact photos (ticket 297, preview-297.html): a square JPEG of at most 512 px per contact, shown wherever the
// contact appears. A parent chooses it in the form; the box prepares it, the form shows it as it will appear, and
// "Speichern" stores it. The prepared photo waits under a token (the pending pictures of the library) until then.

// contactPhotoPath is where the photo of a contact is served, with its version so that a changed photo is fetched again.
func contactPhotoPath(c store.Contact) string {
	if !c.HasPhoto {
		return ""
	}
	return contactsPath + "/" + c.ID + "/photo?v=" + photoVersion(c)
}

// photoVersion is the version in the address of a photo: a changed photo is fetched again.
func photoVersion(c store.Contact) string { return strconv.FormatInt(c.PhotoAt.UnixNano(), 36) }

// ctPhotoView is what a template needs to draw one .ct-photo: the photo, or the initials (or the gallery icon
// when there are none).
type ctPhotoView struct {
	URL, Initials string
}

func photoViewOf(c store.Contact) ctPhotoView {
	return ctPhotoView{URL: contactPhotoPath(c), Initials: contact.Initials(c.HejmoName)}
}

// initialsPhrase says which letters stand in for a missing photo: "den Anfangsbuchstaben „K“", "die
// Anfangsbuchstaben „OG“", or the general phrase while there is no name yet.
func (e ctEnv) initialsPhrase(name string) string {
	switch letters := contact.Initials(name); contact.Runes(letters) {
	case 0:
		return e.t("contact.photo.initials.none")
	case 1:
		return e.t("contact.photo.initials.one", "letters", letters)
	default:
		return e.t("contact.photo.initials.two", "letters", letters)
	}
}

// photoOrigin says where the photo of a stored contact came from, for the form card ("Von Papa hinzugefügt am ...").
func (e ctEnv) photoOrigin(c store.Contact) string {
	when := e.p.dateTimeSeconds(c.PhotoAt)
	switch {
	case c.PhotoOrigin == store.PhotoSync:
		return e.t("contact.photo.sync", "when", when, "device", c.PhotoDevice)
	case c.PhotoByName == "":
		return e.t("contact.photo.by.anon", "when", when)
	}
	return e.t("contact.photo.by", "name", c.PhotoByName, "when", when)
}

// photoHint is the same for the line under the facts of the contact page ("Foto von Papa hinzugefügt am ...").
func (e ctEnv) photoHint(c store.Contact) string {
	if !c.HasPhoto {
		return ""
	}
	when := e.p.dateTimeSeconds(c.PhotoAt)
	switch {
	case c.PhotoOrigin == store.PhotoSync:
		return e.t("contact.hint.photo.sync", "when", when, "device", c.PhotoDevice)
	case c.PhotoByName == "":
		return e.t("contact.hint.photo.anon", "when", when)
	}
	return e.t("contact.hint.photo", "name", c.PhotoByName, "when", when)
}

// handleContactPhoto serves the photo of a contact to a parent. Children may see photos of the contacts they are
// allowed to see once ticket 298 decides which those are; until then it is for parents only.
func (s *server) handleContactPhoto(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	ph, err := s.db.ContactPhoto(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "contact photo", err)
		return
	}
	serveContactPhoto(w, r, ph)
}

func serveContactPhoto(w http.ResponseWriter, r *http.Request, ph store.ContactPhoto) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("v") != "" { // the address carries the version
		w.Header().Set("Cache-Control", "private, max-age=86400")
	} else { // the REST address does not: ask again every time (Last-Modified answers with 304)
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	http.ServeContent(w, r, "", ph.At, bytes.NewReader(ph.JPEG))
}

// processContactPhoto makes the stored photo of an upload.
func processContactPhoto(ctx context.Context, h *multipart.FileHeader) (picture.Result, error) {
	if h.Size > picture.MaxBytes {
		return picture.Result{}, &picture.TooLargeError{Bytes: h.Size}
	}
	f, err := h.Open()
	if err != nil {
		return picture.Result{}, err
	}
	defer f.Close()
	return picture.ProcessSquare(ctx, io.LimitReader(f, picture.MaxBytes+1), picture.ContactMaxSide)
}

// photoError is the sentence at the card for a photo that cannot be used; it names the file.
func (e ctEnv) photoError(h *multipart.FileHeader, err error) string {
	var big *picture.TooLargeError
	switch {
	case errors.As(err, &big):
		size := max(big.Bytes, h.Size)
		return e.t("contact.photo.err.big", "file", h.Filename, "size", humanBytes(e.lang, size))
	case errors.Is(err, picture.ErrTooManyPixels):
		return e.t("contact.photo.err.pixels", "file", h.Filename)
	case errors.Is(err, picture.ErrNotAnImage), errors.Is(err, picture.ErrWebPage):
		return e.t("contact.photo.err.notimage", "file", h.Filename)
	}
	return e.t("contact.photo.err.read")
}

// ctPhotoCard is the card "Foto" of the form.
type ctPhotoCard struct {
	Mode        string // none, saved, new, gone
	Invalid     bool
	Err         string
	Name        string // the name in Hejmo as typed, for the texts
	Initials    string // "" shows the gallery icon
	Src, Alt    string // the saved photo
	View        ctPhotoView
	Title, Text string // the state line
	Result      *ctPhotoResult
	Other       bool   // a photo is there or chosen: the buttons say "another"
	RemoveLabel string // "Foto entfernen" or "Neues Foto verwerfen"; empty: no such button
	Gone        bool
	Token       string
	GoneText    string
	GoneAfter   string
}

// ctPhotoResult is the .pic-result of a photo that was just prepared.
type ctPhotoResult struct{ URL, Alt, Title, Text, Save string }

// photoCard works out what the card shows. cur is the stored contact (nil for a new one), pend the prepared photo.
func (e ctEnv) photoCard(f ctForm, cur *store.Contact, pend *pendingPicture, photoErr string, saveLabel string) ctPhotoCard {
	name := strings.TrimSpace(f.Name)
	if cur != nil && cur.IsPerson() {
		name = cur.HejmoName
	}
	card := ctPhotoCard{Name: name, Initials: contact.Initials(name), Invalid: photoErr != "", Err: photoErr, Token: f.Token}
	has := cur != nil && cur.HasPhoto
	switch {
	case pend != nil:
		card.Mode, card.Other, card.RemoveLabel = "new", true, e.t("contact.photo.discard")
		altName := name
		key := "contact.photo.new.text"
		if name == "" {
			key = "contact.photo.new.text.noname"
		}
		alt := e.t("contact.photo.alt.new", "name", altName)
		if name == "" {
			alt = e.t("contact.photo.new.title")
		}
		card.Result = &ctPhotoResult{
			URL: "/settings/lists/library/picture/" + f.Token, Alt: alt, Title: e.t("contact.photo.new.title"),
			Text: e.t(key, "name", name, "fw", strconv.Itoa(pend.FromW), "fh", strconv.Itoa(pend.FromH), "tw", strconv.Itoa(pend.ToW), "th", strconv.Itoa(pend.ToH),
				"from", humanBytes(e.lang, int64(pend.FromBytes)), "to", humanBytes(e.lang, int64(len(pend.JPEG)))),
			Save: e.t("contact.photo.new.save", "button", saveLabel),
		}
	case has && f.Gone:
		card.Mode, card.Gone = "gone", true
		card.View = ctPhotoView{Initials: card.Initials}
		card.GoneText = e.t("contact.photo.gone")
		card.GoneAfter = e.t("contact.photo.gone.after", "initials", e.initialsPhrase(name))
	case has:
		card.Mode, card.Other, card.RemoveLabel = "saved", true, e.t("contact.photo.remove")
		card.Src, card.Alt = contactPhotoPath(*cur), e.t("contact.photo.alt", "name", cur.HejmoName)
		card.View = ctPhotoView{URL: card.Src}
		card.Title, card.Text = e.t("contact.photo.saved"), e.photoOrigin(*cur)
	default:
		card.Mode = "none"
		card.View = ctPhotoView{Initials: card.Initials}
		card.Title, card.Text = e.t("contact.photo.none"), e.t("contact.photo.none.hint", "initials", e.initialsPhrase(name))
	}
	return card
}

// readContactBody reads the posted form, multipart when a photo may come with it. It returns the photo that came
// with it, if any, and whether the form was too large to read at all.
func (s *server) readContactBody(w http.ResponseWriter, r *http.Request) (file *multipart.FileHeader, tooBig bool) {
	if r.Method != http.MethodPost {
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxForm)
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return nil, false
	}
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var big *http.MaxBytesError
		return nil, errors.As(err, &big)
	}
	for _, h := range r.MultipartForm.File["photo"] { // the buttons of the card are several inputs; one holds the file
		if h.Size > 0 {
			return h, false
		}
	}
	return nil, false
}

// contactPhotoStep does what the form asks of the photo before anything is saved: the prepared photo is looked up,
// "Foto entfernen" and "Foto doch behalten" are applied, and a photo that came with the form is prepared. It
// returns the prepared photo and the sentence for the card when the photo cannot be used. f.Token and f.Gone say
// afterwards what the form holds.
func (s *server) contactPhotoStep(r *http.Request, e ctEnv, f *ctForm, cur *store.Contact, file *multipart.FileHeader, tooBig bool) (*pendingPicture, string) {
	now := s.now()
	pend, have := s.pictures.get(f.Token, now)
	gone := f.Token != "" && !have // the prepared photo expired or was pushed out of the shared store
	if !have {
		f.Token = ""
	}
	stored := cur != nil && cur.HasPhoto
	if !stored {
		f.Gone = false
	}
	switch r.PostFormValue("picture") {
	case "remove":
		if have { // "Neues Foto verwerfen": back to what is saved
			s.pictures.drop(f.Token)
			f.Token, have = "", false
		} else if stored {
			f.Gone = true
		}
	case "keep":
		f.Gone = false
	}
	var photoErr string
	if tooBig {
		photoErr = e.t("contact.photo.err.form")
	}
	if file != nil {
		if res, err := processContactPhoto(r.Context(), file); err != nil {
			photoErr = e.photoError(file, err)
		} else {
			s.pictures.drop(f.Token)
			pend, have = pendingFrom(res, "upload", "", file.Filename), true
			f.Token, f.Gone = newToken(), false
			s.pictures.put(f.Token, pend, now)
		}
	}
	if gone && file == nil && photoErr == "" { // never save without a photo the parent chose
		photoErr = e.t("contact.photo.err.gone")
	}
	if !have {
		return nil, photoErr
	}
	return &pend, photoErr
}

// The REST side of the photo (ticket 297): the same rules as the form, for parents.

func (s *server) apiGetContactPhoto(w http.ResponseWriter, r *http.Request, p principal) {
	ph, err := s.db.ContactPhoto(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no photo")
		return
	} else if err != nil {
		s.fail(w, "contact photo", err)
		return
	}
	serveContactPhoto(w, r, ph)
}

func (s *server) apiSetContactPhoto(w http.ResponseWriter, r *http.Request, p principal) {
	id := r.PathValue("id")
	cur, err := s.db.Contact(p.household.ID, id)
	if s.storeError(w, "contact", err) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, picture.MaxBytes+(1<<20))
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			writeError(w, http.StatusRequestEntityTooLarge, "photos are at most 15 MB")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "send the photo as multipart/form-data in the part file")
		return
	}
	defer r.MultipartForm.RemoveAll()
	_, h, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "send the photo as multipart/form-data in the part file")
		return
	}
	res, err := processContactPhoto(r.Context(), h)
	var big *picture.TooLargeError
	switch {
	case errors.As(err, &big):
		writeError(w, http.StatusRequestEntityTooLarge, "photos are at most 15 MB")
		return
	case errors.Is(err, picture.ErrTooManyPixels):
		writeError(w, http.StatusUnprocessableEntity, "the photo has too many pixels")
		return
	case errors.Is(err, picture.ErrNotAnImage), errors.Is(err, picture.ErrWebPage):
		writeError(w, http.StatusUnprocessableEntity, "the file is not a JPEG, PNG, WebP or GIF picture")
		return
	case err != nil:
		s.fail(w, "contact photo", err)
		return
	}
	saved, err := s.setContactPhoto(p, cur, res.JPEG, store.SetPicture)
	if s.storeError(w, "set contact photo", err) {
		return
	}
	writeJSON(w, http.StatusOK, s.contactToJSON(p, saved))
}

func (s *server) apiDeleteContactPhoto(w http.ResponseWriter, r *http.Request, p principal) {
	cur, err := s.db.Contact(p.household.ID, r.PathValue("id"))
	if s.storeError(w, "contact", err) {
		return
	}
	if cur.HasPhoto {
		if _, err := s.setContactPhoto(p, cur, nil, store.RemovePicture); s.storeError(w, "remove contact photo", err) {
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// setContactPhoto changes only the photo of a stored contact; everything else stays as it is.
func (s *server) setContactPhoto(p principal, cur store.Contact, jpeg []byte, change store.PictureChange) (store.Contact, error) {
	var photo *store.ContactPhoto
	if change == store.SetPicture {
		photo = &store.ContactPhoto{JPEG: jpeg}
	}
	return s.db.SetContactPhoto(p.household.ID, cur.ID, p.person.ID, change, photo)
}
