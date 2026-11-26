package server

import (
	"errors"
	"html"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/region"
	rh "github.com/hejmo-home/hejmo/internal/religiousholiday"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The page "Religious holidays" under Settings (ticket 339, preview-339, ADR 0015 parity with the REST
// settings) and the generated calendar it fills.

const religiousPath = "/settings/religious-holidays"

// rhMonths is how far the page lists days: the next twelve months. The calendar itself reaches further.
const rhMonths = 12

// religionText is the locale text of a religion: its label, its name in a sentence or its holidays.
func (s *server) religionText(lang string, r rh.Religion, part string) string {
	key := "rh.rel." + string(r)
	if part != "" {
		key += "." + part
	}
	return s.i18n.T(lang, key)
}

// religionNames names religions in a sentence, "A, B und C", sorted by name.
func (s *server) religionNames(lang string, rs []rh.Religion) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = s.religionText(lang, r, "short")
	}
	sortNames(lang, names)
	return joinAnd(names, s.i18n.T(lang, "rh.and"))
}

// religiousHubState is the state in words of the hub row: off, or on with every chosen religion by name.
func (s *server) religiousHubState(lang string, h store.Household) string {
	if h.ReligiousHolidaysOn && len(h.Religions) > 0 {
		return s.i18n.Format(lang, i18n.Msg("rh.state.on", "names", s.religionNames(lang, h.Religions)))
	}
	return s.i18n.T(lang, "rh.state.off")
}

// religiousTitle titles the events of the calendar in the language of the parent who saved.
func (s *server) religiousTitle(lang, religion, key string, eve bool) string {
	name := s.i18n.T(lang, "rh.h."+religion+"."+key)
	if eve {
		return s.i18n.Format(lang, i18n.Msg("rh.eve", "name", name))
	}
	return name
}

// rhForm is what the form shows: the ticks of a request or the stored choice.
type rhForm struct {
	On       bool
	Religion map[rh.Religion]bool
	Islamic  rh.IslamicCalendar
	Orthodox rh.OrthodoxCalendar
}

func storedRhForm(h store.Household) rhForm {
	f := rhForm{On: h.ReligiousHolidaysOn, Religion: map[rh.Religion]bool{}, Islamic: h.IslamicCalendar, Orthodox: h.OrthodoxCalendar}
	for _, r := range h.Religions {
		f.Religion[r] = true
	}
	return f
}

func (f rhForm) religions() []rh.Religion {
	var out []rh.Religion
	for _, r := range rh.Religions() {
		if f.Religion[r] {
			out = append(out, r)
		}
	}
	return out
}

func readRhForm(r *http.Request, h store.Household) rhForm {
	_ = r.ParseForm()
	f := rhForm{On: r.PostFormValue("religious_holidays") != "", Religion: map[rh.Religion]bool{}, Islamic: h.IslamicCalendar, Orthodox: h.OrthodoxCalendar}
	for _, v := range r.PostForm["religions"] {
		f.Religion[rh.Religion(v)] = true
	}
	if v := rh.IslamicCalendar(r.PostFormValue("islamic_calendar")); v != "" {
		f.Islamic = v
	}
	if v := rh.OrthodoxCalendar(r.PostFormValue("orthodox_calendar")); v != "" {
		f.Orthodox = v
	}
	return f
}

type rhCard struct {
	Code, Name, Hint string
	Checked          bool
	Sources          []rhSourceOf // the lines of the source (ticket 377): one, or one for each Islamic calendar
}

type rhDay struct {
	Date, Name, Detail string
	Vary               bool
}

type rhRel struct {
	Name string
	Note template.HTML
	End  string
	Days []rhDay
	Dup  template.HTML
}

type rhError struct {
	Code string // none, hindu or invalid
	HTML template.HTML
}

func (s *server) rhCards(lang string, f rhForm) []rhCard {
	var cards []rhCard
	for _, r := range rh.Religions() {
		cards = append(cards, rhCard{Code: string(r), Name: s.religionText(lang, r, ""), Hint: s.religionText(lang, r, "hint"), Checked: f.Religion[r], Sources: s.rhSourcesOf(lang, r)})
	}
	names := make([]string, len(cards))
	for i, c := range cards {
		names[i] = c.Name
	}
	sortNames(lang, names)
	slices.SortFunc(cards, func(a, b rhCard) int { return slices.Index(names, a.Name) - slices.Index(names, b.Name) })
	return cards
}

// rhRange is a date or a range of days in words: "29. November 2026", "9. – 11. März 2027".
func (s *server) rhRange(lang string, from, to time.Time) string {
	msg := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	month := func(t time.Time) string { return s.i18n.T(lang, "month."+strconv.Itoa(int(t.Month()))) }
	day := func(t time.Time) string { return strconv.Itoa(t.Day()) }
	switch {
	case from.Equal(to):
		return msg("rh.d.one", "day", day(from), "month", month(from), "year", strconv.Itoa(from.Year()))
	case from.Year() != to.Year():
		return msg("rh.d.range.years", "day", day(from), "month", month(from), "year", strconv.Itoa(from.Year()), "endday", day(to), "endmonth", month(to), "endyear", strconv.Itoa(to.Year()))
	case from.Month() != to.Month():
		return msg("rh.d.range.months", "day", day(from), "month", month(from), "endday", day(to), "endmonth", month(to), "year", strconv.Itoa(from.Year()))
	}
	return msg("rh.d.range", "day", day(from), "endday", day(to), "month", month(from), "year", strconv.Itoa(from.Year()))
}

// ownDate is a date in the calendar of the religion itself: "15. bis 22. Nisan 5787".
func (s *server) ownDate(lang string, c rh.CalDate) string {
	if c.Cal == "" {
		return ""
	}
	msg := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	month := func(m int) string { return s.i18n.T(lang, "rh.month."+c.Cal+"."+strconv.Itoa(m)) }
	n := strconv.Itoa
	switch {
	case c.Day == c.EndDay && c.Month == c.EndMonth && c.Year == c.EndYear:
		return msg("rh.own.one", "day", n(c.Day), "month", month(c.Month), "year", n(c.Year))
	case c.Year != c.EndYear:
		return msg("rh.own.range.years", "day", n(c.Day), "month", month(c.Month), "year", n(c.Year), "endday", n(c.EndDay), "endmonth", month(c.EndMonth), "endyear", n(c.EndYear))
	case c.Month != c.EndMonth:
		return msg("rh.own.range.months", "day", n(c.Day), "month", month(c.Month), "endday", n(c.EndDay), "endmonth", month(c.EndMonth), "year", n(c.Year))
	}
	return msg("rh.own.range", "day", n(c.Day), "endday", n(c.EndDay), "month", month(c.Month), "year", n(c.Year))
}

// holidayName is the name of a holiday in the language.
func (s *server) holidayName(lang string, h rh.Holiday) string {
	return s.i18n.T(lang, "rh.h."+string(h.Religion)+"."+h.Key)
}

// holidayDetail is the small line under a name in the list: the own date, or a fixed word.
func (s *server) holidayDetail(lang string, h rh.Holiday) string {
	if h.ShowOwn {
		return s.ownDate(lang, h.Own)
	}
	key := "rh.h." + string(h.Religion) + "." + h.Key + ".detail"
	if t := s.i18n.T(lang, key); t != key {
		return t
	}
	return ""
}

func (s *server) rhSourceName(lang, id string) string {
	args := []string{}
	for _, t := range rh.Tables() {
		if t.ID == id {
			args = []string{"from", strconv.Itoa(t.From), "to", strconv.Itoa(t.To)}
		}
	}
	return s.i18n.Format(lang, i18n.Msg("rh.source."+id, args...))
}

// rhRels builds the cards under "What is in the calendar" for the chosen religions.
func (s *server) rhRels(lang string, h store.Household, today time.Time) []rhRel {
	opts := h.ReligiousOptions()
	horizon := today.AddDate(0, rhMonths, 0)
	calEnd := today.Year() + 9
	var out []rhRel
	regionName := ""
	if reg, ok := region.Lookup(h.Region); ok {
		regionName = reg.Name
	}
	religions := s.sortReligions(lang, h.Religions)
	for _, plan := range rh.PlanFor(religions, opts, h.Region, today.Year()-1, today.Year()+1) {
		rel := rhRel{Name: s.religionText(lang, plan.Religion, "")}
		for _, d := range s.mergeRhDays(lang, plan.Kept, today, horizon) {
			rel.Days = append(rel.Days, d)
		}
		rel.Note = s.rhNote(lang, h, plan.Religion)
		if plan.TableEnd > 0 && plan.TableEnd < calEnd {
			key := "rh.end"
			for _, k := range plan.Kept {
				if k.Unchecked {
					key = "rh.end.unchecked"
				}
			}
			rel.End = s.i18n.Format(lang, i18n.Msg(key, "year", strconv.Itoa(plan.TableEnd)))
		}
		rel.Dup = s.rhDup(lang, regionName, plan.Skipped, today, horizon)
		out = append(out, rel)
	}
	return out
}

// mergeRhDays lists the holidays from today to the horizon as rows; holidays of one religion on the same days are one row.
func (s *server) mergeRhDays(lang string, hs []rh.Holiday, today, horizon time.Time) []rhDay {
	var out []rhDay
	var last *rh.Holiday
	for i := range hs {
		h := hs[i]
		if h.End.Before(today) || !h.Start.Before(horizon) {
			continue
		}
		if last != nil && last.Start.Equal(h.Start) && last.End.Equal(h.End) {
			row := &out[len(out)-1]
			row.Name += " " + s.i18n.T(lang, "rh.and") + " " + s.holidayName(lang, h)
			row.Vary = row.Vary || h.Vary
			continue
		}
		out = append(out, rhDay{Date: s.rhRange(lang, h.Start, h.End), Name: s.holidayName(lang, h), Detail: s.holidayDetail(lang, h), Vary: h.Vary})
		last = &hs[i]
	}
	return out
}

// rhDup names the days left out because they are public holidays in the region.
func (s *server) rhDup(lang, regionName string, skipped []rh.Skipped, today, horizon time.Time) template.HTML {
	var names []string
	seen := map[string]bool{}
	for _, k := range skipped {
		if k.End.Before(today.AddDate(-1, 0, 0)) || !k.Start.Before(horizon.AddDate(1, 0, 0)) {
			continue
		}
		name := s.holidayName(lang, k.Holiday)
		if k.Partial {
			name += " (" + s.i18n.T(lang, "rh.dup.partial") + ")"
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return rich(s.i18n.Format(lang, i18n.Msg("rh.dup", "region", regionName, "names", joinAnd(names, s.i18n.T(lang, "rh.and")))))
}

// rhNote says in words what the dates of a religion follow and where communities differ.
func (s *server) rhNote(lang string, h store.Household, r rh.Religion) template.HTML {
	key := "rh.note." + string(r)
	switch r {
	case rh.Muslim, rh.Alevi:
		key += "." + islamicKey(h)
	case rh.Orthodox:
		key += "." + string(orthodoxKey(h))
	}
	text := s.i18n.T(lang, key)
	if r == rh.Hindu {
		text = strings.ReplaceAll(text, "{place}", h.HomeName)
	}
	return rich(text)
}

// sortReligions orders religions by their names in the language.
func (s *server) sortReligions(lang string, rs []rh.Religion) []rh.Religion {
	names := map[rh.Religion]string{}
	list := make([]string, 0, len(rs))
	for _, r := range rs {
		names[r] = s.religionText(lang, r, "short")
		list = append(list, names[r])
	}
	sortNames(lang, list)
	out := slices.Clone(rs)
	slices.SortFunc(out, func(a, b rh.Religion) int { return slices.Index(list, names[a]) - slices.Index(list, names[b]) })
	return out
}

func islamicKey(h store.Household) string {
	if h.IslamicCalendar == rh.UmmAlQura {
		return string(rh.UmmAlQura)
	}
	return string(rh.Diyanet)
}

func orthodoxKey(h store.Household) rh.OrthodoxCalendar {
	if h.OrthodoxCalendar == rh.Julian {
		return rh.Julian
	}
	return rh.RevisedJulian
}

// rhMoreSum is the choice shown on the closed "More options" line, for the religions chosen.
func (s *server) rhMoreSum(lang string, f rhForm) string {
	var parts []string
	if f.Religion[rh.Muslim] || f.Religion[rh.Alevi] {
		parts = append(parts, s.i18n.Format(lang, i18n.Msg("rh.more.sum.islamic", "value", s.i18n.T(lang, "rh.islamic."+islamicChoice(f)+".sum"))))
	}
	if f.Religion[rh.Orthodox] {
		parts = append(parts, s.i18n.Format(lang, i18n.Msg("rh.more.sum.orthodox", "value", s.i18n.T(lang, "rh.orthodox."+orthodoxChoice(f)+".sum"))))
	}
	return strings.Join(parts, " · ")
}

func islamicChoice(f rhForm) string {
	if f.Islamic == rh.UmmAlQura {
		return string(rh.UmmAlQura)
	}
	return string(rh.Diyanet)
}

func orthodoxChoice(f rhForm) string {
	if f.Orthodox == rh.Julian {
		return string(rh.Julian)
	}
	return string(rh.RevisedJulian)
}

func (s *server) handleReligious(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderReligious(w, r, p, http.StatusOK, storedRhForm(p.household), nil, i18n.Message{})
}

func (s *server) renderReligious(w http.ResponseWriter, r *http.Request, p principal, status int, f rhForm, bad *rhError, ok i18n.Message) {
	lang := s.langOf(r)
	h := p.household
	today := s.now().In(p.location())
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	legal := s.i18n.T(lang, "rh.legal.none")
	if reg, found := region.Lookup(h.Region); found {
		legal = s.i18n.Format(lang, i18n.Msg("rh.legal.region", "region", reg.Name))
	}
	state := s.religiousHubState(lang, h)
	if !h.ReligiousHolidaysOn && len(h.Religions) > 0 {
		state = s.i18n.Format(lang, i18n.Msg("rh.state.off.last", "names", s.religionNames(lang, h.Religions)))
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(h.ID), "Form": f, "Cards": s.rhCards(lang, f), "Legal": legal, "State": state,
		"StoredOn": h.ReligiousHolidaysOn, "Err": bad,
		"HasIslamic": f.Religion[rh.Muslim] || f.Religion[rh.Alevi], "HasOrthodox": f.Religion[rh.Orthodox],
		"MoreSum": s.rhMoreSum(lang, f), "Islamic": islamicChoice(f), "Orthodox": orthodoxChoice(f),
		"CalendarEnd": today.Year() + 9,
		"CiteDiyanet": s.rhSourceLine(lang, rh.Muslim, rh.Options{Islamic: rh.Diyanet}), "CiteUmmAlQura": s.rhSourceLine(lang, rh.Muslim, rh.Options{Islamic: rh.UmmAlQura}),
	}
	if h.ReligiousHolidaysOn && len(h.Religions) > 0 {
		data["Rels"] = s.rhRels(lang, h, today)
	}
	if ok.Key != "" {
		data["SuccessMsg"] = ok
	}
	s.render(w, r, status, "religious_holidays.html", data)
}

func (s *server) handleReligiousSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	f := readRhForm(r, p.household)
	in := store.ReligiousInput{On: f.On, Religions: f.religions(), Islamic: f.Islamic, Orthodox: f.Orthodox}
	if f.On {
		for _, v := range r.PostForm["religions"] { // an unknown religion is refused, not dropped
			if !rh.Religion(v).Valid() {
				in.Religions = append(in.Religions, rh.Religion(v))
			}
		}
	}
	if err := store.ValidateReligious(in, p.household.HasLocation()); err != nil {
		s.renderReligious(w, r, p, http.StatusUnprocessableEntity, f, s.religiousError(lang, err), i18n.Message{})
		return
	}
	before := p.household
	change, err := s.db.SaveReligiousHolidays(before.ID, in, s.i18n.T(lang, "rh.calendar_name"), lang)
	if err != nil {
		var fe *store.FieldError
		if errors.As(err, &fe) {
			s.renderReligious(w, r, p, http.StatusUnprocessableEntity, f, s.religiousError(lang, err), i18n.Message{})
			return
		}
		s.fail(w, "save religious holidays", err)
		return
	}
	saved, err := s.db.Household(before.ID)
	if err != nil {
		s.fail(w, "save religious holidays", err)
		return
	}
	p.household = saved
	s.renderReligious(w, r, p, http.StatusOK, storedRhForm(saved), nil, s.religiousSaved(lang, before, saved, change, s.now().In(p.location()).Year()))
}

// religiousSaved is the success line after a save.
func (s *server) religiousSaved(lang string, before, saved store.Household, change store.ReligiousChange, year int) i18n.Message {
	n, y := strconv.Itoa(change.Count), strconv.Itoa(year+1)
	switch {
	case !saved.ReligiousHolidaysOn:
		return i18n.Msg("rh.ok.off")
	case before.ReligiousHolidaysOn && change.Moved > 0 && before.IslamicCalendar != saved.IslamicCalendar:
		return i18n.Msg("rh.ok.moved."+islamicKey(saved), "n", strconv.Itoa(change.Moved))
	case !before.ReligiousHolidaysOn || !slices.Equal(before.Religions, saved.Religions):
		key := "rh.ok.on"
		if change.Count == 1 {
			key = "rh.ok.on.one"
		}
		return i18n.Msg(key, "names", s.religionNames(lang, saved.Religions), "n", n, "year", y)
	}
	return i18n.Msg("rh.ok.saved", "n", n, "year", y)
}

// religiousError is the error at the choice of religions.
func (s *server) religiousError(lang string, err error) *rhError {
	switch {
	case errors.Is(err, store.ErrNoReligion):
		return &rhError{Code: "none", HTML: template.HTML(html.EscapeString(s.i18n.T(lang, "rh.err.none")))} // #nosec G203 -- escaped
	case errors.Is(err, store.ErrHinduNeedsLocation):
		text := html.EscapeString(s.i18n.T(lang, "rh.err.hindu"))
		link := `<a href="/settings/household">` + html.EscapeString(s.i18n.T(lang, "rh.err.hindu.link")) + `</a>`
		return &rhError{Code: "hindu", HTML: template.HTML(strings.ReplaceAll(text, "{link}", link))} // #nosec G203 -- escaped, the link is ours
	}
	return &rhError{Code: "invalid", HTML: template.HTML(html.EscapeString(s.i18n.T(lang, "rh.err.invalid")))} // #nosec G203 -- escaped
}

// rhEventPage is the page of an event of the generated calendar (the variant .ev-gen of the event page).
type rhEventPage struct {
	Title, Lead, Moved, Explain, Calendar, Religion, OwnLabel, Own, Repeat, Source, UncheckedText string
	Unchecked                                                                                     bool
	Href                                                                                          string
}

// fixedDate says a holiday falls on the same day every year.
var rhFixed = map[string]bool{
	"christian/epiphany": true, "christian/assumption": true, "christian/reformation": true, "christian/all_saints": true, "christian/st_martin": true,
	"christian/saint_nicholas": true, "christian/christmas_eve": true, "christian/christmas_days": true, "orthodox/christmas": true, "orthodox/theophany": true,
	"alevi/hizir_fast": true, "alevi/nevruz": true, "alevi/hidrellez": true, "buddhist/bodhi_day": true, "sikh/vaisakhi": true,
}

// religiousEventPage describes an event of the religious holidays calendar; false when the holiday is not computed any more
// (the choice changed), so the page falls back to the plain read-only page.
func (s *server) religiousEventPage(r *http.Request, p principal, calendarName string, occ calendar.Occurrence) (rhEventPage, bool) {
	rel := rh.Religion(occ.Religious)
	if !rel.Valid() {
		return rhEventPage{}, false
	}
	lang := s.langOf(r)
	loc := p.location()
	start := occ.Start.In(loc)
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	target := day
	if occ.ReligiousEve {
		target = day.AddDate(0, 0, 1)
	}
	var hol rh.Holiday
	found := false
	for _, c := range rh.Compute(rel, p.household.ReligiousOptions(), target.Year()-1, target.Year()+1) {
		if c.Key == occ.ReligiousKey && c.Start.Equal(target) {
			hol, found = c, true
			break
		}
	}
	if !found {
		return rhEventPage{}, false
	}
	f := p.household.DateFormat
	long := func(t time.Time, year bool) string {
		key := "rh.d.long"
		kv := []string{"weekday", s.i18n.T(lang, "weekday."+strconv.Itoa(int(t.Weekday()))), "day", strconv.Itoa(t.Day()), "month", s.i18n.T(lang, "month."+strconv.Itoa(int(t.Month())))}
		if year {
			kv = append(kv, "year", strconv.Itoa(t.Year()))
		} else {
			key = "rh.d.long.nodyear"
		}
		return s.i18n.Format(lang, i18n.Msg(key, kv...))
	}
	// The lead: the days in the household's date format.
	wd := func(t time.Time) string { return s.i18n.T(lang, "weekday."+strconv.Itoa(int(t.Weekday()))) }
	from, to := day, day
	if !occ.ReligiousEve {
		to = hol.End
	}
	lead := wd(from) + ", " + datefmt.Date(f, from)
	if !from.Equal(to) {
		a := datefmt.Date(f, from)
		if from.Year() == to.Year() {
			a = datefmt.Short(f, from)
		}
		lead = s.i18n.Format(lang, i18n.Msg("rh.ev.date.range", "from", wd(from)+", "+a, "to", wd(to)+", "+datefmt.Date(f, to)))
	}
	pg := rhEventPage{Title: occ.Title, Calendar: calendarName, Religion: s.religionText(lang, rel, "short"), Explain: s.i18n.T(lang, "rh.ev.explain"),
		Source: s.rhSourceName(lang, hol.Source), Unchecked: hol.Unchecked, UncheckedText: s.i18n.T(lang, "rh.unchecked"), Href: religiousPath}
	if pg.Unchecked {
		pg.Source += "." // the note that it was not checked follows as its own sentence
	}
	pg.Lead = s.i18n.Format(lang, i18n.Msg("rh.ev.lead", "when", lead))
	if hol.Own.Cal != "" {
		pg.OwnLabel, pg.Own = s.i18n.T(lang, "rh.ev.own."+hol.Own.Cal), s.ownDate(lang, hol.Own)
	}
	pg.Repeat = s.i18n.T(lang, "rh.repeat."+string(rel))
	if rhFixed[string(rel)+"/"+hol.Key] {
		pg.Repeat = s.i18n.T(lang, "rh.repeat.fixed")
	}
	name := s.holidayName(lang, hol)
	switch {
	case occ.ReligiousEve:
		pg.Moved = s.i18n.Format(lang, i18n.Msg("rh.ev.eve.own", "name", name, "from", datefmt.Date(f, hol.Start), "to", datefmt.Date(f, hol.End)))
	case hol.Vary:
		alt := s.i18n.Format(lang, i18n.Msg("rh.ev.vary.one", "date", long(hol.Start.AddDate(0, 0, 1), false)))
		if hol.Days() > 1 {
			alt = s.i18n.Format(lang, i18n.Msg("rh.ev.vary.range", "from", long(hol.Start.AddDate(0, 0, 1), false), "to", long(hol.End.AddDate(0, 0, 1), false)))
		}
		pg.Moved = s.i18n.Format(lang, i18n.Msg("rh.ev.vary", "source", s.i18n.T(lang, "rh.src."+islamicKey(p.household)), "alt", alt))
	case hol.Eve:
		days := ""
		if hol.Days() > 1 {
			days = s.i18n.Format(lang, i18n.Msg("rh.ev.eve.days", "n", strconv.Itoa(hol.Days())))
		}
		if rel != rh.Jewish {
			days = ""
		}
		eve := s.religiousTitle(lang, string(rel), hol.Key, true)
		pg.Moved = s.i18n.Format(lang, i18n.Msg("rh.ev.eve", "name", name, "date", long(hol.Start.AddDate(0, 0, -1), false), "eve", eve, "days", days))
	default:
		if t := s.i18n.T(lang, "rh.moved."+string(rel)); t != "rh.moved."+string(rel) {
			pg.Moved = t
		}
	}
	return pg, true
}
