package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"

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
}

// renderDisplayContent renders what a paired display shows now: the family
// view, or in quiet mode the night page or a black page. The version names the
// markup (without the clock), so a display repaints only when it changed.
func (s *server) renderDisplayContent(h store.Household, d store.Display, lang string) (template.HTML, string, error) {
	now := s.householdNow(h)
	if !d.Quiet.Active(now) {
		return s.renderFamily(h, lang, d.ShowTomorrow)
	}
	v := quietView{Black: d.Type == store.DisplayScreen}
	if !v.Black {
		morning := d.Quiet.Morning(now)
		occs, err := s.occurrences(h, lang, morning, morning.AddDate(0, 0, 1))
		if err != nil {
			return "", "", err
		}
		v.Night = familyview.BuildNight(familyview.NightInput{Morning: morning, Occurrences: occs, TimeFormat: h.TimeFormat})
		v.ReloadSeconds = quietReloadSeconds
	}
	var buf bytes.Buffer
	if err := s.execute(&buf, "quietView", lang, v); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return template.HTML(buf.String()), hex.EncodeToString(sum[:8]), nil
}
