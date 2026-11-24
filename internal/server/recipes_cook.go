package server

import (
	"errors"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The cooking view (ticket 286, ADR 0015 and 0021, preview-286.html): large
// text, ingredients to tick, one step at a time, on every device. Parents and
// children both open it. The page is one GET form: portions, the ticked
// ingredients (have), the step and the part a phone shows (view) travel in the
// address, so it works without script; static/cook.js handles the same form in
// place. Nothing is stored: ticks and step live on the device and in the address.

// cookIng is one ingredient to tick.
type cookIng struct {
	Index              int
	Checked, Free      bool
	Symbol, Picture    string
	Qty, Name, Variety string
	Warns              []template.HTML
}

// cookDot is one numbered button that jumps to a step.
type cookDot struct {
	I             int
	Done, Current bool
	Label         string
}

// cookStep is the step card and the buttons below it for one step. The page
// holds one for every step: the shown one and, for the script, a template each.
type cookStep struct {
	K                            int
	Title                        string
	Dots                         []cookDot
	Text, Next                   string
	Last                         bool
	BackStep, FwdStep            int // BackStep 0: "To the ingredients" (a phone only); FwdStep 0: Done
	BackLabel, FwdLabel, ContLbl string
}

var (
	cookDigits   = regexp.MustCompile(`^\+?\d+$`)
	cookNegative = regexp.MustCompile(`^-\d+$`)
)

// clampPortions keeps a stepper value within 1 and 99.
func clampPortions(n int) int { return min(max(n, 1), recipe.MaxPortions) }

// cookPortionsParam picks the portions out of what the address carries. The
// stepper's buttons and the field all send "portions": minus before the field,
// plus after it, and the form says which number it showed (from). A button sends
// that number minus or plus one; it wins over what was typed.
func cookPortionsParam(vals []string, from int) (string, bool) {
	switch len(vals) {
	case 0:
		return "", false
	case 1:
		return vals[0], true
	}
	first, last := vals[0], vals[len(vals)-1]
	minus, plus := strconv.Itoa(clampPortions(from-1)), strconv.Itoa(clampPortions(from+1))
	switch {
	case first == minus:
		return first, true
	case last == plus:
		return last, true
	}
	return last, true
}

// cookStepParam reads the step: a number, clamped to the steps there are; a
// number too large for an int is past the end, anything else is the first.
func cookStepParam(raw string, n int) int {
	raw = strings.TrimSpace(raw)
	if n == 0 {
		return 1
	}
	if cookDigits.MatchString(raw) {
		if v, err := strconv.Atoi(raw); err == nil {
			return min(max(v, 1), n)
		}
		return n
	}
	if cookNegative.MatchString(raw) {
		return 1
	}
	return 1
}

func lastOf(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	return vals[len(vals)-1]
}

func (s *server) handleRecipeCook(w http.ResponseWriter, r *http.Request) {
	p, ok := s.memberPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	c, err := s.itemContext(p, lang)
	if err != nil {
		s.fail(w, "recipe context", err)
		return
	}
	rec, err := s.db.Recipe(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, r, http.StatusNotFound, "recipe_gone.html", map[string]any{"Title": c.t("cook.gone.crumb"), "RecipesHref": recipesPath})
		return
	}
	if err != nil {
		s.fail(w, "recipe", err)
		return
	}
	q := r.URL.Query()
	if q.Get("done") == "1" { // writes nothing: "last cooked" belongs to ticket 287
		s.redirectSavedNamed(w, r, recipesPath+"/"+rec.ID, "flash.cook_done", rec.Name)
		return
	}
	parent := auth.RoleAllows(p.person.Role, auth.Perm("meals", auth.Write))
	base := rec.Portions

	// Portions: a typed number that is no number from 1 to 99 stays in the field with
	// its error, and the quantities stay as written for the recipe's own portions.
	from := base
	if v, err := strconv.Atoi(q.Get("from")); err == nil {
		from = v
	}
	portions, typed, invalid, stepFrom := base, strconv.Itoa(base), false, base
	if raw, has := cookPortionsParam(q["portions"], from); has {
		if n, ok := recipe.ParsePortions(raw); ok {
			portions, typed, stepFrom = n, strconv.Itoa(n), n
		} else {
			invalid = true
			typed = raw
			if utf8.RuneCountInString(typed) > 20 {
				typed = string([]rune(typed)[:20])
			}
			if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
				stepFrom = n
			}
		}
	}
	sc := scaledRecipe(rec, portions)

	// The ticked ingredients.
	ticked := map[int]bool{}
	for _, v := range q["have"] {
		if i, err := strconv.Atoi(v); err == nil && i >= 0 && i < len(rec.Ingredients) {
			ticked[i] = true
		}
	}
	var ings []cookIng
	for i, in := range sc.Ingredients {
		v := cookIng{Index: i, Checked: ticked[i], Qty: c.qtyText(in), Name: in.Text, Variety: in.Variety}
		if it, ok := c.article(in.ArticleID); ok {
			v.Symbol = symbolOf(it)
			if it.own != nil && it.own.HasPicture {
				v.Picture = pictureURL(*it.own)
			}
			v.Warns = c.warnLines(library.Warnings(it.Article, c.st.persons))
		} else {
			v.Free = true
		}
		ings = append(ings, v)
	}

	// The step and the part a phone shows.
	n := len(rec.Steps)
	k := cookStepParam(lastOf(q["step"]), n)
	view := lastOf(q["view"])
	if view != "ings" && view != "steps" {
		view = "ings"
		if q.Has("step") {
			view = "steps"
		}
	}
	var steps []cookStep
	for i := 1; i <= n; i++ {
		steps = append(steps, c.cookStepView(rec.Steps, i))
	}

	count := func(key string, kv ...string) string { return c.t(key, kv...) }
	tabIngs, tabSteps := count("cook.none"), count("cook.none")
	if len(ings) > 0 {
		tabIngs = count("cook.ticked", "k", strconv.Itoa(len(ticked)), "n", strconv.Itoa(len(ings)))
	}
	if n > 0 {
		tabSteps = count("cook.step", "k", strconv.Itoa(k), "n", strconv.Itoa(n))
	}
	ones := func(key string) string { // the sentence with its number, and the one for 1 portion
		return c.s.i18n.T(lang, key)
	}
	data := map[string]any{
		"R": rec, "Title": rec.Name, "Parent": parent, "ID": rec.ID, "RecipeHref": recipesPath + "/" + rec.ID, "EditHref": recipesPath + "/" + rec.ID + "/edit",
		"View": view, "Step": k, "Steps": steps, "HasSteps": n > 0, "Ings": ings, "Typed": typed, "Invalid": invalid, "Base": base, "From": stepFrom,
		"Less": clampPortions(stepFrom - 1), "More": clampPortions(stepFrom + 1),
		"Ticked": len(ticked), "TabIngs": tabIngs, "TabSteps": tabSteps,
		"TickedText": count("cook.ticked", "k", strconv.Itoa(len(ticked)), "n", strconv.Itoa(len(ings))), "AllReady": len(ings) > 0 && len(ticked) == len(ings),
		"AllReadyText": count("cook.allready", "n", strconv.Itoa(len(ings))),
		"TickedTpl":    ones("cook.ticked"), "AllReadyTpl": ones("cook.allready"),
		"ErrTpl": ones("cook.portions.err"), "ErrOne": ones("cook.portions.err.one"), "OfflineTpl": ones("cook.portions.offline"), "OfflineOne": ones("cook.portions.offline.one"),
	}
	if n > 0 {
		data["Current"] = steps[k-1]
	}
	switch {
	case invalid && base == 1:
		data["PortionsErr"] = c.t("cook.portions.err.one")
	case invalid:
		data["PortionsErr"] = c.t("cook.portions.err", "n", strconv.Itoa(base))
	case portions != base && portions == 1:
		data["Scaled"] = c.t("cook.portions.scaled.one", "b", strconv.Itoa(base))
	case portions != base:
		data["Scaled"] = c.t("cook.portions.scaled", "b", strconv.Itoa(base), "p", strconv.Itoa(portions))
	}
	s.render(w, r, http.StatusOK, "recipe_cook.html", data)
}

// cookStepView is what the page shows for step k (1-based) of the steps.
func (c *itemCtx) cookStepView(steps []string, k int) cookStep {
	n := len(steps)
	v := cookStep{K: k, Text: steps[k-1], Last: k == n, Title: c.t("cook.step", "k", strconv.Itoa(k), "n", strconv.Itoa(n)),
		BackStep: k - 1, FwdStep: k + 1, BackLabel: c.t("cook.back"), ContLbl: c.t("cook.cont", "k", strconv.Itoa(k))}
	if k == 1 {
		v.BackLabel = c.t("cook.toings")
		v.ContLbl = c.t("cook.start")
	}
	if k < n {
		v.Next = steps[k]
		v.FwdLabel = c.t("cook.fwd", "k", strconv.Itoa(k+1))
	} else {
		v.FwdStep, v.FwdLabel = 0, c.t("cook.done")
	}
	for i := 1; i <= n; i++ {
		d := cookDot{I: i, Current: i == k, Done: i < k, Label: c.t("cook.goto", "k", strconv.Itoa(i))}
		if d.Done {
			d.Label = c.t("cook.gotodone", "k", strconv.Itoa(i))
		}
		v.Dots = append(v.Dots, d)
	}
	return v
}
