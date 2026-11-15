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

	"github.com/hejmo-home/hejmo/internal/quiet"
)

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
}

const displayColumns = `id, name, room, type, pairing_code, code_expires_at, paired_at, last_seen_at, show_tomorrow, quiet_enabled, quiet_from, quiet_to, kiosk_url, kiosk_key != ''`

type rowScanner interface{ Scan(...any) error }

func scanDisplay(r rowScanner, at time.Time) (Display, error) {
	var d Display
	var typ string
	var code, expires, paired, seen sql.NullString
	var qOn bool
	var qFrom, qTo string
	if err := r.Scan(&d.ID, &d.Name, &d.Room, &typ, &code, &expires, &paired, &seen, &d.ShowTomorrow, &qOn, &qFrom, &qTo, &d.KioskURL, &d.KioskKeySet); err != nil {
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
	return d, err
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
	err := db.sql.QueryRow(`UPDATE displays SET name = ?, room = ?, type = ?, paired_at = ?, last_seen_at = ?, pairing_code = NULL, code_expires_at = NULL
		WHERE household_id = ? AND pairing_code = ? AND code_expires_at > ? AND paired_at IS NULL AND revoked_at IS NULL
		RETURNING id`,
		in.Name, in.Room, string(in.Type), stamp(at), stamp(at), householdID, code, stamp(at)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Display{}, ErrNotFound
	}
	if err != nil {
		return Display{}, err
	}
	return scanDisplay(db.sql.QueryRow(`SELECT `+displayColumns+` FROM displays WHERE id = ?`, id), at)
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
	return out, rows.Err()
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

// SetDisplayShowTomorrow switches the "show tomorrow block" setting of one of
// the household's paired displays.
func (db *DB) SetDisplayShowTomorrow(householdID, id string, on bool) error {
	return expectOne(db.sql.Exec(`UPDATE displays SET show_tomorrow = ? WHERE household_id = ? AND id = ? AND paired_at IS NOT NULL AND revoked_at IS NULL`,
		on, householdID, id))
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
