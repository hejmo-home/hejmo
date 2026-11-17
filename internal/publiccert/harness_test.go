package publiccert_test

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
	"github.com/libdns/libdns"
	"github.com/miekg/dns"
)

// zoneName is the one zone the fake name server is authoritative for.
const zoneName = boxName

// fakeDNS is a DNS provider (libdns) and the name server that answers for it.
type fakeDNS struct {
	mu      sync.Mutex
	records map[string][]libdns.RR // fully qualified name (no trailing dot) -> records
	addr    string                 // address of the name server
	// failDelete, when set, is what DeleteRecords returns instead of deleting.
	failDelete error
	// credentials the provider factory was asked to build a provider for.
	credentials []publiccert.Credentials
}

func newFakeDNS(t *testing.T) *fakeDNS {
	t.Helper()
	f := &fakeDNS{records: map[string][]libdns.RR{}}
	pc, ln, err := listenPair(net.Listen)
	if err != nil {
		t.Fatal(err)
	}
	f.addr = pc.LocalAddr().String()
	h := dns.HandlerFunc(f.serve)
	udp := &dns.Server{PacketConn: pc, Handler: h}
	tcp := &dns.Server{Listener: ln, Handler: h}
	started := make(chan struct{}, 2)
	udp.NotifyStartedFunc = func() { started <- struct{}{} }
	tcp.NotifyStartedFunc = func() { started <- struct{}{} }
	go udp.ActivateAndServe()
	go tcp.ActivateAndServe()
	<-started
	<-started
	t.Cleanup(func() { udp.Shutdown(); tcp.Shutdown() })
	return f
}

func (f *fakeDNS) serve(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range r.Question {
		name := strings.TrimSuffix(strings.ToLower(q.Name), ".")
		if q.Qtype == dns.TypeSOA {
			if name == zoneName {
				m.Answer = append(m.Answer, &dns.SOA{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 1},
					Ns: "ns." + zoneName + ".", Mbox: "hostmaster." + zoneName + ".", Serial: 1, Refresh: 1, Retry: 1, Expire: 1, Minttl: 1})
			} else {
				m.Rcode = dns.RcodeNameError
			}
			continue
		}
		for _, rr := range f.records[name] {
			if rr.Type == "TXT" && q.Qtype == dns.TypeTXT {
				m.Answer = append(m.Answer, &dns.TXT{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 1}, Txt: []string{rr.Data}})
			}
		}
	}
	w.WriteMsg(m)
}

func fqdn(zone, rel string) string {
	zone = strings.TrimSuffix(zone, ".")
	if rel == "@" || rel == "" {
		return zone
	}
	return rel + "." + zone
}

func (f *fakeDNS) AppendRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range recs {
		rr := r.RR()
		n := fqdn(zone, rr.Name)
		f.records[n] = append(f.records[n], rr)
	}
	return recs, nil
}

func (f *fakeDNS) SetRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range recs {
		rr := r.RR()
		n := fqdn(zone, rr.Name)
		var kept []libdns.RR
		for _, old := range f.records[n] {
			if old.Type != rr.Type {
				kept = append(kept, old)
			}
		}
		f.records[n] = append(kept, rr)
	}
	return recs, nil
}

func (f *fakeDNS) DeleteRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDelete != nil {
		return nil, f.failDelete
	}
	for _, r := range recs {
		rr := r.RR()
		n := fqdn(zone, rr.Name)
		var kept []libdns.RR
		for _, old := range f.records[n] {
			if old.Type != rr.Type || old.Data != rr.Data {
				kept = append(kept, old)
			}
		}
		f.records[n] = kept
	}
	return recs, nil
}

// addressOf returns the A record set for name.
func (f *fakeDNS) addressOf(name string) string { return f.addressOfType(name, "A") }

// addressOfType is the data of the first record of the type at the name.
func (f *fakeDNS) addressOfType(name, typ string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rr := range f.records[name] {
		if rr.Type == typ {
			return rr.Data
		}
	}
	return ""
}

// fakeCA is a Pebble ACME server running in the test process.
type fakeCA struct {
	URL    string // ACME directory
	Roots  *x509.CertPool
	Orders atomic.Int32
	down   atomic.Bool
	srv    *httptest.Server
}

type caOption struct {
	eabKeyID string
	eabHMAC  []byte
}

// startCA runs Pebble, validating DNS-01 challenges against dns.
func startCA(t *testing.T, dnsServer *fakeDNS, opt caOption) *fakeCA {
	t.Helper()
	t.Setenv("PEBBLE_VA_NOSLEEP", "1")
	t.Setenv("PEBBLE_WFE_NONCEREJECT", "0") // Pebble rejects 5% of nonces by default, which would repeat orders
	t.Setenv("PEBBLE_AUTHZREUSE", "0")
	logger := log.New(io.Discard, "", 0)
	store := db.NewMemoryStore()
	authority := ca.New(logger, store, "", "ecdsa", 0, 1, map[string]ca.Profile{"default": {Description: "default"}})
	validator := va.New(logger, 5002, 5001, false, dnsServer.addr, store)
	if opt.eabKeyID != "" {
		if err := store.AddExternalAccountKeyByID(opt.eabKeyID, base64.RawURLEncoding.EncodeToString(opt.eabHMAC)); err != nil {
			t.Fatal(err)
		}
	}
	front := wfe.New(logger, store, validator, authority, []string{"pebble.letsencrypt.org"}, false, opt.eabKeyID != "", 1, 1)
	c := &fakeCA{}
	inner := front.Handler()
	c.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.down.Load() {
			http.Error(w, "down for maintenance", http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/order-plz") {
			c.Orders.Add(1)
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(c.srv.Close)
	c.URL = c.srv.URL + wfe.DirectoryPath
	c.Roots = x509.NewCertPool()
	c.Roots.AddCert(c.srv.Certificate())
	return c
}

// listenPair opens a UDP and a TCP socket on the same loopback port. The
// kernel picks the UDP port; if TCP has that number taken, try again.
func listenPair(listen func(network, addr string) (net.Listener, error)) (net.PacketConn, net.Listener, error) {
	var lastErr error
	for range 20 {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			return nil, nil, err
		}
		ln, err := listen("tcp", pc.LocalAddr().String())
		if err == nil {
			return pc, ln, nil
		}
		pc.Close()
		lastErr = err
	}
	return nil, nil, lastErr
}

// GetRecords lists the zone's records (libdns.RecordGetter).
func (f *fakeDNS) GetRecords(_ context.Context, zone string) ([]libdns.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	zone = strings.TrimSuffix(zone, ".")
	var out []libdns.Record
	for n, rrs := range f.records {
		if n != zone && !strings.HasSuffix(n, "."+zone) {
			continue
		}
		for _, rr := range rrs {
			rel := strings.TrimSuffix(strings.TrimSuffix(n, zone), ".")
			if rel == "" {
				rel = "@"
			}
			rr.Name = rel
			rec, err := rr.Parse()
			if err != nil {
				return nil, err
			}
			out = append(out, rec)
		}
	}
	return out, nil
}

// put adds records (relative names) to the zone before the test starts.
func (f *fakeDNS) put(zone string, recs ...libdns.RR) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rr := range recs {
		n := fqdn(zone, rr.Name)
		f.records[n] = append(f.records[n], rr)
	}
}

func (f *fakeDNS) txtAt(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, rr := range f.records[name] {
		if rr.Type == "TXT" {
			out = append(out, rr.Data)
		}
	}
	return out
}
