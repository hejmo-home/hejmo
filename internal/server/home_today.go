package server

import (
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
	"github.com/hejmo-home/hejmo/internal/wholine"
)

// The day on Home (ticket 133, design pass 2). A parent sees the whole
// family's events, one card per person and today's meals, above the note. A
// child sees their own day and tasks. Both are built from what the display
// shows, so the box says the same thing in both places.

// todayEvent is a line of an event list: the start time (or "all day"), the
// title and, where it matters, who it is for.
type todayEvent struct{ Time, Title, Who string }

// todayProgress is a person's tasks of today as a bar and a line in words.
type todayProgress struct {
	Pct int
	// Text is "3 of 4 tasks". Stars is "3 stars" for a child, "" otherwise.
	Text, Stars string
}

type todayPerson struct {
	ID, Name, Shape string
	Slot            int
	Events          []todayEvent
	Progress        *todayProgress
}

// todayView is the card "Today, Saturday 14 November".
type todayView struct {
	Title  string
	Family []todayEvent
	People []todayPerson
	// Empty is true when nobody has an event or a task today.
	Empty bool
	// Meal is "Lunch: Soup · Dinner: Bread"; empty when nothing is planned.
	Meal string
}

// childTask is a row of a child's tasks.
type childTask struct {
	Title string
	Done  bool
	Stars int
}

// childView is a child's Home.
type childView struct {
	Chip     personChip
	Events   []todayEvent
	Tasks    []childTask
	Progress *todayProgress
}

// startOf is the start of a time range: "09:00" of "09:00–10:00".
func startOf(t string) string {
	if i := strings.Index(t, "–"); i > 0 {
		return t[:i]
	}
	return t
}

func (s *server) todayCard(h store.Household, lang string, people []store.Person) (*todayView, error) {
	v, err := s.familyView(h, lang, false)
	if err != nil {
		return nil, err
	}
	t := func(key string) string { return s.i18n.T(lang, key) }
	f := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	now := s.householdNow(h)
	day := now.Format(task.DateLayout)
	due, err := s.db.TasksForDay(h.ID, day)
	if err != nil {
		return nil, err
	}
	stars := map[string]int{}
	for _, d := range due {
		if d.Done {
			stars[d.PersonID] += d.Stars
		}
	}
	role := map[string]store.Role{}
	for _, p := range people {
		role[p.ID] = p.Role
	}
	out := &todayView{Title: f("home.today.title", "weekday", s.weekdayName(lang, now), "day", strconv.Itoa(now.Day()), "month", t("month."+strconv.Itoa(int(now.Month()))))}
	for _, it := range v.Strip {
		ev := todayEvent{Title: it.Title, Time: startOf(it.Time)}
		switch {
		case it.Kind == familyview.KindHoliday:
			ev.Who = t("home.today.holiday")
		case len(it.WhoParts) == 0:
			ev.Who = t("home.today.family")
		default:
			ev.Who = strings.Trim(wholine.Plain(it.WhoParts), "[] ")
		}
		if it.AllDay && ev.Time == "" {
			ev.Time = t("home.today.allday")
		}
		out.Family = append(out.Family, ev)
	}
	busy := len(out.Family) > 0
	for _, c := range v.Columns {
		p := todayPerson{ID: c.Person.ID, Name: c.Person.Name, Shape: c.Person.Shape, Slot: c.Person.Slot}
		for _, e := range c.Events {
			p.Events = append(p.Events, todayEvent{Time: startOf(e.Time), Title: e.Title})
		}
		if c.Progress != nil {
			p.Progress = &todayProgress{Pct: c.Progress.Percent(), Text: f("home.today.progress", "done", strconv.Itoa(c.Progress.Done), "total", strconv.Itoa(c.Progress.Total))}
			if role[c.Person.ID] == store.RoleChild {
				p.Progress.Stars = starsText(f, stars[c.Person.ID], "home.today.stars")
			}
			busy = true
		}
		busy = busy || len(p.Events) > 0
		out.People = append(out.People, p)
	}
	out.Empty = !busy
	meals, err := s.db.Meals(h.ID, day, day)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, m := range meals {
		lines = append(lines, t("home.today.meal."+string(m.Mealtime))+": "+m.Text)
	}
	out.Meal = strings.Join(lines, " · ")
	return out, nil
}

// starsText is "1 star" or "3 stars" under the key (key.one, key.other).
func starsText(f func(string, ...string) string, n int, key string) string {
	if n == 1 {
		return f(key+".one", "n", "1")
	}
	return f(key+".other", "n", strconv.Itoa(n))
}

func (s *server) childHome(h store.Household, lang string, person store.Person, people []store.Person) (*childView, error) {
	pv, err := s.personalView(h, lang, person.ID)
	if err != nil {
		return nil, err
	}
	t := func(key string) string { return s.i18n.T(lang, key) }
	f := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	out := &childView{}
	for i, p := range people {
		if p.ID == person.ID {
			out.Chip = chipsOf(people)[i]
		}
	}
	for _, e := range pv.Days[0].Events {
		ev := todayEvent{Time: startOf(e.Time), Title: e.Title}
		if e.AllDay {
			ev.Time = t("home.today.allday")
		}
		if e.Family {
			ev.Who = t("home.child.with_family")
		}
		out.Events = append(out.Events, ev)
	}
	earned := 0
	for _, tk := range pv.Tasks {
		out.Tasks = append(out.Tasks, childTask{Title: tk.Title, Done: tk.Done, Stars: tk.Stars})
		if tk.Done {
			earned += tk.Stars
		}
	}
	if pr := pv.Progress(); pr.Total > 0 {
		out.Progress = &todayProgress{Pct: pr.Percent(), Text: f("home.child.progress", "done", strconv.Itoa(pr.Done), "total", strconv.Itoa(pr.Total))}
		if pv.Stars.Shown {
			out.Progress.Stars = starsText(f, earned, "home.child.stars")
		}
	}
	return out, nil
}
