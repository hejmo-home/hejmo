package auth

import "strings"

// Device kinds and browsers are stored as keys; the web interface renders
// them through the locales (device.* and browser.*). The raw User-Agent is
// never kept (issue #108).
const Unknown = "unknown"

// DeviceFromUserAgent derives the kind of device and the browser from a
// User-Agent header. It returns keys: device is one of iphone, ipad,
// android_phone, android_tablet, mac, windows, linux, chromebook, unknown;
// browser is one of safari, chrome, firefox, edge, samsung, unknown.
func DeviceFromUserAgent(ua string) (device, browser string) {
	return deviceKind(ua), browserOf(ua)
}

// DeviceLabel is what a browser session stores: "device|browser" as keys.
func DeviceLabel(ua string) string {
	d, b := DeviceFromUserAgent(ua)
	return d + "|" + b
}

func deviceKind(ua string) string {
	has := func(s string) bool { return strings.Contains(ua, s) }
	switch {
	case has("iPhone"):
		return "iphone"
	case has("iPad"):
		return "ipad"
	case has("Android"):
		if has("Mobile") {
			return "android_phone"
		}
		return "android_tablet"
	case has("CrOS"):
		return "chromebook"
	case has("Windows"):
		return "windows"
	case has("Macintosh"):
		return "mac"
	case has("Linux") || has("X11"):
		return "linux"
	}
	return Unknown
}

func browserOf(ua string) string {
	has := func(s string) bool { return strings.Contains(ua, s) }
	// Order matters: most browsers also say Chrome and Safari.
	switch {
	case has("Edg/") || has("EdgA/") || has("EdgiOS/") || has("Edge/"):
		return "edge"
	case has("SamsungBrowser/"):
		return "samsung"
	case has("Firefox/") || has("FxiOS/"):
		return "firefox"
	case has("Chrome/") || has("CriOS/"):
		return "chrome"
	case has("Safari/") && has("Version/"):
		return "safari"
	}
	return Unknown
}
