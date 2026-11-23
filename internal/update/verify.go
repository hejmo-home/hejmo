package update

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"sync"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// The identity a release is signed with: the release workflow of this
// repository, on the tag of the version (ADR 0018). The host script checks the
// very same two values with cosign.
const (
	SignerPrefix = "https://github.com/hejmo-home/hejmo/.github/workflows/release.yml@refs/tags/v"
	SignerIssuer = "https://token.actions.githubusercontent.com"
)

// Verifier checks that a cosign bundle signs the manifest as the release
// workflow of that version, and names who signed.
type Verifier interface {
	Verify(manifest, bundleJSON []byte, version string) (signer string, err error)
}

// SigstoreVerifier verifies a keyless cosign bundle completely offline with
// sigstore-go: the certificate chain to Fulcio's root, the certificate's
// transparency log promise, the entry in the Rekor log (its inclusion proof
// and signed checkpoint) and the signature, all against a trusted root that is
// part of the box. Nothing is fetched.
type SigstoreVerifier struct {
	v *verify.Verifier
}

// NewSigstoreVerifier builds a verifier from trusted material (the Fulcio
// authorities, the log keys and the timestamp authorities). Every certificate
// must carry a signed certificate timestamp, as Fulcio's do; only a test's
// virtual Sigstore, which issues none, is checked without (requireSCT false).
func NewSigstoreVerifier(trust root.TrustedMaterial, requireSCT bool) (*SigstoreVerifier, error) {
	opts := []verify.VerifierOption{verify.WithTransparencyLog(1), verify.WithObserverTimestamps(1)}
	if requireSCT {
		opts = append(opts, verify.WithSignedCertificateTimestamps(1))
	}
	v, err := verify.NewVerifier(trust, opts...)
	if err != nil {
		return nil, err
	}
	return &SigstoreVerifier{v: v}, nil
}

//go:embed trusted_root.json
var trustedRootJSON []byte

// TrustedRootJSON is the Sigstore trusted root the box ships with. The install
// script keeps the same bytes for the host script's own check.
func TrustedRootJSON() []byte { return bytes.Clone(trustedRootJSON) }

var (
	defaultOnce sync.Once
	defaultV    *SigstoreVerifier
	defaultErr  error
)

// DefaultVerifier checks against the Sigstore public-good root built into the box.
func DefaultVerifier() (*SigstoreVerifier, error) {
	defaultOnce.Do(func() {
		tr, err := root.NewTrustedRootFromJSON(trustedRootJSON)
		if err != nil {
			defaultErr = fmt.Errorf("update: the trusted root: %w", err)
			return
		}
		defaultV, defaultErr = NewSigstoreVerifier(tr, true)
	})
	return defaultV, defaultErr
}

// Verify implements Verifier.
func (s *SigstoreVerifier) Verify(manifest, bundleJSON []byte, version string) (string, error) {
	var b bundle.Bundle
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return "", fmt.Errorf("the signature bundle: %w", err)
	}
	signer := SignerPrefix + version
	id, err := verify.NewShortCertificateIdentity(SignerIssuer, "", signer, "")
	if err != nil {
		return "", err
	}
	res, err := s.v.Verify(&b, verify.NewPolicy(verify.WithArtifact(bytes.NewReader(manifest)), verify.WithCertificateIdentity(id)))
	if err != nil {
		return "", err
	}
	if res == nil || res.Signature == nil || res.Signature.Certificate == nil {
		return "", errors.New("the signature names no certificate")
	}
	return res.Signature.Certificate.SubjectAlternativeName, nil
}
