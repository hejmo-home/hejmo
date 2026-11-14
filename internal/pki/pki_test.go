package pki_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"encoding/xml"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/pki"
)

var now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func provisioned(t *testing.T, host string) *pki.Store {
	t.Helper()
	s, err := pki.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Provision(host, now); err != nil {
		t.Fatal(err)
	}
	return s
}

func caCert(t *testing.T, s *pki.Store) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(s.CAPEM())
	if block == nil {
		t.Fatal("CA is not PEM")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFreshStoreIsNotReady(t *testing.T) {
	s, err := pki.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.Ready() {
		t.Fatal("fresh store reports ready")
	}
}

// Acceptance: Given the box's CA, when it is inspected, then it carries name
// constraints limited to the box.
func TestCAIsNameConstrainedToTheBox(t *testing.T) {
	s := provisioned(t, "192.168.1.20")
	ca := caCert(t, s)
	if !ca.IsCA || !ca.PermittedDNSDomainsCritical {
		t.Fatalf("IsCA=%v critical=%v", ca.IsCA, ca.PermittedDNSDomainsCritical)
	}
	if len(ca.PermittedIPRanges) != 2 || ca.PermittedIPRanges[0].String() != "192.168.1.20/32" {
		t.Errorf("IP ranges = %v", ca.PermittedIPRanges)
	}
	for _, want := range []string{"hejmo.local", "localhost"} {
		if !slices.Contains(ca.PermittedDNSDomains, want) {
			t.Errorf("DNS domains = %v, missing %s", ca.PermittedDNSDomains, want)
		}
	}
}

func TestBoxCertificateIsTrustedForBoxNamesOnly(t *testing.T) {
	s := provisioned(t, "192.168.1.20")
	pool := x509.NewCertPool()
	pool.AddCert(caCert(t, s))

	cert, err := s.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	for _, name := range []string{"192.168.1.20", "hejmo.local", "localhost"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: name, CurrentTime: now}); err != nil {
			t.Errorf("verify %s: %v", name, err)
		}
	}

	// The CA key must not be able to vouch for other sites.
	foreign := signWithCA(t, s, "bank.example.com")
	if _, err := foreign.Verify(x509.VerifyOptions{Roots: pool, DNSName: "bank.example.com", CurrentTime: now}); err == nil {
		t.Error("CA vouches for a foreign name")
	}
}

func TestProvisionedStoreSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, _ := pki.Open(dir)
	if err := s.Provision("hejmo.local", now); err != nil {
		t.Fatal(err)
	}
	again, err := pki.Open(dir)
	if err != nil || !again.Ready() {
		t.Fatalf("reopen: err=%v", err)
	}
	if string(again.CAPEM()) != string(s.CAPEM()) {
		t.Error("CA changed across restart")
	}
}

func TestProvisionOnlyOnce(t *testing.T) {
	s := provisioned(t, "hejmo.local")
	if err := s.Provision("other.local", now); err == nil {
		t.Error("second Provision succeeded")
	}
}

func TestMobileConfigCarriesTheCA(t *testing.T) {
	s := provisioned(t, "hejmo.local")
	profile, err := s.MobileConfig()
	if err != nil {
		t.Fatal(err)
	}
	var plist struct {
		Dict struct {
			Strings []string `xml:"string"`
			Array   struct {
				Dict struct {
					Strings []string `xml:"string"`
					Data    []string `xml:"data"`
				} `xml:"dict"`
			} `xml:"array"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(profile, &plist); err != nil {
		t.Fatalf("profile is not XML: %v\n%s", err, profile)
	}
	payload := plist.Dict.Array.Dict
	if !slices.Contains(payload.Strings, "com.apple.security.root") || !slices.Contains(plist.Dict.Strings, "Configuration") {
		t.Errorf("payload types missing\n%s", profile)
	}
	want := base64.StdEncoding.EncodeToString(caCert(t, s).Raw)
	if len(payload.Data) != 1 || strings.Join(strings.Fields(payload.Data[0]), "") != want {
		t.Errorf("payload does not carry the CA certificate")
	}
}

func signWithCA(t *testing.T, s *pki.Store, name string) *x509.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: name},
		DNSNames: []string{name}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert(t, s), &key.PublicKey, pki.CAKeyForTest(s))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}
