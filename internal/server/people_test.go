package server_test

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

// peopleBox is a signed-in parent (Mama), a paired colour display and an API
// client with the parent's session: ticket 5.21 manages people in the web
// interface, the API does the same.
type peopleBox struct {
	*box
	api     *apiClient
	display *screen
}

func newPeopleBox(t *testing.T) peopleBox {
	t.Helper()
	b := signedInBox(t)
	api := newAPIClient(t, b)
	api.cookie = true
	p := peopleBox{box: b, api: api, display: newScreen(b)}
	form := pairForm(codeOn(t, p.display.get("/display", "en").body))
	form.Set("type", "eink_color")
	if r := b.post("/displays", form); r.status != http.StatusSeeOther {
		t.Fatalf("pair = %d", r.status)
	}
	return p
}

// add adds a person through the web interface and returns their API record.
func (p peopleBox) add(t *testing.T, name, role string) map[string]any {
	t.Helper()
	r := p.post("/people", url.Values{"name": {name}, "role": {role}})
	if r.status != http.StatusSeeOther || r.location != "/people" {
		t.Fatalf("add %s = %d %q\n%s", name, r.status, r.location, r.body)
	}
	return p.byName(t, name)
}

func (p peopleBox) list(t *testing.T) []map[string]any {
	t.Helper()
	status, _, list := p.api.call(http.MethodGet, "/people", nil)
	if status != http.StatusOK {
		t.Fatalf("list people = %d", status)
	}
	var out []map[string]any
	for _, x := range list {
		out = append(out, x.(map[string]any))
	}
	return out
}

func (p peopleBox) byName(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, x := range p.list(t) {
		if x["name"] == name {
			return x
		}
	}
	t.Fatalf("no person %q", name)
	return nil
}

func (p peopleBox) order(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, x := range p.list(t) {
		out = append(out, x["name"].(string))
	}
	return out
}

func (p peopleBox) en(path string) resp {
	return p.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": "en"})
}

func (p peopleBox) save(id string, f url.Values) resp {
	return p.do(http.MethodPost, "/people/"+id, f, map[string]string{"Accept-Language": "en"})
}

func columnOrder(page string) []string {
	var out []string
	for _, m := range columnRe.FindAllStringSubmatch(page, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestPeoplePageNeedsASignedInParent(t *testing.T) {
	b, _ := configuredBox(t)
	if r := b.get("/people"); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Fatalf("signed out = %d %q", r.status, r.location)
	}
	if r := b.post("/people", url.Values{"name": {"Mia"}, "role": {"child"}}); r.status != http.StatusSeeOther {
		t.Fatalf("signed-out add = %d", r.status)
	}
	child := childSession(t, signedInBox(t))
	for _, path := range []string{"/people", "/groups/new"} {
		if r := child.get(path); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d, want 403", path, r.status)
		}
	}
	if r := child.post("/people", url.Values{"name": {"X"}, "role": {"child"}}); r.status != http.StatusForbidden {
		t.Errorf("child add = %d, want 403", r.status)
	}
}

// Acceptance: Given a parent adds the child Mia, then she gets a column on the displays.
func TestAddedChildGetsAColumnOnTheDisplays(t *testing.T) {
	p := newPeopleBox(t)
	if got := columnOrder(p.display.get("/display", "en").body); len(got) != 1 {
		t.Fatalf("columns before = %v", got)
	}
	mia := p.add(t, "Mia", "child")
	if mia["role"] != "child" {
		t.Fatalf("Mia = %v", mia)
	}
	page := p.en("/people")
	for _, want := range []string{"Mia", "Mama", "data-success", "Person added."} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("people page lacks %q:\n%s", want, page.body)
		}
	}
	if got := columnOrder(p.display.get("/display", "en").body); len(got) != 2 || got[1] != "Mia" {
		t.Fatalf("columns after = %v", got)
	}
	p.add(t, "Papa", "parent")
	if papa := p.byName(t, "Papa"); papa["role"] != "parent" {
		t.Fatalf("Papa = %v", papa)
	}
}

func TestAddingAPersonNeedsANameAndKeepsWhatWasTyped(t *testing.T) {
	p := newPeopleBox(t)
	r := p.do(http.MethodPost, "/people", url.Values{"name": {" "}, "role": {"child"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-name"`) {
		t.Fatalf("blank name = %d\n%s", r.status, r.body)
	}
	r = p.do(http.MethodPost, "/people", url.Values{"name": {"Mia"}, "role": {"alien"}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-role"`) || !strings.Contains(r.body, `value="Mia"`) {
		t.Fatalf("bad role = %d\n%s", r.status, r.body)
	}
	if len(p.list(t)) != 1 {
		t.Fatal("a person was added")
	}
}

// Acceptance: Changing a person's colour changes their column, chips and markers everywhere.
func TestChangingAColourChangesColumnChipsAndMarkersEverywhere(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")
	id := mia["id"].(string)
	if mia["color"] != "#2F5E7A" {
		t.Fatalf("Mia starts with %v, want the second palette colour", mia["color"])
	}
	column := regexp.MustCompile(`<section class="column" data-person="(\d)" data-name="Mia"`)
	if m := column.FindStringSubmatch(p.display.get("/display", "en").body); m == nil || m[1] != "2" {
		t.Fatalf("column before = %v", m)
	}

	r := p.save(id, url.Values{"name": {"Mia"}, "color": {"#4E7D52"}, "shape": {"circle-empty"}})
	if r.status != http.StatusSeeOther || r.location != "/people" {
		t.Fatalf("save = %d %q\n%s", r.status, r.location, r.body)
	}
	if got := p.byName(t, "Mia"); got["color"] != "#4E7D52" {
		t.Fatalf("API colour = %v", got["color"])
	}
	if m := column.FindStringSubmatch(p.display.get("/display", "en").body); m == nil || m[1] != "3" {
		t.Fatalf("column after = %v", m)
	}
	personal := p.display.get("/display/person/"+id, "en").body
	if !strings.Contains(personal, `data-person="3"`) {
		t.Errorf("personal view keeps the old colour:\n%s", personal)
	}
	chip := regexp.MustCompile(`data-person="3"><input[^>]*value="` + id + `"`)
	for _, path := range []string{"/tasks", "/people", "/people/" + id} {
		if page := p.en(path).body; !strings.Contains(page, `data-person="3"`) || (path == "/tasks" && !chip.MatchString(page)) {
			t.Errorf("%s keeps the old colour:\n%s", path, page)
		}
	}
	// Events of Mia carry her marker in the strip.
	f, _ := p.db.HomeHousehold()
	cal, _ := p.db.FamilyCalendar(f.ID)
	if _, err := p.db.CreateEvent(f.ID, calendar.Event{CalendarID: cal.ID, Title: "Swimming", Start: p.now, End: p.now.Add(3600e9), People: []string{id}}); err != nil {
		t.Fatal(err)
	}
	if page := p.display.get("/display", "en").body; !strings.Contains(page, `class="marker small" data-person="3"`) {
		t.Errorf("markers keep the old colour:\n%s", page)
	}
}

func TestPersonCanBeRenamedAndNeedsAName(t *testing.T) {
	p := newPeopleBox(t)
	id := p.add(t, "Mia", "child")["id"].(string)
	if r := p.save(id, url.Values{"name": {"Mia Maier"}, "color": {"#2F5E7A"}, "shape": {"circle-empty"}}); r.status != http.StatusSeeOther {
		t.Fatalf("rename = %d\n%s", r.status, r.body)
	}
	if got := p.byName(t, "Mia Maier"); got["role"] != "child" {
		t.Fatalf("renamed = %v", got)
	}
	r := p.save(id, url.Values{"name": {" "}, "color": {"#2F5E7A"}, "shape": {"circle-empty"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-name"`) {
		t.Fatalf("blank name = %d\n%s", r.status, r.body)
	}
	if r := p.en("/people/00000000-0000-4000-8000-000000000000"); r.status != http.StatusNotFound {
		t.Fatalf("unknown person = %d", r.status)
	}
	if r := p.save("00000000-0000-4000-8000-000000000000", url.Values{"name": {"X"}}); r.status != http.StatusNotFound {
		t.Fatalf("save unknown = %d", r.status)
	}
}

func TestOnlyThePaletteColoursAndKnownMarkersAreOffered(t *testing.T) {
	p := newPeopleBox(t)
	id := p.add(t, "Mia", "child")["id"].(string)
	for name, f := range map[string]url.Values{
		"colour": {"name": {"Mia"}, "color": {"#123456"}, "shape": {"circle-empty"}},
		"shape":  {"name": {"Mia"}, "color": {"#2F5E7A"}, "shape": {"star"}},
	} {
		field := map[string]string{"colour": "color", "shape": "shape"}[name]
		r := p.save(id, f)
		if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-`+field+`"`) {
			t.Errorf("unknown %s = %d\n%s", name, r.status, r.body)
		}
	}
	page := p.en("/people/" + id).body
	if n := strings.Count(page, `name="color"`); n != 6 {
		t.Errorf("%d colour choices, want 6", n)
	}
	if n := strings.Count(page, `name="shape"`); n != 6 {
		t.Errorf("%d marker choices, want 6", n)
	}
}

// Acceptance: Colours and markers stay distinguishable (no two people with the
// same marker shape and colour).
func TestNoTwoPeopleShareMarkerShapeAndColour(t *testing.T) {
	p := newPeopleBox(t)
	mama := p.byName(t, "Mama")
	mia := p.add(t, "Mia", "child")
	id := mia["id"].(string)
	same := url.Values{"name": {"Mia Maier"}, "color": {mama["color"].(string)}, "shape": {mama["shape"].(string)}}
	r := p.save(id, same)
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-shape"`) || !strings.Contains(r.body, `value="Mia Maier"`) {
		t.Fatalf("same colour and marker as Mama = %d\n%s", r.status, r.body)
	}
	if got := p.byName(t, "Mia"); got["color"] == mama["color"] && got["shape"] == mama["shape"] {
		t.Fatal("the clash was saved")
	}
	status, body, _ := p.api.call(http.MethodPatch, "/people/"+id, map[string]any{"color": mama["color"], "shape": mama["shape"]})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("API clash = %d %v", status, body)
	}
	status, body, _ = p.api.call(http.MethodPost, "/people", map[string]any{"name": "Oma", "role": "parent", "color": mama["color"], "shape": mama["shape"]})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("API create clash = %d %v", status, body)
	}
	// The same colour with another marker (or the other way round) is fine, and a
	// person may keep their own combination.
	other := "square-empty"
	if mama["shape"] == other {
		other = "diamond-filled"
	}
	if r := p.save(id, url.Values{"name": {"Mia"}, "color": {mama["color"].(string)}, "shape": {other}}); r.status != http.StatusSeeOther {
		t.Fatalf("same colour, other marker = %d\n%s", r.status, r.body)
	}
	if r := p.save(id, url.Values{"name": {"Mia"}, "color": {mama["color"].(string)}, "shape": {other}}); r.status != http.StatusSeeOther {
		t.Fatalf("unchanged = %d\n%s", r.status, r.body)
	}

	for i := 0; i < 8; i++ { // eight more people: the defaults never repeat a combination
		p.add(t, fmt.Sprintf("Person %d", i), "child")
	}
	seen := map[string]string{}
	for _, x := range p.list(t) {
		key := fmt.Sprint(x["color"], "/", x["shape"])
		if other, dup := seen[key]; dup {
			t.Errorf("%v and %v share colour and marker %s", other, x["name"], key)
		}
		seen[key] = x["name"].(string)
	}

}

func TestAddingAPersonLetsTheParentPickColourAndMarker(t *testing.T) {
	p := newPeopleBox(t)
	r := p.post("/people", url.Values{"name": {"Mia"}, "role": {"child"}, "color": {"#9A7420"}, "shape": {"diamond-filled"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("add = %d\n%s", r.status, r.body)
	}
	if mia := p.byName(t, "Mia"); mia["color"] != "#9A7420" || mia["shape"] != "diamond-filled" {
		t.Fatalf("Mia = %v", mia)
	}
	mama := p.byName(t, "Mama")
	r = p.do(http.MethodPost, "/people", url.Values{"name": {"Papa"}, "role": {"parent"}, "color": {mama["color"].(string)}, "shape": {mama["shape"].(string)}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-shape"`) || !strings.Contains(r.body, `value="Papa"`) {
		t.Fatalf("clash on add = %d\n%s", r.status, r.body)
	}
}

func TestParentOrdersPeopleForTheFamilyView(t *testing.T) {
	p := newPeopleBox(t)
	p.add(t, "Papa", "parent")
	mia := p.add(t, "Mia", "child")["id"].(string)
	papa := p.byName(t, "Papa")["id"].(string)
	if r := p.post("/people/"+mia+"/move", url.Values{"direction": {"up"}}); r.status != http.StatusSeeOther || r.location != "/people" {
		t.Fatalf("move up = %d %q", r.status, r.location)
	}
	if got := fmt.Sprint(p.order(t)); got != "[Mama Mia Papa]" {
		t.Fatalf("order = %s", got)
	}
	if got := fmt.Sprint(columnOrder(p.display.get("/display", "en").body)); got != "[Mama Mia Papa]" {
		t.Fatalf("columns = %s", got)
	}
	p.post("/people/"+papa+"/move", url.Values{"direction": {"up"}})
	p.post("/people/"+papa+"/move", url.Values{"direction": {"up"}})
	if got := fmt.Sprint(p.order(t)); got != "[Papa Mama Mia]" {
		t.Fatalf("order after moving Papa to the front = %s", got)
	}
	// The ends stay where they are.
	if r := p.post("/people/"+papa+"/move", url.Values{"direction": {"up"}}); r.status != http.StatusSeeOther {
		t.Fatalf("move first up = %d", r.status)
	}
	if r := p.post("/people/"+mia+"/move", url.Values{"direction": {"down"}}); r.status != http.StatusSeeOther {
		t.Fatalf("move last down = %d", r.status)
	}
	if got := fmt.Sprint(p.order(t)); got != "[Papa Mama Mia]" {
		t.Fatalf("order = %s", got)
	}
	if r := p.post("/people/"+mia+"/move", url.Values{"direction": {"sideways"}}); r.status != http.StatusBadRequest {
		t.Fatalf("bad direction = %d", r.status)
	}
	if r := p.post("/people/00000000-0000-4000-8000-000000000000/move", url.Values{"direction": {"up"}}); r.status != http.StatusNotFound {
		t.Fatalf("unknown person = %d", r.status)
	}
	page := p.en("/people").body
	if strings.Count(page, `name="direction"`) < 4 { // up and down for the middle one, one each for the ends
		t.Errorf("people page lacks the order buttons:\n%s", page)
	}
}

func TestAPIChangesAPersonAndTheOrder(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")["id"].(string)
	papa := p.add(t, "Papa", "parent")["id"].(string)
	mama := p.byName(t, "Mama")["id"].(string)

	status, got, _ := p.api.call(http.MethodPatch, "/people/"+mia, map[string]any{"name": "Mia Maier", "color": "#4E7D52"})
	if status != http.StatusOK || got["name"] != "Mia Maier" || got["color"] != "#4E7D52" || got["role"] != "child" {
		t.Fatalf("patch = %d %v", status, got)
	}
	for name, body := range map[string]map[string]any{
		"blank name": {"name": " "},
	} {
		if status, _, _ := p.api.call(http.MethodPatch, "/people/"+mia, body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
	// The contract refuses these before they are sent; the box refuses them too.
	for name, body := range map[string]string{"unknown colour": `{"color":"#123456"}`, "unknown shape": `{"shape":"star"}`, "too long a name": `{"name":"` + strings.Repeat("x", 81) + `"}`} {
		req, _ := http.NewRequest(http.MethodPatch, p.srv.URL+"/api/v1/people/"+mia, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", p.srv.URL)
		res, err := p.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, res.StatusCode)
		}
	}
	if status, _, _ := p.api.call(http.MethodPatch, "/people/00000000-0000-4000-8000-000000000000", map[string]any{"name": "X"}); status != http.StatusNotFound {
		t.Errorf("unknown person = %d", status)
	}

	status, _, list := p.api.call(http.MethodPut, "/people/order", map[string]any{"ids": []string{papa, mia, mama}})
	if status != http.StatusOK || len(list) != 3 || list[0].(map[string]any)["name"] != "Papa" || list[2].(map[string]any)["name"] != "Mama" {
		t.Fatalf("order = %d %v", status, list)
	}
	if got := fmt.Sprint(p.order(t)); got != "[Papa Mia Maier Mama]" {
		t.Fatalf("stored order = %s", got)
	}
	for name, ids := range map[string][]string{"missing": {papa}, "duplicate": {papa, papa, mia}, "unknown": {papa, mia, "nobody"}} {
		if status, _, _ := p.api.call(http.MethodPut, "/people/order", map[string]any{"ids": ids}); status != http.StatusUnprocessableEntity {
			t.Errorf("%s order = %d, want 422", name, status)
		}
	}
	ro := p.api.withToken(p.api.newToken("read"))
	if status, _, _ := ro.call(http.MethodPatch, "/people/"+mia, map[string]any{"name": "X"}); status != http.StatusForbidden {
		t.Errorf("read-only token patch = %d, want 403", status)
	}
	if status, _, _ := ro.call(http.MethodPut, "/people/order", map[string]any{"ids": []string{papa, mia, mama}}); status != http.StatusForbidden {
		t.Errorf("read-only token order = %d, want 403", status)
	}
}

func TestParentManagesSubgroups(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")["id"].(string)
	sebastian := p.add(t, "Sebastian", "child")["id"].(string)

	if page := p.en("/groups/new"); page.status != http.StatusOK || !strings.Contains(page.body, `name="people"`) {
		t.Fatalf("new group page = %d\n%s", page.status, page.body)
	}
	r := p.do(http.MethodPost, "/groups", url.Values{"name": {" "}, "people": {mia}}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-name"`) {
		t.Fatalf("blank name = %d\n%s", r.status, r.body)
	}
	if r := p.post("/groups", url.Values{"name": {"School children"}, "people": {mia}}); r.status != http.StatusSeeOther || r.location != "/people" {
		t.Fatalf("create = %d %q", r.status, r.location)
	}
	status, _, groups := p.api.call(http.MethodGet, "/groups", nil)
	if status != http.StatusOK || len(groups) != 1 {
		t.Fatalf("groups = %d %v", status, groups)
	}
	g := groups[0].(map[string]any)
	gid := g["id"].(string)
	if page := p.en("/people").body; !strings.Contains(page, "School children") || !strings.Contains(page, "/groups/"+gid) {
		t.Fatalf("people page lacks the group:\n%s", page)
	}

	page := p.en("/groups/" + gid).body
	if !strings.Contains(page, `value="School children"`) {
		t.Fatalf("group page lacks its name:\n%s", page)
	}
	if r := p.post("/groups/"+gid, url.Values{"name": {"Pupils"}, "people": {mia, sebastian}}); r.status != http.StatusSeeOther || r.location != "/people" {
		t.Fatalf("save = %d %q", r.status, r.location)
	}
	_, _, groups = p.api.call(http.MethodGet, "/groups", nil)
	g = groups[0].(map[string]any)
	if g["name"] != "Pupils" || len(g["members"].([]any)) != 2 {
		t.Fatalf("saved group = %v", g)
	}
	// Without any chip the group has no members.
	if r := p.post("/groups/"+gid, url.Values{"name": {"Pupils"}}); r.status != http.StatusSeeOther {
		t.Fatalf("save empty = %d", r.status)
	}
	if _, _, groups = p.api.call(http.MethodGet, "/groups", nil); len(groups[0].(map[string]any)["members"].([]any)) != 0 {
		t.Fatalf("members = %v", groups[0])
	}
	if r := p.post("/groups/"+gid, url.Values{"name": {""}}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("blank rename = %d", r.status)
	}

	if r := p.post("/groups/"+gid+"/delete", url.Values{}); r.status != http.StatusSeeOther || r.location != "/people" {
		t.Fatalf("delete = %d %q", r.status, r.location)
	}
	if _, _, groups = p.api.call(http.MethodGet, "/groups", nil); len(groups) != 0 {
		t.Fatalf("left over: %v", groups)
	}
	for _, path := range []string{"/groups/" + gid, "/groups/" + gid + "/delete"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "delete") {
			method = http.MethodPost
		}
		if r := p.do(method, path, url.Values{}, nil); r.status != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", method, path, r.status)
		}
	}
}

func TestSubgroupInUseIsNotDeleted(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")["id"].(string)
	h, _ := p.db.HomeHousehold()
	g, _ := p.db.CreateGroup(h.ID, "School children", []string{mia})
	if _, err := p.db.CreateCalendar(store.Calendar{HouseholdID: h.ID, Name: "School", Kind: "ics", Audience: calendar.Audience{Groups: []string{g.ID}}}); err != nil {
		t.Fatal(err)
	}
	r := p.do(http.MethodPost, "/groups/"+g.ID+"/delete", url.Values{}, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusConflict || !strings.Contains(r.body, "role=\"alert\"") {
		t.Fatalf("delete used group = %d\n%s", r.status, r.body)
	}
	if status, _, _ := p.api.call(http.MethodDelete, "/groups/"+g.ID, nil); status != http.StatusConflict {
		t.Fatalf("API delete used group = %d, want 409", status)
	}
	if _, err := p.db.Group(h.ID, g.ID); err != nil {
		t.Fatalf("group is gone: %v", err)
	}
}

func TestAPIChangesAndDeletesSubgroups(t *testing.T) {
	p := newPeopleBox(t)
	mia := p.add(t, "Mia", "child")["id"].(string)
	status, g, _ := p.api.call(http.MethodPost, "/groups", map[string]any{"name": "School children", "members": []string{mia}})
	if status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, g)
	}
	gid := g["id"].(string)
	status, g, _ = p.api.call(http.MethodPatch, "/groups/"+gid, map[string]any{"name": "Pupils", "members": []string{}})
	if status != http.StatusOK || g["name"] != "Pupils" || len(g["members"].([]any)) != 0 {
		t.Fatalf("patch = %d %v", status, g)
	}
	if status, _, _ := p.api.call(http.MethodPatch, "/groups/"+gid, map[string]any{"name": " "}); status != http.StatusUnprocessableEntity {
		t.Fatalf("blank name = %d", status)
	}
	if status, _, _ := p.api.call(http.MethodPatch, "/groups/nobody", map[string]any{"name": "X"}); status != http.StatusNotFound {
		t.Fatalf("unknown group = %d", status)
	}
	if status, _, _ := p.api.call(http.MethodDelete, "/groups/"+gid, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _, _ := p.api.call(http.MethodDelete, "/groups/"+gid, nil); status != http.StatusNotFound {
		t.Fatalf("second delete = %d", status)
	}
}

func TestPeopleAreReachableFromSettingsAndNavigation(t *testing.T) {
	p := newPeopleBox(t)
	if page := p.en("/").body; !strings.Contains(page, `href="/people"`) {
		t.Errorf("navigation lacks People:\n%s", page)
	}
	if page := p.en("/settings").body; !strings.Contains(page, `href="/people"`) {
		t.Errorf("settings lack a link to People:\n%s", page)
	}
}

func TestPeoplePageIsGerman(t *testing.T) {
	p := newPeopleBox(t)
	p.add(t, "Mia", "child")
	page := p.do(http.MethodGet, "/people", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{"Personen", "Kind", "Erwachsene"} {
		if !strings.Contains(page, want) {
			t.Errorf("German people page lacks %q:\n%s", want, page)
		}
	}
}
