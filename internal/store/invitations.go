package store

import (
	"database/sql"
	"errors"
	"time"
)

// InvitationLifetime is how long an invitation link works.
const InvitationLifetime = 7 * 24 * time.Hour

// ErrInvitationNotOpen means the invitation was used, withdrawn or has
// expired; the caller reads which from the invitation.
var ErrInvitationNotOpen = errors.New("store: invitation is not open")

// InvitationState is where an invitation stands.
type InvitationState string

// The states of an invitation (GLOSSARY: Invitation).
const (
	InvitationOpen      InvitationState = "open"
	InvitationUsed      InvitationState = "used"
	InvitationExpired   InvitationState = "expired"
	InvitationWithdrawn InvitationState = "withdrawn"
)

// Invitation is a one-time link for an adult without an account. The link
// itself is not stored, only its hash.
type Invitation struct {
	ID          string
	HouseholdID string
	PersonID    string
	// CreatedBy is the parent who made the link; empty if that person was removed.
	CreatedBy               string
	CreatedAt, ExpiresAt    time.Time
	AcceptedAt, WithdrawnAt time.Time // zero while not
}

// State is the state at the given time: used beats withdrawn beats expired.
func (i Invitation) State(at time.Time) InvitationState {
	switch {
	case !i.AcceptedAt.IsZero():
		return InvitationUsed
	case !i.WithdrawnAt.IsZero():
		return InvitationWithdrawn
	case !at.Before(i.ExpiresAt):
		return InvitationExpired
	}
	return InvitationOpen
}

const invitationColumns = `id, household_id, person_id, COALESCE(created_by, ''), created_at, expires_at, accepted_at, withdrawn_at`

func scanInvitation(row rowScanner) (Invitation, error) {
	var i Invitation
	var created, expires, accepted, withdrawn sql.NullString
	err := row.Scan(&i.ID, &i.HouseholdID, &i.PersonID, &i.CreatedBy, &created, &expires, &accepted, &withdrawn)
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	i.CreatedAt, i.ExpiresAt, i.AcceptedAt, i.WithdrawnAt = parseStamp(created), parseStamp(expires), parseStamp(accepted), parseStamp(withdrawn)
	return i, err
}

// CreateInvitation stores the hash of a new link for an adult without an
// account. A link that is still open for the person is withdrawn: a new link
// replaces the old one. A child is ErrInvalid, a person with an account is
// ErrConflict, a person outside the household is ErrNotFound.
func (db *DB) CreateInvitation(householdID, personID, createdBy, tokenHash string, at time.Time) (Invitation, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return Invitation{}, err
	}
	defer tx.Rollback()
	var role string
	err = tx.QueryRow(`SELECT role FROM household_members WHERE household_id = ? AND person_id = ?`, householdID, personID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	if err != nil {
		return Invitation{}, err
	}
	if Role(role) != RoleParent {
		return Invitation{}, &FieldError{"person", "only adults can be invited"}
	}
	var accounts int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM accounts WHERE person_id = ?`, personID).Scan(&accounts); err != nil {
		return Invitation{}, err
	}
	if accounts > 0 {
		return Invitation{}, ErrConflict
	}
	if _, err := tx.Exec(`UPDATE invitations SET withdrawn_at = ? WHERE person_id = ? AND accepted_at IS NULL AND withdrawn_at IS NULL`, stamp(at), personID); err != nil {
		return Invitation{}, err
	}
	inv := Invitation{ID: newID(), HouseholdID: householdID, PersonID: personID, CreatedBy: createdBy, CreatedAt: at, ExpiresAt: at.Add(InvitationLifetime)}
	if _, err := tx.Exec(`INSERT INTO invitations (id, household_id, person_id, token_hash, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		inv.ID, householdID, personID, tokenHash, nullIfEmpty(createdBy), stamp(at), stamp(inv.ExpiresAt)); err != nil {
		return Invitation{}, err
	}
	return inv, tx.Commit()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// InvitationByHash finds an invitation by the hash of its link.
func (db *DB) InvitationByHash(tokenHash string) (Invitation, error) {
	return scanInvitation(db.sql.QueryRow(`SELECT `+invitationColumns+` FROM invitations WHERE token_hash = ?`, tokenHash))
}

// LatestInvitation is the newest invitation made for a person, whatever its state.
func (db *DB) LatestInvitation(householdID, personID string) (Invitation, error) {
	return scanInvitation(db.sql.QueryRow(`SELECT `+invitationColumns+` FROM invitations
		WHERE household_id = ? AND person_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, householdID, personID))
}

// OpenInvitations lists the invitations that can still be accepted at the given time.
func (db *DB) OpenInvitations(householdID string, at time.Time) ([]Invitation, error) {
	rows, err := db.sql.Query(`SELECT `+invitationColumns+` FROM invitations
		WHERE household_id = ? AND accepted_at IS NULL AND withdrawn_at IS NULL AND expires_at > ?
		ORDER BY created_at, rowid`, householdID, stamp(at))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invitation
	for rows.Next() {
		i, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// WithdrawInvitation makes the person's open link useless. When nothing is
// open (none, used, expired or withdrawn already) it is ErrNotFound.
func (db *DB) WithdrawInvitation(householdID, personID string, at time.Time) error {
	return expectOne(db.sql.Exec(`UPDATE invitations SET withdrawn_at = ?
		WHERE household_id = ? AND person_id = ? AND accepted_at IS NULL AND withdrawn_at IS NULL AND expires_at > ?`,
		stamp(at), householdID, personID, stamp(at)))
}

// AcceptInvitation creates the invited person's account (PersonID of acc is
// ignored) and uses the link up, in one step: a link never creates a second
// account. An unknown hash is ErrNotFound; a used, withdrawn or expired link
// is ErrInvitationNotOpen together with the invitation; a username that is
// taken is a FieldError on "username" and keeps the link open.
func (db *DB) AcceptInvitation(tokenHash string, at time.Time, acc Account) (Invitation, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return Invitation{}, err
	}
	defer tx.Rollback()
	inv, err := scanInvitation(tx.QueryRow(`SELECT `+invitationColumns+` FROM invitations WHERE token_hash = ?`, tokenHash))
	if err != nil {
		return Invitation{}, err
	}
	if inv.State(at) != InvitationOpen {
		return inv, ErrInvitationNotOpen
	}
	var taken int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM accounts WHERE username = ?`, acc.Username).Scan(&taken); err != nil {
		return Invitation{}, err
	}
	if taken > 0 {
		return Invitation{}, &FieldError{"username", "this username is taken"}
	}
	if _, err := tx.Exec(`INSERT INTO accounts (person_id, username, password_hash, totp_secret, created_at) VALUES (?, ?, ?, ?, ?)`,
		inv.PersonID, acc.Username, acc.PasswordHash, acc.TOTPSecret, stamp(at)); err != nil {
		return Invitation{}, err
	}
	if err := expectOne(tx.Exec(`UPDATE invitations SET accepted_at = ? WHERE id = ? AND accepted_at IS NULL AND withdrawn_at IS NULL`, stamp(at), inv.ID)); err != nil {
		return inv, ErrInvitationNotOpen
	}
	inv.AcceptedAt = at
	return inv, tx.Commit()
}
