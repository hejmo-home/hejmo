package server

import (
	"encoding/json"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/qrsvg"
	"github.com/hejmo-home/hejmo/internal/store"
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
func displayToken(r *http.Request) (string, bool) {
	c, err := r.Cookie(displayCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	return auth.HashToken(c.Value), true
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
		ip, _, splitErr := net.SplitHostPort(r.RemoteAddr)
		if splitErr != nil {
			ip = r.RemoteAddr
		}
		key := "open:" + ip
		if s.opens.blocked(key) {
			w.Header().Set("Retry-After", "600")
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
		s.opens.fail(key)
		token := auth.NewToken()
		hash = auth.HashToken(token)
		if d, err = s.db.OpenPairing(h.ID, hash, s.now()); err != nil {
			s.fail(w, "open pairing", err)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: displayCookie, Value: token, Path: "/", Expires: s.now().AddDate(10, 0, 0),
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		})
	}
	if d.Paired {
		if s.now().Sub(d.LastSeen) > time.Minute {
			_ = s.db.TouchDisplay(hash, s.now())
		}
		view, version, err := s.renderDisplayContent(h, d, s.langOf(r))
		if err != nil {
			s.fail(w, "display view", err)
			return
		}
		s.render(w, r, http.StatusOK, "display_family.html", map[string]any{
			"Display": d, "PollSeconds": displayPollSeconds, "View": view, "Version": version, "Theme": displayTheme(d),
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
	svg, err := qrsvg.SVG(pair, s.i18n.T(s.langOf(r), "display.pair.qr"))
	if err != nil {
		s.fail(w, "qr code", err)
		return
	}
	s.render(w, r, http.StatusOK, "display_pairing.html", map[string]any{
		"Code": d.Code, "PairURL": pair, "QR": template.HTML(svg), "PollSeconds": displayPollSeconds,
	})
}

// displayTheme is the theme of a display's pages.
//
// Placeholder until the per-display themes ticket: a normal screen is always
// light, although ADR 0015 gives it an automatic default theme.
func displayTheme(d store.Display) string {
	if d.Type == store.DisplayEInkBW {
		return "bw"
	}
	return "light"
}

// markerShapes are the black-and-white marker shapes in person slot order (DESIGN.md).
var markerShapes = []string{"circle-filled", "circle-empty", "square-filled", "square-empty", "triangle-filled", "diamond-filled"}

// familyView builds today's family view of the household.
func (s *server) familyView(h store.Household, lang string, showTomorrow bool) (familyview.View, error) {
	data, err := s.displayData(h, lang, 2)
	if err != nil {
		return familyview.View{}, err
	}
	return familyview.Build(familyview.Input{Now: data.now, Lang: lang, Region: h.Region, People: data.people,
		Calendars: data.calendars, Occurrences: data.occurrences, ShowTomorrow: showTomorrow, SyncStale: s.syncStale(h.ID),
		TimeFormat: h.TimeFormat, TemperatureUnit: h.TemperatureUnit, Weather: s.homeWeather(h)}), nil
}

// displayData is what the views of a display are built from: the household's
// people with their markers and groups, the calendars' audiences and the
// occurrences of the next days starting today.
type displayData struct {
	now         time.Time // in the household's time zone
	people      []familyview.Person
	calendars   map[string]calendar.Audience
	occurrences []calendar.Occurrence
}

func (s *server) displayData(h store.Household, lang string, days int) (displayData, error) {
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
	if out.occurrences, err = s.occurrences(h, lang, d0, end); err != nil {
		return out, err
	}
	for _, c := range cals {
		out.calendars[c.ID] = c.Audience
	}
	allGroups, err := s.db.Groups(h.ID) // one query for every person's memberships
	if err != nil {
		return out, err
	}
	groupsOf := map[string][]string{}
	for _, g := range allGroups {
		for _, m := range g.Members {
			groupsOf[m] = append(groupsOf[m], g.ID)
		}
	}
	for i, p := range people {
		shape := p.Shape
		if shape == "" {
			shape = markerShapes[i%len(markerShapes)]
		}
		out.people = append(out.people, familyview.Person{ID: p.ID, Name: p.Name, Slot: i%len(markerShapes) + 1, Shape: shape, Groups: groupsOf[p.ID]})
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
func (s *server) handleDisplayState(w http.ResponseWriter, r *http.Request) {
	state := map[string]string{"state": "unpaired"}
	if d, hash, ok := s.currentDisplay(r); ok {
		switch {
		case d.Paired:
			state["state"] = "paired"
			if s.now().Sub(d.LastSeen) > time.Minute {
				_ = s.db.TouchDisplay(hash, s.now())
			}
		case d.Code != "":
			state["state"], state["code"] = "pairing", d.Code
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(state)
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
