// Package nutrition is the domain of nutrition values (ADR 0021, ticket 288):
// the seven values of the EU nutrition declaration, the foods they come from
// (BLS 4.0 and USDA FoodData Central, shipped inside the box), what an
// article of the library is made of, how an ingredient line becomes grams and
// how a recipe's values per portion are summed, rounded and worded. It is pure
// and offline: no storage, no pages, no network.
//
// The rule that governs everything here: a value that is not known is never
// 0. A food that lacks a value, an ingredient that cannot be counted and a
// recipe with nothing countable all say so instead of showing a number.
package nutrition

// The food data, data/foods.tsv.gz, is made by tools/foodlist from the files of
// the two sources, which are never part of the repository (BLS: the two xlsx
// files of BLS 4.0 from blsdb.de; USDA: the zip files of Foundation Foods and SR
// Legacy from fdc.nal.usda.gov). To make it again:
//
//	HEJMO_BLS_DIR=/path/to/bls HEJMO_USDA_DIR=/path/to/usda go generate ./internal/nutrition
//
//go:generate go run ../../tools/foodlist -bls ${HEJMO_BLS_DIR} -usda ${HEJMO_USDA_DIR} -out data/foods.tsv.gz

// Key names one of the eight numbers of the seven values (energy has two).
type Key int

// The eight numbers, in the order of the data file and of the page.
const (
	KJ Key = iota
	KCal
	Fat
	Saturates
	Carbs
	Sugars
	Protein
	Salt
	NumKeys
)

// Values are numbers per 100 g of a food, or per portion of a recipe. A
// number that is not known is missing, not 0.
type Values struct {
	v   [NumKeys]float64
	has uint16
}

// Get returns the value and whether it is known.
func (v Values) Get(k Key) (float64, bool) {
	if k < 0 || k >= NumKeys || v.has&(1<<k) == 0 {
		return 0, false
	}
	return v.v[k], true
}

// Set makes a value known.
func (v *Values) Set(k Key, x float64) {
	v.v[k] = x
	v.has |= 1 << k
}
