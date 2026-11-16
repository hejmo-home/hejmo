// Package places is the list of towns, villages and districts of Germany,
// Austria and Switzerland that ships inside the box (ADR 0023). A parent picks
// the home location from it while typing; nothing goes to the internet.
//
// The list is data/places.tsv.gz, built by tools/placelist from GeoNames (CC BY
// 4.0, data/LICENSE-GeoNames.txt) and embedded in the binary. It is parsed
// once into memory (about 22,000 places).
package places

//go:generate go run ../../tools/placelist -out data/places.tsv.gz

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/region"
)

//go:embed data/places.tsv.gz
var embedded []byte

// MaxResults is how many places a search lists.
const MaxResults = 5

// minQuery is the number of letters from which a search starts.
const minQuery = 2

// Place is a place as the page offers it.
type Place struct {
	ID         int // the GeoNames id
	Name       string
	Region     string // the line under the name, in the page language: "district of München, Bayern, Germany"
	Lat, Lon   float64
	RegionCode string // ISO 3166-2, one of the regions of the setup form
}

type entry struct {
	id         int
	name       string
	admin2     string
	lat, lon   float64
	population int
	country    string
	region     string
	parent     int
	// keys are the folded names (name, ascii name, alternates): de is the form
	// with ü = ue, plain the one with ü = u.
	de, plain []string
}

// List is the loaded list of places. It is read-only after Parse.
type List struct {
	date    string
	entries []*entry
	byID    map[int]*entry
	tr      *i18n.Bundle
}

var (
	once    sync.Once
	shared  *List
	sharedE error
)

// Embedded returns the list that ships in the binary, parsed on first use and
// shared afterwards. It panics if the embedded data is broken (a build error).
func Embedded() *List {
	once.Do(func() {
		var zr *gzip.Reader
		zr, sharedE = gzip.NewReader(bytes.NewReader(embedded))
		if sharedE == nil {
			shared, sharedE = Parse(zr)
		}
	})
	if sharedE != nil {
		panic("places: embedded list: " + sharedE.Error())
	}
	return shared
}

// EmbeddedSize is the size of the embedded file, gzipped.
func EmbeddedSize() int { return len(embedded) }

// Parse reads the tab-separated list (already decompressed): a header line
// that starts with "# GeoNames extract of <date>", then one place per line.
func Parse(r io.Reader) (*List, error) {
	l := &List{byID: map[int]*entry{}, tr: i18n.MustLoad()}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if n == 1 {
			if !strings.HasPrefix(line, "# ") {
				return nil, errors.New("places: the header line is missing")
			}
			l.date = headerDate(line)
			continue
		}
		e, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("places: line %d: %w", n, err)
		}
		l.entries = append(l.entries, e)
		l.byID[e.id] = e
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(l.entries) == 0 {
		return nil, errors.New("places: the list is empty")
	}
	return l, nil
}

// headerDate reads the dump date out of "# GeoNames extract of 2026-11-15, ...".
func headerDate(h string) string {
	_, rest, ok := strings.Cut(h, "extract of ")
	if !ok {
		return ""
	}
	d, _, _ := strings.Cut(rest, ",")
	return strings.TrimSpace(d)
}

func parseLine(line string) (*entry, error) {
	c := strings.Split(line, "\t")
	if len(c) != 11 {
		return nil, fmt.Errorf("%d columns, want 11", len(c))
	}
	e := &entry{name: c[1], admin2: c[9], country: c[7], region: c[8]}
	var err error
	if e.id, err = strconv.Atoi(c[0]); err != nil || e.id <= 0 {
		return nil, fmt.Errorf("id %q", c[0])
	}
	if e.lat, err = strconv.ParseFloat(c[4], 64); err != nil {
		return nil, fmt.Errorf("latitude %q", c[4])
	}
	if e.lon, err = strconv.ParseFloat(c[5], 64); err != nil {
		return nil, fmt.Errorf("longitude %q", c[5])
	}
	if e.population, err = strconv.Atoi(c[6]); err != nil {
		return nil, fmt.Errorf("population %q", c[6])
	}
	if c[10] != "" {
		if e.parent, err = strconv.Atoi(c[10]); err != nil {
			return nil, fmt.Errorf("parent %q", c[10])
		}
	}
	names := []string{c[1], c[2]}
	if c[3] != "" {
		names = append(names, strings.Split(c[3], "|")...)
	}
	seen := map[string]bool{}
	for _, n := range names {
		d, p := fold(n, true), fold(n, false)
		if d == "" || seen[d+"\x00"+p] {
			continue
		}
		seen[d+"\x00"+p] = true
		e.de, e.plain = append(e.de, d), append(e.plain, p)
	}
	return e, nil
}

// Date is the date of the GeoNames dump the list was made from (2006-01-02).
func (l *List) Date() string { return l.date }

// Len is the number of places.
func (l *List) Len() int { return len(l.entries) }

// RegionCodes lists the distinct region codes of the places.
func (l *List) RegionCodes() []string {
	seen := map[string]bool{}
	for _, e := range l.entries {
		seen[e.region] = true
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// OrphanParents counts districts whose parent is not in the list.
func (l *List) OrphanParents() int {
	n := 0
	for _, e := range l.entries {
		if e.parent != 0 && l.byID[e.parent] == nil {
			n++
		}
	}
	return n
}

// Get returns the place with a GeoNames id, labelled in lang. When another
// place of the list has the same name in the same region, the label adds the
// area (admin2) so the two can be told apart.
func (l *List) Get(id int, lang string) (Place, bool) {
	e := l.byID[id]
	if e == nil {
		return Place{}, false
	}
	return l.place(e, lang, l.sharesName(e)), true
}

// sharesName reports whether another place has e's name in e's region.
func (l *List) sharesName(e *entry) bool {
	for _, o := range l.entries {
		if o != e && o.region == e.region && o.name == e.name {
			return true
		}
	}
	return false
}

// Search returns at most MaxResults places that match query, and whether more
// matched. It starts at two letters. Matching is case- and diacritic-
// insensitive (ü = ue = u, ß = ss) over the name, the ASCII name and the
// alternate names; a name that starts with the query comes before one that
// has a word starting with it, then larger places come first.
func (l *List) Search(query, lang string) (found []Place, more bool) {
	qd, qp := fold(query, true), fold(query, false)
	if len([]rune(strings.ReplaceAll(qd, " ", ""))) < minQuery {
		return nil, false
	}
	type hit struct {
		e    *entry
		rank int
	}
	var hits []hit
	for _, e := range l.entries {
		rank := 2
		for i := range e.de {
			if r := rankOf(e.de[i], qd); r < rank {
				rank = r
			}
			if r := rankOf(e.plain[i], qp); r < rank {
				rank = r
			}
		}
		if rank < 2 {
			hits = append(hits, hit{e, rank})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		switch {
		case a.rank != b.rank:
			return a.rank < b.rank
		case a.e.population != b.e.population:
			return a.e.population > b.e.population
		case a.e.name != b.e.name:
			return a.e.name < b.e.name
		}
		return a.e.id < b.e.id
	})
	if more = len(hits) > MaxResults; more {
		hits = hits[:MaxResults]
	}
	type nameRegion struct{ name, region string }
	count := map[nameRegion]int{}
	for _, h := range hits {
		count[nameRegion{h.e.name, h.e.region}]++
	}
	for _, h := range hits {
		found = append(found, l.place(h.e, lang, count[nameRegion{h.e.name, h.e.region}] > 1))
	}
	return found, more
}

// rankOf is 0 when key starts with q, 1 when a later word of key does, else 2.
func rankOf(key, q string) int {
	switch {
	case strings.HasPrefix(key, q):
		return 0
	case strings.Contains(key, " "+q):
		return 1
	}
	return 2
}

// place builds the page's view of an entry.
func (l *List) place(e *entry, lang string, withArea bool) Place {
	var parts []string
	if p := l.byID[e.parent]; p != nil {
		parts = append(parts, l.tr.Format(lang, i18n.Msg("place.district", "c", p.name)))
	}
	if withArea {
		parts = append(parts, e.admin2)
	}
	if r, ok := region.Lookup(e.region); ok {
		parts = append(parts, r.Name)
	}
	parts = append(parts, l.tr.T(lang, "setup.country."+e.country))
	return Place{ID: e.id, Name: e.name, Region: join(e.name, parts), Lat: e.lat, Lon: e.lon, RegionCode: e.region}
}

// join writes the parts after a name, leaving out empty ones and ones that
// repeat the name or an earlier part.
func join(name string, parts []string) string {
	seen := map[string]bool{name: true}
	var out []string
	for _, p := range parts {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

// fold lowercases s, takes the diacritics off and turns everything that is not
// a letter or digit into one space. With german, ä ö ü are written ae oe ue
// (Müller = Mueller); without it they lose their dots (Muller). ß is ss either way.
func fold(s string, german bool) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r == 'ä' && german:
			b.WriteString("ae")
		case r == 'ö' && german:
			b.WriteString("oe")
		case r == 'ü' && german:
			b.WriteString("ue")
		case r == 'ß':
			b.WriteString("ss")
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if base, ok := baseLetter[r]; ok {
				b.WriteString(base)
			} else {
				b.WriteRune(r)
			}
		default:
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
	}
	return strings.TrimRight(b.String(), " ")
}

// baseLetter maps accented letters (and a few special ones) to the letters
// they are written with in ASCII.
var baseLetter = func() map[rune]string {
	m := map[rune]string{}
	for base, accented := range map[string]string{
		"a": "àáâãäåāăą", "c": "çćĉċč", "d": "ďđ", "e": "èéêëēĕėęě", "g": "ĝğġģ", "h": "ĥħ", "i": "ìíîïĩīĭįı",
		"j": "ĵ", "k": "ķ", "l": "ĺļľŀł", "n": "ñńņňŉ", "o": "òóôõöōŏőø", "r": "ŕŗř", "s": "śŝşšș", "t": "ţťŧț",
		"u": "ùúûüũūŭůűų", "w": "ŵ", "y": "ýÿŷ", "z": "źżž",
	} {
		for _, r := range accented {
			m[r] = base
		}
	}
	m['æ'], m['œ'], m['þ'] = "ae", "oe", "th"
	return m
}()
