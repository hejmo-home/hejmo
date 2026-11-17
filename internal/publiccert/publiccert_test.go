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
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/store/storetest"
	"github.com/libdns/libdns"
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
	r.dbPath = storetest.Path(t)
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
		publiccert.WithProviderFactory(func(provider string, c publiccert.Credentials) (publiccert.DNSProvider, error) {
			switch provider {
			case "desec", "cloudflare", "hetzner", "inwx":
				r.dns.credentials = append(r.dns.credentials, c)
				return r.dns, nil
			}
			return nil, publiccert.ErrUnknownProvider
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
	if !m.Ready() || !m.HasCertificate() || m.Name() != boxName {
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
	// Each refusal names the form field it belongs to, so the wizard can show
	// the error at that field (ticket 133).
	cases := map[string]struct {
		mutate func(*publiccert.Request)
		field  string
	}{
		"no token":          {func(q *publiccert.Request) { q.Token = "" }, "token"},
		"single label name": {func(q *publiccert.Request) { q.Name = "mia" }, "name"},
		"wildcard name":     {func(q *publiccert.Request) { q.Name = "*.dedyn.io" }, "name"},
		"name with slash":   {func(q *publiccert.Request) { q.Name = "mia.dedyn.io/x" }, "name"},
		"bad LAN address":   {func(q *publiccert.Request) { q.LANIP = "not-an-ip" }, "lan_ip"},
		"unspecified IP":    {func(q *publiccert.Request) { q.LANIP = "0.0.0.0" }, "lan_ip"},
		"unknown issuer":    {func(q *publiccert.Request) { q.Issuer = "cheapcerts" }, "issuer"},
		"Actalis no keys":   {func(q *publiccert.Request) { q.Issuer = "actalis" }, "eab"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			q := request("letsencrypt")
			c.mutate(&q)
			err := m.Setup(context.Background(), q)
			if !errors.Is(err, publiccert.ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			var inv *publiccert.InvalidError
			if !errors.As(err, &inv) || inv.Field != c.field {
				t.Errorf("err = %v, want an InvalidError for field %q", err, c.field)
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

// credentialsOf returns what the factory was last asked to build a provider for.
func (r *rig) credentialsOf() publiccert.Credentials {
	r.dns.mu.Lock()
	defer r.dns.mu.Unlock()
	return r.dns.credentials[len(r.dns.credentials)-1]
}

// Validation per provider: the wizard shows each refusal at its field, before
// any provider is asked.
func TestSetupValidatesTheCredentialsOfEachProviderBeforeContactingIt(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	inwx := func(mut func(*publiccert.Request)) publiccert.Request {
		q := request("letsencrypt")
		q.Provider, q.Token, q.Username, q.Password = "inwx", "", "mueller-familie", "pw"
		mut(&q)
		return q
	}
	cases := map[string]struct {
		req   publiccert.Request
		field string
	}{
		"inwx no username":        {inwx(func(q *publiccert.Request) { q.Username = "" }), "username"},
		"inwx no password":        {inwx(func(q *publiccert.Request) { q.Password = "" }), "password"},
		"inwx 2fa without secret": {inwx(func(q *publiccert.Request) { q.TwoFA = true }), "otp_secret"},
		"inwx six digit code":     {inwx(func(q *publiccert.Request) { q.TwoFA, q.OTPSecret = true, "123456" }), "otp_secret"},
		"inwx not base32":         {inwx(func(q *publiccert.Request) { q.TwoFA, q.OTPSecret = true, "JBSWY3DPEHPK3PX1JBSWY3DP" }), "otp_secret"},
		"inwx too long":           {inwx(func(q *publiccert.Request) { q.TwoFA, q.OTPSecret = true, strings.Repeat("A", 33) }), "otp_secret"},
		"inwx bad name":           {inwx(func(q *publiccert.Request) { q.Name = "box" }), "name"},
		"cloudflare no token": {func() publiccert.Request {
			q := request("letsencrypt")
			q.Provider, q.Token = "cloudflare", ""
			return q
		}(), "token"},
		"hetzner no token": {func() publiccert.Request { q := request("letsencrypt"); q.Provider, q.Token = "hetzner", ""; return q }(), "token"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var inv *publiccert.InvalidError
			if err := m.Setup(context.Background(), c.req); !errors.As(err, &inv) || inv.Field != c.field {
				t.Fatalf("err = %v, want an InvalidError for %q", err, c.field)
			}
		})
	}
	if len(r.dns.credentials) != 0 {
		t.Error("a provider was built for invalid input")
	}
}

// A secret typed in groups and lower case is accepted, as INWX shows it; it
// reaches the provider cleaned up. With the switch off the secret is ignored.
func TestInwxSecretIsCleanedUpAndIgnoredWhenTwoFactorIsOff(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	q := request("letsencrypt")
	q.Provider, q.Token, q.Username, q.Password, q.TwoFA, q.OTPSecret = "inwx", "", "mueller-familie", "pw", true, "jbsw y3dp ehpk 3pxp"
	if err := m.Setup(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if got := r.credentialsOf(); got.OTPSecret != "JBSWY3DPEHPK3PXP" || got.Username != "mueller-familie" || got.Password != "pw" {
		t.Errorf("provider got %+v", got)
	}
	r2 := newRig(t)
	le2 := r2.startCA(caOption{})
	m2 := r2.manager(map[string]string{"letsencrypt": le2.URL})
	q.TwoFA, q.OTPSecret = false, "123456"
	if err := m2.Setup(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if got := r2.credentialsOf(); got.OTPSecret != "" {
		t.Errorf("secret of a switched-off 2FA reached the provider: %+v", got)
	}
}

// All credentials are sealed together, survive a restart and are not
// readable in the database file.
func TestAllCredentialsAreSealedTogetherAndResumedAfterARestart(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	q := request("letsencrypt")
	q.Provider, q.Token, q.Username, q.Password, q.TwoFA, q.OTPSecret = "inwx", "", "mueller-familie", "geheim-pw-4711", true, "JBSWY3DPEHPK3PXP"
	if err := m.Setup(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	m.Close()
	r.db.SQL().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	for _, f := range []string{r.dbPath, r.dbPath + "-wal"} {
		if raw, err := os.ReadFile(f); err == nil {
			for _, secret := range []string{"geheim-pw-4711", "JBSWY3DPEHPK3PXP"} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Errorf("%q readable in %s", secret, filepath.Base(f))
				}
			}
		}
	}
	m2 := r.manager(map[string]string{"letsencrypt": le.URL})
	if err := m2.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.credentialsOf(); got.Username != "mueller-familie" || got.Password != "geheim-pw-4711" || got.OTPSecret != "JBSWY3DPEHPK3PXP" {
		t.Errorf("resumed provider got %+v", got)
	}
	// Cloudflare's two tokens too.
	r3 := newRig(t)
	le3 := r3.startCA(caOption{})
	m3 := r3.manager(map[string]string{"letsencrypt": le3.URL})
	c := request("letsencrypt")
	c.Provider, c.Token, c.ZoneToken = "cloudflare", "cf-edit", "cf-read"
	if err := m3.Setup(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	m3.Close()
	m4 := r3.manager(map[string]string{"letsencrypt": le3.URL})
	if err := m4.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r3.credentialsOf(); got.Token != "cf-edit" || got.ZoneToken != "cf-read" {
		t.Errorf("resumed Cloudflare provider got %+v", got)
	}
}

// Safety: the box never overwrites an address that is already public.
func TestSetupRefusesANameThatAlreadyPointsToAPublicAddress(t *testing.T) {
	for name, c := range map[string]struct {
		rec  libdns.RR
		want string
	}{
		"public A":    {libdns.RR{Name: "@", Type: "A", Data: "203.0.113.7"}, "203.0.113.7"},
		"public AAAA": {libdns.RR{Name: "@", Type: "AAAA", Data: "2001:db8::7"}, "2001:db8::7"},
		"CNAME":       {libdns.RR{Name: "@", Type: "CNAME", Data: "shop.example.net."}, "shop.example.net."},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			le := r.startCA(caOption{})
			m := r.manager(map[string]string{"letsencrypt": le.URL})
			r.dns.put(boxName, c.rec)
			err := m.Setup(context.Background(), request("letsencrypt"))
			var taken *publiccert.NameTakenError
			if !errors.Is(err, publiccert.ErrNameTaken) || !errors.As(err, &taken) {
				t.Fatalf("err = %v, want ErrNameTaken", err)
			}
			if taken.Address != c.want || taken.Suggestion != "box.mia.dedyn.io" {
				t.Errorf("taken = %+v", taken)
			}
			if got := r.dns.addressOf(boxName); got == "192.168.178.20" {
				t.Error("the existing record was overwritten")
			}
			if m.Ready() || le.Orders.Load() != 0 {
				t.Error("went on after the refusal")
			}
		})
	}
}

func TestSetupReplacesAPrivateAddressOfAnEarlierBox(t *testing.T) {
	// old is the address already at the name; lan is the address of this try
	// ("" means the default one). An address equal to the LAN address is the
	// box's own record of a failed earlier attempt.
	cases := []struct{ old, lan string }{
		{"192.168.178.99", ""}, {"10.0.0.5", ""}, {"172.16.3.4", ""}, {"fd12:3456::1", ""},
		{"100.64.1.20", ""}, {"169.254.3.4", ""}, {"fe80::1", ""}, {"127.0.0.1", ""},
		{"2a02:8070:1234::20", "2a02:8070:1234::20"},
		{"100.64.1.20", "100.64.1.20"},
		{"169.254.3.4", "169.254.3.4"},
		{"203.0.113.7", "203.0.113.7"},
	}
	for _, c := range cases {
		r := newRig(t)
		le := r.startCA(caOption{})
		m := r.manager(map[string]string{"letsencrypt": le.URL})
		typ := "A"
		if strings.Contains(c.old, ":") {
			typ = "AAAA"
		}
		r.dns.put(boxName, libdns.RR{Name: "@", Type: typ, Data: c.old})
		req := request("letsencrypt")
		want := "192.168.178.20"
		if c.lan != "" {
			req.LANIP, want = c.lan, c.lan
		}
		if err := m.Setup(context.Background(), req); err != nil {
			t.Fatalf("%s: %v", c.old, err)
		}
		wantType := "A"
		if strings.Contains(want, ":") {
			wantType = "AAAA"
		}
		if got := r.dns.addressOfType(boxName, wantType); got != want {
			t.Errorf("%s: %s record = %q, want %q", c.old, wantType, got, want)
		}
	}
}

// An earlier box's record of the other address family is deleted, so the old
// box cannot be resolved any more (a ULA AAAA while this try writes an A and
// the other way round). A public record of the other family still refuses.
func TestSetupRemovesAPrivateRecordOfTheOtherAddressFamily(t *testing.T) {
	cases := []struct{ old, oldType, lan, wantType, goneType string }{
		{"fd12:3456::1", "AAAA", "", "A", "AAAA"},
		{"192.168.178.99", "A", "fd12:3456::20", "AAAA", "A"},
	}
	for _, c := range cases {
		r := newRig(t)
		le := r.startCA(caOption{})
		m := r.manager(map[string]string{"letsencrypt": le.URL})
		r.dns.put(boxName, libdns.RR{Name: "@", Type: c.oldType, Data: c.old})
		req := request("letsencrypt")
		if c.lan != "" {
			req.LANIP = c.lan
		}
		if err := m.Setup(context.Background(), req); err != nil {
			t.Fatalf("%s: %v", c.old, err)
		}
		if got := r.dns.addressOfType(boxName, c.goneType); got != "" {
			t.Errorf("%s: %s record = %q, want none", c.old, c.goneType, got)
		}
		if got := r.dns.addressOfType(boxName, c.wantType); got != req.LANIP {
			t.Errorf("%s: %s record = %q, want %q", c.old, c.wantType, got, req.LANIP)
		}
	}
}

// An earlier box's record of the other address family that cannot be deleted
// stops the setup: the old box would stay resolvable at the name.
func TestSetupFailsWhenAnEarlierBoxsRecordCannotBeDeleted(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	r.dns.put(boxName, libdns.RR{Name: "@", Type: "AAAA", Data: "fd12:3456::1"})
	r.dns.failDelete = errors.New("provider says no")
	err := m.Setup(context.Background(), request("letsencrypt"))
	if err == nil || !strings.Contains(err.Error(), "provider says no") {
		t.Fatalf("Setup = %v, want the delete error", err)
	}
	if got := r.dns.addressOf(boxName); got != "" {
		t.Errorf("an address was written (%q) although the old record stays", got)
	}
}

// Stale challenge records of an earlier attempt are removed, so a provider
// that cannot handle several values at one name is not confused.
func TestSetupRemovesStaleChallengeRecordsOfThisName(t *testing.T) {
	r := newRig(t)
	le := r.startCA(caOption{})
	m := r.manager(map[string]string{"letsencrypt": le.URL})
	r.dns.put(boxName,
		libdns.RR{Name: "_acme-challenge", Type: "TXT", Data: "stale-1"},
		libdns.RR{Name: "_acme-challenge", Type: "TXT", Data: "stale-2"},
		libdns.RR{Name: "_acme-challenge.other", Type: "TXT", Data: "not-ours"})
	if err := m.Setup(context.Background(), request("letsencrypt")); err != nil {
		t.Fatal(err)
	}
	if got := r.dns.txtAt("_acme-challenge.mia.dedyn.io"); len(got) != 0 {
		t.Errorf("stale challenge records left: %v", got)
	}
	if got := r.dns.txtAt("_acme-challenge.other.mia.dedyn.io"); len(got) != 1 {
		t.Errorf("another name's record was touched: %v", got)
	}
}
