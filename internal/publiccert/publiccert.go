// Package publiccert gives the box a publicly trusted HTTPS certificate
// (ADR 0014): the family's DNS name (a free deSEC dedyn.io name or an own
// domain at a DNS provider with an API) points to the box's LAN address, and
// CertMagic obtains and renews the certificate through the DNS-01 challenge.
// Let's Encrypt is the default issuer, Actalis is selectable, and ZeroSSL is
// the automatic fallback.
package publiccert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/hejmo-home/hejmo/internal/secret"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/libdns/desec"
	"github.com/libdns/libdns"
	"github.com/mholt/acmez/v3/acme"
	"go.uber.org/zap"
)

const (
	// expirySoon is how long before expiry the box warns although no
	// renewal has failed yet: CertMagic renews at a third of the lifetime
	// remaining, so a certificate this close to its end means it did not.
	expirySoon = 7 * 24 * time.Hour

	zeroSSLEABURL = "https://api.zerossl.com/acme/eab-credentials-email"
	actalisCA     = "https://acme-api.actalis.com/acme/directory"
)

// Issuer names, as stored and shown in the wizard.
const (
	LetsEncrypt = "letsencrypt"
	Actalis     = "actalis"
	zeroSSL     = "zerossl"
)

// ErrInvalid means the request cannot work; the message says why.
var ErrInvalid = errors.New("publiccert: invalid request")

// ErrUnknownProvider means the DNS provider is not supported.
var ErrUnknownProvider = errors.New("publiccert: unknown DNS provider")

// DNSProvider is what the box needs from a DNS provider: setting the box's
// address record, and the TXT records of the DNS-01 challenge.
type DNSProvider interface {
	certmagic.DNSProvider
	libdns.RecordSetter
}

// ProviderFactory builds the DNS provider client for an API token.
type ProviderFactory func(provider, token string) (DNSProvider, error)

func defaultProviders(provider, token string) (DNSProvider, error) {
	switch provider {
	case "desec":
		return &desec.Provider{Token: token}, nil
	}
	return nil, ErrUnknownProvider
}

// Providers lists the DNS providers the wizard offers.
func Providers() []string { return []string{"desec"} }

// Request is what the wizard collects.
type Request struct {
	Name     string // DNS name of the box, e.g. familie.dedyn.io
	Provider string // DNS provider, see Providers
	Token    string // API token of the DNS provider
	Issuer   string // LetsEncrypt or Actalis
	LANIP    string // the box's address on the home network
	Email    string // optional contact for the certificate authority; needed for the ZeroSSL fallback
	// EABKeyID and EABHMAC are the issuer's external account binding
	// (required for Actalis): key identifier and base64url HMAC key.
	EABKeyID, EABHMAC string
}

// Status is what the web interface shows.
type Status struct {
	Name, Issuer string
	Expires      time.Time
	// Warning is set when the certificate could not be renewed or is about
	// to expire; parents are told (ADR 0014).
	Warning      bool
	RenewalError string
}

// Option configures a Manager.
type Option func(*Manager)

// WithDirectories overrides the ACME directory URL per issuer (tests).
func WithDirectories(d map[string]string) Option {
	return func(m *Manager) {
		for k, v := range d {
			m.directories[k] = v
		}
	}
}

// WithTrustedRoots sets the CAs trusted for the ACME servers' own TLS (tests).
func WithTrustedRoots(p *x509.CertPool) Option { return func(m *Manager) { m.roots = p } }

// WithProviderFactory replaces how DNS providers are built (tests).
func WithProviderFactory(f ProviderFactory) Option { return func(m *Manager) { m.providers = f } }

// WithPropagation sets how long to wait before and for DNS propagation of the
// challenge record; a timeout of -1 skips the check.
func WithPropagation(delay, timeout time.Duration) Option {
	return func(m *Manager) { m.propDelay, m.propTimeout = delay, timeout }
}

// WithResolvers sets the DNS servers used to find the zone and check propagation.
func WithResolvers(r []string) Option { return func(m *Manager) { m.resolvers = r } }

// WithLogger sets where problems are logged.
func WithLogger(l *slog.Logger) Option { return func(m *Manager) { m.logger = l } }

// WithClock sets the clock behind expiry warnings.
func WithClock(now func() time.Time) Option { return func(m *Manager) { m.now = now } }

// WithZeroSSLEABURL overrides where ZeroSSL account keys are requested (tests).
func WithZeroSSLEABURL(u string) Option { return func(m *Manager) { m.zeroSSLEAB = u } }

// Manager obtains, serves and renews the box's public certificate.
type Manager struct {
	dir         string
	db          *store.DB
	sealer      *secret.Sealer
	directories map[string]string
	roots       *x509.CertPool
	providers   ProviderFactory
	propDelay   time.Duration
	propTimeout time.Duration
	resolvers   []string
	logger      *slog.Logger
	now         func() time.Time
	zeroSSLEAB  string

	mu     sync.RWMutex
	cfg    *certmagic.Config
	cache  *certmagic.Cache
	name   string
	issuer string
	// setupMu serialises Setup; a certificate is ordered once at a time.
	setupMu sync.Mutex
}

// New returns a manager that keeps its certificates in dir and its
// configuration in db, with credentials sealed by sealer.
func New(dir string, db *store.DB, sealer *secret.Sealer, opts ...Option) (*Manager, error) {
	m := &Manager{
		dir: dir, db: db, sealer: sealer, providers: defaultProviders,
		directories: map[string]string{
			LetsEncrypt: certmagic.LetsEncryptProductionCA,
			Actalis:     actalisCA,
			zeroSSL:     certmagic.ZeroSSLProductionCA,
		},
		logger: slog.Default(), now: time.Now, zeroSSLEAB: zeroSSLEABURL,
	}
	for _, o := range opts {
		o(m)
	}
	return m, nil
}

// Close stops certificate maintenance.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache != nil {
		m.cache.Stop()
		m.cache, m.cfg = nil, nil
	}
}

// Start resumes a stored setup: the certificate is loaded from disk and its
// renewal continues. Without a stored setup it does nothing.
func (m *Manager) Start(ctx context.Context) error {
	saved, ok, err := m.db.PublicHTTPS()
	if err != nil || !ok {
		return err
	}
	req, err := m.requestFrom(saved)
	if err != nil {
		return err
	}
	cfg, cache, err := m.newConfig(req)
	if err != nil {
		return err
	}
	// Load the stored certificate at once, so the first handshake after a
	// start needs no network; maintenance then continues in the background.
	if _, err := cfg.CacheManagedCertificate(ctx, saved.Name); err != nil {
		m.logger.Warn("stored HTTPS certificate not loaded; ordering it again", "err", err)
	}
	if err := cfg.ManageAsync(ctx, []string{saved.Name}); err != nil {
		cache.Stop()
		return err
	}
	m.install(cfg, cache, saved.Name, saved.Issuer)
	return nil
}

func (m *Manager) requestFrom(s store.PublicHTTPS) (Request, error) {
	token, err := m.sealer.Open(s.Token)
	if err != nil {
		return Request{}, fmt.Errorf("publiccert: DNS token: %w", err)
	}
	req := Request{Name: s.Name, Provider: s.Provider, Token: string(token), Issuer: s.Issuer, LANIP: s.LANIP, Email: s.Email, EABKeyID: s.EABKeyID}
	if len(s.EABHMAC) > 0 {
		hmac, err := m.sealer.Open(s.EABHMAC)
		if err != nil {
			return Request{}, fmt.Errorf("publiccert: account key: %w", err)
		}
		req.EABHMAC = string(hmac)
	}
	return req, nil
}

func (m *Manager) install(cfg *certmagic.Config, cache *certmagic.Cache, name, issuer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache != nil {
		m.cache.Stop()
	}
	m.cfg, m.cache, m.name, m.issuer = cfg, cache, name, issuer
}

// Ready reports whether the box has a certificate for its public name.
func (m *Manager) Ready() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg != nil
}

// Name is the box's public DNS name, empty before setup.
func (m *Manager) Name() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.name
}

// GetCertificate serves the certificate (tls.Config.GetCertificate). A client
// that sends no or another name (for example the LAN address) gets the box's
// name certificate too; the browser then reports the mismatch.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	cfg, name := m.cfg, m.name
	m.mu.RUnlock()
	if cfg == nil {
		return nil, errors.New("publiccert: HTTPS is not set up")
	}
	h := *hello
	h.ServerName = name
	return cfg.GetCertificate(&h)
}

// Status describes the certificate for the web interface.
func (m *Manager) Status() Status {
	m.mu.RLock()
	cfg, name, issuer := m.cfg, m.name, m.issuer
	m.mu.RUnlock()
	st := Status{Name: name, Issuer: issuer}
	if cfg == nil {
		return st
	}
	if c, err := cfg.CacheManagedCertificate(context.Background(), name); err == nil && c.Leaf != nil {
		st.Expires = c.Leaf.NotAfter
		st.Warning = m.now().Add(expirySoon).After(st.Expires)
	}
	if saved, ok, err := m.db.PublicHTTPS(); err == nil && ok && saved.RenewalError != "" {
		st.RenewalError, st.Warning = saved.RenewalError, true
	}
	return st
}

func (m *Manager) recordRenewal(err error) {
	text := ""
	if err != nil {
		text = err.Error()
		m.logger.Error("renew HTTPS certificate", "err", err)
	}
	if dbErr := m.db.SetRenewalResult(m.now(), text); dbErr != nil {
		m.logger.Error("store renewal result", "err", dbErr)
	}
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func (r Request) validate() error {
	if r.Token == "" {
		return invalid("DNS token missing")
	}
	if !validName(r.Name) {
		return invalid("%q is not a valid DNS name", r.Name)
	}
	if ip, err := netip.ParseAddr(r.LANIP); err != nil || ip.IsUnspecified() || ip.IsMulticast() {
		return invalid("%q is not a valid LAN address", r.LANIP)
	}
	switch r.Issuer {
	case LetsEncrypt:
	case Actalis:
		if r.EABKeyID == "" || r.EABHMAC == "" {
			return invalid("Actalis needs the key ID and HMAC key of an Actalis account")
		}
	default:
		return invalid("unknown issuer %q", r.Issuer)
	}
	return nil
}

func validName(name string) bool {
	if name != strings.ToLower(name) || len(name) > 253 || strings.Count(name, ".") < 1 {
		return false
	}
	for _, l := range strings.Split(name, ".") {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// Setup points the name to the box's LAN address, obtains the certificate
// and stores the configuration. It blocks until the certificate is there or
// the attempt failed; nothing is stored then.
func (m *Manager) Setup(ctx context.Context, req Request) error {
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	if err := req.validate(); err != nil {
		return err
	}
	provider, err := m.providers(req.Provider, req.Token)
	if err != nil {
		return err
	}
	m.setupMu.Lock()
	defer m.setupMu.Unlock()

	zone, err := certmagic.FindZoneByFQDN(ctx, zap.NewNop(), req.Name, certmagic.RecursiveNameservers(m.resolvers))
	if err != nil {
		return fmt.Errorf("find the DNS zone of %s: %w", req.Name, err)
	}
	ip, _ := netip.ParseAddr(req.LANIP)
	rec := libdns.Address{Name: libdns.RelativeName(req.Name+".", zone), IP: ip, TTL: 5 * time.Minute}
	if _, err := provider.SetRecords(ctx, zone, []libdns.Record{rec}); err != nil {
		return fmt.Errorf("point %s to %s: %w", req.Name, req.LANIP, err)
	}

	cfg, cache, err := m.newConfig(req)
	if err != nil {
		return err
	}
	if err := cfg.ManageSync(ctx, []string{req.Name}); err != nil {
		cache.Stop()
		return fmt.Errorf("obtain certificate for %s: %w", req.Name, err)
	}
	sealedToken, err := m.sealer.Seal([]byte(req.Token))
	if err != nil {
		cache.Stop()
		return err
	}
	saved := store.PublicHTTPS{Name: req.Name, Zone: strings.TrimSuffix(zone, "."), Provider: req.Provider, Issuer: req.Issuer, LANIP: req.LANIP, Email: req.Email, Token: sealedToken, EABKeyID: req.EABKeyID}
	if req.EABHMAC != "" {
		if saved.EABHMAC, err = m.sealer.Seal([]byte(req.EABHMAC)); err != nil {
			cache.Stop()
			return err
		}
	}
	if err := m.db.SavePublicHTTPS(saved); err != nil {
		cache.Stop()
		return err
	}
	m.install(cfg, cache, req.Name, req.Issuer)
	return nil
}

// newConfig builds the CertMagic configuration: the chosen issuer first, ZeroSSL as fallback.
func (m *Manager) newConfig(req Request) (*certmagic.Config, *certmagic.Cache, error) {
	provider, err := m.providers(req.Provider, req.Token)
	if err != nil {
		return nil, nil, err
	}
	solver := &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{
		DNSProvider: provider, PropagationDelay: m.propDelay, PropagationTimeout: m.propTimeout, Resolvers: m.resolvers,
	}}
	var cfg *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return cfg, nil },
		Logger:           zap.NewNop(),
	})
	cfg = certmagic.New(cache, certmagic.Config{
		Storage: &certmagic.FileStorage{Path: m.dir},
		Logger:  zap.NewNop(),
		OnEvent: m.onEvent,
	})
	newIssuer := func(ca string) *certmagic.ACMEIssuer {
		return certmagic.NewACMEIssuer(cfg, certmagic.ACMEIssuer{
			CA: ca, Email: req.Email, Agreed: true, TrustedRoots: m.roots, DNS01Solver: solver,
			DisableHTTPChallenge: true, DisableTLSALPNChallenge: true, Logger: zap.NewNop(),
		})
	}
	primary := newIssuer(m.directories[req.Issuer])
	if req.Issuer == Actalis {
		primary.ExternalAccount = &acme.EAB{KeyID: req.EABKeyID, MACKey: req.EABHMAC}
	}
	cfg.Issuers = []certmagic.Issuer{primary}
	if req.Email != "" { // ZeroSSL hands out account keys against an email address
		fallback := newIssuer(m.directories[zeroSSL])
		fallback.NewAccountFunc = func(ctx context.Context, iss *certmagic.ACMEIssuer, a acme.Account) (acme.Account, error) {
			eab, err := m.zeroSSLAccountKeys(ctx, req.Email)
			if err != nil {
				return a, err
			}
			iss.ExternalAccount = eab
			return a, nil
		}
		cfg.Issuers = append(cfg.Issuers, fallback)
	}
	return cfg, cache, nil
}

// onEvent turns CertMagic's background renewals into the state parents see.
func (m *Manager) onEvent(_ context.Context, event string, data map[string]any) error {
	renewal, _ := data["renewal"].(bool)
	if !renewal {
		return nil
	}
	switch event {
	case "cert_failed":
		err, _ := data["error"].(error)
		if err == nil {
			err = errors.New("renewal failed")
		}
		m.recordRenewal(err)
	case "cert_obtained":
		m.recordRenewal(nil)
	}
	return nil
}

// zeroSSLAccountKeys asks ZeroSSL for account keys for email.
func (m *Manager) zeroSSLAccountKeys(ctx context.Context, email string) (*acme.EAB, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.zeroSSLEAB, strings.NewReader(url.Values{"email": {email}}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ZeroSSL account keys: %w", err)
	}
	defer res.Body.Close()
	var out struct {
		Success bool   `json:"success"`
		KeyID   string `json:"eab_kid"`
		HMAC    string `json:"eab_hmac_key"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || !out.Success || out.KeyID == "" {
		return nil, fmt.Errorf("ZeroSSL account keys: status %d", res.StatusCode)
	}
	return &acme.EAB{KeyID: out.KeyID, MACKey: out.HMAC}, nil
}
