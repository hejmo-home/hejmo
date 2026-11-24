package server

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// The recipe form (ticket 279): one page, one sticky primary button, only the
// name is required. Everything also works without a script: the buttons that
// add, move and remove lines post the form, which comes back with everything
// typed and the change made; an ingredient is matched with the library when
// the recipe is saved.

// rcpIngLine is an ingredient line of the form as it was typed, with what Hejmo made of it.
type rcpIngLine struct {
	N                                   int // 1, 2, 3 ... in the order of the form
	Amount, Unit, Name, Variety, Artcle string
	Blank                               bool
	State                               ingredientState
	ArtName                             string // the library's name of the article
	ShownVariety                        string
	Warns                               []template.HTML
	Err                                 string // the text of the error at the line
	Bad                                 string // which part: amount, unit, name or variety
	KnownVarieties                      []string
	VarietyOpen                         bool
	AriaRemove                          string
	VarLabel                            string        // "Variety of Flour"
	StateHTML                           template.HTML // "From the library: Flour · Type 405", with the name in bold
	FreeState                           string        // "Free text: Hejmo does not know ..."
}

// Num is the number of the line as the page writes it; a line made for the script's template says {n}.
func (l rcpIngLine) Num() string { return lineNumber(l.N) }

func lineNumber(n int) string {
	if n == 0 {
		return "{n}"
	}
	return strconv.Itoa(n)
}

// IsArticle says Hejmo made an article of the library of the line.
func (l rcpIngLine) IsArticle() bool { return l.State == ingArticle }

// rcpStepLine is a step of the form.
type rcpStepLine struct {
	N     int
	Text  string
	Blank bool
	// NoUp and NoDown leave out the buttons that would move the step nowhere:
	// the first step cannot go up, and none goes down below the empty line at the end.
	NoUp, NoDown bool
	Err          string
}

// Num is the number of the step as the page writes it; a step made for the script's template says {n}.
func (l rcpStepLine) Num() string { return lineNumber(l.N) }

// rcpForm is what the recipe form holds.
type rcpForm struct {
	Name, Category, Portions, Minutes string
	Mealtimes, Tags                   []string
	NewTag, Tips, Source              string
	Ings                              []rcpIngLine
	Steps                             []rcpStepLine
}

const (
	blankIngLines  = 3
	blankStepLines = 2
)

func (f *rcpForm) normalize() {
	for len(f.Ings) < blankIngLines {
		f.Ings = append(f.Ings, rcpIngLine{})
	}
	for len(f.Steps) < blankStepLines {
		f.Steps = append(f.Steps, rcpStepLine{})
	}
}

// formOfRecipe fills the form with a stored recipe: its lines, and one blank line after them to type into.
func (c *itemCtx) formOfRecipe(r recipe.Recipe) rcpForm {
	f := rcpForm{Name: r.Name, Category: string(r.Category), Portions: strconv.Itoa(r.Portions), Mealtimes: r.Mealtimes, Tags: r.Tags, Tips: r.Tips, Source: r.Source}
	if r.Minutes > 0 {
		f.Minutes = strconv.Itoa(r.Minutes)
	}
	for _, in := range r.Ingredients {
		line := rcpIngLine{Unit: in.Unit, Name: in.Text, Variety: in.Variety, Artcle: in.ArticleID}
		if in.Amount > 0 {
			line.Amount = recipe.FormatAmount(in.Amount, c.lang)
		}
		if in.ArticleID != "" && in.Variety != "" {
			line.Name = in.Text + " " + in.Variety // typing it again means the same
		}
		f.Ings = append(f.Ings, line)
	}
	f.Ings = append(f.Ings, rcpIngLine{})
	for _, st := range r.Steps {
		f.Steps = append(f.Steps, rcpStepLine{Text: st})
	}
	f.Steps = append(f.Steps, rcpStepLine{})
	return f
}

// readRcpForm reads the form as posted. The lines of an ingredient arrive as
// parallel lists, one value per line.
func readRcpForm(r *http.Request) rcpForm {
	_ = r.ParseForm()
	v := r.PostForm
	f := rcpForm{Name: v.Get("name"), Category: v.Get("category"), Portions: strings.TrimSpace(v.Get("portions")), Minutes: strings.TrimSpace(v.Get("minutes")),
		Mealtimes: v["mealtime"], Tags: v["tag"], NewTag: v.Get("new_tag"), Tips: v.Get("tips"), Source: v.Get("source")}
	at := func(key string, i int) string {
		if i < len(v[key]) {
			return v[key][i]
		}
		return ""
	}
	for i := range v["ing_name"] {
		f.Ings = append(f.Ings, rcpIngLine{Amount: at("ing_amount", i), Unit: at("ing_unit", i), Name: at("ing_name", i), Variety: at("ing_variety", i), Artcle: at("ing_article", i)})
	}
	for _, st := range v["step"] {
		f.Steps = append(f.Steps, rcpStepLine{Text: st})
	}
	return f
}

// act applies what a button of the form asked for: another line, a line moved or removed.
// It reports whether the form was only changed (and not saved).
func (f *rcpForm) act(act string) bool {
	switch {
	case act == "more-ing":
		f.Ings = append(f.Ings, rcpIngLine{})
	case act == "more-step":
		f.Steps = append(f.Steps, rcpStepLine{})
	case strings.HasPrefix(act, "rm-ing-"):
		if i, ok := lineOf(act, "rm-ing-", len(f.Ings)); ok {
			f.Ings = slices.Delete(f.Ings, i, i+1)
		}
	case strings.HasPrefix(act, "rm-step-"):
		if i, ok := lineOf(act, "rm-step-", len(f.Steps)); ok {
			f.Steps = slices.Delete(f.Steps, i, i+1)
		}
	case strings.HasPrefix(act, "up-step-"):
		if i, ok := lineOf(act, "up-step-", len(f.Steps)); ok && i > 0 {
			f.Steps[i-1], f.Steps[i] = f.Steps[i], f.Steps[i-1]
		}
	case strings.HasPrefix(act, "down-step-"):
		if i, ok := lineOf(act, "down-step-", len(f.Steps)); ok && i < len(f.Steps)-1 {
			f.Steps[i+1], f.Steps[i] = f.Steps[i], f.Steps[i+1]
		}
	default:
		return false
	}
	return true
}

// lineOf reads the 1-based number after a prefix as a 0-based index below n.
func lineOf(act, prefix string, n int) (int, bool) {
	k, err := strconv.Atoi(strings.TrimPrefix(act, prefix))
	return k - 1, err == nil && k >= 1 && k <= n
}

// formError is one problem of a saved form: where the summary links to, how it
// reads in the summary and the text at the field.
type formError struct {
	Anchor, Link string
}

// rcpIssues is what the checking of a form found.
type rcpIssues struct {
	Errors      []formError
	Name        template.HTML // the error at the name field, with a link when the name is taken
	Portions    string
	Minutes     string
	More        map[string]string // tips, source, tags
	ManyIngs    string
	ManySteps   string
	hasLineProb bool
}

func (is *rcpIssues) add(anchor, link string) {
	if !slices.ContainsFunc(is.Errors, func(e formError) bool { return e.Anchor == anchor && e.Link == link }) {
		is.Errors = append(is.Errors, formError{anchor, link})
	}
}

// resolveLines reads each ingredient line: what Hejmo made of it, and the first problem of it.
// With keep set, a line is the article the form chose while its text still names it.
func (c *itemCtx) resolveLines(f *rcpForm, keep bool) []recipe.Ingredient {
	var out []recipe.Ingredient
	for i := range f.Ings {
		l := &f.Ings[i]
		l.N = i + 1
		l.Err, l.Bad, l.Warns, l.ArtName, l.ShownVariety, l.KnownVarieties, l.StateHTML, l.FreeState = "", "", nil, "", "", nil, "", ""
		l.Name = strings.Join(strings.Fields(l.Name), " ")
		l.Blank = strings.TrimSpace(l.Amount) == "" && l.Unit == "" && l.Name == ""
		l.AriaRemove = c.t("recipes.f.ing.remove.aria.blank", "n", strconv.Itoa(l.N))
		if l.Name != "" {
			l.AriaRemove = fmt.Sprintf(c.s.i18n.T(c.lang, "recipes.f.ing.remove.aria"), l.N, l.Name)
		}
		in := recipe.IngredientInput{Amount: l.Amount, Unit: l.Unit, Name: l.Name, Variety: l.Variety}
		if id := strings.TrimSpace(l.Artcle); id != "" && keep {
			if it, ok := c.article(id); ok && namesArticle(l.Name, it) {
				in.ArticleID = id
			}
		}
		ing, art, err := c.recipeIngredient(in)
		var fe *recipe.FieldError
		switch {
		case err == nil:
			out = append(out, ing)
			l.State = ingFree
			l.Artcle = ""
			if art != nil {
				l.State, l.Artcle, l.ArtName, l.ShownVariety = ingArticle, art.ID, art.Name(c.lang), ing.Variety
				l.Warns = c.warnLines(library.Warnings(art.Article, c.st.persons))
				l.KnownVarieties = c.varieties(art.ID)
				l.Variety = ing.Variety
				l.StateHTML = c.stateHTML(l.ArtName, ing.Variety)
				l.VarLabel = c.t("recipes.ing.variety.label", "name", l.ArtName)
			} else {
				l.FreeState = c.t("recipes.ing.state.free", "name", ing.Text)
			}
		case errors.Is(err, recipe.ErrBlank):
			l.State = ingBlank
		case errors.As(err, &fe):
			l.State = ingFree
			l.Bad = fe.Field
			l.Err = c.ingError(fe, *l)
			if art != nil {
				l.State, l.Artcle, l.ArtName = ingArticle, art.ID, art.Name(c.lang)
				l.StateHTML = c.stateHTML(l.ArtName, l.Variety)
				l.VarLabel = c.t("recipes.ing.variety.label", "name", l.ArtName)
				l.ShownVariety = l.Variety
			}
		default:
			l.State = ingFree
		}
	}
	return out
}

// stateHTML is "From the library: Flour · Type 405" with the name in bold.
func (c *itemCtx) stateHTML(name, variety string) template.HTML {
	b := "<b>" + template.HTMLEscapeString(name)
	if variety != "" {
		b += `<span class="variety"> · ` + template.HTMLEscapeString(variety) + `</span>`
	}
	return c.htmlMsg("recipes.ing.state.article", "name", b+"</b>")
}

func (c *itemCtx) ingError(fe *recipe.FieldError, l rcpIngLine) string {
	switch fe.Field {
	case "amount":
		return c.t("recipes.err.ing.amount")
	case "unit":
		return c.t("recipes.err.ing.unit")
	case "variety":
		return c.t("recipes.err.ing.variety")
	case "article_id":
		return c.t("recipes.err.ing.article")
	}
	if fe.Msg == "too long" {
		return c.t("recipes.err.ing.name.long")
	}
	qty := strings.TrimSpace(l.Amount)
	if qty == "" {
		qty = "1"
	}
	if l.Unit != "" {
		qty += " " + c.unitWord(l.Unit, qty != "1")
	}
	return c.t("recipes.err.ing.name", "qty", qty)
}

// buildRecipe checks the form and returns the recipe it describes; issues says what is wrong.
func (c *itemCtx) buildRecipe(f *rcpForm, known []string, existing []store.Recipe, selfID string) (recipe.Recipe, *rcpIssues) {
	is := &rcpIssues{More: map[string]string{}}
	r := recipe.Recipe{Name: f.Name, Category: recipe.Category(f.Category), Mealtimes: f.Mealtimes}
	if r.Category == "" {
		r.Category = recipe.Main
	}
	if n, err := strconv.Atoi(f.Portions); err == nil {
		r.Portions = n
	}
	if f.Minutes != "" {
		if n, err := strconv.Atoi(f.Minutes); err == nil && n >= 0 {
			r.Minutes = n
		} else {
			r.Minutes = -1
		}
	}
	r.Ingredients = c.resolveLines(f, true)
	for i := range f.Steps {
		l := &f.Steps[i]
		l.N, l.Err = i+1, ""
		l.Blank = strings.TrimSpace(l.Text) == ""
		if utf8.RuneCountInString(strings.TrimSpace(l.Text)) > recipe.MaxStep {
			l.Err = c.t("recipes.err.step.long")
		}
		r.Steps = append(r.Steps, l.Text)
	}
	r.Tags = recipe.CanonTags(append(slices.Clone(f.Tags), recipe.SplitTags(f.NewTag)...), known)
	r.Tips, r.Source = f.Tips, f.Source
	// problems, in the order of the page
	if strings.TrimSpace(f.Name) == "" {
		is.Name = template.HTML(template.HTMLEscapeString(c.t("recipes.err.name.required")))
	} else if utf8.RuneCountInString(strings.Join(strings.Fields(f.Name), " ")) > recipe.MaxName {
		is.Name = template.HTML(template.HTMLEscapeString(c.t("recipes.err.name.long")))
	}
	if is.Name == "" {
		if i := slices.IndexFunc(existing, func(x store.Recipe) bool { return x.ID != selfID && recipe.NameKey(x.Name) == recipe.NameKey(f.Name) }); i >= 0 {
			is.Name = c.nameTaken(f.Name, existing[i].ID, existing)
		}
	}
	if is.Name != "" {
		is.add("rcp", c.t("recipes.err.link.name"))
	}
	if r.Portions < 1 || r.Portions > recipe.MaxPortions {
		is.Portions = c.t("recipes.err.portions")
		is.add("rcp", c.t("recipes.err.link.portions"))
	}
	if r.Minutes < 0 || r.Minutes > recipe.MaxMinutes {
		is.Minutes = c.t("recipes.err.minutes")
		is.add("rcp", c.t("recipes.err.link.minutes"))
	}
	for _, l := range f.Ings {
		if l.Err != "" {
			is.add("ing"+strconv.Itoa(l.N), c.t("recipes.err.link.ing", "n", strconv.Itoa(l.N)))
		}
	}
	if len(r.Ingredients) > recipe.MaxIngredients {
		is.ManyIngs = c.t("recipes.err.ings.many")
		is.add("ings", c.t("recipes.err.link.more"))
	}
	for _, l := range f.Steps {
		if l.Err != "" {
			is.add("step"+strconv.Itoa(l.N), c.t("recipes.err.link.step", "n", strconv.Itoa(l.N)))
		}
	}
	if len(slices.DeleteFunc(slices.Clone(r.Steps), func(s string) bool { return strings.TrimSpace(s) == "" })) > recipe.MaxSteps {
		is.ManySteps = c.t("recipes.err.steps.many")
		is.add("steps", c.t("recipes.err.link.more"))
	}
	if utf8.RuneCountInString(strings.TrimSpace(f.Tips)) > recipe.MaxTips {
		is.More["tips"] = c.t("recipes.err.tips")
		is.add("more", c.t("recipes.err.link.tips"))
	}
	if utf8.RuneCountInString(strings.TrimSpace(f.Source)) > recipe.MaxSource {
		is.More["source"] = c.t("recipes.err.source")
		is.add("more", c.t("recipes.err.link.source"))
	}
	for _, tag := range r.Tags {
		if utf8.RuneCountInString(tag) > recipe.MaxTag {
			is.More["tags"] = c.t("recipes.err.tags")
			is.add("more", c.t("recipes.err.link.tags"))
			break
		}
	}
	if len(r.Tags) > recipe.MaxTags && is.More["tags"] == "" {
		is.More["tags"] = c.t("recipes.err.tags")
		is.add("more", c.t("recipes.err.link.tags"))
	}
	if len(is.Errors) > 0 {
		return r, is
	}
	return r, nil
}

// mealtimeChips are the four mealtimes with whether the form chose them.
type rcpMealtime struct {
	Key, Name string
	Checked   bool
}

type rcpTagChip struct {
	Tag     string
	Checked bool
}

// formPage renders the form of a new recipe (id empty) or of an existing one.
func (c *itemCtx) formPage(w http.ResponseWriter, r *http.Request, id, name string, f rcpForm, is *rcpIssues, status int, known []string) {
	f.normalize()
	if f.Category == "" {
		f.Category = string(recipe.Main)
	}
	c.resolveLines(&f, true)
	for i := range f.Steps {
		f.Steps[i].N, f.Steps[i].Blank = i+1, strings.TrimSpace(f.Steps[i].Text) == ""
	}
	for i := range f.Steps {
		st := &f.Steps[i]
		st.NoUp, st.NoDown = false, false
		if !st.Blank {
			st.NoUp = i == 0
			st.NoDown = i == len(f.Steps)-1 || f.Steps[i+1].Blank
		}
	}
	var mealtimes []rcpMealtime
	for _, m := range meal.Mealtimes() {
		mealtimes = append(mealtimes, rcpMealtime{Key: string(m), Name: c.t("meals.mealtime." + string(m)), Checked: slices.Contains(f.Mealtimes, string(m))})
	}
	// the tags in use, then the ones typed that are not in use
	var chips []rcpTagChip
	for _, tag := range known {
		chips = append(chips, rcpTagChip{tag, slices.ContainsFunc(f.Tags, func(t string) bool { return strings.EqualFold(t, tag) })})
	}
	for _, tag := range f.Tags {
		if !slices.ContainsFunc(chips, func(x rcpTagChip) bool { return strings.EqualFold(x.Tag, tag) }) {
			chips = append(chips, rcpTagChip{tag, true})
		}
	}
	var cats []map[string]any
	for _, k := range recipe.Categories {
		cats = append(cats, map[string]any{"Key": string(k), "Symbol": k.Symbol(), "Name": c.categoryName(k), "Checked": f.Category == string(k)})
	}
	var parts []string
	if len(f.Mealtimes) > 0 {
		parts = append(parts, c.t("recipes.f.more.mealtimes"))
	}
	if len(chips) > 0 && (len(f.Tags) > 0 || strings.TrimSpace(f.NewTag) != "") {
		parts = append(parts, c.t("recipes.f.more.tags"))
	}
	if strings.TrimSpace(f.Tips) != "" {
		parts = append(parts, c.t("recipes.f.more.tips"))
	}
	if strings.TrimSpace(f.Source) != "" {
		parts = append(parts, c.t("recipes.f.more.source"))
	}
	sum := c.t("recipes.f.more.none")
	if len(parts) > 0 {
		sum = c.t("recipes.f.more.sum", "parts", c.names(parts))
	}
	groups := c.unitGroups()
	data := map[string]any{
		"F": f, "Cats": cats, "Mealtimes": mealtimes, "TagChips": chips, "MoreSum": sum, "MoreOpen": is != nil && len(is.More) > 0,
		"UnitGroups": groups, "Issues": is, "Counter": fmt.Sprintf(c.s.i18n.T(c.lang, "recipes.f.tips.counter"), utf8.RuneCountInString(f.Tips), recipe.MaxTips),
		"MaxTips": recipe.MaxTips, "RecipeID": id, "Editing": id != "",
		"Action": recipesPath, "CancelHref": recipesPath, "IngTpl": rcpIngLine{N: 0, Blank: true}, "StepTpl": rcpStepLine{Blank: true},
	}
	title, lead, save := c.t("recipes.title"), c.t("recipes.new.lead"), c.t("recipes.save.new")
	if id != "" {
		data["Action"] = recipesPath + "/" + id
		data["CancelHref"] = recipesPath + "/" + id
		data["RecipeName"] = name
		title, lead, save = c.t("recipes.edit.title"), c.t("recipes.edit.lead"), c.t("recipes.save")
	} else {
		title = c.t("recipes.new")
	}
	data["Title"], data["Lead"], data["Save"] = title, lead, save
	if is != nil && len(is.Errors) > 0 {
		var links []string
		for _, e := range is.Errors {
			links = append(links, htmlLink("#"+e.Anchor, e.Link))
		}
		data["ErrSummary"] = c.s.joinHTMLWords(c.lang, links, c.s.i18n.T(c.lang, "recipes.err.lead"))
	}
	c.s.render(w, r, status, "recipe_form.html", data)
}

// joinHTMLWords puts links, joined like words, into a sentence at its {links}.
func (s *server) joinHTMLWords(lang string, links []string, sentence string) template.HTML {
	text := template.HTMLEscapeString(sentence)
	return template.HTML(strings.Replace(text, "{links}", s.joinWords(lang, links), 1)) // #nosec G203 -- the links are built with escaped text, the sentence is ours
}

type rcpUnitGroup struct {
	Label string
	Units []rcpUnitOption
}
type rcpUnitOption struct{ Key, Name string }

// unitGroups are the units of the select: the household's, the kitchen measures, the containers.
func (c *itemCtx) unitGroups() []rcpUnitGroup {
	household := unit.Normalize(c.active)
	var containers []string
	for _, k := range unit.Containers {
		if !slices.Contains(household, k) {
			containers = append(containers, k)
		}
	}
	opts := func(keys []string) []rcpUnitOption {
		var out []rcpUnitOption
		for _, k := range keys {
			out = append(out, rcpUnitOption{k, c.s.unitName(c.lang, k)})
		}
		return out
	}
	return []rcpUnitGroup{
		{c.t("recipes.units.group.household"), opts(household)},
		{c.t("recipes.units.group.kitchen"), opts(unit.Kitchen)},
		{c.t("recipes.units.group.container"), opts(containers)},
	}
}

// ---- handlers

func (s *server) recipeFormContext(w http.ResponseWriter, r *http.Request) (principal, *itemCtx, []string, bool) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return p, nil, nil, false
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "recipe context", err)
		return p, nil, nil, false
	}
	known, err := s.db.RecipeTags(p.household.ID)
	if err != nil {
		s.fail(w, "recipe tags", err)
		return p, nil, nil, false
	}
	return p, c, known, true
}

func (s *server) handleRecipeNew(w http.ResponseWriter, r *http.Request) {
	_, c, known, ok := s.recipeFormContext(w, r)
	if !ok {
		return
	}
	f := rcpForm{Name: strings.Join(strings.Fields(r.URL.Query().Get("name")), " "), Category: string(recipe.Main), Portions: strconv.Itoa(c.householdPortions())}
	c.formPage(w, r, "", "", f, nil, http.StatusOK, known)
}

func (s *server) handleRecipeEdit(w http.ResponseWriter, r *http.Request) {
	p, c, known, ok := s.recipeFormContext(w, r)
	if !ok {
		return
	}
	rec, err := s.db.Recipe(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, "recipe", err)
		return
	}
	c.formPage(w, r, rec.ID, rec.Name, c.formOfRecipe(rec.Recipe), nil, http.StatusOK, known)
}

func (s *server) handleRecipeCreate(w http.ResponseWriter, r *http.Request) {
	s.saveRecipePage(w, r, "")
}
func (s *server) handleRecipeSave(w http.ResponseWriter, r *http.Request) {
	s.saveRecipePage(w, r, r.PathValue("id"))
}

// saveRecipePage handles the form: a button that changes the lines shows the form again, anything else saves.
func (s *server) saveRecipePage(w http.ResponseWriter, r *http.Request, id string) {
	p, c, known, ok := s.recipeFormContext(w, r)
	if !ok {
		return
	}
	name := ""
	if id != "" {
		rec, err := s.db.Recipe(p.household.ID, id)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			s.fail(w, "recipe", err)
			return
		}
		name = rec.Name
	}
	f := readRcpForm(r)
	if f.act(r.PostFormValue("act")) {
		c.formPage(w, r, id, name, f, nil, http.StatusOK, known)
		return
	}
	existing, err := s.db.Recipes(p.household.ID)
	if err != nil {
		s.fail(w, "recipes", err)
		return
	}
	rec, is := c.buildRecipe(&f, known, existing, id)
	if is != nil {
		c.formPage(w, r, id, name, f, is, http.StatusUnprocessableEntity, known)
		return
	}
	var saved store.Recipe
	if id == "" {
		saved, err = s.db.CreateRecipe(p.household.ID, p.person.ID, rec)
	} else {
		rec.ID = id
		saved, err = s.db.UpdateRecipe(p.household.ID, p.person.ID, rec)
	}
	var taken *store.RecipeNameTakenError
	var fe *store.FieldError
	switch {
	case errors.As(err, &taken):
		is := &rcpIssues{More: map[string]string{}}
		is.Name = c.nameTaken(f.Name, taken.ExistingID, nil)
		is.add("rcp", c.t("recipes.err.link.name"))
		c.formPage(w, r, id, name, f, is, http.StatusUnprocessableEntity, known)
		return
	case errors.As(err, &fe):
		is := &rcpIssues{More: map[string]string{}}
		is.add("rcp", fe.Field)
		is.Name = template.HTML(template.HTMLEscapeString(fe.Msg))
		c.formPage(w, r, id, name, f, is, http.StatusUnprocessableEntity, known)
		return
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		s.fail(w, "save recipe", err)
		return
	}
	s.redirectSavedNamed(w, r, recipesPath+"/"+saved.ID, "flash.recipe_saved", saved.Name)
}

// nameTaken is the error of a name another recipe has: it proposes another name
// and links to the one that exists.
func (c *itemCtx) nameTaken(typed, existingID string, list []store.Recipe) template.HTML {
	typed = strings.Join(strings.Fields(typed), " ")
	if list == nil {
		list, _ = c.s.db.Recipes(c.p.household.ID)
	}
	used := func(name string) bool {
		return slices.ContainsFunc(list, func(r store.Recipe) bool { return recipe.NameKey(r.Name) == recipe.NameKey(name) })
	}
	proposal := c.t("recipes.err.name.proposal", "name", typed)
	for n := 2; used(proposal) && n < 50; n++ {
		proposal = c.t("recipes.err.name.proposal", "name", typed) + " " + strconv.Itoa(n)
	}
	text := template.HTMLEscapeString(c.s.i18n.T(c.lang, "recipes.err.name.taken"))
	text = repl(text, "{name}", template.HTMLEscapeString(typed), "{proposal}", template.HTMLEscapeString(proposal),
		"{link}", htmlLink(recipesPath+"/"+existingID, c.t("recipes.err.name.taken.link")))
	return template.HTML(text) // #nosec G203 -- the names are escaped, the text is ours
}

// ---- suggestions under an ingredient line

type rcpSugRow struct {
	ID, Name, Variety, Symbol, Sub string
	Warns                          []template.HTML
}

type rcpSuggView struct {
	N, Intro, FreeName, FreeSub string
	Rows                        []rcpSugRow
}

// maxSuggestedArticles is how many articles are offered under an ingredient line, and
// maxSuggestedVarieties how many varieties used before each of them has.
const (
	maxSuggestedArticles  = 4
	maxSuggestedVarieties = 2
)

// handleRecipeSuggest answers the list under an ingredient line while it is typed (the script of the form asks
// for it): articles of the library that fit, with the warning already in the row, and the typed text as free
// text last. Without the script the name is matched when the recipe is saved.
func (s *server) handleRecipeSuggest(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	c, err := s.itemContext(p, lang)
	if err != nil {
		s.fail(w, "recipe context", err)
		return
	}
	typed := strings.Join(strings.Fields(r.URL.Query().Get("q")), " ")
	n := r.URL.Query().Get("n")
	if _, err := strconv.Atoi(n); err != nil || utf8.RuneCountInString(typed) < 2 || utf8.RuneCountInString(typed) > recipe.MaxIngredient {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	view := rcpSuggView{N: n, FreeName: c.t("recipes.sugg.free", "q", typed), FreeSub: c.t("recipes.sugg.free.hint")}
	articles := library.Similar(typed, c.visibleArticles(), lang, maxSuggestedArticles)
	for _, a := range articles {
		it, ok := c.article(a.ID)
		if !ok {
			continue
		}
		symbol := it.Symbol
		if symbol == "" {
			symbol = library.DeptSymbol(it.Dept)
		}
		dept := s.i18n.T(lang, "dept."+it.Dept)
		warns := c.warnLines(library.Warnings(it.Article, c.st.persons))
		view.Rows = append(view.Rows, rcpSugRow{ID: it.ID, Name: it.Name(lang), Symbol: symbol, Warns: warns, Sub: c.t("recipes.sugg.novariety") + " · " + dept})
		varieties := c.varieties(it.ID)
		if len(varieties) > maxSuggestedVarieties {
			varieties = varieties[:maxSuggestedVarieties]
		}
		for _, v := range varieties {
			view.Rows = append(view.Rows, rcpSugRow{ID: it.ID, Name: it.Name(lang), Variety: v, Symbol: symbol, Warns: warns, Sub: c.t("recipes.sugg.variety", "name", it.Name(lang)) + " · " + dept})
		}
	}
	switch len(articles) {
	case 0:
		view.Intro = c.t("recipes.sugg.title.none", "q", typed)
	case 1:
		view.Intro = c.t("recipes.sugg.title.one", "q", typed)
	default:
		view.Intro = c.t("recipes.sugg.title.other", "q", typed, "n", strconv.Itoa(len(articles)))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.execute(w, "rcpSuggest", lang, view); err != nil {
		s.logger.Error("render suggestions", "err", err)
	}
}
