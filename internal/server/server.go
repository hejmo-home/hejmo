// Package server wires the box's HTTP handlers: web UI, setup wizard,
// sign-in and the REST API.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/weather"
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
	// Weather holds the weather of the home location, refreshed in the
	// background (ADR 0016). Nil means a service that never has a reading.
	Weather *weather.Service
	// LiveInterval is how often the box checks whether a connected display's
	// view changed (live updates, ticket 5.5). Zero means 2 seconds.
	LiveInterval time.Duration
	// Done, when closed, ends open live streams so a shutdown does not wait
	// for them. Nil means they end only when their client goes away.
	Done <-chan struct{}
	// UpdateDir is the data volume directory the host script of auto-update
	// shares with the box (ADR 0018). Empty means updates are not managed by a
	// host script: no settings file is written, no status is read and the
	// settings page does not offer auto-update.
	UpdateDir string
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
	weather   *weather.Service
	live      time.Duration
	done      <-chan struct{}
	updateDir string
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
	if s.weather = opts.Weather; s.weather == nil {
		s.weather = weather.New(weather.WithClock(s.now))
	}
	s.done = opts.Done
	s.updateDir = opts.UpdateDir
	if s.live = opts.LiveInterval; s.live <= 0 {
		s.live = 2 * time.Second
	}
	s.limiter = newLoginLimiter(5, 15*time.Minute, s.now)
	s.opens = newLoginLimiter(10, 10*time.Minute, s.now)
	s.pages = parsePages()
	s.syncUpdateSettings()

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
	mux.HandleFunc("POST /connections", s.handleConnect)
	mux.HandleFunc("GET /connections/{id}", s.handleConnectionPage)
	mux.HandleFunc("POST /connections/{id}/calendars", s.handleConnectionCalendars)
	mux.HandleFunc("POST /connections/{id}/delete", s.handleConnectionDelete)
	mux.HandleFunc("GET /caldav", s.handleCalDAVPage)
	mux.HandleFunc("POST /caldav/tokens", s.handleDeviceTokenCreate)
	mux.HandleFunc("POST /caldav/profile", s.handleIOSProfile)
	mux.HandleFunc("POST /caldav/tokens/{id}/revoke", s.handleDeviceTokenRevoke)
	mux.HandleFunc("POST /caldav/calendars/{id}/share", s.handleCalendarShare)
	mux.HandleFunc("GET /tasks", s.handleTasksPage)
	mux.HandleFunc("POST /tasks", s.handleTaskAdd)
	mux.HandleFunc("GET /tasks/{id}", s.handleTaskPage)
	mux.HandleFunc("POST /tasks/{id}/done", s.handleTaskDone)
	mux.HandleFunc("POST /tasks/{id}/delete", s.handleTaskDelete)
	mux.HandleFunc("GET /display", s.handleDisplay)
	mux.HandleFunc("GET /display/state", s.handleDisplayState)
	mux.HandleFunc("GET /display/view", s.handleDisplayView)
	mux.HandleFunc("GET /display/events", s.handleDisplayEvents)
	mux.HandleFunc("GET /display/person/{id}", s.handleDisplayPerson)
	mux.HandleFunc("GET /display/person/{id}/view", s.handleDisplayPersonView)
	mux.HandleFunc("POST /display/tasks/{id}/done", s.handleDisplayTaskDone)
	mux.HandleFunc("GET /displays", s.handleDisplaysPage)
	mux.HandleFunc("POST /displays", s.handleDisplayPair)
	mux.HandleFunc("POST /displays/{id}/unpair", s.handleDisplayUnpair)
	mux.HandleFunc("GET /phones", s.handlePhonesPage)
	mux.HandleFunc("POST /phones/pairings", s.handlePhonePairingCreate)
	mux.HandleFunc("POST /phones/pairings/{id}/confirm", s.handlePhonePairingConfirm)
	mux.HandleFunc("POST /phones/{id}/revoke", s.handlePhoneRevoke)
	mux.HandleFunc("GET /phones/join/{code}", s.handlePhoneJoin)
	mux.HandleFunc("GET /manifest.webmanifest", s.handleManifest)
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker)
	mux.HandleFunc("GET /settings", s.handleSettingsPage)
	mux.HandleFunc("POST /settings", s.handleSettingsSave)
	mux.HandleFunc("POST /update/dismiss", s.handleUpdateDismiss)
	mux.HandleFunc("POST /displays/{id}/settings", s.handleDisplaySettings)
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
	s.render(w, r, http.StatusOK, "home.html", map[string]any{"Person": p, "SyncStale": s.syncStale(h.ID), "HTTPSWarning": p.Role == store.RoleParent && s.httpsWarning(), "Update": s.parentUpdateNotice(p, h)})
}

// parsePages parses the embedded templates; "t" is replaced per render.
func parsePages() *template.Template {
	return template.Must(template.New("").Funcs(template.FuncMap{
		"t":    func(string) string { return "" },
		"dict": dict,
		"has":  func(list []string, v string) bool { return slices.Contains(list, v) },
		"strs": strs,
	}).ParseFS(web.Templates, "templates/*.html"))
}

func (s *server) render(w http.ResponseWriter, r *http.Request, status int, name string, data map[string]any) {
	lang := s.i18n.Match(r.Header.Get("Accept-Language"))
	if data == nil {
		data = map[string]any{}
	}
	data["Lang"] = lang
	for _, k := range []string{"Error", "ErrorField"} { // templates compare them
		if _, ok := data[k]; !ok {
			data[k] = ""
		}
	}
	if !strings.HasPrefix(name, "display_") {
		fr := s.frameFor(r)
		if key := s.takeFlash(w, r); key != "" {
			fr.Success = s.i18n.T(lang, key)
		} else if key, ok := data["SuccessKey"].(string); ok {
			fr.Success = s.i18n.T(lang, key) // a page that shows the result itself
		}
		data["Frame"] = fr
	}
	var buf bytes.Buffer
	err := s.execute(&buf, name, lang, data)
	if err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Language", lang)
		w.WriteHeader(status)
		_, _ = buf.WriteTo(w)
	} else {
		s.fail(w, "render "+name, err)
	}
}

// execute runs the named page template in the given language.
func (s *server) execute(w io.Writer, name, lang string, data any) error {
	tpl, err := s.pages.Clone()
	if err != nil {
		return err
	}
	tpl = tpl.Funcs(template.FuncMap{"t": func(key string) string { return s.i18n.T(lang, key) }})
	return tpl.ExecuteTemplate(w, name, data)
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

// parentUpdateNotice is the update notice for a signed-in parent; children see none.
func (s *server) parentUpdateNotice(p store.Person, h store.Household) *updateView {
	if p.Role != store.RoleParent {
		return nil
	}
	return s.updateNotice(h)
}

// strs turns a form value (nothing, one string or a list) into a list for the
// chips and options that mark their selected values.
func strs(v any) []string {
	switch x := v.(type) {
	case string:
		if x != "" {
			return []string{x}
		}
	case []string:
		return x
	}
	return nil
}
