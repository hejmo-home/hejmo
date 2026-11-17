// Package kiosk is the box's kiosk adapter (ADR 0005): its connection to the
// kiosk app on a display, which does what a web page cannot, such as switching
// the front light. V1.0 ships one adapter, FreeKiosk through its REST API.
package kiosk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
)

// Status is what the kiosk app reports about its device.
type Status struct {
	// HasLightSensor is whether the device reports an ambient light sensor.
	HasLightSensor bool
	// Brightness is the current brightness in percent; ScreenOn whether the
	// screen is on; AutoBrightness whether the light sensor regulates it.
	Brightness     int
	ScreenOn       bool
	AutoBrightness bool
}

// Adapter controls one display's kiosk app (ADR 0005, 0020).
type Adapter interface {
	// SetBrightness sets the brightness (E-Ink: the front light) in percent, 0
	// to 100. A fixed brightness ends the light sensor's regulation.
	SetBrightness(ctx context.Context, percent int) error
	// EnableAutoBrightness hands the brightness to the device's light sensor.
	EnableAutoBrightness(ctx context.Context) error
	// ScreenOff and ScreenOn switch a normal screen off and on again.
	ScreenOff(ctx context.Context) error
	ScreenOn(ctx context.Context) error
	// Status reads what the device reports; it is also the check that the
	// kiosk app answers.
	Status(ctx context.Context) (Status, error)
}

// Factory builds the adapter for a display's kiosk address and API key. The
// server takes one as an option, so tests use a fake.
type Factory func(baseURL, apiKey string) Adapter

// FreeKiosk is the adapter for FreeKiosk (MIT), see its REST API: the API key
// goes in the X-Api-Key header, POST /api/brightness sets 0 to 100, /api/screen/on and /off switch the screen, GET /api/status reports the sensors.
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

// SetBrightness sets the brightness in percent.
func (f *FreeKiosk) SetBrightness(ctx context.Context, percent int) error {
	if percent < 0 || percent > 100 {
		return fmt.Errorf("kiosk: brightness %d is not between 0 and 100", percent)
	}
	_, err := f.do(ctx, http.MethodPost, "/api/brightness", fmt.Sprintf(`{"value":%d}`, percent))
	return err
}

// EnableAutoBrightness lets the device's light sensor regulate the brightness.
func (f *FreeKiosk) EnableAutoBrightness(ctx context.Context) error {
	_, err := f.do(ctx, http.MethodPost, "/api/autoBrightness/enable", `{}`)
	return err
}

// ScreenOff switches the screen off.
func (f *FreeKiosk) ScreenOff(ctx context.Context) error {
	_, err := f.do(ctx, http.MethodPost, "/api/screen/off", "")
	return err
}

// ScreenOn switches the screen on.
func (f *FreeKiosk) ScreenOn(ctx context.Context) error {
	_, err := f.do(ctx, http.MethodPost, "/api/screen/on", "")
	return err
}

// Status reads GET /api/status.
func (f *FreeKiosk) Status(ctx context.Context) (Status, error) {
	body, err := f.do(ctx, http.MethodGet, "/api/status", "")
	if err != nil {
		return Status{}, err
	}
	var in struct {
		Data struct {
			Screen struct {
				On         bool `json:"on"`
				Brightness int  `json:"brightness"`
			} `json:"screen"`
			Sensors struct {
				Light *float64 `json:"light"`
			} `json:"sensors"`
			AutoBrightness struct {
				Enabled bool `json:"enabled"`
			} `json:"autoBrightness"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return Status{}, fmt.Errorf("kiosk: unreadable status: %w", err)
	}
	d := in.Data
	return Status{HasLightSensor: d.Sensors.Light != nil, Brightness: d.Screen.Brightness, ScreenOn: d.Screen.On, AutoBrightness: d.AutoBrightness.Enabled}, nil
}

// do sends one request and returns the (limited) body of a 2xx answer.
func (f *FreeKiosk) do(ctx context.Context, method, path, body string) ([]byte, error) {
	var rd io.Reader
	if body != "" {
		rd = bytes.NewBufferString(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, f.base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if f.key != "" {
		req.Header.Set("X-Api-Key", f.key)
	}
	res, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiosk: %w", err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("kiosk: %s refused with status %d", path, res.StatusCode)
	}
	return out, nil
}
