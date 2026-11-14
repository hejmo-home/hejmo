package server

import (
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/auth"
)

// navItem is one screen of the web interface frame. A person sees it when
// their role holds the permission the screen needs (ADR 0015), so a child's
// device session sees only the screens its role allows. Later screens
// (calendar, lists, meal plan, tasks, phones) add a line here.
type navItem struct {
	Href, Label string
	Needs       auth.Permission
}

var navItems = []navItem{
	{"/", "nav.home", auth.Perm("events", auth.Read)},
	{"/calendars", "nav.connections", auth.Perm("calendars", auth.Write)},
	{"/caldav", "nav.caldav", auth.Perm("tokens", auth.Read)},
	{"/displays", "nav.displays", auth.Perm("displays", auth.Write)},
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
}

// frameFor builds the frame for the person behind the request, or a bare
// frame (brand only) for sign-in and setup screens.
func (s *server) frameFor(r *http.Request) frame {
	p, ok := s.currentPerson(r)
	if !ok {
		return frame{}
	}
	f := frame{SignedIn: true}
	for _, it := range navItems {
		if !auth.RoleAllows(p.Role, it.Needs) {
			continue
		}
		cur := r.URL.Path == it.Href || (it.Href != "/" && strings.HasPrefix(r.URL.Path, it.Href+"/"))
		f.Items = append(f.Items, navLink{it.Href, it.Label, cur})
	}
	return f
}
