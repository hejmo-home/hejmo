package server

import (
	"context"
	"errors"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/googleauth"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/update"
)

// Offline mode and the connections to the outside (ticket 201, Q141 to Q146):
// the state, the changes parents make to it and the notices that say a page's
// feature is off. The gate itself is package outbound.

// switchable are the connections with a switch of their own, in the order of the page.
var switchable = []outbound.Service{outbound.Weather, outbound.Update, outbound.Calendars, outbound.ICS, outbound.Recipes, outbound.Images}

const (
	connectionsPath = "/settings/connections"
	// guideUpdateAnchor is the key of the address of the install guide's chapter "Update without the internet".
	guideUpdateAnchor = "outside.updates.guide.url"
)

// offline reports whether offline mode is on.
func (s *server) offline() bool { return s.gate.Verdict(outbound.Other) == outbound.VerdictOffline }

// outboundBox is the box's state about connections to the outside.
func (s *server) outboundBox() store.OutboundBox {
	box, err := s.db.OutboundBox()
	if err != nil {
		s.logger.Error("outbound state", "err", err)
	}
	return box
}

// writeUpdateSettings tells the host script the household's mode, and to stay
// offline while offline mode is on or the update check is switched off.
func (s *server) writeUpdateSettings(mode store.UpdateMode) {
	if err := update.WriteState(s.updateDir, string(mode), !s.gate.Allowed(outbound.Update)); err != nil {
		s.logger.Error("write update settings", "err", err)
	}
}

// noteUpdated remembers an update the host script reported, for the reminder.
func (s *server) noteUpdated() {
	st := update.ReadStatus(s.updateDir)
	if st.State != update.Updated || st.Time == "" {
		return
	}
	if t, err := time.Parse(time.RFC3339, st.Time); err == nil {
		if err := s.db.NoteUpdated(t); err != nil {
			s.logger.Error("note update", "err", err)
		}
	}
}

// ---- changes

var (
	errLocked     = errors.New("the single connections are locked while offline mode is on")
	errPublicName = errors.New("offline mode needs the change to the home network first")
)

// outsideChange is what a parent changes on the page or through the API: nil
// and empty mean "leave as it is".
type outsideChange struct {
	Offline  *bool
	Services map[outbound.Service]bool
	Days     *int
}

// applyOutside makes a change, or refuses it whole: errLocked, errPublicName or
// a store.ErrInvalid for something wrong.
func (s *server) applyOutside(h store.Household, c outsideChange) error {
	box := s.outboundBox()
	goingOn := c.Offline != nil && *c.Offline && !box.Offline
	goingOff := c.Offline != nil && !*c.Offline && box.Offline
	stillOffline := box.Offline && !goingOff
	if stillOffline && len(c.Services) > 0 {
		return errLocked
	}
	if goingOn && s.publicReady() {
		return errPublicName
	}
	for svc := range c.Services {
		if !svc.Valid() || svc == outbound.DNS {
			return fmt.Errorf("%w: %q has no switch", store.ErrInvalid, svc)
		}
	}
	if c.Days != nil && (*c.Days < store.MinReminderDays || *c.Days > store.MaxReminderDays) {
		return fmt.Errorf("%w: the reminder is between %d and %d days", store.ErrInvalid, store.MinReminderDays, store.MaxReminderDays)
	}
	for svc, on := range c.Services {
		if err := s.db.SetServiceEnabled(svc, on); err != nil {
			return err
		}
	}
	if c.Days != nil {
		if err := s.db.SetUpdateReminderDays(*c.Days); err != nil {
			return err
		}
	}
	if goingOn || goingOff {
		if err := s.db.SetOffline(goingOn, s.now()); err != nil {
			return err
		}
	}
	s.writeUpdateSettings(h.AutoUpdate)
	return nil
}

// recordDeleter is a public certificate manager that can delete the box's records at the DNS service.
type recordDeleter interface {
	DeleteRecords(context.Context) error
}

// errDNS means the DNS service refused to delete the records: nothing was changed.
var errDNS = errors.New("the DNS service did not delete the records")

// leavePublicName changes to "only in the home network" and switches offline
// mode on, in the order the page promises: delete the DNS records (if asked),
// forget the public name, make the box's own CA for its address, switch on.
// It returns the box's address at home.
func (s *server) leavePublicName(ctx context.Context, h store.Household, deleteDNS bool) (string, error) {
	saved, ok, err := s.db.PublicHTTPS()
	if err != nil {
		return "", err
	}
	if !ok || saved.LANIP == "" {
		return "", errors.New("the box has no address at home to change to")
	}
	if deleteDNS {
		if d, ok := s.public.(recordDeleter); ok {
			if err := d.DeleteRecords(ctx); err != nil {
				s.logger.Warn("delete DNS records", "err", err)
				return saved.LANIP, errors.Join(errDNS, err)
			}
		}
	}
	if err := s.public.Clear(); err != nil {
		return saved.LANIP, err
	}
	if err := s.pki.Reprovision(saved.LANIP, s.now()); err != nil {
		return saved.LANIP, err
	}
	if err := s.db.SetOffline(true, s.now()); err != nil {
		return saved.LANIP, err
	}
	s.writeUpdateSettings(h.AutoUpdate)
	return saved.LANIP, nil
}

// ---- what the page shows

// outsideRow is one connection on the page.
type outsideRow struct {
	ID, Name  string
	State     string // in words
	On        bool   // the state word is "On"
	Locked    bool   // offline mode: off and not movable
	HasSwitch bool
	Checked   bool
	Purpose   template.HTML
	Goes      string
	Last      string // time with seconds; empty: never
	Before    bool   // the last contact was before offline mode
	Offer     bool   // the DNS row offers the change to the home network
}

// names is a list of names of things in the household, each in its own span.
func names(list []string, and string) template.HTML {
	var b strings.Builder
	for i, n := range list {
		switch {
		case i > 0 && i == len(list)-1:
			b.WriteString(" " + html.EscapeString(and) + " ")
		case i > 0:
			b.WriteString(", ")
		}
		b.WriteString(`<span class="names">` + html.EscapeString(n) + `</span>`)
	}
	return template.HTML(b.String()) // #nosec G203 -- every name is escaped
}

// hostsOf lists the distinct hosts of addresses, in order.
func hostsOf(addrs []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range addrs {
		h := a
		if u, err := url.Parse(a); err == nil && u.Host != "" {
			h = u.Host
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

func joinAnd(list []string, and string) string {
	switch len(list) {
	case 0:
		return ""
	case 1:
		return list[0]
	}
	return strings.Join(list[:len(list)-1], ", ") + " " + and + " " + list[len(list)-1]
}

// outsideFacts are the household's things that go outside: what the rows and the
// confirmation pages name.
type outsideFacts struct {
	Connections []store.Connection
	ConnLabels  []string // the user name of each connection
	ConnHosts   []string
	ICS         []store.SyncedCalendar
	ICSHosts    []string
	CalMinutes  int // the shortest interval of a connection's calendars, 0 if none
	ICSMinutes  int
}

func (s *server) outsideFacts(h store.Household) (outsideFacts, error) {
	var f outsideFacts
	conns, err := s.db.Connections(h.ID)
	if err != nil {
		return f, err
	}
	cals, err := s.db.SyncedCalendars(h.ID)
	if err != nil {
		return f, err
	}
	f.Connections = conns
	var addrs []string
	google := false
	for _, c := range conns {
		f.ConnLabels = append(f.ConnLabels, c.Username)
		addrs = append(addrs, c.URL)
		if c.Kind == "google" && !google {
			// The calendar address is only one of the three places a Google account is asked.
			google = true
			addrs = append(addrs, googleauth.DefaultEndpoints.Token, googleauth.DefaultEndpoints.CalendarList)
		}
	}
	f.ConnHosts = hostsOf(addrs)
	addrs = nil
	for _, c := range cals {
		switch c.Kind {
		case "ics":
			f.ICS = append(f.ICS, c)
			addrs = append(addrs, c.URL)
			if m := c.Sync.IntervalMinutes; m > 0 && (f.ICSMinutes == 0 || m < f.ICSMinutes) {
				f.ICSMinutes = m
			}
		case "caldav", "google":
			if m := c.Sync.IntervalMinutes; m > 0 && (f.CalMinutes == 0 || m < f.CalMinutes) {
				f.CalMinutes = m
			}
		}
	}
	f.ICSHosts = hostsOf(addrs)
	return f, nil
}

// every says how often something is asked: "every 15 minutes", "every 6 hours".
func (s *server) every(lang string, minutes int) string {
	if minutes >= 60 && minutes%60 == 0 {
		return s.i18n.Format(lang, i18n.Msg("outside.every.hours", "n", fmt.Sprint(minutes/60)))
	}
	return s.i18n.Format(lang, i18n.Msg("outside.every.minutes", "n", fmt.Sprint(minutes)))
}

func (s *server) hostLine(lang string, hosts []string, minutes int) string {
	line := joinAnd(hosts, s.i18n.T(lang, "outside.and"))
	if minutes > 0 {
		line += ", " + s.every(lang, minutes)
	}
	return line
}

// providerName is the name of the DNS provider as the texts use it.
func providerName(id string) string {
	for _, p := range domainProviders {
		if p.ID == id {
			return p.Name
		}
	}
	return id
}

// outsideRows builds the rows of the page.
func (s *server) outsideRows(r *http.Request, p principal, box store.OutboundBox, upd update.Status) ([]outsideRow, error) {
	lang := s.langOf(r)
	T := func(k string) string { return s.i18n.T(lang, k) }
	svcs, err := s.db.OutboundServices()
	if err != nil {
		return nil, err
	}
	facts, err := s.outsideFacts(p.household)
	if err != nil {
		return nil, err
	}
	and := T("outside.and")
	var rows []outsideRow
	for _, id := range outbound.Services {
		sv := svcs[id]
		row := outsideRow{ID: string(id), Name: T("outside.name." + string(id)), HasSwitch: id != outbound.DNS}
		last := sv.LastContact
		if id == outbound.Update {
			if t, err := time.Parse(time.RFC3339, upd.Checked); err == nil && t.After(last) {
				last = t
			}
		}
		if !last.IsZero() {
			row.Last = datefmt.DateTimeSeconds(p.household.DateFormat, p.household.TimeFormat, last.In(p.location()))
			row.Before = box.Offline
		}
		row.Checked = sv.Enabled && !box.Offline
		row.Locked = box.Offline
		switch {
		case !row.HasSwitch:
		case box.Offline:
			row.State = T("outside.row.locked")
		case sv.Enabled:
			row.State, row.On = T("outside.row.on"), true
		default:
			row.State = T("outside.row.off")
			if k := "outside.row.off." + string(id); s.i18n.T(lang, k) != k {
				row.State = T(k)
			}
		}
		switch id {
		case outbound.Weather:
			row.Purpose = template.HTML(html.EscapeString(T("outside.weather.for")))
			if h := p.household.HomeName; h != "" {
				row.Purpose = template.HTML(html.EscapeString(s.i18n.Format(lang, i18n.Msg("outside.weather.for_home", "home", h))))
			}
			row.Goes = s.hostLine(lang, []string{"api.open-meteo.com"}, 15)
		case outbound.Update:
			row.Purpose = template.HTML(html.EscapeString(T("outside.update.for")))
			row.Goes = T("outside.update.to")
		case outbound.Calendars:
			if len(facts.ConnLabels) == 0 {
				row.Purpose = template.HTML(html.EscapeString(T("outside.calendars.for_none")))
				row.Goes = T("outside.calendars.to_none")
				break
			}
			var b strings.Builder
			b.WriteString(html.EscapeString(T("outside.calendars.for")) + " ")
			for i, c := range facts.Connections {
				if i > 0 {
					b.WriteString(" " + html.EscapeString(and) + " ")
				}
				b.WriteString(string(names([]string{c.Username}, and)) + " (" + html.EscapeString(s.serviceName(r, c)) + ")")
			}
			b.WriteString(".")
			row.Purpose = template.HTML(b.String()) // #nosec G203 -- every part is escaped
			row.Goes = s.hostLine(lang, facts.ConnHosts, facts.CalMinutes)
		case outbound.ICS:
			if len(facts.ICS) == 0 {
				row.Purpose = template.HTML(html.EscapeString(T("outside.ics.for_none")))
				row.Goes = T("outside.ics.to_none")
				break
			}
			var list []string
			for _, c := range facts.ICS {
				list = append(list, c.Name)
			}
			row.Purpose = template.HTML(html.EscapeString(T("outside.ics.for")) + " " + string(names(list, and)) + ".") // #nosec G203 -- escaped
			row.Goes = s.hostLine(lang, facts.ICSHosts, facts.ICSMinutes)
		case outbound.Recipes:
			row.Purpose = template.HTML(html.EscapeString(T("outside.recipes.for")))
			row.Goes = T("outside.recipes.to")
		case outbound.Images:
			row.Purpose = template.HTML(html.EscapeString(T("outside.images.for")))
			row.Goes = T("outside.images.to")
		case outbound.DNS:
			row.Purpose = template.HTML(html.EscapeString(T("outside.dns.for")))
			row.State = T("outside.dns.own")
			if s.publicReady() {
				provider := ""
				if saved, ok, _ := s.db.PublicHTTPS(); ok {
					provider = providerName(saved.Provider)
				}
				row.State = s.i18n.Format(lang, i18n.Msg("outside.dns.public", "name", s.public.Name(), "provider", provider))
				row.Offer = true
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// refusedRow is one refused attempt on the page.
type refusedRow struct{ At, Name, Target string }

func (s *server) refusedRows(r *http.Request, p principal) ([]refusedRow, error) {
	list, err := s.db.RefusedAttempts(store.MaxRefusedKept)
	if err != nil {
		return nil, err
	}
	lang := s.langOf(r)
	var out []refusedRow
	for _, a := range list {
		name := s.i18n.T(lang, "outside.name."+string(a.Service))
		out = append(out, refusedRow{At: datefmt.DateTimeSeconds(p.household.DateFormat, p.household.TimeFormat, a.At.In(p.location())), Name: name, Target: a.Target})
	}
	return out, nil
}

// ---- notices

// offlineNote is the notice that a page's feature is off, with the link that
// changes it. Offline mode says so for every page; a single connection that is off
// says which.
type offlineNote struct {
	Lead, Text, Href string
}

// noteFor is the notice for a page whose feature needs connection svc: the
// text key says what this page cannot do now. Nil if the connection is on.
func (s *server) noteFor(r *http.Request, svc outbound.Service, textKey string) *offlineNote {
	lang := s.langOf(r)
	switch s.gate.Verdict(svc) {
	case outbound.VerdictOffline:
		return &offlineNote{Lead: s.i18n.T(lang, "outside.note.lead"), Text: s.i18n.T(lang, textKey), Href: connectionsPath}
	case outbound.VerdictSwitchedOff:
		return &offlineNote{Lead: s.i18n.Format(lang, i18n.Msg("outside.note.lead_one", "name", s.i18n.T(lang, "outside.name."+string(svc)))), Text: s.i18n.T(lang, textKey), Href: connectionsPath + "#" + string(svc)}
	}
	return nil
}

// homeNote is the notice on the parents' Home and the Settings hub.
func (s *server) homeNote(r *http.Request, person store.Person) *offlineNote {
	if person.Role != store.RoleParent || !s.offline() {
		return nil
	}
	lang := s.langOf(r)
	return &offlineNote{Lead: s.i18n.T(lang, "outside.note.lead"), Text: s.i18n.T(lang, "outside.note.home"), Href: connectionsPath}
}

// staleUpdate is the reminder on the parents' Home: offline, and for longer
// than the chosen days without an update (Q145).
type staleUpdate struct {
	Lead, Text, Link, GuideURL string
}

func (s *server) staleUpdate(r *http.Request, person store.Person, h store.Household) *staleUpdate {
	if person.Role != store.RoleParent || !s.offline() {
		return nil
	}
	s.noteUpdated()
	box := s.outboundBox()
	if box.UpdatedAt.IsZero() {
		return nil
	}
	days := int(s.now().Sub(box.UpdatedAt).Hours() / 24)
	if days < box.ReminderDays {
		return nil
	}
	lang := s.langOf(r)
	loc := time.UTC
	if l, err := time.LoadLocation(h.Timezone); err == nil {
		loc = l
	}
	date := datefmt.Date(h.DateFormat, box.UpdatedAt.In(loc))
	st := update.ReadStatus(s.updateDir)
	u := &staleUpdate{GuideURL: s.i18n.T(lang, guideUpdateAnchor)}
	u.Lead = s.i18n.Format(lang, i18n.Msg("outside.stale.lead", "n", fmt.Sprint(days)))
	key := "outside.stale.text"
	if st.Version == "" {
		key = "outside.stale.text_noversion"
	}
	u.Text = s.i18n.Format(lang, i18n.Msg(key, "version", st.Version, "date", date))
	u.Link = s.i18n.T(lang, "outside.updates.guide")
	return u
}
