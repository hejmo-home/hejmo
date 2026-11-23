package server

import (
	"context"
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/update"
)

// ---- the setup wizard (Q142)

// renderOfflineQuestion is the wizard's question right after "Only in the home
// network": should the box stay fully offline?
func (s *server) renderOfflineQuestion(w http.ResponseWriter, r *http.Request, status int, withError bool) {
	s.renderOfflineQuestionAt(w, r, status, withError, "")
}

// renderOfflineQuestionAt is the question whose form posts to action (empty:
// /setup/offline). A custom action is the switch to the own CA after a stuck
// public name; the way back to the public name is not offered there.
func (s *server) renderOfflineQuestionAt(w http.ResponseWriter, r *http.Request, status int, withError bool, action string) {
	data := map[string]any{"Wizard": wizardAt(1), "Choice": s.public != nil && action == "", "Invalid": withError, "Action": "/setup/offline"}
	if action != "" {
		data["Action"] = action
	}
	if withError {
		data["ErrorField"], data["Error"] = "offline", "setup.offline.error"
	}
	s.render(w, r, status, "setup_offline.html", data)
}

func (s *server) handleOfflineQuestion(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() || s.httpsReady() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.renderOfflineQuestion(w, r, http.StatusOK, false)
}

func (s *server) handleOfflineAnswer(w http.ResponseWriter, r *http.Request) {
	if s.db.Configured() || s.httpsReady() {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	var offline bool
	switch r.PostFormValue("offline") {
	case "on":
		offline = true
	case "off":
	default:
		s.renderOfflineQuestion(w, r, http.StatusUnprocessableEntity, true)
		return
	}
	if err := s.db.AnswerOfflineQuestion(offline, s.now()); err != nil {
		s.fail(w, "offline answer", err)
		return
	}
	http.Redirect(w, r, "/setup/https?way=own", http.StatusSeeOther)
}

// takeOfflineAnswerBack forgets the wizard's answer when the parent goes back
// to choose another way: a public name needs the internet.
func (s *server) takeOfflineAnswerBack() {
	box := s.outboundBox()
	if !box.Asked && !box.Offline {
		return
	}
	if err := s.db.ResetOfflineQuestion(); err != nil {
		s.logger.Error("reset offline answer", "err", err)
	}
}

// ---- the page "Connections to the outside"

type connectionsForm struct {
	Days  string
	Error string // the text key of the error at the field
}

func (s *server) renderConnections(w http.ResponseWriter, r *http.Request, p principal, status int, errField string, form connectionsForm) {
	box := s.outboundBox()
	s.noteUpdated()
	box = s.outboundBox()
	st := update.ReadStatus(s.updateDir)
	rows, err := s.outsideRows(r, p, box, st)
	if err != nil {
		s.fail(w, "connections", err)
		return
	}
	refused, err := s.refusedRows(r, p)
	if err != nil {
		s.fail(w, "refused attempts", err)
		return
	}
	lang := s.langOf(r)
	days := strconv.Itoa(box.ReminderDays)
	if form.Days != "" {
		days = form.Days
	}
	data := map[string]any{
		"Person": p.person, "SyncStale": s.syncStale(p.household.ID), "Offline": box.Offline, "Rows": rows, "Refused": refused,
		"Days": days, "ErrorField": errField, "Error": form.Error, "UpdateManaged": s.updateDir != "",
		"Guide": s.i18n.T(lang, guideUpdateAnchor), "TimeGuide": s.i18n.T(lang, "outside.scope.time.url"),
		"Scope": s.scopeNote(r, p),
	}
	if box.Offline && !box.OfflineSince.IsZero() {
		data["Since"] = datefmt.DateTimeSeconds(p.household.DateFormat, p.household.TimeFormat, box.OfflineSince.In(p.location()))
	}
	if st.Version != "" {
		data["Installed"] = s.i18n.Format(lang, i18n.Msg("outside.updates.installed", "version", st.Version, "date", datefmt.Date(p.household.DateFormat, box.UpdatedAt.In(p.location()))))
	} else {
		data["Installed"] = s.i18n.Format(lang, i18n.Msg("outside.updates.installed_noversion", "date", datefmt.Date(p.household.DateFormat, box.UpdatedAt.In(p.location()))))
	}
	s.render(w, r, status, "connections_outside.html", data)
}

// scopeNote is what the page says offline mode does not cover: the host system
// and the AI assistants (Q146).
func (s *server) scopeNote(r *http.Request, p principal) template.HTML {
	if !p.household.MCPEnabled {
		return ""
	}
	tokens, err := s.mcpTokens(p.household.ID)
	if err != nil || len(tokens) == 0 {
		return ""
	}
	lang := s.langOf(r)
	var list []string
	for _, t := range tokens {
		list = append(list, "„"+t.Name+"“")
	}
	key := "outside.scope.mcp.many"
	if len(list) == 1 {
		key = "outside.scope.mcp.one"
	}
	text := s.i18n.Format(lang, i18n.Msg(key, "n", fmt.Sprint(len(list)), "names", joinAnd(list, s.i18n.T(lang, "outside.and"))))
	return template.HTML(html.EscapeString(text)) // #nosec G203 -- escaped
}

func (s *server) handleConnectionsPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	s.renderConnections(w, r, p, http.StatusOK, "", connectionsForm{})
}

func (s *server) handleConnectionsSave(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	_ = r.ParseForm()
	box := s.outboundBox()
	wantOffline := r.PostForm.Has("offline")
	switch {
	case box.Offline && !wantOffline: // switching off asks first what goes outside again
		http.Redirect(w, r, connectionsPath+"/online", http.StatusSeeOther)
		return
	case !box.Offline && wantOffline && s.publicReady(): // a public name needs the change to the home network first
		http.Redirect(w, r, connectionsPath+"/offline", http.StatusSeeOther)
		return
	}
	change := outsideChange{Offline: &wantOffline}
	if !box.Offline { // a locked switch is not sent at all: only an open form says what is on
		change.Services = map[outbound.Service]bool{}
		for _, svc := range switchable {
			change.Services[svc] = r.PostForm.Has(string(svc))
		}
	}
	form := connectionsForm{Days: strings.TrimSpace(r.PostFormValue("update_reminder_days"))}
	if form.Days != "" && wantOffline {
		n, err := strconv.Atoi(form.Days)
		if err != nil || n < store.MinReminderDays || n > store.MaxReminderDays {
			form.Error = "outside.error.days"
			s.renderConnections(w, r, p, http.StatusUnprocessableEntity, "update_reminder_days", form)
			return
		}
		change.Days = &n
	}
	if err := s.applyOutside(p.household, change); err != nil {
		s.fail(w, "save connections", err)
		return
	}
	key := "flash.outside_saved"
	if !box.Offline && wantOffline {
		key = "flash.offline_on"
	}
	s.redirectSaved(w, r, connectionsPath, key)
}

// ---- switching on with a public name (Q143)

type costView struct {
	Name, Provider, Address         string
	Displays, PersonalDevices, Apps string // the names, joined by commas
	Passkeys                        []personChip
	Lead                            template.HTML
	NewAddress                      template.HTML
}

func (s *server) costOf(r *http.Request, p principal) (costView, bool, error) {
	var v costView
	saved, ok, err := s.db.PublicHTTPS()
	if err != nil || !ok || !s.publicReady() {
		return v, false, err
	}
	v.Name, v.Provider, v.Address = s.public.Name(), providerName(saved.Provider), saved.LANIP
	ds, err := s.db.Displays(p.household.ID)
	if err != nil {
		return v, false, err
	}
	var displays, personalDevices, apps []string
	for _, d := range ds {
		displays = append(displays, d.Name)
	}
	ph, err := s.db.PersonalDevices(p.household.ID)
	if err != nil {
		return v, false, err
	}
	for _, x := range ph {
		personalDevices = append(personalDevices, x.Name)
	}
	toks, err := s.deviceTokens(p.household.ID)
	if err != nil {
		return v, false, err
	}
	for _, t := range toks {
		apps = append(apps, t.Name)
	}
	v.Displays, v.PersonalDevices, v.Apps = strings.Join(displays, ", "), strings.Join(personalDevices, ", "), strings.Join(apps, ", ")
	people, err := s.db.People(p.household.ID)
	if err != nil {
		return v, false, err
	}
	var withPasskey []store.Person
	for _, pe := range people {
		if pks, err := s.db.Passkeys(pe.ID); err == nil && len(pks) > 0 {
			withPasskey = append(withPasskey, pe)
		}
	}
	if len(withPasskey) == 0 { // a box without passkeys yet: the parents who could have them
		for _, pe := range people {
			if pe.Role == store.RoleParent {
				withPasskey = append(withPasskey, pe)
			}
		}
	}
	v.Passkeys = chipsOf(withPasskey)
	lang := s.langOf(r)
	v.Lead = boldIn(s.i18n.T(lang, "outside.on.lead"), map[string]string{"{name}": v.Name, "{provider}": v.Provider})
	v.NewAddress = boldIn(s.i18n.T(lang, "outside.on.cost.address.text"), map[string]string{"{ip}": v.Address, "{name}": v.Name})
	return v, true, nil
}

// boldIn escapes a text and puts the values in place of their placeholders, each in bold.
func boldIn(text string, values map[string]string) template.HTML {
	text = html.EscapeString(text)
	for k, v := range values {
		text = strings.ReplaceAll(text, html.EscapeString(k), "<b>"+html.EscapeString(v)+"</b>")
	}
	return template.HTML(text) // #nosec G203 -- the text and the values are escaped
}

func (s *server) renderOfflineOn(w http.ResponseWriter, r *http.Request, p principal, status int, v costView, failed bool) {
	data := map[string]any{"Cost": v, "Failed": failed, "Person": p.person}
	if failed {
		data["ErrorField"] = "dns"
	}
	s.render(w, r, status, "offline_on.html", data)
}

func (s *server) handleOfflineOnPage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	v, public, err := s.costOf(r, p)
	if err != nil {
		s.fail(w, "offline cost", err)
		return
	}
	if !public || s.offline() {
		http.Redirect(w, r, connectionsPath, http.StatusSeeOther)
		return
	}
	s.renderOfflineOn(w, r, p, http.StatusOK, v, false)
}

func (s *server) handleOfflineOn(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	v, public, err := s.costOf(r, p)
	if err != nil {
		s.fail(w, "offline cost", err)
		return
	}
	if !public || s.offline() {
		http.Redirect(w, r, connectionsPath, http.StatusSeeOther)
		return
	}
	// The change can take a minute (the DNS service) and must not stop partway if the browser gives up.
	ctx, cancel := contextWithSetupTimeout(r)
	defer cancel()
	lan, err := s.leavePublicName(ctx, p.household, r.PostFormValue("delete_dns") != "")
	switch {
	case errors.Is(err, errDNS):
		s.renderOfflineOn(w, r, p, http.StatusBadGateway, v, true)
		return
	case err != nil:
		s.fail(w, "leave the public name", err)
		return
	}
	// The next page is the trust page of the box's new address: every device trusts the box again.
	s.setFlash(w, r, "flash.offline_on")
	http.Redirect(w, r, "http://"+lan+"/ca", http.StatusSeeOther)
}

// contextWithSetupTimeout is the context of a change that can take a minute (the
// DNS service) and must not stop partway if the browser gives up.
func contextWithSetupTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), setupTimeout)
}

// ---- switching off (Q141)

type backItem struct{ Name, Text template.HTML }

type backView struct {
	Now, OnUse, Stays []backItem
}

// backOnline lists what goes outside again, grouped by when.
func (s *server) backOnline(r *http.Request, p principal) (backView, error) {
	var v backView
	lang := s.langOf(r)
	T := func(k string) string { return s.i18n.T(lang, k) }
	esc := func(x string) template.HTML { return template.HTML(html.EscapeString(x)) }
	svcs, err := s.db.OutboundServices()
	if err != nil {
		return v, err
	}
	facts, err := s.outsideFacts(p.household)
	if err != nil {
		return v, err
	}
	box := s.outboundBox()
	and := T("outside.and")
	item := func(id outbound.Service, text template.HTML) backItem {
		return backItem{Name: esc(T("outside.name." + string(id))), Text: text}
	}
	stays := func(id outbound.Service) {
		v.Stays = append(v.Stays, item(id, esc(T("outside.back.was_off"))))
	}
	if svcs[outbound.Weather].Enabled {
		home := p.household.HomeName
		v.Now = append(v.Now, item(outbound.Weather, esc(s.i18n.Format(lang, i18n.Msg("outside.back.weather", "home", home, "every", s.every(lang, 15))))))
	} else {
		stays(outbound.Weather)
	}
	if svcs[outbound.Update].Enabled {
		mode := T("settings.auto_update." + string(p.household.AutoUpdate))
		v.Now = append(v.Now, item(outbound.Update, esc(s.i18n.Format(lang, i18n.Msg("outside.back.update", "mode", mode)))))
	} else {
		stays(outbound.Update)
	}
	if svcs[outbound.Calendars].Enabled {
		if len(facts.Connections) > 0 {
			var parts []string
			for _, c := range facts.Connections {
				parts = append(parts, `<span class="names">`+html.EscapeString(c.Username)+`</span> (`+html.EscapeString(connectionHost(c))+`)`)
			}
			text := html.EscapeString(T("outside.back.calendars")) + " " + joinAnd(parts, html.EscapeString(and)) + "."
			if !box.OfflineSince.IsZero() {
				text += " " + html.EscapeString(s.i18n.Format(lang, i18n.Msg("outside.back.since", "date", datefmt.Date(p.household.DateFormat, box.OfflineSince.In(p.location())))))
			}
			v.Now = append(v.Now, item(outbound.Calendars, template.HTML(text))) // #nosec G203 -- escaped
		}
	} else {
		stays(outbound.Calendars)
	}
	if svcs[outbound.ICS].Enabled {
		if len(facts.ICS) > 0 {
			var parts []string
			for _, c := range facts.ICS {
				parts = append(parts, `<span class="names">`+html.EscapeString(c.Name)+`</span> (`+html.EscapeString(hostsOf([]string{c.URL})[0])+`)`)
			}
			v.Now = append(v.Now, item(outbound.ICS, template.HTML(joinAnd(parts, html.EscapeString(and))+"."))) // #nosec G203 -- escaped
		}
	} else {
		stays(outbound.ICS)
	}
	for _, id := range []outbound.Service{outbound.Recipes, outbound.Images} {
		if svcs[id].Enabled {
			v.OnUse = append(v.OnUse, item(id, esc(T("outside.back."+string(id)))))
		} else {
			stays(id)
		}
	}
	return v, nil
}

func (s *server) handleOnlinePage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if !s.offline() {
		http.Redirect(w, r, connectionsPath, http.StatusSeeOther)
		return
	}
	v, err := s.backOnline(r, p)
	if err != nil {
		s.fail(w, "back online", err)
		return
	}
	s.render(w, r, http.StatusOK, "offline_off.html", map[string]any{"Back": v, "Person": p.person})
}

func (s *server) handleOnline(w http.ResponseWriter, r *http.Request) {
	p, ok := s.parentPage(w, r)
	if !ok {
		return
	}
	if s.offline() {
		off := false
		if err := s.applyOutside(p.household, outsideChange{Offline: &off}); err != nil {
			s.fail(w, "switch offline mode off", err)
			return
		}
	}
	s.redirectSaved(w, r, connectionsPath, "flash.offline_off")
}
