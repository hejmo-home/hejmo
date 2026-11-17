// Package kiosk is the box's kiosk adapter (ADR 0005): its connection to the
// kiosk app on a display, which does what a web page cannot, such as switching
// the front light. V1.0 ships one adapter, FreeKiosk through its REST API.
package kiosk

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
)

// Adapter controls one display's kiosk app.
type Adapter interface {
	// FrontLightOff switches the front light off. Smooth dimming is not part of
	// V1.0: it probably does not work on the reference tablets (ADR 0005).
	FrontLightOff(ctx context.Context) error
}

// Factory builds the adapter for a display's kiosk address and API key. The
// server takes one as an option, so tests use a fake.
type Factory func(baseURL, apiKey string) Adapter

// FreeKiosk is the adapter for FreeKiosk (MIT), see its REST API: the API key
// goes in the X-Api-Key header and POST /api/brightness sets 0 to 100.
type FreeKiosk struct {
	base   string
	key    string
	client *http.Client
}

// NewFreeKiosk returns the FreeKiosk adapter for the app at baseURL, for
// example "http://192.168.1.20:8080".
func NewFreeKiosk(baseURL, apiKey string) Adapter {
	return &FreeKiosk{base: strings.TrimRight(baseURL, "/"), key: apiKey, client: outbound.Local(10 * time.Second)}
}

// ValidURL reports whether s is an http or https address with a host.
func ValidURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// FrontLightOff sets the brightness to 0.
func (f *FreeKiosk) FrontLightOff(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.base+"/api/brightness", bytes.NewBufferString(`{"value":0}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if f.key != "" {
		req.Header.Set("X-Api-Key", f.key)
	}
	res, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("kiosk: %w", err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("kiosk: brightness refused with status %d", res.StatusCode)
	}
	return nil
}
