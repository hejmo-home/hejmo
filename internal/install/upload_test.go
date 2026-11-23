package install

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/update"
)

// Ticket 221 (Q154): the host script installs an update file the box handed
// over, and checks it again on its own. These tests run it as a black box like
// the nightly update: --install, against the stand-in Docker.

// updateFile builds an update file (the cosign bundle is a stand-in: the stand-in
// Docker's cosign decides). Modify changes the archive's members.
type fileSpec struct {
	version, arch string
	image         []byte
	extra         func(tw *tar.Writer)
	drop          string // a member to leave out
	manifest      []byte
}

func (f fileSpec) bytes(t *testing.T) []byte {
	t.Helper()
	if f.version == "" {
		f.version = "0.2.4"
	}
	if f.arch == "" {
		f.arch = "arm64"
	}
	if f.image == nil {
		f.image = []byte("layers of an image")
	}
	sum := sha256.Sum256(f.image)
	m := update.Manifest{Format: 1, Version: f.version, Arch: f.arch, Released: "2026-11-20", ImageSHA256: hex.EncodeToString(sum[:]), ImageSize: int64(len(f.image))}
	manifest := f.manifest
	if manifest == nil {
		var err error
		if manifest, err = update.MarshalManifest(m); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	put := func(name string, data []byte) {
		if name == f.drop {
			return
		}
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(data)
	}
	put(update.ManifestName, manifest)
	put(update.BundleName, []byte(`{"stand-in":"bundle"}`))
	put(update.ImageName, f.image)
	if f.extra != nil {
		f.extra(tw)
	}
	_ = tw.Close()
	return buf.Bytes()
}

func shaOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// handOver puts the file where the box does and writes the request the box writes.
func (b *box) handOver(file []byte, version string, at time.Time) {
	b.t.Helper()
	b.write("opt/hejmo/data/update-file", string(file))
	b.writeRequest(shaOf(file), version, at)
}

func (b *box) writeRequest(sum, version string, at time.Time) {
	b.t.Helper()
	b.write("opt/hejmo/data/update-settings", "mode=on\noffline=yes\ninstall="+version+"\ninstall_sha256="+sum+"\ninstall_time="+stamp(at)+"\n")
}

// install runs the installed updater as the path unit would: with --install.
func (b *box) install(env ...string) (string, error) {
	b.t.Helper()
	cmd := exec.Command("bash", filepath.Join(b.root, "opt/hejmo/hejmo-update"), "--install")
	cmd.Env = append(append([]string{}, b.env...), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (b *box) mustInstall(extra ...string) {
	b.t.Helper()
	if out, err := b.install(extra...); err != nil {
		b.t.Fatalf("install failed: %v\n%s", err, out)
	}
}

func (b *box) snapshot(name string) string {
	data, _ := os.ReadFile(filepath.Join(b.state, name))
	return string(data)
}

func (b *box) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(b.root, rel))
	return err == nil
}

func (b *box) dockerCalls(prefix string) []string {
	var out []string
	for _, l := range strings.Split(b.calls(), "\n") {
		if strings.HasPrefix(l, "docker "+prefix) {
			out = append(out, l)
		}
	}
	return out
}

// withoutCosignImage makes the stand-in Docker not know the cosign image.
func (b *box) withoutCosignImage() {
	b.t.Helper()
	data, _ := os.ReadFile(filepath.Join(b.bin, "docker"))
	patched := strings.Replace(string(data), "image)\n\tcase \"$2\" in\n\tinspect)", "image)\n\tcase \"$2\" in\n\tinspect)\n\t\tcase \"$*\" in *cosign*) exit 1 ;; esac", 1)
	if patched == string(data) {
		b.t.Fatal("the stand-in did not change")
	}
	if err := os.WriteFile(filepath.Join(b.bin, "docker"), []byte(patched), 0o755); err != nil {
		b.t.Fatal(err)
	}
}

func TestInstallRegistersThePathUnitAndTellsTheBoxItInstallsFiles(t *testing.T) {
	b := installed(t)
	path := b.read("etc/systemd/system/hejmo-update.path")
	for _, want := range []string{"PathChanged=/opt/hejmo/data/update-settings", "Unit=hejmo-update-install.service", "WantedBy="} {
		if !strings.Contains(path, want) {
			t.Errorf("path unit lacks %q:\n%s", want, path)
		}
	}
	svc := b.read("etc/systemd/system/hejmo-update-install.service")
	for _, want := range []string{"Type=oneshot", "ExecStart=/opt/hejmo/hejmo-update --install", "TimeoutStartSec="} {
		if !strings.Contains(svc, want) {
			t.Errorf("install service lacks %q:\n%s", want, svc)
		}
	}
	if !strings.Contains(b.read("opt/hejmo/compose.yaml"), `HEJMO_HOST_UPDATER_INSTALL: "1"`) {
		t.Error("the box is not told that the updater installs files")
	}
	if _, err := os.Stat(filepath.Join(b.state, "enabled-hejmo-update.path")); err != nil {
		t.Error("the path unit is not enabled")
	}
	// The signature checker is fetched now, while there is internet: an offline box has no way to.
	b.withoutCosignImage()
	_ = os.Remove(filepath.Join(b.state, "calls"))
	if out, err := b.run(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(b.calls(), "docker pull ghcr.io/sigstore/cosign/cosign:v2.5.3@sha256:") {
		t.Errorf("the signature checker is not fetched at install:\n%s", b.calls())
	}
	// The updater is the same file each time: running the script again does not rewrite the units.
	for _, f := range []string{"etc/systemd/system/hejmo-update.path", "etc/systemd/system/hejmo-update-install.service", "opt/hejmo/trusted-root.json"} {
		before, _ := os.Stat(filepath.Join(b.root, f))
		if out, err := b.run(); err != nil {
			t.Fatalf("second run: %v\n%s", err, out)
		}
		after, _ := os.Stat(filepath.Join(b.root, f))
		if !before.ModTime().Equal(after.ModTime()) {
			t.Errorf("%s was rewritten", f)
		}
	}
}

func TestTheHostScriptTrustsTheSameSigstoreRootAsTheBox(t *testing.T) {
	b := installed(t)
	got := b.read("opt/hejmo/trusted-root.json")
	if got != strings.TrimRight(string(update.TrustedRootJSON()), "\n")+"\n" {
		t.Error("opt/hejmo/trusted-root.json is not internal/update/trusted_root.json: refresh both with tools/trustedroot")
	}
	// The cosign image of the offline check is the one of the nightly check.
	script, _ := os.ReadFile("../../install.sh")
	pins := regexp.MustCompile(`ghcr\.io/sigstore/cosign/cosign:v[0-9.]+@sha256:[0-9a-f]{64}`).FindAllString(string(script), -1)
	if len(pins) < 2 {
		t.Fatalf("pins = %v", pins)
	}
	for _, p := range pins {
		if p != pins[0] {
			t.Errorf("two different cosign images: %s and %s", p, pins[0])
		}
	}
}

func TestAnUploadedFileIsCheckedAgainAndInstalledWithBackupAndHealthCheck(t *testing.T) {
	b := installed(t)
	file := fileSpec{}.bytes(t)
	b.handOver(file, "0.2.4", time.Now())
	b.mustInstall()

	if !hasLine(b.status(), "state=updated") || !hasLine(b.status(), "version=0.2.4") {
		t.Fatalf("status:\n%s", b.status())
	}
	if hasLine(b.status(), "step=verify") || strings.Contains(b.status(), "step=") {
		t.Errorf("a finished installation still names a step:\n%s", b.status())
	}
	if b.running() == "" || !strings.Contains(b.running(), "ghcr.io/hejmo-home/hejmo:0.2.4") {
		t.Errorf("the box runs %q", b.running())
	}
	if !strings.Contains(b.read("opt/hejmo/.env"), "HEJMO_IMAGE="+imageRepo+":0.2.4") {
		t.Errorf(".env:\n%s", b.read("opt/hejmo/.env"))
	}
	// The signature is checked on the host script's own account: the signer of that tag, offline, with the root of the install.
	verify := b.dockerCalls("run")
	if len(verify) != 1 {
		t.Fatalf("docker run calls: %v", verify)
	}
	for _, want := range []string{"--network none", "verify-blob", "--new-bundle-format", "--offline", "--trusted-root /trusted-root.json", "--bundle /in/hejmo-update.sigstore.json",
		"--certificate-identity https://github.com/hejmo-home/hejmo/.github/workflows/release.yml@refs/tags/v0.2.4", "--certificate-oidc-issuer https://token.actions.githubusercontent.com", "/in/hejmo-update.json"} {
		if !strings.Contains(verify[0], want) {
			t.Errorf("the check lacks %q:\n%s", want, verify[0])
		}
	}
	if len(b.dockerCalls("load")) != 1 || len(b.dockerCalls("pull")) != 0 {
		t.Errorf("the image is loaded from the file, never pulled:\n%s", b.calls())
	}
	if strings.Contains(b.calls(), "curl -fsS --max-time 30 -H") || strings.Contains(b.calls(), "api.github.com") {
		t.Errorf("an uploaded file needs no internet:\n%s", b.calls())
	}
	// Backup before the switch, and the file is gone from the data volume.
	if b.read("opt/hejmo/state/pre-update.db") != "the family's data" {
		t.Error("no backup of the data was kept")
	}
	if b.exists("opt/hejmo/data/update-file") || b.exists("opt/hejmo/state/incoming.update") || b.exists("opt/hejmo/state/incoming") {
		t.Error("the file or its copy stays on the box")
	}
	// The steps are reported while it goes.
	if st := b.snapshot("status-at-backup"); !hasLine(st, "state=installing") || !hasLine(st, "step=backup") || !hasLine(st, "target=0.2.4") || !hasLine(st, "version=0.1.0") {
		t.Errorf("status while the data is saved:\n%s", st)
	}
	if st := b.snapshot("status-at-start"); !hasLine(st, "step=start") || !regexp.MustCompile(`(?m)^backup=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`).MatchString(st) {
		t.Errorf("status while the new version starts:\n%s", st)
	}
	if st := b.snapshot("status-at-health"); !hasLine(st, "step=health") {
		t.Errorf("status while the health check runs:\n%s", st)
	}
	// The same yes is not answered twice.
	_ = os.Remove(filepath.Join(b.state, "calls"))
	b.mustInstall()
	if b.calls() != "" {
		t.Errorf("the script acted on a yes it answered already:\n%s", b.calls())
	}
}

func TestAnUploadedFileThatDoesNotAnswerIsRolledBack(t *testing.T) {
	b := installed(t)
	b.handOver(fileSpec{}.bytes(t), "0.2.4", time.Now())
	b.mustInstall("FAKE_NEW_HEALTH=down")
	st := b.status()
	for _, want := range []string{"state=rolled_back", "version=0.1.0", "target=0.2.4", "reason=health_check_failed"} {
		if !hasLine(st, want) {
			t.Errorf("status lacks %q:\n%s", want, st)
		}
	}
	if !regexp.MustCompile(`(?m)^backup=`).MatchString(st) {
		t.Errorf("the rollback does not say when the data was saved:\n%s", st)
	}
	if b.read("opt/hejmo/data/hejmo.db") != "the family's data" {
		t.Error("the data of before the update is not back")
	}
	if !strings.Contains(b.running(), "sha256:") || strings.Contains(b.running(), ":0.2.4") {
		t.Errorf("the box runs %q, not the previous image", b.running())
	}
	// An uploaded file that failed may be uploaded again: nothing is held back until a newer release.
	if b.exists("opt/hejmo/state/skip-version") {
		t.Error("an uploaded version is skipped from now on")
	}
}

func TestAnUploadedFileWhoseNewVersionDoesNotStartIsRolledBack(t *testing.T) {
	b := installed(t)
	b.handOver(fileSpec{}.bytes(t), "0.2.4", time.Now())
	b.mustInstall("FAKE_START=fail")
	if st := b.status(); !hasLine(st, "state=rolled_back") || !hasLine(st, "reason=start_failed") {
		t.Errorf("status:\n%s", st)
	}
}

func TestAnUploadedFileThatCannotBeBackedUpChangesNothing(t *testing.T) {
	b := installed(t)
	b.handOver(fileSpec{}.bytes(t), "0.2.4", time.Now())
	if out, err := b.install("FAKE_BACKUP=fail"); err == nil {
		t.Fatalf("a failed backup is a failed run:\n%s", out)
	}
	if st := b.status(); !hasLine(st, "state=failed") || !hasLine(st, "reason=backup_failed") || !hasLine(st, "target=0.2.4") {
		t.Errorf("status:\n%s", st)
	}
	if strings.Contains(b.calls(), "compose up") || b.read("opt/hejmo/data/hejmo.db") != "the family's data" {
		t.Errorf("the box was touched:\n%s", b.calls())
	}
}

func TestAnUploadedFileIsRefusedForItsReasonAndNothingIsChanged(t *testing.T) {
	good := fileSpec{}.bytes(t)
	for name, tc := range map[string]struct {
		file    []byte
		sum     string // the checksum the box names; empty: the file's own
		env     []string
		version string
		state   string
		reason  string
	}{
		"the file is not the one the box checked": {file: good, sum: strings.Repeat("0", 64), state: "failed", reason: "file_invalid"},
		"a signature that is not the project's":   {file: good, env: []string{"FAKE_COSIGN=bad"}, state: "rejected", reason: "signature_invalid"},
		"an extra member": {file: fileSpec{extra: func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "run.sh", Mode: 0o755, Size: 0, Typeflag: tar.TypeReg})
		}}.bytes(t), state: "failed", reason: "file_invalid"},
		"a link in place of the image": {file: fileSpec{drop: "image.tar", extra: func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "image.tar", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
		}}.bytes(t), state: "failed", reason: "file_invalid"},
		"a missing member":                      {file: fileSpec{drop: "hejmo-update.sigstore.json"}.bytes(t), state: "failed", reason: "file_invalid"},
		"a file that is no archive":             {file: []byte("not a tar"), state: "failed", reason: "file_invalid"},
		"the other architecture":                {file: fileSpec{arch: "amd64"}.bytes(t), state: "failed", reason: "file_invalid"},
		"an older version":                      {file: fileSpec{version: "0.0.9"}.bytes(t), version: "0.0.9", state: "failed", reason: "file_invalid"},
		"another version than the box was told": {file: fileSpec{version: "0.2.5"}.bytes(t), version: "0.2.4", state: "failed", reason: "file_invalid"},
		"an image that is not the signed one":   {file: fileSpec{manifest: []byte("{\n  \"format\": 1,\n  \"version\": \"0.2.4\",\n  \"arch\": \"arm64\",\n  \"released\": \"2026-11-20\",\n  \"image_sha256\": \"" + strings.Repeat("a", 64) + "\",\n  \"image_size\": 18\n}")}.bytes(t), state: "failed", reason: "file_invalid"},
		"an image that does not load":           {file: good, env: []string{"FAKE_LOAD=fail"}, state: "failed", reason: "load_failed"},
		"an image of another version":           {file: good, env: []string{"FAKE_LABEL=0.2.9"}, state: "failed", reason: "load_failed"},
	} {
		t.Run(name, func(t *testing.T) {
			b := installed(t)
			version := tc.version
			if version == "" {
				version = "0.2.4"
			}
			sum := tc.sum
			if sum == "" {
				sum = shaOf(tc.file)
			}
			b.write("opt/hejmo/data/update-file", string(tc.file))
			b.writeRequest(sum, version, time.Now())
			b.mustInstall(tc.env...)
			st := b.status()
			if !hasLine(st, "state="+tc.state) || !hasLine(st, "reason="+tc.reason) || !hasLine(st, "target="+version) || !hasLine(st, "version=0.1.0") {
				t.Errorf("status:\n%s", st)
			}
			if strings.Contains(b.calls(), "compose up") || strings.Contains(b.calls(), "compose stop") || b.read("opt/hejmo/data/hejmo.db") != "the family's data" {
				t.Errorf("the box was touched:\n%s", b.calls())
			}
			if b.exists("opt/hejmo/data/update-file") || b.exists("opt/hejmo/state/incoming") || b.exists("opt/hejmo/state/incoming.update") {
				t.Error("the file or what was taken from it stays on the box")
			}
			if !strings.Contains(b.read("opt/hejmo/.env"), "HEJMO_IMAGE=") || strings.Contains(b.read("opt/hejmo/.env"), ":0.2.") {
				t.Errorf("the image was switched:\n%s", b.read("opt/hejmo/.env"))
			}
		})
	}
}

func TestADoubtfulRequestIsNoRequest(t *testing.T) {
	good := fileSpec{}.bytes(t)
	sum := shaOf(good)
	now := stamp(time.Now())
	for name, settings := range map[string]string{
		"no request":         "mode=on\n",
		"a bad version":      "mode=on\ninstall=0.2.4-rc.1\ninstall_sha256=" + sum + "\ninstall_time=" + now + "\n",
		"markup in version":  "mode=on\ninstall=0.2.4;id\ninstall_sha256=" + sum + "\ninstall_time=" + now + "\n",
		"a short checksum":   "mode=on\ninstall=0.2.4\ninstall_sha256=abc\ninstall_time=" + now + "\n",
		"an upper-case sum":  "mode=on\ninstall=0.2.4\ninstall_sha256=" + strings.ToUpper(sum) + "\ninstall_time=" + now + "\n",
		"no time":            "mode=on\ninstall=0.2.4\ninstall_sha256=" + sum + "\n",
		"a bad time":         "mode=on\ninstall=0.2.4\ninstall_sha256=" + sum + "\ninstall_time=yesterday\n",
		"a request too old":  "mode=on\ninstall=0.2.4\ninstall_sha256=" + sum + "\ninstall_time=" + stamp(time.Now().Add(-3*time.Hour)) + "\n",
		"from the future":    "mode=on\ninstall=0.2.4\ninstall_sha256=" + sum + "\ninstall_time=" + stamp(time.Now().Add(time.Hour)) + "\n",
		"spaces in the line": "mode=on\ninstall = 0.2.4\ninstall_sha256=" + sum + "\ninstall_time=" + now + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			b := installed(t)
			b.write("opt/hejmo/data/update-file", string(good))
			b.write("opt/hejmo/data/update-settings", settings)
			_ = os.Remove(filepath.Join(b.state, "calls"))
			b.mustInstall()
			if b.calls() != "" || b.status() != "" {
				t.Errorf("the script acted:\ncalls %s\nstatus %s", b.calls(), b.status())
			}
		})
	}
	// A settings file that is a link is not read at all.
	b := installed(t)
	b.write("opt/hejmo/data/update-file", string(good))
	b.write("opt/hejmo/elsewhere", "mode=on\ninstall=0.2.4\ninstall_sha256="+sum+"\ninstall_time="+now+"\n")
	_ = os.Remove(filepath.Join(b.root, "opt/hejmo/data/update-settings"))
	if err := os.Symlink(filepath.Join(b.root, "opt/hejmo/elsewhere"), filepath.Join(b.root, "opt/hejmo/data/update-settings")); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(b.state, "calls"))
	b.mustInstall()
	if b.calls() != "" || b.status() != "" {
		t.Errorf("the script read a linked settings file:\ncalls %s\nstatus %s", b.calls(), b.status())
	}
}

func TestAFileThatIsALinkIsNeverRead(t *testing.T) {
	b := installed(t)
	secret := "root's secret"
	b.write("opt/hejmo/secret", secret)
	if err := os.Symlink(filepath.Join(b.root, "opt/hejmo/secret"), filepath.Join(b.root, "opt/hejmo/data/update-file")); err != nil {
		t.Fatal(err)
	}
	b.writeRequest(shaOf([]byte(secret)), "0.2.4", time.Now())
	b.mustInstall()
	if st := b.status(); !hasLine(st, "reason=file_invalid") {
		t.Errorf("status:\n%s", st)
	}
	if strings.Contains(b.calls(), "docker load") {
		t.Error("a linked file was loaded")
	}
}

// The container is not trusted: it can swap the file for a link between a check of the host script
// and its copy. The script must copy without following links and check what arrived.
func TestAFileSwappedForALinkBetweenCheckAndCopyIsNeverRead(t *testing.T) {
	b := installed(t)
	good := fileSpec{}.bytes(t)
	b.handOver(good, "0.2.4", time.Now())
	secret := "root's secret"
	b.write("opt/hejmo/secret", secret)
	b.mustInstall("FAKE_SWAP_FILE=" + filepath.Join(b.root, "opt/hejmo/secret"))
	if st := b.status(); !hasLine(st, "reason=file_invalid") {
		t.Errorf("status:\n%s", st)
	}
	if b.exists("opt/hejmo/state/incoming.update") {
		t.Error("what arrived was kept")
	}
	if strings.Contains(b.calls(), "docker load") {
		t.Error("a swapped file was loaded")
	}
}

func TestAnUnknownRunningVersionRefusesTheFile(t *testing.T) {
	b := installed(t)
	if err := os.WriteFile(filepath.Join(b.state, "version"), []byte("\n"), 0o644); err != nil { // a preview build has no version label
		t.Fatal(err)
	}
	b.handOver(fileSpec{}.bytes(t), "0.2.4", time.Now())
	b.mustInstall()
	if st := b.status(); !hasLine(st, "state=failed") || !hasLine(st, "reason=file_invalid") {
		t.Errorf("status:\n%s", st)
	}
	if strings.Contains(b.calls(), "docker load") || strings.Contains(b.calls(), "compose up") {
		t.Error("an unknown version was replaced")
	}
}

func TestAStoppedBoxCannotBeUpdatedFromAFile(t *testing.T) {
	b := installed(t)
	b.handOver(fileSpec{}.bytes(t), "0.2.4", time.Now())
	if out, err := b.install("FAKE_STOPPED=1"); err == nil {
		t.Fatalf("a stopped box is a failed run:\n%s", out)
	}
	if st := b.status(); !hasLine(st, "reason=box_not_running") {
		t.Errorf("status:\n%s", st)
	}
}

func TestWithoutTheSignatureCheckerAnUploadedFileIsNotInstalled(t *testing.T) {
	b := installed(t)
	b.withoutCosignImage()
	b.handOver(fileSpec{}.bytes(t), "0.2.4", time.Now())
	b.mustInstall()
	if st := b.status(); !hasLine(st, "state=failed") || !hasLine(st, "reason=verifier_missing") {
		t.Errorf("status:\n%s", st)
	}
	if strings.Contains(b.calls(), "docker load") {
		t.Error("an unchecked file was loaded")
	}
}

func TestTheNightlyRunRecordsTheRunningVersionWhenOfflineAndKeepsTheReport(t *testing.T) {
	b := installed(t)
	b.write("opt/hejmo/data/update-settings", "mode=on\noffline=yes\n")
	b.write("opt/hejmo/data/update-status", "state=updated\nversion=0.0.9\ntime=2026-11-15T03:30:00Z\nchecked=2026-11-16T03:30:00Z\n")
	b.mustUpdate()
	st := b.status()
	for _, want := range []string{"state=updated", "version=0.1.0", "time=2026-11-15T03:30:00Z", "checked=2026-11-16T03:30:00Z"} {
		if !hasLine(st, want) {
			t.Errorf("status lacks %q:\n%s", want, st)
		}
	}
	if hasLine(st, "version=0.0.9") {
		t.Errorf("the old version stays:\n%s", st)
	}
	if strings.Contains(b.calls(), "curl") {
		t.Errorf("the offline run connected:\n%s", b.calls())
	}
}

func TestTheNightlyRunIgnoresAnInstallRequest(t *testing.T) {
	b := installed(t)
	b.handOver(fileSpec{}.bytes(t), "0.2.4", time.Now())
	b.mustUpdate("FAKE_LATEST=0.1.0")
	if strings.Contains(b.calls(), "docker load") || strings.Contains(b.calls(), "compose up") {
		t.Errorf("the nightly run installed an uploaded file:\n%s", b.calls())
	}
	if !b.exists("opt/hejmo/data/update-file") {
		t.Error("the nightly run took the file")
	}
}
