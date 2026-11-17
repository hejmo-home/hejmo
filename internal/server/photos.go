package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/photo"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Photo screensaver (ticket 178, ADR 0020). Photos and albums are managed by
// adults only: the routes need the scopes of displays, which children never
// have.

type displayRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type albumJSON struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Source       string       `json:"source"`
	Folder       string       `json:"folder"`
	Default      bool         `json:"default"`
	PhotoCount   int          `json:"photo_count"`
	HiddenCount  int          `json:"hidden_count"`
	CoverPhotoID *string      `json:"cover_photo_id"`
	Displays     []displayRef `json:"displays"`
}

type photoJSON struct {
	ID        string `json:"id"`
	AlbumID   string `json:"album_id"`
	Name      string `json:"name"`
	Hidden    bool   `json:"hidden"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	CreatedAt string `json:"created_at"`
}

func photoToJSON(p store.Photo) photoJSON {
	return photoJSON{ID: p.ID, AlbumID: p.AlbumID, Name: p.Name, Hidden: p.Hidden, Width: p.Width, Height: p.Height, CreatedAt: p.Created.UTC().Format(time.RFC3339)}
}

// showingDisplays lists the displays that show an album, in pairing order.
func showingDisplays(displays []store.Display, a store.Album) []displayRef {
	out := []displayRef{}
	for _, d := range displays {
		if d.Saver.Shows(a.ID) {
			out = append(out, displayRef{d.ID, d.Name})
		}
	}
	return out
}

func albumToJSON(a store.Album, displays []store.Display) albumJSON {
	j := albumJSON{ID: a.ID, Name: a.Name, Source: "upload", Folder: a.Folder, Default: a.Default, PhotoCount: a.Photos, HiddenCount: a.Hidden, Displays: showingDisplays(displays, a)}
	if a.FromFolder() {
		j.Source = "folder"
	}
	if a.Cover != "" {
		j.CoverPhotoID = &a.Cover
	}
	return j
}

// photoAlbums brings the album list up to date with the folder and returns it.
func (s *server) photoAlbums(h store.Household, lang string) ([]store.Album, error) {
	if _, err := s.db.EnsureDefaultAlbum(h.ID, s.i18n.T(lang, "photos.album.default")); err != nil {
		return nil, err
	}
	s.syncPhotoFolder(h, photoScanBudget)
	return s.db.Albums(h.ID)
}

func (s *server) photosUnavailable(w http.ResponseWriter) bool {
	if s.photos == nil {
		writeError(w, http.StatusServiceUnavailable, "this box keeps no photos: it has no data directory for them")
		return true
	}
	return false
}

func (s *server) apiListAlbums(w http.ResponseWriter, r *http.Request, p principal) {
	albums, err := s.photoAlbums(p.household, s.langOf(r))
	if err != nil {
		s.fail(w, "albums", err)
		return
	}
	displays, err := s.db.Displays(p.household.ID)
	if err != nil {
		s.fail(w, "albums", err)
		return
	}
	out := make([]albumJSON, 0, len(albums))
	for _, a := range albums {
		out = append(out, albumToJSON(a, displays))
	}
	writeJSON(w, http.StatusOK, out)
}

// albumReply writes one album as JSON.
func (s *server) albumReply(w http.ResponseWriter, status int, householdID, id string) {
	a, err := s.db.Album(householdID, id)
	if err == nil {
		var displays []store.Display
		if displays, err = s.db.Displays(householdID); err == nil {
			writeJSON(w, status, albumToJSON(a, displays))
			return
		}
	}
	s.fail(w, "album", err)
}

// photoError answers a refused photo operation with the matching status.
func (s *server) photoError(w http.ResponseWriter, what string, err error) {
	var fe *store.FieldError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "album or photo not found")
	case errors.As(err, &fe):
		writeFieldError(w, http.StatusUnprocessableEntity, fe.Field, fe.Msg)
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), "invalid: "))
	case errors.Is(err, store.ErrPhotosFull):
		writeError(w, http.StatusInsufficientStorage, "the space for photos is full; nothing was deleted. Delete photos you do not need or raise the limit")
	case errors.Is(err, photo.ErrNotPhoto):
		writeError(w, http.StatusUnprocessableEntity, "not a photo: only JPEG and PNG are kept (the web interface converts HEIC before it sends it)")
	case errors.Is(err, photo.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "the photo is too large (at most 64 MB and 60 megapixels)")
	default:
		s.fail(w, what, err)
	}
}

func (s *server) apiCreateAlbum(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct{ Name string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	a, err := s.db.CreateAlbum(p.household.ID, in.Name)
	if err != nil {
		s.photoError(w, "create album", err)
		return
	}
	s.albumReply(w, http.StatusCreated, p.household.ID, a.ID)
}

func (s *server) apiGetAlbum(w http.ResponseWriter, r *http.Request, p principal) {
	if _, err := s.photoAlbums(p.household, s.langOf(r)); err != nil {
		s.fail(w, "album", err)
		return
	}
	if _, err := s.db.Album(p.household.ID, r.PathValue("id")); err != nil {
		s.photoError(w, "album", err)
		return
	}
	s.albumReply(w, http.StatusOK, p.household.ID, r.PathValue("id"))
}

func (s *server) apiRenameAlbum(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct{ Name string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.db.RenameAlbum(p.household.ID, r.PathValue("id"), in.Name); err != nil {
		s.photoError(w, "rename album", err)
		return
	}
	s.albumReply(w, http.StatusOK, p.household.ID, r.PathValue("id"))
}

func (s *server) apiDeleteAlbum(w http.ResponseWriter, r *http.Request, p principal) {
	if err := s.deleteAlbum(p.household.ID, r.PathValue("id")); err != nil {
		s.photoError(w, "delete album", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteAlbum deletes an uploaded album, its photos and their files.
func (s *server) deleteAlbum(householdID, id string) error {
	ids, err := s.db.AlbumPhotoIDs(householdID, id)
	if err != nil {
		return err
	}
	if err := s.db.DeleteAlbum(householdID, id); err != nil {
		return err
	}
	s.removePhotoFiles(ids)
	return nil
}

func (s *server) removePhotoFiles(ids []string) {
	if s.photos == nil {
		return
	}
	for _, id := range ids {
		if err := s.photos.files.Remove(id); err != nil {
			s.logger.Error("remove photo file", "id", id, "err", err)
		}
	}
}

func (s *server) apiListAlbumPhotos(w http.ResponseWriter, r *http.Request, p principal) {
	if _, err := s.db.Album(p.household.ID, r.PathValue("id")); err != nil {
		s.photoError(w, "album photos", err)
		return
	}
	list, err := s.db.Photos(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, "album photos", err)
		return
	}
	out := make([]photoJSON, 0, len(list))
	for _, ph := range list {
		out = append(out, photoToJSON(ph))
	}
	writeJSON(w, http.StatusOK, out)
}

// photoName is the name a photo is listed under: the file name without any path.
func photoName(filename string) string {
	n := strings.TrimSpace(filepath.Base(strings.ReplaceAll(filename, `\`, "/")))
	if n == "" || n == "." || n == "/" {
		return "photo"
	}
	if utf8.RuneCountInString(n) > 120 {
		n = string([]rune(n)[:120])
	}
	return n
}

// addPhoto shrinks a photo and keeps it in an uploaded album.
func (s *server) addPhoto(householdID, albumID, filename string, src io.Reader) (store.Photo, error) {
	a, err := s.db.Album(householdID, albumID)
	if err != nil {
		return store.Photo{}, err
	}
	if a.FromFolder() {
		return store.Photo{}, fmt.Errorf("%w: photos are added to a folder album in the folder on the box", store.ErrInvalid)
	}
	s.photos.slots <- struct{}{}
	res, err := photo.Process(src)
	<-s.photos.slots
	if err != nil {
		return store.Photo{}, err
	}
	ph, err := s.db.AddPhoto(householdID, store.Photo{AlbumID: albumID, Name: photoName(filename), Bytes: int64(len(res.Display) + len(res.Thumb)), Width: res.Width, Height: res.Height})
	if err != nil {
		return store.Photo{}, err
	}
	if err := s.photos.files.Put(ph.ID, res.Display, res.Thumb); err != nil {
		_, _ = s.db.DeletePhotos(householdID, []string{ph.ID})
		return store.Photo{}, err
	}
	return ph, nil
}

func (s *server) apiUploadPhoto(w http.ResponseWriter, r *http.Request, p principal) {
	if s.photosUnavailable(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, photo.MaxBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.photoError(w, "upload", photo.ErrTooLarge)
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "send the photo as multipart/form-data in the part file")
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "send the photo as multipart/form-data in the part file")
		return
	}
	defer f.Close()
	ph, err := s.addPhoto(p.household.ID, r.PathValue("id"), hdr.Filename, f)
	if err != nil {
		s.photoError(w, "upload", err)
		return
	}
	writeJSON(w, http.StatusCreated, photoToJSON(ph))
}

func (s *server) apiUpdatePhoto(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Hidden  *bool   `json:"hidden"`
		AlbumID *string `json:"album_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	id := r.PathValue("id")
	if _, err := s.db.Photo(p.household.ID, id); err != nil {
		s.photoError(w, "update photo", err)
		return
	}
	if in.AlbumID != nil {
		if err := s.db.MovePhotos(p.household.ID, []string{id}, *in.AlbumID); err != nil {
			s.photoError(w, "move photo", err)
			return
		}
	}
	if in.Hidden != nil {
		if err := s.db.SetPhotoHidden(p.household.ID, id, *in.Hidden); err != nil {
			s.photoError(w, "hide photo", err)
			return
		}
	}
	ph, err := s.db.Photo(p.household.ID, id)
	if err != nil {
		s.photoError(w, "update photo", err)
		return
	}
	writeJSON(w, http.StatusOK, photoToJSON(ph))
}

func (s *server) apiDeletePhoto(w http.ResponseWriter, r *http.Request, p principal) {
	gone, err := s.db.DeletePhotos(p.household.ID, []string{r.PathValue("id")})
	if err != nil {
		s.photoError(w, "delete photo", err)
		return
	}
	if len(gone) == 0 {
		writeError(w, http.StatusNotFound, "photo not found")
		return
	}
	s.removePhotoFiles(gone)
	w.WriteHeader(http.StatusNoContent)
}

// serveStoredPhoto sends the box's copy of a photo.
func (s *server) serveStoredPhoto(w http.ResponseWriter, r *http.Request, ph store.Photo, thumb bool) {
	f, err := s.photos.files.Open(ph.ID, thumb)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	// The id of a folder photo stays when its file changes; the time of the
	// stored copy tells. no-cache: ask the box each time, it answers 304 if unchanged.
	modified := ph.Created
	if st, err := f.Stat(); err == nil {
		modified = st.ModTime()
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", modified, f)
}

func (s *server) apiPhotoImage(w http.ResponseWriter, r *http.Request, p principal) {
	if s.photos == nil {
		writeError(w, http.StatusNotFound, "photo not found")
		return
	}
	ph, err := s.db.Photo(p.household.ID, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "photo not found")
		return
	}
	s.serveStoredPhoto(w, r, ph, r.URL.Query().Get("size") == "thumbnail")
}

type photoStorageJSON struct {
	UsedBytes  int64           `json:"used_bytes"`
	LimitBytes int64           `json:"limit_bytes"`
	LimitGB    int             `json:"limit_gb"`
	PhotoCount int             `json:"photo_count"`
	Full       bool            `json:"full"`
	Waiting    []waitingFolder `json:"waiting"`
}

// photoSpaceLeft is the space under which the box counts the space for photos
// as full: no photo fits in it.
const photoSpaceLeft = 100 << 10

type photoSpace struct {
	Used, Limit int64
	LimitGB     int
	Photos      int
	Full        bool
	Waiting     []waitingFolder
	Pending     int
}

func (s *server) photoSpace(householdID string) (photoSpace, error) {
	used, n, err := s.db.PhotoUsage(householdID)
	if err != nil {
		return photoSpace{}, err
	}
	gb, err := s.db.PhotoLimitGB(householdID)
	if err != nil {
		return photoSpace{}, err
	}
	limit := int64(gb) << 30
	sp := photoSpace{Used: used, Limit: limit, LimitGB: gb, Photos: n, Full: limit-used < photoSpaceLeft}
	if s.photos != nil {
		sp.Waiting, sp.Pending = s.photos.folderState()
	}
	return sp, nil
}

func (s *server) apiPhotoStorage(w http.ResponseWriter, _ *http.Request, p principal) {
	s.syncPhotoFolder(p.household, photoScanBudget)
	sp, err := s.photoSpace(p.household.ID)
	if err != nil {
		s.fail(w, "photo storage", err)
		return
	}
	waiting := sp.Waiting
	if waiting == nil {
		waiting = []waitingFolder{}
	}
	writeJSON(w, http.StatusOK, photoStorageJSON{sp.Used, sp.Limit, sp.LimitGB, sp.Photos, sp.Full, waiting})
}

func (s *server) apiSetPhotoLimit(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		LimitGB *int `json:"limit_gb"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.LimitGB == nil {
		writeError(w, http.StatusBadRequest, "send limit_gb")
		return
	}
	if err := s.db.SetPhotoLimitGB(p.household.ID, *in.LimitGB); err != nil {
		s.photoError(w, "photo limit", err)
		return
	}
	s.apiPhotoStorage(w, r, p)
}
