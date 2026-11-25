package server

import (
	"html"
	"html/template"
	"slices"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/nutrition"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/recipeimport"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Nutrition values on the recipe page and over the API (ticket 288, ADR 0021,
// preview-288.html). Values are per portion, calculated from the ingredients
// with BLS 4.0 and USDA FoodData Central, only on the recipe page, hidden for
// children unless a parent switched them on for them, never on displays.

// mayWatchNutrition says whether the person sees nutrition values: parents
// always, a child only when a parent switched them on for that child.
func (c *itemCtx) mayWatchNutrition() bool { return c.p.mayWatchNutrition() }

// mayWatchNutrition is the same rule for the API, where there is no itemCtx.
func (p principal) mayWatchNutrition() bool {
	return p.person.Role == store.RoleParent || p.person.NutritionShown
}

// nutritionLines turns the ingredients into the lines the calculation takes.
// Free text, and an article that is not in the library any more, has no food.
func (c *itemCtx) nutritionLines(r recipe.Recipe) []nutrition.Line {
	lines := make([]nutrition.Line, 0, len(r.Ingredients))
	for _, in := range r.Ingredients {
		l := nutrition.Line{Name: in.Text, Amount: in.Amount, Unit: in.Unit}
		it, ok := c.article(in.ArticleID)
		if in.ArticleID == "" || !ok {
			l.Free = true
			lines = append(lines, l)
			continue
		}
		if in.Variety != "" {
			l.Name += " · " + in.Variety
		}
		l.ArticleID, l.Own = it.ID, it.own != nil
		foodID := ""
		if it.own != nil {
			foodID, l.PieceG = it.own.Food, it.own.PieceG
		} else if link, ok := nutrition.ForArticle(it.ID); ok {
			foodID, l.PieceG, l.GPerML, l.NoWeight = link.FoodID, link.PieceG, link.GPerML, link.NoWeight
		}
		if f, ok := nutrition.Lookup(foodID); ok && foodID != "" {
			l.Food = &f
		}
		lines = append(lines, l)
	}
	return lines
}

func (c *itemCtx) nutritionOf(r recipe.Recipe) nutrition.Result {
	return nutrition.Calculate(r.Portions, c.nutritionLines(r))
}

// The order of the seven values on the page and in the API.
var nutritionRows = []struct {
	Key nutrition.Key
	Sub bool
}{
	{nutrition.KCal, false},
	{nutrition.Fat, false},
	{nutrition.Saturates, true},
	{nutrition.Carbs, false},
	{nutrition.Sugars, true},
	{nutrition.Protein, false},
	{nutrition.Salt, false},
}

type nutRowView struct {
	Label, Value, KJ string
	Sub, NA, Big     bool
	Web              bool // a value of a web page: "not stated" when it has none
}

type nutItemView struct {
	Name, Reason string
	LinkHref     string
	LinkText     string
}

type nutView struct {
	Lead   string
	Flag   string // "Unvollständig" or "Keine Nährwerte"; empty when nothing is missing
	Done   string // the tick line, when everything counts, or the sentence without ingredients
	Skip   string // the lead of the list of lines not counted
	Items  []nutItemView
	Miss   string // the lead of the list of values the food data lacks
	Gaps   []template.HTML
	Rows   []nutRowView
	Source template.HTML
	Who    template.HTML
	Hint   string // why Hejmo calculates, after the page's values stopped to hold
	WebGap string // the values the page did not state
}

// sourceWords names a source with its licence, in words.
func (c *itemCtx) sourceWords(s nutrition.Source) string { return c.t("nutrition.source." + string(s)) }

func (c *itemCtx) nutReason(s nutrition.Skipped, parent bool) nutItemView {
	v := nutItemView{Name: s.Name}
	switch s.Reason {
	case nutrition.ReasonFree:
		v.Reason = c.t("nutrition.reason.free")
	case nutrition.ReasonFreeNoAmount:
		v.Reason = c.t("nutrition.reason.free.noamount")
	case nutrition.ReasonNoAmount:
		v.Reason = c.t("nutrition.reason.noamount")
	case nutrition.ReasonNoWeight:
		key := s.Unit
		if key == "" {
			key = unit.Piece
		}
		v.Reason = c.t("nutrition.reason.noweight", "unit", c.unitWord(key, false))
	case nutrition.ReasonNoFood:
		if !s.Own {
			v.Reason = c.t("nutrition.reason.nofood")
			break
		}
		v.Reason = c.t("nutrition.reason.nofood.own")
		if parent {
			v.Reason += "."
			v.LinkHref, v.LinkText = "/settings/lists/library/"+s.ArticleID+"#food", c.t("nutrition.reason.choose")
		}
	}
	v.Reason = ": " + v.Reason // after the bold name
	return v
}

// nutName is the part of a catalogue key that names a value.
func nutName(k nutrition.Key) string {
	return [...]string{"energy", "energy", "fat", "saturates", "carbs", "sugars", "protein", "salt"}[k]
}

// gapLines say which values the food data lacks and for which food: values
// missing for the same foods share a line.
func (c *itemCtx) gapLines(gaps []nutrition.Gap) []template.HTML {
	type group struct {
		foods []nutrition.Food
		keys  []nutrition.Key
	}
	var groups []group
	for _, g := range gaps {
		i := slices.IndexFunc(groups, func(x group) bool {
			return slices.EqualFunc(x.foods, g.Foods, func(a, b nutrition.Food) bool { return a.ID() == b.ID() })
		})
		if i < 0 {
			groups = append(groups, group{foods: g.Foods})
			i = len(groups) - 1
		}
		groups[i].keys = append(groups[i].keys, g.Key)
	}
	var out []template.HTML
	for _, g := range groups {
		var names, foods []string
		for _, k := range g.keys {
			names = append(names, "<b>"+html.EscapeString(c.t("nutrition.gap."+nutName(k)))+"</b>")
		}
		for _, f := range g.foods {
			foods = append(foods, html.EscapeString(f.Name(c.lang))+" ("+html.EscapeString(c.t("nutrition.food."+string(f.Source)))+")")
		}
		text := repl(html.EscapeString(c.s.i18n.T(c.lang, "nutrition.gap.item")), "{values}", c.s.joinWords(c.lang, names), "{foods}", c.s.joinWords(c.lang, foods))
		out = append(out, template.HTML(text)) // #nosec G203 -- names escaped, the text is ours
	}
	return out
}

// whoSees is the sentence that tells parents which children see the values,
// each child named with marker and name, never counted.
func (c *itemCtx) whoSees() template.HTML {
	var on, off []string
	for _, p := range c.st.people {
		switch {
		case p.Role != store.RoleChild:
		case p.NutritionShown:
			on = append(on, p.ID)
		default:
			off = append(off, p.ID)
		}
	}
	text := func(key string) string { return html.EscapeString(c.s.i18n.T(c.lang, key)) }
	out := text("nutrition.who.base")
	switch {
	case len(on) == 0 && len(off) == 0:
		return template.HTML(out) // #nosec G203 -- our text
	case len(off) == 0:
		out += " " + repl(text(oneOrMany("nutrition.who.allon", len(on))), "{on}", c.s.whoHTML(c.st, on))
	case len(on) == 0:
		out += " " + repl(text(oneOrMany("nutrition.who.alloff", len(off))), "{off}", c.s.whoHTML(c.st, off))
	default:
		out += " " + repl(text("nutrition.who.mixed"), "{on}", c.s.whoHTML(c.st, on), "{off}", c.s.whoHTML(c.st, off))
	}
	return template.HTML(out + ` <a href="/people">` + text("nutrition.who.link") + `</a>`) // #nosec G203 -- names escaped, the text is ours
}

func oneOrMany(key string, n int) string {
	if n == 1 {
		return key + ".one"
	}
	return key + ".many"
}

// nutritionView builds the card of the recipe page; nil when the person does
// not see nutrition values at all.
func (c *itemCtx) nutritionView(r recipe.Recipe) *nutView {
	if !c.mayWatchNutrition() {
		return nil
	}
	parent := c.p.person.Role == store.RoleParent
	res := c.nutritionOf(r)
	v := &nutView{}
	if r.Portions == 1 {
		v.Lead = c.t("nutrition.lead.one")
	} else {
		v.Lead = c.t("nutrition.lead.other", "n", strconv.Itoa(r.Portions))
	}
	if parent {
		v.Who = c.whoSees()
	}
	if len(r.Ingredients) == 0 {
		v.Done = c.t("nutrition.empty")
		return v
	}
	for _, s := range res.Skipped {
		v.Items = append(v.Items, c.nutReason(s, parent))
	}
	switch {
	case res.Counted == 0:
		v.Flag, v.Skip = c.t("nutrition.flag.none"), c.t("nutrition.none.lead")
		return v
	case len(res.Skipped) == 0 && len(res.Gaps) == 0:
		v.Done = c.t("nutrition.complete")
	default:
		v.Flag = c.t("nutrition.flag.incomplete")
	}
	if len(res.Skipped) > 0 {
		v.Skip = c.t("nutrition.skipped.lead")
	}
	if len(res.Gaps) > 0 {
		v.Miss, v.Gaps = c.t("nutrition.missing.lead"), c.gapLines(res.Gaps)
	}
	for _, row := range nutritionRows {
		rv := nutRowView{Label: c.t("nutrition.row." + nutName(row.Key)), Sub: row.Sub, Big: row.Key == nutrition.KCal}
		if x, ok := res.Values.Get(row.Key); ok {
			rv.Value = nutrition.Format(row.Key, x, c.lang)
			if row.Key == nutrition.KCal {
				if kj, ok := res.Values.Get(nutrition.KJ); ok {
					rv.KJ = nutrition.Format(nutrition.KJ, kj, c.lang)
				}
			}
		} else {
			rv.NA = true
		}
		v.Rows = append(v.Rows, rv)
	}
	var sources []string
	for _, s := range res.Sources {
		sources = append(sources, c.sourceWords(s))
	}
	src := html.EscapeString(c.t("nutrition.source", "sources", strings.Join(sources, "; ")))
	if parent {
		src += ` <a href="/settings/licences">` + html.EscapeString(c.t("nutrition.source.link")) + `</a>`
	}
	v.Source = template.HTML(src) // #nosec G203 -- escaped, the link is ours
	return v
}

// nutritionViewOf is the card of the recipe page: the values an imported page stated while they hold (ticket 289),
// else the calculation of ticket 288, with a word on why it replaced them when they stopped to hold.
func (c *itemCtx) nutritionViewOf(rec store.Recipe) *nutView {
	pn, ok, err := c.s.db.RecipePageNutrition(c.p.household.ID, rec.ID)
	if err != nil || !ok || !c.mayWatchNutrition() {
		return c.nutritionView(rec.Recipe)
	}
	parent := c.p.person.Role == store.RoleParent
	imp, _, _ := c.s.db.RecipeImport(c.p.household.ID, rec.ID)
	if !pn.Active() {
		v := c.nutritionView(rec.Recipe)
		if v == nil {
			return nil
		}
		who := pn.DroppedByName
		if who == "" {
			who = c.t("recipes.import.someone")
		}
		key := "recipes.import.nut.dropped.ingredients"
		if pn.DroppedWhat == "portions" {
			key = "recipes.import.nut.dropped.portions"
		}
		v.Hint = c.t(key, "host", imp.Host, "name", who, "date", c.p.day(pn.DroppedAt), "from", strconv.Itoa(pn.Portions), "to", strconv.Itoa(pn.DroppedTo))
		return v
	}
	rows, stated := c.pageNutRows(&recipeimport.Nutrition{KCal: pn.KCal, KJ: pn.KJ, Fat: pn.Fat, Saturates: pn.Saturates, Carbs: pn.Carbs, Sugars: pn.Sugars, Protein: pn.Protein, Salt: pn.Salt})
	v := &nutView{Lead: c.t("recipes.import.nut.lead"), Rows: rows}
	if stated < len(rows) {
		var missing []string
		for _, r := range rows {
			if r.NA {
				missing = append(missing, r.Label)
			}
		}
		key := "recipes.import.nut.gap.one"
		if len(missing) > 1 {
			key = "recipes.import.nut.gap.many"
		}
		v.Flag, v.WebGap = c.t("nutrition.flag.incomplete"), c.t(key, "values", c.names(missing))
	}
	link := `<a class="ext" href="` + html.EscapeString(pn.URL) + `" rel="noopener noreferrer" target="_blank">` + html.EscapeString(imp.Host) + `</a>`
	text := html.EscapeString(c.t("recipes.import.nut.source", "link", "\x00", "date", c.p.day(pn.At)))
	v.Source = template.HTML(strings.Replace(text, "\x00", link, 1)) // #nosec G203 -- escaped, the link is built with escaped text
	if parent {
		v.Who = c.whoSees()
	}
	return v
}
