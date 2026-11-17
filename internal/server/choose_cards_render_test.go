package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calsync"
)

// The cards of Choose calendars in a real browser (#183): the stylesheet, not
// the markup, decides what is seen. Needs Chrome (see layout_render_test.go).
//
// probeCards reports, per card, the state in words as the browser shows it
// (innerText leaves out what display:none hides), whether the fields and the
// colour swatch are seen, and the count line.
const probeCards = `JSON.stringify([...document.querySelectorAll(".calchoice")].map(c => ({
	on: c.classList.contains("is-on"),
	state: c.querySelector(".state").innerText.replace(/\s+/g, " ").trim(),
	fields: getComputedStyle(c.querySelector(".calfields")).display !== "none",
	count: c.querySelector(".count").innerText.replace(/\s+/g, " ").trim(),
	invalid: c.classList.contains("invalid"),
})))`

type cardSeen struct {
	On, Fields, Invalid bool
	State, Count        string
}

func TestChooseCalendarCardsInTheBrowserShowOneStateAndFieldsOnlyWhileOn(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	b, id := cardsBox(t)
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) {
		return countingRemote{hostedRemote{found: []caldavclient.Calendar{{URL: flowCalendar, Name: "Home"}, {URL: flowCalendar2, Name: "Work"}}}}, nil
	})
	site := siteOf("app", b, b.client)
	ctx := startChrome(t, chrome)

	for _, tc := range []struct {
		lang, theme, on, off string
		width                int
	}{
		{"en", "light", "On: shown on the box", "Off: not on the box", 375},
		{"de", "light", "An: auf der Box sichtbar", "Aus: nicht auf der Box", 1024},
		{"en", "bw", "On: shown on the box", "Off: not on the box", 768},
	} {
		t.Run(fmt.Sprintf("%s/%s/%d", tc.lang, tc.theme, tc.width), func(t *testing.T) {
			base, _ := url.Parse(site.chromeBase())
			if _, err := chromedp.Call(ctx, network.ClearBrowserCookies, cdp.Empty{}); err != nil {
				t.Fatal(err)
			}
			for _, c := range append(site.cookies, newCookie(langCookieForTest, tc.lang)) {
				if _, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{Name: c.Name, Value: c.Value, URL: base.String()}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: int64(tc.width), Height: 900, DeviceScaleFactor: 1}); err != nil {
				t.Fatal(err)
			}
			if err := chromedp.Do(ctx, chromedp.Navigate(site.chromeBase()+"/connections/"+id)); err != nil {
				t.Fatal(err)
			}
			eval := func(js string) string {
				t.Helper()
				got, err := chromedp.Run(ctx, chromedp.Evaluate[string](js, chromedp.EvalAwaitPromise))
				if err != nil {
					t.Fatalf("evaluate: %v", err)
				}
				return got
			}
			seen := func() []cardSeen {
				t.Helper()
				var cards []cardSeen
				if err := json.Unmarshal([]byte(eval(probeCards)), &cards); err != nil || len(cards) != 2 {
					t.Fatalf("cards: %v", err)
				}
				return cards
			}
			eval(fmt.Sprintf(`document.documentElement.setAttribute("data-theme", %q); "ok"`, tc.theme))
			// Let the counts arrive.
			eval(`(async () => { for (let i = 0; i < 50 && document.querySelector('.count[data-state="loading"]'); i++) await new Promise(r => setTimeout(r, 100)); return "ok"; })()`)

			cards := seen()
			if cards[0].State != tc.on || cards[1].State != tc.off {
				t.Errorf("state words = %q and %q, want %q and %q", cards[0].State, cards[1].State, tc.on, tc.off)
			}
			if !cards[0].Fields || cards[1].Fields {
				t.Errorf("fields seen: on card %v, off card %v; want the on card's only", cards[0].Fields, cards[1].Fields)
			}
			if cards[1].Count == "" || cards[1].Count == "The number of events is shown when JavaScript is on." {
				t.Errorf("the off card does not say how many events it has: %q", cards[1].Count)
			}

			// Switching the off card on shows its fields and flips the words, live; the words never run together.
			eval(`document.querySelectorAll(".calchoice .switch input")[1].click(); "ok"`)
			cards = seen()
			if cards[1].State != tc.on || !cards[1].Fields {
				t.Errorf("after switching on: %q, fields seen %v", cards[1].State, cards[1].Fields)
			}
			eval(`document.querySelectorAll(".calchoice .switch input")[0].click(); "ok"`)
			cards = seen()
			if cards[0].State != tc.off || cards[0].Fields {
				t.Errorf("after switching off: %q, fields seen %v", cards[0].State, cards[0].Fields)
			}

			// An empty calendar is a quiet fact while off and a notice only while on.
			eval(`document.querySelectorAll(".calchoice .count").forEach(c => c.setAttribute("data-state", "empty")); "ok"`)
			cards = seen() // card 0 is off, card 1 is on
			if cards[0].Count != "No events in the last 12 months or later" && cards[0].Count != "Keine Termine in den letzten 12 Monaten oder später" {
				t.Errorf("off and empty: %q, want the quiet text", cards[0].Count)
			}
			if cards[1].Count != "Shows nothing on the box right now: no events in the last 12 months or later" && cards[1].Count != "Zeigt zurzeit nichts auf der Box: keine Termine in den letzten 12 Monaten oder später" {
				t.Errorf("on and empty: %q, want the notice", cards[1].Count)
			}
		})
	}
}

func newCookie(name, value string) *http.Cookie { return &http.Cookie{Name: name, Value: value} }
