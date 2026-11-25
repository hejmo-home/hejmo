package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"image/jpeg"
	"net/http"
	"net/url"
	"regexp"
	"strings"
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
	return c.addressDeviceWith(t, c.newToken(scopes))
}

func (c *apiClient) addressDeviceWith(t *testing.T, secret string) *addressDevice {
	t.Helper()
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

const vcardType = "text/vcard; charset=utf-8"

func vcardText(lines ...string) string {
	return "BEGIN:VCARD\r\nVERSION:3.0\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VCARD\r\n"
}

// put saves a card the way iOS Contacts does: a new one with If-None-Match, a changed one with If-Match.
func (d *addressDevice) put(path, body string, header map[string]string) (int, http.Header, string) {
	d.t.Helper()
	h := map[string]string{"Content-Type": vcardType}
	for k, v := range header {
		h[k] = v
	}
	return d.raw("PUT", path, body, h)
}

func (d *addressDevice) contactOnBox(c *apiClient, id string) store.Contact {
	d.t.Helper()
	got, err := c.b.db.Contact(mustHousehold(d.t, c).ID, id)
	if err != nil {
		d.t.Fatalf("contact %s: %v", id, err)
	}
	return got
}

func TestAPhoneCreatesChangesAndDeletesAFurtherContact(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	d := c.addressDevice(t, "contacts")
	book, err := d.book()
	if err != nil {
		t.Fatal(err)
	}
	path := book.Path + "1B2D-CAFE.vcf"
	body := vcardText("FN:Gerda Müller", "N:Müller;Gerda;;;", "NICKNAME:Oma Gerda", "item1.TEL;type=pref:+49 89 123", "item1.X-ABLabel:Oma Festnetz",
		"EMAIL;type=INTERNET;type=HOME:oma@example.org", "ORG:Acme", "NOTE:Hallo")
	status, hdr, _ := d.put(path, body, map[string]string{"If-None-Match": "*"})
	if status != http.StatusCreated || hdr.Get("ETag") == "" {
		t.Fatalf("create = %d %v", status, hdr)
	}
	got := d.contactOnBox(c, "1B2D-CAFE")
	if got.HejmoName != "Oma Gerda" || got.FullName != "Gerda Müller" || got.Note != "Hallo" ||
		len(got.Numbers) != 1 || got.Numbers[0].Kind != contact.Custom || got.Numbers[0].Label != "Oma Festnetz" || got.Numbers[0].Value != "+49 89 123" ||
		len(got.Emails) != 1 || got.Emails[0].Kind != contact.Home || got.CreatedBy == "" {
		t.Fatalf("stored = %+v", got)
	}
	// Creating it again with If-None-Match is a conflict; a changed card needs the ETag it was read with.
	if status, _, _ := d.put(path, body, map[string]string{"If-None-Match": "*"}); status != http.StatusPreconditionFailed {
		t.Fatalf("second create = %d", status)
	}
	if status, _, _ := d.put(path, body, map[string]string{"If-Match": `"stale"`}); status != http.StatusPreconditionFailed {
		t.Fatalf("stale = %d", status)
	}
	etag := hdr.Get("ETag")
	changed := strings.Replace(body, "+49 89 123", "+49 89 999", 1)
	status, hdr2, _ := d.put(path, changed, map[string]string{"If-Match": etag})
	if status != http.StatusCreated && status != http.StatusNoContent {
		t.Fatalf("change = %d", status)
	}
	if got = d.contactOnBox(c, "1B2D-CAFE"); got.Numbers[0].Value != "+49 89 999" || hdr2.Get("ETag") == etag {
		t.Fatalf("after the change: %+v etag %q", got.Numbers, hdr2.Get("ETag"))
	}
	// What the phone saved that Hejmo does not model comes back.
	_, _, text := d.raw("GET", path, "", nil)
	if !strings.Contains(text, "ORG:Acme") {
		t.Fatalf("the organisation is lost:\n%s", text)
	}
	if status, _, _ := d.raw("DELETE", path, "", map[string]string{"If-Match": hdr2.Get("ETag")}); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if _, err := c.b.db.Contact(mustHousehold(t, c).ID, "1B2D-CAFE"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
}

func TestAHouseholdPersonChangesOnlyPhoneAddressAndPhotoFromAPhone(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h := mustHousehold(t, c)
	people, _ := c.b.db.People(h.ID)
	mama := people[0]
	if _, err := c.b.db.UpdateContact(h.ID, mama.ID, mama.ID, store.ContactInput{FullName: "Anna Muster", BdayDay: 1, BdayMonth: 2, BdayYear: 1985,
		Emails: []contact.Line{{Kind: contact.Home, Value: "mama@example.org"}}}); err != nil {
		t.Fatal(err)
	}
	d := c.addressDevice(t, "contacts")
	book, _ := d.book()
	path := book.Path + mama.ID + ".vcf"
	_, hdr, _ := d.raw("GET", path, "", nil)
	status, _, _ := d.put(path, vcardText("FN:Anja Neu", "NICKNAME:Mamma", "TEL;type=CELL:0170 55", "EMAIL;type=HOME:evil@example.org", "BDAY:1990-01-01",
		"ADR;type=HOME:;;Weg 1;Pasing;;80689;DE"), map[string]string{"If-Match": hdr.Get("ETag")})
	if status >= 300 {
		t.Fatalf("put = %d", status)
	}
	got := d.contactOnBox(c, mama.ID)
	if len(got.Numbers) != 1 || got.Numbers[0].Value != "0170 55" || got.Street != "Weg 1" || got.City != "Pasing" || got.Postcode != "80689" {
		t.Errorf("phone and address come from the phone: %+v", got)
	}
	if got.HejmoName != mama.Name || got.FullName != "Anna Muster" || len(got.Emails) != 1 || got.Emails[0].Value != "mama@example.org" || got.BdayYear != 1985 {
		t.Errorf("identity data changed through sync: %+v", got)
	}
	if status, _, _ := d.raw("DELETE", path, "", nil); status != http.StatusForbidden {
		t.Fatalf("deleting a person = %d, want 403", status)
	}
}

func TestAnUnchangedContactKeepsItsETag(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	d := c.addressDevice(t, "contacts")
	book, _ := d.book()
	path := book.Path + "same.vcf"
	_, hdr, _ := d.put(path, vcardText("FN:Opa", "TEL;type=CELL:1", "EMAIL;type=HOME:", "TEL:", "NOTE:", "ADR:;;;;;;"), nil)
	etag := hdr.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	got := d.contactOnBox(c, "same")
	if len(got.Numbers) != 1 || len(got.Emails) != 0 || got.Street != "" || got.Note != "" {
		t.Fatalf("empty values were stored: %+v", got)
	}
	// What the box sent, sent back, and the same again.
	_, hdr, text := d.raw("GET", path, "", nil)
	if hdr.Get("ETag") != etag {
		t.Fatalf("ETag %q after reading, %q before", hdr.Get("ETag"), etag)
	}
	before := d.contactOnBox(c, "same").UpdatedAt
	for i := 0; i < 2; i++ {
		status, hdr, _ := d.put(path, text, map[string]string{"If-Match": etag})
		if status >= 300 || hdr.Get("ETag") != etag {
			t.Fatalf("saving it again = %d, ETag %q, want %q", status, hdr.Get("ETag"), etag)
		}
	}
	if after := d.contactOnBox(c, "same").UpdatedAt; !after.Equal(before) {
		t.Fatalf("an unchanged card was written: %v -> %v", before, after)
	}
	// A change in Hejmo changes it.
	h := mustHousehold(t, c)
	if _, err := c.b.db.UpdateContact(h.ID, "same", "", store.ContactInput{HejmoName: "Opa", Note: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, hdr, _ = d.raw("GET", path, "", nil); hdr.Get("ETag") == etag {
		t.Fatal("a changed contact kept its ETag")
	}
}

func TestBirthdaysOverCardDAV(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h := mustHousehold(t, c)
	d := c.addressDevice(t, "contacts")
	book, _ := d.book()
	cases := []struct {
		line          string
		day, mo, year int
	}{
		{"BDAY;X-APPLE-OMIT-YEAR=1604:1604-03-14", 14, 3, 0},
		{"BDAY:--0314", 14, 3, 0},
		{"BDAY:1985-03-14T23:30:00Z", 14, 3, 1985},
	}
	for i, tc := range cases {
		id := "bd" + string(rune('a'+i))
		if status, _, _ := d.put(book.Path+id+".vcf", vcardText("FN:Person "+id, tc.line), nil); status >= 300 {
			t.Fatalf("%s: put = %d", tc.line, status)
		}
		if got := d.contactOnBox(c, id); got.BdayDay != tc.day || got.BdayMonth != tc.mo || got.BdayYear != tc.year {
			t.Errorf("%s stored %d.%d.%d", tc.line, got.BdayDay, got.BdayMonth, got.BdayYear)
		}
	}
	// Out: 3.0 by default, 4.0 where the phone asks for it.
	if _, err := c.b.db.CreateContact(h.ID, "", store.ContactInput{ID: "out", HejmoName: "Out", BdayDay: 5, BdayMonth: 6}); err != nil {
		t.Fatal(err)
	}
	_, _, v3 := d.raw("GET", book.Path+"out.vcf", "", nil)
	if !strings.Contains(v3, "VERSION:3.0") || !strings.Contains(v3, "BDAY;X-APPLE-OMIT-YEAR=1604:1604-06-05") {
		t.Errorf("3.0:\n%s", v3)
	}
	_, _, v4 := d.raw("GET", book.Path+"out.vcf", "", map[string]string{"Accept": "text/vcard; version=4.0"})
	if !strings.Contains(v4, "VERSION:4.0") || !strings.Contains(v4, "BDAY:--0605") {
		t.Errorf("4.0 by Accept:\n%s", v4)
	}
	report := `<?xml version="1.0"?><C:addressbook-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav"><D:prop><D:getetag/><C:address-data content-type="text/vcard" version="4.0"/></D:prop><D:href>` + book.Path + `out.vcf</D:href></C:addressbook-multiget>`
	_, _, ms := d.raw("REPORT", book.Path, report, map[string]string{"Content-Type": "application/xml", "Depth": "1"})
	if !strings.Contains(ms, "VERSION:4.0") || !strings.Contains(ms, "BDAY:--0605") {
		t.Errorf("4.0 by REPORT:\n%s", ms)
	}
	// A 4.0 card is read as well.
	body := "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Vier\r\nBDAY:--1224\r\nTEL;VALUE=uri;TYPE=work:tel:+49-89-1\r\nEND:VCARD\r\n"
	if status, _, _ := d.put(book.Path+"four.vcf", body, nil); status >= 300 {
		t.Fatalf("4.0 put = %d", status)
	}
	if got := d.contactOnBox(c, "four"); got.BdayDay != 24 || got.BdayMonth != 12 || got.BdayYear != 0 || got.Numbers[0].Kind != contact.Work || got.Numbers[0].Value != "+49-89-1" {
		t.Errorf("4.0 stored %+v", got)
	}
}

// Product owner, 23/11/2026 (ticket 297): a photo from a phone is the contact's photo; one added in Hejmo goes
// to the phone.
func TestPhotosGoBothWays(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h := mustHousehold(t, c)
	d := c.addressDevice(t, "contacts")
	book, _ := d.book()
	path := book.Path + "pic.vcf"
	b64 := base64.StdEncoding.EncodeToString(testJPEG(t, 800, 600))
	status, hdr, _ := d.put(path, vcardText("FN:Mit Foto", "PHOTO;ENCODING=b;TYPE=JPEG:"+b64), nil)
	if status >= 300 {
		t.Fatalf("put = %d", status)
	}
	got := d.contactOnBox(c, "pic")
	if !got.HasPhoto || got.PhotoOrigin != store.PhotoSync || got.PhotoDevice != "test" {
		t.Fatalf("photo from the phone: %+v", got)
	}
	ph, err := c.b.db.ContactPhoto(h.ID, "pic")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(ph.JPEG))
	if err != nil || cfg.Width != 512 || cfg.Height != 512 {
		t.Fatalf("the photo is made under the picture rules: %v %+v", err, cfg)
	}
	// Read back, saved again: the photo and the ETag stay.
	_, hdr2, text := d.raw("GET", path, "", nil)
	if hdr2.Get("ETag") != hdr.Get("ETag") || !strings.Contains(text, "PHOTO;") {
		t.Fatalf("the card does not carry the photo or changed its ETag:\n%s", text)
	}
	before := d.contactOnBox(c, "pic")
	if status, hdr3, _ := d.put(path, text, map[string]string{"If-Match": hdr2.Get("ETag")}); status >= 300 || hdr3.Get("ETag") != hdr2.Get("ETag") {
		t.Fatalf("same photo again = %d %q", status, hdr3.Get("ETag"))
	}
	if after := d.contactOnBox(c, "pic"); !after.PhotoAt.Equal(before.PhotoAt) {
		t.Fatal("the unchanged photo was stored again")
	}
	// A photo set in Hejmo is sent to the phone; removing it on the phone removes it.
	if _, err := c.b.db.SetContactPhoto(h.ID, "pic", "", store.SetPicture, &store.ContactPhoto{JPEG: testJPEG(t, 64, 96)}); err != nil {
		t.Fatal(err)
	}
	_, hdr4, text4 := d.raw("GET", path, "", nil)
	if hdr4.Get("ETag") == hdr2.Get("ETag") || !strings.Contains(text4, "PHOTO;") {
		t.Fatalf("a new photo did not reach the card:\n%s", text4)
	}
	if status, _, _ := d.put(path, vcardText("FN:Mit Foto"), map[string]string{"If-Match": hdr4.Get("ETag")}); status >= 300 {
		t.Fatalf("put = %d", status)
	}
	if d.contactOnBox(c, "pic").HasPhoto {
		t.Fatal("the photo stayed after the phone removed it")
	}
	// A photo that is only a link is never fetched and changes nothing; a file that is no picture is ignored.
	if _, err := c.b.db.SetContactPhoto(h.ID, "pic", "", store.SetPicture, &store.ContactPhoto{JPEG: testJPEG(t, 64, 64)}); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := d.put(path, vcardText("FN:Mit Foto", "PHOTO;VALUE=uri:https://example.org/p.jpg"), nil); status >= 300 || !d.contactOnBox(c, "pic").HasPhoto {
		t.Fatalf("a photo link removed the photo: %d", status)
	}
	if status, _, _ := d.put(path, vcardText("FN:Mit Foto", "PHOTO;ENCODING=b;TYPE=JPEG:"+base64.StdEncoding.EncodeToString([]byte("not a picture"))), nil); status >= 300 {
		t.Fatalf("a file that is no picture refused the card: %d", status)
	}
}

func TestACardNeedsSomethingAndANameThatIsTakenIsChanged(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	d := c.addressDevice(t, "contacts")
	book, _ := d.book()
	if status, _, _ := d.put(book.Path+"empty.vcf", vcardText("FN:"), nil); status != http.StatusBadRequest {
		t.Fatalf("empty card = %d", status)
	}
	if status, _, _ := d.put(book.Path+"a%20b.vcf", vcardText("FN:X"), nil); status != http.StatusBadRequest {
		t.Fatalf("bad name = %d", status)
	}
	if status, _, _ := d.put(book.Path+"mamas.vcf", vcardText("FN:Mama"), nil); status >= 300 {
		t.Fatalf("a name a person has = %d", status)
	}
	if got := d.contactOnBox(c, "mamas"); got.HejmoName == "Mama" || got.HejmoName == "" {
		t.Fatalf("name = %q", got.HejmoName)
	}
	if status, _, _ := d.put(book.Path+"big.vcf", vcardText("FN:Big", "NOTE:"+strings.Repeat("x", 9<<20)), nil); status != http.StatusRequestEntityTooLarge && status != http.StatusBadRequest {
		t.Fatalf("a card of 9 MB = %d", status)
	}
}

func TestLabelsComeBackAsTheyWentOut(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	d := c.addressDevice(t, "contacts")
	book, _ := d.book()
	path := book.Path + "labels.vcf"
	in := vcardText("FN:Opa", "item1.TEL:111", "item1.X-ABLabel:Opa Handy", "item2.TEL:222", "item2.X-ABLabel:_$!<HomeFAX>!$_",
		"TEL;type=CELL:333", "TEL;type=WORK:444", "item3.EMAIL:o@example.org", "item3.X-ABLabel:Verein")
	if status, _, _ := d.put(path, in, nil); status >= 300 {
		t.Fatalf("put = %d", status)
	}
	_, _, out := d.raw("GET", path, "", nil)
	got, err := vcard.NewDecoder(strings.NewReader(out)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	labelOf := func(f *vcard.Field) string {
		for _, l := range got["X-ABLABEL"] {
			if l.Group == f.Group && f.Group != "" {
				return l.Value
			}
		}
		return strings.Join(f.Params["TYPE"], ",")
	}
	var seen []string
	for _, f := range got["TEL"] {
		seen = append(seen, f.Value+"="+labelOf(f))
	}
	for _, f := range got["EMAIL"] {
		seen = append(seen, f.Value+"="+labelOf(f))
	}
	want := []string{"111=Opa Handy", "222=_$!<HomeFAX>!$_", "333=CELL", "444=WORK", "o@example.org=Verein"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("labels %v, want %v\n%s", seen, want, out)
	}
}

// The web page and the answer that shows a new token say where the address book is.
func TestTheDevicePagesNameTheAddressOfTheAddressBook(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	page := c.b.do(http.MethodGet, "/caldav", nil, map[string]string{"Accept-Language": "en"})
	if page.status != http.StatusOK || !strings.Contains(page.body, "Address of the address book (CardDAV)") || !strings.Contains(page.body, "/carddav/") {
		t.Fatalf("/caldav = %d\n%s", page.status, page.body)
	}
	created := c.b.do(http.MethodPost, "/caldav/tokens", url.Values{"name": {"Pixel"}}, map[string]string{"Accept-Language": "de"})
	if created.status != http.StatusOK || !strings.Contains(created.body, "Adresse des Adressbuchs (CardDAV)") || !strings.Contains(created.body, "/carddav/") {
		t.Fatalf("new token = %d\n%s", created.status, created.body)
	}
}

// A sync as iOS Contacts and DAVx5 do it: list the ETags (PROPFIND, depth 1), fetch what is new (multiget), save
// a change with If-Match, list again and see only that ETag move.
func TestASyncRoundListsEtagsFetchesAndSeesOnlyTheChangedCard(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	h := mustHousehold(t, c)
	for _, n := range []string{"Opa", "Oma"} {
		if _, err := c.b.db.CreateContact(h.ID, "", store.ContactInput{HejmoName: n}); err != nil {
			t.Fatal(err)
		}
	}
	d := c.addressDevice(t, "contacts")
	book, _ := d.book()
	etags := func() map[string]string {
		_, _, ms := d.raw("PROPFIND", book.Path, `<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:prop><D:getetag/></D:prop></D:propfind>`, map[string]string{"Depth": "1", "Content-Type": "application/xml"})
		out := map[string]string{}
		for _, m := range regexp.MustCompile(`(?s)<[^>]*href>([^<]*\.vcf)</[^>]*href>.*?<[^>/]*getetag[^>]*>([^<]*)<`).FindAllStringSubmatch(ms, -1) {
			out[m[1]] = html.UnescapeString(m[2])
		}
		return out
	}
	first := etags()
	if len(first) != 3 { // Mama, Opa, Oma
		t.Fatalf("etags = %v", first)
	}
	var paths []string
	for p := range first {
		paths = append(paths, p)
	}
	got, err := d.cc.MultiGetAddressBook(context.Background(), book.Path, &carddav.AddressBookMultiGet{Paths: paths, DataRequest: carddav.AddressDataRequest{AllProp: true}})
	if err != nil || len(got) != 3 {
		t.Fatalf("multiget = %d, %v", len(got), err)
	}
	var opa string
	for _, o := range got {
		if o.Card.PreferredValue(vcard.FieldNickname) == "Opa" {
			opa = o.Path
		}
	}
	if status, _, _ := d.put(opa, vcardText("FN:Opa", "NICKNAME:Opa", "TEL;type=CELL:12"), map[string]string{"If-Match": first[opa]}); status >= 300 {
		t.Fatalf("put = %d", status)
	}
	second := etags()
	for p, e := range second {
		if (p == opa) == (e == first[p]) {
			t.Errorf("%s: etag %q -> %q", p, first[p], e)
		}
	}
}
