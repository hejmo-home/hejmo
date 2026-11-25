package server

import (
	"html/template"
	"math"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/nutrition"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The section "Nährwerte" of the form of an own article (ticket 288,
// preview-288): the food behind the article's nutrition values, found with a
// search that works without script, and the weight of one piece.

// maxFoodCards is how many foods a search lists.
const maxFoodCards = 12

type foodCard struct {
	ID, Name, Values string
	Checked          bool
}

type foodChosen struct{ Name, Detail string }

// foodSection is what the template shows of the section.
type foodSection struct {
	Name   string // the article, for the hint
	Chosen *foodChosen
	Q      string
	Head   string // "4 Lebensmittel zu „Pflaumen“"
	None   string // the sentence when the search found nothing
	Cards  []foodCard
}

// gramsOf100 words a value per 100 g for a choice card: one decimal below
// 10 g, whole from 10 g.
func gramsOf100(v float64, lang string) string {
	var s string
	if r := math.Round(v*10) / 10; r < 10 {
		s = strconv.FormatFloat(r, 'f', 1, 64)
		s = strings.TrimSuffix(s, ".0")
	} else {
		s = strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	if lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// foodValues is "100 g: 225 kcal · 0,3 g Fett · 52 g Kohlenhydrate · davon 46 g Zucker";
// a value the food lacks says so instead of a number.
func (s *server) foodValues(lang string, f nutrition.Food) string {
	part := func(k nutrition.Key, key string) string {
		v, ok := f.Per100.Get(k)
		if !ok {
			return s.i18n.Format(lang, i18n.Msg("artf.food.v.na", "what", s.i18n.T(lang, "nutrition.gap."+nutName(k))))
		}
		n := gramsOf100(v, lang)
		if k == nutrition.KCal {
			n = strconv.FormatFloat(math.Round(v), 'f', 0, 64)
		}
		return s.i18n.Format(lang, i18n.Msg(key, "n", n))
	}
	parts := []string{part(nutrition.KCal, "artf.food.v.kcal"), part(nutrition.Fat, "artf.food.v.fat"), part(nutrition.Carbs, "artf.food.v.carbs"), part(nutrition.Sugars, "artf.food.v.sugars")}
	return s.i18n.Format(lang, i18n.Msg("artf.food.v.per100", "values", strings.Join(parts, " · ")))
}

// foodSectionOf builds the section from what the form holds.
func (s *server) foodSectionOf(lang string, f artForm, articleName string) foodSection {
	sec := foodSection{Name: articleName, Q: f.FoodQ}
	if f.Food != "" {
		if food, ok := nutrition.Lookup(f.Food); ok {
			sec.Chosen = &foodChosen{Name: food.Name(lang), Detail: s.foodValues(lang, food) + " · " + s.i18n.T(lang, "artf.food.from."+string(food.Source))}
		}
	}
	if f.FoodQ == "" {
		return sec
	}
	found := nutrition.Search(f.FoodQ, lang, maxFoodCards)
	if len(found) == 0 {
		sec.None = s.i18n.Format(lang, i18n.Msg("artf.food.nohit", "q", f.FoodQ))
		return sec
	}
	key := "artf.food.count.many"
	switch {
	case len(found) == maxFoodCards:
		key = "artf.food.count.first"
	case len(found) == 1:
		key = "artf.food.count.one"
	}
	sec.Head = s.i18n.Format(lang, i18n.Msg(key, "n", strconv.Itoa(len(found)), "q", f.FoodQ))
	for _, food := range found {
		sec.Cards = append(sec.Cards, foodCard{ID: food.ID(), Name: food.Name(lang), Values: s.foodValues(lang, food), Checked: food.ID() == f.Food})
	}
	return sec
}

// pieceText writes a stored piece weight for the field.
func pieceText(v float64, lang string) string {
	if v <= 0 {
		return ""
	}
	t := strconv.FormatFloat(v, 'f', -1, 64)
	if lang != "en" {
		t = strings.Replace(t, ".", ",", 1)
	}
	return t
}

// parsePiece reads the weight of one piece in grams: empty is none (0); a
// decimal comma or point is fine. ok is false for anything else.
func parsePiece(text string) (float64, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, true
	}
	v, err := strconv.ParseFloat(strings.Replace(text, ",", ".", 1), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > store.MaxPieceG {
		return 0, false
	}
	return v, true
}

// recipesLinks are the recipes that use an own article, as links, for the
// facts of its page.
func (s *server) recipesLinks(lang string, refs []store.RecipeRef) template.HTML {
	parts := make([]string, len(refs))
	for i, r := range refs {
		parts[i] = htmlLink("/meals/recipes/"+r.ID, r.Name)
	}
	return template.HTML(s.joinWords(lang, parts)) // #nosec G203 -- htmlLink escapes
}
