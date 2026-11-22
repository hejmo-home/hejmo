package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The host-side updater (ADR 0018, ticket 7.12) is exercised as a black box
// like install.sh: the installed script runs in the fake root against a stand-in
// Docker that knows two images, the running one and the newer one, and can be
// told to fail at each step.

const (
	oldDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	newDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	imageRepo = "ghcr.io/hejmo-home/hejmo"
)

const updaterDocker = `#!/bin/sh
echo "docker $*" >> "$STATE/calls"
DATA="$HEJMO_ROOT/opt/hejmo/data"
OLD=1111111111111111111111111111111111111111111111111111111111111111
NEW=2222222222222222222222222222222222222222222222222222222222222222
for a; do last="$a"; done
case "$1" in
compose)
	case "$2" in
	version) echo "Docker Compose version v2.40.0" ;;
	up)
		img=$(sed -n 's/^HEJMO_IMAGE=//p' .env | head -n 1)
		echo "$img" > "$STATE/running"
		case "$img" in
		*"$NEW"*)
			[ "${FAKE_START:-ok}" = fail ] && exit 1
			echo "data migrated by the new version" > "$DATA/hejmo.db" ;;
		esac ;;
	stop) rm -f "$STATE/running" ;;
	esac ;;
inspect)
	case "$*" in
	*Labels*) cat "$STATE/version" ;;
	*State.Running*) if [ "${FAKE_STOPPED:-0}" = 1 ]; then echo false; else echo true; fi ;;
	*.Image*) echo "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" ;;
	esac ;;
image)
	case "$2" in
	inspect)
		case "$last" in
		sha256:*) echo "ghcr.io/hejmo-home/hejmo@sha256:$OLD" ;;
		ghcr.io/hejmo-home/hejmo:*) echo "ghcr.io/hejmo-home/hejmo@sha256:$NEW" ;;
		esac ;;
	esac ;;
pull) [ "${FAKE_PULL:-ok}" = fail ] && exit 1 ;;
run) [ "${FAKE_COSIGN:-ok}" = bad ] && exit 1 ;;
exec)
	[ "${FAKE_BACKUP:-ok}" = fail ] && exit 1
	cp "$DATA/hejmo.db" "$DATA/pre-update.db" ;;
cp)
	cp "$DATA/${2#hejmo:/data/}" "$3" ;;
esac
exit 0
`

const updaterCurl = `#!/bin/sh
echo "curl $*" >> "$STATE/calls"
for a; do last="$a"; done
case "$last" in
*api.github.com*)
	[ "${FAKE_LATEST:-0.2.0}" = none ] && exit 22
	echo "{\"id\": 1, \"tag_name\": \"${FAKE_TAG:-v${FAKE_LATEST:-0.2.0}}\", \"prerelease\": false}" ;;
*healthz)
	[ "${FAKE_HEALTH:-up}" = down ] && exit 22
	if [ "${FAKE_NEW_HEALTH:-up}" = down ] && grep -q 2222 "$STATE/running" 2>/dev/null; then exit 22; fi
	echo '{"status":"ok"}' ;;
esac
exit 0
`

// installed returns a box with the install script already run, version 0.1.0
// running with some data.
func installed(t *testing.T) *box {
	t.Helper()
	b := newBox(t)
	for name, body := range map[string]string{"docker": updaterDocker, "curl": updaterCurl} {
		if err := os.WriteFile(filepath.Join(b.bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b.env = append(b.env, "FAKE_TIMEZONE=none")
	if err := os.WriteFile(filepath.Join(b.state, "version"), []byte("0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := b.run(); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	b.write("opt/hejmo/data/hejmo.db", "the family's data")
	_ = os.Remove(filepath.Join(b.state, "calls"))
	return b
}

func (b *box) write(rel, content string) {
	b.t.Helper()
	p := filepath.Join(b.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		b.t.Fatal(err)
	}
}

func (b *box) read(rel string) string {
	data, _ := os.ReadFile(filepath.Join(b.root, rel))
	return string(data)
}

// update runs the installed updater as the systemd timer would.
func (b *box) update(extra ...string) (string, error) {
	b.t.Helper()
	cmd := exec.Command("bash", filepath.Join(b.root, "opt/hejmo/hejmo-update"))
	cmd.Env = append(append([]string{}, b.env...), extra...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (b *box) status() string { return b.read("opt/hejmo/data/update-status") }

func (b *box) running() string {
	data, _ := os.ReadFile(filepath.Join(b.state, "running"))
	return strings.TrimSpace(string(data))
}

func (b *box) mustUpdate(extra ...string) {
	b.t.Helper()
	if out, err := b.update(extra...); err != nil {
		b.t.Fatalf("update failed: %v\n%s", err, out)
	}
}

func hasLine(status, line string) bool {
	for _, l := range strings.Split(status, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

func TestInstallRegistersANightlyTimerAndTheUpdater(t *testing.T) {
	b := installed(t)
	timer := b.read("etc/systemd/system/hejmo-update.timer")
	for _, want := range []string{"OnCalendar=*-*-* 03:30:00", "RandomizedDelaySec=", "WantedBy=timers.target"} {
		if !strings.Contains(timer, want) {
			t.Errorf("timer lacks %q:\n%s", want, timer)
		}
	}
	svc := b.read("etc/systemd/system/hejmo-update.service")
	for _, want := range []string{"Type=oneshot", "ExecStart=/opt/hejmo/hejmo-update"} {
		if !strings.Contains(svc, want) {
			t.Errorf("service lacks %q:\n%s", want, svc)
		}
	}
	fi, err := os.Stat(filepath.Join(b.root, "opt/hejmo/hejmo-update"))
	if err != nil || fi.Mode().Perm()&0o111 == 0 || fi.Mode().Perm()&0o022 != 0 {
		t.Errorf("the updater must be executable and writable by nobody else: %v %v", fi, err)
	}
	if !strings.Contains(b.read("opt/hejmo/compose.yaml"), "image:") || strings.Contains(b.read("opt/hejmo/compose.yaml"), "docker.sock") {
		t.Error("the container must get no access to the Docker socket")
	}
	if !strings.Contains(b.read("opt/hejmo/compose.yaml"), "HEJMO_HOST_UPDATER") {
		t.Error("the box is not told that a host updater manages it")
	}
	// The updater changes only by re-running the install script (ADR 0018).
	if strings.Contains(b.read("opt/hejmo/hejmo-update"), "raw.githubusercontent.com") {
		t.Error("the updater must not fetch or replace itself")
	}
}

func TestSecondInstallRunLeavesTheUpdaterUnitsAlone(t *testing.T) {
	b := installed(t)
	files := []string{"opt/hejmo/hejmo-update", "etc/systemd/system/hejmo-update.service", "etc/systemd/system/hejmo-update.timer"}
	before := map[string]os.FileInfo{}
	for _, f := range files {
		fi, err := os.Stat(filepath.Join(b.root, f))
		if err != nil {
			t.Fatal(err)
		}
		before[f] = fi
	}
	_ = os.Remove(filepath.Join(b.state, "calls"))
	if out, err := b.run(); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	for _, f := range files {
		if fi, _ := os.Stat(filepath.Join(b.root, f)); !fi.ModTime().Equal(before[f].ModTime()) {
			t.Errorf("%s was rewritten", f)
		}
	}
	if calls := b.calls(); strings.Contains(calls, "systemctl enable") || strings.Contains(calls, "daemon-reload") {
		t.Errorf("second run re-registered units:\n%s", calls)
	}
}

func TestInstallEnablesTheTimer(t *testing.T) {
	b := newBox(t)
	b.withDocker()
	if out, err := b.run(); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if calls := b.calls(); !strings.Contains(calls, "systemctl enable --now hejmo-update.timer") {
		t.Errorf("the timer is not enabled:\n%s", calls)
	}
}

// Acceptance: given a newer signed release at 03:30, then it is installed and the box is reachable afterwards.
func TestInstallsANewerSignedRelease(t *testing.T) {
	b := installed(t)
	b.mustUpdate()
	if got := b.running(); got != imageRepo+"@"+newDigest {
		t.Fatalf("running image = %q, want the new one by digest", got)
	}
	st := b.status()
	for _, want := range []string{"state=updated", "version=0.2.0"} {
		if !hasLine(st, want) {
			t.Errorf("status lacks %q:\n%s", want, st)
		}
	}
	calls := b.calls()
	// Verified with the project's keyless identity, before anything was started.
	verify := strings.Index(calls, "verify")
	up := strings.Index(calls, "compose up")
	if verify < 0 || up < 0 || verify > up {
		t.Fatalf("signature not verified before starting:\n%s", calls)
	}
	for _, want := range []string{
		imageRepo + "@" + newDigest,
		"--certificate-identity https://github.com/hejmo-home/hejmo/.github/workflows/release.yml@refs/tags/v0.2.0",
		"--certificate-oidc-issuer https://token.actions.githubusercontent.com",
		"exec hejmo /hejmo backup",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing %q in calls:\n%s", want, calls)
		}
	}
	if strings.Contains(b.read("opt/hejmo/.env"), ":latest") {
		t.Error(".env still names the moving tag")
	}
}

// Acceptance: given a release whose start fails, then the previous version
// runs again with its database and parents see the reason.
func TestRollsBackWhenTheNewVersionDoesNotStart(t *testing.T) {
	b := installed(t)
	b.mustUpdate("FAKE_START=fail")
	assertRolledBack(t, b, "start_failed")
}

func TestRollsBackWhenTheNewVersionDoesNotAnswerHealthz(t *testing.T) {
	b := installed(t)
	b.mustUpdate("FAKE_NEW_HEALTH=down")
	assertRolledBack(t, b, "health_check_failed")
}

func assertRolledBack(t *testing.T, b *box, reason string) {
	t.Helper()
	if got := b.running(); got != imageRepo+"@"+oldDigest {
		t.Errorf("running image = %q, want the previous one by digest", got)
	}
	if got := b.read("opt/hejmo/data/hejmo.db"); got != "the family's data" {
		t.Errorf("database = %q, want the backup restored", got)
	}
	st := b.status()
	for _, want := range []string{"state=rolled_back", "version=0.1.0", "target=0.2.0", "reason=" + reason} {
		if !hasLine(st, want) {
			t.Errorf("status lacks %q:\n%s", want, st)
		}
	}
	// The same broken release is not tried again every night.
	_ = os.Remove(filepath.Join(b.state, "calls"))
	if out, err := b.update("FAKE_START=fail", "FAKE_NEW_HEALTH=down"); err != nil {
		t.Fatalf("second night: %v\n%s", err, out)
	}
	if strings.Contains(b.calls(), "pull") {
		t.Error("the failed release was pulled again")
	}
}

// Acceptance: given an unsigned or wrongly signed image, then it is not installed.
func TestDoesNotInstallAnImageWithoutAValidSignature(t *testing.T) {
	b := installed(t)
	b.mustUpdate("FAKE_COSIGN=bad")
	if got := b.running(); got != imageRepo+":latest" {
		t.Errorf("running image changed to %q", got)
	}
	if strings.Contains(b.read("opt/hejmo/.env"), newDigest) {
		t.Error(".env names the rejected image")
	}
	if got := b.read("opt/hejmo/data/hejmo.db"); got != "the family's data" {
		t.Errorf("database touched: %q", got)
	}
	st := b.status()
	for _, want := range []string{"state=rejected", "target=0.2.0", "reason=signature_invalid"} {
		if !hasLine(st, want) {
			t.Errorf("status lacks %q:\n%s", want, st)
		}
	}
	calls := b.calls()
	if strings.Contains(calls, "compose up") || strings.Contains(calls, "backup") {
		t.Errorf("went on after a failed verification:\n%s", calls)
	}
}

// Acceptance: given auto-update set to notify only, then nothing is installed
// and parents see that an update is available.
func TestNotifyOnlyInstallsNothingAndReportsTheUpdate(t *testing.T) {
	b := installed(t)
	b.write("opt/hejmo/data/update-settings", "mode=notify\n")
	b.mustUpdate()
	calls := b.calls()
	for _, bad := range []string{"pull", "compose up", "backup", "verify"} {
		if strings.Contains(calls, bad) {
			t.Errorf("notify only called %q:\n%s", bad, calls)
		}
	}
	st := b.status()
	for _, want := range []string{"state=available", "version=0.1.0", "target=0.2.0"} {
		if !hasLine(st, want) {
			t.Errorf("status lacks %q:\n%s", want, st)
		}
	}
}

func TestOffDoesNothingAtAll(t *testing.T) {
	b := installed(t)
	b.write("opt/hejmo/data/update-settings", "mode=off\n")
	b.mustUpdate()
	if calls := b.calls(); strings.Contains(calls, "pull") || strings.Contains(calls, "api.github.com") {
		t.Errorf("off still looked for updates:\n%s", calls)
	}
	if b.status() != "" {
		t.Errorf("off wrote a status: %q", b.status())
	}
}

func TestDoesNothingWhenAlreadyUpToDate(t *testing.T) {
	b := installed(t)
	b.write("opt/hejmo/data/update-status", "state=updated\nversion=0.1.0\ntime=2026-11-15T03:30:00Z\n")
	b.mustUpdate("FAKE_LATEST=0.1.0")
	if strings.Contains(b.calls(), "pull") {
		t.Error("pulled although up to date")
	}
	if !strings.Contains(b.status(), "state=updated") {
		t.Errorf("the report parents have not seen yet was overwritten: %q", b.status())
	}
}

func TestOnlyPublishedVersionsAreInstalled(t *testing.T) {
	b := installed(t)
	b.mustUpdate("FAKE_TAG=v0.2.0-rc.1")
	if strings.Contains(b.calls(), "pull") {
		t.Error("a pre-release was pulled")
	}
	b.mustUpdate("FAKE_TAG=latest; rm -rf /")
	if strings.Contains(b.calls(), "pull") {
		t.Error("a malformed tag was pulled")
	}
}

func TestOfflineNightChangesNothing(t *testing.T) {
	b := installed(t)
	b.mustUpdate("FAKE_LATEST=none")
	if strings.Contains(b.calls(), "pull") || b.status() != "" {
		t.Errorf("an unreachable release feed must not change anything: %q", b.status())
	}
}

func TestStopsBeforeSwitchingWhenTheBackupFails(t *testing.T) {
	b := installed(t)
	_, _ = b.update("FAKE_BACKUP=fail") // exits 1 so that systemd shows the failed run
	if got := b.running(); got != imageRepo+":latest" {
		t.Errorf("switched to %q without a backup", got)
	}
	st := b.status()
	if !hasLine(st, "state=failed") || !hasLine(st, "reason=backup_failed") {
		t.Errorf("status:\n%s", st)
	}
}

func TestReportsAFailedDownload(t *testing.T) {
	b := installed(t)
	_, _ = b.update("FAKE_PULL=fail")
	st := b.status()
	if !hasLine(st, "state=failed") || !hasLine(st, "reason=pull_failed") {
		t.Errorf("status:\n%s", st)
	}
}

// ADR 0018: the script accepts nothing but the fixed values from the file and
// never runs or interpolates its content.
func TestHostileSettingsFileInstallsNothingAndRunsNothing(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pwned")
	cases := map[string]string{
		"command":   "mode=on; touch " + marker + "\n",
		"subshell":  "mode=$(touch " + marker + ")\n",
		"backticks": "mode=`touch " + marker + "`\n",
		"source":    "touch " + marker + "\nmode=garbage\n",
		"unknown":   "mode=sometimes\n",
		"empty":     "",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			b := installed(t)
			b.write("opt/hejmo/data/update-settings", content)
			b.mustUpdate()
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("the settings file's content was executed")
			}
			if calls := b.calls(); strings.Contains(calls, "pull") || strings.Contains(calls, "compose up") {
				t.Errorf("a doubtful settings file still installed an update:\n%s", calls)
			}
		})
	}
}

func TestSettingsFileThatIsALinkIsNotFollowed(t *testing.T) {
	b := installed(t)
	secret := filepath.Join(t.TempDir(), "mode")
	if err := os.WriteFile(secret, []byte("mode=on\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(b.root, "opt/hejmo/data/update-settings")); err != nil {
		t.Fatal(err)
	}
	b.mustUpdate()
	if strings.Contains(b.calls(), "pull") {
		t.Error("followed a link out of the data volume")
	}
}

// The status file is replaced, never written through a link the container planted.
func TestStatusIsNotWrittenThroughALink(t *testing.T) {
	b := installed(t)
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(b.root, "opt/hejmo/data/update-status")); err != nil {
		t.Fatal(err)
	}
	b.mustUpdate()
	if data, _ := os.ReadFile(target); string(data) != "untouched" {
		t.Fatalf("the script wrote through the link: %q", data)
	}
	if !strings.Contains(b.status(), "state=updated") {
		t.Errorf("status not written: %q", b.status())
	}
}

func TestStatusFileHasOnlyFixedKeys(t *testing.T) {
	b := installed(t)
	b.mustUpdate("FAKE_START=fail")
	allowed := map[string]bool{"state": true, "version": true, "target": true, "reason": true, "time": true, "checked": true}
	for _, l := range strings.Split(strings.TrimSpace(b.status()), "\n") {
		k, _, ok := strings.Cut(l, "=")
		if !ok || !allowed[k] {
			t.Errorf("unexpected line %q", l)
		}
	}
}

// The identity the updater demands is the one the release workflow signs with
// and verifies itself.
func TestUpdaterTrustsTheReleaseWorkflowsIdentity(t *testing.T) {
	script, release := read(t, "install.sh"), read(t, ".github/workflows/release.yml")
	for _, want := range []string{
		"https://github.com/hejmo-home/hejmo/.github/workflows/release.yml@refs/tags/",
		"https://token.actions.githubusercontent.com",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install.sh lacks %q", want)
		}
	}
	for _, want := range []string{"/.github/workflows/release.yml@refs/tags/", "https://token.actions.githubusercontent.com", "cosign sign"} {
		if !strings.Contains(release, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}
	if !strings.Contains(script, "ghcr.io/sigstore/cosign/cosign:v") || !strings.Contains(script, "@sha256:") {
		t.Error("the cosign image must be pinned by digest")
	}
}

// A preview build has no version label. Installing the latest release over it
// could be a downgrade, with a database the older binary cannot read.
func TestUnknownRunningVersionInstallsNothing(t *testing.T) {
	b := installed(t)
	if err := os.WriteFile(filepath.Join(b.state, "version"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"on", "notify"} {
		b.write("opt/hejmo/data/update-settings", "mode="+mode+"\n")
		b.mustUpdate()
		calls := b.calls()
		if strings.Contains(calls, "pull") || strings.Contains(calls, "compose up") {
			t.Errorf("mode %s: installed over an unknown version:\n%s", mode, calls)
		}
		if got := b.running(); got != imageRepo+":latest" {
			t.Errorf("mode %s: running image changed to %q", mode, got)
		}
		if strings.Contains(b.status(), "state=updated") {
			t.Errorf("mode %s: reported an update: %q", mode, b.status())
		}
	}
}

// A stopped container still has its image and version label, like on a real
// host, so the check must look at whether it runs.
func TestStoppedBoxIsNotReportedAsABackupFailure(t *testing.T) {
	b := installed(t)
	_, _ = b.update("FAKE_STOPPED=1")
	st := b.status()
	if hasLine(st, "reason=backup_failed") || !hasLine(st, "reason=box_not_running") {
		t.Errorf("status:\n%s", st)
	}
}

func TestFailedRestoreIsReportedAsFailedRollback(t *testing.T) {
	b := installed(t)
	script := "#!/bin/sh\n[ \"$FAKE_RESTORE\" = fail ] && exit 1\nexit 0\n"
	if err := os.WriteFile(filepath.Join(b.bin, "chown"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _ = b.update("FAKE_START=fail", "FAKE_RESTORE=fail")
	st := b.status()
	if hasLine(st, "state=rolled_back") || !hasLine(st, "state=failed") || !hasLine(st, "reason=rollback_failed") {
		t.Errorf("status:\n%s", st)
	}
}

// An "available" report that the box cannot dismiss must not outlive the
// reason for it, for example when the box was updated another way.
func TestOutdatedAvailableReportBecomesIdle(t *testing.T) {
	b := installed(t)
	b.write("opt/hejmo/data/update-status", "state=available\nversion=0.1.0\ntarget=0.2.0\ntime=2026-11-15T03:30:00Z\n")
	b.mustUpdate("FAKE_LATEST=0.1.0")
	if !hasLine(b.status(), "state=idle") {
		t.Errorf("status:\n%s", b.status())
	}
}

// Ticket 201 (Q145): offline mode stops the host script too. The box writes
// offline=yes into the settings file and the script goes nowhere: no check for
// a new version, no download.
func TestOfflineKeyStopsTheScriptBeforeAnyConnection(t *testing.T) {
	for _, mode := range []string{"on", "notify"} {
		b := installed(t)
		b.write("opt/hejmo/data/update-settings", "mode="+mode+"\noffline=yes\n")
		b.mustUpdate()
		calls := b.calls()
		if strings.Contains(calls, "curl") || strings.Contains(calls, "pull") || strings.Contains(calls, "api.github.com") {
			t.Errorf("mode %s: the script connected although offline=yes:\n%s", mode, calls)
		}
		if b.status() != "" {
			t.Errorf("mode %s: wrote a status: %q", mode, b.status())
		}
	}
}

func TestOnlyTheExactOfflineKeyCounts(t *testing.T) {
	for _, line := range []string{"offline=no", "offline=1", "offline=yes please", "offline = yes", "#offline=yes"} {
		b := installed(t)
		b.write("opt/hejmo/data/update-settings", "mode=on\n"+line+"\n")
		b.mustUpdate("FAKE_LATEST=0.1.0")
		if !strings.Contains(b.calls(), "api.github.com") {
			t.Errorf("%q kept the script from looking for updates", line)
		}
	}
}

func TestTheScriptRecordsWhenItLastLookedForAnUpdate(t *testing.T) {
	b := installed(t)
	b.write("opt/hejmo/data/update-status", "state=updated\nversion=0.1.0\ntime=2026-11-15T03:30:00Z\n")
	b.mustUpdate("FAKE_LATEST=0.1.0")
	st := b.status()
	if !hasLine(st, "state=updated") || !hasLine(st, "version=0.1.0") || !hasLine(st, "time=2026-11-15T03:30:00Z") {
		t.Errorf("the report parents have not seen yet was changed:\n%s", st)
	}
	if !regexp.MustCompile(`(?m)^checked=\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`).MatchString(st) {
		t.Errorf("no checked= line:\n%s", st)
	}
	// An update report carries it too.
	b = installed(t)
	b.mustUpdate()
	if st := b.status(); !hasLine(st, "state=updated") || !strings.Contains(st, "checked=") {
		t.Errorf("status after an update:\n%s", st)
	}
}
