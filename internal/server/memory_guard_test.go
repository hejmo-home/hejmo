package server_test

import (
	"runtime/debug"
	"testing"
)

// Under -race every byte the heap ever grew to costs about three more of race
// shadow, so the peak of the whole package follows the garbage the collector
// lets pile up between two runs, not the live data (ticket 238: about 50 MB live,
// 370 MB of heap, 1.3 GB resident in one test). TestMain keeps the collector tight.
func TestTheServerTestsKeepTheCollectorTight(t *testing.T) {
	// SetGCPercent returns the setting it replaces; put it back for the other tests.
	got := debug.SetGCPercent(100)
	debug.SetGCPercent(got)
	if got > serverTestGCPercent {
		t.Errorf("GC percent of the server tests = %d, want at most %d (see TestMain)", got, serverTestGCPercent)
	}
}

// The big test photos are drawn and encoded once per size, not once per test:
// a 3024 x 4032 picture is 48 MB of pixels each time.
func TestATestPhotoOfOneSizeIsDrawnOnce(t *testing.T) {
	a := testJPEG(t, 1200, 900)
	b := testJPEG(t, 1200, 900)
	if len(a) == 0 || string(a) != string(b) {
		t.Fatal("two photos of one size differ")
	}
	a[0] ^= 0xff // a test that changes its photo must not change the next test's
	if c := testJPEG(t, 1200, 900); c[0] == a[0] {
		t.Error("the photo is shared between tests: a change in one shows in the next")
	}
	if n := jpegDrawn(1200, 900); n != 1 {
		t.Errorf("a photo of one size was drawn %d times, want 1", n)
	}
}
