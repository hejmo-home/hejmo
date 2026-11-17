package server

import (
	"github.com/hejmo-home/hejmo/internal/store"
)

// kioskState is what the runner knows about the display's kiosk app; false
// without a kiosk app, without a runner, or before the first check.
func (s *server) kioskState(d store.Display) (KioskState, bool) {
	if s.kiosk == nil || d.KioskURL == "" {
		return KioskState{}, false
	}
	return s.kiosk.State(d.ID)
}
