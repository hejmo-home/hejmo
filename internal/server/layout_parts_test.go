package server_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

// layoutPartsCoverEveryPageOnce checks the split of the pages into parts. It runs
// as a subtest of TestEveryPageIsInTheRenderedLayoutCheck, on the pages that test
// already crawled: a second crawl in its own test held its own fixture, server and
// pages, and under -race that was most of the memory of the whole package (ticket 238).
func layoutPartsCoverEveryPageOnce(t *testing.T, pages []layoutPage) {
	t.Helper()
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

func TestLayoutPartSummaryShowsSlowestCasesAndHeadroom(t *testing.T) {
	t.Parallel()
	took := []layoutTook{{"de/a", time.Second}, {"en/b", 5 * time.Second}, {"de/c", 3 * time.Second}}
	got := layoutPartSummary(1, took, layoutBudget/2)
	for _, want := range []string{fmt.Sprintf("layout part 2: 3 cases in %s of %s budget (50 %% headroom)", layoutBudget/2, layoutBudget), "slow case: en/b took 5s", "slow case: de/a took 1s"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "en/b") > strings.Index(got, "de/c") {
		t.Errorf("slowest case not first:\n%s", got)
	}
	if took[0].name != "de/a" {
		t.Error("the summary reordered the caller's slice")
	}
}

func TestLayoutSummaryIsAppendedToTheJobSummaryFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "summary.md")
	for _, text := range []string{"one\n", "two\n"} {
		if err := appendLayoutSummary(path, text); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	if want := "```text\none\n```\n```text\ntwo\n```\n"; string(b) != want {
		t.Errorf("got %q, want %q", b, want)
	}
}
