// Package passkey is the box's side of passkeys (ADR 0018): the WebAuthn
// ceremonies for creating a passkey, signing in with one and confirming an
// action with one. The protocol itself is the library go-webauthn/webauthn;
// this package fixes what Hejmo asks of it: a discoverable credential that
// needs the user's verification (so one passkey counts as password and
// one-time code), no attestation, and the relying-party ID is the box's name,
// never an IP address.
package passkey

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// LocalName is the DNS name of a box that uses its own certificate authority
// (ADR 0014, GLOSSARY: box name); every such box's certificate carries it.
const LocalName = "hejmo.local"

// Timeout is how long the browser keeps its dialog open; the box keeps a
// ceremony a little longer than that.
const Timeout = 2 * time.Minute

const ceremonyTTL = Timeout + 30*time.Second

// ErrUnknownCredential means no passkey of the box has the credential ID (it
// was removed, or never existed here).
var ErrUnknownCredential = errors.New("passkey: unknown credential")

// ErrWrongName means the passkey was created for another box name than the
// one the box has now.
var ErrWrongName = errors.New("passkey: created for another box name")

// UsableName reports whether name can be a relying-party ID: a DNS name,
// never an IP address.
func UsableName(name string) bool {
	return name != "" && net.ParseIP(strings.Trim(name, "[]")) == nil && !strings.ContainsAny(name, "/: ")
}

// User is the person a ceremony is about.
type User struct {
	ID          string // the person's ID; it is the user handle of the passkeys
	Name        string // the account's username
	DisplayName string
	Credentials []webauthn.Credential
}

func (u User) WebAuthnID() []byte                         { return []byte(u.ID) }
func (u User) WebAuthnName() string                       { return u.Name }
func (u User) WebAuthnDisplayName() string                { return u.DisplayName }
func (u User) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

// New returns the relying party for the box name and the origin the browser
// is on ("https://name" or "https://name:port").
func New(name, origin string) (*webauthn.WebAuthn, error) {
	if !UsableName(name) {
		return nil, fmt.Errorf("passkey: %q is not a box name", name)
	}
	return webauthn.New(&webauthn.Config{
		RPID:          name,
		RPDisplayName: "Hejmo",
		RPOrigins:     []string{origin},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Timeout: Timeout, TimeoutUVD: Timeout},
			Registration: webauthn.TimeoutConfig{Timeout: Timeout, TimeoutUVD: Timeout},
		},
	})
}

// BeginRegistration starts creating a passkey for the user. The passkeys the
// user has are excluded, so one device does not register twice.
func BeginRegistration(wa *webauthn.WebAuthn, u User) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	return wa.BeginRegistration(u,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithExclusions(webauthn.Credentials(u.Credentials).CredentialDescriptors()),
	)
}

// FinishRegistration checks the browser's answer (the JSON of the created
// credential) and returns the new credential.
func FinishRegistration(wa *webauthn.WebAuthn, u User, session webauthn.SessionData, body []byte) (*webauthn.Credential, error) {
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		return nil, err
	}
	return wa.CreateCredential(u, session, parsed)
}

// BeginSignIn starts a sign-in without a username: the browser offers the
// passkeys it has for the box name.
func BeginSignIn(wa *webauthn.WebAuthn) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
}

// Lookup finds the user a passkey belongs to, from the credential ID and the
// user handle the authenticator sent. It returns ErrUnknownCredential (or
// ErrWrongName) when there is none.
type Lookup func(credentialID, userHandle []byte) (User, error)

// FinishSignIn checks the browser's answer and returns whose passkey it was
// and the credential with its updated counters.
func FinishSignIn(wa *webauthn.WebAuthn, lookup Lookup, session webauthn.SessionData, body []byte) (User, *webauthn.Credential, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return User{}, nil, err
	}
	var found User
	_, cred, err := wa.ValidatePasskeyLogin(func(rawID, handle []byte) (webauthn.User, error) {
		u, err := lookup(rawID, handle)
		if err != nil {
			return nil, err
		}
		found = u
		return u, nil
	}, session, parsed)
	if err != nil {
		return User{}, nil, err
	}
	if cred.Authenticator.CloneWarning {
		return User{}, nil, errors.New("passkey: the counter went back, the passkey may be cloned")
	}
	return found, cred, nil
}

// BeginConfirm starts a confirmation by the user's own passkeys.
func BeginConfirm(wa *webauthn.WebAuthn, u User) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return wa.BeginLogin(u, webauthn.WithUserVerification(protocol.VerificationRequired))
}

// FinishConfirm checks the browser's answer to BeginConfirm.
func FinishConfirm(wa *webauthn.WebAuthn, u User, session webauthn.SessionData, body []byte) (*webauthn.Credential, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return nil, err
	}
	cred, err := wa.ValidateLogin(u, session, parsed)
	if err != nil {
		return nil, err
	}
	if cred.Authenticator.CloneWarning {
		return nil, errors.New("passkey: the counter went back, the passkey may be cloned")
	}
	return cred, nil
}

// Kind tells which ceremony a stored session belongs to; a session of one
// kind never finishes another.
type Kind string

// The three ceremonies.
const (
	Register Kind = "register"
	SignIn   Kind = "signin"
	Confirm  Kind = "confirm"
)

// Ceremonies keeps the sessions of ceremonies that were begun and not yet
// finished. They live in memory only: after a restart a ceremony is begun
// again. A session works once and for a short time.
type Ceremonies struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]ceremony
}

type ceremony struct {
	kind    Kind
	person  string
	session webauthn.SessionData
	until   time.Time
}

// maxCeremonies bounds the memory: sign-in ceremonies need no sign-in.
const maxCeremonies = 500

// NewCeremonies returns an empty store that reads the time from now.
func NewCeremonies(now func() time.Time) *Ceremonies {
	return &Ceremonies{now: now, m: map[string]ceremony{}}
}

// Put stores a session and returns the ID the browser sends back. person is
// who the ceremony belongs to ("" for a sign-in).
func (c *Ceremonies) Put(kind Kind, person string, session webauthn.SessionData) string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	id := hex.EncodeToString(raw)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, v := range c.m {
		if now.After(v.until) {
			delete(c.m, k)
		}
	}
	for len(c.m) >= maxCeremonies { // drop the oldest
		oldest, at := "", time.Time{}
		for k, v := range c.m {
			if oldest == "" || v.until.Before(at) {
				oldest, at = k, v.until
			}
		}
		delete(c.m, oldest)
	}
	c.m[id] = ceremony{kind: kind, person: person, session: session, until: now.Add(ceremonyTTL)}
	return id
}

// Take returns the session and forgets it. It fails when the ID is unknown,
// was used before, ran out, or belongs to another kind of ceremony or another
// person.
func (c *Ceremonies) Take(id string, kind Kind, person string) (webauthn.SessionData, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[id]
	delete(c.m, id)
	if !ok || c.now().After(v.until) || v.kind != kind || v.person != person {
		return webauthn.SessionData{}, false
	}
	return v.session, true
}
