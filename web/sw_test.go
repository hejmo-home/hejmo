package web_test

import (
	"os"
	"os/exec"
	"testing"
)

// The service worker is tested with node's test runner (web/test/*.test.js).
// CI sets HEJMO_REQUIRE_NODE so that a missing node fails instead of skipping.
func TestServiceWorkerWithNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("HEJMO_REQUIRE_NODE") != "" {
			t.Fatal("node is required: ", err)
		}
		t.Skip("node is not installed")
	}
	// No path argument: node finds web/test/*.test.js itself. (A directory
	// argument is read as a module by current node versions.)
	cmd := exec.Command(node, "--test")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}
