package recipe

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/meal"
)

// A recipe on the meal plan (ticket 281, ADR 0021): the quantities for the
// portions of an entry and the suggestions while a meal is typed.

// MinSuggestLetters is how many letters are typed before anything is suggested.
const MinSuggestLetters = 2

// Maximum suggestions under a meal.
const (
	MaxSuggestedRecipes = 5
	MaxSuggestedDishes  = 3
)

// Scale is a quantity written for base portions, for n portions: rounded to
// one decimal, never to a whole number, and anything above zero shows at
// least 0.1, never 0. No quantity (0) stays none.
func Scale(amount float64, base, n int) float64 {
	if amount <= 0 || base < 1 {
		return 0
	}
	v := math.Floor(amount*float64(n)/float64(base)*10+0.5+1e-9) / 10
	return math.Max(v, 0.1)
}

// FormatScaled writes a scaled quantity with at most one decimal, a comma in
// German and a point in English.
func FormatScaled(v float64, lang string) string {
	s := strconv.FormatFloat(math.Floor(v*10+0.5+1e-9)/10, 'f', -1, 64)
	if lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// typedLetters is what was typed with the spaces around and between words
// cleaned, and whether it is enough to suggest from.
func typedLetters(q string) (string, bool) {
	q = strings.Join(strings.Fields(q), " ")
	return q, utf8.RuneCountInString(q) >= MinSuggestLetters
}

// SuggestRecipes returns up to limit recipes whose name holds the typed text
// anywhere (case and umlauts ignored); from two letters on. Those that fit the
// mealtime come first (a recipe without mealtimes fits every one), then the
// rest, each from A to Z. An empty mealtime prefers none.
func SuggestRecipes(list []Recipe, q string, mt meal.Mealtime, limit int) []Recipe {
	q, ok := typedLetters(q)
	if !ok {
		return nil
	}
	key := library.Fold(q)
	var out []Recipe
	for _, r := range list {
		if strings.Contains(library.Fold(r.Name), key) {
			out = append(out, r)
		}
	}
	fits := func(r Recipe) bool {
		return mt == "" || len(r.Mealtimes) == 0 || slices.Contains(r.Mealtimes, string(mt))
	}
	SortByName(out)
	sort.SliceStable(out, func(i, j int) bool { return fits(out[i]) && !fits(out[j]) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Dish is a free-text meal of the plan and the last day it was planned.
type Dish struct {
	Text string
	Day  string // YYYY-MM-DD
}

// EarlierDishes returns up to limit free-text dishes that hold the typed text
// (case and umlauts ignored; from two letters on), each once, the most recent
// first. A dish that is the name of a recipe is left out: the recipe is
// suggested instead.
func EarlierDishes(rows []Dish, q string, recipeNames []string, limit int) []Dish {
	q, ok := typedLetters(q)
	if !ok {
		return nil
	}
	key := library.Fold(q)
	rows = slices.Clone(rows)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Day > rows[j].Day })
	var out []Dish
	seen := map[string]bool{}
	for _, d := range rows {
		k := library.Fold(strings.Join(strings.Fields(d.Text), " "))
		if k == "" || seen[k] || !strings.Contains(k, key) || slices.ContainsFunc(recipeNames, func(n string) bool { return NameKey(n) == NameKey(d.Text) }) {
			continue
		}
		seen[k] = true
		out = append(out, d)
		if len(out) == limit {
			break
		}
	}
	return out
}
