package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/pki"
	"github.com/hejmo-home/hejmo/internal/publiccert"
)

// setupTimeout bounds one attempt to get the public certificate.
const setupTimeout = 3 * time.Minute

// requireHTTPS redirects plain-HTTP requests to HTTPS once the box has its
// certificate (ADR 0014), whatever the method. Only what a phone needs before
// it trusts the box (health check, trust page, CA downloads) stays reachable
// over HTTP.
func (s *server) requireHTTPS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil || !s.httpsReady() || s.plainHTTPAllowed(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, s.httpsOrigin(r)+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})
}

func (s *server) plainHTTPAllowed(path string) bool {
	switch path {
	case "/healthz", "/ca", "/ca/hejmo-ca.crt", "/ca/hejmo.mobileconfig":
		return true
	case "/setup/https/check", "/setup/https/own-ca", "/static/https-check.js", "/static/hejmo.css":
		// While the wizard tests the new name, the browser is still on the
		// LAN address and has no other way to show the page.
		return s.publicReady() && !s.db.Configured()
	}
	return false
}

func (s *server) publicReady() bool { return s.public != nil && s.public.Ready() }

// publicInUse reports whether the box serves its public certificate. It is
// false for a stored public setup without a certificate, where only the own
// CA is served (decision Q27).
func (s *server) publicInUse() bool { return s.public != nil && s.public.HasCertificate() }

// httpsReady reports whether the box has a certificate, its own CA's or a public one.
func (s *server) httpsReady() bool { return s.pki.Ready() || s.publicInUse() }

// httpsWarning reports whether parents must be told that the public
// certificate cannot be renewed or that its setup could not be resumed
// (ADR 0014).
func (s *server) httpsWarning() bool { return s.public != nil && s.public.Status().Warning }

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
	if s.publicReady() {
		host = s.public.Name() // a public certificate fits only its name, not the address used
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if s.httpsPort != "443" {
		host += ":" + s.httpsPort
	}
	return (&url.URL{Scheme: "https", Host: host}).String()
}

func (s *server) handleHTTPSForm(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() || s.httpsReady() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.renderHTTPSStep(w, r, http.StatusOK, publicForm{Issuer: publiccert.LetsEncrypt}, "")
}

// publicForm is what the wizard's public-name form keeps between attempts.
// Secrets (token, account key) are never sent back.
type publicForm struct {
	Name, Issuer, LANIP, Email, EABKeyID string
}

func (s *server) renderHTTPSStep(w http.ResponseWriter, r *http.Request, status int, f publicForm, errKey string) {
	if f.LANIP == "" && net.ParseIP(hostOf(r)) != nil {
		f.LANIP = hostOf(r)
	}
	s.render(w, r, status, "setup_https.html", map[string]any{
		"Host": hostOf(r), "Form": f, "Public": s.public != nil, "Error": errKey, "HTTPSWarning": s.httpsWarning(),
	})
}

func (s *server) handlePublicHTTPSSubmit(w http.ResponseWriter, r *http.Request) {
	if s.public == nil {
		http.NotFound(w, r)
		return
	}
	if s.db.Configured() || s.httpsReady() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	trim := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	req := publiccert.Request{
		Name: strings.ToLower(trim("name")), Provider: trim("provider"), Token: trim("token"), Issuer: trim("issuer"),
		LANIP: trim("lan_ip"), Email: trim("email"), EABKeyID: trim("eab_key_id"), EABHMAC: trim("eab_hmac"),
	}
	form := publicForm{Name: req.Name, Issuer: req.Issuer, LANIP: req.LANIP, Email: req.Email, EABKeyID: req.EABKeyID}
	// The order can take a minute and must not be cancelled partway through
	// if the browser gives up: it runs on its own context.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), setupTimeout)
	defer cancel()
	err := s.public.Setup(ctx, req)
	switch {
	case err == nil:
		http.Redirect(w, r, "/setup/https/check", http.StatusSeeOther)
	case errors.Is(err, publiccert.ErrInvalid), errors.Is(err, publiccert.ErrUnknownProvider):
		s.renderHTTPSStep(w, r, http.StatusUnprocessableEntity, form, "setup.https.public.invalid")
	default:
		s.logger.Error("public HTTPS setup", "err", err) // the CA's text is English and technical: log only
		s.renderHTTPSStep(w, r, http.StatusBadGateway, form, "setup.https.public.failed")
	}
}

// handleNameCheck is the wizard step after the certificate is there: the
// browser itself tries the new name (see static/https-check.js), because only
// the browser knows whether the router lets the name resolve to a LAN address.
func (s *server) handleNameCheck(w http.ResponseWriter, r *http.Request) {
	if !s.publicReady() {
		http.Redirect(w, r, "/setup/https", http.StatusSeeOther)
		return
	}
	origin := s.httpsOrigin(r)
	next := "/setup"
	if s.db.Configured() {
		next = "/"
	}
	s.render(w, r, http.StatusOK, "https_check.html", map[string]any{"Name": s.public.Name(), "CheckURL": origin + "/healthz", "Continue": origin + next})
}

// handleSwitchToOwnCA is the way out of the name check: if the router cannot
// let the name resolve, the family uses the box's own CA instead (ADR 0014).
// It forgets the public setup and is closed once the box is set up.
func (s *server) handleSwitchToOwnCA(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if !s.publicReady() {
		http.Redirect(w, r, "/setup/https", http.StatusSeeOther)
		return
	}
	if err := s.public.Clear(); err != nil {
		s.fail(w, "forget public HTTPS setup", err)
		return
	}
	s.provisionOwnCA(w, r)
}

func (s *server) apiHTTPS(w http.ResponseWriter, r *http.Request, _ principal) {
	out := httpsJSON{Mode: "own-ca"}
	if s.public != nil {
		st := s.public.Status()
		// A stored setup that cannot be resumed has no name yet but still warns.
		out.Warning, out.RenewalError = st.Warning, st.RenewalError
		if s.publicInUse() {
			out.Mode, out.Name, out.Issuer = "public", st.Name, st.Issuer
			if !st.Expires.IsZero() {
				exp := st.Expires.UTC().Format(time.RFC3339)
				out.ExpiresAt = &exp
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type httpsJSON struct {
	Mode         string  `json:"mode"`
	Name         string  `json:"name,omitempty"`
	Issuer       string  `json:"issuer,omitempty"`
	ExpiresAt    *string `json:"expires_at,omitempty"`
	Warning      bool    `json:"warning"`
	RenewalError string  `json:"renewal_error,omitempty"`
}

func (s *server) handleHTTPSSubmit(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() || s.httpsReady() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	// Choosing the own CA forgets any public attempt, including its warning.
	if s.public != nil {
		if err := s.public.Clear(); err != nil {
			s.fail(w, "forget public HTTPS setup", err)
			return
		}
	}
	s.provisionOwnCA(w, r)
}

func (s *server) provisionOwnCA(w http.ResponseWriter, r *http.Request) {
	if err := s.pki.Provision(hostOf(r), s.now()); err != nil {
		if errors.Is(err, pki.ErrInvalidHost) {
			s.renderHTTPSStep(w, r, http.StatusBadRequest, publicForm{Issuer: publiccert.LetsEncrypt}, "setup.https.invalidhost")
			return
		}
		s.fail(w, "provision HTTPS", err)
		return
	}
	http.Redirect(w, r, "/ca", http.StatusSeeOther)
}

func (s *server) handleTrustPage(w http.ResponseWriter, r *http.Request) {
	if s.publicInUse() { // a public certificate needs no trust step
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
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
