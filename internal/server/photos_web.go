package server

import (
	"errors"
	"html"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/photo"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The pages for photos and albums (ticket 178, design preview-178). Adults only.

// gbText writes bytes as gigabytes with at most two decimals ("1,24" in German).
func (s *server) gbText(lang string, b int64) string {
	g := math.Round(float64(b)/float64(1<<30)*100) / 100
	t := strconv.FormatFloat(g, 'f', -1, 64)
	if s.i18n.T(lang, "photos.decimal") == "," {
		t = strings.Replace(t, ".", ",", 1)
	}
	return t
}

// groupedInt writes a number with the language's thousands separator.
func (s *server) groupedInt(lang string, n int) string {
	sep := s.i18n.T(lang, "photos.thousands")
	t := strconv.Itoa(n)
	for i := len(t) - 3; i > 0; i -= 3 {
		t = t[:i] + sep + t[i:]
	}
	return t
}

// plural picks the .one or .other form of a text.
func (s *server) plural(lang, key string, n int, kv ...string) string {
	form := "other"
	if n == 1 {
		form = "one"
	}
	return s.i18n.Format(lang, i18n.Msg(key+"."+form, append([]string{"n", strconv.Itoa(n)}, kv...)...))
}

// countText is how many photos an album holds, in words.
func (s *server) countText(lang string, a store.Album) string {
	switch {
	case a.Photos == 0:
		return s.i18n.T(lang, "photos.n.empty")
	case a.Hidden > 0:
		return s.plural(lang, "photos.n.with_hidden", a.Photos, "h", strconv.Itoa(a.Hidden))
	}
	return s.plural(lang, "photos.n", a.Photos)
}

type spaceView struct {
	UsedText, Hint string
	Pct, LimitGB   int
	Full           bool
}

// estimatedPhotos is how many more photos fit, rounded to two digits.
func estimatedPhotos(free, used int64, n int) int {
	avg := int64(500 << 10)
	if n > 0 && used > 0 {
		avg = max(used/int64(n), 50<<10)
	}
	est := int(free / avg)
	if est < 100 {
		return est
	}
	scale := int(math.Pow(10, math.Floor(math.Log10(float64(est)))-1))
	return est / scale * scale
}

// spaceView describes the space for photos in words.
func (s *server) spaceView(lang string, sp photoSpace) spaceView {
	free := max(sp.Limit-sp.Used, 0)
	v := spaceView{Full: sp.Full, LimitGB: sp.LimitGB, Pct: int(min(sp.Used*100/max(sp.Limit, 1), 100))}
	v.UsedText = s.i18n.Format(lang, i18n.Msg("photos.used", "used", s.gbText(lang, sp.Used), "limit", strconv.Itoa(sp.LimitGB)))
	if sp.Full {
		v.Hint = s.i18n.T(lang, "photos.full.hint")
		if disk, ok := s.diskFree(); ok {
			v.Hint += s.i18n.Format(lang, i18n.Msg("photos.full.disk", "free", s.gbText(lang, disk)))
		}
		v.Hint += "."
		for _, w := range sp.Waiting {
			key := "photos.full.waiting"
			if w.Folder == "" {
				key = "photos.full.waiting_root"
			}
			v.Hint += " " + s.plural(lang, key, w.Count, "folder", w.Folder)
		}
		return v
	}
	est := estimatedPhotos(free, sp.Used, sp.Photos)
	key := "photos.space.more"
	if sp.Photos == 0 {
		key = "photos.space.empty"
	}
	v.Hint = s.i18n.Format(lang, i18n.Msg("photos.space.free", "free", s.gbText(lang, free)))
	if est >= 10 {
		v.Hint += " " + s.i18n.Format(lang, i18n.Msg(key, "n", s.groupedInt(lang, est)))
	}
	if sp.Photos == 0 {
		v.Hint = s.i18n.Format(lang, i18n.Msg(key, "n", s.groupedInt(lang, est)))
	}
	return v
}

func (s *server) diskFree() (int64, bool) {
	if s.photos == nil {
		return 0, false
	}
	return freeDiskBytes(filepath.Dir(s.photos.folder))
}

type albumRow struct {
	ID, Name, Cover, Folder, Count string
	FromFolder                     bool
	Displays                       []displayRef
}

type albumOption struct {
	ID, Name       string
	Selected, Here bool
}

type refusedFile struct{ Name, Reason string }

// photoTexts are the texts the upload script shows.
func (s *server) photoTexts(lang string) string {
	keys := []string{"wait", "convert", "upload", "done", "not_photo", "full", "too_large", "failed", "heic", "progress", "keep_open", "skip", "added", "refused", "refused_full", "reload"}
	m := make(map[string]string, len(keys))
	for _, k := range keys {
		m[k] = s.i18n.T(lang, "photos.up."+k)
	}
	return jsonString(m)
}

func (s *server) photosData(r *http.Request, p principal, selected string) (map[string]any, error) {
	lang := s.langOf(r)
	albums, err := s.photoAlbums(p.household, lang)
	if err != nil {
		return nil, err
	}
	displays, err := s.db.Displays(p.household.ID)
	if err != nil {
		return nil, err
	}
	sp, err := s.photoSpace(p.household.ID)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Space": s.spaceView(lang, sp), "HasPhotos": sp.Photos > 0, "Texts": s.photoTexts(lang)}
	if sp.Pending > 0 {
		data["Pending"], data["PendingText"] = true, s.plural(lang, "photos.pending", sp.Pending)
	}
	var rows []albumRow
	var opts []albumOption
	for _, a := range albums {
		rows = append(rows, albumRow{ID: a.ID, Name: a.Name, Cover: a.Cover, Folder: a.Folder, FromFolder: a.FromFolder(), Count: s.countText(lang, a), Displays: showingDisplays(displays, a)})
		if !a.FromFolder() {
			opts = append(opts, albumOption{ID: a.ID, Name: a.Name, Selected: a.ID == selected || (selected == "" && a.Default)})
		}
	}
	data["Albums"], data["AlbumOptions"] = rows, opts
	data["LimitSummary"] = s.i18n.Format(lang, i18n.Msg("photos.limit.summary", "gb", strconv.Itoa(sp.LimitGB)))
	hint := s.i18n.T(lang, "photos.limit.hint")
	if disk, ok := s.diskFree(); ok {
		hint = s.i18n.Format(lang, i18n.Msg("photos.limit.hint_disk", "free", s.gbText(lang, disk))) + " " + hint
	}
	data["LimitHint"] = hint
	return data, nil
}

func (s *server) renderPhotos(w http.ResponseWriter, r *http.Request, p principal, status int, extra map[string]any, selected string) {
	data, err := s.photosData(r, p, selected)
	if err != nil {
		s.fail(w, "photos", err)
		return
	}
	for k, v := range extra {
		data[k] = v
	}
	s.render(w, r, status, "photos.html", data)
}

func (s *server) handlePhotosPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderPhotos(w, r, p, http.StatusOK, nil, "")
	}
}

// handlePhotosUpload takes photos from the form without script: the web page
// uploads them one by one in the background and only falls back to this.
func (s *server) handlePhotosUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	if s.photos == nil {
		s.renderPhotos(w, r, p, http.StatusServiceUnavailable, map[string]any{"Error": "photos.error.unavailable"}, "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.renderPhotos(w, r, p, http.StatusRequestEntityTooLarge, map[string]any{"Error": "photos.error.too_large"}, "")
		return
	}
	defer r.MultipartForm.RemoveAll()
	albumID := r.PostFormValue("album")
	album, err := s.db.Album(p.household.ID, albumID)
	if err != nil || album.FromFolder() {
		http.NotFound(w, r)
		return
	}
	added := 0
	var refused []refusedFile
	for _, hdr := range r.MultipartForm.File["file"] {
		if hdr.Filename == "" {
			continue
		}
		f, err := hdr.Open()
		if err != nil {
			refused = append(refused, refusedFile{hdr.Filename, s.i18n.T(lang, "photos.up.failed")})
			continue
		}
		_, err = s.addPhoto(p.household.ID, albumID, hdr.Filename, f)
		f.Close()
		switch {
		case err == nil:
			added++
		case errors.Is(err, store.ErrPhotosFull):
			refused = append(refused, refusedFile{hdr.Filename, ""})
		case errors.Is(err, photo.ErrNotPhoto):
			refused = append(refused, refusedFile{hdr.Filename, s.i18n.T(lang, "photos.up.not_photo_reason")})
		case errors.Is(err, photo.ErrTooLarge):
			refused = append(refused, refusedFile{hdr.Filename, s.i18n.T(lang, "photos.up.too_large")})
		default:
			s.logger.Error("upload photo", "err", err)
			refused = append(refused, refusedFile{hdr.Filename, s.i18n.T(lang, "photos.up.failed")})
		}
	}
	extra := map[string]any{}
	status := http.StatusOK
	if added > 0 {
		extra["Added"] = s.plural(lang, "photos.up.added_text", added, "album", album.Name)
	}
	if len(refused) > 0 {
		extra["Refused"], extra["RefusedTitle"] = refused, s.plural(lang, "photos.up.refused_text", len(refused))
		if added == 0 {
			status = http.StatusUnprocessableEntity
		}
	}
	s.renderPhotos(w, r, p, status, extra, albumID)
}

func (s *server) handleAlbumCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	name := r.PostFormValue("album_name")
	a, err := s.db.CreateAlbum(p.household.ID, name)
	var fe *store.FieldError
	switch {
	case errors.As(err, &fe):
		s.renderPhotos(w, r, p, http.StatusUnprocessableEntity, map[string]any{"Error": "photos.error." + albumErrorKey(fe.Msg), "ErrorField": "album_name", "NewName": name}, "")
	case err != nil:
		s.fail(w, "create album", err)
	default:
		s.redirectSavedNamed(w, r, "/photos/albums/"+a.ID, "flash.album_created", a.Name)
	}
}

// albumErrorKey picks the text of a refused album name.
func albumErrorKey(msg string) string {
	switch {
	case strings.Contains(msg, "exists"):
		return "album_taken"
	case strings.Contains(msg, "at most"):
		return "album_long"
	}
	return "album_empty"
}

func (s *server) handleLimitSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	gb, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("photo_limit")))
	if err == nil {
		err = s.db.SetPhotoLimitGB(p.household.ID, gb)
	}
	if err != nil {
		s.renderPhotos(w, r, p, http.StatusUnprocessableEntity, map[string]any{"Error": "photos.error.limit", "ErrorField": "photo_limit"}, "")
		return
	}
	s.redirectSaved(w, r, "/photos", "flash.photo_limit_saved")
}

// albumTargets are the uploaded albums, for moving photos.
func albumTargets(albums []store.Album, here string) []albumOption {
	var out []albumOption
	for _, a := range albums {
		if !a.FromFolder() {
			out = append(out, albumOption{ID: a.ID, Name: a.Name, Here: a.ID == here})
		}
	}
	return out
}

type afterDelete struct {
	ID, Name, After string
}

func (s *server) quotedAlbums(lang string, names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = s.i18n.Format(lang, i18n.Msg("photos.quoted", "name", n))
	}
	return strings.Join(q, ", ")
}

func (s *server) renderAlbum(w http.ResponseWriter, r *http.Request, p principal, status int, albumID string, extra map[string]any) {
	lang := s.langOf(r)
	albums, err := s.photoAlbums(p.household, lang)
	if err != nil {
		s.fail(w, "album", err)
		return
	}
	var album store.Album
	found := false
	for _, a := range albums {
		if a.ID == albumID {
			album, found = a, true
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	displays, err := s.db.Displays(p.household.ID)
	if err != nil {
		s.fail(w, "album", err)
		return
	}
	photos, err := s.db.Photos(p.household.ID, albumID)
	if err != nil {
		s.fail(w, "album photos", err)
		return
	}
	sp, err := s.photoSpace(p.household.ID)
	if err != nil {
		s.fail(w, "album", err)
		return
	}
	count := s.plural(lang, "photos.fact.count", album.Photos)
	if album.Hidden > 0 {
		count = s.plural(lang, "photos.fact.count_hidden", album.Photos, "h", strconv.Itoa(album.Hidden))
	}
	origin := s.i18n.T(lang, "photos.origin.upload")
	if album.FromFolder() {
		origin = s.i18n.Format(lang, i18n.Msg("photos.origin.folder", "folder", album.Folder))
	}
	shown := showingDisplays(displays, album)
	var after []afterDelete
	others := []string{}
	for _, a := range albums {
		if a.ID != album.ID {
			others = append(others, a.Name)
		}
	}
	for _, d := range displays {
		if !d.Saver.Shows(album.ID) {
			continue
		}
		var text string
		if len(d.Saver.Albums) == 0 {
			text = s.i18n.T(lang, "photos.after.all")
		} else {
			var left []string
			for _, a := range albums {
				if a.ID != album.ID && d.Saver.Shows(a.ID) {
					left = append(left, a.Name)
				}
			}
			if len(left) == 0 {
				text = s.i18n.T(lang, "photos.after.back_to_all")
			} else {
				text = s.i18n.Format(lang, i18n.Msg("photos.after.only", "albums", s.quotedAlbums(lang, left)))
			}
		}
		after = append(after, afterDelete{d.ID, d.Name, text})
	}
	var explain template.HTML
	confirm := s.i18n.Format(lang, i18n.Msg("photos.delete.confirm_empty", "album", album.Name))
	if album.Photos == 0 {
		explain = template.HTML(html.EscapeString(s.i18n.Format(lang, i18n.Msg("photos.delete.explain_empty", "album", album.Name))))
	} else {
		bold := "<b>" + html.EscapeString(s.plural(lang, "photos.n", album.Photos)) + "</b>"
		explain = template.HTML(strings.NewReplacer("{photos}", bold, "{album}", html.EscapeString(album.Name)).Replace(html.EscapeString(s.i18n.T(lang, "photos.delete.explain"))))
		confirm = s.plural(lang, "photos.delete.confirm", album.Photos, "album", album.Name)
	}
	data := map[string]any{
		"Person": p.person, "Album": album, "Count": count, "CountTitle": s.plural(lang, "photos.n", album.Photos),
		"Origin": origin, "Displays": shown, "Photos": photos, "Targets": albumTargets(albums, album.ID), "Full": sp.Full,
		"AfterDelete": after, "DeleteExplain": explain, "DeleteConfirm": confirm, "RenameValue": album.Name,
		"Texts": s.photoTexts(lang), "Title": album.Name, "SyncStale": s.syncStale(p.household.ID),
	}
	if album.Photos == 0 {
		data["CountTitle"] = s.i18n.T(lang, "photos.n.empty_title")
	}
	for k, v := range extra {
		data[k] = v
	}
	s.render(w, r, status, "photo_album.html", data)
}

func (s *server) handleAlbumPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderAlbum(w, r, p, http.StatusOK, r.PathValue("id"), nil)
	}
}

func (s *server) handleAlbumRename(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	id, name := r.PathValue("id"), r.PostFormValue("album_name")
	err := s.db.RenameAlbum(p.household.ID, id, name)
	var fe *store.FieldError
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.As(err, &fe):
		s.renderAlbum(w, r, p, http.StatusUnprocessableEntity, id, map[string]any{"Error": "photos.error." + albumErrorKey(fe.Msg), "ErrorField": "album_name", "RenameValue": name})
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
	case err != nil:
		s.fail(w, "rename album", err)
	default:
		s.redirectSavedNamed(w, r, "/photos/albums/"+id, "flash.album_renamed", strings.TrimSpace(name))
	}
}

func (s *server) handleAlbumDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	a, err := s.db.Album(p.household.ID, id)
	if err == nil {
		err = s.deleteAlbum(p.household.ID, id)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
	case err != nil:
		s.fail(w, "delete album", err)
	default:
		s.redirectSavedNamed(w, r, "/photos", "flash.album_deleted", a.Name)
	}
}

// handleSelection moves or deletes the photos ticked on an album page.
func (s *server) handleSelection(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id := r.PathValue("id")
	back := "/photos/albums/" + id
	if _, err := s.db.Album(p.household.ID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	ids := r.PostForm["photo"]
	if len(ids) == 0 {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	n := strconv.Itoa(len(ids))
	switch r.PostFormValue("do") {
	case "move":
		target, err := s.db.Album(p.household.ID, r.PostFormValue("target"))
		if err == nil {
			err = s.db.MovePhotos(p.household.ID, ids, target.ID)
		}
		switch {
		case errors.Is(err, store.ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, store.ErrInvalid):
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		case err != nil:
			s.fail(w, "move photos", err)
		default:
			key := "flash.photos_moved"
			if len(ids) == 1 {
				key = "flash.photo_moved"
			}
			s.redirectSavedFields(w, r, back, key, n, target.Name)
		}
	case "delete":
		gone, err := s.db.DeletePhotos(p.household.ID, ids)
		switch {
		case errors.Is(err, store.ErrInvalid):
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		case err != nil:
			s.fail(w, "delete photos", err)
		default:
			s.removePhotoFiles(gone)
			key := "flash.photos_deleted"
			if len(gone) == 1 {
				key = "flash.photo_deleted"
			}
			s.redirectSavedFields(w, r, back, key, strconv.Itoa(len(gone)))
		}
	default:
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	}
}

// handlePhotoHidden is the switch "on displays" of one photo; an unticked box sends nothing.
func (s *server) handlePhotoHidden(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	id := r.PathValue("id")
	ph, err := s.db.Photo(p.household.ID, id)
	if err == nil {
		err = s.db.SetPhotoHidden(p.household.ID, id, r.PostFormValue("shown") == "")
	}
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "hide photo", err)
		return
	}
	http.Redirect(w, r, "/photos/albums/"+url.PathEscape(ph.AlbumID)+"#photo-"+id, http.StatusSeeOther)
}

// photoHubState is the state the Settings hub names before the hint of the photos row.
func (s *server) photoHubState(lang string, h store.Household) string {
	used, n, err := s.db.PhotoUsage(h.ID)
	if err != nil || n == 0 {
		return s.i18n.T(lang, "hub.photos.none")
	}
	albums, err := s.db.Albums(h.ID)
	if err != nil {
		return ""
	}
	gb, _ := s.db.PhotoLimitGB(h.ID)
	return s.i18n.Format(lang, i18n.Msg("hub.photos.state", "photos", s.plural(lang, "photos.n", n), "albums", s.plural(lang, "photos.albums_n", len(albums)),
		"used", s.gbText(lang, used), "limit", strconv.Itoa(gb)))
}
