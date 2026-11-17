package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
)

// OutboundBox is the box's state about connections to the outside (ticket 201).
type OutboundBox struct {
	// Offline is offline mode (Q141): nothing goes outside the home network.
	Offline bool
	// OfflineSince is when it was switched on; zero while it is off.
	OfflineSince time.Time
	// Asked is whether the setup wizard's question was answered.
	Asked bool
	// ReminderDays is how many days without an update make parents see a reminder (Q145).
	ReminderDays int
	// UpdatedAt is when the box was installed or last updated, as far as it knows.
	UpdatedAt time.Time
}

// Bounds of OutboundBox.ReminderDays.
const (
	MinReminderDays = 7
	MaxReminderDays = 365
)

// MaxRefusedKept is how many refused attempts the box keeps.
const MaxRefusedKept = 200

// OutboundBox returns the box's state.
func (db *DB) OutboundBox() (OutboundBox, error) {
	var b OutboundBox
	var since sql.NullString
	var updated string
	err := db.sql.QueryRow(`SELECT offline, offline_since, asked, update_reminder_days, updated_at FROM outbound_box WHERE id = 1`).
		Scan(&b.Offline, &since, &b.Asked, &b.ReminderDays, &updated)
	if err != nil {
		return OutboundBox{}, err
	}
	b.OfflineSince = parseStamp(since)
	b.UpdatedAt = parseStamp(sql.NullString{String: updated, Valid: true})
	return b, nil
}

// SetOffline switches offline mode on or off. Switching it on again keeps the
// time it was first switched on. The single switches are left alone: they are
// as they were when it is switched off.
func (db *DB) SetOffline(on bool, at time.Time) error {
	if on {
		_, err := db.sql.Exec(`UPDATE outbound_box SET offline = 1, offline_since = COALESCE(offline_since, ?) WHERE id = 1`, stamp(at))
		return err
	}
	_, err := db.sql.Exec(`UPDATE outbound_box SET offline = 0, offline_since = NULL WHERE id = 1`)
	return err
}

// AnswerOfflineQuestion records the setup wizard's answer to "Should the box
// stay fully offline?" (Q142).
func (db *DB) AnswerOfflineQuestion(offline bool, at time.Time) error {
	if err := db.SetOffline(offline, at); err != nil {
		return err
	}
	_, err := db.sql.Exec(`UPDATE outbound_box SET asked = 1 WHERE id = 1`)
	return err
}

// ResetOfflineQuestion takes the wizard's answer back: online, not asked.
func (db *DB) ResetOfflineQuestion() error {
	_, err := db.sql.Exec(`UPDATE outbound_box SET offline = 0, offline_since = NULL, asked = 0 WHERE id = 1`)
	return err
}

// SetUpdateReminderDays sets after how many days without an update parents are reminded.
func (db *DB) SetUpdateReminderDays(days int) error {
	if days < MinReminderDays || days > MaxReminderDays {
		return fmt.Errorf("%w: the reminder is between %d and %d days", ErrInvalid, MinReminderDays, MaxReminderDays)
	}
	_, err := db.sql.Exec(`UPDATE outbound_box SET update_reminder_days = ? WHERE id = 1`, days)
	return err
}

// NoteUpdated remembers that the box was installed or updated at t; an earlier
// time than the one known is ignored.
func (db *DB) NoteUpdated(t time.Time) error {
	_, err := db.sql.Exec(`UPDATE outbound_box SET updated_at = ? WHERE id = 1 AND updated_at < ?`, stamp(t), stamp(t))
	return err
}

// OutboundService is the state of one connection with a switch.
type OutboundService struct {
	Enabled     bool
	LastContact time.Time // zero: never
	LastTarget  string
}

// OutboundServices returns every connection of outbound.Services; a
// connection never switched is on.
func (db *DB) OutboundServices() (map[outbound.Service]OutboundService, error) {
	out := map[outbound.Service]OutboundService{}
	for _, s := range outbound.Services {
		out[s] = OutboundService{Enabled: true}
	}
	rows, err := db.sql.Query(`SELECT service, enabled FROM outbound_service`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s string
		var on bool
		if err := rows.Scan(&s, &on); err != nil {
			rows.Close()
			return nil, err
		}
		if v, ok := out[outbound.Service(s)]; ok {
			v.Enabled = on
			out[outbound.Service(s)] = v
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = db.sql.Query(`SELECT service, last_contact, last_target FROM outbound_contact`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s, at, target string
		if err := rows.Scan(&s, &at, &target); err != nil {
			return nil, err
		}
		if v, ok := out[outbound.Service(s)]; ok {
			v.LastContact = parseStamp(sql.NullString{String: at, Valid: true})
			v.LastTarget = target
			out[outbound.Service(s)] = v
		}
	}
	return out, rows.Err()
}

// SetServiceEnabled switches one connection. The DNS service has no switch of
// its own: it goes off only by leaving the public name.
func (db *DB) SetServiceEnabled(s outbound.Service, on bool) error {
	if !s.Valid() || s == outbound.DNS {
		return fmt.Errorf("%w: %q is not a connection with a switch", ErrInvalid, s)
	}
	_, err := db.sql.Exec(`INSERT INTO outbound_service (service, enabled) VALUES (?, ?)
		ON CONFLICT (service) DO UPDATE SET enabled = excluded.enabled`, string(s), on)
	return err
}

// RefusedAttempt is one connection the gate refused.
type RefusedAttempt struct {
	At      time.Time
	Service outbound.Service
	Target  string
}

// RefusedAttempts returns up to limit refused attempts, newest first.
func (db *DB) RefusedAttempts(limit int) ([]RefusedAttempt, error) {
	rows, err := db.sql.Query(`SELECT at, service, target FROM outbound_refused ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefusedAttempt
	for rows.Next() {
		var at, s, target string
		if err := rows.Scan(&at, &s, &target); err != nil {
			return nil, err
		}
		out = append(out, RefusedAttempt{At: parseStamp(sql.NullString{String: at, Valid: true}), Service: outbound.Service(s), Target: target})
	}
	return out, rows.Err()
}

// ClearRefusedAttempts forgets the refused attempts.
func (db *DB) ClearRefusedAttempts() error {
	_, err := db.sql.Exec(`DELETE FROM outbound_refused`)
	return err
}

// Outbound returns the policy and the ledger of the gate (package outbound)
// backed by this database.
func (db *DB) Outbound() *OutboundGate { return &OutboundGate{db: db} }

// OutboundGate implements outbound.Policy and outbound.Ledger.
type OutboundGate struct{ db *DB }

// Verdict implements outbound.Policy. A database that cannot be read refuses.
func (g *OutboundGate) Verdict(s outbound.Service) outbound.Verdict {
	var offline bool
	if err := g.db.sql.QueryRow(`SELECT offline FROM outbound_box WHERE id = 1`).Scan(&offline); err != nil || offline {
		return outbound.VerdictOffline
	}
	if s == outbound.Other || s == outbound.DNS {
		return outbound.VerdictAllowed
	}
	var on bool
	switch err := g.db.sql.QueryRow(`SELECT enabled FROM outbound_service WHERE service = ?`, string(s)).Scan(&on); err {
	case nil:
		if !on {
			return outbound.VerdictSwitchedOff
		}
	case sql.ErrNoRows:
	default:
		return outbound.VerdictOffline
	}
	return outbound.VerdictAllowed
}

// Refused implements outbound.Ledger.
func (g *OutboundGate) Refused(at time.Time, s outbound.Service, target string) {
	_, _ = g.db.sql.Exec(`INSERT INTO outbound_refused (at, service, target) VALUES (?, ?, ?)`, stamp(at), string(s), target)
	_, _ = g.db.sql.Exec(`DELETE FROM outbound_refused WHERE id <= (SELECT MAX(id) FROM outbound_refused) - ?`, MaxRefusedKept)
}

// Contacted implements outbound.Ledger.
func (g *OutboundGate) Contacted(at time.Time, s outbound.Service, target string) {
	_, _ = g.db.sql.Exec(`INSERT INTO outbound_contact (service, last_contact, last_target) VALUES (?, ?, ?)
		ON CONFLICT (service) DO UPDATE SET last_contact = excluded.last_contact, last_target = excluded.last_target
		WHERE excluded.last_contact >= outbound_contact.last_contact`, string(s), stamp(at), target)
}
