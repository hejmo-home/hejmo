package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/kiosk"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/sun"
)

// KioskOptions configures the kiosk runner.
type KioskOptions struct {
	DB *store.DB
	// Factory builds the adapter for a display's kiosk app. Nil means FreeKiosk
	// (ADR 0005); tests pass a fake.
	Factory kiosk.Factory
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Logger receives errors. Nil means slog.Default().
	Logger *slog.Logger
}

// KioskRunner switches the front light of displays with a kiosk app: off at
// night, which means after sunset at the home location (computed locally; no
// light sensor is read) and during quiet mode (ADR 0005, 0015). It sends the
// command once when the night begins, not every minute, and tries again after
// a failure. Without a kiosk app, or while the household has no home
// coordinates, the sun rule does not apply (the quiet schedule still does).
type KioskRunner struct {
	opts KioskOptions

	mu      sync.Mutex
	applied map[string]string // display ID -> kiosk address the light was switched off through
}

// NewKioskRunner returns a runner; call Run to start it.
func NewKioskRunner(opts KioskOptions) *KioskRunner {
	if opts.Factory == nil {
		opts.Factory = kiosk.NewFreeKiosk
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &KioskRunner{opts: opts, applied: map[string]string{}}
}

// Run ticks every interval until ctx ends.
func (k *KioskRunner) Run(ctx context.Context, every time.Duration) {
	k.Tick(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			k.Tick(ctx)
		}
	}
}

// Tick looks at every paired display once.
func (k *KioskRunner) Tick(ctx context.Context) {
	h, err := k.opts.DB.HomeHousehold()
	if err != nil {
		return // no household yet: the box is not set up
	}
	displays, err := k.opts.DB.Displays(h.ID)
	if err != nil {
		k.opts.Logger.Error("kiosk: list displays", "err", err)
		return
	}
	loc := time.UTC
	if l, err := time.LoadLocation(h.Timezone); err == nil {
		loc = l
	}
	now := k.opts.Now()
	local := now.In(loc)
	sunDown := (h.Latitude != 0 || h.Longitude != 0) && sun.IsDown(now, h.Latitude, h.Longitude)

	k.mu.Lock()
	defer k.mu.Unlock()
	for _, d := range displays {
		if d.KioskURL == "" {
			delete(k.applied, d.ID)
			continue
		}
		if !sunDown && !d.Quiet.Active(local) {
			delete(k.applied, d.ID) // day: the next night switches the light off again
			continue
		}
		if k.applied[d.ID] == d.KioskURL {
			continue
		}
		key, err := k.opts.DB.DisplayKioskKey(h.ID, d.ID)
		if err != nil {
			k.opts.Logger.Error("kiosk: read key", "display", d.ID, "err", err)
			continue
		}
		if err := k.opts.Factory(d.KioskURL, key).FrontLightOff(ctx); err != nil {
			k.opts.Logger.Warn("kiosk: front light not switched off, will retry", "display", d.ID, "err", err)
			continue
		}
		k.applied[d.ID] = d.KioskURL
	}
}
