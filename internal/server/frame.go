package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/sun"
)

// navItem is one screen of the web interface frame. A person sees it when
// their role holds the permission the screen needs (ADR 0015), so a child's
// device session sees only the screens its role allows. Later screens
// (calendar, lists, meal plan, phones) add a line here.
type navItem struct {
	Href, Label string
	Needs       auth.Permission
}

var navItems = []navItem{
	{"/", "nav.home", auth.Perm("events", auth.Read)},
	{"/calendar", "nav.calendar", auth.Perm("events", auth.Write)},
	{"/tasks", "nav.tasks", auth.Perm("tasks", auth.Read)},
	{"/settings", "nav.settings", auth.Perm("settings", auth.Write)},
}

type navLink struct {
	Href, Label string
	Current     bool
}

// frame is the chrome around a web screen: brand bar, sign-out and navigation.
type frame struct {
	SignedIn bool
	Items    []navLink
	// SunRise and SunSet are today's sun times at the home location (Unix
	// seconds, 0 when unknown); the page script uses them for the browser
	// theme "automatic" (ADR 0015).
	SunRise, SunSet int64
	// Back is set on setup screens: they link back to the Settings hub.
	Back bool
	// Success is the success line after a saved change, already translated.
	Success string
}

// frameFor builds the frame for the person behind the request, or a bare
// frame (brand only, no sun times) for sign-in and setup screens.
func (s *server) frameFor(r *http.Request) frame {
	p, ok := s.currentPerson(r)
	if !ok {
		// No sun times here: from sunrise and sunset anyone could work out
		// where the home is.
		return frame{}
	}
	f := s.sunFrame()
	f.SignedIn = true
	f.Back = isSetupPath(r.URL.Path)
	for _, it := range navItems {
		if !auth.RoleAllows(p.Role, it.Needs) {
			continue
		}
		cur := (it.Href == "/settings" && f.Back) || r.URL.Path == it.Href || (it.Href != "/" && strings.HasPrefix(r.URL.Path, it.Href+"/"))
		f.Items = append(f.Items, navLink{it.Href, it.Label, cur})
	}
	return f
}

// sunFrame is a bare frame carrying today's sun times at the home location.
func (s *server) sunFrame() frame {
	if !s.db.Configured() {
		return frame{}
	}
	h, err := s.db.HomeHousehold()
	if err != nil || (h.Latitude == 0 && h.Longitude == 0) {
		return frame{}
	}
	loc := time.UTC
	if l, err := time.LoadLocation(h.Timezone); err == nil {
		loc = l
	}
	t := sun.On(s.now().In(loc), h.Latitude, h.Longitude)
	if t.Kind != sun.Normal {
		return frame{}
	}
	return frame{SunRise: t.Rise.Unix(), SunSet: t.Set.Unix()}
}

// isSetupPath reports whether path is a screen reached from the Settings hub
// (ADR 0017): a hub entry or a page below one. Connections hang off Calendars.
func isSetupPath(path string) bool {
	paths := []string{"/connections"}
	for _, it := range hubItems {
		paths = append(paths, it.Href)
	}
	for _, p := range paths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// hubItem is one entry of the Settings hub. Entries appear as their screens
// exist (people and my account join with their tickets).
type hubItem struct {
	Href, Label, Hint string
	Needs             auth.Permission
}

var hubItems = []hubItem{
	{"/calendars", "hub.connections", "hub.connections.hint", auth.Perm("calendars", auth.Write)},
	{"/caldav", "hub.caldav", "hub.caldav.hint", auth.Perm("tokens", auth.Read)},
	{"/displays", "hub.displays", "hub.displays.hint", auth.Perm("displays", auth.Write)},
	{"/phones", "hub.phones", "hub.phones.hint", auth.Perm("tokens", auth.Write)},
	{"/people", "hub.people", "hub.people.hint", auth.Perm("people", auth.Write)},
	{"/settings/household", "hub.household", "hub.household.hint", auth.Perm("settings", auth.Write)},
	{"/otp", "hub.otp", "hub.otp.hint", auth.Perm("settings", auth.Write)},
}
