package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Signed-in devices (issue #108): a parent sees the browser sessions of the
// household and ends another browser's.

// flashSessionEnded is the success line after a session was ended. Its
// argument is "<device>-<browser>-<person id>"; the text is built from what
// the box knows, never from the request.
const flashSessionEnded = "flash.session_ended"

type sessionView struct {
	ID, Device, LastUsed, SignedIn, SignedInAt, Ends string
	PersonID, Person                                 string
	Slot                                             int
	Shape                                            string
	Own                                              bool
}

type sessionGroup struct {
	ID, Name string
	Slot     int
	Shape    string
	Sessions []sessionView
}

// deviceText is "Device · Browser" in the language, from the stored keys. A
// session made before the box kept them, or one it could not tell, is an
// unknown device.
func (s *server) deviceText(lang, device string) string {
	d, b, _ := strings.Cut(device, "|")
	if d == "" {
		d, b = auth.Unknown, auth.Unknown
	}
	name := func(prefix, key string) string {
		if t := s.i18n.T(lang, prefix+"."+key); t != prefix+"."+key {
			return t
		}
		return s.i18n.T(lang, prefix+"."+auth.Unknown)
	}
	if b == auth.Unknown {
		return name("device", d)
	}
	return name("device", d) + " · " + name("browser", b)
}

// dayLayout is the date as the language writes it.
func dayLayout(lang string) string {
	if lang == "de" {
		return "02.01.2006"
	}
	return "02/01/2006"
}

func (s *server) sessionViews(lang string, p principal, list []store.BrowserSession, ownID string) []sessionView {
	loc := p.location()
	clock := "15:04"
	if p.household.TimeFormat == store.Time12h {
		clock = "3:04 PM"
	}
	day := dayLayout(lang)
	now := s.now().In(loc)
	lastUsed := func(t time.Time) string {
		if t.IsZero() {
			return s.i18n.T(lang, "sessions.never_seen")
		}
		t = t.In(loc)
		switch {
		case sameDay(t, now):
			return s.i18n.T(lang, "sessions.today") + ", " + t.Format(clock)
		case sameDay(t, now.AddDate(0, 0, -1)):
			return s.i18n.T(lang, "sessions.yesterday") + ", " + t.Format(clock)
		}
		return t.Format(day) + ", " + t.Format(clock)
	}
	chips := map[string]personChip{}
	if people, err := s.db.People(p.household.ID); err == nil {
		for _, c := range chipsOf(people) {
			chips[c.ID] = c
		}
	}
	out := make([]sessionView, 0, len(list))
	for _, b := range list {
		c := chips[b.Person.ID]
		out = append(out, sessionView{
			ID: b.ID, Device: s.deviceText(lang, b.Device), LastUsed: lastUsed(b.LastSeen),
			SignedIn: b.SignedIn.In(loc).Format(day), SignedInAt: b.SignedIn.In(loc).Format(day + ", " + clock),
			Ends: b.Expires.In(loc).Format(day), PersonID: b.Person.ID, Person: b.Person.Name, Slot: c.Slot, Shape: c.Shape, Own: b.ID == ownID,
		})
	}
	return out
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// ownSessionID is the public id of the session this request comes with.
func (s *server) ownSessionID(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	id, _ := s.db.SessionID(auth.HashToken(c.Value))
	return id
}

func (s *server) handleSessionsPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	list, err := s.db.ListBrowserSessions(p.household.ID, s.now())
	if err != nil {
		s.fail(w, "browser sessions", err)
		return
	}
	lang := s.langOf(r)
	own := s.ownSessionID(r)
	var groups []sessionGroup
	for _, v := range s.sessionViews(lang, p, list, own) {
		if n := len(groups); n == 0 || groups[n-1].ID != v.PersonID {
			groups = append(groups, sessionGroup{ID: v.PersonID, Name: v.Person, Slot: v.Slot, Shape: v.Shape})
		}
		g := &groups[len(groups)-1]
		g.Sessions = append(g.Sessions, v)
	}
	s.render(w, r, http.StatusOK, "sessions.html", map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID),
		"Groups": groups, "OnlyThis": len(list) == 1 && list[0].ID == own, "NoBrowser": len(list) == 0, "Gone": r.URL.Query().Get("gone") == "1",
	})
}

func (s *server) handleSessionPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	b, err := s.db.BrowserSession(p.household.ID, r.PathValue("id"), s.now())
	if errors.Is(err, store.ErrNotFound) {
		http.Redirect(w, r, "/sessions?gone=1", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, "browser session", err)
		return
	}
	v := s.sessionViews(s.langOf(r), p, []store.BrowserSession{b}, s.ownSessionID(r))[0]
	s.render(w, r, http.StatusOK, "session.html", map[string]any{"Person": p.person, "Session": v})
}

func (s *server) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	b, err := s.db.BrowserSession(p.household.ID, id, s.now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Redirect(w, r, "/sessions?gone=1", http.StatusSeeOther)
		return
	case err != nil:
		s.fail(w, "browser session", err)
		return
	case id == s.ownSessionID(r):
		// Ending the browser one is using locks oneself out: sign out instead.
		http.Redirect(w, r, "/sessions/"+id, http.StatusSeeOther)
		return
	}
	switch err := s.db.EndBrowserSession(p.household.ID, id); {
	case errors.Is(err, store.ErrNotFound): // ended by someone else in the meantime
		http.Redirect(w, r, "/sessions?gone=1", http.StatusSeeOther)
	case err != nil:
		s.fail(w, "end browser session", err)
	default:
		s.redirectSaved(w, r, "/sessions", flashSessionEnded+":"+flashDevice(b.Device)+"-"+b.Person.ID)
	}
}

// flashDevice is a stored device label as a flash argument ("windows|edge"
// becomes "windows-edge"; none becomes unknown-unknown).
func flashDevice(device string) string {
	d, b, _ := strings.Cut(device, "|")
	if d == "" {
		return auth.Unknown + "-" + auth.Unknown
	}
	return d + "-" + b
}

// sessionEndedText is the success line for the flash argument, or "" if it
// does not name a person of this household.
func (s *server) sessionEndedText(lang, arg string) string {
	parts := strings.SplitN(arg, "-", 3)
	if len(parts) != 3 {
		return ""
	}
	p, ok := s.member(parts[2])
	if !ok {
		return ""
	}
	return s.i18n.Format(lang, i18n.Msg("sessions.ended", "device", s.deviceText(lang, parts[0]+"|"+parts[1]), "person", p.Name))
}
