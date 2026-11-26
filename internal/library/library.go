// Package library is the domain of the article library (ADR 0019, ticket
// 164): what an article is, the articles Hejmo ships, the symbols they can
// have, the rules for an own article, and what a household's diets and
// allergies make of them (who it suits, a warning in words, hiding). It is
// pure: no storage, no pages.
package library

import (
	"cmp"
	_ "embed"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Kind says what is in an article. There is no default: a wrong default would
// suggest meat to someone who eats none.
type Kind string

// The kinds.
const (
	Meat    Kind = "meat"
	Fish    Kind = "fish"
	Veg     Kind = "veg"
	Vegan   Kind = "vegan"
	NonFood Kind = "nonfood"
)

// Kinds in the order the pages show them.
var Kinds = []Kind{Meat, Fish, Veg, Vegan, NonFood}

// ValidKind reports whether s is a kind.
func ValidKind(s string) bool { return slices.Contains(Kinds, Kind(s)) }

// Fits reports whether an article of this kind suits someone with this diet.
// Something that is not food suits everyone.
func (k Kind) Fits(d food.Diet) bool {
	switch k {
	case Meat:
		return d == food.Omnivore
	case Fish:
		return d == food.Omnivore || d == food.Pescatarian
	case Veg:
		return d != food.Vegan
	case Vegan, NonFood:
		return true
	}
	return false
}

// Depts are the 13 departments in the order of a shop (catalogue keys
// "dept.<key>").
var Depts = []string{"produce", "bakery", "dairy", "meat_fish", "frozen", "pantry", "baking_spices", "sweets", "drinks", "drugstore", "household", "baby", "other"}

// DefaultDept is the department an own article starts in.
const DefaultDept = "other"

var deptSymbols = map[string]string{
	"produce": "🍎", "bakery": "🥖", "dairy": "🧀", "meat_fish": "🥩", "frozen": "🧊", "pantry": "🥫", "baking_spices": "🧂",
	"sweets": "🍫", "drinks": "🥤", "drugstore": "🧴", "household": "🧽", "baby": "🍼", "other": "📦",
}

// DeptSymbol is the symbol of a department, which an article without a
// picture or symbol of its own shows.
func DeptSymbol(dept string) string { return deptSymbols[dept] }

// ValidDept reports whether s is a department.
func ValidDept(s string) bool { return slices.Contains(Depts, s) }

// Limits.
const (
	MaxName = 60
	// MaxPicture is the largest photo or web image Hejmo takes, in bytes.
	MaxPicture = 15 << 20
)

// Article is a thing that can be put on a list.
type Article struct {
	// ID is the key of a shipped article, the id of an own one.
	ID string
	// Own is true for an article the household made.
	Own bool
	// De and En are the names; an own article has one name for both.
	De, En    string
	Dept      string
	Kind      Kind
	Symbol    string   // the emoji; an own article may have none
	Allergens []string // keys of food.Allergens, in that order
	Units     []string // one or two default units, the first is preselected
	// Pantry marks an article usually at home (salt, oil): recipes leave it
	// out of the shopping list by default. For a shipped article this is the
	// shipped default; the household's own choice is applied on top of it.
	Pantry bool
}

// Name is the name in the language of the page.
func (a Article) Name(lang string) string {
	if lang == "en" && a.En != "" {
		return a.En
	}
	return cmp.Or(a.De, a.En)
}

var fold = strings.NewReplacer("ä", "a", "ö", "o", "ü", "u", "ß", "ss")

// Fold lowers case and treats ä, ö, ü like a, o, u, so searching ignores them.
func Fold(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if !hasNonASCII(s) {
		return s // no ä, ö, ü or ß in it: the replacer would only copy it
	}
	return fold.Replace(s)
}

func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

// Matches reports whether the article's name in either language holds q.
func Matches(a Article, q string) bool {
	q = Fold(q)
	if q == "" {
		return true
	}
	return strings.Contains(Fold(a.De), q) || strings.Contains(Fold(a.En), q)
}

// Similar returns up to max articles whose name is like name, the exact name
// first, then names that start with it, then names that hold it. A name of
// one letter is like nothing.
func Similar(name string, all []Article, lang string, max int) []Article {
	n := Fold(name)
	if utf8.RuneCountInString(n) < 2 {
		return nil
	}
	rank := func(a Article) int {
		best := 4
		for _, s := range []string{Fold(a.De), Fold(a.En)} {
			switch {
			case s == n:
				best = min(best, 0)
			case strings.HasPrefix(s, n):
				best = min(best, 1)
			case strings.Contains(s, n):
				best = min(best, 2)
			case strings.Contains(n, s) && s != "":
				best = min(best, 3)
			}
		}
		return best
	}
	type hit struct {
		a    Article
		rank int
	}
	var hits []hit
	for _, a := range all {
		if r := rank(a); r < 4 {
			hits = append(hits, hit{a, r})
		}
	}
	slices.SortStableFunc(hits, func(x, y hit) int { return x.rank - y.rank })
	out := []Article{}
	for _, h := range hits {
		if len(out) == max {
			break
		}
		out = append(out, h.a)
	}
	return out
}

//go:embed articles.txt
var shippedText string

var (
	shippedOnce sync.Once
	shipped     []Article
	shippedByID map[string]Article
)

func loadShipped() {
	shippedByID = map[string]Article{}
	for _, line := range strings.Split(shippedText, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "|")
		if len(f) != 9 || (f[8] != "" && f[8] != "athome") {
			panic("library: malformed line in articles.txt: " + line)
		}
		a := Article{ID: f[0], De: f[1], En: f[2], Dept: f[3], Kind: Kind(f[4]), Symbol: f[5], Units: strings.Split(f[6], ","), Pantry: f[8] == "athome"}
		if f[7] != "" {
			a.Allergens = strings.Split(f[7], ",")
		}
		shipped = append(shipped, a)
		shippedByID[a.ID] = a
	}
}

// Shipped returns the articles Hejmo ships, in the order of the data file.
func Shipped() []Article {
	shippedOnce.Do(loadShipped)
	return slices.Clone(shipped)
}

// ShippedByID finds a shipped article by its key.
func ShippedByID(id string) (Article, bool) {
	shippedOnce.Do(loadShipped)
	a, ok := shippedByID[id]
	return a, ok
}

// Input is what an own article's form sends.
type Input struct {
	Name, Dept, Kind string
	Allergens        []string
	Unit1, Unit2     string
	// PicSource is "", "symbol", "upload" or "web"; Symbol is the emoji of "symbol".
	PicSource, Symbol string
}

// FieldErr says which field is wrong and why (Code is a short word the pages
// turn into a sentence).
type FieldErr struct{ Field, Code string }

// Normalized tidies the input: one line of text for the name, the allergens
// once each in order, none for something that is not food.
func (in Input) Normalized() Input {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if Kind(in.Kind) == NonFood {
		in.Allergens = []string{}
	} else if got, ok := food.NormalizeAllergens(in.Allergens); ok {
		in.Allergens = got
	}
	return in
}

// Validate checks the input against the units that are switched on.
func (in Input) Validate(active []string) []FieldErr {
	in = in.Normalized()
	var errs []FieldErr
	add := func(f, c string) { errs = append(errs, FieldErr{f, c}) }
	switch {
	case in.Name == "":
		add("name", "required")
	case utf8.RuneCountInString(in.Name) > MaxName:
		add("name", "long")
	}
	if !ValidDept(in.Dept) {
		add("dept", "unknown")
	}
	switch {
	case in.Kind == "":
		add("kind", "required")
	case !ValidKind(in.Kind):
		add("kind", "unknown")
	}
	if _, ok := food.NormalizeAllergens(in.Allergens); !ok {
		add("allergens", "unknown")
	}
	switch {
	case in.Unit1 == "":
		add("unit1", "required")
	case !unit.Valid(in.Unit1):
		add("unit1", "unknown")
	case !slices.Contains(active, in.Unit1):
		add("unit1", "off")
	}
	switch {
	case in.Unit2 == "":
	case !unit.Valid(in.Unit2):
		add("unit2", "unknown")
	case in.Unit2 == in.Unit1:
		add("unit2", "same")
	case !slices.Contains(active, in.Unit2):
		add("unit2", "off")
	}
	if in.PicSource == "symbol" {
		if _, ok := SymbolByEmoji(in.Symbol); !ok {
			add("symbol", "unknown")
		}
	}
	return errs
}

// Complete returns up to max articles for what is typed while adding to a
// list, from two letters on: names that start with it first, then names with a
// word that starts with it, then names that hold it; within one rank own
// articles come before shipped ones, then by name. German and English names
// count, ä, ö, ü count like a, o, u.
func Complete(typed string, all []Article, max int) []Article {
	q := Fold(typed)
	if utf8.RuneCountInString(q) < 2 {
		return nil
	}
	rank := func(a Article) int {
		best := 3
		for _, n := range []struct {
			name string
			lang Lang
		}{{Fold(a.De), German}, {Fold(a.En), English}} {
			name := n.name
			if name == "" {
				continue
			}
			switch {
			case strings.HasPrefix(name, q), SameName(name, q, n.lang):
				best = min(best, 0)
			case slices.ContainsFunc(strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), func(w string) bool { return strings.HasPrefix(w, q) }):
				best = min(best, 1)
			case strings.Contains(name, q):
				best = min(best, 2)
			}
		}
		return best
	}
	type hit struct {
		a    Article
		rank int
	}
	var hits []hit
	for _, a := range all {
		if r := rank(a); r < 3 {
			hits = append(hits, hit{a, r})
		}
	}
	slices.SortStableFunc(hits, func(x, y hit) int {
		if x.rank != y.rank {
			return x.rank - y.rank
		}
		if x.a.Own != y.a.Own {
			if x.a.Own {
				return -1
			}
			return 1
		}
		return strings.Compare(Fold(cmp.Or(x.a.De, x.a.En)), Fold(cmp.Or(y.a.De, y.a.En)))
	})
	var out []Article
	for _, h := range hits {
		if len(out) == max {
			break
		}
		out = append(out, h.a)
	}
	return out
}

// Resolve finds the article that the text of a list entry names: the whole
// text equals the name in German or English, ignoring case, spaces, umlauts
// and singular or plural ("Zitronen" names "Zitrone"); an exact name wins.
// The first match wins, so a household's own articles (listed first) beat
// shipped ones. Text that names no article stays free text.
func Resolve(text string, all []Article) (Article, bool) {
	return namedBy(text, all)
}
