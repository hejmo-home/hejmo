package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

// heartbeatEvery keeps idle streams open through proxies. A comment line is
// not a view: nothing of the picture is transmitted.
const heartbeatEvery = 25 * time.Second

// renderFamily renders the family view markup and names it by a version: a
// hash of the markup, so two renders are equal exactly when the display would
// show the same.
func (s *server) renderFamily(h store.Household, lang string, showTomorrow bool) (template.HTML, string, error) {
	view, err := s.familyView(h, lang, showTomorrow)
	if err != nil {
		return "", "", err
	}
	tpl, err := s.pages.Clone()
	if err != nil {
		return "", "", err
	}
	tpl = tpl.Funcs(template.FuncMap{"t": func(key string) string { return s.i18n.T(lang, key) }})
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, "familyView", view); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return template.HTML(buf.String()), hex.EncodeToString(sum[:8]), nil
}

// pairedDisplay answers 403 unless the request comes from a paired display.
func (s *server) pairedDisplay(w http.ResponseWriter, r *http.Request) (store.Display, store.Household, bool) {
	d, _, ok := s.currentDisplay(r)
	if !ok || !d.Paired {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return store.Display{}, store.Household{}, false
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return store.Display{}, store.Household{}, false
	}
	return d, h, true
}

// handleDisplayView serves the family view markup alone, for the display's
// script to swap in when the box announced a change (partial update).
func (s *server) handleDisplayView(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.pairedDisplay(w, r)
	if !ok {
		return
	}
	markup, version, err := s.renderFamily(h, s.langOf(r), d.ShowTomorrow)
	if err != nil {
		s.fail(w, "family view", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Hejmo-View", version)
	_, _ = w.Write([]byte(markup))
}

// handleDisplayEvents is the display's Server-Sent Events stream. It names the
// current view version on connect, so a display that was cut off catches up,
// and again whenever the view changes (an event, the clock, 18:00, midnight).
// While nothing changes it sends only comment heartbeats, no view.
func (s *server) handleDisplayEvents(w http.ResponseWriter, r *http.Request) {
	d, h, ok := s.pairedDisplay(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	lang := s.langOf(r)
	hash, _ := displayToken(r)
	last := ""
	check := func() bool {
		if cur, err := s.db.DisplayByToken(hash, s.now()); err != nil || !cur.Paired {
			return false // revoked: the display's state check takes it back to pairing
		} else {
			d = cur
		}
		_, version, err := s.renderFamily(h, lang, d.ShowTomorrow)
		if err != nil {
			s.logger.Error("live view", "err", err)
			return true
		}
		if version != last {
			last = version
			fmt.Fprintf(w, "event: view\ndata: %s\n\n", version)
			flusher.Flush()
		}
		return true
	}
	if !check() {
		return
	}
	tick := time.NewTicker(s.live)
	defer tick.Stop()
	beat := time.NewTicker(heartbeatEvery)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if !check() {
				return
			}
		case <-beat.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}
