package recipe_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/library"
	"github.com/hejmo-home/hejmo/internal/recipe"
)

var arts = []library.Article{
	{ID: "lentils", De: "Linsen", En: "Lentils"},
	{ID: "yoghurt", De: "Joghurt", En: "Yoghurt"},
	{ID: "cumin", De: "Kreuzkümmel", En: "Cumin"},
	{ID: "oil", De: "Öl", En: "Oil"},
	{ID: "olive-oil", De: "Olivenöl", En: "Olive oil"},
	{ID: "beet", De: "Rote Beete", En: "Beetroot"},
	{ID: "egg", De: "Ei", En: "Eggs"},
}

func TestProposeFindsArticlesThatOccurInTheText(t *testing.T) {
	cases := []struct {
		text, lang, id, variety string
	}{
		{"rote Linsen", "de", "lentils", "rot"},
		{"Naturjoghurt", "de", "yoghurt", "Natur"},
		{"Linse", "de", "lentils", ""},
		{"gemahlener Kreuzkümmel", "de", "cumin", "gemahlener"},
		{"red lentils", "en", "lentils", "red"},
		{"Rote Beete, vorgekocht", "de", "beet", "vorgekocht"},
		{"extra natives Olivenöl", "de", "olive-oil", "extra natives"},
	}
	for _, c := range cases {
		got := recipe.Propose(c.text, arts, c.lang, 3)
		if len(got) == 0 || got[0].Article.ID != c.id || got[0].Variety != c.variety {
			t.Errorf("Propose(%q) = %+v, want %s with variety %q", c.text, got, c.id, c.variety)
		}
	}
}

func TestProposeNothingAndLimits(t *testing.T) {
	if got := recipe.Propose("Harissa", arts, "de", 3); len(got) != 0 {
		t.Errorf("Harissa = %+v", got)
	}
	if got := recipe.Propose("Olivenöl und Joghurt und Linsen und Eier", arts, "de", 2); len(got) != 2 {
		t.Errorf("limit: %+v", got)
	}
	// the longest name first: "Olivenöl" before "Öl"
	if got := recipe.Propose("Olivenöl", arts, "de", 3); len(got) != 1 || got[0].Article.ID != "olive-oil" {
		t.Errorf("exact: %+v", got)
	}
	long := "Joghurt " + string(make([]rune, 0)) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got := recipe.Propose(long, arts, "de", 3); len(got) != 1 || got[0].Variety != "" {
		t.Errorf("a variety over 60 letters is not proposed: %+v", got)
	}
}
