package server

import (
	"context"
	"errors"
	"html"
	"html/template"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/pki"
)

// The page "Sichere Verbindung" of Settings and the router name (ticket 216,
// decision Q92): the names the box certificate covers, the way from away
// through a VPN, and the form that adds the name of the box in the router.
// The CA is name-constrained to the box, so a new name needs a new CA: the
// form says so before the button, and afterwards leads to trusting the box again.

const httpsSettingsPath = "/settings/https"

// lookupTimeout bounds the reverse lookup of the box's address: a router that
// does not answer must not hold the wizard back.
const lookupTimeout = 2 * time.Second

// httpsHubState is the key of the state the hub row opens its hint with.
func (s *server) httpsHubState() string {
	if s.publicInUse() {
		return "hub.https.public"
	}
	return "hub.https.own"
}

// lookupRouterName asks the router for the name of the box (a reverse lookup
// of its LAN address, which is the host the browser used) and returns the
// first name a certificate can carry, or "". Only addresses of the home
// network are looked up: a public address would go to a DNS server outside.
func (s *server) lookupRouterName(ctx context.Context, host string) string {
	addr, err := netip.ParseAddr(host)
	if err != nil || !(addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast()) {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	names, err := s.resolver.LookupAddr(ctx, addr.String())
	if err != nil { // a router that does not answer leaves the field empty
		return ""
	}
	covered, _ := pki.PlannedNames(host, "")
	for _, n := range names {
		if clean, err := pki.CleanRouterName(n, covered); err == nil {
			return clean
		}
	}
	return ""
}

// routerNameProblem turns the refusal of a router name into the error of its
// field. ok is false for any other error.
func (s *server) routerNameProblem(r *http.Request, err error, typed string) (e httpsError, ok bool) {
	key := ""
	switch {
	case errors.Is(err, pki.ErrNameEmpty):
		key = "empty"
	case errors.Is(err, pki.ErrNameNoDot):
		key = "nodot"
	case errors.Is(err, pki.ErrNameChars):
		key = "chars"
	case errors.Is(err, pki.ErrAddressCovered):
		key = "address_covered"
	case errors.Is(err, pki.ErrNameIsAddress):
		key = "address"
	case errors.Is(err, pki.ErrNameCovered):
		key = "covered"
	default:
		return httpsError{}, false
	}
	key = "https.router.error." + key
	msg := i18n.Msg(key)
	if ip := net.ParseIP(strings.TrimSpace(typed)); ip != nil {
		msg = i18n.Msg(key, "address", ip.String())
	}
	text := template.HTML(html.EscapeString(s.i18n.Format(s.pageLanguage(r), msg))) // #nosec G203 -- the text is escaped, the address too
	return httpsError{Key: key, Field: "router_name", Text: text}, true
}

// httpsPageState is what the settings page shows besides the box's state:
// what was typed in the form, its error, and the name that was just entered.
type httpsPageState struct {
	Typed string
	Err   httpsError
	Saved string
}

func (s *server) handleHTTPSSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderHTTPSSettings(w, r, p, http.StatusOK, httpsPageState{Typed: s.pki.RouterName()})
}

func (s *server) handleHTTPSSettingsSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if s.publicInUse() || !s.pki.Ready() { // a public name has no router name to add
		http.Redirect(w, r, httpsSettingsPath, http.StatusSeeOther)
		return
	}
	typed := strings.TrimSpace(r.PostFormValue("router_name"))
	if err := s.pki.Reissue(typed, s.now()); err != nil {
		if e, ok := s.routerNameProblem(r, err, typed); ok {
			s.renderHTTPSSettings(w, r, p, http.StatusUnprocessableEntity, httpsPageState{Typed: typed, Err: e})
			return
		}
		s.fail(w, "reissue box certificate", err)
		return
	}
	// The answer is the page itself, not a redirect: the browser still holds
	// the old connection, so it shows this page without a warning, and the
	// next click already meets the new certificate.
	s.renderHTTPSSettings(w, r, p, http.StatusOK, httpsPageState{Saved: s.pki.RouterName()})
}

func (s *server) renderHTTPSSettings(w http.ResponseWriter, r *http.Request, p principal, status int, st httpsPageState) {
	public := s.publicInUse()
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Public": public,
		"Error": st.Err.Key, "ErrorField": st.Err.Field, "FieldError": "", "ErrorText": st.Err.Text,
		"Value": st.Typed, "Saved": st.Saved != "", "HTTPSWarning": s.httpsWarning(),
	}
	var until time.Time
	if public {
		data["Name"] = s.public.Name()
		until = s.public.Status().Expires
	} else {
		data["Names"] = s.pki.Names()
		until = s.pki.CAExpires()
	}
	if !until.IsZero() {
		data["Until"] = datefmt.Date(p.household.DateFormat, until.UTC())
	}
	if !public {
		lang, address, router := s.pageLanguage(r), s.pki.LANAddress(), s.pki.RouterName()
		hint := i18n.Msg("https.away.hint.below")
		switch {
		case address != "" && router != "":
			hint = i18n.Msg("https.away.hint.address_router", "address", address, "name", router)
		case address != "":
			hint = i18n.Msg("https.away.hint.address_below", "address", address)
		case router != "":
			hint = i18n.Msg("https.away.hint.router", "name", router)
		}
		data["AwayHint"] = s.i18n.Format(lang, hint)
		if st.Saved != "" {
			data["SuccessMsg"] = i18n.Msg("https.router.saved", "name", st.Saved)
			// The page that works before the device trusts the box: plain HTTP (ADR 0014).
			host := address
			if host == "" {
				host = hostOf(r)
			}
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
			data["TrustURL"] = "http://" + host + "/ca"
			data["TrustHint"] = s.i18n.Format(lang, i18n.Msg("https.trust.hint", "url", data["TrustURL"].(string)))
		}
	}
	s.render(w, r, status, "https_settings.html", data)
}
