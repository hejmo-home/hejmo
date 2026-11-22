package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/hejmo-home/hejmo/internal/publiccert"
)

// Ticket 57: the own-domain page in headless Chrome, as the preview
// (docs/design/preview-57.html) draws it: each provider's guide chosen, the
// error states after a submit, long names. No page may be wider than the
// viewport (375 px phone first), no text may overflow its box.
func TestOwnDomainPageFitsEveryProviderAndErrorState(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	ctx := startChrome(t, chrome)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	long := "box.familie-schmidt-oberhuber-mueller-von-und-zu.de"

	type scenario struct {
		name, provider string
		twofa          bool
		failure        error
		fill           map[string]string
	}
	scenarios := []scenario{
		{"chosen", "cloudflare", false, nil, nil},
		{"chosen", "desec", false, nil, nil},
		{"chosen", "hetzner", false, nil, nil},
		{"chosen", "inwx", false, nil, nil},
		{"inwx two-factor on", "inwx", true, nil, map[string]string{"username": "familie.schmidt-oberhuber-mueller", "otp_secret": "JBSWY3DPEHPK3PXP"}},
		{"name taken", "hetzner", false, &publiccert.NameTakenError{Name: "familie-schmidt-oberhuber-mueller-von-und-zu.de", Address: "203.0.113.7", Suggestion: long}, nil},
		{"inwx refused", "inwx", true, errors.New("login refused"), map[string]string{"username": "familie.schmidt-oberhuber-mueller", "otp_secret": "JBSWY3DPEHPK3PXP"}},
		{"secret in the wrong form", "inwx", true, &publiccert.InvalidError{Field: "otp_secret"}, map[string]string{"otp_secret": "123456"}},
		{"cloudflare failed", "cloudflare", false, errors.New("no"), map[string]string{"zone_token": "zone"}},
		{"hetzner failed", "hetzner", false, errors.New("no"), nil},
	}
	names, _ := json.Marshal(layoutNames)
	for _, sc := range scenarios {
		for _, lang := range []string{"de", "en"} {
			t.Run(fmt.Sprintf("%s/%s/%s", sc.provider, sc.name, lang), func(t *testing.T) {
				b := newFreshBox(t)
				b.public.failure = sc.failure
				base := b.srv.URL
				u, _ := url.Parse(base)
				if _, err := chromedp.Call(ctx, network.ClearBrowserCookies, cdp.Empty{}); err != nil {
					t.Fatal(err)
				}
				if _, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{Name: langCookieForTest, Value: lang, URL: u.String()}); err != nil {
					t.Fatal(err)
				}
				if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: 375, Height: 812, DeviceScaleFactor: 1}); err != nil {
					t.Fatal(err)
				}
				if err := chromedp.Do(ctx, chromedp.Navigate(base+"/setup/https?way=domain")); err != nil {
					t.Fatal(err)
				}
				fill := map[string]string{"name": long, "token": "x", "password": "y", "username": "mueller-familie"}
				for k, v := range sc.fill {
					fill[k] = v
				}
				fillJSON, _ := json.Marshal(fill)
				script := fmt.Sprintf(`(() => {
					const fill = %s;
					document.querySelector('input[name=dns][value=%q]').click();
					const form = document.querySelector('form.guide[data-provider=%q]');
					for (const [k, v] of Object.entries(fill)) { const el = form.querySelector('[name=' + k + ']'); if (el) el.value = v; }
					const sw = form.querySelector('input[name=twofa]');
					if (sw && sw.checked !== %t) sw.click();
					return true;
				})()`, fillJSON, sc.provider, sc.provider, sc.twofa)
				if _, err := chromedp.Run(ctx, chromedp.Evaluate[bool](script)); err != nil {
					t.Fatal(err)
				}
				if sc.failure != nil {
					submit := fmt.Sprintf(`(() => { document.querySelector('form.guide[data-provider=%q]').requestSubmit(); return true; })()`, sc.provider)
					if _, err := chromedp.Run(ctx, chromedp.Evaluate[bool](submit)); err != nil {
						t.Fatal(err)
					}
					deadline := time.Now().Add(20 * time.Second)
					for {
						got, _ := chromedp.Run(ctx, chromedp.Evaluate[bool](`!!document.querySelector('.guide .error, .guide .ferr')`))
						if got {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("the error page did not come back")
						}
						time.Sleep(100 * time.Millisecond)
					}
				}
				for _, theme := range []string{"light", "bw"} {
					probe := fmt.Sprintf(`(async () => {
						document.documentElement.setAttribute("data-theme", %q);
						%s
						return JSON.stringify(await probe(375, %s));
					})()`, theme, layoutProbeJS, names)
					got, err := chromedp.Run(ctx, chromedp.Evaluate[string](probe, chromedp.EvalAwaitPromise))
					if err != nil {
						t.Fatal(err)
					}
					var probs []layoutProblem
					if err := json.Unmarshal([]byte(got), &probs); err != nil {
						t.Fatal(err)
					}
					for _, p := range probs {
						t.Errorf("theme %s: %s: %s (%s)", theme, p.Kind, p.El, p.Detail)
					}
				}
				// Only the chosen guide shows.
				visible, err := chromedp.Run(ctx, chromedp.Evaluate[string](`[...document.querySelectorAll('form.guide')].filter(f => f.offsetParent !== null).map(f => f.dataset.provider).join(',')`))
				if err != nil || visible != sc.provider {
					t.Errorf("visible guides = %q (%v), want only %q", visible, err, sc.provider)
				}
			})
		}
	}
}

// Before a choice, no guide shows, only the hint that says what happens next.
func TestOwnDomainPageShowsOnlyTheHintBeforeAChoice(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	ctx := startChrome(t, chrome)
	b := newFreshBox(t)
	if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: 1024, Height: 768, DeviceScaleFactor: 1}); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(b.srv.URL+"/setup/https?way=domain")); err != nil {
		t.Fatal(err)
	}
	got, err := chromedp.Run(ctx, chromedp.Evaluate[string](`JSON.stringify({guides: [...document.querySelectorAll('form.guide')].filter(f => f.offsetParent !== null).length, pick: document.querySelector('.pick-first').offsetParent !== null})`))
	if err != nil || got != `{"guides":0,"pick":true}` {
		t.Errorf("got %s (%v)", got, err)
	}
	// The calendar connect page keeps showing its guides (the provider rules must not reach it).
	f := newLayoutFixture(t)
	site := siteOf("app", f.box, f.client)
	if _, err := chromedp.Call(ctx, network.ClearBrowserCookies, cdp.Empty{}); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(site.chromeBase())
	for _, c := range site.cookies {
		if _, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{Name: c.Name, Value: c.Value, URL: u.String()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(site.chromeBase()+"/connections/new")); err != nil {
		t.Fatal(err)
	}
	got, err = chromedp.Run(ctx, chromedp.Evaluate[string](`String([...document.querySelectorAll('form.guide')].filter(f => f.offsetParent !== null).length)`))
	if err != nil || got != "1" {
		t.Errorf("calendar connect page shows %s guides (%v), want 1", got, err)
	}
}
