package religiousholiday

import (
	"time"

	"github.com/hejmo-home/hejmo/internal/holiday"
)

// Plan is what the calendar holds for one religion: the holidays it gets and
// the ones left out because the region has them as public holidays already.
type Plan struct {
	Religion Religion
	Kept     []Holiday
	Skipped  []Skipped
	// TableEnd is the last year a table of the religion covers (0: the dates are computed with no end).
	TableEnd int
}

// PlanFor lists, for each religion, the holidays that start from fromYear to
// toYear and are not public holidays of the region (an empty or unknown region
// has none). The religions keep the order given.
func PlanFor(religions []Religion, o Options, regionCode string, fromYear, toYear int) []Plan {
	public := map[time.Time]bool{}
	for y := fromYear - 1; y <= toYear+1; y++ {
		for _, h := range holiday.For(regionCode, y) {
			public[h.Date] = public[h.Date] || h.Partial
		}
	}
	isPublic := func(t time.Time) (bool, bool) {
		p, ok := public[t]
		return p, ok
	}
	var out []Plan
	for _, r := range religions {
		kept, skipped := Skip(Compute(r, o, fromYear, toYear), isPublic)
		out = append(out, Plan{Religion: r, Kept: kept, Skipped: skipped, TableEnd: TableEnd(r, o)})
	}
	return out
}
