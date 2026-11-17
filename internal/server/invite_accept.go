package server

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"unicode"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// The invited parent's side of an invitation (ticket 145): the link opens a
// welcome page, then "Your account" (username and password they choose
// themselves), then the one-time code and the recovery codes of the setup
// wizard (ticket 133) in the shell of the invitation.

// inviteWizard is the shell of the invitation: "Der Box vertrauen" (only when
// the box has its own CA), "Dein Konto", "Einmalcode". Step 0 shows no progress.
func inviteWizard(step int, trust bool) *wizardView {
	labels := []string{"setup.wizard.step.2", "setup.wizard.step.3"}
	if trust {
		labels = append([]string{"trust.title"}, labels...)
	}
	w := &wizardView{Step: step, Total: len(labels), CountKey: "invite.wizard.count", AriaKey: "invite.wizard.progress"}
	if step < 1 {
		return w
	}
	for i, l := range labels {
		st := wizardStep{N: i + 1, Label: l, State: "open"}
		switch {
		case i+1 < step:
			st.State = "done"
		case i+1 == step:
			st.State = "current"
		}
		w.Steps = append(w.Steps, st)
	}
	return w
}

// ownCA reports whether devices must trust the box's own certificate first.
func (s *server) ownCA() bool { return s.pki.Ready() && !s.publicInUse() }

// inviteShellFor is the shell of the one-time code steps for a person who
// accepted an invitation and has not set up the code yet, nil for anyone else
// (the first parent, or somebody whose codes were reset).
func (s *server) inviteShellFor(acc store.Account) *wizardView {
	if acc.TOTPReset {
		return nil
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		return nil
	}
	if inv, err := s.db.LatestInvitation(h.ID, acc.PersonID); err != nil || inv.AcceptedAt.IsZero() {
		return nil
	}
	trust := s.ownCA()
	if trust {
		return inviteWizard(3, true)
	}
	return inviteWizard(2, false)
}

// inviteAccountShell is the shell of the step "Your account" of an invitation.
func (s *server) inviteAccountShell() *wizardView {
	if s.ownCA() {
		return inviteWizard(2, true)
	}
	return inviteWizard(1, false)
}

// inviteView is what the pages of an invitation need to say.
type inviteView struct {
	Token      string
	Inv        store.Invitation
	Invitee    store.Person
	Chip       personChip
	Inviter    string // the name of who invited, or a phrase when that person is gone
	Household  store.Household
	Valid      string
	Possessive string
}

func clientKey(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	return "invite|" + ip
}

// deadPage renders a link that does not work. An unknown or cut-off link, and
// too many wrong tries, get the same page that names nobody and no date.
func (s *server) deadPage(w http.ResponseWriter, r *http.Request, status int, kind string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Kind"], data["Wizard"] = kind, inviteWizard(0, false)
	for _, k := range []string{"Date", "Inviter"} { // the texts of every kind take them
		if _, ok := data[k]; !ok {
			data[k] = ""
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, status, "invite_dead.html", data)
}

// deadInvite renders the page of a used, expired or withdrawn invitation and
// reports whether it did; an open invitation renders nothing.
func (s *server) deadInvite(w http.ResponseWriter, r *http.Request, v inviteView, inv store.Invitation) bool {
	h := v.Household
	switch inv.State(s.now()) {
	case store.InvitationUsed:
		s.deadPage(w, r, http.StatusGone, "used", map[string]any{"Inviter": v.Inviter, "Date": stampText(h, inv.AcceptedAt)})
	case store.InvitationExpired:
		s.deadPage(w, r, http.StatusGone, "expired", map[string]any{"Inviter": v.Inviter, "Date": v.Valid})
	case store.InvitationWithdrawn:
		s.deadPage(w, r, http.StatusGone, "withdrawn", map[string]any{"Inviter": v.Inviter, "Date": stampText(h, inv.WithdrawnAt)})
	default:
		return false
	}
	return true
}

// openInvite checks the link of the request. It answers with a page itself
// and returns false when the link does not work, or when somebody else is
// signed in on this device.
func (s *server) openInvite(w http.ResponseWriter, r *http.Request) (inviteView, bool) {
	if !s.db.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return inviteView{}, false
	}
	token := r.PathValue("token")
	key := clientKey(r)
	if s.opens.blocked(key) {
		s.deadPage(w, r, http.StatusTooManyRequests, "unknown", nil)
		return inviteView{}, false
	}
	if !auth.LooksLikeInvitationToken(token) {
		s.opens.fail(key)
		s.deadPage(w, r, http.StatusNotFound, "unknown", nil)
		return inviteView{}, false
	}
	inv, err := s.db.InvitationByHash(auth.HashInvitationToken(token))
	if errors.Is(err, store.ErrNotFound) {
		s.opens.fail(key)
		s.deadPage(w, r, http.StatusNotFound, "unknown", nil)
		return inviteView{}, false
	}
	if err != nil {
		s.fail(w, "invitation", err)
		return inviteView{}, false
	}
	h, err := s.db.Household(inv.HouseholdID)
	if err != nil {
		s.fail(w, "household", err)
		return inviteView{}, false
	}
	lang := s.langOf(r)
	v := inviteView{Token: token, Inv: inv, Household: h, Inviter: s.i18n.T(lang, "invite.inviter.fallback")}
	if by, err := s.db.Person(h.ID, inv.CreatedBy); err == nil {
		v.Inviter = by.Name
	}
	if v.Invitee, err = s.db.Person(h.ID, inv.PersonID); err != nil {
		s.fail(w, "invitee", err)
		return inviteView{}, false
	}
	if v.Chip, err = s.chipOf(h.ID, inv.PersonID); err != nil {
		s.fail(w, "people", err)
		return inviteView{}, false
	}
	v.Valid, v.Possessive = s.validUntil(lang, h, inv.ExpiresAt), possessive(lang, v.Invitee.Name)
	if s.deadInvite(w, r, v, inv) {
		return inviteView{}, false
	}
	if who, ok := s.currentPerson(r); ok { // somebody is signed in on this device
		chip, _ := s.chipOf(h.ID, who.ID)
		s.deadPage(w, r, http.StatusOK, "other", map[string]any{"Invitee": v.Invitee, "InviteeChip": v.Chip, "Who": chip, "Token": token, "ChildPhone": s.childPhoneSession(r, who)})
		return inviteView{}, false
	}
	return v, true
}

func (s *server) handleInviteWelcome(w http.ResponseWriter, r *http.Request) {
	v, ok := s.openInvite(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, "invite_welcome.html", map[string]any{"V": v, "Wizard": inviteWizard(0, false)})
}

type inviteAccountForm struct{ Username, errKey, errField, errText string }

func (s *server) renderInviteAccount(w http.ResponseWriter, r *http.Request, status int, v inviteView, f inviteAccountForm) {
	step := 1
	if s.ownCA() {
		step = 2
	}
	hint := "invite.field.password.hint"
	if status != http.StatusOK {
		hint = "invite.field.password.hint.again" // the password is never sent back
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, status, "invite_account.html", map[string]any{
		"V": v, "Wizard": inviteWizard(step, s.ownCA()), "Form": f, "Error": f.errKey, "ErrorField": f.errField, "ErrorText": f.errText, "PasswordHint": hint,
	})
}

func (s *server) handleInviteAccountForm(w http.ResponseWriter, r *http.Request) {
	v, ok := s.openInvite(w, r)
	if !ok {
		return
	}
	s.renderInviteAccount(w, r, http.StatusOK, v, inviteAccountForm{Username: suggestUsername(v.Invitee.Name)})
}

// suggestUsername is a username made from a name: lower-case letters and
// digits, umlauts written out.
func suggestUsername(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r == 'ä':
			b.WriteString("ae")
		case r == 'ö':
			b.WriteString("oe")
		case r == 'ü':
			b.WriteString("ue")
		case r == 'ß':
			b.WriteString("ss")
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *server) handleInviteAccountSubmit(w http.ResponseWriter, r *http.Request) {
	v, ok := s.openInvite(w, r)
	if !ok {
		return
	}
	lang := s.langOf(r)
	key := clientKey(r)
	if s.opens.blocked(key) {
		s.deadPage(w, r, http.StatusTooManyRequests, "unknown", nil)
		return
	}
	s.opens.fail(key) // every try counts: hashing a password is slow on purpose
	f := inviteAccountForm{Username: strings.TrimSpace(r.PostFormValue("username"))}
	refuse := func(errKey, field string) {
		f.errKey, f.errField = errKey, field
		s.renderInviteAccount(w, r, http.StatusUnprocessableEntity, v, f)
	}
	if f.Username == "" {
		refuse("invite.error.username", "username")
		return
	}
	hash, err := auth.HashPassword(r.PostFormValue("password"))
	if err != nil {
		refuse("invite.error.password", "password")
		return
	}
	inv, err := s.db.AcceptInvitation(auth.HashInvitationToken(v.Token), s.now(), store.Account{Username: f.Username, PasswordHash: hash, TOTPSecret: auth.NewTOTPSecret()})
	var fieldErr *store.FieldError
	switch {
	case errors.As(err, &fieldErr):
		f.errText = s.i18n.Format(lang, i18n.Msg("invite.error.taken", "suggestion", suggestUsername(v.Invitee.Name)))
		refuse("invite.error.taken", "username")
		return
	case errors.Is(err, store.ErrInvitationNotOpen):
		// it ended between opening the form and sending it
		if !s.deadInvite(w, r, v, inv) {
			s.deadPage(w, r, http.StatusGone, "unknown", nil)
		}
		return
	case errors.Is(err, store.ErrNotFound):
		s.deadPage(w, r, http.StatusNotFound, "unknown", nil)
		return
	case err != nil:
		s.fail(w, "accept invitation", err)
		return
	}
	if err := s.startSession(w, r, inv.PersonID); err != nil {
		s.fail(w, "invitation session", err)
		return
	}
	// The language chosen before the sign-in becomes the account's (ticket 134).
	if err := s.db.SetAccountLanguage(inv.PersonID, lang); err != nil {
		s.logger.Error("save account language", "err", err)
	}
	s.logger.Info("invitation accepted", "account", f.Username, "person", v.Invitee.Name, "invited by", v.Inviter)
	s.afterAccountCreated(w, r, v.Invitee.Name)
}

// handleInviteShort is the short plain-HTTP address of the QR code. Over HTTP
// it shows the trust page of a box with its own CA, then leads on to the
// invitation; with a public certificate there is nothing to trust, so it
// leads straight to the invitation.
func (s *server) handleInviteShort(w http.ResponseWriter, r *http.Request) {
	v, ok := s.openInvite(w, r)
	if !ok {
		return
	}
	next := s.httpsOrigin(r) + "/invite/" + v.Token
	if !s.ownCA() {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	s.renderTrust(w, r, trustOptions{Continue: next, ContinueKey: "invite.trust.continue", Wizard: inviteWizard(1, true), Invite: &v})
}
