package nutrition

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Source is the database a food comes from.
type Source string

// The two databases that ship in the box.
const (
	// BLS is the Bundeslebensmittelschlüssel 4.0 of the Max Rubner-Institut (CC BY 4.0).
	BLS Source = "bls"
	// USDA is USDA FoodData Central, Foundation Foods and SR Legacy (CC0).
	USDA Source = "usda"
)

// Food is a food of one of the databases with its values per 100 g.
type Food struct {
	Source Source
	Code   string // the BLS code or the FDC id
	De, En string // USDA foods have no German name
	Per100 Values
}

// ID is the key that stores and the API use: "bls:B101000", "usda:170000".
func (f Food) ID() string { return string(f.Source) + ":" + f.Code }

// Name is the name in the language of the page; a food without a name in that
// language shows the other one.
func (f Food) Name(lang string) string {
	if lang == "en" {
		if f.En != "" {
			return f.En
		}
		return f.De
	}
	if f.De != "" {
		return f.De
	}
	return f.En
}

// MaxID is the longest food id.
const MaxID = 40

//go:embed data/foods.tsv.gz
var embedded []byte

var (
	loadOnce sync.Once
	foods    []Food
	byID     map[string]int
	folded   []string // De + " | " + En, folded, per food
)

func load() {
	zr, err := gzip.NewReader(bytes.NewReader(embedded))
	if err != nil {
		panic("nutrition: " + err.Error())
	}
	byID = map[string]int{}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f, err := parseLine(line)
		if err != nil {
			panic("nutrition: " + err.Error())
		}
		byID[f.ID()] = len(foods)
		foods = append(foods, f)
		folded = append(folded, Fold(f.De)+" | "+Fold(f.En))
	}
	if err := sc.Err(); err != nil {
		panic("nutrition: " + err.Error())
	}
}

func parseLine(line string) (Food, error) {
	c := strings.Split(line, "\t")
	if len(c) != 4+int(NumKeys) {
		return Food{}, fmt.Errorf("malformed line in foods.tsv.gz: %q", line)
	}
	f := Food{Source: Source(c[0]), Code: c[1], De: c[2], En: c[3]}
	if f.Source != BLS && f.Source != USDA {
		return Food{}, fmt.Errorf("unknown source in foods.tsv.gz: %q", line)
	}
	for k := Key(0); k < NumKeys; k++ {
		if s := c[4+int(k)]; s != "" { // empty: the source does not give it, never 0
			x, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return Food{}, fmt.Errorf("malformed number in foods.tsv.gz: %q", line)
			}
			f.Per100.Set(k, x)
		}
	}
	return f, nil
}

// Count is the number of foods that ship.
func Count() int {
	loadOnce.Do(load)
	return len(foods)
}

// All lists every food, BLS first, in the order of the file.
func All() []Food {
	loadOnce.Do(load)
	return foods
}

// Lookup finds a food by its id.
func Lookup(id string) (Food, bool) {
	loadOnce.Do(load)
	i, ok := byID[id]
	if !ok {
		return Food{}, false
	}
	return foods[i], true
}

var foldReplacer = strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "ß", "ss", "é", "e", "è", "e", "ê", "e", "á", "a", "à", "a", "â", "a", "í", "i", "ó", "o", "ô", "o", "ú", "u", "ñ", "n", "ç", "c",
	"ae", "a", "oe", "o", "ue", "u")

// Fold lowers case and treats ä, ö, ü like a, o, u and like ae, oe, ue, so
// "Müsli" and "muesli" are one word.
func Fold(s string) string { return foldReplacer.Replace(strings.ToLower(strings.TrimSpace(s))) }

// MinQuery is the number of letters from which a search starts, MaxQuery the
// most it reads.
const (
	MinQuery = 2
	MaxQuery = 100
)

// Search finds up to max foods whose name (German or English) holds every word
// of q, in any order: a name that is the query comes first, then names that
// start with it, then names with a word that starts with it, then the rest;
// shorter names first within each, BLS before USDA.
func Search(q, lang string, max int) []Food {
	loadOnce.Do(load)
	fq := Fold(q)
	if utf8.RuneCountInString(fq) < MinQuery {
		return nil
	}
	if r := []rune(fq); len(r) > MaxQuery {
		fq = string(r[:MaxQuery])
	}
	words := strings.Fields(fq)
	type hit struct {
		i, rank, size int
	}
	var hits []hit
	for i := range foods {
		h := folded[i]
		ok := true
		for _, w := range words {
			if !strings.Contains(h, w) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		name := Fold(foods[i].Name(lang))
		rank := 3
		switch {
		case name == fq:
			rank = 0
		case strings.HasPrefix(name, fq):
			rank = 1
		case allWordPrefixes(name, words):
			rank = 2
		}
		hits = append(hits, hit{i, rank, utf8.RuneCountInString(name)})
	}
	sort.SliceStable(hits, func(a, b int) bool {
		x, y := hits[a], hits[b]
		if x.rank != y.rank {
			return x.rank < y.rank
		}
		if x.size != y.size {
			return x.size < y.size
		}
		return foods[x.i].Source == BLS && foods[y.i].Source != BLS
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]Food, len(hits))
	for n, h := range hits {
		out[n] = foods[h.i]
	}
	return out
}

func allWordPrefixes(name string, words []string) bool {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == ',' || r == '(' || r == ')' || r == '-' || r == '/' })
outer:
	for _, w := range words {
		for _, p := range parts {
			if strings.HasPrefix(p, w) {
				continue outer
			}
		}
		return false
	}
	return true
}
