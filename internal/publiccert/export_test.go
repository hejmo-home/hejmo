package publiccert

import (
	"context"
	"errors"
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
