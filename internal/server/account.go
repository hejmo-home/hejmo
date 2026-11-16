package server

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// "Mein Konto" (ticket 95, ADR 0017 and 0018): the parent's own sign-in. It
// holds the passkeys, the one-time code with its recovery codes, and the reset
// of another parent's one-time codes, which has a page of its own per parent.

// whenWords says when something happened: "today, 21:05", "yesterday, 08:14",
// else the date alone (short) or the date with the time (long).
func (s *server) whenWords(lang string, p principal, t time.Time, long bool) string {
	loc := p.location()
	t, now := t.In(loc), s.now().In(loc)
	df, tf := p.household.DateFormat, p.household.TimeFormat
	switch {
	case sameDay(t, now):
		return s.i18n.T(lang, "sessions.today") + ", " + datefmt.Clock(tf, t)
	case sameDay(t, now.AddDate(0, 0, -1)):
		return s.i18n.T(lang, "sessions.yesterday") + ", " + datefmt.Clock(tf, t)
	case long:
		return datefmt.Date(df, t) + ", " + datefmt.Clock(tf, t)
	}
	return datefmt.Date(df, t)
}

type passkeyRowView struct {
	ID, Name, Meta string
	// Flag is a state in words under the row: "open" (calm) or "warn".
	Flag, FlagKind string
}

// passkeyRows describes passkeys as calm rows. current is the passkey the
// request was signed in with.
func (s *server) passkeyRows(lang string, p principal, list []store.Passkey, current string) []passkeyRowView {
	out := make([]passkeyRowView, 0, len(list))
	for _, pk := range list {
		meta := s.i18n.Format(lang, i18n.Msg("passkey.meta.created", "when", s.whenWords(lang, p, pk.Created, false)))
		if pk.LastUsed.IsZero() {
			meta += " · " + s.i18n.T(lang, "passkey.meta.unused")
		} else {
			meta += " · " + s.i18n.Format(lang, i18n.Msg("passkey.meta.used", "when", s.whenWords(lang, p, pk.LastUsed, false)))
		}
		row := passkeyRowView{ID: pk.ID, Name: pk.Name, Meta: meta}
		switch {
		case !s.passkeyWorks(pk):
			row.Flag, row.FlagKind = s.i18n.Format(lang, i18n.Msg("passkey.flag.broken", "name", pk.RPID)), "warn"
		case pk.ID == current:
			row.Flag, row.FlagKind = s.i18n.T(lang, "passkey.flag.current"), "open"
		}
		out = append(out, row)
	}
	return out
}

// thisDevice is "this Mac" as the button of the account page says it.
func (s *server) thisDevice(lang string, r *http.Request) string {
	d, _ := auth.DeviceFromUserAgent(r.UserAgent())
	if key := "passkey.this." + d; d != auth.Unknown && s.i18n.T(lang, key) != key {
		return s.i18n.T(lang, key)
	}
	return s.i18n.T(lang, "passkey.this.unknown")
}

type otherParent struct {
	ID, Name, Meta string
	Chip           personChip
}

func (s *server) otherParents(lang string, p principal) ([]otherParent, error) {
	people, err := s.db.People(p.household.ID)
	if err != nil {
		return nil, err
	}
	chips := chipsOf(people)
	var out []otherParent
	for i, x := range people {
		if x.ID == p.person.ID || x.Role != store.RoleParent {
			continue
		}
		acc, err := s.db.AccountByPerson(x.ID)
		if err != nil {
			continue
		}
		meta := s.i18n.Format(lang, i18n.Msg("account.others.row", "name", x.Name))
		if !acc.TOTPConfirmed && acc.TOTPReset {
			meta = s.i18n.Format(lang, i18n.Msg("account.others.reset", "when", s.whenWords(lang, p, acc.TOTPResetAt, true), "name", x.Name))
		}
		out = append(out, otherParent{ID: x.ID, Name: x.Name, Meta: meta, Chip: chips[i]})
	}
	return out, nil
}

// sessionPasskey is the passkey the request's session was made with, or "".
func (s *server) sessionPasskey(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	id, _ := s.db.SessionPasskey(auth.HashToken(c.Value))
	return id
}

func (s *server) handleAccountPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderAccount(w, r, p, http.StatusOK)
}

func (s *server) renderAccount(w http.ResponseWriter, r *http.Request, p principal, status int) {
	lang := s.langOf(r)
	acc, err := s.db.AccountByPerson(p.person.ID)
	if err != nil {
		s.fail(w, "account", err)
		return
	}
	list, err := s.db.Passkeys(p.person.ID)
	if err != nil {
		s.fail(w, "passkeys", err)
		return
	}
	unused, err := s.db.UnusedRecoveryCodes(p.person.ID)
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	others, err := s.otherParents(lang, p)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	var broken []string // the passkeys that stopped working with a new box name
	var oldName string
	for _, pk := range list {
		if !s.passkeyWorks(pk) {
			broken = append(broken, pk.Name)
			oldName = pk.RPID
		}
	}
	brokenKey := "account.passkeys.renamed.one"
	if len(broken) > 1 {
		brokenKey = "account.passkeys.renamed.many"
	}
	s.render(w, r, status, "account.html", map[string]any{
		"BrokenKey": brokenKey,
		"Person":    p.person, "Username": acc.Username, "SyncStale": s.syncStale(p.household.ID),
		"Passkeys": s.passkeyRows(lang, p, list, s.sessionPasskey(r)), "AtBoxName": s.atBoxName(r),
		"BoxName": s.boxName(), "BoxURL": s.boxURL("/account"), "HostHere": hostOf(r), "ThisDevice": s.thisDevice(lang, r),
		"Broken": s.quotedNames(lang, broken), "OldName": oldName, "BrokenCount": len(broken),
		"CodesSince": datefmt.Date(p.household.DateFormat, acc.TOTPConfirmedAt.In(p.location())),
		"LeftText":   s.i18n.Format(lang, i18n.Msg("account.codes.left", "count", strconv.Itoa(len(unused)), "total", strconv.Itoa(recoveryCodeCount))),
		"Others":     others, "SuccessInline": true,
	})
}

// quotedNames writes names in quotes in a row: “A”, “B” and “C”.
func (s *server) quotedNames(lang string, names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = s.i18n.Format(lang, i18n.Msg("passkey.quoted", "name", n))
	}
	switch len(q) {
	case 0:
		return ""
	case 1:
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " " + s.i18n.T(lang, "passkey.and") + " " + q[len(q)-1]
}

func (s *server) handleRecoveryCodesNew(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.issueRecoveryCodes(w, r, p.person.ID)
	}
}

// handleOTPMoved sends an old address of "One-time codes" to "Mein Konto".
func (s *server) handleOTPMoved(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/account", http.StatusMovedPermanently)
}

// passkeyPage finds the passkey of the request's person, or answers 404.
func (s *server) passkeyPage(w http.ResponseWriter, r *http.Request) (principal, store.Passkey, bool) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return principal{}, store.Passkey{}, false
	}
	pk, err := s.db.Passkey(p.person.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return principal{}, store.Passkey{}, false
	}
	if err != nil {
		s.fail(w, "passkey", err)
		return principal{}, store.Passkey{}, false
	}
	return p, pk, true
}

func (s *server) handlePasskeyPage(w http.ResponseWriter, r *http.Request) {
	if p, pk, ok := s.passkeyPage(w, r); ok {
		s.renderPasskey(w, r, p, pk, http.StatusOK, "", pk.Name)
	}
}

func (s *server) renderPasskey(w http.ResponseWriter, r *http.Request, p principal, pk store.Passkey, status int, errKey, name string) {
	lang := s.langOf(r)
	others, err := s.db.Passkeys(p.person.ID)
	if err != nil {
		s.fail(w, "passkeys", err)
		return
	}
	var stays []string
	for _, o := range others {
		if o.ID != pk.ID {
			stays = append(stays, o.Name)
		}
	}
	state := s.i18n.T(lang, "passkey.state.works")
	if !s.passkeyWorks(pk) {
		state = s.i18n.Format(lang, i18n.Msg("passkey.state.broken", "name", pk.RPID))
	}
	if s.sessionPasskey(r) == pk.ID {
		state += " " + s.i18n.T(lang, "passkey.state.current")
	}
	created := s.whenWords(lang, p, pk.Created, true)
	if pk.Device != "" && pk.Device != auth.Unknown {
		created = s.i18n.Format(lang, i18n.Msg("passkey.created.on", "when", created, "device", s.i18n.T(lang, "device."+pk.Device)))
	}
	used := s.i18n.T(lang, "passkey.meta.unused")
	if !pk.LastUsed.IsZero() {
		used = s.whenWords(lang, p, pk.LastUsed, true)
	}
	onDevice := s.i18n.T(lang, "passkey.remove.on.device")
	if pk.Device != "" && pk.Device != auth.Unknown {
		onDevice = s.i18n.Format(lang, i18n.Msg("passkey.remove.on", "device", s.i18n.T(lang, "device."+pk.Device)))
	}
	s.render(w, r, status, "passkey.html", map[string]any{
		"Person": p.person, "Passkey": pk, "State": state, "Created": created, "Used": used,
		"Name": name, "Error": errKey, "ErrorField": map[bool]string{true: "name"}[errKey != ""],
		"Stays": s.quotedNames(lang, stays), "StaysKey": staysKey(len(stays)), "OnDevice": onDevice, "BoxName": pk.RPID,
		"SuccessInline": false,
	})
}

func staysKey(others int) string {
	switch others {
	case 0:
		return "passkey.remove.stays.none"
	case 1:
		return "passkey.remove.stays.one"
	}
	return "passkey.remove.stays.many"
}

func (s *server) handlePasskeyRename(w http.ResponseWriter, r *http.Request) {
	p, pk, ok := s.passkeyPage(w, r)
	if !ok {
		return
	}
	name := cleanPasskeyName(r.PostFormValue("name"))
	if name == "" {
		s.renderPasskey(w, r, p, pk, http.StatusUnprocessableEntity, "passkey.error.name", "")
		return
	}
	if err := s.db.RenamePasskey(p.person.ID, pk.ID, name); err != nil {
		s.fail(w, "rename passkey", err)
		return
	}
	s.redirectSavedNamed(w, r, "/account/passkeys/"+pk.ID, "flash.passkey_renamed", name)
}

func (s *server) handlePasskeyRemove(w http.ResponseWriter, r *http.Request) {
	p, pk, ok := s.passkeyPage(w, r)
	if !ok {
		return
	}
	if err := s.db.RemovePasskey(p.person.ID, pk.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, "remove passkey", err)
		return
	}
	s.logger.Info("passkey removed", "person", p.person.Name, "by", "the owner")
	s.redirectSavedNamed(w, r, "/account", "flash.passkey_removed", pk.Name)
}

// handlePasskeyOfferForm is the form of the offer without a script: nothing can
// be created, so it says so and offers to try again.
func (s *server) handlePasskeyOfferForm(w http.ResponseWriter, r *http.Request) {
	if acc, ok := s.setupAccount(r); ok && !acc.TOTPConfirmed {
		s.renderPasskeyOffer(w, r, acc, http.StatusUnprocessableEntity, true, r.PostFormValue("name"))
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

// ---- Reset of another parent's one-time codes ----

type resetOutcome int

const (
	resetDone resetOutcome = iota
	resetSelf
	resetUnknown
	resetWrongConfirmation
	resetLimited
)

// resetConfirmation is how the acting parent proves who they are: password and
// one-time (or recovery) code, or a passkey of their own (ADR 0018; then Passkey
// is set and decides).
type resetConfirmation struct {
	Password, Code string
	Passkey        func() bool
}

// resetResult is what a reset did.
type resetResult struct {
	Target  store.Person
	Removed []store.Passkey
}

// resetOtherOTP resets another parent's one-time codes after the acting parent
// confirmed (ADR 0017), and removes the passkeys the acting parent chose of the
// other parent (ADR 0018 review).
func (s *server) resetOtherOTP(actor store.Person, household, targetID string, confirm resetConfirmation, removePasskeys []string) (resetResult, resetOutcome, error) {
	if targetID == actor.ID {
		return resetResult{}, resetSelf, nil
	}
	target, err := s.db.Person(household, targetID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && target.Role != store.RoleParent) {
		return resetResult{}, resetUnknown, nil
	}
	if err != nil {
		return resetResult{}, 0, err
	}
	targetAcc, err := s.db.AccountByPerson(target.ID)
	if errors.Is(err, store.ErrNotFound) {
		return resetResult{}, resetUnknown, nil
	}
	if err != nil {
		return resetResult{}, 0, err
	}
	own, err := s.db.AccountByPerson(actor.ID)
	if err != nil {
		return resetResult{}, 0, err
	}
	key := strings.ToLower(own.Username)
	if s.limiter.blocked(key) {
		return resetResult{}, resetLimited, nil
	}
	var confirmed bool
	if confirm.Passkey != nil {
		confirmed = confirm.Passkey()
	} else {
		passwordOK := auth.VerifyPassword(own.PasswordHash, confirm.Password)
		secondOK := s.secondFactorOK(own, confirm.Code, passwordOK)
		confirmed = passwordOK && secondOK
	}
	if !confirmed {
		s.limiter.fail(key)
		return resetResult{}, resetWrongConfirmation, nil
	}
	s.limiter.reset(key)
	removed, err := s.db.ResetOTPBy(target.ID, actor.ID, removePasskeys)
	if err != nil {
		return resetResult{}, 0, err
	}
	how := "password and code"
	if confirm.Passkey != nil {
		how = "passkey"
	}
	s.logger.Info("one-time codes reset", "account", targetAcc.Username, "by", own.Username, "via", "web interface", "confirmed with", how, "passkeys removed", len(removed))
	return resetResult{Target: target, Removed: removed}, resetDone, nil
}

// resetTarget finds the other parent a reset page is about.
func (s *server) resetTarget(w http.ResponseWriter, r *http.Request) (principal, store.Person, store.Account, bool) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return principal{}, store.Person{}, store.Account{}, false
	}
	id := r.PathValue("id")
	if id == p.person.ID {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return principal{}, store.Person{}, store.Account{}, false
	}
	target, err := s.db.Person(p.household.ID, id)
	if err == nil && target.Role != store.RoleParent {
		err = store.ErrNotFound
	}
	var acc store.Account
	if err == nil {
		acc, err = s.db.AccountByPerson(target.ID)
	}
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return principal{}, store.Person{}, store.Account{}, false
	}
	if err != nil {
		s.fail(w, "reset target", err)
		return principal{}, store.Person{}, store.Account{}, false
	}
	return p, target, acc, true
}

type resetForm struct {
	Ticked  []string
	ErrKey  string // the text of the error
	ErrAt   string // where: "password" (the field) or "passkey" (above the button)
	Limited bool
}

func (s *server) renderReset(w http.ResponseWriter, r *http.Request, p principal, target store.Person, acc store.Account, status int, f resetForm) {
	lang := s.langOf(r)
	chips, err := s.chipOf(p.household.ID, target.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	list, err := s.db.Passkeys(target.ID)
	if err != nil {
		s.fail(w, "passkeys", err)
		return
	}
	type pickable struct {
		ID, Name, Used string
		Ticked         bool
	}
	var picks []pickable
	var lines []string
	for _, pk := range list {
		used := s.i18n.T(lang, "passkey.meta.unused")
		if !pk.LastUsed.IsZero() {
			used = s.i18n.Format(lang, i18n.Msg("passkey.meta.used", "when", s.whenWords(lang, p, pk.LastUsed, false)))
		}
		picks = append(picks, pickable{ID: pk.ID, Name: pk.Name, Used: used, Ticked: slices.Contains(f.Ticked, pk.ID)})
		lines = append(lines, pk.Name+", "+used)
	}
	unused, err := s.db.UnusedRecoveryCodes(target.ID)
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	codes := s.i18n.T(lang, "account.reset.code.none")
	if acc.TOTPConfirmed {
		key := "account.reset.code.set"
		if len(unused) == 1 {
			key += ".one"
		}
		codes = s.i18n.Format(lang, i18n.Msg(key, "date", datefmt.Date(p.household.DateFormat, acc.TOTPConfirmedAt.In(p.location())), "count", strconv.Itoa(len(unused))))
	}
	mine, _, _ := s.userOf(p.person.ID)
	field := ""
	if f.ErrAt == "password" {
		field = "password"
	}
	s.render(w, r, status, "account_reset.html", map[string]any{
		"Person": p.person, "Target": target, "TargetChip": chips, "Username": acc.Username, "CodesFact": codes,
		"Picks": picks, "PasskeyLines": lines, "ViaPasskey": s.atBoxName(r) && len(mine.Credentials) > 0,
		"Error": f.ErrKey, "ErrorField": field, "PasskeyError": f.ErrAt == "passkey",
		"Open": f.ErrKey != "", "BoxName": s.boxName(), "Possessive": possessive(lang, target.Name),
	})
}

func (s *server) handleResetPage(w http.ResponseWriter, r *http.Request) {
	if p, target, acc, ok := s.resetTarget(w, r); ok {
		s.renderReset(w, r, p, target, acc, http.StatusOK, resetForm{})
	}
}

func (s *server) handleResetSubmit(w http.ResponseWriter, r *http.Request) {
	p, target, acc, ok := s.resetTarget(w, r)
	if !ok {
		return
	}
	confirm := resetConfirmation{Password: r.PostFormValue("password"), Code: r.PostFormValue("code")}
	viaPasskey := r.PostFormValue("assertion") != "" || r.PostFormValue("ceremony") != ""
	if viaPasskey {
		ceremony, assertion := r.PostFormValue("ceremony"), r.PostFormValue("assertion")
		confirm.Passkey = func() bool { return s.confirmWithPasskey(r, p.person, ceremony, assertion) }
	}
	ticked := r.PostForm["remove_passkey"]
	res, outcome, err := s.resetOtherOTP(p.person, p.household.ID, target.ID, confirm, ticked)
	switch {
	case err != nil:
		s.fail(w, "reset one-time codes", err)
	case outcome == resetDone:
		s.redirectResetDone(w, r, res)
	case outcome == resetSelf:
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
	case outcome == resetUnknown:
		http.NotFound(w, r)
	case outcome == resetLimited:
		s.renderReset(w, r, p, target, acc, http.StatusTooManyRequests, resetForm{Ticked: ticked, ErrKey: "login.error.limit", ErrAt: "password"})
	case viaPasskey:
		s.renderReset(w, r, p, target, acc, http.StatusUnprocessableEntity, resetForm{Ticked: ticked, ErrKey: "account.reset.error.passkey", ErrAt: "passkey"})
	default:
		s.renderReset(w, r, p, target, acc, http.StatusUnprocessableEntity, resetForm{Ticked: ticked, ErrKey: "otp.error.confirm", ErrAt: "password"})
	}
}

// redirectResetDone leads back to Mein Konto with a success line that names
// whose codes were reset and which of their passkeys went with it.
func (s *server) redirectResetDone(w http.ResponseWriter, r *http.Request, res resetResult) {
	lang := s.langOf(r)
	if len(res.Removed) == 0 {
		s.redirectSavedFields(w, r, "/account", "flash.otp_reset", res.Target.Name)
		return
	}
	names := make([]string, len(res.Removed))
	for i, pk := range res.Removed {
		names[i] = pk.Name
	}
	key := "flash.otp_reset_passkey"
	if len(names) > 1 {
		key = "flash.otp_reset_passkeys"
	}
	s.redirectSavedFields(w, r, "/account", key, res.Target.Name, possessive(lang, res.Target.Name), s.quotedNames(lang, names))
}
