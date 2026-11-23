package server

import (
	"encoding/json"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/layout"
	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/internal/qrsvg"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
	"github.com/hejmo-home/hejmo/internal/theme"
	"github.com/hejmo-home/hejmo/internal/wholine"
)

const (
	// displayCookie holds a display's own token. The box keeps only its hash.
	displayCookie = "hejmo_display"
	// displayPollSeconds is how often a display asks the box whether it is
	// still paired. It must stay well under a minute: a revoked display shows
	// the pairing screen again within a minute (ticket 5.2).
	displayPollSeconds = 30
)

// displayToken returns the hash of the token in the display's cookie.
// A display may send it as its cookie (the web pages) or as a bearer token
// (the API, ADR 0015); a Bearer header wins.
func displayToken(r *http.Request) (string, bool) {
	if header := r.Header.Get("Authorization"); header != "" {
		// Only a Bearer header is a display token; another scheme (say Basic
		// auth added by a reverse proxy) leaves the cookie in charge.
		if token, ok := strings.CutPrefix(header, "Bearer "); ok {
			token = strings.TrimSpace(token)
			if token == "" {
				return "", false
			}
			return auth.HashToken(token), true
		}
	}
	c, err := r.Cookie(displayCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	return auth.HashToken(c.Value), true
}

// errPairingsLimited is returned while one address opened too many pairings.
var errPairingsLimited = errors.New("too many pairings opened")

// openPairing registers a new waiting display for a request that carries no
// valid display token. It returns the display and its token, which only the
// caller sees: the box keeps its hash. Opening is limited per client address.
func (s *server) openPairing(r *http.Request, h store.Household) (store.Display, string, error) {
	ip, _, splitErr := net.SplitHostPort(r.RemoteAddr)
	if splitErr != nil {
		ip = r.RemoteAddr
	}
	key := "open:" + ip
	if s.opens.blocked(key) {
		return store.Display{}, "", errPairingsLimited
	}
	s.opens.fail(key)
	token := auth.NewToken()
	d, err := s.db.OpenPairing(h.ID, auth.HashToken(token), s.now())
	return d, token, err
}

// currentDisplay finds the display behind the request's token.
func (s *server) currentDisplay(r *http.Request) (store.Display, string, bool) {
	hash, ok := displayToken(r)
	if !ok {
		return store.Display{}, "", false
	}
	d, err := s.db.DisplayByToken(hash, s.now())
	return d, hash, err == nil
}

// pairURL is where the QR code on a pairing screen leads a parent's phone.
func (s *server) pairURL(r *http.Request, code string) string {
	origin := s.httpsOrigin(r)
	if r.TLS != nil {
		origin = "https://" + r.Host
	}
	return origin + "/displays?" + url.Values{"code": {code}}.Encode()
}

// handleDisplay is the display address: the pairing screen until a parent
// confirms the code, then the family view.
func (s *server) handleDisplay(w http.ResponseWriter, r *http.Request) {
	if !s.db.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	d, hash, ok := s.currentDisplay(r)
	if !ok {
		var token string
		d, token, err = s.openPairing(r, h)
		if errors.Is(err, errPairingsLimited) {
			w.Header().Set("Retry-After", "600")
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
		if err != nil {
			s.fail(w, "open pairing", err)
			return
		}
		hash = auth.HashToken(token)
		http.SetCookie(w, &http.Cookie{
			Name: displayCookie, Value: token, Path: "/", Expires: s.now().AddDate(10, 0, 0),
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		})
	}
	if d.Paired {
		if s.now().Sub(d.LastSeen) > time.Minute {
			_ = s.db.TouchDisplay(hash, s.now())
		}
		view, version, err := s.renderDisplayContent(h, d, s.displayLang(r))
		if err != nil {
			s.fail(w, "display view", err)
			return
		}
		s.render(w, r, http.StatusOK, "display_family.html", map[string]any{
			"Display": d, "PollSeconds": displayPollSeconds, "View": view, "Version": version, "Theme": s.displayTheme(h, d),
			"SaverSeconds": s.saverSeconds(d),
		})
		return
	}
	if d.Code == "" {
		if d, err = s.db.RenewPairingCode(hash, s.now()); err != nil {
			s.fail(w, "renew pairing code", err)
			return
		}
	}
	pair := s.pairURL(r, d.Code)
	svg, err := qrsvg.SVG(pair, s.i18n.T(s.displayLang(r), "display.pair.qr"))
	if err != nil {
		s.fail(w, "qr code", err)
		return
	}
	s.render(w, r, http.StatusOK, "display_pairing.html", map[string]any{
		"Code": d.Code, "PairURL": pair, "QR": template.HTML(svg), "PollSeconds": displayPollSeconds,
	})
}

// displayTheme is the palette a display shows now: its theme setting resolved
// against the sun at the home location, or in quiet mode the palette of the
// quiet page, whatever the setting.
func (s *server) displayTheme(h store.Household, d store.Display) string {
	now := s.householdNow(h)
	if d.Quiet.Active(now) {
		// Quiet mode takes precedence over the theme (ADR 0015): a normal
		// screen shows a black page, an E-Ink display its own night page.
		switch d.Type {
		case store.DisplayScreen:
			return string(theme.Dark)
		case store.DisplayEInkBW:
			return "bw"
		}
		return string(theme.Light)
	}
	t := theme.Resolve(theme.Input{
		Setting: d.Theme, SunriseOffset: d.SunriseOffset, SunsetOffset: d.SunsetOffset,
		Now: now, Lat: h.Latitude, Lon: h.Longitude,
	})
	// Black-and-white E-Ink has its own light palette; dark uses the dark one.
	if t == theme.Light && d.Type == store.DisplayEInkBW {
		return "bw"
	}
	return string(t)
}

// markerShapes are the black-and-white marker shapes in person slot order (DESIGN.md).
var markerShapes = []string{"circle-filled", "circle-empty", "square-filled", "square-empty", "triangle-filled", "diamond-filled", "cross-filled", "star-filled"}

// familyView builds today's family view of the household as the display d
// shows it: its people in its order and its blocks (ticket 180). Without a
// display (the home page) it is the layout of a new display.
func (s *server) familyView(h store.Household, lang string, d *store.Display) (familyview.View, error) {
	data, err := s.displayData(h, lang, 2)
	if err != nil {
		return familyview.View{}, err
	}
	everyone := data.people
	showTomorrow := false
	var lay layout.Layout
	if d != nil {
		showTomorrow = d.ShowTomorrow
		if lay, err = s.db.DisplayLayout(h.ID, d.ID); err != nil {
			return familyview.View{}, err
		}
		data.people = orderedPeople(everyone, lay.Order(layout.RotationDay(data.now)))
	} else {
		ids, err := s.db.PersonIDs(h.ID)
		if err != nil {
			return familyview.View{}, err
		}
		lay = layout.Default(ids)
	}
	meals, cutoffs := s.mealsToday(h, data.now)
	today := data.now.Format(store.DayLayout)
	if err := s.db.PruneDayNotes(h.ID, today); err != nil { // a note is deleted once its day is over
		return familyview.View{}, err
	}
	notes, err := s.db.DayNotes(h.ID)
	if err != nil {
		return familyview.View{}, err
	}
	byDay := map[string]string{}
	for _, n := range notes {
		byDay[n.Day] = n.Text
	}
	due, err := s.db.TasksForDay(h.ID, data.now.Format(task.DateLayout))
	if err != nil {
		return familyview.View{}, err
	}
	byPerson := map[string][]familyview.Task{}
	for _, t := range due {
		byPerson[t.PersonID] = append(byPerson[t.PersonID], familyview.Task{Done: t.Done})
	}
	progress := map[string]familyview.Progress{}
	for id, ts := range byPerson {
		progress[id] = familyview.ProgressOf(ts)
	}
	lists, err := s.db.Lists(h.ID)
	if err != nil {
		return familyview.View{}, err
	}
	routines := map[string][]familyview.RoutineLine{}
	if lay.Enabled(layout.Routines) {
		if routines, err = s.routineLines(h, due); err != nil {
			return familyview.View{}, err
		}
	}
	var tileList *familyview.ListInput
	if lay.Enabled(layout.List) {
		if tileList, err = s.listTileInput(h, lay.ListID); err != nil {
			return familyview.View{}, err
		}
	}
	return familyview.Build(familyview.Input{Layout: &lay, Everyone: everyone, Routines: routines, List: tileList, HasLists: len(lists) > 0, Meals: meals, MealCutoffs: cutoffs, Now: data.now, Lang: lang, Region: h.Region, People: data.people, Groups: data.groups, I18n: s.i18n,
		Waste: data.waste, DayNotes: byDay, TomorrowLabel: s.i18n.T(lang, "family.tomorrow"), TodayLabel: s.i18n.T(lang, "family.today"),
		Calendars: data.calendars, Occurrences: data.occurrences, ShowTomorrow: showTomorrow, SyncStale: s.syncStale(h.ID), TaskProgress: progress,
		TimeFormat: h.TimeFormat, TemperatureUnit: h.TemperatureUnit, Weather: s.homeWeather(h)}), nil
}

// mealsToday reads today's meals for the meal tile. If they cannot be read it
// logs that and returns none: the family view renders without the tile
// instead of failing.
func (s *server) mealsToday(h store.Household, now time.Time) (map[meal.Mealtime]string, meal.Cutoffs) {
	day := now.Format(store.DayLayout)
	entries, err := s.db.Meals(h.ID, day, day)
	if err != nil {
		s.logger.Error("meal tile: reading today's meals", "err", err)
		return nil, meal.Cutoffs{}
	}
	out := map[meal.Mealtime]string{}
	for _, e := range entries {
		out[e.Mealtime] = e.Text
	}
	return out, h.MealCutoffs
}

// displayData is what the views of a display are built from: the household's
// people with their markers and groups, the calendars' audiences and the
// occurrences of the next days starting today.
type displayData struct {
	now         time.Time // in the household's time zone
	people      []familyview.Person
	groups      []wholine.Group // subgroups with their members, for the who-line
	calendars   map[string]calendar.Audience
	occurrences []calendar.Occurrence // without waste collections
	// waste holds the occurrences of the waste collection calendars: they show
	// only in the tomorrow tile and on the night page (ADR 0015).
	waste []calendar.Occurrence
}

func (s *server) displayData(h store.Household, lang string, days int) (displayData, error) {
	s.rollMealPlan(h, lang) // the meal tile reads the meal-plan calendar
	now := s.householdNow(h)
	loc := now.Location()
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	end := d0.AddDate(0, 0, days)
	out := displayData{now: now, calendars: map[string]calendar.Audience{}}
	people, err := s.db.People(h.ID)
	if err != nil {
		return out, err
	}
	cals, err := s.db.Calendars(h.ID)
	if err != nil {
		return out, err
	}
	// The meal plan has its own place on a display (the meal tile), not the
	// family strip, the person columns or the personal view (ADR 0015).
	generated := map[string]bool{}
	for _, c := range cals {
		if c.Kind == "meals" {
			generated[c.ID] = true
		}
	}
	all, err := s.occurrences(h, lang, d0, end)
	if err != nil {
		return out, err
	}
	all = slices.DeleteFunc(all, func(o calendar.Occurrence) bool { return generated[o.CalendarID] })
	out.occurrences, out.waste = splitWaste(all, cals)
	for _, c := range cals {
		out.calendars[c.ID] = c.Audience
	}
	allGroups, err := s.db.Groups(h.ID) // one query for every person's memberships
	if err != nil {
		return out, err
	}
	groupsOf := map[string][]string{}
	for _, g := range allGroups {
		out.groups = append(out.groups, wholine.Group{Name: g.Name, Members: g.Members})
		for _, m := range g.Members {
			groupsOf[m] = append(groupsOf[m], g.ID)
		}
	}
	for i, p := range people {
		_, shape := displayedLook(p, i)
		out.people = append(out.people, familyview.Person{ID: p.ID, Name: p.Name, Slot: slotOf(p, i), Shape: shape, Groups: groupsOf[p.ID]})
	}
	return out, nil
}

// householdNow is the current time in the household's time zone.
func (s *server) householdNow(h store.Household) time.Time {
	loc := time.UTC
	if l, err := time.LoadLocation(h.Timezone); err == nil {
		loc = l
	}
	return s.now().In(loc)
}

// occurrences returns the household's occurrences in [from, to), with a
// placeholder for untitled events.
func (s *server) occurrences(h store.Household, lang string, from, to time.Time) ([]calendar.Occurrence, error) {
	events, err := s.db.EventsBetween(h.ID, from, to)
	if err != nil {
		return nil, err
	}
	occs, err := calendar.Expand(events, from, to)
	if err != nil {
		return nil, err
	}
	for i := range occs {
		if strings.TrimSpace(occs[i].Title) == "" {
			occs[i].Title = s.i18n.T(lang, "event.untitled")
		}
	}
	return occs, nil
}

// homeWeather is the last reading for the home, nil when there is none (or
// only a stale one): the chip then hides. A display never fetches it itself.
func (s *server) homeWeather(h store.Household) *familyview.Weather {
	r, ok := s.weather.Latest(h.Latitude, h.Longitude)
	if !ok {
		return nil
	}
	return &familyview.Weather{TempC: r.TempC, Icon: string(r.Icon)}
}

// handleDisplayState tells a display whether it is still what its page says:
// "paired", "pairing" (with its code) or "unpaired" (revoked, unknown or its
// code ran out). The display's script reloads the page when this changes.
// A paired display also learns whether its screensaver is due now ("saver" is
// "1": on and not in quiet mode) and after how many idle seconds it asks
// ("saver_seconds", "0" when it is off).
func (s *server) handleDisplayState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(s.displayState(r))
}

// displayState is the answer of the state check, for the page route and the
// API route alike. It touches a paired display's last-seen time.
func (s *server) displayState(r *http.Request) map[string]string {
	state := map[string]string{"state": "unpaired"}
	if d, hash, ok := s.currentDisplay(r); ok {
		switch {
		case d.Paired:
			state["state"] = "paired"
			// How the display hangs, so the layout preview on the settings page is drawn that way (ticket 180).
			if o := r.URL.Query().Get("orientation"); (o == "portrait" || o == "landscape") && (o == "portrait") != d.Portrait {
				_ = s.db.SetDisplayOrientation(hash, o == "portrait")
			}
			state["saver"], state["saver_seconds"] = "0", strconv.Itoa(s.saverSeconds(d))
			if h, err := s.db.HomeHousehold(); err == nil {
				state["theme"] = s.displayTheme(h, d)
				if s.saverActive(h, d) {
					state["saver"] = "1"
				}
			}
			if s.now().Sub(d.LastSeen) > time.Minute {
				_ = s.db.TouchDisplay(hash, s.now())
			}
		case d.Code != "":
			state["state"], state["code"] = "pairing", d.Code
		}
	}
	return state
}

// errTooManyAttempts is returned while too many wrong codes were entered.
var errTooManyAttempts = errors.New("too many wrong codes")

// pairDisplay confirms a pairing code for the display showing it. Wrong and
// expired codes count against a per-person limit, so six digits cannot be
// guessed.
func (s *server) pairDisplay(p principal, code string, in store.Display) (store.Display, error) {
	key := "pair:" + p.person.ID
	if s.limiter.blocked(key) {
		return store.Display{}, errTooManyAttempts
	}
	d, err := s.db.ConfirmPairing(p.household.ID, code, in, s.now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.limiter.fail(key)
	case err == nil:
		s.limiter.reset(key)
	}
	return d, err
}

// splitWaste separates the occurrences of waste collection calendars from the rest.
func splitWaste(occs []calendar.Occurrence, cals []store.Calendar) (rest, waste []calendar.Occurrence) {
	isWaste := map[string]bool{}
	for _, c := range cals {
		isWaste[c.ID] = c.Waste
	}
	for _, o := range occs {
		if isWaste[o.CalendarID] {
			waste = append(waste, o)
		} else {
			rest = append(rest, o)
		}
	}
	return rest, waste
}

// saverSeconds is how long the family view waits without a touch before it asks
// the box whether the screensaver is due; 0 when the display has it switched
// off. It is not 0 in quiet mode: the page is rendered once and stays for days,
// so the timer must be there when the night ends. The box decides when the
// timer fires (displayState).
func (s *server) saverSeconds(d store.Display) int {
	if !d.Paired || !d.Saver.Enabled {
		return 0
	}
	return d.Saver.IdleMinutes * 60
}
