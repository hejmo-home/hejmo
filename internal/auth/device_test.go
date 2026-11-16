package auth_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
)

// Issue #108: the box keeps only a device kind and a browser, never the raw
// User-Agent.
func TestDeviceFromUserAgent(t *testing.T) {
	cases := []struct {
		name, ua, device, browser string
	}{
		{"iPhone Safari", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "iphone", "safari"},
		{"iPhone Chrome", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/126.0.6478.153 Mobile/15E148 Safari/604.1", "iphone", "chrome"},
		{"iPhone Firefox", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) FxiOS/127.0 Mobile/15E148 Safari/605.1.15", "iphone", "firefox"},
		{"iPhone Edge", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 EdgiOS/126.2592.87 Mobile/15E148 Safari/605.1.15", "iphone", "edge"},
		{"iPad Safari", "Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "ipad", "safari"},
		{"Android phone Chrome", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36", "android_phone", "chrome"},
		{"Android tablet Chrome", "Mozilla/5.0 (Linux; Android 14; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36", "android_tablet", "chrome"},
		{"Android tablet Samsung Internet", "Mozilla/5.0 (Linux; Android 14; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0.0.0 Safari/537.36", "android_tablet", "samsung"},
		{"Android phone Samsung Internet", "Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/25.0 Chrome/121.0.0.0 Mobile Safari/537.36", "android_phone", "samsung"},
		{"Android Firefox", "Mozilla/5.0 (Android 14; Mobile; rv:127.0) Gecko/127.0 Firefox/127.0", "android_phone", "firefox"},
		{"Mac Safari", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", "mac", "safari"},
		{"Mac Firefox", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:127.0) Gecko/20100101 Firefox/127.0", "mac", "firefox"},
		{"Mac Chrome", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36", "mac", "chrome"},
		{"Windows Edge", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0", "windows", "edge"},
		{"Windows Chrome", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36", "windows", "chrome"},
		{"Linux Firefox", "Mozilla/5.0 (X11; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0", "linux", "firefox"},
		{"Chromebook Chrome", "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36", "chromebook", "chrome"},
		{"empty", "", "unknown", "unknown"},
		{"curl", "curl/8.4.0", "unknown", "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, b := auth.DeviceFromUserAgent(c.ua)
			if d != c.device || b != c.browser {
				t.Errorf("DeviceFromUserAgent = %q, %q; want %q, %q", d, b, c.device, c.browser)
			}
		})
	}
}

// The stored label is a pair of keys, so a hostile User-Agent never reaches
// the database or a page.
func TestDeviceLabelStoresKeysOnly(t *testing.T) {
	if got := auth.DeviceLabel("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126 Safari/537.36 Edg/126"); got != "windows|edge" {
		t.Errorf("DeviceLabel = %q, want windows|edge", got)
	}
	if got := auth.DeviceLabel("<script>alert(1)</script>"); got != "unknown|unknown" {
		t.Errorf("DeviceLabel(hostile) = %q, want unknown|unknown", got)
	}
}
