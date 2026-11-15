package server

import (
	"encoding/json"
	"net/http"

	"github.com/hejmo-home/hejmo/web"
)

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

// webManifest makes the web interface installable as an app (ticket 5.13).
// The colours are "Nordic Calm" light-background (DESIGN.md).
var webManifest = struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	BackgroundColor string         `json:"background_color"`
	ThemeColor      string         `json:"theme_color"`
	Icons           []manifestIcon `json:"icons"`
}{
	ID: "/", Name: "Hejmo", ShortName: "Hejmo", StartURL: "/", Scope: "/", Display: "standalone",
	BackgroundColor: "#F6F3EE", ThemeColor: "#F6F3EE",
	Icons: []manifestIcon{
		{"/static/icons/icon-192.png", "192x192", "image/png", "any"},
		{"/static/icons/icon-512.png", "512x512", "image/png", "any"},
		{"/static/icons/icon-maskable-512.png", "512x512", "image/png", "maskable"},
	},
}

func (s *server) handleManifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(webManifest)
}

// handleServiceWorker serves the service worker from the root so that its
// scope is the whole web interface. It keeps the last pages a signed-in person
// loaded and shows them read-only when the box cannot be reached.
func (s *server) handleServiceWorker(w http.ResponseWriter, _ *http.Request) {
	script, err := web.Static.ReadFile("static/sw.js")
	if err != nil {
		s.fail(w, "service worker", err)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(script)
}
