package server

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/qrsvg"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Inviting a second parent (ticket 145, ADR 0006, 0010, 0017). A parent creates
// a link for an adult who has no account; the invited person opens it on their
// own device and chooses username and password themselves.

// Where a person stands with their sign-in.
const (
	signInNone    = "none"    // no account, no open invitation (also after withdrawing)
	signInOpen    = "open"    // an invitation that works
	signInExpired = "expired" // the last invitation ran out
	signInAccount = "account" // the person has an account
)

// signIn is the sign-in state of an adult.
type signIn struct {
	Kind    string
	Invite  store.Invitation // the latest invitation, if any
	HasInv  bool
	Account store.Account // set for signInAccount
	// OTPSet is false for an account whose one-time code is not set up (yet or after a reset).
	OTPSet bool
}

func (s *server) signInOf(householdID string, person store.Person) (signIn, error) {
	var st signIn
	inv, err := s.db.LatestInvitation(householdID, person.ID)
	switch {
	case err == nil:
		st.Invite, st.HasInv = inv, true
	case !errors.Is(err, store.ErrNotFound):
		return st, err
	}
	acc, err := s.db.AccountByPerson(person.ID)
	switch {
	case err == nil:
		st.Kind, st.Account, st.OTPSet = signInAccount, acc, acc.TOTPConfirmed
		return st, nil
	case !errors.Is(err, store.ErrNotFound):
		return st, err
	}
	st.Kind = signInNone
	if st.HasInv {
		switch st.Invite.State(s.now()) {
		case store.InvitationOpen:
			st.Kind = signInOpen
		case store.InvitationExpired:
			st.Kind = signInExpired
		}
	}
	return st, nil
}

// possessive writes a name's genitive in the page language: "Papas", "Hans'",
// "Papa's", "Max's".
func possessive(lang, name string) string {
	if lang == "de" {
		if strings.HasSuffix(name, "s") || strings.HasSuffix(name, "ß") || strings.HasSuffix(name, "x") || strings.HasSuffix(name, "z") {
			return name + "'"
		}
		return name + "s"
	}
	return name + "'s"
}

// validUntil is "Saturday, 21.11.2026, 13:00": a date in words with its weekday.
func (s *server) validUntil(lang string, h store.Household, t time.Time) string {
	p := principal{household: h}
	t = t.In(p.location())
	return s.weekdayName(lang, t) + ", " + datefmt.Date(h.DateFormat, t) + ", " + datefmt.Clock(h.TimeFormat, t)
}

// stampText is a date with its time, "14.11.2026, 12:00".
func stampText(h store.Household, t time.Time) string {
	t = t.In(principal{household: h}.location())
	return datefmt.Date(h.DateFormat, t) + ", " + datefmt.Clock(h.TimeFormat, t)
}

// inviteURL is the address that goes into the QR code and the link. With the
// box's own certificate authority the phone cannot reach the HTTPS address
// before it trusts the box, so a short plain-HTTP address leads to the trust
// page first (like phone pairing, ADR 0014, 0015).
func (s *server) inviteURL(r *http.Request, token string) string {
	if s.pki.Ready() && !s.publicInUse() {
		host := hostOf(r)
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		return (&url.URL{Scheme: "http", Host: host, Path: "/i/" + token}).String()
	}
	return s.httpsOrigin(r) + "/invite/" + token
}

// newInvitation stores a fresh link for the person and returns its address.
func (s *server) newInvitation(r *http.Request, p principal, person store.Person) (store.Invitation, string, error) {
	token := auth.NewInvitationToken()
	inv, err := s.db.CreateInvitation(p.household.ID, person.ID, p.person.ID, auth.HashInvitationToken(token), s.now())
	if err != nil {
		return store.Invitation{}, "", err
	}
	s.logger.Info("invitation created", "person", person.Name, "by", p.person.Name, "expires", inv.ExpiresAt.Format(time.RFC3339))
	return inv, s.inviteURL(r, token), nil
}

// invitee loads the person of the path for an invitation page: an adult of the
// household. A child, or somebody unknown, is a 404 (ADR 0010).
func (s *server) invitee(w http.ResponseWriter, r *http.Request, p principal) (store.Person, bool) {
	person, err := s.db.Person(p.household.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && person.Role != store.RoleParent) {
		http.NotFound(w, r)
		return store.Person{}, false
	}
	if err != nil {
		s.fail(w, "person", err)
		return store.Person{}, false
	}
	return person, true
}

// factsOf builds the label and value pairs of the invitation page.
type invitePage struct {
	Subject store.Person
	Chip    personChip
	Kind    string
	Facts   []factView
	// Link, QR and Valid are set only on the page that shows a new link.
	Link, Notice string
	QR           template.HTML
	Valid        string
	ResetName    string
}

func (s *server) handleInvitationPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	person, ok := s.invitee(w, r, p)
	if !ok {
		return
	}
	if person.ID == p.person.ID {
		http.Redirect(w, r, "/otp", http.StatusSeeOther)
		return
	}
	s.renderInvitation(w, r, p, http.StatusOK, person, nil)
}

func (s *server) renderInvitation(w http.ResponseWriter, r *http.Request, p principal, status int, person store.Person, fresh *invitePage) {
	lang := s.langOf(r)
	st, err := s.signInOf(p.household.ID, person)
	if err != nil {
		s.fail(w, "sign-in state", err)
		return
	}
	chip, err := s.chipOf(p.household.ID, person.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	f := func(key string, kv ...string) string { return s.i18n.Format(lang, i18n.Msg(key, kv...)) }
	name := person.Name
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Subject": person, "Chip": chip, "Kind": st.Kind,
		"Possessive": possessive(lang, name), "SuccessInline": true,
	}
	if fresh != nil {
		data["Kind"], data["Link"], data["QR"], data["Valid"], data["Notice"] = "new_link", fresh.Link, fresh.QR, fresh.Valid, fresh.Notice
		w.Header().Set("Cache-Control", "no-store")
		data["SuccessText"] = f("invite.created", "name", name)
		s.render(w, r, status, "invitation.html", data)
		return
	}
	who := func(id string) string {
		if id == p.person.ID {
			return ""
		}
		if by, err := s.db.Person(p.household.ID, id); err == nil {
			return by.Name
		}
		return ""
	}
	flag := func(kind, icon, key string) *flagView {
		return &flagView{Kind: kind, Text: s.i18n.T(lang, key), Icon: icon}
	}
	byYou := func(created time.Time, id string) string {
		if by := who(id); by != "" {
			return f("invite.created.by", "date", stampText(p.household, created), "name", by)
		}
		return f("invite.created.you", "date", stampText(p.household, created))
	}
	var facts []factView
	switch st.Kind {
	case signInOpen:
		facts = []factView{
			{Label: s.i18n.T(lang, "invite.fact.state"), Flag: flag("open", "clock", "invite.state.open"), Value: f("invite.open.text", "name", name)},
			{Label: s.i18n.T(lang, "invite.fact.until"), Value: f("invite.until.once", "date", s.validUntil(lang, p.household, st.Invite.ExpiresAt))},
			{Label: s.i18n.T(lang, "invite.fact.created"), Value: byYou(st.Invite.CreatedAt, st.Invite.CreatedBy)},
			{Label: s.i18n.T(lang, "invite.fact.link"), Value: s.i18n.T(lang, "invite.link.hidden")},
		}
	case signInExpired:
		facts = []factView{
			{Label: s.i18n.T(lang, "invite.fact.state"), Flag: flag("warn", "triangle", "invite.state.expired"), Value: f("invite.expired.text", "date", s.validUntil(lang, p.household, st.Invite.ExpiresAt))},
			{Label: s.i18n.T(lang, "invite.fact.created"), Value: byYou(st.Invite.CreatedAt, st.Invite.CreatedBy)},
		}
	case signInAccount:
		facts = s.accountFacts(lang, p, person, st, f)
	}
	data["Facts"], data["CanReset"] = facts, st.Kind == signInAccount && st.OTPSet
	s.render(w, r, status, "invitation.html", data)
}

// accountFacts are the facts of a person who has an account: the state, the
// username, when and through whose invitation it was made, the one-time code.
func (s *server) accountFacts(lang string, p principal, person store.Person, st signIn, f func(string, ...string) string) []factView {
	T := func(key string) string { return s.i18n.T(lang, key) }
	var facts []factView
	if st.OTPSet {
		facts = append(facts, factView{Label: T("invite.fact.state"), Value: T("invite.state.can_sign_in")})
	} else {
		facts = append(facts, factView{Label: T("invite.fact.state"), Flag: &flagView{Kind: "warn", Text: T("invite.state.otp_missing"), Icon: "triangle"}, Value: f("invite.otp_missing.text", "name", person.Name)})
	}
	facts = append(facts, factView{Label: T("invite.fact.username"), Value: st.Account.Username})
	if st.HasInv && !st.Invite.AcceptedAt.IsZero() {
		when := stampText(p.household, st.Invite.AcceptedAt)
		if by := st.Invite.CreatedBy; by == p.person.ID {
			facts = append(facts, factView{Label: T("invite.fact.account"), Value: f("invite.account.by_you", "date", when)})
		} else if inviter, err := s.db.Person(p.household.ID, by); err == nil {
			facts = append(facts, factView{Label: T("invite.fact.account"), Value: f("invite.account.by", "date", when, "name", possessive(lang, inviter.Name))})
		} else {
			facts = append(facts, factView{Label: T("invite.fact.account"), Value: when})
		}
	}
	if st.OTPSet {
		n := 0
		if codes, err := s.db.UnusedRecoveryCodes(person.ID); err == nil {
			n = len(codes)
		}
		facts = append(facts, factView{Label: T("invite.fact.otp"), Value: f("invite.otp.set", "n", strconv.Itoa(n))})
	}
	return facts
}

func (s *server) chipOf(householdID, personID string) (personChip, error) {
	people, err := s.db.People(householdID)
	if err != nil {
		return personChip{}, err
	}
	for _, c := range chipsOf(people) {
		if c.ID == personID {
			return c, nil
		}
	}
	return personChip{}, nil
}

// handleInvitationCreate makes a new link, which replaces an open one, and
// shows it: the only time it is visible.
func (s *server) handleInvitationCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	person, ok := s.invitee(w, r, p)
	if !ok {
		return
	}
	if person.ID == p.person.ID {
		http.Redirect(w, r, "/otp", http.StatusSeeOther)
		return
	}
	inv, link, err := s.newInvitation(r, p, person)
	switch {
	case errors.Is(err, store.ErrConflict): // the person has an account now
		http.Redirect(w, r, "/people/"+person.ID+"/invitation", http.StatusSeeOther)
		return
	case err != nil:
		s.fail(w, "create invitation", err)
		return
	}
	lang := s.langOf(r)
	svg, err := qrsvg.SVG(link, s.i18n.Format(lang, i18n.Msg("invite.qr.label", "name", person.Name)))
	if err != nil {
		s.fail(w, "qr code", err)
		return
	}
	page := &invitePage{Link: link, QR: template.HTML(svg), Valid: s.validUntil(lang, p.household, inv.ExpiresAt)} // #nosec G203 -- own QR encoder
	if s.pki.Ready() && !s.publicInUse() {
		page.Notice = "invite.notice.ca"
	}
	s.renderInvitation(w, r, p, http.StatusOK, person, page)
}

func (s *server) handleInvitationWithdraw(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	person, ok := s.invitee(w, r, p)
	if !ok {
		return
	}
	switch err := s.db.WithdrawInvitation(p.household.ID, person.ID, s.now()); {
	case err == nil:
		s.logger.Info("invitation withdrawn", "person", person.Name, "by", p.person.Name)
		s.redirectSaved(w, r, "/people/"+person.ID+"/invitation", "flash.invitation_withdrawn")
	case errors.Is(err, store.ErrNotFound):
		http.Redirect(w, r, "/people/"+person.ID+"/invitation", http.StatusSeeOther)
	default:
		s.fail(w, "withdraw invitation", err)
	}
}

// --- REST API ---

type invitationJSON struct {
	State       string  `json:"state"`
	CreatedAt   string  `json:"created_at"`
	ExpiresAt   string  `json:"expires_at"`
	CreatedBy   *string `json:"created_by"`
	AcceptedAt  *string `json:"accepted_at"`
	WithdrawnAt *string `json:"withdrawn_at"`
	URL         string  `json:"url,omitempty"`
}

func (s *server) invitationJSON(p principal, inv store.Invitation) invitationJSON {
	loc := p.location()
	out := invitationJSON{State: string(inv.State(s.now())), CreatedAt: rfc3339(inv.CreatedAt, loc), ExpiresAt: rfc3339(inv.ExpiresAt, loc)}
	if inv.CreatedBy != "" {
		out.CreatedBy = &inv.CreatedBy
	}
	if !inv.AcceptedAt.IsZero() {
		at := rfc3339(inv.AcceptedAt, loc)
		out.AcceptedAt = &at
	}
	if !inv.WithdrawnAt.IsZero() {
		at := rfc3339(inv.WithdrawnAt, loc)
		out.WithdrawnAt = &at
	}
	return out
}

// apiInvitee loads the adult of the path: a child is refused with 422, an unknown person with 404.
func (s *server) apiInvitee(w http.ResponseWriter, r *http.Request, p principal) (store.Person, bool) {
	person, err := s.db.Person(p.household.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "person not found")
		return store.Person{}, false
	case err != nil:
		s.fail(w, "person", err)
		return store.Person{}, false
	case person.Role != store.RoleParent:
		writeError(w, http.StatusUnprocessableEntity, "only adults can be invited")
		return store.Person{}, false
	}
	return person, true
}

func (s *server) apiGetInvitation(w http.ResponseWriter, r *http.Request, p principal) {
	person, ok := s.apiInvitee(w, r, p)
	if !ok {
		return
	}
	inv, err := s.db.LatestInvitation(p.household.ID, person.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no invitation")
		return
	}
	if err != nil {
		s.fail(w, "invitation", err)
		return
	}
	writeJSON(w, http.StatusOK, s.invitationJSON(p, inv))
}

func (s *server) apiCreateInvitation(w http.ResponseWriter, r *http.Request, p principal) {
	person, ok := s.apiInvitee(w, r, p)
	if !ok {
		return
	}
	inv, link, err := s.newInvitation(r, p, person)
	switch {
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "this person has an account already")
	case err != nil:
		s.fail(w, "create invitation", err)
	default:
		out := s.invitationJSON(p, inv)
		out.URL = link
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, out)
	}
}

func (s *server) apiWithdrawInvitation(w http.ResponseWriter, r *http.Request, p principal) {
	person, ok := s.apiInvitee(w, r, p)
	if !ok {
		return
	}
	switch err := s.db.WithdrawInvitation(p.household.ID, person.ID, s.now()); {
	case err == nil:
		s.logger.Info("invitation withdrawn", "person", person.Name, "by", p.person.Name, "via", "API")
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no open invitation")
	default:
		s.fail(w, "withdraw invitation", err)
	}
}

// signInRow is the calm row "Sign-in" on the page of an adult: the state in
// words, leading to the invitation page, or for yourself to your account.
func (s *server) signInRow(r *http.Request, p principal, person store.Person) (rowView, error) {
	lang := s.langOf(r)
	f := func(key string) string { return s.i18n.Format(lang, i18n.Msg(key, "name", person.Name)) }
	if person.ID == p.person.ID {
		acc, err := s.db.AccountByPerson(person.ID)
		if err != nil {
			return rowView{}, err
		}
		return rowView{Href: "/otp", Title: s.i18n.T(lang, "people.signin.self"), Lines: []rowLine{{Text: s.i18n.Format(lang, i18n.Msg("people.signin.self.text", "username", acc.Username))}}}, nil
	}
	st, err := s.signInOf(p.household.ID, person)
	if err != nil {
		return rowView{}, err
	}
	row := rowView{Href: "/people/" + person.ID + "/invitation"}
	switch st.Kind {
	case signInOpen:
		row.Title = s.i18n.T(lang, "people.acc.open")
		row.Lines = []rowLine{{Text: s.i18n.Format(lang, i18n.Msg("people.signin.open.text", "date", s.validUntil(lang, p.household, st.Invite.ExpiresAt)))}}
	case signInExpired:
		row.Title, row.Lines = s.i18n.T(lang, "people.acc.expired"), []rowLine{{Text: f("people.signin.expired.text")}}
	case signInAccount:
		row.Title = s.i18n.T(lang, "people.acc.can")
		if !st.OTPSet {
			row.Title = s.i18n.T(lang, "people.acc.otp_missing")
		}
		row.Lines = []rowLine{{Text: s.i18n.Format(lang, i18n.Msg("people.signin.account.text", "username", st.Account.Username))}}
	default:
		row.Title, row.Lines = s.i18n.T(lang, "people.acc.none"), []rowLine{{Text: f("people.signin.none.text")}}
	}
	return row, nil
}
