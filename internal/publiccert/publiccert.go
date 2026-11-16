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

// ErrNameTaken means the name already points to a public address or is a
// CNAME: the box never overwrites it, the family's website could be gone.
var ErrNameTaken = errors.New("publiccert: the name is in use")

// NameTakenError says what the name points to and offers another name.
type NameTakenError struct {
	Name       string
	Address    string // the public address, or the target of the CNAME
	Suggestion string
}

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("%s: %s already points to %s", ErrNameTaken, e.Name, e.Address)
}

// Is makes errors.Is(err, ErrNameTaken) true.
func (e *NameTakenError) Is(target error) bool { return target == ErrNameTaken }

// DNSProvider is what the box needs from a DNS provider: setting the box's
// address record, the TXT records of the DNS-01 challenge, and listing the zone
// (to see what a name already points to before it is overwritten).
type DNSProvider interface {
	certmagic.DNSProvider
	libdns.RecordSetter
	libdns.RecordGetter
}

// Request is what the wizard collects.
type Request struct {
	Name     string // DNS name of the box, e.g. familie.dedyn.io
	Provider string // DNS provider, see Providers
	Token    string // API token of the DNS provider (deSEC, Hetzner, Cloudflare)
	// ZoneToken is Cloudflare's optional second token that only reads zones.
	ZoneToken string
	// Username and Password sign in at INWX. TwoFA says INWX asks for a code
	// from an app; OTPSecret is then the shared secret that makes the codes.
	Username, Password, OTPSecret string
	TwoFA                         bool
	Issuer                        string // LetsEncrypt or Actalis
	LANIP                         string // the box's address on the home network
	Email                         string // optional contact for the certificate authority; needed for the ZeroSSL fallback
	// EABKeyID and EABHMAC are the issuer's external account binding
	// (required for Actalis): key identifier and base64url HMAC key.
	EABKeyID, EABHMAC string
}

func (r Request) credentials() Credentials {
	return Credentials{Token: r.Token, ZoneToken: r.ZoneToken, Username: r.Username, Password: r.Password, OTPSecret: r.OTPSecret}
}

// sealable is what is sealed with the box's key. A deSEC-style setup with a
// single token keeps the plain token (the format of earlier versions); several
// credentials are stored as JSON.
func (c Credentials) sealable() []byte {
	if c.ZoneToken == "" && c.Username == "" && c.Password == "" && c.OTPSecret == "" {
		return []byte(c.Token)
	}
	b, _ := json.Marshal(c)
	return b
}

func openCredentials(plain []byte) Credentials {
	if len(plain) > 0 && plain[0] == '{' {
		var c Credentials
		if json.Unmarshal(plain, &c) == nil {
			return c
		}
	}
	return Credentials{Token: string(plain)}
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
	// expires is the certificate's end, read when it is loaded or renewed.
	expires time.Time
	// startErr is why a stored setup could not be resumed; empty if it could.
	startErr string
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
// renewal continues. Without a stored setup it does nothing. If the stored
// setup cannot be resumed (for example the key file was lost, so the token
// cannot be opened), the manager stays not ready and Status warns parents; the
// public name can then be set up again.
func (m *Manager) Start(ctx context.Context) error {
	err := m.start(ctx)
	m.mu.Lock()
	m.startErr = ""
	if err != nil {
		m.startErr = err.Error()
	}
	m.mu.Unlock()
	return err
}

func (m *Manager) start(ctx context.Context) error {
	saved, ok, err := m.db.PublicHTTPS()
	if err != nil || !ok {
		return err
	}
	req, err := m.requestFrom(saved)
	if err != nil {
		// The token is only needed to order and renew: the stored
		// certificate is still served, so HTTPS keeps working (ADR 0014).
		if serveErr := m.serveStored(ctx, saved); serveErr != nil {
			m.logger.Warn("stored HTTPS certificate cannot be served either", "err", serveErr)
		}
		return err
	}
	cfg, cache, err := m.newConfig(req, true)
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
	m.refreshExpiry(ctx)
	return nil
}

// noRenewal stands in for an issuer when the DNS token cannot be opened. It
// carries the real issuer's key because CertMagic stores certificates per
// issuer.
type noRenewal struct{ key string }

func (noRenewal) Issue(context.Context, *x509.CertificateRequest) (*certmagic.IssuedCertificate, error) {
	return nil, errors.New("the DNS token cannot be opened")
}
func (n noRenewal) IssuerKey() string { return n.key }

// serveStored serves the certificate on disk without being able to renew it.
// It was issued by the chosen issuer or by the ZeroSSL fallback.
func (m *Manager) serveStored(ctx context.Context, saved store.PublicHTTPS) error {
	var lastErr error
	for _, issuer := range []string{saved.Issuer, zeroSSL} {
		var cfg *certmagic.Config
		cache := certmagic.NewCache(certmagic.CacheOptions{
			GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return cfg, nil },
			Logger:           zap.NewNop(),
		})
		cfg = certmagic.New(cache, certmagic.Config{Storage: &certmagic.FileStorage{Path: m.dir}, Logger: zap.NewNop(), OnEvent: func(ctx context.Context, event string, data map[string]any) error {
			return m.onEvent(ctx, event, data, true)
		}})
		key := certmagic.NewACMEIssuer(cfg, certmagic.ACMEIssuer{CA: m.directories[issuer], Logger: zap.NewNop()}).IssuerKey()
		cfg.Issuers = []certmagic.Issuer{noRenewal{key}}
		if _, lastErr = cfg.CacheManagedCertificate(ctx, saved.Name); lastErr == nil {
			m.install(cfg, cache, saved.Name, saved.Issuer)
			m.refreshExpiry(ctx)
			return nil
		}
		cache.Stop()
	}
	return lastErr
}

func (m *Manager) requestFrom(s store.PublicHTTPS) (Request, error) {
	plain, err := m.sealer.Open(s.Token)
	if err != nil {
		return Request{}, fmt.Errorf("publiccert: DNS credentials: %w", err)
	}
	c := openCredentials(plain)
	req := Request{Name: s.Name, Provider: s.Provider, Token: c.Token, ZoneToken: c.ZoneToken, Username: c.Username, Password: c.Password, OTPSecret: c.OTPSecret, TwoFA: c.OTPSecret != "",
		Issuer: s.Issuer, LANIP: s.LANIP, Email: s.Email, EABKeyID: s.EABKeyID}
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
	m.cfg, m.cache, m.name, m.issuer, m.startErr = cfg, cache, name, issuer, ""
	m.expires = time.Time{}
}

// refreshExpiry reads the end of the certificate from the store, after it was
// obtained or renewed; Status then needs no disk access.
func (m *Manager) refreshExpiry(ctx context.Context) {
	m.mu.RLock()
	cfg, name := m.cfg, m.name
	m.mu.RUnlock()
	if cfg == nil {
		return
	}
	c, err := cfg.CacheManagedCertificate(ctx, name)
	if err != nil || c.Leaf == nil {
		return
	}
	m.mu.Lock()
	if m.cfg == cfg {
		m.expires = c.Leaf.NotAfter
	}
	m.mu.Unlock()
}

// Clear forgets the public setup, with its sealed token: the family goes back
// to the box's own CA. The box then serves no public certificate. The
// address record at the DNS provider is left as it is: the token is not
// kept long enough to remove it, and a stale record does no harm.
func (m *Manager) Clear() error {
	m.setupMu.Lock()
	defer m.setupMu.Unlock()
	if err := m.db.DeletePublicHTTPS(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache != nil {
		m.cache.Stop()
	}
	m.cfg, m.cache, m.name, m.issuer, m.startErr = nil, nil, "", "", ""
	m.expires = time.Time{}
	return nil
}

// Ready reports whether a public setup is installed. Its certificate may still
// be missing, see HasCertificate.
func (m *Manager) Ready() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg != nil
}

// HasCertificate reports whether a certificate is loaded to serve. It is false
// for a stored setup whose certificate could not be loaded and is not yet
// obtained, although Ready is true then.
func (m *Manager) HasCertificate() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg != nil && !m.expires.IsZero()
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
	cfg, name, issuer, expires, startErr := m.cfg, m.name, m.issuer, m.expires, m.startErr
	m.mu.RUnlock()
	st := Status{Name: name, Issuer: issuer}
	if startErr != "" {
		st.Warning, st.RenewalError = true, startErr
	}
	if cfg == nil {
		return st
	}
	if !expires.IsZero() {
		st.Expires = expires
		st.Warning = st.Warning || m.now().Add(expirySoon).After(expires)
	}
	if saved, ok, err := m.db.PublicHTTPS(); err == nil && ok && saved.RenewalError != "" && startErr == "" {
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

// InvalidError is an ErrInvalid that names the form field of the wizard it
// belongs to: token, username, password, otp_secret, name, lan_ip, issuer or
// eab (the account keys of Actalis).
type InvalidError struct {
	Field string
	msg   string
}

func (e *InvalidError) Error() string { return ErrInvalid.Error() + ": " + e.msg }

// Is makes errors.Is(err, ErrInvalid) true.
func (e *InvalidError) Is(target error) bool { return target == ErrInvalid }

func invalid(field, format string, args ...any) error {
	return &InvalidError{Field: field, msg: fmt.Sprintf(format, args...)}
}

func (r Request) validate() error {
	if r.Provider == "inwx" {
		if r.Username == "" {
			return invalid("username", "INWX user name missing")
		}
		if r.Password == "" {
			return invalid("password", "INWX password missing")
		}
		if r.TwoFA && !validSecret(r.OTPSecret) {
			return invalid("otp_secret", "the INWX shared secret is missing or not base32 of 16 to 32 characters")
		}
	} else if r.Token == "" {
		return invalid("token", "DNS token missing")
	}
	if !validName(r.Name) {
		return invalid("name", "%q is not a valid DNS name", r.Name)
	}
	if ip, err := netip.ParseAddr(r.LANIP); err != nil || ip.IsUnspecified() || ip.IsMulticast() {
		return invalid("lan_ip", "%q is not a valid LAN address", r.LANIP)
	}
	switch r.Issuer {
	case LetsEncrypt:
	case Actalis:
		if r.EABKeyID == "" || r.EABHMAC == "" {
			return invalid("eab", "Actalis needs the key ID and HMAC key of an Actalis account")
		}
	default:
		return invalid("issuer", "unknown issuer %q", r.Issuer)
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
	if req.TwoFA {
		req.OTPSecret = cleanSecret(req.OTPSecret)
	} else {
		req.OTPSecret = "" // a secret of a switched-off 2FA is not kept
	}
	if err := req.validate(); err != nil {
		return err
	}
	provider, err := m.providers(req.Provider, req.credentials())
	if err != nil {
		return err
	}
	m.setupMu.Lock()
	defer m.setupMu.Unlock()

	zone, err := certmagic.FindZoneByFQDN(ctx, zap.NewNop(), req.Name, certmagic.RecursiveNameservers(m.resolvers))
	if err != nil {
		return fmt.Errorf("find the DNS zone of %s: %w", req.Name, err)
	}
	rel := libdns.RelativeName(req.Name+".", zone)
	if err := m.prepareName(ctx, provider, req.Name, zone, rel); err != nil {
		return err
	}
	ip, _ := netip.ParseAddr(req.LANIP)
	rec := libdns.Address{Name: rel, IP: ip, TTL: 5 * time.Minute}
	if _, err := provider.SetRecords(ctx, zone, []libdns.Record{rec}); err != nil {
		return fmt.Errorf("point %s to %s: %w", req.Name, req.LANIP, err)
	}

	cfg, cache, err := m.newConfig(req, false)
	if err != nil {
		return err
	}
	if err := cfg.ManageSync(ctx, []string{req.Name}); err != nil {
		cache.Stop()
		// A failed attempt is not a broken setup: no warning stays behind.
		m.mu.Lock()
		m.startErr = ""
		m.mu.Unlock()
		return fmt.Errorf("obtain certificate for %s: %w", req.Name, err)
	}
	sealedToken, err := m.sealer.Seal(req.credentials().sealable())
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
	m.refreshExpiry(ctx)
	return nil
}

// prepareName looks at what the zone holds for the name before anything is
// written: a public address or a CNAME is never overwritten (the family's
// website would be gone), a private address of an earlier box is replaced. Old
// challenge records of this name are removed, so a provider that cannot handle
// several values at one name is not confused by a failed earlier attempt.
func (m *Manager) prepareName(ctx context.Context, provider DNSProvider, name, zone, rel string) error {
	existing, err := provider.GetRecords(ctx, zone)
	if err != nil {
		return fmt.Errorf("list the records of %s: %w", zone, err)
	}
	challenge := "_acme-challenge"
	if rel != "@" {
		challenge += "." + rel
	}
	var stale []libdns.Record
	for _, rec := range existing {
		rr := rec.RR()
		switch {
		case rr.Name == rel && rr.Type == "CNAME":
			return m.nameTaken(name, zone, rr.Data)
		case rr.Name == rel && (rr.Type == "A" || rr.Type == "AAAA"):
			if ip, err := netip.ParseAddr(rr.Data); err != nil || !ip.IsPrivate() {
				return m.nameTaken(name, zone, rr.Data)
			}
		case rr.Name == challenge && rr.Type == "TXT":
			stale = append(stale, rec)
		}
	}
	if len(stale) > 0 {
		if _, err := provider.DeleteRecords(ctx, zone, stale); err != nil {
			m.logger.Warn("stale challenge records not removed", "name", name, "err", err) // a real problem shows when ordering
		}
	}
	return nil
}

func (m *Manager) nameTaken(name, zone, address string) error {
	zone = strings.TrimSuffix(zone, ".")
	suggestion := "box." + zone
	if name == suggestion {
		suggestion = "hejmo." + zone
	}
	return &NameTakenError{Name: name, Address: address, Suggestion: suggestion}
}

// newConfig builds the CertMagic configuration: the chosen issuer first, ZeroSSL as fallback.
// resuming is true for a stored setup being resumed at start: only then does a
// failed order count as a broken setup (see onEvent).
func (m *Manager) newConfig(req Request, resuming bool) (*certmagic.Config, *certmagic.Cache, error) {
	provider, err := m.providers(req.Provider, req.credentials())
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
		OnEvent: func(ctx context.Context, event string, data map[string]any) error {
			return m.onEvent(ctx, event, data, resuming)
		},
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

// onEvent turns CertMagic's background work into the state parents see.
func (m *Manager) onEvent(ctx context.Context, event string, data map[string]any, resuming bool) error {
	renewal, _ := data["renewal"].(bool)
	switch event {
	case "cert_failed":
		err, _ := data["error"].(error)
		if err == nil {
			err = errors.New("renewal failed")
		}
		if !renewal {
			if !resuming {
				return nil // an attempt of the wizard: Setup reports it to the caller
			}
			// A certificate ordered again after a restart with lost files.
			m.mu.Lock()
			m.startErr = err.Error()
			m.mu.Unlock()
			return nil
		}
		m.recordRenewal(err)
	case "cert_obtained":
		m.mu.Lock()
		if !renewal {
			m.startErr = ""
		}
		m.mu.Unlock()
		if renewal {
			m.recordRenewal(nil)
		}
		m.refreshExpiry(context.WithoutCancel(ctx)) // also a certificate ordered again after a restart
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
