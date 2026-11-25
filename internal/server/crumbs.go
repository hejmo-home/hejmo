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
	crumbRewards  = crumb{"/tasks/rewards", "rwd.title", ""}

	crumbConnections     = crumb{"/calendars", "hub.connections", ""}
	crumbConnect         = crumb{"/connections/new", "connect.title", ""}
	crumbCalDAV          = crumb{"/caldav", "hub.caldav", ""}
	crumbDisplays        = crumb{"/displays", "hub.displays", ""}
	crumbPhotos          = crumb{"/photos", "hub.photos", ""}
	crumbLists           = crumb{"/settings/lists", "hub.lists", ""}
	crumbLibrary         = crumb{"/settings/lists/library", "lib.title", ""}
	crumbPersonalDevices = crumb{"/personal-devices", "hub.personal_devices", ""}
	crumbOutside         = crumb{"/settings/connections", "hub.outside", ""}
	crumbUpload          = crumb{uploadPagePath, "updf.title", ""}
	crumbMCP             = crumb{"/mcp", "hub.mcp", ""}
	crumbPeople          = crumb{"/people", "hub.people", ""}
	crumbContacts        = crumb{"/contacts", "hub.contacts", ""}
	crumbContactGroups   = crumb{"/contacts/groups", "contact.groups.title", ""}
	crumbAccount         = crumb{"/account", "hub.otp", ""}
	crumbSessions        = crumb{"/sessions", "hub.sessions", ""}

	crumbMealTemplates    = crumb{"/meals/templates", "meals.tpl.title", ""}
	crumbRoutineTemplates = crumb{"/routines/templates", "routine.tpl.title", ""}
)

// crumbTrails are the parts above the page, by template: every page below
// Settings, and an everyday page two or more levels below its screen (an
// everyday page one level deep keeps its back link). Pages that are the hub
// itself, or that hold no trail, are not listed.
var crumbTrails = map[string][]crumb{
	"calendars.html": {crumbSettings}, "caldav.html": {crumbSettings}, "displays.html": {crumbSettings}, "photos.html": {crumbSettings}, "settings_lists.html": {crumbSettings}, "personal_devices.html": {crumbSettings},
	"mcp.html": {crumbSettings}, "people.html": {crumbSettings}, "household.html": {crumbSettings}, "account.html": {crumbSettings}, "sessions.html": {crumbSettings},
	"licences.html": {crumbSettings}, "connections_outside.html": {crumbSettings}, "https_settings.html": {crumbSettings},
	"offline_on.html": {crumbSettings, crumbOutside}, "offline_off.html": {crumbSettings, crumbOutside},
	"update_upload.html": {crumbSettings, crumbOutside}, "update_install.html": {crumbSettings, crumbOutside, crumbUpload},

	"calendar_detail.html": {crumbSettings, crumbConnections}, "connection.html": {crumbSettings, crumbConnections}, "connection_paused.html": {crumbSettings, crumbConnections}, "connection_new.html": {crumbSettings, crumbConnections}, "google_signin.html": {crumbSettings, crumbConnections},
	"library.html": {crumbSettings, crumbLists}, "always.html": {crumbSettings, crumbLists}, "article.html": {crumbSettings, crumbLists, crumbLibrary}, "article_form.html": {crumbSettings, crumbLists, crumbLibrary},
	"display.html": {crumbSettings, crumbDisplays}, "photo_album.html": {crumbSettings, crumbPhotos},
	"token.html": {crumbSettings, crumbCalDAV}, "token_once.html": {crumbSettings, crumbCalDAV},
	"personal_device.html": {crumbSettings, crumbPersonalDevices},
	"mcp_token.html":       {crumbSettings, crumbMCP}, "mcp_token_new.html": {crumbSettings, crumbMCP}, "mcp_token_once.html": {crumbSettings, crumbMCP},
	"contacts.html": {crumbSettings}, "contacts_kids.html": {crumbSettings, crumbContacts}, "contact_birthdays.html": {crumbSettings, crumbContacts}, "contact_import.html": {crumbSettings, crumbContacts}, "contact_import_review.html": {crumbSettings, crumbContacts}, "contact.html": {crumbSettings, crumbContacts}, "contact_groups.html": {crumbSettings, crumbContacts}, "contact_group.html": {crumbSettings, crumbContacts, crumbContactGroups},
	"person_edit.html": {crumbSettings, crumbPeople}, "group.html": {crumbSettings, crumbPeople},
	"account_reset.html": {crumbSettings, crumbAccount}, "passkey.html": {crumbSettings, crumbAccount}, "recovery_codes.html": {crumbSettings, crumbAccount},
	"session.html": {crumbSettings, crumbSessions},

	"pause.html":   {crumbTasks},
	"recipes.html": {crumbMeals}, "starter.html": {crumbMeals, crumbRecipes}, "recipe.html": {crumbMeals, crumbRecipes}, "recipe_gone.html": {crumbMeals, crumbRecipes},
	"meal_template.html":    {crumbMeals, crumbMealTemplates},
	"routine_template.html": {crumbTasks, crumbRoutineTemplates},
	"reward_form.html":      {crumbTasks, crumbRewards}, "reward.html": {crumbTasks, crumbRewards}, "reward_goal.html": {crumbTasks, crumbRewards},
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
	case "recipe_form.html":
		if id, name := lookup(data, "RecipeID"), lookup(data, "RecipeName"); id != "" {
			return []crumb{crumbMeals, crumbRecipes, {Href: recipesPath + "/" + id, Name: name}}
		}
		return []crumb{crumbMeals, crumbRecipes}
	case "contact_form.html":
		if id, who := lookup(data, "CT", "ID"), lookup(data, "CT", "Name"); id != "" {
			return []crumb{crumbSettings, crumbContacts, {Href: "/contacts/" + id, Name: who}}
		}
		if id, who := lookup(data, "ForPerson", "ID"), lookup(data, "ForPerson", "Name"); id != "" { // opened from a person's page
			return []crumb{crumbSettings, crumbPeople, {Href: "/people/" + id, Name: who}}
		}
		return []crumb{crumbSettings, crumbContacts}
	case "connection_off.html":
		if id, who := lookup(data, "Connection", "ID"), lookup(data, "Connection", "Username"); id != "" {
			return []crumb{crumbSettings, crumbConnections, {Href: "/connections/" + id, Name: who}}
		}
		return []crumb{crumbSettings, crumbConnections}
	case "outlook_signin.html":
		if id, who := lookup(data, "ConnectionID"), lookup(data, "Account"); id != "" {
			return []crumb{crumbSettings, crumbConnections, {Href: "/connections/" + id, Name: who}}
		}
		return []crumb{crumbSettings, crumbConnections, crumbConnect}
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
