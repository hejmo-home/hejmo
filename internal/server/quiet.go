package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"

	"github.com/hejmo-home/hejmo/internal/brightness"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/store"
)

// quietReloadSeconds is how often an E-Ink display reloads the whole page in
// quiet mode, to clear ghosting (ADR 0015). Outside quiet mode it does not.
const quietReloadSeconds = 3600

// quietView is the data of the quiet mode markup: a night page on E-Ink
// displays, a black page on a normal screen.
type quietView struct {
	Black         bool
	ReloadSeconds int
	Night         familyview.NightView
	// WakeURL is where a touch tells the box to wake the display (ticket 179,
	// Q79): set only while the box switched it off through a kiosk app.
	WakeURL string
}

// wakeURL is where a touch on a quiet display wakes it.
const wakeURL = "/display/wake"

// quietBlack reports whether quiet mode shows a black page on d: a normal
// screen does, unless a kiosk app dims it (the night page stays faintly
// visible, ticket 179) or a touch woke it for a while (it shows the night
// page, then goes off again).
func (s *server) quietBlack(d store.Display) bool {
	switch {
	case d.Type != store.DisplayScreen:
		return false
	case d.KioskURL != "" && d.Brightness.QuietMode == brightness.QuietDim:
		return false
	}
	return s.kiosk == nil || !s.kiosk.Woken(d.ID)
}

// listensForWake reports whether the quiet page of d tells the box about a
// touch: the kiosk app switched it off (Off), so a touch is how it comes back.
func listensForWake(d store.Display) bool {
	return d.KioskURL != "" && d.Brightness.QuietMode == brightness.QuietOff
}

// renderDisplayContent renders what a paired display shows now: the family
// view, or in quiet mode the night page or a black page. The version names the
// markup (without the clock), so a display repaints only when it changed.
func (s *server) renderDisplayContent(h store.Household, d store.Display, lang string) (template.HTML, string, error) {
	now := s.householdNow(h)
	if !d.Quiet.Active(now) {
		lay, err := s.db.DisplayLayout(h.ID, d.ID)
		if err != nil {
			return "", "", err
		}
		if person, ok := lay.Personal(); ok { // Q88: one person is their personal view
			markup, version, err := s.renderPersonal(h, &d, lang, person, true)
			if !errors.Is(err, errNoPerson) {
				return markup, version, err
			}
		}
		return s.renderFamily(h, lang, &d)
	}
	v := quietView{Black: s.quietBlack(d)}
	if listensForWake(d) {
		v.WakeURL = wakeURL
	}
	if !v.Black {
		morning := d.Quiet.Morning(now)
		all, err := s.occurrences(h, lang, morning, morning.AddDate(0, 0, 1))
		if err != nil {
			return "", "", err
		}
		cals, err := s.db.Calendars(h.ID)
		if err != nil {
			return "", "", err
		}
		occs, waste := splitWaste(all, cals)
		v.Night = familyview.BuildNight(familyview.NightInput{Morning: morning, Occurrences: occs, TimeFormat: h.TimeFormat,
			Waste: familyview.WasteTitles(waste, morning, morning.AddDate(0, 0, 1))})
		if d.Type != store.DisplayScreen { // against ghosting: only E-Ink
			v.ReloadSeconds = quietReloadSeconds
		}
	}
	var buf bytes.Buffer
	if err := s.execute(&buf, "quietView", lang, v); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return template.HTML(buf.String()), hex.EncodeToString(sum[:8]), nil
}
