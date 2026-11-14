package server

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/hejmo-home/hejmo/internal/pki"
)

// requireHTTPS redirects plain-HTTP requests to HTTPS once the box has its
// certificate (ADR 0014), whatever the method. Only what a phone needs before
// it trusts the box (health check, trust page, CA downloads) stays reachable
// over HTTP.
func (s *server) requireHTTPS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil || !s.pki.Ready() || plainHTTPAllowed(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, s.httpsOrigin(r)+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})
}

func plainHTTPAllowed(path string) bool {
	switch path {
	case "/healthz", "/ca", "/ca/hejmo-ca.crt", "/ca/hejmo.mobileconfig":
		return true
	}
	return false
}

// hostOf returns the request's host name without port.
func hostOf(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		return h
	}
	return strings.Trim(r.Host, "[]")
}

// httpsOrigin is the scheme and authority under which the client reaches the box over HTTPS.
func (s *server) httpsOrigin(r *http.Request) string {
	host := hostOf(r)
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if s.httpsPort != "443" {
		host += ":" + s.httpsPort
	}
	return (&url.URL{Scheme: "https", Host: host}).String()
}

func (s *server) handleHTTPSForm(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() || s.pki.Ready() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "setup_https.html", map[string]any{"Host": hostOf(r)})
}

func (s *server) handleHTTPSSubmit(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() || s.pki.Ready() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if err := s.pki.Provision(hostOf(r), s.now()); err != nil {
		if errors.Is(err, pki.ErrInvalidHost) {
			http.Error(w, "This address cannot be used for HTTPS. Open the box by its IP address or a name like hejmo.local.", http.StatusBadRequest)
			return
		}
		s.fail(w, "provision HTTPS", err)
		return
	}
	http.Redirect(w, r, "/ca", http.StatusSeeOther)
}

func (s *server) handleTrustPage(w http.ResponseWriter, r *http.Request) {
	if !s.pki.Ready() {
		http.Redirect(w, r, "/setup/https", http.StatusSeeOther)
		return
	}
	data := map[string]any{"Fingerprint": s.pki.CAFingerprint()}
	if !s.db.Configured() {
		data["Continue"] = s.httpsOrigin(r) + "/setup"
	}
	s.render(w, r, http.StatusOK, "trust.html", data)
}

func (s *server) handleCACert(w http.ResponseWriter, r *http.Request) {
	pem := s.pki.CAPEM()
	if pem == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="hejmo-ca.crt"`)
	_, _ = w.Write(pem)
}

func (s *server) handleMobileConfig(w http.ResponseWriter, r *http.Request) {
	if !s.pki.Ready() {
		http.NotFound(w, r)
		return
	}
	profile, err := s.pki.MobileConfig()
	if err != nil {
		s.fail(w, "mobileconfig", err)
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="hejmo.mobileconfig"`)
	_, _ = w.Write(profile)
}
