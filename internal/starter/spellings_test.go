package starter

import "testing"

// Review of #329: a key nobody can reach is a spelling that was reviewed for
// nothing ("Mehl mit Backpulver (Self-raising flour)" never matched).
func TestEverySpellingResolvesAtLeastOneLineOfTheFiles(t *testing.T) {
	load()
	hit := map[string]int{}
	for _, e := range entries {
		for _, in := range e.raw.Ingredients {
			if key, ok := spellKey(in.ArticleDE); ok {
				hit[key]++
			}
		}
	}
	for key := range spellings {
		if hit[key] == 0 {
			t.Errorf("no line of the files resolves through the spelling %q", key)
		}
	}
}
