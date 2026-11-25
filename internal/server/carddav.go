package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/emersion/go-vcard"
	"github.com/emersion/go-webdav"
	webcard "github.com/emersion/go-webdav/carddav"

	"github.com/hejmo-home/hejmo/internal/contactsync"
	"github.com/hejmo-home/hejmo/internal/picture"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The box as CardDAV server (ADR 0021, ticket 299): parents' phones sync the address book with a device token
// of the area contacts. Hejmo is one address book of its own. Addresses:
//
//	/carddav/                                  root, names the signed-in person's principal
//	/carddav/{person}/                         principal
//	/carddav/{person}/contacts/                address book home set
//	/carddav/{person}/contacts/hejmo/          the address book
//	/carddav/{person}/contacts/hejmo/{id}.vcf  one contact
//
// Cards go out in vCard 3.0, in 4.0 where the phone asks for it (address-data version="4.0" or an Accept header
// with version=4.0). Both are read.
const (
	cardPrefix   = "/carddav"
	cardBookName = "hejmo"
	// maxCardBytes is the largest card (a photo included) the box takes from a phone.
	maxCardBytes = 8 << 20
)

type cardSession struct {
	login   davLogin
	req     *http.Request
	version contactsync.Version
}

type cardKey struct{}

func (s *server) routeCardDAV(mux *http.ServeMux) {
	backend := &cardBackend{s: s}
	handler := &webcard.Handler{Backend: backend, Prefix: cardPrefix}
	mux.Handle(cardPrefix+"/", s.cardAuth(handler))
	redirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, cardPrefix+"/", http.StatusPermanentRedirect)
	}
	mux.HandleFunc(cardPrefix, redirect)
	mux.HandleFunc("/.well-known/carddav", redirect)
}

var wantsV4 = regexp.MustCompile(`(?i)address-data\b[^>]*\bversion\s*=\s*["']4\.0["']`)

// cardAuth signs a parent's device in (username and a token with scope contacts) and finds out which vCard
// version the phone asks for.
func (s *server) cardAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l, ok := s.davSignIn(w, r, cardPrefix, "contacts")
		if !ok {
			return
		}
		sess := &cardSession{login: l, req: r, version: contactsync.V3}
		if strings.Contains(strings.ReplaceAll(r.Header.Get("Accept"), " ", ""), "version=4.0") {
			sess.version = contactsync.V4
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxCardBytes)
		if r.Method == "PROPFIND" || r.Method == "REPORT" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
				return
			}
			if wantsV4.Match(body) {
				sess.version = contactsync.V4
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), cardKey{}, sess)))
	})
}

// cardBackend serves the household's contacts to go-webdav's CardDAV handler.
type cardBackend struct {
	s  *server
	mu sync.Mutex // one CardDAV write at a time; edits through the web interface are not covered
}

func (b *cardBackend) session(ctx context.Context) (*cardSession, error) {
	sess, ok := ctx.Value(cardKey{}).(*cardSession)
	if !ok {
		return nil, httpError(http.StatusUnauthorized, "not signed in")
	}
	return sess, nil
}

func cardPrincipal(sess *cardSession) string { return cardPrefix + "/" + sess.login.person.ID + "/" }
func cardHome(sess *cardSession) string      { return cardPrincipal(sess) + "contacts/" }
func cardBook(sess *cardSession) string      { return cardHome(sess) + cardBookName + "/" }

func (b *cardBackend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return "", err
	}
	return cardPrincipal(sess), nil
}

func (b *cardBackend) AddressBookHomeSetPath(ctx context.Context) (string, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return "", err
	}
	return cardHome(sess), nil
}

func (b *cardBackend) book(sess *cardSession) webcard.AddressBook {
	return webcard.AddressBook{
		Path: cardBook(sess), Name: "Hejmo", Description: "Hejmo", MaxResourceSize: maxCardBytes,
		SupportedAddressData: []webcard.AddressDataType{{ContentType: vcard.MIMEType, Version: "3.0"}, {ContentType: vcard.MIMEType, Version: "4.0"}},
	}
}

func (b *cardBackend) ListAddressBooks(ctx context.Context) ([]webcard.AddressBook, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return nil, err
	}
	return []webcard.AddressBook{b.book(sess)}, nil
}

func (b *cardBackend) GetAddressBook(ctx context.Context, p string) (*webcard.AddressBook, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return nil, err
	}
	if p != cardBook(sess) {
		return nil, httpError(http.StatusNotFound, "no such address book")
	}
	book := b.book(sess)
	return &book, nil
}

func (b *cardBackend) CreateAddressBook(context.Context, *webcard.AddressBook) error {
	return httpError(http.StatusForbidden, "Hejmo is the one address book of the box")
}

func (b *cardBackend) DeleteAddressBook(context.Context, string) error {
	return httpError(http.StatusForbidden, "Hejmo is the one address book of the box")
}

// objectID reads the contact id from /carddav/{person}/contacts/hejmo/{id}.vcf.
func objectID(sess *cardSession, p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, cardBook(sess))
	if !ok {
		return "", false
	}
	id, isCard := strings.CutSuffix(rest, ".vcf")
	return id, isCard && id != "" && !strings.Contains(id, "/")
}

// object renders a contact as a resource of the address book.
func (b *cardBackend) object(sess *cardSession, c store.Contact) (webcard.AddressObject, error) {
	var photo []byte
	if c.HasPhoto {
		ph, err := b.s.db.ContactPhoto(sess.login.house.ID, c.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return webcard.AddressObject{}, err
		}
		photo = ph.JPEG
	}
	card := contactsync.Encode(c, photo, sess.version)
	mod := c.UpdatedAt
	if c.PhotoAt.After(mod) {
		mod = c.PhotoAt
	}
	return webcard.AddressObject{Path: cardBook(sess) + c.ID + ".vcf", ModTime: mod, ETag: contactsync.ETag(c), Card: card}, nil
}

func (b *cardBackend) GetAddressObject(ctx context.Context, p string, _ *webcard.AddressDataRequest) (*webcard.AddressObject, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return nil, err
	}
	id, ok := objectID(sess, p)
	if !ok {
		return nil, httpError(http.StatusNotFound, "no such contact")
	}
	c, err := b.s.db.Contact(sess.login.house.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, httpError(http.StatusNotFound, "no such contact")
	}
	if err != nil {
		return nil, err
	}
	o, err := b.object(sess, c)
	return &o, err
}

func (b *cardBackend) ListAddressObjects(ctx context.Context, p string, _ *webcard.AddressDataRequest) ([]webcard.AddressObject, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return nil, err
	}
	if p != cardBook(sess) {
		return nil, httpError(http.StatusNotFound, "no such address book")
	}
	all, err := b.s.db.Contacts(sess.login.house.ID)
	if err != nil {
		return nil, err
	}
	var out []webcard.AddressObject
	for _, c := range all {
		o, err := b.object(sess, c)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func (b *cardBackend) QueryAddressObjects(ctx context.Context, p string, q *webcard.AddressBookQuery) ([]webcard.AddressObject, error) {
	all, err := b.ListAddressObjects(ctx, p, &q.DataRequest)
	if err != nil {
		return nil, err
	}
	if len(q.PropFilters) == 0 { // a query without a filter asks for everything (RFC 6352, section 10.5)
		return all, nil
	}
	return webcard.Filter(q, all)
}

// validCardName limits the names a phone gives its cards to what is safe in an address and as an id.
var validCardName = regexp.MustCompile(`^[A-Za-z0-9._@=+-]{1,128}$`)

func checkCardETag(c *store.Contact, ifMatch, ifNoneMatch webdav.ConditionalMatch) error {
	if c == nil {
		return checkETag("", false, ifMatch, ifNoneMatch)
	}
	return checkETag(contactsync.ETag(*c), true, ifMatch, ifNoneMatch)
}

func (b *cardBackend) PutAddressObject(ctx context.Context, p string, card vcard.Card, opts *webcard.PutAddressObjectOptions) (*webcard.AddressObject, error) {
	sess, err := b.session(ctx)
	if err != nil {
		return nil, err
	}
	id, ok := objectID(sess, p)
	if !ok || !validCardName.MatchString(id) || strings.Trim(id, ".") == "" {
		return nil, httpError(http.StatusBadRequest, "card names may use letters, digits and . _ @ = + -")
	}
	house, by := sess.login.house, sess.login.person
	in := contactsync.Decode(card)

	b.mu.Lock()
	defer b.mu.Unlock()
	all, err := b.s.db.Contacts(house.ID)
	if err != nil {
		return nil, err
	}
	var cur *store.Contact
	if i := slices.IndexFunc(all, func(c store.Contact) bool { return c.ID == id }); i >= 0 {
		cur = &all[i]
	}
	if err := checkCardETag(cur, opts.IfMatch, opts.IfNoneMatch); err != nil {
		return nil, err
	}
	input, changed := contactsync.Apply(cur, in, all)
	if cur == nil && !changed {
		return nil, httpError(http.StatusBadRequest, "the card has no name, number or e-mail address")
	}
	input.Today = b.s.householdNow(house)
	input.CalendarName = b.s.i18n.T(b.s.langOf(sess.req), "birthdays.calendar_name")
	input.PhotoChange, input.Photo = b.photoOf(ctx, sess, cur, in)
	changed = changed || input.PhotoChange != store.KeepPicture

	switch {
	case cur == nil:
		input.ID = id
		_, err = b.s.db.CreateContact(house.ID, by.ID, input)
	case changed:
		_, err = b.s.db.UpdateContact(house.ID, id, by.ID, input)
		var taken *store.NameTakenError
		if errors.As(err, &taken) { // a rename to a name another contact has: the name stays
			input.HejmoName = cur.HejmoName
			_, err = b.s.db.UpdateContact(house.ID, id, by.ID, input)
		}
	}
	if err != nil {
		var field *store.FieldError
		if errors.Is(err, store.ErrInvalid) || errors.As(err, &field) {
			return nil, httpError(http.StatusBadRequest, err.Error())
		}
		return nil, err
	}
	saved, err := b.s.db.Contact(house.ID, id)
	if err != nil {
		return nil, err
	}
	o, err := b.object(sess, saved)
	return &o, err
}

// photoOf says what a card does to the photo of a contact: a photo that differs from the stored one replaces it
// (made under the picture rules, marked as coming from the phone), a card without a photo removes it, a photo
// given as a web address is never fetched and changes nothing (ADR 0024).
func (b *cardBackend) photoOf(ctx context.Context, sess *cardSession, cur *store.Contact, in contactsync.Incoming) (store.PictureChange, *store.ContactPhoto) {
	has := cur != nil && cur.HasPhoto
	switch {
	case in.PhotoLink:
		return store.KeepPicture, nil
	case len(in.Photo) == 0:
		if has {
			return store.RemovePicture, nil
		}
		return store.KeepPicture, nil
	}
	var stored []byte
	if has {
		if ph, err := b.s.db.ContactPhoto(sess.login.house.ID, cur.ID); err == nil {
			stored = ph.JPEG
		}
	}
	if bytes.Equal(stored, in.Photo) {
		return store.KeepPicture, nil
	}
	res, err := picture.ProcessSquare(ctx, bytes.NewReader(in.Photo), picture.ContactMaxSide)
	if err != nil || bytes.Equal(stored, res.JPEG) {
		return store.KeepPicture, nil
	}
	return store.SetPicture, &store.ContactPhoto{JPEG: res.JPEG, Origin: store.PhotoSync, Device: sess.login.token.Name}
}

func (b *cardBackend) DeleteAddressObject(ctx context.Context, p string) error {
	sess, err := b.session(ctx)
	if err != nil {
		return err
	}
	id, ok := objectID(sess, p)
	if !ok {
		return httpError(http.StatusNotFound, "no such contact")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.s.db.Contact(sess.login.house.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return httpError(http.StatusNotFound, "no such contact")
	}
	if err != nil {
		return err
	}
	if c.IsPerson() {
		return httpError(http.StatusForbidden, "a person of the household is deleted in Hejmo, not from a phone")
	}
	if m := webdav.ConditionalMatch(sess.req.Header.Get("If-Match")); m.IsSet() {
		if err := checkCardETag(&c, m, ""); err != nil {
			return err
		}
	}
	return b.s.db.DeleteContact(sess.login.house.ID, id)
}
