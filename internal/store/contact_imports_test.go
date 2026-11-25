package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 300: contact files wait in the store for the parent's yes, and what is taken over says where it came from.

func TestANumberIsKeptAsTypedWithItsE164FormBesideItReadWithTheHomeCountry(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	h, err := db.CreateHousehold(store.Household{Name: "Familie", Region: "DE-BY"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Oma Gerda", Numbers: []contact.Line{
		{Kind: contact.Cell, Value: "0171 3456 789"}, {Kind: contact.Other, Value: "116117"}, {Kind: contact.Home, Value: "+43 664 1234567"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{{"0171 3456 789", "+491713456789"}, {"116117", ""}, {"+43 664 1234567", "+436641234567"}}
	for i, n := range c.Numbers {
		if n.Value != want[i][0] || n.E164 != want[i][1] {
			t.Errorf("number %d = %q / %q, want %q / %q", i+1, n.Value, n.E164, want[i][0], want[i][1])
		}
	}
	if len(c.Emails) != 0 {
		t.Error("e-mail addresses")
	}
}

func importBox(t *testing.T) (*store.DB, store.Household, map[string]store.Person) {
	t.Helper()
	return exampleFamily(t)
}

func TestAnUploadedFileIsOpenedByItsUploaderAndExpiresAfterAnHourKeepingOnlyANote(t *testing.T) {
	t.Parallel()
	db, h, f := importBox(t)
	t0 := time.Date(2026, 11, 23, 20, 0, 0, 0, time.UTC)
	imp, err := db.AddContactImport(h.ID, f["Papa"].ID, "Kontakte Papa.vcf", []byte("BEGIN:VCARD\r\nEND:VCARD\r\n"), map[int][]byte{2: []byte("jpeg")}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if imp.UploadedBy != f["Papa"].ID || imp.UploadedByName != "Papa" || imp.FileName != "Kontakte Papa.vcf" || !imp.UploadedAt.Equal(t0) || imp.Expired {
		t.Fatalf("import = %+v", imp)
	}
	if photos, err := db.ContactImportPhotos(imp.ID); err != nil || string(photos[2]) != "jpeg" || len(photos) != 1 {
		t.Fatalf("photos = %v, %v", photos, err)
	}
	if _, err := db.ContactImport("other-household", imp.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another household opens it: %v", err)
	}
	if err := db.ExpireContactImports(t0.Add(-time.Hour), t0); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.ContactImport(h.ID, imp.ID); got.Expired || len(got.Data) == 0 {
		t.Error("a fresh upload expired")
	}
	if err := db.ExpireContactImports(t0, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := db.ContactImport(h.ID, imp.ID)
	if err != nil || !got.Expired || len(got.Data) != 0 || got.FileName != "Kontakte Papa.vcf" {
		t.Fatalf("after an hour = %+v, %v: the file is gone, the name stays for the page", got, err)
	}
	if photos, _ := db.ContactImportPhotos(imp.ID); len(photos) != 0 {
		t.Error("the photos of an expired file stay")
	}
	if err := db.ExpireContactImports(t0, t0.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ContactImport(h.ID, imp.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the note of an expired file stays for ever: %v", err)
	}
}

func TestApplyingAnImportMakesAndCompletesContactsInOneStepAndDeletesTheFile(t *testing.T) {
	t.Parallel()
	db, h, f := importBox(t)
	oma, err := db.CreateContact(h.ID, f["Mama"].ID, store.ContactInput{HejmoName: "Oma Gerda", FullName: "Gerda Müller",
		Numbers:   []contact.Line{{Kind: contact.Cell, Value: "0171 3456 789"}},
		Relations: []store.Relation{{Word: "Oma", PersonIDs: []string{f["Mia"].ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	imp, _ := db.AddContactImport(h.ID, f["Papa"].ID, "Kontakte Papa.vcf", []byte("x"), nil, time.Now())
	merged := store.ContactInput{HejmoName: "Oma Gerda", FullName: "Gerda Müller", Relations: oma.Relations, GroupIDs: oma.GroupIDs, ImportFile: "Kontakte Papa.vcf",
		Numbers:     []contact.Line{{Kind: contact.Cell, Value: "0171 3456 789"}, {Kind: contact.Work, Value: "08031 40 12 77"}},
		PhotoChange: store.SetPicture, Photo: &store.ContactPhoto{JPEG: []byte("jpeg"), File: "Kontakte Papa.vcf"}}
	fresh := store.ContactInput{HejmoName: "Familie Nguyễn", ImportFile: "Kontakte Papa.vcf", Numbers: []contact.Line{{Kind: contact.Home, Value: "089 5566 1234"}}}
	ids, err := db.ApplyContactImport(h.ID, imp.ID, f["Papa"].ID, []store.ImportOp{{MergeInto: oma.ID, Input: merged}, {Input: fresh}})
	if err != nil || len(ids) != 2 || ids[0] != oma.ID {
		t.Fatalf("apply = %v, %v", ids, err)
	}
	got, _ := db.Contact(h.ID, oma.ID)
	if len(got.Numbers) != 2 || got.UpdatedFile != "Kontakte Papa.vcf" || got.UpdatedByName != "Papa" || got.PhotoFile != "Kontakte Papa.vcf" || !got.HasPhoto || len(got.Relations) != 1 || got.CreatedFile != "" {
		t.Errorf("completed contact = %+v", got)
	}
	nu, _ := db.Contact(h.ID, ids[1])
	if nu.CreatedFile != "Kontakte Papa.vcf" || nu.CreatedByName != "Papa" || nu.UpdatedFile != "" {
		t.Errorf("new contact = %+v", nu)
	}
	if _, err := db.ContactImport(h.ID, imp.ID); !errors.Is(err, store.ErrNotFound) {
		t.Error("the file stays after the import")
	}
	// A change by hand later forgets that the contact was completed from the file.
	if got, err = db.UpdateContact(h.ID, oma.ID, f["Mama"].ID, store.ContactInput{HejmoName: "Oma Gerda", FullName: "Gerda Müller", Numbers: got.Numbers, Relations: got.Relations}); err != nil || got.UpdatedFile != "" || got.PhotoFile != "Kontakte Papa.vcf" {
		t.Errorf("after a change by hand = %+v, %v", got, err)
	}
}

func TestAnImportThatFailsChangesNothingAndKeepsTheFile(t *testing.T) {
	t.Parallel()
	db, h, f := importBox(t)
	imp, _ := db.AddContactImport(h.ID, f["Papa"].ID, "a.vcf", []byte("x"), nil, time.Now())
	ops := []store.ImportOp{
		{Input: store.ContactInput{HejmoName: "Neu", ImportFile: "a.vcf"}},
		{Input: store.ContactInput{HejmoName: "mama", ImportFile: "a.vcf"}}, // a name that is taken
	}
	if _, err := db.ApplyContactImport(h.ID, imp.ID, f["Papa"].ID, ops); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("err = %v, want a taken name", err)
	}
	if got := len(contactNames(t, db, h)); got != 4 {
		t.Errorf("%d contacts after a failed import, want 4", got)
	}
	if _, err := db.ContactImport(h.ID, imp.ID); err != nil {
		t.Errorf("the file is gone after a failed import: %v", err)
	}
}
