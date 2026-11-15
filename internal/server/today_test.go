package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/task"
)

// todayBox is the example family (Mama, Papa, Sebastian, Mia) on Saturday
// 14/11/2026 at 08:00 with the first steps hidden, as after the setup.
func todayBox(t *testing.T) *familyBox {
	t.Helper()
	f := newFamilyBox(t, "eink_color")
	f.now = f.at(14, 8, 0)
	f.post("/first-steps/hide", url.Values{})
	return f
}

func (f *familyBox) addTask(t *testing.T, who, title string, stars int, done bool) {
	t.Helper()
	tk, err := f.db.AddTask(f.householdID, task.Task{PersonID: f.people[who].ID, Title: title, Repeat: task.Daily, Date: "2026-11-14", Stars: stars})
	if err != nil {
		t.Fatal(err)
	}
	if done {
		if err := f.db.SetTaskDone(f.householdID, tk.ID, "2026-11-14", true, "", f.at(14, 8, 0)); err != nil {
			t.Fatal(err)
		}
	}
}

func todayCard(page string) string {
	i := strings.Index(page, `<section class="today"`)
	if i < 0 {
		return ""
	}
	return page[i : i+strings.Index(page[i:], "</section>")]
}

// Ticket 133, design pass 2: Home for a parent shows the day: the whole
// family's events, one card per person in order, today's meals; above the note.
func TestParentHomeShowsTheDay(t *testing.T) {
	f := todayBox(t)
	f.event(t, "Flohmarkt", f.at(14, 11, 0), f.at(14, 13, 0), false)
	f.event(t, "Yoga", f.at(14, 9, 0), f.at(14, 10, 0), false, "Mama")
	f.event(t, "Zahnarzt", f.at(14, 8, 15), f.at(14, 9, 0), false, "Papa")
	f.event(t, "Elternabend", f.at(14, 19, 30), f.at(14, 21, 0), false, "Papa")
	f.event(t, "Schwimmkurs", f.at(14, 15, 30), f.at(14, 16, 30), false, "Mia")
	f.event(t, "Morgen", f.at(15, 9, 0), f.at(15, 10, 0), false, "Mia")
	f.addTask(t, "Sebastian", "Zähne putzen", 1, true)
	f.addTask(t, "Sebastian", "Anziehen", 1, true)
	f.addTask(t, "Sebastian", "Ranzen packen", 2, false)
	f.addTask(t, "Mia", "Zähne putzen", 1, false)
	f.addTask(t, "Papa", "Müll rausbringen", 0, true)
	meals := url.Values{"week": {"2026-11-09"}, mealField("2026-11-14", "lunch"): {"Kartoffelsuppe"}, mealField("2026-11-14", "dinner"): {"Brotzeit"}}
	f.post("/meals", meals)

	page := f.page("de", "/").body
	c := todayCard(page)
	if c == "" {
		t.Fatalf("no today card:\n%s", page)
	}
	if strings.Index(page, `<section class="today"`) > strings.Index(page, `class="steps note-day"`) {
		t.Error("the today card is below the note card")
	}
	for _, want := range []string{
		`<div class="today-head"><h2>Heute, Samstag 14. November</h2><a class="link" href="/calendar">Ganze Woche</a></div>`,
		`<ul class="today-fam"><li><span class="tm">11:00</span><span><b>Flohmarkt</b><span class="who">Ganze Familie</span></span></li></ul>`,
		`<p class="today-meal"><b>Essen heute:</b> Mittag: Kartoffelsuppe · Abend: Brotzeit</p>`,
	} {
		if !strings.Contains(c, want) {
			t.Errorf("today card misses %q\n%s", want, c)
		}
	}
	// One li per person, in the household order, each with its marker.
	order := []string{"Mama", "Papa", "Sebastian", "Mia"}
	last := -1
	for _, n := range order {
		at := strings.Index(c, `<h3><span class="marker"`)
		_ = at
		i := strings.Index(c, `</span>`+n+`</h3>`)
		if i < 0 || i < last {
			t.Errorf("%s is missing or out of order", n)
		}
		last = i
	}
	if n := strings.Count(c, `<li data-person="`); n != 4 {
		t.Errorf("%d person cards, want 4", n)
	}
	papa := personCard(t, c, "Papa")
	for _, want := range []string{`<ul class="tev"><li><span class="tm">08:15</span><span>Zahnarzt</span></li><li><span class="tm">19:30</span><span>Elternabend</span></li></ul>`, `<a class="pbar" href="/tasks?person=` + f.people["Papa"].ID + `">`, "1 von 1 Aufgaben"} {
		if !strings.Contains(papa, want) {
			t.Errorf("Papa's card misses %q\n%s", want, papa)
		}
	}
	if strings.Contains(papa, "Sterne") || strings.Contains(papa, `class="icon star"`) {
		t.Error("an adult's card shows stars")
	}
	seb := personCard(t, c, "Sebastian")
	if !strings.Contains(seb, `<li class="none">Keine Termine</li>`) || !strings.Contains(seb, "2 von 3 Aufgaben · ") || !strings.Contains(seb, `class="icon star"`) || !strings.Contains(seb, "2 Sterne") || !strings.Contains(seb, `<span style="--pct:66">`) {
		t.Errorf("Sebastian's card:\n%s", seb)
	}
	mia := personCard(t, c, "Mia")
	if !strings.Contains(mia, "0 von 1 Aufgaben · ") || !strings.Contains(mia, "0 Sterne") || !strings.Contains(mia, "Schwimmkurs") || strings.Contains(mia, "Morgen") {
		t.Errorf("Mia's card:\n%s", mia)
	}
	mama := personCard(t, c, "Mama")
	if strings.Contains(mama, "pbar") || !strings.Contains(mama, "Yoga") {
		t.Errorf("a person without tasks shows a bar, or lacks the event:\n%s", mama)
	}
	if strings.Contains(c, "Heute steht für niemanden") {
		t.Error("a day with events says it is empty")
	}
	en := todayCard(f.page("en", "/").body)
	for _, want := range []string{"Today, Saturday 14 November", "Whole week", "Whole family", "Lunch: Kartoffelsuppe", "No events", "2 of 3 tasks", "2 stars", "Meals today:"} {
		if !strings.Contains(en, want) {
			t.Errorf("English today card misses %q\n%s", want, en)
		}
	}
}

func personCard(t *testing.T, card, name string) string {
	t.Helper()
	i := strings.Index(card, `</span>`+name+`</h3>`)
	if i < 0 {
		t.Fatalf("no card for %s", name)
	}
	start := strings.LastIndex(card[:i], `<li data-person="`)
	end := strings.Index(card[i:], "</li></ul>") // the list of events and the bar end the card, the next card starts with li
	rest := card[start:]
	if next := strings.Index(rest[1:], `<li data-person="`); next > 0 {
		return rest[:next+1]
	}
	_ = end
	return rest
}

func TestEmptyDayHasOneSentenceAndOneAction(t *testing.T) {
	f := todayBox(t)
	c := todayCard(f.page("de", "/").body)
	for _, want := range []string{
		`<div class="empty"><p>Heute steht für niemanden etwas im Kalender und es gibt keine Aufgaben.</p><a class="button secondary" href="/calendar/new">Termin eintragen</a></div>`,
		`<p class="today-meal"><b>Essen heute:</b> noch nichts geplant. <a href="/meals">Essensplan öffnen</a></p>`,
	} {
		if !strings.Contains(c, want) {
			t.Errorf("empty day misses %q\n%s", want, c)
		}
	}
	if strings.Contains(c, `data-person=`) || strings.Contains(c, "today-people") {
		t.Errorf("an empty day shows eight empty cards:\n%s", c)
	}
}

// The card is for after the first steps: while the steps are shown, Home is
// the steps and the note; a child never sees it.
func TestTodayCardComesAfterTheFirstSteps(t *testing.T) {
	f := newFamilyBox(t, "eink_color")
	f.now = f.at(14, 8, 0)
	if page := f.page("de", "/").body; todayCard(page) != "" || card(page) == "" {
		t.Error("the today card is shown beside the first steps")
	}
	f.post("/first-steps/hide", url.Values{})
	page := f.page("de", "/").body
	if todayCard(page) == "" || card(page) != "" {
		t.Error("after hiding, the today card is missing or the steps are still there")
	}
	if m := successRe.FindStringSubmatch(page); m == nil {
		t.Log("the success line is shown on the page after the redirect only")
	}
}

func childOf(t *testing.T, f *familyBox, name string) *box {
	t.Helper()
	token := auth.NewToken()
	if err := f.db.CreateSession(auth.HashToken(token), f.people[name].ID, f.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	c := *f.box
	c.client = f.trustingClient()
	u, _ := url.Parse(f.srv.URL)
	c.client.Jar.SetCookies(u, []*http.Cookie{{Name: "hejmo_session", Value: token, Path: "/"}})
	return &c
}

// A child's Home: their marker large next to "Hallo Mia", their day, their
// tasks as large rows, one main button; the removed placeholder key is gone.
func TestChildHomeShowsTheirDayAndTasks(t *testing.T) {
	f := todayBox(t)
	f.event(t, "Flohmarkt", f.at(14, 11, 0), f.at(14, 13, 0), false)
	f.event(t, "Schwimmkurs", f.at(14, 15, 30), f.at(14, 16, 30), false, "Mia")
	f.event(t, "Yoga", f.at(14, 9, 0), f.at(14, 10, 0), false, "Mama")
	f.addTask(t, "Mia", "Zähne putzen", 1, true)
	f.addTask(t, "Mia", "Anziehen", 1, true)
	f.addTask(t, "Mia", "Schulranzen packen", 1, false)
	f.addTask(t, "Mia", "Zimmer aufräumen", 2, false)
	mia := childOf(t, f, "Mia")
	r := mia.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "de"})
	if r.status != http.StatusOK {
		t.Fatalf("child Home = %d", r.status)
	}
	p := r.body
	i := strings.Index(p, `<h1 class="child-h">`)
	if i < 0 {
		t.Fatalf("no child heading:\n%s", p)
	}
	main := p[i:]
	for _, want := range []string{
		`<h1 class="child-h"><span class="marker" data-person="` + "", "</span>Hallo Mia</h1>",
		`<section class="today child" data-person="`, `<h2>Dein Tag heute</h2>`,
		`<ul class="tev big"><li><span class="tm">11:00</span><span>Flohmarkt <span class="who">mit der ganzen Familie</span></span></li><li><span class="tm">15:30</span><span>Schwimmkurs</span></li></ul>`,
		`<h3>Deine Aufgaben</h3><ul class="ctasks"><li data-done><span class="box">`, `<span class="t">Zähne putzen</span><span class="stars"><svg class="icon star"`, `</svg>1</span></li>`,
		`<li><span class="box"></span><span class="t">Zimmer aufräumen</span><span class="stars">`, `</svg>2</span></li>`,
		`<a class="pbar" href="/tasks">`, "2 von 4 erledigt · ", "2 Sterne heute", `<span style="--pct:50">`,
		`<div class="actions"><a class="button" href="/tasks">Aufgaben abhaken</a></div>`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("child Home misses %q\n%s", want, main)
		}
	}
	if strings.Contains(main, "Yoga") {
		t.Error("the child sees an event of Mama")
	}
	for _, bad := range []string{"home.placeholder", "note-day", "Notiz", `data-firststeps`, `class="today"`, "Einstellungen"} {
		if strings.Contains(main, bad) {
			t.Errorf("child Home shows %q", bad)
		}
	}
	if n := strings.Count(main, `class="button"`); n != 1 {
		t.Errorf("%d main buttons, want 1", n)
	}
	en := mia.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "en"}).body
	for _, want := range []string{"Hello Mia", "Your day today", "with the whole family", "Your tasks", "2 of 4 done · ", "2 stars today", "Tick off tasks"} {
		if !strings.Contains(en, want) {
			t.Errorf("English child Home misses %q", want)
		}
	}
}

func TestChildHomeWithNothingToday(t *testing.T) {
	f := todayBox(t)
	mia := childOf(t, f, "Mia")
	p := mia.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{`<ul class="tev big"><li class="none">Keine Termine</li></ul>`, "Heute hast du keine Aufgaben."} {
		if !strings.Contains(p, want) {
			t.Errorf("empty child Home misses %q\n%s", want, p)
		}
	}
	if strings.Contains(p, "pbar") || strings.Contains(p, "Aufgaben abhaken") {
		t.Error("a child without tasks gets a progress bar or the tick-off button")
	}
}
