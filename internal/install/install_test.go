package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// box is a fake machine: a root directory the script writes into, stand-in
// commands first on PATH, and a log of every stand-in call.
type box struct {
	t     *testing.T
	root  string
	bin   string // stand-ins
	state string // stand-in state and the call log
	env   []string
}

// shims are the stand-in commands; each one's call is logged first.
var shims = map[string]string{
	"id":       `[ "$1" = "-u" ] && echo "${FAKE_UID:-0}"; exit 0`,
	"uname":    `[ "$1" = "-m" ] && echo "${FAKE_ARCH:-aarch64}"; exit 0`,
	"hostname": `echo 192.168.1.50`,
	"chown":    `exit 0`,
	"dpkg":     `echo "${FAKE_DPKG_ARCH:-arm64}"`,
	"sleep":    `exit 0`,
	"curl": `out=""
while [ $# -gt 0 ]; do
	case "$1" in -o) out="$2"; shift ;; *healthz) [ "${FAKE_HEALTHZ:-up}" = down ] && exit 22; echo '{"status":"ok"}'; exit 0 ;; esac
	shift
done
[ -n "$out" ] && echo "fake key" > "$out"
exit 0`,
	"apt-get": `case "$*" in *docker-ce*) cp "$STATE/docker.shim" "$BIN/docker"; chmod +x "$BIN/docker" ;; esac
exit 0`,
	"systemctl": `for unit; do :; done
case "$1" in
is-enabled) [ -e "$STATE/enabled-$unit" ] || exit 1 ;;
enable) touch "$STATE/enabled-$unit" ;;
is-active) [ -e "$STATE/started" ] || exit 1 ;;
start) touch "$STATE/started" ;;
esac
exit 0`,
}

const dockerShim = `#!/bin/sh
echo "docker $*" >> "$STATE/calls"
case "$1 $2" in
"compose version") echo "Docker Compose version v2.40.0" ;;
esac
exit 0
`

func newBox(t *testing.T) *box {
	t.Helper()
	b := &box{t: t, root: t.TempDir(), bin: t.TempDir(), state: t.TempDir()}
	tools := t.TempDir()
	for _, n := range []string{"bash", "env", "sh", "mkdir", "cat", "cmp", "mv", "rm", "cp", "chmod", "sed", "awk", "grep", "date", "tr", "dirname", "mktemp", "install", "tee", "head", "cut", "touch", "printf", "ls", "test", "ln", "wc", "tar", "sort", "sha256sum", "shasum"} {
		if p, err := exec.LookPath(n); err == nil {
			_ = os.Symlink(p, filepath.Join(tools, n))
		}
	}
	for n, body := range shims {
		script := "#!/bin/sh\necho \"" + n + " $*\" >> \"$STATE/calls\"\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(b.bin, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(b.state, "docker.shim"), []byte(dockerShim), 0o755); err != nil {
		t.Fatal(err)
	}
	// Raspberry Pi OS 64-bit is Debian; /etc/rpi-issue marks it.
	if err := os.MkdirAll(filepath.Join(b.root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	osr := "ID=debian\nVERSION_CODENAME=bookworm\n"
	if err := os.WriteFile(filepath.Join(b.root, "etc/os-release"), []byte(osr), 0o644); err != nil {
		t.Fatal(err)
	}
	b.env = []string{"PATH=" + b.bin + ":" + tools, "HOME=" + t.TempDir(), "HEJMO_ROOT=" + b.root, "STATE=" + b.state, "BIN=" + b.bin}
	return b
}

func (b *box) withDocker() {
	b.t.Helper()
	data, _ := os.ReadFile(filepath.Join(b.state, "docker.shim"))
	if err := os.WriteFile(filepath.Join(b.bin, "docker"), data, 0o755); err != nil {
		b.t.Fatal(err)
	}
}

func (b *box) run(extra ...string) (string, error) {
	b.t.Helper()
	script, _ := filepath.Abs("../../install.sh")
	cmd := exec.Command("bash", script)
	cmd.Env = append(append([]string{}, b.env...), extra...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (b *box) calls() string {
	data, _ := os.ReadFile(filepath.Join(b.state, "calls"))
	return string(data)
}

func (b *box) file(rel string) string {
	b.t.Helper()
	data, err := os.ReadFile(filepath.Join(b.root, rel))
	if err != nil {
		b.t.Fatalf("expected %s: %v", rel, err)
	}
	return string(data)
}

func TestRefusesA32BitSystem(t *testing.T) {
	b := newBox(t)
	b.withDocker()
	out, err := b.run("FAKE_ARCH=armv7l")
	if err == nil {
		t.Fatal("install on 32-bit succeeded, want failure")
	}
	if !strings.Contains(out, "64-bit") {
		t.Errorf("message should name the 64-bit requirement, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(b.root, "opt/hejmo")); err == nil {
		t.Error("a refused install must not create files")
	}
}

func TestRefusesWithoutRoot(t *testing.T) {
	b := newBox(t)
	b.withDocker()
	out, err := b.run("FAKE_UID=1000")
	if err == nil || !strings.Contains(out, "root") {
		t.Fatalf("want a refusal naming root, got err=%v out=%s", err, out)
	}
}

func TestFreshInstallWritesBoxAndStartsIt(t *testing.T) {
	b := newBox(t)
	b.withDocker()
	out, err := b.run()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	compose := b.file("opt/hejmo/compose.yaml")
	for _, want := range []string{"ghcr.io/hejmo-home/hejmo", "/opt/hejmo/data:/data", "restart: unless-stopped", "HEJMO_HTTP_PORT"} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose.yaml lacks %q:\n%s", want, compose)
		}
	}
	unit := b.file("etc/systemd/system/hejmo.service")
	for _, want := range []string{"Requires=docker.service", "WorkingDirectory=/opt/hejmo", "docker compose up -d", "WantedBy=multi-user.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("hejmo.service lacks %q:\n%s", want, unit)
		}
	}
	if fi, err := os.Stat(filepath.Join(b.root, "opt/hejmo/data")); err != nil || !fi.IsDir() {
		t.Errorf("data directory missing: %v", err)
	}
	calls := b.calls()
	for _, want := range []string{"systemctl daemon-reload", "systemctl enable hejmo.service", "docker compose up -d", "healthz"} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing call %q in:\n%s", want, calls)
		}
	}
	if !strings.Contains(out, "http://192.168.1.50") {
		t.Errorf("output should point to the setup wizard on the box's address, got:\n%s", out)
	}
}

func TestSecondRunChangesNothing(t *testing.T) {
	b := newBox(t)
	b.withDocker()
	if out, err := b.run(); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	files := []string{"opt/hejmo/compose.yaml", "opt/hejmo/.env", "etc/systemd/system/hejmo.service"}
	before := map[string]os.FileInfo{}
	content := map[string]string{}
	for _, f := range files {
		fi, err := os.Stat(filepath.Join(b.root, f))
		if err != nil {
			t.Fatal(err)
		}
		before[f], content[f] = fi, b.file(f)
	}
	_ = os.Remove(filepath.Join(b.state, "calls"))
	if out, err := b.run(); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	for _, f := range files {
		fi, _ := os.Stat(filepath.Join(b.root, f))
		if !fi.ModTime().Equal(before[f].ModTime()) || b.file(f) != content[f] {
			t.Errorf("%s changed on the second run", f)
		}
	}
	calls := b.calls()
	for _, bad := range []string{"daemon-reload", "systemctl enable", "apt-get"} {
		if strings.Contains(calls, bad) {
			t.Errorf("second run must not call %q:\n%s", bad, calls)
		}
	}
}

func TestSecondRunKeepsEditsToTheEnvFile(t *testing.T) {
	b := newBox(t)
	b.withDocker()
	if out, err := b.run(); err != nil {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	env := filepath.Join(b.root, "opt/hejmo/.env")
	if err := os.WriteFile(env, []byte("HEJMO_HTTP_PORT=8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := b.run(); err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if got := b.file("opt/hejmo/.env"); got != "HEJMO_HTTP_PORT=8080\n" {
		t.Errorf(".env was overwritten: %q", got)
	}
}

func TestInstallsDockerWhenMissing(t *testing.T) {
	b := newBox(t)
	out, err := b.run()
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	calls := b.calls()
	if !strings.Contains(calls, "apt-get install") || !strings.Contains(calls, "docker-ce") || !strings.Contains(calls, "docker-compose-plugin") {
		t.Errorf("Docker was not installed from apt:\n%s", calls)
	}
	list := b.file("etc/apt/sources.list.d/docker.list")
	if !strings.Contains(list, "download.docker.com/linux/debian") || !strings.Contains(list, "bookworm") || !strings.Contains(list, "arm64") {
		t.Errorf("docker.list wrong:\n%s", list)
	}
}

func TestVersionAndImageOverrides(t *testing.T) {
	b := newBox(t)
	b.withDocker()
	if out, err := b.run("HEJMO_VERSION=0.1.0"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if env := b.file("opt/hejmo/.env"); !strings.Contains(env, "HEJMO_IMAGE=ghcr.io/hejmo-home/hejmo:0.1.0") {
		t.Errorf(".env lacks the pinned image:\n%s", env)
	}
}

func TestRefuses32BitUserlandOnA64BitKernelEvenWithDocker(t *testing.T) {
	for name, extra := range map[string][]string{
		"dpkg armhf": {"FAKE_DPKG_ARCH=armhf"},
		"raspbian":   {"FAKE_OS_ID=raspbian"},
	} {
		t.Run(name, func(t *testing.T) {
			b := newBox(t)
			b.withDocker()
			if name == "raspbian" {
				osr := "ID=raspbian\nVERSION_CODENAME=bookworm\n"
				if err := os.WriteFile(filepath.Join(b.root, "etc/os-release"), []byte(osr), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, err := b.run(extra...)
			if err == nil {
				t.Fatal("install on 32-bit userland succeeded, want failure")
			}
			if !strings.Contains(out, "64-bit") {
				t.Errorf("message should name the 64-bit requirement, got:\n%s", out)
			}
			if _, err := os.Stat(filepath.Join(b.root, "opt/hejmo")); err == nil {
				t.Error("a refused install must not create files")
			}
		})
	}
}

func TestFailsWithMessageWhenTheBoxDoesNotAnswer(t *testing.T) {
	b := newBox(t)
	b.withDocker()
	out, err := b.run("FAKE_HEALTHZ=down")
	if err == nil {
		t.Fatal("install succeeded although the box never answered, want failure")
	}
	if !strings.Contains(out, "did not answer") {
		t.Errorf("want the 'did not answer' message, got:\n%s", out)
	}
	if strings.Contains(out, "Hejmo is running") {
		t.Errorf("must not claim success, got:\n%s", out)
	}
}
