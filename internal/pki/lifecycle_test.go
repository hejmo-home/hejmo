package pki_test

import (
	"bytes"
	"crypto/x509"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/pki"
)

func TestFailedProvisioningDoesNotPublishTheCA(t *testing.T) {
	dir := t.TempDir()
	s, _ := pki.Open(dir)
	if err := os.Mkdir(filepath.Join(dir, "box.pem"), 0o700); err != nil { // makes writing the certificate fail
		t.Fatal(err)
	}
	if err := s.Provision("hejmo.local", now); err == nil {
		t.Fatal("provisioning succeeded")
	}
	if s.CAPEM() != nil || s.CAFingerprint() != "" {
		t.Error("a CA is served although the box is not ready")
	}
}

func TestProvisioningLogsTheCAFingerprint(t *testing.T) {
	var logs bytes.Buffer
	s, _ := pki.Open(t.TempDir(), pki.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err := s.Provision("hejmo.local", now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), s.CAFingerprint()) {
		t.Errorf("fingerprint not logged: %q", logs.String())
	}
}

func TestRenewedCertificateNeverOutlivesTheCA(t *testing.T) {
	clock := now
	var logs bytes.Buffer
	s, _ := pki.Open(t.TempDir(), pki.WithClock(func() time.Time { return clock }), pki.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err := s.Provision("hejmo.local", now); err != nil {
		t.Fatal(err)
	}
	ca := caCert(t, s)
	for i := 0; i < 4; i++ {
		clock = clock.Add(771 * 24 * time.Hour)
		c, err := s.GetCertificate(nil)
		if err != nil {
			t.Fatal(err)
		}
		leaf, _ := x509.ParseCertificate(c.Certificate[0])
		if leaf.NotAfter.After(ca.NotAfter) {
			t.Fatalf("renewal %d: leaf expires %v, after the CA at %v", i+1, leaf.NotAfter, ca.NotAfter)
		}
	}
	if !strings.Contains(logs.String(), "CA expires") {
		t.Errorf("no warning about the expiring CA: %q", logs.String())
	}
}

func TestFailingRenewalIsRetriedOnlyOncePerHour(t *testing.T) {
	dir := t.TempDir()
	clock := now
	var logs bytes.Buffer
	s, _ := pki.Open(dir, pki.WithClock(func() time.Time { return clock }), pki.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err := s.Provision("hejmo.local", now); err != nil {
		t.Fatal(err)
	}
	// A directory in place of box.pem makes every write fail, also as root.
	if err := os.Remove(filepath.Join(dir, "box.pem")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "box.pem"), 0o700); err != nil {
		t.Fatal(err)
	}
	clock = now.Add(771 * 24 * time.Hour)
	for i := 0; i < 5; i++ {
		s.GetCertificate(nil)
	}
	if n := strings.Count(logs.String(), "renew box certificate"); n != 1 {
		t.Fatalf("%d renewal attempts within the hour, want 1", n)
	}
	clock = clock.Add(61 * time.Minute)
	s.GetCertificate(nil)
	if n := strings.Count(logs.String(), "renew box certificate"); n != 2 {
		t.Fatalf("%d renewal attempts after an hour, want 2", n)
	}
}

func TestCertificateCappedAtTheCAIsNotReissuedEveryHour(t *testing.T) {
	clock := now
	var logs bytes.Buffer
	s, _ := pki.Open(t.TempDir(), pki.WithClock(func() time.Time { return clock }), pki.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err := s.Provision("hejmo.local", now); err != nil {
		t.Fatal(err)
	}
	var capped *x509.Certificate
	for i := 0; i < 4; i++ { // the last renewal is capped at the CA's expiry
		clock = clock.Add(771 * 24 * time.Hour)
		c, err := s.GetCertificate(nil)
		if err != nil {
			t.Fatal(err)
		}
		capped, _ = x509.ParseCertificate(c.Certificate[0])
	}
	if !capped.NotAfter.Equal(caCert(t, s).NotAfter) {
		t.Fatalf("setup: leaf expires %v, want the CA's expiry", capped.NotAfter)
	}
	clock = capped.NotAfter.Add(-10 * 24 * time.Hour) // inside the renewal window
	for i := 0; i < 5; i++ {
		c, err := s.GetCertificate(nil)
		if err != nil {
			t.Fatal(err)
		}
		leaf, _ := x509.ParseCertificate(c.Certificate[0])
		if leaf.SerialNumber.Cmp(capped.SerialNumber) != 0 {
			t.Fatalf("certificate re-issued at step %d although it cannot last longer", i)
		}
		clock = clock.Add(61 * time.Minute)
	}
	if n := strings.Count(logs.String(), "CA expires"); n != 1 {
		t.Errorf("CA warning logged %d times, want once", n)
	}
}
