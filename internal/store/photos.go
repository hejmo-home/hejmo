package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrPhotosFull is returned when a photo does not fit in the space for photos
// (the limit is set per household; the box never deletes photos by itself).
var ErrPhotosFull = errors.New("store: no space left for photos")

// Album is a named collection of screensaver photos (GLOSSARY.md). Folder is
// the subfolder of the box's photo folder an album comes from, empty for an
// uploaded album; the default album takes the first uploads and the photos
// lying directly in the folder.
type Album struct {
	ID, Name, Folder string
	Default          bool
	Photos, Hidden   int    // how many photos, and how many of them are hidden
	Cover            string // the newest shown photo, else the newest; empty without photos
}

// FromFolder reports whether the album is changed only in the box's folder.
func (a Album) FromFolder() bool { return a.Folder != "" }

// Photo is one screensaver photo: the box keeps a copy shrunk to display size.
type Photo struct {
	ID, AlbumID, Name string
	Hidden            bool
	Bytes             int64
	Width, Height     int
	SourcePath        string // a folder photo: its path in the folder
	SourceStamp       string // size and time of the source file when it was copied
	Created           time.Time
}

const maxAlbumName = 80

func albumNameError(name string) error {
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		return &FieldError{"album_name", "an album needs a name"}
	case n > maxAlbumName:
		return &FieldError{"album_name", "an album name may have at most 80 characters"}
	}
	return nil
}

func isNameTaken(err error) bool {
	return isUniqueViolation(err) && strings.Contains(err.Error(), "albums.name")
}

// EnsureDefaultAlbum returns the household's default album, making it under
// name when it does not exist yet.
func (db *DB) EnsureDefaultAlbum(householdID, name string) (Album, error) {
	if a, err := db.albumWhere(`household_id = ? AND is_default = 1`, householdID); !errors.Is(err, ErrNotFound) {
		return a, err
	}
	name = strings.TrimSpace(name)
	for i := 1; ; i++ { // a family album of the same name already exists: take a numbered one
		try := name
		if i > 1 {
			try = fmt.Sprintf("%s %d", name, i)
		}
		id := newID()
		_, err := db.sql.Exec(`INSERT INTO albums (id, household_id, name, is_default, created_at) VALUES (?, ?, ?, 1, ?)`, id, householdID, try, now())
		switch {
		case err == nil:
			return db.albumWhere(`id = ?`, id)
		case isNameTaken(err):
			continue
		case isUniqueViolation(err): // another request made it first
			return db.albumWhere(`household_id = ? AND is_default = 1`, householdID)
		default:
			return Album{}, err
		}
	}
}

// albumColumns reads an album with its counts; the cover is the newest shown
// photo, else the newest photo.
const albumColumns = `a.id, a.name, a.folder, a.is_default,
	(SELECT COUNT(*) FROM photos p WHERE p.album_id = a.id),
	(SELECT COUNT(*) FROM photos p WHERE p.album_id = a.id AND p.hidden = 1),
	COALESCE((SELECT p.id FROM photos p WHERE p.album_id = a.id ORDER BY p.hidden, p.created_at DESC, p.rowid DESC LIMIT 1), '')`

func scanAlbum(r rowScanner) (Album, error) {
	var a Album
	err := r.Scan(&a.ID, &a.Name, &a.Folder, &a.Default, &a.Photos, &a.Hidden, &a.Cover)
	return a, err
}

func (db *DB) albumWhere(where string, args ...any) (Album, error) {
	a, err := scanAlbum(db.sql.QueryRow(`SELECT `+albumColumns+` FROM albums a WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Album{}, ErrNotFound
	}
	return a, err
}

// Albums lists the household's albums: the default album first, then by name.
func (db *DB) Albums(householdID string) ([]Album, error) {
	rows, err := db.sql.Query(`SELECT `+albumColumns+` FROM albums a WHERE a.household_id = ? ORDER BY a.is_default DESC, a.created_at, a.rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Album
	for rows.Next() {
		a, err := scanAlbum(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Album loads one album.
func (db *DB) Album(householdID, id string) (Album, error) {
	return db.albumWhere(`a.household_id = ? AND a.id = ?`, householdID, id)
}

// CreateAlbum makes an uploaded album. A name another album already has (in
// any letter case) is refused.
func (db *DB) CreateAlbum(householdID, name string) (Album, error) {
	name = strings.TrimSpace(name)
	if err := albumNameError(name); err != nil {
		return Album{}, err
	}
	id := newID()
	if _, err := db.sql.Exec(`INSERT INTO albums (id, household_id, name, created_at) VALUES (?, ?, ?, ?)`, id, householdID, name, now()); err != nil {
		if isNameTaken(err) {
			return Album{}, &FieldError{"album_name", "an album with this name exists"}
		}
		return Album{}, err
	}
	return db.Album(householdID, id)
}

// RenameAlbum renames an uploaded album; a folder album is renamed in the folder.
func (db *DB) RenameAlbum(householdID, id, name string) error {
	name = strings.TrimSpace(name)
	if err := albumNameError(name); err != nil {
		return err
	}
	a, err := db.Album(householdID, id)
	if err != nil {
		return err
	}
	if a.FromFolder() {
		return fmt.Errorf("%w: a folder album is changed in the folder", ErrInvalid)
	}
	_, err = db.sql.Exec(`UPDATE albums SET name = ? WHERE household_id = ? AND id = ?`, name, householdID, id)
	if isNameTaken(err) {
		return &FieldError{"album_name", "an album with this name exists"}
	}
	return err
}

// AlbumPhotoIDs lists the photos of an album, so their files can be removed
// with it.
func (db *DB) AlbumPhotoIDs(householdID, id string) ([]string, error) {
	rows, err := db.sql.Query(`SELECT id FROM photos WHERE household_id = ? AND album_id = ? ORDER BY created_at, rowid`, householdID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteAlbum deletes an uploaded album with its photos. The default album and
// folder albums cannot be deleted. A display that showed only this album shows
// all albums afterwards.
func (db *DB) DeleteAlbum(householdID, id string) error {
	a, err := db.Album(householdID, id)
	if err != nil {
		return err
	}
	if a.Default || a.FromFolder() {
		return fmt.Errorf("%w: this album cannot be deleted here", ErrInvalid)
	}
	_, err = db.sql.Exec(`DELETE FROM albums WHERE household_id = ? AND id = ?`, householdID, id)
	return err
}

const photoColumns = `id, album_id, name, hidden, bytes, width, height, source_path, source_stamp, created_at`

func scanPhoto(r rowScanner) (Photo, error) {
	var p Photo
	var created sql.NullString
	err := r.Scan(&p.ID, &p.AlbumID, &p.Name, &p.Hidden, &p.Bytes, &p.Width, &p.Height, &p.SourcePath, &p.SourceStamp, &created)
	p.Created = parseStamp(created)
	return p, err
}

// GB is the unit of the limit for photos.
const gigabyte = int64(1) << 30

// AddPhoto stores a photo in an album. It is refused with ErrPhotosFull when
// the shrunk copy does not fit under the household's limit, and with ErrInvalid
// when a folder photo is already known.
func (db *DB) AddPhoto(householdID string, p Photo) (Photo, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || p.Bytes < 0 {
		return Photo{}, fmt.Errorf("%w: a photo needs a name", ErrInvalid)
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return Photo{}, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM albums WHERE household_id = ? AND id = ?`, householdID, p.AlbumID).Scan(&n); err != nil {
		return Photo{}, err
	}
	if n == 0 {
		return Photo{}, ErrNotFound
	}
	var used, limit int64
	if err := tx.QueryRow(`SELECT COALESCE(SUM(bytes), 0) FROM photos WHERE household_id = ?`, householdID).Scan(&used); err != nil {
		return Photo{}, err
	}
	if err := tx.QueryRow(`SELECT photo_limit_gb FROM households WHERE id = ?`, householdID).Scan(&limit); err != nil {
		return Photo{}, err
	}
	if used+p.Bytes > limit*gigabyte {
		return Photo{}, ErrPhotosFull
	}
	p.ID = newID()
	if _, err := tx.Exec(`INSERT INTO photos (`+photoColumns+`, household_id) VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.AlbumID, p.Name, p.Bytes, p.Width, p.Height, p.SourcePath, p.SourceStamp, now(), householdID); err != nil {
		if isUniqueViolation(err) {
			return Photo{}, fmt.Errorf("%w: this photo of the folder is known already", ErrInvalid)
		}
		return Photo{}, err
	}
	if err := tx.Commit(); err != nil {
		return Photo{}, err
	}
	return db.Photo(householdID, p.ID)
}

// Photo loads one photo.
func (db *DB) Photo(householdID, id string) (Photo, error) {
	p, err := scanPhoto(db.sql.QueryRow(`SELECT `+photoColumns+` FROM photos WHERE household_id = ? AND id = ?`, householdID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Photo{}, ErrNotFound
	}
	return p, err
}

// Photos lists an album's photos, newest first.
func (db *DB) Photos(householdID, albumID string) ([]Photo, error) {
	return db.photoList(`SELECT `+photoColumns+` FROM photos WHERE household_id = ? AND album_id = ? ORDER BY created_at DESC, rowid DESC`, householdID, albumID)
}

func (db *DB) photoList(query string, args ...any) ([]Photo, error) {
	rows, err := db.sql.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Photo
	for rows.Next() {
		p, err := scanPhoto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetPhotoHidden hides a photo from, or shows it on, all displays (Q38).
func (db *DB) SetPhotoHidden(householdID, id string, hidden bool) error {
	return expectOne(db.sql.Exec(`UPDATE photos SET hidden = ? WHERE household_id = ? AND id = ?`, hidden, householdID, id))
}

// MovePhotos moves uploaded photos to another uploaded album. Photos and
// albums from the folder stay where the folder puts them.
func (db *DB) MovePhotos(householdID string, ids []string, albumID string) error {
	to, err := db.Album(householdID, albumID)
	if err != nil {
		return err
	}
	if to.FromFolder() {
		return fmt.Errorf("%w: photos cannot be moved into a folder album", ErrInvalid)
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		var src string
		err := tx.QueryRow(`SELECT source_path FROM photos WHERE household_id = ? AND id = ?`, householdID, id).Scan(&src)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if src != "" {
			return fmt.Errorf("%w: a photo from the folder is moved in the folder", ErrInvalid)
		}
		if _, err := tx.Exec(`UPDATE photos SET album_id = ? WHERE household_id = ? AND id = ?`, albumID, householdID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeletePhotos deletes uploaded photos and returns the ones that existed, so
// the caller can remove their files. Photos from the folder are deleted in the
// folder.
func (db *DB) DeletePhotos(householdID string, ids []string) ([]string, error) {
	return db.deletePhotos(householdID, ids, false)
}

// RemoveFolderPhotos forgets photos whose file left the folder.
func (db *DB) RemoveFolderPhotos(householdID string, ids []string) ([]string, error) {
	return db.deletePhotos(householdID, ids, true)
}

func (db *DB) deletePhotos(householdID string, ids []string, folder bool) ([]string, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var gone []string
	for _, id := range ids {
		var src string
		err := tx.QueryRow(`SELECT source_path FROM photos WHERE household_id = ? AND id = ?`, householdID, id).Scan(&src)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if (src != "") != folder {
			return nil, fmt.Errorf("%w: a photo from the folder is deleted in the folder", ErrInvalid)
		}
		if _, err := tx.Exec(`DELETE FROM photos WHERE household_id = ? AND id = ?`, householdID, id); err != nil {
			return nil, err
		}
		gone = append(gone, id)
	}
	return gone, tx.Commit()
}

// PhotoUsage is the space the household's photos take (the shrunk copies) and
// how many photos there are.
func (db *DB) PhotoUsage(householdID string) (bytes int64, photos int, err error) {
	err = db.sql.QueryRow(`SELECT COALESCE(SUM(bytes), 0), COUNT(*) FROM photos WHERE household_id = ?`, householdID).Scan(&bytes, &photos)
	return
}

// PhotoLimitGB is the space for photos in GB (2 unless a parent changed it).
func (db *DB) PhotoLimitGB(householdID string) (int, error) {
	var gb int
	err := db.sql.QueryRow(`SELECT photo_limit_gb FROM households WHERE id = ?`, householdID).Scan(&gb)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return gb, err
}

// SetPhotoLimitGB sets the space for photos in GB.
func (db *DB) SetPhotoLimitGB(householdID string, gb int) error {
	if gb < 1 || gb > 100000 {
		return &FieldError{"photo_limit", "the limit for photos is at least 1 GB"}
	}
	return expectOne(db.sql.Exec(`UPDATE households SET photo_limit_gb = ? WHERE id = ?`, gb, householdID))
}

// EnsureFolderAlbum returns the album of a subfolder of the box's photo
// folder, making it when the folder is new.
func (db *DB) EnsureFolderAlbum(householdID, folder string) (Album, error) {
	if a, err := db.albumWhere(`a.household_id = ? AND a.folder = ?`, householdID, folder); !errors.Is(err, ErrNotFound) {
		return a, err
	}
	for i := 1; ; i++ { // an album of the same name exists: number the new one
		name := folder
		if i > 1 {
			name = fmt.Sprintf("%s (%d)", folder, i)
		}
		id := newID()
		_, err := db.sql.Exec(`INSERT INTO albums (id, household_id, name, folder, created_at) VALUES (?, ?, ?, ?, ?)`, id, householdID, name, folder, now())
		switch {
		case err == nil:
			return db.Album(householdID, id)
		case isNameTaken(err):
			continue
		case isUniqueViolation(err):
			return db.albumWhere(`a.household_id = ? AND a.folder = ?`, householdID, folder)
		default:
			return Album{}, err
		}
	}
}

// FolderPhotos returns the photos copied from the folder, by source path.
func (db *DB) FolderPhotos(householdID string) (map[string]Photo, error) {
	list, err := db.photoList(`SELECT `+photoColumns+` FROM photos WHERE household_id = ? AND source_path != ''`, householdID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Photo, len(list))
	for _, p := range list {
		out[p.SourcePath] = p
	}
	return out, nil
}

// RemoveFolderAlbumsNotIn deletes the folder albums whose folder is gone.
func (db *DB) RemoveFolderAlbumsNotIn(householdID string, folders []string) error {
	albums, err := db.Albums(householdID)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, f := range folders {
		keep[f] = true
	}
	for _, a := range albums {
		if a.FromFolder() && !keep[a.Folder] {
			if _, err := db.sql.Exec(`DELETE FROM albums WHERE household_id = ? AND id = ?`, householdID, a.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// SaverPhotos lists the photos a display shows as its screensaver: the shown
// ones of its albums, or of all albums when it has not chosen any.
func (db *DB) SaverPhotos(householdID string, d Display) ([]Photo, error) {
	if !d.Saver.Enabled {
		return nil, nil
	}
	q := `SELECT ` + photoColumns + ` FROM photos WHERE household_id = ? AND hidden = 0`
	args := []any{householdID}
	if len(d.Saver.Albums) > 0 {
		q += ` AND album_id IN (?` + strings.Repeat(`, ?`, len(d.Saver.Albums)-1) + `)`
		for _, a := range d.Saver.Albums {
			args = append(args, a)
		}
	}
	return db.photoList(q+` ORDER BY created_at, rowid`, args...)
}
