package server

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/nutrition"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The REST API of nutrition values (ticket 288, ADR 0015): the values of a
// recipe per portion, the food data and the food of an article. A value that is
// not known is null, never 0.

// valuesJSON are the seven values, eight numbers; null is unknown.
type valuesJSON struct {
	EnergyKJ      *float64 `json:"energy_kj"`
	EnergyKcal    *float64 `json:"energy_kcal"`
	FatG          *float64 `json:"fat_g"`
	SaturatesG    *float64 `json:"saturates_g"`
	CarbohydrateG *float64 `json:"carbohydrate_g"`
	SugarsG       *float64 `json:"sugars_g"`
	ProteinG      *float64 `json:"protein_g"`
	SaltG         *float64 `json:"salt_g"`
}

func round2(v float64) float64 { return math.Round(v*100+1e-9) / 100 }

func valuesToJSON(v nutrition.Values) valuesJSON {
	get := func(k nutrition.Key) *float64 {
		if x, ok := v.Get(k); ok {
			x = round2(x)
			return &x
		}
		return nil
	}
	return valuesJSON{get(nutrition.KJ), get(nutrition.KCal), get(nutrition.Fat), get(nutrition.Saturates), get(nutrition.Carbs), get(nutrition.Sugars), get(nutrition.Protein), get(nutrition.Salt)}
}

// jsonKey names a value in the API.
func jsonKey(k nutrition.Key) string {
	return [...]string{"energy_kj", "energy_kcal", "fat_g", "saturates_g", "carbohydrate_g", "sugars_g", "protein_g", "salt_g"}[k]
}

type nutFoodJSON struct {
	ID      string     `json:"id"`
	Source  string     `json:"source"`
	NameDE  *string    `json:"name_de"`
	NameEN  *string    `json:"name_en"`
	Per100g valuesJSON `json:"per_100g"`
}

func nutFoodToJSON(f nutrition.Food) nutFoodJSON {
	return nutFoodJSON{ID: f.ID(), Source: string(f.Source), NameDE: strPtr(f.De), NameEN: strPtr(f.En), Per100g: valuesToJSON(f.Per100)}
}

// articleFood is the food and the piece weight of an article for the API: a
// shipped article's come from the data file, an own article's from the household.
func articleFood(it articleItem) (*nutFoodJSON, *float64) {
	var foodID string
	var pieceG float64
	if it.own != nil {
		foodID, pieceG = it.own.Food, it.own.PieceG
	} else if l, ok := nutrition.ForArticle(it.ID); ok {
		foodID, pieceG = l.FoodID, l.PieceG
	}
	var food *nutFoodJSON
	if f, ok := nutrition.Lookup(foodID); ok && foodID != "" {
		j := nutFoodToJSON(f)
		food = &j
	}
	var piece *float64
	if pieceG > 0 {
		piece = &pieceG
	}
	return food, piece
}

// maxFoods is the most foods one search answers.
const maxFoods = 50

func (s *server) apiSearchFoods(w http.ResponseWriter, r *http.Request, p principal) {
	if !p.mayWatchNutrition() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxFoods {
			writeError(w, http.StatusUnprocessableEntity, "limit: 1 to "+strconv.Itoa(maxFoods))
			return
		}
		limit = n
	}
	out := []nutFoodJSON{}
	for _, f := range nutrition.Search(r.URL.Query().Get("q"), s.langOf(r), limit) {
		out = append(out, nutFoodToJSON(f))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiGetNutritionFood(w http.ResponseWriter, r *http.Request, p principal) {
	if !p.mayWatchNutrition() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	f, ok := nutrition.Lookup(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "food not found")
		return
	}
	writeJSON(w, http.StatusOK, nutFoodToJSON(f))
}

type notCountedJSON struct {
	Name      string  `json:"name"`
	ArticleID *string `json:"article_id"`
	Unit      *string `json:"unit"`
	Reason    string  `json:"reason"`
}

type missingJSON struct {
	Value string        `json:"value"`
	Foods []nutFoodJSON `json:"foods"`
}

type recipeNutritionJSON struct {
	RecipeID string `json:"recipe_id"`
	Portions int    `json:"portions"`
	Counted  int    `json:"counted"`
	// Complete is true when every ingredient counted and no value is missing.
	Complete      bool             `json:"complete"`
	PerPortion    valuesJSON       `json:"per_portion"`
	NotCounted    []notCountedJSON `json:"not_counted"`
	MissingValues []missingJSON    `json:"missing_values"`
	Sources       []string         `json:"sources"`
	Note          string           `json:"note"`
}

// apiRecipeNutrition gives the nutrition values of a recipe per portion. A
// child whose switch is off gets 404, as if the recipe had none.
func (s *server) apiRecipeNutrition(w http.ResponseWriter, r *http.Request, p principal) {
	c, ok := s.apiRecipeContext(w, r, p)
	if !ok {
		return
	}
	got, _, err := c.recipeOrStarter(p.household.ID, r.PathValue("id"))
	if s.storeError(w, "get recipe", err) {
		return
	}
	if !c.mayWatchNutrition() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	res := c.nutritionOf(got.Recipe)
	out := recipeNutritionJSON{RecipeID: got.ID, Portions: got.Portions, Counted: res.Counted, PerPortion: valuesToJSON(res.Values),
		NotCounted: []notCountedJSON{}, MissingValues: []missingJSON{}, Sources: []string{},
		Complete: res.Counted > 0 && len(res.Skipped) == 0 && len(res.Gaps) == 0, Note: s.i18n.T(s.langOf(r), "nutrition.api.note")}
	for _, sk := range res.Skipped {
		j := notCountedJSON{Name: sk.Name, ArticleID: strPtr(sk.ArticleID), Unit: strPtr(sk.Unit)}
		switch sk.Reason {
		case nutrition.ReasonFree, nutrition.ReasonFreeNoAmount:
			j.Reason = "free_text"
		case nutrition.ReasonNoAmount:
			j.Reason = "no_quantity"
		case nutrition.ReasonNoWeight:
			j.Reason = "no_weight"
		default:
			j.Reason = "no_food"
		}
		out.NotCounted = append(out.NotCounted, j)
	}
	for _, g := range res.Gaps {
		m := missingJSON{Value: jsonKey(g.Key), Foods: []nutFoodJSON{}}
		for _, f := range g.Foods {
			m.Foods = append(m.Foods, nutFoodToJSON(f))
		}
		out.MissingValues = append(out.MissingValues, m)
	}
	for _, src := range res.Sources {
		out.Sources = append(out.Sources, string(src))
	}
	writeJSON(w, http.StatusOK, out)
}

// checkFood reads the food and piece weight of an article request; the error
// names the field.
func checkOwnFood(food *string, pieceG *float64, cur *store.OwnArticle) *fieldRef {
	if food != nil {
		if *food != "" {
			if _, ok := nutrition.Lookup(strings.TrimSpace(*food)); !ok {
				return &fieldRef{"food", "food: unknown, find one with GET /foods"}
			}
		}
		cur.Food = strings.TrimSpace(*food)
	}
	if pieceG != nil {
		if *pieceG < 0 || *pieceG > store.MaxPieceG || math.IsNaN(*pieceG) {
			return &fieldRef{"piece_g", "piece_g: 0 up to " + strconv.Itoa(store.MaxPieceG)}
		}
		cur.PieceG = *pieceG
	}
	return nil
}
