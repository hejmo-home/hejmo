// Package server wires the box's HTTP handlers: web UI, setup wizard,
// sign-in and the REST API.
package server

import (
	"context"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/web"
)

// Options configures the HTTP server.
type Options struct {
	// DB is the box's database (required).
	DB *store.DB
	// PKI holds the box's own certificate authority and certificate (required).
	PKI *pki.Store
	// HTTPSPort is the port clients reach HTTPS on, used in redirects and
	// links. Empty means 443.
	HTTPSPort string
	// Public is the public HTTPS certificate (ADR 0014). Nil means the
	// wizard offers only the box's own CA.
	Public PublicHTTPS
	// Sync syncs calendars from outside. Nil means a service with HTTP
	// fetching; main passes its own so the scheduler and the web UI share it.
	Sync *calsync.Service
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Logger receives errors. Nil means slog.Default().
	Logger *slog.Logger
}

// PublicHTTPS is the box's publicly trusted certificate, set up through the
// wizard; implemented by publiccert.Manager.
type PublicHTTPS interface {
	// Ready reports whether a public setup is installed (its certificate may
	// still be missing, see HasCertificate).
	Ready() bool
	// HasCertificate reports whether the manager has a certificate to serve.
	// It is false while a stored setup is ready but its certificate could not
	// be loaded or obtained: the box then serves its own CA's certificate.
	HasCertificate() bool
	// Name is the public DNS name of the box.
	Name() string
	// Status is shown to parents.
	Status() publiccert.Status
	// Setup points the name to the box and obtains the certificate.
	Setup(context.Context, publiccert.Request) error
	// Clear forgets the public setup (back to the box's own CA).
	Clear() error
}

type server struct {
	db        *store.DB
	pki       *pki.Store
	public    PublicHTTPS
	httpsPort string
	now       func() time.Time
	i18n      *i18n.Bundle
	pages     *template.Template
	logger    *slog.Logger
	limiter   *loginLimiter
	opens     *loginLimiter // pairing screens opened, per client address
	sync      *calsync.Service
}

// New returns the box's root HTTP handler.
func New(opts Options) http.Handler {
	if opts.DB == nil {
		panic("server: Options.DB is required")
	}
	if opts.PKI == nil {
		panic("server: Options.PKI is required")
	}
	s := &server{db: opts.DB, pki: opts.PKI, public: opts.Public, httpsPort: opts.HTTPSPort, now: opts.Now, logger: opts.Logger, i18n: i18n.MustLoad()}
	if s.httpsPort == "" {
		s.httpsPort = "443"
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.sync = opts.Sync; s.sync == nil {
		s.sync = calsync.New(s.db, nil, s.now)
	}
	s.limiter = newLoginLimiter(5, 15*time.Minute, s.now)
	s.opens = newLoginLimiter(10, 10*time.Minute, s.now)
	s.pages = template.Must(template.New("").Funcs(template.FuncMap{
		"t":    func(string) string { return "" }, // replaced per request in render
		"dict": dict,
	}).ParseFS(web.Templates, "templates/*.html"))

	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /setup/https", s.handleHTTPSForm)
	mux.HandleFunc("POST /setup/https", s.handleHTTPSSubmit)
	mux.HandleFunc("POST /setup/https/public", s.handlePublicHTTPSSubmit)
	mux.HandleFunc("GET /setup/https/check", s.handleNameCheck)
	mux.HandleFunc("POST /setup/https/own-ca", s.handleSwitchToOwnCA)
	mux.HandleFunc("GET /ca", s.handleTrustPage)
	mux.HandleFunc("GET /ca/hejmo-ca.crt", s.handleCACert)
	mux.HandleFunc("GET /ca/hejmo.mobileconfig", s.handleMobileConfig)
	mux.HandleFunc("GET /setup", s.handleSetupForm)
	mux.HandleFunc("POST /setup", s.handleSetupSubmit)
	mux.HandleFunc("GET /setup/totp", s.handleSetupTOTPForm)
	mux.HandleFunc("POST /setup/totp", s.handleSetupTOTPSubmit)
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /calendars", s.handleCalendarsPage)
	mux.HandleFunc("POST /calendars", s.handleCalendarAdd)
	mux.HandleFunc("POST /calendars/{id}/sync", s.handleCalendarSync)
	mux.HandleFunc("POST /calendars/{id}/delete", s.handleCalendarDelete)
	mux.HandleFunc("GET /caldav", s.handleCalDAVPage)
	mux.HandleFunc("POST /caldav/tokens", s.handleDeviceTokenCreate)
	mux.HandleFunc("POST /caldav/tokens/{id}/revoke", s.handleDeviceTokenRevoke)
	mux.HandleFunc("POST /caldav/calendars/{id}/share", s.handleCalendarShare)
	mux.HandleFunc("GET /tasks", s.handleTasksPage)
	mux.HandleFunc("POST /tasks", s.handleTaskAdd)
	mux.HandleFunc("POST /tasks/{id}/done", s.handleTaskDone)
	mux.HandleFunc("POST /tasks/{id}/delete", s.handleTaskDelete)
	mux.HandleFunc("GET /display", s.handleDisplay)
	mux.HandleFunc("GET /display/state", s.handleDisplayState)
	mux.HandleFunc("GET /displays", s.handleDisplaysPage)
	mux.HandleFunc("POST /displays", s.handleDisplayPair)
	mux.HandleFunc("POST /displays/{id}/unpair", s.handleDisplayUnpair)
	s.routeAPI(mux)
	s.routeCalDAV(mux)
	return s.requireHTTPS(sameOrigin(mux))
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	status, code := "ok", http.StatusOK
	if err := s.db.SQL().PingContext(r.Context()); err != nil {
		status, code = "database unavailable", http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func (s *server) handleHome(w http.ResponseWriter, r *http.Request) {
	if !s.db.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	p, ok := s.currentPerson(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	// The family view arrives with work package 5.
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	s.render(w, r, http.StatusOK, "home.html", map[string]any{"Person": p, "SyncStale": s.syncStale(h.ID), "HTTPSWarning": p.Role == store.RoleParent && s.httpsWarning()})
}

func (s *server) render(w http.ResponseWriter, r *http.Request, status int, name string, data map[string]any) {
	lang := s.i18n.Match(r.Header.Get("Accept-Language"))
	if data == nil {
		data = map[string]any{}
	}
	data["Lang"] = lang
	tpl, err := s.pages.Clone()
	if err == nil {
		tpl = tpl.Funcs(template.FuncMap{"t": func(key string) string { return s.i18n.T(lang, key) }})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Language", lang)
		w.WriteHeader(status)
		err = tpl.ExecuteTemplate(w, name, data)
	}
	if err != nil {
		s.fail(w, "render "+name, err)
	}
}

func (s *server) fail(w http.ResponseWriter, what string, err error) {
	s.logger.Error(what, "err", err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// dict builds a map for passing several values to a sub-template.
func dict(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			m[k] = kv[i+1]
		}
	}
	return m
}
