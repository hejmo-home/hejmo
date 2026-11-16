package server_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/store"
)

var footerRe = regexp.MustCompile(`(?s)<footer class="fv-footer" data-footer>.*?</footer>`)

// plan stores today's (14/11/2026) meals through the same path as the meal plan page.
func (f *familyBox) plan(t *testing.T, entries map[meal.Mealtime]string) {
	t.Helper()
	var ms []store.Meal
	for m, text := range entries {
		ms = append(ms, store.Meal{Day: "2026-11-14", Mealtime: m, Text: text})
	}
	err := f.db.SetMeals(f.householdID, ms, store.MealOptions{CalendarName: "Meal plan", Location: berlinTZ,
		Title: func(m meal.Mealtime, text string) string { return meal.EventTitle(string(m), text) }})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *familyBox) footer(t *testing.T, lang string, day, h, m int) string {
	t.Helper()
	page := f.view(t, lang, f.at(day, h, m))
	return footerRe.FindString(page)
}

var dayPlan = map[meal.Mealtime]string{
	meal.Breakfast: "Porridge with apple", meal.Lunch: "Tomato soup", meal.Snack: "Banana bread", meal.Dinner: "Pasta bake",
}

const longDish = "Kartoffel-Lauch-Suppe mit geräucherten Forellenfilets, Meerrettichsahne und selbstgebackenem Dinkel-Sauerteigbrot, danach Zwetschgenkompott"

// 6.4: the four states of the preview, in colour and black and white, plus a long German dish.
func TestMealTileSnapshots(t *testing.T) {
	for _, tc := range []struct{ name, display string }{{"colour", "eink_color"}, {"bw", "eink_bw"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFamilyBox(t, tc.display)
			f.plan(t, dayPlan)
			var got strings.Builder
			// The colour and black-and-white markup of the footer is the same; the
			// difference is the theme on the page (data-theme, CSS only), so the
			// golden records the page's <html> tag too.
			htmlTag := regexp.MustCompile(`<html[^>]*>`).FindString(f.view(t, "en", f.at(14, 7, 15)))
			if want := tc.display == "eink_bw"; strings.Contains(htmlTag, `data-theme="bw"`) != want {
				t.Errorf("page theme: %s", htmlTag)
			}
			got.WriteString(htmlTag + "\n")
			add := func(title, footer string) { got.WriteString("<!-- " + title + " -->\n" + footer + "\n") }
			add("several meals (07:15)", f.footer(t, "en", 14, 7, 15))
			add("one meal (18:30)", f.footer(t, "en", 14, 18, 30))
			add("past cut-off (14:00)", f.footer(t, "en", 14, 14, 0))
			if err := f.db.SaveHouseholdSettings(f.householdID, store.HouseholdSettings{DateFormat: store.DateDMY, TimeFormat: store.Time24h, TemperatureUnit: store.Celsius, AutoUpdate: store.UpdateOn, MealCutoffs: meal.DefaultCutoffs().With(meal.Dinner, "20:00")}); err != nil {
				t.Fatal(err)
			}
			if p := f.view(t, "en", f.at(14, 21, 0)); strings.Contains(p, "data-footer") || strings.Contains(p, "data-tile=") {
				t.Errorf("nothing left, yet a tile or footer is shown")
			} else {
				add("nothing left (21:00, dinner cut-off 20:00)", "(no footer)")
			}
			// SetMeals merges: an empty text clears the snack of the first plan.
			f.plan(t, map[meal.Mealtime]string{meal.Lunch: longDish, meal.Dinner: "Maultaschen", meal.Snack: ""})
			add("German long text (11:00)", f.footer(t, "de", 14, 11, 0))
			compareGolden(t, "testdata/meal_tile_"+tc.name+".html", got.String())
		})
	}
}

func compareGolden(t *testing.T, golden, got string) {
	t.Helper()
	if os.Getenv("HEJMO_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("markup differs from %s (HEJMO_UPDATE_GOLDEN=1 rewrites it after a reviewed change):\n%s", golden, got)
	}
}

// 6.4: the markup of the meal tile follows the preview.
func TestMealTileMarkup(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.plan(t, dayPlan)
	several := f.footer(t, "en", 14, 7, 15)
	for _, want := range []string{`<div class="tile meal" data-tile="meal">`, `<div class="lab">Meals today</div>`, `<ul class="meals">`,
		`<li class="meal-ln" data-mealtime="breakfast"><span class="mt">`, `Breakfast</span><span class="dish">Porridge with apple</span>`} {
		if !strings.Contains(several, want) {
			t.Errorf("several: missing %q in\n%s", want, several)
		}
	}
	if strings.Contains(several, "alert") {
		t.Error("the meal tile is highlighted")
	}
	one := f.footer(t, "en", 14, 18, 30)
	for _, want := range []string{`<div class="tile meal" data-tile="meal" data-mealtime="dinner">`, `<div class="lab mt">`, `Dinner</div><div class="ln dish">Pasta bake</div>`} {
		if !strings.Contains(one, want) {
			t.Errorf("one: missing %q in\n%s", want, one)
		}
	}
	if strings.Contains(one, "<ul") || strings.Contains(one, "Meals today") {
		t.Error("one meal left, yet the list label is shown")
	}
}

// 6.4: a mealtime vanishes at its cut-off minute without interaction: the
// version the display's event stream announces changes, so the display swaps the view.
func TestMealTileCutoffChangesTheViewVersion(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.plan(t, dayPlan)
	version := func(h, m int) string {
		return regexp.MustCompile(`data-version="([0-9a-f]+)"`).FindStringSubmatch(f.view(t, "en", f.at(14, h, m)))[1]
	}
	if version(13, 29) == version(13, 30) {
		t.Error("the view is the same before and after the lunch cut-off")
	}
}

// 6.4: if the meal plan cannot be read the view renders without the tile, not with an error.
func TestMealTileIsLeftOutWhenTheMealsCannotBeRead(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.plan(t, dayPlan)
	if !strings.Contains(f.view(t, "en", f.at(14, 7, 15)), `data-tile="meal"`) {
		t.Fatal("no meal tile before the table is dropped")
	}
	if _, err := f.db.SQL().Exec(`DROP TABLE meal_plan`); err != nil {
		t.Fatal(err)
	}
	if p := f.view(t, "en", f.at(14, 7, 15)); strings.Contains(p, `data-tile="meal"`) || !strings.Contains(p, "data-family") {
		t.Errorf("the view should render without the meal tile:\n%s", p)
	}
}

// ADR 0015/0016: regression guard: the display page's language is set so the browser can hyphenate.
func TestDisplayPageSetsLanguageForHyphenation(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	if p := f.view(t, "de", f.at(14, 8, 0)); !strings.Contains(p, `<html lang="de"`) {
		t.Errorf("no lang on the display page")
	}
}
