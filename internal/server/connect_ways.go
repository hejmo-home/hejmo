package server

import (
	"net/http"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
)

// calendarWay is one way to add a calendar, one card of the choice page
// "Connect a calendar" (ticket 231, decision Q156). The page is built from this
// list: a new way is one entry here, its guide in connection_new.html and its
// texts in the locales.
type calendarWay struct {
	ID string // the card's radio value and the data-service of its guide
	// Service is the connection to the outside the way needs (#201): when it is
	// off, the card is locked and says so.
	Service outbound.Service
	// Name and Sub are the locale keys of the card's title and its one sentence.
	Name, Sub string
}

// calendarWays are the ways in the order of the choice page (Q156): iCloud,
// Google, Outlook, another calendar server, a calendar link.
//
// Outlook is paused by the calendars switch alone (ADR 0024).
var calendarWays = []calendarWay{
	{"icloud", outbound.Calendars, "connect.icloud.name", "connect.icloud.sub"},
	{"google", outbound.Calendars, "connect.google.name", "connect.google.sub"},
	{"outlook", outbound.Calendars, "connect.outlook.name", "connect.outlook.sub"},
	{"caldav", outbound.Calendars, "connect.caldav.name", "connect.caldav.sub"},
	{"ics", outbound.ICS, "connect.ics.name", "connect.ics.sub"},
}

// wayCard is a way as the choice page shows it.
type wayCard struct {
	ID, Name, Sub string
	Checked       bool
	// Lock is the reason in words when the way is locked: its connection is off.
	Lock   string
	Locked bool
}

// wayOf finds the way with the id; the second result is false for an unknown one.
func wayOf(id string) (calendarWay, bool) {
	for _, w := range calendarWays {
		if w.ID == id {
			return w, true
		}
	}
	return calendarWay{}, false
}

// openWays counts the ways whose connection to the outside is on.
func (s *server) openWays() int {
	n := 0
	for _, w := range calendarWays {
		if s.gate.Allowed(w.Service) {
			n++
		}
	}
	return n
}

// wayCards builds the cards. chosen is the way to show chosen: none unless one
// is asked for and open, or only one is left open (then it is chosen).
func (s *server) wayCards(r *http.Request, chosen string) []wayCard {
	if w, ok := wayOf(chosen); !ok || !s.gate.Allowed(w.Service) {
		chosen = ""
	}
	only := s.openWays() == 1
	cards := make([]wayCard, 0, len(calendarWays))
	for _, w := range calendarWays {
		c := wayCard{ID: w.ID, Name: s.say(r, i18n.Msg(w.Name)), Sub: s.say(r, i18n.Msg(w.Sub))}
		switch {
		case !s.gate.Allowed(w.Service):
			c.Locked = true
			c.Lock = s.say(r, i18n.Msg("connect.locked", "name", s.say(r, i18n.Msg("outside.name."+string(w.Service)))))
		default:
			c.Checked = w.ID == chosen || only && chosen == ""
		}
		cards = append(cards, c)
	}
	return cards
}

// connectNotes are the notices above the choice page (#201). Offline mode says
// so once; otherwise there is one for each connection that is off, in the order
// of the ways.
func (s *server) connectNotes(r *http.Request) []*offlineNote {
	if s.offline() {
		return []*offlineNote{s.noteFor(r, calendarWays[0].Service, "connect.note.offline")}
	}
	var out []*offlineNote
	seen := map[outbound.Service]bool{}
	for _, w := range calendarWays {
		if seen[w.Service] {
			continue
		}
		seen[w.Service] = true
		if n := s.noteFor(r, w.Service, "connect.note."+string(w.Service)); n != nil {
			out = append(out, n)
		}
	}
	return out
}

// canAddCalendar is whether the page "Connect a calendar" has a way to offer:
// offline mode, or every connection switched off, leaves only the notices.
func (s *server) canAddCalendar() bool { return s.openWays() > 0 }
