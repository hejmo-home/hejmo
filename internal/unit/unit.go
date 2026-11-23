// Package unit is the catalogue of units a quantity can have (ADR 0019): the
// counting units, metric, imperial and US measures. US and UK measures that
// differ are named apart. It is pure: no storage, no pages.
package unit

import "slices"

// All lists the units in the order the pages show them (catalogue keys
// "unit.<key>").
var All = []string{"piece", "dozen", "pack", "bunch", "g", "kg", "ml", "l", "oz", "lb", "floz_us", "floz_uk", "cup_us", "pint_us", "pint_uk", "quart_us", "gallon_us", "gallon_uk"}

// DefaultActive are the units a household starts with: the counting units and
// metric, which fit most countries. The country of the home location will
// change the preset (ticket 165, Settings, Lists, Units).
var DefaultActive = []string{"piece", "dozen", "pack", "bunch", "g", "kg", "ml", "l"}

// Piece is the unit offered when none of an article's default units is active.
const Piece = "piece"

// Valid reports whether key is a unit of the catalogue.
func Valid(key string) bool { return slices.Contains(All, key) }
