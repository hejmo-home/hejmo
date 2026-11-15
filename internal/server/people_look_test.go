package server_test

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

// lookOption is one colour or marker choice of the people form as the page
// shows it.
type lookOption struct {
	title, aria        string
	used, off, checked bool
}

var lookRe = regexp.MustCompile(`<label class="pchip([^"]*)"[^>]*?title="([^"]*)"[^>]*>(<input[^>]*>)`)

// lookOptions reads the colour (field "color") or marker ("shape") choices
// from a page, keyed by the value of the radio button.
func lookOptions(page, field string) map[string]lookOption {
	out := map[string]lookOption{}
	for _, m := range lookRe.FindAllStringSubmatch(page, -1) {
		in := m[3]
		if !strings.Contains(in, `name="`+field+`"`) {
			continue
		}
		val := regexp.MustCompile(`value="([^"]*)"`).FindStringSubmatch(in)[1]
		aria := regexp.MustCompile(`aria-label="([^"]*)"`).FindStringSubmatch(in)
		o := lookOption{title: html.UnescapeString(m[2]), used: strings.Contains(m[1], "used"),
			off: strings.Contains(in, " disabled"), checked: strings.Contains(in, " checked")}
		if aria != nil {
			o.aria = html.UnescapeString(aria[1])
		}
		out[val] = o
	}
	return out
}

const (
	terracotta = "#B4583A"
	blue       = "#2F5E7A"
)

// Acceptance: colours and markers already used by another person are faded and
// name that person on hover and focus (tooltip and accessible name); free ones
// say "free".
func TestLookOptionsNameWhoUsesThemAndFadeTheTaken(t *testing.T) {
	p := newPeopleBox(t)
	p.add(t, "Papa", "parent") // blue, empty circle; Mama has terracotta, filled circle
	for _, page := range []string{p.en("/people").body, p.en("/people/" + p.byName(t, "Papa")["id"].(string)).body} {
		colors, shapes := lookOptions(page, "color"), lookOptions(page, "shape")
		if len(colors) != 8 || len(shapes) != 8 {
			t.Fatalf("%d colours and %d markers found, want 8 each", len(colors), len(shapes))
		}
		if o := colors[terracotta]; o.title != "Terracotta: Mama" || o.aria != "Terracotta: Mama" || !o.used {
			t.Errorf("Mama's colour = %+v", o)
		}
		if o := colors["#9A7420"]; o.title != "Ochre: free" || o.aria != "Ochre: free" || o.used {
			t.Errorf("a free colour = %+v", o)
		}
		if o := shapes["circle-filled"]; o.title != "Filled circle: Mama" || o.aria != "Filled circle: Mama" || !o.used {
			t.Errorf("Mama's marker = %+v", o)
		}
		if o := shapes["star-filled"]; o.title != "Star: free" || o.aria != "Star: free" || o.used {
			t.Errorf("a free marker = %+v", o)
		}
	}
	// On Papa's own page his own colour and marker are not "used by another".
	papa := lookOptions(p.en("/people/"+p.byName(t, "Papa")["id"].(string)).body, "color")[blue]
	if papa.used || papa.title != "Blue: free" {
		t.Errorf("Papa's own colour on his page = %+v", papa)
	}
}

func TestLookOptionsAreNamedInGerman(t *testing.T) {
	p := newPeopleBox(t)
	r := p.do(http.MethodGet, "/people", nil, map[string]string{"Accept-Language": "de"})
	colors, shapes := lookOptions(r.body, "color"), lookOptions(r.body, "shape")
	if o := colors[terracotta]; o.title != "Terrakotta: Mama" || o.aria != "Terrakotta: Mama" {
		t.Errorf("Mama's colour = %+v", o)
	}
	if o := colors["#9A7420"]; o.title != "Ocker: frei" || o.aria != "Ocker: frei" {
		t.Errorf("a free colour = %+v", o)
	}
	if o := shapes["star-filled"]; o.title != "Stern: frei" || o.aria != "Stern: frei" {
		t.Errorf("a free marker = %+v", o)
	}
}

// Acceptance: a colour and marker pair that would be refused is disabled; the
// current choice is never disabled, so a refused form still shows the error.
func TestLookPairsThatWouldBeRefusedAreDisabled(t *testing.T) {
	p := newPeopleBox(t)
	papa := p.add(t, "Papa", "parent")
	jonas := p.add(t, "Jonas", "child")
	// Papa gets Mama's colour with the empty circle; Jonas too, with the filled square.
	for _, x := range []struct {
		id, name, shape string
	}{{papa["id"].(string), "Papa", "circle-empty"}, {jonas["id"].(string), "Jonas", "square-filled"}} {
		if r := p.save(x.id, url.Values{"name": {x.name}, "color": {terracotta}, "shape": {x.shape}}); r.status != http.StatusSeeOther {
			t.Fatalf("save = %d\n%s", r.status, r.body)
		}
	}
	// Jonas has terracotta: the markers Mama and Papa wear with it are disabled.
	page := p.en("/people/" + jonas["id"].(string)).body
	shapes := lookOptions(page, "shape")
	for shape, want := range map[string]bool{"circle-filled": true, "circle-empty": true, "square-filled": false, "star-filled": false} {
		if shapes[shape].off != want {
			t.Errorf("marker %s disabled = %v, want %v", shape, shapes[shape].off, want)
		}
	}
	// Papa wears the empty circle, which nobody else has with another colour:
	// no colour is disabled on his page.
	papaPage := lookOptions(p.en("/people/"+papa["id"].(string)).body, "color")
	for c, o := range papaPage {
		if o.off {
			t.Errorf("colour %s disabled on Papa's page (empty circle is free in every colour)", c)
		}
	}
	// Give Jonas the filled circle in blue; on Mama's page (her marker is the
	// filled circle) blue is then disabled and terracotta is her own colour.
	if r := p.save(jonas["id"].(string), url.Values{"name": {"Jonas"}, "color": {blue}, "shape": {"circle-filled"}}); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	mama := p.byName(t, "Mama")["id"].(string)
	colors := lookOptions(p.en("/people/"+mama).body, "color")
	if !colors[blue].off || colors[terracotta].off || colors["#9A7420"].off {
		t.Errorf("Mama's colours with the filled circle selected: blue off=%v terracotta off=%v ochre off=%v", colors[blue].off, colors[terracotta].off, colors["#9A7420"].off)
	}
	// Disabled options still name the person.
	if colors[blue].title != "Blue: Jonas" {
		t.Errorf("title = %q", colors[blue].title)
	}
}

// Without JavaScript the server still refuses the pair, with the field error,
// even when a disabled option is posted by hand.
func TestRefusalStaysOnTheServer(t *testing.T) {
	p := newPeopleBox(t)
	mama := p.byName(t, "Mama")
	id := p.add(t, "Mia", "child")["id"].(string)
	r := p.save(id, url.Values{"name": {"Mia"}, "color": {mama["color"].(string)}, "shape": {mama["shape"].(string)}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(r.body, `id="err-shape"`) {
		t.Fatalf("refusal = %d\n%s", r.status, r.body)
	}
	// The refused choice stays selected and selectable, so the form can be sent again.
	shapes := lookOptions(r.body, "shape")
	if o := shapes[mama["shape"].(string)]; !o.checked || o.off {
		t.Errorf("refused marker = %+v", o)
	}
}

// The script keeps the disabled options in step while choosing: every option
// carries what it is taken with.
func TestLookOptionsCarryWhatTheyAreTakenWith(t *testing.T) {
	p := newPeopleBox(t)
	page := p.en("/people").body
	if !strings.Contains(page, `data-taken-with="circle-filled"`) || !strings.Contains(page, `data-taken-with="`+terracotta+`"`) {
		t.Errorf("no data-taken-with for Mama's pair:\n%s", page)
	}
}

// A person stored with a colour outside the palette can be renamed on the web
// page too, also when the form sends the stored colour and marker back.
func TestWebRenamesAPersonWithALegacyLook(t *testing.T) {
	p := newPeopleBox(t)
	h, _ := p.db.HomeHousehold()
	legacy, err := p.db.AddPerson(h.ID, store.Person{Name: "Oma", Role: store.RoleParent, Color: "#123456", Shape: "circle-empty"})
	if err != nil {
		t.Fatal(err)
	}
	r := p.save(legacy.ID, url.Values{"name": {"Oma Maier"}, "color": {"#123456"}, "shape": {"circle-empty"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("rename = %d\n%s", r.status, r.body)
	}
}
