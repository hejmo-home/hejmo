package server

import (
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/region"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Default person colours and black-and-white shapes ("Nordic Calm"), in order.
var personStyles = []struct{ color, shape string }{
	{"#B4583A", "circle-filled"}, {"#2F5E7A", "circle-empty"},
	{"#4E7D52", "square-filled"}, {"#9A7420", "square-empty"},
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
	s.render(w, r, status, "setup.html", map[string]any{"Form": f, "Regions": region.All(), "Error": errKey})
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
	personID, err := s.createFirstParent(f, passwordHash, pinHash)
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

func (s *server) createFirstParent(f setupForm, passwordHash, pinHash string) (string, error) {
	h, err := s.db.CreateHousehold(store.Household{Name: f.HomeName, HomeName: f.HomeName, Region: f.Region})
	if err != nil {
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

func (s *server) setupAccount(r *http.Request) (store.Account, bool) {
	p, ok := s.currentPerson(r)
	if !ok {
		return store.Account{}, false
	}
	acc, err := s.db.AccountByPerson(p.ID)
	return acc, err == nil
}

func (s *server) handleSetupTOTPForm(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	acc, ok := s.setupAccount(r)
	if !ok {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.renderTOTP(w, r, http.StatusOK, acc, "")
}

func (s *server) renderTOTP(w http.ResponseWriter, r *http.Request, status int, acc store.Account, errKey string) {
	s.render(w, r, status, "setup_totp.html", map[string]any{
		"Secret": acc.TOTPSecret, "URI": template.URL(auth.TOTPURI(acc.TOTPSecret, acc.Username)), // built here, safe scheme, "Error": errKey,
	})
}

func (s *server) handleSetupTOTPSubmit(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	acc, ok := s.setupAccount(r)
	if !ok {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if !auth.VerifyTOTP(acc.TOTPSecret, r.PostFormValue("code"), s.now()) {
		s.renderTOTP(w, r, http.StatusUnprocessableEntity, acc, "setup.error.code")
		return
	}
	if err := s.db.ConfirmTOTP(acc.PersonID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "confirm TOTP", err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
