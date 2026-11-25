// Package contactsync turns the household's contacts into vCards for phones and the cards a phone saves into
// changes of a contact (ticket 299, ADR 0021). It is pure: it reads and writes cards and knows what Hejmo
// models; the server decides who may change what and stores the result.
//
// Hejmo name goes out as NICKNAME and the full name as FN and N. Labels go out as TYPE for the four kinds and as
// Apple's X-ABLabel for the others. A birthday without a year goes out as X-APPLE-OMIT-YEAR=1604 in vCard 3.0
// and as --MMDD in 4.0, and comes in from either, from iCloud's 1604 and from a date with a time. What a
// phone saved that Hejmo does not model (organisation, web addresses, further addresses) is kept as Extra and
// written back, so changing a number in Hejmo never takes it from the phone.
package contactsync

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-vcard"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/contactimport"
	"github.com/hejmo-home/hejmo/internal/store"
	hvcard "github.com/hejmo-home/hejmo/internal/vcard"
)

// Version is the vCard version a card is written in.
type Version string

// The versions the box writes. Phones get 3.0 unless they ask for 4.0.
const (
	V3 Version = "3.0"
	V4 Version = "4.0"
)

const omitYear = "1604" // the year Apple's contacts use for "no year"

// appleLabels are the labels Apple's address book has of its own, written as _$!<Label>!$_ so a phone shows its
// own translation. Mobile, Home, Work and Other are kinds of Hejmo and written as types.
var appleLabels = []string{"iPhone", "Main", "HomeFAX", "WorkFAX", "OtherFAX", "Pager", "HomePage", "Fax"}

func appleLabel(label string) (string, bool) {
	for _, a := range appleLabels {
		if strings.EqualFold(a, label) {
			return a, true
		}
	}
	return "", false
}

func wrapLabel(l string) string { return "_$!<" + l + ">!$_" }

func unwrapLabel(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "_$!<") && strings.HasSuffix(s, ">!$_") {
		return strings.TrimSuffix(strings.TrimPrefix(s, "_$!<"), ">!$_")
	}
	return s
}

// extraField is one kept line of a card.
type extraField struct {
	Group  string              `json:"g,omitempty"`
	Key    string              `json:"k"`
	Params map[string][]string `json:"p,omitempty"`
	Value  string              `json:"v"`
}

// Encode makes the card of a contact. photo is the stored JPEG, nil for none.
func Encode(c store.Contact, photo []byte, v Version) vcard.Card {
	card := vcard.Card{}
	card.SetValue(vcard.FieldVersion, string(v))
	card.SetValue(vcard.FieldUID, c.ID)
	full := c.FullName
	if full == "" {
		full = c.HejmoName
	}
	card.SetValue(vcard.FieldFormattedName, full)
	card.SetValue(vcard.FieldName, structuredName(full))
	card.SetValue(vcard.FieldNickname, c.HejmoName)
	if c.Street != "" || c.City != "" || c.Postcode != "" || c.Country != "" {
		card.Add(vcard.FieldAddress, &vcard.Field{Value: strings.Join([]string{"", "", part(c.Street), part(c.City), "", part(c.Postcode), part(c.Country)}, ";")})
	}
	group := 0
	line := func(key string, l contact.Line, email bool) {
		f := &vcard.Field{Value: l.Value, Params: vcard.Params{}}
		if email && v == V3 {
			f.Params.Add(vcard.ParamType, "INTERNET")
		}
		switch k := l.Kind; {
		case k == contact.Cell && !email:
			f.Params.Add(vcard.ParamType, "CELL")
		case k == contact.Home:
			f.Params.Add(vcard.ParamType, "HOME")
		case k == contact.Work:
			f.Params.Add(vcard.ParamType, "WORK")
		case k == contact.Custom:
			group++
			f.Group = "item" + strconv.Itoa(group)
			label := l.Label
			if a, ok := appleLabel(label); ok {
				label = wrapLabel(a)
			}
			card.Add("X-ABLabel", &vcard.Field{Group: f.Group, Value: label})
		default:
			f.Params.Add(vcard.ParamType, "OTHER")
		}
		card.Add(key, f)
	}
	for _, l := range c.Numbers {
		line(vcard.FieldTelephone, l, false)
	}
	for _, l := range c.Emails {
		line(vcard.FieldEmail, l, true)
	}
	if c.BdayDay != 0 {
		switch {
		case c.BdayYear != 0:
			card.SetValue(vcard.FieldBirthday, fmt.Sprintf("%04d-%02d-%02d", c.BdayYear, c.BdayMonth, c.BdayDay))
		case v == V4:
			card.SetValue(vcard.FieldBirthday, fmt.Sprintf("--%02d%02d", c.BdayMonth, c.BdayDay))
		default:
			card.Set(vcard.FieldBirthday, &vcard.Field{Value: fmt.Sprintf("%s-%02d-%02d", omitYear, c.BdayMonth, c.BdayDay), Params: vcard.Params{"X-APPLE-OMIT-YEAR": {omitYear}}})
		}
	}
	if c.Note != "" {
		card.SetValue(vcard.FieldNote, c.Note)
	}
	if len(photo) > 0 {
		f := &vcard.Field{Value: base64.StdEncoding.EncodeToString(photo), Params: vcard.Params{"ENCODING": {"b"}}}
		if v == V4 {
			f.Params["MEDIATYPE"] = []string{"image/jpeg"}
		} else {
			f.Params["TYPE"] = []string{"JPEG"}
		}
		card.Set(vcard.FieldPhoto, f)
	}
	var extras []extraField
	if c.Extra != "" && json.Unmarshal([]byte(c.Extra), &extras) == nil {
		for _, e := range extras {
			if e.Key == "X-ABLABEL" {
				e.Key = "X-ABLabel"
			}
			card.Add(e.Key, &vcard.Field{Group: e.Group, Params: e.Params, Value: e.Value})
		}
	}
	return card
}

// part keeps a value out of the way of the field separator: the encoder cannot write an escaped semicolon.
func part(s string) string { return strings.ReplaceAll(s, ";", ",") }

func structuredName(full string) string {
	full = strings.TrimSpace(full)
	if i := strings.LastIndex(full, " "); i > 0 {
		return part(full[i+1:]) + ";" + part(full[:i]) + ";;;"
	}
	return part(full) + ";;;;"
}

// Text writes a card the way the box sends it.
func Text(card vcard.Card) (string, error) {
	var b bytes.Buffer
	err := vcard.NewEncoder(&b).Encode(card)
	return b.String(), err
}

// ETag is the entity tag of a contact: a hash of what a phone is sent, so it changes exactly when that does.
// The photo counts through the time it was set.
func ETag(c store.Contact) string {
	card := Encode(c, nil, V3)
	h := sha256.New()
	keys := make([]string, 0, len(card))
	for k := range card {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, f := range card[k] {
			pk := make([]string, 0, len(f.Params))
			for p := range f.Params {
				pk = append(pk, p)
			}
			sort.Strings(pk)
			fmt.Fprintf(h, "%s|%s|", f.Group, k)
			for _, p := range pk {
				fmt.Fprintf(h, "%s=%q;", p, f.Params[p])
			}
			fmt.Fprintf(h, "|%q\n", f.Value)
		}
	}
	if c.HasPhoto {
		fmt.Fprintf(h, "photo|%d", c.PhotoAt.UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// Incoming is what a card a phone saved says.
type Incoming struct {
	Full, Nickname, Org string
	Numbers, Emails     []contact.Line
	// HasAddress says the card has an address; the parts are its first one.
	HasAddress             bool
	Street, Postcode, City string
	Country                string
	// BdayYear is 0 when the year is unknown (also for 1604 and years before 1900).
	BdayDay, BdayMonth, BdayYear int
	Note                         string
	// Photo is the embedded photo as it came (not yet checked to be a picture); PhotoLink says it was given as a
	// web address, which is never fetched (ADR 0024).
	Photo     []byte
	PhotoLink bool
	// Extra is what Hejmo does not model, ready to store.
	Extra string
}

var modeled = map[string]bool{"VERSION": true, "UID": true, "PRODID": true, "REV": true, "FN": true, "N": true, "NICKNAME": true,
	"TEL": true, "EMAIL": true, "BDAY": true, "NOTE": true, "PHOTO": true, "BEGIN": true, "END": true}

func text(s string) string {
	return strings.NewReplacer(`\;`, ";", `\N`, "\n").Replace(s)
}

func splitParts(s string) []string {
	var out []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == ';':
			cur.WriteByte(';')
			i++
		case s[i] == ';':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	return append(out, cur.String())
}

func firstPart(s string) string { return strings.TrimSpace(splitParts(s)[0]) }

func fieldHas(f *vcard.Field, param, value string) bool {
	for _, v := range f.Params[param] {
		for _, piece := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(piece), value) {
				return true
			}
		}
	}
	return false
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// kindOfLabel is the kind a label word stands for; any other word stays an own label.
func kindOfLabel(label string, email bool) (contact.Kind, string) {
	switch strings.ToLower(label) {
	case "mobile", "cell", "mobil", "handy":
		if !email {
			return contact.Cell, ""
		}
	case "home", "zuhause", "privat":
		return contact.Home, ""
	case "work", "arbeit":
		return contact.Work, ""
	case "other", "sonstige":
		return contact.Other, ""
	}
	return contact.Custom, clip(label, contact.MaxOwnLabel)
}

func lineOf(f *vcard.Field, labels map[string]string, email bool) (contact.Line, bool) {
	value := strings.TrimSpace(text(f.Value))
	if !email {
		if rest, ok := strings.CutPrefix(strings.ToLower(value), "tel:"); ok {
			value, _, _ = strings.Cut(value[len(value)-len(rest):], ";")
			value = strings.TrimSpace(value)
		}
	}
	if value == "" {
		return contact.Line{}, false // empty values are never stored
	}
	if label := labels[f.Group]; f.Group != "" && label != "" {
		k, l := kindOfLabel(label, email)
		return contact.Line{Kind: k, Label: l, Value: value}, true
	}
	switch {
	case !email && (fieldHas(f, "TYPE", "FAX")):
		return contact.Line{Kind: contact.Custom, Label: "Fax", Value: value}, true
	case !email && (fieldHas(f, "TYPE", "CELL") || fieldHas(f, "TYPE", "IPHONE") || fieldHas(f, "TYPE", "MOBILE")):
		return contact.Line{Kind: contact.Cell, Value: value}, true
	case fieldHas(f, "TYPE", "WORK"):
		return contact.Line{Kind: contact.Work, Value: value}, true
	case fieldHas(f, "TYPE", "HOME"):
		return contact.Line{Kind: contact.Home, Value: value}, true
	}
	return contact.Line{Kind: contact.Other, Value: value}, true
}

// Decode reads a card a phone saved.
func Decode(card vcard.Card) Incoming {
	var in Incoming
	labels := map[string]string{}
	for _, f := range card["X-ABLABEL"] {
		if l := unwrapLabel(text(f.Value)); l != "" {
			labels[f.Group] = l
		}
	}
	in.Full = strings.TrimSpace(text(card.Value(vcard.FieldFormattedName)))
	if in.Full == "" {
		if n := card.Get(vcard.FieldName); n != nil {
			p := splitParts(n.Value)
			get := func(i int) string {
				if i < len(p) {
					return strings.TrimSpace(p[i])
				}
				return ""
			}
			var words []string
			for _, w := range []string{get(3), get(1), get(2), get(0)} {
				if w != "" {
					words = append(words, w)
				}
			}
			in.Full = strings.Join(words, " ")
		}
	}
	if f := card.Get(vcard.FieldNickname); f != nil {
		in.Nickname = firstPart(strings.ReplaceAll(f.Value, ",", ";"))
	}
	if f := card.Get(vcard.FieldOrganization); f != nil {
		in.Org = firstPart(f.Value)
	}
	groups := map[string]bool{} // groups of lines Hejmo models: their labels go with them
	add := func(list *[]contact.Line, key string, email bool) {
		for _, f := range card[key] {
			if f.Group != "" {
				groups[f.Group] = true
			}
			if l, ok := lineOf(f, labels, email); ok && !hasLine(*list, l) {
				*list = append(*list, l)
			}
		}
	}
	add(&in.Numbers, vcard.FieldTelephone, false)
	add(&in.Emails, vcard.FieldEmail, true)
	if adrs := card[vcard.FieldAddress]; len(adrs) > 0 {
		f := adrs[0]
		if f.Group != "" {
			groups[f.Group] = true
		}
		p := splitParts(f.Value)
		get := func(i int) string {
			if i < len(p) {
				return strings.TrimSpace(strings.ReplaceAll(p[i], "\n", ", "))
			}
			return ""
		}
		in.Street, in.City, in.Postcode, in.Country = get(2), get(3), get(5), get(6)
		in.HasAddress = in.Street != "" || in.City != "" || in.Postcode != "" || in.Country != ""
	}
	if f := card.Get(vcard.FieldBirthday); f != nil {
		in.BdayDay, in.BdayMonth, in.BdayYear = hvcard.ParseBirthday(strings.TrimSpace(f.Value), len(f.Params["X-APPLE-OMIT-YEAR"]) > 0)
		if in.BdayYear != 0 && in.BdayYear < contact.MinYear {
			in.BdayYear = 0
		}
	}
	in.Note = strings.TrimSpace(text(card.Value(vcard.FieldNote)))
	if f := card.Get(vcard.FieldPhoto); f != nil {
		in.Photo, in.PhotoLink = photoOf(f)
	}
	in.Extra = extraOf(card, groups)
	return in
}

func hasLine(list []contact.Line, l contact.Line) bool {
	for _, h := range list {
		if h == l {
			return true
		}
	}
	return false
}

func photoOf(f *vcard.Field) (data []byte, link bool) {
	raw := strings.TrimSpace(f.Value)
	low := strings.ToLower(raw)
	if strings.HasPrefix(low, "data:") {
		_, after, ok := strings.Cut(raw, ",")
		if !ok {
			return nil, false
		}
		raw = after
	} else if fieldHas(f, "VALUE", "URI") || fieldHas(f, "VALUE", "URL") || strings.Contains(low, "://") {
		return nil, true
	}
	raw = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, strings.ReplaceAll(raw, `\,`, ","))
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(raw, "="))
	}
	if err != nil {
		return nil, false
	}
	return b, false
}

// extraOf keeps every line Hejmo does not model, groups together (a web address with its label) under group
// names of their own so they cannot meet the ones written for numbers.
func extraOf(card vcard.Card, modeledGroups map[string]bool) string {
	keys := make([]string, 0, len(card))
	for k := range card {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	renamed := map[string]string{}
	var out []extraField
	for _, k := range keys {
		for i, f := range card[k] {
			switch {
			case modeled[k] || (k == vcard.FieldAddress && i == 0):
				continue
			case k == "X-ABLABEL" && modeledGroups[f.Group]:
				continue
			}
			g := ""
			if f.Group != "" {
				if renamed[f.Group] == "" {
					renamed[f.Group] = "keep" + strconv.Itoa(len(renamed)+1)
				}
				g = renamed[f.Group]
			}
			out = append(out, extraField{Group: g, Key: k, Params: f.Params, Value: f.Value})
		}
	}
	if len(out) == 0 {
		return ""
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// InputOf is the contact as a save would write it unchanged: everything a parent can set, copied.
func InputOf(c store.Contact) store.ContactInput {
	return store.ContactInput{HejmoName: c.HejmoName, FullName: c.FullName, Street: c.Street, Postcode: c.Postcode, City: c.City, Country: c.Country,
		BdayDay: c.BdayDay, BdayMonth: c.BdayMonth, BdayYear: c.BdayYear, HideBirthday: c.HideBirthday, ForChildren: c.ForChildren,
		Note: c.Note, Numbers: stripE164(c.Numbers), Emails: stripE164(c.Emails), GroupIDs: slices.Clone(c.GroupIDs), Relations: slices.Clone(c.Relations)}
}

func stripE164(in []contact.Line) []contact.Line {
	var out []contact.Line
	for _, l := range in {
		l.E164 = ""
		out = append(out, l)
	}
	return out
}

func fitLines(in []contact.Line) []contact.Line {
	out := contact.CleanLines(in)
	for i := range out {
		out[i].Label = clip(out[i].Label, contact.MaxOwnLabel)
	}
	if len(out) > contact.MaxLines {
		out = out[:contact.MaxLines]
	}
	return out
}

// Apply works out what a card from a phone changes. cur is the contact the card is for, nil for a new one;
// others are the contacts of the household, for naming a new one. For a person of the household only the
// numbers and the address come from the card: name, e-mail addresses, birthday and note are identity data of
// Hejmo (ticket 299). Limits of Hejmo never refuse a card: too long values are cut. changed says whether the
// result differs from what is stored, so an unchanged card writes nothing and keeps its ETag. Photos are not
// part of it.
func Apply(cur *store.Contact, in Incoming, others []store.Contact) (out store.ContactInput, changed bool) {
	if cur == nil {
		name := contactimport.NameFor(hvcard.Card{Nickname: in.Nickname, Full: in.Full, Org: in.Org, Numbers: in.Numbers, Emails: in.Emails}, others)
		if in.Nickname == "" && in.Full == "" && in.Org == "" && len(fitLines(in.Numbers)) == 0 && len(fitLines(in.Emails)) == 0 {
			return store.ContactInput{}, false
		}
		out = store.ContactInput{HejmoName: name, FullName: clip(in.Full, contact.MaxFullName)}
		fillCard(&out, in)
		extra := in.Extra
		out.Extra = &extra
		return out, true
	}
	out = InputOf(*cur)
	base := InputOf(*cur)
	person := cur.IsPerson()
	if !person {
		if n := strings.TrimSpace(in.Nickname); n != "" {
			out.HejmoName = clip(n, contact.MaxName)
		}
		switch full := strings.TrimSpace(in.Full); {
		case full == cur.FullName, cur.FullName == "" && full == cur.HejmoName, full == "" && cur.FullName == "":
		default:
			out.FullName = clip(full, contact.MaxFullName)
		}
	}
	card := out
	fillCard(&card, in)
	out.Numbers, out.Street, out.Postcode, out.City, out.Country = card.Numbers, card.Street, card.Postcode, card.City, card.Country
	if !person {
		out.Emails, out.Note = card.Emails, card.Note
		out.BdayDay, out.BdayMonth, out.BdayYear = card.BdayDay, card.BdayMonth, card.BdayYear
	}
	extra := in.Extra
	out.Extra = &extra
	changed = cur.Extra != extra || !sameInput(base, out)
	return out, changed
}

// fillCard copies the values of a card into the fields it can give, cut to the limits.
func fillCard(out *store.ContactInput, in Incoming) {
	out.Numbers, out.Emails = fitLines(in.Numbers), fitLines(in.Emails)
	out.Street, out.Postcode, out.City, out.Country = "", "", "", ""
	if in.HasAddress {
		out.Street, out.Postcode, out.City, out.Country = in.Street, in.Postcode, in.City, in.Country
	}
	out.Note = clip(in.Note, contact.MaxNote)
	out.BdayDay, out.BdayMonth, out.BdayYear = in.BdayDay, in.BdayMonth, in.BdayYear
	if in.BdayDay != 0 && contact.CheckBirthday(in.BdayDay, in.BdayMonth, in.BdayYear, time.Now()) != "" {
		out.BdayYear = 0
		if contact.CheckBirthday(in.BdayDay, in.BdayMonth, 0, time.Now()) != "" {
			out.BdayDay, out.BdayMonth = 0, 0
		}
	}
}

func sameInput(a, b store.ContactInput) bool {
	a.Numbers, a.Emails, b.Numbers, b.Emails = fitLines(a.Numbers), fitLines(a.Emails), fitLines(b.Numbers), fitLines(b.Emails)
	a.Extra, b.Extra = nil, nil
	return reflect.DeepEqual(a, b)
}
