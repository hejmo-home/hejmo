package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/contact"
)

// ShippedGroups are the keys of the five contact groups that come with Hejmo,
// in the order the pages show them. Their names are in the locales.
var ShippedGroups = []string{"familie", "kinderbetreuung_schule", "arztpraxen", "notfall", "sonstige"}

// FamilyGroup is the shipped group every person's contact is in.
const FamilyGroup = "familie"

// Contact is an entry of the household's address book (ADR 0021). The contact
// of a person has the id of the person; its Hejmo name is the person's name.
type Contact struct {
	ID, PersonID string
	HejmoName    string
	FullName     string
	Street       string
	Postcode     string
	City         string
	Country      string
	// BdayDay and BdayMonth belong together; BdayYear is 0 when it is unknown.
	BdayDay, BdayMonth, BdayYear int
	// HideBirthday keeps the birthday out of the generated calendar Birthdays (ticket 302).
	HideBirthday    bool
	Note            string
	Numbers, Emails []contact.Line
	GroupIDs        []string
	// ForChildren shows the contact on the children's page "Wichtige Nummern" (ticket 301); off for every new contact.
	ForChildren bool
	// Relations say who the contact is for the family; only further contacts have any (ticket 298).
	Relations                []Relation
	CreatedBy, CreatedByName string
	CreatedAt                time.Time
	UpdatedBy, UpdatedByName string
	UpdatedAt                time.Time
	// HasPhoto says there is a photo (ContactPhoto reads it); the rest says where it came from: PhotoOrigin is
	// PhotoManual (PhotoBy, the parent who set it) or PhotoSync (PhotoDevice, the phone). PhotoBy is empty when
	// that person is gone.
	HasPhoto             bool
	PhotoOrigin          string
	PhotoBy, PhotoByName string
	PhotoDevice          string
	PhotoAt              time.Time
	// CreatedFile, UpdatedFile and PhotoFile name the contact file (ticket 300) the contact was taken over
	// from, the last change completed it from, and its photo came from; empty for what a person did by hand.
	CreatedFile, UpdatedFile, PhotoFile string
	// NameDayMode, NameDayEntry and NameDayName say which name day the contact has (ticket 318): nameday.ModeAuto,
	// ModeEntry (the id of an entry of the household's list), ModeName (another name) or ModeNone. NameDayBy is
	// the person who chose, empty for auto.
	NameDayMode, NameDayEntry, NameDayName string
	NameDayBy, NameDayByName               string
}

// Where a contact photo came from.
const (
	PhotoManual = "manual" // a parent chose it
	PhotoSync   = "sync"   // a phone sent it over contact sync (ticket 299)
)

// ContactPhoto is a photo to store, or the stored one: the JPEG Hejmo made. Origin defaults to PhotoManual; the
// parent who sets it is the `by` of the save.
type ContactPhoto struct {
	JPEG   []byte
	Origin string
	Device string // the phone, for PhotoSync
	File   string // the contact file the photo came from (ticket 300)
	By     string
	At     time.Time
}

// IsPerson reports whether the contact is a person of the household.
func (c Contact) IsPerson() bool { return c.PersonID != "" }

// ContactGroup is a category of contacts: one of the five of Hejmo (ShippedKey
// set, no Name) or one of the household's own.
type ContactGroup struct {
	ID, Name, ShippedKey string
	CreatedByName        string
	CreatedAt            time.Time
}

// NameTakenError says the Hejmo name is in use already, by the contact Existing
// (a person of the household when ExistingIsPerson). It matches ErrInvalid.
type NameTakenError struct {
	Existing         string
	ExistingIsPerson bool
}

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("%s: the name %q is taken", ErrInvalid, e.Existing)
}
func (e *NameTakenError) Unwrap() error { return ErrInvalid }

// seedContactGroups makes the five shipped groups of a household.
func seedContactGroups(tx *sql.Tx, householdID, ts string) error {
	for _, k := range ShippedGroups {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO contact_groups (id, household_id, name, shipped_key, created_at) VALUES (?, ?, '', ?, ?)`,
			newID(), householdID, k, ts); err != nil {
			return err
		}
	}
	return nil
}

// addPersonContact makes the contact of a new person: Hejmo name = the
// person's name, always in the group Familie.
func addPersonContact(tx *sql.Tx, householdID string, p Person, ts string) error {
	if err := seedContactGroups(tx, householdID, ts); err != nil { // a household made before the groups existed
		return err
	}
	if _, err := tx.Exec(`INSERT INTO contacts (id, household_id, person_id, hejmo_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, householdID, p.ID, p.Name, ts, ts); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO contact_group_members (group_id, contact_id)
		SELECT id, ? FROM contact_groups WHERE household_id = ? AND shipped_key = ?`, p.ID, householdID, FamilyGroup)
	return err
}

const contactColumns = `c.id, COALESCE(c.person_id, ''), c.hejmo_name, c.full_name, c.street, c.postcode, c.city, c.country,
	COALESCE(c.bday_day, 0), COALESCE(c.bday_month, 0), COALESCE(c.bday_year, 0), c.hide_birthday, c.for_children, c.note,
	COALESCE(c.created_by, ''), COALESCE(cp.name, ''), c.created_at, COALESCE(c.updated_by, ''), COALESCE(up.name, ''), c.updated_at,
	c.photo IS NOT NULL, c.photo_origin, COALESCE(c.photo_by, ''), COALESCE(pp.name, ''), c.photo_device, c.photo_at,
	c.created_file, c.updated_file, c.photo_file, c.name_day_mode, c.name_day_entry, c.name_day_name, COALESCE(c.name_day_by, ''), COALESCE(np.name, '')`

const contactFrom = ` FROM contacts c LEFT JOIN people cp ON cp.id = c.created_by LEFT JOIN people up ON up.id = c.updated_by LEFT JOIN people pp ON pp.id = c.photo_by LEFT JOIN people np ON np.id = c.name_day_by `

func scanContact(sc interface{ Scan(...any) error }) (Contact, error) {
	var c Contact
	var created, updated, photoAt string
	err := sc.Scan(&c.ID, &c.PersonID, &c.HejmoName, &c.FullName, &c.Street, &c.Postcode, &c.City, &c.Country,
		&c.BdayDay, &c.BdayMonth, &c.BdayYear, &c.HideBirthday, &c.ForChildren, &c.Note, &c.CreatedBy, &c.CreatedByName, &created, &c.UpdatedBy, &c.UpdatedByName, &updated,
		&c.HasPhoto, &c.PhotoOrigin, &c.PhotoBy, &c.PhotoByName, &c.PhotoDevice, &photoAt, &c.CreatedFile, &c.UpdatedFile, &c.PhotoFile, &c.NameDayMode, &c.NameDayEntry, &c.NameDayName, &c.NameDayBy, &c.NameDayByName)
	c.PhotoAt, _ = time.Parse(time.RFC3339, photoAt)
	c.CreatedAt, _ = time.Parse(time.RFC3339, created)
	c.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return c, err
}

// loadLines fills the numbers, the e-mail addresses and the groups of contacts.
func (db *DB) loadContactParts(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, householdID, onlyID string, list []Contact) error {
	at := map[string]int{}
	for i := range list {
		at[list[i].ID] = i
	}
	for _, t := range []struct{ table, e164 string }{{"contact_phones", "l.e164"}, {"contact_emails", "''"}} {
		rows, err := q.Query(`SELECT l.contact_id, l.kind, l.custom_label, l.value, `+t.e164+` FROM `+t.table+` l
			JOIN contacts c ON c.id = l.contact_id WHERE c.household_id = ? AND (? = '' OR c.id = ?) ORDER BY l.contact_id, l.position`, householdID, onlyID, onlyID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			var l contact.Line
			if err := rows.Scan(&id, &l.Kind, &l.Label, &l.Value, &l.E164); err != nil {
				rows.Close()
				return err
			}
			if i, ok := at[id]; ok {
				if t.table == "contact_phones" {
					list[i].Numbers = append(list[i].Numbers, l)
				} else {
					list[i].Emails = append(list[i].Emails, l)
				}
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	rows, err := q.Query(`SELECT m.contact_id, m.group_id FROM contact_group_members m JOIN contacts c ON c.id = m.contact_id
		WHERE c.household_id = ? AND (? = '' OR c.id = ?) ORDER BY m.contact_id, m.group_id`, householdID, onlyID, onlyID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, g string
		if err := rows.Scan(&id, &g); err != nil {
			return err
		}
		if i, ok := at[id]; ok {
			list[i].GroupIDs = append(list[i].GroupIDs, g)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	return loadRelations(q, householdID, onlyID, list, at)
}

// Contacts lists the contacts of a household: those of people first in the
// order of the people, then the others by creation (callers sort by name).
func (db *DB) Contacts(householdID string) ([]Contact, error) {
	rows, err := db.sql.Query(`SELECT `+contactColumns+contactFrom+`
		LEFT JOIN household_members m ON m.person_id = c.person_id AND m.household_id = c.household_id
		WHERE c.household_id = ? ORDER BY (c.person_id IS NULL), m.sort_order, c.created_at, c.rowid`, householdID)
	if err != nil {
		return nil, err
	}
	var out []Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return out, db.loadContactParts(db.sql, householdID, "", out)
}

// Contact loads one contact of a household.
func (db *DB) Contact(householdID, id string) (Contact, error) {
	rows, err := db.sql.Query(`SELECT `+contactColumns+contactFrom+` WHERE c.household_id = ? AND c.id = ?`, householdID, id)
	if err != nil {
		return Contact{}, err
	}
	var out []Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			rows.Close()
			return Contact{}, err
		}
		out = append(out, c)
	}
	if err := rows.Close(); err != nil {
		return Contact{}, err
	}
	if len(out) == 0 {
		return Contact{}, ErrNotFound
	}
	return out[0], db.loadContactParts(db.sql, householdID, id, out)
}

// checkContactName looks for another contact of the household (people
// included) with the same Hejmo name, ignoring case.
func checkContactName(tx *sql.Tx, householdID, exceptID, name string) error {
	rows, err := tx.Query(`SELECT id, hejmo_name, person_id IS NOT NULL FROM contacts WHERE household_id = ? AND id <> ?`, householdID, exceptID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, n string
		var person bool
		if err := rows.Scan(&id, &n, &person); err != nil {
			return err
		}
		if strings.EqualFold(n, name) {
			return &NameTakenError{Existing: n, ExistingIsPerson: person}
		}
	}
	return rows.Err()
}

// ContactInput is what a parent can set on a contact. For a person's contact
// HejmoName is ignored: the name belongs to the person.
type ContactInput struct {
	HejmoName, FullName, Street, Postcode, City, Country string
	BdayDay, BdayMonth, BdayYear                         int
	// HideBirthday keeps the birthday out of the generated calendar Birthdays.
	HideBirthday bool
	// ForChildren shows the contact on the children's page "Wichtige Nummern".
	ForChildren bool
	// CalendarName names that calendar if this change makes it ("Birthdays", in the language of whoever saves).
	CalendarName    string
	Note            string
	Numbers, Emails []contact.Line
	// GroupIDs are the groups the contact is in; a person is always in Familie as well.
	GroupIDs []string
	// Relations replace the contact's relations; a person's contact has none.
	Relations []Relation
	// NewGroup, when not empty, is the name of a group made together with the contact and added to it.
	NewGroup string
	// Today is the household's today, which the birthday is checked against; the zero value means now.
	Today time.Time
	// PhotoChange says what the save does to the photo: keep it (the zero value), set Photo, or remove it.
	PhotoChange PictureChange
	Photo       *ContactPhoto
	// ImportFile is the name of the contact file this save takes the contact over from (a new contact) or
	// completes it from (ticket 300); empty for a save by hand.
	ImportFile string
	// NameDayMode, NameDayEntry and NameDayName are the choice of the name day (ticket 318, see nameday.Choice).
	// An empty mode keeps what the contact has (a new contact: auto), so a save that does not know about name
	// days, such as the contact import, changes nothing.
	NameDayMode, NameDayEntry, NameDayName string
}

func (in *ContactInput) clean() error {
	in.HejmoName, in.FullName = strings.TrimSpace(in.HejmoName), strings.TrimSpace(in.FullName)
	in.Street, in.Postcode, in.City, in.Country = strings.TrimSpace(in.Street), strings.TrimSpace(in.Postcode), strings.TrimSpace(in.City), strings.TrimSpace(in.Country)
	in.Note, in.NewGroup = strings.TrimSpace(in.Note), strings.TrimSpace(in.NewGroup)
	in.Numbers, in.Emails = contact.CleanLines(in.Numbers), contact.CleanLines(in.Emails)
	switch {
	case in.HejmoName == "" || contact.Runes(in.HejmoName) > contact.MaxName:
		return &FieldError{Field: "name", Msg: "the Hejmo name is required, at most 40 characters"}
	case contact.Runes(in.FullName) > contact.MaxFullName:
		return &FieldError{Field: "full_name", Msg: "the full name is at most 120 characters"}
	case contact.Runes(in.Note) > contact.MaxNote:
		return &FieldError{Field: "note", Msg: "the note is at most 1000 characters"}
	case len(in.Numbers) > contact.MaxLines:
		return &FieldError{Field: "numbers", Msg: "at most 10 numbers"}
	case len(in.Emails) > contact.MaxLines:
		return &FieldError{Field: "emails", Msg: "at most 10 e-mail addresses"}
	case contact.Runes(in.NewGroup) > contact.MaxName:
		return &FieldError{Field: "new_group", Msg: "the group name is at most 40 characters"}
	}
	for _, l := range append(append([]contact.Line{}, in.Numbers...), in.Emails...) {
		if !contact.KindValid(l.Kind) || (l.Kind == contact.Custom && (l.Label == "" || contact.Runes(l.Label) > contact.MaxOwnLabel)) {
			return &FieldError{Field: "kind", Msg: "a custom kind needs a label of at most 30 characters"}
		}
	}
	if in.PhotoChange == SetPicture {
		if in.Photo == nil || len(in.Photo.JPEG) == 0 {
			return &FieldError{Field: "photo", Msg: "a photo to set is needed"}
		}
		if in.Photo.Origin == "" {
			in.Photo.Origin = PhotoManual
		}
		if in.Photo.Origin != PhotoManual && in.Photo.Origin != PhotoSync {
			return &FieldError{Field: "photo", Msg: "the photo comes from the hand or from sync"}
		}
	}
	if in.BdayDay == 0 && in.BdayMonth == 0 {
		in.BdayYear = 0 // a year alone is no birthday
	}
	if in.BdayDay != 0 || in.BdayMonth != 0 || in.BdayYear != 0 {
		today := in.Today
		if today.IsZero() {
			today = time.Now()
		}
		if msg := contact.CheckBirthday(in.BdayDay, in.BdayMonth, in.BdayYear, today); msg != "" {
			return &FieldError{Field: "birthday", Msg: "the birthday is not a date: " + msg}
		}
	}
	return nil
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// homeCountry is the country of the household's home (ISO 3166-1), the region numbers are read with (ADR 0026);
// empty when the household has none.
func homeCountry(tx *sql.Tx, householdID string) string {
	var region string
	_ = tx.QueryRow(`SELECT region FROM households WHERE id = ?`, householdID).Scan(&region)
	country, _, _ := strings.Cut(region, "-")
	return country
}

func writeContactParts(tx *sql.Tx, householdID, id string, in ContactInput, isPerson bool, by, ts string) error {
	country := homeCountry(tx, householdID)
	for _, t := range []struct {
		table string
		lines []contact.Line
	}{{"contact_phones", in.Numbers}, {"contact_emails", in.Emails}} {
		if _, err := tx.Exec(`DELETE FROM `+t.table+` WHERE contact_id = ?`, id); err != nil {
			return err
		}
		for i, l := range t.lines {
			var err error
			if t.table == "contact_phones" {
				_, err = tx.Exec(`INSERT INTO contact_phones (contact_id, position, kind, custom_label, value, e164) VALUES (?, ?, ?, ?, ?, ?)`,
					id, i+1, l.Kind, l.Label, l.Value, contact.E164(l.Value, country))
			} else {
				_, err = tx.Exec(`INSERT INTO `+t.table+` (contact_id, position, kind, custom_label, value) VALUES (?, ?, ?, ?, ?)`, id, i+1, l.Kind, l.Label, l.Value)
			}
			if err != nil {
				return err
			}
		}
	}
	if err := writeRelations(tx, householdID, id, in.Relations, isPerson); err != nil {
		return err
	}
	ids := append([]string(nil), in.GroupIDs...)
	if in.NewGroup != "" {
		g, err := addGroup(tx, householdID, in.NewGroup, by, ts)
		if err != nil {
			return err
		}
		ids = append(ids, g)
	}
	if isPerson { // always in Familie
		var fam string
		if err := tx.QueryRow(`SELECT id FROM contact_groups WHERE household_id = ? AND shipped_key = ?`, householdID, FamilyGroup).Scan(&fam); err != nil {
			return err
		}
		ids = append(ids, fam)
	}
	if _, err := tx.Exec(`DELETE FROM contact_group_members WHERE contact_id = ?`, id); err != nil {
		return err
	}
	for _, g := range ids {
		res, err := tx.Exec(`INSERT OR IGNORE INTO contact_group_members (group_id, contact_id)
			SELECT id, ? FROM contact_groups WHERE id = ? AND household_id = ?`, id, g, householdID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var have int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM contact_group_members WHERE contact_id = ? AND group_id = ?`, id, g).Scan(&have); err != nil {
				return err
			}
			if have == 0 {
				return &FieldError{Field: "groups", Msg: "unknown group"}
			}
		}
	}
	return nil
}

// CreateContact stores a contact made by the parent `by`.
func (db *DB) CreateContact(householdID, by string, in ContactInput) (Contact, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return Contact{}, err
	}
	defer tx.Rollback()
	id, err := createContact(tx, householdID, by, in)
	if err != nil {
		return Contact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Contact{}, err
	}
	return db.Contact(householdID, id)
}

func createContact(tx *sql.Tx, householdID, by string, in ContactInput) (string, error) {
	if err := in.clean(); err != nil {
		return "", err
	}
	id, ts := newID(), now()
	if err := checkContactName(tx, householdID, "", in.HejmoName); err != nil {
		return "", err
	}
	if err := checkNameDayChoice(tx, householdID, &in); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO contacts (id, household_id, hejmo_name, full_name, street, postcode, city, country, bday_day, bday_month, bday_year, hide_birthday, for_children, note, created_by, created_at, updated_by, updated_at, created_file)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, householdID, in.HejmoName, in.FullName, in.Street, in.Postcode, in.City, in.Country, nullInt(in.BdayDay), nullInt(in.BdayMonth), nullInt(in.BdayYear), in.HideBirthday, in.ForChildren, in.Note,
		nullString(by), ts, nullString(by), ts, in.ImportFile); err != nil {
		return "", err
	}
	if err := writeNameDayChoice(tx, id, by, in, "", "", ""); err != nil {
		return "", err
	}
	if err := writeContactParts(tx, householdID, id, in, false, by, ts); err != nil {
		return "", err
	}
	if err := writeContactPhoto(tx, id, in, by); err != nil {
		return "", err
	}
	if err := syncBirthdays(tx, householdID, in.CalendarName); err != nil {
		return "", err
	}
	return id, nil
}

// writeContactPhoto applies the photo change of a save; the last change wins.
func writeContactPhoto(tx *sql.Tx, id string, in ContactInput, by string) error {
	return applyContactPhoto(tx, id, in.PhotoChange, in.Photo, by)
}

// applyContactPhoto writes the photo columns. The time of a photo has nanoseconds: it is part of the address of
// the picture, so two changes in one second must not give the same address.
func applyContactPhoto(tx *sql.Tx, id string, change PictureChange, photo *ContactPhoto, by string) error {
	switch change {
	case SetPicture:
		device := ""
		if photo.Origin == PhotoSync {
			device, by = photo.Device, ""
		}
		_, err := tx.Exec(`UPDATE contacts SET photo = ?, photo_origin = ?, photo_by = ?, photo_device = ?, photo_at = ?, photo_file = ? WHERE id = ?`,
			photo.JPEG, photo.Origin, nullString(by), device, time.Now().UTC().Format(time.RFC3339Nano), photo.File, id)
		return err
	case RemovePicture:
		_, err := tx.Exec(`UPDATE contacts SET photo = NULL, photo_origin = '', photo_by = NULL, photo_device = '', photo_at = '', photo_file = '' WHERE id = ?`, id)
		return err
	}
	return nil
}

// SetContactPhoto sets or removes the photo of a contact and touches nothing else: no other field is checked, and
// updated_by and updated_at stay as they are. ErrNotFound for an unknown contact; ErrInvalid for a photo to set
// that is missing or has an unknown origin.
func (db *DB) SetContactPhoto(householdID, id, by string, change PictureChange, photo *ContactPhoto) (Contact, error) {
	if change == SetPicture {
		if photo == nil || len(photo.JPEG) == 0 {
			return Contact{}, &FieldError{Field: "photo", Msg: "a photo to set is needed"}
		}
		if photo.Origin == "" {
			photo.Origin = PhotoManual
		}
		if photo.Origin != PhotoManual && photo.Origin != PhotoSync {
			return Contact{}, &FieldError{Field: "photo", Msg: "the photo comes from the hand or from sync"}
		}
	}
	if _, err := db.Contact(householdID, id); err != nil {
		return Contact{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return Contact{}, err
	}
	defer tx.Rollback()
	if err := applyContactPhoto(tx, id, change, photo, by); err != nil {
		return Contact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Contact{}, err
	}
	return db.Contact(householdID, id)
}

// ContactPhoto reads the stored JPEG of a contact; ErrNotFound when there is none.
func (db *DB) ContactPhoto(householdID, id string) (ContactPhoto, error) {
	var p ContactPhoto
	var at string
	err := db.sql.QueryRow(`SELECT photo, photo_origin, COALESCE(photo_by, ''), photo_device, photo_at FROM contacts WHERE household_id = ? AND id = ?`,
		householdID, id).Scan(&p.JPEG, &p.Origin, &p.By, &p.Device, &at)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && p.JPEG == nil) {
		return ContactPhoto{}, ErrNotFound
	}
	p.At, _ = time.Parse(time.RFC3339, at)
	return p, err
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// UpdateContact replaces everything a parent can set on a contact. A person's
// contact keeps its name (the person page changes it) and its place in Familie.
func (db *DB) UpdateContact(householdID, id, by string, in ContactInput) (Contact, error) {
	cur, err := db.Contact(householdID, id)
	if err != nil {
		return Contact{}, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return Contact{}, err
	}
	defer tx.Rollback()
	if err := updateContact(tx, householdID, id, by, cur.IsPerson(), cur.HejmoName, in); err != nil {
		return Contact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Contact{}, err
	}
	return db.Contact(householdID, id)
}

func updateContact(tx *sql.Tx, householdID, id, by string, isPerson bool, curName string, in ContactInput) error {
	if isPerson {
		in.HejmoName = curName
	}
	if err := in.clean(); err != nil {
		return err
	}
	ts := now()
	if err := checkContactName(tx, householdID, id, in.HejmoName); err != nil {
		return err
	}
	if err := checkNameDayChoice(tx, householdID, &in); err != nil {
		return err
	}
	var ndMode, ndEntry, ndName string
	if err := tx.QueryRow(`SELECT name_day_mode, name_day_entry, name_day_name FROM contacts WHERE id = ? AND household_id = ?`, id, householdID).Scan(&ndMode, &ndEntry, &ndName); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE contacts SET hejmo_name = ?, full_name = ?, street = ?, postcode = ?, city = ?, country = ?, bday_day = ?, bday_month = ?, bday_year = ?, hide_birthday = ?, for_children = ?, note = ?, updated_by = ?, updated_at = ?, updated_file = ?
		WHERE id = ? AND household_id = ?`,
		in.HejmoName, in.FullName, in.Street, in.Postcode, in.City, in.Country, nullInt(in.BdayDay), nullInt(in.BdayMonth), nullInt(in.BdayYear), in.HideBirthday, in.ForChildren, in.Note, nullString(by), ts, in.ImportFile, id, householdID); err != nil {
		return err
	}
	if err := writeNameDayChoice(tx, id, by, in, ndMode, ndEntry, ndName); err != nil {
		return err
	}
	if err := writeContactParts(tx, householdID, id, in, isPerson, by, ts); err != nil {
		return err
	}
	if err := writeContactPhoto(tx, id, in, by); err != nil {
		return err
	}
	return syncBirthdays(tx, householdID, in.CalendarName)
}

// DeleteContact removes a contact outside the household. A person's contact
// goes only with the person: ErrInvalid.
func (db *DB) DeleteContact(householdID, id string) error {
	c, err := db.Contact(householdID, id)
	if err != nil {
		return err
	}
	if c.IsPerson() {
		return &FieldError{Field: "id", Msg: "the contact of a person goes with the person"}
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := expectOne(tx.Exec(`DELETE FROM contacts WHERE id = ? AND household_id = ?`, id, householdID)); err != nil {
		return err
	}
	// The name is for a calendar that has to be made; deleting a contact makes
	// none (a calendar exists as soon as one birthday was saved).
	if err := syncBirthdays(tx, householdID, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// ContactGroups lists the groups of a household: the five of Hejmo in their
// order, then the own ones by name.
func (db *DB) ContactGroups(householdID string) ([]ContactGroup, error) {
	rows, err := db.sql.Query(`SELECT g.id, g.name, COALESCE(g.shipped_key, ''), COALESCE(p.name, ''), g.created_at FROM contact_groups g
		LEFT JOIN people p ON p.id = g.created_by WHERE g.household_id = ? ORDER BY g.name COLLATE NOCASE, g.rowid`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var shipped, own []ContactGroup
	for rows.Next() {
		var g ContactGroup
		var ts string
		if err := rows.Scan(&g.ID, &g.Name, &g.ShippedKey, &g.CreatedByName, &ts); err != nil {
			return nil, err
		}
		g.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		if g.ShippedKey != "" {
			shipped = append(shipped, g)
		} else {
			own = append(own, g)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []ContactGroup
	for _, k := range ShippedGroups {
		for _, g := range shipped {
			if g.ShippedKey == k {
				out = append(out, g)
			}
		}
	}
	return append(out, own...), nil
}

// ContactGroup loads one group of a household.
func (db *DB) ContactGroup(householdID, id string) (ContactGroup, error) {
	all, err := db.ContactGroups(householdID)
	if err != nil {
		return ContactGroup{}, err
	}
	for _, g := range all {
		if g.ID == id {
			return g, nil
		}
	}
	return ContactGroup{}, ErrNotFound
}

// GroupExistsMsg is the Msg of the FieldError for a group name that is in use.
const GroupExistsMsg = "a group of this name exists"

func addGroup(tx *sql.Tx, householdID, name, by, ts string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || contact.Runes(name) > contact.MaxName {
		return "", &FieldError{Field: "new_group", Msg: "a group name is 1 to 40 characters"}
	}
	var have int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM contact_groups WHERE household_id = ? AND shipped_key IS NULL AND name = ? COLLATE NOCASE`, householdID, name).Scan(&have); err != nil {
		return "", err
	}
	if have > 0 {
		return "", &FieldError{Field: "new_group", Msg: GroupExistsMsg}
	}
	id := newID()
	_, err := tx.Exec(`INSERT INTO contact_groups (id, household_id, name, created_by, created_at) VALUES (?, ?, ?, ?, ?)`, id, householdID, name, nullString(by), ts)
	return id, err
}

// CreateContactGroup makes an own group. A name in use gives a FieldError "new_group"; names that
// clash with a group of Hejmo are checked by the caller (their names are in the locales).
func (db *DB) CreateContactGroup(householdID, by, name string) (ContactGroup, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return ContactGroup{}, err
	}
	defer tx.Rollback()
	id, err := addGroup(tx, householdID, name, by, now())
	if err != nil {
		return ContactGroup{}, err
	}
	if err := tx.Commit(); err != nil {
		return ContactGroup{}, err
	}
	return db.ContactGroup(householdID, id)
}

// RenameContactGroup renames an own group; the groups of Hejmo keep their names.
func (db *DB) RenameContactGroup(householdID, id, name string) error {
	g, err := db.ContactGroup(householdID, id)
	if err != nil {
		return err
	}
	if g.ShippedKey != "" {
		return &FieldError{Field: "id", Msg: "the groups of Hejmo cannot be renamed"}
	}
	name = strings.TrimSpace(name)
	if name == "" || contact.Runes(name) > contact.MaxName {
		return &FieldError{Field: "name", Msg: "a group name is 1 to 40 characters"}
	}
	var have int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM contact_groups WHERE household_id = ? AND shipped_key IS NULL AND name = ? COLLATE NOCASE AND id <> ?`, householdID, name, id).Scan(&have); err != nil {
		return err
	}
	if have > 0 {
		return &FieldError{Field: "name", Msg: GroupExistsMsg}
	}
	return expectOne(db.sql.Exec(`UPDATE contact_groups SET name = ? WHERE id = ? AND household_id = ?`, name, id, householdID))
}

// DeleteContactGroup removes an own group; its contacts stay, without the group.
func (db *DB) DeleteContactGroup(householdID, id string) error {
	g, err := db.ContactGroup(householdID, id)
	if err != nil {
		return err
	}
	if g.ShippedKey != "" {
		return &FieldError{Field: "id", Msg: "the groups of Hejmo cannot be deleted"}
	}
	return expectOne(db.sql.Exec(`DELETE FROM contact_groups WHERE id = ? AND household_id = ?`, id, householdID))
}
