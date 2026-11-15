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
	out, err := exec.Command(node, "--test", "test/").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test failed: %v\n%s", err, out)
	}
}
