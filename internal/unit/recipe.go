package unit

import "slices"

// Recipes know two groups of units on top of the household's (ADR 0021,
// product owner decision of 24/11/2026, ticket 279). They are not part of the
// catalogue All: the lists, their settings page and the active units stay as
// ticket 165 made them.

// Kitchen are the kitchen measures (catalogue keys "unit.<key>"): tablespoon,
// teaspoon, pinch, knife tip, dash, splash, handful and cup. They never
// convert into or add up with g and ml, not even with each other, and on a
// shopping list the article goes on without a quantity.
var Kitchen = []string{"tbsp", "tsp", "pinch", "knifetip", "dash", "splash", "handful", "cup"}

// Containers are the containers and piece measures: can, packet, tub, bunch,
// jar, bottle, stick, clove, slice, cube, sprig, stalk, ball, leaf, punnet and pot.
// They keep their quantity on a shopping list ("2 cans of tomatoes"), and
// quantities add up only within the same unit.
var Containers = []string{"can", "packet", "tub", "bunch", "jar", "bottle", "stick", "clove", "slice", "cube", "sprig", "stalk", "ball", "leaf", "punnet", "pot"}

// IsKitchen reports whether key is a kitchen measure.
func IsKitchen(key string) bool { return slices.Contains(Kitchen, key) }

// IsContainer reports whether key is a container or piece measure.
func IsContainer(key string) bool { return slices.Contains(Containers, key) }

// KeepsQuantityOnList says whether an ingredient in this unit goes onto a
// shopping list with its quantity: everything but the kitchen measures.
func KeepsQuantityOnList(key string) bool { return !IsKitchen(key) }

// ValidForRecipe reports whether key is a unit an ingredient can have: the
// catalogue's, a kitchen measure or a container.
func ValidForRecipe(key string) bool { return Valid(key) || IsKitchen(key) || IsContainer(key) }

// ForRecipe returns the units a recipe offers for an ingredient: the active
// ones of the household in the order of the catalogue, then the kitchen
// measures, then the containers (bunch stays where the household has it).
// Piece is always there.
func ForRecipe(active []string) []string {
	out := Normalize(active)
	for _, k := range slices.Concat(Kitchen, Containers) {
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}
