package auth

import (
	"crypto/rand"
	"strings"
)

// invitationLength is the number of characters of an invitation link, without dashes.
const invitationLength = 20

// NewInvitationToken returns the secret of an invitation link such as
// "K7QX-M2PD-9WRT-4HNA-ZC6E": 20 characters from the recovery alphabet (about
// 99 bits), in groups of four so it is easy to read out and to retype.
func NewInvitationToken() string {
	var b strings.Builder
	for n := 0; n < invitationLength; {
		var one [1]byte
		_, _ = rand.Read(one[:])
		if one[0] >= 248 { // 31 * 8 = 248: reject the rest so every character is equally likely
			continue
		}
		if n > 0 && n%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(recoveryAlphabet[int(one[0])%len(recoveryAlphabet)])
		n++
	}
	return b.String()
}

// HashInvitationToken is how an invitation link is stored. Case, dashes and
// spaces do not matter, so a retyped link finds the same invitation.
func HashInvitationToken(token string) string { return HashToken(normalizeRecoveryCode(token)) }

// LooksLikeInvitationToken reports whether token has the length and the
// characters of an invitation link, so other input is not looked up at all.
func LooksLikeInvitationToken(token string) bool {
	n := normalizeRecoveryCode(token)
	if len(n) != invitationLength {
		return false
	}
	return strings.Trim(n, recoveryAlphabet) == ""
}
