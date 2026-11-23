package store

import (
	"database/sql"
	"errors"
	"time"
)

// States of an uploaded update file.
const (
	UploadReady      = "ready"      // checked, waiting for a parent's yes
	UploadInstalling = "installing" // handed to the host script
)

// UpdateUpload is an update file a parent uploaded in offline mode (ticket 221).
type UpdateUpload struct {
	State       string
	Version     string // what the file holds
	FromVersion string // what ran when it was uploaded; empty if unknown
	Arch        string
	FileName    string
	Size        int64
	SHA256      string
	Released    string // YYYY-MM-DD
	Signer      string
	UploadedAt  time.Time
	StartedAt   time.Time // zero until installing
	StartedBy   string    // the name of the parent who said yes
}

// UpdateUpload returns the upload, if there is one.
func (db *DB) UpdateUpload() (UpdateUpload, bool, error) {
	var u UpdateUpload
	var up string
	var started, by sql.NullString
	err := db.sql.QueryRow(`SELECT state, version, from_version, arch, file_name, size, sha256, released, signer, uploaded_at, started_at, started_by FROM update_upload WHERE id = 1`).
		Scan(&u.State, &u.Version, &u.FromVersion, &u.Arch, &u.FileName, &u.Size, &u.SHA256, &u.Released, &u.Signer, &up, &started, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdateUpload{}, false, nil
	}
	if err != nil {
		return UpdateUpload{}, false, err
	}
	u.UploadedAt = parseStamp(sql.NullString{String: up, Valid: true})
	u.StartedAt = parseStamp(started)
	u.StartedBy = by.String
	return u, true, nil
}

// ErrUploadInstalling means an installation is running, so no other file can be taken.
var ErrUploadInstalling = errors.New("store: an update is being installed")

// SaveUpdateUpload keeps a checked file waiting for confirmation, replacing
// one that waits. It refuses while an installation runs.
func (db *DB) SaveUpdateUpload(u UpdateUpload) error {
	res, err := db.sql.Exec(`INSERT INTO update_upload (id, state, version, from_version, arch, file_name, size, sha256, released, signer, uploaded_at)
		VALUES (1, 'ready', ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET state = 'ready', version = excluded.version, from_version = excluded.from_version, arch = excluded.arch,
			file_name = excluded.file_name, size = excluded.size, sha256 = excluded.sha256, released = excluded.released, signer = excluded.signer,
			uploaded_at = excluded.uploaded_at, started_at = NULL, started_by = NULL
		WHERE update_upload.state = 'ready'`,
		u.Version, u.FromVersion, u.Arch, u.FileName, u.Size, u.SHA256, u.Released, u.Signer, stamp(u.UploadedAt))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUploadInstalling
	}
	return nil
}

// StartUpdateUpload marks the waiting file as being installed by the parent
// called by. It reports false when there is no waiting file (none, or one that
// is already being installed), so two parents cannot both start it.
func (db *DB) StartUpdateUpload(by string, at time.Time) (bool, error) {
	res, err := db.sql.Exec(`UPDATE update_upload SET state = 'installing', started_at = ?, started_by = ? WHERE id = 1 AND state = 'ready'`, stamp(at), by)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ClearUpdateUpload forgets the upload.
func (db *DB) ClearUpdateUpload() error {
	_, err := db.sql.Exec(`DELETE FROM update_upload WHERE id = 1`)
	return err
}
