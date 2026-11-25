package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The shopping list from meals over the API (ticket 283): look first (preview),
// then add (with the versions of the recipes), and take a run back. Parents only.

type shopSourceJSON struct {
	Date       string   `json:"date"`
	Mealtime   string   `json:"mealtime"`
	RecipeID   string   `json:"recipe_id"`
	RecipeName string   `json:"recipe_name"`
	Portions   int      `json:"portions"`
	Amount     *float64 `json:"amount,omitempty"`
	Unit       string   `json:"unit,omitempty"`
}

type shopMealJSON struct {
	Key      string `json:"key"`
	Date     string `json:"date"`
	Mealtime string `json:"mealtime"`
	RecipeID string `json:"recipe_id"`
	Name     string `json:"recipe_name"`
	Portions int    `json:"portions"`
	OnList   int    `json:"on_list"`
	ToAdd    int    `json:"to_add"`
	// Status is open (ingredients to put on), done (all portions are on a list) or empty (the recipe has no ingredients).
	Status   string `json:"status"`
	Selected bool   `json:"selected"`
}

type shopRowJSON struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	ArticleID *string  `json:"article_id"`
	Variety   *string  `json:"variety"`
	Amount    *float64 `json:"amount,omitempty"`
	Unit      string   `json:"unit,omitempty"`
	// Group is new, on_list or pantry; Outcome says what adding does: new, added (to an entry of the same kind), filled (an entry without quantity takes it) or separate (another kind of unit gets its own entry).
	Group   string `json:"group"`
	Outcome string `json:"outcome"`
	// Tick says whether the page would tick it: everything but the pantry articles.
	Tick    bool             `json:"tick"`
	Kitchen string           `json:"kitchen,omitempty"`
	Sources []shopSourceJSON `json:"sources"`
}

type shopPreviewJSON struct {
	Meal     *string           `json:"meal"`
	Week     *string           `json:"week"`
	ListID   *string           `json:"list_id"`
	ListName *string           `json:"list_name"`
	Meals    []shopMealJSON    `json:"meals"`
	Versions map[string]string `json:"versions"`
	Rows     []shopRowJSON     `json:"rows"`
	// Same are the entries that stay as they are: no quantity to add and already on the list.
	Same   []string `json:"same"`
	Counts struct {
		New    int `json:"new"`
		OnList int `json:"on_list"`
		Same   int `json:"same"`
		Pantry int `json:"pantry"`
		Ticked int `json:"ticked"`
	} `json:"counts"`
}

func (c *itemCtx) shopJSON(pg *shopPage) shopPreviewJSON {
	out := shopPreviewJSON{Meals: []shopMealJSON{}, Versions: map[string]string{}, Rows: []shopRowJSON{}, Same: []string{}}
	if pg.Sel.Week {
		w := pg.Sel.Monday.Format("2006-01-02")
		out.Week = &w
	} else {
		m := pg.Sel.Meal
		out.Meal = &m
	}
	if pg.ListID != "" {
		out.ListID, out.ListName = &pg.ListID, &pg.ListName
	}
	for _, m := range pg.MealCands {
		out.Meals = append(out.Meals, shopMealJSON{Key: m.Key, Date: m.Day, Mealtime: string(m.Mealtime), RecipeID: m.Rec.ID, Name: m.Rec.Name, Portions: m.Planned, OnList: m.OnList, ToAdd: m.Add, Status: m.Status(), Selected: m.Checked})
		out.Versions[m.Rec.ID] = m.Version
	}
	for _, st := range pg.steps {
		if st.Plan.Action == list.Exists {
			out.Same = append(out.Same, entryLabel(st.Row.Text, st.Row.Variety))
			continue
		}
		r := st.Row
		row := shopRowJSON{Key: r.Key, Name: r.Text, Group: "on_list", Outcome: string(st.Plan.Action), Sources: []shopSourceJSON{}, Tick: !r.Pantry}
		switch {
		case r.Pantry:
			row.Group = "pantry"
		case st.Plan.Action == list.AddedNew:
			row.Group = "new"
		}
		if st.Plan.Action == list.AddedNew {
			row.Outcome = "new"
			if st.Plan.After != "" {
				row.Outcome = "separate"
			}
		}
		if r.ArticleID != "" {
			row.ArticleID = &r.ArticleID
		}
		if r.Variety != "" {
			row.Variety = &r.Variety
		}
		if q := c.shopShown(r, r.Qty); !q.None() {
			a := formatJSONAmount(q)
			row.Amount, row.Unit = &a, q.Unit
		}
		row.Kitchen = c.kitchenText(r, len(pg.Selected) > 1)
		for _, p := range r.Parts {
			for _, m := range pg.Selected {
				if m.Key == p.Meal {
					s := shopSourceJSON{Date: m.Day, Mealtime: string(m.Mealtime), RecipeID: m.Rec.ID, RecipeName: m.Rec.Name, Portions: m.Add}
					if q := c.shopShown(r, p.Qty); !q.None() {
						a := formatJSONAmount(q)
						s.Amount, s.Unit = &a, q.Unit
					}
					row.Sources = append(row.Sources, s)
				}
			}
		}
		out.Rows = append(out.Rows, row)
		switch row.Group {
		case "new":
			out.Counts.New++
		case "on_list":
			out.Counts.OnList++
		default:
			out.Counts.Pantry++
		}
		if row.Tick {
			out.Counts.Ticked++
		}
	}
	out.Counts.Same = len(out.Same)
	return out
}

func apiShopSelection(s *server, p principal, meal, week string) (shopSelection, bool) {
	switch {
	case meal != "" && week == "":
		return parseShopSelection("meal:"+meal, s.today(p))
	case week != "" && meal == "":
		return parseShopSelection("week:"+week, s.today(p))
	}
	return shopSelection{}, false
}

func (s *server) apiShopPreview(w http.ResponseWriter, r *http.Request, p principal) {
	q := r.URL.Query()
	sel, ok := apiShopSelection(s, p, q.Get("meal"), q.Get("week"))
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "give meal (<day>.<mealtime>) or week (a day or an ISO week), not both")
		return
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "shop context", err)
		return
	}
	pg, err := c.buildShop(shopIn{Sel: sel, ListID: q.Get("list_id")})
	if err != nil {
		s.fail(w, "shop preview", err)
		return
	}
	if !sel.Week && len(pg.MealCands) == 0 {
		writeError(w, http.StatusNotFound, "that meal is not planned as a recipe")
		return
	}
	writeJSON(w, http.StatusOK, c.shopJSON(pg))
}

type shopAddedJSON struct {
	EntryID string   `json:"entry_id"`
	Name    string   `json:"name"`
	Action  string   `json:"action"`
	Amount  *float64 `json:"amount,omitempty"`
	Unit    string   `json:"unit,omitempty"`
}

type shopRunJSON struct {
	RunID    string          `json:"run_id"`
	ListID   string          `json:"list_id"`
	ListName string          `json:"list_name"`
	Added    []shopAddedJSON `json:"added"`
	LeftOut  []string        `json:"left_out"`
}

func (s *server) apiShopAdd(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Meal     string            `json:"meal"`
		Week     string            `json:"week"`
		ListID   string            `json:"list_id"`
		Meals    *[]string         `json:"meals"`
		Picks    *[]string         `json:"picks"`
		Versions map[string]string `json:"versions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	sel, ok := apiShopSelection(s, p, in.Meal, in.Week)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "give meal (<day>.<mealtime>) or week (a day or an ISO week), not both")
		return
	}
	if in.ListID == "" {
		writeError(w, http.StatusUnprocessableEntity, "list_id is required")
		return
	}
	c, err := s.itemContext(p, s.langOf(r))
	if err != nil {
		s.fail(w, "shop context", err)
		return
	}
	si := shopIn{Sel: sel, ListID: in.ListID, Versions: in.Versions, Picks: map[string]bool{}}
	if in.Picks != nil {
		si.Posted = true
		for _, k := range *in.Picks {
			si.Picks[k] = true
		}
	}
	if in.Meals != nil && sel.Week {
		si.Meals = map[string]bool{}
		for _, k := range *in.Meals {
			si.Meals[k] = true
		}
	}
	pg, err := c.buildShop(si)
	if err != nil {
		s.fail(w, "shop", err)
		return
	}
	switch {
	case len(pg.Changed) > 0:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "a recipe changed since the versions were read: nothing was added", "changed": changedIDs(pg, in.Versions)})
		return
	case !sel.Week && len(pg.MealCands) == 0, len(c.shopMissing(pg, si)) > 0:
		writeError(w, http.StatusConflict, "a meal is not planned as a recipe any more: nothing was added")
		return
	case pg.ListID == "":
		writeError(w, http.StatusUnprocessableEntity, "list_id is not a list with suggestions of this household")
		return
	case pg.Ticked == 0:
		writeError(w, http.StatusUnprocessableEntity, "nothing is ticked: nothing to add")
		return
	}
	run, err := c.addShop(pg)
	switch {
	case errors.Is(err, store.ErrShopStale):
		writeError(w, http.StatusConflict, "a meal got on a list since it was read: nothing was added")
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusUnprocessableEntity, "list_id is not a list of this household")
		return
	case err != nil:
		s.fail(w, "shop add", err)
		return
	}
	out := shopRunJSON{RunID: run.ID, ListID: run.ListID, ListName: run.ListName, Added: []shopAddedJSON{}, LeftOut: []string{}}
	out.LeftOut = append(out.LeftOut, run.LeftOut...)
	for _, st := range run.Steps {
		a := shopAddedJSON{EntryID: st.EntryID, Name: entryLabel(st.Text, st.Variety), Action: string(st.Action)}
		if !st.Added.None() {
			v := formatJSONAmount(st.Added)
			a.Amount, a.Unit = &v, st.Added.Unit
		}
		out.Added = append(out.Added, a)
	}
	writeJSON(w, http.StatusCreated, out)
}

// changedIDs are the recipes whose version differs from the one the client read.
func changedIDs(pg *shopPage, versions map[string]string) []string {
	out := []string{}
	for _, m := range pg.MealCands {
		if v, ok := versions[m.Rec.ID]; ok && v != m.Version {
			out = append(out, m.Rec.ID)
		}
	}
	return out
}

type shopUndoJSON struct {
	Removed  []string `json:"removed"`
	Reverted []struct {
		Name   string   `json:"name"`
		Amount *float64 `json:"amount,omitempty"`
		Unit   string   `json:"unit,omitempty"`
	} `json:"reverted"`
	Ticked []string `json:"ticked"`
}

func (s *server) apiShopUndo(w http.ResponseWriter, r *http.Request, p principal) {
	res, err := s.db.UndoMealShop(p.household.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrShopTaken):
		if _, e := s.db.MealShopRun(p.household.ID, r.PathValue("id")); errors.Is(e, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no such run")
		} else {
			writeError(w, http.StatusConflict, "this run was taken back already")
		}
		return
	case err != nil:
		s.fail(w, "shop undo", err)
		return
	}
	out := shopUndoJSON{Removed: append([]string{}, res.Removed...), Ticked: append([]string{}, res.Ticked...)}
	out.Reverted = []struct {
		Name   string   `json:"name"`
		Amount *float64 `json:"amount,omitempty"`
		Unit   string   `json:"unit,omitempty"`
	}{}
	for _, rv := range res.Reverted {
		e := struct {
			Name   string   `json:"name"`
			Amount *float64 `json:"amount,omitempty"`
			Unit   string   `json:"unit,omitempty"`
		}{Name: rv.Text}
		if !rv.Qty.None() {
			a := formatJSONAmount(rv.Qty)
			e.Amount, e.Unit = &a, rv.Qty.Unit
		}
		out.Reverted = append(out.Reverted, e)
	}
	writeJSON(w, http.StatusOK, out)
}
