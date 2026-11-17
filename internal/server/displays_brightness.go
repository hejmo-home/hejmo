package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/brightness"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/sun"
)

// freeKioskGuide explains how to set up FreeKiosk for Hejmo.
const freeKioskGuide = "https://github.com/hejmo-home/hejmo/blob/main/docs/freekiosk.md"

// kioskState is what the runner knows about the display's kiosk app; false
// without a kiosk app, without a runner, or before the first check.
func (s *server) kioskState(d store.Display) (KioskState, bool) {
	if s.kiosk == nil || d.KioskURL == "" {
		return KioskState{}, false
	}
	return s.kiosk.State(d.ID)
}

// brightFields are the form fields of the Brightness block; a refused one is
// shown under the sentence that names the block.
var brightFields = map[string]bool{"bright_day": true, "bright_evening": true, "bright_quiet": true, "wake_minutes": true, "quiet_light": true, "light_source": true}

// brightPage is the Brightness block of a display's settings page (ticket 179).
// The texts are built here, in the page's language; the values come from what
// was submitted when the page is shown again after an error.
type brightPage struct {
	// Available: the display has a kiosk app. Without one the block is greyed out.
	Available bool
	// EInk is a front light display (tag "Experimental").
	EInk bool
	// NowHead and NowTail are the state in words ("Now 40 %", "Evening, since
	// 16:38. The kiosk app confirmed it."); Problem replaces them while the
	// kiosk app does not answer.
	NowHead, NowTail, Problem string
	// HasSensor offers the choice between sun and room light; Room is the choice.
	HasSensor, Room, NoSensor bool
	RoomNote                  string
	Day, Evening              string // the values, as typed
	DaySub, EveningSub        string
	NoHome                    bool
	QuietOn                   bool
	QuietHead, QuietNote      string
	QuietLight                string // off or dim
	OffText                   string
	Level, Wake, WakeUnit     string
	WakeSub                   string
	Why                       string // why the block is greyed out
	KioskSummary              string
	KioskOpen                 bool
	SunSummary                string
	SunOpen                   bool
	GuideURL                  string
}

// fillBrightness builds the Brightness block and the sun offsets' summary.
func (s *server) fillBrightness(r *http.Request, p principal, v *displayPage, d store.Display, in url.Values, errField string) {
	lang := s.langOf(r)
	h := p.household
	loc := p.location()
	now := s.now().In(loc)
	st, known := s.kioskState(d)
	b := brightPage{Available: d.KioskURL != "", EInk: d.Type != store.DisplayScreen, GuideURL: freeKioskGuide}
	say := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	clock := func(t time.Time) string { return datefmt.Clock(h.TimeFormat, t.In(loc)) }

	// The values: what is stored, or what was submitted.
	set := d.Brightness
	b.Day, b.Evening, b.Level, b.Wake = strconv.Itoa(set.Day), strconv.Itoa(set.Evening), strconv.Itoa(set.QuietLevel), strconv.Itoa(set.WakeMinutes)
	b.QuietLight, b.Room = string(set.QuietMode), set.Source == brightness.SourceRoom
	if in != nil {
		for field, dst := range map[string]*string{"bright_day": &b.Day, "bright_evening": &b.Evening, "bright_quiet": &b.Level, "wake_minutes": &b.Wake, "quiet_light": &b.QuietLight} {
			if in.Has(field) {
				*dst = in.Get(field)
			}
		}
		if in.Has("light_source") {
			b.Room = in.Get("light_source") == string(brightness.SourceRoom)
		}
	}
	b.HasSensor = b.Available && known && st.HasSensor
	b.NoSensor = b.Available && known && !st.HasSensor
	unit := "displays.bright.unit.minute.other"
	if b.Wake == "1" {
		unit = "displays.bright.unit.minute.one"
	}
	b.WakeUnit = say(unit)
	b.RoomNote = say("displays.bright.room_note", "day", strconv.Itoa(set.Day), "evening", strconv.Itoa(set.Evening))
	b.WakeSub = say("displays.bright.wake.sub", "p", strconv.Itoa(set.Evening))

	// Sun times of today, shifted like the automatic theme.
	b.DaySub, b.EveningSub = say("displays.bright.day.sub"), say("displays.bright.evening.sub")
	b.NoHome = h.Latitude == 0 && h.Longitude == 0
	if !b.NoHome {
		today, tomorrow := sun.On(now, h.Latitude, h.Longitude), sun.On(now.AddDate(0, 0, 1), h.Latitude, h.Longitude)
		if today.Kind == sun.Normal && tomorrow.Kind == sun.Normal {
			rise, set := today.Rise.Add(time.Duration(d.SunriseOffset)*time.Minute), today.Set.Add(time.Duration(d.SunsetOffset)*time.Minute)
			next := tomorrow.Rise.Add(time.Duration(d.SunriseOffset) * time.Minute)
			b.DaySub = say("displays.bright.day.sub_times", "from", clock(rise), "to", clock(set))
			b.EveningSub = say("displays.bright.evening.sub_times", "from", clock(set), "to", clock(next))
		}
	}

	// Quiet mode: its times in the heading, or a sentence while it is off.
	b.QuietOn = d.Quiet.Enabled
	if b.QuietOn {
		b.QuietHead = say("displays.bright.quiet_head", "from", d.Quiet.FromText(), "to", d.Quiet.ToText())
	} else {
		b.QuietHead, b.QuietNote = say("displays.bright.quiet_head_off"), say("displays.bright.quiet_note")
	}
	b.OffText = say("displays.bright.quiet.off.screen")
	if b.EInk {
		b.OffText = say("displays.bright.quiet.off.eink")
	}

	// What the display does now, and whether the kiosk app confirmed it.
	target := brightness.Plan(brightness.Input{
		Settings: d.Brightness, Quiet: d.Quiet, Woken: s.kiosk != nil && s.kiosk.Woken(d.ID), Screen: d.Type == store.DisplayScreen, HasSensor: b.HasSensor,
		SunriseOffset: d.SunriseOffset, SunsetOffset: d.SunsetOffset, Now: now, Lat: h.Latitude, Lon: h.Longitude,
	})
	if b.Available {
		b.NowHead, b.NowTail, b.Problem = s.brightNow(lang, target, st, known, clock)
	} else {
		b.Why = say("displays.bright.why.screen")
		if b.EInk {
			b.Why = say("displays.bright.why.eink")
		}
	}

	// The kiosk app fields live in this block.
	host := d.KioskURL
	if u, err := url.Parse(d.KioskURL); err == nil && u.Host != "" {
		host = u.Host
	}
	switch {
	case d.KioskURL == "":
		b.KioskSummary = say("displays.kiosk.summary.off")
	case known && !st.Reachable:
		b.KioskSummary = say("displays.kiosk.summary.down", "host", host)
	default:
		b.KioskSummary = say("displays.kiosk.summary.on", "host", host)
	}
	b.KioskOpen = !b.Available || errField == "kiosk_url"

	// The sun offsets stay under "More options".
	b.SunSummary = say("displays.sun.summary")
	if d.SunriseOffset != 0 || d.SunsetOffset != 0 {
		b.SunSummary = say("displays.sun.summary_shifted")
	}
	b.SunOpen = errField == "sunrise_offset" || errField == "sunset_offset"
	v.Bright = b
}

// brightNow words what the display does now. The problem sentence replaces
// the state while the kiosk app does not answer; before the box has checked,
// the state says it will pass the target on.
func (s *server) brightNow(lang string, t brightness.Target, st KioskState, known bool, clock func(time.Time) string) (head, tail, problem string) {
	say := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	pct := strconv.Itoa(t.Percent)
	if known && !st.Reachable {
		kind, args := "set", []string{"p", pct}
		switch {
		case t.Auto:
			kind, args = "auto", nil
		case t.Off:
			kind, args = "off", nil
		}
		if !t.Since.IsZero() && t.Phase != brightness.PhaseWake {
			args = append(args, "t", clock(t.Since))
		} else {
			kind += "_now"
		}
		return "", "", say("displays.bright.problem", "what", say("displays.bright.should."+kind, args...))
	}
	switch {
	case t.Auto:
		head = say("displays.bright.now.head_room")
	case t.Off:
		head = say("displays.bright.now.head_off")
	default:
		head = say("displays.bright.now.head", "p", pct)
	}
	sentence := ""
	switch t.Phase {
	case brightness.PhaseDay, brightness.PhaseEvening:
		key := "displays.bright.now." + string(t.Phase)
		if t.Since.IsZero() {
			sentence = say(key + "_nosince")
		} else {
			sentence = say(key, "t", clock(t.Since))
		}
	case brightness.PhaseQuietOff, brightness.PhaseQuietDim:
		sentence = say("displays.bright.now.quiet", "t", clock(t.Since))
	case brightness.PhaseWake:
		sentence = say("displays.bright.now.wake")
	}
	confirm := say("displays.bright.pending")
	if known && st.Confirmed {
		confirm = say("displays.bright.confirmed")
	}
	if t.Phase == brightness.PhaseRoom {
		if known && st.Confirmed {
			sentence = say("displays.bright.now.room", "t", clock(st.ConfirmedAt))
		} else {
			sentence = say("displays.bright.now.room_pending")
		}
		confirm = ""
	}
	return head, strings.TrimSpace(sentence + " " + confirm), ""
}
