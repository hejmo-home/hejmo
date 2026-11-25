// Package starter is the starter collection of 0.3 (ADR 0021, ticket 290): 200
// proven dishes in German and English that ship with the box. The files in
// data/ are the product owner's approved drafts and stay as they are; this
// package reads them, maps their unit spellings to the units of a recipe, maps
// ingredients to articles of the library where it can and builds a
// recipe.Recipe in the language of the person viewing it. It is pure: no
// storage, no pages.
package starter

import (
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/recipe"
)

//go:embed data/*.json
var files embed.FS

// IDPrefix starts the id of every starter recipe; the id of an own recipe never has it.
const IDPrefix = "starter-"

// IsID reports whether id names a starter recipe.
func IsID(id string) bool { return strings.HasPrefix(id, IDPrefix) }

type rawIngredient struct {
	Amount    *float64 `json:"amount"`
	Unit      *string  `json:"unit"`
	ArticleDE string   `json:"article_de"`
	ArticleEN string   `json:"article_en"`
	NoteDE    *string  `json:"note_de"`
	NoteEN    *string  `json:"note_en"`
}

type rawText struct {
	Title string   `json:"title"`
	Steps []string `json:"steps"`
	Tips  string   `json:"tips"`
}

type rawRecipe struct {
	N           int    `json:"n"`
	Slug        string `json:"slug"`
	Category    string `json:"category"`
	Servings    *int   `json:"servings"`
	TimeMinutes *int   `json:"time_minutes"`
	Source      struct {
		URL  string `json:"url"`
		Site string `json:"site"`
	} `json:"source"`
	Ingredients []rawIngredient `json:"ingredients"`
	DE          rawText         `json:"de"`
	EN          rawText         `json:"en"`
}

// Entry is one recipe of the collection.
type Entry struct {
	// Number is the number in the approved list (1 to 200); Slug names the file.
	Number int
	Slug   string
	// Site is the source named in the credit ("Ingredients after: Chefkoch"), URL its link.
	Site, URL string
	raw       *rawRecipe
	built     map[string]recipe.Recipe
}

// ID is the id of the recipe as the pages and the API use it.
func (e Entry) ID() string { return IDPrefix + e.Slug }

var (
	loadOnce sync.Once
	entries  []Entry
	byID     map[string]int
)

func load() {
	loadOnce.Do(func() {
		list, err := files.ReadDir("data")
		if err != nil {
			panic("starter: " + err.Error())
		}
		byID = map[string]int{}
		arts := library.Shipped()
		for _, f := range list {
			b, err := files.ReadFile("data/" + f.Name())
			if err != nil {
				panic("starter: " + err.Error())
			}
			r := new(rawRecipe)
			if err := json.Unmarshal(b, r); err != nil || r.Servings == nil || r.TimeMinutes == nil || r.Slug == "" {
				panic(fmt.Sprintf("starter: malformed %s: %v", f.Name(), err))
			}
			e := Entry{Number: r.N, Slug: r.Slug, Site: r.Source.Site, URL: r.Source.URL, raw: r, built: map[string]recipe.Recipe{}}
			for _, lang := range []string{"de", "en"} {
				e.built[lang] = build(r, lang, arts)
			}
			entries = append(entries, e)
		}
		slices.SortFunc(entries, func(a, b Entry) int { return a.Number - b.Number })
		for i, e := range entries {
			byID[e.ID()] = i
		}
	})
}

// All returns the 200 recipes in the order of the approved list.
func All() []Entry {
	load()
	return slices.Clone(entries)
}

// Count is the number of recipes, without copying them.
func Count() int {
	load()
	return len(entries)
}

// ByID finds a starter recipe by its id ("starter-rahmspinat").
func ByID(id string) (Entry, bool) {
	load()
	i, ok := byID[id]
	if !ok {
		return Entry{}, false
	}
	return entries[i], true
}

// Recipe is the recipe in the language of the viewer: "en" for English, German
// for anything else. The result is the caller's own copy.
func (e Entry) Recipe(lang string) recipe.Recipe {
	if lang != "en" {
		lang = "de"
	}
	r := e.built[lang]
	r.Ingredients = slices.Clone(r.Ingredients)
	r.Steps = slices.Clone(r.Steps)
	r.Tags = slices.Clone(r.Tags)
	return r
}

var categories = map[string]recipe.Category{
	"Hauptgerichte": recipe.Main, "Suppen": recipe.Main, "Beilagen": recipe.Side, "Desserts": recipe.Dessert,
	"Backen": recipe.Baking, "Frühstück": recipe.Breakfast, "Snacks und Brotdose": recipe.Snack,
}

func build(r *rawRecipe, lang string, arts []library.Article) recipe.Recipe {
	cat, ok := categories[r.Category]
	if !ok {
		panic("starter: unknown category " + r.Category)
	}
	text := r.DE
	if lang == "en" {
		text = r.EN
	}
	out := recipe.Recipe{ID: IDPrefix + r.Slug, Name: text.Title, Category: cat, Portions: *r.Servings, Minutes: *r.TimeMinutes,
		Tips: text.Tips, Source: r.Source.URL, Steps: slices.Clone(text.Steps)}
	if r.Category == "Suppen" {
		out.Tags = []string{map[string]string{"de": "Suppe", "en": "Soup"}[lang]}
	}
	for _, in := range r.Ingredients {
		out.Ingredients = append(out.Ingredients, ingredient(in, lang, arts))
	}
	return out
}

func ingredient(in rawIngredient, lang string, arts []library.Article) recipe.Ingredient {
	var out recipe.Ingredient
	if in.Amount != nil {
		out.Amount = *in.Amount
	}
	var length string // "5 cm": a length becomes one piece, but stays in the text
	if in.Unit != nil {
		if *in.Unit == "cm" {
			length = strconv.FormatFloat(out.Amount, 'f', -1, 64) + " cm"
			if lang == "de" {
				length = strings.Replace(length, ".", ",", 1)
			}
		}
		u, amount, ok := MapUnit(*in.Unit, out.Amount)
		if !ok {
			panic("starter: unit spelling without a mapping: " + *in.Unit)
		}
		out.Unit, out.Amount = u, amount
	}
	name, note, other := in.ArticleDE, in.NoteDE, in.ArticleEN
	if lang == "en" {
		name, note, other = in.ArticleEN, in.NoteEN, in.ArticleDE
	}
	// The German name decides the article, so both languages show the same one.
	// The text keeps the name as the file writes it ("Zwiebeln, mittelgroß"); a
	// variety would stop quantities from adding up and shows as "Salz · und Pfeffer".
	if art, ok := resolve(in.ArticleDE, arts); ok {
		out.ArticleID = art.ID
	}
	out.Text = strings.Join(strings.Fields(name), " ")
	if out.Text == "" {
		out.Text = other
	}
	var n string
	if note != nil {
		n = strings.TrimSpace(*note)
	}
	if length != "" && !strings.Contains(n, length) {
		n = strings.Join(slices.DeleteFunc([]string{n, length}, func(s string) bool { return s == "" }), ", ")
	}
	if n != "" {
		out.Text += " (" + n + ")"
	}
	return out
}

// alternative are the words that make the rest of a name another ingredient
// ("Wasser oder Milch", "Salz und Pfeffer"): such a line stays free text, so no
// allergen is missed because the first name is an article.
var alternative = []string{"oder", "und", "bzw.", "sowie", "or", "and"}

func alternativeWord(w string) bool { return slices.Contains(alternative, w) }

// resolve finds the article a German ingredient name means: a reviewed spelling
// of the files, the whole name, or an article's name followed by a description
// ("Linsen, rot").
func resolve(name string, arts []library.Article) (library.Article, bool) {
	if a, ok := spelled(name, arts); ok {
		return a, true
	}
	if a, ok := library.Resolve(name, arts); ok {
		return a, true
	}
	if a, rest, ok := library.LeadingName(name, arts); ok {
		for _, w := range strings.Fields(strings.ToLower(rest)) {
			if alternativeWord(strings.Trim(w, ",;()")) {
				return library.Article{}, false
			}
		}
		return a, true
	}
	return library.Article{}, false
}

// units maps the spellings of the files to recipe units (product owner,
// 24/11/2026, ticket 279).
var units = map[string]string{
	"g": "g", "kg": "kg", "ml": "ml", "l": "l", "Liter": "l", "Stk": "piece", "Stück": "piece",
	"EL": "tbsp", "TL": "tsp", "Prise": "pinch", "Msp.": "knifetip", "Schuss": "dash", "Spritzer": "splash",
	"Handvoll": "handful", "Tasse": "cup", "Dose": "can", "Pck.": "packet", "Pck": "packet", "Packung": "packet",
	"Päckchen": "packet", "Becher": "tub", "Bund": "bunch", "Glas": "jar", "Flasche": "bottle", "Stange": "stick",
	"Zehe": "clove", "Scheibe": "slice", "Würfel": "cube", "Zweig": "sprig", "Stiel": "stalk", "Kugel": "ball",
	"Blatt": "leaf", "Beet": "punnet", "Topf": "pot", "Töpfchen": "pot",
}

// MapUnit turns a spelling of the files into a recipe unit and the amount in
// it. Centilitres become millilitres; a length ("5 cm" of ginger) becomes one
// piece, so no ingredient is free text because of its unit (the line keeps the
// length in its text). ok is false for a
// spelling nobody decided.
func MapUnit(spelling string, amount float64) (key string, converted float64, ok bool) {
	switch spelling {
	case "cl":
		return "ml", amount * 10, true
	case "cm":
		return "piece", 1, true
	}
	key, ok = units[spelling]
	return key, amount, ok
}

// UnmappedUnits lists the unit spellings of the files that MapUnit does not know.
func UnmappedUnits() []string {
	load()
	var out []string
	for _, e := range entries {
		for _, in := range e.raw.Ingredients {
			if in.Unit == nil {
				continue
			}
			if _, _, ok := MapUnit(*in.Unit, 1); !ok && !slices.Contains(out, *in.Unit) {
				out = append(out, *in.Unit)
			}
		}
	}
	return out
}
