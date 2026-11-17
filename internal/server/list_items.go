package server

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/unit"
)

// Adding to lists (ticket 165, ADR 0019): articles from the library or free
// text, with or without a quantity, in the web interface, on the displays and
// over the API. The rules live in internal/list and internal/unit; this file
// decides which units a household has and how an article's default units
// become the unit a total is shown in.

// homeCountry is the country of the home (ISO 3166-1 alpha-2, from the region
// code of the place), "" when the home has no place yet.
func homeCountry(h store.Household) string {
	c, _, _ := strings.Cut(h.Region, "-")
	return strings.ToUpper(c)
}

// activeUnits are the units the household switched on: what a parent saved
// under Settings, Lists, Units, otherwise the preset of the home's country.
func (s *server) activeUnits(h store.Household) []string {
	if on, custom, err := s.db.ActiveUnits(h.ID); err == nil && custom {
		return on
	}
	return unit.Preset(homeCountry(h))
}

// freeTextUnits are the default units of text that is no article.
var freeTextUnits = []string{unit.Piece, "pack"}

// itemCtx is what adding to a list or changing an entry needs to know.
type itemCtx struct {
	s      *server
	p      principal
	lang   string
	st     libState
	active []string
	listID string // the list the context was made for, "" if none
}

func (s *server) itemContext(p principal, lang string) (*itemCtx, error) {
	st, err := s.libState(p, lang)
	if err != nil {
		return nil, err
	}
	return &itemCtx{s: s, p: p, lang: lang, st: st, active: s.activeUnits(p.household)}, nil
}

// article finds a shipped or an own article by key or id.
func (c *itemCtx) article(id string) (articleItem, bool) {
	if id == "" {
		return articleItem{}, false
	}
	return c.st.item(id)
}

// byName finds the article a text names exactly (ignoring case and umlauts),
// in German or English; own articles first, and never one a parent hid by hand.
func (c *itemCtx) byName(text string) (articleItem, bool) {
	n := library.Fold(text)
	if n == "" {
		return articleItem{}, false
	}
	for _, it := range c.st.items() {
		if it.handHidden {
			continue
		}
		if library.Fold(it.De) == n || library.Fold(it.En) == n {
			return it, true
		}
	}
	return articleItem{}, false
}

// defaults are the default units of an article, or of free text.
func defaultsOf(it *articleItem) []string {
	if it == nil {
		return freeTextUnits
	}
	return it.Units
}

// show is how a total of this article is shown.
func (c *itemCtx) show(it *articleItem) list.Show {
	defaults := defaultsOf(it)
	return func(base float64, kind unit.Kind) string { return unit.ShowUnit(base, kind, defaults, c.active) }
}

// offered are the default units to offer first for an article (or text).
func (c *itemCtx) offered(it *articleItem) []string { return unit.Effective(defaultsOf(it), c.active) }

// preselected is the unit a new quantity starts with: the one last used for
// the article if it is still on, otherwise the article's first default unit.
func (c *itemCtx) preselected(it *articleItem) string {
	if it != nil {
		if u, err := c.s.db.LastUnit(c.p.household.ID, it.ID); err == nil && u != "" && slices.Contains(c.active, u) {
			return u
		}
	}
	return c.offered(it)[0]
}

// itemField names what is wrong with the input.
type itemField string

const (
	fieldText   itemField = "text"
	fieldAmount itemField = "amount"
	fieldUnit   itemField = "unit"
	fieldItem   itemField = "article"
)

// itemError is a refused input; Field is where the person sees it.
type itemError struct {
	Field itemField
	Msg   string
}

func (e *itemError) Error() string { return string(e.Field) + ": " + e.Msg }

// itemIn is what a form or a request says about an item to add.
type itemIn struct {
	Text, ArticleID string
	Qty             bool   // with quantity
	Amount          string // as typed, comma or point
	Unit            string // "" takes the preselected one
}

// resolved is an item ready to be stored.
type resolved struct {
	Item    store.Item
	Article *articleItem // nil for free text
	Unit    string       // the unit chosen, "" without quantity
}

// resolve turns the input into what is stored: the article (by id, or by the
// exact name typed), the text in the language of the page and the quantity.
func (c *itemCtx) resolve(in itemIn) (resolved, error) {
	var art *articleItem
	text := strings.TrimSpace(in.Text)
	if in.ArticleID != "" {
		it, ok := c.article(in.ArticleID)
		if !ok {
			return resolved{}, &itemError{fieldItem, "unknown article"}
		}
		art = &it
	} else if it, ok := c.byName(text); ok {
		art = &it
	}
	if art != nil && (in.ArticleID != "" || library.Fold(text) == library.Fold(art.Name(c.lang))) {
		text = art.Name(c.lang) // the name as the library writes it; a name typed in the other language stays as typed
	}
	if err := list.ValidateText(text); err != nil {
		return resolved{}, &itemError{fieldText, err.Error()}
	}
	out := resolved{Item: store.Item{Text: text}, Article: art}
	if art != nil {
		out.Item.ArticleID = art.ID
	}
	if in.Qty {
		q, u, err := c.quantity(art, in.Amount, in.Unit)
		if err != nil {
			return resolved{}, err
		}
		out.Item.Qty, out.Item.ChosenUnit, out.Unit = q, u, u
	}
	return out, nil
}

// quantity reads an amount and a unit: the amount must be a number above 0 up
// to 9999, the unit one that is switched on (the preselected one when empty).
func (c *itemCtx) quantity(art *articleItem, amount, key string) (list.Quantity, string, error) {
	n, err := unit.Parse(amount)
	if err != nil {
		return list.Quantity{}, "", &itemError{fieldAmount, "not an amount above 0 up to 9999"}
	}
	if key == "" {
		key = c.preselected(art)
	}
	if !unit.Valid(key) || !slices.Contains(c.active, key) {
		return list.Quantity{}, "", &itemError{fieldUnit, "unknown or switched off unit"}
	}
	return list.NewQuantity(n, key), key, nil
}

// asItemError reports whether err is a refused input.
func asItemError(err error) (*itemError, bool) {
	var ie *itemError
	return ie, errors.As(err, &ie)
}

// amountText is a number the way it is typed back into a field: rounded for
// display, a point or a comma by language.
func amountText(q list.Quantity, lang string) string {
	return unit.Format(q.Amount(), q.Unit, lang)
}

func formatJSONAmount(q list.Quantity) float64 { return unit.Round(q.Amount(), q.Unit) }

// numberText writes a JSON number the way it would be typed.
func numberText(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
