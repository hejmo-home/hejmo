package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/carddav"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 299 (ADR 0021): the box as CardDAV server. The tests talk to it with the CardDAV client of go-webdav
// and with raw requests, as iOS Contacts and DAVx5 do; no real account is ever used.

type addressDevice struct {
	*personalDevice
	cc *carddav.Client
}

func (c *apiClient) addressDevice(t *testing.T, scopes string) *addressDevice {
	t.Helper()
	secret := c.newToken(scopes)
	hc := webdav.HTTPClientWithBasicAuth(c.b.bareClient(), "mama", secret)
	cc, err := carddav.NewClient(hc, c.b.srv.URL+"/carddav/")
	if err != nil {
		t.Fatal(err)
	}
	return &addressDevice{&personalDevice{t, c.b, "mama", secret, nil}, cc}
}

// book discovers the address book the way a phone does: principal, home set, address books.
func (d *addressDevice) book() (carddav.AddressBook, error) {
	ctx := context.Background()
	principal, err := d.cc.FindCurrentUserPrincipal(ctx)
	if err != nil {
		return carddav.AddressBook{}, err
	}
	home, err := d.cc.FindAddressBookHomeSet(ctx, principal)
	if err != nil {
		return carddav.AddressBook{}, err
	}
	books, err := d.cc.FindAddressBooks(ctx, home)
	if err != nil {
		return carddav.AddressBook{}, err
	}
	if len(books) != 1 {
		return carddav.AddressBook{}, fmt.Errorf("%d address books", len(books))
	}
	return books[0], nil
}

func (d *addressDevice) objects(path string) map[string]carddav.AddressObject {
	d.t.Helper()
	objs, err := d.cc.QueryAddressBook(context.Background(), path, &carddav.AddressBookQuery{DataRequest: carddav.AddressDataRequest{AllProp: true}})
	if err != nil {
		d.t.Fatalf("query %s: %v", path, err)
	}
	out := map[string]carddav.AddressObject{}
	for _, o := range objs {
		out[o.Card.PreferredValue(vcard.FieldNickname)] = o
	}
	return out
}

func TestWellKnownCardDAVPointsToTheServer(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	if r := b.get("/.well-known/carddav"); r.status != http.StatusMovedPermanently && r.status != http.StatusPermanentRedirect || r.location != "/carddav/" {
		t.Fatalf("/.well-known/carddav = %d %q", r.status, r.location)
	}
}

func TestAPhoneFindsHejmoAsItsOwnAddressBookWithTheHouseholdsPeople(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h := mustHousehold(t, c)
	c.call(http.MethodPost, "/people", map[string]any{"name": "Mia", "role": "child"})
	if _, err := c.b.db.CreateContact(h.ID, "", store.ContactInput{HejmoName: "Oma Gerda", FullName: "Gerda Müller",
		Numbers: []contact.Line{{Kind: contact.Cell, Value: "+49 170 123"}}}); err != nil {
		t.Fatal(err)
	}
	d := c.addressDevice(t, "contacts")
	book, err := d.book()
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if book.Name != "Hejmo" {
		t.Errorf("name = %q", book.Name)
	}
	got := d.objects(book.Path)
	for _, name := range []string{"Mama", "Mia", "Oma Gerda"} {
		if _, ok := got[name]; !ok {
			t.Fatalf("%q is not in the address book: %v", name, got)
		}
	}
	oma := got["Oma Gerda"].Card
	if oma.PreferredValue(vcard.FieldFormattedName) != "Gerda Müller" || oma.PreferredValue(vcard.FieldTelephone) != "+49 170 123" {
		t.Errorf("card = %v", oma)
	}
}

// Only the contacts scope opens it, and only for parents with a device token.
func TestOnlyContactScopesOpenCardDAV(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	for _, scope := range []string{"read", "write", "calendars", "tasks"} {
		d := c.addressDevice(t, scope)
		if status, _, _ := d.raw("PROPFIND", "/carddav/", "", map[string]string{"Depth": "0"}); status != http.StatusForbidden {
			t.Fatalf("token %q on CardDAV = %d, want 403", scope, status)
		}
	}
	d := c.addressDevice(t, "contacts:read")
	if status, _, _ := d.raw("PROPFIND", "/carddav/", "", map[string]string{"Depth": "0"}); status != http.StatusMultiStatus {
		t.Fatalf("contacts:read = %d", status)
	}
	if status, _, _ := d.raw("PUT", "/carddav/x/contacts/hejmo/new.vcf", "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\nEND:VCARD\r\n", map[string]string{"Content-Type": "text/vcard"}); status != http.StatusForbidden {
		t.Fatalf("write with a read token = %d", status)
	}
	// CardDAV scopes do not open CalDAV, and the other way round.
	if status, _, _ := d.raw("PROPFIND", "/dav/", "", map[string]string{"Depth": "0"}); status != http.StatusForbidden {
		t.Fatalf("contacts token on CalDAV = %d", status)
	}
	// A wrong password and no password are asked for again.
	if status, hdr, _ := (&personalDevice{t, c.b, "mama", "hejmo_wrong", nil}).raw("PROPFIND", "/carddav/", "", nil); status != http.StatusUnauthorized || hdr.Get("WWW-Authenticate") == "" {
		t.Fatalf("wrong token = %d", status)
	}
}
