package store_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/store"
)

var offT0 = time.Date(2026, 11, 16, 21, 4, 12, 0, time.UTC)

func TestAFreshBoxIsOnlineWithEveryConnectionOn(t *testing.T) {
	db, _ := openTemp(t)
	box, err := db.OutboundBox()
	if err != nil {
		t.Fatal(err)
	}
	if box.Offline || box.Asked || box.ReminderDays != 90 || box.UpdatedAt.IsZero() {
		t.Errorf("box = %+v, want online, not asked, 90 days, with an install date", box)
	}
	p := db.Outbound()
	for _, s := range outbound.Services {
		if v := p.Verdict(s); v != outbound.VerdictAllowed {
			t.Errorf("%s: verdict %v on a fresh box", s, v)
		}
	}
}

func TestOfflineModeRefusesEverythingAndKeepsTheSwitchesAsTheyWere(t *testing.T) {
	db, _ := openTemp(t)
	p := db.Outbound()
	if err := db.SetServiceEnabled(outbound.Images, false); err != nil {
		t.Fatal(err)
	}
	if err := db.SetOffline(true, offT0); err != nil {
		t.Fatal(err)
	}
	for _, s := range append(outbound.Services, outbound.Other) {
		if v := p.Verdict(s); v != outbound.VerdictOffline {
			t.Errorf("%s: verdict %v, want offline", s, v)
		}
	}
	box, _ := db.OutboundBox()
	if !box.Offline || !box.OfflineSince.Equal(offT0) {
		t.Errorf("box = %+v", box)
	}
	// Switching on again does not move the time it was switched on.
	if err := db.SetOffline(true, offT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if box, _ := db.OutboundBox(); !box.OfflineSince.Equal(offT0) {
		t.Errorf("offline since moved to %v", box.OfflineSince)
	}
	// Off again: the switches are as they were; web images stayed off.
	if err := db.SetOffline(false, offT0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if v := p.Verdict(outbound.Weather); v != outbound.VerdictAllowed {
		t.Errorf("weather after offline mode: %v", v)
	}
	if v := p.Verdict(outbound.Images); v != outbound.VerdictSwitchedOff {
		t.Errorf("web images after offline mode: %v, want still off", v)
	}
	if box, _ := db.OutboundBox(); box.Offline || !box.OfflineSince.IsZero() {
		t.Errorf("box = %+v", box)
	}
}

func TestASingleSwitchRefusesOnlyThatConnection(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.SetServiceEnabled(outbound.Calendars, false); err != nil {
		t.Fatal(err)
	}
	p := db.Outbound()
	if v := p.Verdict(outbound.Calendars); v != outbound.VerdictSwitchedOff {
		t.Errorf("calendars = %v", v)
	}
	if v := p.Verdict(outbound.Weather); v != outbound.VerdictAllowed {
		t.Errorf("weather = %v", v)
	}
	if err := db.SetServiceEnabled(outbound.DNS, false); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("the DNS service has no switch of its own: %v", err)
	}
	if err := db.SetServiceEnabled("nonsense", true); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("unknown connection: %v", err)
	}
}

func TestTheLastContactIsKeptPerConnection(t *testing.T) {
	db, _ := openTemp(t)
	l := db.Outbound()
	l.Contacted(offT0, outbound.Weather, "api.open-meteo.com")
	l.Contacted(offT0.Add(time.Hour), outbound.Weather, "api.open-meteo.com")
	svcs, err := db.OutboundServices()
	if err != nil {
		t.Fatal(err)
	}
	w := svcs[outbound.Weather]
	if !w.LastContact.Equal(offT0.Add(time.Hour)) || w.LastTarget != "api.open-meteo.com" || !w.Enabled {
		t.Errorf("weather = %+v", w)
	}
	if got := svcs[outbound.Calendars]; !got.LastContact.IsZero() || !got.Enabled {
		t.Errorf("calendars = %+v, want never contacted and on", got)
	}
	if len(svcs) != len(outbound.Services) {
		t.Errorf("services = %d, want every listed connection", len(svcs))
	}
}

func TestRefusedAttemptsComeNewestFirstAndAreCapped(t *testing.T) {
	db, _ := openTemp(t)
	l := db.Outbound()
	l.Refused(offT0, outbound.Weather, "api.open-meteo.com")
	l.Refused(offT0.Add(time.Minute), outbound.Calendars, "p12-caldav.icloud.com")
	got, err := db.RefusedAttempts(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Service != outbound.Calendars || got[0].Target != "p12-caldav.icloud.com" || !got[0].At.Equal(offT0.Add(time.Minute)) || got[1].Service != outbound.Weather {
		t.Fatalf("attempts = %+v", got)
	}
	for i := 0; i < store.MaxRefusedKept+20; i++ {
		l.Refused(offT0.Add(time.Duration(i)*time.Second), outbound.Other, fmt.Sprintf("host%d.example", i))
	}
	all, _ := db.RefusedAttempts(10000)
	if len(all) != store.MaxRefusedKept {
		t.Errorf("kept %d attempts, want %d", len(all), store.MaxRefusedKept)
	}
	if err := db.ClearRefusedAttempts(); err != nil {
		t.Fatal(err)
	}
	if all, _ := db.RefusedAttempts(10); len(all) != 0 {
		t.Errorf("after clearing: %d", len(all))
	}
}

func TestTheWizardAnswerIsKeptBeforeAnyHouseholdExists(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.AnswerOfflineQuestion(true, offT0); err != nil {
		t.Fatal(err)
	}
	box, _ := db.OutboundBox()
	if !box.Asked || !box.Offline || !box.OfflineSince.Equal(offT0) {
		t.Errorf("box = %+v", box)
	}
	// A box set up offline has no earlier state: every switch is on.
	if v := db.Outbound().Verdict(outbound.Weather); v != outbound.VerdictOffline {
		t.Errorf("verdict = %v", v)
	}
	if err := db.SetOffline(false, offT0); err != nil {
		t.Fatal(err)
	}
	for _, s := range outbound.Services {
		if v := db.Outbound().Verdict(s); v != outbound.VerdictAllowed {
			t.Errorf("%s after switching off: %v, want on", s, v)
		}
	}
	db2, _ := openTemp(t)
	if err := db2.AnswerOfflineQuestion(false, offT0); err != nil {
		t.Fatal(err)
	}
	if box, _ := db2.OutboundBox(); !box.Asked || box.Offline {
		t.Errorf("box = %+v", box)
	}
}

func TestTheUpdateReminderDaysAreBounded(t *testing.T) {
	db, _ := openTemp(t)
	for _, n := range []int{6, 366, 0, -1} {
		if err := db.SetUpdateReminderDays(n); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%d days: %v, want ErrInvalid", n, err)
		}
	}
	if err := db.SetUpdateReminderDays(30); err != nil {
		t.Fatal(err)
	}
	if box, _ := db.OutboundBox(); box.ReminderDays != 30 {
		t.Errorf("days = %d", box.ReminderDays)
	}
}

func TestTheDateOfTheLastUpdateOnlyMovesForward(t *testing.T) {
	db, _ := openTemp(t)
	if err := db.NoteUpdated(offT0); err != nil {
		t.Fatal(err)
	}
	if err := db.NoteUpdated(offT0.Add(-24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if box, _ := db.OutboundBox(); !box.UpdatedAt.Equal(offT0) {
		t.Errorf("updated at %v, want %v", box.UpdatedAt, offT0)
	}
	if err := db.NoteUpdated(offT0.Add(24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if box, _ := db.OutboundBox(); !box.UpdatedAt.Equal(offT0.Add(24 * time.Hour)) {
		t.Errorf("updated at %v", box.UpdatedAt)
	}
}
