package publiccert_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/libdns/libdns"
)

// The DNS service (and the certificate authority behind it) is one of the
// connections to the outside (ticket 201): it goes through the gate.

type offlinePolicy struct{}

func (offlinePolicy) Verdict(outbound.Service) outbound.Verdict { return outbound.VerdictOffline }

type recorder struct{ refused []string }

func (r *recorder) Refused(_ time.Time, s outbound.Service, target string) {
	r.refused = append(r.refused, string(s)+" "+target)
}
func (r *recorder) Contacted(time.Time, outbound.Service, string) {}

func TestSetupIsRefusedWhileOfflineAndTouchesNoDNSService(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	rec := &recorder{}
	m := r.manager(map[string]string{"letsencrypt": le.URL}, publiccert.WithGate(outbound.New(offlinePolicy{}, rec)))

	err := m.Setup(context.Background(), request("letsencrypt"))
	if !errors.Is(err, outbound.ErrRefused) {
		t.Fatalf("Setup = %v, want a refused connection", err)
	}
	if got := r.dns.addressOf(boxName); got != "" {
		t.Errorf("an address record was written: %q", got)
	}
	if len(rec.refused) == 0 || rec.refused[0][:3] != "dns" {
		t.Errorf("refused attempts = %v, want one for the DNS service", rec.refused)
	}
}

func TestEveryProviderCallGoesThroughTheGate(t *testing.T) {
	rec := &recorder{}
	g := outbound.New(offlinePolicy{}, rec)
	p := publiccert.GatedProvider(g, "desec", &countingDNS{})
	ctx := context.Background()
	calls := map[string]error{}
	_, calls["get"] = p.GetRecords(ctx, "dedyn.io")
	_, calls["set"] = p.SetRecords(ctx, "dedyn.io", nil)
	_, calls["append"] = p.AppendRecords(ctx, "dedyn.io", nil)
	_, calls["delete"] = p.DeleteRecords(ctx, "dedyn.io", nil)
	for name, err := range calls {
		if !errors.Is(err, outbound.ErrRefused) {
			t.Errorf("%s = %v, want refused", name, err)
		}
	}
	if len(rec.refused) != 4 {
		t.Errorf("refused = %v", rec.refused)
	}
}

type countingDNS struct{ calls int }

func (c *countingDNS) GetRecords(context.Context, string) ([]libdns.Record, error) {
	c.calls++
	return nil, nil
}
func (c *countingDNS) SetRecords(context.Context, string, []libdns.Record) ([]libdns.Record, error) {
	c.calls++
	return nil, nil
}
func (c *countingDNS) AppendRecords(context.Context, string, []libdns.Record) ([]libdns.Record, error) {
	c.calls++
	return nil, nil
}
func (c *countingDNS) DeleteRecords(context.Context, string, []libdns.Record) ([]libdns.Record, error) {
	c.calls++
	return nil, nil
}

// Leaving the public name for offline mode (Q143): the records of the name at
// the DNS service are deleted first.
func TestDeleteRecordsRemovesTheAddressAndChallengesOfTheName(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := m.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	r.dns.put(boxName,
		libdns.RR{Name: "_acme-challenge", Type: "TXT", Data: "left-over"},
		libdns.RR{Name: "other", Type: "A", Data: "203.0.113.7"})
	if err := m.DeleteRecords(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.dns.addressOf(boxName); got != "" {
		t.Errorf("address record left: %q", got)
	}
	if got := r.dns.txtAt("_acme-challenge." + boxName); len(got) != 0 {
		t.Errorf("challenge records left: %v", got)
	}
	if got := r.dns.addressOf("other." + boxName); got != "203.0.113.7" {
		t.Errorf("a record of another name was touched: %q", got)
	}
	if !m.Ready() {
		t.Error("deleting the records must not forget the setup: that is the caller's next step")
	}
}

func TestDeleteRecordsReportsWhatTheServiceRefused(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := m.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	r.dns.failDelete = errors.New("token no longer valid")
	err := m.DeleteRecords(context.Background())
	if err == nil || !errors.Is(err, publiccert.ErrDeleteRecords) {
		t.Fatalf("DeleteRecords = %v, want ErrDeleteRecords", err)
	}
	if got := r.dns.addressOf(boxName); got == "" {
		t.Error("the address record is gone although the service refused")
	}
	if !m.Ready() {
		t.Error("nothing may change when the deletion fails")
	}
}

type allowPolicy struct{}

func (allowPolicy) Verdict(outbound.Service) outbound.Verdict { return outbound.VerdictAllowed }

type contactRecorder struct{ contacted []string }

func (c *contactRecorder) Refused(time.Time, outbound.Service, string) {}
func (c *contactRecorder) Contacted(_ time.Time, s outbound.Service, target string) {
	c.contacted = append(c.contacted, string(s)+" "+target)
}

// The Hetzner provider has an HTTP client of its own: it must be the
// Manager's gate's, not an ungated one (Q144).
func TestHetznerProviderUsesTheManagersGate(t *testing.T) {
	f := newFakeHetzner(t)
	creds := publiccert.Credentials{Token: "hz-token"}
	// As the factory builds it, before the Manager hands it out: closed.
	bare := publiccert.NewHetznerUngated(creds, f.srv.URL+"/v1")
	if _, err := bare.GetRecords(context.Background(), providerZone); !errors.Is(err, outbound.ErrRefused) {
		t.Fatalf("an ungated provider connected: %v", err)
	}
	build := publiccert.WithProviderFactory(func(string, publiccert.Credentials) (publiccert.DNSProvider, error) {
		return publiccert.NewHetznerUngated(creds, f.srv.URL+"/v1"), nil
	})
	r := newRig(t)

	rec := &contactRecorder{}
	m := r.manager(nil, build, publiccert.WithGate(outbound.New(allowPolicy{}, rec)))
	p, err := publiccert.ProviderOf(m, "hetzner", creds)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetRecords(context.Background(), providerZone); err != nil {
		t.Fatalf("GetRecords through an open gate: %v", err)
	}
	if len(rec.contacted) == 0 || rec.contacted[len(rec.contacted)-1][:3] != "dns" {
		t.Errorf("the request did not pass the gate: contacted = %v", rec.contacted)
	}
}
