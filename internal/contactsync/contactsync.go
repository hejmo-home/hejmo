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
	"encoding/xml"
	"fmt"
	"reflect"
	"regexp"
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
//
// Raw is everything after the key as the phone wrote it (parameters, colon and value), so the line goes back
// byte for byte; go-vcard cannot write it back (it escapes what it should leave). Params and Value are the
// older form, read when Raw is empty. First marks the first address of the card: Hejmo models its street, city,
// postcode and country, the rest of it (TYPE, label, region) is kept here.
type extraField struct {
	Group  string              `json:"g,omitempty"`
	Key    string              `json:"k"`
	Params map[string][]string `json:"p,omitempty"`
	Value  string              `json:"v,omitempty"`
	Raw    string              `json:"r,omitempty"`
	First  bool                `json:"a,omitempty"`
	// For is, on a kept N, the full name it belongs to: N goes back only while the name is that one.
	For string `json:"f,omitempty"`
}

// rawKey is the key of a placeholder line in a card: its value is a kept line, base64 so the encoder leaves it
// alone. Text and ExpandRaw turn it back into the line itself.
const rawKey = "X-HEJMO-RAW"

var (
	rawInText = regexp.MustCompile(`(?m)^` + rawKey + `:([A-Za-z0-9_-]*)`)
	rawInXML  = regexp.MustCompile(`&#xA;` + rawKey + `:([A-Za-z0-9_-]*)`)
)

func rawPlaceholder(line string) *vcard.Field {
	return &vcard.Field{Value: base64.RawURLEncoding.EncodeToString([]byte(line))}
}

// ExpandRaw writes the kept lines of a card into its text. inXML is for card text inside an XML answer (REPORT),
// where line breaks are character references. Only a placeholder that begins a line counts, never one inside a
// value.
func ExpandRaw(text string, inXML bool) string {
	re, lead := rawInText, ""
	if inXML {
		re, lead = rawInXML, "&#xA;"
	}
	return re.ReplaceAllStringFunc(text, func(m string) string {
		b, err := base64.RawURLEncoding.DecodeString(m[len(lead)+len(rawKey)+1:])
		if err != nil {
			return lead
		}
		if !inXML {
			return string(b)
		}
		var esc bytes.Buffer
		_ = xml.EscapeText(&esc, b)
		return lead + esc.String()
	})
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
	card.SetValue(vcard.FieldNickname, c.HejmoName)
	hasAddress := c.Street != "" || c.City != "" || c.Postcode != "" || c.Country != ""
	var extras []extraField
	if c.Extra != "" {
		_ = json.Unmarshal([]byte(c.Extra), &extras)
	}
	// Hejmo does not store N: the phone's own goes back while the full name is the one it came with, else N is
	// made from the full name.
	keptN := slices.IndexFunc(extras, func(e extraField) bool { return e.Key == "N" && e.Raw != "" && e.For == strings.TrimSpace(full) })
	if keptN < 0 {
		card.SetValue(vcard.FieldName, structuredName(full))
	}
	firstAddress := slices.IndexFunc(extras, func(e extraField) bool { return e.First })
	if hasAddress && firstAddress < 0 {
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
	// Without an address in Hejmo the kept first address and its label are not sent.
	dropGroup := ""
	if firstAddress >= 0 && !hasAddress {
		dropGroup = extras[firstAddress].Group
	}
	for i, e := range extras {
		switch {
		case e.Key == "N" && i != keptN:
			continue // a stale N
		case e.First && !hasAddress:
			continue
		case dropGroup != "" && e.Group == dropGroup:
			continue
		case e.Raw == "": // the older form
			if e.Key == "X-ABLABEL" {
				e.Key = "X-ABLabel"
			}
			card.Add(e.Key, &vcard.Field{Group: e.Group, Params: e.Params, Value: e.Value})
		case e.First:
			card.Add(rawKey, rawPlaceholder(keptLine(e, addressParts(e.Raw, c))))
		default:
			card.Add(rawKey, rawPlaceholder(keptLine(e, e.Raw)))
		}
	}
	return card
}

// keptLine is a kept line as it is written: group, key, then the rest as it came.
func keptLine(e extraField, rest string) string {
	if e.Group != "" {
		return e.Group + "." + e.Key + rest
	}
	return e.Key + rest
}

var valueEscaper = strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, "\n", `\n`, "\r", "")

// addressParts is the rest of a kept address line with the parts Hejmo models replaced: street, city, postcode
// and country. The parameters and the other parts (post box, extended address, region) stay as they came.
func addressParts(rest string, c store.Contact) string {
	params, value := splitRest(rest)
	parts := splitRaw(value)
	for len(parts) < 7 {
		parts = append(parts, "")
	}
	parts[2], parts[3], parts[5], parts[6] = valueEscaper.Replace(c.Street), valueEscaper.Replace(c.City), valueEscaper.Replace(c.Postcode), valueEscaper.Replace(c.Country)
	return params + ":" + strings.Join(parts, ";")
}

// splitRest splits what follows a key into parameters (with their leading ;) and value, at the first colon
// outside quotes.
func splitRest(rest string) (params, value string) {
	quoted := false
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case '"':
			quoted = !quoted
		case ':':
			if !quoted {
				return rest[:i], rest[i+1:]
			}
		}
	}
	return rest, ""
}

// splitRaw splits a value at its semicolons, leaving the escapes in the parts.
func splitRaw(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case ';':
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
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
	return ExpandRaw(b.String(), false), err
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

// IsGroup says a card is a group of contacts, not a contact: iOS writes X-ADDRESSBOOKSERVER-KIND:group, vCard
// 4.0 and DAVx5 KIND:group.
func IsGroup(card vcard.Card) bool {
	for _, k := range []string{vcard.FieldKind, "X-ADDRESSBOOKSERVER-KIND"} {
		for _, f := range card[k] {
			if strings.EqualFold(strings.TrimSpace(f.Value), "group") {
				return true
			}
		}
	}
	return false
}

// Decode reads a card a phone saved.
func Decode(card vcard.Card) Incoming { return DecodeRaw(card, nil) }

// DecodeRaw reads a card a phone saved, together with the text it came in (nil: written from the card, which
// loses how values were escaped). The lines Hejmo does not model are kept from the text.
func DecodeRaw(card vcard.Card, raw []byte) Incoming {
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
	overflow := map[string]map[int]bool{}
	add := func(list *[]contact.Line, key string, email bool) {
		for n, f := range card[key] {
			l, ok := lineOf(f, labels, email)
			if ok && len(*list) >= contact.MaxLines && !hasLine(*list, l) {
				// Over the limit of Hejmo: the line stays on the card as it came, with its label.
				if overflow[key] == nil {
					overflow[key] = map[int]bool{}
				}
				overflow[key][n] = true
				continue
			}
			if f.Group != "" {
				groups[f.Group] = true
			}
			if ok && !hasLine(*list, l) {
				*list = append(*list, l)
			}
		}
	}
	add(&in.Numbers, vcard.FieldTelephone, false)
	add(&in.Emails, vcard.FieldEmail, true)
	if adrs := card[vcard.FieldAddress]; len(adrs) > 0 {
		f := adrs[0]
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
	in.Extra = extraOf(rawLinesOf(card, raw), groups, in.Full, overflow)
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

// rawLine is a line of a card as it was written: group, key and everything after the key.
type rawLine struct {
	Group, Key, Rest string
	Nth              int // the place among the lines of the same key
}

// rawLinesOf reads the lines of the first card in raw, unfolded. Without raw the card is written out first.
func rawLinesOf(card vcard.Card, raw []byte) []rawLine {
	if raw == nil {
		c := card
		if c.Get(vcard.FieldVersion) == nil {
			c = vcard.Card{}
			for k, v := range card {
				c[k] = v
			}
			c.SetValue(vcard.FieldVersion, string(V3))
		}
		var b bytes.Buffer
		if vcard.NewEncoder(&b).Encode(c) != nil {
			return nil
		}
		raw = b.Bytes()
	}
	var unfolded []string
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimRight(l, "\r")
		if len(l) > 0 && (l[0] == ' ' || l[0] == '\t') && len(unfolded) > 0 {
			unfolded[len(unfolded)-1] += l[1:]
			continue
		}
		unfolded = append(unfolded, l)
	}
	var out []rawLine
	begun := false
	for _, l := range unfolded {
		l = strings.NewReplacer("\r", "").Replace(l)
		var rl rawLine
		if i := strings.IndexAny(l, ".;:"); i >= 0 && l[i] == '.' {
			rl.Group, l = l[:i], l[i+1:]
		}
		i := strings.IndexAny(l, ";:")
		if i < 0 {
			continue
		}
		rl.Key, rl.Rest = l[:i], l[i:]
		switch uk := strings.ToUpper(rl.Key); {
		case !begun:
			begun = uk == "BEGIN"
			continue
		case uk == "END":
			return out
		}
		out = append(out, rl)
	}
	return out
}

// extraOf keeps every line Hejmo does not model as it was written, groups together (a web address with its
// label) under group names of their own so they cannot meet the ones written for numbers.
//
// N is kept with the full name it came with (fn). Of the numbers and addresses only those over the limit of ten
// (overflow, by their place among the lines of their key) are kept: the others are modeled.
func extraOf(lines []rawLine, modeledGroups map[string]bool, fn string, overflow map[string]map[int]bool) string {
	sorted := slices.Clone(lines)
	seen := map[string]int{}
	for i := range sorted {
		uk := strings.ToUpper(sorted[i].Key)
		sorted[i].Nth = seen[uk]
		seen[uk]++
	}
	sort.SliceStable(sorted, func(i, j int) bool { return strings.ToUpper(sorted[i].Key) < strings.ToUpper(sorted[j].Key) })
	renamed := map[string]string{}
	var out []extraField
	addresses := 0
	for _, l := range sorted {
		uk := strings.ToUpper(l.Key)
		first := false
		switch {
		case uk == "N" && l.Rest == ":"+structuredName(fn):
			continue // what Hejmo would write itself
		case uk == "N":
			out = append(out, extraField{Key: l.Key, Raw: l.Rest, For: fn})
			continue
		case modeled[uk] && !overflow[uk][l.Nth]:
			continue
		case uk == vcard.FieldAddress:
			first = addresses == 0
			addresses++
			if first && l.Group == "" && plainAddress(l.Rest) {
				continue // nothing beyond what Hejmo models
			}
		case uk == "X-ABLABEL" && modeledGroups[l.Group]:
			continue
		}
		g := ""
		if l.Group != "" {
			if renamed[l.Group] == "" {
				renamed[l.Group] = "keep" + strconv.Itoa(len(renamed)+1)
			}
			g = renamed[l.Group]
		}
		key := l.Key
		if uk == "X-ABLABEL" {
			key = "X-ABLabel" // as Apple writes it
		}
		out = append(out, extraField{Group: g, Key: key, Raw: l.Rest, First: first})
	}
	if len(out) == 0 {
		return ""
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// plainAddress says an address line has no parameters and nothing in the parts Hejmo does not model.
func plainAddress(rest string) bool {
	params, value := splitRest(rest)
	parts := splitRaw(value)
	return params == "" && len(parts) <= 7 && (len(parts) < 1 || parts[0] == "") && (len(parts) < 2 || parts[1] == "") && (len(parts) < 5 || parts[4] == "")
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
