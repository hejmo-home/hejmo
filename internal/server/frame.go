package server

import (
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/sun"
)

// navItem is one screen of the web interface frame. A person sees it when
// their role holds the permission the screen needs (ADR 0015), so a child's
// device session sees only the screens its role allows. Later screens
// (calendar, lists, phones) add a line here.
type navItem struct {
	Href, Label string
	Needs       auth.Permission
}

var navItems = []navItem{
	{"/", "nav.home", auth.Perm("events", auth.Read)},
	{"/calendar", "nav.calendar", auth.Perm("events", auth.Write)},
	{"/tasks", "nav.tasks", auth.Perm("tasks", auth.Read)},
	{"/meals", "nav.meals", auth.Perm("meals", auth.Read)},
	{"/lists", "nav.lists", auth.Perm("lists", auth.Read)},
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
	// TimeFormat is the household's clock ("24h" or "12h"); the time picker
	// follows it.
	TimeFormat string
	// DateFormat is the household's date layout ("dmy" or "mdy"); the offline
	// line on phones writes its date that way.
	DateFormat string
	// Back is set on setup screens: they link back to the Settings hub.
	Back bool
	// BackHref and BackLabel (a locale key) are the link above the content that
	// leads one level up: the Settings hub for setup screens, the meal plan and
	// the templates menu for the meal plan templates, the device list for a device.
	BackHref, BackLabel string
	// BackText is the text of the back link when it is a name and not a locale key.
	BackText string
	// Success is the success line after a saved change, already translated
	// and escaped; a text of the locales may hold <b> around a figure.
	Success template.HTML
	// Error is an error line shown above the page, already translated.
	Error string
	// PlainBrand shows the brand as plain text, not a link: the page of an
	// unpaired device leads nowhere (ticket 199).
	PlainBrand bool
	// The language switcher (ticket 134): the active language, its own name and
	// flag file, the other languages, and the page to come back to.
	LangName, LangFlag string
	Others             []language
	Next               string
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
	if h, err := s.db.HomeHousehold(); err == nil {
		f.TimeFormat = string(h.TimeFormat)
		f.DateFormat = string(h.DateFormat)
	}
	f.Back = isSetupPath(r.URL.Path)
	switch {
	case strings.HasPrefix(r.URL.Path, "/sessions/"):
		f.BackHref, f.BackLabel = "/sessions", "sessions.back"
	case f.Back:
		f.BackHref, f.BackLabel = "/settings", "hub.back"
	case strings.HasPrefix(r.URL.Path, "/routines/templates/"):
		f.BackHref, f.BackLabel = "/routines/templates", "routine.tpl.title" // a template's pages lead back to the templates
	case r.URL.Path == "/routines" || strings.HasPrefix(r.URL.Path, "/routines/"),
		strings.HasPrefix(r.URL.Path, "/tasks/") && r.URL.Path != "/tasks/stars":
		f.BackHref, f.BackLabel = "/tasks", "tasks.back" // the routine pages and the task page lead back to the tasks
	case r.URL.Path == "/meals/templates":
		f.BackHref, f.BackLabel = "/meals", "meals.tpl.back_plan"
	case strings.HasPrefix(r.URL.Path, "/meals/templates/"):
		f.BackHref, f.BackLabel = "/meals/templates", "meals.tpl.back_templates"
	}
	for _, it := range navItems {
		if !auth.RoleAllows(p.Role, it.Needs) {
			continue
		}
		cur := (it.Href == "/settings" && f.Back) || r.URL.Path == it.Href || (it.Href != "/" && strings.HasPrefix(r.URL.Path, it.Href+"/")) || (it.Href == "/" && strings.HasPrefix(r.URL.Path, "/notes/")) || (it.Href == "/tasks" && strings.HasPrefix(r.URL.Path, "/routines/"))
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
	{"/mcp", "hub.mcp", "hub.mcp.hint", auth.Perm("tokens", auth.Write)},
	{"/people", "hub.people", "hub.people.hint", auth.Perm("people", auth.Write)},
	{"/settings/household", "hub.household", "hub.household.hint", auth.Perm("settings", auth.Write)},
	{"/account", "hub.otp", "hub.otp.hint", auth.Perm("settings", auth.Write)},
	{"/sessions", "hub.sessions", "hub.sessions.hint", auth.Perm("settings", auth.Write)},
}

// detailPages are the templates of detail pages (ticket 111, ADR 0017): they
// start with a link back to their overview, which replaces the hub link.
var detailPages = map[string]bool{
	"display.html": true, "token.html": true, "token_once.html": true, "connection.html": true, "connection_off.html": true,
	"calendar_detail.html": true, "phone.html": true, "account.html": true, "account_reset.html": true, "passkey.html": true, "mcp_token.html": true, "mcp_token_new.html": true, "mcp_token_once.html": true, "connection_new.html": true, "person_edit.html": true, "invitation.html": true,
}
