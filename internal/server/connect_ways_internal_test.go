package server

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
)

// Ticket 231: the choice page is built from calendarWays, so a way that joins
// later (Google, Outlook) needs one entry, its guide and its texts, nothing more.
func TestEveryCalendarWayCanBeShownAsACard(t *testing.T) {
	t.Parallel()
	b := i18n.MustLoad()
	seen := map[string]bool{}
	for _, w := range calendarWays {
		if w.ID == "" || seen[w.ID] {
			t.Errorf("way %q: empty or repeated id", w.ID)
		}
		seen[w.ID] = true
		known := false
		for _, s := range outbound.Services {
			known = known || s == w.Service
		}
		if !known {
			t.Errorf("way %s needs the unknown connection %q", w.ID, w.Service)
		}
		for _, lang := range []string{"de", "en"} {
			for _, k := range []string{w.Name, w.Sub, "outside.name." + string(w.Service)} {
				if got := b.T(lang, k); got == k || got == "" {
					t.Errorf("%s: way %s lacks the text %s", lang, w.ID, k)
				}
			}
		}
	}
	// Q156 order: iCloud first, the calendar link last.
	if calendarWays[0].ID != "icloud" || calendarWays[len(calendarWays)-1].ID != "ics" {
		t.Errorf("order = %v, want iCloud first and the link last", calendarWays)
	}
}
