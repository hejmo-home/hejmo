// Package updatetest builds update files for tests: real ones, signed with a
// Sigstore of its own (sigstore-go's virtual Fulcio and Rekor), so the
// verification that runs is the production one, only against another root.
package updatetest

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"testing"
	"time"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	rekorpb "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"google.golang.org/protobuf/proto"

	"github.com/hejmo-home/hejmo/internal/update"
)

// Sigstore is a virtual Sigstore: a Fulcio, a Rekor and the root that trusts them.
type Sigstore struct {
	vs *ca.VirtualSigstore
}

// New returns a fresh virtual Sigstore.
func New(t testing.TB) *Sigstore {
	t.Helper()
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatal(err)
	}
	return &Sigstore{vs: vs}
}

// Verifier is the box's verifier, trusting this Sigstore and nothing else.
// The virtual Fulcio issues no certificate timestamps, so these are not
// required; the production verifier requires them (see StrictVerifier).
func (s *Sigstore) Verifier(t testing.TB) update.Verifier {
	t.Helper()
	v, err := update.NewSigstoreVerifier(s.vs, false)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// StrictVerifier requires certificate timestamps like the box does, which a
// virtual Fulcio cannot give.
func (s *Sigstore) StrictVerifier(t testing.TB) update.Verifier {
	t.Helper()
	v, err := update.NewSigstoreVerifier(s.vs, true)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Spec says what an update file is made of.
type Spec struct {
	Version  string // default 0.2.4
	Arch     string // default arm64
	Released string // default 2026-11-20
	Image    []byte // default a few bytes
	// Identity and Issuer the signature names; default the release workflow of the tag.
	Identity, Issuer string
	// Unsigned leaves the bundle out; Foreign signs with a Sigstore that is not trusted.
	Unsigned bool
	Foreign  *Sigstore
	// Manifest, if set, is the manifest bytes to use instead of the generated one.
	Manifest []byte
	// SwapImage changes the image after signing, as damage or a swap would.
	SwapImage []byte
}

// File builds the update file's bytes.
func (s *Sigstore) File(t testing.TB, sp Spec) []byte {
	t.Helper()
	if sp.Version == "" {
		sp.Version = "0.2.4"
	}
	if sp.Arch == "" {
		sp.Arch = "arm64"
	}
	if sp.Released == "" {
		sp.Released = "2026-11-20"
	}
	if sp.Image == nil {
		sp.Image = []byte("a docker image, as far as this test is concerned")
	}
	if sp.Identity == "" {
		sp.Identity = update.SignerPrefix + sp.Version
	}
	if sp.Issuer == "" {
		sp.Issuer = update.SignerIssuer
	}
	sum := sha256.Sum256(sp.Image)
	m := update.Manifest{Format: 1, Version: sp.Version, Arch: sp.Arch, Released: sp.Released, ImageSHA256: hexOf(sum[:]), ImageSize: int64(len(sp.Image))}
	manifest := sp.Manifest
	if manifest == nil {
		var err error
		if manifest, err = update.MarshalManifest(m); err != nil {
			t.Fatal(err)
		}
	}
	var bundleJSON []byte
	if !sp.Unsigned {
		signer := s
		if sp.Foreign != nil {
			signer = sp.Foreign
		}
		bundleJSON = signer.Bundle(t, sp.Identity, sp.Issuer, manifest)
	}
	image := sp.Image
	if sp.SwapImage != nil {
		image = sp.SwapImage
	}
	var out bytes.Buffer
	if sp.Unsigned {
		bundleJSON = nil
	}
	if err := packMaybeWithout(&out, manifest, bundleJSON, image); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Bundle signs the artifact as identity and returns the cosign bundle (JSON).
func (s *Sigstore) Bundle(t testing.TB, identity, issuer string, artifact []byte) []byte {
	t.Helper()
	e, err := s.vs.SignAtTime(identity, issuer, artifact, time.Now().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	vc, _ := e.VerificationContent()
	sc, _ := e.SignatureContent()
	ms := sc.MessageSignatureContent()
	entries, _ := e.TlogEntries()
	// The virtual Rekor returns entries without the parts a bundle carries: the
	// kind of the entry and the log's signed promise (the SET). Both are what
	// the real Rekor puts into a bundle, so add them.
	var tle []*rekorpb.TransparencyLogEntry
	for _, en := range entries {
		p := proto.Clone(en.TransparencyLogEntry()).(*rekorpb.TransparencyLogEntry)
		p.KindVersion = &rekorpb.KindVersion{Kind: "hashedrekord", Version: "0.0.1"}
		set, err := s.vs.RekorSignPayload(tlog.RekorPayload{Body: en.Body(), IntegratedTime: p.IntegratedTime, LogIndex: p.LogIndex, LogID: hexOf(p.LogId.KeyId)})
		if err != nil {
			t.Fatal(err)
		}
		p.InclusionPromise = &rekorpb.InclusionPromise{SignedEntryTimestamp: set}
		tle = append(tle, p)
	}
	stamps, _ := e.Timestamps()
	var rfc []*protocommon.RFC3161SignedTimestamp
	for _, ts := range stamps {
		rfc = append(rfc, &protocommon.RFC3161SignedTimestamp{SignedTimestamp: ts})
	}
	pb := &protobundle.Bundle{
		MediaType: "application/vnd.dev.sigstore.bundle+json;version=0.1", // the virtual Rekor makes a promise, no inclusion proof
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content:                   &protobundle.VerificationMaterial_X509CertificateChain{X509CertificateChain: &protocommon.X509CertificateChain{Certificates: []*protocommon.X509Certificate{{RawBytes: vc.Certificate().Raw}}}},
			TlogEntries:               tle,
			TimestampVerificationData: &protobundle.TimestampVerificationData{Rfc3161Timestamps: rfc},
		},
		Content: &protobundle.Bundle_MessageSignature{MessageSignature: &protocommon.MessageSignature{
			MessageDigest: &protocommon.HashOutput{Algorithm: protocommon.HashAlgorithm_SHA2_256, Digest: ms.Digest()},
			Signature:     ms.Signature(),
		}},
	}
	b := bundle.Bundle{Bundle: pb}
	data, err := b.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

func packMaybeWithout(out *bytes.Buffer, manifest, bundleJSON, image []byte) error {
	if bundleJSON == nil {
		return packWithoutBundle(out, manifest, image)
	}
	return update.Pack(out, manifest, bundleJSON, bytes.NewReader(image), int64(len(image)))
}

// packWithoutBundle is an update file as an image saved by hand would be: no signature.
func packWithoutBundle(out *bytes.Buffer, manifest, image []byte) error {
	tw := tar.NewWriter(out)
	for _, m := range []struct {
		name string
		data []byte
	}{{update.ManifestName, manifest}, {update.ImageName, image}} {
		if err := tw.WriteHeader(&tar.Header{Name: m.name, Mode: 0o644, Size: int64(len(m.data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(m.data); err != nil {
			return err
		}
	}
	return tw.Close()
}
