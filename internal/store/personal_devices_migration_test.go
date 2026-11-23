package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #203: the migration that renames phones to personal devices keeps the data. A database as
// 0.1 development builds left it, with a paired phone and its device session, is migrated; the
// device is listed under the new name and its session is still valid and still a device session.
func TestRenamingPhonesToPersonalDevicesKeepsAPairedDeviceAndItsSession(t *testing.T) {
	t.Parallel()
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "hejmo.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{sql: conn}
	defer db.Close()
	if _, err := conn.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	all, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	var before []migration
	var rename migration
	for _, m := range all {
		if m.name == "202611231100_personal_devices.sql" {
			rename = m
			break
		}
		before = append(before, m)
	}
	if rename.name == "" {
		t.Fatal("the migration is not shipped")
	}
	if err := db.apply(before); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	later := now.Add(24 * time.Hour)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO households (id, name, created_at, updated_at) VALUES ('h1', 'Familie', ?, ?)`, []any{stamp(now), stamp(now)}},
		{`INSERT INTO people (id, name, created_at, updated_at) VALUES ('p1', 'Mia', ?, ?)`, []any{stamp(now), stamp(now)}},
		{`INSERT INTO household_members (household_id, person_id, role) VALUES ('h1', 'p1', 'child')`, nil},
		{`INSERT INTO phones (id, household_id, person_id, name, created_at) VALUES ('d1', 'h1', 'p1', 'Mias Handy', ?)`, []any{stamp(now)}},
		{`INSERT INTO sessions (id, token_hash, person_id, created_at, expires_at, phone_id) VALUES ('s1', 'hash1', 'p1', ?, ?, 'd1')`, []any{stamp(now), stamp(later)}},
	} {
		if _, err := conn.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seeding the old database: %v\n%s", err, q.sql)
		}
	}

	if err := db.apply([]migration{rename}); err != nil {
		t.Fatal(err)
	}

	list, err := db.PersonalDevices("h1")
	if err != nil || len(list) != 1 || list[0].ID != "d1" || list[0].Name != "Mias Handy" || list[0].PersonID != "p1" {
		t.Fatalf("personal devices after the migration = %+v, %v", list, err)
	}
	if !list[0].SessionEnds.Equal(later) {
		t.Errorf("session end = %v, want %v", list[0].SessionEnds, later)
	}
	if id, err := db.SessionPerson("hash1", now.Add(time.Hour)); err != nil || id != "p1" {
		t.Errorf("session person = %q, %v, want p1: the session is no longer valid", id, err)
	}
	if !db.IsPersonalDeviceSession("hash1") {
		t.Error("the session is no longer a personal device's device session")
	}
}

// #243: the migration that adds the kind of device derives it once from the label that
// pairings opened before it carry ("Safari on iPhone"), and turns the label into device keys.
func TestAddingTheDeviceKindDerivesItFromTheOldLabel(t *testing.T) {
	t.Parallel()
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "hejmo.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{sql: conn}
	defer db.Close()
	if _, err := conn.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	all, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	var before []migration
	var add migration
	for _, m := range all {
		if strings.HasSuffix(m.name, "_device_kind.sql") {
			add = m
			break
		}
		before = append(before, m)
	}
	if add.name == "" {
		t.Fatal("the migration is not shipped")
	}
	if err := db.apply(before); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	if _, err := conn.Exec(`INSERT INTO households (id, name, created_at, updated_at) VALUES ('h1', 'Familie', ?, ?)`, stamp(now), stamp(now)); err != nil {
		t.Fatal(err)
	}
	labels := []struct{ label, device, kind string }{
		{"Safari on iPhone", "safari|iphone", "phone"},
		{"Chrome on Android phone", "chrome|android_phone", "phone"},
		{"Safari on iPad", "safari|ipad", "tablet"},
		{"Samsung Internet on Android tablet", "samsung|android_tablet", "tablet"},
		{"Firefox", "firefox|unknown", "device"},
		{"Edge", "edge|unknown", "device"},
		{"Browser", "unknown|unknown", "device"},
		{"", "", "device"},
	}
	for i, l := range labels {
		if _, err := conn.Exec(`INSERT INTO personal_device_pairings (id, household_id, code_hash, created_at, expires_at, device) VALUES (?, 'h1', ?, ?, ?, ?)`,
			fmt.Sprint("p", i), fmt.Sprint("c", i), stamp(now), stamp(now.Add(time.Hour)), l.label); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.apply([]migration{add}); err != nil {
		t.Fatal(err)
	}
	for i, l := range labels {
		var device, kind string
		if err := conn.QueryRow(`SELECT device, device_kind FROM personal_device_pairings WHERE id = ?`, fmt.Sprint("p", i)).Scan(&device, &kind); err != nil {
			t.Fatal(err)
		}
		if device != l.device || kind != l.kind {
			t.Errorf("%q became device %q, kind %q; want %q, %q", l.label, device, kind, l.device, l.kind)
		}
	}
}

// #296: the migration that adds the address book gives every existing household the five groups
// and every existing person a contact with their name, in Familie.
func TestAddingTheAddressBookMakesContactsOfExistingPeople(t *testing.T) {
	t.Parallel()
	conn, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "hejmo.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{sql: conn}
	defer db.Close()
	if _, err := conn.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	all, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	var before []migration
	var add migration
	for _, m := range all {
		if strings.HasSuffix(m.name, "_contacts.sql") {
			add = m
			break
		}
		before = append(before, m)
	}
	if add.name == "" {
		t.Fatal("the migration is not shipped")
	}
	if err := db.apply(before); err != nil {
		t.Fatal(err)
	}
	ts := stamp(time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC))
	for _, q := range []string{
		`INSERT INTO households (id, name, created_at, updated_at) VALUES ('h1', 'Familie', '` + ts + `', '` + ts + `')`,
		`INSERT INTO people (id, name, created_at, updated_at) VALUES ('p1', 'Mama', '` + ts + `', '` + ts + `')`,
		`INSERT INTO people (id, name, created_at, updated_at) VALUES ('p2', 'Mia', '` + ts + `', '` + ts + `')`,
		`INSERT INTO household_members (household_id, person_id, role, sort_order) VALUES ('h1', 'p1', 'parent', 1)`,
		`INSERT INTO household_members (household_id, person_id, role, sort_order) VALUES ('h1', 'p2', 'child', 2)`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.apply([]migration{add}); err != nil {
		t.Fatal(err)
	}
	var groups, members int
	var name string
	if err := conn.QueryRow(`SELECT COUNT(*) FROM contact_groups WHERE household_id = 'h1' AND shipped_key IS NOT NULL`).Scan(&groups); err != nil || groups != 5 {
		t.Errorf("shipped groups = %d, %v", groups, err)
	}
	if err := conn.QueryRow(`SELECT hejmo_name FROM contacts WHERE person_id = 'p2'`).Scan(&name); err != nil || name != "Mia" {
		t.Errorf("Mia's contact = %q, %v", name, err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM contact_group_members m JOIN contact_groups g ON g.id = m.group_id WHERE g.shipped_key = 'familie'`).Scan(&members); err != nil || members != 2 {
		t.Errorf("members of Familie = %d, %v", members, err)
	}
}
