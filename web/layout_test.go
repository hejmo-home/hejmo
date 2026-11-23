package web_test

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// px reads a "144px" token value.
func px(t *testing.T, v string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(strings.TrimSuffix(v, "px"), 64)
	if err != nil {
		t.Fatalf("not a px value: %q", v)
	}
	return f
}

// portraitTokens returns the custom properties the stylesheet overrides in
// portrait: the :root block inside @media (orientation: portrait).
func portraitTokens(t *testing.T, css string) map[string]string {
	t.Helper()
	m := regexp.MustCompile(`(?s)@media \(orientation: portrait\)\s*\{\s*:root\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatal("no :root block inside @media (orientation: portrait)")
	}
	out := map[string]string{}
	for _, d := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`).FindAllStringSubmatch(m[1], -1) {
		out[d[1]] = strings.TrimSpace(d[2])
	}
	return out
}

// visibleColumns models the family view grid of hejmo.css: the columns share
// the width up to the orientation's limit, none is narrower than --col-min,
// and the rest scrolls sideways. It returns how many of n person columns are
// fully visible on a w by h viewport, and how wide a column is. The viewport
// decides the orientation, so a rotated display switches limit by itself.
func visibleColumns(t *testing.T, css string, w, h float64, n int) (int, float64) {
	t.Helper()
	root := cssTokens(t, css, "light")
	limit, err := strconv.Atoi(root["--cols-max"])
	if err != nil {
		t.Fatalf("--cols-max = %q", root["--cols-max"])
	}
	if h >= w { // CSS orientation: portrait also matches a square viewport
		limit, err = strconv.Atoi(portraitTokens(t, css)["--cols-max"])
		if err != nil {
			t.Fatal("no --cols-max in portrait")
		}
	}
	margin, gap, min := px(t, root["--margin"]), px(t, root["--gap"]), px(t, root["--col-min"])
	if w < 600 || h <= 500 { // ticket 208: a phone's page padding (also in landscape) is --gap, not --margin
		margin = gap
	}
	avail := w - 2*margin
	share := math.Min(float64(n), float64(limit))
	colw := math.Max(min, (avail-(share-1)*gap)/share)
	vis := int(math.Floor((avail+gap)/(colw+gap) + 1e-9))
	if vis > n {
		vis = n
	}
	return vis, colw
}

// Proposed in ADR 0016 (pending approval of preview-27): at most 6 person columns in landscape,
// 4 in portrait, each at least about 150 px; beyond that they scroll sideways.
// Rotating the display switches the limit and back: the same five people on
// the same device, portrait then landscape, are asserted as one case below.
func TestColumnLimitsPerOrientationAndWidth(t *testing.T) {
	css := stylesheet(t)
	cases := []struct {
		name         string
		w, h         float64
		people, want int
	}{
		{"iPad portrait, 5 people: 4 visible, the fifth by scrolling", 768, 1024, 5, 4},
		{"iPad portrait, 4 people", 768, 1024, 4, 4},
		{"iPhone portrait, 4 people: 2 visible", 375, 812, 4, 2},
		{"iPad Air portrait, 5 people", 820, 1180, 5, 4},
		{"iPad Air portrait, 3 people share the width", 820, 1180, 3, 3},
		{"E-Ink landscape, 6 people all visible", 1024, 768, 6, 6},
		{"E-Ink landscape, 7 people: 6 visible, then scroll", 1024, 768, 7, 6},
		{"E-Ink landscape, 4 people", 1024, 768, 4, 4},
		{"iPad Air landscape, 8 people", 1180, 820, 8, 6},
		{"square viewport counts as portrait", 800, 800, 5, 4},
	}
	for _, c := range cases {
		if got, _ := visibleColumns(t, css, c.w, c.h, c.people); got != c.want {
			t.Errorf("%s: %d columns visible, want %d", c.name, got, c.want)
		}
	}
	// Rotation in one case: the same device and people, both orientations.
	portrait, _ := visibleColumns(t, css, 768, 1024, 5)
	landscape, _ := visibleColumns(t, css, 1024, 768, 5)
	if portrait != 4 || landscape != 5 {
		t.Errorf("rotating 5 people: portrait %d (want 4), landscape %d (want 5)", portrait, landscape)
	}
}

// The grid applies that model: columns flow sideways, never narrower than the
// token, and the grid scrolls (no per-view scroll flag).
func TestFamilyGridUsesColumnTokens(t *testing.T) {
	css := stylesheet(t)
	m := regexp.MustCompile(`(?s)\n\.fv-grid\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil {
		t.Fatal("no .fv-grid rule")
	}
	for _, want := range []string{
		"grid-auto-flow: column", "overflow-x: auto",
		"--visible: min(var(--cols), var(--cols-max));",
		"grid-auto-columns: max(var(--col-min), calc((100% - (var(--visible) - 1) * var(--gap)) / var(--visible)));",
	} {
		if !strings.Contains(m[1], want) {
			t.Errorf(".fv-grid lacks %q", want)
		}
	}
	if strings.Contains(css, "data-scroll") {
		t.Error("stylesheet still keys scrolling on data-scroll")
	}
}

func TestColumnMinimumIsInDesignMd(t *testing.T) {
	d := design(t)
	if got := d.Sizes["column-min"]; got != "144px" {
		t.Errorf("DESIGN.md sizes.column-min = %q, want 144px", got)
	}
	if got := cssTokens(t, stylesheet(t), "light")["--col-min"]; got != "144px" {
		t.Errorf("css --col-min = %q, want 144px", got)
	}
}

// The page fits the display: dynamic viewport height (iOS toolbars) with a
// plain vh fallback, a header that wraps instead of overflowing, and footer
// tiles that grow from a 200 px basis and wrap.
func TestFamilyPageFitsNarrowAndShortDisplays(t *testing.T) {
	css := stylesheet(t)
	rule := func(sel string) string {
		m := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(sel) + `\s*\{([^}]*)\}`).FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("no %s rule", sel)
		}
		return m[1]
	}
	page := rule(".family-page")
	vh, dvh := strings.Index(page, "height: calc(100vh - 2 * var(--margin));"), strings.Index(page, "height: calc(100dvh - 2 * var(--margin));")
	if vh < 0 || dvh < 0 || vh > dvh {
		t.Errorf(".family-page needs the 100vh fallback before the 100dvh height: %q", page)
	}
	if h := rule(".fv-header"); !strings.Contains(h, "flex-wrap: wrap;") {
		t.Errorf(".fv-header does not wrap: %q", h)
	}
	if f := rule(".fv-footer"); !strings.Contains(f, "flex-wrap: wrap;") {
		t.Errorf(".fv-footer does not wrap: %q", f)
	}
	if tile := rule(".fv-footer .tile"); !strings.Contains(tile, "flex: 1 1 200px;") || !strings.Contains(tile, "min-width: 0;") {
		t.Errorf(".fv-footer .tile must grow from 200px and be shrinkable: %q", tile)
	}
}

// Readability snapshot (acceptance criterion 2). For each reference device and
// number of people it records how many columns are visible, how wide a column
// is, how much room its content has inside the padding, and whether the
// longest example name (with its marker) fits that room. A very long single
// word ("fits", "wraps" at the column edge, or "TOO NARROW" if clipped) is
// recorded too. Text width is
// estimated at 0.51 em per bold character (measured 0.50 em for "Sebastian" in Figtree 700 at 19 px).
// HEJMO_UPDATE_GOLDEN=1 rewrites the golden after a reviewed change.
func TestColumnReadabilitySnapshot(t *testing.T) {
	css := stylesheet(t)
	root := cssTokens(t, css, "light")
	pad, fs := px(t, root["--pad-column"]), px(t, root["--fs-person-name"])
	marker := px(t, themed(t, css, "bw")["--marker-size-bw-column"]) // the larger of the two themes
	const longest = "Sebastian"
	const veryLong = "Konstantinos" // 12 characters, a single word: must wrap, not clip
	need := func(name string) float64 { return float64(len(name))*0.51*fs + 8 + marker }
	// A name wraps at the column edge only if it is its own shrinkable flex
	// item (min-width: 0) with overflow-wrap: break-word, and the template
	// puts the name in that element.
	nameRule := regexp.MustCompile(`(?m)^\.fv \.person-name\s*\{([^}]*)\}`).FindStringSubmatch(css)
	wraps := nameRule != nil && strings.Contains(nameRule[1], "overflow-wrap: break-word;") && strings.Contains(nameRule[1], "min-width: 0;") &&
		familyTemplateHasNameSpan(t)

	devices := []struct {
		name string
		w, h float64
	}{
		{"iPhone portrait 375x812", 375, 812}, {"iPad portrait 768x1024", 768, 1024},
		{"iPad Air portrait 820x1180", 820, 1180}, {"E-Ink landscape 1024x768", 1024, 768},
	}
	var b strings.Builder
	for _, d := range devices {
		for _, n := range []int{3, 4, 5, 6} {
			vis, colw := visibleColumns(t, css, d.w, d.h, n)
			content := colw - 2*pad
			verdict := func(name string, mustFit bool) string {
				switch {
				case content >= need(name):
					return "fits"
				case !mustFit && wraps:
					return "wraps"
				}
				return "TOO NARROW"
			}
			fmt.Fprintf(&b, "%s, %d people: %d visible, column %.0f px, content %.0f px, %q %s (needs %.0f px), %q %s (needs %.0f px)\n",
				d.name, n, vis, colw, content, longest, verdict(longest, true), need(longest), veryLong, verdict(veryLong, false), need(veryLong))
		}
	}
	got := b.String()
	if strings.Contains(got, "TOO NARROW") {
		t.Errorf("a person name does not fit its column:\n%s", got)
	}
	const golden = "testdata/column_layout.golden"
	if os.Getenv("HEJMO_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (HEJMO_UPDATE_GOLDEN=1 writes it)", err)
	}
	if got != string(want) {
		t.Errorf("layout differs from %s (HEJMO_UPDATE_GOLDEN=1 rewrites it after a reviewed change):\n%s", golden, got)
	}
}

// Ticket #135: running text keeps the width of the cards at every screen
// width, and a link used as a button inside a card keeps its button text
// colour. (The wide frame grid is covered by
// TestFrameLayoutCoversTheBackLinkAndNarrowScreens.)
func TestRunningTextAndLinkButtonsInCards(t *testing.T) {
	css := stylesheet(t)
	if !strings.Contains(css, ".lead, main > .hint, section > .hint { max-width: 560px; }") {
		t.Error("running text is not limited to 560 px")
	}
	if !strings.Contains(css, ".cards a.button, .steps a.button { color: var(--accent-text); font-weight: 700; }") ||
		!strings.Contains(css, ".cards a.button.secondary, .steps a.button.secondary { color: var(--accent); }") {
		t.Error("link buttons in cards and steps lose their text colour")
	}
}

// Ticket 133: the navigation row of a framed page must not widen the page on a
// 375 px phone. The grid has one column that may shrink to nothing, the
// navigation may shrink and scrolls sideways inside its own row, and the
// two-column grid with navigation starts at 900 px only.
func TestFramedPagesAreNeverWiderThanAPhone(t *testing.T) {
	css := stylesheet(t)
	for _, want := range []string{
		`.frame { grid-template-columns: minmax(0, 1fr); }`,
		`.frame-nav { min-width: 0; }`,
		`@media (min-width: 900px) { .frame.has-nav { grid-template-columns: max-content minmax(0, 1fr); } }`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet lacks %q", want)
		}
	}
	// Design pass 2: the row wraps on a phone, nothing is cut or hidden (as in #86).
	if !strings.Contains(css, `.frame-nav ul { flex-wrap: wrap; overflow-x: visible; }`) {
		t.Error("the navigation row does not wrap")
	}
}

// Ticket 133, design pass 2: the components of the first steps, the picker, the
// today card, the child's day and the outside links exist; the language
// switcher rules of #134 are there once only.
func TestStylesheetHasTheDesignPass2Components(t *testing.T) {
	css := stylesheet(t)
	for _, sel := range []string{
		`.wsteps .wiz-count {`, "a.ext {", ".copyrow {", ".place-pick {", ".place-list {", ".place-status.ok {", ".fs-bar {", ".fs-head {", ".fs-state[data-state=\"done\"]",
		".fs-acts {", ".link.quiet {", ".fs-foot {", ".who-chips {", ".fs-sub, .fs-rows {", ".fs-rows .for {", ".today {", ".today-fam ", ".today-people {",
		".tev", ".today-meal {", ".child-h .marker {", ".today.child {", ".ctasks {", ".icon.star {", `ol.howto > li[data-skipped]::before`, "button.hub-row {",
	} {
		if !strings.Contains(css, sel) {
			t.Errorf("stylesheet lacks %q", sel)
		}
	}
	if n := strings.Count(css, "\n.lang-menu {"); n != 1 {
		t.Errorf("the language switcher rules are there %d times, want once", n)
	}
	if !regexp.MustCompile(`\.wsteps \.wiz-count \{[^}]*margin: 0 0 8px[^}]*text-align: left[^}]*color: var\(--text-secondary\)`).MatchString(css) {
		t.Error("the step count is not left aligned with an 8 px gap")
	}
}

// Ticket 133: the setup wizard's components exist in the stylesheet.
func TestStylesheetHasTheWizardComponents(t *testing.T) {
	css := stylesheet(t)
	for _, sel := range []string{
		".wizard {", ".wiz-count", ".wsteps ol", "ol.howto", ".devices", ".detected", `.checkline[data-state="waiting"]`,
		`.checkline[data-state="ok"]`, `.checkline[data-state="fail"]`, ".fp {", ".addr", ".secret.grouped", "ol.codes", ".confirm",
		".pw {", ".firststeps", ".actions", ".link {", ".button[disabled]", `[data-theme="bw"] .button[disabled]`, "@media print",
	} {
		if !strings.Contains(css, sel) {
			t.Errorf("stylesheet lacks %q", sel)
		}
	}
}

// #150 AC 1: with 8 people the names from the issue (Maximilian, Anna-Lena,
// Konstantin) must not break inside a word at 1024x768 and 768x1024, in both
// themes. Widths are browser measurements (Figtree 700, 19 px, one line,
// measured with getBoundingClientRect on a nowrap span); the snapshot's 0.51 em
// estimate is too coarse here (121 px for Maximilian, the browser 94). Anna-Lena
// may break at its hyphen, so only its longer half "Anna-" (5 of 9 characters)
// has to fit; that half is the proportional share of the measured 94.7 px, not
// a separate measurement. The bw theme has a 16 px marker and a 6 px gap in the
// name row (.fv .person-link, the flex row of marker and name) (light: 14 px and 8 px); the room is the column minus padding, gap
// and marker.
func TestIssueNamesFitWithEightPeople(t *testing.T) {
	css := stylesheet(t)
	root := cssTokens(t, css, "light")
	pad := px(t, root["--pad-column"])
	measured := map[string]float64{"Maximilian": 93.7, "Anna-Lena": 94.7 * 5 / 9, "Konstantin": 94.6}
	lightGap := px(t, regexp.MustCompile(`gap:\s*(\d+px)`).FindStringSubmatch(ruleFor(t, css, ".fv .person-link"))[1])
	bwGap := lightGap
	bwNameRow := regexp.MustCompile(`(?m)^\[data-theme="bw"\] \.fv \.person-link\s*\{[^}]*\}`)
	if m := regexp.MustCompile(`gap:\s*(\d+px)`).FindStringSubmatch(bwNameRow.FindString(css)); m != nil {
		bwGap = px(t, m[1])
	}
	for _, theme := range []struct {
		name        string
		marker, gap float64
	}{
		{"light", px(t, root["--marker-size"]), lightGap},
		{"bw", px(t, themed(t, css, "bw")["--marker-size-bw-column"]), bwGap},
	} {
		for _, d := range []struct {
			name string
			w, h float64
		}{{"1024x768", 1024, 768}, {"768x1024", 768, 1024}} {
			_, colw := visibleColumns(t, css, d.w, d.h, 8)
			room := colw - 2*pad - theme.gap - theme.marker
			for n, w := range measured {
				if w > room {
					t.Errorf("%s %s, 8 people: %q needs %.1f px, the name has %.1f px", theme.name, d.name, n, w, room)
				}
			}
		}
	}
}

// Ticket 208: with the 14 px phone padding the third column shows about 31 px
// at the edge of a 375 px phone, which says without a symbol that there is more.
func TestPhonePaddingLeavesAPeekOfTheThirdColumn(t *testing.T) {
	css := stylesheet(t)
	vis, colw := visibleColumns(t, css, 375, 812, 3)
	gap := px(t, cssTokens(t, css, "light")["--gap"])
	peek := 375 - 2*gap - float64(vis)*colw - float64(vis)*gap
	if vis != 2 || colw != 144 || math.Round(peek) != 31 {
		t.Errorf("375 px: %d columns of %.0f px, %.0f px of the next; want 2, 144, 31", vis, colw, peek)
	}
}
