package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Articles that are always on the list on the web (ticket 168, preview-168).
// alwaysCtx gathers what the words need: the household's time and holiday, the
// day, the lists and the item context for quantities.

type alwaysCtx struct {
	s       *server
	p       principal
	lang    string
	c       *itemCtx
	now     time.Time // in the household's zone
	today   time.Time // its day, at UTC midnight
	set     always.Settings
	lastRun string
	lists   []list.List
}

func (s *server) alwaysContext(p principal, lang string) (*alwaysCtx, error) {
	c, err := s.itemContext(p, lang)
	if err != nil {
		return nil, err
	}
	a := &alwaysCtx{s: s, p: p, lang: lang, c: c, now: s.householdNow(p.household)}
	a.today = civil(a.now)
	if a.set, err = s.db.AlwaysSettings(p.household.ID); err != nil {
		return nil, err
	}
	if a.lastRun, err = s.db.AlwaysLastRun(p.household.ID); err != nil {
		return nil, err
	}
	if a.lists, err = s.db.Lists(p.household.ID); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *alwaysCtx) tr(key string, kv ...string) string { return a.s.tr(a.lang, key, kv...) }

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

// nb joins a number and its unit with a no-break space.
func nb(s string) string { return strings.Replace(s, " ", " ", 1) }

func (a *alwaysCtx) listByID(id string) (list.List, bool) {
	for _, l := range a.lists {
		if l.ID == id {
			return l, true
		}
	}
	return list.List{}, false
}

// weekday is the name of an ISO weekday (1 Monday to 7 Sunday).
func (a *alwaysCtx) weekday(n int) string { return a.s.mealWeekday(a.lang, n) }

// rhythm is "Jeden Montag", "Jeden zweiten Montag", "Alle 4 Wochen am Donnerstag".
func (a *alwaysCtx) rhythm(every, weekday int) string {
	day := a.weekday(weekday)
	switch every {
	case 1:
		return a.tr("always.rhythm.1", "day", day)
	case 2:
		return a.tr("always.rhythm.2", "day", day)
	}
	return a.tr("always.rhythm.n", "n", strconv.Itoa(every), "day", day)
}

// rhythmOn is the rhythm with the list: "Jeden Montag auf „Einkauf“".
func (a *alwaysCtx) rhythmOn(r always.Rule) string {
	return a.tr("always.on_list", "rhythm", a.rhythm(r.Every, r.Weekday), "list", r.ListName)
}

func (a *alwaysCtx) qty(q list.Quantity) string { return nb(a.c.qtyWords(q)) }

// qtyOrNone is the quantity of a rule, or "ohne Menge".
func (a *alwaysCtx) qtyOrNone(q list.Quantity) string {
	if q.None() {
		return a.tr("always.qty.none")
	}
	return a.qty(q)
}

// date is a day without its year, with it once it is not this year's.
func (a *alwaysCtx) date(d time.Time) string {
	if d.Year() != a.today.Year() {
		return datefmt.Date(a.p.household.DateFormat, d)
	}
	return datefmt.Short(a.p.household.DateFormat, d)
}

// dayName is "Montag, 23.11.".
func (a *alwaysCtx) dayName(d time.Time) string {
	return a.tr("always.day", "weekday", a.s.mealWeekday(a.lang, int(d.Weekday())), "date", a.date(d))
}

// at is the household's time in words: "3:00 Uhr".
func (a *alwaysCtx) at() string { return a.atOf(a.set.At) }

func (a *alwaysCtx) atOf(hhmm string) string {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return hhmm
	}
	if a.p.household.TimeFormat == store.Time12h {
		return datefmt.Clock(store.Time12h, t)
	}
	return a.tr("always.at", "time", fmt.Sprintf("%d:%02d", t.Hour(), t.Minute()))
}

func (a *alwaysCtx) todayStr() string { return a.today.Format(always.DateLayout) }

// from is the first day the box may still put something on a list: today while
// its time has not come, else tomorrow.
func (a *alwaysCtx) from() time.Time {
	if a.lastRun != a.todayStr() && a.now.Format("15:04") < a.set.At {
		return a.today
	}
	return a.today.AddDate(0, 0, 1)
}

// holidayLive is the holiday as far as it still counts: not over.
func (a *alwaysCtx) holiday() always.Holiday {
	h := a.set.Holiday
	if !h.On() || h.Expired(a.today) {
		return always.Holiday{}
	}
	return h
}

// next is the day a rule comes next, and whether the holiday moved it.
func (a *alwaysCtx) next(r always.Rule) (d time.Time, afterHoliday, ok bool) {
	d, ok = r.Next(a.from())
	if !ok {
		return
	}
	if h := a.holiday(); h.Covers(d) {
		to, _ := time.Parse(always.DateLayout, h.To)
		d, ok = r.Next(to.AddDate(0, 0, 1))
		afterHoliday = true
	}
	return
}

// names is article names joined like words.
func (a *alwaysCtx) names(n []string) string { return a.s.joinWords(a.lang, n) }

// firstList is the list a new rule preselects: the first with suggestions.
func (a *alwaysCtx) firstList() string {
	for _, l := range a.lists {
		if l.Suggestions {
			return l.ID
		}
	}
	if len(a.lists) > 0 {
		return a.lists[0].ID
	}
	return ""
}
