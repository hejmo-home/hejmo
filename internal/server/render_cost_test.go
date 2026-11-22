package server_test

import (
	"runtime"
	"testing"
)

// Ticket 238: rendering a page must not copy the whole template set. A copy of
// the 80-odd templates per request cost several MB each, which under -race made
// the server package grow to 10 GB. Not parallel: it reads the process-wide
// allocation counter, and parallel tests only run after the serial ones.
func TestRenderingAPageDoesNotCopyTheTemplateSet(t *testing.T) {
	b := newBox(t)
	b.get("/setup") // the first render of a language may build what later ones reuse
	const renders = 20
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < renders; i++ {
		if res := b.get("/setup"); res.status != 200 {
			t.Fatalf("GET /setup = %d", res.status)
		}
	}
	runtime.ReadMemStats(&after)
	perRender := (after.TotalAlloc - before.TotalAlloc) / renders
	if limit := uint64(512 << 10); perRender > limit {
		t.Fatalf("a render allocated %d KiB, want under %d KiB", perRender>>10, limit>>10)
	}
}
