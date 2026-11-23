package server

import (
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// "Always on the list" on the list pages and the displays (ticket 168,
// preview-168 screens 17 to 23): the line above the entries on the day, the
// reason under an entry Hejmo put on, and for parents the articles that come.

// autoNote is the line above the entries.
type autoNote struct {
	Head, Text string
	Holiday    bool
}

// autoRules is the details element for parents: what comes by itself on this list.
type autoRules struct {
	Summary string
	Rows    []alwaysRow
}

// listAuto is everything the list page and the display add for a list.
type listAuto struct {
	Note  *autoNote
	Rules *autoRules
}

// forList builds the notes and the lines of the entries for one list, and fills
// AutoSrc and TileAuto of the entry views (open and ticked).
func (s *server) autoOverlay(p principal, lang string, l list.List, es []list.Entry, views ...[]entryView) (listAuto, error) {
	a, err := s.alwaysContext(p, lang)
	if err != nil {
		return listAuto{}, err
	}
	rules, err := a.rules()
	if err != nil {
		return listAuto{}, err
	}
	byArticle := map[string]alwaysRule{}
	var here []alwaysRule
	for _, r := range rules {
		if r.ListID == l.ID && r.Running() {
			byArticle[r.ArticleID] = r
			here = append(here, r)
		}
	}
	origin := map[string]list.Entry{}
	for _, e := range es {
		if e.Origin.Auto {
			origin[e.ID] = e
		}
	}
	for _, vs := range views {
		for i := range vs {
			e, ok := origin[vs[i].ID]
			if !ok {
				continue
			}
			r, ok := byArticle[e.ArticleID]
			if !ok {
				continue
			}
			vs[i].AutoSrc, vs[i].TileAuto = a.entryLines(r.Rule, e)
		}
	}
	out := listAuto{}
	if len(here) > 0 {
		out.Note = a.listNote(here, l)
	}
	if out.Note == nil {
		var err error
		if out.Note, err = a.todayNote(l); err != nil {
			return listAuto{}, err
		}
	}
	if len(here) > 0 && p.person.Role != store.RoleChild {
		out.Rules = a.listRules(here)
	}
	return out, nil
}

// entryLines are the two lines for an entry a rule put on: under the entry in
// the list view, and on the tile.
func (a *alwaysCtx) entryLines(r always.Rule, e list.Entry) (src, tile string) {
	rhythm := lowerFirst(a.rhythm(r.Every, r.Weekday))
	if e.Origin.Prior > 0 && !e.Qty.None() {
		prior := list.Quantity{Base: e.Origin.Prior, Unit: e.Qty.Unit}
		part := list.Quantity{Base: e.Origin.Part, Unit: e.Qty.Unit}
		return a.tr("always.src.split", "prior", a.qty(prior), "part", a.qty(part), "rhythm", rhythm),
			a.tr("always.tile.split", "part", a.qty(part), "rhythm", rhythm)
	}
	return a.tr("always.src", "rhythm", rhythm), a.rhythm(r.Every, r.Weekday)
}

// listNote is the line instead of the day's note while a holiday runs.
func (a *alwaysCtx) listNote(here []alwaysRule, l list.List) *autoNote {
	h := a.holiday()
	if !h.On() || !h.Covers(a.today) {
		return nil
	}
	to, _ := time.Parse(always.DateLayout, h.To)
	var first time.Time
	var names []string
	for _, r := range here {
		d, _, ok := a.next(r.Rule)
		if !ok {
			continue
		}
		switch {
		case first.IsZero() || d.Before(first):
			first, names = d, []string{a.ruleName(r)}
		case d.Equal(first):
			names = append(names, a.ruleName(r))
		}
	}
	n := &autoNote{Holiday: true, Head: a.tr("always.note.running", "to", a.dayName(to))}
	switch {
	case len(names) == 1:
		n.Text = a.tr("always.lnote.holiday.one", "day", a.dayName(first), "names", names[0])
	case len(names) > 1:
		n.Text = a.tr("always.lnote.holiday.many", "day", a.dayName(first), "names", a.names(names))
	default:
		n.Text = a.tr("always.lnote.holiday")
	}
	return n
}

// todayNote says what the rules did on this list today, once their time has passed.
func (a *alwaysCtx) todayNote(l list.List) (*autoNote, error) {
	if a.lastRun != a.todayStr() {
		return nil, nil
	}
	runs, err := a.s.db.AlwaysRuns(a.p.household.ID, a.todayStr())
	if err != nil {
		return nil, err
	}
	var added, sentences []string
	for _, r := range runs {
		if r.ListID != l.ID {
			continue
		}
		switch r.Action {
		case "new":
			t := r.Text
			if !r.Total.None() {
				t += " (" + a.qty(r.Total) + ")"
			}
			added = append(added, t)
		case "added":
			sentences = append(sentences, a.tr("always.lnote.added", "text", r.Text, "part", a.qty(r.Part), "total", a.qty(r.Total)))
		case "filled":
			sentences = append(sentences, a.tr("always.lnote.filled", "text", r.Text, "total", a.qty(r.Total)))
		default:
			sentences = append(sentences, a.tr("always.lnote.exists", "text", r.Text))
		}
	}
	if len(added)+len(sentences) == 0 {
		return nil, nil
	}
	n := &autoNote{}
	var text []string
	if len(added) > 0 {
		n.Head = a.tr("always.lnote.head", "at", a.atOf(a.set.At))
		text = append(text, endSentence(a.names(added)))
	} else {
		n.Head = a.tr("always.lnote.head.none", "at", a.atOf(a.set.At))
	}
	text = append(text, sentences...)
	n.Text = strings.Join(text, " ")
	return n, nil
}

// listRules is "Kommt von selbst auf diese Liste: Milch, Eier" with the rows.
func (a *alwaysCtx) listRules(here []alwaysRule) *autoRules {
	var names []string
	var rows []alwaysRow
	for _, r := range here {
		names = append(names, a.ruleName(r))
		row := a.rowBase(r)
		row.Meta1 = []string{a.tr("always.meta.rhythm", "text", a.rhythm(r.Every, r.Weekday)), a.qtyOrNone(r.Qty)}
		if n := a.nextLine(r.Rule); n != "" {
			row.Meta2 = []string{n}
		}
		rows = append(rows, row)
	}
	return &autoRules{Summary: a.tr("always.lrules.summary", "names", a.names(names)), Rows: rows}
}
