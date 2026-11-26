package server_test

import (
	"os"
	"runtime/debug"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
)

const serverTestGCPercent = 25

// The server tests hash passwords and recovery codes hundreds of times, under
// -race and on arm64. They test the flow, not the cost, so they hash with the
// cheapest valid Argon2id parameters. Production keeps the ADR 0010 cost, and
// internal/auth tests that (TestProductionUsesTheADR0010Parameters).
//
// The collector also runs tight (serverTestGCPercent): under -race the whole
// package peaked above 2 GB resident with about 50 MB of live data, because the
// heap grew to twice its live size between two runs and the race shadow follows
// the highest the heap ever was (ticket 238).
func TestMain(m *testing.M) {
	debug.SetGCPercent(serverTestGCPercent)
	auth.SetHashCost(auth.HashCost{MemoryKiB: 8, Passes: 1, Threads: 1})
	os.Exit(m.Run())
}
