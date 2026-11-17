package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/hejmo-home/hejmo/internal/brightness"
	"github.com/hejmo-home/hejmo/internal/kiosk"
	"github.com/hejmo-home/hejmo/internal/store"
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
	// After runs f once after d. Nil means time.AfterFunc; tests pass a stub
	// and tick by hand.
	After func(d time.Duration, f func())
}

// KioskState is what the box knows about a display's kiosk app, for the
// display's settings page.
type KioskState struct {
	// Reachable: the last check or command got an answer.
	Reachable bool
	// HasSensor: the device reported a light sensor at the last check.
	HasSensor bool
	// Target is what the display should do now; Confirmed says the kiosk app
	// received it (at ConfirmedAt).
	Target      brightness.Target
	Confirmed   bool
	ConfirmedAt time.Time
}

// KioskRunner sets the brightness of displays with a kiosk app (ADR 0005,
// 0020, ticket 179): Day or Evening by the sun at the home location, in quiet
// mode off or dimmed, or the device's light sensor. It sends a command once
// when the target changes, not every minute, and tries again after a failure.
// Without a kiosk app nothing is sent (the quiet page still follows the
// schedule); without home coordinates Day applies all day.
type KioskRunner struct {
	opts KioskOptions

	tickMu  sync.Mutex // one tick at a time; it talks to the tablets
	applied map[string]appliedTarget

	mu    sync.Mutex // guards state and woken, which the web pages read
	state map[string]KioskState
	woken map[string]time.Time // display ID -> until when a touch keeps it awake
}

// appliedTarget is what was last sent to a display: the part of a target
// that needs a new command when it changes (not its Since).
type appliedTarget struct {
	url                        string
	phase                      brightness.Phase
	percent                    int
	off, screenOff, autoBright bool
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
	if opts.After == nil {
		opts.After = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
	}
	return &KioskRunner{opts: opts, applied: map[string]appliedTarget{}, state: map[string]KioskState{}, woken: map[string]time.Time{}}
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

// State returns what the box knows about the kiosk app of a display; false
// when the display has none or nothing was checked yet.
func (k *KioskRunner) State(displayID string) (KioskState, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	s, ok := k.state[displayID]
	return s, ok
}

// Woken reports whether a touch keeps the display awake at the moment.
func (k *KioskRunner) Woken(displayID string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.woken[displayID].After(k.opts.Now())
}

// Wake records a touch on a display that quiet mode switched off: it stays
// awake for the display's wake minutes (Q79). Only displays with a kiosk app
// wake; it reports whether this one did. The runner looks at the display
// right away and again when the wake ends.
func (k *KioskRunner) Wake(displayID string) bool {
	h, err := k.opts.DB.HomeHousehold()
	if err != nil {
		return false
	}
	displays, err := k.opts.DB.Displays(h.ID)
	if err != nil {
		return false
	}
	for _, d := range displays {
		if d.ID != displayID || d.KioskURL == "" {
			continue
		}
		span := time.Duration(d.Brightness.WakeMinutes) * time.Minute
		k.mu.Lock()
		k.woken[d.ID] = k.opts.Now().Add(span)
		k.mu.Unlock()
		tick := func() { k.tick(context.Background(), d.ID) }
		k.opts.After(0, tick)
		k.opts.After(span+time.Second, tick)
		return true
	}
	return false
}

// Tick looks at every paired display once.
func (k *KioskRunner) Tick(ctx context.Context) { k.tick(ctx, "") }

// tick looks at one display, or at every one when only is empty.
func (k *KioskRunner) tick(ctx context.Context, only string) {
	k.tickMu.Lock()
	defer k.tickMu.Unlock()
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
	for _, d := range displays {
		if only != "" && d.ID != only {
			continue
		}
		if d.KioskURL == "" {
			delete(k.applied, d.ID)
			k.mu.Lock()
			delete(k.state, d.ID)
			delete(k.woken, d.ID)
			k.mu.Unlock()
			continue
		}
		k.tickDisplay(ctx, h, d, local)
	}
}

func (k *KioskRunner) tickDisplay(ctx context.Context, h store.Household, d store.Display, local time.Time) {
	prev, _ := k.State(d.ID)
	plan := func(sensor bool) brightness.Target {
		return brightness.Plan(brightness.Input{
			Settings: d.Brightness, Quiet: d.Quiet, Woken: k.Woken(d.ID), Screen: d.Type == store.DisplayScreen, HasSensor: sensor,
			SunriseOffset: d.SunriseOffset, SunsetOffset: d.SunsetOffset, Now: local, Lat: h.Latitude, Lon: h.Longitude,
		})
	}
	fail := func(what string, err error) {
		k.opts.Logger.Warn("kiosk: "+what+", will retry", "display", d.ID, "err", err)
		// The tablet may have restarted and lost the brightness: forget what
		// was sent, so the first answer afterwards sends the target again.
		delete(k.applied, d.ID)
		target := plan(prev.HasSensor) // before the lock: it asks Woken
		k.mu.Lock()
		k.state[d.ID] = KioskState{HasSensor: prev.HasSensor, Target: target}
		k.mu.Unlock()
	}
	key, err := k.opts.DB.DisplayKioskKey(h.ID, d.ID)
	if err != nil {
		k.opts.Logger.Error("kiosk: read key", "display", d.ID, "err", err)
		return
	}
	ad := k.opts.Factory(d.KioskURL, key)
	st, err := ad.Status(ctx)
	if err != nil {
		fail("kiosk app does not answer", err)
		return
	}
	target := plan(st.HasLightSensor)
	sig := appliedTarget{d.KioskURL, target.Phase, target.Percent, target.Off, target.ScreenOff, target.Auto}
	if k.applied[d.ID] == sig {
		k.mu.Lock()
		s := k.state[d.ID]
		s.Reachable, s.HasSensor, s.Target = true, st.HasLightSensor, target
		k.state[d.ID] = s
		k.mu.Unlock()
		return
	}
	if err := apply(ctx, ad, target, st); err != nil {
		fail("brightness not set", err)
		return
	}
	k.applied[d.ID] = sig
	k.mu.Lock()
	k.state[d.ID] = KioskState{Reachable: true, HasSensor: st.HasLightSensor, Target: target, Confirmed: true, ConfirmedAt: k.opts.Now()}
	k.mu.Unlock()
}

// apply sends a target: a normal screen is switched off as a whole, an E-Ink
// front light is set to 0 %, anything else switches the screen back on first
// when it was off.
func apply(ctx context.Context, ad kiosk.Adapter, t brightness.Target, st kiosk.Status) error {
	switch {
	case t.Off && t.ScreenOff:
		return ad.ScreenOff(ctx)
	case t.Off:
		return ad.SetBrightness(ctx, 0)
	}
	if !st.ScreenOn {
		if err := ad.ScreenOn(ctx); err != nil {
			return err
		}
	}
	if t.Auto {
		return ad.EnableAutoBrightness(ctx)
	}
	return ad.SetBrightness(ctx, t.Percent)
}
