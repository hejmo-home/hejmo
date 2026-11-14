package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

type displayView struct {
	ID, Name, Room, Type, Paired, LastSeen string
}

func (s *server) renderDisplays(w http.ResponseWriter, r *http.Request, p principal, status int, form map[string]any) {
	list, err := s.db.Displays(p.household.ID)
	if err != nil {
		s.fail(w, "displays", err)
		return
	}
	loc := p.location()
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.In(loc).Format(p.timeLayout())
	}
	views := make([]displayView, 0, len(list))
	for _, d := range list {
		views = append(views, displayView{d.ID, d.Name, d.Room, string(d.Type), at(d.PairedAt), at(d.LastSeen)})
	}
	data := map[string]any{
		"Person": p.person, "Displays": views, "SyncStale": s.syncStale(p.household.ID), "Form": form,
		"Types": []store.DisplayType{store.DisplayEInkBW, store.DisplayEInkColor, store.DisplayScreen},
	}
	if form != nil {
		data["Error"] = form["Error"]
	}
	s.render(w, r, status, "displays.html", data)
}

func (s *server) handleDisplaysPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderDisplays(w, r, p, http.StatusOK, map[string]any{"Code": r.URL.Query().Get("code"), "Type": string(store.DisplayEInkBW)})
}

func (s *server) handleDisplayPair(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	in := store.Display{Name: r.PostFormValue("name"), Room: r.PostFormValue("room"), Type: store.DisplayType(r.PostFormValue("type"))}
	form := func(key string) map[string]any {
		return map[string]any{"Error": key, "Code": code, "Name": in.Name, "Room": in.Room, "Type": string(in.Type)}
	}
	switch _, err := s.pairDisplay(p, code, in); {
	case err == nil:
		http.Redirect(w, r, "/displays", http.StatusSeeOther)
	case errors.Is(err, errTooManyAttempts):
		s.renderDisplays(w, r, p, http.StatusTooManyRequests, form("displays.error.too_many"))
	case errors.Is(err, store.ErrNotFound):
		s.renderDisplays(w, r, p, http.StatusUnprocessableEntity, form("displays.error.code"))
	case errors.Is(err, store.ErrInvalid):
		s.renderDisplays(w, r, p, http.StatusUnprocessableEntity, form("displays.error.invalid"))
	default:
		s.fail(w, "pair display", err)
	}
}

func (s *server) handleDisplayUnpair(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	switch err := s.db.RevokeDisplay(p.household.ID, r.PathValue("id"), s.now()); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "unpair display", err)
	default:
		http.Redirect(w, r, "/displays", http.StatusSeeOther)
	}
}

type displayJSON struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Room       string  `json:"room"`
	Type       string  `json:"type"`
	PairedAt   string  `json:"paired_at"`
	LastSeenAt *string `json:"last_seen_at"`
}

func displayToJSON(d store.Display, loc *time.Location) displayJSON {
	j := displayJSON{ID: d.ID, Name: d.Name, Room: d.Room, Type: string(d.Type), PairedAt: d.PairedAt.In(loc).Format(time.RFC3339)}
	if !d.LastSeen.IsZero() {
		seen := d.LastSeen.In(loc).Format(time.RFC3339)
		j.LastSeenAt = &seen
	}
	return j
}

func (s *server) apiListDisplays(w http.ResponseWriter, _ *http.Request, p principal) {
	list, err := s.db.Displays(p.household.ID)
	if err != nil {
		s.fail(w, "list displays", err)
		return
	}
	loc := p.location()
	out := make([]displayJSON, 0, len(list))
	for _, d := range list {
		out = append(out, displayToJSON(d, loc))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiPairDisplay(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct{ Code, Name, Room, Type string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	d, err := s.pairDisplay(p, strings.TrimSpace(in.Code), store.Display{Name: in.Name, Room: in.Room, Type: store.DisplayType(in.Type)})
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, displayToJSON(d, p.location()))
	case errors.Is(err, errTooManyAttempts):
		writeError(w, http.StatusTooManyRequests, "too many wrong codes, try again later")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no display is waiting with this code, or the code has expired")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "a display needs a name and a type of eink_bw, eink_color or screen")
	default:
		s.fail(w, "pair display", err)
	}
}

func (s *server) apiRevokeDisplay(w http.ResponseWriter, r *http.Request, p principal) {
	switch err := s.db.RevokeDisplay(p.household.ID, r.PathValue("id"), s.now()); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "display not found")
	case err != nil:
		s.fail(w, "revoke display", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
