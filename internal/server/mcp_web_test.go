package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// Ticket 7.7, preview-96 (approved 15/11/2026, Q10): the page "AI assistants
// (MCP)" in the Settings hub. Texts are checked in English; one test reads the
// German page too.

var (
	mcpSecretRe  = regexp.MustCompile(`data-device-token="([^"]+)"`)
	mcpTokenHref = regexp.MustCompile(`href="/mcp/tokens/([0-9a-f-]{36})"`)
)

func postEN(b *box, path string, form url.Values) resp {
	b.t.Helper()
	return b.do(http.MethodPost, path, form, map[string]string{"Accept-Language": "en"})
}

func mcpHas(t *testing.T, where, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("%s lacks %q\n%s", where, want, body)
		}
	}
}

func mcpHasNot(t *testing.T, where, body string, nots ...string) {
	t.Helper()
	for _, not := range nots {
		if strings.Contains(body, not) {
			t.Errorf("%s shows %q\n%s", where, not, body)
		}
	}
}

func switchMCPOnPage(t *testing.T, b *box, on bool) resp {
	t.Helper()
	form := url.Values{}
	if on {
		form.Set("mcp", "on")
	}
	r := postEN(b, "/mcp", form)
	if r.status != http.StatusSeeOther || r.location != "/mcp" {
		t.Fatalf("POST /mcp = %d %s", r.status, r.location)
	}
	return r
}

// newMCPTokenOnPage creates a token through the form and returns its id and secret.
func newMCPTokenOnPage(t *testing.T, b *box, name string, levels map[string]string) (id, secret string) {
	t.Helper()
	form := url.Values{"name": {name}}
	for _, area := range []string{"events", "lists", "tasks", "meals"} {
		level := levels[area]
		if level == "" {
			level = "none"
		}
		form.Set(area, level)
	}
	r := postEN(b, "/mcp/tokens", form)
	m, h := mcpSecretRe.FindStringSubmatch(r.body), mcpTokenHref.FindStringSubmatch(r.body)
	if r.status != http.StatusOK || m == nil || h == nil {
		t.Fatalf("create MCP token = %d\n%s", r.status, r.body)
	}
	return h[1], m[1]
}

func TestSettingsHubRowSaysInWordsWhetherMCPIsOn(t *testing.T) {
	b := signedInBox(t)
	body := getEN(b, "/settings").body
	mcpHas(t, "hub", body, `<a class="hub-row" href="/mcp">`, "<strong>AI assistants (MCP)</strong>", `<b>Off.</b> Let an AI assistant`)
	// After Phones, because it is another access from outside.
	if strings.Index(body, `href="/phones"`) > strings.Index(body, `href="/mcp"`) || strings.Index(body, `href="/mcp"`) > strings.Index(body, `href="/people"`) {
		t.Error("the MCP row is not between Phones and People")
	}
	switchMCPOnPage(t, b, true)
	mcpHas(t, "hub", getEN(b, "/settings").body, `<b>On.</b> Let an AI assistant`)
}

func TestMCPPageFirstVisitExplainsBeforeTheSwitch(t *testing.T) {
	b := signedInBox(t)
	r := getEN(b, "/mcp")
	if r.status != http.StatusOK {
		t.Fatalf("GET /mcp = %d", r.status)
	}
	mcpHas(t, "/mcp", r.body,
		`<a class="back" href="/settings">`, "<h1>AI assistants (MCP)</h1>", "As long as MCP is off, no assistant can reach your data.",
		`<section class="explain" aria-labelledby="what">`, "<h2 id=\"what\">What is MCP?</h2>", "<b>MCP</b> (Model Context Protocol)",
		"Put milk and oats on the shopping list.", "What an assistant may do", "Good to know",
		`<ul class="links">`, `href="https://github.com/hejmo-home/hejmo/blob/main/docs/mcp.md"`, `href="https://modelcontextprotocol.io/docs/getting-started/intro"`, "(English)",
		`<form method="post" action="/mcp" class="steps">`, "Off: No AI assistant can reach your data.", `name="mcp"`, `role="switch"`,
		`<button class="button" type="submit">Save</button>`,
	)
	mcpHasNot(t, "/mcp", r.body, "MCP tokens</h2>", "<details class=\"more explain-more\">", "Create the first MCP token", ` checked`)
	if strings.Index(r.body, `class="explain"`) > strings.Index(r.body, `action="/mcp"`) {
		t.Error("the explanation comes after the switch on the first visit")
	}
	if strings.Contains(r.body, `class="danger"`) {
		t.Error("switching MCP off or on needs no danger box")
	}
}

func TestMCPSwitchedOnShowsTheAddressAndInvitesToTheFirstToken(t *testing.T) {
	b := signedInBox(t)
	switchMCPOnPage(t, b, true)
	r := getEN(b, "/mcp")
	mcpHas(t, "/mcp (on)", r.body,
		"On: AI assistants with an MCP token reach the box at the address below.", `class="state is-on"`, ` checked`,
		"<dt>MCP address</dt>", "https://127.0.0.1", "/mcp</dd>",
		`<button class="button secondary" type="submit">Save</button>`,
		`<h2 id="tokens">MCP tokens</h2>`, `<div class="empty" data-empty>`, "No MCP token yet.", `<a class="button" href="/mcp/tokens/new">Create the first MCP token</a>`,
		`<section class="explain" aria-labelledby="what">`,
		// The box has its own certificate: the device has to trust it first.
		`<p class="notice" role="status">`, `<a href="/ca">Trust the box</a>`,
	)
	// The success line names the next step.
	b.logout()
	b2 := signedInBox(t)
	r2 := switchMCPOnPage(t, b2, true)
	_ = r2
	mcpHas(t, "after switching on", getEN(b2, "/mcp").body, "MCP is on. Create a token for your assistant now.")
	if again := getEN(b2, "/mcp").body; strings.Contains(again, "MCP is on. Create") {
		t.Error("the success line shows twice")
	}
}

func TestMCPSwitchingOffNeedsNoConfirmationAndSaysSo(t *testing.T) {
	b := signedInBox(t)
	switchMCPOnPage(t, b, true)
	getEN(b, "/mcp")
	switchMCPOnPage(t, b, false)
	body := getEN(b, "/mcp").body
	mcpHas(t, "after switching off", body, "MCP is off. No AI assistant can reach your data any more.", `<button class="button" type="submit">Save</button>`)
	mcpHasNot(t, "after switching off", body, ` checked`)
}

func TestNewMCPTokenFormDefaultsEveryAreaToReadOnly(t *testing.T) {
	b := signedInBox(t)
	switchMCPOnPage(t, b, true)
	r := getEN(b, "/mcp/tokens/new")
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	mcpHas(t, "new token", r.body,
		`<a class="back" href="/mcp">`, "All MCP tokens", "<h1>New MCP token</h1>", "One token per assistant and device.",
		`<form method="post" action="/mcp/tokens" class="steps">`, `name="name"`, `maxlength="80"`, "required",
		"The assistant acts as you, ", `class="marker"`, ". What it enters, the box enters under your name.",
		`<fieldset class="scopes">`, "What may the assistant do?", "“Change” also means adding, ticking off and deleting.",
		"Events", "Lists", "Tasks (with routines and stars)", "Meal plan",
		"Never possible: settings, tokens, people, displays and phones.", `<button class="button" type="submit">Create token</button>`)
	for _, area := range []string{"events", "lists", "tasks", "meals"} {
		mcpHas(t, "area "+area, r.body,
			`<input type="radio" name="`+area+`" value="none">No`,
			`<input type="radio" name="`+area+`" value="read" checked>Read only`,
			`<input type="radio" name="`+area+`" value="write">Read and change`)
	}
	// The token belongs to the signed-in parent: nobody is chosen.
	mcpHasNot(t, "new token", r.body, `class="chips"`, `name="person`)
}

func TestNewMCPTokenIsShownOnceAndWorksOnlyForMCP(t *testing.T) {
	b := signedInBox(t)
	switchMCPOnPage(t, b, true)
	r := postEN(b, "/mcp/tokens", url.Values{"name": {"Claude on Mama's laptop"}, "events": {"write"}, "lists": {"write"}, "tasks": {"read"}, "meals": {"none"}})
	if r.status != http.StatusOK {
		t.Fatalf("create = %d\n%s", r.status, r.body)
	}
	mcpHas(t, "once page", r.body,
		"MCP token for “Claude on Mama&#39;s laptop” created.", "<h1>Claude on Mama&#39;s laptop</h1>", `<section class="once"`,
		"Enter the token in the AI assistant now.", "The box shows it only this once.", `class="secret"`, "Copy token",
		"<dt>MCP address</dt>", "/mcp</dd>", "some assistants call it “API key” or “bearer token”",
		"<dt>Reads and changes</dt>", "<dd>Events, Lists</dd>", "<dt>Reads only</dt>", "<dd>Tasks (with routines and stars)</dd>",
		`href="https://github.com/hejmo-home/hejmo/blob/main/docs/mcp.md"`, "Done, the token is entered")
	mcpHasNot(t, "once page", r.body, "Meal plan</dd>")
	secret := mcpSecretRe.FindStringSubmatch(r.body)[1]
	// Shown on this answer only, and never cached.
	if cc := b.do(http.MethodPost, "/mcp/tokens", url.Values{"name": {"Zwei"}, "events": {"read"}}, nil); cc.status != http.StatusOK {
		t.Fatalf("second create = %d", cc.status)
	}
	id := mcpTokenHref.FindStringSubmatch(r.body)[1]
	detail := getEN(b, "/mcp/tokens/"+id).body
	if strings.Contains(detail, secret) || strings.Contains(getEN(b, "/mcp").body, secret) {
		t.Error("the token secret shows again after the once page")
	}
	// It works for MCP, as the parent who made it, with exactly those areas.
	c := newAPIClient(t, b)
	c.cookie = true
	s := c.mcpSession(t, secret)
	names := toolNames(t, s)
	if !hasName(names, "create_event") || !hasName(names, "add_list_entry") || !hasName(names, "list_tasks") || hasName(names, "create_task") || hasName(names, "list_meals") {
		t.Errorf("tools = %v", names)
	}
}

func TestNewMCPTokenErrorsSitAtTheFieldAndKeepWhatWasTyped(t *testing.T) {
	b := signedInBox(t)
	switchMCPOnPage(t, b, true)
	// No name.
	r := postEN(b, "/mcp/tokens", url.Values{"name": {"  "}, "events": {"none"}, "lists": {"read"}, "tasks": {"none"}, "meals": {"none"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("no name = %d", r.status)
	}
	mcpHas(t, "no name", r.body, `<div class="f invalid">`, `aria-invalid="true"`, `id="err-name"`, "Give the token a name so you can find it again.",
		`<input type="radio" name="lists" value="read" checked>`)
	// No name and everything "No": both errors show at once, each at its place.
	r = postEN(b, "/mcp/tokens", url.Values{"name": {""}, "events": {"none"}, "lists": {"none"}, "tasks": {"none"}, "meals": {"none"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("both errors = %d", r.status)
	}
	mcpHas(t, "both errors", r.body, `<div class="f invalid">`, "Give the token a name so you can find it again.", `<fieldset class="scopes invalid" aria-describedby="err-scopes">`, `id="err-scopes"`)
	// Everything "No": the error frames the area block.
	r = postEN(b, "/mcp/tokens", url.Values{"name": {"Claude"}, "events": {"none"}, "lists": {"none"}, "tasks": {"none"}, "meals": {"none"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("all none = %d", r.status)
	}
	mcpHas(t, "all none", r.body, `<fieldset class="scopes invalid" aria-describedby="err-scopes">`, `id="err-scopes"`,
		"Choose “Read only” or “Read and change” for at least one area.", `value="Claude"`, `<input type="radio" name="events" value="none" checked>`)
	mcpHasNot(t, "all none", r.body, `class="f invalid"`)
	// A name that is taken.
	newMCPTokenOnPage(t, b, "Claude", map[string]string{"events": "read"})
	r = postEN(b, "/mcp/tokens", url.Values{"name": {" claude "}, "events": {"read"}, "lists": {"none"}, "tasks": {"none"}, "meals": {"none"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate = %d", r.status)
	}
	mcpHas(t, "duplicate", r.body, `<div class="f invalid">`, "There is already an MCP token with this name. Choose another one.")
	// Unknown values never widen the rights.
	r = postEN(b, "/mcp/tokens", url.Values{"name": {"Evil"}, "events": {"write"}, "lists": {"admin"}, "tasks": {"none"}, "meals": {"none"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Errorf("unknown level = %d", r.status)
	}
}

func TestMCPOverviewListsTokensAsCalmRowsWithWordsNotCounts(t *testing.T) {
	b := signedInBox(t)
	switchMCPOnPage(t, b, true)
	id, secret := newMCPTokenOnPage(t, b, "Claude on Mama's laptop", map[string]string{"events": "write", "lists": "write", "tasks": "read"})
	body := getEN(b, "/mcp").body
	mcpHas(t, "overview", body,
		`<h2 id="tokens">MCP tokens</h2>`, "Tap a token to see what it may do", `<ul class="list-rows">`, `<a href="/mcp/tokens/`+id+`">`, "<b>Claude on Mama&#39;s laptop</b>",
		"For ", `class="marker"`, "Reads and changes: Events, Lists", "Reads only: Tasks (with routines and stars)",
		`class="flag warn"`, "Never used. Is the token entered in the assistant?",
		`<a class="button" href="/mcp/tokens/new">Create a new MCP token</a>`,
		`<details class="more explain-more"><summary>What is MCP? What is it good for?</summary>`)
	mcpHasNot(t, "overview", body, secret, `<section class="explain"><h2`, "Create the first MCP token")
	// Once used, the row says when, with seconds.
	c := newAPIClient(t, b)
	c.mcpSession(t, secret)
	body = getEN(b, "/mcp").body
	if !regexp.MustCompile(`Last used \d\d\.\d\d\.\d{4},\x{00a0}\d{1,2}:\d\d:\d\d`).MatchString(body) || strings.Contains(body, "Never used") {
		t.Errorf("last use is not shown with seconds\n%s", body)
	}
	// MCP tokens are not device tokens: the calendar apps page does not list them.
	if cal := getEN(b, "/caldav").body; strings.Contains(cal, "Claude on Mama") {
		t.Error("the calendar apps page lists an MCP token")
	}
}

func TestMCPTokenDetailPageShowsRightsInWordsAndRevokesByName(t *testing.T) {
	b := signedInBox(t)
	switchMCPOnPage(t, b, true)
	id, secret := newMCPTokenOnPage(t, b, "Einkaufshelfer", map[string]string{"lists": "write", "meals": "read"})
	r := getEN(b, "/mcp/tokens/"+id)
	if r.status != http.StatusOK {
		t.Fatalf("GET = %d", r.status)
	}
	mcpHas(t, "detail", r.body,
		`<a class="back" href="/mcp">`, "All MCP tokens", "<h1>Einkaufshelfer</h1>", "<dt>For</dt>", `class="marker"`,
		"<dt>Reads and changes</dt>", "<dd>Lists</dd>", "<dt>Reads only</dt>", "<dd>Meal plan</dd>", "<dt>May not</dt>", "<dd>Events, Tasks (with routines and stars)</dd>",
		"<dt>Created on</dt>", "<dt>Last used</dt>", `class="flag warn"`, "Never used.",
		"The box never shows the token itself again", "for other rights you create a new token and revoke this one",
		`<details class="danger">`, "Revoke access", `action="/mcp/tokens/`+id+`/revoke"`, "Yes, revoke access for “Einkaufshelfer”", "No, change nothing")
	mcpHasNot(t, "detail", r.body, secret, `<a class="back" href="/settings">`)
	if c := b.get("/mcp/tokens/nope"); c.status != http.StatusNotFound {
		t.Errorf("unknown token = %d", c.status)
	}
	// A device token of the calendar apps is not an MCP token.
	devID, _ := newDeviceToken(t, b, "iPhone Mama")
	if c := b.get("/mcp/tokens/" + devID); c.status != http.StatusNotFound {
		t.Errorf("a device token opened as an MCP token = %d", c.status)
	}
	if c := b.get("/caldav/tokens/" + id); c.status != http.StatusNotFound {
		t.Errorf("an MCP token opened as a device token = %d", c.status)
	}
	// Revoking.
	rv := postEN(b, "/mcp/tokens/"+id+"/revoke", url.Values{})
	if rv.status != http.StatusSeeOther || rv.location != "/mcp" {
		t.Fatalf("revoke = %d %s", rv.status, rv.location)
	}
	after := getEN(b, "/mcp").body
	mcpHas(t, "after revoking", after, "Access for “Einkaufshelfer” revoked.")
	mcpHasNot(t, "after revoking", after, `href="/mcp/tokens/`+id+`"`)
	c := newAPIClient(t, b)
	if s, err := c.mcpConnect(secret); err == nil {
		s.Close()
		t.Error("a revoked token still connects")
	}
	if c := b.get("/mcp/tokens/" + id); c.status != http.StatusNotFound {
		t.Errorf("revoked token page = %d", c.status)
	}
}

func TestMCPSwitchedOffKeepsTokensAndSaysTheyDoNotWorkNow(t *testing.T) {
	b := signedInBox(t)
	switchMCPOnPage(t, b, true)
	id, _ := newMCPTokenOnPage(t, b, "Claude", map[string]string{"events": "read"})
	switchMCPOnPage(t, b, false)
	body := getEN(b, "/mcp").body
	mcpHas(t, "off with tokens", body, "The tokens stay stored and work again as soon as you switch MCP on.", "<b>Claude</b>", `class="flag warn"`, "Only works when MCP is on.",
		`<details class="more explain-more">`)
	mcpHasNot(t, "off with tokens", body, "Create a new MCP token", "Tap a token to see", "Never used")
	detail := getEN(b, "/mcp/tokens/"+id).body
	mcpHas(t, "detail while off", detail, "<dt>State</dt>", "MCP is off. The token only works once you switch MCP on.", `<a href="/mcp">Switch MCP on</a>`)
	// No new token while MCP is off: the form is not offered and a post makes none.
	if r := b.get("/mcp/tokens/new"); r.status != http.StatusSeeOther || r.location != "/mcp" {
		t.Errorf("new token form while off = %d %s", r.status, r.location)
	}
	if r := postEN(b, "/mcp/tokens", url.Values{"name": {"Zwei"}, "events": {"read"}}); r.status != http.StatusSeeOther || r.location != "/mcp" {
		t.Errorf("creating a token while off = %d %s", r.status, r.location)
	}
	if n := strings.Count(getEN(b, "/mcp").body, `href="/mcp/tokens/`); n != 1 {
		t.Errorf("%d tokens after a refused creation, want 1", n)
	}
}

func TestMCPPageInGerman(t *testing.T) {
	b := signedInBox(t)
	body := b.get("/mcp").body
	mcpHas(t, "/mcp de", body, "KI-Assistenten (MCP)", "Was ist MCP?", "Aus: Kein KI-Assistent kommt an eure Daten.", "Speichern", "(Englisch)")
	switchMCPOnPage(t, b, true)
	nb := b.get("/mcp/tokens/new").body
	mcpHas(t, "new de", nb, "Neues MCP-Token", "Nur lesen", "Lesen und ändern", "Der Assistent handelt als du, ")
	if !regexp.MustCompile(`name="events" value="read" checked>Nur lesen`).MatchString(nb) {
		t.Error("German form does not default to Nur lesen")
	}
}

func TestMCPPagesAreForParentsOnly(t *testing.T) {
	b := newFreshBox(t)
	for _, path := range []string{"/mcp", "/mcp/tokens/new", "/mcp/tokens/x"} {
		if r := b.get(path); r.status != http.StatusSeeOther {
			t.Errorf("GET %s before setup = %d", path, r.status)
		}
	}
	box := signedInBox(t)
	box.logout()
	for _, path := range []string{"/mcp", "/mcp/tokens/new"} {
		if r := box.get(path); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
			t.Errorf("GET %s signed out = %d %s", path, r.status, r.location)
		}
	}
}

// API parity (ADR 0015): everything the page does is possible through the REST API.
func TestMCPThroughTheRESTAPI(t *testing.T) {
	c := signedInAPI(t)
	status, body, _ := c.call(http.MethodGet, "/mcp", nil)
	if status != http.StatusOK || body["enabled"] != false || !strings.HasSuffix(body["address"].(string), "/mcp") || !strings.HasPrefix(body["address"].(string), "https://") {
		t.Fatalf("GET /mcp = %d %v", status, body)
	}
	c.switchMCP(t, true)
	if status, body, _ = c.call(http.MethodGet, "/mcp", nil); body["enabled"] != true {
		t.Errorf("GET /mcp after switching on = %d %v", status, body)
	}
	status, tok, _ := c.call(http.MethodPost, "/mcp/tokens", map[string]any{"name": "Claude", "areas": map[string]string{"events": "write", "meals": "read"}})
	if status != http.StatusCreated || tok["scopes"] != "events meals:read" || tok["address"] == "" || tok["token"] == "" {
		t.Fatalf("create = %d %v", status, tok)
	}
	// Listed with the other tokens, marked as made for MCP.
	_, _, list := c.call(http.MethodGet, "/tokens", nil)
	found := false
	for _, x := range list {
		if m := x.(map[string]any); m["id"] == tok["id"] {
			found = m["mcp"] == true
		}
	}
	if !found {
		t.Errorf("GET /tokens does not list the MCP token as mcp: %v", list)
	}
	for name, in := range map[string]map[string]any{
		"no name":   {"name": " ", "areas": map[string]string{"events": "read"}},
		"duplicate": {"name": "claude", "areas": map[string]string{"events": "read"}},
		"all none":  {"name": "Other", "areas": map[string]string{"events": "none"}},
		"no areas":  {"name": "Other", "areas": map[string]string{}},
		"unknown":   {"name": "Other", "areas": map[string]string{"settings": "write"}},
	} {
		if status, _, _ := c.call(http.MethodPost, "/mcp/tokens", in); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
	// A token that is not for MCP cannot manage MCP; a child cannot either.
	tb := newTaskBox(t)
	if status, _, _ := tb.child.call(http.MethodPut, "/mcp", map[string]any{"enabled": true}); status != http.StatusForbidden {
		t.Errorf("a child switched MCP on: %d", status)
	}
	if status, _, _ := tb.child.call(http.MethodPost, "/mcp/tokens", map[string]any{"name": "x", "areas": map[string]string{"events": "read"}}); status != http.StatusForbidden {
		t.Errorf("a child made an MCP token: %d", status)
	}
	readOnly := tb.parent.withToken(tb.parent.newToken("read"))
	if status, _, _ := readOnly.call(http.MethodPut, "/mcp", map[string]any{"enabled": true}); status != http.StatusForbidden {
		t.Errorf("a read-only token switched MCP on: %d", status)
	}
}

func TestMCPTokenNameOfMoreThan80CharactersIsRefusedWithItsOwnText(t *testing.T) {
	long := strings.Repeat("ä", 81)
	// The contract test client refuses 81 characters before they reach the server (maxLength in openapi.yaml), so only the edge that is allowed is tried here.
	c := signedInAPI(t)
	if status, _, _ := c.call(http.MethodPost, "/mcp/tokens", map[string]any{"name": strings.Repeat("ä", 80), "areas": map[string]string{"events": "read"}}); status != http.StatusCreated {
		t.Errorf("REST, 80 characters = %d, want 201", status)
	}
	b := signedInBox(t)
	switchMCPOnPage(t, b, true)
	r := postEN(b, "/mcp/tokens", url.Values{"name": {long}, "events": {"read"}, "lists": {"none"}, "tasks": {"none"}, "meals": {"none"}})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("page, 81 characters = %d", r.status)
	}
	mcpHas(t, "too long", r.body, `<div class="f invalid">`, "The name is at most 80 characters.")
}
