package publiccert

import (
	"context"
	"errors"
	"net/http"

	"github.com/hejmo-home/hejmo/internal/outbound"
)

// RenewNow renews the certificate now, whether or not it is due, the way
// CertMagic's background maintenance does. It records nothing itself: the
// outcome reaches Status only through onEvent, as in production.
func (m *Manager) RenewNow(ctx context.Context) error {
	m.mu.RLock()
	cfg, name := m.cfg, m.name
	m.mu.RUnlock()
	if cfg == nil {
		return errors.New("publiccert: HTTPS is not set up")
	}
	return cfg.RenewCertSync(ctx, name, true)
}

// HetznerBaseURL is where the Hetzner provider talks to by default.
const HetznerBaseURL = hetznerBaseURL

// DefaultProviders is the factory the box uses.
var DefaultProviders = defaultProviders

// NewHetzner, NewCloudflare and NewINWX build a provider against a fake API.
func NewHetzner(c Credentials, baseURL string) DNSProvider { return newHetzner(c, baseURL) }
func NewCloudflare(c Credentials, client *http.Client) DNSProvider {
	return newCloudflare(c, client)
}
func NewINWX(c Credentials, endpoint string) DNSProvider { return newINWX(c, endpoint) }

// GatedProvider wraps a provider so that every call passes the gate.
func GatedProvider(g *outbound.Gate, name string, p DNSProvider) DNSProvider {
	return gatedProvider(g, name, p)
}
