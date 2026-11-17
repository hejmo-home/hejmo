package server_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Ticket 164 (product owner, 16/11/2026, seen in previews 163, 164 and 168): a
// person's name inside a sentence sat above the text around it. A rule in the
// stylesheet that exists but does not work cannot be told from one that does,
// so this renders the pages and measures: an empty inline box put at the end of
// the name and one put right after it must sit on the same baseline.
const baselineProbe = `(() => {
  const sels = %q;
  const out = [];
  let n = 0;
  for (const who of document.querySelectorAll(sels)) {
    const mk = () => { const p = document.createElement("span"); p.style.cssText = "display:inline-block;width:0;height:0;vertical-align:baseline"; return p; };
    const a = mk(), b = mk();
    who.appendChild(a);
    who.after(b);
    const d = Math.abs(a.getBoundingClientRect().bottom - b.getBoundingClientRect().bottom);
    // The marker sits on the same line, centred on the x-height within a pixel.
    const m = who.querySelector(".marker");
    a.remove(); b.remove();
    n++;
    if (d > 0.5) out.push(who.textContent.trim() + " is " + d.toFixed(2) + " px off the baseline of the text after it");
    const cs = getComputedStyle(who);
    if (cs.display.includes("flex")) out.push(who.textContent.trim() + " is " + cs.display + ": its baseline is the marker's");
    if (m && !cs.display.includes("inline")) out.push(who.textContent.trim() + " is not inline");
  }
  return JSON.stringify({n, out});
})()`

func TestAPersonsNameInAWarningSharesTheBaselineOfTheTextAroundIt(t *testing.T) {
	chrome := requireChrome(t)
	f := newLayoutFixture(t)
	site := siteOf("app", f.box, f.client)
	ctx := startChrome(t, chrome)
	base, _ := url.Parse(site.chromeBase())
	for _, c := range site.cookies {
		if _, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{Name: c.Name, Value: c.Value, URL: base.String()}); err != nil {
			t.Fatal(err)
		}
	}
	checks := []struct{ path, sel string }{
		{"/settings/lists", ".allergen-warn .who"},
		{"/settings/lists/library?f=warn", ".allergen-warn .who"},
		{"/settings/lists/library/new", "small.aff .who"},
		{"/settings/lists/library/salmon-fillet", ".facts .allergen-warn .who"},
	}
	for _, vp := range []int{320, 375, 1024} {
		for _, theme := range []string{"light", "dark", "bw"} {
			for _, c := range checks {
				t.Run(fmt.Sprintf("%s/%d/%s", c.path, vp, theme), func(t *testing.T) {
					if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: int64(vp), Height: 900, DeviceScaleFactor: 1}); err != nil {
						t.Fatal(err)
					}
					if err := chromedp.Do(ctx, chromedp.Navigate(site.chromeBase()+c.path)); err != nil {
						t.Fatal(err)
					}
					script := fmt.Sprintf(`document.documentElement.setAttribute("data-theme", %q); `+baselineProbe, theme, c.sel)
					got, err := chromedp.Run(ctx, chromedp.Evaluate[string](script))
					if err != nil {
						t.Fatal(err)
					}
					var res struct {
						N   int
						Out []string
					}
					if err := jsonUnmarshal(got, &res); err != nil {
						t.Fatal(err)
					}
					if res.N == 0 {
						t.Errorf("no %s on %s: the check measured nothing", c.sel, c.path)
					}
					for _, o := range res.Out {
						t.Errorf("%s: %s", c.path, o)
					}
				})
			}
		}
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
