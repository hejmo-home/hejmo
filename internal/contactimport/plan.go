// Package contactimport works out what a contact file brings to a household's address book (ticket 300): which
// cards are possible duplicates of contacts in Hejmo or of each other, what merging would add, which cards are
// new and what they will be called. It changes nothing; the store applies the operations it returns in one step.
//
// Matching: a number matches by its E.164 form read with the household's home country (ADR 0026), or by its digits
// when it cannot be read; an e-mail address by its lower-case form; failing both, an exact name (the Hejmo name or
// the full name, ignoring case). Empty values never match. Only a number or e-mail match preselects merging.
package contactimport

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/vcard"
)

// What a match is made of.
const (
	MatchNumber = "number"
	MatchEmail  = "email"
	MatchName   = "name"
)

// Why a name was changed.
const (
	NameLong  = "long"  // over 40 characters, shortened
	NameTaken = "taken" // another contact has it
)

// The choices for a possible duplicate.
const (
	ChoiceMerge = "merge"
	ChoiceNew   = "new"
	ChoiceSkip  = "skip"
)

// Match is one thing two cards or a card and a contact have in common.
type Match struct {
	Kind string
	// Hejmo is the number or e-mail address as it stands on the other side (the contact in Hejmo, or the first
	// card of the file); for a name its Value is the name.
	Hejmo contact.Line
	// File is how the card writes it. Code is the country code ("+49") when the card wrote the number with it and
	// the other side did not.
	File, Code string
}

// NameNote says why a new contact does not have the name the file gives it.
type NameNote struct {
	Kind     string
	Wanted   string // the name it would have had
	ByPerson bool   // for NameTaken: a person of the household has that name
}

// Merge is what merging a card into a contact adds and what stays as Hejmo has it. Nothing is ever deleted or
// overwritten; for a person of the household the file changes neither the name, nor the birthday, nor the
// e-mail addresses (the Locked fields say the file had something there).
type Merge struct {
	AddNumbers, AddEmails []contact.Line
	AddFull               string
	AddAddress            bool
	AddBirthday, AddYear  bool
	AddNote, AddPhoto     bool
	// PhotoChoice is set when both sides have a photo and something else is added: the parent chooses.
	PhotoChoice bool
	// What the file says and Hejmo keeps as it is (only where both have a value that differs).
	FileFull, FileAddress, FileNote string
	FileBirthday                    [3]int
	// Locked: the file brings something a person's contact does not take.
	LockedEmails, LockedBirthday, LockedName bool
	// Dropped counts numbers and e-mail addresses over the limit of ten, which are not taken.
	Dropped int
}

// Empty says merging would add nothing.
func (m Merge) Empty() bool {
	return len(m.AddNumbers) == 0 && len(m.AddEmails) == 0 && m.AddFull == "" && !m.AddAddress && !m.AddBirthday && !m.AddYear && !m.AddNote && !m.AddPhoto
}

// Preview is the contact that two cards of the file make when merged.
type Preview struct {
	Name, Full      string
	Numbers, Emails []contact.Line
	HasPhoto        bool
}

// Dup is a card (or several cards of the file) that may be a contact Hejmo has, or each other.
type Dup struct {
	Key string // "d" and the number of the first card
	// Cards is the file side: one card, or the cards that look the same when InFile.
	Cards []vcard.Card
	// Existing is the contact in Hejmo, nil when InFile.
	Existing  *store.Contact
	InFile    bool
	Matches   []Match
	Preselect string // ChoiceMerge for a number or e-mail match, else empty
	Choices   []string
	Merge     Merge
	// NewName is the name an own contact of the (first) card would get; NewNames has one per card.
	NewName  string
	NewNote  *NameNote
	NewNames []string
	NewNotes []*NameNote
	Merged   *Preview // for InFile: what the cards make together

	merged fields
	region string
}

// join adds a card to the question of a contact in Hejmo, in the order of the file, and what it has in common.
func (d *Dup) join(c vcard.Card, ms []Match) {
	i, _ := slices.BinarySearchFunc(d.Cards, c.Number, func(x vcard.Card, n int) int { return x.Number - n })
	d.Cards = slices.Insert(d.Cards, i, c)
	if len(d.Matches) == 0 || ms[0].Kind != MatchName {
		d.Matches = append(d.Matches, ms...)
	}
	if d.Matches[0].Kind != MatchName {
		d.Preselect = ChoiceMerge
	}
}

// New is a card that is nobody in Hejmo yet.
type New struct {
	Key  string // "n" and the number of the card
	Card vcard.Card
	Name string
	Note *NameNote
}

// Same is a card whose contact already holds everything it says.
type Same struct {
	Name string
	ID   string
}

// Unread is a card Hejmo cannot take over.
type Unread struct {
	Card    vcard.Card
	Problem vcard.Problem
}

// Plan is everything the review page shows.
type Plan struct {
	Dups   []Dup
	News   []New
	Same   []Same
	Unread []Unread
	// Cards is the number of cards in the file.
	Cards int
}

// fields are the values of a contact that a card can add to.
type fields struct {
	Full, Street, Postcode, City, Country, Note string
	Day, Month, Year                            int
	Numbers, Emails                             []contact.Line
	Photo                                       bool
}

func fieldsOfContact(c store.Contact) fields {
	return fields{Full: c.FullName, Street: c.Street, Postcode: c.Postcode, City: c.City, Country: c.Country, Note: c.Note,
		Day: c.BdayDay, Month: c.BdayMonth, Year: c.BdayYear, Numbers: slices.Clone(c.Numbers), Emails: slices.Clone(c.Emails), Photo: c.HasPhoto}
}

func fieldsOfCard(c vcard.Card) fields {
	note := c.Note
	if note == "" {
		var parts []string
		for _, p := range []string{c.Org, c.Title} {
			if p != "" && p != c.Name() {
				parts = append(parts, p)
			}
		}
		note = strings.Join(parts, ", ")
	}
	return fields{Full: c.Full, Street: c.Street, Postcode: c.Postcode, City: c.City, Country: c.Country, Note: note,
		Day: c.BdayDay, Month: c.BdayMonth, Year: c.BdayYear, Numbers: c.Numbers, Emails: c.Emails, Photo: len(c.Photo) > 0}
}

func addressLine(street, postcode, city, country string) string {
	var parts []string
	if street != "" {
		parts = append(parts, street)
	}
	if l := strings.TrimSpace(postcode + " " + city); l != "" {
		parts = append(parts, l)
	}
	if country != "" {
		parts = append(parts, country)
	}
	return strings.Join(parts, ", ")
}

func (f fields) address() string { return addressLine(f.Street, f.Postcode, f.City, f.Country) }

// absorb adds what b has and f lacks and says what it did. A person's contact (person) takes neither a
// full name, nor a birthday, nor e-mail addresses.
func (f *fields) absorb(b fields, person bool, region string) Merge {
	var m Merge
	have := map[string]bool{}
	for _, l := range f.Numbers {
		have[contact.NumberKey(l.Value, region)] = true
	}
	for _, l := range b.Numbers {
		k := contact.NumberKey(l.Value, region)
		if k == "" || have[k] {
			continue
		}
		if len(f.Numbers) >= contact.MaxLines {
			m.Dropped++
			continue
		}
		have[k] = true
		l.E164 = ""
		f.Numbers = append(f.Numbers, l)
		m.AddNumbers = append(m.AddNumbers, l)
	}
	haveMail := map[string]bool{}
	for _, l := range f.Emails {
		haveMail[contact.EmailKey(l.Value)] = true
	}
	for _, l := range b.Emails {
		k := contact.EmailKey(l.Value)
		if k == "" || haveMail[k] {
			continue
		}
		if person {
			m.LockedEmails = true
			continue
		}
		if len(f.Emails) >= contact.MaxLines {
			m.Dropped++
			continue
		}
		haveMail[k] = true
		f.Emails = append(f.Emails, l)
		m.AddEmails = append(m.AddEmails, l)
	}
	switch {
	case b.Full == "":
	case person:
		m.LockedName = !strings.EqualFold(f.Full, b.Full)
	case f.Full == "":
		f.Full, m.AddFull = b.Full, b.Full
	case !strings.EqualFold(f.Full, b.Full):
		m.FileFull = b.Full
	}
	switch {
	case b.address() == "":
	case f.address() == "":
		f.Street, f.Postcode, f.City, f.Country = b.Street, b.Postcode, b.City, b.Country
		m.AddAddress = true
	case !strings.EqualFold(f.address(), b.address()):
		m.FileAddress = b.address()
	}
	switch {
	case b.Day == 0:
	case person:
		m.LockedBirthday = f.Day != b.Day || f.Month != b.Month || (f.Year == 0 && b.Year != 0) || (f.Year != 0 && b.Year != 0 && f.Year != b.Year)
	case f.Day == 0:
		f.Day, f.Month, f.Year = b.Day, b.Month, b.Year
		m.AddBirthday = true
	case f.Day == b.Day && f.Month == b.Month && f.Year == 0 && b.Year != 0:
		f.Year = b.Year
		m.AddYear = true
	case f.Day != b.Day || f.Month != b.Month || (f.Year != 0 && b.Year != 0 && f.Year != b.Year):
		m.FileBirthday = [3]int{b.Day, b.Month, b.Year}
	}
	switch {
	case b.Note == "":
	case f.Note == "":
		f.Note, m.AddNote = b.Note, true
	case f.Note != b.Note:
		m.FileNote = b.Note
	}
	switch {
	case !b.Photo:
	case !f.Photo:
		f.Photo, m.AddPhoto = true, true
	default:
		m.PhotoChoice = true
	}
	return m
}

// merge adds the later cards to the first one's values.
func mergeInto(f fields, rest []fields, person bool, region string) (fields, Merge) {
	var all Merge
	for _, b := range rest {
		m := f.absorb(b, person, region)
		all.AddNumbers = append(all.AddNumbers, m.AddNumbers...)
		all.AddEmails = append(all.AddEmails, m.AddEmails...)
		if all.AddFull == "" {
			all.AddFull = m.AddFull
		}
		all.AddAddress = all.AddAddress || m.AddAddress
		all.AddBirthday = all.AddBirthday || m.AddBirthday
		all.AddYear = all.AddYear || m.AddYear
		all.AddNote = all.AddNote || m.AddNote
		all.AddPhoto = all.AddPhoto || m.AddPhoto
		all.PhotoChoice = all.PhotoChoice || m.PhotoChoice
		all.LockedEmails = all.LockedEmails || m.LockedEmails
		all.LockedBirthday = all.LockedBirthday || m.LockedBirthday
		all.LockedName = all.LockedName || m.LockedName
		all.Dropped += m.Dropped
		for _, p := range []struct {
			dst *string
			src string
		}{{&all.FileFull, m.FileFull}, {&all.FileAddress, m.FileAddress}, {&all.FileNote, m.FileNote}} {
			if *p.dst == "" {
				*p.dst = p.src
			}
		}
		if all.FileBirthday == ([3]int{}) {
			all.FileBirthday = m.FileBirthday
		}
	}
	return f, all
}

type cardInfo struct {
	card vcard.Card
	nm   []string
	nums map[string]contact.Line // key -> line
	mail map[string]contact.Line
}

func newCardInfo(c vcard.Card, region string) cardInfo {
	ci := cardInfo{card: c, nums: map[string]contact.Line{}, mail: map[string]contact.Line{}}
	// A name matches by the full name of the card; a nickname alone ("Ben") is no reason to think of the same
	// person, but a card without a full name has no other name.
	if n := strings.TrimSpace(c.Full); n != "" {
		ci.nm = []string{n}
	} else if n := strings.TrimSpace(c.Name()); n != "" {
		ci.nm = []string{n}
	}
	for _, l := range c.Numbers {
		if k := contact.NumberKey(l.Value, region); k != "" {
			if _, ok := ci.nums[k]; !ok {
				ci.nums[k] = l
			}
		}
	}
	for _, l := range c.Emails {
		if k := contact.EmailKey(l.Value); k != "" {
			if _, ok := ci.mail[k]; !ok {
				ci.mail[k] = l
			}
		}
	}
	return ci
}

// matchOf finds what a card has in common with a contact's values and names, in the order numbers, e-mail
// addresses, name. file is how the card spells it; other is how the other side does.
func (ci cardInfo) matchOf(numbers, emails []contact.Line, names []string, region string) []Match {
	var out []Match
	seen := map[string]bool{}
	for _, l := range numbers {
		k := contact.NumberKey(l.Value, region)
		if fl, ok := ci.nums[k]; ok && k != "" && !seen["n"+k] {
			seen["n"+k] = true
			m := Match{Kind: MatchNumber, Hejmo: l, File: fl.Value}
			if strings.TrimSpace(fl.Value) != strings.TrimSpace(l.Value) && strings.HasPrefix(strings.TrimSpace(fl.Value), "+") && !strings.HasPrefix(strings.TrimSpace(l.Value), "+") {
				m.Code = contact.CallingCode(fl.Value, region)
			}
			out = append(out, m)
		}
	}
	for _, l := range emails {
		k := contact.EmailKey(l.Value)
		if fl, ok := ci.mail[k]; ok && k != "" && !seen["e"+k] {
			seen["e"+k] = true
			out = append(out, Match{Kind: MatchEmail, Hejmo: l, File: fl.Value})
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, n := range names {
		for _, mine := range ci.nm {
			if n != "" && strings.EqualFold(strings.TrimSpace(n), mine) {
				return []Match{{Kind: MatchName, Hejmo: contact.Line{Value: n}, File: mine}}
			}
		}
	}
	return nil
}

func contactNames(c store.Contact) []string { return []string{c.HejmoName, c.FullName} }

// sanitize drops a birthday that cannot be stored (a date that is still to come, a year before 1900).
func sanitize(c vcard.Card, today time.Time) vcard.Card {
	if c.BdayDay == 0 {
		return c
	}
	if contact.CheckBirthday(c.BdayDay, c.BdayMonth, c.BdayYear, today) != "" {
		if contact.CheckBirthday(c.BdayDay, c.BdayMonth, 0, today) == "" {
			c.BdayYear = 0
		} else {
			c.BdayDay, c.BdayMonth, c.BdayYear = 0, 0, 0
		}
	}
	return c
}

type cluster struct {
	infos []cardInfo
	match []Match
}

// Build works out what the cards bring to the contacts of a household. region is the home country (ISO 3166-1).
func Build(cards []vcard.Card, existing []store.Contact, region string) Plan {
	p := Plan{Cards: len(cards)}
	today := time.Now()
	var clusters []*cluster
	for _, raw := range cards {
		c := sanitize(raw, today)
		if c.Problem != "" {
			p.Unread = append(p.Unread, Unread{Card: c, Problem: c.Problem})
			continue
		}
		ci := newCardInfo(c, region)
		// A contact of Hejmo: the one with the most numbers and e-mail addresses in common; the first on a tie.
		best, bestScore := -1, 0
		var bestMatches []Match
		for i, e := range existing {
			ms := ci.matchOf(e.Numbers, e.Emails, contactNames(e), region)
			score := 0
			for _, m := range ms {
				if m.Kind != MatchName {
					score += 2
				} else {
					score = 1
				}
			}
			if len(ms) > 0 && score > bestScore {
				best, bestScore, bestMatches = i, score, ms
			}
		}
		if best >= 0 {
			e := existing[best]
			_, m := mergeInto(fieldsOfContact(e), []fields{fieldsOfCard(c)}, e.IsPerson(), region)
			if m.Empty() {
				p.Same = append(p.Same, Same{Name: e.HejmoName, ID: e.ID})
				continue
			}
			// Cards that match the same contact by number or e-mail address are one question (a match by name
			// alone is a question of its own; Ops still makes one merge of them if both are chosen).
			if i := slices.IndexFunc(p.Dups, func(d Dup) bool {
				return d.Existing.ID == e.ID && d.Matches[0].Kind != MatchName && bestMatches[0].Kind != MatchName
			}); i >= 0 {
				p.Dups[i].join(c, bestMatches)
				continue
			}
			d := Dup{Key: "d" + itoa(c.Number), Existing: &existing[best], region: region}
			d.Choices = []string{ChoiceMerge, ChoiceNew, ChoiceSkip}
			if e.IsPerson() {
				d.Choices = []string{ChoiceMerge, ChoiceSkip}
			}
			d.join(c, bestMatches)
			p.Dups = append(p.Dups, d)
			continue
		}
		// Another new card of the file.
		joined := false
		for _, cl := range clusters {
			for _, other := range cl.infos {
				ms := ci.matchOf(other.card.Numbers, other.card.Emails, other.nm, region)
				if len(ms) == 0 {
					continue
				}
				cl.infos = append(cl.infos, ci)
				if len(cl.match) == 0 || (cl.match[0].Kind == MatchName && ms[0].Kind != MatchName) {
					cl.match = ms
				}
				joined = true
				break
			}
			if joined {
				break
			}
		}
		if !joined {
			clusters = append(clusters, &cluster{infos: []cardInfo{ci}})
		}
	}
	// A new card that shares a number, an e-mail address or the name with a card that matched a contact is in that
	// question too, whichever of the two comes first in the file.
	clusters = slices.DeleteFunc(clusters, func(cl *cluster) bool {
		for i := range p.Dups {
			d := &p.Dups[i]
			for _, dc := range slices.Clone(d.Cards) {
				for _, ci := range cl.infos {
					ms := ci.matchOf(dc.Numbers, dc.Emails, nil, region) // a name alone does not join
					if len(ms) == 0 || d.Matches[0].Kind == MatchName {
						continue
					}
					for _, in := range cl.infos {
						d.join(in.card, ms)
					}
					return true
				}
			}
		}
		return false
	})
	for i := range p.Dups {
		d := &p.Dups[i]
		var all []fields
		for _, c := range d.Cards {
			all = append(all, fieldsOfCard(c))
		}
		_, d.Merge = mergeInto(fieldsOfContact(*d.Existing), all, d.Existing.IsPerson(), region)
		d.Key = "d" + itoa(d.Cards[0].Number)
	}
	taken := map[string]takenBy{}
	for _, e := range existing {
		taken[strings.ToLower(e.HejmoName)] = takenBy{e.HejmoName, e.IsPerson()}
	}
	// Names are given in the order of the file: dups of contacts in Hejmo and clusters and new cards by first card.
	type unit struct {
		first int
		dup   int // index in p.Dups, or -1
		cl    *cluster
	}
	var units []unit
	for i, d := range p.Dups {
		units = append(units, unit{d.Cards[0].Number, i, nil})
	}
	for _, cl := range clusters {
		units = append(units, unit{cl.infos[0].card.Number, -1, cl})
	}
	slices.SortStableFunc(units, func(a, b unit) int { return a.first - b.first })
	var news []New
	var dups []Dup
	for _, u := range units {
		switch {
		case u.dup >= 0:
			d := p.Dups[u.dup]
			for _, c := range d.Cards {
				name, note := assignName(c, taken)
				d.NewNames, d.NewNotes = append(d.NewNames, name), append(d.NewNotes, note)
			}
			d.NewName, d.NewNote = d.NewNames[0], d.NewNotes[0]
			dups = append(dups, d)
		case len(u.cl.infos) == 1:
			c := u.cl.infos[0].card
			name, note := assignName(c, taken)
			news = append(news, New{Key: "n" + itoa(c.Number), Card: c, Name: name, Note: note})
		default:
			d := Dup{Key: "d" + itoa(u.cl.infos[0].card.Number), InFile: true, region: region, Matches: u.cl.match, Choices: []string{ChoiceMerge, ChoiceNew, ChoiceSkip}}
			for _, ci := range u.cl.infos {
				d.Cards = append(d.Cards, ci.card)
				name, note := assignName(ci.card, taken)
				d.NewNames, d.NewNotes = append(d.NewNames, name), append(d.NewNotes, note)
			}
			d.NewName, d.NewNote = d.NewNames[0], d.NewNotes[0]
			if d.Matches[0].Kind != MatchName {
				d.Preselect = ChoiceMerge
			}
			var rest []fields
			for _, c := range d.Cards[1:] {
				rest = append(rest, fieldsOfCard(c))
			}
			merged, m := mergeInto(fieldsOfCard(d.Cards[0]), rest, false, region)
			d.Merge, d.merged = m, merged
			pv := &Preview{Name: d.NewName, Full: merged.Full, Numbers: merged.Numbers, Emails: merged.Emails, HasPhoto: merged.Photo}
			d.Merged = pv
			dups = append(dups, d)
		}
	}
	p.Dups, p.News = dups, news
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

type takenBy struct {
	name   string
	person bool
}

// shorten cuts a name to at most 40 characters at a word boundary, without a dangling separator.
func shorten(s string) string {
	if utf8.RuneCountInString(s) <= contact.MaxName {
		return s
	}
	r := []rune(s)[:contact.MaxName]
	cut := string(r)
	if i := strings.LastIndexAny(cut, " ,;-"); i > contact.MaxName/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-–")
}

// assignName gives a card the Hejmo name of a contact made from it: the nickname, else the full name, else the
// organisation, else the first number or e-mail address; shortened when over 40 characters; when taken, the
// next of these that is free; else with a number behind it ("Anna 2"). The name is then taken for the cards after it.
func assignName(c vcard.Card, taken map[string]takenBy) (string, *NameNote) {
	var cands []string
	for _, n := range []string{c.Nickname, c.Full, c.Org} {
		if n = strings.TrimSpace(n); n != "" && !slices.ContainsFunc(cands, func(o string) bool { return strings.EqualFold(o, n) }) {
			cands = append(cands, n)
		}
	}
	if len(cands) == 0 {
		if len(c.Numbers) > 0 {
			cands = append(cands, c.Numbers[0].Value)
		} else if len(c.Emails) > 0 {
			cands = append(cands, c.Emails[0].Value)
		}
	}
	if len(cands) == 0 {
		cands = []string{"?"}
	}
	wanted := cands[0]
	var note *NameNote
	if utf8.RuneCountInString(wanted) > contact.MaxName {
		note = &NameNote{Kind: NameLong, Wanted: wanted}
	}
	var first *takenBy
	for _, cand := range cands {
		n := shorten(cand)
		if t, ok := taken[strings.ToLower(n)]; ok {
			if first == nil {
				first = &t
			}
			continue
		}
		taken[strings.ToLower(n)] = takenBy{n, false}
		if cand != wanted || first != nil {
			note = &NameNote{Kind: NameTaken, Wanted: first.name, ByPerson: first.person}
		}
		return n, note
	}
	base := shorten(cands[len(cands)-1])
	if first != nil {
		base = first.name // "Anna 2", whatever the case of the second "ANNA"
	}
	for i := 2; ; i++ {
		suffix := " " + itoa(i)
		n := base
		if utf8.RuneCountInString(n)+len(suffix) > contact.MaxName {
			n = strings.TrimRight(string([]rune(n)[:contact.MaxName-len(suffix)]), " ")
		}
		n += suffix
		if _, ok := taken[strings.ToLower(n)]; !ok {
			taken[strings.ToLower(n)] = takenBy{n, false}
			if first == nil {
				first = &takenBy{name: wanted}
			}
			return n, &NameNote{Kind: NameTaken, Wanted: first.name, ByPerson: first.person}
		}
	}
}

// Decisions are what the parent chose on the review page. Dup maps the key of a possible duplicate to
// ChoiceMerge, ChoiceNew or ChoiceSkip; where it has no entry the preselection counts. Photo maps the key of
// a duplicate that has two photos to "keep" (the default) or "file". Skip holds the keys of new cards that are
// not taken over. Groups are the ids of the groups the new contacts come into.
type Decisions struct {
	Dup, Photo map[string]string
	Skip       map[string]bool
	Groups     []string
}

func (d Dup) chosen(dec Decisions) (string, bool) {
	v, ok := dec.Dup[d.Key]
	if !ok {
		v = d.Preselect
	}
	return v, slices.Contains(d.Choices, v)
}

// Missing lists the keys of the possible duplicates the parent has not chosen for.
func (p Plan) Missing(dec Decisions) []string {
	var out []string
	for _, d := range p.Dups {
		if _, ok := d.chosen(dec); !ok {
			out = append(out, d.Key)
		}
	}
	return out
}

// Result names what an import made and completed, in the order of the file.
type Result struct {
	New, Completed []string
	Entries        []Entry
}

// Entry is one contact the import writes, in the order of its operations.
type Entry struct {
	Name      string
	Completed bool
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func firstPhoto(cards []vcard.Card) []byte {
	for _, c := range cards {
		if len(c.Photo) > 0 {
			return c.Photo
		}
	}
	return nil
}

func cleanLines(in []contact.Line) []contact.Line {
	var out []contact.Line
	for _, l := range in {
		l.E164 = ""
		out = append(out, l)
	}
	return out
}

// input is the contact a set of values makes; the caller adds what differs between a new and a completed one.
func input(name string, f fields, file string) store.ContactInput {
	return store.ContactInput{HejmoName: name, FullName: clip(f.Full, contact.MaxFullName), Street: f.Street, Postcode: f.Postcode, City: f.City, Country: f.Country,
		BdayDay: f.Day, BdayMonth: f.Month, BdayYear: f.Year, Note: clip(f.Note, contact.MaxNote),
		Numbers: cleanLines(f.Numbers), Emails: cleanLines(f.Emails), ImportFile: file}
}

func withPhoto(in store.ContactInput, jpeg []byte, file string) store.ContactInput {
	if len(jpeg) > 0 {
		in.PhotoChange, in.Photo = store.SetPicture, &store.ContactPhoto{JPEG: jpeg, Origin: store.PhotoManual, File: file}
	}
	return in
}

// apply makes the operation for merging: into the contact in Hejmo, or, for cards of the file, into one new
// contact. photo is "file" when the parent wants the photo of the file over the one Hejmo has. It also returns the
// name of the contact.
func (d Dup) apply(file, photo string) (store.ImportOp, string) {
	var rest []fields
	for _, c := range d.Cards {
		rest = append(rest, fieldsOfCard(c))
	}
	if d.InFile {
		merged, _ := mergeInto(rest[0], rest[1:], false, d.region)
		return store.ImportOp{Input: withPhoto(input(d.NewNames[0], merged, file), firstPhoto(d.Cards), file)}, d.NewNames[0]
	}
	e := *d.Existing
	f, m := mergeInto(fieldsOfContact(e), rest, e.IsPerson(), d.region)
	in := input(e.HejmoName, f, file)
	in.HideBirthday, in.GroupIDs, in.Relations = e.HideBirthday, slices.Clone(e.GroupIDs), slices.Clone(e.Relations)
	if m.AddPhoto || (m.PhotoChoice && photo == "file") {
		in = withPhoto(in, firstPhoto(d.Cards), file)
	}
	return store.ImportOp{MergeInto: e.ID, Input: in}, e.HejmoName
}

// applyNew makes the operations for taking every card of a possible duplicate as a contact of its own.
func (d Dup) applyNew(file string) []store.ImportOp {
	var out []store.ImportOp
	for i, c := range d.Cards {
		name := d.NewName
		if i < len(d.NewNames) {
			name = d.NewNames[i]
		}
		out = append(out, store.ImportOp{Input: withPhoto(input(name, fieldsOfCard(c), file), c.Photo, file)})
	}
	return out
}

// Ops makes the operations the decisions ask for, in the order of the file, and names what they make and
// complete. The caller has checked that nothing is missing.
func (p Plan) Ops(dec Decisions, file string) ([]store.ImportOp, Result) {
	type item struct {
		first int
		ops   []store.ImportOp
		names []Entry
	}
	var items []item
	// Two questions may merge into one contact (one by name, one by number): each operation is the whole
	// contact, so they are made as one from the cards of both and the second would drop what the first adds.
	into := map[string][]vcard.Card{}
	for _, d := range p.Dups {
		if c, _ := d.chosen(dec); c == ChoiceMerge && !d.InFile {
			into[d.Existing.ID] = append(into[d.Existing.ID], d.Cards...)
		}
	}
	made := map[string]bool{}
	for _, d := range p.Dups {
		choice, _ := d.chosen(dec)
		it := item{first: d.Cards[0].Number}
		switch choice {
		case ChoiceMerge:
			if !d.InFile {
				if made[d.Existing.ID] {
					continue
				}
				made[d.Existing.ID] = true
				d.Cards = slices.Clone(into[d.Existing.ID])
				slices.SortStableFunc(d.Cards, func(a, b vcard.Card) int { return a.Number - b.Number })
			}
			op, name := d.apply(file, dec.Photo[d.Key])
			it.ops, it.names = []store.ImportOp{op}, []Entry{{Name: name, Completed: !d.InFile}}
		case ChoiceNew:
			it.ops = d.applyNew(file)
			for i := range it.ops {
				it.names = append(it.names, Entry{Name: it.ops[i].Input.HejmoName})
			}
		default:
			continue
		}
		items = append(items, it)
	}
	for _, n := range p.News {
		if dec.Skip[n.Key] {
			continue
		}
		op := store.ImportOp{Input: withPhoto(input(n.Name, fieldsOfCard(n.Card), file), n.Card.Photo, file)}
		items = append(items, item{first: n.Card.Number, ops: []store.ImportOp{op}, names: []Entry{{Name: n.Name}}})
	}
	slices.SortStableFunc(items, func(a, b item) int { return a.first - b.first })
	var ops []store.ImportOp
	var res Result
	for _, it := range items {
		for i, op := range it.ops {
			if op.MergeInto == "" {
				op.Input.GroupIDs = slices.Clone(dec.Groups)
			}
			ops = append(ops, op)
			res.Entries = append(res.Entries, it.names[i])
			if it.names[i].Completed {
				res.Completed = append(res.Completed, it.names[i].Name)
			} else {
				res.New = append(res.New, it.names[i].Name)
			}
		}
	}
	return ops, res
}
