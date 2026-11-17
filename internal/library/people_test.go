package library_test

import (
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/food"
	"github.com/hejmo-home/hejmo/internal/library"
)

var family = []library.Person{
	{ID: "mama", Diet: food.Pescatarian},
	{ID: "papa", Diet: food.Omnivore, Allergens: []string{"milk"}},
	{ID: "mia", Diet: food.Vegetarian, Allergens: []string{"nuts", "peanuts"}},
	{ID: "konstantin", Diet: food.Omnivore, Allergens: []string{"milk"}},
	{ID: "charlotte", Diet: food.Vegan, Allergens: []string{"gluten", "soy", "milk"}},
}

func article(kind library.Kind, allergens ...string) library.Article {
	return library.Article{ID: "x", Kind: kind, Allergens: allergens}
}

func TestOneWarningPerGroupOfPeopleWithTheSameAllergensNeverACount(t *testing.T) {
	a := article(library.Veg, "milk", "nuts", "soy", "gluten")
	got := library.Warnings(a, family)
	want := []library.Warning{
		{People: []string{"papa", "konstantin"}, Allergens: []string{"milk"}},
		{People: []string{"mia"}, Allergens: []string{"nuts"}},
		{People: []string{"charlotte"}, Allergens: []string{"gluten", "milk", "soy"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("warnings = %+v\nwant      %+v", got, want)
	}
}

func TestAnArticleWithoutAMatchingAllergenWarnsNobody(t *testing.T) {
	if got := library.Warnings(article(library.Vegan, "sesame"), family); len(got) != 0 {
		t.Errorf("warnings = %+v", got)
	}
	if got := library.Warnings(article(library.NonFood), family); len(got) != 0 {
		t.Errorf("warnings = %+v", got)
	}
}

func TestAnArticleThatIsNotFoodNeverWarnsEvenWithAllergensSet(t *testing.T) {
	if got := library.Warnings(article(library.NonFood, "milk"), family); len(got) != 0 {
		t.Errorf("warnings = %+v", got)
	}
}

func TestWhoTheArticleSuits(t *testing.T) {
	fit, notFit := library.FitsPeople(article(library.Fish, "fish"), family)
	ids := func(ps []library.Person) []string {
		out := []string{}
		for _, p := range ps {
			out = append(out, p.ID)
		}
		return out
	}
	if !reflect.DeepEqual(ids(fit), []string{"mama", "papa", "konstantin"}) || !reflect.DeepEqual(ids(notFit), []string{"mia", "charlotte"}) {
		t.Errorf("fit %v, not %v", ids(fit), ids(notFit))
	}
}

func hide(everyone bool, ids ...string) library.Hiding {
	return library.Hiding{Enabled: true, Everyone: everyone, People: ids}
}

func TestHidingIsOffByDefaultAndOnlyAffectsThePeopleChosen(t *testing.T) {
	a := article(library.Veg, "milk", "nuts")
	if got := (library.Hiding{}).HiddenFor(a, family); len(got) != 0 {
		t.Errorf("hiding off hides for %v", got)
	}
	if got := hide(true).HiddenFor(a, family); !reflect.DeepEqual(got, []string{"papa", "mia", "konstantin", "charlotte"}) {
		t.Errorf("whole family = %v", got)
	}
	if got := hide(false, "mia").HiddenFor(a, family); !reflect.DeepEqual(got, []string{"mia"}) {
		t.Errorf("only Mia = %v", got)
	}
	if got := hide(false, "mama").HiddenFor(a, family); len(got) != 0 {
		t.Errorf("Mama has no allergy: %v", got)
	}
	if got := hide(true).HiddenFor(article(library.NonFood, "milk"), family); len(got) != 0 {
		t.Errorf("not food: %v", got)
	}
}

func TestSuggestionsFollowDietsHandAndAllergy(t *testing.T) {
	salmon := article(library.Fish, "fish")
	steak := article(library.Meat)
	none := library.Hiding{}

	if s := library.Suggest(steak, family, false, none); !s.Suggested {
		t.Errorf("a steak suits the omnivores: %+v", s)
	}
	veganOnly := []library.Person{{ID: "v", Diet: food.Vegan}}
	if s := library.Suggest(steak, veganOnly, false, none); s.Suggested || s.Reason != library.ReasonDiet {
		t.Errorf("nobody eats it: %+v", s)
	}
	if s := library.Suggest(salmon, family, true, none); s.Suggested || s.Reason != library.ReasonHand {
		t.Errorf("hidden by hand: %+v", s)
	}
	allergic := []library.Person{{ID: "f", Diet: food.Omnivore, Allergens: []string{"fish"}}}
	if s := library.Suggest(salmon, allergic, false, none); !s.Suggested {
		t.Errorf("hiding off: a warning, not a hiding: %+v", s)
	}
	if s := library.Suggest(salmon, allergic, false, hide(true)); s.Suggested || s.Reason != library.ReasonAllergy || !reflect.DeepEqual(s.By, []string{"f"}) {
		t.Errorf("hiding on: %+v", s)
	}
	if s := library.Suggest(salmon, allergic, true, hide(true)); s.Reason != library.ReasonHand {
		t.Errorf("by hand takes the reason first: %+v", s)
	}
	if s := library.Suggest(steak, nil, false, none); !s.Suggested {
		t.Errorf("a household without people still gets suggestions: %+v", s)
	}
}

func TestPersonWarningsNameEveryAffectedPersonOnTheirOwnLine(t *testing.T) {
	a := library.Article{ID: "x", Kind: library.Veg, Allergens: []string{"milk", "nuts"}}
	people := []library.Person{
		{ID: "mia", Allergens: []string{"nuts"}},
		{ID: "papa"},
		{ID: "seb", Allergens: []string{"nuts"}},
		{ID: "oma", Allergens: []string{"milk", "nuts", "soy"}},
	}
	got := library.PersonWarnings(a, people)
	want := []library.PersonWarning{{Person: "mia", Allergens: []string{"nuts"}}, {Person: "seb", Allergens: []string{"nuts"}}, {Person: "oma", Allergens: []string{"milk", "nuts"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v (one line per person, even with the same allergens)", got, want)
	}
	if got := library.PersonWarnings(library.Article{Kind: library.NonFood, Allergens: []string{"nuts"}}, people); len(got) != 0 {
		t.Errorf("non-food warned: %+v", got)
	}
}
