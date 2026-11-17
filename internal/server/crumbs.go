package server

import (
	"reflect"
)

// crumb is one linked part of a breadcrumb trail (ticket 182, ADR 0017). The
// last part of a trail is the page itself: the layout takes its text from the
// page title, so a trail holds only the links above the page.
type crumb struct{ Href, Key, Name string } // Key is a locale key; Name, when set, is a name of the household shown as it is

var (
	crumbSettings = crumb{"/settings", "nav.settings", ""}
	crumbMeals    = crumb{"/meals", "nav.meals", ""}
	crumbTasks    = crumb{"/tasks", "nav.tasks", ""}

	crumbConnections = crumb{"/calendars", "hub.connections", ""}
	crumbCalDAV      = crumb{"/caldav", "hub.caldav", ""}
	crumbDisplays    = crumb{"/displays", "hub.displays", ""}
	crumbPhones      = crumb{"/phones", "hub.phones", ""}
	crumbMCP         = crumb{"/mcp", "hub.mcp", ""}
	crumbPeople      = crumb{"/people", "hub.people", ""}
	crumbAccount     = crumb{"/account", "hub.otp", ""}
	crumbSessions    = crumb{"/sessions", "hub.sessions", ""}

	crumbLists   = crumb{"/settings/lists", "lists.set.title", ""}
	crumbLibrary = crumb{"/settings/lists/library", "lib.title", ""}

	crumbMealTemplates    = crumb{"/meals/templates", "meals.tpl.title", ""}
	crumbRoutineTemplates = crumb{"/routines/templates", "routine.tpl.title", ""}
)

// crumbTrails are the parts above the page, by template: every page below
// Settings, and an everyday page two or more levels below its screen (an
// everyday page one level deep keeps its back link). Pages that are the hub
// itself, or that hold no trail, are not listed.
var crumbTrails = map[string][]crumb{
	"calendars.html": {crumbSettings}, "caldav.html": {crumbSettings}, "displays.html": {crumbSettings}, "phones.html": {crumbSettings},
	"mcp.html": {crumbSettings}, "people.html": {crumbSettings}, "household.html": {crumbSettings}, "account.html": {crumbSettings}, "sessions.html": {crumbSettings},
	"licences.html": {crumbSettings}, "settings_lists.html": {crumbSettings},

	"calendar_detail.html": {crumbSettings, crumbConnections}, "connection.html": {crumbSettings, crumbConnections}, "connection_new.html": {crumbSettings, crumbConnections},
	"display.html": {crumbSettings, crumbDisplays},
	"token.html":   {crumbSettings, crumbCalDAV}, "token_once.html": {crumbSettings, crumbCalDAV},
	"phone.html":     {crumbSettings, crumbPhones},
	"mcp_token.html": {crumbSettings, crumbMCP}, "mcp_token_new.html": {crumbSettings, crumbMCP}, "mcp_token_once.html": {crumbSettings, crumbMCP},
	"person_edit.html": {crumbSettings, crumbPeople}, "group.html": {crumbSettings, crumbPeople},
	"account_reset.html": {crumbSettings, crumbAccount}, "passkey.html": {crumbSettings, crumbAccount}, "recovery_codes.html": {crumbSettings, crumbAccount},
	"session.html": {crumbSettings, crumbSessions},

	"library.html": {crumbSettings, crumbLists}, "article.html": {crumbSettings, crumbLists, crumbLibrary}, "article_form.html": {crumbSettings, crumbLists, crumbLibrary},

	"meal_template.html":    {crumbMeals, crumbMealTemplates},
	"routine_template.html": {crumbTasks, crumbRoutineTemplates},
}

// crumbsFor returns the parts above the page for a template, or nil when the
// page has no trail. Three pages hang off a named page of their own: the
// invitation below the person, the switch-off confirmation below the
// connection, a template's task below the template.
func crumbsFor(name string, data map[string]any) []crumb {
	switch name {
	case "invitation.html", "food.html":
		if id, who := lookup(data, "Subject", "ID"), lookup(data, "Subject", "Name"); id != "" {
			return []crumb{crumbSettings, crumbPeople, {Href: "/people/" + id, Name: who}}
		}
		return []crumb{crumbSettings, crumbPeople}
	case "connection_off.html":
		if id, who := lookup(data, "Connection", "ID"), lookup(data, "Connection", "Username"); id != "" {
			return []crumb{crumbSettings, crumbConnections, {Href: "/connections/" + id, Name: who}}
		}
		return []crumb{crumbSettings, crumbConnections}
	case "routine_template_step.html":
		href, _ := data["BackHref"].(string)
		text, _ := data["BackText"].(string)
		if href != "" {
			return []crumb{crumbTasks, crumbRoutineTemplates, {Href: href, Name: text}}
		}
		return []crumb{crumbTasks, crumbRoutineTemplates}
	}
	return crumbTrails[name]
}

// lookup follows a path of map keys and struct fields through page data and
// returns the string found, or "" when a step is missing.
func lookup(data any, path ...string) string {
	v := reflect.ValueOf(data)
	for _, p := range path {
		for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
			if v.IsNil() {
				return ""
			}
			v = v.Elem()
		}
		switch {
		case !v.IsValid():
			return ""
		case v.Kind() == reflect.Map:
			v = v.MapIndex(reflect.ValueOf(p))
		case v.Kind() == reflect.Struct:
			v = v.FieldByName(p)
		default:
			return ""
		}
	}
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.IsValid() && v.Kind() == reflect.String {
		return v.String()
	}
	return ""
}
