package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/brightness"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/quiet"
	"github.com/hejmo-home/hejmo/internal/theme"
)

// Theme is a display's theme setting: light, dark or auto (ADR 0015).
type Theme = theme.Theme

// PairingCodeLifetime is how long a pairing code can be confirmed.
const PairingCodeLifetime = 10 * time.Minute

// MaxWaitingDisplays is how many unpaired displays a household keeps at once.
// Opening one more drops the oldest, so a client that keeps opening pairing
// screens cannot hold a large share of the six-digit code space.
const MaxWaitingDisplays = 20

// staleWaiting is how long an unconfirmed display's row is kept.
const staleWaiting = 24 * time.Hour

// DisplayType says how a display shows things (ADR 0015).
type DisplayType string

// Display types.
const (
	DisplayEInkBW    DisplayType = "eink_bw"
	DisplayEInkColor DisplayType = "eink_color"
	DisplayScreen    DisplayType = "screen"
)

// Valid reports whether t is a known display type.
func (t DisplayType) Valid() bool {
	return t == DisplayEInkBW || t == DisplayEInkColor || t == DisplayScreen
}

// Display is a wall display: waiting for a parent to confirm its code, or
// paired.
type Display struct {
	ID       string
	Name     string
	Room     string
	Type     DisplayType
	Paired   bool
	PairedAt time.Time
	LastSeen time.Time // zero = never
	// Code is the pairing code of a waiting display; empty once paired or
	// when it has expired.
	Code        string
	CodeExpires time.Time
	// ShowTomorrow is the setting "show tomorrow block" (ADR 0016), off by default.
	ShowTomorrow bool
	// Quiet is the quiet mode schedule (ADR 0015), off by default.
	Quiet quiet.Schedule
	// KioskURL is the address of the display's kiosk app (ADR 0005); empty
	// without one. KioskKeySet says whether an API key is stored; the key
	// itself is read only by DisplayKioskKey.
	KioskURL    string
	KioskKeySet bool
	// Theme is light, dark or auto; in auto the display turns dark at sunset
	// and light at sunrise, each shifted by its offset in minutes (-180..180).
	Theme                       Theme
	SunriseOffset, SunsetOffset int
	// Saver is the photo screensaver (ticket 178).
	Saver Saver
	// Brightness is what the kiosk app sets by time of day and in quiet mode (ticket 179).
	Brightness brightness.Settings
	// ListView is how the display shows lists (ADR 0019): tiles until someone switches it.
	ListView list.View
	// Portrait is how the display last reported it hangs (ticket 180).
	Portrait bool
}

// Saver is a display's screensaver setting: on by default, starting after
// IdleMinutes without a touch and showing a new photo every IntervalMinutes
// (15 on E-Ink, where each change flashes the screen). Albums are the albums it
// shows; none means all albums, so a new album appears there by itself.
type Saver struct {
	Enabled                      bool
	IdleMinutes, IntervalMinutes int
	Albums                       []string
}

// Shows reports whether the screensaver shows album id.
func (s Saver) Shows(albumID string) bool {
	if !s.Enabled {
		return false
	}
	if len(s.Albums) == 0 {
		return true
	}
	for _, a := range s.Albums {
		if a == albumID {
			return true
		}
	}
	return false
}

const displayColumns = `id, name, room, type, pairing_code, code_expires_at, paired_at, last_seen_at, show_tomorrow, quiet_enabled, quiet_from, quiet_to, kiosk_url, kiosk_key != '', theme, sunrise_offset, sunset_offset, saver_enabled, saver_idle_minutes, saver_interval_minutes, bright_day, bright_evening, bright_quiet_mode, bright_quiet, wake_minutes, light_source, COALESCE((SELECT view FROM display_list_view WHERE display_id = displays.id), 'tiles'), portrait`

type rowScanner interface{ Scan(...any) error }

func scanDisplay(r rowScanner, at time.Time) (Display, error) {
	var d Display
	var typ string
	var code, expires, paired, seen sql.NullString
	var qOn bool
	var qFrom, qTo string
	if err := r.Scan(&d.ID, &d.Name, &d.Room, &typ, &code, &expires, &paired, &seen, &d.ShowTomorrow, &qOn, &qFrom, &qTo, &d.KioskURL, &d.KioskKeySet, &d.Theme, &d.SunriseOffset, &d.SunsetOffset, &d.Saver.Enabled, &d.Saver.IdleMinutes, &d.Saver.IntervalMinutes,
		&d.Brightness.Day, &d.Brightness.Evening, &d.Brightness.QuietMode, &d.Brightness.QuietLevel, &d.Brightness.WakeMinutes, &d.Brightness.Source, &d.ListView, &d.Portrait); err != nil {
		return Display{}, err
	}
	if q, err := quiet.Parse(qOn, qFrom, qTo); err == nil {
		d.Quiet = q
	}
	d.Type = DisplayType(typ)
	d.PairedAt, d.LastSeen = parseStamp(paired), parseStamp(seen)
	d.Paired = paired.Valid
	if !d.Paired && code.Valid {
		if exp := parseStamp(expires); exp.After(at) {
			d.Code, d.CodeExpires = code.String, exp
		}
	}
	return d, nil
}

// sixDigits returns a random code such as "042917".
func sixDigits() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// assignCode gives the waiting display with tokenHash a fresh, unused code.
func (db *DB) assignCode(tokenHash string, at time.Time) error {
	// Free codes that ran out so they can be handed out again.
	if _, err := db.sql.Exec(`UPDATE displays SET pairing_code = NULL WHERE pairing_code IS NOT NULL AND code_expires_at <= ?`, stamp(at)); err != nil {
		return err
	}
	for range 20 {
		code, err := sixDigits()
		if err != nil {
			return err
		}
		res, err := db.sql.Exec(`UPDATE displays SET pairing_code = ?, code_expires_at = ?
			WHERE token_hash = ? AND paired_at IS NULL AND revoked_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM displays WHERE pairing_code = ?)`,
			code, stamp(at.Add(PairingCodeLifetime)), tokenHash, code)
		if isUniqueViolation(err) {
			continue // another display took this code a moment ago
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			return nil
		}
		var waiting int
		if err := db.sql.QueryRow(`SELECT COUNT(*) FROM displays WHERE token_hash = ? AND paired_at IS NULL AND revoked_at IS NULL`, tokenHash).Scan(&waiting); err != nil {
			return err
		}
		if waiting == 0 {
			return ErrNotFound
		}
	}
	return errors.New("store: no free pairing code")
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// OpenPairing registers a new display, identified by the hash of its own
// token, and gives it a pairing code to be confirmed by a parent.
func (db *DB) OpenPairing(householdID, tokenHash string, at time.Time) (Display, error) {
	if _, err := db.sql.Exec(`DELETE FROM displays WHERE paired_at IS NULL AND created_at < ?`, stamp(at.Add(-staleWaiting))); err != nil {
		return Display{}, err
	}
	if _, err := db.sql.Exec(`DELETE FROM displays WHERE household_id = ? AND paired_at IS NULL AND id IN (
		SELECT id FROM displays WHERE household_id = ? AND paired_at IS NULL ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?)`,
		householdID, householdID, MaxWaitingDisplays-1); err != nil {
		return Display{}, err
	}
	if _, err := db.sql.Exec(`INSERT INTO displays (id, household_id, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		newID(), householdID, tokenHash, stamp(at)); err != nil {
		return Display{}, err
	}
	if err := db.assignCode(tokenHash, at); err != nil {
		return Display{}, err
	}
	return db.DisplayByToken(tokenHash, at)
}

// RenewPairingCode gives a waiting display a new code, for when the old one
// ran out.
func (db *DB) RenewPairingCode(tokenHash string, at time.Time) (Display, error) {
	if err := db.assignCode(tokenHash, at); err != nil {
		return Display{}, err
	}
	return db.DisplayByToken(tokenHash, at)
}

// DisplayByToken returns the display behind a token hash, waiting or paired.
// A revoked or unknown token is ErrNotFound.
func (db *DB) DisplayByToken(tokenHash string, at time.Time) (Display, error) {
	d, err := scanDisplay(db.sql.QueryRow(`SELECT `+displayColumns+` FROM displays WHERE token_hash = ? AND revoked_at IS NULL`, tokenHash), at)
	if errors.Is(err, sql.ErrNoRows) {
		return Display{}, ErrNotFound
	}
	if err != nil {
		return Display{}, err
	}
	return db.withSaverAlbums(d)
}

// ConfirmPairing pairs the waiting display that shows code, giving it the
// name, room and type in in. A wrong, used or expired code is ErrNotFound.
func (db *DB) ConfirmPairing(householdID, code string, in Display, at time.Time) (Display, error) {
	in.Name, in.Room = strings.TrimSpace(in.Name), strings.TrimSpace(in.Room)
	switch {
	case in.Name == "" || len(in.Name) > 80:
		return Display{}, &FieldError{"name", "a display needs a name of at most 80 characters"}
	case len(in.Room) > 80:
		return Display{}, &FieldError{"room", "a room may have at most 80 characters"}
	case !in.Type.Valid():
		return Display{}, &FieldError{"type", "a display needs a known type"}
	}
	var id string
	err := db.sql.QueryRow(`UPDATE displays SET name = ?, room = ?, type = ?, theme = ?, saver_interval_minutes = ?, paired_at = ?, last_seen_at = ?, pairing_code = NULL, code_expires_at = NULL
		WHERE household_id = ? AND pairing_code = ? AND code_expires_at > ? AND paired_at IS NULL AND revoked_at IS NULL
		RETURNING id`,
		in.Name, in.Room, string(in.Type), string(theme.DefaultFor(string(in.Type))), defaultSaverInterval(in.Type), stamp(at), stamp(at), householdID, code, stamp(at)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Display{}, ErrNotFound
	}
	if err != nil {
		return Display{}, err
	}
	d, err := scanDisplay(db.sql.QueryRow(`SELECT `+displayColumns+` FROM displays WHERE id = ?`, id), at)
	if err != nil {
		return Display{}, err
	}
	return db.withSaverAlbums(d)
}

// Displays lists a household's paired displays, oldest first.
func (db *DB) Displays(householdID string) ([]Display, error) {
	rows, err := db.sql.Query(`SELECT `+displayColumns+` FROM displays
		WHERE household_id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL ORDER BY paired_at, rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Display
	for rows.Next() {
		d, err := scanDisplay(rows, time.Time{})
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, db.attachSaverAlbums(out)
}

// Display returns one of the household's paired displays, or ErrNotFound.
func (db *DB) Display(householdID, id string) (Display, error) {
	d, err := scanDisplay(db.sql.QueryRow(`SELECT `+displayColumns+` FROM displays
		WHERE household_id = ? AND id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`, householdID, id), time.Time{})
	if errors.Is(err, sql.ErrNoRows) {
		return Display{}, ErrNotFound
	}
	return d, err
}

// RevokeDisplay unpairs a display: its token stops working.
func (db *DB) RevokeDisplay(householdID, id string, at time.Time) error {
	return expectOne(db.sql.Exec(`UPDATE displays SET revoked_at = ? WHERE household_id = ? AND id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`,
		stamp(at), householdID, id))
}

// TouchDisplay records that the display with tokenHash was seen at t.
func (db *DB) TouchDisplay(tokenHash string, t time.Time) error {
	return expectOne(db.sql.Exec(`UPDATE displays SET last_seen_at = ? WHERE token_hash = ? AND revoked_at IS NULL`, stamp(t), tokenHash))
}

// SetDisplayIdentity renames one of the household's paired displays and sets
// its type. The name is trimmed; a refused field is a *FieldError.
func (db *DB) SetDisplayIdentity(householdID, id, name string, typ DisplayType) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "" || utf8.RuneCountInString(name) > 80:
		return &FieldError{"name", "a display needs a name of at most 80 characters"}
	case !typ.Valid():
		return &FieldError{"type", "a display needs a known type"}
	}
	return expectOne(db.sql.Exec(`UPDATE displays SET name = ?, type = ? WHERE household_id = ? AND id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`,
		name, string(typ), householdID, id))
}

// DisplayLook is what a display shows and how: the tomorrow block (ADR 0016)
// and the theme with its sunrise and sunset offsets in minutes, -180..180
// (ADR 0015). They are saved together.
type DisplayLook struct {
	ShowTomorrow                bool
	Theme                       Theme
	SunriseOffset, SunsetOffset int
}

// SetDisplayLook saves the look of one of the household's paired displays in
// one statement, so a save is never partial.
func (db *DB) SetDisplayLook(householdID, id string, l DisplayLook) error {
	if !l.Theme.Valid() || !theme.ValidOffset(l.SunriseOffset) || !theme.ValidOffset(l.SunsetOffset) {
		return fmt.Errorf("%w: theme light, dark or auto and offsets within 180 minutes", ErrInvalid)
	}
	return expectOne(db.sql.Exec(`UPDATE displays SET show_tomorrow = ?, theme = ?, sunrise_offset = ?, sunset_offset = ? WHERE household_id = ? AND id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`,
		l.ShowTomorrow, string(l.Theme), l.SunriseOffset, l.SunsetOffset, householdID, id))
}

// SetDisplayBrightness saves the brightness values of one of the household's
// paired displays; a refused value is a *FieldError named like its form field.
func (db *DB) SetDisplayBrightness(householdID, id string, b brightness.Settings) error {
	var refused *brightness.Refused
	if err := b.Validate(); errors.As(err, &refused) {
		return &FieldError{refused.Field, refused.Msg}
	}
	return expectOne(db.sql.Exec(`UPDATE displays SET bright_day = ?, bright_evening = ?, bright_quiet_mode = ?, bright_quiet = ?, wake_minutes = ?, light_source = ? WHERE household_id = ? AND id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`,
		b.Day, b.Evening, string(b.QuietMode), b.QuietLevel, b.WakeMinutes, string(b.Source), householdID, id))
}

// SetDisplayQuiet saves the quiet mode schedule of one of the household's
// paired displays.
func (db *DB) SetDisplayQuiet(householdID, id string, s quiet.Schedule) error {
	return expectOne(db.sql.Exec(`UPDATE displays SET quiet_enabled = ?, quiet_from = ?, quiet_to = ? WHERE household_id = ? AND id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`,
		s.Enabled, s.FromText(), s.ToText(), householdID, id))
}

// SetDisplayKiosk saves the address of a display's kiosk app. A nil key keeps
// the stored key, an empty key removes it, anything else replaces it (sealed).
// An empty address removes the kiosk and its key.
func (db *DB) SetDisplayKiosk(householdID, id, url string, key *string) error {
	sealed := ""
	if url != "" && key != nil && *key != "" {
		if db.secrets == nil {
			return errNoKey
		}
		raw, err := db.secrets.Seal([]byte(*key))
		if err != nil {
			return err
		}
		sealed = base64.StdEncoding.EncodeToString(raw)
	}
	where := ` WHERE household_id = ? AND id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`
	switch {
	case url == "":
		return expectOne(db.sql.Exec(`UPDATE displays SET kiosk_url = '', kiosk_key = ''`+where, householdID, id))
	case key == nil:
		return expectOne(db.sql.Exec(`UPDATE displays SET kiosk_url = ?`+where, url, householdID, id))
	default:
		return expectOne(db.sql.Exec(`UPDATE displays SET kiosk_url = ?, kiosk_key = ?`+where, url, sealed, householdID, id))
	}
}

// DisplayKioskKey returns the unsealed API key of a display's kiosk app, empty
// when none is set.
func (db *DB) DisplayKioskKey(householdID, id string) (string, error) {
	var sealed string
	err := db.sql.QueryRow(`SELECT kiosk_key FROM displays WHERE household_id = ? AND id = ?`, householdID, id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil || sealed == "" {
		return "", err
	}
	if db.secrets == nil {
		return "", errNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", errors.New("store: stored kiosk key is not sealed text")
	}
	plain, err := db.secrets.Open(raw)
	if err != nil {
		return "", errors.New("store: cannot open stored kiosk key (wrong key or altered text)")
	}
	return string(plain), nil
}

// defaultSaverInterval is how often a new display of this type changes the
// photo: each change flashes an E-Ink screen, so it is slower there.
func defaultSaverInterval(t DisplayType) int {
	if t == DisplayEInkBW || t == DisplayEInkColor {
		return 15
	}
	return 1
}

// attachSaverAlbums reads the albums each display chose for its screensaver.
func (db *DB) attachSaverAlbums(ds []Display) error {
	if len(ds) == 0 {
		return nil
	}
	rows, err := db.sql.Query(`SELECT display_id, album_id FROM display_albums ORDER BY rowid`)
	if err != nil {
		return err
	}
	defer rows.Close()
	chosen := map[string][]string{}
	for rows.Next() {
		var d, a string
		if err := rows.Scan(&d, &a); err != nil {
			return err
		}
		chosen[d] = append(chosen[d], a)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range ds {
		ds[i].Saver.Albums = chosen[ds[i].ID]
	}
	return nil
}

func (db *DB) withSaverAlbums(d Display) (Display, error) {
	one := []Display{d}
	err := db.attachSaverAlbums(one)
	return one[0], err
}

// SetDisplaySaver saves the screensaver settings of one of the household's
// paired displays. Idle time and interval are 1 to 240 minutes; the albums
// must be the household's, none meaning all albums.
func (db *DB) SetDisplaySaver(householdID, id string, s Saver) error {
	if s.IdleMinutes < 1 || s.IdleMinutes > 240 || s.IntervalMinutes < 1 || s.IntervalMinutes > 240 {
		return fmt.Errorf("%w: the screensaver times are 1 to 240 minutes", ErrInvalid)
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := expectOne(tx.Exec(`UPDATE displays SET saver_enabled = ?, saver_idle_minutes = ?, saver_interval_minutes = ? WHERE household_id = ? AND id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`,
		s.Enabled, s.IdleMinutes, s.IntervalMinutes, householdID, id)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM display_albums WHERE display_id = ?`, id); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, a := range s.Albums {
		if seen[a] {
			continue
		}
		seen[a] = true
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM albums WHERE household_id = ? AND id = ?`, householdID, a).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: unknown album", ErrInvalid)
		}
		if _, err := tx.Exec(`INSERT INTO display_albums (display_id, album_id) VALUES (?, ?)`, id, a); err != nil {
			return err
		}
	}
	return tx.Commit()
}
