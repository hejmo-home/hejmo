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
	"strings"
	"sync"
	"text/template"
	"time"
)

const (
	caLifetime   = 10 * 365 * 24 * time.Hour
	leafLifetime = 800 * 24 * time.Hour // iOS rejects server certificates valid longer than 825 days
	renewBefore  = 30 * 24 * time.Hour
)

// alwaysNames are reachable on every box besides the name used during setup.
var alwaysNames = []string{"hejmo.local", "localhost"}

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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leaf != nil {
		return errors.New("pki: already provisioned")
	}
	dns, ips, err := boxNames(host)
	if err != nil {
		return err
	}

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
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
	if err != nil {
		return err
	}
	if err := writeKey(s.path("ca-key.pem"), caKey); err != nil {
		return err
	}
	if err := writeFile(s.path("ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	s.caDER, s.caKey = der, caKey
	return s.issueLocked(now, dns, ips)
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

func (s *Store) issueLocked(now time.Time, dns []string, ips []net.IP) error {
	ca, err := x509.ParseCertificate(s.caDER)
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "Hejmo box"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, s.caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	// One file, replaced atomically, so certificate and key always match.
	boxPEM := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...)
	if err := writeFile(s.path("box.pem"), boxPEM, 0o600); err != nil {
		return err
	}
	s.setLeaf(&tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key})
	return nil
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
	if s.expiry.Sub(s.now()) <= renewBefore && s.leaf.Leaf != nil {
		if err := s.issueLocked(s.now(), s.leaf.Leaf.DNSNames, s.leaf.Leaf.IPAddresses); err != nil {
			s.logger.Error("renew box certificate", "err", err, "expires", s.expiry)
			if !s.now().Before(s.expiry) {
				return nil, err
			}
		}
	}
	return s.leaf, nil
}

// CAFingerprint returns the CA certificate's SHA-256 fingerprint as colon
// separated upper-case hex, for comparing what a device installed.
func (s *Store) CAFingerprint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.caDER == nil {
		return ""
	}
	sum := sha256.Sum256(s.caDER)
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
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
