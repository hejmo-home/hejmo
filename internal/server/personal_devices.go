package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/qrsvg"
	"github.com/hejmo-home/hejmo/internal/store"
)

const (
	// pairingCookie holds a personal device's own claim on a pairing code while it waits
	// for a parent's confirmation. The box keeps only its hash.
	pairingCookie = "hejmo_pairing"
	// touchCookie is set by the pairing page's script when the device has a touch screen.
	touchCookie = "hejmo_touch"
	// personalDevicePollSeconds is how often a waiting personal device asks whether a parent
	// confirmed.
	personalDevicePollSeconds = 3
)

// waitingView is a personal device that opened its code. The form fields and the error
// are filled only for the pairing a failed confirmation was about.
type waitingView struct {
	ID, Device, Expires, Verification, Name, Person, Error, ErrorField string
	// Proposals is a JSON object from person ID to the name proposed when that person is
	// chosen (Q148); web/static/name-proposal.js puts it in the name field.
	Proposals string
	// Selected is Person as a list, for the person chips.
	Selected []string
}

// confirmForm is the parent's entry on a waiting personal device that was refused, shown
// again with its error. The zero value is "no entry".
type confirmForm struct{ PairingID, Name, Person, Error, ErrorField string }

// pairingCode is a fresh pairing code and where its QR code leads. A code that
// came from re-pairing a personal device also says whom the personal device is for next (ForPerson)
// and what it was called (OldName).
type pairingCode struct {
	ID        string
	URL       string
	Expires   time.Time
	ForPerson *personChip
	OldName   string
}

// newPairingCode stores a pairing code for the household. The code itself is
// only in the returned address: the box keeps its hash.
func (s *server) newPairingCode(r *http.Request, householdID string) (pairingCode, error) {
	code := auth.NewToken()
	pr, err := s.db.CreatePersonalDevicePairing(householdID, auth.HashToken(code), s.now())
	if err != nil {
		return pairingCode{}, err
	}
	return pairingCode{ID: pr.ID, URL: s.joinURL(r, code), Expires: pr.ExpiresAt}, nil
}

// joinURL is where a personal device that scans the QR code goes. With the box's own CA
// the personal device cannot reach the HTTPS address before it trusts the box, so the
// code points to the trust page first, over plain HTTP, which then leads on
// (ADR 0014, 0015).
func (s *server) joinURL(r *http.Request, code string) string {
	join := s.httpsOrigin(r) + "/personal-devices/join/" + code
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
	if err != nil || oerr != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, origin.Host) || !strings.HasPrefix(u.Path, "/personal-devices/join/") {
		return ""
	}
	return u.String()
}

func (s *server) renderPersonalDevices(w http.ResponseWriter, r *http.Request, p principal, status int, code *pairingCode, form confirmForm) {
	at := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return p.dateTime(t)
	}
	personalDevices, err := s.db.PersonalDevices(p.household.ID)
	if err != nil {
		s.fail(w, "personal_devices", err)
		return
	}
	waiting, err := s.db.PendingPersonalDevicePairings(p.household.ID, s.now())
	if err != nil {
		s.fail(w, "waiting personal devices", err)
		return
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	chips, err := s.chipByPerson(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	var rows []rowView
	for _, ph := range personalDevices {
		row := rowView{Href: "/personal-devices/" + ph.ID, Title: ph.Name, Lines: []rowLine{s.whoLine(r, chips, ph.PersonID, ph.PersonName)}}
		switch {
		case !ph.SessionEnds.IsZero() && !ph.SessionEnds.After(s.now()):
			row.Flag = &flagView{Kind: "warn", Text: s.say(r, i18n.Msg("personal_devices.expired"))}
		case ph.LastSeen.IsZero():
			row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("personal_devices.never"))})
		default:
			row.Lines = append(row.Lines, rowLine{Text: s.say(r, i18n.Msg("personal_devices.last_seen_at", "when", p.when(ph.LastSeen)))})
		}
		rows = append(rows, row)
	}
	var waitingViews []waitingView
	for _, wp := range waiting {
		v := waitingView{ID: wp.ID, Device: wp.Device, Expires: at(wp.ExpiresAt), Verification: verificationNumber(wp.ClaimHash)}
		proposals := make(map[string]string, len(people))
		for _, c := range chipsOf(people) {
			proposals[c.ID] = s.defaultPersonalDeviceName(r, c.Name, wp.Device)
		}
		if b, err := json.Marshal(proposals); err == nil {
			v.Proposals = string(b)
		}
		if c, ok := chips[wp.ForPersonID]; ok { // a personal device that was re-paired: the parent only compares and confirms
			v.Person, v.Name = c.ID, s.defaultPersonalDeviceName(r, c.Name, wp.Device)
		}
		if form.PairingID == wp.ID {
			v.Name, v.Person, v.Error, v.ErrorField = form.Name, form.Person, form.Error, form.ErrorField
		}
		if v.Person != "" {
			v.Selected = []string{v.Person}
		}
		waitingViews = append(waitingViews, v)
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID),
		"Rows": rows, "Waiting": waitingViews, "People": chipsOf(people),
	}
	if code != nil {
		svg, err := qrsvg.SVG(code.URL, s.i18n.T(s.langOf(r), "personal_devices.qr.label"))
		if err != nil {
			s.fail(w, "qr code", err)
			return
		}
		data["Code"] = map[string]any{"URL": code.URL, "QR": template.HTML(svg), "Expires": at(code.Expires)}
		if code.ForPerson != nil {
			// "Works once, until <b>Monday, 16.11.2026, 17:52</b>."
			valid := html.EscapeString(s.say(r, i18n.Msg("personal_devices.repair.valid", "when", "\x00")))
			valid = strings.Replace(valid, "\x00", "<b>"+html.EscapeString(p.dateTime(code.Expires))+"</b>", 1)
			data["Repair"] = map[string]any{"OldName": code.OldName, "For": *code.ForPerson, "Valid": template.HTML(valid)}
			data["SuccessMsg"] = i18n.Msg("personal_devices.repair.done", "personal_device", code.OldName)
		}
	}
	data["CAFirst"] = s.pki.Ready() && !s.publicInUse()
	s.render(w, r, status, "personal_devices.html", data)
}

func (s *server) handlePersonalDevicesPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.parentPage(w, r); ok {
		s.renderPersonalDevices(w, r, p, http.StatusOK, nil, confirmForm{})
	}
}

func (s *server) handlePersonalDevicePairingCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	code, err := s.newPairingCode(r, p.household.ID)
	if err != nil {
		s.fail(w, "personal device pairing", err)
		return
	}
	s.renderPersonalDevices(w, r, p, http.StatusOK, &code, confirmForm{})
}

func (s *server) handlePersonalDevicePairingConfirm(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	name, person := r.PostFormValue("name"), r.PostFormValue("person")
	form := func(key, field string) confirmForm {
		return confirmForm{PairingID: r.PathValue("id"), Name: name, Person: person, Error: key, ErrorField: field}
	}
	var fieldErr *store.FieldError
	switch _, err := s.db.ConfirmPersonalDevicePairing(p.household.ID, r.PathValue("id"), person, name, s.now()); {
	case err == nil:
		s.redirectSaved(w, r, "/personal-devices", "flash.personal_device_paired")
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.As(err, &fieldErr):
		s.renderPersonalDevices(w, r, p, http.StatusUnprocessableEntity, nil, form("personal_devices.error."+fieldErr.Field, fieldErr.Field))
	default:
		s.fail(w, "confirm personal device", err)
	}
}

// personalDeviceOf finds one of the household's paired personal devices.
func (s *server) personalDeviceOf(householdID, id string) (store.PersonalDevice, bool, error) {
	personalDevices, err := s.db.PersonalDevices(householdID)
	if err != nil {
		return store.PersonalDevice{}, false, err
	}
	for _, ph := range personalDevices {
		if ph.ID == id {
			return ph, true, nil
		}
	}
	return store.PersonalDevice{}, false, nil
}

func (s *server) handlePersonalDevicePage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderPersonalDevice(w, r, p, http.StatusOK, "")
}

// renderPersonalDevice shows one paired personal device. errKey, when set, is the error at the
// person chips of the "pair for another person" box, which then stays open.
func (s *server) renderPersonalDevice(w http.ResponseWriter, r *http.Request, p principal, status int, errKey string) {
	ph, found, err := s.personalDeviceOf(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, "personal_device", err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	people, err := s.db.People(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	chips := chipsOf(people)
	who := factView{Label: s.say(r, i18n.Msg("personal_devices.fact.for")), Value: ph.PersonName}
	can := "personal_devices.can.child"
	var others []personChip
	for i, c := range chips {
		if c.ID != ph.PersonID {
			others = append(others, c)
			continue
		}
		who.Value, who.People = "", []personChip{c}
		if people[i].Role == store.RoleParent {
			can = "personal_devices.can.parent"
		}
	}
	last := factView{Label: s.say(r, i18n.Msg("personal_devices.fact.last_seen")), Value: s.say(r, i18n.Msg("personal_devices.never"))}
	if !ph.LastSeen.IsZero() {
		last.Value = p.when(ph.LastSeen)
	}
	facts := []factView{who,
		{Label: s.say(r, i18n.Msg("personal_devices.fact.can")), Value: s.say(r, i18n.Msg(can))},
		{Label: s.say(r, i18n.Msg("personal_devices.fact.paired")), Value: p.day(ph.PairedAt)},
		last}
	if !ph.SessionEnds.IsZero() && !ph.SessionEnds.After(s.now()) {
		facts = append(facts, factView{Label: s.say(r, i18n.Msg("personal_devices.fact.session")), Flag: &flagView{Kind: "warn", Text: s.say(r, i18n.Msg("personal_devices.expired"))}})
	}
	data := map[string]any{"Person": p.person, "PersonalDevice": ph, "Facts": facts, "Title": ph.Name}
	if can == "personal_devices.can.child" { // parents can sign themselves out (#197): only a child's personal device is handed on
		data["Repair"] = map[string]any{
			"Name": ph.PersonName, "Whose": s.whose(r, ph.PersonName), "People": others, "Open": errKey != "",
		}
	}
	if errKey != "" {
		data["Error"], data["ErrorField"] = errKey, "person"
	}
	s.render(w, r, status, "personal_device.html", data)
}

// whose is a name's genitive in the page language, with the typographic
// apostrophe: "Mias", "Jonas’", "Mia’s".
func (s *server) whose(r *http.Request, name string) string {
	return strings.ReplaceAll(possessive(s.langOf(r), name), "'", "’")
}

// defaultPersonalDeviceName is the name proposed for a personal device that is for name:
// "Mias Handy", "Jonas’ Tablet", "Mia’s device". The word says phone or tablet by the kind of
// screen the pairing label (see deviceLabel) names, never a brand or a model, and device when
// the kind is unknown (Q148, #203).
func (s *server) defaultPersonalDeviceName(r *http.Request, name, label string) string {
	return s.say(r, i18n.Msg("personal_devices.default_name."+deviceNoun(label), "whose", s.whose(r, name)))
}

// screenKinds are the devices deviceLabel can name, with the kind of screen each has. The one
// table makes the label and the kind one fact: rewording a label cannot lose the kind.
var screenKinds = []struct{ device, noun string }{
	{deviceIPhone, "phone"},
	{deviceAndroidPhone, "phone"},
	{deviceIPad, "tablet"},
	{deviceAndroidTablet, "tablet"},
}

const (
	deviceIPhone        = "iPhone"
	deviceAndroidPhone  = "Android phone"
	deviceIPad          = "iPad"
	deviceAndroidTablet = "Android tablet"
)

// deviceNoun is phone, tablet or device, from the label deviceLabel made.
func deviceNoun(label string) string {
	for _, k := range screenKinds {
		if strings.HasSuffix(label, " on "+k.device) {
			return k.noun
		}
	}
	return "device"
}

// handlePersonalDeviceRepair ends a child's device session and shows a new pairing code
// for the person the personal device is handed to, in one step (#198).
func (s *server) handlePersonalDeviceRepair(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	ph, found, err := s.personalDeviceOf(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, "personal_device", err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	code, pr, err := s.repairPersonalDevice(r, p, ph.ID, r.PostFormValue("person"))
	var fieldErr *store.FieldError
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.As(err, &fieldErr):
		s.renderPersonalDevice(w, r, p, http.StatusUnprocessableEntity, "personal_devices.repair.error")
		return
	default:
		s.fail(w, "repair personal device", err)
		return
	}
	chips, err := s.chipByPerson(p.household.ID)
	if err != nil {
		s.fail(w, "people", err)
		return
	}
	forPerson := chips[pr.ForPersonID]
	s.renderPersonalDevices(w, r, p, http.StatusOK, &pairingCode{ID: pr.ID, URL: code, Expires: pr.ExpiresAt, ForPerson: &forPerson, OldName: ph.Name}, confirmForm{})
}

// repairPersonalDevice ends the personal device's session and stores a code for personID; the
// address of the code is returned with the stored pairing.
func (s *server) repairPersonalDevice(r *http.Request, p principal, personalDeviceID, personID string) (string, store.PersonalDevicePairing, error) {
	code := auth.NewToken()
	pr, err := s.db.RepairPersonalDevice(p.household.ID, personalDeviceID, personID, auth.HashToken(code), s.now())
	if err != nil {
		return "", pr, err
	}
	return s.joinURL(r, code), pr, nil
}

func (s *server) handlePersonalDeviceRevoke(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	ph, found, err := s.personalDeviceOf(p.household.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, "personal_device", err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	switch err := s.db.RevokePersonalDevice(p.household.ID, ph.ID); {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, "revoke personal device", err)
	default:
		s.redirectSavedNamed(w, r, "/personal-devices", "flash.personal_device_revoked", ph.Name)
	}
}

// handlePersonalDeviceJoin is the address in the QR code, opened by the personal device. The
// personal device waits here until a parent confirms, then gets its device session.
func (s *server) handlePersonalDeviceJoin(w http.ResponseWriter, r *http.Request) {
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
	refuse := func() {
		s.render(w, r, http.StatusGone, "personal_devices_join.html", map[string]any{"State": "refused"})
	}
	if err := s.db.JoinPersonalDevicePairing(codeHash, claimHash, deviceLabel(r.UserAgent(), touchScreen(r)), s.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			refuse()
		} else {
			s.fail(w, "join personal device pairing", err)
		}
		return
	}
	token := auth.NewToken()
	expires := s.now().Add(store.PersonalDeviceSessionLifetime)
	switch _, err := s.db.CompletePersonalDevicePairing(codeHash, claimHash, auth.HashToken(token), s.now(), expires); {
	case err == nil:
		http.SetCookie(w, &http.Cookie{Name: pairingCookie, Value: "", Path: "/personal-devices/join/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: token, Path: "/", Expires: expires,
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		})
		s.setDeviceMarker(w, r)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case errors.Is(err, store.ErrPersonalDevicePending):
		http.SetCookie(w, &http.Cookie{
			Name: pairingCookie, Value: claim, Path: "/personal-devices/join/", MaxAge: int(store.PairingCodeLifetime.Seconds()),
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		})
		data := map[string]any{"State": "waiting", "Poll": personalDevicePollSeconds, "Verification": verificationNumber(claimHash)}
		if next, ok, err := s.db.PairingForPerson(codeHash, claimHash, s.now()); err != nil {
			s.fail(w, "pairing person", err)
			return
		} else if ok {
			h, err := s.db.HomeHousehold()
			if err != nil {
				s.fail(w, "household", err)
				return
			}
			chips, err := s.chipByPerson(h.ID)
			if err != nil {
				s.fail(w, "people", err)
				return
			}
			data["For"] = chips[next.ID]
		}
		s.render(w, r, http.StatusOK, "personal_devices_join.html", data)
	case errors.Is(err, store.ErrNotFound):
		refuse()
	default:
		s.fail(w, "complete personal device pairing", err)
	}
}

// verificationNumber is a six-digit number both the waiting personal device and the
// parent's personal devices screen show, taken from the hash of the personal device's secret. The
// pairing address can travel over plain HTTP (the trust page of the box's own
// CA), so someone on the home network could open the code before the real
// personal device does; that someone cannot make their personal device show the number of the
// personal device in the parent's hand, so the parent confirms only a matching number.
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

// touchScreen is whether the pairing page reported a touch screen (web/static/screen-kind.js).
func touchScreen(r *http.Request) bool {
	c, err := r.Cookie(touchCookie)
	return err == nil && c.Value == "1"
}

// deviceLabel names a personal device for the parent from its User-Agent, for example
// "Safari on iPhone". It only helps a parent tell personal devices apart. A Mac user agent with a
// touch screen is an iPad, which has sent the user agent of a Mac since iPadOS 13.
func deviceLabel(ua string, touch bool) string {
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
		device = deviceIPhone
	case has("iPad"), has("Macintosh") && touch:
		device = deviceIPad
	case has("Android") && has("Mobile"):
		device = deviceAndroidPhone
	case has("Android"):
		device = deviceAndroidTablet
	}
	if device == "" {
		return browser
	}
	return browser + " on " + device
}

func rfc3339(t time.Time, loc *time.Location) string { return t.In(loc).Format(time.RFC3339) }

type personalDeviceJSON struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	PersonID   string  `json:"person_id"`
	PersonName string  `json:"person_name"`
	PairedAt   string  `json:"paired_at"`
	LastSeenAt *string `json:"last_seen_at"`
}

func personalDeviceToJSON(ph store.PersonalDevice, loc *time.Location) personalDeviceJSON {
	j := personalDeviceJSON{ID: ph.ID, Name: ph.Name, PersonID: ph.PersonID, PersonName: ph.PersonName, PairedAt: rfc3339(ph.PairedAt, loc)}
	if !ph.LastSeen.IsZero() {
		seen := rfc3339(ph.LastSeen, loc)
		j.LastSeenAt = &seen
	}
	return j
}

func (s *server) apiListPersonalDevices(w http.ResponseWriter, _ *http.Request, p principal) {
	list, err := s.db.PersonalDevices(p.household.ID)
	if err != nil {
		s.fail(w, "list personal devices", err)
		return
	}
	out := make([]personalDeviceJSON, 0, len(list))
	for _, ph := range list {
		out = append(out, personalDeviceToJSON(ph, p.location()))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiCreatePersonalDevicePairing(w http.ResponseWriter, r *http.Request, p principal) {
	code, err := s.newPairingCode(r, p.household.ID)
	if err != nil {
		s.fail(w, "personal device pairing", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": code.ID, "url": code.URL, "expires_at": rfc3339(code.Expires, p.location())})
}

func (s *server) apiListPersonalDevicePairings(w http.ResponseWriter, _ *http.Request, p principal) {
	list, err := s.db.PendingPersonalDevicePairings(p.household.ID, s.now())
	if err != nil {
		s.fail(w, "list personal device pairings", err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, pr := range list {
		item := map[string]any{"id": pr.ID, "device": pr.Device, "verification": verificationNumber(pr.ClaimHash), "expires_at": rfc3339(pr.ExpiresAt, p.location()), "for_person_id": nil}
		if pr.ForPersonID != "" {
			item["for_person_id"] = pr.ForPersonID
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiConfirmPersonalDevicePairing(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		PersonID string `json:"person_id"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var fieldErr *store.FieldError
	ph, err := s.db.ConfirmPersonalDevicePairing(p.household.ID, r.PathValue("id"), in.PersonID, in.Name, s.now())
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no personal device is waiting with this pairing, or the code has expired")
		return
	case errors.As(err, &fieldErr):
		writeError(w, http.StatusUnprocessableEntity, fieldErr.Error())
		return
	default:
		s.fail(w, "confirm personal device", err)
		return
	}
	// The personal device collects its session at its next request; the answer shows the
	// personal device as it will be listed.
	ph.PersonName = s.personName(p, ph.PersonID)
	writeJSON(w, http.StatusCreated, personalDeviceToJSON(ph, p.location()))
}

func (s *server) personName(p principal, id string) string {
	if person, err := s.db.Person(p.household.ID, id); err == nil {
		return person.Name
	}
	return ""
}

// apiRepairPersonalDevice gives a personal device to another person: its session ends and a new
// pairing code for that person is returned (#198).
func (s *server) apiRepairPersonalDevice(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		PersonID string `json:"person_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	var fieldErr *store.FieldError
	url, pr, err := s.repairPersonalDevice(r, p, r.PathValue("id"), in.PersonID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, map[string]string{"id": pr.ID, "url": url, "expires_at": rfc3339(pr.ExpiresAt, p.location()), "person_id": pr.ForPersonID})
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "personal device not found")
	case errors.As(err, &fieldErr):
		writeError(w, http.StatusUnprocessableEntity, fieldErr.Error())
	default:
		s.fail(w, "repair personal device", err)
	}
}

func (s *server) apiRevokePersonalDevice(w http.ResponseWriter, r *http.Request, p principal) {
	switch err := s.db.RevokePersonalDevice(p.household.ID, r.PathValue("id")); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "personal device not found")
	case err != nil:
		s.fail(w, "revoke personal device", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
