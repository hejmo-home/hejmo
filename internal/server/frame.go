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
	{"/calendars", "nav.connections", auth.Perm("calendars", auth.Write)},
	{"/caldav", "nav.caldav", auth.Perm("tokens", auth.Read)},
	{"/displays", "nav.displays", auth.Perm("displays", auth.Write)},
	{"/tasks", "nav.tasks", auth.Perm("tasks", auth.Write)},
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
	for _, it := range navItems {
		if !auth.RoleAllows(p.Role, it.Needs) {
			continue
		}
		cur := r.URL.Path == it.Href || (it.Href != "/" && strings.HasPrefix(r.URL.Path, it.Href+"/"))
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
