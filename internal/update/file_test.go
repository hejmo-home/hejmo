package update_test

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/update"
	"github.com/hejmo-home/hejmo/internal/update/updatetest"
)

// Ticket 221: the update file a parent uploads. The box checks it before it
// asks anyone to confirm: format, signature, version, architecture, image.

func write(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hejmo-0.2.4-arm64.update")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func refusal(t *testing.T, err error) *update.Refusal {
	t.Helper()
	var r *update.Refusal
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	return r
}

func TestAnUpdateFileSignedByTheReleaseWorkflowIsTaken(t *testing.T) {
	t.Parallel()
	sig := updatetest.New(t)
	data := sig.File(t, updatetest.Spec{})
	info, err := update.Inspect(write(t, data), update.Check{Arch: "arm64", Running: "0.2.3", Verify: sig.Verifier(t)})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if info.Version != "0.2.4" || info.Arch != "arm64" || info.Released != "2026-11-20" || info.Size != int64(len(data)) || info.SHA256 != hexOf(sum[:]) {
		t.Errorf("info = %+v", info)
	}
	if info.Signer != update.SignerPrefix+"0.2.4" {
		t.Errorf("signer = %q", info.Signer)
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

func TestAnUpdateFileIsRefusedWithItsReason(t *testing.T) {
	t.Parallel()
	sig := updatetest.New(t)
	other := updatetest.New(t)
	check := update.Check{Arch: "arm64", Running: "0.2.3", Verify: sig.Verifier(t)}
	for name, tc := range map[string]struct {
		file          []byte
		check         update.Check
		code          string
		version, arch string
	}{
		"signed by a Sigstore the box does not trust": {file: sig.File(t, updatetest.Spec{Foreign: other}), check: check, code: update.RefuseSignature, version: "0.2.4", arch: "arm64"},
		"signed by another workflow":                  {file: sig.File(t, updatetest.Spec{Identity: "https://github.com/someone/else/.github/workflows/release.yml@refs/tags/v0.2.4"}), check: check, code: update.RefuseSignature, version: "0.2.4", arch: "arm64"},
		"signed for another tag":                      {file: sig.File(t, updatetest.Spec{Identity: update.SignerPrefix + "0.2.3"}), check: check, code: update.RefuseSignature, version: "0.2.4", arch: "arm64"},
		"signed with another issuer":                  {file: sig.File(t, updatetest.Spec{Issuer: "https://accounts.google.com"}), check: check, code: update.RefuseSignature, version: "0.2.4", arch: "arm64"},
		"no signature":                                {file: sig.File(t, updatetest.Spec{Unsigned: true}), check: check, code: update.RefuseNoSignature, version: "0.2.4", arch: "arm64"},
		"the image swapped after signing":             {file: sig.File(t, updatetest.Spec{SwapImage: []byte("another image")}), check: check, code: update.RefuseDamaged, version: "0.2.4", arch: "arm64"},
		"the other architecture":                      {file: sig.File(t, updatetest.Spec{Arch: "amd64"}), check: check, code: update.RefuseArch, version: "0.2.4", arch: "amd64"},
		"the same version":                            {file: sig.File(t, updatetest.Spec{Version: "0.2.3"}), check: check, code: update.RefuseOlder, version: "0.2.3", arch: "arm64"},
		"an older version":                            {file: sig.File(t, updatetest.Spec{Version: "0.1.9"}), check: check, code: update.RefuseOlder, version: "0.1.9", arch: "arm64"},
		"a pre-release":                               {file: sig.File(t, updatetest.Spec{Version: "0.2.5-rc.1"}), check: check, code: update.RefusePrerelease, version: "0.2.5-rc.1", arch: "arm64"},
		"not an archive":                              {file: []byte("hello, this is not an update"), check: check, code: update.RefuseNotUpdate},
		"empty":                                       {file: nil, check: check, code: update.RefuseNotUpdate},
		"a manifest that is not JSON":                 {file: sig.File(t, updatetest.Spec{Manifest: []byte("version: 0.2.4")}), check: check, code: update.RefuseNotUpdate},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := update.Inspect(write(t, tc.file), tc.check)
			r := refusal(t, err)
			if r.Code != tc.code || r.Version != tc.version || r.Arch != tc.arch {
				t.Errorf("refusal = %+v, want %s %s %s", r, tc.code, tc.version, tc.arch)
			}
		})
	}
}

func TestWithoutAKnownRunningVersionAnyNewEnoughFileIsTakenOnceSigned(t *testing.T) {
	t.Parallel()
	sig := updatetest.New(t)
	if _, err := update.Inspect(write(t, sig.File(t, updatetest.Spec{Version: "0.0.1"})), update.Check{Arch: "arm64", Verify: sig.Verifier(t)}); err != nil {
		t.Fatalf("err = %v", err)
	}
}

// An image saved by hand ("docker save") has the members of a Docker archive, none of a release.
func TestADockerSaveIsReportedAsUnsigned(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, n := range []string{"blobs/", "blobs/sha256/abc", "manifest.json", "index.json", "oci-layout"} {
		h := &tar.Header{Name: n, Mode: 0o644, Size: 2, Typeflag: tar.TypeReg}
		if strings.HasSuffix(n, "/") {
			h.Typeflag, h.Size = tar.TypeDir, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			_, _ = tw.Write([]byte("{}"))
		}
	}
	_ = tw.Close()
	sig := updatetest.New(t)
	_, err := update.Inspect(write(t, buf.Bytes()), update.Check{Arch: "arm64", Running: "0.2.3", Verify: sig.Verifier(t)})
	if r := refusal(t, err); r.Code != update.RefuseNoSignature {
		t.Errorf("code = %s", r.Code)
	}
}

func TestAnArchiveWithAnythingElseInItIsNotAnUpdateFile(t *testing.T) {
	t.Parallel()
	sig := updatetest.New(t)
	good := sig.File(t, updatetest.Spec{})
	// A link and a second copy of a member, appended before the end of the archive.
	for name, extra := range map[string]*tar.Header{
		"a symbolic link":  {Name: "image.tar.bak", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777},
		"a stray file":     {Name: "run.sh", Typeflag: tar.TypeReg, Mode: 0o755, Size: 0},
		"an absolute path": {Name: "/etc/cron.d/x", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			if err := tw.WriteHeader(extra); err != nil {
				t.Fatal(err)
			}
			// The release's own members follow.
			tr := tar.NewReader(bytes.NewReader(good))
			for {
				h, err := tr.Next()
				if err != nil {
					break
				}
				_ = tw.WriteHeader(h)
				data := new(bytes.Buffer)
				_, _ = data.ReadFrom(tr)
				_, _ = tw.Write(data.Bytes())
			}
			_ = tw.Close()
			_, err := update.Inspect(write(t, buf.Bytes()), update.Check{Arch: "arm64", Running: "0.2.3", Verify: sig.Verifier(t)})
			if r := refusal(t, err); r.Code != update.RefuseNotUpdate {
				t.Errorf("code = %s", r.Code)
			}
		})
	}
}

func TestVersionGreater(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"0.2.4", "0.2.3", true}, {"0.2.3", "0.2.3", false}, {"0.2.3", "0.2.4", false},
		{"0.10.0", "0.9.9", true}, {"1.0.0", "0.99.99", true}, {"0.2.4", "garbage", false}, {"", "0.1.0", false},
	} {
		if got := update.VersionGreater(tc.a, tc.b); got != tc.want {
			t.Errorf("VersionGreater(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

func TestPackAndManifestRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	img := filepath.Join(dir, "image.tar")
	if err := os.WriteFile(img, []byte("layers"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := update.NewManifest("0.3.0", "amd64", "2026-12-01", img)
	if err != nil || m.ImageSize != 6 || len(m.ImageSHA256) != 64 || m.Format != 1 {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	if _, err := update.NewManifest("0.3", "amd64", "2026-12-01", img); err == nil {
		t.Error("a version without three numbers was taken")
	}
	if _, err := update.NewManifest("0.3.0", "riscv", "2026-12-01", img); err == nil {
		t.Error("an unknown architecture was taken")
	}
}

// The box requires the certificate timestamp every Fulcio certificate carries; the
// test Sigstore issues none, so a verifier set up like the box's refuses its files.
func TestTheBoxRequiresCertificateTimestamps(t *testing.T) {
	t.Parallel()
	sig := updatetest.New(t)
	_, err := update.Inspect(write(t, sig.File(t, updatetest.Spec{})), update.Check{Arch: "arm64", Running: "0.2.3", Verify: sig.StrictVerifier(t)})
	if r := refusal(t, err); r.Code != update.RefuseSignature || !strings.Contains(r.Error(), "certificate timestamp") {
		t.Errorf("err = %v", err)
	}
}

// The box ships Sigstore's public root: a file signed by anyone else is refused
// by the production verifier without a connection.
func TestTheShippedTrustedRootLoadsAndRefusesForeignFiles(t *testing.T) {
	t.Parallel()
	v, err := update.DefaultVerifier()
	if err != nil {
		t.Fatal(err)
	}
	sig := updatetest.New(t)
	_, err = update.Inspect(write(t, sig.File(t, updatetest.Spec{})), update.Check{Arch: "arm64", Running: "0.2.3", Verify: v})
	if r := refusal(t, err); r.Code != update.RefuseSignature {
		t.Errorf("err = %v", err)
	}
	if len(update.TrustedRootJSON()) < 1000 {
		t.Error("the trusted root is empty")
	}
}
