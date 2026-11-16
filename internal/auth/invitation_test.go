package auth_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
)

var invitationShape = regexp.MustCompile(`^[A-HJKMNP-Z2-9]{4}(-[A-HJKMNP-Z2-9]{4}){4}$`)

func TestInvitationTokenIsFiveGroupsOfFourReadableCharacters(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		tok := auth.NewInvitationToken()
		if !invitationShape.MatchString(tok) {
			t.Fatalf("token %q has the wrong shape", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q repeated", tok)
		}
		seen[tok] = true
	}
}

func TestInvitationTokenHashIgnoresCaseDashesAndSpaces(t *testing.T) {
	want := auth.HashInvitationToken("K7QX-M2PD-9WRT-4HNA-ZC6E")
	for _, typed := range []string{"k7qx-m2pd-9wrt-4hna-zc6e", "K7QXM2PD9WRT4HNAZC6E", " K7QX M2PD 9WRT 4HNA ZC6E "} {
		if got := auth.HashInvitationToken(typed); got != want {
			t.Errorf("hash of %q differs", typed)
		}
	}
	if auth.HashInvitationToken("K7QX-M2PD-9WRT-4HNA-ZC6F") == want {
		t.Error("a different token has the same hash")
	}
	if strings.Contains(want, "K7QX") {
		t.Error("hash contains the token")
	}
}

func TestOnlyFullLengthInvitationTokensLookLikeOne(t *testing.T) {
	if !auth.LooksLikeInvitationToken("K7QX-M2PD-9WRT-4HNA-ZC6E") {
		t.Error("a full token is refused")
	}
	for _, cut := range []string{"", "K7QX-M2PD-9WRT", "K7QX-M2PD-9WRT-4HNA-ZC6", "K7QX-M2PD-9WRT-4HNA-ZC6E-AAAA", "K7QX-M2PD-9WRT-4HNA-ZC0E"} {
		if auth.LooksLikeInvitationToken(cut) {
			t.Errorf("%q passes as a token", cut)
		}
	}
}
