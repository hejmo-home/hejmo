package caldavtest

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// With HEJMO_REQUIRE_RADICALE=1 a missing container runtime fails the test
// instead of skipping it, so CI cannot pass without running Radicale. The test
// runs Start in a child process with an empty PATH (no docker).
func TestMissingDockerFailsWhenRadicaleIsRequired(t *testing.T) {
	if os.Getenv("HEJMO_CHILD") == "1" {
		Start(t)
		return
	}
	run := func(require string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMissingDockerFailsWhenRadicaleIsRequired$", "-test.v")
		cmd.Env = []string{"HEJMO_CHILD=1", "PATH=", RequireEnv + "=" + require}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("1")
	if err == nil || !strings.Contains(out, "must not be skipped") {
		t.Errorf("with %s=1: err = %v, want a failure; output:\n%s", RequireEnv, err, out)
	}
	out, err = run("")
	if err != nil || !strings.Contains(out, "SKIP") {
		t.Errorf("without %s: err = %v, want a skip; output:\n%s", RequireEnv, err, out)
	}
}
