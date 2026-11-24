// Package recipe is the domain of the household's own recipes (ADR 0019 and
// 0021, ticket 279): what a recipe is, the six categories, how an ingredient
// line is read (a quantity such as ½ or 1 1/2, a unit, an article or free
// text), the limits, the warnings of the family's allergies, the diet mark and
// the search. It is pure: no storage, no pages.
package recipe

import (
	"errors"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Category is what kind of dish a recipe is.
type Category string

// The six categories; Main is the default.
const (
	Main      Category = "main"
	Side      Category = "side"
	Dessert   Category = "dessert"
	Baking    Category = "baking"
	Breakfast Category = "breakfast"
	Snack     Category = "snack"
)

// Categories lists the categories in the order the pages show them (catalogue
// keys "recipes.cat.<key>").
var Categories = []Category{Main, Side, Dessert, Baking, Breakfast, Snack}

var symbols = map[Category]string{Main: "🍲", Side: "🥗", Dessert: "🍮", Baking: "🥨", Breakfast: "🥣", Snack: "🥕"}

// Symbol is the emoji of the category: it helps children who cannot read yet.
func (c Category) Symbol() string { return symbols[c] }

// ValidCategory reports whether s is one of the six categories.
func ValidCategory(s string) bool { return slices.Contains(Categories, Category(s)) }

// Limits.
const (
	MaxName        = 120
	MaxPortions    = 99
	MaxMinutes     = 9999
	MaxStep        = 2000
	MaxTips        = 2000
	MaxSource      = 300
	MaxTag         = 30
	MaxTags        = 30
	MaxIngredients = 100
	MaxSteps       = 100
	// MaxIngredient is the longest ingredient name and MaxVariety the longest variety.
	MaxIngredient = 200
	MaxVariety    = 60
	// MaxAmount is the largest quantity of an ingredient.
	MaxAmount = 9999
)

// Ingredient is one line of a recipe: free text or an article of the library
// (with an optional variety), with a quantity for the recipe's portions.
type Ingredient struct {
	// Amount is the quantity, 0 for none ("Salz"); Unit is "" for none.
	Amount float64
	Unit   string
	// Text is the name as written; ArticleID is "" for free text, which
	// Hejmo does not check for allergens.
	Text      string
	ArticleID string
	// Variety is "Type 405" of "Mehl · Type 405"; free text has none.
	Variety string
}

// Recipe is a dish with its ingredients and steps.
type Recipe struct {
	ID          string
	Name        string
	Category    Category
	Portions    int
	Minutes     int // 0: not known
	Mealtimes   []string
	Tags        []string
	Tips        string
	Source      string
	Ingredients []Ingredient
	Steps       []string
}

// FieldError says which field of a recipe was refused.
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Msg }

// ErrAmount means the text is no quantity between 0 and 9999.
var ErrAmount = errors.New("recipe: not a quantity above 0 up to 9999")

// ErrBlank means an ingredient line is empty: it is not saved.
var ErrBlank = errors.New("recipe: empty line")

var (
	plainRe    = regexp.MustCompile(`^(\d+)(?:[.,](\d+))?$`)
	fractionRe = regexp.MustCompile(`^(?:(\d+)\s*)?(\d+)\s*/\s*(\d+)$`)
	vulgar     = map[rune]float64{'½': 0.5, '¼': 0.25, '¾': 0.75, '⅓': 1.0 / 3, '⅔': 2.0 / 3, '⅛': 0.125, '⅜': 0.375, '⅝': 0.625, '⅞': 0.875, '⅕': 0.2, '⅖': 0.4, '⅗': 0.6, '⅘': 0.8, '⅙': 1.0 / 6, '⅚': 5.0 / 6}
)

// ParseAmount reads a quantity as people write it: 2, 0,5, 0.5, ½, 1 1/2,
// 1½ or 3/4. It must be above 0 and at most 9999.
func ParseAmount(s string) (float64, error) {
	s = strings.TrimSpace(s)
	v, ok := parseAmount(s)
	if !ok || v <= 0 || v > MaxAmount || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, ErrAmount
	}
	return v, nil
}

func parseAmount(s string) (float64, bool) {
	if m := plainRe.FindStringSubmatch(s); m != nil {
		text := m[1]
		if m[2] != "" {
			text += "." + m[2]
		}
		v, err := strconv.ParseFloat(text, 64)
		return v, err == nil
	}
	if rs := []rune(s); len(rs) > 0 {
		if f, ok := vulgar[rs[len(rs)-1]]; ok {
			whole := strings.TrimSpace(string(rs[:len(rs)-1]))
			if whole == "" {
				return f, true
			}
			n, err := strconv.Atoi(whole)
			return float64(n) + f, err == nil
		}
	}
	if m := fractionRe.FindStringSubmatch(s); m != nil {
		num, _ := strconv.Atoi(m[2])
		den, _ := strconv.Atoi(m[3])
		if den == 0 {
			return 0, false
		}
		whole := 0
		if m[1] != "" {
			whole, _ = strconv.Atoi(m[1])
		}
		return float64(whole) + float64(num)/float64(den), true
	}
	return 0, false
}

// FormatAmount writes a quantity the way it is typed back: at most two
// decimals, no trailing zeros, a comma in German and a point in English.
func FormatAmount(v float64, lang string) string {
	s := strconv.FormatFloat(math.Floor(v*100+0.5+1e-9)/100, 'f', -1, 64)
	if lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// IngredientInput is an ingredient line as a form or a request has it: the
// quantity as typed.
type IngredientInput struct {
	Amount, Unit, Name, Variety string
	ArticleID                   string
}

// CheckIngredient reads one line. A line with nothing in it is ErrBlank (it is
// not saved). A unit without a quantity means one ("Prise Salz"); a quantity
// without an ingredient, a quantity that is no number and an unknown unit are
// a *FieldError naming amount, name or unit. Free text has no variety.
func CheckIngredient(in IngredientInput) (Ingredient, error) {
	amount, name, variety := strings.TrimSpace(in.Amount), strings.Join(strings.Fields(in.Name), " "), strings.Join(strings.Fields(in.Variety), " ")
	if amount == "" && in.Unit == "" && name == "" {
		return Ingredient{}, ErrBlank
	}
	if in.Unit != "" && !unit.ValidForRecipe(in.Unit) {
		return Ingredient{}, &FieldError{"unit", "unknown unit"}
	}
	out := Ingredient{Unit: in.Unit, Text: name, ArticleID: in.ArticleID}
	if amount != "" {
		v, err := ParseAmount(amount)
		if err != nil {
			return Ingredient{}, &FieldError{"amount", err.Error()}
		}
		out.Amount = v
	} else if in.Unit != "" {
		out.Amount = 1
	}
	switch {
	case name == "":
		return Ingredient{}, &FieldError{"name", "required"}
	case utf8.RuneCountInString(name) > MaxIngredient:
		return Ingredient{}, &FieldError{"name", "too long"}
	}
	if in.ArticleID != "" && variety != "" {
		if utf8.RuneCountInString(variety) > MaxVariety {
			return Ingredient{}, &FieldError{"variety", "too long"}
		}
		out.Variety = variety
	}
	return out, nil
}

// Check cleans a recipe and refuses what is not allowed. Only the name is
// required. Empty steps and tags are dropped, tags are kept once each ignoring
// case, the mealtimes once each in the order of the day.
func Check(r Recipe) (Recipe, error) {
	r.Name = strings.Join(strings.Fields(r.Name), " ")
	switch {
	case r.Name == "":
		return r, &FieldError{"name", "required"}
	case utf8.RuneCountInString(r.Name) > MaxName:
		return r, &FieldError{"name", "too long"}
	case !ValidCategory(string(r.Category)):
		return r, &FieldError{"category", "unknown category"}
	case r.Portions < 1 || r.Portions > MaxPortions:
		return r, &FieldError{"portions", "between 1 and 99"}
	case r.Minutes < 0 || r.Minutes > MaxMinutes:
		return r, &FieldError{"minutes", "between 0 and 9999"}
	case len(r.Ingredients) > MaxIngredients:
		return r, &FieldError{"ingredients", "too many"}
	case len(r.Steps) > MaxSteps:
		return r, &FieldError{"steps", "too many"}
	}
	var times []string
	for _, m := range meal.Mealtimes() {
		for _, got := range r.Mealtimes {
			if string(m) == got && !slices.Contains(times, got) {
				times = append(times, got)
			}
		}
	}
	for _, got := range r.Mealtimes {
		if !meal.Mealtime(got).Valid() {
			return r, &FieldError{"mealtimes", "unknown mealtime"}
		}
	}
	r.Mealtimes = times
	var steps []string
	for _, s := range r.Steps {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if utf8.RuneCountInString(s) > MaxStep {
			return r, &FieldError{"steps", "too long"}
		}
		steps = append(steps, s)
	}
	r.Steps = steps
	var tags []string
	for _, tag := range r.Tags {
		tag = strings.Join(strings.Fields(tag), " ")
		if tag == "" || slices.ContainsFunc(tags, func(o string) bool { return tagKey(o) == tagKey(tag) }) {
			continue
		}
		if utf8.RuneCountInString(tag) > MaxTag {
			return r, &FieldError{"tags", "too long"}
		}
		tags = append(tags, tag)
	}
	if len(tags) > MaxTags {
		return r, &FieldError{"tags", "too many"}
	}
	r.Tags = tags
	r.Tips, r.Source = strings.TrimSpace(r.Tips), strings.TrimSpace(r.Source)
	switch {
	case utf8.RuneCountInString(r.Tips) > MaxTips:
		return r, &FieldError{"tips", "too long"}
	case utf8.RuneCountInString(r.Source) > MaxSource:
		return r, &FieldError{"source", "too long"}
	}
	return r, nil
}

// NameKey is what two recipe names are compared by: case and the spaces
// around and between words do not make a second name.
func NameKey(name string) string { return strings.ToLower(strings.Join(strings.Fields(name), " ")) }

func tagKey(tag string) string { return strings.ToLower(strings.Join(strings.Fields(tag), " ")) }

// SplitTags splits what was typed for new tags at commas: trimmed, empty ones dropped.
func SplitTags(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CanonTags returns the tags once each, ignoring case; a tag the household
// already uses keeps that spelling, so "schnell" never becomes "Schnell".
func CanonTags(tags, known []string) []string {
	var out []string
	for _, t := range tags {
		t = strings.Join(strings.Fields(t), " ")
		if t == "" || slices.ContainsFunc(out, func(o string) bool { return tagKey(o) == tagKey(t) }) {
			continue
		}
		if i := slices.IndexFunc(known, func(k string) bool { return tagKey(k) == tagKey(t) }); i >= 0 {
			t = known[i]
		}
		out = append(out, t)
	}
	return out
}

// SourceLink returns the source when it is a web address (http or https, no
// spaces), otherwise "": a book or "Oma Gerda" is a source too, but no link.
func SourceLink(source string) string {
	s := strings.TrimSpace(source)
	if s == "" || strings.ContainsFunc(s, func(r rune) bool { return r == ' ' || r < 0x20 }) {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	return s
}

// HoursMinutes splits minutes into whole hours and the rest.
func HoursMinutes(min int) (hours, minutes int) { return min / 60, min % 60 }

// Marks of the diet.
const (
	MarkVegan      = "vegan"
	MarkVegetarian = "vegetarian"
)

// Diet is what a recipe's articles say about its diet.
type Diet struct {
	// Mark is MarkVegan or MarkVegetarian when every article fits, else "".
	Mark string
	// Prevents names the ingredients that stop the next mark: for vegetarian
	// what is not vegan, for no mark what is not vegetarian.
	Prevents []string
	// Free names the ingredients Hejmo does not know: free text, or an
	// article that is not in the library any more. They do not stop the mark.
	Free []string
}

// DietOf decides the diet mark. lookup finds the article of an ingredient.
func DietOf(ings []Ingredient, lookup func(id string) (library.Article, bool)) Diet {
	var d Diet
	var arts []Ingredient
	kinds := map[string]library.Kind{}
	for _, in := range ings {
		a, ok := library.Article{}, false
		if in.ArticleID != "" {
			a, ok = lookup(in.ArticleID)
		}
		if !ok {
			if !slices.Contains(d.Free, in.Text) {
				d.Free = append(d.Free, in.Text)
			}
			continue
		}
		arts = append(arts, in)
		kinds[in.Text+"\x00"+in.ArticleID] = a.Kind
	}
	if len(arts) == 0 {
		return d
	}
	var notVegan, notVeg []string
	for _, in := range arts {
		k := kinds[in.Text+"\x00"+in.ArticleID]
		if !k.Fits(food.Vegan) && !slices.Contains(notVegan, in.Text) {
			notVegan = append(notVegan, in.Text)
		}
		if !k.Fits(food.Vegetarian) && !slices.Contains(notVeg, in.Text) {
			notVeg = append(notVeg, in.Text)
		}
	}
	switch {
	case len(notVegan) == 0:
		d.Mark = MarkVegan
	case len(notVeg) == 0:
		d.Mark, d.Prevents = MarkVegetarian, notVegan
	default:
		d.Prevents = notVeg
	}
	return d
}

// Warnings returns, for the articles of a recipe, who has to look out for what:
// one warning per allergen (in the order of food.Allergens), naming everyone
// affected in the order of the household. It never blocks anything.
func Warnings(arts []library.Article, people []library.Person) []library.Warning {
	hit := map[string]map[string]bool{}
	for _, a := range arts {
		for _, w := range library.PersonWarnings(a, people) {
			for _, k := range w.Allergens {
				if hit[k] == nil {
					hit[k] = map[string]bool{}
				}
				hit[k][w.Person] = true
			}
		}
	}
	var out []library.Warning
	for _, k := range food.Allergens {
		if len(hit[k]) == 0 {
			continue
		}
		w := library.Warning{Allergens: []string{k}}
		for _, p := range people {
			if hit[k][p.ID] {
				w.People = append(w.People, p.ID)
			}
		}
		out = append(out, w)
	}
	return out
}

// Matches reports whether a recipe is found by the search: the typed text is
// part of its name, of an ingredient or of a tag (case, umlauts and the spaces
// around it ignored). An empty search finds every recipe.
func Matches(r Recipe, q string) bool {
	q = library.Fold(q)
	if q == "" {
		return true
	}
	has := func(s string) bool { return strings.Contains(library.Fold(s), q) }
	if has(r.Name) {
		return true
	}
	for _, in := range r.Ingredients {
		if has(in.Text) {
			return true
		}
	}
	return slices.ContainsFunc(r.Tags, has)
}

// SortByName puts recipes from A to Z, ignoring case and treating ä, ö, ü like a, o, u.
func SortByName(list []Recipe) {
	slices.SortStableFunc(list, func(a, b Recipe) int { return strings.Compare(library.Fold(a.Name), library.Fold(b.Name)) })
}

// FormatAmountNumber rounds a quantity to the two decimals pages show, as the
// number a JSON client reads.
func FormatAmountNumber(v float64) float64 { return math.Floor(v*100+0.5+1e-9) / 100 }
