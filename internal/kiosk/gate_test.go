package kiosk_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/kiosk"
	"github.com/hejmo-home/hejmo/internal/outbound"
)

// The kiosk app runs on the display in the home network; the adapter must not
// be made to talk to anything outside it.
func TestTheKioskAdapterReachesOnlyTheHomeNetwork(t *testing.T) {
	a := kiosk.NewFreeKiosk("http://203.0.113.9:8080", "key")
	err := a.FrontLightOff(context.Background())
	if !errors.Is(err, outbound.ErrNotHome) {
		t.Fatalf("FrontLightOff = %v, want ErrNotHome", err)
	}
}
