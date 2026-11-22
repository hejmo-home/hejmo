package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// AlwaysOptions configures the runner of "always on the list".
type AlwaysOptions struct {
	DB *store.DB
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Logger receives errors. Nil means slog.Default().
	Logger *slog.Logger
}

// AlwaysRunner puts articles on lists (ticket 168, Q27, Q43 to Q45): at the
// household's time of day on the weekday of a due week. It runs once a day,
// skips the holiday and catches nothing up, and switches the holiday off after
// its last day.
type AlwaysRunner struct {
	opts AlwaysOptions
	s    *server // only for the library and the units: no request is served
}

// NewAlwaysRunner returns a runner; call Run to start it.
func NewAlwaysRunner(opts AlwaysOptions) *AlwaysRunner {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &AlwaysRunner{opts: opts, s: &server{db: opts.DB, now: opts.Now, i18n: i18n.MustLoad()}}
}

// Run ticks every interval until ctx ends.
func (a *AlwaysRunner) Run(ctx context.Context, every time.Duration) {
	a.Tick(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.Tick(ctx)
		}
	}
}

// civil is the calendar day of a moment in the household's zone, as a date at UTC midnight.
func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Tick runs what is due now.
func (a *AlwaysRunner) Tick(ctx context.Context) {
	h, err := a.opts.DB.HomeHousehold()
	if err != nil {
		return // no household yet
	}
	if err := a.run(ctx, h); err != nil {
		a.opts.Logger.Error("always on the list not run", "err", err)
	}
}

func (a *AlwaysRunner) run(ctx context.Context, h store.Household) error {
	db := a.opts.DB
	now := a.s.householdNow(h)
	today := civil(now)
	day := today.Format(always.DateLayout)
	st, err := db.AlwaysSettings(h.ID)
	if err != nil {
		return err
	}
	if st.Holiday.On() && st.Holiday.Expired(today) { // the holiday switches itself off
		st.Holiday = always.Holiday{}
		if err := db.SetAlwaysSettings(h.ID, st); err != nil {
			return err
		}
	}
	if now.Format("15:04") < st.At {
		return nil
	}
	if last, err := db.AlwaysLastRun(h.ID); err != nil || last == day {
		return err
	}
	if err := db.SetAlwaysLastRun(h.ID, day); err != nil { // before the work: a failing rule must not run again and again
		return err
	}
	_ = db.PruneAlwaysRuns(h.ID, today.AddDate(0, 0, -14).Format(always.DateLayout))
	if st.Holiday.Covers(today) {
		return nil
	}
	rules, err := db.AlwaysRules(h.ID)
	if err != nil {
		return err
	}
	for _, r := range rules {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !r.Due(today) {
			continue
		}
		if err := a.put(h, r, day); err != nil {
			a.opts.Logger.Error("article not put on the list", "article", r.ArticleID, "err", err)
		}
	}
	return nil
}

// put puts one article on its list.
func (a *AlwaysRunner) put(h store.Household, r always.Rule, day string) error {
	lang := r.Lang
	if lang == "" {
		lang = i18n.Default
	}
	c, err := a.s.itemContext(principal{household: h}, lang)
	if err != nil {
		return err
	}
	art, ok := c.article(r.ArticleID)
	if !ok {
		return nil // the article is gone
	}
	text := art.Name(lang)
	res, err := a.opts.DB.AddItemFromRule(h.ID, r.ListID, store.Item{Text: text, ArticleID: art.ID, Qty: r.Qty}, c.show(&art))
	if err != nil {
		return err
	}
	return a.opts.DB.RecordAlwaysRun(h.ID, store.AlwaysRun{ArticleID: r.ArticleID, ListID: r.ListID, Day: day, Text: res.Entry.Text,
		Action: string(res.Plan.Action), Part: r.Qty, Total: res.Entry.Qty})
}
