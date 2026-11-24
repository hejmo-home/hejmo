package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/layout"
	"github.com/hejmo-home/hejmo/internal/store"
)

// layoutError is a refused layout: Field names the form field (people, blocks,
// list) and Msg says what is wrong, in English, for the API.
type layoutError struct{ Field, Msg string }

func (e *layoutError) Error() string { return "layout " + e.Field + ": " + e.Msg }

// layoutChange is a change of a display's layout, from the form or the API; nil
// fields stay as they are.
type layoutChange struct {
	Whole  *bool
	People *[]string // the chosen people, in the order of the columns of OrderDay
	// OrderDay is the rotation day of the order People are in: the day the form
	// was loaded, so a form saved after 03:00 moves nobody. nil: today.
	OrderDay *int
	Rotate   *bool
	// Blocks name blocks in the order they should have inside their zone; a
	// block not named keeps its place after the named ones.
	Blocks []blockChange
	ListID *string // "" removes the choice
}

type blockChange struct {
	ID string
	On *bool
}

// layoutFromForm reads the Layout section of the display form.
func layoutFromForm(in url.Values) layoutChange {
	on := func(name string) *bool { v := in.Get(name) != ""; return &v }
	c := layoutChange{Whole: on("whole"), Rotate: on("rotate")}
	if day, err := strconv.Atoi(in.Get("order_day")); err == nil {
		c.OrderDay = &day
	}
	people := slices.Clone(in["person"])
	if people == nil {
		people = []string{}
	}
	c.People = &people
	for _, id := range in["order"] {
		c.Blocks = append(c.Blocks, blockChange{ID: id, On: on("b_" + id)})
	}
	if in.Has("list") {
		l := in.Get("list")
		c.ListID = &l
	}
	return c
}

// applyLayout returns the layout after the change, with the people in their base
// order (layout.Order). It always returns the layout the change asks for,
// together with the error that refuses it, so a refused form can be shown again
// as it was filled in. cur is the stored layout, today the rotation day,
// household the IDs of the people of the household.
func (s *server) applyLayout(householdID string, cur layout.Layout, c layoutChange, today int, household []string) (layout.Layout, error) {
	var refused error
	refuse := func(field, msg string) {
		if refused == nil {
			refused = &layoutError{field, msg}
		}
	}
	l := cur
	if c.Whole != nil {
		l.Whole = *c.Whole
	}
	if c.Rotate != nil {
		l.Rotate = *c.Rotate
	}
	switch {
	case c.People != nil:
		// The people come in the order of the columns of orderDay. While the
		// rotation runs the base order is that order un-rotated; a rotation that
		// is switched on starts from the order as it is, so every display
		// switched on with the same people agrees (Q91).
		var shown []string
		for _, id := range *c.People {
			switch {
			case !slices.Contains(household, id):
				refuse("people", "unknown person")
			case !slices.Contains(shown, id):
				shown = append(shown, id)
			}
		}
		l.People = shown
		if cur.Rotate && l.Rotate {
			orderDay := today
			if c.OrderDay != nil {
				orderDay = *c.OrderDay
			}
			l.People = layout.Unrotate(shown, orderDay)
		}
	case cur.Rotate && !l.Rotate:
		l.People = cur.Order(today) // a rotation switched off stays as it is today
	}
	if len(l.People) == 0 {
		refuse("people", "a display shows at least one person")
	}
	blocks := slices.Clone(l.Blocks)
	named := map[layout.BlockID]bool{}
	var head []layout.Block
	for _, b := range c.Blocks {
		id := layout.BlockID(b.ID)
		if !layout.Known(id) {
			refuse("blocks", "unknown block "+b.ID)
			continue
		}
		if named[id] {
			continue
		}
		named[id] = true
		on := l.Enabled(id)
		if b.On != nil {
			on = *b.On
		}
		head = append(head, layout.Block{ID: id, On: on})
	}
	var rest []layout.Block
	for _, b := range blocks {
		if !named[b.ID] {
			rest = append(rest, b)
		}
	}
	l.Blocks = append(head, rest...)
	switch {
	case c.ListID != nil && *c.ListID != "":
		id, name, ok, err := s.pickList(householdID, *c.ListID)
		switch {
		case err != nil:
			return l, err
		case !ok:
			refuse("list", "unknown list")
		default:
			l.ListID, l.ListName = id, name
		}
	case c.ListID != nil && l.ListID != "":
		// "No choice" removes the choice of a list that is there. The form posts it
		// for a list that was deleted: that one stays, so the page keeps warning.
		_, _, there, err := s.pickList(householdID, l.ListID)
		if err != nil {
			return l, err
		}
		if there {
			l.ListID, l.ListName = "", ""
		}
	}
	if l.Enabled(layout.List) && l.ListID == "" {
		// Only a block that is switched on in this change takes a list by itself.
		id, name, ok, err := s.pickList(householdID, "")
		switch {
		case err != nil:
			return l, err
		case ok && !cur.Enabled(layout.List):
			l.ListID, l.ListName = id, name
		default:
			refuse("list", "there is no list to show")
		}
	}
	l = l.Normalize(household)
	return l, refused
}

// saveLayout validates and saves a layout change of a display.
func (s *server) saveLayout(h store.Household, displayID string, c layoutChange) error {
	cur, err := s.db.DisplayLayout(h.ID, displayID)
	if err != nil {
		return err
	}
	household, err := s.db.PersonIDs(h.ID)
	if err != nil {
		return err
	}
	l, err := s.applyLayout(h.ID, cur, c, layout.RotationDay(s.householdNow(h)), household)
	if err != nil {
		return err
	}
	return s.db.SetDisplayLayout(h.ID, displayID, l)
}

// layoutRefusal says which field the refusal is about, for the page.
func layoutRefusal(err error) (field, key string, ok bool) {
	var le *layoutError
	if !errors.As(err, &le) {
		return "", "", false
	}
	switch le.Field {
	case "list":
		return "list", "displays.error.layout_list", true
	case "blocks":
		return "blocks", "displays.error.layout_blocks", true
	}
	return "person", "displays.error.layout_people", true
}

// handleDisplayLayoutCopy gives a display the layout of another one (Q84) and
// saves at once.
func (s *server) handleDisplayLayoutCopy(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	d, ok := s.displayOf(w, r, p.household.ID)
	if !ok {
		return
	}
	from, err := s.db.Display(p.household.ID, r.PostForm.Get("copy_from"))
	if err != nil || from.ID == d.ID {
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.fail(w, "copy layout", err)
			return
		}
		s.renderDisplay(w, r, p, http.StatusUnprocessableEntity, d, r.PostForm, "layout.copy.error", "copy_from")
		return
	}
	if err := s.db.CopyDisplayLayout(p.household.ID, from.ID, d.ID); err != nil {
		s.fail(w, "copy layout", err)
		return
	}
	s.redirectSavedNamed(w, r, "/displays/"+d.ID, "flash.layout_copied", from.Name)
}

// ---- the page ----

// layoutPage is the Layout section of a display's settings page (ticket 180).
type layoutPage struct {
	Name           string
	Portrait       bool
	Dirty          bool // the form shows an unsaved change
	OrderDay       int  // the rotation day of the order the form shows
	WasRotating    bool // the rotation runs in the saved layout
	Whole, Rotate  bool
	Chosen         []layoutPerson // the rows of the form; today's columns, unless a rotation is switched on now
	Off            []personChip   // people not on this display
	NoPeople       bool
	CanRotate      bool
	Personal       bool
	Words          string
	Zones          []layoutZone
	Lists          []layoutListOption
	ListGone       string // the name of the deleted list, "" when it is there
	NoList         bool
	ListName       string
	Mini           layoutMini
	Copy           []layoutCopyOption
	RotateTomorrow string
	// Texts for the script, which words the preview as the page does.
	WordsData layoutWordsData
}

type layoutPerson struct {
	personChip
	Pos      string // "Column 3", "Column 5, by swiping"
	Up, Down string // aria labels
	First    bool
	Last     bool
}

type layoutZone struct {
	ID     string // header, columns, footer
	Class  string // z-head, z-cols, z-foot
	Title  string
	Always string
	Off    bool // does not apply to a personal view
	Rows   []layoutBlockRow
}

type layoutBlockRow struct {
	ID                      string
	Title                   string
	On, Disabled            bool
	OnText, OffText         string
	UpLabel, DownLabel      string
	First, Last             bool
	Sub                     string // "tomorrow" or "list": the sub-setting that follows
	SubTitle, SubOn, SubOff string
	SubChecked              bool
	Sos                     *layoutSos // the words under the row of the emergency numbers
}

type layoutListOption struct {
	ID, Name string
	Selected bool
}

type layoutCopyOption struct{ ID, Name string }

type layoutWordsData struct {
	Order, OrderToday, FitLandscape, FitPortrait, Swipe, None, One, And string
	Cols, Pos, PosSwipe                                                 string
	Name                                                                string
	TomorrowOn, Up, Down                                                string
}

// layoutMini is the miniature of the display (the figure of the page).
type layoutMini struct {
	Weekday, Date, Clock, WeatherText, WeatherIcon string
	HasLists                                       bool
	Strip                                          string
	Columns                                        []miniColumn
	Visible                                        int
	List                                           string
	Sos                                            []int // one entry per contact of the emergency numbers
	HeaderOrder, ColumnOrder, FooterOrder          []string
	TomorrowLabel, MealsLabel                      string
	Zones                                          map[string]bool
}

type miniColumn struct {
	personChip
	Routines []int // one entry per routine outline to draw
	Order    int
	Percent  int
	Hidden   bool
}

func (s *server) fillLayout(r *http.Request, p principal, v *displayPage, d store.Display, in url.Values, errField string) {
	lang := s.langOf(r)
	say := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	h := p.household
	people, _ := s.db.People(h.ID)
	chips := chipsOf(people)
	household := make([]string, len(chips))
	byID := map[string]personChip{}
	for i, c := range chips {
		household[i], byID[c.ID] = c.ID, c
	}
	today := layout.RotationDay(s.householdNow(h))
	cur, err := s.db.DisplayLayout(h.ID, d.ID)
	if err != nil {
		cur = layout.Default(household)
	}
	// l is the layout with the people in their base order; the page shows today's.
	l := cur.Normalize(household)
	pg := layoutPage{Name: d.Name, Portrait: d.Portrait, OrderDay: today, WasRotating: l.Rotate}
	if in != nil && in.Has("layout_form") {
		pg.Dirty = true
		l, _ = s.applyLayout(h.ID, l, layoutFromForm(in), today, household)
		if len(in["person"]) == 0 && in.Get("whole") == "" { // what was refused stays in view: nobody chosen
			l.People, l.Whole = nil, false
		}
	}
	// Tomorrow's order in words, also while the switch is off: the rotation
	// starts from the order as it is.
	tomorrow := l
	tomorrow.Rotate = true
	tomorrowOrder := tomorrow.Order(today + 1)
	// The rows list the people in the order they are saved from. A rotation that
	// is switched on now takes them as its base order, so today's columns (the
	// preview) are moved by today mod n while the rows stay as they are.
	rows := l.People
	if pg.WasRotating || !l.Rotate {
		rows = l.Order(today)
	}
	l.People = l.Order(today)
	pg.Whole, pg.Rotate = l.Whole, l.Rotate
	pg.NoPeople = len(l.People) == 0
	_, pg.Personal = l.Personal()
	pg.CanRotate = len(l.People) >= 2
	visible := layout.Visible(d.Portrait)
	for i, id := range rows {
		c := byID[id]
		pos := say("layout.col", "n", strconv.Itoa(i+1))
		if i >= visible {
			pos = say("layout.col_swipe", "n", strconv.Itoa(i+1))
		}
		pg.Chosen = append(pg.Chosen, layoutPerson{personChip: c, Pos: pos,
			Up: say("layout.up", "name", c.Name), Down: say("layout.down", "name", c.Name), First: i == 0, Last: i == len(rows)-1})
	}
	for _, c := range chips {
		if !slices.Contains(l.People, c.ID) {
			pg.Off = append(pg.Off, c)
		}
	}
	{
		names := make([]string, 0, len(tomorrowOrder))
		for _, id := range tomorrowOrder {
			names = append(names, byID[id].Name)
		}
		pg.RotateTomorrow = say("layout.rotate.on", "names", strings.Join(names, ", "))
	}

	// Lists.
	lists, _ := s.db.Lists(h.ID)
	pg.NoList = len(lists) == 0
	for _, li := range lists {
		pg.Lists = append(pg.Lists, layoutListOption{ID: li.ID, Name: li.Name, Selected: li.ID == l.ListID})
		if li.ID == l.ListID {
			pg.ListName = li.Name
		}
	}
	if l.ListID != "" && pg.ListName == "" && l.ListName != "" {
		pg.ListGone = l.ListName
	}
	if pg.ListName == "" && len(lists) > 0 && pg.ListGone == "" {
		pg.ListName = lists[0].Name
		pg.Lists[0].Selected = true
	}

	// Zones and blocks.
	home := h.HomeName
	for _, z := range layout.Zones() {
		zone := layoutZone{ID: string(z), Title: say("layout.zone." + string(z)), Off: pg.Personal && z != layout.Header}
		zone.Class = map[layout.Zone]string{layout.Header: "z-head", layout.Columns: "z-cols", layout.Footer: "z-foot"}[z]
		if z != layout.Footer {
			zone.Always = say("layout.always." + string(z))
		}
		blocks := l.In(z)
		for i, b := range blocks {
			row := layoutBlockRow{ID: string(b.ID), Title: say("layout.b." + string(b.ID)), On: b.On,
				OnText: say("layout.b." + string(b.ID) + ".on"), OffText: say("layout.b." + string(b.ID) + ".off"),
				UpLabel: say("layout.up", "name", say("layout.b."+string(b.ID))), DownLabel: say("layout.down", "name", say("layout.b."+string(b.ID))),
				First: i == 0, Last: i == len(blocks)-1}
			switch b.ID {
			case layout.Weather:
				if home != "" {
					row.OnText = say("layout.b.weather.on", "home", home)
				} else {
					row.OnText = say("layout.b.weather.on_nohome")
				}
			case layout.Events:
				row.Sub, row.SubTitle = "tomorrow", say("layout.b.tomorrow_events")
				row.SubOn, row.SubOff = say("layout.b.tomorrow_events.on"), say("layout.b.tomorrow_events.off")
				row.SubChecked = v.ShowTomorrow
			case layout.Emergency:
				row.Sub = "emergency"
				sos, err := s.layoutSosFor(h, lang, d.Name, d.Portrait)
				if err != nil {
					s.logger.Error("emergency numbers", "err", err)
				} else {
					row.Sos = &sos
				}
			case layout.List:
				row.Sub = "list"
				row.Disabled = pg.NoList
				if pg.NoList {
					row.On = false
				}
				if pg.ListName != "" {
					row.OnText = say("layout.b.list.on", "list", pg.ListName)
				} else {
					row.OnText = say("layout.b.list.on_none")
				}
			}
			zone.Rows = append(zone.Rows, row)
		}
		pg.Zones = append(pg.Zones, zone)
	}

	// Copy: the other displays.
	others, _ := s.db.Displays(h.ID)
	for _, o := range others {
		if o.ID != d.ID {
			pg.Copy = append(pg.Copy, layoutCopyOption{ID: o.ID, Name: o.Name})
		}
	}

	s.fillMini(r, p, &pg, l, chips, byID, visible, lang)

	// Words under the preview.
	pg.WordsData = layoutWordsData{
		Order: say("layout.words.order", "names", "{names}"), OrderToday: say("layout.words.order_today", "names", "{names}"),
		FitLandscape: say("layout.words.fit.landscape", "n", "{n}"), FitPortrait: say("layout.words.fit.portrait", "n", "{n}"),
		Swipe: say("layout.words.swipe", "names", "{names}"), None: say("layout.words.none"),
		One: say("layout.words.one", "person", "{person}", "name", "{name}"), And: say("layout.words.and"),
		Cols: say("layout.col", "n", "{n}"), Pos: say("layout.col", "n", "{n}"), PosSwipe: say("layout.col_swipe", "n", "{n}"),
		Name: d.Name, TomorrowOn: say("layout.rotate.on", "names", "{names}"),
		Up: say("layout.up", "name", "{name}"), Down: say("layout.down", "name", "{name}"),
	}
	names := make([]string, len(l.People))
	for i, id := range l.People {
		names[i] = byID[id].Name
	}
	pg.Words = layoutWords(pg.WordsData, names, d.Portrait, l.Rotate, pg.Personal)
	v.Layout = pg
}

// layoutWords words the order of the columns under the preview. The script
// builds the same sentence from the same texts when the form changes.
func layoutWords(t layoutWordsData, names []string, portrait, rotate, personal bool) string {
	if len(names) == 0 {
		return t.None
	}
	if personal {
		return replaceAll(t.One, map[string]string{"{person}": names[0], "{name}": t.Name})
	}
	order := t.Order
	if rotate {
		order = t.OrderToday
	}
	out := replaceAll(order, map[string]string{"{names}": strings.Join(names, ", ")})
	visible := layout.Visible(portrait)
	if len(names) > visible {
		fit := t.FitLandscape
		if portrait {
			fit = t.FitPortrait
		}
		out += " " + replaceAll(fit, map[string]string{"{n}": strconv.Itoa(visible)})
		out += " " + replaceAll(t.Swipe, map[string]string{"{names}": joinWithAnd(names[visible:], t.And)})
	}
	return out
}

func replaceAll(s string, kv map[string]string) string {
	for k, v := range kv {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

// joinWithAnd is "A, B and C".
func joinWithAnd(names []string, and string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + and + names[len(names)-1]
}

// fillMini builds the small preview from the real household: its people,
// their progress today and the first line of the family strip.
func (s *server) fillMini(r *http.Request, p principal, pg *layoutPage, l layout.Layout, chips []personChip, byID map[string]personChip, visible int, lang string) {
	h := p.household
	now := s.householdNow(h)
	m := layoutMini{Visible: min(max(len(l.People), 1), visible), HasLists: !pg.NoList}
	_, week := now.ISOWeek()
	m.Weekday = fmt.Sprintf("%s · %s %d", s.i18n.T(lang, fmt.Sprintf("weekday.%d", int(now.Weekday()))), s.i18n.T(lang, "family.cw"), week)
	m.Date = fmt.Sprintf("%d%s %s", now.Day(), s.i18n.T(lang, "family.day_suffix"), s.i18n.T(lang, fmt.Sprintf("month.%d", int(now.Month()))))
	m.Clock = datefmt.Clock(h.TimeFormat, now)
	m.WeatherText, m.WeatherIcon = "–", "partly-cloudy"
	if w := s.homeWeather(h); w != nil {
		if chip := familyview.Chip(w, h.TemperatureUnit); chip != nil {
			m.WeatherText, m.WeatherIcon = strings.Replace(chip.Text, "°", " °", 1), chip.Icon
		}
	}
	// The real day: progress per person, routines and the first strip line.
	if v, err := s.familyView(h, lang, nil); err == nil {
		if len(v.Strip) > 0 {
			m.Strip = v.Strip[0].Title
			if v.Strip[0].Time != "" {
				m.Strip += " " + startOf(v.Strip[0].Time)
			}
		}
	}
	progress, routines := s.miniFacts(h, now)
	pos := map[string]int{}
	for i, id := range l.People {
		pos[id] = i
	}
	cols := make([]miniColumn, 0, len(chips))
	for _, c := range chips {
		mc := miniColumn{personChip: c, Percent: progress[c.ID], Routines: make([]int, min(routines[c.ID], 2))}
		if i, ok := pos[c.ID]; ok {
			mc.Order = i
		} else {
			mc.Hidden, mc.Order = true, len(chips)+len(cols)
		}
		cols = append(cols, mc)
	}
	slices.SortStableFunc(cols, func(a, b miniColumn) int { return a.Order - b.Order })
	m.Columns = cols
	for _, z := range pg.Zones {
		for _, b := range z.Rows {
			if b.Sos != nil {
				m.Sos = make([]int, b.Sos.Count)
			}
			switch z.ID {
			case "header":
				m.HeaderOrder = append(m.HeaderOrder, b.ID)
			case "columns":
				m.ColumnOrder = append(m.ColumnOrder, b.ID)
			default:
				m.FooterOrder = append(m.FooterOrder, b.ID)
			}
		}
	}
	m.List = pg.ListName
	m.TomorrowLabel = s.i18n.T(lang, "family.tomorrow")
	m.MealsLabel = s.i18n.T(lang, "layout.b.meals")
	pg.Mini = m
}

// miniFacts are today's task progress in percent and the number of routines
// with a step due today, by person.
func (s *server) miniFacts(h store.Household, now time.Time) (progress, routines map[string]int) {
	progress, routines = map[string]int{}, map[string]int{}
	due, err := s.db.TasksForDay(h.ID, now.Format("2006-01-02"))
	if err != nil {
		return
	}
	done, total := map[string]int{}, map[string]int{}
	for _, t := range due {
		total[t.PersonID]++
		if t.Done {
			done[t.PersonID]++
		}
	}
	for id, n := range total {
		progress[id] = done[id] * 100 / n
	}
	if lines, err := s.routineLines(h, due); err == nil {
		for id, ls := range lines {
			routines[id] = len(ls)
		}
	}
	return
}
