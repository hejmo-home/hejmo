// Package passkeytest is a virtual authenticator for tests: a software
// stand-in for the device or password manager that holds passkeys. It speaks
// what a browser passes between the page and the box (the JSON of the
// options and of the created credential or assertion), so tests need no real
// credentials and no browser. Never use it outside tests.
package passkeytest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/fxamacker/cbor/v2"
)

var b64 = base64.RawURLEncoding

// Authenticator holds passkeys. It always verifies the user (a fingerprint
// that always matches), like a platform authenticator.
type Authenticator struct {
	creds []*credential
	// SkipUserVerification makes it answer without verifying the user, as a
	// security key without a PIN does.
	SkipUserVerification bool
}

type credential struct {
	id     []byte
	key    *ecdsa.PrivateKey
	rpID   string
	handle []byte
	count  uint32
}

// New returns an authenticator without passkeys.
func New() *Authenticator { return &Authenticator{} }

// CredentialIDs returns the IDs of the passkeys it holds, as the page sends
// them (base64url).
func (a *Authenticator) CredentialIDs() []string {
	var out []string
	for _, c := range a.creds {
		out = append(out, b64.EncodeToString(c.id))
	}
	return out
}

type creationOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Exclude []struct {
			ID string `json:"id"`
		} `json:"excludeCredentials"`
		Selection struct {
			ResidentKey string `json:"residentKey"`
			UV          string `json:"userVerification"`
		} `json:"authenticatorSelection"`
	} `json:"publicKey"`
}

type requestOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		RPID      string `json:"rpId"`
		Allow     []struct {
			ID string `json:"id"`
		} `json:"allowCredentials"`
	} `json:"publicKey"`
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// Create answers the options of a registration (the JSON the box sent) as the
// browser would after the user confirmed on origin, and returns the JSON of the
// created credential.
func (a *Authenticator) Create(options []byte, origin string) ([]byte, error) {
	var o creationOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, err
	}
	pk := o.PublicKey
	for _, ex := range pk.Exclude {
		for _, c := range a.creds {
			if ex.ID == b64.EncodeToString(c.id) {
				return nil, fmt.Errorf("passkeytest: the authenticator already has a passkey of this account")
			}
		}
	}
	handle, err := b64.DecodeString(pk.User.ID)
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	c := &credential{id: make([]byte, 32), key: key, rpID: pk.RP.ID, handle: handle}
	_, _ = rand.Read(c.id)
	a.creds = append(a.creds, c)

	cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: key.PublicKey.X.FillBytes(make([]byte, 32)), -3: key.PublicKey.Y.FillBytes(make([]byte, 32))})
	if err != nil {
		return nil, err
	}
	var data bytes.Buffer
	rp := sha256.Sum256([]byte(pk.RP.ID))
	data.Write(rp[:])
	flags := byte(0x01 | 0x40 | 0x08 | 0x10) // user present, attested data, backup eligible, backed up (a synced passkey)
	if !a.SkipUserVerification {
		flags |= 0x04
	}
	data.WriteByte(flags)
	_ = binary.Write(&data, binary.BigEndian, uint32(0))
	data.Write(make([]byte, 16)) // AAGUID: none
	_ = binary.Write(&data, binary.BigEndian, uint16(len(c.id)))
	data.Write(c.id)
	data.Write(cose)
	att, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": data.Bytes()})
	if err != nil {
		return nil, err
	}
	cd, _ := json.Marshal(clientData{Type: "webauthn.create", Challenge: pk.Challenge, Origin: origin})
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(c.id), "rawId": b64.EncodeToString(c.id), "type": "public-key",
		"response":               map[string]any{"attestationObject": b64.EncodeToString(att), "clientDataJSON": b64.EncodeToString(cd), "transports": []string{"internal"}},
		"clientExtensionResults": map[string]any{},
	})
}

// Get answers the options of a sign-in or a confirmation as the browser would
// after the user confirmed on origin, and returns the JSON of the assertion.
// It fails when it has no passkey for the box name or none the options allow.
func (a *Authenticator) Get(options []byte, origin string) ([]byte, error) {
	var o requestOptions
	if err := json.Unmarshal(options, &o); err != nil {
		return nil, err
	}
	pk := o.PublicKey
	var c *credential
	for _, have := range a.creds {
		if have.rpID != pk.RPID {
			continue
		}
		allowed := len(pk.Allow) == 0
		for _, al := range pk.Allow {
			allowed = allowed || al.ID == b64.EncodeToString(have.id)
		}
		if allowed {
			c = have
		}
	}
	if c == nil {
		return nil, fmt.Errorf("passkeytest: no passkey for %q that the options allow", pk.RPID)
	}
	c.count++
	var data bytes.Buffer
	rp := sha256.Sum256([]byte(pk.RPID))
	data.Write(rp[:])
	flags := byte(0x01 | 0x08 | 0x10)
	if !a.SkipUserVerification {
		flags |= 0x04
	}
	data.WriteByte(flags)
	_ = binary.Write(&data, binary.BigEndian, c.count)
	cd, _ := json.Marshal(clientData{Type: "webauthn.get", Challenge: pk.Challenge, Origin: origin})
	hash := sha256.Sum256(cd)
	sum := sha256.Sum256(append(append([]byte{}, data.Bytes()...), hash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, c.key, sum[:])
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"id": b64.EncodeToString(c.id), "rawId": b64.EncodeToString(c.id), "type": "public-key",
		"response": map[string]any{
			"authenticatorData": b64.EncodeToString(data.Bytes()), "clientDataJSON": b64.EncodeToString(cd),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(c.handle),
		},
		"clientExtensionResults": map[string]any{},
	})
}

// Forget deletes every passkey it holds, as removing them on the device does.
func (a *Authenticator) Forget() { a.creds = nil }
