package server_test

import (
	"os"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
)

// The server tests hash passwords and recovery codes hundreds of times, under
// -race and on arm64. They test the flow, not the cost, so they hash with the
// cheapest valid Argon2id parameters. Production keeps the ADR 0010 cost, and
// internal/auth tests that (TestProductionUsesTheADR0010Parameters).
func TestMain(m *testing.M) {
	auth.SetHashCost(auth.HashCost{MemoryKiB: 8, Passes: 1, Threads: 1})
	os.Exit(m.Run())
}
