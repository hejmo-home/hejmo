package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/store"
)

func sharedByID(t *testing.T, f calFixture) map[string]bool {
	t.Helper()
	cals, err := f.db.Calendars(f.h.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range cals {
		out[c.ID] = c.Shared
	}
	return out
}

// ADR 0012: the family calendar is always served, ICS calendars are shared by
// default.
func TestSharedThroughTheBoxDefaults(t *testing.T) {
	f := newCalFixture(t)
	ics := newICS(t, f)
	shared := sharedByID(t, f)
	if !shared[f.cal.ID] || !shared[ics.ID] {
		t.Fatalf("shared = %v, want family and ICS calendars shared", shared)
	}
}

func TestSharingCanBeSwitchedOffForSyncedCalendarsOnly(t *testing.T) {
	f := newCalFixture(t)
	ics := newICS(t, f)
	if err := f.db.SetCalendarShared(f.h.ID, ics.ID, false); err != nil {
		t.Fatal(err)
	}
	if sharedByID(t, f)[ics.ID] {
		t.Fatal("ICS calendar still shared")
	}
	if err := f.db.SetCalendarShared(f.h.ID, ics.ID, true); err != nil || !sharedByID(t, f)[ics.ID] {
		t.Fatalf("switching on again: %v", err)
	}
	if err := f.db.SetCalendarShared(f.h.ID, f.cal.ID, false); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("family calendar can always be served, got %v", err)
	}
	if err := f.db.SetCalendarShared(f.h.ID, "nope", true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown calendar: %v", err)
	}
}

func TestCalendarEventsReturnsSeriesWithChangedOccurrencesAndModTime(t *testing.T) {
	f := newCalFixture(t)
	master, _ := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: f.cal.ID, Title: "Choir", Start: at(2026, 11, 3, 17, 0), End: at(2026, 11, 3, 18, 0), RRule: "FREQ=WEEKLY"})
	_, err := f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: f.cal.ID, Title: "Choir B", SeriesID: master.ID, RecurrenceID: at(2026, 11, 10, 17, 0),
		Start: at(2026, 11, 10, 18, 0), End: at(2026, 11, 10, 19, 0)})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := f.db.CreateCalendar(store.Calendar{HouseholdID: f.h.ID, Name: "Other", Kind: "local"})
	_, _ = f.db.CreateEvent(f.h.ID, calendar.Event{CalendarID: other.ID, Title: "Elsewhere", Start: at(2026, 11, 4, 9, 0), End: at(2026, 11, 4, 10, 0)})

	evs, err := f.db.CalendarEvents(f.h.ID, f.cal.ID)
	if err != nil || len(evs) != 2 {
		t.Fatalf("CalendarEvents = %+v, %v", evs, err)
	}
	for _, e := range evs {
		if e.UpdatedAt.IsZero() {
			t.Fatalf("%s has no UpdatedAt", e.Title)
		}
	}
}

func TestAPITokenListShowsActiveTokensWithOwnerAndUse(t *testing.T) {
	f := newCalFixture(t)
	keep, _ := f.db.CreateAPIToken(store.APIToken{Name: "iPhone Mama", Hash: "h1", PersonID: f.mama.ID, Scopes: "calendars"})
	gone, _ := f.db.CreateAPIToken(store.APIToken{Name: "Old", Hash: "h2", PersonID: f.mama.ID, Scopes: "calendars"})
	if err := f.db.RevokeAPIToken(gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.TouchAPIToken(keep.ID, t0); err != nil {
		t.Fatal(err)
	}
	list, err := f.db.APITokens(f.h.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("APITokens = %+v, %v", list, err)
	}
	got := list[0]
	if got.Name != "iPhone Mama" || got.PersonName != "Mama" || got.Scopes != "calendars" || !got.LastUsed.Equal(t0) || got.Hash != "" {
		t.Fatalf("token = %+v", got)
	}
}
