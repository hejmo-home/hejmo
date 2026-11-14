// Package server wires the box's HTTP handlers.
package server

import (
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/web"
)

// SetupState tells whether the box has finished its first-run setup.
type SetupState interface {
	Configured() bool
}

// Options configures the HTTP server.
type Options struct {
	// Setup reports the first-run state. Nil means "not configured".
	Setup SetupState
	// Logger receives request errors. Nil means slog.Default().
	Logger *slog.Logger
}

type server struct {
	setup  SetupState
	i18n   *i18n.Bundle
	pages  *template.Template
	logger *slog.Logger
}

// New returns the box's root HTTP handler.
func New(opts Options) http.Handler {
	s := &server{
		setup:  opts.Setup,
		i18n:   i18n.MustLoad(),
		logger: opts.Logger,
	}
	if s.setup == nil {
		s.setup = unconfigured{}
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	s.pages = template.Must(template.New("").Funcs(template.FuncMap{
		// Replaced per request in render; declared here so templates parse.
		"t": func(string) string { return "" },
	}).ParseFS(web.Templates, "templates/*.html"))

	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /setup", s.handleSetup)
	mux.HandleFunc("GET /{$}", s.handleHome)
	return mux
}

type unconfigured struct{}

func (unconfigured) Configured() bool { return false }

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *server) handleHome(w http.ResponseWriter, r *http.Request) {
	if !s.setup.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	// The family view arrives with work package 5.
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Hejmo\n"))
}

func (s *server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.setup.Configured() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, "setup.html", nil)
}

func (s *server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	lang := s.i18n.Match(r.Header.Get("Accept-Language"))
	tpl, err := s.pages.Clone()
	if err == nil {
		tpl = tpl.Funcs(template.FuncMap{"t": func(key string) string { return s.i18n.T(lang, key) }})
	}
	if data == nil {
		data = map[string]any{}
	}
	data["Lang"] = lang
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Language", lang)
	if err == nil {
		err = tpl.ExecuteTemplate(w, name, data)
	}
	if err != nil {
		s.logger.Error("render page", "page", name, "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}
