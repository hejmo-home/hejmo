package server

import (
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/starter"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Own recipes (ticket 279, ADR 0019 and 0021): the rules the web pages and
// the REST API share. Parents write recipes, everyone reads them; Hejmo
// warns about the family's allergies and never blocks.

// visibleArticles are the articles a typed name can mean: not those a parent hid by hand.
func (c *itemCtx) visibleArticles() []library.Article {
	var out []library.Article
	for _, it := range c.st.items() {
		if !it.handHidden {
			out = append(out, it.Article)
		}
	}
	return out
}

// recipeUnits are the units a recipe offers: the household's, the kitchen
// measures and the containers and piece measures.
func (c *itemCtx) recipeUnits() []string { return unit.ForRecipe(c.active) }

// ingredientState says what Hejmo made of an ingredient line.
type ingredientState int

const (
	ingBlank   ingredientState = iota // nothing typed
	ingArticle                        // an article of the library
	ingFree                           // free text: not checked for allergens
)

// recipeIngredient reads an ingredient line: an article (named by its id, by
// the exact name typed, or by an article's name followed by a variety such as
// "Mehl Type 405"), else free text. A variety written in the name wins over a
// different one given in the field; the given variety counts when the name has
// none. The error is a
// *recipe.FieldError, ErrBlank for an empty line.
func (c *itemCtx) recipeIngredient(in recipe.IngredientInput) (recipe.Ingredient, *articleItem, error) {
	name := strings.Join(strings.Fields(in.Name), " ")
	var art *articleItem
	variety, given := in.Variety, in.ArticleID != ""
	switch {
	case in.ArticleID != "":
		it, ok := c.article(in.ArticleID)
		if !ok {
			return recipe.Ingredient{}, nil, &recipe.FieldError{Field: "article_id", Msg: "unknown article"}
		}
		art = &it
		// A name that ends in a variety ("Erdnussbutter · cremig") takes it over a given one.
		if a, rest, ok := library.LeadingName(name, c.visibleArticles()); ok && a.ID == it.ID {
			variety = rest
		}
	case name != "":
		if it, ok := c.byName(name); ok {
			art = &it
		} else if a, rest, ok := library.LeadingName(name, c.visibleArticles()); ok {
			if it, ok := c.article(a.ID); ok {
				art = &it
				name = strings.TrimRight(strings.TrimSuffix(name, rest), " \t,·-:") // the name as typed, without the variety
				variety = rest
			}
		}
	}
	if art != nil {
		// The name as the library writes it, unless it was typed in the other language.
		if name == "" || given || library.SameName(name, art.Name(c.lang), library.Lang(c.lang)) {
			name = art.Name(c.lang)
		}
		in.ArticleID = art.ID
	}
	in.Name, in.Variety = name, variety
	ing, err := recipe.CheckIngredient(in)
	return ing, art, err
}

// lookup finds the article of an ingredient.
func (c *itemCtx) lookup(id string) (library.Article, bool) {
	it, ok := c.article(id)
	return it.Article, ok
}

// articlesOf are the articles of a recipe's ingredients that exist; one that
// is gone counts as free text.
func (c *itemCtx) articlesOf(r recipe.Recipe) []library.Article {
	var out []library.Article
	for _, in := range r.Ingredients {
		if in.ArticleID == "" {
			continue
		}
		if a, ok := c.lookup(in.ArticleID); ok {
			out = append(out, a)
		}
	}
	return out
}

// freeNames are the ingredients Hejmo does not check: free text, or an
// article that is not in the library any more; each name once.
func (c *itemCtx) freeNames(r recipe.Recipe) []string {
	var out []string
	for _, in := range r.Ingredients {
		if in.ArticleID != "" {
			if _, ok := c.lookup(in.ArticleID); ok {
				continue
			}
		}
		if !slices.Contains(out, in.Text) {
			out = append(out, in.Text)
		}
	}
	return out
}

// warnings are the family's allergy warnings of a recipe, one per allergen.
func (c *itemCtx) warnings(r recipe.Recipe) []library.Warning {
	return recipe.Warnings(c.articlesOf(r), c.st.persons)
}

// warnLines are warning lines in words: "Contains peanuts: ● Mia is allergic",
// as the library says it (#164) with the allergens in lower case in English,
// as the sentence runs on in the preview of ticket 279. One line per group.
func (c *itemCtx) warnLines(ws []library.Warning) []template.HTML {
	out := make([]template.HTML, 0, len(ws))
	for _, w := range ws {
		key := "lib.warn.one"
		if len(w.People) > 1 {
			key = "lib.warn.many"
		}
		words := c.s.allergenWords(c.lang, w.Allergens)
		if c.lang == "en" {
			words = strings.ToLower(words)
		}
		text := repl(html.EscapeString(c.s.i18n.T(c.lang, key)), "{allergens}", html.EscapeString(words), "{who}", c.s.whoHTML(c.st, w.People))
		out = append(out, template.HTML(`<span class="allergen-warn">`+triangleIcon+`<span>`+text+`</span></span>`)) // #nosec G203 -- names escaped, the text is ours
	}
	return out
}

// ---- REST API

type recipeIngredientJSON struct {
	Quantity  *float64 `json:"quantity"`
	Unit      *string  `json:"unit"`
	Name      string   `json:"name"`
	ArticleID *string  `json:"article_id"`
	Variety   *string  `json:"variety"`
}

type recipeJSON struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Category    string                 `json:"category"`
	Portions    int                    `json:"portions"`
	Minutes     *int                   `json:"minutes"`
	Mealtimes   []string               `json:"mealtimes"`
	Tags        []string               `json:"tags"`
	Tips        string                 `json:"tips"`
	Source      string                 `json:"source"`
	SourceURL   *string                `json:"source_url"`
	Ingredients []recipeIngredientJSON `json:"ingredients"`
	Steps       []string               `json:"steps"`
	Diet        *string                `json:"diet"`
	Starter     bool                   `json:"starter"`
	SourceName  *string                `json:"source_name"`
	CreatedBy   *string                `json:"created_by"`
	CreatedAt   *string                `json:"created_at"`
	UpdatedBy   *string                `json:"updated_by"`
	UpdatedAt   *string                `json:"updated_at"`
}

func (c *itemCtx) recipeToJSON(r store.Recipe) recipeJSON {
	out := recipeJSON{ID: r.ID, Name: r.Name, Category: string(r.Category), Portions: r.Portions, Mealtimes: nonNil(r.Mealtimes), Tags: nonNil(r.Tags),
		Tips: r.Tips, Source: r.Source, SourceURL: strPtr(recipe.SourceLink(r.Source)), Ingredients: []recipeIngredientJSON{}, Steps: nonNil(r.Steps),
		CreatedBy: strPtr(r.CreatedBy), UpdatedBy: strPtr(r.UpdatedBy)}
	if !r.CreatedAt.IsZero() { // a starter recipe was written by nobody in the household
		created, updated := r.CreatedAt.UTC().Format(time.RFC3339), r.UpdatedAt.UTC().Format(time.RFC3339)
		out.CreatedAt, out.UpdatedAt = &created, &updated
	}
	if r.Minutes > 0 {
		out.Minutes = &r.Minutes
	}
	for _, in := range r.Ingredients {
		j := recipeIngredientJSON{Name: in.Text, Unit: strPtr(in.Unit), ArticleID: strPtr(in.ArticleID), Variety: strPtr(in.Variety)}
		if in.Amount > 0 {
			q := recipe.FormatAmountNumber(in.Amount)
			j.Quantity = &q
		}
		out.Ingredients = append(out.Ingredients, j)
	}
	if d := recipe.DietOf(r.Ingredients, c.lookup); d.Mark != "" {
		out.Diet = &d.Mark
	}
	return out
}

// recipeInput is what a request says about a recipe.
type recipeInput struct {
	Name        string                 `json:"name"`
	Category    string                 `json:"category"`
	Portions    *int                   `json:"portions"`
	Minutes     *int                   `json:"minutes"`
	Mealtimes   []string               `json:"mealtimes"`
	Tags        []string               `json:"tags"`
	Tips        string                 `json:"tips"`
	Source      string                 `json:"source"`
	Ingredients []recipeIngredientJSON `json:"ingredients"`
	Steps       []string               `json:"steps"`
}

// householdPortions is how many portions a new recipe starts with: the number of people.
func (c *itemCtx) householdPortions() int { return min(max(len(c.st.people), 1), recipe.MaxPortions) }

// fromInput builds the recipe a request describes. The error names the field
// the way the API does ("ingredients[2].quantity").
func (c *itemCtx) fromInput(in recipeInput, tagsKnown []string) (recipe.Recipe, *fieldRef) {
	r := recipe.Recipe{Name: in.Name, Category: recipe.Category(in.Category), Portions: c.householdPortions(), Mealtimes: in.Mealtimes,
		Tags: recipe.CanonTags(in.Tags, tagsKnown), Tips: in.Tips, Source: in.Source, Steps: in.Steps}
	if r.Category == "" {
		r.Category = recipe.Main
	}
	if in.Portions != nil {
		r.Portions = *in.Portions
	}
	if in.Minutes != nil {
		r.Minutes = *in.Minutes
	}
	for i, j := range in.Ingredients {
		line := recipe.IngredientInput{Name: j.Name}
		if j.Quantity != nil {
			if *j.Quantity <= 0 || *j.Quantity > recipe.MaxAmount {
				return r, &fieldRef{fmt.Sprintf("ingredients[%d].quantity", i), "quantity: above 0 up to 9999"}
			}
			line.Amount = fmt.Sprint(*j.Quantity)
		}
		if j.Unit != nil {
			line.Unit = *j.Unit
		}
		if j.ArticleID != nil {
			line.ArticleID = *j.ArticleID
		}
		if j.Variety != nil {
			line.Variety = *j.Variety
		}
		ing, _, err := c.recipeIngredient(line)
		var fe *recipe.FieldError
		switch {
		case errors.Is(err, recipe.ErrBlank):
			continue
		case errors.As(err, &fe):
			return r, &fieldRef{fmt.Sprintf("ingredients[%d].%s", i, fe.Field), fe.Field + ": " + fe.Msg}
		case err != nil:
			return r, &fieldRef{fmt.Sprintf("ingredients[%d]", i), err.Error()}
		}
		r.Ingredients = append(r.Ingredients, ing)
	}
	return r, nil
}

type fieldRef struct{ Field, Msg string }

// saveError answers a refused recipe: 422 with the field, and for a name that is taken, the existing recipe.
func (s *server) recipeError(w http.ResponseWriter, what string, err error) bool {
	var taken *store.RecipeNameTakenError
	var fe *store.FieldError
	switch {
	case err == nil:
		return false
	case errors.As(err, &taken):
		writeFieldError(w, http.StatusUnprocessableEntity, "name", "name: taken by the recipe "+taken.ExistingID)
	case errors.As(err, &fe):
		writeFieldError(w, http.StatusUnprocessableEntity, fe.Field, fe.Field+": "+fe.Msg)
	default:
		return s.storeError(w, what, err)
	}
	return true
}

func (s *server) apiRecipeContext(w http.ResponseWriter, r *http.Request, p principal) (*itemCtx, bool) {
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "recipe context", err)
		return nil, false
	}
	return c, true
}

func (s *server) apiListRecipes(w http.ResponseWriter, r *http.Request, p principal) {
	c, ok := s.apiRecipeContext(w, r, p)
	if !ok {
		return
	}
	list, err := s.db.Recipes(p.household.ID)
	if err != nil {
		s.fail(w, "list recipes", err)
		return
	}
	q, cat := r.URL.Query().Get("q"), r.URL.Query().Get("category")
	out := []recipeJSON{}
	for _, x := range list {
		if (cat == "" || string(x.Category) == cat) && recipe.Matches(x.Recipe, q) {
			out = append(out, c.recipeToJSON(x))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiGetRecipe(w http.ResponseWriter, r *http.Request, p principal) {
	c, ok := s.apiRecipeContext(w, r, p)
	if !ok {
		return
	}
	got, entry, err := c.recipeOrStarter(p.household.ID, r.PathValue("id"))
	if s.storeError(w, "get recipe", err) {
		return
	}
	out := c.recipeToJSON(got)
	if r.URL.Query().Has("portions") {
		n, ok := recipe.ParsePortions(r.URL.Query().Get("portions"))
		if !ok {
			writeFieldError(w, http.StatusUnprocessableEntity, "portions", "portions must be a whole number from 1 to 99")
			return
		}
		out = c.recipeToJSON(scaledRecipe(got, n))
	}
	if entry != nil {
		out = c.markStarter(out, *entry)
	}
	writeJSON(w, http.StatusOK, out)
}

// scaledRecipe is the recipe as written for other portions (ADR 0021, ticket
// 286): the quantities scaled and rounded to one decimal, the portions the
// ones asked for. The stored recipe does not change.
func scaledRecipe(r store.Recipe, portions int) store.Recipe {
	if portions == r.Portions {
		return r
	}
	ings := make([]recipe.Ingredient, len(r.Ingredients))
	for i, in := range r.Ingredients {
		in.Amount = recipe.ScaleAmount(in.Amount, r.Portions, portions)
		ings[i] = in
	}
	r.Ingredients, r.Portions = ings, portions
	return r
}

// apiSaveRecipe creates (POST) or replaces (PUT) a recipe.
func (s *server) apiSaveRecipe(w http.ResponseWriter, r *http.Request, p principal) {
	var in recipeInput
	if !readJSON(r, &in) {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if starter.IsID(r.PathValue("id")) {
		writeError(w, http.StatusForbidden, starterReadOnly)
		return
	}
	c, ok := s.apiRecipeContext(w, r, p)
	if !ok {
		return
	}
	known, err := s.db.RecipeTags(p.household.ID)
	if err != nil {
		s.fail(w, "recipe tags", err)
		return
	}
	rec, bad := c.fromInput(in, known)
	if bad != nil {
		writeFieldError(w, http.StatusUnprocessableEntity, bad.Field, bad.Msg)
		return
	}
	var saved store.Recipe
	status := http.StatusCreated
	if id := r.PathValue("id"); id != "" {
		rec.ID, status = id, http.StatusOK
		saved, err = s.db.UpdateRecipe(p.household.ID, p.person.ID, rec, s.mealOptions(s.langOf(r), p))
	} else {
		saved, err = s.db.CreateRecipe(p.household.ID, p.person.ID, rec)
	}
	if s.recipeError(w, "save recipe", err) {
		return
	}
	writeJSON(w, status, c.recipeToJSON(saved))
}

func (s *server) apiDeleteRecipe(w http.ResponseWriter, r *http.Request, p principal) {
	if starter.IsID(r.PathValue("id")) {
		writeError(w, http.StatusForbidden, starterReadOnly)
		return
	}
	ids, err := s.db.DeleteRecipeWithPhotos(p.household.ID, r.PathValue("id"))
	if s.storeError(w, "delete recipe", err) {
		return
	}
	s.removeRecipePhotoFiles(ids)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) apiRecipeUnits(w http.ResponseWriter, r *http.Request, p principal) {
	writeJSON(w, http.StatusOK, unit.ForRecipe(s.activeUnits(p.household)))
}

type recipeWarningJSON struct {
	Allergen string   `json:"allergen"`
	People   []string `json:"people"`
}

type freeAllergyJSON struct {
	PersonID string `json:"person_id"`
	Text     string `json:"text"`
}

// apiRecipeWarnings tells who has to look out for what in a recipe: health
// data, so only the scope health reaches it. It never blocks anything.
func (s *server) apiRecipeWarnings(w http.ResponseWriter, r *http.Request, p principal) {
	c, ok := s.apiRecipeContext(w, r, p)
	if !ok {
		return
	}
	got, _, err := c.recipeOrStarter(p.household.ID, r.PathValue("id"))
	if s.storeError(w, "get recipe", err) {
		return
	}
	warnings := []recipeWarningJSON{}
	for _, wn := range c.warnings(got.Recipe) {
		warnings = append(warnings, recipeWarningJSON{Allergen: wn.Allergens[0], People: wn.People})
	}
	free := []freeAllergyJSON{}
	for _, x := range c.st.people {
		if items, _ := food.ParseOther(c.st.foods[x.ID].Other); len(items) > 0 {
			free = append(free, freeAllergyJSON{PersonID: x.ID, Text: strings.Join(items, ", ")})
		}
	}
	var diet *string
	if d := recipe.DietOf(got.Ingredients, c.lookup); d.Mark != "" {
		diet = &d.Mark
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"recipe_id": got.ID, "warnings": warnings, "not_checked_ingredients": nonNil(c.freeNames(got.Recipe)),
		"not_checked_allergies": free, "diet": diet, "note": recipeCheckNote,
	})
}

// recipeCheckNote is part of every answer about warnings: what Hejmo cannot check.
const recipeCheckNote = "Nur die 14 Hauptallergene von Artikeln der Bibliothek werden geprüft; freie Zutaten und freie Allergien der Personen sind nicht automatisch geprüft. Only the 14 main allergens of library articles are checked; free-text ingredients and people's free-text allergies are not checked automatically."
