package web

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// With HEJMO_REQUIRE_NODE=1 a missing node fails TestServiceWorkerWithNode
// instead of skipping it, so CI cannot pass without running the JavaScript
// tests. The test runs it in a child process with an empty PATH (no node).
func TestMissingNodeFailsWhenNodeIsRequired(t *testing.T) {
	run := func(require string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestServiceWorkerWithNode$", "-test.v")
		cmd.Env = []string{"PATH=", "HEJMO_REQUIRE_NODE=" + require}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("1")
	if err == nil || !strings.Contains(out, "node is required") {
		t.Errorf("with HEJMO_REQUIRE_NODE=1: err = %v, want a failure saying node is required; output:\n%s", err, out)
	}
	out, err = run("")
	if err != nil || !strings.Contains(out, "SKIP") {
		t.Errorf("without HEJMO_REQUIRE_NODE: err = %v, want a skip; output:\n%s", err, out)
	}
}
