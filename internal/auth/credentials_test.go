package auth_test

import (
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/hejmo-home/hejmo/internal/auth"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash = %q, want argon2id PHC string", hash)
	}
	if strings.Contains(hash, "correct horse") {
		t.Fatal("hash contains the password")
	}
	if !auth.VerifyPassword(hash, "correct horse battery") {
		t.Error("right password rejected")
	}
	if auth.VerifyPassword(hash, "wrong horse battery") {
		t.Error("wrong password accepted")
	}
	again, _ := auth.HashPassword("correct horse battery")
	if again == hash {
		t.Error("two hashes of the same password are equal (missing salt)")
	}
}

func TestPasswordPolicy(t *testing.T) {
	if _, err := auth.HashPassword("short"); err == nil {
		t.Error("password under 10 characters accepted")
	}
}

func TestVerifyPasswordRejectsGarbage(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2id$v=19$m=x"} {
		if auth.VerifyPassword(h, "anything long") {
			t.Errorf("garbage hash %q verified", h)
		}
	}
}

// RFC 6238, appendix B (SHA-1), truncated to 6 digits.
func TestTOTPMatchesRFC6238(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	cases := map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	}
	for unix, want := range cases {
		got, err := auth.TOTPCode(secret, time.Unix(unix, 0))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("TOTPCode(t=%d) = %s, want %s", unix, got, want)
		}
	}
}

func TestTOTPVerifyAllowsOneStepClockDrift(t *testing.T) {
	secret := auth.NewTOTPSecret()
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	code, _ := auth.TOTPCode(secret, at)

	for _, drift := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		if !auth.VerifyTOTP(secret, code, at.Add(drift)) {
			t.Errorf("code rejected at drift %v", drift)
		}
	}
	for _, drift := range []time.Duration{-90 * time.Second, 90 * time.Second} {
		if auth.VerifyTOTP(secret, code, at.Add(drift)) {
			t.Errorf("code accepted at drift %v", drift)
		}
	}
	if auth.VerifyTOTP(secret, "", at) || auth.VerifyTOTP(secret, "12345", at) {
		t.Error("malformed code accepted")
	}
}

func TestTOTPURIForAuthenticatorApps(t *testing.T) {
	uri := auth.TOTPURI("JBSWY3DPEHPK3PXP", "mama")
	want := "otpauth://totp/Hejmo:mama?algorithm=SHA1&digits=6&issuer=Hejmo&period=30&secret=JBSWY3DPEHPK3PXP"
	if uri != want {
		t.Errorf("uri = %s\nwant  %s", uri, want)
	}
}

func TestPIN(t *testing.T) {
	for _, bad := range []string{"", "123", "12a4", "123456789"} {
		if _, err := auth.HashPIN(bad); err == nil {
			t.Errorf("PIN %q accepted", bad)
		}
	}
	hash, err := auth.HashPIN("2468")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.VerifyPIN(hash, "2468") || auth.VerifyPIN(hash, "1357") {
		t.Error("PIN verification wrong")
	}
}

func TestOpaqueTokens(t *testing.T) {
	a, b := auth.NewToken(), auth.NewToken()
	if a == b || !strings.HasPrefix(a, "hejmo_") || len(a) < 40 {
		t.Fatalf("tokens %q, %q", a, b)
	}
	if auth.HashToken(a) == a || auth.HashToken(a) != auth.HashToken(a) || auth.HashToken(a) == auth.HashToken(b) {
		t.Error("HashToken must be deterministic, distinct and not the token")
	}
}

// A stored hash is read from the database; one that asks for absurd work or an
// absurd key length must be refused, not computed (gosec G115, ADR 0018).
func TestVerifyPasswordRefusesAbsurdParameters(t *testing.T) {
	salt := "c2FsdHNhbHRzYWx0c2E"
	key := strings.Repeat("A", 43)
	for name, h := range map[string]string{
		"memory": "$argon2id$v=19$m=4294967295,t=2,p=1$" + salt + "$" + key,
		"passes": "$argon2id$v=19$m=19456,t=4294967295,p=1$" + salt + "$" + key,
	} {
		start := time.Now()
		if auth.VerifyPassword(h, "anything long") {
			t.Errorf("%s: verified", name)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: took %v, parameters were not refused", name, d)
		}
	}
}

func TestTOTPCodeRefusesTimesBeforeTheEpoch(t *testing.T) {
	secret := auth.NewTOTPSecret()
	if _, err := auth.TOTPCode(secret, time.Unix(-1, 0)); err == nil {
		t.Error("a time before 1970 must not wrap into a huge counter")
	}
	if auth.VerifyTOTP(secret, "123456", time.Unix(-1, 0)) {
		t.Error("verified at a time before 1970")
	}
}

// Bounds on key length and threads: each hash below is genuine for the secret,
// so without the bound it would verify. Hashes at the bound must still verify.
func TestVerifyPasswordKeyLengthAndThreadBounds(t *testing.T) {
	const secret = "anything long"
	salt := []byte("saltsaltsaltsalt")
	phc := func(keyLen uint32, threads uint8) string {
		key := argon2.IDKey([]byte(secret), salt, 1, 8, threads, keyLen)
		b64 := base64.RawStdEncoding
		return fmt.Sprintf("$argon2id$v=%d$m=8,t=1,p=%d$%s$%s",
			argon2.Version, threads, b64.EncodeToString(salt), b64.EncodeToString(key))
	}
	for name, c := range map[string]struct {
		keyLen  uint32
		threads uint8
		ok      bool
	}{
		"shortest key":      {16, 1, true},
		"longest key":       {64, 1, true},
		"most threads":      {32, 8, true},
		"key below minimum": {8, 1, false},
		"key above maximum": {65, 1, false},
		"threads above max": {32, 9, false},
	} {
		if got := auth.VerifyPassword(phc(c.keyLen, c.threads), secret); got != c.ok {
			t.Errorf("%s: verified = %v, want %v", name, got, c.ok)
		}
	}
}
