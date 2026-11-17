package library

import (
	"slices"

	"github.com/hejmo-home/hejmo/internal/food"
)

// Person is what the library needs to know about a member of the household:
// the diet and the allergens ticked on the person's page. Free text is not
// part of it: it cannot be checked.
type Person struct {
	ID        string
	Diet      food.Diet
	Allergens []string
}

// Warning says that the people (by id, in the order of the household) are
// allergic to these allergens of the article (in the order of food.Allergens).
// People with exactly the same allergens share one warning: a line in words
// names each of them and never counts.
type Warning struct {
	People    []string
	Allergens []string
}

// affecting is the allergens of the article that the person must avoid.
func affecting(a Article, p Person) []string {
	if a.Kind == NonFood {
		return nil
	}
	var out []string
	for _, k := range food.Allergens {
		if slices.Contains(a.Allergens, k) && slices.Contains(p.Allergens, k) {
			out = append(out, k)
		}
	}
	return out
}

// Warnings returns who has to look out for the article, grouped by what they
// are allergic to.
func Warnings(a Article, people []Person) []Warning {
	var out []Warning
	for _, p := range people {
		hit := affecting(a, p)
		if len(hit) == 0 {
			continue
		}
		if i := slices.IndexFunc(out, func(w Warning) bool { return slices.Equal(w.Allergens, hit) }); i >= 0 {
			out[i].People = append(out[i].People, p.ID)
			continue
		}
		out = append(out, Warning{People: []string{p.ID}, Allergens: hit})
	}
	return out
}

// FitsPeople splits the household by whether the article suits their diet.
func FitsPeople(a Article, people []Person) (fit, notFit []Person) {
	for _, p := range people {
		if a.Kind.Fits(p.Diet) {
			fit = append(fit, p)
		} else {
			notFit = append(notFit, p)
		}
	}
	return fit, notFit
}

// Hiding is the household's optional, experimental setting to leave articles
// with an allergen out of the suggestions (ADR 0019). Off by default.
type Hiding struct {
	Enabled bool
	// Everyone means every person with allergies; otherwise only People.
	Everyone bool
	People   []string
}

// HiddenFor returns the people whose allergen makes the article hidden; none
// when hiding is off or the article touches nobody it is switched on for.
func (h Hiding) HiddenFor(a Article, people []Person) []string {
	if !h.Enabled {
		return nil
	}
	var out []string
	for _, p := range people {
		if !h.Everyone && !slices.Contains(h.People, p.ID) {
			continue
		}
		if len(affecting(a, p)) > 0 {
			out = append(out, p.ID)
		}
	}
	return out
}

// Reason says why an article is not suggested.
type Reason string

// The reasons, in the order they are checked.
const (
	ReasonNone    Reason = ""
	ReasonHand    Reason = "hand"
	ReasonAllergy Reason = "allergy"
	ReasonDiet    Reason = "diet"
)

// Suggestion is whether Hejmo suggests an article, and if not why; By names
// the people behind an allergy.
type Suggestion struct {
	Suggested bool
	Reason    Reason
	By        []string
}

// Suggest decides whether the article is suggested: not when a parent hid it,
// not when hiding is on for someone it endangers, and only when it suits at
// least one person's diet (a household without people gets every article).
func Suggest(a Article, people []Person, hiddenByHand bool, h Hiding) Suggestion {
	if hiddenByHand {
		return Suggestion{Reason: ReasonHand}
	}
	if by := h.HiddenFor(a, people); len(by) > 0 {
		return Suggestion{Reason: ReasonAllergy, By: by}
	}
	if fit, _ := FitsPeople(a, people); len(people) > 0 && len(fit) == 0 {
		return Suggestion{Reason: ReasonDiet}
	}
	return Suggestion{Suggested: true}
}

// PersonWarning is one person and the allergens of an article that person
// must avoid. The tile form of a warning has one line per person, so nobody
// is hidden behind a count.
type PersonWarning struct {
	Person    string
	Allergens []string
}

// PersonWarnings returns a line for every person the article affects, in the
// order of the household.
func PersonWarnings(a Article, people []Person) []PersonWarning {
	var out []PersonWarning
	for _, p := range people {
		if hit := affecting(a, p); len(hit) > 0 {
			out = append(out, PersonWarning{Person: p.ID, Allergens: hit})
		}
	}
	return out
}
