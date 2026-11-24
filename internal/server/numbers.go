package server

import (
	"errors"
	"html/template"
	"net/http"
	"slices"
	"strconv"

	"golang.org/x/text/collate"
	textlang "golang.org/x/text/language"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

// "Wichtige Nummern" (ticket 301, preview-301.html, decisions Q118 and Q127 on #195): the page of a child's personal
// device that lists the contacts a parent switched on, with one button per number; and the parents' look at it.

const (
	numbersPath     = "/numbers"
	kidsParentsPath = "/contacts/for-children"
)

// knCall is one number of a card: a button on a device that can call, a line of text on one that cannot.
type knCall struct {
	Href         template.URL
	Kind, Number string
	Text, Aria   string // "Mobil anrufen" and its label for the button
}

// knCard is one contact of the page.
type knCard struct {
	Name, Link string // Link is the contact's page, for parents only
	Person     bool
	Slot       int
	Shape      string
	Photo      ctPhotoView
	Calls      []knCall
}

// knSection is "Im Notfall", "Familie" or "Weitere Nummern".
type knSection struct {
	Key, ID, Title string
	Cards          []knCard
}

// kidsPage is what the children's page and the parents' look at it are made from.
type kidsPage struct {
	e        ctEnv
	all      []store.Contact
	ids      contact.KidSectionIDs
	sections []knSection
}

// kidsPageFor sorts the contacts for the person who looks. A child never sees their own contact; parents see
// everyone, the children included. photoBase is where the photos are served ("" for the address of the address book).
func (s *server) kidsPageFor(w http.ResponseWriter, r *http.Request, p principal, photoBase string, link bool) (kidsPage, bool) {
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return kidsPage{}, false
	}
	all, err := s.db.Contacts(p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return kidsPage{}, false
	}
	groupKey := map[string]string{}
	for _, g := range e.groups {
		if g.ShippedKey != "" {
			groupKey[g.ID] = g.ShippedKey
		}
	}
	own := ""
	if p.person.Role == store.RoleChild {
		own = p.person.ID
	}
	var cands []contact.KidCandidate
	byID := map[string]store.Contact{}
	for _, c := range all {
		byID[c.ID] = c
		k := contact.KidCandidate{ID: c.ID, Name: c.HejmoName, Person: c.IsPerson(), Visible: c.ForChildren, Numbers: len(c.Numbers)}
		for _, g := range c.GroupIDs {
			k.Notfall = k.Notfall || groupKey[g] == "notfall"
			k.Familie = k.Familie || groupKey[g] == store.FamilyGroup
		}
		cands = append(cands, k)
	}
	coll := collate.New(textlang.Make(e.lang))
	ids := contact.KidSections(cands, own, coll.CompareString)
	kp := kidsPage{e: e, all: all, ids: ids}
	for _, sec := range []struct {
		key, id string
		ids     []string
	}{{"emergency", "s-sos", ids.Emergency}, {"family", "s-fam", ids.Family}, {"other", "s-other", ids.Other}} {
		if len(sec.ids) == 0 {
			continue
		}
		out := knSection{Key: sec.key, ID: sec.id, Title: e.t("numbers.sec." + map[string]string{"emergency": "sos", "family": "family", "other": "other"}[sec.key])}
		for _, id := range sec.ids {
			out.Cards = append(out.Cards, e.knCard(byID[id], photoBase, link))
		}
		kp.sections = append(kp.sections, out)
	}
	return kp, true
}

func (e ctEnv) knCard(c store.Contact, photoBase string, link bool) knCard {
	card := knCard{Name: c.HejmoName, Photo: photoViewOf(c)}
	if photoBase != "" && c.HasPhoto {
		card.Photo.URL = photoBase + "/" + c.ID + "/photo?v=" + strconv.FormatInt(c.PhotoAt.UnixNano(), 36)
	}
	if link {
		card.Link = contactsPath + "/" + c.ID
	}
	if chip, ok := e.chips[c.PersonID]; ok && c.IsPerson() {
		card.Person, card.Slot, card.Shape = true, chip.Slot, chip.Shape
	}
	for _, l := range c.Numbers {
		kind := e.kindWord(l, false)
		card.Calls = append(card.Calls, knCall{Href: template.URL(contact.TelHref(l.Value)), Kind: kind, Number: l.Value,
			Text: e.t("numbers.call", "kind", kind), Aria: e.t("numbers.call.aria", "name", c.HejmoName, "kind", kind, "number", l.Value)})
	}
	return card
}

// canCall says whether the device of the request can place a call: a tablet cannot. A phone, and a device of
// unknown kind, can.
func (s *server) canCall(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return true
	}
	return s.db.PersonalDeviceKind(auth.HashToken(c.Value)) != "tablet"
}

func (s *server) handleNumbers(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	if p.person.Role == store.RoleParent { // parents have the address book; they see the children's page from Kontakte
		http.Redirect(w, r, kidsParentsPath, http.StatusSeeOther)
		return
	}
	kp, ok := s.kidsPageFor(w, r, p, numbersPath, false)
	if !ok {
		return
	}
	data := map[string]any{"Sections": kp.sections, "CanCall": s.canCall(r)}
	if len(kp.sections) == 0 {
		var parents []string
		for _, x := range kp.e.people {
			if x.Role == store.RoleParent {
				parents = append(parents, x.Name)
			}
		}
		data["Ask"] = kp.e.t("numbers.empty.ask", "parents", contact.JoinNames(parents, kp.e.t("numbers.empty.or")))
	}
	s.render(w, r, http.StatusOK, "numbers.html", data)
}

// kidsPhotoVisible says whether the children may see the photo of a contact: it is on their page.
func kidsPhotoVisible(c store.Contact) bool { return c.ForChildren && len(c.Numbers) > 0 && c.HasPhoto }

func (s *server) handleNumbersPhoto(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	s.serveKidsPhoto(w, r, p, func(msg string) { http.NotFound(w, r) })
}

func (s *server) serveKidsPhoto(w http.ResponseWriter, r *http.Request, p principal, notFound func(string)) {
	c, err := s.db.Contact(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.person.Role != store.RoleParent && !kidsPhotoVisible(c)) {
		notFound("not found")
		return
	} else if err != nil {
		s.fail(w, "contact", err)
		return
	}
	ph, err := s.db.ContactPhoto(p.household.ID, c.ID)
	if errors.Is(err, store.ErrNotFound) {
		notFound("no photo")
		return
	} else if err != nil {
		s.fail(w, "contact photo", err)
		return
	}
	serveContactPhoto(w, r, ph)
}

// knLink is a contact named in a line of the parents' page.
type knLink struct {
	Href, Name, Sep string
	Short           bool // short names never break
}

func (e ctEnv) knLinks(list []store.Contact) []knLink {
	out := make([]knLink, 0, len(list))
	for i, c := range list {
		sep := ", "
		if i == len(list)-1 {
			sep = "."
		}
		out = append(out, knLink{Href: contactsPath + "/" + c.ID, Name: c.HejmoName, Sep: sep, Short: contact.Runes(c.HejmoName) <= 24})
	}
	return out
}

func (s *server) handleKidsParents(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	kp, ok := s.kidsPageFor(w, r, p, "", true)
	if !ok {
		return
	}
	e := kp.e
	coll := collate.New(textlang.Make(e.lang))
	var others, family []store.Contact // as the address book orders them: the others A to Z, then the family in the order of the people
	for _, c := range kp.all {
		if c.IsPerson() {
			family = append(family, c)
		} else {
			others = append(others, c)
		}
	}
	slices.SortStableFunc(others, func(a, b store.Contact) int { return coll.CompareString(a.HejmoName, b.HejmoName) })
	var wait, hidden []store.Contact
	for _, c := range append(others, family...) {
		switch {
		case !c.ForChildren:
			hidden = append(hidden, c)
		case len(c.Numbers) == 0:
			wait = append(wait, c)
		}
	}
	data := map[string]any{"Sections": kp.sections, "CanCall": true, "Wait": e.knLinks(wait), "Hidden": e.knLinks(hidden)}
	names := e.childNames()
	switch len(names) {
	case 0:
		data["Lead"] = e.t("kidspage.lead.none")
	case 1:
		data["Lead"] = e.t("kidspage.lead.one", "names", names[0])
	default:
		data["Lead"] = e.t("kidspage.lead.many", "names", contact.JoinNames(names, e.t("contact.rel.and")))
	}
	s.render(w, r, http.StatusOK, "contacts_kids.html", data)
}

// The REST side of the children's page (ADR 0015: parity). A child's device session reads it; so does a token with
// the scope read or numbers.

type numberJSON struct {
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	Value string `json:"value"`
}

type kidContactJSON struct {
	ID       string       `json:"id"`
	PersonID *string      `json:"person_id"`
	Name     string       `json:"name"`
	HasPhoto bool         `json:"has_photo"`
	Numbers  []numberJSON `json:"numbers"`
}

type numberSectionJSON struct {
	Key      string           `json:"key"`
	Contacts []kidContactJSON `json:"contacts"`
}

func (s *server) apiNumbers(w http.ResponseWriter, r *http.Request, p principal) {
	all, err := s.db.Contacts(p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return
	}
	groups, err := s.db.ContactGroups(p.household.ID)
	if err != nil {
		s.fail(w, "contact groups", err)
		return
	}
	groupKey := map[string]string{}
	for _, g := range groups {
		groupKey[g.ID] = g.ShippedKey
	}
	own := ""
	if p.person.Role == store.RoleChild {
		own = p.person.ID
	}
	var cands []contact.KidCandidate
	byID := map[string]store.Contact{}
	for _, c := range all {
		byID[c.ID] = c
		k := contact.KidCandidate{ID: c.ID, Name: c.HejmoName, Person: c.IsPerson(), Visible: c.ForChildren, Numbers: len(c.Numbers)}
		for _, g := range c.GroupIDs {
			k.Notfall = k.Notfall || groupKey[g] == "notfall"
			k.Familie = k.Familie || groupKey[g] == store.FamilyGroup
		}
		cands = append(cands, k)
	}
	coll := collate.New(textlang.Make(s.langOf(r)))
	ids := contact.KidSections(cands, own, coll.CompareString)
	out := struct {
		Sections []numberSectionJSON `json:"sections"`
	}{Sections: []numberSectionJSON{}}
	for _, sec := range []struct {
		key string
		ids []string
	}{{"emergency", ids.Emergency}, {"family", ids.Family}, {"other", ids.Other}} {
		if len(sec.ids) == 0 {
			continue
		}
		js := numberSectionJSON{Key: sec.key, Contacts: []kidContactJSON{}}
		for _, id := range sec.ids {
			c := byID[id]
			kc := kidContactJSON{ID: c.ID, PersonID: strPtr(c.PersonID), Name: c.HejmoName, HasPhoto: c.HasPhoto, Numbers: []numberJSON{}}
			for _, l := range c.Numbers {
				kc.Numbers = append(kc.Numbers, numberJSON{Kind: string(l.Kind), Label: l.Label, Value: l.Value})
			}
			js.Contacts = append(js.Contacts, kc)
		}
		out.Sections = append(out.Sections, js)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiNumbersPhoto(w http.ResponseWriter, r *http.Request, p principal) {
	s.serveKidsPhoto(w, r, p, func(msg string) { writeError(w, http.StatusNotFound, msg) })
}
