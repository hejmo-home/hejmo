// Package shop is the rule that puts the ingredients of planned recipes on a
// shopping list (ticket 283, ADR 0019 and 0021). It is pure: the meals and the
// entries of the list come in, rows and the steps of adding come out. Every
// adding goes through list.PlanAdd, so nothing lands twice.
package shop

import (
	"crypto/sha1"
	"encoding/hex"
	"slices"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/recipe"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Article is what the library says about an ingredient: the article it names
// (ID "" for free text), the name to put on the list and whether it is a pantry article.
type Article struct {
	ID, Text string
	Pantry   bool
}

// Meal is one planned recipe to take ingredients from. Portions is how many to
// shop for now (all of them, or only the missing ones); Base is the portions the recipe is written for.
type Meal struct {
	Key, Day   string // Key is "<day>.<mealtime>"
	Mealtime   meal.Mealtime
	RecipeID   string
	RecipeName string
	Portions   int
	Base       int
	// Ingredients are the recipe's, not yet scaled.
	Ingredients []recipe.Ingredient
}

// Part is what one meal brings to a row. A kitchen measure brings no
// quantity; Kitchen and KitchenAmount keep what the recipe says for the sentence.
type Part struct {
	Meal          string
	Qty           list.Quantity
	Kitchen       string
	KitchenAmount float64
}

// Row is one article (with its variety and kind of unit) with everything the
// chosen meals bring, summed.
type Row struct {
	Key       string
	ArticleID string
	Variety   string
	Text      string
	Qty       list.Quantity
	Pantry    bool
	Parts     []Part
}

// Scaled is an ingredient for n portions: kitchen measures have no quantity, a
// container or a piece keeps its quantity, and an amount without a unit counts pieces.
func Scaled(in recipe.Ingredient, base, n int) (q list.Quantity, p Part) {
	if in.Amount <= 0 {
		return list.Quantity{}, Part{}
	}
	amount := recipe.Scale(in.Amount, base, n)
	if unit.IsKitchen(in.Unit) {
		return list.Quantity{}, Part{Kitchen: in.Unit, KitchenAmount: amount}
	}
	u := in.Unit
	if u == "" {
		u = unit.Piece
	}
	q = list.NewQuantity(amount, u)
	return q, Part{Qty: q}
}

func hash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:4])
}

// RowKey names a row on the page: article (or the text), variety and kind of unit.
func RowKey(articleID, text, variety string, q list.Quantity) string {
	id := "a:" + articleID
	if articleID == "" {
		id = "t:" + hash(library.Fold(text))
	}
	v := ""
	if k := list.VarietyKey(variety); k != "" {
		v = hash(k)
	}
	kind := ""
	if !q.None() {
		kind = string(q.Kind())
	}
	return id + ":" + v + ":" + kind
}

// Rows scales the ingredients of the meals and sums the same article, variety
// and kind of unit across them, in the order they first appear.
func Rows(meals []Meal, resolve func(recipe.Ingredient) Article) []Row {
	var rows []Row
	at := map[string]int{}
	for _, m := range meals {
		for _, in := range m.Ingredients {
			a := resolve(in)
			q, part := Scaled(in, m.Base, m.Portions)
			part.Meal = m.Key
			variety := ""
			if a.ID != "" {
				variety = list.NormalizeVariety(in.Variety)
			}
			key := RowKey(a.ID, a.Text, variety, q)
			i, ok := at[key]
			if !ok {
				i = len(rows)
				at[key] = i
				rows = append(rows, Row{Key: key, ArticleID: a.ID, Variety: variety, Text: a.Text, Pantry: a.Pantry, Qty: list.Quantity{Unit: q.Unit}})
			}
			r := &rows[i]
			r.Qty.Base += q.Base
			r.Parts = append(r.Parts, part)
		}
	}
	return rows
}

// Step is what adding one row does to a list.
type Step struct {
	Row  Row
	Plan list.AddPlan
	// Entry is the entry that changes, already exists or is made; a new one has the id "new:<row key>".
	Entry list.Entry
}

// NewID is the placeholder id of the entry a row makes.
func NewID(rowKey string) string { return "new:" + rowKey }

// Plan decides, row by row and in this order, what adding the rows to the
// entries of a list does: each step sees the entries as the steps before it
// leave them. The entries are not changed.
func Plan(entries []list.Entry, rows []Row, show func(Row) list.Show) []Step {
	cur := slices.Clone(entries)
	var steps []Step
	for _, r := range rows {
		p := list.PlanAdd(cur, r.ArticleID, r.Variety, r.Text, r.Qty, show(r))
		s := Step{Row: r, Plan: p}
		switch p.Action {
		case list.AddedTo, list.Filled:
			for i := range cur {
				if cur[i].ID == p.Target.ID {
					cur[i].Qty = p.Result
					s.Entry = cur[i]
				}
			}
		case list.Exists:
			s.Entry = p.Target
		default:
			e := list.Entry{ID: NewID(r.Key), Text: r.Text, ArticleID: r.ArticleID, Variety: r.Variety, Qty: p.Result}
			at := len(cur)
			if p.After != "" {
				if i := slices.IndexFunc(cur, func(x list.Entry) bool { return x.ID == p.After }); i >= 0 {
					at = i + 1
				}
			}
			cur = slices.Insert(cur, at, e)
			s.Entry = e
		}
		steps = append(steps, s)
	}
	return steps
}
