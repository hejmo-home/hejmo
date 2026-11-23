package list_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/list"
)

func words(w string) (string, bool) {
	m := map[string]string{"kg": "kg", "g": "g", "stück": "piece", "dozen": "dozen", "packung": "pack", "packungen": "pack", "l": "l"}
	k, ok := m[w]
	return k, ok
}

func TestSplitTitle(t *testing.T) {
	cases := []struct {
		title, name string
		amount      float64
		unit        string
		bare        bool
	}{
		{"Mehl · 1,5 kg", "Mehl", 1.5, "kg", false},
		{"Mehl 2 kg", "Mehl", 2, "kg", false},
		{"Mehl - 500 g", "Mehl", 500, "g", false},
		{"Mehl, 2 kg", "Mehl", 2, "kg", false},
		{"Mehl 500g", "Mehl", 500, "g", false},
		{"Eier · 10 Stück", "Eier", 10, "piece", false},
		{"Nudeln 2 Packungen", "Nudeln", 2, "pack", false},
		{"Eier 6", "Eier", 6, "", true},
		{"Milch", "Milch", 0, "", false},
		{"Käse 2 Scheiben", "Käse 2 Scheiben", 0, "", false}, // unit not active or unknown: name only
		{"Vitamin B12", "Vitamin B12", 0, "", false},
		{"6", "6", 0, "", false},
		{"Mehl 0 kg", "Mehl 0 kg", 0, "", false},
		{"Mehl 10000 kg", "Mehl 10000 kg", 0, "", false},
		{"  Mehl  ·  2 kg ", "Mehl", 2, "kg", false},
	}
	for _, c := range cases {
		got := list.SplitTitle(c.title, words)
		if got.Name != c.name || got.Amount != c.amount || got.Unit != c.unit || got.Bare != c.bare {
			t.Errorf("SplitTitle(%q) = %+v, want %s %v %q bare=%v", c.title, got, c.name, c.amount, c.unit, c.bare)
		}
	}
}

func TestJoinTitle(t *testing.T) {
	if got := list.JoinTitle("Mehl", "1,5 kg"); got != "Mehl · 1,5 kg" {
		t.Fatal(got)
	}
	if got := list.JoinTitle("Milch", ""); got != "Milch" {
		t.Fatal(got)
	}
}
