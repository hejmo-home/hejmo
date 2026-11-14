package qrsvg_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/qrsvg"
)

var moduleRe = regexp.MustCompile(`M(\d+) (\d+)h1v1h-1z`)
var sizeRe = regexp.MustCompile(`viewBox="0 0 (\d+) (\d+)"`)

// parse reads the dark modules back from the SVG.
func parse(t *testing.T, svg string) (size int, dark map[[2]int]bool) {
	t.Helper()
	m := sizeRe.FindStringSubmatch(svg)
	if m == nil {
		t.Fatalf("no viewBox in %q", svg)
	}
	size, _ = strconv.Atoi(m[1])
	dark = map[[2]int]bool{}
	for _, d := range moduleRe.FindAllStringSubmatch(svg, -1) {
		x, _ := strconv.Atoi(d[1])
		y, _ := strconv.Atoi(d[2])
		dark[[2]int{x, y}] = true
	}
	return size, dark
}

// Every QR code has a 7x7 finder pattern in three corners: a dark ring, a
// light ring and a dark 3x3 centre (ISO/IEC 18004).
func TestCodeHasFinderPatternsInThreeCorners(t *testing.T) {
	svg, err := qrsvg.SVG("https://hejmo.local/displays?code=123456", "Pair a display")
	if err != nil {
		t.Fatal(err)
	}
	size, dark := parse(t, svg)
	const quiet = 4 // light border around the code
	origin := func(x, y int) [2]int { return [2]int{quiet + x, quiet + y} }
	n := size - 2*quiet
	for _, corner := range [][2]int{{0, 0}, {n - 7, 0}, {0, n - 7}} {
		for y := 0; y < 7; y++ {
			for x := 0; x < 7; x++ {
				ring := max(abs(x-3), abs(y-3))
				want := ring != 2 // ring 3 and 0..1 dark, ring 2 light
				if got := dark[origin(corner[0]+x, corner[1]+y)]; got != want {
					t.Fatalf("finder at %v, module (%d,%d) dark=%v, want %v", corner, x, y, got, want)
				}
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestSVGIsLabelledAndEscapesTheLabel(t *testing.T) {
	svg, err := qrsvg.SVG("x", `a "<b>" & c`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(svg, `role="img"`) || !strings.Contains(svg, `aria-label="a &#34;&lt;b&gt;&#34; &amp; c"`) {
		t.Fatalf("label missing or unescaped: %s", svg)
	}
	if strings.Contains(svg, "<script") {
		t.Fatal("script in svg")
	}
}
