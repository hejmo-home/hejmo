package meal_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/meal"
)

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func dinners(start string, every int, entries map[meal.Slot]string) meal.Template {
	return meal.Template{Name: "T", Mealtime: meal.Dinner, Interval: every, Start: start, Entries: entries}
}

var monToFri = map[meal.Slot]string{
	meal.At(1, meal.Dinner): "Pasta", meal.At(2, meal.Dinner): "Soup", meal.At(3, meal.Dinner): "Pizza", meal.At(4, meal.Dinner): "Curry", meal.At(5, meal.Dinner): "Fish",
}

func TestATemplateAppliesEveryNthWeekFromItsStartWeek(t *testing.T) {
	tpl := dinners("2026-11-16", 2, monToFri)
	for monday, want := range map[string]bool{
		"2026-11-09": false, // before the start week
		"2026-11-16": true,
		"2026-11-23": false,
		"2026-11-30": true,
		"2026-12-28": true, // week 53 of 2026
		"2027-01-04": false,
		"2027-01-11": true, // week 2 of 2027: the interval counts weeks, not even or odd numbers
	} {
		if got := tpl.AppliesInWeek(day(monday)); got != want {
			t.Errorf("week of %s: applies = %v, want %v", monday, got, want)
		}
	}
}

func TestADishComesFromTheEntryOfThatWeekdayAndMealtime(t *testing.T) {
	tpl := dinners("2026-11-16", 1, monToFri)
	if d, ok := tpl.Dish(day("2026-11-17"), meal.Dinner); !ok || d != "Soup" {
		t.Fatalf("Tuesday dinner = %q %v", d, ok)
	}
	if _, ok := tpl.Dish(day("2026-11-21"), meal.Dinner); ok { // Saturday: no entry
		t.Fatal("Saturday has no entry")
	}
	if _, ok := tpl.Dish(day("2026-11-17"), meal.Lunch); ok { // lunch is not filled by this template
		t.Fatal("lunch is not filled")
	}
	if _, ok := tpl.Dish(day("2026-11-10"), meal.Dinner); ok { // before the start week
		t.Fatal("before start")
	}
}

func TestEntriesOfMealtimesTheTemplateDoesNotFillAreIgnored(t *testing.T) {
	tpl := dinners("2026-11-16", 1, map[meal.Slot]string{meal.At(1, meal.Dinner): "Pasta", meal.At(1, meal.Lunch): "Ignored", meal.At(2, meal.Dinner): "   "})
	want := map[meal.Slot]string{meal.At(1, meal.Dinner): "Pasta"}
	if got := tpl.Filled(); !reflect.DeepEqual(got, want) {
		t.Fatalf("filled = %v", got)
	}
	all := meal.Template{Name: "W", AllMealtimes: true, Interval: 1, Start: "2026-11-16", Entries: map[meal.Slot]string{meal.At(6, meal.Lunch): "Roast"}}
	if !all.Fills(meal.Snack) || len(all.Filled()) != 1 {
		t.Fatalf("all = %v", all.Filled())
	}
}

func TestValidateNamesTheFieldThatIsWrong(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*meal.Template)
		field string
	}{
		{"no name", func(t *meal.Template) { t.Name = "  " }, "name"},
		{"interval 0", func(t *meal.Template) { t.Interval = 0 }, "every"},
		{"interval 53", func(t *meal.Template) { t.Interval = 53 }, "every"},
		{"empty", func(t *meal.Template) { t.Entries = map[meal.Slot]string{meal.At(1, meal.Lunch): "ignored"} }, "entries"},
		{"start not a monday", func(t *meal.Template) { t.Start = "2026-11-17" }, "start"},
		{"unknown mealtime", func(t *meal.Template) { t.Mealtime = "brunch" }, "mealtime"},
		{"entry too long", func(t *meal.Template) {
			t.Entries = map[meal.Slot]string{meal.At(1, meal.Dinner): string(make([]rune, 201))}
		}, "entries"},
	}
	for _, c := range cases {
		tpl := dinners("2026-11-16", 2, monToFri)
		tpl.Name = "Week B"
		c.edit(&tpl)
		var bad *meal.InvalidError
		if err := tpl.Validate(); !errors.As(err, &bad) || bad.Field != c.field {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	ok := dinners("2026-11-16", 52, monToFri)
	ok.Name = "Yearly"
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWeeksListsTheWeeksATemplateFillsAcrossTheTurnOfTheYear(t *testing.T) {
	tpl := dinners("2026-11-16", 2, monToFri)
	var got []string
	for _, w := range tpl.Weeks(day("2026-11-16"), 5) {
		got = append(got, w.Format("2006-01-02"))
	}
	want := []string{"2026-11-16", "2026-11-30", "2026-12-14", "2026-12-28", "2027-01-11"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("weeks = %v", got)
	}
	// Counting from a later week skips the weeks before it.
	if w := tpl.Weeks(day("2026-11-23"), 1); len(w) != 1 || w[0].Format("2006-01-02") != "2026-11-30" {
		t.Fatalf("from 23/11 = %v", w)
	}
}

func TestTwoTemplatesOverlapOnlyWhereBothFillTheSameMealOnTheSameDayOfTheSameWeek(t *testing.T) {
	today := day("2026-11-16")
	a := dinners("2026-11-16", 2, monToFri)
	// Week B starts one week later: the weeks never meet.
	b := dinners("2026-11-23", 2, monToFri)
	if c, ok := meal.FirstClash(a, b, today); ok {
		t.Fatalf("alternating weeks clash: %+v", c)
	}
	// Same start: they meet in the start week.
	b.Start = "2026-11-16"
	c, ok := meal.FirstClash(a, b, today)
	if !ok || c.Mealtime != meal.Dinner || c.Week.Format("2006-01-02") != "2026-11-16" || !reflect.DeepEqual(c.Weekdays, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("clash = %+v %v", c, ok)
	}
	// Different days of the week do not clash even in the same week.
	weekend := dinners("2026-11-16", 1, map[meal.Slot]string{meal.At(6, meal.Dinner): "Burgers"})
	if _, ok := meal.FirstClash(a, weekend, today); ok {
		t.Fatal("Saturday vs Monday to Friday")
	}
	// A different mealtime does not clash.
	lunch := meal.Template{Name: "L", Mealtime: meal.Lunch, Interval: 1, Start: "2026-11-16", Entries: map[meal.Slot]string{meal.At(1, meal.Lunch): "Soup"}}
	if _, ok := meal.FirstClash(a, lunch, today); ok {
		t.Fatal("lunch vs dinner")
	}
	// An empty entry does not count: only filled entries clash.
	sparse := dinners("2026-11-16", 1, map[meal.Slot]string{meal.At(1, meal.Dinner): "", meal.At(2, meal.Dinner): "Soup"})
	c, ok = meal.FirstClash(a, sparse, today)
	if !ok || !reflect.DeepEqual(c.Weekdays, []int{2}) {
		t.Fatalf("sparse = %+v", c)
	}
}

func TestOverlapsFoundAcrossIntervalsThatMeetOnlyLater(t *testing.T) {
	today := day("2026-11-16")
	every2 := dinners("2026-11-16", 2, monToFri) // weeks 0, 2, 4, 6 ...
	every3 := dinners("2026-11-23", 3, monToFri) // weeks 1, 4, 7 ...: first meet in week 4
	c, ok := meal.FirstClash(every2, every3, today)
	if !ok || c.Week.Format("2006-01-02") != "2026-12-14" {
		t.Fatalf("clash = %+v %v", c, ok)
	}
	// Two and four weeks, one week apart, never meet.
	if _, ok := meal.FirstClash(every2, dinners("2026-11-23", 4, monToFri), today); ok {
		t.Fatal("2 vs 4 weeks, offset 1")
	}
}

func TestClashesBeforeTodayDoNotCount(t *testing.T) {
	mondays := dinners("2026-11-02", 1, map[meal.Slot]string{meal.At(1, meal.Dinner): "Pasta"})
	every4 := dinners("2026-11-02", 4, map[meal.Slot]string{meal.At(1, meal.Dinner): "Soup"}) // Mondays 02/11, 30/11, 28/12
	// On Tuesday 01/12 the Monday of 30/11 is over; the next one both fill is 28/12.
	c, ok := meal.FirstClash(mondays, every4, day("2026-12-01"))
	if !ok || c.Week.Format("2006-01-02") != "2026-12-28" {
		t.Fatalf("clash = %+v %v", c, ok)
	}
	// On the Monday itself the day still counts.
	if c, ok := meal.FirstClash(mondays, every4, day("2026-11-30")); !ok || c.Week.Format("2006-01-02") != "2026-11-30" {
		t.Fatalf("on the day = %+v %v", c, ok)
	}
}

// Ticket 282: a template slot can hold a recipe.

func TestATemplateSlotCanHoldARecipeInsteadOfText(t *testing.T) {
	tpl := dinners("2026-11-16", 1, map[meal.Slot]string{})
	tpl.Recipes = map[meal.Slot]string{meal.At(2, meal.Dinner): "r-curry"}
	if err := tpl.Validate(); err != nil {
		t.Fatalf("a template of one recipe is not empty: %v", err)
	}
	if got := tpl.RecipeAt(day("2026-11-17"), meal.Dinner); got != "r-curry" {
		t.Errorf("recipe on Tuesday = %q", got)
	}
	if got := tpl.RecipeAt(day("2026-11-18"), meal.Dinner); got != "" {
		t.Errorf("recipe on Wednesday = %q, want none", got)
	}
	if got := tpl.RecipeAt(day("2026-11-24"), meal.Dinner); got != "r-curry" {
		t.Errorf("a recipe slot follows the interval, got %q", got)
	}
	tpl.Mealtime = meal.Lunch
	if got := tpl.RecipeAt(day("2026-11-17"), meal.Dinner); got != "" {
		t.Errorf("a slot of a mealtime the template does not fill holds no recipe, got %q", got)
	}
}
