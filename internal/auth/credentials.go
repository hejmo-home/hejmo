// Package auth holds credentials (passwords, TOTP, PIN, tokens) and the
// permission model.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// MinPasswordLength is the shortest accepted password.
const MinPasswordLength = 10

// Argon2id parameters: OWASP minimum (19 MiB, 2 passes), sized for a Pi 4.
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32

	// Upper bounds accepted when verifying a stored hash.
	minKeyLen    = 16
	maxKeyLen    = 64
	maxMemoryKiB = 256 * 1024
	maxPasses    = 10
	maxThreads   = 8
)

// HashPassword returns an Argon2id PHC string for password.
func HashPassword(password string) (string, error) {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return "", fmt.Errorf("auth: password needs at least %d characters", MinPasswordLength)
	}
	return hashSecret(password), nil
}

// VerifyPassword checks password against an Argon2id PHC string.
func VerifyPassword(hash, password string) bool { return verifySecret(hash, password) }

func hashSecret(secret string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

func verifySecret(hash, secret string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var memory, passes uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &passes, &threads); err != nil || threads == 0 {
		return false
	}
	// The hash comes from the database; refuse parameters that would make a
	// check take minutes or gigabytes.
	if memory > maxMemoryKiB || passes == 0 || passes > maxPasses || threads > maxThreads {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) < minKeyLen || len(want) > maxKeyLen {
		return false
	}
	got := argon2.IDKey([]byte(secret), salt, passes, memory, threads, uint32(len(want))) // #nosec G115 -- len(want) is within minKeyLen..maxKeyLen above
	return subtle.ConstantTimeCompare(got, want) == 1
}

// --- TOTP (RFC 6238: SHA-1, 6 digits, 30 s) ---

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a fresh base32 secret (160 bit).
func NewTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return b32.EncodeToString(b)
}

// TOTPCode returns the 6-digit code for secret at time t.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		return "", errors.New("auth: invalid TOTP secret")
	}
	if t.Unix() < 0 {
		return "", errors.New("auth: time before 1970")
	}
	return hotp(key, uint64(t.Unix()/30)), nil // #nosec G115 -- t.Unix() >= 0 checked above
}

func hotp(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000)
}

// VerifyTOTP accepts the code for t and one 30-second step either side.
func VerifyTOTP(secret, code string, t time.Time) bool {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return false
	}
	ok := false
	for _, step := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		want, err := TOTPCode(secret, t.Add(step))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			ok = true
		}
	}
	return ok
}

// TOTPURI is the otpauth:// link authenticator apps read (often as QR code).
func TOTPURI(secret, account string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", "Hejmo")
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/Hejmo:" + url.PathEscape(account) + "?" + q.Encode()
}

// --- Display PIN ---

// HashPIN validates (4–8 digits) and hashes a display PIN.
func HashPIN(pin string) (string, error) {
	if len(pin) < 4 || len(pin) > 8 {
		return "", errors.New("auth: PIN needs 4 to 8 digits")
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return "", errors.New("auth: PIN may only contain digits")
		}
	}
	return hashSecret(pin), nil
}

// VerifyPIN checks a PIN against its hash.
func VerifyPIN(hash, pin string) bool { return verifySecret(hash, pin) }

// --- Opaque tokens (sessions, API tokens) ---

// NewToken returns a random bearer token.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "hejmo_" + base64.RawURLEncoding.EncodeToString(b)
}

// HashToken is how tokens are stored: SHA-256 is enough for 256-bit random values.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// --- Recovery codes ---

// recoveryAlphabet leaves out 0, O, 1, I and L so a printed code is easy to read.
const recoveryAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// NewRecoveryCodes returns n fresh recovery codes like "K7QD2-M9XPA" (about 50 bit each).
func NewRecoveryCodes(n int) []string {
	codes := make([]string, n)
	for i := range codes {
		var b strings.Builder
		for b.Len() < 11 {
			if b.Len() == 5 {
				b.WriteByte('-')
				continue
			}
			var one [1]byte
			_, _ = rand.Read(one[:])
			// 31 * 8 = 248: reject the rest so every letter is equally likely.
			if one[0] < 248 {
				b.WriteByte(recoveryAlphabet[int(one[0])%len(recoveryAlphabet)])
			}
		}
		codes[i] = b.String()
	}
	return codes
}

func normalizeRecoveryCode(code string) string {
	code = strings.ToUpper(code)
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, code)
}

// HashRecoveryCode hashes a recovery code with Argon2id, like a password.
func HashRecoveryCode(code string) string { return hashSecret(normalizeRecoveryCode(code)) }

// VerifyRecoveryCode checks a typed code against a stored hash.
func VerifyRecoveryCode(hash, code string) bool {
	return verifySecret(hash, normalizeRecoveryCode(code))
}

// LooksLikeRecoveryCode reports whether code has the length of a recovery
// code once spaces and dashes are left out, so other input is not hashed.
func LooksLikeRecoveryCode(code string) bool { return len(normalizeRecoveryCode(code)) == 10 }
