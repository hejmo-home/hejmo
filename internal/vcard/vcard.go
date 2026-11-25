// Package vcard reads contact files (vCard 2.1, 3.0 and 4.0; ticket 300). It reads what an address book of a
// family needs: names, every number and e-mail address, an address, a birthday, a note and an embedded photo.
// It never follows a link: a photo given as a web address is only noted (ADR 0024). Relations and every other
// property are ignored. Nothing here touches the household's data; see internal/contactimport.
package vcard

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"

	"github.com/hejmo-home/hejmo/internal/contact"
)

// Errors of a whole file.
var (
	// ErrEmpty is a file without any content.
	ErrEmpty = errors.New("vcard: the file is empty")
	// ErrNotVCard is a file that holds no card at all.
	ErrNotVCard = errors.New("vcard: the file is not a contact file")
)

// Problem says why a card cannot be taken over.
type Problem string

// The reasons.
const (
	ProblemEmpty Problem = "empty" // no name, no number and no e-mail address
	ProblemCut   Problem = "cut"   // the card ends in the middle (no END:VCARD)
)

// Card is one contact of a file.
type Card struct {
	Number int // 1-based position in the file
	Line   int // the line BEGIN:VCARD stands on
	// Problem is set when the card cannot be taken over; the other fields hold what could be read.
	Problem Problem
	Version string

	Full, Nickname, Org, Title string
	Numbers, Emails            []contact.Line
	Street, Postcode, City     string
	Country                    string
	// BdayYear is 0 when the year is not known.
	BdayDay, BdayMonth, BdayYear int
	Note                         string
	// Photo is an embedded photo as it stood in the file (not yet checked to be a picture); PhotoLink says the
	// card has a photo given as a web address, which is not fetched.
	Photo     []byte
	PhotoLink bool
}

// Name is the name Hejmo gives the contact: the nickname, else the full name, else the organisation, else
// the first number or e-mail address.
func (c Card) Name() string {
	switch {
	case c.Nickname != "":
		return c.Nickname
	case c.Full != "":
		return c.Full
	case c.Org != "":
		return c.Org
	case len(c.Numbers) > 0:
		return c.Numbers[0].Value
	case len(c.Emails) > 0:
		return c.Emails[0].Value
	}
	return ""
}

// logical is a content line after unfolding, with the physical line it started on.
type logical struct {
	text string
	line int
}

// decodeText turns the bytes of a file into text: UTF-16 with a byte order mark is converted, a UTF-8 byte
// order mark is dropped. Other bytes stay as they are; values with their own CHARSET are converted later.
func decodeText(data []byte) []byte {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}), bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		le := data[0] == 0xFF
		var u []uint16
		for i := 2; i+1 < len(data); i += 2 {
			if le {
				u = append(u, uint16(data[i])|uint16(data[i+1])<<8)
			} else {
				u = append(u, uint16(data[i+1])|uint16(data[i])<<8)
			}
		}
		return []byte(string(utf16.Decode(u)))
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return data[3:]
	}
	return data
}

func splitLines(data []byte) []string {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// unfold joins folded lines. A line that starts with a space or a tab continues the line before it (RFC 6350,
// section 3.2; vCard 2.1 has the same rule). A quoted-printable value that ends in "=" continues on the next
// physical line (a soft line break), whatever that line starts with.
func unfold(lines []string) []logical {
	var out []logical
	softBreak := false
	for i, l := range lines {
		switch {
		case softBreak && len(out) > 0:
			last := &out[len(out)-1]
			last.text = strings.TrimSuffix(last.text, "=") + l
			softBreak = strings.HasSuffix(l, "=")
		case len(out) > 0 && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")):
			out[len(out)-1].text += l[1:]
			softBreak = isQP(out[len(out)-1].text) && strings.HasSuffix(l, "=")
		default:
			out = append(out, logical{l, i + 1})
			softBreak = isQP(l) && strings.HasSuffix(l, "=")
		}
	}
	return out
}

func isQP(line string) bool {
	head, _, ok := strings.Cut(line, ":")
	return ok && strings.Contains(strings.ToUpper(head), "QUOTED-PRINTABLE")
}

type prop struct {
	group, name string
	params      map[string][]string // upper-case names; bare 2.1 parameters under "TYPE"
	raw         string              // the value, not yet decoded
}

func (p prop) has(param, value string) bool {
	for _, v := range p.params[param] {
		if strings.EqualFold(v, value) {
			return true
		}
	}
	return false
}

func (p prop) first(param string) string {
	if v := p.params[param]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// parseProp splits a content line into group, name, parameters and the raw value.
func parseProp(text string) (prop, bool) {
	// The name and the parameters end at the first colon outside quotes.
	inQuote, colon := false, -1
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"':
			inQuote = !inQuote
		case ':':
			if !inQuote {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon < 0 {
		return prop{}, false
	}
	head, raw := text[:colon], text[colon+1:]
	parts := splitUnquoted(head, ';')
	name := strings.TrimSpace(parts[0])
	p := prop{params: map[string][]string{}, raw: raw}
	if g, n, ok := strings.Cut(name, "."); ok {
		p.group, name = strings.ToUpper(g), n
	}
	p.name = strings.ToUpper(name)
	for _, part := range parts[1:] {
		k, v, hasEq := strings.Cut(part, "=")
		k = strings.ToUpper(strings.TrimSpace(k))
		if !hasEq { // a bare parameter of vCard 2.1: a type, or ENCODING/CHARSET shorthand
			switch k {
			case "BASE64", "QUOTED-PRINTABLE":
				p.params["ENCODING"] = append(p.params["ENCODING"], k)
			default:
				p.params["TYPE"] = append(p.params["TYPE"], k)
			}
			continue
		}
		for _, one := range strings.Split(strings.Trim(strings.TrimSpace(v), `"`), ",") {
			p.params[k] = append(p.params[k], strings.TrimSpace(one))
		}
	}
	return p, p.name != ""
}

func splitUnquoted(s string, sep byte) []string {
	var out []string
	inQuote, start := false, 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			inQuote = !inQuote
		case s[i] == sep && !inQuote:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// qpDecode decodes quoted-printable bytes; a broken escape stays as it is.
func qpDecode(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '=' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				out = append(out, byte(v))
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return out
}

// toUTF8 converts bytes in a charset to UTF-8. Without a charset, or with an unknown one, bytes that are valid
// UTF-8 stay and others are read as windows-1252, the charset such files nearly always have.
func toUTF8(b []byte, charset string) string {
	if charset != "" {
		if enc, err := htmlindex.Get(charset); err == nil {
			if out, err := enc.NewDecoder().Bytes(b); err == nil {
				return string(out)
			}
		}
	}
	if utf8.Valid(b) {
		return string(b)
	}
	if enc, err := htmlindex.Get("windows-1252"); err == nil {
		if out, err := enc.NewDecoder().Bytes(b); err == nil {
			return string(out)
		}
	}
	return strings.ToValidUTF8(string(b), "�")
}

// text turns one piece of a raw value into text: quoted-printable decoded, the charset converted, escapes undone.
func (p prop) text(piece string) string {
	b := []byte(piece)
	if p.has("ENCODING", "QUOTED-PRINTABLE") {
		b = qpDecode(piece)
	}
	return strings.ReplaceAll(unescape(toUTF8(b, p.first("CHARSET"))), "\r\n", "\n")
}

// unescape undoes the backslash escapes of vCard text.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n', 'N':
				b.WriteByte('\n')
			default:
				b.WriteByte(s[i])
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// splitEscaped splits on a separator that is not escaped with a backslash.
func splitEscaped(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\':
			i++
		case s[i] == sep:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// parts reads a structured value (N, ADR, ORG): split first, then decoded piece by piece.
func (p prop) parts() []string {
	pieces := splitEscaped(p.raw, ';')
	out := make([]string, len(pieces))
	for i, piece := range pieces {
		out[i] = strings.TrimSpace(p.text(piece))
	}
	return out
}

func (p prop) value() string { return strings.TrimSpace(p.text(p.raw)) }

// Parse reads every card of a file, in order. Cards that cannot be taken over are in the list with a
// Problem. A file that is empty gives ErrEmpty; one without any card gives ErrNotVCard.
func Parse(data []byte) ([]Card, error) {
	data = decodeText(data)
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, ErrEmpty
	}
	lines := unfold(splitLines(data))
	var cards []Card
	var cur *Card
	var props []prop
	finish := func(problem Problem) {
		if cur == nil {
			return
		}
		fill(cur, props)
		if cur.Name() == "" && len(cur.Numbers) == 0 && len(cur.Emails) == 0 && problem == "" {
			problem = ProblemEmpty
		}
		cur.Problem = problem
		cards = append(cards, *cur)
		cur, props = nil, nil
	}
	for _, l := range lines {
		t := strings.TrimSpace(l.text)
		up := strings.ToUpper(t)
		switch {
		case up == "BEGIN:VCARD":
			finish(ProblemCut)
			cur = &Card{Number: len(cards) + 1, Line: l.line}
		case up == "END:VCARD":
			finish("")
		case cur != nil && t != "":
			if p, ok := parseProp(l.text); ok {
				props = append(props, p)
			}
		}
	}
	finish(ProblemCut)
	if len(cards) == 0 {
		return nil, ErrNotVCard
	}
	return cards, nil
}

func fill(c *Card, props []prop) {
	labels := map[string]string{} // group -> own label (Apple: item1.X-ABLabel)
	for _, p := range props {
		if p.name == "X-ABLABEL" && p.group != "" {
			if v := cleanLabel(p.value()); v != "" {
				labels[p.group] = v
			}
		}
		if p.name == "VERSION" {
			c.Version = p.value()
		}
	}
	for _, p := range props {
		switch p.name {
		case "FN":
			if c.Full == "" {
				c.Full = p.value()
			}
		case "NICKNAME":
			if c.Nickname == "" {
				c.Nickname = firstOf(p.parts())
			}
		case "ORG":
			if c.Org == "" {
				c.Org = firstOf(p.parts())
			}
		case "TITLE":
			if c.Title == "" {
				c.Title = p.value()
			}
		case "TEL":
			if v := telValue(p); v != "" {
				addLine(&c.Numbers, numberLine(p, labels[p.group], v))
			}
		case "EMAIL":
			if v := p.value(); v != "" {
				addLine(&c.Emails, emailLine(p, labels[p.group], v))
			}
		case "ADR":
			readAddress(c, p)
		case "BDAY":
			if c.BdayDay == 0 {
				c.BdayDay, c.BdayMonth, c.BdayYear = parseBirthday(p.value(), p.has("X-APPLE-OMIT-YEAR", "1604"))
			}
		case "NOTE":
			if c.Note == "" {
				c.Note = strings.TrimSpace(p.text(p.raw))
			}
		case "PHOTO":
			readPhoto(c, p)
		}
	}
	if c.Full == "" { // no FN: the structured name
		for _, p := range props {
			if p.name == "N" {
				c.Full = nameFromN(p.parts())
				break
			}
		}
	}
}

func firstOf(parts []string) string {
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// nameFromN writes the structured name as one: prefix, given, additional, family, suffix.
func nameFromN(n []string) string {
	get := func(i int) string {
		if i < len(n) {
			return n[i]
		}
		return ""
	}
	var words []string
	for _, w := range []string{get(3), get(1), get(2), get(0)} {
		if w != "" {
			words = append(words, w)
		}
	}
	out := strings.Join(words, " ")
	if suffix := get(4); suffix != "" && out != "" {
		out += ", " + suffix
	}
	return out
}

// cleanLabel turns Apple's own label markers ("_$!<Mobile>!$_") into the word inside.
func cleanLabel(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "_$!<") && strings.HasSuffix(s, ">!$_") {
		return strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(s, "_$!<"), ">!$_"))
	}
	return s
}

func telValue(p prop) string {
	v := p.value()
	if rest, ok := strings.CutPrefix(strings.ToLower(v), "tel:"); ok { // vCard 4.0 may give a tel: URI
		v, _, _ = strings.Cut(v[len(v)-len(rest):], ";")
	}
	return strings.TrimSpace(v)
}

func addLine(list *[]contact.Line, l contact.Line) {
	for _, have := range *list {
		if have.Kind == l.Kind && have.Label == l.Label && have.Value == l.Value {
			return
		}
	}
	*list = append(*list, l)
}

func limit(label string) string {
	if r := []rune(label); len(r) > contact.MaxOwnLabel {
		return string(r[:contact.MaxOwnLabel])
	}
	return label
}

// ownLabel is the word of a group label: the kind a known word stands for is returned as a kind, anything
// else stays an own label.
func fromLabel(label string, email bool) (contact.Kind, string) {
	switch strings.ToLower(label) {
	case "mobile", "cell", "iphone", "mobil", "handy":
		if !email {
			return contact.Cell, ""
		}
		return contact.Other, ""
	case "home", "zuhause", "privat":
		return contact.Home, ""
	case "work", "arbeit":
		return contact.Work, ""
	case "other", "sonstige":
		return contact.Other, ""
	}
	return contact.Custom, limit(label)
}

func numberLine(p prop, label, value string) contact.Line {
	if label != "" {
		k, l := fromLabel(label, false)
		return contact.Line{Kind: k, Label: l, Value: value}
	}
	types := map[string]bool{}
	for _, t := range p.params["TYPE"] {
		types[strings.ToUpper(t)] = true
	}
	switch {
	case types["FAX"]:
		return contact.Line{Kind: contact.Custom, Label: "Fax", Value: value}
	case types["CELL"] || types["IPHONE"] || types["MOBILE"]:
		return contact.Line{Kind: contact.Cell, Value: value}
	case types["WORK"]:
		return contact.Line{Kind: contact.Work, Value: value}
	case types["HOME"]:
		return contact.Line{Kind: contact.Home, Value: value}
	}
	return contact.Line{Kind: contact.Other, Value: value}
}

func emailLine(p prop, label, value string) contact.Line {
	if label != "" {
		k, l := fromLabel(label, true)
		return contact.Line{Kind: k, Label: l, Value: value}
	}
	switch {
	case p.has("TYPE", "WORK"):
		return contact.Line{Kind: contact.Work, Value: value}
	case p.has("TYPE", "HOME"):
		return contact.Line{Kind: contact.Home, Value: value}
	}
	return contact.Line{Kind: contact.Other, Value: value}
}

func readAddress(c *Card, p prop) {
	if c.Street != "" || c.City != "" || c.Postcode != "" || c.Country != "" {
		return // the first address
	}
	a := p.parts()
	get := func(i int) string {
		if i < len(a) {
			return a[i]
		}
		return ""
	}
	c.Street = strings.ReplaceAll(get(2), "\n", ", ")
	c.City, c.Postcode, c.Country = get(3), get(5), get(6)
}

// parseBirthday reads 1950-03-14, 19500314, --0314, --03-14 and a date with a time. A year of 1604, or
// the mark of Apple, means the year is not known. A date that does not exist gives zeros.
func parseBirthday(v string, omitYear bool) (day, month, year int) {
	v = strings.TrimSpace(v)
	if i := strings.IndexAny(v, "Tt"); i > 0 {
		v = v[:i]
	}
	digits := strings.ReplaceAll(v, "-", "")
	var ys, ms, ds string
	switch {
	case strings.HasPrefix(v, "--") && len(digits) == 4:
		ms, ds = digits[:2], digits[2:]
	case len(digits) == 8:
		ys, ms, ds = digits[:4], digits[4:6], digits[6:]
	default:
		return 0, 0, 0
	}
	m, err1 := strconv.Atoi(ms)
	d, err2 := strconv.Atoi(ds)
	y := 0
	var err3 error
	if ys != "" {
		y, err3 = strconv.Atoi(ys)
	}
	if err1 != nil || err2 != nil || err3 != nil || m < 1 || m > 12 || d < 1 || d > contact.DaysIn(m, y) {
		return 0, 0, 0
	}
	if omitYear || y == 1604 {
		y = 0
	}
	return d, m, y
}

func readPhoto(c *Card, p prop) {
	raw := strings.TrimSpace(p.raw)
	isLink := p.has("VALUE", "URI") || p.has("VALUE", "URL") || strings.HasPrefix(strings.ToLower(raw), "http://") || strings.HasPrefix(strings.ToLower(raw), "https://") || strings.HasPrefix(strings.ToLower(raw), "ftp://")
	if low := strings.ToLower(raw); strings.HasPrefix(low, "data:") {
		isLink = false
		if _, after, ok := strings.Cut(raw, ","); ok {
			raw = after
		} else {
			return
		}
	}
	if isLink {
		c.PhotoLink = true
		return
	}
	raw = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, raw)
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(raw, "="))
	}
	if err != nil || len(b) == 0 {
		return
	}
	if len(c.Photo) == 0 {
		c.Photo = b
	}
}

// ParseBirthday reads the value of a BDAY property (see parseBirthday): the day, the month and the year, which is 0
// when it is not known. omitYear says the property carries Apple's X-APPLE-OMIT-YEAR mark.
func ParseBirthday(v string, omitYear bool) (day, month, year int) { return parseBirthday(v, omitYear) }
