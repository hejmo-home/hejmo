package server

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/text/collate"
	textlang "golang.org/x/text/language"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/layout"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The layout block "Emergency numbers" (ticket 303, ADR 0021 Q121): the contacts of the group "Notfall" with a phone
// number, large, in a row of the footer of their own.

// notfallGroup is the id of the household's shipped group "Notfall"; false when the household has none.
func (s *server) notfallGroup(householdID string) (string, bool, error) {
	groups, err := s.db.ContactGroups(householdID)
	if err != nil {
		return "", false, err
	}
	for _, g := range groups {
		if g.ShippedKey == "notfall" {
			return g.ID, true, nil
		}
	}
	return "", false, nil
}

// sortFurther puts further contacts (no person of the household) in the order of the overview: A to Z by Hejmo name.
func sortFurther(lang string, cs []store.Contact) {
	coll := collate.New(textlang.Make(lang))
	slices.SortStableFunc(cs, func(a, b store.Contact) int { return coll.CompareString(a.HejmoName, b.HejmoName) })
}

// sosGroup is the group "Notfall" in the order of the contacts overview: further contacts A to Z by Hejmo name, then
// the household's people in people order.
type sosGroup struct {
	ID string
	// WithNumbers appear on the display; Without have no phone number and stay off it.
	WithNumbers, Without []store.Contact
}

func (s *server) emergencyGroup(h store.Household, lang string) (sosGroup, error) {
	id, ok, err := s.notfallGroup(h.ID)
	if err != nil || !ok {
		return sosGroup{}, err
	}
	all, err := s.db.Contacts(h.ID)
	if err != nil {
		return sosGroup{}, err
	}
	var others, family []store.Contact
	for _, c := range all {
		switch {
		case !slices.Contains(c.GroupIDs, id):
		case c.IsPerson():
			family = append(family, c)
		default:
			others = append(others, c)
		}
	}
	sortFurther(lang, others)
	g := sosGroup{ID: id}
	for _, c := range append(others, family...) {
		if len(c.Numbers) > 0 {
			g.WithNumbers = append(g.WithNumbers, c)
		} else {
			g.Without = append(g.Without, c)
		}
	}
	return g, nil
}

// emergencyInputs is the input of the emergency numbers tile: the contacts with their numbers, kinds in words.
func (s *server) emergencyInputs(h store.Household, lang string) ([]familyview.EmergencyInput, error) {
	g, err := s.emergencyGroup(h, lang)
	if err != nil {
		return nil, err
	}
	people, err := s.db.People(h.ID)
	if err != nil {
		return nil, err
	}
	chips := map[string]personChip{}
	for _, c := range chipsOf(people) {
		chips[c.ID] = c
	}
	e := ctEnv{s: s, lang: lang}
	var out []familyview.EmergencyInput
	for _, c := range g.WithNumbers {
		in := familyview.EmergencyInput{ID: c.ID, Name: c.HejmoName, Initials: contact.Initials(c.HejmoName)}
		if c.HasPhoto {
			in.PhotoURL = "/display/contacts/" + c.ID + "/photo?v=" + photoVersion(c)
		}
		if chip, ok := chips[c.PersonID]; ok && c.IsPerson() {
			in.Person, in.Slot, in.Shape = true, chip.Slot, chip.Shape
		}
		for _, l := range c.Numbers {
			in.Numbers = append(in.Numbers, familyview.EmergencyNumberInput{Kind: e.kindWord(l, false), Value: l.Value})
		}
		out = append(out, in)
	}
	return out, nil
}

// handleDisplayContactPhoto serves the photo of a contact of the group "Notfall" to a paired display, which has no
// session: the emergency numbers tile shows it. No other contact's photo leaves the box this way.
func (s *server) handleDisplayContactPhoto(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.pairedDisplay(w, r)
	if !ok {
		return
	}
	lay, err := s.db.DisplayLayout(h.ID, d.ID)
	if err != nil {
		s.fail(w, "display layout", err)
		return
	}
	if !lay.Enabled(layout.Emergency) {
		http.NotFound(w, r)
		return
	}
	group, has, err := s.notfallGroup(h.ID)
	if err != nil {
		s.fail(w, "contact groups", err)
		return
	}
	c, err := s.db.Contact(h.ID, r.PathValue("id"))
	if !has || errors.Is(err, store.ErrNotFound) || (err == nil && (!slices.Contains(c.GroupIDs, group) || !c.HasPhoto)) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.fail(w, "contact", err)
		return
	}
	ph, err := s.db.ContactPhoto(h.ID, c.ID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveContactPhoto(w, r, ph)
}

// layoutSos is what the settings page says under the row of the block (preview-303): who will appear, who lacks a
// number, whether the rows fit, and the way to the group.
type layoutSos struct {
	Empty    bool   // no contact with a number: the tile stays off the display
	Names    string // "Notruf, Oma Gerda und Papa."
	Missing  string // "Ohne Nummer und darum nicht dabei: ...", "" when nobody
	Warn     string // more contacts than fit, "" otherwise
	LinkHref string
	Count    int
}

// maxRowWord is the largest row count that has a word of its own (layout.sos.rows.N); more rows say the number.
const maxRowWord = 12

func (s *server) layoutSosFor(h store.Household, lang, display string, portrait bool) (layoutSos, error) {
	g, err := s.emergencyGroup(h, lang)
	if err != nil {
		return layoutSos{}, err
	}
	say := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	out := layoutSos{Count: len(g.WithNumbers), Empty: len(g.WithNumbers) == 0, LinkHref: contactsPath}
	if g.ID != "" && !out.Empty { // an empty group is not worth a filtered list: "Open contacts" goes to the overview
		out.LinkHref = contactsPath + "?g=" + g.ID
	}
	names := func(cs []store.Contact) string {
		var n []string
		for _, c := range cs {
			n = append(n, c.HejmoName)
		}
		return joinWithAnd(n, say("layout.words.and"))
	}
	out.Names = names(g.WithNumbers) + "."
	if len(g.Without) > 0 {
		out.Missing = say("layout.sos.nonumber", "names", names(g.Without))
	}
	if n, fit := len(g.WithNumbers), layout.EmergencyFit(portrait); n > fit {
		rows := layout.EmergencyRows(n, portrait)
		rowsText := say("layout.sos.rows.n", "n", strconv.Itoa(rows))
		if rows <= maxRowWord {
			rowsText = say("layout.sos.rows." + strconv.Itoa(rows))
		}
		orient := say("layout.orient.landscape")
		if portrait {
			orient = say("layout.orient.portrait")
		}
		parts := []string{say("layout.sos.many", "display", display, "orientation", orient, "fit", strconv.Itoa(fit), "n", strconv.Itoa(n), "rows", rowsText)}
		if rows >= 3 {
			parts = append(parts, say("layout.sos.many.swipe"))
		}
		out.Warn = strings.Join(append(parts, say("layout.sos.many.rest")), " ")
	}
	return out, nil
}
