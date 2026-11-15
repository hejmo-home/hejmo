package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/qrsvg"
	"github.com/hejmo-home/hejmo/internal/store"
)

const (
	// pairingCookie holds a phone's own claim on a pairing code while it waits
	// for a parent's confirmation. The box keeps only its hash.
	pairingCookie = "hejmo_pairing"
	// phonePollSeconds is how often a waiting phone asks whether a parent
	// confirmed.
	phonePollSeconds = 3
)

type phoneView struct {
	ID, Name, Person, Paired, LastSeen string
	Expired                            bool
}

// waitingView is a phone that opened its code. The form fields and the error
// are filled only for the pairing a failed confirmation was about.
type waitingView struct{ ID, Device, Expires, Verification, Name, Person, Error, ErrorField string }

// confirmForm is the parent's entry on a waiting phone that was refused, shown
// again with its error. The zero value is "no entry".
type confirmForm struct{ PairingID, Name, Person, Error, ErrorField string }

type phonePersonView struct{ ID, Name string }

// pairingCode is a fresh pairing code and where its QR code leads.
type pairingCode struct {
	ID      string
	URL     string
	Expires time.Time
}

// newPairingCode stores a pairing code for the household. The code itself is
// only in the returned address: the box keeps its hash.
func (s *server) newPairingCode(r *http.Request, householdID string) (pairingCode, error) {
	code := auth.NewToken()
	pr, err := s.db.CreatePhonePairing(householdID, auth.HashToken(code), s.now())
	if err != nil {
		return pairingCode{}, err
	}
	return pairingCode{ID: pr.ID, URL: s.joinURL(r, code), Expires: pr.ExpiresAt}, nil
}

// joinURL is where a phone that scans the QR code goes. With the box's own CA
// the phone cannot reach the HTTPS address before it trusts the box, so the
// code points to the trust page first, over plain HTTP, which then leads on
// (ADR 0014, 0015).
func (s *server) joinURL(r *http.Request, code string) string {
	join := s.httpsOrigin(r) + "/phones/join/" + code
	if s.pki.Ready() && !s.publicInUse() {
		host := hostOf(r)
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		return (&url.URL{Scheme: "http", Host: host, Path: "/ca", RawQuery: url.Values{"continue": {join}}.Encode()}).String()
	}
	return join
}

// trustContinue is the address the trust page leads on to, if it is one of this
// box's own pairing addresses.
func (s *server) trustContinue(r *http.Request) string {
	raw := r.URL.Query().Get("continue")
	u, err := url.Parse(raw)
	origin, oerr := url.Parse(s.httpsOrigin(r))
	if err != nil || oerr != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, origin.Host) || !strings.HasPrefix(u.Path, "/phones/join/") {
		return ""
	}
	return u.String()
}

func (s *server) renderPhones(w http.ResponseWriter, r *http.Request, p principal, status int, code *pairingCode, form confirmForm) {
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return p.dateTime(t)
	}
	phones, err := s.db.Phones(p.household.ID)
	if err != nil {
		s.fail(w, "phones", err)
		return
	}
	waiting, err := s.db.PendingPhonePairings(p.household.ID, s.now())
	if err != nil {
		s.fail(w, "waiting phones", err)
		return
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	var phoneViews []phoneView
	for _, ph := range phones {
		expired := !ph.SessionEnds.IsZero() && !ph.SessionEnds.After(s.now())
		phoneViews = append(phoneViews, phoneView{ph.ID, ph.Name, ph.PersonName, at(ph.PairedAt), at(ph.LastSeen), expired})
	}
	var waitingViews []waitingView
	for _, wp := range waiting {
		v := waitingView{ID: wp.ID, Device: wp.Device, Expires: at(wp.ExpiresAt), Verification: verificationNumber(wp.ClaimHash)}
		if form.PairingID == wp.ID {
			v.Name, v.Person, v.Error, v.ErrorField = form.Name, form.Person, form.Error, form.ErrorField
		}
		waitingViews = append(waitingViews, v)
	}
	var peopleViews []phonePersonView
	for _, pe := range people {
		peopleViews = append(peopleViews, phonePersonView{pe.ID, pe.Name})
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID),
		"Phones": phoneViews, "Waiting": waitingViews, "People": peopleViews,
	}
	if code != nil {
		svg, err := qrsvg.SVG(code.URL, s.i18n.T(s.langOf(r), "phones.qr.label"))
		if err != nil {
			s.fail(w, "qr code", err)
			return
		}
		data["Code"] = map[string]any{"URL": code.URL, "QR": template.HTML(svg), "Expires": at(code.Expires)}
	}
	data["CAFirst"] = s.pki.Ready() && !s.publicInUse()
	s.render(w, r, status, "phones.html", data)
}

func (s *server) handlePhonesPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderPhones(w, r, p, http.StatusOK, nil, confirmForm{})
	}
}

func (s *server) handlePhonePairingCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	code, err := s.newPairingCode(r, p.household.ID)
	if err != nil {
		s.fail(w, "phone pairing", err)
		return
	}
	s.renderPhones(w, r, p, http.StatusOK, &code, confirmForm{})
}

func (s *server) handlePhonePairingConfirm(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	name, person := r.PostFormValue("name"), r.PostFormValue("person")
	form := func(key, field string) confirmForm {
		return confirmForm{PairingID: r.PathValue("id"), Name: name, Person: person, Error: key, ErrorField: field}
	}
	var fieldErr *store.FieldError
	switch _, err := s.db.ConfirmPhonePairing(p.household.ID, r.PathValue("id"), person, name, s.now()); {
	case err == nil:
		s.redirectSaved(w, r, "/phones", "flash.phone_paired")
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.As(err, &fieldErr):
		s.renderPhones(w, r, p, http.StatusUnprocessableEntity, nil, form("phones.error."+fieldErr.Field, fieldErr.Field))
	default:
		s.fail(w, "confirm phone", err)
	}
}

func (s *server) handlePhoneRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	switch err := s.db.RevokePhone(p.household.ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "revoke phone", err)
	default:
		s.redirectSaved(w, r, "/phones", "flash.phone_revoked")
	}
}

// handlePhoneJoin is the address in the QR code, opened by the phone. The
// phone waits here until a parent confirms, then gets its device session.
func (s *server) handlePhoneJoin(w http.ResponseWriter, r *http.Request) {
	if !s.db.Configured() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	codeHash := auth.HashToken(r.PathValue("code"))
	claim := ""
	if c, err := r.Cookie(pairingCookie); err == nil {
		claim = c.Value
	}
	if claim == "" {
		claim = auth.NewToken()
	}
	claimHash := auth.HashToken(claim)
	refuse := func() { s.render(w, r, http.StatusGone, "phones_join.html", map[string]any{"State": "refused"}) }
	if err := s.db.JoinPhonePairing(codeHash, claimHash, deviceLabel(r.UserAgent()), s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			refuse()
		} else {
			s.fail(w, "join phone pairing", err)
		}
		return
	}
	token := auth.NewToken()
	expires := s.now().Add(store.PhoneSessionLifetime)
	switch _, err := s.db.CompletePhonePairing(codeHash, claimHash, auth.HashToken(token), s.now(), expires); {
	case err == nil:
		http.SetCookie(w, &http.Cookie{Name: pairingCookie, Value: "", Path: "/phones/join/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: token, Path: "/", Expires: expires,
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case errors.Is(err, store.ErrPhonePending):
		http.SetCookie(w, &http.Cookie{
			Name: pairingCookie, Value: claim, Path: "/phones/join/", MaxAge: int(store.PairingCodeLifetime.Seconds()),
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		})
		s.render(w, r, http.StatusOK, "phones_join.html", map[string]any{"State": "waiting", "Poll": phonePollSeconds, "Verification": verificationNumber(claimHash)})
	case errors.Is(err, store.ErrNotFound):
		refuse()
	default:
		s.fail(w, "complete phone pairing", err)
	}
}

// verificationNumber is a six-digit number both the waiting phone and the
// parent's phones screen show, taken from the hash of the phone's secret. The
// pairing address can travel over plain HTTP (the trust page of the box's own
// CA), so someone on the home network could open the code before the real
// phone does; that someone cannot make their phone show the number of the
// phone in the parent's hand, so the parent confirms only a matching number.
func verificationNumber(claimHash string) string {
	if len(claimHash) < 8 {
		return ""
	}
	n, err := strconv.ParseUint(claimHash[:8], 16, 32)
	if err != nil {
		return ""
	}
	n %= 1000000
	return fmt.Sprintf("%03d %03d", n/1000, n%1000)
}

// deviceLabel names a phone for the parent from its User-Agent, for example
// "Safari on iPhone". It only helps a parent tell phones apart.
func deviceLabel(ua string) string {
	has := func(subs ...string) bool {
		for _, sub := range subs {
			if strings.Contains(ua, sub) {
				return true
			}
		}
		return false
	}
	browser := "Browser"
	switch {
	case has("Firefox/", "FxiOS"):
		browser = "Firefox"
	case has("Edg/", "EdgA/", "EdgiOS"):
		browser = "Edge"
	case has("SamsungBrowser"):
		browser = "Samsung Internet"
	case has("CriOS", "Chrome/"):
		browser = "Chrome"
	case has("Safari/"):
		browser = "Safari"
	}
	device := ""
	switch {
	case has("iPhone"):
		device = "iPhone"
	case has("iPad"):
		device = "iPad"
	case has("Android"):
		device = "Android"
	}
	if device == "" {
		return browser
	}
	return browser + " on " + device
}

func rfc3339(t time.Time, loc *time.Location) string { return t.In(loc).Format(time.RFC3339) }

type phoneJSON struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	PersonID   string  `json:"person_id"`
	PersonName string  `json:"person_name"`
	PairedAt   string  `json:"paired_at"`
	LastSeenAt *string `json:"last_seen_at"`
}

func phoneToJSON(ph store.Phone, loc *time.Location) phoneJSON {
	j := phoneJSON{ID: ph.ID, Name: ph.Name, PersonID: ph.PersonID, PersonName: ph.PersonName, PairedAt: rfc3339(ph.PairedAt, loc)}
	if !ph.LastSeen.IsZero() {
		seen := rfc3339(ph.LastSeen, loc)
		j.LastSeenAt = &seen
	}
	return j
}

func (s *server) apiListPhones(w http.ResponseWriter, _ *http.Request, p principal) {
	list, err := s.db.Phones(p.household.ID)
	if err != nil {
		s.fail(w, "list phones", err)
		return
	}
	out := make([]phoneJSON, 0, len(list))
	for _, ph := range list {
		out = append(out, phoneToJSON(ph, p.location()))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiCreatePhonePairing(w http.ResponseWriter, r *http.Request, p principal) {
	code, err := s.newPairingCode(r, p.household.ID)
	if err != nil {
		s.fail(w, "phone pairing", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": code.ID, "url": code.URL, "expires_at": rfc3339(code.Expires, p.location())})
}

func (s *server) apiListPhonePairings(w http.ResponseWriter, _ *http.Request, p principal) {
	list, err := s.db.PendingPhonePairings(p.household.ID, s.now())
	if err != nil {
		s.fail(w, "list phone pairings", err)
		return
	}
	out := make([]map[string]string, 0, len(list))
	for _, pr := range list {
		out = append(out, map[string]string{"id": pr.ID, "device": pr.Device, "verification": verificationNumber(pr.ClaimHash), "expires_at": rfc3339(pr.ExpiresAt, p.location())})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiConfirmPhonePairing(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		PersonID string `json:"person_id"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var fieldErr *store.FieldError
	ph, err := s.db.ConfirmPhonePairing(p.household.ID, r.PathValue("id"), in.PersonID, in.Name, s.now())
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no phone is waiting with this pairing, or the code has expired")
		return
	case errors.As(err, &fieldErr):
		writeError(w, http.StatusUnprocessableEntity, fieldErr.Error())
		return
	default:
		s.fail(w, "confirm phone", err)
		return
	}
	// The phone collects its session at its next request; the answer shows the
	// phone as it will be listed.
	ph.PersonName = s.personName(p, ph.PersonID)
	writeJSON(w, http.StatusCreated, phoneToJSON(ph, p.location()))
}

func (s *server) personName(p principal, id string) string {
	if person, err := s.db.Person(p.household.ID, id); err == nil {
		return person.Name
	}
	return ""
}

func (s *server) apiRevokePhone(w http.ResponseWriter, r *http.Request, p principal) {
	switch err := s.db.RevokePhone(p.household.ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "phone not found")
	case err != nil:
		s.fail(w, "revoke phone", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
