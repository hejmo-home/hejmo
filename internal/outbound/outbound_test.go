package outbound_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
)

// fakePolicy and ledger stand in for the database.
type fakePolicy struct {
	offline bool
	off     map[outbound.Service]bool
}

func (p fakePolicy) Verdict(s outbound.Service) outbound.Verdict {
	switch {
	case p.offline:
		return outbound.VerdictOffline
	case p.off[s]:
		return outbound.VerdictSwitchedOff
	}
	return outbound.VerdictAllowed
}

type entry struct {
	Service outbound.Service
	Target  string
}

type ledger struct {
	mu       sync.Mutex
	refused  []entry
	contacts []entry
}

func (l *ledger) Refused(_ time.Time, s outbound.Service, target string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refused = append(l.refused, entry{s, target})
}

func (l *ledger) Contacted(_ time.Time, s outbound.Service, target string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.contacts = append(l.contacts, entry{s, target})
}

// outside is a server that counts what reaches it.
func outside(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func get(t *testing.T, c *http.Client, u string) error {
	t.Helper()
	res, err := c.Get(u)
	if err == nil {
		res.Body.Close()
	}
	return err
}

func TestOfflineModeRefusesAndRecordsTheAttempt(t *testing.T) {
	srv, hits := outside(t)
	l := &ledger{}
	g := outbound.New(fakePolicy{offline: true}, l)

	err := get(t, g.Client(outbound.Weather, time.Second), srv.URL+"/forecast?lat=48.1")

	var refused *outbound.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want a RefusedError", err)
	}
	if refused.Service != outbound.Weather || refused.Verdict != outbound.VerdictOffline {
		t.Errorf("refused = %+v", refused)
	}
	if hits.Load() != 0 {
		t.Error("the request reached the server although offline mode is on")
	}
	u, _ := url.Parse(srv.URL)
	if len(l.refused) != 1 || l.refused[0] != (entry{outbound.Weather, u.Host}) {
		t.Errorf("refused attempts = %+v, want one for weather at %s", l.refused, u.Host)
	}
	if len(l.contacts) != 0 {
		t.Errorf("contacts = %+v, want none", l.contacts)
	}
}

func TestASwitchedOffConnectionIsRefusedAndTheOthersAreNot(t *testing.T) {
	srv, hits := outside(t)
	l := &ledger{}
	g := outbound.New(fakePolicy{off: map[outbound.Service]bool{outbound.Calendars: true}}, l)

	err := get(t, g.Client(outbound.Calendars, time.Second), srv.URL)
	var refused *outbound.RefusedError
	if !errors.As(err, &refused) || refused.Verdict != outbound.VerdictSwitchedOff {
		t.Fatalf("calendars: error = %v, want refused (switched off)", err)
	}
	if err := get(t, g.Client(outbound.Weather, time.Second), srv.URL); err != nil {
		t.Fatalf("weather is on and must connect: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1 (only weather)", hits.Load())
	}
}

func TestAnAllowedConnectionRecordsItsLastContact(t *testing.T) {
	srv, _ := outside(t)
	l := &ledger{}
	g := outbound.New(fakePolicy{}, l)
	if err := get(t, g.Client(outbound.ICS, time.Second), srv.URL); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(srv.URL)
	if len(l.contacts) != 1 || l.contacts[0] != (entry{outbound.ICS, u.Host}) {
		t.Errorf("contacts = %+v, want one for ics at %s", l.contacts, u.Host)
	}
	if len(l.refused) != 0 {
		t.Errorf("refused = %+v", l.refused)
	}
}

func TestARedirectToAnotherHostIsCheckedToo(t *testing.T) {
	target, hits := outside(t)
	hop := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	t.Cleanup(hop.Close)
	l := &ledger{}
	// The policy lets the first request pass and refuses the second.
	pol := &flipPolicy{}
	g := outbound.New(pol, l)
	pol.after = func() { pol.offline.Store(true) }
	err := get(t, g.Client(outbound.Images, time.Second), hop.URL)
	if err == nil || hits.Load() != 0 {
		t.Fatalf("redirect was followed to the target: err=%v hits=%d", err, hits.Load())
	}
}

type flipPolicy struct {
	offline atomic.Bool
	after   func()
	n       atomic.Int32
}

func (p *flipPolicy) Verdict(outbound.Service) outbound.Verdict {
	if p.n.Add(1) > 1 || p.offline.Load() {
		return outbound.VerdictOffline
	}
	return outbound.VerdictAllowed
}

func TestCheckRefusesWorkThatIsNotHTTP(t *testing.T) {
	l := &ledger{}
	g := outbound.New(fakePolicy{offline: true}, l)
	err := g.Check(outbound.DNS, "api.desec.io")
	if !errors.Is(err, outbound.ErrRefused) {
		t.Fatalf("Check = %v, want ErrRefused", err)
	}
	if len(l.refused) != 1 || l.refused[0] != (entry{outbound.DNS, "api.desec.io"}) {
		t.Errorf("refused = %+v", l.refused)
	}
	if g.Allowed(outbound.DNS) {
		t.Error("Allowed(DNS) = true while offline")
	}
	if len(l.refused) != 1 {
		t.Error("Allowed must not record an attempt")
	}
}

func TestAGateWithoutPolicyRefuses(t *testing.T) {
	srv, hits := outside(t)
	var nilGate *outbound.Gate
	for name, g := range map[string]*outbound.Gate{"nil": nilGate, "zero": {}, "no policy": outbound.New(nil, nil)} {
		err := get(t, g.Client(outbound.Weather, time.Second), srv.URL)
		if !errors.Is(err, outbound.ErrRefused) || hits.Load() != 0 {
			t.Errorf("%s gate: err=%v hits=%d, want a refusal and no connection", name, err, hits.Load())
		}
		if g.Allowed(outbound.Weather) || g.Check(outbound.Weather, "x") == nil {
			t.Errorf("%s gate lets a connection through", name)
		}
	}
}

func TestAllowAllConnectsForTests(t *testing.T) {
	srv, hits := outside(t)
	if err := get(t, outbound.AllowAll().Client(outbound.Weather, time.Second), srv.URL); err != nil || hits.Load() != 1 {
		t.Fatalf("AllowAll: err=%v hits=%d", err, hits.Load())
	}
}

func TestADatabaseThatCannotBeReadMeansRefused(t *testing.T) {
	// Fail closed: a policy that does not know says offline.
	g := outbound.New(outbound.Closed{}, &ledger{})
	if g.Allowed(outbound.Weather) {
		t.Error("a policy that cannot be read must refuse")
	}
}

func TestTheDialControlSeesEveryAddress(t *testing.T) {
	srv, hits := outside(t)
	g := outbound.New(fakePolicy{}, &ledger{})
	refuse := errors.New("not public")
	c := g.NewClient(outbound.Images, outbound.ClientConfig{
		Timeout:     time.Second,
		AllowAddr:   func(netip.Addr) error { return refuse },
		NoKeepAlive: true,
	})
	err := get(t, c, srv.URL)
	if !errors.Is(err, refuse) || hits.Load() != 0 {
		t.Fatalf("err=%v hits=%d, want the address refused before connecting", err, hits.Load())
	}
}

func TestLocalReachesOnlyTheHomeNetwork(t *testing.T) {
	srv, hits := outside(t) // 127.0.0.1
	if err := get(t, outbound.Local(time.Second), srv.URL); err != nil || hits.Load() != 1 {
		t.Fatalf("loopback: err=%v hits=%d", err, hits.Load())
	}
	for _, u := range []string{"http://8.8.8.8/", "http://example.com/", "http://[2001:db8::1]/"} {
		err := get(t, outbound.Local(time.Second), u)
		if !errors.Is(err, outbound.ErrNotHome) {
			t.Errorf("Local %s = %v, want ErrNotHome", u, err)
		}
	}
	for _, u := range []string{"http://192.168.178.40:8080/", "http://10.0.0.5/", "http://hejmo.local/", "http://tablet/", "http://tablet.home/", "http://nas.internal/"} {
		if err := outbound.CheckHome(u); err != nil {
			t.Errorf("CheckHome(%s) = %v, want home network", u, err)
		}
	}
}

func TestStrayCodeUsingTheDefaultClientIsGatedToo(t *testing.T) {
	l := &ledger{}
	g := outbound.New(fakePolicy{offline: true}, l)
	restore := outbound.InstallDefault(g)
	t.Cleanup(restore)

	err := get(t, http.DefaultClient, "http://example.com/x")
	if !errors.Is(err, outbound.ErrRefused) {
		t.Fatalf("default client = %v, want refused", err)
	}
	if len(l.refused) != 1 || l.refused[0].Service != outbound.Other || l.refused[0].Target != "example.com" {
		t.Errorf("refused = %+v, want one for other at example.com", l.refused)
	}
	ctx := outbound.WithService(context.Background(), outbound.DNS)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.desec.io/", nil)
	if _, err := http.DefaultClient.Do(req); !errors.Is(err, outbound.ErrRefused) {
		t.Fatalf("default client with service = %v", err)
	}
	if got := l.refused[len(l.refused)-1].Service; got != outbound.DNS {
		t.Errorf("service = %q, want dns from the context", got)
	}
}

func TestServicesListIsStableAndValid(t *testing.T) {
	want := []outbound.Service{outbound.Weather, outbound.Update, outbound.Calendars, outbound.ICS, outbound.Recipes, outbound.Images, outbound.DNS}
	if len(outbound.Services) != len(want) {
		t.Fatalf("Services = %v", outbound.Services)
	}
	for i, s := range want {
		if outbound.Services[i] != s || !s.Valid() {
			t.Errorf("Services[%d] = %q", i, outbound.Services[i])
		}
	}
	if outbound.Service("nonsense").Valid() || outbound.Other.Valid() {
		t.Error("only the listed connections are valid single switches")
	}
}

// The reverse lookup of the box's address (router name, decision Q92) goes
// through this package: it asks the DNS only about addresses of the home network.
func TestHomeResolverLooksUpOnlyHomeAddresses(t *testing.T) {
	for _, a := range []string{"8.8.8.8", "2001:db8::1", "not an address"} {
		names, err := outbound.HomeResolver{}.LookupAddr(context.Background(), a)
		if !errors.Is(err, outbound.ErrNotHome) || len(names) != 0 {
			t.Errorf("LookupAddr(%s) = %v, %v, want ErrNotHome before any lookup", a, names, err)
		}
	}
	if _, err := (outbound.HomeResolver{}).LookupAddr(context.Background(), "192.168.178.40"); errors.Is(err, outbound.ErrNotHome) {
		t.Errorf("a home address was refused: %v", err)
	}
}
