package holiday

import "time"

// swissRules: federal and canton-wide holidays. Provisional until the
// per-canton table is verified against official sources.
var swissRules = []rule{
	{"Neujahr", "New Year's Day", fixed(time.January, 1), []string{"CH"}, 0},
	{"Auffahrt", "Ascension Day", easterOffset(39), []string{"CH"}, 0},
	{"Bundesfeier", "Swiss National Day", fixed(time.August, 1), []string{"CH"}, 0},
	{"Weihnachten", "Christmas Day", fixed(time.December, 25), []string{"CH"}, 0},
}
