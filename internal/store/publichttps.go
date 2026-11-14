package store

import (
	"database/sql"
	"errors"
	"time"
)

// PublicHTTPS is the box's setup for a publicly trusted certificate. Token and
// EABHMAC are sealed by the caller (internal/secret); the store never sees
// them in plain text.
type PublicHTTPS struct {
	Name, Zone, Provider, Issuer, LANIP, Email string
	Token                                      []byte
	EABKeyID                                   string
	EABHMAC                                    []byte
	// RenewalError is why the last certificate renewal failed; empty if it did not.
	RenewalError    string
	RenewalFailedAt time.Time
}

// SavePublicHTTPS stores the configuration, replacing an earlier one and
// clearing its renewal state.
func (db *DB) SavePublicHTTPS(p PublicHTTPS) error {
	_, err := db.sql.Exec(`INSERT INTO public_https (id, name, zone, provider, issuer, lan_ip, email, token, eab_key_id, eab_hmac)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, zone = excluded.zone, provider = excluded.provider,
			issuer = excluded.issuer, lan_ip = excluded.lan_ip, email = excluded.email, token = excluded.token,
			eab_key_id = excluded.eab_key_id, eab_hmac = excluded.eab_hmac, renewal_error = '', renewal_failed_at = NULL`,
		p.Name, p.Zone, p.Provider, p.Issuer, p.LANIP, p.Email, p.Token, p.EABKeyID, p.EABHMAC)
	return err
}

// PublicHTTPS returns the configuration; ok is false if the box uses none.
func (db *DB) PublicHTTPS() (p PublicHTTPS, ok bool, err error) {
	var failed sql.NullString
	err = db.sql.QueryRow(`SELECT name, zone, provider, issuer, lan_ip, email, token, eab_key_id, eab_hmac, renewal_error, renewal_failed_at
		FROM public_https WHERE id = 1`).
		Scan(&p.Name, &p.Zone, &p.Provider, &p.Issuer, &p.LANIP, &p.Email, &p.Token, &p.EABKeyID, &p.EABHMAC, &p.RenewalError, &failed)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicHTTPS{}, false, nil
	}
	if err != nil {
		return PublicHTTPS{}, false, err
	}
	if failed.Valid {
		p.RenewalFailedAt, _ = time.Parse(time.RFC3339, failed.String)
	}
	return p, true, nil
}

// SetRenewalResult records the outcome of a renewal: an empty failure text
// clears an earlier failure.
func (db *DB) SetRenewalResult(at time.Time, failure string) error {
	if failure == "" {
		_, err := db.sql.Exec(`UPDATE public_https SET renewal_error = '', renewal_failed_at = NULL WHERE id = 1`)
		return err
	}
	_, err := db.sql.Exec(`UPDATE public_https SET renewal_error = ?, renewal_failed_at = ? WHERE id = 1`, failure, stamp(at))
	return err
}

// DeletePublicHTTPS forgets the configuration, with the sealed token.
func (db *DB) DeletePublicHTTPS() error {
	_, err := db.sql.Exec(`DELETE FROM public_https WHERE id = 1`)
	return err
}
