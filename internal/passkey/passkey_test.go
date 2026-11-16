package passkey_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/hejmo-home/hejmo/internal/passkey"
	"github.com/hejmo-home/hejmo/internal/passkey/passkeytest"
)

const origin = "https://hejmo.local"

func relying(t *testing.T) *webauthn.WebAuthn {
	t.Helper()
	wa, err := passkey.New("hejmo.local", origin)
	if err != nil {
		t.Fatal(err)
	}
	return wa
}

func creation(t *testing.T, wa *webauthn.WebAuthn, u passkey.User) ([]byte, webauthn.SessionData) {
	t.Helper()
	opts, session, err := passkey.BeginRegistration(wa, u)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(opts)
	return raw, *session
}

func TestRegisterThenSignInWithoutUsername(t *testing.T) {
	wa, dev := relying(t), passkeytest.New()
	mama := passkey.User{ID: "p-1", Name: "mama", DisplayName: "Mama"}
	opts, session := creation(t, wa, mama)
	answer, err := dev.Create(opts, origin)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := passkey.FinishRegistration(wa, mama, session, answer)
	if err != nil {
		t.Fatalf("a passkey made by an authenticator that verifies the user is accepted: %v", err)
	}
	mama.Credentials = append(mama.Credentials, *cred)

	assertion, session2, err := passkey.BeginSignIn(wa)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(assertion)
	signed, err := dev.Get(raw, origin)
	if err != nil {
		t.Fatal(err)
	}
	who, _, err := passkey.FinishSignIn(wa, func(id, handle []byte) (passkey.User, error) {
		if string(handle) != mama.ID {
			return passkey.User{}, passkey.ErrUnknownCredential
		}
		return mama, nil
	}, *session2, signed)
	if err != nil || who.ID != "p-1" {
		t.Fatalf("sign-in = %+v, %v", who, err)
	}
}

func TestRegistrationNeedsUserVerification(t *testing.T) {
	wa, dev := relying(t), passkeytest.New()
	dev.SkipUserVerification = true
	opts, session := creation(t, wa, passkey.User{ID: "p-1", Name: "mama", DisplayName: "Mama"})
	answer, _ := dev.Create(opts, origin)
	if _, err := passkey.FinishRegistration(wa, passkey.User{ID: "p-1", Name: "mama", DisplayName: "Mama"}, session, answer); err == nil {
		t.Fatal("a passkey that did not verify the user counts as one factor only and must be refused")
	}
}

func TestAnAnswerFromAnotherOriginIsRefused(t *testing.T) {
	wa, dev := relying(t), passkeytest.New()
	mama := passkey.User{ID: "p-1", Name: "mama", DisplayName: "Mama"}
	opts, session := creation(t, wa, mama)
	answer, _ := dev.Create(opts, "https://evil.example")
	if _, err := passkey.FinishRegistration(wa, mama, session, answer); err == nil {
		t.Fatal("origin of another site accepted")
	}
}

func TestSignInRefusesAnUnknownPasskey(t *testing.T) {
	wa, dev := relying(t), passkeytest.New()
	mama := passkey.User{ID: "p-1", Name: "mama", DisplayName: "Mama"}
	opts, session := creation(t, wa, mama)
	answer, _ := dev.Create(opts, origin)
	if _, err := passkey.FinishRegistration(wa, mama, session, answer); err != nil {
		t.Fatal(err)
	}
	assertion, s2, _ := passkey.BeginSignIn(wa)
	raw, _ := json.Marshal(assertion)
	signed, _ := dev.Get(raw, origin)
	_, _, err := passkey.FinishSignIn(wa, func(id, handle []byte) (passkey.User, error) {
		return passkey.User{}, passkey.ErrUnknownCredential // removed on the box
	}, *s2, signed)
	if !errors.Is(err, passkey.ErrUnknownCredential) {
		t.Fatalf("err = %v, want ErrUnknownCredential", err)
	}
}

func TestBoxNamesAreNeverIPAddresses(t *testing.T) {
	for name, want := range map[string]bool{"hejmo.local": true, "familie.dedyn.io": true, "192.168.178.40": false, "::1": false, "[::1]": false, "": false, "a/b": false} {
		if got := passkey.UsableName(name); got != want {
			t.Errorf("UsableName(%q) = %v, want %v", name, got, want)
		}
	}
	if _, err := passkey.New("192.168.178.40", "https://192.168.178.40"); err == nil {
		t.Error("a relying party was made for an IP address")
	}
}

func TestCeremoniesWorkOnceForTheirKindAndPerson(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	c := passkey.NewCeremonies(func() time.Time { return now })
	id := c.Put(passkey.Register, "p-1", webauthn.SessionData{Challenge: "x"})
	if _, ok := c.Take(id, passkey.SignIn, "p-1"); ok {
		t.Error("a registration finished a sign-in")
	}
	id = c.Put(passkey.Register, "p-1", webauthn.SessionData{Challenge: "x"})
	if _, ok := c.Take(id, passkey.Register, "p-2"); ok {
		t.Error("another person finished the ceremony")
	}
	id = c.Put(passkey.Register, "p-1", webauthn.SessionData{Challenge: "x"})
	if s, ok := c.Take(id, passkey.Register, "p-1"); !ok || s.Challenge != "x" {
		t.Fatal("the ceremony of the person did not work")
	}
	if _, ok := c.Take(id, passkey.Register, "p-1"); ok {
		t.Error("a ceremony worked twice")
	}
	id = c.Put(passkey.Register, "p-1", webauthn.SessionData{})
	now = now.Add(10 * time.Minute)
	if _, ok := c.Take(id, passkey.Register, "p-1"); ok {
		t.Error("a ceremony worked after it ran out")
	}
}

// Sign-in ceremonies need no sign-in: flooding them must not push out the
// registration of a parent who is in the middle of creating a passkey.
func TestFloodOfSignInCeremoniesKeepsRegistrations(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	c := passkey.NewCeremonies(func() time.Time { return now })
	reg := c.Put(passkey.Register, "p-1", webauthn.SessionData{Challenge: "x"})
	for i := 0; i < 5000; i++ {
		now = now.Add(time.Millisecond)
		c.Put(passkey.SignIn, "", webauthn.SessionData{})
	}
	if _, ok := c.Take(reg, passkey.Register, "p-1"); !ok {
		t.Fatal("a flood of sign-in ceremonies evicted a registration")
	}
}
