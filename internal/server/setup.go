package server

import (
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/qrsvg"
	"github.com/hejmo-home/hejmo/internal/region"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Default person colours and black-and-white shapes ("Nordic Calm"), in order.
var personStyles = []struct{ color, shape string }{
	{"#B4583A", "circle-filled"}, {"#2F5E7A", "circle-empty"},
	{"#4E7D52", "square-filled"}, {"#9A7420", "square-empty"},
	{"#86587F", "triangle-filled"}, {"#3A7A74", "diamond-filled"},
}

func (s *server) handleSetupForm(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.httpsReady() {
		http.Redirect(w, r, "/setup/https", http.StatusSeeOther)
		return
	}
	s.renderSetup(w, r, http.StatusOK, setupForm{Region: "DE-BY"}, "")
}

type setupForm struct {
	HomeName, Region, Name, Username, password, pin string
}

func (s *server) renderSetup(w http.ResponseWriter, r *http.Request, status int, f setupForm, errKey string) {
	s.render(w, r, status, "setup.html", map[string]any{"Form": f, "Regions": region.All(), "Error": errKey, "ErrorField": setupErrorField(errKey, f)})
}

func (s *server) handleSetupSubmit(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.httpsReady() { // ADR 0014: no password before HTTPS works
		http.Redirect(w, r, "/setup/https", http.StatusSeeOther)
		return
	}
	f := setupForm{
		HomeName: strings.TrimSpace(r.PostFormValue("home_name")),
		Region:   r.PostFormValue("region"),
		Name:     strings.TrimSpace(r.PostFormValue("name")),
		Username: strings.TrimSpace(r.PostFormValue("username")),
		password: r.PostFormValue("password"),
		pin:      strings.TrimSpace(r.PostFormValue("pin")),
	}
	passwordHash, pinHash, errKey := validateSetup(f)
	if errKey != "" {
		s.renderSetup(w, r, http.StatusUnprocessableEntity, f, errKey)
		return
	}
	if err := s.db.ResetUnfinishedSetup(); err != nil {
		s.fail(w, "reset setup", err)
		return
	}
	personID, err := s.createFirstParent(f, passwordHash, pinHash, s.i18n.Match(r.Header.Get("Accept-Language")))
	if err != nil {
		s.fail(w, "setup", err)
		return
	}
	if err := s.startSession(w, r, personID); err != nil {
		s.fail(w, "setup session", err)
		return
	}
	http.Redirect(w, r, "/setup/totp", http.StatusSeeOther)
}

func validateSetup(f setupForm) (passwordHash, pinHash, errKey string) {
	if f.HomeName == "" {
		return "", "", "setup.error.home"
	}
	if _, ok := region.Lookup(f.Region); !ok {
		return "", "", "setup.error.region"
	}
	if f.Name == "" || f.Username == "" {
		return "", "", "setup.error.name"
	}
	passwordHash, err := auth.HashPassword(f.password)
	if err != nil {
		return "", "", "setup.error.password"
	}
	pinHash, err = auth.HashPIN(f.pin)
	if err != nil {
		return "", "", "setup.error.pin"
	}
	return passwordHash, pinHash, ""
}

func (s *server) createFirstParent(f setupForm, passwordHash, pinHash, lang string) (string, error) {
	h, err := s.db.CreateHousehold(store.Household{Name: f.HomeName, HomeName: f.HomeName, Region: f.Region})
	if err != nil {
		return "", err
	}
	if err := s.createDefaultLists(h.ID, lang); err != nil {
		return "", err
	}
	style := personStyles[0]
	p, err := s.db.AddPerson(h.ID, store.Person{Name: f.Name, Role: store.RoleParent, Color: style.color, Shape: style.shape})
	if err != nil {
		return "", err
	}
	if err := s.db.CreateAccount(store.Account{PersonID: p.ID, Username: f.Username, PasswordHash: passwordHash, TOTPSecret: auth.NewTOTPSecret()}); err != nil {
		return "", err
	}
	return p.ID, s.db.SetDisplayPIN(h.ID, pinHash)
}

// setupAccount is the account behind a browser session that may set up
// one-time codes. A paired phone's device session survives a reset but must not
// take the new setup over: that is for the parent, after their password.
func (s *server) setupAccount(r *http.Request) (store.Account, bool) {
	p, ok := s.pendingPerson(r)
	if !ok || s.phoneSession(r) {
		return store.Account{}, false
	}
	acc, err := s.db.AccountByPerson(p.ID)
	return acc, err == nil
}

// totpSetupAccount returns the account that is setting up one-time codes: the
// first parent in the wizard, or a person whose codes were reset. Anyone else
// is sent on.
func (s *server) totpSetupAccount(w http.ResponseWriter, r *http.Request) (store.Account, bool) {
	acc, ok := s.setupAccount(r)
	switch {
	case !ok:
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	case acc.TOTPConfirmed:
		http.Redirect(w, r, "/", http.StatusSeeOther)
	default:
		return acc, true
	}
	return store.Account{}, false
}

func (s *server) handleSetupTOTPForm(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.totpSetupAccount(w, r)
	if !ok {
		return
	}
	if acc.TOTPSecret == "" { // reset: a new secret for the new setup
		acc.TOTPSecret = auth.NewTOTPSecret()
		if err := s.db.SetTOTPSecret(acc.PersonID, acc.TOTPSecret); err != nil {
			s.fail(w, "new TOTP secret", err)
			return
		}
	}
	s.renderTOTP(w, r, http.StatusOK, acc, "")
}

func (s *server) renderTOTP(w http.ResponseWriter, r *http.Request, status int, acc store.Account, errKey string) {
	uri := auth.TOTPURI(acc.TOTPSecret, acc.Username)
	svg, err := qrsvg.SVG(uri, s.i18n.T(s.langOf(r), "setup.totp.qr"))
	if err != nil {
		s.fail(w, "qr code", err)
		return
	}
	s.render(w, r, status, "setup_totp.html", map[string]any{
		"Secret": acc.TOTPSecret, "URI": template.URL(uri), // built here, safe scheme
		"QR": template.HTML(svg), "Error": errKey, "ErrorField": totpErrorField(errKey),
	})
}

func (s *server) handleSetupTOTPSubmit(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.totpSetupAccount(w, r)
	if !ok {
		return
	}
	if acc.TOTPSecret == "" { // the setup page was not opened yet
		http.Redirect(w, r, "/setup/totp", http.StatusSeeOther)
		return
	}
	if !auth.VerifyTOTP(acc.TOTPSecret, r.PostFormValue("code"), s.now()) {
		s.renderTOTP(w, r, http.StatusUnprocessableEntity, acc, "setup.error.code")
		return
	}
	// The codes are stored before the confirmation: if storing fails, the
	// person is not confirmed yet and can try again instead of being confirmed
	// without ever seeing their codes.
	codes, err := s.newRecoveryCodes(acc.PersonID)
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	if err := s.db.ConfirmTOTP(acc.PersonID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "confirm TOTP", err)
		return
	}
	s.setFlash(w, r, "flash.setup_done") // shown on the home page after the codes
	s.showRecoveryCodes(w, r, codes)
}

// setupErrorField names the input of the setup form that an error belongs to.
func setupErrorField(errKey string, f setupForm) string {
	switch errKey {
	case "setup.error.home":
		return "home_name"
	case "setup.error.region":
		return "region"
	case "setup.error.name":
		if f.Name == "" {
			return "name"
		}
		return "username"
	case "setup.error.password":
		return "password"
	case "setup.error.pin":
		return "pin"
	}
	return ""
}

func totpErrorField(errKey string) string {
	if errKey == "" {
		return ""
	}
	return "code"
}

// createDefaultLists gives a new household the lists "Shopping" and "To-dos"
// (ADR 0015), named in the language of the setup.
func (s *server) createDefaultLists(householdID, lang string) error {
	for _, d := range []struct{ key, color, symbol string }{
		{"lists.default.shopping", "green", "square-filled"},
		{"lists.default.todos", "blue", "circle-empty"},
	} {
		if _, err := s.db.CreateList(householdID, list.List{Name: s.i18n.T(lang, d.key), Color: d.color, Symbol: d.symbol, Sort: list.Manual}); err != nil {
			return err
		}
	}
	return nil
}
