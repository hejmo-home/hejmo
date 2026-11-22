package server

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
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
		if r.TLS != nil || !s.httpsReady() || s.plainHTTPAllowed(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, s.httpsOrigin(r)+r.URL.RequestURI(), http.StatusPermanentRedirect)
	})
}

// plainStaticAsset reports whether the request is for one of the static files
// the plain-HTTP pages need to look right before the device trusts the box:
// stylesheet, font, icons and the flags of the language switcher. They are public and identical on every box, so
// serving them in the clear leaks nothing. Scripts stay HTTPS-only (code
// fetched over HTTP could be swapped). Only single files directly inside the
// three directories match, and only for a clean, unencoded path: "..%2f" or
// "%2e%2e" must not reach a script, and no directory listing is served.
func plainStaticAsset(u *url.URL) bool {
	p := u.Path
	if p == "/static/hejmo.css" {
		return u.RawPath == ""
	}
	if u.RawPath != "" || path.Clean(p) != p {
		return false
	}
	for _, dir := range []string{"/static/fonts/", "/static/icons/", "/static/flags/"} {
		if name, ok := strings.CutPrefix(p, dir); ok {
			return name != "" && !strings.Contains(name, "/") && name != "." && name != ".."
		}
	}
	return false
}

func (s *server) plainHTTPAllowed(r *http.Request) bool {
	u := r.URL
	if plainStaticAsset(u) {
		return true
	}
	// The trust page has the language switcher (ticket 134): choosing a
	// language sets only a cookie, so it may work before the box is trusted.
	if u.Path == "/language" && r.Method == http.MethodPost {
		return true
	}
	if strings.HasPrefix(u.Path, "/i/") && !strings.Contains(strings.TrimPrefix(u.Path, "/i/"), "/") {
		// The short address of an invitation (ticket 145) shows the trust
		// page of a box with its own CA before the device trusts the box.
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	switch u.Path {
	case "/healthz", "/ca", "/ca/hejmo-ca.crt", "/ca/hejmo.mobileconfig", "/static/https-check.js":
		// Pages to read: nothing is posted to them over HTTP. The trust page
		// runs its own check script (ticket 133) before the device trusts the box.
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	case "/setup/https/check", "/setup/https/own-ca":
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
	way := httpsChoice
	switch r.URL.Query().Get("way") {
	case "public":
		way = httpsPublic
	case "domain":
		way = httpsDomain
	case "own":
		way = httpsOwn
	}
	// Right after "Only in the home network" the wizard asks whether the box
	// stays fully offline (ticket 201, Q142); a public name needs the internet,
	// so choosing it takes an earlier "yes" back.
	if own := way == httpsOwn || s.public == nil; own && !s.outboundBox().Asked {
		s.renderOfflineQuestion(w, r, http.StatusOK, false)
		return
	} else if !own {
		s.takeOfflineAnswerBack()
	}
	// "Continue" without a choice (the form sends next=1, no way is preselected):
	// the choice again, with the error under the cards.
	if way == httpsChoice && r.URL.Query().Get("next") != "" && s.public != nil {
		s.renderHTTPSStep(w, r, http.StatusUnprocessableEntity, way, publicForm{Issuer: publiccert.LetsEncrypt},
			httpsError{Key: "setup.https.error.way", Field: "way"})
		return
	}
	f := publicForm{Issuer: publiccert.LetsEncrypt}
	if way == httpsOwn || s.public == nil { // the box asks the router for its own name (decision Q92); without the public service the own CA page is the only one
		way = httpsOwn
		f.RouterName = s.lookupRouterName(r.Context(), hostOf(r))
		f.RouterHint = "none"
		if f.RouterName != "" {
			f.RouterHint = "found"
		}
	}
	s.renderHTTPSStep(w, r, http.StatusOK, way, f, httpsError{})
}

// The pages of the wizard's first step: the choice between three ways, the
// free name at deSEC, the family's own domain at a DNS provider (ticket 57)
// and the own certificate authority.
const (
	httpsChoice = "choice"
	httpsPublic = "public"
	httpsDomain = "domain"
	httpsOwn    = "own"
)

// domainProviders are the DNS providers of the own-domain page, in the order
// of its cards, with the name the texts use for them.
var domainProviders = []struct{ ID, Name string }{
	{"cloudflare", "Cloudflare"}, {"desec", "deSEC"}, {"hetzner", "Hetzner"}, {"inwx", "INWX"},
}

// publicForm is what the wizard's public-name form keeps between attempts.
// Secrets (token, second token, password, shared secret, account key) are
// never sent back.
type publicForm struct {
	Name, Issuer, LANIP, Email, EABKeyID string
	Provider, Username                   string
	TwoFA                                bool
	// RouterName is what the router-name field of the own-CA page holds; RouterHint says where
	// it comes from: "found" (the box asked the router), "none" (the router had no name for it)
	// or "typed" (the family typed it).
	RouterName, RouterHint string
}

// httpsError is an error of the first step: Key is the page-level text, Field
// the form field it belongs to and FieldKey a text of its own for that field
// (when empty, the field shows Key). Fields names further fields (field to
// text key) that are wrong at the same time; Text is a ready HTML text for
// Field, built with escaped names.
type httpsError struct {
	Key, Field, FieldKey string
	Fields               map[string]string
	Text                 template.HTML
}

func (s *server) renderHTTPSStep(w http.ResponseWriter, r *http.Request, status int, way string, f publicForm, e httpsError) {
	if s.public == nil && way != httpsOwn { // only the box's own certificate authority is on offer
		way = httpsOwn
	}
	if f.LANIP == "" && net.ParseIP(hostOf(r)) != nil {
		f.LANIP = hostOf(r)
	}
	// The own-domain page has one guide per provider; an error belongs to
	// the guide that was submitted (as on the page that connects a calendar).
	guides := map[string]any{}
	for _, p := range domainProviders {
		g := map[string]any{"Error": "", "ErrorField": "", "FieldError": "", "ErrorFields": map[string]string(nil), "ErrorText": template.HTML(""), "MoreOpen": f.LANIP == ""}
		if p.ID == f.Provider {
			g["Error"], g["ErrorField"], g["FieldError"], g["ErrorFields"], g["ErrorText"] = e.Key, e.Field, e.FieldKey, e.Fields, e.Text
			g["MoreOpen"] = f.LANIP == "" || e.Field == "lan_ip" || e.Field == "zone_token" || e.Field == "eab" || e.Fields["zone_token"] != ""
		}
		guides[p.ID] = g
	}
	// The own-CA page lists the names the certificate will cover, and keeps
	// "More options" open when it has a router name or the name was refused.
	names, err := pki.PlannedNames(hostOf(r), f.RouterName)
	if err != nil { // the typed name is wrong or the host unusable: show what holds without it
		names, err = pki.PlannedNames(hostOf(r), "")
	}
	if err != nil {
		names = []string{hostOf(r)}
	}
	s.render(w, r, status, "setup_https.html", map[string]any{
		"CertNames": names, "RouterOpen": f.RouterName != "" || e.Field == "router_name",
		"Way": way, "Host": hostOf(r), "Form": f, "Wizard": wizardAt(1), "Choice": s.public != nil, "Provider": f.Provider,
		"Providers": domainProviders, "Guides": guides,
		"Error": e.Key, "ErrorField": e.Field, "FieldError": e.FieldKey, "ErrorFields": e.Fields, "ErrorText": e.Text, "HTTPSWarning": s.httpsWarning(),
		// "More options" are open when the address is missing or was refused.
		"MoreOpen": f.LANIP == "" || e.Field == "lan_ip" || e.Field == "zone_token" || e.Field == "eab",
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
	s.takeOfflineAnswerBack() // the DNS service is refused while offline
	trim := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	req := publiccert.Request{
		Name: strings.ToLower(trim("name")), Provider: trim("provider"), Token: trim("token"), Issuer: trim("issuer"),
		LANIP: trim("lan_ip"), Email: trim("email"), EABKeyID: trim("eab_key_id"), EABHMAC: trim("eab_hmac"),
		ZoneToken: trim("zone_token"), Username: trim("username"), Password: r.PostFormValue("password"),
		TwoFA: r.PostFormValue("twofa") != "",
	}
	if req.TwoFA {
		req.OTPSecret = trim("otp_secret")
	}
	form := publicForm{Name: req.Name, Issuer: req.Issuer, LANIP: req.LANIP, Email: req.Email, EABKeyID: req.EABKeyID,
		Provider: req.Provider, Username: req.Username, TwoFA: req.TwoFA}
	// The page the form came from comes back with its error: the own-domain
	// page names "domain", the free-name page nothing.
	way := httpsPublic
	if r.PostFormValue("way") == httpsDomain {
		way = httpsDomain
	}
	// The order can take a minute and must not be cancelled partway through
	// if the browser gives up: it runs on its own context.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), setupTimeout)
	defer cancel()
	err := s.public.Setup(ctx, req)
	var inv *publiccert.InvalidError
	var taken *publiccert.NameTakenError
	switch {
	case err == nil:
		http.Redirect(w, r, "/setup/https/check", http.StatusSeeOther)
	case errors.As(err, &taken):
		// Nothing went wrong with the credentials, but the box keeps none of
		// them: the secret fields come back empty and the family pastes them
		// again (the other fields keep their values).
		s.renderHTTPSStep(w, r, http.StatusUnprocessableEntity, way, form, httpsError{
			Key: "setup.https.error.name_taken", Field: "name", Text: s.nameTakenText(r, taken),
		})
	case errors.Is(err, publiccert.ErrInvalid), errors.Is(err, publiccert.ErrUnknownProvider):
		e := httpsError{Key: "setup.https.error.invalid"}
		if errors.As(err, &inv) && slices.Contains([]string{"token", "username", "password", "otp_secret", "name", "lan_ip", "eab"}, inv.Field) {
			e = httpsError{Key: "setup.https.error." + inv.Field, Field: inv.Field}
			if inv.Field == "otp_secret" && req.OTPSecret == "" {
				e.Key = "setup.https.error.otp_secret_missing" // nothing was typed: not "the 6-digit code"
			}
		}
		s.renderHTTPSStep(w, r, http.StatusUnprocessableEntity, way, form, e)
	default:
		s.logger.Error("public HTTPS setup", "err", err) // the CA's text is English and technical: log only
		s.renderHTTPSStep(w, r, http.StatusBadGateway, way, form, failedStep(req))
	}
}

// failedStep is the error after a failed attempt: the likely cause for the
// provider on top of its guide, and a note at each secret that the box does
// not keep one after an error (none of them is sent back).
func failedStep(req publiccert.Request) httpsError {
	key := "setup.https.public.failed"
	if req.Provider != "desec" {
		key += "." + req.Provider
	}
	switch req.Provider {
	case "inwx":
		e := httpsError{Key: key, Field: "password", FieldKey: "setup.https.error.password_again", Fields: map[string]string{"password": "setup.https.error.password_again"}} // #nosec G101 -- an i18n key, no credential
		if req.TwoFA {
			e.Fields["otp_secret"] = "setup.https.error.secret_again"
		}
		return e
	case "cloudflare":
		e := httpsError{Key: key, Field: "token", FieldKey: "setup.https.error.token_again"}
		if req.ZoneToken != "" {
			e.Fields = map[string]string{"token": "setup.https.error.token_again", "zone_token": "setup.https.error.zone_token_again"} // #nosec G101 -- an i18n key, no credential
		}
		return e
	}
	return httpsError{Key: key, Field: "token", FieldKey: "setup.https.error.token_again"}
}

// nameTakenText is the error at the name field: names and address come from
// the provider and the family, so they are escaped; each is a .host that may
// wrap on a narrow screen.
func (s *server) nameTakenText(r *http.Request, e *publiccert.NameTakenError) template.HTML {
	text := html.EscapeString(s.i18n.T(s.pageLanguage(r), "setup.https.error.name_taken"))
	host := func(v string) string { return `<span class="host">` + html.EscapeString(v) + `</span>` }
	text = strings.NewReplacer("{name}", host(e.Name), "{address}", host(e.Address), "{suggestion}", host(e.Suggestion)).Replace(text)
	return template.HTML(text) // #nosec G203 -- the text is escaped, the values too
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
	s.render(w, r, http.StatusOK, "https_check.html", map[string]any{"Name": s.public.Name(), "CheckURL": origin + "/healthz", "Continue": origin + next, "Wizard": wizardAt(1)})
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
	// Choosing the public name took the offline answer back, so the wizard
	// asks again (ticket 201, Q142). The browser is on plain HTTP here, so the
	// question is answered at this address, not at /setup/offline.
	asked, offline := s.outboundBox().Asked, false
	if !asked {
		switch r.PostFormValue("offline") {
		case "on":
			offline = true
		case "off":
		case "":
			s.renderOfflineQuestionAt(w, r, http.StatusOK, false, "/setup/https/own-ca")
			return
		default:
			s.renderOfflineQuestionAt(w, r, http.StatusUnprocessableEntity, true, "/setup/https/own-ca")
			return
		}
	}
	if err := s.public.Clear(); err != nil {
		s.fail(w, "forget public HTTPS setup", err)
		return
	}
	// The answer is stored last: if the public setup could not be forgotten or
	// the own CA not made, the question stays open and a retry asks again.
	s.provisionOwnCA(w, r, "", func() error {
		if asked {
			return nil
		}
		return s.db.AnswerOfflineQuestion(offline, s.now())
	})
}

func (s *server) apiHTTPS(w http.ResponseWriter, r *http.Request, _ principal) {
	writeJSON(w, http.StatusOK, s.httpsStatus())
}

// httpsStatus is how the box secures HTTPS now.
func (s *server) httpsStatus() httpsJSON {
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
			return out
		}
	}
	if s.pki.Ready() { // the box's own CA: the names its certificate covers and when the CA ends
		out.Names, out.RouterName = s.pki.Names(), s.pki.RouterName()
		exp := s.pki.CAExpires().UTC().Format(time.RFC3339)
		out.ExpiresAt = &exp
	}
	return out
}

type httpsJSON struct {
	Mode         string   `json:"mode"`
	Name         string   `json:"name,omitempty"`
	Issuer       string   `json:"issuer,omitempty"`
	ExpiresAt    *string  `json:"expires_at,omitempty"`
	Names        []string `json:"names,omitempty"`
	RouterName   string   `json:"router_name,omitempty"`
	Warning      bool     `json:"warning"`
	RenewalError string   `json:"renewal_error,omitempty"`
}

// apiSetRouterName is the settings page's form as an API call (ADR 0015): it
// adds the router name and makes a new box CA, so every device must trust the
// box again.
func (s *server) apiSetRouterName(w http.ResponseWriter, r *http.Request, _ principal) {
	var in struct {
		RouterName *string `json:"router_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if s.publicInUse() || !s.pki.Ready() {
		writeError(w, http.StatusConflict, "the box uses a public name or has no CA yet: a router name belongs to the box's own CA")
		return
	}
	if in.RouterName == nil {
		writeError(w, http.StatusUnprocessableEntity, "router_name is required")
		return
	}
	if err := s.pki.Reissue(*in.RouterName, s.now()); err != nil {
		if _, ok := s.routerNameProblem(r, err, *in.RouterName); ok {
			writeError(w, http.StatusUnprocessableEntity, "router_name: "+strings.TrimPrefix(err.Error(), "pki: "))
			return
		}
		s.fail(w, "reissue box certificate", err)
		return
	}
	writeJSON(w, http.StatusOK, s.httpsStatus())
}

func (s *server) handleHTTPSSubmit(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() || s.httpsReady() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	// The router name is optional (decision Q92); a name that cannot go into
	// the certificate comes back at its field, with what was typed.
	router := strings.TrimSpace(r.PostFormValue("router_name"))
	if router != "" {
		if _, err := pki.PlannedNames(hostOf(r), router); err != nil && !errors.Is(err, pki.ErrInvalidHost) {
			if e, ok := s.routerNameProblem(r, err, router); ok {
				s.renderHTTPSStep(w, r, http.StatusUnprocessableEntity, httpsOwn, publicForm{Issuer: publiccert.LetsEncrypt, RouterName: router, RouterHint: "typed"}, e)
				return
			}
		}
	}
	// Choosing the own CA forgets any public attempt, including its warning.
	if s.public != nil {
		if err := s.public.Clear(); err != nil {
			s.fail(w, "forget public HTTPS setup", err)
			return
		}
	}
	s.provisionOwnCA(w, r, router, nil)
}

// provisionOwnCA makes the box's own CA and certificate and sends the browser
// on to the trust page. after, if set, runs once that worked.
func (s *server) provisionOwnCA(w http.ResponseWriter, r *http.Request, routerName string, after func() error) {
	if err := s.pki.ProvisionWithRouterName(hostOf(r), routerName, s.now()); err != nil {
		if errors.Is(err, pki.ErrInvalidHost) {
			s.renderHTTPSStep(w, r, http.StatusBadRequest, httpsOwn, publicForm{Issuer: publiccert.LetsEncrypt}, httpsError{Key: "setup.https.invalidhost"})
			return
		}
		s.fail(w, "provision HTTPS", err)
		return
	}
	if after != nil {
		if err := after(); err != nil {
			s.fail(w, "offline answer", err)
			return
		}
	}
	http.Redirect(w, r, "/ca", http.StatusSeeOther)
}

// trustOptions says where the trust page leads on to and in which shell it is
// shown. The zero value is the plain page (what a phone opens before pairing).
type trustOptions struct {
	Continue, ContinueKey string
	Wizard                *wizardView
	// Invite is set when the page is the first step of accepting an invitation.
	Invite *inviteView
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
	var opts trustOptions
	if !s.db.Configured() {
		opts = trustOptions{Continue: s.httpsOrigin(r) + "/setup", ContinueKey: "trust.continue.setup", Wizard: wizardAt(1)}
	} else if next := s.trustContinue(r); next != "" {
		opts = trustOptions{Continue: next, ContinueKey: "trust.continue"} // a phone on its way to pair (ticket 5.13)
	}
	s.renderTrust(w, r, opts)
}

func (s *server) renderTrust(w http.ResponseWriter, r *http.Request, opts trustOptions) {
	page := s.trustDevice(r)
	fp := s.pki.CAFingerprint()
	download, downloadKey := "/ca/hejmo-ca.crt", "trust.download.cert"
	if page.Device == devIOS {
		download, downloadKey = "/ca/hejmo.mobileconfig", "trust.download.profile"
	}
	data := map[string]any{
		"Device": page.Device, "Detected": page.Detected, "Known": page.Known,
		"Steps": trustSteps(page.Device), "Fingerprint": fp, "Pairs": strings.Split(fp, ":"),
		"Download": download, "DownloadKey": downloadKey, "CheckURL": s.httpsOrigin(r) + "/healthz",
		"Continue": opts.Continue, "ContinueKey": opts.ContinueKey, "Invite": opts.Invite,
	}
	if opts.Wizard != nil {
		data["Wizard"] = opts.Wizard
	}
	cont := "" // only a phone's pairing target travels with the device chooser
	if s.db.Configured() && opts.Invite == nil {
		cont = s.trustContinue(r)
	}
	data["Devices"] = trustDeviceLinks(page.Device, cont)
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
