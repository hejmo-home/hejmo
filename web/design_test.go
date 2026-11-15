package web_test

import (
	"io/fs"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"

	"github.com/hejmo-home/hejmo/web"
)

// Values below are copied from docs/spec.md, section "Design specification
// Nordic Calm". They are the independent source of truth for these tests.

var themes = []string{"light", "dark", "bw"}

func stylesheet(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(web.Static, "static/hejmo.css")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// cssTokens returns the custom properties declared for a theme. Light is the
// default (:root); dark and bw override it in [data-theme="..."] blocks.
func cssTokens(t *testing.T, css, theme string) map[string]string {
	t.Helper()
	sel := `:root`
	if theme != "light" {
		sel = `[data-theme="` + theme + `"]`
	}
	re := regexp.MustCompile(`(?s)(?:^|\n)` + regexp.QuoteMeta(sel) + `[^{]*\{([^}]*)\}`)
	m := re.FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("no CSS block for theme %q (%s)", theme, sel)
	}
	out := map[string]string{}
	for _, d := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`).FindAllStringSubmatch(m[1], -1) {
		out[d[1]] = strings.TrimSpace(d[2])
	}
	return out
}

// themed returns the tokens a theme resolves to: light values overlaid with
// the theme's own.
func themed(t *testing.T, css, theme string) map[string]string {
	out := cssTokens(t, css, "light")
	if theme != "light" {
		for k, v := range cssTokens(t, css, theme) {
			out[k] = v
		}
	}
	return out
}

type designDoc struct {
	Colors     map[string]string            `yaml:"colors"`
	Typography map[string]map[string]string `yaml:"typography"`
	Rounded    map[string]string            `yaml:"rounded"`
	Spacing    map[string]string            `yaml:"spacing"`
	Sizes      map[string]string            `yaml:"sizes"`
}

func design(t *testing.T) designDoc {
	t.Helper()
	b, err := os.ReadFile("../DESIGN.md")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(b), "---\n", 3)
	if len(parts) < 3 || parts[0] != "" {
		t.Fatal("DESIGN.md has no YAML front matter")
	}
	var d designDoc
	if err := yaml.Unmarshal([]byte(parts[1]), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSpecColoursExistAsTokensInColourAndBlackAndWhite(t *testing.T) {
	type row struct{ name, light, bw string }
	rows := []row{
		{"background", "#F6F3EE", "#EDEDEA"},
		{"surface", "#FFFFFF", "#FFFFFF"},
		{"text", "#2A2A28", "#111111"},
		{"text-secondary", "#5C5A55", "#3A3A38"},
		{"event-card", "", "#E4E4E1"},
		{"person-1", "#B4583A", "#111111"},
		{"person-1-tint", "#F7E7E0", "#E4E4E1"},
		{"person-1-text", "#5C3A2C", "#111111"},
		{"person-2", "#2F5E7A", "#111111"},
		{"person-2-tint", "#E1EBF1", "#E4E4E1"},
		{"person-2-text", "#24485E", "#111111"},
		{"person-3", "#4E7D52", "#111111"},
		{"person-3-tint", "#E3EEE3", "#E4E4E1"},
		{"person-3-text", "#2F4D31", "#111111"},
		{"person-4", "#9A7420", "#111111"},
		{"person-4-tint", "#F3EAD3", "#E4E4E1"},
		{"person-4-text", "#5A4513", "#111111"},
	}
	css, d := stylesheet(t), design(t)
	for _, r := range rows {
		for theme, want := range map[string]string{"light": r.light, "bw": r.bw} {
			if want == "" {
				continue
			}
			if got := themed(t, css, theme)["--"+r.name]; !strings.EqualFold(got, want) {
				t.Errorf("css %s --%s = %q, want %s", theme, r.name, got, want)
			}
			if got := d.Colors[theme+"-"+r.name]; !strings.EqualFold(got, want) {
				t.Errorf("DESIGN.md colors.%s-%s = %q, want %s", theme, r.name, got, want)
			}
		}
	}
	// Progress bar: person colour on person tint (colour), #111 on #D6D6D3 (bw).
	bw := themed(t, css, "bw")
	if bw["--progress-fill"] != "#111111" || !strings.EqualFold(bw["--progress-track"], "#D6D6D3") {
		t.Errorf("bw progress = %q on %q", bw["--progress-fill"], bw["--progress-track"])
	}
	if d.Colors["bw-progress-track"] != "#D6D6D3" || d.Colors["bw-progress-fill"] != "#111111" {
		t.Errorf("DESIGN.md bw progress tokens missing")
	}
	// Icons: Papa blue (bw #111111). Journeys: dashed border in person colour (bw #111111).
	if got := themed(t, css, "light")["--accent"]; !strings.EqualFold(got, "#2F5E7A") {
		t.Errorf("light --accent = %q, want Papa blue", got)
	}
	if got := themed(t, css, "bw")["--accent"]; !strings.EqualFold(got, "#111111") {
		t.Errorf("bw --accent = %q", got)
	}
}

func TestSpecDimensionsAndTypographyExistAsTokens(t *testing.T) {
	css, d := stylesheet(t), design(t)
	root := cssTokens(t, css, "light")
	dims := []struct {
		cssVar string
		want   string
		doc    map[string]string
		key    string
	}{
		{"--radius-tile", "22px", d.Rounded, "tile"},
		{"--radius-chip", "18px", d.Rounded, "chip"},
		{"--radius-card", "14px", d.Rounded, "card"},
		{"--radius-field", "12px", d.Rounded, "field"},
		{"--radius-progress", "4px", d.Rounded, "progress"},
		{"--progress-height", "8px", d.Sizes, "progress-height"},
		{"--margin", "28px", d.Spacing, "margin"},
		{"--gap", "14px", d.Spacing, "gap"},
		{"--gap-section", "20px", d.Spacing, "section-gap"},
		{"--pad-column", "16px", d.Spacing, "column-padding"},
		{"--pad-card", "10px 12px", d.Spacing, "card-padding"},
		{"--gap-cards", "10px", d.Spacing, "card-gap"},
		{"--marker-size", "14px", d.Sizes, "marker"},
		{"--marker-size-bw-column", "16px", d.Sizes, "marker-bw-column"},
		{"--journey-border", "1.5px", d.Sizes, "journey-border"},
		{"--icon-stroke", "1.8px", d.Sizes, "icon-stroke"},
		{"--touch-target", "44px", d.Sizes, "touch-target"},
	}
	for _, c := range dims {
		if got := root[c.cssVar]; got != c.want {
			t.Errorf("css %s = %q, want %s", c.cssVar, got, c.want)
		}
		if got := c.doc[c.key]; got != c.want {
			t.Errorf("DESIGN.md %s = %q, want %s", c.key, got, c.want)
		}
	}
	type ty struct{ name, size, weight string }
	for _, c := range []ty{
		{"date", "36px", "700"},
		{"key-figure", "22px", "700"},
		{"person-name", "19px", "700"},
		{"event-title", "16px", "600"},
		{"body", "16px", "400"},
		{"label", "14px", "400"},
		{"label-large", "15px", "400"},
		{"meta", "13px", "400"},
	} {
		if got := root["--fs-"+c.name]; got != c.size {
			t.Errorf("css --fs-%s = %q, want %s", c.name, got, c.size)
		}
		if got := root["--fw-"+c.name]; got != c.weight {
			t.Errorf("css --fw-%s = %q, want %s", c.name, got, c.weight)
		}
		e := d.Typography[c.name]
		if e["fontFamily"] != "Figtree" || e["fontSize"] != c.size || e["fontWeight"] != c.weight {
			t.Errorf("DESIGN.md typography.%s = %v", c.name, e)
		}
	}
	if got := themed(t, css, "bw")["--fw-event-title"]; got != "700" {
		t.Errorf("bw --fw-event-title = %q, want 700", got)
	}
}

func TestBlackAndWhiteMarkersUseDistinctShapes(t *testing.T) {
	css := stylesheet(t)
	seen := map[string]string{}
	for _, shape := range []string{"circle-filled", "circle-empty", "square-filled", "square-empty", "triangle-filled", "diamond-filled"} {
		m := regexp.MustCompile(`\[data-theme="bw"\] \.marker\[data-shape="` + shape + `"\]\s*\{([^}]*)\}`).FindStringSubmatch(css)
		if m == nil {
			t.Errorf("no bw marker rule for shape %s", shape)
			continue
		}
		body := strings.Join(strings.Fields(m[1]), " ")
		if other, dup := seen[body]; dup {
			t.Errorf("shape %s has the same rule as %s", shape, other)
		}
		seen[body] = shape
	}
}

// 5.22: cards carry no person markers; the who-line wraps, so the card grows
// and no name is ever cut (no ellipsis, no line clamp, no fixed height).
func TestWhoLineWrapsAndIsNeverCut(t *testing.T) {
	css := stylesheet(t)
	for _, sel := range []string{".event .who", ".strip-item .who", ".ev .who, .bar .who"} {
		rule := ruleFor(t, css, sel)
		for _, bad := range []string{"text-overflow", "line-clamp", "white-space: nowrap", "overflow: hidden", "height:"} {
			if strings.Contains(rule, bad) {
				t.Errorf("%s cuts the who-line: %s", sel, bad)
			}
		}
	}
	// Names are never split: a name breaks only when wider than the card, and the
	// who-line takes no automatic hyphens (the .ev/.bar rule above it has them).
	for _, sel := range []string{".event .who", ".strip-item .who", ".ev .who, .bar .who"} {
		rule := ruleFor(t, css, sel)
		if strings.Contains(rule, "anywhere") || !strings.Contains(rule, "overflow-wrap: break-word") || !strings.Contains(rule, "hyphens: manual") {
			t.Errorf("%s must use overflow-wrap: break-word and hyphens: manual: %s", sel, rule)
		}
	}
	if rule := ruleFor(t, css, ".who .nm"); !strings.Contains(rule, "display: inline-block") || !strings.Contains(rule, "max-width: 100%") {
		t.Errorf(".who .nm keeps a name whole: %q", rule)
	}
	if strings.Contains(css, ".markers") || strings.Contains(css, ".marker.small") {
		t.Error("the stylesheet still has card markers")
	}
}

// Every colour in DESIGN.md equals the custom property of the same role in
// that theme's CSS block, and every colour custom property has a token.
func TestDesignColoursAndStylesheetAgreeBothWays(t *testing.T) {
	css, d := stylesheet(t), design(t)
	hex := regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	for _, theme := range themes {
		block := cssTokens(t, css, theme)
		prefix := theme + "-"
		for name, want := range d.Colors {
			role, ok := strings.CutPrefix(name, prefix)
			if !ok {
				continue
			}
			got, ok := block["--"+role]
			if !ok {
				t.Errorf("DESIGN.md colors.%s has no --%s in the %s CSS block", name, role, theme)
				continue
			}
			if !strings.EqualFold(got, want) {
				t.Errorf("%s: css --%s = %s, DESIGN.md colors.%s = %s", theme, role, got, name, want)
			}
		}
		for prop, val := range block {
			if !hex.MatchString(val) {
				continue
			}
			if _, ok := d.Colors[prefix+strings.TrimPrefix(prop, "--")]; !ok {
				t.Errorf("%s: css %s has no DESIGN.md token colors.%s%s", theme, prop, prefix, strings.TrimPrefix(prop, "--"))
			}
		}
	}
}

// Component rules take colours from tokens, never hard-coded hex values.
func TestComponentRulesDoNotHardCodeColours(t *testing.T) {
	css := stylesheet(t)
	for _, line := range strings.Split(css, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") || strings.HasPrefix(strings.TrimSpace(line), "/*") {
			continue
		}
		if regexp.MustCompile(`#[0-9A-Fa-f]{3,6}\b`).MatchString(line) {
			t.Errorf("hard-coded colour in component rule: %s", strings.TrimSpace(line))
		}
	}
}

// --- contrast ---

func lum(t *testing.T, hex string) float64 {
	t.Helper()
	if !regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`).MatchString(hex) {
		t.Fatalf("not a hex colour: %q", hex)
	}
	ch := func(s string) float64 {
		v, _ := strconv.ParseUint(s, 16, 8)
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(hex[1:3]) + 0.7152*ch(hex[3:5]) + 0.0722*ch(hex[5:7])
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := lum(t, a), lum(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestContrastOnEveryPaletteReachesAA(t *testing.T) {
	css := stylesheet(t)
	for _, theme := range themes {
		tok := themed(t, css, theme)
		check := func(fg, bg string, min float64) {
			t.Helper()
			if c := contrast(t, tok["--"+fg], tok["--"+bg]); c < min {
				t.Errorf("%s: %s on %s = %.2f:1, want >= %.1f", theme, fg, bg, c, min)
			}
		}
		for _, bg := range []string{"background", "surface"} {
			check("text", bg, 4.5)
			check("text-secondary", bg, 4.5)
		}
		if theme == "bw" {
			check("text", "event-card", 4.5)
		}
		check("text", "error-bg", 4.5)
		check("error-text", "error-bg", 4.5)
		check("notice-text", "notice-bg", 4.5)
		for i := 1; i <= 6; i++ {
			n := strconv.Itoa(i)
			check("person-"+n+"-text", "person-"+n+"-tint", 4.5) // text on person tints
			check("person-"+n, "surface", 3)                     // marker against the column
		}
		// accent is text (links, .button.secondary) and the button background.
		check("accent", "background", 4.5)
		check("accent", "surface", 4.5)
		check("accent-text", "accent", 4.5)
	}
}

// --- fonts ---

func TestFontsLoadFromTheBoxOnly(t *testing.T) {
	css := stylesheet(t)
	if strings.Contains(css, "//") && regexp.MustCompile(`(?:https?:)?//[a-z0-9]`).MatchString(css) {
		t.Error("stylesheet references an outside host")
	}
	srcs := regexp.MustCompile(`url\(\s*["']?([^"')]+)`).FindAllStringSubmatch(css, -1)
	if len(srcs) == 0 {
		t.Fatal("no @font-face url() in stylesheet")
	}
	for _, s := range srcs {
		if !strings.HasPrefix(s[1], "/static/fonts/") {
			t.Errorf("url(%s) does not point at the box", s[1])
			continue
		}
		if _, err := fs.Stat(web.Static, "static/"+strings.TrimPrefix(s[1], "/static/")); err != nil {
			t.Errorf("font %s is not embedded: %v", s[1], err)
		}
	}
	if !strings.Contains(css, "font-family: Figtree") && !strings.Contains(css, `font-family: "Figtree"`) {
		t.Error("no Figtree @font-face")
	}
	if _, err := fs.Stat(web.Static, "static/fonts/OFL.txt"); err != nil {
		t.Error("OFL.txt licence missing next to the font")
	}
	// No page may pull in anything from outside either.
	tmpl, _ := fs.Glob(web.Templates, "templates/*.html")
	for _, f := range tmpl {
		b, _ := fs.ReadFile(web.Templates, f)
		if regexp.MustCompile(`(?:src|href)="(?:https?:)?//`).Match(b) {
			t.Errorf("%s loads an outside resource", f)
		}
	}
}

// ruleFor returns the declarations of the first rule whose selector is exactly sel.
func ruleFor(t *testing.T, css, sel string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)(?:^|\n|\{)\s*` + regexp.QuoteMeta(sel) + `\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatalf("no CSS rule for %q", sel)
	}
	return m[1]
}

// 5.11: every navigation target and button is at least 44 px (touch-target).
func TestFrameNavigationTargetsAreTouchSized(t *testing.T) {
	css := stylesheet(t)
	for _, sel := range []string{".frame-nav a", ".frame-bar .button", ".button"} {
		if rule := ruleFor(t, css, sel); !strings.Contains(rule, "min-height: var(--touch-target)") {
			t.Errorf("%s lacks min-height: var(--touch-target): %s", sel, rule)
		}
	}
	if tok := cssTokens(t, css, "light")["--touch-target"]; tok != "44px" {
		t.Errorf("--touch-target = %q, want 44px", tok)
	}
}

// 5.6: touch targets of the personal view are at least 44 px: the person link in
// the family view, the way back and the large checkbox.
func TestPersonalViewTargetsAreTouchSized(t *testing.T) {
	css := stylesheet(t)
	for _, sel := range []string{".fv .person-link", ".pv-back", ".big-check"} {
		rule := ruleFor(t, css, sel)
		if !strings.Contains(rule, "min-height: var(--touch-target)") {
			t.Errorf("%s lacks min-height: var(--touch-target): %s", sel, rule)
		}
	}
	if rule := ruleFor(t, css, ".big-check"); !strings.Contains(rule, "min-width: var(--touch-target)") {
		t.Errorf(".big-check lacks min-width: var(--touch-target): %s", rule)
	}
}

// 5.11: "follow the device" (ADR 0015) is the default: without data-theme the
// dark palette applies when the device asks for it, with the same values.
func TestDarkFollowsTheDeviceByDefaultWithTheSameValues(t *testing.T) {
	css := stylesheet(t)
	m := regexp.MustCompile(`(?s)@media \(prefers-color-scheme: dark\) \{\s*:root:not\(\[data-theme\]\) \{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatal("no prefers-color-scheme: dark block for :root:not([data-theme])")
	}
	got := map[string]string{}
	for _, d := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`).FindAllStringSubmatch(m[1], -1) {
		got[d[1]] = strings.TrimSpace(d[2])
	}
	for k, v := range cssTokens(t, css, "dark") {
		if got[k] != v {
			t.Errorf("device-dark %s = %q, dark theme has %q", k, got[k], v)
		}
	}
	if len(got) != len(cssTokens(t, css, "dark")) {
		t.Errorf("device-dark has %d tokens, dark theme %d", len(got), len(cssTokens(t, css, "dark")))
	}
}

// 5.11: the frame is responsive: from 900 px the navigation sits beside the
// content in a grid.
func TestFrameHasAWideLayout(t *testing.T) {
	m := regexp.MustCompile(`(?s)@media \(min-width: 900px\) \{(.*?)\n\}`).FindStringSubmatch(stylesheet(t))
	if m == nil {
		t.Fatal("no min-width: 900px media query for the wide layout")
	}
	if !regexp.MustCompile(`\.frame\.has-nav\s*\{[^}]*grid-template-columns`).MatchString(m[1]) {
		t.Errorf("900 px query has no .frame.has-nav grid rule:\n%s", m[1])
	}
}

// Displays are untouched by the web frame: the padding changes only apply to
// body.web, never to a bare body.
func TestFramePaddingAppliesOnlyToWebPages(t *testing.T) {
	css := stylesheet(t)
	if rule := ruleFor(t, css, "body"); !strings.Contains(rule, "padding: var(--margin)") {
		t.Errorf("bare body padding changed: %s", rule)
	}
	if !regexp.MustCompile(`body\.web\s*\{`).MatchString(css) {
		t.Error("no body.web rule")
	}
}

// The family view layout of docs/spec.md ("Layout of the family view"): a
// grid with one white column per person, header above, footer tiles below,
// spaced with the spec tokens. A CSS regression in these rules fails here.
func TestFamilyViewLayoutRulesFollowTheSpec(t *testing.T) {
	css := stylesheet(t)
	rule := func(sel string) string {
		m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(sel) + `\s*\{([^}]*)\}`).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("no CSS rule for %q", sel)
		}
		return m[1]
	}
	for sel, wants := range map[string][]string{
		".family-page":          {"height: calc(100vh - 2 * var(--margin))"},
		".fv":                   {"display: flex", "flex-direction: column", "gap: var(--gap-section)"},
		".fv-header":            {"display: flex", "justify-content: space-between", "flex: none"},
		".fv-grid":              {"display: grid", "grid-auto-flow: column", "gap: var(--gap)", "flex: 1"},
		".fv .events":           {"gap: var(--gap-cards)", "overflow-y: auto"},
		".fv .events .tomorrow": {"opacity: 0.6"}, // ADR 0016: the tomorrow block is faded
		".strip.tomorrow":       {"opacity: 0.6"},
		".fv-footer":            {"display: flex", "gap: var(--gap)", "flex: none"},
		".fv-footer .tile":      {"flex: 1", "padding: var(--pad-column)"},
		".strip-item":           {"background: var(--surface)", "border-radius: var(--radius-card)", "padding: var(--pad-card)"},
	} {
		body := rule(sel)
		for _, w := range wants {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q (has %q)", sel, w, strings.TrimSpace(body))
			}
		}
	}
}
