package meal_test

import (
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
)

func TestMealtimesRunThroughTheDay(t *testing.T) {
	got := meal.Mealtimes()
	want := []meal.Mealtime{meal.Breakfast, meal.Lunch, meal.Snack, meal.Dinner}
	if len(got) != len(want) {
		t.Fatalf("mealtimes = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mealtimes = %v, want %v", got, want)
		}
	}
	for _, m := range want {
		if !m.Valid() {
			t.Errorf("%s not valid", m)
		}
	}
	if meal.Mealtime("brunch").Valid() || meal.Mealtime("").Valid() {
		t.Error("unknown mealtime accepted")
	}
}

func TestCutoffDefaultsFollowADR0016(t *testing.T) {
	c := meal.DefaultCutoffs()
	if c.Breakfast != "10:00" || c.Lunch != "13:30" || c.Snack != "16:30" || c.Dinner != "" {
		t.Fatalf("defaults = %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.For(meal.Lunch) != "13:30" || c.For(meal.Dinner) != "" {
		t.Fatal("For does not pick the mealtime")
	}
}

func TestCutoffValidation(t *testing.T) {
	for time, ok := range map[string]bool{
		"": true, "00:00": true, "10:00": true, "23:59": true,
		"24:00": false, "10:60": false, "9:00": false, "10:0": false, "1000": false, "ten": false, "10:00:00": false, " 10:00": false,
	} {
		c := meal.DefaultCutoffs()
		c.Snack = time
		err := c.Validate()
		if ok && err != nil {
			t.Errorf("%q refused: %v", time, err)
		}
		if !ok {
			var bad *meal.InvalidError
			if err == nil {
				t.Errorf("%q accepted", time)
			} else if !asInvalid(err, &bad) || bad.Field != "snack" {
				t.Errorf("%q: error %v does not name the snack field", time, err)
			}
		}
	}
}

func asInvalid(err error, target **meal.InvalidError) bool {
	e, ok := err.(*meal.InvalidError)
	if ok {
		*target = e
	}
	return ok
}

func TestSetCutoffChangesOnlyOneMealtime(t *testing.T) {
	c := meal.DefaultCutoffs().With(meal.Dinner, "21:00")
	if c.Dinner != "21:00" || c.Lunch != "13:30" {
		t.Fatalf("with = %+v", c)
	}
}

func TestTextIsTrimmedAndLimited(t *testing.T) {
	if got, err := meal.CleanText("  Pasta bake \n"); err != nil || got != "Pasta bake" {
		t.Fatalf("clean = %q, %v", got, err)
	}
	if _, err := meal.CleanText(strings.Repeat("ä", meal.MaxText+1)); err == nil {
		t.Error("over-long text accepted")
	}
	if _, err := meal.CleanText(strings.Repeat("ä", meal.MaxText)); err != nil {
		t.Errorf("limit refused: %v", err)
	}
	if _, err := meal.CleanText("a\x00b"); err == nil {
		t.Error("control character accepted")
	}
}

func TestEventTitleNamesTheMealtime(t *testing.T) {
	if got := meal.EventTitle("Dinner", "Pasta bake"); got != "Dinner: Pasta bake" {
		t.Fatalf("title = %q", got)
	}
}
