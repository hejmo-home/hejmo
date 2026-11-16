package server

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
)

// recoveryCodeCount is the size of a set of recovery codes (ADR 0017).
const recoveryCodeCount = 10

// newRecoveryCodes stores a fresh set for the person, which voids the old
// one, and returns the codes in plain text: this is the only time they exist.
func (s *server) newRecoveryCodes(personID string) ([]string, error) {
	codes := auth.NewRecoveryCodes(recoveryCodeCount)
	// Argon2id is slow on purpose; four at a time keeps a Pi's memory in check.
	hashes := make([]string, len(codes))
	var wg sync.WaitGroup
	gate := make(chan struct{}, 4)
	for i, c := range codes {
		wg.Add(1)
		gate <- struct{}{}
		go func() {
			defer func() { <-gate; wg.Done() }()
			hashes[i] = auth.HashRecoveryCode(c)
		}()
	}
	wg.Wait()
	return codes, s.db.ReplaceRecoveryCodes(personID, hashes)
}

// The three places that show a new set of recovery codes (ticket 176): the
// first parent's setup, the invited parent's last step, and "Create new
// recovery codes" on My account. They share one card; the flow decides the
// shell, the label of the continue button and where the tick leads.
const (
	flowWizard  = "wizard"
	flowInvite  = "invite"
	flowAccount = "account"
)

// issueRecoveryCodes makes a new set and shows it once. A set that was never
// confirmed as saved is replaced by the next request that has no "fresh"
// field: that is a reload of this very POST, and the page says so.
func (s *server) issueRecoveryCodes(w http.ResponseWriter, r *http.Request, personID string) {
	// A pending account sets up new codes first; the phone must not mint any.
	if acc, err := s.db.AccountByPerson(personID); err == nil && !acc.TOTPConfirmed {
		http.Redirect(w, r, "/setup/totp", http.StatusSeeOther)
		return
	}
	reloaded := s.unsavedSet(personID) && r.PostFormValue("fresh") == ""
	codes, err := s.newRecoveryCodes(personID)
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	s.showRecoveryCodes(w, r, personID, recoveryPage{Flow: flowAccount, Codes: codes, Reloaded: reloaded})
}

// unsavedSet reports whether the person has a set of recovery codes that was
// never confirmed as saved.
func (s *server) unsavedSet(personID string) bool {
	set, err := s.db.RecoverySet(personID)
	return err == nil && set.Exists && set.SavedAt.IsZero()
}

// recoveryPage is what the card needs: the codes (shown once, or echoed back
// after a refused continue), the flow, and which notices to show.
type recoveryPage struct {
	Flow     string
	Codes    []string
	Reloaded bool // a new set made because the page was loaded again before the tick
	Refused  bool // continue without the tick: 422 with the error at the checkbox
}

// showRecoveryCodes renders the card. Codes are only ever shown here, right
// after they were stored (or echoed back when a continue was refused).
func (s *server) showRecoveryCodes(w http.ResponseWriter, r *http.Request, personID string, pg recoveryPage) {
	acc, err := s.db.AccountByPerson(personID)
	if err != nil {
		s.fail(w, "account", err)
		return
	}
	h, err := s.db.HomeHousehold()
	if err != nil {
		s.fail(w, "household", err)
		return
	}
	lang := s.pageLanguage(r)
	var name string
	if person, ok := s.member(personID); ok {
		name = person.Name
	}
	var shell *wizardView
	continueKey := "recovery.continue.account"
	switch pg.Flow {
	case flowWizard:
		shell, continueKey = wizardAt(3), "recovery.continue.wizard"
	case flowInvite:
		if shell = s.inviteShellFor(acc); shell != nil {
			continueKey = "invite.recovery.continue"
		}
	default:
		pg.Flow = flowAccount
	}
	if shell == nil && pg.Flow == flowInvite {
		pg.Flow = flowAccount
	}
	today := s.now().In(principal{household: h}.location())
	date := datefmt.Date(h.DateFormat, today)
	status := http.StatusOK
	if pg.Refused {
		status = http.StatusUnprocessableEntity
	}
	w.Header().Set("Cache-Control", "no-store")
	data := map[string]any{
		"Codes": pg.Codes, "ContinueKey": continueKey, "Flow": pg.Flow, "Reloaded": pg.Reloaded, "Refused": pg.Refused,
		"Username": acc.Username, "BoxName": s.boxName(), "Date": date, "Count": len(pg.Codes),
		"ForText":   s.i18n.Format(lang, i18n.Msg("recovery.card.for", "username", acc.Username, "box", s.boxName(), "date", date)),
		"CardTitle": s.i18n.Format(lang, i18n.Msg("recovery.card.title", "n", strconv.Itoa(len(pg.Codes)))),
		"PDFDoc":    s.recoveryPDFDoc(lang, name, acc.Username, date, today.Format("2006-01-02")),
	}
	if shell != nil {
		data["Wizard"] = shell
	} else {
		data["SuccessKey"] = "flash.recovery_codes_created"
	}
	s.render(w, r, status, "recovery_codes.html", data)
}

// recoveryPDFDoc is everything the page script needs to write the PDF: texts
// in the page language and the file name. The codes come from the page.
func (s *server) recoveryPDFDoc(lang, name, username, date, iso string) template.HTML {
	t := func(key string) string { return s.i18n.T(lang, key) }
	box := s.boxName()
	doc := map[string]any{
		"file":     fileSafe(t("recovery.pdf.file_prefix") + "-" + box + "-" + username + "-" + iso),
		"page":     t("recovery.pdf.page"),
		"title":    s.i18n.Format(lang, i18n.Msg("recovery.pdf.title", "name", name)),
		"boxLabel": t("recovery.pdf.box"), "box": box,
		"userLabel": t("recovery.pdf.username"), "username": username,
		"dateLabel": t("recovery.pdf.created"), "date": date,
		"usedHint": t("recovery.pdf.used_hint"), "howTitle": t("recovery.pdf.how_title"),
		"how":    []string{t("recovery.pdf.how.1"), t("recovery.pdf.how.2"), t("recovery.pdf.how.3"), t("recovery.pdf.how.4"), t("recovery.pdf.how.5")},
		"footer": t("recovery.pdf.footer"),
		"ok":     t("recovery.pdf.ok"), "failed": t("recovery.pdf.failed"), "gateError": t("recovery.saved.error"),
	}
	raw, _ := json.Marshal(doc) // escapes < > & so it cannot end the script element
	return template.HTML(raw)   // #nosec G203 -- JSON with HTML characters escaped
}

// fileSafe keeps a file name to letters, digits, dot, dash and underscore.
func fileSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		}
		return '-'
	}, s)
}

func (s *server) apiNewRecoveryCodes(w http.ResponseWriter, _ *http.Request, p principal) {
	// A token outlives a reset; until the new codes are confirmed the account
	// may only set them up, not mint recovery codes.
	acc, err := s.db.AccountByPerson(p.person.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusForbidden, "this person has no account")
		return
	}
	if err != nil {
		s.fail(w, "account", err)
		return
	}
	if !acc.TOTPConfirmed {
		writeError(w, http.StatusForbidden, "confirm your new one-time codes first")
		return
	}
	codes, err := s.newRecoveryCodes(p.person.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusForbidden, "this person has no account")
		return
	}
	if err != nil {
		s.fail(w, "recovery codes", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string][]string{"codes": codes})
}

func (s *server) apiResetOTP(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Password       string   `json:"password"`
		Code           string   `json:"code"`
		RemovePasskeys []string `json:"remove_passkeys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	_, outcome, err := s.resetOtherOTP(p.person, p.household.ID, r.PathValue("id"), resetConfirmation{Password: in.Password, Code: in.Code}, in.RemovePasskeys)
	switch {
	case err != nil:
		s.fail(w, "reset one-time codes", err)
	case outcome == resetDone:
		w.WriteHeader(http.StatusNoContent)
	case outcome == resetSelf:
		writeError(w, http.StatusForbidden, "a parent cannot reset their own one-time codes this way")
	case outcome == resetUnknown:
		writeError(w, http.StatusNotFound, "parent not found")
	case outcome == resetLimited:
		writeError(w, http.StatusTooManyRequests, "too many failed attempts")
	default:
		writeError(w, http.StatusUnprocessableEntity, "password or one-time code is wrong")
	}
}
