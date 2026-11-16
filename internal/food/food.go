// Package food is the domain of diet and allergies per person (ADR 0019): the
// four diets, the 14 main EU allergens and the free text that Hejmo cannot
// check. It is pure: no storage, no pages.
package food

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Diet is how a person eats.
type Diet string

// The four diets; Omnivore is the default.
const (
	Omnivore    Diet = "omnivore"
	Vegetarian  Diet = "vegetarian"
	Pescatarian Diet = "pescatarian"
	Vegan       Diet = "vegan"
)

// Diets lists the diets in the order the pages show them.
var Diets = []Diet{Omnivore, Vegetarian, Pescatarian, Vegan}

// ValidDiet reports whether s is one of the four diets.
func ValidDiet(s string) bool { return slices.Contains(Diets, Diet(s)) }

// Allergens are the 14 main allergens of EU Regulation 1169/2011, Annex II,
// by key (catalogue keys "allergen.<key>").
var Allergens = []string{"celery", "gluten", "crustaceans", "eggs", "fish", "lupin", "milk", "molluscs", "mustard", "nuts", "peanuts", "sesame", "soy", "sulphites"}

// MaxOther is the longest free text, in characters.
const MaxOther = 200

// OtherFits reports whether the free text is short enough.
func OtherFits(s string) bool { return utf8.RuneCountInString(s) <= MaxOther }

// NormalizeAllergens returns the keys once each in the order of Allergens; ok
// is false when one is not an allergen.
func NormalizeAllergens(keys []string) ([]string, bool) {
	for _, k := range keys {
		if !slices.Contains(Allergens, k) {
			return nil, false
		}
	}
	out := []string{}
	for _, a := range Allergens {
		if slices.Contains(keys, a) {
			out = append(out, a)
		}
	}
	return out, true
}

// Conflict says a free-text entry names one of the 14 allergens, which has its
// own card.
type Conflict struct {
	Allergen string // the key
	Word     string // the entry as typed
}

// words are names and common words for each allergen (German and English). An
// entry equal to one of them is a conflict; single kinds ("Haselnuss") are not.
var words = map[string][]string{
	"eggs":        {"ei", "eier", "egg", "eggs"},
	"peanuts":     {"erdnuss", "erdnüsse", "erdnuesse", "peanut", "peanuts"},
	"fish":        {"fisch", "fische", "fish"},
	"gluten":      {"gluten", "glutenhaltiges getreide", "getreide", "cereals", "grain", "gluten-containing cereals"},
	"crustaceans": {"krebstiere", "krustentiere", "krebstier", "crustacean", "crustaceans"},
	"lupin":       {"lupine", "lupinen", "lupin", "lupins"},
	"milk":        {"milch", "laktose", "lactose", "milk", "milch und laktose", "milchprodukte", "milchzucker", "dairy", "milk and lactose"},
	"nuts":        {"nuss", "nüsse", "nuesse", "schalenfrüchte", "schalenfrucht", "schalenfrüchte (nüsse)", "nut", "nuts", "tree nuts", "tree nuts (nuts)"},
	"sulphites":   {"sulfite", "sulfit", "sulphites", "sulphite", "sulfites", "schwefeldioxid", "schwefeldioxid und sulfite", "sulphur dioxide and sulphites"},
	"celery":      {"sellerie", "celery"},
	"mustard":     {"senf", "mustard"},
	"sesame":      {"sesam", "sesame"},
	"soy":         {"soja", "soy", "soya", "sojabohne", "soybeans"},
	"molluscs":    {"weichtiere", "weichtier", "mollusken", "molluscs", "mollusks"},
}

// ParseOther splits the free text at commas into its entries (trimmed, empty
// ones dropped, repeated ones once). When an entry names one of the 14 it also
// returns the first such conflict.
func ParseOther(text string) ([]string, *Conflict) {
	items := []string{}
	var conflict *Conflict
	seen := map[string]bool{}
	for _, part := range strings.Split(text, ",") {
		item := strings.Join(strings.Fields(part), " ")
		key := strings.ToLower(item)
		if item == "" || seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, item)
		if conflict == nil {
			for a, ws := range words {
				if slices.Contains(ws, key) {
					conflict = &Conflict{Allergen: a, Word: item}
				}
			}
		}
	}
	return items, conflict
}
