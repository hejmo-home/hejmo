package web_test

import (
	"math"
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
// fully visible on a w by h viewport. The viewport decides the orientation,
// so a rotated display switches limit by itself.
func visibleColumns(t *testing.T, css string, w, h float64, n int) int {
	t.Helper()
	root := cssTokens(t, css, "light")
	limit, err := strconv.Atoi(root["--cols-max"])
	if err != nil {
		t.Fatalf("--cols-max = %q", root["--cols-max"])
	}
	if h > w {
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
	return vis
}

// Product owner, 14/11/2026 (ADR 0016): at most 6 person columns in landscape,
// 4 in portrait, each at least about 150 px; beyond that they scroll sideways.
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
		{"rotated: the same 5 people, portrait then landscape", 1024, 768, 5, 5},
	}
	for _, c := range cases {
		if got := visibleColumns(t, css, c.w, c.h, c.people); got != c.want {
			t.Errorf("%s: %d columns visible, want %d", c.name, got, c.want)
		}
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
	for _, want := range []string{"grid-auto-flow: column", "overflow-x: auto", "var(--col-min)", "var(--cols-max)", "var(--cols)"} {
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
