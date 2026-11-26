package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The MCP server (ticket 7.7, ADR 0018). It offers the REST API's functions
// for events, lists, tasks (with routines, templates and stars) and the meal
// plan as MCP tools. A tool runs the very REST handler of its route, for the
// person the token belongs to and with the token's scopes, so every rule and
// every scope check of the REST API applies unchanged. Nothing that manages
// tokens, settings, people, displays or personal devices is a tool.

const mcpInstructions = "These tools read and change the family's shared Hejmo box: calendar events, lists, tasks with routines and stars, the meal plan and the recipes. " +
	"You act as the parent who created your token. Tools you cannot see are not allowed for your token."

// mcpParam is one argument of a tool. Arguments named in the path of the
// route go into the path; the others go into the JSON body, or into the query
// string for GET routes and for those marked query.
type mcpParam struct {
	name     string
	typ      string // string, integer, boolean or strings (a list of strings)
	desc     string
	required bool
	query    bool
}

type mcpTool struct {
	name, desc string
	method     string
	path       string
	params     []mcpParam
}

func req(name, typ, desc string) mcpParam {
	return mcpParam{name: name, typ: typ, desc: desc, required: true}
}
func opt(name, typ, desc string) mcpParam { return mcpParam{name: name, typ: typ, desc: desc} }
func qry(name, typ, desc string) mcpParam {
	return mcpParam{name: name, typ: typ, desc: desc, query: true}
}

var (
	idOf = func(what string) mcpParam { return req("id", "string", "The id of the "+what) }

	scopeParam = qry("scope", "string", "For a series: this (default), following or all")
	occParam   = qry("occurrence", "string", "For a series: the start of the occurrence, RFC 3339 (default: the first)")

	eventFields = []mcpParam{
		opt("location", "string", "Where it takes place"),
		opt("description", "string", "Notes"),
		opt("people", "strings", "Person ids it is for (with groups empty: the whole family)"),
		opt("groups", "strings", "Group ids it is for"),
		opt("rrule", "string", "Repetition as an iCalendar RRULE, for example FREQ=WEEKLY;COUNT=3"),
	}
	rewardFields = []mcpParam{
		opt("symbol", "string", "An emoji (default the gift)"),
		opt("limit_period", "string", "none, day, week or month"), opt("limit_count", "integer", "How often one person may redeem it per period, 1 to 99"),
		opt("whole_family", "boolean", "Meant for the whole family"), opt("person_ids", "strings", "Person ids it is meant for"),
	}
	taskTitle  = "The task title"
	mealtime   = req("mealtime", "string", "breakfast, lunch, snack or dinner")
	mealDate   = req("date", "string", "The day, YYYY-MM-DD")
	recipeBody = []mcpParam{
		req("name", "string", "Recipe name (at most 120 characters), unique ignoring case"),
		opt("category", "string", "main (default), side, dessert, baking, breakfast or snack"),
		opt("portions", "integer", "1 to 99; the number of people in the household when missing"),
		opt("minutes", "integer", "From start to table, 0 to 9999"),
		opt("mealtimes", "strings", "breakfast, lunch, snack or dinner; none means every mealtime"),
		opt("tags", "strings", "Free tags, at most 30 characters each"),
		opt("tips", "string", "Tips, at most 2000 characters"),
		opt("source", "string", "A book, a person or a web address"),
		opt("ingredients", "array", "A list of {quantity (number, none for as needed), unit (see list_recipe_units), name (an article of the library by name, or free text that is not checked for allergens), article_id, variety}"),
		opt("steps", "strings", "The steps in order, one text each (at most 2000 characters)"),
	}
	mealTplBody = []mcpParam{
		req("name", "string", "Template name (at most 60 characters)"),
		req("mealtimes", "string", "all (fills every mealtime of the day) or one"),
		opt("mealtime", "string", "With mealtimes one: breakfast, lunch, snack or dinner"),
		req("interval", "integer", "Repeats every n weeks"),
		req("start_week", "string", "Monday of the first week it fills, YYYY-MM-DD"),
		req("entries", "array", "A list of {weekday (1 Monday to 7 Sunday), mealtime, text or recipe_id (a recipe of the household, planned with the household's number of people as portions)}"),
	}
)

// mcpTools is the whole tool list. The route of each tool is looked up in
// apiRoutes, which also gives the permission the tool needs.
var mcpTools = []mcpTool{
	// Events
	{name: "list_events", desc: "List the occurrences of events between two times, series expanded.", method: "GET", path: "/events", params: []mcpParam{
		req("from", "string", "Start of the range, RFC 3339"), req("to", "string", "End of the range, RFC 3339, at most 400 days after from"), opt("person", "string", "Only events of this person id")}},
	{name: "create_event", desc: "Create an event or a series.", method: "POST", path: "/events", params: append([]mcpParam{
		req("title", "string", "Event title"), req("start", "string", "RFC 3339, or YYYY-MM-DD for an all-day event"), req("end", "string", "RFC 3339, or YYYY-MM-DD (exclusive) for an all-day event"),
		opt("all_day", "boolean", "An all-day event"), opt("timezone", "string", "IANA time zone of the event"), opt("calendar_id", "string", "The calendar to put it in (default: the family calendar)")}, eventFields...)},
	{name: "update_event", desc: "Change an event, or occurrences of a series.", method: "PATCH", path: "/events/{id}", params: append([]mcpParam{
		idOf("event"), scopeParam, occParam, opt("title", "string", "Event title"), opt("start", "string", "RFC 3339, or YYYY-MM-DD"), opt("end", "string", "RFC 3339, or YYYY-MM-DD")}, eventFields...)},
	{name: "delete_event", desc: "Delete an event, or occurrences of a series.", method: "DELETE", path: "/events/{id}", params: []mcpParam{idOf("event"), scopeParam, occParam}},
	{name: "search_events", desc: "Search events by text.", method: "GET", path: "/search", params: []mcpParam{req("q", "string", "Search text")}},
	{name: "list_holidays", desc: "List public holidays of the home region.", method: "GET", path: "/holidays", params: []mcpParam{
		req("from", "string", "First day, YYYY-MM-DD"), req("to", "string", "Last day, YYYY-MM-DD, at most three years after from")}},
	{name: "list_calendars", desc: "List the calendars of the family with their audience.", method: "GET", path: "/calendars"},

	// Lists
	{name: "list_lists", desc: "List the household's lists (shopping and others) with their number of entries.", method: "GET", path: "/lists"},
	{name: "get_list", desc: "Get one list with its entries in the order the list shows them.", method: "GET", path: "/lists/{id}", params: []mcpParam{idOf("list")}},
	{name: "create_list", desc: "Create a list.", method: "POST", path: "/lists", params: []mcpParam{
		req("name", "string", "List name (at most 60 characters)"), req("color", "string", "terracotta, blue, green, ochre, plum or teal"),
		req("symbol", "string", "circle-filled, circle-empty, square-filled, square-empty, triangle-filled or diamond-filled"), opt("sort", "string", "manual (default) or alphabetical")}},
	{name: "update_list", desc: "Rename a list or change its colour, symbol or sorting.", method: "PATCH", path: "/lists/{id}", params: []mcpParam{
		idOf("list"), opt("name", "string", "List name"), opt("color", "string", "terracotta, blue, green, ochre, plum or teal"), opt("symbol", "string", "The list symbol"), opt("sort", "string", "manual or alphabetical")}},
	{name: "delete_list", desc: "Delete a list with all its entries.", method: "DELETE", path: "/lists/{id}", params: []mcpParam{idOf("list")}},
	{name: "add_list_entry", desc: "Add an entry at the end of a list, for example an item on the shopping list.", method: "POST", path: "/lists/{id}/entries", params: []mcpParam{
		idOf("list"), req("text", "string", "The entry text; the exact name of an article of the library (any language, singular or plural) makes it that article"), opt("variety", "string", "A variety of the article, for example Type 405 for flour (at most 60 characters). Only for an article; quantities add up only for the same variety")}},
	{name: "delete_list_entry", desc: "Delete one entry of a list.", method: "DELETE", path: "/lists/{id}/entries/{entry}", params: []mcpParam{idOf("list"), req("entry", "string", "The id of the entry")}},
	{name: "set_list_entry_done", desc: "Tick an entry off, or open it again.", method: "PUT", path: "/lists/{id}/entries/{entry}/done", params: []mcpParam{
		idOf("list"), req("entry", "string", "The id of the entry"), req("done", "boolean", "true to tick off, false to open again")}},
	{name: "move_list_entry", desc: "Move an entry to a place of a manually sorted list.", method: "PUT", path: "/lists/{id}/entries/{entry}/position", params: []mcpParam{
		idOf("list"), req("entry", "string", "The id of the entry"), req("position", "integer", "The new place, counted from 0")}},
	{name: "delete_completed_list_entries", desc: "Delete all ticked-off entries of a list.", method: "DELETE", path: "/lists/{id}/completed-entries", params: []mcpParam{idOf("list")}},

	// Tasks, routines, templates and stars
	{name: "list_tasks", desc: "List the household's tasks with their state on one day.", method: "GET", path: "/tasks", params: []mcpParam{
		opt("date", "string", "The day, YYYY-MM-DD (default today)"), opt("person", "string", "Only tasks of this person id")}},
	{name: "create_task", desc: "Create a task for a person.", method: "POST", path: "/tasks", params: []mcpParam{
		req("person_id", "string", "The person id"), req("title", "string", taskTitle), req("repeat", "string", "once, daily or weekly"),
		opt("date", "string", "For once: the day, YYYY-MM-DD"), opt("weekdays", "strings", "For weekly: MO, TU, WE, TH, FR, SA, SU"), opt("emoji", "string", "One emoji"),
		opt("stars", "integer", "Stars earned"), opt("notes", "string", "Notes"), opt("part_of_day", "string", "morning, evening or empty for any time"), opt("routine_id", "string", "Put it into this routine")}},
	{name: "update_task", desc: "Change a task's star value.", method: "PATCH", path: "/tasks/{id}", params: []mcpParam{idOf("task"), req("stars", "integer", "Stars earned")}},
	{name: "delete_task", desc: "Delete a task with its history.", method: "DELETE", path: "/tasks/{id}", params: []mcpParam{idOf("task")}},
	{name: "set_task_done", desc: "Tick a task off, or open it again.", method: "PUT", path: "/tasks/{id}/done", params: []mcpParam{idOf("task"), req("done", "boolean", "true to tick off, false to open again")}},
	{name: "list_pauses", desc: "List the pause days: spans of days on which routines do not count. No person means the whole family.", method: "GET", path: "/tasks/pauses"},
	{name: "create_pause", desc: "Enter pause days (for example an illness or a holiday); past days are fine. Overlapping pauses of the same people are merged.", method: "POST", path: "/tasks/pauses", params: []mcpParam{
		req("from", "string", "First day, YYYY-MM-DD"), req("to", "string", "Last day, YYYY-MM-DD, at most 366 days long"), opt("person_ids", "strings", "The people it is for; leave out for the whole family")}},
	{name: "update_pause", desc: "Change the days or the people of a pause.", method: "PATCH", path: "/tasks/pauses/{id}", params: []mcpParam{
		idOf("pause"), opt("from", "string", "First day, YYYY-MM-DD"), opt("to", "string", "Last day, YYYY-MM-DD"), opt("person_ids", "strings", "The people it is for; an empty list means the whole family")}},
	{name: "delete_pause", desc: "Remove a pause. The days count normally again; what was ticked off stays.", method: "DELETE", path: "/tasks/pauses/{id}", params: []mcpParam{idOf("pause")}},
	{name: "list_routines", desc: "List the routines with their tasks in order.", method: "GET", path: "/routines", params: []mcpParam{opt("person", "string", "Only routines of this person id")}},
	{name: "get_routine", desc: "Get one routine with its tasks in order.", method: "GET", path: "/routines/{id}", params: []mcpParam{idOf("routine")}},
	{name: "create_routine", desc: "Create a routine for a person, or from a routine template.", method: "POST", path: "/routines", params: []mcpParam{
		req("person_id", "string", "The person id"), opt("name", "string", "Routine name"), opt("part_of_day", "string", "morning, afterschool or evening"), opt("template_id", "string", "Make it from this routine template")}},
	{name: "update_routine", desc: "Rename a routine or change its part of the day.", method: "PATCH", path: "/routines/{id}", params: []mcpParam{
		idOf("routine"), opt("name", "string", "Routine name"), opt("part_of_day", "string", "morning, afterschool or evening")}},
	{name: "dissolve_routine", desc: "Dissolve a routine; its tasks stay.", method: "DELETE", path: "/routines/{id}", params: []mcpParam{idOf("routine")}},
	{name: "add_task_to_routine", desc: "Take a person's single task into a routine, at the end.", method: "POST", path: "/routines/{id}/tasks", params: []mcpParam{idOf("routine"), req("task_id", "string", "The task id")}},
	{name: "remove_task_from_routine", desc: "Take a task out of a routine.", method: "DELETE", path: "/routines/{id}/tasks/{task_id}", params: []mcpParam{idOf("routine"), req("task_id", "string", "The task id")}},
	{name: "set_routine_order", desc: "Put a routine's tasks in a new order.", method: "PUT", path: "/routines/{id}/order", params: []mcpParam{idOf("routine"), req("task_ids", "strings", "All task ids of the routine, in the new order")}},
	{name: "list_routine_templates", desc: "List routine templates, own first, then the ones that come with Hejmo.", method: "GET", path: "/routine-templates"},
	{name: "get_routine_template", desc: "Get one routine template with its steps.", method: "GET", path: "/routine-templates/{id}", params: []mcpParam{idOf("template")}},
	{name: "save_routine_as_template", desc: "Save a routine as an own template.", method: "POST", path: "/routine-templates", params: []mcpParam{
		req("routine_id", "string", "The routine id"), opt("name", "string", "Template name")}},
	{name: "update_routine_template", desc: "Rename an own template or change its suggested part of the day.", method: "PATCH", path: "/routine-templates/{id}", params: []mcpParam{
		idOf("template"), opt("name", "string", "Template name"), opt("part_of_day", "string", "morning, afterschool or evening")}},
	{name: "delete_routine_template", desc: "Delete an own routine template.", method: "DELETE", path: "/routine-templates/{id}", params: []mcpParam{idOf("template")}},
	{name: "add_routine_template_step", desc: "Add a step at the end of an own template.", method: "POST", path: "/routine-templates/{id}/steps", params: []mcpParam{
		idOf("template"), req("title", "string", "Step title"), req("repeat", "string", "daily or weekly"), opt("weekdays", "strings", "For weekly: MO, TU, WE, TH, FR, SA, SU"), opt("emoji", "string", "One emoji"), opt("stars", "integer", "Stars earned")}},
	{name: "update_routine_template_step", desc: "Change a step of an own template.", method: "PATCH", path: "/routine-templates/{id}/steps/{step_id}", params: []mcpParam{
		idOf("template"), req("step_id", "string", "The step id"), opt("title", "string", "Step title"), opt("repeat", "string", "daily or weekly"), opt("weekdays", "strings", "MO, TU, WE, TH, FR, SA, SU"), opt("emoji", "string", "One emoji"), opt("stars", "integer", "Stars earned")}},
	{name: "delete_routine_template_step", desc: "Remove a step from an own template.", method: "DELETE", path: "/routine-templates/{id}/steps/{step_id}", params: []mcpParam{idOf("template"), req("step_id", "string", "The step id")}},
	{name: "set_routine_template_order", desc: "Put the steps of an own template in a new order.", method: "PUT", path: "/routine-templates/{id}/order", params: []mcpParam{idOf("template"), req("step_ids", "strings", "All step ids, in the new order")}},
	{name: "get_stars", desc: "Get a person's star balance and star log.", method: "GET", path: "/people/{id}/stars", params: []mcpParam{
		req("id", "string", "The person id"), opt("limit", "integer", "How many log entries, 1 to 200 (default 50)")}},
	{name: "get_badges", desc: "Get a person's badges (ten fixed milestones, earned once and never taken away), newest first, with whether each is new and whether the person's switch \"week count and badges\" is on. The list is empty while the switch is off. Read only.", method: "GET", path: "/people/{id}/badges", params: []mcpParam{idOf("person")}},
	{name: "book_stars", desc: "Book stars by hand with a note: positive adds, negative takes away.", method: "POST", path: "/people/{id}/stars", params: []mcpParam{
		req("id", "string", "The person id"), req("delta", "integer", "Stars to add (positive) or take away (negative)"), req("note", "string", "Why")}},
	{name: "get_savings_goal", desc: "Get the reward a person saves for, with their balance and whether it is reached.", method: "GET", path: "/people/{id}/savings-goal", params: []mcpParam{idOf("person")}},
	{name: "set_savings_goal", desc: "Choose the reward a person saves for: an offered reward meant for them. A child token may only set its own.", method: "PUT", path: "/people/{id}/savings-goal", params: []mcpParam{
		idOf("person"), req("reward_id", "string", "The reward id")}},
	{name: "clear_savings_goal", desc: "Clear a person's savings goal; their stars stay.", method: "DELETE", path: "/people/{id}/savings-goal", params: []mcpParam{idOf("person")}},

	// Reward catalogue
	{name: "list_rewards", desc: "List the reward catalogue by price (for a child: only what is meant for them, with what they can afford).", method: "GET", path: "/rewards"},
	{name: "get_reward", desc: "Get one reward.", method: "GET", path: "/rewards/{id}", params: []mcpParam{idOf("reward")}},
	{name: "create_reward", desc: "Create a reward children trade stars for. Say who it is for: whole_family or person_ids.", method: "POST", path: "/rewards", params: append([]mcpParam{
		req("name", "string", "Reward name (at most 60 characters)"), req("price", "integer", "Stars, 1 to 999")}, rewardFields...)},
	{name: "update_reward", desc: "Change an offered reward (an archived one must be restored first).", method: "PATCH", path: "/rewards/{id}", params: append([]mcpParam{
		idOf("reward"), opt("name", "string", "Reward name"), opt("price", "integer", "Stars, 1 to 999; applies from now on")}, rewardFields...)},
	{name: "archive_reward", desc: "Archive a reward: children no longer see it, redemptions stay in the star logs.", method: "POST", path: "/rewards/{id}/archive", params: []mcpParam{idOf("reward")}},
	{name: "restore_reward", desc: "Offer an archived reward again. One archived because its people left needs whole_family or person_ids.", method: "POST", path: "/rewards/{id}/restore", params: []mcpParam{
		idOf("reward"), opt("whole_family", "boolean", "Meant for the whole family"), opt("person_ids", "strings", "Person ids it is meant for")}},
	{name: "delete_reward", desc: "Delete a reward that was never redeemed; a redeemed one must be archived.", method: "DELETE", path: "/rewards/{id}", params: []mcpParam{idOf("reward")}},

	// Redeeming rewards (ticket 292)
	{name: "list_reward_requests", desc: "List the open requests to redeem rewards, oldest first. (Children ask and withdraw in the app; MCP is for parents.)", method: "GET", path: "/rewards/requests"},
	{name: "confirm_reward_request", desc: "As a parent: say yes. Balance and limit are checked again and the price is booked; a second decision books nothing.", method: "POST", path: "/rewards/requests/{id}/confirm", params: []mcpParam{idOf("request")}},
	{name: "decline_reward_request", desc: "As a parent: say no. The child keeps the stars and sees the optional note.", method: "POST", path: "/rewards/requests/{id}/decline", params: []mcpParam{
		idOf("request"), opt("note", "string", "Why not (at most 120 characters); the child sees it")}},
	{name: "redeem_reward", desc: "As a parent: redeem a reward for a person now, without a request. An open request of that person for the reward is confirmed instead of a second booking.", method: "POST", path: "/rewards/{id}/redeem", params: []mcpParam{
		idOf("reward"), req("person_id", "string", "The person who redeems")}},

	// Meal plan
	{name: "list_meals", desc: "List the meal plan from one day to another (default: the coming seven days).", method: "GET", path: "/meals", params: []mcpParam{
		opt("from", "string", "First day, YYYY-MM-DD (give both from and to, or neither)"), opt("to", "string", "Last day, YYYY-MM-DD, at most 62 days after from")}},
	{name: "set_meal", desc: "Plan a meal, or change the planned one: free text, or a recipe with portions.", method: "PUT", path: "/meals/{date}/{mealtime}", params: []mcpParam{mealDate, mealtime,
		opt("text", "string", "What there is to eat as free text (at most 200 characters); not with recipe_id"),
		opt("recipe_id", "string", "A recipe of the family (see list_recipes) instead of text"),
		opt("portions", "integer", "With recipe_id: 1 to 99; the number of people in the household when missing")}},
	{name: "delete_meal", desc: "Remove a planned meal.", method: "DELETE", path: "/meals/{date}/{mealtime}", params: []mcpParam{mealDate, mealtime}},
	{name: "restore_meal", desc: "Back to the template: drop this week's change of a meal.", method: "POST", path: "/meals/{date}/{mealtime}/restore", params: []mcpParam{mealDate, mealtime}},
	{name: "preview_meal_shopping", desc: "Look first: what putting the ingredients of one planned recipe meal (meal) or of a week from today on (week) on a shopping list would do. Scaled to the planned portions; kitchen measures go on without a quantity; pantry articles are not ticked. Returns the rows with their outcome, the counts and the recipe versions to send back when adding.", method: "GET", path: "/meal-shopping", params: []mcpParam{
		qry("meal", "string", "One meal as <day>.<mealtime>, for example 2026-11-17.dinner; not with week"), qry("week", "string", "A day of the week or an ISO week such as 2026-W47; not with meal"), qry("list_id", "string", "A list with suggestions; the list last used for meals when missing")}},
	{name: "add_meal_shopping", desc: "Put the ingredients of a meal or a week on a shopping list through the same rule as the app: nothing lands twice, a meal already on a list adds only its missing portions. Send the versions from preview_meal_shopping: if a recipe changed, nothing is added. Returns a run_id for undo_meal_shopping.", method: "POST", path: "/meal-shopping", params: []mcpParam{
		opt("meal", "string", "One meal as <day>.<mealtime>; not with week"), opt("week", "string", "A day of the week or an ISO week; not with meal"), req("list_id", "string", "The list to put them on"),
		opt("meals", "strings", "With week: the meal keys to take; all by default"), opt("picks", "strings", "The row keys to add; the rows the preview ticks by default"), opt("versions", "object", "Recipe id to version, from the preview")}},
	{name: "undo_meal_shopping", desc: "Take back exactly one run of add_meal_shopping: new entries go, added quantities come off. Entries ticked off meanwhile stay.", method: "POST", path: "/meal-shopping/runs/{id}/undo", params: []mcpParam{idOf("run")}},
	{name: "list_diets_and_allergies", desc: "List every person's diet (omnivore, vegetarian, pescatarian, vegan) and allergies: the 14 main EU allergens by key, plus free text that is not checked automatically and cannot be filtered by. Health data, read only.", method: "GET", path: "/food"},
	{name: "get_diet_and_allergies", desc: "Get one person's diet and allergies. Free-text allergies (other) are not checked automatically and cannot be filtered by. Health data, read only.", method: "GET", path: "/people/{id}/food", params: []mcpParam{idOf("person")}},
	{name: "list_recipes", desc: "List the household's own recipes from A to Z; q searches name, ingredients and tags.", method: "GET", path: "/recipes", params: []mcpParam{
		opt("q", "string", "Search text"), opt("category", "string", "main, side, dessert, baking, breakfast or snack")}},
	{name: "list_starter_recipes", desc: "List the 200 starter recipes that ship with Hejmo (read only, in the language of the request, each with its source); get one with get_recipe by its id, which starts with starter-.", method: "GET", path: "/starter-recipes", params: []mcpParam{
		opt("q", "string", "Search text"), opt("category", "string", "main, side, dessert, baking, breakfast or snack")}},
	{name: "get_recipe", desc: "Get one recipe with its ingredients and its steps in order.", method: "GET", path: "/recipes/{id}", params: []mcpParam{idOf("recipe")}},
	{name: "create_recipe", desc: "Write a recipe. Only the name is required. Warnings about allergies never block it.", method: "POST", path: "/recipes", params: recipeBody},
	{name: "update_recipe", desc: "Replace a recipe (give all fields): its ingredients, steps and tags are replaced.", method: "PUT", path: "/recipes/{id}", params: append([]mcpParam{idOf("recipe")}, recipeBody...)},
	{name: "delete_recipe", desc: "Delete a recipe with its ingredients, steps and tips.", method: "DELETE", path: "/recipes/{id}", params: []mcpParam{idOf("recipe")}},
	{name: "read_recipe_page", desc: "Read a recipe from a web page (schema.org/Recipe) into a draft that lives one hour; nothing is saved yet. Answers 409 when the recipes connection is off or offline mode is on. Check the draft with get_recipe_draft; lines with status needs_choice need a choice when saved.", method: "POST", path: "/recipe-imports", params: []mcpParam{
		req("url", "string", "The address of the web page (https:// or http://, on the internet)")}},
	{name: "get_recipe_draft", desc: "Get a draft read from a web page: the recipe as the page described it, each ingredient line with what the page wrote, what Hejmo made of it and up to three proposals of the library for a line it does not know.", method: "GET", path: "/recipe-imports/{id}", params: []mcpParam{idOf("draft"),
		qry("candidate", "integer", "Which recipe of the page when it holds several (default 0)")}},
	{name: "save_recipe_draft", desc: "Save a draft as a recipe, as it should be stored (usually the draft's, with changes). Every ingredient that is no article of the library needs choice own (make an own article without allergens) or free (keep free text); without it nothing is saved. The source is the page's address and never changes.", method: "POST", path: "/recipe-imports/{id}", params: []mcpParam{idOf("draft"),
		opt("candidate", "integer", "Which recipe of the page (default 0)"), req("name", "string", "Recipe name (at most 120 characters), unique ignoring case"),
		opt("category", "string", "main (default), side, dessert, baking, breakfast or snack"), opt("portions", "integer", "1 to 99"), opt("minutes", "integer", "0 to 9999"),
		opt("mealtimes", "strings", "breakfast, lunch, snack or dinner; none means every mealtime"), opt("tags", "strings", "Tags to keep, at most 30 characters each; the page's keywords are none until given here"), opt("tips", "string", "Tips"),
		opt("ingredients", "array", "A list of {quantity, unit, name, article_id, variety, choice (own or free, for a line that is no library article)}"),
		opt("steps", "strings", "The steps in order"), opt("picture", "boolean", "Keep the page's picture as the cover (default true)")}},
	{name: "discard_recipe_draft", desc: "Discard a draft read from a web page.", method: "DELETE", path: "/recipe-imports/{id}", params: []mcpParam{idOf("draft")}},
	{name: "list_recipe_photos", desc: "List the own photos of a recipe (an own or a starter recipe), the cover first, then the newest first. The image of a photo is at its image_path.", method: "GET", path: "/recipes/{id}/photos", params: []mcpParam{idOf("recipe")}},
	{name: "set_recipe_photo_cover", desc: "Make a photo the cover of its recipe: it shows at the top of the recipe and in the recipe list.", method: "POST", path: "/recipes/{id}/photos/{photo_id}/cover", params: []mcpParam{idOf("recipe"), req("photo_id", "string", "The id of the photo (see list_recipe_photos)")}},
	{name: "delete_recipe_photo", desc: "Remove a photo of a recipe for the whole family. When it was the cover, the newest remaining photo becomes the cover.", method: "DELETE", path: "/recipes/{id}/photos/{photo_id}", params: []mcpParam{idOf("recipe"), req("photo_id", "string", "The id of the photo (see list_recipe_photos)")}},
	{name: "set_recipe_favourites", desc: "Say who likes a recipe (\"Mia likes it\"): the recipe is afterwards a favourite of exactly these people, of nobody with an empty list. The meal plan suggests favourites first. Own recipes only. last_cooked in a recipe is the latest planned day that is over.", method: "PUT", path: "/recipes/{id}/favourites", params: []mcpParam{
		idOf("recipe"), req("person_ids", "strings", "The ids of the people who like the recipe")}},
	{name: "list_recipe_units", desc: "List the units an ingredient can have: the household's, kitchen measures and containers.", method: "GET", path: "/recipe-units"},
	{name: "get_recipe_nutrition", desc: "The seven nutrition values of the EU declaration (energy, fat, saturated fat, carbohydrate, sugars, protein, salt) per portion of a recipe, calculated from its ingredients (BLS 4.0, USDA). Guide values. A value that is null is unknown, never 0; not_counted lists the ingredients that could not be counted.", method: "GET", path: "/recipes/{id}/nutrition", params: []mcpParam{idOf("recipe")}},
	{name: "get_recipe_warnings", desc: "Who in the family has to look out for what in a recipe. Health data, read only. Free-text ingredients and free-text allergies are not checked automatically. Allergen information without guarantee. The ingredient list on the pack is what counts.", method: "GET", path: "/recipes/{id}/warnings", params: []mcpParam{idOf("recipe")}},
	{name: "list_meal_templates", desc: "List the meal plan templates, by name.", method: "GET", path: "/meal-templates"},
	{name: "get_meal_template", desc: "Get one meal plan template.", method: "GET", path: "/meal-templates/{id}", params: []mcpParam{idOf("template")}},
	{name: "create_meal_template", desc: "Create a meal plan template.", method: "POST", path: "/meal-templates", params: mealTplBody},
	{name: "update_meal_template", desc: "Replace a meal plan template (give all fields).", method: "PUT", path: "/meal-templates/{id}", params: append([]mcpParam{idOf("template")}, mealTplBody...)},
	{name: "delete_meal_template", desc: "Delete a meal plan template.", method: "DELETE", path: "/meal-templates/{id}", params: []mcpParam{idOf("template")}},
}

// MCPTool is what the contract test needs to know about a tool.
type MCPTool struct {
	Name, Method, Path string
	Permission         auth.Permission
}

// MCPTools lists the tools of the MCP server with the REST route each one runs.
func MCPTools() []MCPTool {
	out := make([]MCPTool, 0, len(mcpTools))
	for _, t := range mcpTools {
		out = append(out, MCPTool{t.name, t.method, t.path, mustRoute(t.method, t.path).perm})
	}
	return out
}

func mustRoute(method, path string) APIRoute {
	for _, r := range apiRoutes {
		if r.Method == method && r.Path == path {
			return r
		}
	}
	panic(fmt.Sprintf("mcp: no REST route %s %s", method, path))
}

// schema is the JSON schema of the tool's arguments.
func (t mcpTool) schema() map[string]any {
	props, required := map[string]any{}, []string{}
	for _, p := range t.params {
		prop := map[string]any{"description": p.desc}
		switch p.typ {
		case "strings":
			prop["type"], prop["items"] = "array", map[string]any{"type": "string"}
		case "array":
			prop["type"], prop["items"] = "array", map[string]any{"type": "object"}
		default:
			prop["type"] = p.typ
		}
		props[p.name] = prop
		if p.required {
			required = append(required, p.name)
		}
	}
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// mcpServerFor builds the MCP server of one request: it holds only the tools
// the token's scopes allow.
func (s *server) mcpServerFor(p principal) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "hejmo", Version: "0.1"}, &mcp.ServerOptions{Instructions: mcpInstructions})
	no := false
	for _, t := range mcpTools {
		route := mustRoute(t.method, t.path)
		if !p.may(route.perm) {
			continue
		}
		ann := &mcp.ToolAnnotations{ReadOnlyHint: t.method == http.MethodGet, OpenWorldHint: &no, IdempotentHint: t.method == http.MethodGet || t.method == http.MethodPut || t.method == http.MethodDelete}
		if t.method != http.MethodGet {
			destructive := t.method == http.MethodDelete
			ann.DestructiveHint = &destructive
		}
		srv.AddTool(&mcp.Tool{Name: t.name, Description: t.desc, InputSchema: t.schema(), Annotations: ann}, s.mcpCall(p, t, route))
	}
	return srv
}

func toolError(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}}}
}

// recorder collects the answer of a REST handler.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *recorder) Write(b []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return r.body.Write(b)
}

// mcpCall runs the REST handler of a tool's route for the token's person and scopes.
func (s *server) mcpCall(p principal, t mcpTool, route APIRoute) mcp.ToolHandler {
	return func(ctx context.Context, call *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Defence in depth: only allowed tools are registered, so this is not reached in
		// practice. It is the same check the REST API makes, kept in case a tool is ever
		// registered without the scope filter.
		if !p.may(route.perm) {
			return toolError("forbidden: this token does not allow %s", route.perm), nil
		}
		args := map[string]json.RawMessage{}
		if len(call.Params.Arguments) > 0 {
			if err := json.Unmarshal(call.Params.Arguments, &args); err != nil {
				return toolError("the arguments must be a JSON object"), nil
			}
		}
		known := map[string]mcpParam{}
		for _, prm := range t.params {
			known[prm.name] = prm
		}
		for name := range args {
			if _, ok := known[name]; !ok {
				return toolError("unknown argument %q", name), nil
			}
		}
		path, query, body := route.Path, url.Values{}, map[string]json.RawMessage{}
		pathValues := map[string]string{}
		for _, prm := range t.params {
			raw, given := args[prm.name]
			if !given || string(raw) == "null" {
				if prm.required {
					return toolError("missing argument %q", prm.name), nil
				}
				continue
			}
			switch {
			case strings.Contains(route.Path, "{"+prm.name+"}"):
				var v string
				if err := json.Unmarshal(raw, &v); err != nil || v == "" {
					return toolError("argument %q must be a non-empty string", prm.name), nil
				}
				pathValues[prm.name] = v
				path = strings.ReplaceAll(path, "{"+prm.name+"}", url.PathEscape(v))
			case prm.query || t.method == http.MethodGet:
				query.Set(prm.name, scalar(raw))
			default:
				body[prm.name] = raw
			}
		}
		var payload []byte
		if len(body) > 0 {
			payload, _ = json.Marshal(body)
		}
		target := "/api/v1" + path
		if len(query) > 0 {
			target += "?" + query.Encode()
		}
		hr, err := http.NewRequestWithContext(ctx, t.method, target, bytes.NewReader(payload))
		if err != nil {
			return toolError("cannot build the request: %v", err), nil
		}
		if payload != nil {
			hr.Header.Set("Content-Type", "application/json")
		}
		for name, v := range pathValues {
			hr.SetPathValue(name, v)
		}
		rec := &recorder{header: http.Header{}}
		route.handle(s, rec, hr, p)
		text := strings.TrimSpace(rec.body.String())
		if rec.status >= 400 {
			var e struct {
				Error string `json:"error"`
			}
			if json.Unmarshal([]byte(text), &e) == nil && e.Error != "" {
				text = e.Error
			}
			return toolError("%s (HTTP %d)", text, rec.status), nil
		}
		if text == "" {
			text = "Done."
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

// scalar writes a JSON string, number or boolean as the text of a query value.
func scalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

// handleMCPEndpoint answers the MCP protocol at /mcp. Order matters: a box
// with MCP switched off tells a client nothing else, and only an API token
// created for MCP, of a parent, gets in.
func (s *server) handleMCPEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil {
		writeError(w, http.StatusForbidden, "MCP is served over HTTPS only")
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil || !s.db.Configured() || !h.MCPEnabled {
		writeError(w, http.StatusForbidden, "MCP is switched off")
		return
	}
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	token, err := s.db.APITokenByHash(auth.HashToken(strings.TrimSpace(secret)))
	if !ok || err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="hejmo"`)
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !token.MCP {
		writeError(w, http.StatusForbidden, "this token was not created for MCP")
		return
	}
	person, err := s.db.Person(h.ID, token.PersonID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && person.Role != store.RoleParent) {
		writeError(w, http.StatusForbidden, "only the token of a parent works with MCP")
		return
	}
	if err != nil {
		s.fail(w, "mcp person", err)
		return
	}
	s.touchToken(token)
	p := principal{household: h, person: person, scopes: token.Scopes}
	// Stateless: every request is checked on its own, so switching MCP off or
	// revoking a token takes effect at once, also for a client with an open session.
	mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcpServerFor(p) },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}).ServeHTTP(w, r)
}
