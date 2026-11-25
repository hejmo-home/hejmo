package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/nameday"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The Birthdays calendar page and the name days on it (ticket 318): the household switches name days on or off and
// chooses the church; every person of the family and every other contact shows what that makes of their name.

const birthdaysPath = "/contacts/birthdays"

// ndGroups are the groups of the church choice, with the lists of each; the groups grow with tickets 338 and 339.
var ndGroups = []struct {
	Key   string
	Lists []nameday.List
}{
	{"nd.grp.west", []nameday.List{nameday.Catholic, nameday.Protestant}},
	{"nd.grp.orthodox", []nameday.List{nameday.BulgarianOrthodox, nameday.GreekOrthodox, nameday.RomanianOrthodox, nameday.RussianOrthodox, nameday.UkrainianOrthodox}},
}

// churchAdj is the church as an adjective for a sentence: "katholischen", in "nach den katholischen Namenstagen".
func (e ctEnv) churchAdj(l string) string { return e.t("nd.list." + l + ".adj") }

// ndDay writes a day of the year as people say it: "25. November", or with the year for a day that moves.
func (e ctEnv) ndDay(d time.Time, withYear bool) string {
	if withYear {
		return e.t("nd.date.year", "day", strconv.Itoa(d.Day()), "month", e.month(int(d.Month())), "year", strconv.Itoa(d.Year()))
	}
	return e.t("nd.date", "day", strconv.Itoa(d.Day()), "month", e.month(int(d.Month())))
}

// ndEntryName is the name of an entry as the pages write it: "Aikaterini (Katharina)".
func ndEntryName(en nameday.Entry) string {
	if en.Gloss != "" {
		return en.Name + " (" + en.Gloss + ")"
	}
	return en.Name
}

// ndInfo is the name day of a contact for the household's list, with its next day.
type ndInfo struct {
	Res    nameday.Result
	Occ    nameday.Occurrence // the next occurrence from today; zero without a name day
	Today  bool
	Yearly bool
}

// nextOn is the next day of an entry from a day on, in a calendar style. A day of the Julian style that is late in
// December falls in January of the next year, so the year before is looked at too.
func nextOn(en nameday.Entry, day time.Time, st nameday.Style) nameday.Occurrence {
	for y := day.Year() - 1; ; y++ {
		if o := en.OnStyle(y, st); !o.Date.Before(day) {
			return o
		}
	}
}

// occurrenceOf is the occurrence of an entry that falls on a date (the start of an event).
func occurrenceOf(en nameday.Entry, date time.Time, st nameday.Style) nameday.Occurrence {
	for _, y := range []int{date.Year(), date.Year() - 1, date.Year() + 1} {
		if o := en.OnStyle(y, st); o.Date.Year() == date.Year() && o.Date.YearDay() == date.YearDay() {
			return o
		}
	}
	return en.OnStyle(date.Year(), st)
}

// ndOf resolves the name day of a contact and finds the next day it falls on, in the calendar style of the household.
func ndOf(c store.Contact, cal *nameday.Calendar, today time.Time, st nameday.Style) ndInfo {
	res := cal.For(c.FullName, c.HejmoName, nameday.Choice{Mode: c.NameDayMode, Entry: c.NameDayEntry, Name: c.NameDayName})
	info := ndInfo{Res: res}
	if !res.Found {
		return info
	}
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	info.Yearly = res.Entry.Yearly()
	info.Occ = nextOn(res.Entry, day, st)
	info.Today = info.Occ.Date.Equal(day)
	return info
}

// ndOrdinal says "ersten", "zweiten", ... for the part of a double name.
func (e ctEnv) ndOrdinal(n int) string {
	if n >= 1 && n <= 5 {
		return e.t("nd.ordinal." + strconv.Itoa(n))
	}
	return e.t("nd.ordinal.n", "n", strconv.Itoa(n))
}

// ndWho says who chose the name day of a contact.
func (e ctEnv) ndWho(c store.Contact) string {
	if c.NameDayByName == "" {
		return e.t("nd.row.chosen.anon")
	}
	return e.t("nd.row.chosen", "who", c.NameDayByName)
}

// ndFrom says where a name day comes from; "" when the name speaks for itself.
func (e ctEnv) ndFrom(c store.Contact, info ndInfo) string {
	switch info.Res.Via {
	case nameday.ViaPart:
		return e.t("nd.row.from.part", "ordinal", e.ndOrdinal(info.Res.Match.Part), "name", info.Res.Match.Matched)
	case nameday.ViaEntry, nameday.ViaOther:
		return e.ndWho(c)
	}
	if c.FullName != "" && !strings.EqualFold(c.FullName, c.HejmoName) {
		return e.t("nd.row.from.full", "name", c.FullName)
	}
	return ""
}

// ndEaster says what a name day depends on when it follows Orthodox Easter.
func (e ctEnv) ndEaster(en nameday.Entry) string {
	if en.Easter == nil {
		return ""
	}
	switch n := *en.Easter; {
	case n == 0:
		return e.t("nd.easter.0")
	case n == 7:
		return e.t("nd.easter.7")
	case n == -7:
		return e.t("nd.easter.m7")
	case n == 1:
		return e.t("nd.easter.after.one")
	case n > 0:
		return e.t("nd.easter.after", "n", strconv.Itoa(n))
	default:
		return e.t("nd.easter.before", "n", strconv.Itoa(-n))
	}
}

// ndRow is a person of the family on the page, with their name day or the reason there is none.
type ndRow struct {
	Href, Name, Shape   string
	Slot                int
	Person              bool
	Date, What, Depends string // the day in words, the name with where it comes from, what a moving day depends on
	Today               bool
	None                string // why there is no name day
}

// shortFormOf names the long form of a short name, for the hint "etwa „Maria“"; the list must know the long form.
var shortFormOf = map[string]string{"mia": "Maria", "lotta": "Charlotte", "lisa": "Elisabeth", "ben": "Benedikt", "basti": "Sebastian", "tom": "Thomas"}

// ndExample is a name the list knows that the unknown first name may be short for; "" without one.
func ndExample(cal *nameday.Calendar, first string) string {
	long, ok := shortFormOf[strings.ToLower(strings.TrimSpace(first))]
	if !ok || cal.Lookup(long) == nil {
		return ""
	}
	return long
}

func (e ctEnv) ndRowOf(c store.Contact, cal *nameday.Calendar, today time.Time, st nameday.Style) ndRow {
	row := ndRow{Href: contactsPath + "/" + c.ID + "/edit#nameday", Name: c.HejmoName}
	if chip, ok := e.chips[c.PersonID]; ok && c.IsPerson() {
		row.Person, row.Slot, row.Shape = true, chip.Slot, chip.Shape
	}
	info := ndOf(c, cal, today, st)
	if !info.Res.Found {
		row.None = e.ndNone(c, cal, info)
		return row
	}
	en := info.Res.Entry
	row.Date, row.What, row.Today = e.ndDay(info.Occ.Date, !info.Yearly), ndEntryName(en), info.Today
	if from := e.ndFrom(c, info); from != "" {
		row.What += ", " + from // "Katharina, nach „Katharina Maier“"
	}
	switch {
	case en.Easter != nil:
		row.Depends = e.ndEaster(en)
	case en.Move != "":
		m, d, _ := strings.Cut(en.Day, "-")
		mi, _ := strconv.Atoi(m)
		di, _ := strconv.Atoi(d)
		row.Depends = e.t("nd.row.moved", "from", e.ndDay(time.Date(2000, time.Month(mi), di, 0, 0, 0, 0, time.UTC), false))
	}
	return row
}

// ndNone says why a contact has no name day.
func (e ctEnv) ndNone(c store.Contact, cal *nameday.Calendar, info ndInfo) string {
	if info.Res.Why == nameday.WhyNone {
		return e.t("nd.none.chosen", "name", c.HejmoName)
	}
	first := nameday.FirstName(c.FullName, c.HejmoName)
	church := e.churchAdj(string(cal.List))
	if ex := ndExample(cal, first); ex != "" {
		return e.t("nd.none.unknown.example", "church", church, "name", first, "example", ex)
	}
	return e.t("nd.none.unknown", "church", church, "name", first)
}

// ndPage is what the page holds: the stored setting, or what was posted when the page is shown again after errors.
type ndPage struct {
	On                              bool
	List                            string // the church that is selected
	ListErr                         string
	Status                          int
	Style                           nameday.Style // the calendar style that is selected
	SlavaOn                         bool
	Slava                           string // the saint that is selected: an id of the list, nameday.OwnID or ""
	SlavaName, SlavaDay, SlavaMonth string // what is typed for an own Slava (day and month as numbers)
	SlavaErr, NameErr, DayErr       string
}

// ndPageOf is the page as the household stored it.
func ndPageOf(h store.Household) ndPage {
	pg := ndPage{On: h.NameDaysOn, List: h.NameDayList, Status: http.StatusOK, Style: h.OrthodoxCalendar, SlavaOn: h.SlavaOn, Slava: h.Slava, SlavaName: h.SlavaOwnName}
	if m, d, ok := store.OwnSlavaDay(h.SlavaOwnDay); ok {
		pg.SlavaDay, pg.SlavaMonth = strconv.Itoa(d), strconv.Itoa(m)
	}
	return pg
}

func (s *server) handleBirthdays(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	s.renderBirthdays(w, r, e, ndPageOf(p.household))
}

func (s *server) renderBirthdays(w http.ResponseWriter, r *http.Request, e ctEnv, pg ndPage) {
	all, err := s.db.Contacts(e.p.household.ID)
	if err != nil {
		s.fail(w, "contacts", err)
		return
	}
	bdays := 0
	for _, c := range all {
		if c.BdayDay != 0 && !c.HideBirthday {
			bdays++
		}
	}
	h := e.p.household
	data := map[string]any{"Person": e.p.person, "SyncStale": s.syncStale(h.ID), "On": pg.On, "ListErr": pg.ListErr, "Selected": pg.List}
	switch bdays {
	case 0:
		data["Birthdays"] = e.t("nd.now.birthdays.none")
	case 1:
		data["Birthdays"] = e.t("nd.now.birthdays.one")
	default:
		data["Birthdays"] = e.t("nd.now.birthdays.many", "n", strconv.Itoa(bdays))
	}
	data["NameDaysNow"] = e.t("nd.now.off")
	if h.NameDaysOn {
		data["NameDaysNow"] = e.t("nd.now.on", "church", e.churchAdj(h.NameDayList))
		if h.NameDayStyleApplies() {
			data["NameDaysNow"] = e.t("nd.now.on.style", "church", e.churchAdj(h.NameDayList), "style", e.t("nd.style.word."+string(h.NameDayStyle())))
		}
	}
	data["SlavaNow"] = e.slavaNow(h)
	type choice struct {
		Value, Name, Hint string
		Orth              bool
	}
	type group struct {
		Title string
		Lists []choice
	}
	var groups []group
	for _, g := range ndGroups {
		gr := group{Title: e.t(g.Key)}
		for _, l := range g.Lists {
			gr.Lists = append(gr.Lists, choice{string(l), e.t("nd.list." + string(l)), e.t("nd.list." + string(l) + ".hint"), l.Orthodox()})
		}
		groups = append(groups, gr)
	}
	data["Groups"] = groups
	e.styleData(data, pg)
	e.slavaData(data, pg)
	if cal, ok := h.NameDayCalendar(); ok {
		today := s.householdNow(h)
		church := e.churchAdj(h.NameDayList)
		var family []ndRow
		var others []store.Contact
		haveOthers := false
		for _, c := range all {
			if c.IsPerson() {
				family = append(family, e.ndRowOf(c, cal, today, h.NameDayStyle()))
				continue
			}
			if info := ndOf(c, cal, today, h.NameDayStyle()); info.Res.Found {
				haveOthers = true
			} else if info.Res.Why != nameday.WhyNone {
				others = append(others, c)
			}
		}
		sortFurther(e.lang, others)
		var names []map[string]string
		for _, c := range others {
			names = append(names, map[string]string{"Href": contactsPath + "/" + c.ID + "/edit#nameday", "Name": c.HejmoName})
		}
		data["Family"], data["Others"], data["HaveOthers"] = family, names, haveOthers
		data["FamHint"] = e.t("nd.fam.hint", "church", church)
		if h.NameDayStyleApplies() {
			data["FamHint"] = e.t("nd.fam.hint.style", "church", church, "style", e.t("nd.style.word."+string(h.NameDayStyle())))
		}
		data["OthersHint"] = e.t("nd.others.hint", "church", church)
	}
	s.render(w, r, pg.Status, "contact_birthdays.html", data)
}

// ndCounts says how many of the people and how many further contacts have a name day, and how many people there are.
func ndCounts(all []store.Contact, cal *nameday.Calendar) (people, total, others int) {
	for _, c := range all {
		found := cal.For(c.FullName, c.HejmoName, nameday.Choice{Mode: c.NameDayMode, Entry: c.NameDayEntry, Name: c.NameDayName}).Found
		switch {
		case c.IsPerson():
			total++
			if found {
				people++
			}
		case found:
			others++
		}
	}
	return people, total, others
}

func (s *server) handleBirthdaysSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	e, ok := s.contactEnv(w, r, p)
	if !ok {
		return
	}
	_ = r.ParseForm()
	on, list := r.PostFormValue("name_days") != "", r.PostFormValue("name_day_list")
	if !on {
		list = ""
	}
	pg := ndPageOf(p.household)
	pg.On, pg.List = on, list
	if !on { // the church stays selected while name days are off
		pg.List = p.household.NameDayList
	}
	if st := nameday.Style(r.PostFormValue("orthodox_calendar")); st.Valid() {
		pg.Style = st
	}
	pg.SlavaOn = r.PostFormValue("slava_on") != ""
	if pg.SlavaOn || r.PostFormValue("slava") != "" {
		pg.Slava = r.PostFormValue("slava")
		pg.SlavaName, pg.SlavaDay, pg.SlavaMonth = strings.TrimSpace(r.PostFormValue("slava_name")), strings.TrimSpace(r.PostFormValue("slava_day")), strings.TrimSpace(r.PostFormValue("slava_month"))
	}
	// Everything is checked before anything is saved, and every error appears at its field.
	bad := false
	if err := store.ValidateNameDays(on, list); err != nil {
		pg.ListErr, bad = e.t("nd.err.list"), true
	}
	in := store.SlavaInput{On: pg.SlavaOn, OwnName: pg.SlavaName}
	if pg.SlavaOn { // switched off, the stored choice stays as it is
		in.Choice = pg.Slava
	}
	if pg.SlavaOn && pg.Slava == nameday.OwnID {
		in.OwnDay, pg.DayErr = e.ownSlavaDay(pg.SlavaDay, pg.SlavaMonth)
	}
	if err := store.ValidateSlava(in); err != nil {
		var fe *store.FieldError
		if errors.As(err, &fe) {
			switch fe.Field {
			case "slava":
				pg.SlavaErr = e.t("slava.err.none")
			case "slava_name":
				pg.NameErr = e.t("slava.err.name")
			}
		}
		bad = true
	}
	if pg.DayErr != "" {
		bad = true
	}
	if bad {
		pg.Status = http.StatusUnprocessableEntity
		s.renderBirthdays(w, r, e, pg)
		return
	}
	lang := e.lang
	cname := s.i18n.T(lang, "birthdays.calendar_name")
	change, err := s.db.SetNameDays(p.household.ID, on, list, cname, s.i18n.T(lang, "nd.ev.word"))
	if err == nil {
		if r.PostFormValue("orthodox_calendar") != "" && pg.Style.Valid() && pg.Style != p.household.OrthodoxCalendar {
			err = s.db.SetOrthodoxCalendar(p.household.ID, pg.Style, cname)
		}
	}
	if err == nil {
		err = s.db.SetSlava(p.household.ID, in, cname)
	}
	if err != nil {
		s.fail(w, "name days", err)
		return
	}
	saved, err := s.db.Household(p.household.ID)
	if err != nil {
		s.fail(w, "name days", err)
		return
	}
	if !on && !pg.SlavaOn {
		s.redirectSaved(w, r, birthdaysPath, "flash.nd_off")
		return
	}
	church, style, people, total, others := "", "", 0, 0, 0
	if on {
		all, err := s.db.Contacts(p.household.ID)
		if err != nil {
			s.fail(w, "contacts", err)
			return
		}
		cal, _ := nameday.Get(nameday.List(list))
		people, total, others = ndCounts(all, cal)
		church = list // the page says the church in the language of whoever reads it (see birthdaysSavedText)
		if saved.NameDayStyleApplies() {
			style = string(saved.NameDayStyle())
		}
	}
	slava := ""
	if pg.SlavaOn {
		slava = saved.Slava
	}
	var names []string
	for _, d := range change.Dropped {
		names = append(names, d.Name)
	}
	s.redirectSavedFields(w, r, birthdaysPath, "flash.bd_saved", church, style, slava, saved.SlavaOwnName, saved.SlavaOwnDay,
		strconv.Itoa(people), strconv.Itoa(total), strconv.Itoa(others), strings.Join(names, ", "))
}

// ndShort is a day for a fact line in the household's date format: "25.11.", with the year for a moving day.
func (e ctEnv) ndShort(info ndInfo) string {
	if info.Yearly {
		return datefmt.Short(e.p.household.DateFormat, info.Occ.Date)
	}
	return datefmt.Date(e.p.household.DateFormat, info.Occ.Date)
}

// nameDayFact is the row "Namenstag" of the contact page; "" without a name day or while name days are off.
func (e ctEnv) nameDayFact(c store.Contact, today time.Time) string {
	cal, ok := e.p.household.NameDayCalendar()
	if !ok {
		return ""
	}
	info := ndOf(c, cal, today, e.p.household.NameDayStyle())
	if !info.Res.Found {
		return ""
	}
	name := ndEntryName(info.Res.Entry)
	switch info.Res.Via {
	case nameday.ViaEntry, nameday.ViaOther:
		key := "contact.fact.nameday.chosen.anon"
		kv := []string{"date", e.ndShort(info), "name", name}
		if c.NameDayByName != "" {
			key, kv = "contact.fact.nameday.chosen", append(kv, "who", c.NameDayByName)
		}
		return e.t(key, kv...)
	}
	return e.t("contact.fact.nameday.value", "date", e.ndShort(info), "name", name)
}

// The card "Namenstag" of the contact form (only while name days are on).

type ndChoiceCard struct {
	Value, Date, Title, Small string
	Checked                   bool
}

type ndCard struct {
	Hint, Notice                 string
	Cards                        []ndChoiceCard
	Name, NameErr                string
	Names                        []string
	SrcBefore, SrcLink, SrcAfter string
}

// ndWord writes a small number as a word ("zwei", "drei"), else as digits.
func (e ctEnv) ndWord(n int) string {
	if n == 2 {
		return e.t("nd.word.2")
	}
	if n >= 3 && n <= 10 {
		return e.t("contact.word." + strconv.Itoa(n))
	}
	return strconv.Itoa(n)
}

// ndFormChoice reads the radio of the card and what was typed as a choice of the contact. "Kein Namenstag" is the
// pre-selected card of a name the list does not know: it does not pin an auto contact to "none", so the contact has
// its name day again when its name changes.
func ndFormChoice(f ctForm, autoMatch bool, curMode string) (mode, entry, name string) {
	switch {
	case f.NameDay == "":
		return "", "", ""
	case f.NameDay == nameday.ModeAuto:
		return nameday.ModeAuto, "", ""
	case f.NameDay == nameday.ModeNone:
		if !autoMatch && (curMode == "" || curMode == nameday.ModeAuto) {
			return nameday.ModeAuto, "", ""
		}
		return nameday.ModeNone, "", ""
	case f.NameDay == nameday.ModeName:
		return nameday.ModeName, "", strings.TrimSpace(f.NameDayName)
	case strings.HasPrefix(f.NameDay, "entry:"):
		return nameday.ModeEntry, strings.TrimPrefix(f.NameDay, "entry:"), ""
	}
	return "", "", ""
}

// ndFormValue is the value of the radio that stands for a stored choice.
func ndFormValue(c store.Contact) (value, name string) {
	switch c.NameDayMode {
	case nameday.ModeEntry:
		return "entry:" + c.NameDayEntry, ""
	case nameday.ModeName:
		return nameday.ModeName, c.NameDayName
	case nameday.ModeNone:
		return nameday.ModeNone, ""
	}
	return "", ""
}

// nameDayProblems are the choices of a form the list of the household does not have.
func (e ctEnv) nameDayProblems(d ctDraft) []ctProblem {
	cal, ok := e.p.household.NameDayCalendar()
	if !ok {
		return nil
	}
	add := func(field, code, arg string) []ctProblem {
		return []ctProblem{{Anchor: "nameday", Field: field, Code: code, Arg: arg}}
	}
	switch d.NameDayMode {
	case nameday.ModeName:
		switch name := strings.TrimSpace(d.NameDayName); {
		case name == "":
			return add("name_day_name", "empty", "")
		case contactRunes(name) > store.MaxNameDayName:
			return add("name_day_name", "long", "")
		case cal.Lookup(name) == nil:
			return add("name_day_name", "unknown", name)
		}
	case nameday.ModeEntry:
		if _, ok := cal.Entry(d.NameDayEntry); !ok {
			return add("name_day", "entry", "")
		}
	}
	return nil
}

func contactRunes(s string) int { return len([]rune(s)) }

// nameDayProblemText says what is wrong with the choice, with what to do.
func (e ctEnv) nameDayProblemText(pr ctProblem) string {
	church := e.churchAdj(e.p.household.NameDayList)
	switch pr.Field + "." + pr.Code {
	case "name_day_name.empty":
		return e.t("nd.err.name.empty", "church", church)
	case "name_day_name.long":
		return e.t("nd.err.name.long")
	case "name_day_name.unknown":
		if cal, ok := e.p.household.NameDayCalendar(); ok {
			if ex := ndExample(cal, pr.Arg); ex != "" {
				return e.t("nd.err.name.example", "church", church, "name", pr.Arg, "example", ex)
			}
		}
		return e.t("nd.err.name", "church", church, "name", pr.Arg)
	case "name_day.entry":
		return e.t("nd.err.entry", "church", church)
	}
	return ""
}

// ndCardOf builds the card for the form as it is now. It is nil while name days are off.
func (e ctEnv) ndCardOf(f ctForm, errs map[string]string) *ndCard {
	h := e.p.household
	cal, ok := h.NameDayCalendar()
	if !ok {
		return nil
	}
	today := e.s.householdNow(h)
	church := e.churchAdj(h.NameDayList)
	card := &ndCard{Name: f.NameDayName, NameErr: errs["name_day_name"], Names: cal.Names()}
	card.SrcBefore = e.t("nd.f.src.before", "church", church)
	if h.NameDayStyleApplies() {
		card.SrcBefore = e.t("nd.f.src.before.style", "church", church, "style", e.t("nd.style.word."+string(h.NameDayStyle())))
	}
	card.SrcLink, card.SrcAfter = e.t("nd.f.src.link"), e.t("nd.f.src.after")
	shown := f.Full
	if strings.TrimSpace(shown) == "" {
		shown = f.Name
	}
	first := nameday.FirstName(f.Full, f.Name)
	m, found := cal.Resolve(f.Full, f.Name)
	selected := f.NameDay
	if selected == "" {
		selected = nameday.ModeAuto
		if !found {
			selected = nameday.ModeNone
		}
	}
	dayOf := func(en nameday.Entry) string {
		day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
		return e.ndDay(nextOn(en, day, h.NameDayStyle()).Date, !en.Yearly())
	}
	entries := m.Entries
	if strings.HasPrefix(selected, "entry:") { // a chosen day of another name stays a card, or the choice would vanish
		id := strings.TrimPrefix(selected, "entry:")
		if en, ok := cal.Entry(id); ok {
			has := false
			for _, x := range entries {
				has = has || x.ID == id
			}
			if !has {
				entries = append(append([]nameday.Entry{}, entries...), en)
			}
		}
	}
	switch {
	case found && m.Part > 0:
		card.Hint = e.t("nd.f.hint.part", "ordinal", e.ndOrdinal(m.Part), "part", m.Matched, "name", shown)
		if len(m.Entries) > 1 {
			card.Hint = e.t("nd.f.hint.part.days", "ordinal", e.ndOrdinal(m.Part), "part", m.Matched, "name", shown, "church", church, "n", e.ndWord(len(m.Entries)))
		}
	case found && len(m.Entries) > 1:
		card.Hint = e.t("nd.f.hint.name.days", "name", shown, "church", church, "first", first, "n", e.ndWord(len(m.Entries)))
	default:
		card.Hint = e.t("nd.f.hint.name", "name", shown)
	}
	if !found {
		card.Notice = e.t("nd.f.notice.unknown", "church", church, "first", first, "name", f.Name)
	}
	for i, en := range entries {
		c := ndChoiceCard{Date: dayOf(en), Title: ndEntryName(en)}
		if found && i == 0 {
			c.Value, c.Small = nameday.ModeAuto, e.t("nd.f.auto.small", "saint", en.Saint)
		} else {
			c.Value, c.Small = "entry:"+en.ID, e.t("nd.f.entry.small", "saint", en.Saint)
		}
		c.Checked = selected == c.Value
		card.Cards = append(card.Cards, c)
	}
	nameSmall := e.t("nd.f.name.small")
	if ex := ndExample(cal, first); ex != "" && !found {
		nameSmall = e.t("nd.f.name.small.example", "first", first, "example", ex)
	}
	noneSmall := e.t("nd.f.none.small")
	if !found {
		noneSmall = e.t("nd.f.none.small.default")
	}
	card.Cards = append(card.Cards,
		ndChoiceCard{Value: nameday.ModeName, Title: e.t("nd.f.name"), Small: nameSmall, Checked: selected == nameday.ModeName},
		ndChoiceCard{Value: nameday.ModeNone, Title: e.t("nd.f.none"), Small: noneSmall, Checked: selected == nameday.ModeNone})
	return card
}

// ndEventPage is the page of a generated name-day or Slava event: where it comes from, the saint, the source and the
// way back to the contact or the Birthdays calendar.
type ndEventPage struct {
	Title, Lead, Moved, Explain, Calendar, Saint, Repeat, Source, ContactHref, Open string
	Unchecked                                                                       bool // the entry is not yet checked against its source
	Slava                                                                           bool // the page of the Slava: no contact, its own words
}

// nameDayEventPage describes a name-day event for the household; false when the contact or the list is gone, so the
// page falls back to the plain read-only page.
func (s *server) nameDayEventPage(r *http.Request, p principal, contactID, calendarName string, occ calendar.Occurrence) (ndEventPage, bool) {
	cal, ok := p.household.NameDayCalendar()
	if !ok {
		return ndEventPage{}, false
	}
	c, err := s.db.Contact(p.household.ID, contactID)
	if err != nil {
		return ndEventPage{}, false
	}
	e := ctEnv{s: s, lang: s.langOf(r), p: p}
	res := cal.For(c.FullName, c.HejmoName, nameday.Choice{Mode: c.NameDayMode, Entry: c.NameDayEntry, Name: c.NameDayName})
	if !res.Found {
		return ndEventPage{}, false
	}
	en := res.Entry
	st := p.household.NameDayStyle()
	o := occurrenceOf(en, occ.Start, st)
	church := e.churchAdj(p.household.NameDayList)
	pg := ndEventPage{Title: occ.Title, Calendar: calendarName, Saint: en.Saint, Source: e.sourceOf(cal.List, cal, en), Unchecked: en.Unchecked, ContactHref: contactsPath + "/" + c.ID,
		Open: e.t("nd.ev.open", "contact", c.HejmoName), Explain: e.t("nd.ev.explain", "contact", c.HejmoName, "church", church)}
	pg.Lead = e.t("nd.ev.lead", "weekday", e.t("weekday."+strconv.Itoa(int(occ.Start.Weekday()))), "date", datefmt.Date(p.household.DateFormat, occ.Start))
	switch {
	case en.Easter != nil:
		pg.Repeat = e.ndEaster(en)
	case en.Move != "":
		nominal := en
		nominal.Move = ""
		pg.Repeat = e.t("nd.ev.repeat.moved", "date", e.ndDay(nominal.OnStyle(o.Date.Year(), st).Date, false))
		if o.Moved {
			pg.Moved = e.t("nd.ev.moved", "from", e.ndDay(o.From, false), "year", strconv.Itoa(o.Date.Year()), "easter", e.ndDay(o.Easter, false), "to", e.ndDay(o.Date, false))
		}
	case st == nameday.Julian && o.Offset > 0:
		pg.Repeat = e.t("nd.ev.repeat.julian", "date", e.ndDay(o.Date, false), "church", e.ndDay(o.Church, false), "n", strconv.Itoa(o.Offset))
	default:
		pg.Repeat = e.t("nd.ev.repeat.yearly", "date", e.ndDay(o.Date, false))
	}
	return pg, true
}
