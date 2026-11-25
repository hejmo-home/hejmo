package server

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/nameday"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The calendar style and the Serbian Slava on the Birthdays calendar page (ticket 338).

// ndMismatchOrder is the order of the notices "your church follows the other calendar".
var ndMismatchOrder = []nameday.List{nameday.RussianOrthodox, nameday.BulgarianOrthodox, nameday.GreekOrthodox, nameday.RomanianOrthodox}

// sentinel marks a word of a text that the template writes in bold: the text is split at it.
const sentinel = "\x00"

// splitBold splits a translated text with one marked word into the text before it, the word and the text after it.
func splitBold(text string) (pre, word, post string) {
	parts := strings.SplitN(text, sentinel, 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

// styleData puts the calendar style ("Mehr Optionen") into the page: the value, its words, whether the details are
// open and the notices for a church that follows the other calendar.
func (e ctEnv) styleData(data map[string]any, pg ndPage) {
	style := nameday.ParseStyle(string(pg.Style))
	data["Style"], data["StyleSum"] = string(style), e.t("nd.style.sum."+string(style))
	open := style == nameday.Julian
	if want, ok := nameday.List(pg.List).Calendar(); ok && want != style {
		open = true // the church follows the other calendar: the notice and the way to change it stay in sight
	}
	data["StyleOpen"] = open
	type notice struct{ List, Pre, Bold, Post string }
	var notices []notice
	for _, l := range ndMismatchOrder {
		want, _ := l.Calendar()
		pre, word, post := splitBold(e.t("nd.mismatch."+string(l), "calendar", sentinel+e.t("nd.style.word.bold."+string(want))+sentinel))
		notices = append(notices, notice{string(l), pre, word, post})
	}
	data["Mismatches"] = notices
	data["StyleHintBefore"], data["StyleHintLink"], data["StyleHintAfter"] = e.t("nd.style.hint.before"), e.t("nd.style.hint.link"), e.t("nd.style.hint.after")
}

// slavaDate writes the day of a Slava in words: "19. Dezember".
func (e ctEnv) slavaDate(d time.Time) string {
	return e.t("slava.date", "day", strconv.Itoa(d.Day()), "month", e.month(int(d.Month())))
}

// slavaOccurrence is the day of the household's Slava in a year; own is true for an own Slava.
func slavaOccurrence(en nameday.Entry, year int) (o nameday.Occurrence, own bool) {
	if en.ID == nameday.OwnID {
		return en.On(year), true
	}
	return en.SlavaOn(year), false
}

// slavaGloss is the word that says what an Easter feast is in the reader's language.
func (e ctEnv) slavaLabel(en nameday.Entry) string {
	if en.Easter == nil {
		return en.Name
	}
	return en.Name + " (" + e.t("slava.gloss."+en.ID) + ")"
}

// slavaNow is the row "Slava" of the facts: off, or the saint and the day.
func (e ctEnv) slavaNow(h store.Household) string {
	en, ok := h.SlavaEntry()
	if !h.SlavaOn || !ok {
		return e.t("slava.now.off")
	}
	if en.Easter != nil {
		return e.t("slava.now.on.easter", "name", en.Name)
	}
	o, _ := slavaOccurrence(en, e.s.householdNow(h).Year())
	return e.t("slava.now.on", "name", en.Name, "date", e.slavaDate(o.Date))
}

type slavaOpt struct {
	Value, Text string
	Selected    bool
}

type slavaGroup struct {
	Label string
	Opts  []slavaOpt
}

// slavaData puts the card "Slava" into the page: the switch, the select with the saints by month, the own Slava, what
// was typed and the errors at the fields.
func (e ctEnv) slavaData(data map[string]any, pg ndPage) {
	h := e.p.household
	year := e.s.householdNow(h).Year()
	list := nameday.Slavas()
	type dated struct {
		en nameday.Entry
		d  time.Time
	}
	var fixed, easter []dated
	for _, en := range list.Entries {
		o := en.SlavaOn(year)
		if en.Easter != nil {
			easter = append(easter, dated{en, o.Date})
		} else {
			fixed = append(fixed, dated{en, o.Date})
		}
	}
	sort.SliceStable(fixed, func(i, j int) bool { return fixed[i].d.YearDay() < fixed[j].d.YearDay() })
	var groups []slavaGroup
	for _, x := range fixed {
		if n := len(groups); n == 0 || groups[n-1].Label != e.month(int(x.d.Month())) {
			groups = append(groups, slavaGroup{Label: e.month(int(x.d.Month()))})
		}
		g := &groups[len(groups)-1]
		g.Opts = append(g.Opts, slavaOpt{Value: x.en.ID, Selected: pg.Slava == x.en.ID,
			Text: e.t("slava.opt", "day", strconv.Itoa(x.d.Day()), "month", e.month(int(x.d.Month())), "name", x.en.Name)})
	}
	eg := slavaGroup{Label: e.t("slava.group.easter")}
	for _, x := range easter {
		eg.Opts = append(eg.Opts, slavaOpt{Value: x.en.ID, Text: e.slavaLabel(x.en), Selected: pg.Slava == x.en.ID})
	}
	groups = append(groups, eg)
	data["SlavaOn"], data["SlavaGroups"] = pg.SlavaOn, groups
	data["SlavaNone"], data["SlavaOwn"] = pg.Slava == "", pg.Slava == nameday.OwnID
	data["SlavaName"], data["SlavaDay"], data["SlavaMonths"] = pg.SlavaName, pg.SlavaDay, e.months(pg.SlavaMonth)
	data["SlavaErr"], data["SlavaNameErr"], data["SlavaDayErr"] = pg.SlavaErr, pg.NameErr, pg.DayErr
	// The saved choice in words.
	if en, ok := h.SlavaEntry(); ok {
		var text string
		if en.Easter != nil {
			text = e.t("slava.now.after.easter", "saint", e.slavaSaint(en))
		} else {
			o, _ := slavaOccurrence(en, year)
			text = e.t("slava.now.after", "saint", e.slavaSaint(en), "date", e.slavaDate(o.Date))
		}
		data["SlavaNowName"], data["SlavaNowAfter"] = en.Name, text
	}
}

// slavaSaint writes the saint of a Slava: "Sveti Nikola (Nikolaus von Myra)"; an own Slava is its name.
func (e ctEnv) slavaSaint(en nameday.Entry) string {
	if en.ID == nameday.OwnID {
		return en.Name
	}
	return e.t("slava.ev.saint", "saint", en.Saint, "german", en.SaintDE)
}

// ownSlavaDay reads the typed day and month of an own Slava as "MM-DD"; when it cannot be one, the error text.
func (e ctEnv) ownSlavaDay(day, month string) (string, string) {
	d, err1 := strconv.Atoi(day)
	m, err2 := strconv.Atoi(month)
	if err1 != nil || err2 != nil || m < 1 || m > 12 || d < 1 || d > 31 {
		return "", e.t("slava.err.day.empty")
	}
	v, ok := nameday.OwnDay(m, d)
	if !ok {
		return "", e.t("slava.err.day", "day", strconv.Itoa(d), "month", e.month(m))
	}
	return v, ""
}

// birthdaysSavedText is the success line after saving the Birthdays calendar page, built in the language of the
// reader from what was saved: the church, the style and the Slava in words, the count of people and contacts.
func (s *server) birthdaysSavedText(lang string, v map[string]string) string {
	e := ctEnv{s: s, lang: lang}
	var parts []string
	if v["church"] != "" {
		church := e.t("nd.list." + v["church"] + ".nom")
		if v["style"] != "" {
			parts = append(parts, e.t("flash.bd.nd.style", "church", church, "style", e.t("nd.style.word."+v["style"])))
		} else {
			parts = append(parts, e.t("flash.bd.nd", "church", church))
		}
	}
	if v["slava"] != "" {
		var en nameday.Entry
		if v["slava"] == nameday.OwnID {
			en = nameday.OwnEntry(v["slava_name"], v["slava_day"])
		} else {
			en, _ = nameday.Slavas().Entry(v["slava"])
		}
		if en.Easter != nil {
			parts = append(parts, e.t("flash.bd.slava.easter", "name", en.Name))
		} else if o, _ := slavaOccurrence(en, s.now().Year()); !o.Date.IsZero() {
			parts = append(parts, e.t("flash.bd.slava", "name", en.Name, "date", e.slavaDate(o.Date)))
		}
	}
	what := strings.Join(parts, "")
	if len(parts) == 2 {
		what = e.t("flash.bd.and", "a", parts[0], "b", parts[1])
	}
	count := ""
	if v["church"] != "" {
		count = e.t("flash.bd.count", "people", v["people"], "total", v["total"], "others", v["others"])
	}
	if v["dropped"] != "" {
		count += e.t("flash.bd.dropped", "dropped", v["dropped"])
	}
	return e.t("flash.bd_saved", "what", what, "count", count)
}

// sourceName names a source of a name-day list in the reader's language: the i18n text of its key, else the name
// recorded in the data file.
func (e ctEnv) sourceName(cal *nameday.Calendar, key string) string {
	if txt := e.t("nd.src." + key); txt != "nd.src."+key {
		return txt
	}
	return cal.Sources[key]
}

// sourceOf names the source of one name-day entry in the reader's language.
func (e ctEnv) sourceOf(cal *nameday.Calendar, en nameday.Entry) string {
	return e.sourceName(cal, en.Source)
}

// slavaEventPage describes the Slava event of the household; false when the Slava is gone, so the page falls back to
// the plain read-only page.
func (s *server) slavaEventPage(r *http.Request, p principal, calendarName string, occ calendar.Occurrence) (ndEventPage, bool) {
	en, ok := p.household.SlavaEntry()
	if !ok || !p.household.SlavaOn {
		return ndEventPage{}, false
	}
	e := ctEnv{s: s, lang: s.langOf(r), p: p}
	pg := ndEventPage{Title: occ.Title, Calendar: calendarName, Saint: e.slavaSaint(en), Slava: true, Explain: e.t("slava.ev.explain"), Open: e.t("slava.ev.open"),
		Lead: e.t("nd.ev.lead", "weekday", e.t("weekday."+strconv.Itoa(int(occ.Start.Weekday()))), "date", datefmt.Date(p.household.DateFormat, occ.Start))}
	switch {
	case en.ID == nameday.OwnID:
		pg.Repeat = e.t("slava.ev.repeat.own", "date", e.slavaDate(occ.Start))
		pg.Source = e.t("slava.ev.source.own")
	case en.Easter != nil:
		pg.Repeat = e.ndEaster(en)
	default:
		o := occurrenceOf(en, occ.Start, nameday.Julian)
		pg.Repeat = e.t("slava.ev.repeat", "date", e.slavaDate(o.Date), "church", e.slavaDate(o.Church))
	}
	if en.ID != nameday.OwnID {
		pg.Source = e.t("slava.source")
		pg.Unchecked = en.Unchecked
	}
	return pg, true
}
