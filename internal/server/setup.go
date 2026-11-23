package server

import (
	"errors"
	"html/template"
	"net/http"
	"strings"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/places"
	"github.com/hejmo-home/hejmo/internal/qrsvg"
	"github.com/hejmo-home/hejmo/internal/region"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Default person colours and black-and-white shapes ("Nordic Calm"), in order.
var personStyles = []struct{ color, shape string }{
	{"#B4583A", "circle-filled"}, {"#2F5E7A", "circle-empty"},
	{"#4E7D52", "square-filled"}, {"#9A7420", "square-empty"},
	{"#86587F", "triangle-filled"}, {"#3A7A74", "diamond-filled"},
	{"#5559A3", "cross-filled"}, {"#B04A6A", "star-filled"},
}

func (s *server) handleSetupForm(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.httpsReady() { // ADR 0014: HTTPS first; the welcome page says what is coming
		s.render(w, r, http.StatusOK, "setup_welcome.html", map[string]any{"Wizard": wizardAt(0)})
		return
	}
	s.renderSetup(w, r, http.StatusOK, setupForm{Place: &placePick{}}, "")
}

type setupForm struct {
	// Region is the state or canton, "" until a place is chosen: the place
	// then sets it (the parent may still change it).
	Region, Name, Username, password, pin string
	// Place is what the home location picker shows.
	Place *placePick
}

func (s *server) renderSetup(w http.ResponseWriter, r *http.Request, status int, f setupForm, errKey string) {
	if f.Place == nil {
		f.Place = &placePick{}
	}
	s.render(w, r, status, "setup.html", map[string]any{
		"Form": f, "RegionGroups": regionGroups(), "Error": errKey, "ErrorField": setupErrorField(errKey, f), "Wizard": wizardAt(2),
		// The hint under the region says where the region comes from.
		"RegionTaken": f.Place.Chosen != nil && f.Region != "" && f.Region == f.Place.Chosen.RegionCode,
	})
}

// regionGroup is the states or cantons of one country, for the optgroups of
// the region select.
type regionGroup struct {
	Country string
	Regions []region.Region
}

// regionGroups returns every region grouped by country, in the order of the list.
func regionGroups() []regionGroup {
	var out []regionGroup
	for _, r := range region.All() {
		if n := len(out); n == 0 || out[n-1].Country != r.Country {
			out = append(out, regionGroup{Country: r.Country})
		}
		out[len(out)-1].Regions = append(out[len(out)-1].Regions, r)
	}
	return out
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
		Region:   r.PostFormValue("region"),
		Name:     strings.TrimSpace(r.PostFormValue("name")),
		Username: strings.TrimSpace(r.PostFormValue("username")),
		password: r.PostFormValue("password"),
		pin:      strings.TrimSpace(r.PostFormValue("pin")),
	}
	// The home is a place of the list on the box (ADR 0023): the page posts
	// its id and never coordinates.
	pl, status, errKey := s.setupPlace(r, &f)
	if status != 0 {
		s.renderSetup(w, r, status, f, errKey)
		return
	}
	if f.Region == "" {
		f.Region = pl.RegionCode
	}
	f.Place = chosenPicker(pl, s.i18n.T(s.langOf(r), "place.chosen"))
	passwordHash, pinHash, errKey := validateSetup(f)
	if errKey != "" {
		s.renderSetup(w, r, http.StatusUnprocessableEntity, f, errKey)
		return
	}
	if err := s.db.ResetUnfinishedSetup(); err != nil {
		s.fail(w, "reset setup", err)
		return
	}
	personID, err := s.createFirstParent(f, pl, passwordHash, pinHash, s.langOf(r))
	if err != nil {
		s.fail(w, "setup", err)
		return
	}
	if err := s.startSession(w, r, personID); err != nil {
		s.fail(w, "setup session", err)
		return
	}
	s.afterAccountCreated(w, r, f.Name)
}

// setupPlace reads the place of a setup post. It returns the place to create
// the household with, or a status other than 0 when the page is to be shown
// again: 200 for a pick or Change (nothing is created yet), 422 with the key
// of the error at the picker. f.Place and f.Region are set for the page.
func (s *server) setupPlace(r *http.Request, f *setupForm) (pl places.Place, status int, errKey string) {
	in, lang := readPickerInput(r), s.langOf(r)
	chosen := func(pl places.Place) { f.Place = chosenPicker(pl, s.i18n.T(lang, "place.chosen")) }
	switch {
	case in.Change: // open the field again; a region that came from the place goes with it
		text := in.Text
		if old, ok := s.places.Get(in.ID, lang); ok {
			text = old.Name
			if f.Region == old.RegionCode {
				f.Region = ""
			}
		}
		f.Place = s.openPicker(lang, text)
		return pl, http.StatusOK, ""
	case in.HasText && in.HasID: // a suggestion was picked
		pl, ok := s.places.Get(in.ID, lang)
		if !ok {
			f.Place = s.pickError(lang, in.Text)
			return pl, http.StatusUnprocessableEntity, "place.error.pick"
		}
		chosen(pl)
		if f.Region == "" {
			f.Region = pl.RegionCode
		}
		return pl, http.StatusOK, ""
	case in.HasText || !in.HasID: // text and no place, or nothing at all
		f.Place = s.openPickerError(lang, in.Text)
		key := "place.error.pick"
		switch {
		case in.Text == "":
			key = "setup.error.home"
		case len(f.Place.Options) == 0:
			key = "place.error.none"
		}
		return pl, http.StatusUnprocessableEntity, key
	}
	pl, ok := s.places.Get(in.ID, lang)
	if !ok {
		f.Place = s.pickError(lang, "")
		return pl, http.StatusUnprocessableEntity, "place.error.pick"
	}
	chosen(pl)
	return pl, 0, ""
}

func validateSetup(f setupForm) (passwordHash, pinHash, errKey string) {
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

func (s *server) createFirstParent(f setupForm, pl places.Place, passwordHash, pinHash, lang string) (string, error) {
	h, err := s.db.CreateHousehold(store.Household{Name: pl.Name, HomeName: pl.Name, HomePlaceID: pl.ID, Latitude: pl.Lat, Longitude: pl.Lon, Region: f.Region})
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
// one-time codes. A paired personal device's device session survives a reset but must not
// take the new setup over: that is for the parent, after their password.
func (s *server) setupAccount(r *http.Request) (store.Account, bool) {
	p, ok := s.pendingPerson(r)
	if !ok || s.personalDeviceSession(r) {
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
	// In the first setup the page is step 3 of the wizard, when accepting an
	// invitation its last step; after a reset of the one-time codes it is the
	// same shell without the progress.
	shell := wizardAt(0)
	if !s.db.Configured() {
		shell = wizardAt(3)
	} else if inv := s.inviteShellFor(acc); inv != nil {
		shell = inv
	}
	passkeys, _ := s.db.Passkeys(acc.PersonID)
	s.render(w, r, status, "setup_totp.html", map[string]any{
		"ResetNotice": s.resetNotice(r, acc), "HasPasskey": len(passkeys) > 0,
		"Secret": acc.TOTPSecret, "SecretGroups": groupsOfFour(acc.TOTPSecret), "Username": acc.Username,
		"URI": template.URL(uri), // built here, safe scheme
		"QR":  template.HTML(svg), "PersonalDevice": mobileBrowser(r), "InstallKey": authenticatorInstallKey(r), "Error": errKey, "ErrorField": totpErrorField(errKey), "Wizard": shell,
	})
}

func (s *server) handleSetupTOTPSubmit(w http.ResponseWriter, r *http.Request) {
	if s.reloadedRecoveryStep(w, r) {
		return
	}
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
	first := !s.db.Configured() // the first parent's setup, not a reset
	invited := s.inviteShellFor(acc)
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
	flow := flowAccount
	switch {
	case first:
		flow = flowWizard
		s.setFlash(w, r, "flash.setup_done:"+acc.PersonID) // shown on the home page after the codes
	case invited != nil:
		flow = flowInvite
		s.setFlash(w, r, "flash.invite_done:"+acc.PersonID)
	}
	s.showRecoveryCodes(w, r, acc.PersonID, recoveryPage{Flow: flow, Codes: codes})
}

// recoveryFlowOf says which of the three places an account's recovery codes
// belong to (for a page that is opened again after the one-time code step).
func (s *server) recoveryFlowOf(acc store.Account) string {
	switch {
	case s.inviteShellFor(acc) != nil:
		return flowInvite
	case acc.TOTPReset:
		return flowAccount
	}
	return flowWizard
}

// reloadedRecoveryStep handles the one-time code form posted again after it
// succeeded (a reload of the page that showed the codes). While the set was
// never confirmed as saved, a valid code makes a new set, because the old
// codes cannot be shown again; otherwise it goes on as before.
func (s *server) reloadedRecoveryStep(w http.ResponseWriter, r *http.Request) bool {
	acc, ok := s.setupAccount(r)
	if !ok || !acc.TOTPConfirmed || !s.unsavedSet(acc.PersonID) || acc.TOTPSecret == "" ||
		!auth.VerifyTOTP(acc.TOTPSecret, r.PostFormValue("code"), s.now()) {
		return false
	}
	codes, err := s.newRecoveryCodes(acc.PersonID)
	if err != nil {
		s.fail(w, "recovery codes", err)
		return true
	}
	s.showRecoveryCodes(w, r, acc.PersonID, recoveryPage{Flow: s.recoveryFlowOf(acc), Codes: codes, Reloaded: true})
	return true
}

// setupErrorField names the input of the setup form that an error belongs to.
func setupErrorField(errKey string, f setupForm) string {
	switch errKey {
	case "setup.error.home", "place.error.none", "place.error.pick":
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

// groupsOfFour writes a key in groups of four characters, as an authenticator
// app shows it and as it is easiest to type.
func groupsOfFour(key string) string {
	var b strings.Builder
	for i, c := range key {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// authenticatorInstallKey picks the text that says where to get an authenticator
// app: on an iPhone only the App Store, on an Android phone only Google Play,
// on a computer (or a tablet) both stores.
func authenticatorInstallKey(r *http.Request) string {
	ua := strings.ToLower(r.UserAgent())
	switch {
	case strings.Contains(ua, "iphone") || strings.Contains(ua, "ipod"):
		return "setup.totp.s1.install.ios"
	case strings.Contains(ua, "android") && strings.Contains(ua, "mobile"):
		return "setup.totp.s1.install.android"
	}
	return "setup.totp.s1.install.desktop"
}

// mobileBrowser reports whether the request comes from a personal device, where the
// authenticator app can be opened with a button instead of scanning a QR code.
func mobileBrowser(r *http.Request) bool {
	ua := strings.ToLower(r.UserAgent())
	return strings.Contains(ua, "iphone") || strings.Contains(ua, "ipod") || (strings.Contains(ua, "android") && strings.Contains(ua, "mobile"))
}

// createDefaultLists gives a new household the lists "Shopping" and "To-dos"
// (ADR 0015), named in the language of the setup.
func (s *server) createDefaultLists(householdID, lang string) error {
	for _, d := range []struct {
		key, color, symbol string
		suggestions        bool
	}{
		{"lists.default.shopping", "green", "square-filled", true},
		{"lists.default.todos", "blue", "circle-empty", false}, // a to-do list does not suggest groceries
	} {
		if _, err := s.db.CreateList(householdID, list.List{Name: s.i18n.T(lang, d.key), Color: d.color, Symbol: d.symbol, Sort: list.Manual, Suggestions: d.suggestions}); err != nil {
			return err
		}
	}
	return nil
}

// resetNotice says, on the page that sets up new one-time codes after a reset,
// who reset them and when, and with which passkey the parent is signed in, if
// they are (ADR 0018). Empty when the codes were not reset.
func (s *server) resetNotice(r *http.Request, acc store.Account) string {
	if !acc.TOTPReset || acc.TOTPConfirmed {
		return ""
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		return ""
	}
	lang := s.langOf(r)
	p := principal{household: h}
	t := acc.TOTPResetAt.In(p.location())
	when := []string{"date", datefmt.Date(h.DateFormat, t), "time", datefmt.Clock(h.TimeFormat, t)}
	text := s.i18n.Format(lang, i18n.Msg("setup.reset.cli", when...))
	if by, err := s.db.Person(h.ID, acc.TOTPResetBy); err == nil && acc.TOTPResetBy != "" {
		text = s.i18n.Format(lang, i18n.Msg("setup.reset.by", append(when, "name", by.Name)...))
	}
	if id := s.sessionPasskey(r); id != "" {
		if pk, err := s.db.Passkey(acc.PersonID, id); err == nil {
			text += " " + s.i18n.Format(lang, i18n.Msg("setup.reset.passkey", "passkey", pk.Name))
		}
	}
	return text + " " + s.i18n.T(lang, "setup.reset.next")
}
