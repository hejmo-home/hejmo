package server

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/recipeimport"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The import of a recipe from a web page over the REST API (ticket 289, ADR 0015): read a page into a draft,
// get the draft, save it with a choice for every line Hejmo does not know. The same rules as the pages.

type importProposalJSON struct {
	ArticleID string  `json:"article_id"`
	Name      string  `json:"name"`
	Variety   *string `json:"variety"`
}

type importLineJSON struct {
	Original  string               `json:"original"`
	Quantity  *float64             `json:"quantity"`
	Unit      *string              `json:"unit"`
	Name      string               `json:"name"`
	Variety   *string              `json:"variety"`
	ArticleID *string              `json:"article_id"`
	Status    string               `json:"status"`
	Change    *string              `json:"change"`
	Proposals []importProposalJSON `json:"proposals"`
}

type importNutritionJSON struct {
	KCal      *float64 `json:"kcal"`
	KJ        *float64 `json:"kj"`
	Fat       *float64 `json:"fat"`
	Saturates *float64 `json:"saturates"`
	Carbs     *float64 `json:"carbs"`
	Sugars    *float64 `json:"sugars"`
	Protein   *float64 `json:"protein"`
	Salt      *float64 `json:"salt"`
}

type importRecipeJSON struct {
	Name           string               `json:"name"`
	Category       string               `json:"category"`
	CategoryWord   *string              `json:"category_word"`
	Portions       int                  `json:"portions"`
	PortionsStatus string               `json:"portions_status"`
	YieldText      *string              `json:"yield_text"`
	Minutes        *int                 `json:"minutes"`
	Language       *string              `json:"language"`
	Keywords       []string             `json:"keywords"`
	Ingredients    []importLineJSON     `json:"ingredients"`
	Steps          []string             `json:"steps"`
	HasPicture     bool                 `json:"has_picture"`
	Nutrition      *importNutritionJSON `json:"nutrition"`
}

type importCandidateJSON struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	Ingredients int    `json:"ingredients"`
	Steps       int    `json:"steps"`
	Minutes     *int   `json:"minutes"`
	HasPicture  bool   `json:"has_picture"`
}

type importDraftJSON struct {
	ID               string                `json:"id"`
	URL              string                `json:"url"`
	Host             string                `json:"host"`
	ReadAt           string                `json:"read_at"`
	ExpiresAt        string                `json:"expires_at"`
	ExistingRecipeID *string               `json:"existing_recipe_id,omitempty"`
	Candidates       []importCandidateJSON `json:"candidates"`
	Candidate        int                   `json:"candidate"`
	Recipe           importRecipeJSON      `json:"recipe"`
}

// importFieldError is the text of a refused page for the API: the field and what went wrong, in English.
func importFieldError(e *importErr) string {
	switch e.Kind {
	case "empty":
		return "url: required"
	case "notlink":
		return "url: not a link; it starts with https:// or http://"
	case "private":
		return "url: only web pages on the internet are read, not number addresses or the home network (" + e.Host + ")"
	case "dns":
		return "url: the site " + e.Host + " does not exist"
	case "timeout":
		return "url: " + e.Host + " did not answer within 20 seconds"
	case "gone":
		return "url: the page does not exist on " + e.Host
	case "refused":
		return "url: " + e.Host + " does not let Hejmo read the page"
	case "notpage":
		return "url: this is a file (" + e.Extra + "), not a web page"
	case "big":
		return "url: the page is larger than 5 MB"
	case "norecipe":
		return "url: no recipe found on the page (Hejmo reads schema.org/Recipe)"
	}
	return "url: the site " + e.Host + " cannot be reached"
}

func (s *server) offNow(w http.ResponseWriter) bool {
	switch s.importOff() {
	case "":
		return false
	case "offline":
		writeError(w, http.StatusConflict, "offline mode is on: Hejmo reads no web pages")
	default:
		writeError(w, http.StatusConflict, "the connection \"recipes\" is switched off")
	}
	return true
}

func (s *server) apiReadRecipePage(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		URL string `json:"url"`
	}
	if !readJSON(r, &in) {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if s.offNow(w) {
		return
	}
	link, host, data, ierr := s.readRecipePage(r.Context(), in.URL)
	if ierr != nil {
		if ierr.Kind == "off" {
			s.offNow(w)
			return
		}
		writeFieldError(w, http.StatusUnprocessableEntity, "url", importFieldError(ierr))
		return
	}
	s.sweepDrafts()
	d, err := s.newDraft(p, link, host, data)
	if err != nil {
		s.fail(w, "recipe draft", err)
		return
	}
	c, ok := s.apiRecipeContext(w, r, p)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, c.draftToJSON(draftState{d: d, data: data}, 0))
}

// apiDraft opens a draft of the person: found is false after the answer was written.
func (s *server) apiDraft(w http.ResponseWriter, p principal, id string) (draftState, bool) {
	st, found, expired, err := s.loadDraft(p, id)
	switch {
	case err != nil:
		s.fail(w, "recipe draft", err)
	case !found:
		writeError(w, http.StatusNotFound, "not found")
	case expired:
		writeError(w, http.StatusGone, "the page was read more than an hour ago; nothing was saved, read the link again")
	default:
		return st, true
	}
	return st, false
}

func (s *server) apiGetRecipeImport(w http.ResponseWriter, r *http.Request, p principal) {
	st, ok := s.apiDraft(w, p, r.PathValue("id"))
	if !ok {
		return
	}
	n := 0
	if v := r.URL.Query().Get("candidate"); v != "" {
		var err error
		if n, err = strconv.Atoi(v); err != nil || n < 0 || n >= len(st.data.Candidates) {
			writeFieldError(w, http.StatusUnprocessableEntity, "candidate", "candidate: no such recipe on the page")
			return
		}
	}
	c, ok := s.apiRecipeContext(w, r, p)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, c.draftToJSON(st, n))
}

func (s *server) apiDiscardRecipeImport(w http.ResponseWriter, r *http.Request, p principal) {
	st, ok := s.apiDraft(w, p, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.db.DeleteRecipeDraft(p.household.ID, st.d.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "delete draft", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func nutritionToJSON(n *recipeimport.Nutrition) *importNutritionJSON {
	if n == nil {
		return nil
	}
	return &importNutritionJSON{KCal: n.KCal, KJ: n.KJ, Fat: n.Fat, Saturates: n.Saturates, Carbs: n.Carbs, Sugars: n.Sugars, Protein: n.Protein, Salt: n.Salt}
}

func (c *itemCtx) draftToJSON(st draftState, n int) importDraftJSON {
	cand := st.data.Candidates[n]
	out := importDraftJSON{ID: st.d.ID, URL: st.d.URL, Host: st.d.Host, ReadAt: st.d.ReadAt.UTC().Format(time.RFC3339),
		ExpiresAt: st.d.ReadAt.Add(draftTTL).UTC().Format(time.RFC3339), Candidate: n, Candidates: []importCandidateJSON{}}
	if ref, ok, err := c.s.db.RecipeImportedFrom(c.p.household.ID, st.d.URL); err == nil && ok {
		out.ExistingRecipeID = &ref.ID
	}
	for i, f := range st.data.Candidates {
		cj := importCandidateJSON{Index: i, Name: f.Name, Ingredients: len(f.Ingredients), Steps: len(f.Steps), HasPicture: len(st.data.Pictures[i]) > 0}
		if f.Minutes > 0 {
			m := f.Minutes
			cj.Minutes = &m
		}
		out.Candidates = append(out.Candidates, cj)
	}
	rj := importRecipeJSON{Name: cand.Name, Category: cand.Category, CategoryWord: strPtr(cand.CategoryWord), Portions: cand.Portions, PortionsStatus: "stated",
		YieldText: strPtr(cand.YieldText), Language: strPtr(cand.Lang), Keywords: nonNil(cand.Keywords), Steps: nonNil(cand.Steps), HasPicture: len(st.data.Pictures[n]) > 0,
		Nutrition: nutritionToJSON(cand.Nutrition), Ingredients: []importLineJSON{}}
	switch {
	case !cand.PortionsGiven && cand.Portions > 0:
		rj.PortionsStatus = "from_yield"
	case !cand.PortionsGiven:
		rj.PortionsStatus, rj.Portions = "household", c.householdPortions()
	}
	if cand.Minutes > 0 {
		m := cand.Minutes
		rj.Minutes = &m
	}
	units := c.recipeUnits()
	for _, l := range cand.Ingredients {
		lj := importLineJSON{Original: l.Original, Name: l.Head, Unit: strPtr(l.Unit), Proposals: []importProposalJSON{}, Status: "needs_choice", Change: strPtr(l.Change)}
		if !slices.Contains(units, l.Unit) {
			lj.Unit = nil
		}
		if l.Amount > 0 && l.Amount <= recipe.MaxAmount {
			q := recipe.FormatAmountNumber(l.Amount)
			lj.Quantity = &q
		}
		if _, art, err := c.recipeIngredient(recipe.IngredientInput{Name: l.Head}); err == nil && art != nil {
			lj.Status, lj.ArticleID, lj.Name = "library", &art.ID, art.Name(c.lang)
			if len([]rune(l.Rest)) <= recipe.MaxVariety {
				lj.Variety = strPtr(l.Rest)
			}
		} else {
			for _, pr := range recipe.Propose(l.Head, c.visibleArticles(), c.lang, 3) {
				if it, ok := c.article(pr.Article.ID); ok {
					lj.Proposals = append(lj.Proposals, importProposalJSON{ArticleID: it.ID, Name: it.Name(c.lang), Variety: strPtr(pr.Variety)})
				}
			}
		}
		rj.Ingredients = append(rj.Ingredients, lj)
	}
	out.Recipe = rj
	return out
}

type importIngredientJSON struct {
	recipeIngredientJSON
	Choice string `json:"choice"`
}

type importSaveJSON struct {
	Candidate   int                    `json:"candidate"`
	Name        string                 `json:"name"`
	Category    string                 `json:"category"`
	Portions    *int                   `json:"portions"`
	Minutes     *int                   `json:"minutes"`
	Mealtimes   []string               `json:"mealtimes"`
	Tags        []string               `json:"tags"`
	Tips        string                 `json:"tips"`
	Ingredients []importIngredientJSON `json:"ingredients"`
	Steps       []string               `json:"steps"`
	Picture     *bool                  `json:"picture"`
}

func (s *server) apiSaveRecipeImport(w http.ResponseWriter, r *http.Request, p principal) {
	var in importSaveJSON
	if !readJSON(r, &in) {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	st, ok := s.apiDraft(w, p, r.PathValue("id"))
	if !ok {
		return
	}
	if in.Candidate < 0 || in.Candidate >= len(st.data.Candidates) {
		writeFieldError(w, http.StatusUnprocessableEntity, "candidate", "candidate: no such recipe on the page")
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
	// the same reading as POST /recipes, with the lines kept apart to tell which ones need a choice
	plain := recipeInput{Name: in.Name, Category: in.Category, Portions: in.Portions, Minutes: in.Minutes, Mealtimes: in.Mealtimes, Tags: in.Tags, Tips: in.Tips, Steps: in.Steps}
	for _, l := range in.Ingredients {
		plain.Ingredients = append(plain.Ingredients, l.recipeIngredientJSON)
	}
	rec, bad := c.fromInput(plain, known)
	if bad != nil {
		writeFieldError(w, http.StatusUnprocessableEntity, bad.Field, bad.Msg)
		return
	}
	// which line of the request is which ingredient of the recipe: blank lines are not kept
	var own []int // indexes into rec.Ingredients
	k := 0
	for i, l := range in.Ingredients {
		line := recipe.IngredientInput{Name: l.Name, Variety: derefStr(l.Variety), ArticleID: derefStr(l.ArticleID)}
		if l.Quantity != nil {
			line.Amount = fmt.Sprint(*l.Quantity)
		}
		line.Unit = derefStr(l.Unit)
		if _, _, err := c.recipeIngredient(line); err != nil {
			continue // blank
		}
		ing := rec.Ingredients[k]
		if ing.ArticleID == "" {
			switch l.Choice {
			case "own":
				own = append(own, k)
			case "free":
			default:
				writeFieldError(w, http.StatusUnprocessableEntity, fmt.Sprintf("ingredients[%d].choice", i), fmt.Sprintf("ingredients[%d].choice: %q is no article of the library; choose own (make an own article) or free (keep free text)", i, ing.Text))
				return
			}
		}
		k++
	}
	var made []store.OwnArticle
	byName := map[string]string{}
	for _, at := range own {
		key := library.Fold(rec.Ingredients[at].Text)
		if id, ok := byName[key]; ok {
			rec.Ingredients[at].ArticleID = id
			continue
		}
		a, err := s.db.CreateArticle(p.household.ID, p.person.ID, store.OwnArticle{Name: rec.Ingredients[at].Text, Dept: library.DefaultDept, Kind: string(library.Veg), Unit1: c.ownUnit()}, nil)
		if err != nil {
			s.undoArticles(p, made)
			s.fail(w, "create article", err)
			return
		}
		made = append(made, a)
		byName[key] = a.ID
		rec.Ingredients[at].ArticleID = a.ID
	}
	cand := st.data.Candidates[in.Candidate]
	imp := store.Imported{URL: st.d.URL, Host: st.d.Host, Lang: cand.Lang, DraftID: st.d.ID, Nutrition: pageNutrition(cand.Nutrition)}
	if in.Picture == nil || *in.Picture {
		imp.Cover = st.data.Pictures[in.Candidate]
	}
	saved, err := s.db.CreateImportedRecipe(p.household.ID, p.person.ID, rec, imp)
	if err != nil {
		s.undoArticles(p, made)
		if s.recipeError(w, "save imported recipe", err) {
			return
		}
	}
	writeJSON(w, http.StatusCreated, c.recipeToJSON(saved))
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var _ = strings.TrimSpace
