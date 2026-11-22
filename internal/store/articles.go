package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// OwnArticle is an article a household made (ADR 0019, ticket 164). Its
// picture is not part of it: HasPicture says there is one, ArticlePicture
// reads it.
type OwnArticle struct {
	ID               string
	Name, Dept, Kind string
	Allergens        []string
	Unit1, Unit2     string
	// PicSource is "" (the symbol of the department), "symbol" (Symbol, an
	// emoji), "upload" or "web" (a stored picture).
	PicSource, Symbol string

	HasPicture                 bool
	PictureHost                string // the site a web image came from
	PictureFromW, PictureFromH int
	PictureFromBytes           int
	PictureAt                  time.Time
	CreatedBy                  string // the person; "" when that person is gone
	CreatedAt                  time.Time
}

// ArticlePicture is a picture to store: the JPEG Hejmo made, and where it came from.
type ArticlePicture struct {
	JPEG         []byte
	Source       string // "upload" or "web"
	Host         string
	FromW, FromH int
	FromBytes    int
}

// PictureChange says what an update does to the stored picture.
type PictureChange int

// The three things an update can do to the picture.
const (
	KeepPicture PictureChange = iota
	SetPicture
	RemovePicture
)

func (a OwnArticle) input() library.Input {
	return library.Input{Name: a.Name, Dept: a.Dept, Kind: a.Kind, Allergens: a.Allergens, Unit1: a.Unit1, Unit2: a.Unit2, PicSource: a.PicSource, Symbol: a.Symbol}
}

// check applies the rules of the library; the units the household switched on
// are the web interface's business, so every unit is fine here.
func (a OwnArticle) check(pic *ArticlePicture) (OwnArticle, error) {
	in := a.input().Normalized()
	if pic != nil {
		in.PicSource = pic.Source
	}
	if errs := in.Validate(unit.All); len(errs) > 0 {
		return a, &FieldError{Field: errs[0].Field, Msg: "article " + errs[0].Field + ": " + errs[0].Code}
	}
	a.Name, a.Dept, a.Kind, a.Allergens, a.Unit1, a.Unit2 = in.Name, in.Dept, in.Kind, in.Allergens, in.Unit1, in.Unit2
	if a.PicSource != "symbol" {
		a.Symbol = ""
	} else {
		a.Symbol = in.Symbol
	}
	return a, nil
}

const articleColumns = `id, name, dept, kind, allergens, unit1, unit2, pic_source, symbol, picture IS NOT NULL, picture_host, picture_from_w, picture_from_h, picture_from_b, picture_at, COALESCE(created_by, ''), created_at`

func scanArticle(row interface{ Scan(...any) error }) (OwnArticle, error) {
	var a OwnArticle
	var allergens, at, created string
	err := row.Scan(&a.ID, &a.Name, &a.Dept, &a.Kind, &allergens, &a.Unit1, &a.Unit2, &a.PicSource, &a.Symbol, &a.HasPicture,
		&a.PictureHost, &a.PictureFromW, &a.PictureFromH, &a.PictureFromBytes, &at, &a.CreatedBy, &created)
	a.Allergens = splitKeys(allergens)
	a.PictureAt, _ = time.Parse(time.RFC3339, at)
	a.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return a, err
}

// CreateArticle stores an own article made by a person.
func (db *DB) CreateArticle(householdID, personID string, a OwnArticle, pic *ArticlePicture) (OwnArticle, error) {
	a, err := a.check(pic)
	if err != nil {
		return OwnArticle{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	a.ID = newID()
	ts := now()
	var jpeg []byte
	var host, at string
	var fw, fh, fb int
	if pic != nil {
		a.PicSource, a.Symbol = pic.Source, ""
		jpeg, host, at, fw, fh, fb = pic.JPEG, pic.Host, ts, pic.FromW, pic.FromH, pic.FromBytes
	}
	var creator any
	if personID != "" {
		creator = personID
	}
	_, err = db.sql.Exec(`INSERT INTO articles (id, household_id, name, dept, kind, allergens, unit1, unit2, pic_source, symbol, picture, picture_host, picture_from_w, picture_from_h, picture_from_b, picture_at, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, householdID, a.Name, a.Dept, a.Kind, strings.Join(a.Allergens, ","), a.Unit1, a.Unit2, a.PicSource, a.Symbol, jpeg, host, fw, fh, fb, at, creator, ts, ts)
	if err != nil {
		return OwnArticle{}, err
	}
	return db.Article(householdID, a.ID)
}

// Article loads one own article.
func (db *DB) Article(householdID, id string) (OwnArticle, error) {
	a, err := scanArticle(db.sql.QueryRow(`SELECT `+articleColumns+` FROM articles WHERE household_id = ? AND id = ?`, householdID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return OwnArticle{}, ErrNotFound
	}
	return a, err
}

// Articles returns the household's own articles, oldest first.
func (db *DB) Articles(householdID string) ([]OwnArticle, error) {
	rows, err := db.sql.Query(`SELECT `+articleColumns+` FROM articles WHERE household_id = ? ORDER BY created_at, rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OwnArticle
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ArticlePicture reads the stored JPEG of an own article; ErrNotFound when it has none.
func (db *DB) ArticlePicture(householdID, id string) ([]byte, error) {
	var b []byte
	err := db.sql.QueryRow(`SELECT picture FROM articles WHERE household_id = ? AND id = ?`, householdID, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && b == nil) {
		return nil, ErrNotFound
	}
	return b, err
}

// UpdateArticle changes an own article; what happens to its picture is the
// change: keep it, set the given one, or remove it (then PicSource and Symbol
// of the article say what it shows instead).
func (db *DB) UpdateArticle(householdID string, a OwnArticle, change PictureChange, pic *ArticlePicture) (OwnArticle, error) {
	if change != SetPicture {
		pic = nil
	}
	a, err := a.check(pic)
	if err != nil {
		return OwnArticle{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	ts := now()
	var res sql.Result
	switch change {
	case SetPicture:
		res, err = db.sql.Exec(`UPDATE articles SET name = ?, dept = ?, kind = ?, allergens = ?, unit1 = ?, unit2 = ?, pic_source = ?, symbol = '', picture = ?, picture_host = ?, picture_from_w = ?, picture_from_h = ?, picture_from_b = ?, picture_at = ?, updated_at = ?
			WHERE household_id = ? AND id = ?`,
			a.Name, a.Dept, a.Kind, strings.Join(a.Allergens, ","), a.Unit1, a.Unit2, pic.Source, pic.JPEG, pic.Host, pic.FromW, pic.FromH, pic.FromBytes, ts, ts, householdID, a.ID)
	case RemovePicture:
		res, err = db.sql.Exec(`UPDATE articles SET name = ?, dept = ?, kind = ?, allergens = ?, unit1 = ?, unit2 = ?, pic_source = ?, symbol = ?, picture = NULL, picture_host = '', picture_from_w = 0, picture_from_h = 0, picture_from_b = 0, picture_at = '', updated_at = ?
			WHERE household_id = ? AND id = ?`,
			a.Name, a.Dept, a.Kind, strings.Join(a.Allergens, ","), a.Unit1, a.Unit2, a.PicSource, a.Symbol, ts, householdID, a.ID)
	default:
		// Keeping the picture also keeps what the article shows; a symbol cannot be chosen while a picture stays.
		res, err = db.sql.Exec(`UPDATE articles SET name = ?, dept = ?, kind = ?, allergens = ?, unit1 = ?, unit2 = ?, updated_at = ?
			WHERE household_id = ? AND id = ?`,
			a.Name, a.Dept, a.Kind, strings.Join(a.Allergens, ","), a.Unit1, a.Unit2, ts, householdID, a.ID)
	}
	if err := expectOne(res, err); err != nil {
		return OwnArticle{}, err
	}
	return db.Article(householdID, a.ID)
}

// DeleteArticle removes an own article.
func (db *DB) DeleteArticle(householdID, id string) error {
	res, err := db.sql.Exec(`DELETE FROM articles WHERE household_id = ? AND id = ?`, householdID, id)
	return expectOne(res, err)
}

// HiddenArticles returns the keys of the shipped articles a parent hid by hand.
func (db *DB) HiddenArticles(householdID string) (map[string]bool, error) {
	rows, err := db.sql.Query(`SELECT article_key FROM article_hidden WHERE household_id = ?`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// SetArticleHidden hides or shows a shipped article by hand.
func (db *DB) SetArticleHidden(householdID, key string, hidden bool) error {
	if _, ok := library.ShippedByID(key); !ok {
		return ErrNotFound
	}
	if hidden {
		_, err := db.sql.Exec(`INSERT OR IGNORE INTO article_hidden (household_id, article_key) VALUES (?, ?)`, householdID, key)
		return err
	}
	_, err := db.sql.Exec(`DELETE FROM article_hidden WHERE household_id = ? AND article_key = ?`, householdID, key)
	return err
}

// LibrarySettings is the household's experimental setting that hides articles
// with an allergen (ADR 0019). ConfirmedBy and ConfirmedAt say who read the
// notice and when; they stay when the setting is switched off again.
type LibrarySettings struct {
	HideAllergen bool
	Everyone     bool
	People       []string
	ConfirmedBy  string
	ConfirmedAt  time.Time
}

// LibrarySettings loads the setting; a household that never saved one has it off.
func (db *DB) LibrarySettings(householdID string) (LibrarySettings, error) {
	var s LibrarySettings
	var people, by, at string
	err := db.sql.QueryRow(`SELECT hide_allergen, everyone, hide_people, COALESCE(confirmed_by, ''), confirmed_at FROM library_settings WHERE household_id = ?`, householdID).
		Scan(&s.HideAllergen, &s.Everyone, &people, &by, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return LibrarySettings{Everyone: true, People: []string{}}, nil
	}
	s.People, s.ConfirmedBy = splitKeys(people), by
	s.ConfirmedAt, _ = time.Parse(time.RFC3339, at)
	return s, err
}

// SetLibrarySettings saves the setting. Switching it on needs a person who
// confirmed the notice; the time of the confirmation is the time of saving
// unless the same person confirmed before.
func (db *DB) SetLibrarySettings(householdID string, s LibrarySettings) error {
	old, err := db.LibrarySettings(householdID)
	if err != nil {
		return err
	}
	at := old.ConfirmedAt
	switch {
	case s.HideAllergen && s.ConfirmedBy == "":
		return fmt.Errorf("%w: the notice must be confirmed before articles are hidden", ErrInvalid)
	case s.ConfirmedBy != "" && (s.ConfirmedBy != old.ConfirmedBy || at.IsZero() || (s.HideAllergen && !old.HideAllergen)):
		at = time.Now().UTC()
	}
	var by any
	if s.ConfirmedBy != "" {
		by = s.ConfirmedBy
	}
	stamp := ""
	if !at.IsZero() {
		stamp = at.Format(time.RFC3339)
	}
	_, err = db.sql.Exec(`INSERT INTO library_settings (household_id, hide_allergen, everyone, hide_people, confirmed_by, confirmed_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(household_id) DO UPDATE SET hide_allergen = excluded.hide_allergen, everyone = excluded.everyone, hide_people = excluded.hide_people, confirmed_by = excluded.confirmed_by, confirmed_at = excluded.confirmed_at`,
		householdID, s.HideAllergen, s.Everyone, strings.Join(s.People, ","), by, stamp)
	return err
}
