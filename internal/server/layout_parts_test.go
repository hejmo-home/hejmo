package server_test

import "testing"

// The rendered layout check is split in two tests, one per CI shard with Chrome
// (ticket 347). A page lands in a part by a hash of its name, never by timing or
// map order, so the same page is always checked by the same shard.

func TestLayoutPartOfAPageIsStable(t *testing.T) {
	t.Parallel()
	// Worked values of FNV-1a (32 bit) of "site/path", modulo 2, computed outside the code.
	for key, want := range map[string]int{
		"parent/lists":         1,
		"display/display":      0,
		"child tablet/numbers": 0,
		"household box under its name/setup/passkey": 1,
	} {
		for range 3 {
			if got := layoutPartOfKey(key); got != want {
				t.Errorf("layoutPartOfKey(%q) = %d, want %d", key, got, want)
			}
		}
	}
}

func TestLayoutPartsCoverEveryPageOnce(t *testing.T) {
	t.Parallel()
	f := newLayoutFixture(t)
	pages := append(layoutPages(t, f), wizardPages(t)...)
	seen := map[string]int{}
	sizes := [layoutParts]int{}
	for part := range layoutParts {
		for _, p := range layoutPartPages(pages, part) {
			seen[p.site.name+p.path]++
			sizes[part]++
		}
	}
	want := map[string]int{} // a page listed twice stays listed twice, in one part
	for _, p := range pages {
		want[p.site.name+p.path]++
	}
	for key, n := range want {
		if seen[key] != n {
			t.Errorf("%s: %d times in the parts, want %d: a page must be in exactly one part", key, seen[key], n)
		}
	}
	for part, n := range sizes {
		if n < len(pages)/4 {
			t.Errorf("part %d has %d of %d pages: the hash does not spread them", part, n, len(pages))
		}
	}
}
