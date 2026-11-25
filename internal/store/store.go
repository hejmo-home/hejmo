// Package store owns the SQLite database: connection, migrations and queries.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/secret"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, BSD-3-Clause
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// DB is the box's database.
type DB struct {
	sql     *sql.DB
	secrets *secret.Sealer // seals stored credentials; set with SetSecrets
	// religiousTitle titles the events of the religious holidays calendar (ticket 339); set with SetReligiousTitler.
	religiousTitle ReligiousTitler
}

type migration struct {
	version int64
	name    string
	body    string
}

// Open opens (or creates) the database at path and applies pending migrations.
func Open(path string) (*DB, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db := &DB{sql: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := db.SyncAllBirthdays(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the database.
func (db *DB) Close() error { return db.sql.Close() }

// SQL exposes the raw handle for tests and narrow internal use.
func (db *DB) SQL() *sql.DB { return db.sql }

// SchemaVersion returns the highest applied migration (its timestamp prefix).
func (db *DB) SchemaVersion() (int64, error) {
	var v int64
	err := db.sql.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// LatestVersion is the highest migration shipped in this binary.
func LatestVersion() int64 {
	ms, err := migrations()
	if err != nil || len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].version
}

// Migration files are named YYYYMMDDhhmm_name.sql: the minute the migration
// was written, so two branches never claim the same number. The timestamp is
// the version.
var migrationName = regexp.MustCompile(`^(\d{12})_[a-z0-9_]+\.sql$`)

// checkMigrationNames fails on a name that does not follow the convention, on
// a duplicate prefix and on names that are not in ascending order.
func checkMigrationNames(names []string) error {
	prev, prevPrefix := "", ""
	for _, n := range names {
		m := migrationName.FindStringSubmatch(n)
		if m == nil {
			return fmt.Errorf("store: migration %q must be named YYYYMMDDhhmm_name.sql (12 digit UTC timestamp, then lower-case ascii name)", n)
		}
		if _, err := time.Parse("200601021504", m[1]); err != nil {
			return fmt.Errorf("store: migration %q: prefix %s is not a valid time (YYYYMMDDhhmm)", n, m[1])
		}
		switch {
		case prev != "" && m[1] == prevPrefix:
			return fmt.Errorf("store: migrations %q and %q have a duplicate prefix %s", prev, n, prevPrefix)
		case prev != "" && m[1] < prevPrefix:
			return fmt.Errorf("store: migration %q must come after %q: prefixes must be strictly ascending", n, prev)
		}
		prev, prevPrefix = n, m[1]
	}
	return nil
}

// legacyMigrationNames maps the counter names used before the timestamp
// convention to the renamed files, so a development database keeps its history.
// Only the files that existed then are listed. The one exception is
// 0011_caldav_writeback.sql, which existed only on a development branch and is
// listed so databases that applied it keep their history; any other unreleased
// numbered migration renames its own file.
var legacyMigrationNames = map[string]string{
	"0001_people_and_access.sql":     "202611141227_people_and_access.sql",
	"0002_calendar.sql":              "202611141408_calendar.sql",
	"0003_sync.sql":                  "202611141702_sync.sql",
	"0004_share_through_box.sql":     "202611141738_share_through_box.sql",
	"0005_public_https.sql":          "202611141800_public_https.sql",
	"0006_displays.sql":              "202611142004_displays.sql",
	"0007_tasks.sql":                 "202611142019_tasks.sql",
	"0008_display_show_tomorrow.sql": "202611142047_display_show_tomorrow.sql",
	"0009_household_settings.sql":    "202611142100_household_settings.sql",
	"0010_caldav.sql":                "202611142200_caldav.sql",
	"0011_caldav_writeback.sql":      "202611142300_caldav_writeback.sql",
}

func migrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries { // fs.ReadDir sorts by file name
		names = append(names, e.Name())
	}
	if err := checkMigrationNames(names); err != nil {
		return nil, err
	}
	var ms []migration
	for _, n := range names {
		v, _ := strconv.ParseInt(n[:12], 10, 64)
		body, err := migrationFiles.ReadFile("migrations/" + n)
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: n, body: string(body)})
	}
	return ms, nil
}

func (db *DB) migrate() error {
	if _, err := db.sql.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		return err
	}
	ms, err := migrations()
	if err != nil {
		return err
	}
	if err := db.renameLegacy(); err != nil {
		return err
	}
	return db.apply(ms)
}

// renameLegacy rewrites the history of a development database that was
// migrated under the counter names (version 1 to 10).
func (db *DB) renameLegacy() error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for old, name := range legacyMigrationNames {
		counter, _ := strconv.ParseInt(old[:4], 10, 64)
		version, _ := strconv.ParseInt(name[:12], 10, 64)
		if _, err := tx.Exec(`UPDATE schema_migrations SET version = ?, name = ? WHERE version = ? AND name = ?`,
			version, name, counter, old); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// foreignKeysOffMarker, as the first line of a migration, runs it with foreign
// keys off. SQLite cannot change a CHECK constraint in place; the table is
// rebuilt (create, copy, drop, rename), and with foreign keys on the drop would
// cascade into the rows of every child table. Before the migration commits,
// every foreign key is checked, so the rebuild cannot leave a dangling reference.
const foreignKeysOffMarker = "-- hejmo:foreign-keys-off\n"

// apply runs every migration that is not recorded yet, oldest first. It looks
// at the set of applied versions, not at the highest one: a branch merged later
// can carry an older timestamp than a migration the database already has.
func (db *DB) apply(ms []migration) error {
	for _, m := range ms {
		var done int
		if err := db.sql.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&done); err != nil {
			return err
		}
		if done > 0 {
			continue
		}
		if err := db.applyOne(m); err != nil {
			return err
		}
	}
	return nil
}

// applyOne runs one migration and records it, in one transaction.
func (db *DB) applyOne(m migration) (err error) {
	ctx := context.Background()
	conn, err := db.sql.Conn(ctx) // foreign keys are a setting of the connection
	if err != nil {
		return err
	}
	defer conn.Close()
	rebuild := strings.HasPrefix(m.body, foreignKeysOffMarker)
	if rebuild {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return err
		}
		defer func() {
			if _, e := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); e != nil && err == nil {
				err = e
			}
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.body); err != nil {
		return fmt.Errorf("store: migration %s: %w", m.name, err)
	}
	if rebuild {
		if err := checkForeignKeys(ctx, tx); err != nil {
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
		return err
	}
	return tx.Commit()
}

// checkForeignKeys fails if any row refers to a row that does not exist.
func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent sql.NullString
		var rowid, idx sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &idx); err != nil {
			return fmt.Errorf("read foreign key check result: %w", err)
		}
		return fmt.Errorf("row %d of %s refers to a missing row of %s", rowid.Int64, table.String, parent.String)
	}
	return rows.Err()
}
