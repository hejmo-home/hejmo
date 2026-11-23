package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 296 (ADR 0021): the address book in the store.

func contactNames(t *testing.T, db *store.DB, h store.Household) []string {
	t.Helper()
	all, err := db.Contacts(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range all {
		out = append(out, c.HejmoName)
	}
	return out
}

func groupKey(t *testing.T, db *store.DB, h store.Household, key string) store.ContactGroup {
	t.Helper()
	gs, err := db.ContactGroups(h.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gs {
		if g.ShippedKey == key {
			return g
		}
	}
	t.Fatalf("no shipped group %q", key)
	return store.ContactGroup{}
}

func TestEveryPersonIsAContactInFamilieFromTheStartAndTheNameFollows(t *testing.T) {
	t.Parallel()
	db, h, f := exampleFamily(t)
	if got := strings.Join(contactNames(t, db, h), ","); got != "Mama,Papa,Sebastian,Mia" {
		t.Fatalf("contacts = %s", got)
	}
	fam := groupKey(t, db, h, "familie")
	mia, err := db.Contact(h.ID, f["Mia"].ID)
	if err != nil || mia.PersonID != f["Mia"].ID || len(mia.GroupIDs) != 1 || mia.GroupIDs[0] != fam.ID {
		t.Fatalf("Mia's contact = %+v, %v", mia, err)
	}
	if err := db.UpdatePerson(h.ID, f["Mia"].ID, "Mia Maier", "", ""); err != nil {
		t.Fatal(err)
	}
	if mia, _ = db.Contact(h.ID, f["Mia"].ID); mia.HejmoName != "Mia Maier" {
		t.Errorf("the contact keeps the old name: %q", mia.HejmoName)
	}
	groups, _ := db.ContactGroups(h.ID)
	var keys []string
	for _, g := range groups {
		keys = append(keys, g.ShippedKey)
	}
	if strings.Join(keys, ",") != strings.Join(store.ShippedGroups, ",") {
		t.Errorf("groups = %v, want the five of Hejmo", keys)
	}
}

// Acceptance: the number is kept as typed; empty lines and an empty address are never stored.
func TestAContactKeepsTheNumberAsTypedAndNeverStoresEmptyLinesOrAnEmptyAddress(t *testing.T) {
	t.Parallel()
	db, h, f := exampleFamily(t)
	c, err := db.CreateContact(h.ID, f["Mama"].ID, store.ContactInput{
		HejmoName: " Oma Gerda ", FullName: "Gerda Müller",
		Numbers: []contact.Line{{Kind: contact.Cell, Value: " 0171 3456 789 "}, {Kind: contact.Home, Value: "  "}, {Kind: contact.Custom, Label: "Gartenhaus", Value: "+49 (0)160/111-2223"}, {Kind: contact.Work}},
		Emails:  []contact.Line{{Kind: contact.Home}, {Kind: contact.Home, Value: "gerda.mueller@example.de"}},
		Street:  " ", Postcode: "", City: "\t", Country: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.HejmoName != "Oma Gerda" || len(c.Numbers) != 2 || c.Numbers[0].Value != "0171 3456 789" || c.Numbers[1].Value != "+49 (0)160/111-2223" || c.Numbers[1].Label != "Gartenhaus" || len(c.Emails) != 1 {
		t.Fatalf("contact = %+v", c)
	}
	if c.Street != "" || c.Postcode != "" || c.City != "" || c.Country != "" {
		t.Errorf("an empty address was stored: %+v", c)
	}
	var n int
	for _, table := range []string{"contact_phones", "contact_emails"} {
		if err := db.SQL().QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE value = '' OR trim(value) = ''`).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s holds %d empty lines (%v)", table, n, err)
		}
	}
	if c.CreatedByName != "Mama" {
		t.Errorf("created by %q", c.CreatedByName)
	}
}

// Acceptance: a birthday without a year is a birthday without an age.
func TestABirthdayMayLackTheYearButNeverHaveJustADayOrMonth(t *testing.T) {
	t.Parallel()
	db, h, _ := exampleFamily(t)
	c, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Oma Gerda", BdayDay: 14, BdayMonth: 3})
	if err != nil || c.BdayDay != 14 || c.BdayMonth != 3 || c.BdayYear != 0 {
		t.Fatalf("birthday = %+v, %v", c, err)
	}
	for name, in := range map[string]store.ContactInput{
		"day alone":     {HejmoName: "A", BdayDay: 14},
		"31 February":   {HejmoName: "B", BdayDay: 31, BdayMonth: 2, BdayYear: 2018},
		"far past":      {HejmoName: "C", BdayDay: 1, BdayMonth: 1, BdayYear: 1850},
		"future":        {HejmoName: "D", BdayDay: 1, BdayMonth: 1, BdayYear: 2999},
		"leap day 2025": {HejmoName: "E", BdayDay: 29, BdayMonth: 2, BdayYear: 2025},
	} {
		var fe *store.FieldError
		if _, err := db.CreateContact(h.ID, "", in); !errors.As(err, &fe) || fe.Field != "birthday" {
			t.Errorf("%s: err = %v, want a birthday error", name, err)
		}
	}
}

func TestTheHejmoNameIsUniqueIgnoringCaseAmongContactsAndPeople(t *testing.T) {
	t.Parallel()
	db, h, _ := exampleFamily(t)
	var taken *store.NameTakenError
	_, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "mia"})
	if !errors.As(err, &taken) || taken.Existing != "Mia" || !taken.ExistingIsPerson || !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("a person's name: err = %v", err)
	}
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Kinderarzt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "KINDERARZT"}); !errors.As(err, &taken) || taken.ExistingIsPerson {
		t.Fatalf("a contact's name: err = %v", err)
	}
	var fe *store.FieldError
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "  "}); !errors.As(err, &fe) || fe.Field != "name" {
		t.Errorf("an empty name: %v", err)
	}
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: strings.Repeat("x", 41)}); !errors.As(err, &fe) {
		t.Errorf("41 characters: %v", err)
	}
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Zehn", Numbers: make([]contact.Line, 0)}); err != nil {
		t.Fatal(err)
	}
	var lines []contact.Line
	for i := 0; i < 11; i++ {
		lines = append(lines, contact.Line{Kind: contact.Cell, Value: "0171 000"})
	}
	if _, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Elf", Numbers: lines}); !errors.As(err, &fe) || fe.Field != "numbers" {
		t.Errorf("11 numbers: %v", err)
	}
}

func TestAPersonsContactKeepsItsNameAndFamilieAndCannotBeDeleted(t *testing.T) {
	t.Parallel()
	db, h, f := exampleFamily(t)
	fam := groupKey(t, db, h, "familie")
	c, err := db.UpdateContact(h.ID, f["Mia"].ID, f["Mama"].ID, store.ContactInput{HejmoName: "Etwas anderes", FullName: "Mia Maier", Numbers: []contact.Line{{Kind: contact.Cell, Value: "0151 1234567"}}})
	if err != nil || c.HejmoName != "Mia" || c.FullName != "Mia Maier" || len(c.GroupIDs) != 1 || c.GroupIDs[0] != fam.ID {
		t.Fatalf("contact = %+v, %v", c, err)
	}
	if err := db.DeleteContact(h.ID, f["Mia"].ID); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("deleting a person's contact: %v", err)
	}
}

func TestOwnGroupsAreMadeRenamedAndDeletedWhileTheirContactsStay(t *testing.T) {
	t.Parallel()
	db, h, f := exampleFamily(t)
	c, err := db.CreateContact(h.ID, f["Papa"].ID, store.ContactInput{HejmoName: "Anna (Mama von Lukas)", NewGroup: "Fußball-Eltern U11"})
	if err != nil || len(c.GroupIDs) != 1 {
		t.Fatalf("contact = %+v, %v", c, err)
	}
	g, err := db.ContactGroup(h.ID, c.GroupIDs[0])
	if err != nil || g.Name != "Fußball-Eltern U11" || g.ShippedKey != "" {
		t.Fatalf("group = %+v, %v", g, err)
	}
	var fe *store.FieldError
	if _, err := db.CreateContactGroup(h.ID, "", "fußball-eltern u11"); !errors.As(err, &fe) || fe.Field != "new_group" {
		t.Errorf("duplicate group name: %v", err)
	}
	if err := db.RenameContactGroup(h.ID, g.ID, "Fußball U12"); err != nil {
		t.Fatal(err)
	}
	fam := groupKey(t, db, h, "familie")
	if err := db.RenameContactGroup(h.ID, fam.ID, "X"); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("renaming a group of Hejmo: %v", err)
	}
	if err := db.DeleteContactGroup(h.ID, fam.ID); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("deleting a group of Hejmo: %v", err)
	}
	if err := db.DeleteContactGroup(h.ID, g.ID); err != nil {
		t.Fatal(err)
	}
	if c, err = db.Contact(h.ID, c.ID); err != nil || len(c.GroupIDs) != 0 {
		t.Errorf("after deleting the group: %+v, %v", c, err)
	}
	if err := db.DeleteContact(h.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Contact(h.ID, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted contact: %v", err)
	}
}
