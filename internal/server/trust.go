package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The devices the trust page has steps for, in the order of its chooser.
const (
	devIOS     = "ios"
	devAndroid = "android"
	devMac     = "mac"
	devWindows = "windows"
	devLinux   = "linux"
)

var trustDevices = []string{devIOS, devAndroid, devMac, devWindows, devLinux}

// detectDevice guesses the visitor's device from the User-Agent. It returns
// "" when it cannot tell. An iPad that asks for the desktop site calls itself a
// Mac; the page script turns that into iOS when the device has a touch screen.
func detectDevice(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "cros"): // ChromeOS has no update-ca-certificates: unknown
		return ""
	case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipad"), strings.Contains(ua, "ipod"):
		return devIOS
	case strings.Contains(ua, "android"):
		return devAndroid
	case strings.Contains(ua, "windows"):
		return devWindows
	case strings.Contains(ua, "macintosh"), strings.Contains(ua, "mac os x"):
		return devMac
	case strings.Contains(ua, "linux"), strings.Contains(ua, "x11"):
		return devLinux
	}
	return ""
}

// trustStep is one numbered step of a device's guide. Texts are keys in the
// locales. The first step carries the download, the one that usually goes
// wrong carries the failure hint, the last one carries the check.
type trustStep struct {
	N     int
	Title string
	Text  string
	Hint  string
	// After is what step 1 says once the file is loaded.
	After string
	// Fail is the hint that appears here when the check keeps failing after a download.
	Fail string
	// Download is set on the first step, Last on the one that waits for the check.
	Download, Last bool
}

func trustSteps(dev string) []trustStep {
	k := func(n int, part string) string { return "trust." + dev + "." + strconv.Itoa(n) + "." + part }
	steps := []trustStep{
		{N: 1, Title: k(1, "title"), Text: k(1, "text"), After: k(1, "after"), Download: true},
		{N: 2, Title: k(2, "title"), Text: k(2, "text"), Hint: optionalHint(dev, 2)},
		{N: 3, Title: k(3, "title"), Text: k(3, "text"), Fail: k(3, "fail")},
		{N: 4, Title: k(4, "title"), Text: k(4, "text"), Last: true},
	}
	return steps
}

// optionalHint names the steps that have a hint under their text.
func optionalHint(dev string, n int) string {
	if dev == devAndroid && n == 2 {
		return "trust.android.2.hint"
	}
	if dev == devLinux && n == 2 {
		return "trust.linux.2.hint"
	}
	return ""
}

// trustPage is the data of the trust page for one device.
type trustPage struct {
	Device string
	// Detected is true when the device comes from the User-Agent, not from ?device=.
	Detected bool
	// Known is false when the User-Agent said nothing; the first device is shown then.
	Known bool
}

func (s *server) trustDevice(r *http.Request) trustPage {
	if d := r.URL.Query().Get("device"); d != "" {
		for _, known := range trustDevices {
			if d == known {
				return trustPage{Device: d, Known: true}
			}
		}
	}
	if d := detectDevice(r.UserAgent()); d != "" {
		return trustPage{Device: d, Detected: true, Known: true}
	}
	return trustPage{Device: devIOS}
}

// trustDeviceLink is one entry of the device chooser. It keeps the address a
// phone is on its way to pair with (continue), so choosing another device does
// not lose it.
type trustDeviceLink struct {
	Key, Href string
	Current   bool
}

func trustDeviceLinks(current, continueURL string) []trustDeviceLink {
	var out []trustDeviceLink
	for _, d := range trustDevices {
		q := url.Values{"device": {d}}
		if continueURL != "" {
			q.Set("continue", continueURL)
		}
		out = append(out, trustDeviceLink{Key: d, Href: "?" + q.Encode(), Current: d == current})
	}
	return out
}
