package server

import "time"

// Overview rows and detail pages (ticket 111, ADR 0017): an overview lists calm
// rows that open a detail page, which holds the facts, the one form and the
// removal. These are the view types the shared partials in patterns.html take.

// flagView is a problem or a warning in words (Kind: "problem" or "warn"), or
// a calm state (Kind "open"), or a paused connection (Kind "paused": the word
// tag of ticket 201, with After, if set, as the words that follow it). Icon picks another icon than the default one:
// "clock" or "triangle" (ticket 145).
type flagView struct{ Kind, Text, Icon, After string }

// rowLine is one line under a row's title: words, and who it is for.
type rowLine struct {
	Text   string
	Whole  bool
	People []personChip
}

// rowView is one row of an overview.
type rowView struct {
	Href, Title string
	Lines       []rowLine
	Flag        *flagView
}

// factView is one label and value of a detail page's facts card.
type factView struct {
	Label, Value string
	Flag         *flagView
	Whole        bool
	People       []personChip
}

// silentAfter is how long a display may stay out of contact before its row
// warns (ticket 111).
const silentAfter = 15 * time.Minute
