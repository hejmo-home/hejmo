// Package pki is the box's own certificate authority (ADR 0014): it creates a
// CA that is name-constrained to the box, issues the box's HTTPS certificate
// and renders the CA as an iOS configuration profile. Standard library only.
package pki

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"
)

const (
	caLifetime   = 10 * 365 * 24 * time.Hour
	leafLifetime = 800 * 24 * time.Hour // iOS rejects server certificates valid longer than 825 days
	renewBefore  = 30 * 24 * time.Hour
	renewRetry   = time.Hour // pause between renewal attempts
)

// alwaysNames are reachable on every box besides the name used during setup.
var alwaysNames = []string{"hejmo.local", "localhost"} // "hejmo.local" is the box name of passkeys (internal/passkey.LocalName)

// Store keeps the CA and the box certificate in a directory (mode 0700).
type Store struct {
	dir    string
	now    func() time.Time
	logger *slog.Logger

	mu     sync.RWMutex
	caDER  []byte
	caKey  crypto.Signer
	leaf   *tls.Certificate
	expiry time.Time
	// nextRenew is the earliest time of the next renewal attempt, so that a
	// failing renewal does not run on every TLS handshake.
	nextRenew time.Time
	// warnedCA is set once the "CA expires soon" warning has been logged.
	warnedCA bool
	// routerName is the name of the box in the family's router that the
	// certificate covers (decision Q92), or "".
	routerName string
}

// Option configures a Store.
type Option func(*Store)

// WithClock sets the clock that decides when the box certificate is renewed.
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// WithLogger sets where certificate problems are logged.
func WithLogger(l *slog.Logger) Option { return func(s *Store) { s.logger = l } }

// Open loads the store at dir, creating the directory if needed. A directory
// without a CA and certificate (also one whose provisioning was interrupted)
// yields a store that is not Ready.
func Open(dir string, opts ...Option) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, now: time.Now, logger: slog.Default()}
	for _, o := range opts {
		o(s)
	}
	if _, err := os.Stat(s.path("box.pem")); errors.Is(err, os.ErrNotExist) {
		return s, nil // box.pem is written last
	}
	caPEM, err := os.ReadFile(s.path("ca.pem"))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(s.path("ca-key.pem"))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		return nil, errors.New("pki: ca.pem is not PEM")
	}
	s.caDER = block.Bytes
	if s.caKey, err = parseKey(keyPEM); err != nil {
		return nil, err
	}
	boxPEM, err := os.ReadFile(s.path("box.pem")) // certificate, then its key
	if err != nil {
		return nil, err
	}
	leafCert, err := tls.X509KeyPair(boxPEM, boxPEM)
	if err != nil {
		return nil, err
	}
	if name, err := os.ReadFile(s.path(routerNameFile)); err == nil {
		s.routerName = strings.TrimSpace(string(name))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	s.setLeaf(&leafCert)
	return s, nil
}

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

// Ready reports whether the box has its CA and certificate.
func (s *Store) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.leaf != nil
}

// Provision creates the CA and the box certificate for host (the name or IP
// the browser used to reach the box). It fails if the store is already set up.
func (s *Store) Provision(host string, now time.Time) error {
	return s.ProvisionWithRouterName(host, "", now)
}

// ProvisionWithRouterName is Provision for a box that also has a name in the
// family's router (decision Q92): the CA permits that name and the certificate
// covers it. An empty routerName means none. A name that CleanRouterName
// refuses fails before anything is created.
func (s *Store) ProvisionWithRouterName(host, routerName string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leaf != nil {
		return errors.New("pki: already provisioned")
	}
	dns, ips, err := boxNames(host)
	if err != nil {
		return err
	}
	router := ""
	if strings.TrimSpace(routerName) != "" {
		if router, err = CleanRouterName(routerName, displayNames(dns, ips)); err != nil {
			return err
		}
		dns = append(dns, router)
	}

	caDER, caKey, err := newCA(now, dns, ips)
	if err != nil {
		return err
	}
	if err := writeKey(s.path("ca-key.pem"), caKey); err != nil {
		return err
	}
	if err := writeFile(s.path("ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644); err != nil {
		return err
	}
	if router != "" {
		if err := writeFile(s.path(routerNameFile), []byte(router+"\n"), 0o644); err != nil {
			return err
		}
	}
	if err := s.issueLocked(now, caDER, caKey, dns, ips); err != nil {
		return err
	}
	s.caDER, s.caKey, s.routerName = caDER, caKey, router // published only once the box is ready
	s.logger.Info("box CA created; its fingerprint is what a device shows when trusting it", "sha256", fingerprint(caDER))
	return nil
}

// routerNameFile keeps the router name next to the CA, so the settings page
// can show it. The certificate alone cannot say which of its names it is.
const routerNameFile = "router-name"

// newCA creates a CA that is name-constrained to the box's names.
func newCA(now time.Time, dns []string, ips []net.IP) ([]byte, *ecdsa.PrivateKey, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:                serial(),
		Subject:                     pkix.Name{CommonName: "Hejmo box CA", Organization: []string{"Hejmo"}},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    now.Add(caLifetime),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         dns,
		PermittedIPRanges:           ipRanges(ips),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &caKey.PublicKey, caKey)
	return der, caKey, err
}

// Reissue gives the box a new CA and certificate that also cover routerName,
// the name of the box in the family's router, and drops an earlier router
// name. The CA is name-constrained, so an existing CA cannot vouch for a new
// name: every device must trust the box again (ADR 0014). The new files are
// written under temporary names first, so a failure while writing them leaves
// the box as it was (a failed rename is not rolled back; repeat Reissue).
func (s *Store) Reissue(routerName string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leaf == nil || s.leaf.Leaf == nil {
		return errors.New("pki: HTTPS is not set up")
	}
	cur := s.leaf.Leaf
	name, err := CleanRouterName(routerName, displayNames(cur.DNSNames, cur.IPAddresses))
	if err != nil {
		return err
	}
	dns := slices.DeleteFunc(slices.Clone(cur.DNSNames), func(n string) bool { return s.routerName != "" && n == s.routerName })
	dns = append(dns, name)
	ips := cur.IPAddresses

	caDER, caKey, err := newCA(now, dns, ips)
	if err != nil {
		return err
	}
	boxPEM, leaf, err := s.buildLeaf(now, caDER, caKey, dns, ips)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		return err
	}
	// box.pem is last: it is the file that says the box is ready.
	err = s.replaceFiles(
		stagedFile{"ca-key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600},
		stagedFile{"ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o644},
		stagedFile{routerNameFile, []byte(name + "\n"), 0o644},
		stagedFile{"box.pem", boxPEM, 0o600},
	)
	if err != nil {
		return err
	}
	s.caDER, s.caKey, s.routerName, s.warnedCA = caDER, caKey, name, false
	s.setLeaf(leaf)
	s.logger.Info("box CA replaced for a new name; every device must trust it again", "name", name, "sha256", fingerprint(caDER))
	return nil
}

type stagedFile struct {
	name string
	data []byte
	perm os.FileMode
}

// replaceFiles replaces several files of the store as one step: all are
// written under temporary names, and only when that worked are they renamed
// into place, so a full disk or a missing permission leaves the old files.
func (s *Store) replaceFiles(files ...stagedFile) error {
	temps := make([]string, 0, len(files))
	cleanup := func() {
		for _, t := range temps {
			os.Remove(t)
		}
	}
	for _, f := range files {
		tmp, err := stage(s.dir, f.data, f.perm)
		if err != nil {
			cleanup()
			return err
		}
		temps = append(temps, tmp)
	}
	for i, f := range files {
		if err := os.Rename(temps[i], s.path(f.name)); err != nil {
			cleanup()
			return err
		}
	}
	return nil
}

// ErrInvalidHost means the name used to reach the box cannot go into a certificate.
var ErrInvalidHost = errors.New("pki: host is neither an IP address nor a valid DNS name")

func boxNames(host string) (dns []string, ips []net.IP, err error) {
	ips = []net.IP{net.IPv4(127, 0, 0, 1)}
	host = strings.ToLower(host)
	if ip := net.ParseIP(host); ip != nil {
		ips = append([]net.IP{ip}, ips...)
	} else if validDNSName(host) {
		dns = append(dns, host)
	} else {
		return nil, nil, fmt.Errorf("%w: %q", ErrInvalidHost, host)
	}
	for _, n := range alwaysNames {
		if n != host {
			dns = append(dns, n)
		}
	}
	return dns, ips, nil
}

// validDNSName accepts host names of at least two labels (letters, digits and
// hyphens), or "localhost". Single labels, IPv6 zones and other forms are out.
func validDNSName(name string) bool {
	if name == "localhost" {
		return true
	}
	if len(name) > 253 {
		return false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
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

func ipRanges(ips []net.IP) []*net.IPNet {
	var out []*net.IPNet
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(len(ip)*8, len(ip)*8)})
	}
	return out
}

func (s *Store) issueLocked(now time.Time, caDER []byte, caKey crypto.Signer, dns []string, ips []net.IP) error {
	boxPEM, leaf, err := s.buildLeaf(now, caDER, caKey, dns, ips)
	if err != nil {
		return err
	}
	// One file, replaced atomically, so certificate and key always match.
	if err := writeFile(s.path("box.pem"), boxPEM, 0o600); err != nil {
		return err
	}
	s.setLeaf(leaf)
	return nil
}

// buildLeaf makes the box certificate for the names, signed by the CA, and
// the file content that holds it with its key. It writes nothing.
func (s *Store) buildLeaf(now time.Time, caDER []byte, caKey crypto.Signer, dns []string, ips []net.IP) ([]byte, *tls.Certificate, error) {
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	notAfter := leafExpiry(now, ca)
	if notAfter.Equal(ca.NotAfter) && !s.warnedCA {
		s.warnedCA = true
		s.logger.Warn("CA expires soon; the box needs a new CA", "expires", ca.NotAfter)
	}
	tpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "Hejmo box"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	boxPEM := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...)
	return boxPEM, &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// leafExpiry is when a certificate issued now ends: a certificate cannot
// outlive its CA.
func leafExpiry(now time.Time, ca *x509.Certificate) time.Time {
	if end := now.Add(leafLifetime); end.Before(ca.NotAfter) {
		return end
	}
	return ca.NotAfter
}

func (s *Store) setLeaf(c *tls.Certificate) {
	s.leaf = c
	if parsed, err := x509.ParseCertificate(c.Certificate[0]); err == nil {
		s.expiry = parsed.NotAfter
		c.Leaf = parsed
	}
}

// GetCertificate serves the box certificate (tls.Config.GetCertificate). It
// renews the certificate for the same names shortly before it expires. A
// failed renewal is logged; the current certificate keeps being served while
// it is still valid.
func (s *Store) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.mu.RLock()
	c, expiry := s.leaf, s.expiry
	s.mu.RUnlock()
	if c == nil {
		return nil, errors.New("pki: HTTPS is not set up")
	}
	if expiry.Sub(s.now()) > renewBefore {
		return c, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.expiry.Sub(now) <= renewBefore && s.leaf.Leaf != nil && !now.Before(s.nextRenew) && s.renewalHelps(now) {
		s.nextRenew = now.Add(renewRetry)
		if err := s.issueLocked(now, s.caDER, s.caKey, s.leaf.Leaf.DNSNames, s.leaf.Leaf.IPAddresses); err != nil {
			s.logger.Error("renew box certificate", "err", err, "expires", s.expiry)
		}
	}
	if !now.Before(s.expiry) {
		return nil, errors.New("pki: box certificate expired and could not be renewed")
	}
	return s.leaf, nil
}

// renewalHelps reports whether a certificate issued now would last longer than
// the current one; near the CA's expiry it would not.
func (s *Store) renewalHelps(now time.Time) bool {
	ca, err := x509.ParseCertificate(s.caDER)
	return err != nil || leafExpiry(now, ca).After(s.expiry)
}

// CAFingerprint returns the CA certificate's SHA-256 fingerprint as colon
// separated upper-case hex, for comparing what a device installed.
func (s *Store) CAFingerprint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.caDER == nil {
		return ""
	}
	return fingerprint(s.caDER)
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	return strings.Join(parts, ":")
}

// CAPEM returns the CA certificate, PEM encoded, for download.
func (s *Store) CAPEM() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.caDER == nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.caDER})
}

// Names are the names and addresses the box certificate covers, as the
// settings page lists them: the box name first, then the address of the box in
// the home network, then the router name. Names that only serve the box itself
// (localhost, 127.0.0.1) are left out.
func (s *Store) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leaf == nil || s.leaf.Leaf == nil {
		return nil
	}
	return displayNames(s.leaf.Leaf.DNSNames, s.leaf.Leaf.IPAddresses)
}

// LANAddress is the address of the box in the home network that the
// certificate covers, or "" when the box was set up under a name.
func (s *Store) LANAddress() string {
	for _, n := range s.Names() {
		if net.ParseIP(n) != nil {
			return n
		}
	}
	return ""
}

// RouterName is the name of the box in the family's router that the
// certificate covers, or "" when it has none.
func (s *Store) RouterName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.routerName
}

// CAExpires is when the CA ends (10 years after it was made).
func (s *Store) CAExpires() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ca, err := x509.ParseCertificate(s.caDER); err == nil {
		return ca.NotAfter
	}
	return time.Time{}
}

// PlannedNames are the names Provision would cover for host and routerName
// (empty for none), in the order of Names. It reports what the wizard shows
// before the certificate exists, and refuses what Provision would refuse.
func PlannedNames(host, routerName string) ([]string, error) {
	dns, ips, err := boxNames(host)
	if err != nil {
		return nil, err
	}
	shown := displayNames(dns, ips)
	if strings.TrimSpace(routerName) != "" {
		name, err := CleanRouterName(routerName, shown)
		if err != nil {
			return nil, err
		}
		dns = append(dns, name)
	}
	return displayNames(dns, ips), nil
}

// displayNames orders the names of a certificate for people. The last address
// is the box's own loopback address that every certificate has; it is shown
// only when it is the address the box was reached under.
func displayNames(dns []string, ips []net.IP) []string {
	var out []string
	add := func(n string) {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	if slices.Contains(dns, alwaysNames[0]) {
		add(alwaysNames[0])
	}
	if n := len(ips); n > 0 && ips[n-1].Equal(net.IPv4(127, 0, 0, 1)) {
		ips = ips[:n-1]
		if n == 2 && ips[0].Equal(net.IPv4(127, 0, 0, 1)) { // the box was reached under 127.0.0.1
			ips = ips[:1]
		}
	}
	for _, ip := range ips {
		add(ip.String())
	}
	for _, n := range dns {
		if n != "localhost" {
			add(n)
		}
	}
	return out
}

// Why a router name is refused. Each has its own text on the page.
var (
	ErrNameEmpty      = errors.New("pki: the name is empty")
	ErrNameNoDot      = errors.New("pki: the name needs a dot")
	ErrNameChars      = errors.New("pki: the name may hold only letters a to z, digits, hyphens and dots")
	ErrNameIsAddress  = errors.New("pki: an address is not a name")
	ErrAddressCovered = errors.New("pki: the certificate covers this address already")
	ErrNameCovered    = errors.New("pki: the certificate covers this name already")
)

// CleanRouterName checks a name of the box in the router and returns it as the
// certificate holds it: lower case, without spaces around it and without the
// dot that ends a full DNS name. covered are the names and addresses the
// certificate covers already (see Names).
func CleanRouterName(raw string, covered []string) (string, error) {
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if name == "" {
		return "", ErrNameEmpty
	}
	if ip := net.ParseIP(name); ip != nil {
		for _, c := range covered {
			if cip := net.ParseIP(c); cip != nil && cip.Equal(ip) {
				return "", ErrAddressCovered
			}
		}
		return "", ErrNameIsAddress
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			return "", ErrNameChars
		}
	}
	if !strings.Contains(name, ".") {
		return "", ErrNameNoDot
	}
	if !validDNSName(name) {
		return "", ErrNameChars
	}
	if slices.Contains(covered, name) || name == "localhost" {
		return "", ErrNameCovered
	}
	return name, nil
}

var profileTemplate = template.Must(template.New("profile").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
			<key>PayloadIdentifier</key>
			<string>org.hejmo.box.ca.cert</string>
			<key>PayloadUUID</key>
			<string>{{.CertUUID}}</string>
			<key>PayloadDisplayName</key>
			<string>Hejmo box CA</string>
			<key>PayloadContent</key>
			<data>{{.Data}}</data>
		</dict>
	</array>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
	<key>PayloadIdentifier</key>
	<string>org.hejmo.box.ca</string>
	<key>PayloadUUID</key>
	<string>{{.ProfileUUID}}</string>
	<key>PayloadDisplayName</key>
	<string>Hejmo</string>
	<key>PayloadDescription</key>
	<string>Lets this device trust your Hejmo box. The certificate only vouches for the box itself.</string>
</dict>
</plist>
`))

// MobileConfig renders the CA as an unsigned iOS configuration profile. The
// UUIDs derive from the certificate, so reinstalling replaces the old profile.
func (s *Store) MobileConfig() ([]byte, error) {
	s.mu.RLock()
	der := s.caDER
	s.mu.RUnlock()
	if der == nil {
		return nil, errors.New("pki: HTTPS is not set up")
	}
	sum := sha256.Sum256(der)
	var buf bytes.Buffer
	err := profileTemplate.Execute(&buf, map[string]string{
		"Data":        base64.StdEncoding.EncodeToString(der),
		"CertUUID":    uuidFrom(sum[:16]),
		"ProfileUUID": uuidFrom(sum[16:]),
	})
	return buf.Bytes(), err
}

func uuidFrom(b []byte) string {
	return fmt.Sprintf("%X-%X-%X-%X-%X", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		panic(err)
	}
	return n.Add(n, big.NewInt(1))
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}

// writeFile replaces path atomically: it writes a temporary file next to it
// and renames it, so a crash never leaves a half-written file.
func writeFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := stage(filepath.Dir(path), data, perm)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// stage writes data to a new temporary file in dir and returns its name.
func stage(dir string, data []byte, perm os.FileMode) (string, error) {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func parseKey(keyPEM []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("pki: key is not PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	signer, ok := k.(crypto.Signer)
	if !ok {
		return nil, errors.New("pki: key cannot sign")
	}
	return signer, nil
}
