package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/recipephoto"
	"github.com/hejmo-home/hejmo/internal/starter"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Own photos of recipes (ticket 280, preview-280.html, ADR 0021). Every recipe page, own or starter, has the
// section "Fotos"; parents add photos there and choose the cover or remove photos on the page below it. The
// files lie on the box in three sizes (package recipephoto): lists and the grid never load the full photo.

// maxPhotoForm is the most a form with the photos of one recipe may carry.
const maxPhotoForm = store.MaxRecipePhotos*picture.MaxBytes + 1<<20

// rphPic is an image of a photo as the templates draw it.
type rphPic struct {
	Src  string
	W, H int
}

// rphPath is the address of a photo; the size is "medium" or "small", empty for the full photo.
func rphPath(ref, id, size string) string {
	out := recipesPath + "/" + ref + "/photos/" + id
	if size != "" {
		out += "?size=" + size
	}
	return out
}

func rphMedium(ref string, ph store.RecipePhoto) rphPic {
	w, h := recipephoto.MediumSize(ph.Width, ph.Height)
	return rphPic{Src: rphPath(ref, ph.ID, "medium"), W: w, H: h}
}

func rphSmall(ref string, ph store.RecipePhoto) rphPic {
	w, h := recipephoto.SmallSize(ph.Width, ph.Height)
	return rphPic{Src: rphPath(ref, ph.ID, "small"), W: w, H: h}
}

// smallCover is the small picture of the cover of a recipe for a list row, the suggestion or the meal plan box;
// nil when the recipe has no photo (then the symbol of its category shows).
func (c *itemCtx) smallCover(ref string) *rphPic {
	if !c.coversLoaded {
		c.covers, _ = c.s.db.RecipeCovers(c.p.household.ID) // a failure shows the symbols
		c.coversLoaded = true
	}
	ph, ok := c.covers[ref]
	if !ok {
		return nil
	}
	pic := rphSmall(ref, ph)
	return &pic
}

// recipeRefExists says whether the household has the recipe or it is a starter recipe.
func (s *server) recipeRefExists(householdID, ref string) (bool, error) {
	if starter.IsID(ref) {
		_, ok := starter.ByID(ref)
		return ok, nil
	}
	_, err := s.db.Recipe(householdID, ref)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// photoParent says whether the person adds and removes photos: parents, for every recipe, starter ones too.
func photoParent(p principal) bool {
	return auth.RoleAllows(p.person.Role, auth.Perm("meals", auth.Write))
}

// count says "1 Foto" or "12 Fotos".
func (c *itemCtx) photoCount(n int) string {
	if n == 1 {
		return c.t("rph.count.one")
	}
	return c.t("rph.count.other", "n", strconv.Itoa(n))
}

func (c *itemCtx) photoAlt(recipeName string, ph store.RecipePhoto) string {
	date := c.p.day(ph.AddedAt)
	switch {
	case ph.Origin == store.PhotoFromWeb:
		return c.t("rph.alt.web", "recipe", recipeName, "date", date)
	case ph.AddedByName == "":
		return c.t("rph.alt.anon", "recipe", recipeName, "date", date)
	}
	return c.t("rph.alt", "recipe", recipeName, "name", ph.AddedByName, "date", date)
}

// photoBy is the small line under a photo of the grid: "Papa, 24.11.2026".
func (c *itemCtx) photoBy(ph store.RecipePhoto) string {
	date := c.p.day(ph.AddedAt)
	switch {
	case ph.Origin == store.PhotoFromWeb:
		return c.t("rph.by.web", "date", date)
	case ph.AddedByName == "":
		return c.t("rph.by.anon", "date", date)
	}
	return c.t("rph.by", "name", ph.AddedByName, "date", date)
}

// photoByCard is the line of a card of the photos page: "Hinzugefügt von Papa am 24.11.2026".
func (c *itemCtx) photoByCard(ph store.RecipePhoto) string {
	date := c.p.day(ph.AddedAt)
	switch {
	case ph.Origin == store.PhotoFromWeb:
		return c.t("rph.by.card.web", "date", date)
	case ph.AddedByName == "":
		return c.t("rph.by.card.anon", "date", date)
	}
	return c.t("rph.by.card", "name", ph.AddedByName, "date", date)
}

// rphItem is one photo of the grid or of the cards.
type rphItem struct {
	ID, Href, Alt, By string
	Pic               rphPic
	Cover, New        bool
	CoverAction       string
	RemoveAction      string
	By2               string // the line of a card
	RemoveNote        string // what happens after removing it
}

func (c *itemCtx) photoItems(ref, recipeName string, list []store.RecipePhoto) []rphItem {
	out := make([]rphItem, 0, len(list))
	for _, ph := range list {
		out = append(out, rphItem{ID: ph.ID, Href: rphPath(ref, ph.ID, ""), Alt: c.photoAlt(recipeName, ph), By: c.photoBy(ph), By2: c.photoByCard(ph),
			Pic: rphMedium(ref, ph), Cover: ph.IsCover,
			CoverAction: rphPath(ref, ph.ID, "") + "/cover", RemoveAction: rphPath(ref, ph.ID, "") + "/delete"})
	}
	return out
}

// recipePhotoView adds to the data of the recipe page what the cover, the link and the section "Fotos" need.
func (s *server) recipePhotoView(c *itemCtx, rec storeRecipeRef, data map[string]any) error {
	list, err := s.db.RecipePhotos(c.p.household.ID, rec.ID)
	if err != nil {
		return err
	}
	parent := photoParent(c.p)
	items := c.photoItems(rec.ID, rec.Name, list)
	data["PhotoParent"] = parent
	data["PhotoItems"] = items
	data["PhotosHref"] = recipesPath + "/" + rec.ID + "/photos"
	data["PhotoFull"] = len(list) >= store.MaxRecipePhotos
	data["PhotoAction"] = recipesPath + "/" + rec.ID + "/photos"
	data["PhotoSection"] = len(list) > 0 || parent
	if len(items) > 0 {
		data["Cover"] = items[0]
		data["CoverHref"] = items[0].Href
	}
	switch {
	case len(list) >= 2:
		data["Phl"] = c.t("rph.phl.all", "n", strconv.Itoa(len(list)))
		data["PhlIcon"] = "gallery"
	case parent && len(list) == 1:
		data["Phl"], data["PhlIcon"] = c.t("rph.phl.more"), "camera"
	case parent:
		data["Phl"], data["PhlIcon"] = c.t("rph.phl.add"), "camera"
	}
	return nil
}

// storeRecipeRef is what the photo pages need of a recipe.
type storeRecipeRef struct{ ID, Name string }

// ---- adding photos

// rphRefusal is a file that was not added, with the sentence that says why.
type rphRefusal struct{ Name, Reason string }

// addRecipePhotos prepares and stores the files for a recipe. Files that cannot be used are returned with the
// reason; the others are added in the order they came. An error is a failure of the box.
func (s *server) addRecipePhotos(ctx context.Context, p principal, ref string, files []*multipart.FileHeader, reason func(*multipart.FileHeader, error) string) (added []store.RecipePhoto, refused []rphRefusal, err error) {
	for _, h := range files {
		set, perr := prepareRecipePhoto(ctx, h)
		if perr != nil {
			refused = append(refused, rphRefusal{h.Filename, reason(h, perr)})
			continue
		}
		ph, aerr := s.db.AddRecipePhoto(p.household.ID, ref, store.NewRecipePhoto{PersonID: p.person.ID, FileName: h.Filename, Width: set.Width, Height: set.Height})
		if errors.Is(aerr, store.ErrRecipePhotosFull) {
			refused = append(refused, rphRefusal{h.Filename, reason(h, aerr)})
			continue
		}
		if aerr != nil {
			return added, refused, aerr
		}
		if perr := s.recipePhotos.Put(ph.ID, set); perr != nil {
			_, _, _ = s.db.DeleteRecipePhoto(p.household.ID, ref, ph.ID)
			return added, refused, perr
		}
		added = append(added, ph)
	}
	return added, refused, nil
}

func prepareRecipePhoto(ctx context.Context, h *multipart.FileHeader) (recipephoto.Set, error) {
	if h.Size > picture.MaxBytes {
		return recipephoto.Set{}, &picture.TooLargeError{Bytes: h.Size}
	}
	f, err := h.Open()
	if err != nil {
		return recipephoto.Set{}, err
	}
	defer f.Close()
	return recipephoto.Prepare(ctx, f)
}

// photoFiles are the files that came in the part "file", without the empty ones a form sends for a picker nobody used.
func photoFiles(form *multipart.Form) []*multipart.FileHeader {
	var out []*multipart.FileHeader
	if form == nil {
		return nil
	}
	for _, h := range form.File["file"] {
		if h.Size > 0 {
			out = append(out, h)
		}
	}
	return out
}

// photoReason is the sentence for a file that was refused, after the name.
func (c *itemCtx) photoReason(h *multipart.FileHeader, err error) string {
	var big *picture.TooLargeError
	switch {
	case errors.As(err, &big):
		return c.t("rph.err.big", "size", humanBytes(c.lang, max(big.Bytes, h.Size)))
	case errors.Is(err, picture.ErrTooManyPixels):
		return c.t("rph.err.pixels")
	case errors.Is(err, recipephoto.ErrHEIC):
		return c.t("rph.err.heic")
	case errors.Is(err, store.ErrRecipePhotosFull):
		return c.t("rph.err.full")
	case errors.Is(err, picture.ErrNotAnImage), errors.Is(err, picture.ErrWebPage):
		return c.t("rph.err.notimage")
	}
	return c.t("rph.err.read")
}

// ---- the pages

func (s *server) photoPageContext(w http.ResponseWriter, r *http.Request, p principal) (*itemCtx, storeRecipeRef, bool) {
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "recipe context", err)
		return nil, storeRecipeRef{}, false
	}
	rec, _, err := c.recipeOrStarter(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return nil, storeRecipeRef{}, false
	}
	if err != nil {
		s.fail(w, "recipe", err)
		return nil, storeRecipeRef{}, false
	}
	return c, storeRecipeRef{rec.ID, rec.Name}, true
}

// rphRefused is the box "N Fotos nicht hinzugefügt" of the photos page.
type rphRefused struct {
	Title string
	List  []rphRefusal
}

// photosPage shows the page "Fotos" of a recipe. refused and success come from an upload that just ran.
func (s *server) photosPage(w http.ResponseWriter, r *http.Request, p principal, c *itemCtx, rec storeRecipeRef, status int, refused *rphRefused, success *i18n.Message, errText string) {
	list, err := s.db.RecipePhotos(p.household.ID, rec.ID)
	if err != nil {
		s.fail(w, "recipe photos", err)
		return
	}
	items := c.photoItems(rec.ID, rec.Name, list)
	fresh := map[string]bool{}
	for _, id := range strings.Split(r.URL.Query().Get("new"), ",") {
		fresh[id] = true
	}
	for i := range items {
		items[i].New = fresh[items[i].ID]
		if !items[i].Cover {
			continue
		}
		if len(list) == 1 {
			items[i].RemoveNote = c.t("rph.remove.last")
		} else {
			items[i].RemoveNote = c.nextCoverNote(list[1])
		}
	}
	texts, _ := json.Marshal(map[string]string{
		"uploading": c.t("rph.uploading"), "progress": c.t("rph.js.progress", "done", "{done}", "total", "{total}"),
		"convert": c.t("rph.js.convert", "name", "{name}"), "send": c.t("rph.js.send", "name", "{name}"),
	})
	data := map[string]any{
		"Title": c.t("rph.title"), "RecipeID": rec.ID, "RecipeName": rec.Name, "Items": items, "Count": c.photoCount(len(list)), "Any": len(list) > 0,
		"PhotoFull": len(list) >= store.MaxRecipePhotos, "Action": recipesPath + "/" + rec.ID + "/photos", "Texts": string(texts), "Refused": refused,
		"ListHref": recipesPath + "/" + rec.ID + "/photos",
	}
	if success != nil {
		data["SuccessMsg"] = *success
	}
	if errText != "" {
		data["Error"] = errText
	}
	s.render(w, r, status, "recipe_photos.html", data)
}

// nextCoverNote says which photo becomes the cover when the cover is removed (next is the one after it in the list).
func (c *itemCtx) nextCoverNote(next store.RecipePhoto) string {
	date := c.p.day(next.AddedAt)
	switch {
	case next.Origin == store.PhotoFromWeb:
		return c.t("rph.remove.next.web", "date", date)
	case next.AddedByName == "":
		return c.t("rph.remove.next.anon", "date", date)
	}
	return c.t("rph.remove.next", "name", next.AddedByName, "date", date)
}

func (s *server) handleRecipePhotos(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, rec, ok := s.photoPageContext(w, r, p)
	if !ok {
		return
	}
	s.photosPage(w, r, p, c, rec, http.StatusOK, nil, nil, "")
}

func (s *server) handleRecipePhotosUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	c, rec, ok := s.photoPageContext(w, r, p)
	if !ok {
		return
	}
	if s.recipePhotos == nil {
		http.Error(w, "this box keeps no photos", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoForm)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.photosPage(w, r, p, c, rec, http.StatusUnprocessableEntity, nil, nil, c.t("rph.err.read"))
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := photoFiles(r.MultipartForm)
	if len(files) == 0 {
		s.photosPage(w, r, p, c, rec, http.StatusUnprocessableEntity, nil, nil, c.t("rph.err.none"))
		return
	}
	before, err := s.db.RecipePhotos(p.household.ID, rec.ID)
	if err != nil {
		s.fail(w, "recipe photos", err)
		return
	}
	added, refused, err := s.addRecipePhotos(r.Context(), p, rec.ID, files, c.photoReason)
	if err != nil {
		s.fail(w, "add recipe photo", err)
		return
	}
	key := "flash.rph_added_keep"
	if len(before) == 0 {
		key = "flash.rph_added_first"
	}
	if len(added) == 1 {
		key += ".one"
	} else {
		key += ".other"
	}
	if len(refused) == 0 {
		ids := make([]string, len(added))
		for i, ph := range added {
			ids[i] = ph.ID
		}
		to := recipesPath + "/" + rec.ID + "/photos?" + url.Values{"new": {strings.Join(ids, ",")}}.Encode()
		s.redirectSavedFields(w, r, to, key, strconv.Itoa(len(added)))
		return
	}
	// Something was refused: the page shows the line and the files that failed together, so nothing is lost on the way.
	box := &rphRefused{List: refused}
	if len(refused) == 1 {
		box.Title = c.t("rph.refused.one")
	} else {
		box.Title = c.t("rph.refused.other", "n", strconv.Itoa(len(refused)))
	}
	for i := range box.List {
		box.List[i].Name = c.t("rph.refused.name", "name", box.List[i].Name)
	}
	if len(added) == 0 {
		s.photosPage(w, r, p, c, rec, http.StatusUnprocessableEntity, box, nil, "")
		return
	}
	msg := i18n.Msg(key, "n", strconv.Itoa(len(added)))
	s.photosPage(w, r, p, c, rec, http.StatusOK, box, &msg, "")
}

func (s *server) handleRecipePhotoCover(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	ref := r.PathValue("id")
	if err := s.db.SetRecipePhotoCover(p.household.ID, ref, r.PathValue("pid")); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "recipe photo cover", err)
		return
	}
	s.redirectSaved(w, r, recipesPath+"/"+ref+"/photos", "flash.rph_cover")
}

func (s *server) handleRecipePhotoDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	ref := r.PathValue("id")
	gone, next, err := s.removeRecipePhoto(p, ref, r.PathValue("pid"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "delete recipe photo", err)
		return
	}
	to := recipesPath + "/" + ref + "/photos"
	switch {
	case !gone.IsCover:
		s.redirectSaved(w, r, to, "flash.rph_removed")
	case next == nil:
		s.redirectSaved(w, r, to, "flash.rph_removed_last")
	case next.Origin == store.PhotoFromWeb:
		s.redirectSavedFields(w, r, to, "flash.rph_removed_cover_web", p.day(next.AddedAt))
	case next.AddedByName == "":
		s.redirectSavedFields(w, r, to, "flash.rph_removed_cover_anon", p.day(next.AddedAt))
	default:
		s.redirectSavedFields(w, r, to, "flash.rph_removed_cover", next.AddedByName, p.day(next.AddedAt))
	}
}

// removeRecipePhoto removes the row and the files of a photo.
func (s *server) removeRecipePhoto(p principal, ref, id string) (gone store.RecipePhoto, next *store.RecipePhoto, err error) {
	gone, next, err = s.db.DeleteRecipePhoto(p.household.ID, ref, id)
	if err == nil {
		// The row is gone, so is the photo for the user: a file that stays is logged, not an error.
		s.removeRecipePhotoFiles([]string{gone.ID})
	}
	return gone, next, err
}

// removeRecipePhotoFiles removes the files of photos whose rows are gone (a deleted recipe).
func (s *server) removeRecipePhotoFiles(ids []string) {
	if s.recipePhotos == nil {
		return
	}
	for _, id := range ids {
		if err := s.recipePhotos.Remove(id); err != nil {
			s.logger.Error("remove recipe photo file", "err", err)
		}
	}
}

// recipePhotoSize reads ?size=: medium, small or none for the full photo; ok is false for any other value.
func recipePhotoSize(r *http.Request) (size recipephoto.Size, ok bool) {
	switch r.URL.Query().Get("size") {
	case "medium":
		return recipephoto.Medium, true
	case "small":
		return recipephoto.Small, true
	case "":
		return recipephoto.Full, true
	}
	return recipephoto.Full, false
}

// serveRecipePhoto serves a photo of the given size to any member of the household, children included. Photos
// never change under their id, so the browser keeps them.
func (s *server) serveRecipePhoto(w http.ResponseWriter, r *http.Request, householdID, ref, id string, size recipephoto.Size) bool {
	ph, err := s.db.RecipePhoto(householdID, ref, id)
	if err != nil || s.recipePhotos == nil {
		return false
	}
	f, err := s.recipePhotos.Open(ph.ID, size)
	if err != nil {
		return false
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "", ph.AddedAt, f)
	return true
}

func (s *server) handleRecipePhotoFile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	size, ok := recipePhotoSize(r)
	if !ok {
		http.Error(w, "size is medium or small", http.StatusBadRequest)
		return
	}
	if !s.serveRecipePhoto(w, r, p.household.ID, r.PathValue("id"), r.PathValue("pid"), size) {
		http.NotFound(w, r)
	}
}

// ---- the REST API

type recipePhotoJSON struct {
	ID        string  `json:"id"`
	FileName  string  `json:"file_name"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	IsCover   bool    `json:"is_cover"`
	Origin    string  `json:"origin"`
	AddedBy   *string `json:"added_by"`
	AddedAt   string  `json:"added_at"`
	ImagePath string  `json:"image_path"`
}

func recipePhotoToJSON(ph store.RecipePhoto) recipePhotoJSON {
	return recipePhotoJSON{ID: ph.ID, FileName: ph.FileName, Width: ph.Width, Height: ph.Height, IsCover: ph.IsCover, Origin: ph.Origin,
		AddedBy: strPtr(ph.AddedBy), AddedAt: ph.AddedAt.UTC().Format(time.RFC3339), ImagePath: "/api/v1/recipes/" + ph.RecipeRef + "/photos/" + ph.ID + "/image"}
}

// apiRecipeExists answers 404 for a recipe that is not there.
func (s *server) apiRecipeExists(w http.ResponseWriter, p principal, ref string) bool {
	ok, err := s.recipeRefExists(p.household.ID, ref)
	if err != nil {
		s.fail(w, "recipe", err)
		return false
	}
	if !ok {
		writeError(w, http.StatusNotFound, "recipe not found")
	}
	return ok
}

func (s *server) apiListRecipePhotos(w http.ResponseWriter, r *http.Request, p principal) {
	ref := r.PathValue("id")
	if !s.apiRecipeExists(w, p, ref) {
		return
	}
	list, err := s.db.RecipePhotos(p.household.ID, ref)
	if err != nil {
		s.fail(w, "recipe photos", err)
		return
	}
	out := make([]recipePhotoJSON, 0, len(list))
	for _, ph := range list {
		out = append(out, recipePhotoToJSON(ph))
	}
	writeJSON(w, http.StatusOK, out)
}

type recipePhotoRefusedJSON struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func (s *server) apiAddRecipePhotos(w http.ResponseWriter, r *http.Request, p principal) {
	ref := r.PathValue("id")
	if !s.apiRecipeExists(w, p, ref) {
		return
	}
	if s.recipePhotos == nil {
		writeError(w, http.StatusServiceUnavailable, "this box keeps no photos: it has no data directory for them")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoForm)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			writeError(w, http.StatusRequestEntityTooLarge, "photos are at most 15 MB each")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "send the photos as multipart/form-data in the part file")
		return
	}
	defer r.MultipartForm.RemoveAll()
	files := photoFiles(r.MultipartForm)
	if len(files) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "send the photos as multipart/form-data in the part file")
		return
	}
	reason := func(h *multipart.FileHeader, err error) string {
		var big *picture.TooLargeError
		switch {
		case errors.As(err, &big):
			return "the photo is over 15 MB"
		case errors.Is(err, picture.ErrTooManyPixels):
			return "the photo has too many pixels"
		case errors.Is(err, recipephoto.ErrHEIC):
			return "HEIC is not taken here: convert it to JPEG first"
		case errors.Is(err, store.ErrRecipePhotosFull):
			return "a recipe holds at most 12 photos"
		case errors.Is(err, picture.ErrNotAnImage), errors.Is(err, picture.ErrWebPage):
			return "the file is not a JPEG, PNG, WebP or GIF picture"
		}
		return "the file could not be read"
	}
	added, refused, err := s.addRecipePhotos(r.Context(), p, ref, files, reason)
	if err != nil {
		s.fail(w, "add recipe photo", err)
		return
	}
	out := struct {
		Added   []recipePhotoJSON        `json:"added"`
		Refused []recipePhotoRefusedJSON `json:"refused"`
	}{[]recipePhotoJSON{}, []recipePhotoRefusedJSON{}}
	for _, ph := range added {
		if cur, err := s.db.RecipePhoto(p.household.ID, ref, ph.ID); err == nil {
			ph = cur // with the cover mark as it is now
		}
		out.Added = append(out.Added, recipePhotoToJSON(ph))
	}
	for _, f := range refused {
		out.Refused = append(out.Refused, recipePhotoRefusedJSON{f.Name, f.Reason})
	}
	status := http.StatusCreated
	if len(added) == 0 {
		status = http.StatusUnprocessableEntity
		if len(refused) == 1 && strings.Contains(refused[0].Reason, "15 MB") {
			status = http.StatusRequestEntityTooLarge
		}
	}
	writeJSON(w, status, out)
}

func (s *server) apiSetRecipePhotoCover(w http.ResponseWriter, r *http.Request, p principal) {
	ref, id := r.PathValue("id"), r.PathValue("photo_id")
	if s.storeError(w, "recipe photo cover", s.db.SetRecipePhotoCover(p.household.ID, ref, id)) {
		return
	}
	ph, err := s.db.RecipePhoto(p.household.ID, ref, id)
	if s.storeError(w, "recipe photo", err) {
		return
	}
	writeJSON(w, http.StatusOK, recipePhotoToJSON(ph))
}

func (s *server) apiDeleteRecipePhoto(w http.ResponseWriter, r *http.Request, p principal) {
	if _, _, err := s.removeRecipePhoto(p, r.PathValue("id"), r.PathValue("photo_id")); s.storeError(w, "delete recipe photo", err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiRecipePhotoImage(w http.ResponseWriter, r *http.Request, p principal) {
	size, ok := recipePhotoSize(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "size is medium or small")
		return
	}
	if !s.serveRecipePhoto(w, r, p.household.ID, r.PathValue("id"), r.PathValue("photo_id"), size) {
		writeError(w, http.StatusNotFound, "photo not found")
	}
}
