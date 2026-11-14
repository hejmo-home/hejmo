package publiccert_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/store"
)

const boxName = "mia.dedyn.io"

type rig struct {
	t      *testing.T
	dns    *fakeDNS
	db     *store.DB
	dbPath string
	dir    string
	sealer *secret.Sealer
	roots  *x509.CertPool
	now    time.Time
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, dns: newFakeDNS(t), dir: t.TempDir(), now: time.Now()}
	r.dbPath = filepath.Join(r.dir, "hejmo.db")
	var err error
	if r.db, err = store.Open(r.dbPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.db.Close() })
	if r.sealer, err = secret.Open(filepath.Join(r.dir, "secret.key")); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *rig) startCA(opt caOption) *fakeCA {
	c := startCA(r.t, r.dns, opt)
	r.roots = c.Roots // all test CAs share one TLS certificate
	return c
}

// manager builds a manager that asks the given ACME directories (by issuer).
func (r *rig) manager(directories map[string]string, extra ...publiccert.Option) *publiccert.Manager {
	r.t.Helper()
	opts := append([]publiccert.Option{
		publiccert.WithDirectories(directories),
		publiccert.WithTrustedRoots(r.roots),
		publiccert.WithProviderFactory(func(provider, token string) (publiccert.DNSProvider, error) {
			if provider != "desec" {
				return nil, publiccert.ErrUnknownProvider
			}
			return r.dns, nil
		}),
		publiccert.WithPropagation(0, -1), // the fake name server answers at once
		publiccert.WithResolvers([]string{r.dns.addr}),
		publiccert.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		publiccert.WithClock(func() time.Time { return r.now }),
	}, extra...)
	m, err := publiccert.New(filepath.Join(r.dir, "tls", "public"), r.db, r.sealer, opts...)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(m.Close)
	return m
}

func request(issuer string) publiccert.Request {
	return publiccert.Request{Name: boxName, Provider: "desec", Token: "desec-token-4711", Issuer: issuer, LANIP: "192.168.178.20", Email: "papa@example.org"}
}

func serves(t *testing.T, m *publiccert.Manager, name string) {
	t.Helper()
	c, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: name})
	if err != nil {
		t.Fatalf("GetCertificate(%q): %v", name, err)
	}
	if c.Leaf == nil {
		t.Fatal("certificate without parsed leaf")
	}
	if err := c.Leaf.VerifyHostname(name); err != nil {
		t.Fatalf("certificate does not cover %q: %v", name, err)
	}
}

// Acceptance: given a deSEC token, when the wizard finishes, then the box
// serves a certificate for the chosen name (fake ACME server, fake DNS).
func TestSetupObtainsCertificateForTheNameAndPointsItToTheLAN(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})

	if m.Ready() {
		t.Fatal("ready before setup")
	}
	if err := m.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	if !m.Ready() || m.Name() != boxName {
		t.Fatalf("Ready=%v Name=%q", m.Ready(), m.Name())
	}
	serves(t, m, boxName)
	if got := r.dns.addressOf(boxName); got != "192.168.178.20" {
		t.Errorf("A record = %q, want the LAN address", got)
	}
	if le.Orders.Load() != 1 {
		t.Errorf("orders at the CA = %d, want 1", le.Orders.Load())
	}
	if st := m.Status(); st.Issuer != "letsencrypt" || st.Name != boxName || st.Warning || st.Expires.IsZero() {
		t.Errorf("status = %+v", st)
	}
}

// Acceptance: given Actalis selected, then certificates are requested from Actalis.
func TestActalisSelectedRequestsFromActalisOnly(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	actalis := r.startCA(caOption{eabKeyID: "actalis-kid", eabHMAC: []byte("0123456789abcdef0123456789abcdef")})
	m := r.manager(map[string]string{"letsencrypt": le.URL, "actalis": actalis.URL})

	req := request("actalis")
	req.EABKeyID, req.EABHMAC = "actalis-kid", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY" // base64url of the key
	if err := m.Setup(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	serves(t, m, boxName)
	if actalis.Orders.Load() != 1 || le.Orders.Load() != 0 {
		t.Errorf("orders: actalis=%d letsencrypt=%d, want 1 and 0", actalis.Orders.Load(), le.Orders.Load())
	}
	if st := m.Status(); st.Issuer != "actalis" {
		t.Errorf("issuer = %q", st.Issuer)
	}
}

func TestActalisNeedsItsAccountKeys(t *testing.T) {
	r := newRig(t)
	actalis := r.startCA(caOption{})
	m := r.manager(map[string]string{"actalis": actalis.URL})
	err := m.Setup(context.Background(), request("actalis"))
	if !errors.Is(err, publiccert.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if actalis.Orders.Load() != 0 {
		t.Error("the CA was asked without an account")
	}
}

// ADR 0014: ZeroSSL is the automatic fallback issuer.
func TestZeroSSLIsTheFallbackWhenTheIssuerFails(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	le.down.Store(true)
	zero := r.startCA(caOption{eabKeyID: "zero-kid", eabHMAC: []byte("zero-secret-zero-secret-zero-sec")})
	eab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("email") != "papa@example.org" && req.PostFormValue("email") != "papa@example.org" {
			http.Error(w, "no email", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "eab_kid": "zero-kid", "eab_hmac_key": "emVyby1zZWNyZXQtemVyby1zZWNyZXQtemVyby1zZWM"})
	}))
	t.Cleanup(eab.Close)
	m := r.manager(map[string]string{"letsencrypt": le.URL, "zerossl": zero.URL}, publiccert.WithZeroSSLEABURL(eab.URL))

	if err := m.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	serves(t, m, boxName)
	if zero.Orders.Load() != 1 {
		t.Errorf("fallback orders = %d, want 1", zero.Orders.Load())
	}
}

func TestSetupRejectsWhatCannotWork(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	cases := map[string]func(*publiccert.Request){
		"no token":          func(q *publiccert.Request) { q.Token = "" },
		"single label name": func(q *publiccert.Request) { q.Name = "mia" },
		"wildcard name":     func(q *publiccert.Request) { q.Name = "*.dedyn.io" },
		"name with slash":   func(q *publiccert.Request) { q.Name = "mia.dedyn.io/x" },
		"bad LAN address":   func(q *publiccert.Request) { q.LANIP = "not-an-ip" },
		"unspecified IP":    func(q *publiccert.Request) { q.LANIP = "0.0.0.0" },
		"unknown issuer":    func(q *publiccert.Request) { q.Issuer = "cheapcerts" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			q := request("letsencrypt")
			mutate(&q)
			if err := m.Setup(context.Background(), q); !errors.Is(err, publiccert.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
	if err := m.Setup(context.Background(), func() publiccert.Request { q := request("letsencrypt"); q.Provider = "nope"; return q }()); !errors.Is(err, publiccert.ErrUnknownProvider) {
		t.Errorf("unknown provider: err = %v", err)
	}
	if m.Ready() || le.Orders.Load() != 0 {
		t.Error("something was requested for invalid input")
	}
}

func TestFailedSetupLeavesNoConfigurationBehind(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	le.down.Store(true)
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := m.Setup(context.Background(), request("letsencrypt")); err == nil {
		t.Fatal("setup succeeded against a CA that is down")
	}
	if m.Ready() {
		t.Error("ready after a failed setup")
	}
	if _, ok, _ := r.db.PublicHTTPS(); ok {
		t.Error("configuration stored although no certificate was obtained")
	}
	// A wizard attempt that failed is not a broken setup: nothing to warn about.
	if st := m.Status(); st.Warning || st.RenewalError != "" {
		t.Errorf("failed attempt leaves a warning: %+v", st)
	}
}

// Acceptance: the DNS token is not readable in a plain-text dump of the database.
func TestDNSTokenIsNotReadableInTheDatabaseFile(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := m.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	r.db.SQL().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	for _, f := range []string{r.dbPath, r.dbPath + "-wal"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if bytes.Contains(raw, []byte("desec-token-4711")) {
			t.Errorf("DNS token readable in %s", filepath.Base(f))
		}
	}
	saved, ok, _ := r.db.PublicHTTPS()
	if !ok || len(saved.Token) == 0 {
		t.Fatalf("nothing stored: %+v", saved)
	}
	if got, err := r.sealer.Open(saved.Token); err != nil || string(got) != "desec-token-4711" {
		t.Errorf("sealed token does not open: %q %v", got, err)
	}
}

func TestBoxRestartServesTheStoredCertificateWithoutTheCA(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	first := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := first.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	first.Close()

	le.down.Store(true)
	second := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !second.Ready() || second.Name() != boxName {
		t.Fatalf("not ready after restart: ready=%v name=%q", second.Ready(), second.Name())
	}
	serves(t, second, boxName)
	if le.Orders.Load() != 1 {
		t.Errorf("restart ordered again: %d", le.Orders.Load())
	}
}

func TestStartWithoutConfigurationStaysNotReady(t *testing.T) {
	r := newRig(t)
	m := r.manager(nil)
	if err := m.Start(context.Background()); err != nil || m.Ready() {
		t.Fatalf("Start = %v, Ready = %v", err, m.Ready())
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: boxName}); err == nil {
		t.Error("served a certificate without setup")
	}
}

// Acceptance: given a certificate that cannot be renewed, then parents see a
// warning in the web interface (here: the status the interface shows).
func TestFailedRenewalRaisesAWarningUntilItSucceeds(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := m.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	if m.Status().Warning {
		t.Fatal("warning right after setup")
	}

	le.down.Store(true)
	if err := m.RenewNow(context.Background()); err == nil {
		t.Fatal("renewal succeeded against a CA that is down")
	}
	st := m.Status()
	if !st.Warning || st.RenewalError == "" {
		t.Fatalf("no warning after failed renewal: %+v", st)
	}
	serves(t, m, boxName) // the old certificate keeps being served

	// The warning outlives a restart.
	again := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := again.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !again.Status().Warning {
		t.Error("warning lost after restart")
	}

	le.down.Store(false)
	if err := again.RenewNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again.Status().Warning {
		t.Errorf("warning stays after a successful renewal: %+v", again.Status())
	}
}

func TestNearExpiryWarnsEvenWithoutAFailedAttempt(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := m.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	r.now = m.Status().Expires.Add(-3 * 24 * time.Hour)
	if st := m.Status(); !st.Warning {
		t.Errorf("no warning three days before expiry: %+v", st)
	}
}

// A lost or replaced secret.key must not take the box down: the stored token
// cannot be opened, the box keeps serving, parents are warned, and the public
// name can be set up again.
func TestForeignKeyFileDoesNotStopTheBoxAndSetupCanBeRedone(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	first := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := first.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	first.Close()

	// secret.key was lost and the box created a new one.
	foreign, err := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	r.sealer = foreign
	again := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := again.Start(context.Background()); err == nil {
		t.Fatal("Start opened a token sealed with another key")
	}
	// The stored certificate needs no token to be served.
	if !again.Ready() {
		t.Error("not ready although the certificate is stored")
	}
	serves(t, again, boxName)
	if st := again.Status(); !st.Warning || st.RenewalError == "" || st.Name != boxName {
		t.Errorf("parents get no warning: %+v", st)
	}

	// The family enters the token again.
	if err := again.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	serves(t, again, boxName)
	if st := again.Status(); st.Warning || st.RenewalError != "" {
		t.Errorf("warning stays after a new setup: %+v", st)
	}
	third := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := third.Start(context.Background()); err != nil || !third.Ready() {
		t.Errorf("restart after the new setup: err=%v ready=%v", err, third.Ready())
	}
}

// The name check failed and the router cannot be changed: the family goes
// back to the box's own CA, which forgets the public setup (ADR 0014).
func TestClearForgetsThePublicSetup(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := m.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	if err := m.Clear(); err != nil {
		t.Fatal(err)
	}
	if m.Ready() || m.Name() != "" {
		t.Errorf("still set up: ready=%v name=%q", m.Ready(), m.Name())
	}
	if _, ok, _ := r.db.PublicHTTPS(); ok {
		t.Error("the stored setup (with the sealed token) is still there")
	}
	again := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := again.Start(context.Background()); err != nil || again.Ready() {
		t.Errorf("restart after Clear: err=%v ready=%v", err, again.Ready())
	}
}
