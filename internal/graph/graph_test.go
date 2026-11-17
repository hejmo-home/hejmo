package graph_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/graph"
	"github.com/hejmo-home/hejmo/internal/graph/graphtest"
)

func newClient(t *testing.T) (*graphtest.Server, *graph.Client) {
	t.Helper()
	fake := graphtest.New()
	t.Cleanup(fake.Close)
	return fake, &graph.Client{AuthBase: fake.AuthBase(), GraphBase: fake.GraphBase(), DefaultClientID: "app-1"}
}

func TestDeviceSignInReturnsACodeAndWaitsUntilApproved(t *testing.T) {
	fake, c := newClient(t)
	ctx := context.Background()
	in, err := c.StartSignIn(ctx, "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if in.UserCode != "B7KQ2M9XL" || in.VerificationURI != "https://microsoft.com/devicelogin" || in.Interval != 5*time.Second {
		t.Fatalf("sign-in = %+v", in)
	}
	if d := time.Until(in.ExpiresAt); d < 14*time.Minute || d > 15*time.Minute {
		t.Fatalf("code expires in %v, want about 15 minutes", d)
	}
	if _, err := c.PollSignIn(ctx, "app-1", in); !errors.Is(err, graph.ErrPending) {
		t.Fatalf("poll before approval = %v, want ErrPending", err)
	}
	fake.SetDeviceState("approved")
	tok, err := c.PollSignIn(ctx, "app-1", in)
	if err != nil || tok.Refresh == "" {
		t.Fatalf("poll after approval = %+v, %v", tok, err)
	}
}

func TestDeviceSignInFailuresAreTold(t *testing.T) {
	fake, c := newClient(t)
	ctx := context.Background()
	in, _ := c.StartSignIn(ctx, "app-1")
	for state, want := range map[string]error{"declined": graph.ErrDeclined, "expired": graph.ErrExpired, "consent": graph.ErrConsent} {
		fake.SetDeviceState(state)
		if _, err := c.PollSignIn(ctx, "app-1", in); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", state, err, want)
		}
	}
	fake.Set(func(s *graphtest.Server) { s.KnownClients = []string{"other"} })
	if _, err := c.StartSignIn(ctx, "app-1"); !errors.Is(err, graph.ErrUnknownApp) {
		t.Errorf("unknown app id: err = %v, want ErrUnknownApp", err)
	}
	fake.Set(func(s *graphtest.Server) { s.KnownClients = nil; s.DeviceCodeDown = true })
	if _, err := c.StartSignIn(ctx, "app-1"); err == nil || errors.Is(err, graph.ErrUnknownApp) {
		t.Errorf("Microsoft down: err = %v, want a plain error", err)
	}
}

func TestSessionKeepsTheRotatedRefreshToken(t *testing.T) {
	fake, c := newClient(t)
	ctx := context.Background()
	var saved []string
	s := c.Session("app-1", "refresh-0", func(r string) { saved = append(saved, r) })
	acc, err := s.Me(ctx)
	if err != nil || acc.Address != "mama@example.org" || acc.Name != "Mama Maier" {
		t.Fatalf("Me = %+v, %v", acc, err)
	}
	if len(saved) != 1 || saved[0] != "refresh-1" {
		t.Fatalf("saved refresh tokens = %v, want [refresh-1]", saved)
	}
	// A revoked grant is told apart from a broken network.
	fake.Set(func(s *graphtest.Server) { s.RevokeRefresh = true })
	s = c.Session("app-1", "refresh-1", func(string) {})
	if _, err := s.Me(ctx); !errors.Is(err, graph.ErrReauth) {
		t.Fatalf("revoked: err = %v, want ErrReauth", err)
	}
}

func TestCalendarsNameTheOwnerAndWhetherTheyCanBeChanged(t *testing.T) {
	fake, c := newClient(t)
	fake.AddCalendar("cal-1", "Kalender")
	fake.AddShared("cal-2", "Kalender von Papa", "Papa Maier", "papa@example.org")
	s := c.Session("app-1", "refresh-0", func(string) {})
	cals, err := s.Calendars(context.Background())
	if err != nil || len(cals) != 2 {
		t.Fatalf("calendars = %+v, %v", cals, err)
	}
	own, shared := cals[0], cals[1]
	if own.ID != "cal-1" || !own.CanEdit || own.OwnerAddress != "mama@example.org" {
		t.Errorf("own = %+v", own)
	}
	if shared.CanEdit || shared.OwnerName != "Papa Maier" || shared.OwnerAddress != "papa@example.org" {
		t.Errorf("shared = %+v", shared)
	}
}

func TestDeltaFollowsPagesReportsRemovalsAndContinuesFromTheLink(t *testing.T) {
	fake, c := newClient(t)
	fake.AddCalendar("cal-1", "Kalender")
	fake.PageSize = 2
	for i := 0; i < 5; i++ {
		fake.Put("cal-1", map[string]any{"subject": "Termin"})
	}
	s := c.Session("app-1", "refresh-0", func(string) {})
	ctx := context.Background()
	from, to := time.Now().AddDate(-1, 0, 0), time.Now().AddDate(3, 0, 0)
	res, err := s.Delta(ctx, "cal-1", "", from, to)
	if err != nil || len(res.Events) != 5 || res.Link == "" {
		t.Fatalf("first delta = %d events, link %q, %v", len(res.Events), res.Link, err)
	}
	if fake.Count("GET /v1.0/me/calendars/cal-1/calendarView/delta") != 3 {
		t.Errorf("pages fetched = %d, want 3", fake.Count("GET /v1.0/me/calendars/cal-1/calendarView/delta"))
	}
	fake.Remove("cal-1", res.Events[0].ID)
	again, err := s.Delta(ctx, "cal-1", res.Link, from, to)
	if err != nil || len(again.Events) != 1 || again.Events[0].Removed == nil || again.Events[0].ID != res.Events[0].ID {
		t.Fatalf("second delta = %+v, %v", again.Events, err)
	}
	fake.Set(func(s *graphtest.Server) { s.ExpireDeltaTokens = true })
	if _, err := s.Delta(ctx, "cal-1", again.Link, from, to); !errors.Is(err, graph.ErrDeltaExpired) {
		t.Fatalf("old link: err = %v, want ErrDeltaExpired", err)
	}
}

func TestEventWritesGoToGraph(t *testing.T) {
	fake, c := newClient(t)
	fake.AddCalendar("cal-1", "Kalender")
	s := c.Session("app-1", "refresh-0", func(string) {})
	ctx := context.Background()
	ev, err := s.CreateEvent(ctx, "cal-1", map[string]any{"subject": "Zahnarzt"})
	if err != nil || ev.ID == "" {
		t.Fatalf("create = %+v, %v", ev, err)
	}
	if _, err := s.UpdateEvent(ctx, ev.ID, map[string]any{"subject": "Zahnarzt neu"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEvent(ctx, ev.ID)
	if err != nil || got.Subject != "Zahnarzt neu" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	if err := s.DeleteEvent(ctx, ev.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetEvent(ctx, ev.ID); !errors.Is(err, graph.ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
}
