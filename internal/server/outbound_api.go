package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/update"
)

// The API of offline mode and the connections to the outside (ticket 201, ADR
// 0015: the page has an API of its own).

type outboundConnectionJSON struct {
	ID          string  `json:"id"`
	Enabled     bool    `json:"enabled"`
	Switchable  bool    `json:"switchable"`
	Locked      bool    `json:"locked"`
	LastContact *string `json:"last_contact"`
	Target      string  `json:"target,omitempty"`
}

type refusedJSON struct {
	At      string `json:"at"`
	Service string `json:"service"`
	Target  string `json:"target"`
}

type outboundJSON struct {
	Offline      bool                     `json:"offline"`
	OfflineSince *string                  `json:"offline_since"`
	ReminderDays int                      `json:"update_reminder_days"`
	PublicName   *string                  `json:"public_name"`
	Connections  []outboundConnectionJSON `json:"connections"`
	Refused      []refusedJSON            `json:"refused"`
}

func (s *server) outboundState() (outboundJSON, error) {
	box := s.outboundBox()
	svcs, err := s.db.OutboundServices()
	if err != nil {
		return outboundJSON{}, err
	}
	out := outboundJSON{Offline: box.Offline, ReminderDays: box.ReminderDays, Connections: []outboundConnectionJSON{}, Refused: []refusedJSON{}}
	if !box.OfflineSince.IsZero() {
		t := box.OfflineSince.UTC().Format(time.RFC3339)
		out.OfflineSince = &t
	}
	if s.publicReady() {
		n := s.public.Name()
		out.PublicName = &n
	}
	st := update.ReadStatus(s.updateDir)
	for _, id := range outbound.Services {
		sv := svcs[id]
		c := outboundConnectionJSON{ID: string(id), Enabled: sv.Enabled && !box.Offline, Switchable: id != outbound.DNS, Locked: box.Offline, Target: sv.LastTarget}
		if id == outbound.DNS {
			c.Enabled = !box.Offline && out.PublicName != nil
		}
		last := sv.LastContact
		if id == outbound.Update {
			if t, err := time.Parse(time.RFC3339, st.Checked); err == nil && t.After(last) {
				last = t
			}
		}
		if !last.IsZero() {
			t := last.UTC().Format(time.RFC3339)
			c.LastContact = &t
		}
		out.Connections = append(out.Connections, c)
	}
	refused, err := s.db.RefusedAttempts(store.MaxRefusedKept)
	if err != nil {
		return outboundJSON{}, err
	}
	for _, a := range refused {
		out.Refused = append(out.Refused, refusedJSON{At: a.At.UTC().Format(time.RFC3339), Service: string(a.Service), Target: a.Target})
	}
	return out, nil
}

func (s *server) apiGetOutbound(w http.ResponseWriter, _ *http.Request, _ principal) {
	out, err := s.outboundState()
	if err != nil {
		s.fail(w, "outbound", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) apiPatchOutbound(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		Offline      *bool           `json:"offline"`
		Connections  map[string]bool `json:"connections"`
		ReminderDays *int            `json:"update_reminder_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	change := outsideChange{Offline: in.Offline, Days: in.ReminderDays}
	if len(in.Connections) > 0 {
		change.Services = map[outbound.Service]bool{}
		for id, on := range in.Connections {
			svc := outbound.Service(id)
			var known bool
			for _, v := range switchable {
				known = known || v == svc
			}
			if !known {
				writeFieldError(w, http.StatusUnprocessableEntity, "connections", fmt.Sprintf("%q is not a connection with a switch; they are %s", id, switchableNames()))
				return
			}
			change.Services[svc] = on
		}
	}
	err := s.applyOutside(p.household, change)
	switch {
	case errors.Is(err, errLocked):
		writeError(w, http.StatusConflict, "offline mode is on: the single connections are locked; switch offline mode off first")
		return
	case errors.Is(err, errPublicName):
		writeError(w, http.StatusConflict, "the box has a public name, which needs the internet: use POST /outbound/offline to change to the home network and go offline")
		return
	case errors.Is(err, store.ErrInvalid):
		writeFieldError(w, http.StatusUnprocessableEntity, "update_reminder_days", err.Error())
		return
	case err != nil:
		s.fail(w, "outbound", err)
		return
	}
	s.apiGetOutbound(w, r, p)
}

func switchableNames() string {
	var out string
	for i, v := range switchable {
		if i > 0 {
			out += ", "
		}
		out += string(v)
	}
	return out
}

// apiGoOffline is the change of the web page "Switch on offline mode": it
// leaves a public name (deleting the DNS records first, if asked), makes the
// box's own certificate and switches offline mode on. Without a public name it
// just switches offline mode on.
func (s *server) apiGoOffline(w http.ResponseWriter, r *http.Request, p principal) {
	var in struct {
		DeleteDNS bool `json:"delete_dns"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}
	if s.publicReady() {
		ctx, cancel := contextWithSetupTimeout(r)
		defer cancel()
		if _, err := s.leavePublicName(ctx, p.household, in.DeleteDNS); err != nil {
			if errors.Is(err, errDNS) {
				writeError(w, http.StatusBadGateway, "the DNS service did not delete the records. Nothing was changed: try again, or send delete_dns false and delete the records yourself later")
				return
			}
			s.fail(w, "leave the public name", err)
			return
		}
	} else if err := s.applyOutside(p.household, outsideChange{Offline: ptr(true)}); err != nil {
		s.fail(w, "offline", err)
		return
	}
	s.apiGetOutbound(w, r, p)
}

func ptr[T any](v T) *T { return &v }

// refuseOff answers 409 and returns true if connection svc is off: the request
// would have to reach the outside. The body says where to see why.
func (s *server) refuseOff(w http.ResponseWriter, svc outbound.Service) bool {
	switch s.gate.Verdict(svc) {
	case outbound.VerdictAllowed:
		return false
	case outbound.VerdictOffline:
		writeError(w, http.StatusConflict, "offline mode is on: the box makes no connection to the outside (see GET /outbound)")
	default:
		writeError(w, http.StatusConflict, "the connection \""+string(svc)+"\" is switched off (see GET /outbound)")
	}
	return true
}
