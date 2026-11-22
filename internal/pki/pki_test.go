package pki_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"encoding/xml"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
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

// Renewal: a certificate close to its end is replaced by one for the same
// names that the CA vouches for, and the box keeps it across a restart.
func TestCertificateIsRenewedBeforeItExpires(t *testing.T) {
	dir := t.TempDir()
	clock := now
	s, err := pki.Open(dir, pki.WithClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Provision("192.168.1.20", now); err != nil {
		t.Fatal(err)
	}
	first, _ := s.GetCertificate(nil)
	oldLeaf, _ := x509.ParseCertificate(first.Certificate[0])

	clock = now.Add(771 * 24 * time.Hour)
	second, err := s.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	newLeaf, _ := x509.ParseCertificate(second.Certificate[0])
	if newLeaf.SerialNumber.Cmp(oldLeaf.SerialNumber) == 0 || !newLeaf.NotAfter.After(oldLeaf.NotAfter) {
		t.Fatal("certificate was not renewed")
	}
	if !slices.Equal(newLeaf.DNSNames, oldLeaf.DNSNames) || len(newLeaf.IPAddresses) != len(oldLeaf.IPAddresses) {
		t.Fatalf("names changed: %v %v -> %v %v", oldLeaf.DNSNames, oldLeaf.IPAddresses, newLeaf.DNSNames, newLeaf.IPAddresses)
	}
	for i := range oldLeaf.IPAddresses {
		if !newLeaf.IPAddresses[i].Equal(oldLeaf.IPAddresses[i]) {
			t.Fatalf("IPs changed: %v -> %v", oldLeaf.IPAddresses, newLeaf.IPAddresses)
		}
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert(t, s))
	for _, name := range []string{"192.168.1.20", "hejmo.local", "localhost"} {
		if _, err := newLeaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: name, CurrentTime: clock}); err != nil {
			t.Errorf("renewed certificate, verify %s: %v", name, err)
		}
	}

	again, err := pki.Open(dir, pki.WithClock(func() time.Time { return clock }))
	if err != nil || !again.Ready() {
		t.Fatalf("reopen: %v", err)
	}
	got, _ := again.GetCertificate(nil)
	if !bytes.Equal(got.Certificate[0], second.Certificate[0]) {
		t.Error("renewed certificate was not kept across a restart")
	}
}

func TestFailedRenewalIsLoggedAndKeepsTheValidCertificate(t *testing.T) {
	dir := t.TempDir()
	clock := now
	var logs bytes.Buffer
	s, err := pki.Open(dir, pki.WithClock(func() time.Time { return clock }), pki.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Provision("hejmo.local", now); err != nil {
		t.Fatal(err)
	}
	first, _ := s.GetCertificate(nil)

	if err := os.Chmod(dir, 0o500); err != nil { // make writing the renewed certificate fail
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	clock = now.Add(771 * 24 * time.Hour)
	got, err := s.GetCertificate(nil)
	if err != nil || !bytes.Equal(got.Certificate[0], first.Certificate[0]) {
		t.Fatalf("still valid certificate not served: err=%v", err)
	}
	if !strings.Contains(logs.String(), "renew") {
		t.Errorf("failure not logged: %q", logs.String())
	}

	clock = now.Add(801 * 24 * time.Hour) // expired and cannot be renewed
	if _, err := s.GetCertificate(nil); err == nil {
		t.Error("expired certificate served without error")
	}
}

func TestInterruptedProvisioningLeavesARestartableStore(t *testing.T) {
	dir := t.TempDir()
	s, _ := pki.Open(dir)
	if err := s.Provision("hejmo.local", now); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "box.pem")); err != nil { // as if it stopped before the last file
		t.Fatal(err)
	}
	again, err := pki.Open(dir)
	if err != nil {
		t.Fatalf("open after interrupted provisioning: %v", err)
	}
	if again.Ready() {
		t.Fatal("store without certificate reports ready")
	}
	if err := again.Provision("hejmo.local", now); err != nil || !again.Ready() {
		t.Fatalf("provision again: %v", err)
	}
}

func TestProvisionRejectsUnusableHostNames(t *testing.T) {
	for _, host := range []string{"fe80::1%en0", "hejmo", "", "a b.local", "-x.local", "x..local", ".local", "bank.example.com/x"} {
		s, _ := pki.Open(t.TempDir())
		if err := s.Provision(host, now); err == nil {
			t.Errorf("Provision(%q) succeeded", host)
		}
	}
	for _, host := range []string{"192.168.1.20", "fd00::1", "hejmo.local", "localhost", "my-box.fritz.box"} {
		s, _ := pki.Open(t.TempDir())
		if err := s.Provision(host, now); err != nil {
			t.Errorf("Provision(%q): %v", host, err)
		}
	}
}

func TestCAFingerprintIsTheSHA256OfTheCertificate(t *testing.T) {
	s := provisioned(t, "hejmo.local")
	sum := sha256.Sum256(caCert(t, s).Raw)
	want := strings.ToUpper(hex.EncodeToString(sum[:1])) + ":" + strings.ToUpper(hex.EncodeToString(sum[1:2]))
	if fp := s.CAFingerprint(); !strings.HasPrefix(fp, want) || strings.Count(fp, ":") != 31 {
		t.Errorf("fingerprint = %q, want prefix %q and 32 groups", fp, want)
	}
}

// Ticket 201 (Q143): leaving a public name for "only in the home network" needs
// a new CA, because the CA of the standby is limited to the public name and
// cannot vouch for the box's address at home.
func TestReprovisionStartsOverWithANewCAForTheNewAddress(t *testing.T) {
	dir := t.TempDir()
	s, err := pki.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Provision("familie-maier.dedyn.io", now); err != nil {
		t.Fatal(err)
	}
	oldFP := s.CAFingerprint()
	if err := s.Reprovision("192.168.178.40", now); err != nil {
		t.Fatal(err)
	}
	if s.CAFingerprint() == oldFP {
		t.Error("the CA is the old one")
	}
	leaf, err := s.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert(t, s))
	if _, err := leaf.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "", CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Errorf("the new certificate does not verify against the new CA: %v", err)
	}
	if !slices.ContainsFunc(leaf.Leaf.IPAddresses, func(ip net.IP) bool { return ip.Equal(net.ParseIP("192.168.178.40")) }) {
		t.Errorf("the certificate lacks the address: %v", leaf.Leaf.IPAddresses)
	}
	// It survives a restart.
	again, err := pki.Open(dir)
	if err != nil || !again.Ready() || again.CAFingerprint() != s.CAFingerprint() {
		t.Errorf("reopened: %v ready=%v", err, again.Ready())
	}
}

func TestReprovisionKeepsTheOldCAWhenTheNewHostIsRefused(t *testing.T) {
	s := provisioned(t, "familie-maier.dedyn.io")
	fp := s.CAFingerprint()
	if err := s.Reprovision("not a host", now); err == nil {
		t.Fatal("a bad host was accepted")
	}
	if !s.Ready() || s.CAFingerprint() != fp {
		t.Error("a refused change must leave the CA as it was")
	}
}

func TestReprovisionOnAFreshStoreIsProvision(t *testing.T) {
	s, _ := pki.Open(t.TempDir())
	if err := s.Reprovision("192.168.178.40", now); err != nil || !s.Ready() {
		t.Fatalf("Reprovision on a fresh store: %v", err)
	}
}
