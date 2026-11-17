package server_test

import (
	"net/http"
	"testing"
)

// The test clock stands at Saturday 14/11/2026; the next week starts on Monday 16/11.
const weekStart = "2026-11-16"

func dinnerWeek(name, start string, every int) map[string]any {
	entry := func(weekday int, text string) map[string]any {
		return map[string]any{"weekday": weekday, "mealtime": "dinner", "text": text}
	}
	return map[string]any{
		"name": name, "mealtimes": "one", "mealtime": "dinner", "interval": every, "start_week": start,
		"entries": []any{entry(1, "Lentil soup"), entry(2, "Fish fingers"), entry(3, "Spaghetti bolognese")},
	}
}

func createTemplate(t *testing.T, c *apiClient, in map[string]any) (int, map[string]any) {
	t.Helper()
	status, body, _ := c.call(http.MethodPost, "/meal-templates", in)
	return status, body
}

// Acceptance: create, edit and delete templates (name, meal types, entries per weekday and meal type, interval 1-52 weeks, start week).
func TestTemplatesCanBeCreatedReadEditedAndDeletedOverTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, tpl := createTemplate(t, c, dinnerWeek("Week B", weekStart, 2))
	id, _ := tpl["id"].(string)
	if status != http.StatusCreated || id == "" || tpl["name"] != "Week B" || tpl["mealtimes"] != "one" || tpl["mealtime"] != "dinner" ||
		tpl["interval"] != float64(2) || tpl["start_week"] != weekStart || len(tpl["entries"].([]any)) != 3 {
		t.Fatalf("create = %d %v", status, tpl)
	}
	status, got, _ := c.call(http.MethodGet, "/meal-templates/"+id, nil)
	if status != http.StatusOK || got["name"] != "Week B" {
		t.Fatalf("get = %d %v", status, got)
	}
	if _, _, list := c.call(http.MethodGet, "/meal-templates", nil); len(list) != 1 {
		t.Fatalf("list = %v", list)
	}
	edit := dinnerWeek("Week B (school)", weekStart, 3)
	edit["mealtimes"], edit["mealtime"] = "all", nil
	delete(edit, "mealtime")
	status, got, _ = c.call(http.MethodPut, "/meal-templates/"+id, edit)
	if status != http.StatusOK || got["name"] != "Week B (school)" || got["interval"] != float64(3) || got["mealtimes"] != "all" {
		t.Fatalf("put = %d %v", status, got)
	}
	if status, _, _ := c.call(http.MethodDelete, "/meal-templates/"+id, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _, _ := c.call(http.MethodGet, "/meal-templates/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("get after delete = %d", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/meal-templates/"+id, nil); status != http.StatusNotFound {
		t.Fatalf("delete again = %d", status)
	}
}

// The contract itself refuses an interval outside 1 to 52 (minimum, maximum).
func TestInvalidTemplatesAreRefusedWith422(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	for name, edit := range map[string]func(map[string]any){
		"no name":          func(m map[string]any) { m["name"] = " " },
		"start not Monday": func(m map[string]any) { m["start_week"] = "2026-11-17" },
		"one without meal": func(m map[string]any) { delete(m, "mealtime") },
		"no entries":       func(m map[string]any) { m["entries"] = []any{} },
	} {
		in := dinnerWeek("Week B", weekStart, 2)
		edit(in)
		if status, body := createTemplate(t, c, in); status != http.StatusUnprocessableEntity && status != http.StatusBadRequest {
			t.Errorf("%s = %d %v", name, status, body)
		}
	}
	if _, _, list := c.call(http.MethodGet, "/meal-templates", nil); len(list) != 0 {
		t.Fatalf("templates = %v", list)
	}
}

// Acceptance: overlapping templates are refused at save with a message naming the clash (template, weekday, meal type, first affected week).
func TestOverlappingTemplatesAreRefusedWith409NamingTheClash(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, a := createTemplate(t, c, dinnerWeek("Week A", weekStart, 2))
	if status, body := createTemplate(t, c, dinnerWeek("Week B", "2026-11-23", 2)); status != http.StatusCreated {
		t.Fatalf("alternating weeks = %d %v", status, body)
	}
	status, body := createTemplate(t, c, dinnerWeek("Every week", weekStart, 1))
	if status != http.StatusConflict {
		t.Fatalf("clash = %d %v", status, body)
	}
	clashes := body["clashes"].([]any)
	first := clashes[0].(map[string]any)
	tpl := first["template"].(map[string]any)
	if len(clashes) != 2 || tpl["id"] != a["id"] || tpl["name"] != "Week A" || first["mealtime"] != "dinner" || first["first_week"] != weekStart ||
		len(first["weekdays"].([]any)) != 3 || first["weekdays"].([]any)[0] != float64(1) {
		t.Fatalf("clashes = %v", body)
	}
	if _, _, list := c.call(http.MethodGet, "/meal-templates", nil); len(list) != 2 {
		t.Fatalf("templates after the refusal = %d", len(list))
	}
	// Saving a template over itself is no clash.
	if status, body, _ := c.call(http.MethodPut, "/meal-templates/"+a["id"].(string), dinnerWeek("Week A", weekStart, 2)); status != http.StatusOK {
		t.Fatalf("put itself = %d %v", status, body)
	}
}

// Acceptance: the meal plan shows template entries; editing a week's entry overrides only that week; "Back to template" restores it.
func TestTheMealPlanFollowsTemplatesAndAWeeksChangeStaysInThatWeek(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, tpl := createTemplate(t, c, dinnerWeek("Week B", weekStart, 2))
	const mon, tue, nextWed = "2026-11-16", "2026-11-17", "2026-12-02"
	list := mealsBetween(t, c, mon, nextWed)
	if len(list) != 6 {
		t.Fatalf("meals = %v", list) // weeks 47 and 49, three days each
	}
	if m := list[0]; m["date"] != mon || m["text"] != "Lentil soup" || m["source"] != "template" || m["template_id"] != tpl["id"] || m["template_dish"] != "Lentil soup" {
		t.Fatalf("Monday = %v", m)
	}
	// It is in the calendar, too.
	if occs := c.occurrences(t, "from=2026-11-16T00:00:00%2B01:00&to=2026-11-17T00:00:00%2B01:00"); len(occs) != 1 || occs[0]["title"] != "Abendessen: Lentil soup" {
		t.Fatalf("Monday in the calendar = %v", occs)
	}
	status, m := putMeal(t, c, tue, "dinner", "Pizza at Grandma's")
	if status != http.StatusOK || m["source"] != "adjusted" || m["template_dish"] != "Fish fingers" || m["text"] != "Pizza at Grandma's" {
		t.Fatalf("adjust = %d %v", status, m)
	}
	if occs := c.occurrences(t, "from=2026-11-17T00:00:00%2B01:00&to=2026-11-18T00:00:00%2B01:00"); len(occs) != 1 || occs[0]["title"] != "Abendessen: Pizza at Grandma's" {
		t.Fatalf("Tuesday in the calendar = %v", occs)
	}
	if list := mealsBetween(t, c, "2026-12-01", "2026-12-01"); len(list) != 1 || list[0]["text"] != "Fish fingers" || list[0]["source"] != "template" {
		t.Fatalf("the next Tuesday = %v", list)
	}
	status, body, _ := c.call(http.MethodPost, "/meals/"+tue+"/dinner/restore", nil)
	if status != http.StatusOK || body["text"] != "Fish fingers" || body["source"] != "template" {
		t.Fatalf("restore = %d %v", status, body)
	}
	if status, _, _ := c.call(http.MethodPost, "/meals/"+tue+"/dinner/restore", nil); status != http.StatusNotFound {
		t.Fatalf("restore a meal that is not adjusted = %d", status)
	}
	// DELETE empties a template meal for the week: an adjustment, not a removal.
	if status, _, _ := c.call(http.MethodDelete, "/meals/"+tue+"/dinner", nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if list := mealsBetween(t, c, tue, tue); len(list) != 0 {
		t.Fatalf("emptied Tuesday = %v", list)
	}
	if list := mealsBetween(t, c, "2026-12-01", "2026-12-01"); len(list) != 1 {
		t.Fatalf("the next Tuesday = %v", list)
	}
}

func TestOnlyParentsChangeTemplatesAndEveryoneReadsThem(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	_, tpl := createTemplate(t, tb.parent, dinnerWeek("Week B", weekStart, 2))
	if status, _ := createTemplate(t, tb.child, dinnerWeek("Mine", weekStart, 1)); status != http.StatusForbidden {
		t.Errorf("child create = %d", status)
	}
	if status, _, _ := tb.child.call(http.MethodPut, "/meal-templates/"+tpl["id"].(string), dinnerWeek("Mine", weekStart, 2)); status != http.StatusForbidden {
		t.Errorf("child put = %d", status)
	}
	if status, _, _ := tb.child.call(http.MethodDelete, "/meal-templates/"+tpl["id"].(string), nil); status != http.StatusForbidden {
		t.Errorf("child delete = %d", status)
	}
	if status, _, _ := tb.child.call(http.MethodPost, "/meals/2026-11-17/dinner/restore", nil); status != http.StatusForbidden {
		t.Errorf("child restore = %d", status)
	}
	// The child sees the effective meal.
	if list := mealsBetween(t, tb.child, "2026-11-16", "2026-11-16"); len(list) != 1 || list[0]["text"] != "Lentil soup" {
		t.Fatalf("child sees %v", list)
	}
}
