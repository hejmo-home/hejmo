package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The screensaver of a display (ticket 178): a page the box serves, so it runs
// on every display including a plain browser. A display's own token (cookie or
// bearer) opens it.

type displaySaverJSON struct {
	Active          bool     `json:"active"`
	IntervalSeconds int      `json:"interval_seconds"`
	DisplayType     string   `json:"display_type"`
	Photos          []string `json:"photos"`
}

// saverActive reports whether the display shows its screensaver now: it is
// paired, has it on and is not in quiet mode (Q71).
func (s *server) saverActive(h store.Household, d store.Display) bool {
	return d.Paired && d.Saver.Enabled && !d.Quiet.Active(s.householdNow(h))
}

// displaySaver is what the screensaver of the requesting display shows now.
func (s *server) displaySaver(r *http.Request) (displaySaverJSON, store.Display, bool) {
	d, _, ok := s.currentDisplay(r)
	if !ok || !d.Paired {
		return displaySaverJSON{}, d, false
	}
	out := displaySaverJSON{IntervalSeconds: d.Saver.IntervalMinutes * 60, DisplayType: string(d.Type), Photos: []string{}}
	h, err := s.db.HomeHousehold()
	if err != nil || !s.saverActive(h, d) {
		return out, d, true
	}
	out.Active = true
	photos, err := s.db.SaverPhotos(h.ID, d)
	if err != nil {
		return out, d, true
	}
	for _, p := range photos {
		out.Photos = append(out.Photos, p.ID)
	}
	return out, d, true
}

func (s *server) apiDisplaySaver(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.serveDisplaySaver(w, r)
}

func (s *server) serveDisplaySaver(w http.ResponseWriter, r *http.Request) {
	out, _, ok := s.displaySaver(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiDisplayPhoto(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.serveDisplayPhoto(w, r)
}

// serveDisplayPhoto sends a photo to a paired display, but only one the display
// shows: not a hidden photo (Q38), not one of an album it did not choose (Q69).
func (s *server) serveDisplayPhoto(w http.ResponseWriter, r *http.Request) {
	d, _, ok := s.currentDisplay(r)
	if !ok || !d.Paired {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil || s.photos == nil {
		http.NotFound(w, r)
		return
	}
	ph, err := s.db.Photo(h.ID, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ph.Hidden || !d.Saver.Shows(ph.AlbumID) {
		http.NotFound(w, r)
		return
	}
	s.serveStoredPhoto(w, r, ph, false)
}

// handleDisplaySaver is the screensaver page. Whoever may not see it (not
// paired, screensaver off, quiet mode) is sent to the day view.
func (s *server) handleDisplaySaver(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	h, err := s.db.HomeHousehold()
	d, _, ok := s.currentDisplay(r)
	if err != nil || !ok || !s.saverActive(h, d) {
		http.Redirect(w, r, "/display", http.StatusSeeOther)
		return
	}
	lang := s.displayLang(r)
	now := s.householdNow(h)
	photos, err := s.db.SaverPhotos(h.ID, d)
	if err != nil {
		s.fail(w, "saver photos", err)
		return
	}
	data := map[string]any{
		"Display": d, "PollSeconds": displayPollSeconds, "Theme": s.displayTheme(h, d),
		"Interval": d.Saver.IntervalMinutes * 60, "HasPhotos": len(photos) > 0,
		"ClockZone": h.Timezone, "ClockFormat": familyview.ClockFormat(h.TimeFormat), "Clock": familyview.ClockText(now, h.TimeFormat),
		"Date": s.saverDate(lang, now),
	}
	if len(photos) > 0 {
		data["First"] = photos[int(now.UnixNano()/int64(time.Second))%len(photos)].ID
	} else if _, n, err := s.db.PhotoUsage(h.ID); err == nil && n > 0 {
		// Photos exist, but none for this display: its albums are empty or everything in them is hidden.
		data["AnyPhotos"] = true
		data["AlbumsText"] = s.saverAlbumsText(lang, h, d)
	}
	s.render(w, r, http.StatusOK, "display_saver.html", data)
}

// saverAlbumsText says which albums a display shows although nothing in them
// can be shown: its own choice by name, else all albums.
func (s *server) saverAlbumsText(lang string, h store.Household, d store.Display) string {
	if len(d.Saver.Albums) == 0 {
		return s.i18n.T(lang, "saver.hint2.all")
	}
	var names []string
	albums, _ := s.db.Albums(h.ID)
	for _, a := range albums {
		if d.Saver.Shows(a.ID) {
			names = append(names, s.i18n.Format(lang, i18n.Msg("photos.quoted", "name", a.Name)))
		}
	}
	list := strings.Join(names, ", ")
	if n := len(names); n > 1 {
		list = strings.Join(names[:n-1], ", ") + " " + s.i18n.T(lang, "saver.and") + " " + names[n-1]
	}
	return s.i18n.Format(lang, i18n.Msg("saver.hint2.text", "albums", list))
}

// saverDate is the date in words: "Samstag, 14. November".
func (s *server) saverDate(lang string, t time.Time) string {
	return repl(s.i18n.T(lang, "saver.date"),
		"{weekday}", s.i18n.T(lang, fmt.Sprintf("weekday.%d", int(t.Weekday()))),
		"{day}", fmt.Sprint(t.Day()),
		"{month}", s.i18n.T(lang, fmt.Sprintf("month.%d", int(t.Month()))))
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
