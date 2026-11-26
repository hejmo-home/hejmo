package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Ticket 377, rendered: a source line stays inside the card of its choice (the long Cyrillic, Greek and Ukrainian
// records, the two-source Catholic line) at 375 px and at 1024 x 768, and Islam shows one line for the Islamic
// calendar that is ticked.
const citeProbeJS = `(() => {
	const shown = e => e.getClientRects().length > 0;
	const out = {page: document.documentElement.scrollWidth, wide: window.innerWidth, over: [], lines: {}};
	for (const label of document.querySelectorAll(".choice label, .slava")) {
		const lr = label.getBoundingClientRect();
		const lines = [...label.querySelectorAll(".cite")].filter(shown);
		const key = label.classList.contains("slava") ? "slava" : (label.querySelector("input") || {}).value;
		out.lines[key] = lines.length;
		for (const c of lines) {
			const r = c.getBoundingClientRect();
			if (c.scrollWidth > c.clientWidth + 1 || r.right > lr.right + 1 || r.left < lr.left - 1) out.over.push(key + ": " + c.textContent.slice(0, 40));
		}
	}
	return JSON.stringify(out);
})()`

func TestSourceLinesStayInsideTheirCardAndIslamShowsOneLineForTheCalendarTicked(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	f := newFamilyBox(t, "eink_color")
	save := url.Values{"religious_holidays": {"on"}, "islamic_calendar": {"umm_al_qura"}, "religions": {"muslim", "alevi", "orthodox", "christian"}}
	if r := f.do(http.MethodPost, "/settings/religious-holidays", save, nil); r.status != http.StatusOK {
		t.Fatalf("save = %d\n%s", r.status, r.body)
	}
	if r := f.do(http.MethodPost, "/contacts/birthdays", url.Values{"name_days": {"1"}, "name_day_list": {"bulgarian_orthodox"}, "slava_on": {"1"}, "slava": {"nikola"}}, nil); r.status != http.StatusSeeOther {
		t.Fatalf("birthdays = %d\n%s", r.status, r.body)
	}
	parent := siteOf("parent", f.box, f.client)
	ctx := startChrome(t, chrome)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for _, c := range []struct {
		path string
		w, h int64
		lang string
	}{
		{"/settings/religious-holidays", 375, 812, "de"}, {"/settings/religious-holidays", 1024, 768, "de"}, {"/settings/religious-holidays", 375, 812, "en"},
		{"/contacts/birthdays", 375, 812, "de"}, {"/contacts/birthdays", 1024, 768, "de"}, {"/contacts/birthdays", 375, 812, "en"}, {"/contacts/birthdays", 1024, 768, "en"},
	} {
		t.Run(fmt.Sprintf("%s %d %s", c.path, c.w, c.lang), func(t *testing.T) {
			baseURL, _ := url.Parse(parent.chromeBase())
			if _, err := chromedp.Call(ctx, network.ClearBrowserCookies, cdp.Empty{}); err != nil {
				t.Fatal(err)
			}
			for _, ck := range append([]*http.Cookie{{Name: langCookieForTest, Value: c.lang}}, parent.cookies...) {
				if _, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{Name: ck.Name, Value: ck.Value, URL: baseURL.String()}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: c.w, Height: c.h, DeviceScaleFactor: 1}); err != nil {
				t.Fatal(err)
			}
			if err := chromedp.Do(ctx, chromedp.Navigate(parent.chromeBase()+c.path)); err != nil {
				t.Fatal(err)
			}
			if _, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelectorAll("details.more").forEach(d => d.open = true), "ok"`)); err != nil {
				t.Fatal(err)
			}
			got, err := chromedp.Run(ctx, chromedp.Evaluate[string](citeProbeJS))
			if err != nil {
				t.Fatal(err)
			}
			var m struct {
				Page, Wide int
				Over       []string
				Lines      map[string]int
			}
			if err := json.Unmarshal([]byte(got), &m); err != nil {
				t.Fatalf("probe %q: %v", got, err)
			}
			if len(m.Over) > 0 {
				t.Errorf("a source line overflows its card: %s", strings.Join(m.Over, "; "))
			}
			if m.Page > m.Wide {
				t.Errorf("the page is %d px wide in a %d px window", m.Page, m.Wide)
			}
			if strings.HasPrefix(c.path, "/contacts") {
				for _, l := range []string{"catholic", "protestant", "bulgarian_orthodox", "greek_orthodox", "romanian_orthodox", "ukrainian_orthodox", "slava"} {
					if m.Lines[l] != 1 {
						t.Errorf("%s shows %d source lines, want 1", l, m.Lines[l])
					}
				}
				return
			}
			for _, l := range []string{"muslim", "alevi", "diyanet", "umm_al_qura", "orthodox", "christian"} {
				if m.Lines[l] != 1 {
					t.Errorf("%s shows %d source lines with Umm al-Qura ticked, want exactly 1", l, m.Lines[l])
				}
			}
			// Swapping the Islamic calendar before saving (the :has rule, no reload) must still leave one line each.
			if _, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelector('input[name="islamic_calendar"][value="diyanet"]').click(), "ok"`)); err != nil {
				t.Fatal(err)
			}
			got, err = chromedp.Run(ctx, chromedp.Evaluate[string](citeProbeJS))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(got), &m); err != nil {
				t.Fatalf("probe %q: %v", got, err)
			}
			for _, l := range []string{"muslim", "alevi", "diyanet", "umm_al_qura"} {
				if m.Lines[l] != 1 {
					t.Errorf("%s shows %d source lines after ticking Diyanet, want exactly 1", l, m.Lines[l])
				}
			}
			if len(m.Over) > 0 {
				t.Errorf("a source line overflows its card after ticking Diyanet: %s", strings.Join(m.Over, "; "))
			}
		})
	}
}
