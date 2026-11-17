package pki_test

import (
	"crypto/x509"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/hejmo-home/hejmo/internal/pki"
)

// Ticket 216 (decision Q92): the names the box certificate covers and the
// optional name of the box in the router. The CA is name-constrained, so a new
// name needs a new CA.

func verifies(t *testing.T, s *pki.Store, name string) bool {
	t.Helper()
	cert, err := s.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(caCert(t, s))
	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: name, CurrentTime: now})
	return err == nil
}

func TestNamesListTheBoxNameThenTheAddressAndNeverLocalhost(t *testing.T) {
	s := provisioned(t, "192.168.178.40")
	if got, want := s.Names(), []string{"hejmo.local", "192.168.178.40"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	if got := s.LANAddress(); got != "192.168.178.40" {
		t.Errorf("LANAddress() = %q", got)
	}
	if s.RouterName() != "" {
		t.Errorf("RouterName() = %q on a box without one", s.RouterName())
	}
}

func TestNamesOfABoxReachedUnderAName(t *testing.T) {
	s := provisioned(t, "hejmo.local")
	if got, want := s.Names(), []string{"hejmo.local"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	if s.LANAddress() != "" {
		t.Errorf("LANAddress() = %q, want none", s.LANAddress())
	}
}

func TestProvisioningWithARouterNameCoversItAndPersistsIt(t *testing.T) {
	dir := t.TempDir()
	s, _ := pki.Open(dir)
	if err := s.ProvisionWithRouterName("192.168.178.40", "Hejmo.Fritz.Box", now); err != nil {
		t.Fatal(err)
	}
	if got, want := s.Names(), []string{"hejmo.local", "192.168.178.40", "hejmo.fritz.box"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	if s.RouterName() != "hejmo.fritz.box" {
		t.Errorf("RouterName() = %q", s.RouterName())
	}
	for _, name := range []string{"hejmo.local", "192.168.178.40", "hejmo.fritz.box"} {
		if !verifies(t, s, name) {
			t.Errorf("certificate does not verify for %s", name)
		}
	}
	if verifies(t, s, "other.fritz.box") {
		t.Error("certificate verifies for a name nobody asked for")
	}
	again, err := pki.Open(dir)
	if err != nil || again.RouterName() != "hejmo.fritz.box" || !slices.Equal(again.Names(), s.Names()) {
		t.Errorf("after a restart: err=%v router=%q names=%v", err, again.RouterName(), again.Names())
	}
}

func TestProvisioningRefusesABadRouterNameBeforeCreatingAnything(t *testing.T) {
	s, _ := pki.Open(t.TempDir())
	if err := s.ProvisionWithRouterName("192.168.178.40", "hejmo", now); !errors.Is(err, pki.ErrNameNoDot) {
		t.Fatalf("err = %v, want ErrNameNoDot", err)
	}
	if s.Ready() || s.CAPEM() != nil {
		t.Error("a CA was created for a refused name")
	}
}

func TestReissuingForARouterNameMakesANewCAThatVouchesForTheNewName(t *testing.T) {
	dir := t.TempDir()
	s, _ := pki.Open(dir)
	if err := s.Provision("192.168.178.40", now); err != nil {
		t.Fatal(err)
	}
	oldFingerprint, oldCA := s.CAFingerprint(), caCert(t, s)
	if verifies(t, s, "hejmo.fritz.box") {
		t.Fatal("verifies before the name is added")
	}

	if err := s.Reissue("hejmo.fritz.box", now); err != nil {
		t.Fatal(err)
	}
	if s.CAFingerprint() == oldFingerprint {
		t.Error("the CA stayed the same: devices that trust the old one would not see the new name")
	}
	if !slices.Contains(caCert(t, s).PermittedDNSDomains, "hejmo.fritz.box") {
		t.Errorf("the new CA does not permit the name: %v", caCert(t, s).PermittedDNSDomains)
	}
	if oldCA.SerialNumber.Cmp(caCert(t, s).SerialNumber) == 0 {
		t.Error("the new CA is the old certificate")
	}
	for _, name := range []string{"hejmo.local", "192.168.178.40", "hejmo.fritz.box"} {
		if !verifies(t, s, name) {
			t.Errorf("the new certificate does not verify for %s", name)
		}
	}
	if verifies(t, s, "bank.example.com") {
		t.Error("the new CA vouches for a foreign name")
	}
	if got, want := s.Names(), []string{"hejmo.local", "192.168.178.40", "hejmo.fritz.box"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}

	again, err := pki.Open(dir)
	if err != nil || again.CAFingerprint() != s.CAFingerprint() || again.RouterName() != "hejmo.fritz.box" {
		t.Errorf("after a restart: err=%v router=%q", err, again.RouterName())
	}
}

func TestReissuingReplacesAnEarlierRouterName(t *testing.T) {
	s, _ := pki.Open(t.TempDir())
	if err := s.ProvisionWithRouterName("192.168.178.40", "hejmo.fritz.box", now); err != nil {
		t.Fatal(err)
	}
	if err := s.Reissue("hejmo.lan", now); err != nil {
		t.Fatal(err)
	}
	if got, want := s.Names(), []string{"hejmo.local", "192.168.178.40", "hejmo.lan"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	if verifies(t, s, "hejmo.fritz.box") {
		t.Error("the replaced name is still covered")
	}
}

func TestReissueRefusesWhatItCoversAlreadyAndKeepsTheCA(t *testing.T) {
	s, _ := pki.Open(t.TempDir())
	if err := s.ProvisionWithRouterName("192.168.178.40", "hejmo.fritz.box", now); err != nil {
		t.Fatal(err)
	}
	before := s.CAFingerprint()
	for _, c := range []struct {
		in   string
		want error
	}{
		{"", pki.ErrNameEmpty}, {"   ", pki.ErrNameEmpty},
		{"hejmo", pki.ErrNameNoDot},
		{"hejmo box.fritz.box", pki.ErrNameChars}, {"höjmo.fritz.box", pki.ErrNameChars}, {"*.fritz.box", pki.ErrNameChars}, {"a..b", pki.ErrNameChars},
		{"-a.fritz.box", pki.ErrNameChars}, {"https://hejmo.fritz.box", pki.ErrNameChars},
		{"192.168.178.40", pki.ErrAddressCovered}, {"192.168.178.99", pki.ErrNameIsAddress}, {"fd00::1", pki.ErrNameIsAddress},
		{"hejmo.local", pki.ErrNameCovered}, {"HEJMO.LOCAL", pki.ErrNameCovered}, {"hejmo.fritz.box", pki.ErrNameCovered}, {"hejmo.fritz.box.", pki.ErrNameCovered},
	} {
		if err := s.Reissue(c.in, now); !errors.Is(err, c.want) {
			t.Errorf("Reissue(%q) = %v, want %v", c.in, err, c.want)
		}
	}
	if s.CAFingerprint() != before {
		t.Error("a refused name replaced the CA")
	}
}

func TestCleanRouterNameNormalisesAndChecks(t *testing.T) {
	covered := []string{"hejmo.local", "192.168.178.40"}
	for in, want := range map[string]string{
		"hejmo.fritz.box": "hejmo.fritz.box", "  Hejmo.Fritz.Box  ": "hejmo.fritz.box", "hejmo.fritz.box.": "hejmo.fritz.box", "my-box.home.arpa": "my-box.home.arpa",
	} {
		if got, err := pki.CleanRouterName(in, covered); err != nil || got != want {
			t.Errorf("CleanRouterName(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	long := make([]byte, 64)
	for i := range long {
		long[i] = 'a'
	}
	for _, in := range []string{string(long) + ".box", "a." + string(long)} {
		if _, err := pki.CleanRouterName(in, covered); !errors.Is(err, pki.ErrNameChars) {
			t.Errorf("a label of 64 characters: %v", err)
		}
	}
}

func TestReissueFailureKeepsTheOldCertificateAuthority(t *testing.T) {
	dir := t.TempDir()
	s, _ := pki.Open(dir)
	if err := s.Provision("192.168.178.40", now); err != nil {
		t.Fatal(err)
	}
	before := s.CAFingerprint()
	if err := os.Chmod(dir, 0o500); err != nil { // makes writing the new files fail
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := s.Reissue("hejmo.fritz.box", now); err == nil {
		t.Fatal("Reissue succeeded although the certificate could not be written")
	}
	if s.CAFingerprint() != before || s.RouterName() != "" {
		t.Error("a failed Reissue changed what the box serves")
	}
}

func TestPlannedNamesAreWhatProvisioningWillCover(t *testing.T) {
	got, err := pki.PlannedNames("192.168.178.40", "hejmo.fritz.box")
	if err != nil || !slices.Equal(got, []string{"hejmo.local", "192.168.178.40", "hejmo.fritz.box"}) {
		t.Errorf("PlannedNames = %v, %v", got, err)
	}
	got, _ = pki.PlannedNames("192.168.178.40", "")
	if !slices.Equal(got, []string{"hejmo.local", "192.168.178.40"}) {
		t.Errorf("PlannedNames without a router name = %v", got)
	}
	if _, err := pki.PlannedNames("hejmo", ""); !errors.Is(err, pki.ErrInvalidHost) {
		t.Errorf("PlannedNames of an unusable host: %v", err)
	}
}
