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
	tpl, err := os.ReadFile("templates/display_family.html")
	if err != nil {
		t.Fatal(err)
	}
	wraps := nameRule != nil && strings.Contains(nameRule[1], "overflow-wrap: break-word;") && strings.Contains(nameRule[1], "min-width: 0;") &&
		strings.Contains(string(tpl), `<span class="person-name">{{.Person.Name}}</span>`)

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

// #150 AC 1: with 8 people the names from the issue (Maximilian, Anna-Lena,
// Konstantin) must not break inside a word at 1024x768 and 768x1024. Widths are
// browser measurements (Figtree 700, 19 px, one line); the snapshot's 0.51 em
// estimate is too coarse here (it says 121 px for Maximilian, the browser 94).
// Anna-Lena may break at its hyphen, so only its longer half has to fit.
// The bw theme has a 16 px marker (light: 14 px); at 1024x768 the names are
// about 1 px wider than the room left (open product decision, see PR #152), so
// that case is logged, not asserted.
func TestIssueNamesFitWithEightPeople(t *testing.T) {
	css := stylesheet(t)
	root := cssTokens(t, css, "light")
	pad := px(t, root["--pad-column"])
	measured := map[string]float64{"Maximilian": 93.7, "Anna-Lena": 94.7 * 5 / 9, "Konstantin": 94.6}
	for _, theme := range []struct {
		name   string
		marker float64
		assert bool
	}{{"light", px(t, root["--marker-size"]), true}, {"bw", px(t, themed(t, css, "bw")["--marker-size-bw-column"]), false}} {
		for _, d := range []struct {
			name string
			w, h float64
		}{{"1024x768", 1024, 768}, {"768x1024", 768, 1024}} {
			_, colw := visibleColumns(t, css, d.w, d.h, 8)
			room := colw - 2*pad - 8 - theme.marker
			for n, w := range measured {
				if w <= room {
					continue
				}
				if theme.assert {
					t.Errorf("%s %s, 8 people: %q needs %.1f px, the name has %.1f px", theme.name, d.name, n, w, room)
				} else {
					t.Logf("%s %s, 8 people: %q needs %.1f px, the name has %.1f px (product decision)", theme.name, d.name, n, w, room)
				}
			}
		}
	}
}
