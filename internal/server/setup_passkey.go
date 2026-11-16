package server

import (
	"net/http"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The offer of a passkey in the setup wizard and in an invitation (ticket 95,
// ADR 0018): right after "Create account", because a passkey needs both HTTPS
// and an account. It is optional; password and one-time code stay the way back.

// afterAccountCreated leads on from a created account: to the offer of a
// passkey when the box was reached by its name, else straight to the one-time
// code (an IP address cannot have a passkey; no message, nothing to find).
func (s *server) afterAccountCreated(w http.ResponseWriter, r *http.Request, name string) {
	if s.atBoxName(r) {
		s.setFlash(w, r, "flash.account_created:"+flashNamePrefix+b64Name(name))
		http.Redirect(w, r, "/setup/passkey", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/setup/totp", http.StatusSeeOther)
}

// offerShell is the wizard shell of the offer: step 2 of the setup, or the
// "Your account" step of an invitation; nil when this account has no such step.
func (s *server) offerShell(acc store.Account) *wizardView {
	if !s.db.Configured() {
		return wizardAt(2)
	}
	if s.inviteShellFor(acc) != nil {
		return s.inviteAccountShell()
	}
	return nil
}

func (s *server) handlePasskeyOffer(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.setupAccount(r)
	switch {
	case !ok:
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	case acc.TOTPConfirmed:
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case !s.atBoxName(r) || s.offerShell(acc) == nil:
		http.Redirect(w, r, "/setup/totp", http.StatusSeeOther)
	default:
		s.renderPasskeyOffer(w, r, acc, http.StatusOK, false, "")
	}
}

func (s *server) renderPasskeyOffer(w http.ResponseWriter, r *http.Request, acc store.Account, status int, failed bool, name string) {
	shell := s.offerShell(acc)
	if shell == nil || !s.atBoxName(r) {
		http.Redirect(w, r, "/setup/totp", http.StatusSeeOther)
		return
	}
	lang := s.langOf(r)
	device, _ := auth.DeviceFromUserAgent(r.UserAgent())
	person, _ := s.member(acc.PersonID)
	if name = cleanPasskeyName(name); name == "" {
		name = s.passkeyNameSuggestion(lang, person.Name, device)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, status, "setup_passkey.html", map[string]any{
		"Wizard": shell, "Name": name, "BoxName": s.boxName(), "Failed": failed, "Device": s.deviceKey(device),
	})
}

// deviceKey is the key the offer's texts are chosen by: the device, or "unknown".
func (s *server) deviceKey(device string) string {
	switch device {
	case "iphone", "ipad", "android_phone", "android_tablet", "mac", "windows", "linux", "chromebook":
		return device
	}
	return auth.Unknown
}
