package caldavclient_test

import (
	"net/http"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
)

// openClient is a client that connects to the test server (an open gate; the
// box's own code never has one).
func openClient() *http.Client {
	return outbound.AllowAll().Client(outbound.Calendars, 30*time.Second)
}
