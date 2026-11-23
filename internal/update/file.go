package update

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The update file (ticket 221, Q154) is how a release reaches a box that is
// offline. It is a plain tar archive with exactly three members, built by the
// release workflow (tools/mkupdate):
//
//	hejmo-update.json            the manifest: version, architecture, and the SHA-256 of the image
//	hejmo-update.sigstore.json   the cosign bundle that signs the manifest, keyless, by the release workflow
//	image.tar                    the box's image for that architecture (docker save)
//
// Signing the small manifest and not the image keeps the check short on a Pi;
// the manifest's hash binds the image to the signature. The box checks the
// file before it asks parents to confirm (Inspect). The host script checks it
// again on its own before it loads anything, because the container is not
// trusted.
const (
	// FileExt is what an update file's name ends in.
	FileExt = ".update"
	// MaxFileSize is the biggest update file the box takes (a release image is about 60 MB).
	MaxFileSize = 500 << 20

	ManifestName = "hejmo-update.json"
	BundleName   = "hejmo-update.sigstore.json"
	ImageName    = "image.tar"

	maxManifest = 64 << 10
	maxBundle   = 256 << 10
)

// Manifest is the signed description of an update file.
type Manifest struct {
	Format      int    `json:"format"`       // 1
	Version     string `json:"version"`      // x.y.z, or x.y.z-suffix for a pre-release
	Arch        string `json:"arch"`         // "arm64" or "amd64"
	Released    string `json:"released"`     // YYYY-MM-DD
	ImageSHA256 string `json:"image_sha256"` // hex
	ImageSize   int64  `json:"image_size"`
}

var (
	manifestVersionRe = regexp.MustCompile(`^[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}(-[0-9A-Za-z][0-9A-Za-z.-]{0,30})?$`)
	hexRe             = regexp.MustCompile(`^[0-9a-f]{64}$`)
	releasedRe        = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// Architectures a release is built for.
var Architectures = []string{"arm64", "amd64"}

func validArch(a string) bool {
	for _, x := range Architectures {
		if a == x {
			return true
		}
	}
	return false
}

// Why an update file is refused. Each has a text in both languages.
const (
	RefuseNotUpdate   = "not_update"        // not an update file at all
	RefuseNoSignature = "no_signature"      // an image without the project's signature
	RefuseSignature   = "signature_invalid" // a signature that is not the project's
	RefuseArch        = "wrong_arch"        // for the other kind of box
	RefuseOlder       = "not_newer"         // the same or an older version
	RefusePrerelease  = "prerelease"        // a version with a suffix such as -rc.1
	RefuseDamaged     = "damaged"           // signed, but the image does not match: damaged on the way
)

// Refusal is an update file the box does not take. Version and Arch name what
// the file says it is, when it got that far.
type Refusal struct {
	Code          string
	Version, Arch string
	Err           error // what the check said, for the log
}

func (r *Refusal) Error() string {
	if r.Err != nil {
		return "update file refused (" + r.Code + "): " + r.Err.Error()
	}
	return "update file refused (" + r.Code + ")"
}

func (r *Refusal) Unwrap() error { return r.Err }

// Info is what a checked update file holds.
type Info struct {
	Version  string
	Arch     string
	Released string // YYYY-MM-DD
	Size     int64  // of the file
	SHA256   string // of the file, hex
	// Signer is the identity the signature names (the release workflow of the tag).
	Signer string
}

// Check says what an update file must be to be taken.
type Check struct {
	// Arch is the kind of box this is ("arm64" or "amd64").
	Arch string
	// Running is the version that runs now; empty when the box does not know it.
	Running string
	// Verify checks the signature.
	Verify Verifier
}

// Inspect reads the update file at path and returns what it holds, or a
// *Refusal. It touches nothing but the file, and never unpacks it.
func Inspect(path string, c Check) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Info{}, err
	}
	whole := sha256.New()
	tr := tar.NewReader(io.TeeReader(f, whole))

	var manifest, bundle []byte
	var imageSum string
	var imageSize int64
	var sawImage, dockerSave, extra bool
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Info{}, &Refusal{Code: RefuseNotUpdate, Err: err}
		}
		name := strings.TrimPrefix(h.Name, "./")
		switch name {
		case "manifest.json", "oci-layout", "index.json", "repositories":
			dockerSave = true // an image saved by hand, not a release
		}
		if h.Typeflag != tar.TypeReg || seen[name] || (name != ManifestName && name != BundleName && name != ImageName) {
			extra = true // not part of a release: the tar's data is skipped by the next call to Next
			continue
		}
		seen[name] = true
		switch name {
		case ManifestName:
			if manifest, err = readLimited(tr, maxManifest); err != nil {
				return Info{}, &Refusal{Code: RefuseNotUpdate, Err: err}
			}
		case BundleName:
			if bundle, err = readLimited(tr, maxBundle); err != nil {
				return Info{}, &Refusal{Code: RefuseNotUpdate, Err: err}
			}
		case ImageName:
			sum := sha256.New()
			n, err := io.Copy(sum, tr)
			if err != nil {
				return Info{}, &Refusal{Code: RefuseNotUpdate, Err: err}
			}
			imageSum, imageSize, sawImage = hex.EncodeToString(sum.Sum(nil)), n, true
		}
	}
	// What remains after the tar's end marker is not part of the archive; hash
	// it too, so the file's checksum is the whole file's.
	if _, err := io.Copy(io.Discard, io.TeeReader(f, whole)); err != nil {
		return Info{}, err
	}
	if manifest == nil || extra {
		if manifest == nil && dockerSave {
			return Info{}, &Refusal{Code: RefuseNoSignature, Err: errors.New("an image without a manifest of the release")}
		}
		return Info{}, &Refusal{Code: RefuseNotUpdate, Err: errors.New("no manifest, or members that are not part of a release")}
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(manifest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil || m.Format != 1 || !manifestVersionRe.MatchString(m.Version) || !validArch(m.Arch) || !hexRe.MatchString(m.ImageSHA256) || !releasedRe.MatchString(m.Released) || m.ImageSize <= 0 {
		return Info{}, &Refusal{Code: RefuseNotUpdate, Err: fmt.Errorf("bad manifest: %v", err)}
	}
	refuse := func(code string, err error) error {
		return &Refusal{Code: code, Version: m.Version, Arch: m.Arch, Err: err}
	}
	if bundle == nil {
		return Info{}, refuse(RefuseNoSignature, errors.New("no signature bundle"))
	}
	if c.Verify == nil {
		return Info{}, refuse(RefuseSignature, errors.New("no verifier"))
	}
	signer, err := c.Verify.Verify(manifest, bundle, m.Version)
	if err != nil {
		return Info{}, refuse(RefuseSignature, err)
	}
	// From here on the manifest is the project's word.
	if strings.Contains(m.Version, "-") {
		return Info{}, refuse(RefusePrerelease, nil)
	}
	if m.Arch != c.Arch {
		return Info{}, refuse(RefuseArch, nil)
	}
	if c.Running != "" && !VersionGreater(m.Version, c.Running) {
		return Info{}, refuse(RefuseOlder, nil)
	}
	if !sawImage || imageSum != m.ImageSHA256 || imageSize != m.ImageSize {
		return Info{}, refuse(RefuseDamaged, errors.New("the image does not match the signed manifest"))
	}
	return Info{Version: m.Version, Arch: m.Arch, Released: m.Released, Size: st.Size(), SHA256: hex.EncodeToString(whole.Sum(nil)), Signer: signer}, nil
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errors.New("member too large")
	}
	return data, nil
}

// VersionGreater reports whether a is a newer x.y.z than b. A version that
// does not parse is never greater.
func VersionGreater(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) []int {
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

// NewManifest describes the image tar at imagePath for a release.
func NewManifest(version, arch, released, imagePath string) (Manifest, error) {
	f, err := os.Open(imagePath)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	sum := sha256.New()
	n, err := io.Copy(sum, f)
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{Format: 1, Version: version, Arch: arch, Released: released, ImageSHA256: hex.EncodeToString(sum.Sum(nil)), ImageSize: n}
	if !manifestVersionRe.MatchString(version) || !validArch(arch) || !releasedRe.MatchString(released) || n <= 0 {
		return Manifest{}, errors.New("update: bad version, architecture, date or an empty image")
	}
	return m, nil
}

// MarshalManifest is the manifest's bytes: the bytes that are signed.
func MarshalManifest(m Manifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// Pack writes an update file: the manifest, the bundle that signs it and the
// image, in this order.
func Pack(w io.Writer, manifest, bundle []byte, image io.Reader, imageSize int64) error {
	tw := tar.NewWriter(w)
	put := func(name string, size int64, r io.Reader) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: size, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := io.Copy(tw, r)
		return err
	}
	if err := put(ManifestName, int64(len(manifest)), bytes.NewReader(manifest)); err != nil {
		return err
	}
	if err := put(BundleName, int64(len(bundle)), bytes.NewReader(bundle)); err != nil {
		return err
	}
	if err := put(ImageName, imageSize, image); err != nil {
		return err
	}
	return tw.Close()
}
