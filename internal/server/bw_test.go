package server_test

import (
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/meal"
	"github.com/hejmo-home/hejmo/web"
)

// 5.4: Given a fifth person, then they get a further distinct shape (and a
// sixth another); the API hands out the defaults.
func TestFifthAndSixthPersonGetFurtherDistinctShapes(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	shapes := map[string]string{}
	_, me, _ := c.call(http.MethodGet, "/me", nil)
	shapes[me["shape"].(string)] = "Mama"
	for _, n := range []string{"Papa", "Sebastian", "Mia", "Oma", "Opa"} {
		status, p, _ := c.call(http.MethodPost, "/people", map[string]any{"name": n, "role": "child"})
		if status != http.StatusCreated {
			t.Fatalf("create %s = %d", n, status)
		}
		shape := p["shape"].(string)
		if other, dup := shapes[shape]; dup {
			t.Errorf("%s gets shape %q, like %s", n, shape, other)
		}
		shapes[shape] = n
	}
	for _, s := range []string{"triangle-filled", "diamond-filled"} {
		if _, ok := shapes[s]; !ok {
			t.Errorf("no person got %s", s)
		}
	}
}

// 5.4: Rendering uses only the black-and-white palette (snapshot test). Every
// colour a bw page can resolve to comes from the bw palette of DESIGN.md, and
// the markup of the example family is held in a golden file.
func TestBlackAndWhiteRenderingUsesOnlyTheBlackAndWhitePalette(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "eink_bw")
	f.event(t, "Football", f.at(14, 15, 0), f.at(14, 16, 0), false, "Sebastian", "Mia")
	f.event(t, "Dentist", f.at(14, 9, 0), f.at(14, 10, 0), false, "Mama")
	f.event(t, "Grandparents visit", f.at(13, 0, 0), f.at(16, 0, 0), true, "Mama", "Papa")
	f.plan(t, map[meal.Mealtime]string{meal.Lunch: "Tomato soup", meal.Dinner: "Pasta bake"})
	f.addWaste(t, wasteEvent("p1", "Paper bin", "5"))
	page := f.view(t, "en", f.at(14, 8, 0))
	if !strings.Contains(page, `data-theme="bw"`) || !strings.Contains(page, `data-tile="meal"`) || !strings.Contains(page, `data-tile="tomorrow"`) {
		t.Errorf("the bw page lacks the theme or one of the two footer tiles:\n%s", page)
	}

	// The markup carries no colour of its own: no style attributes except the progress share.
	for _, m := range regexp.MustCompile(`style="([^"]*)"`).FindAllStringSubmatch(page, -1) {
		if !regexp.MustCompile(`^--(cols|pct):\d+$`).MatchString(m[1]) {
			t.Errorf("inline style %q", m[1])
		}
	}
	if regexp.MustCompile(`#[0-9A-Fa-f]{3,6}\b|rgb|hsl`).MatchString(page) {
		t.Error("colour value in the markup")
	}

	// The stylesheet: the bw block overrides every colour of the light palette, so no
	// light colour leaks through, and each value is one of the bw palette.
	css, err := fs.ReadFile(web.Static, "static/hejmo.css")
	if err != nil {
		t.Fatal(err)
	}
	hex := regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	light, bw := cssBlock(t, string(css), `:root, [data-theme="light"]`), cssBlock(t, string(css), `[data-theme="bw"]`)
	allowed := map[string]bool{"#EDEDEA": true, "#FFFFFF": true, "#111111": true, "#3A3A38": true, "#E4E4E1": true, "#D6D6D3": true}
	for prop, val := range light {
		if !hex.MatchString(val) {
			continue
		}
		got, ok := bw[prop]
		if !ok {
			t.Errorf("bw does not override colour %s", prop)
		} else if !allowed[strings.ToUpper(got)] {
			t.Errorf("bw %s = %s is outside the bw palette", prop, got)
		}
	}
	for prop, val := range bw {
		if hex.MatchString(val) && !allowed[strings.ToUpper(val)] {
			t.Errorf("bw %s = %s is outside the bw palette", prop, val)
		}
	}

	// Person ids are generated and the version hashes them: the golden holds placeholders.
	page = regexp.MustCompile(`/display/person/[0-9a-f-]{36}`).ReplaceAllString(page, "/display/person/ID")
	page = regexp.MustCompile(`data-version="[0-9a-f]+"`).ReplaceAllString(page, `data-version="VERSION"`)

	const golden = "testdata/family_bw.html"
	if os.Getenv("HEJMO_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if page != string(want) {
		t.Errorf("bw page differs from %s (HEJMO_UPDATE_GOLDEN=1 rewrites it after a reviewed change):\n%s", golden, page)
	}
}

func cssBlock(t *testing.T, css, selector string) map[string]string {
	t.Helper()
	m := regexp.MustCompile(`(?s)(?:^|\n)` + regexp.QuoteMeta(selector) + `\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("no CSS block %s", selector)
	}
	out := map[string]string{}
	for _, d := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`).FindAllStringSubmatch(m[1], -1) {
		out[d[1]] = strings.TrimSpace(d[2])
	}
	return out
}
