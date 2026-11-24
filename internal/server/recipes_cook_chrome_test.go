package server_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Ticket 286: the script of the cooking view in a real browser. The whole view is one form; with the script
// ticks, steps and tabs change in place (no new page: a marker on window survives), the address follows, and
// only the portions ask the box. Needs Chrome like TestRenderedLayout: skipped without, a failure with
// HEJMO_REQUIRE_CHROME=1.
func TestTheCookingViewWorksInPlaceInTheBrowser(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	rb := newRecipeBox(t)
	id := rb.cookCurry(t)
	ctx := startChrome(t, chrome)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	base, _ := url.Parse(rb.b.srv.URL)
	for _, c := range rb.b.client.Jar.Cookies(base) {
		if _, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{Name: c.Name, Value: c.Value, URL: base.String()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := chromedp.Call(ctx, network.SetExtraHTTPHeaders, network.SetExtraHTTPHeadersParams{Headers: network.Headers{"Accept-Language": "en"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: 1024, Height: 768, DeviceScaleFactor: 1}); err != nil {
		t.Fatal(err)
	}
	page := rb.b.srv.URL + cookPath(id)
	if err := chromedp.Do(ctx, chromedp.Navigate(page)); err != nil {
		t.Fatal(err)
	}
	eval := func(js string) map[string]any {
		t.Helper()
		got, err := chromedp.Run(ctx, chromedp.Evaluate[string](`(async () => { const sleep = ms => new Promise(r => setTimeout(r, ms)); const out = {}; `+js+` return JSON.stringify(out); })()`, chromedp.EvalAwaitPromise))
		if err != nil {
			t.Fatalf("script: %v", err)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(got), &out); err != nil {
			t.Fatalf("answer %q: %v", got, err)
		}
		return out
	}
	got := eval(`
		window.__marker = 42;
		const form = document.querySelector('form.cook-form');
		const wake = document.querySelector('.cook-wake'), notice = document.querySelector('p.notice');
		out.wakeOrNotice = wake.hidden !== notice.hidden; // the switch or the notice, never both, never none
		document.querySelector('input[name=have][value="0"]').click();
		document.querySelector('input[name=have][value="2"]').click();
		out.count = document.querySelector('.cook-ings-part > h2 .cnt').textContent;
		out.tab = document.querySelector('.cook-tabs button[value=ings] small').textContent;
		out.url1 = location.search;
		form.querySelector('.cook-nav .for-steps:not(.secondary)').click();
		out.step2 = document.querySelector('.cook-text').textContent;
		out.title2 = document.querySelector('#h-step').textContent;
		out.stepTab = document.querySelector('.cook-tabs button[value=steps] small').textContent;
		out.dot1 = document.querySelector('.cook-dots button[value="1"]').className;
		out.url2 = location.search;
		out.marker = window.__marker;
		document.querySelector('.cook-dots button[value="3"]').click();
		out.last = document.querySelector('.cook-nav button[name=done]') ? document.querySelector('.cook-nav button[name=done]').textContent : '';
		out.lastLine = document.querySelector('.cook-done-line') ? document.querySelector('.cook-done-line').textContent : '';
		form.querySelector('.cook-nav .back').click();
		out.back = document.querySelector('#h-step').textContent;
		const input = document.querySelector('#portions');
		input.value = '6';
		input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
		await sleep(700);
		out.q6 = [...document.querySelectorAll('.cook-ings .q')].map(e => e.textContent);
		out.ticked6 = [...document.querySelectorAll('input[name=have]:checked')].map(e => e.value);
		out.hint6 = (document.querySelector('.cook-portions .hint') || {}).textContent;
		out.url3 = location.search;
		out.stepKept = document.querySelector('#h-step').textContent;
		const bad = document.querySelector('#portions');
		bad.value = '0';
		bad.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
		await sleep(100);
		out.err = (document.querySelector('.cook-portions .ferr') || {}).textContent;
		out.typed = document.querySelector('#portions').value;
		out.qStill = [...document.querySelectorAll('.cook-ings .q')].map(e => e.textContent);
		out.marker2 = window.__marker;
	`)
	check := func(key string, want any) {
		t.Helper()
		if g, _ := json.Marshal(got[key]); string(g) != jsonText(want) {
			t.Errorf("%s = %s, want %s", key, g, jsonText(want))
		}
	}
	check("wakeOrNotice", true)
	check("count", "2 of 4 ticked")
	check("tab", "2 of 4 ticked")
	check("url1", "?view=ings&step=1&have=0&have=2")
	check("step2", "Currypaste anrösten.")
	check("title2", "Step 2 of 3")
	check("stepTab", "Step 2 of 3")
	check("dot1", "done")
	check("url2", "?view=ings&step=2&have=0&have=2")
	check("marker", 42)
	check("last", "Done")
	check("lastLine", "That is the last step.")
	check("back", "Step 2 of 3")
	check("q6", []string{"900 g", "4.5 tbsp", "4.5 tbsp"})
	check("ticked6", []string{"0", "2"})
	check("hint6", "Quantities converted from 4 to 6 servings, rounded to one decimal.")
	check("url3", "?view=ings&step=2&portions=6&have=0&have=2")
	check("stepKept", "Step 2 of 3")
	check("err", "Enter a number from 1 to 99. The quantities still apply to 6 servings.")
	check("typed", "0")
	check("qStill", []string{"900 g", "4.5 tbsp", "4.5 tbsp"})
	check("marker2", 42)

	// Ticks and the step are kept for this visit: the bare address comes back as it was left, "Done" forgets.
	if err := chromedp.Do(ctx, chromedp.Navigate(rb.b.srv.URL+cookPath(id, "portions=6"))); err != nil {
		t.Fatal(err)
	}
	got = eval(`
		out.ticked = [...document.querySelectorAll('input[name=have]:checked')].map(e => e.value);
		out.title = document.querySelector('#h-step').textContent;
	`)
	check("ticked", []string{"0", "2"})
	check("title", "Step 2 of 3")
	eval(`document.querySelector('.cook-dots button[value="3"]').click(); document.querySelector('.cook-nav button[name=done]').click();`)
	deadline := time.Now().Add(5 * time.Second)
	var where string
	for time.Now().Before(deadline) {
		w, err := chromedp.Run(ctx, chromedp.Evaluate[string](`location.pathname + "|" + (document.querySelector('[data-success]') || {}).textContent + "|" + Object.keys(sessionStorage).length`))
		if err == nil && strings.HasPrefix(w, "/meals/recipes/"+id+"|") {
			where = w
			if !strings.Contains(w, "|undefined|") { // the page may still be loading
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if want := "/meals/recipes/" + id + "|Finished cooking: “Erdnuss-Curry mit Süßkartoffeln”. Enjoy your meal!|0"; where != want {
		t.Errorf("after Done: %q, want %q", where, want)
	}

	// On a phone one part shows at a time, and the buttons of the shown part.
	if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: 375, Height: 812, DeviceScaleFactor: 1}); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(page)); err != nil {
		t.Fatal(err)
	}
	got = eval(`
		const shown = sel => { const e = document.querySelector(sel); return !!e && getComputedStyle(e).display !== 'none'; };
		out.ingsFirst = [shown('.cook-ings-part'), shown('.cook-step'), shown('.cook-nav .for-ings'), shown('.cook-nav .for-steps')];
		out.start = document.querySelector('.cook-nav .for-ings').textContent;
		document.querySelector('.cook-nav .for-ings').click();
		out.stepsNext = [shown('.cook-ings-part'), shown('.cook-step'), shown('.cook-nav .for-ings'), shown('.cook-nav .for-steps')];
		out.toIngs = [...document.querySelectorAll('.cook-nav .back')].map(b => b.textContent + ':' + shown('.cook-nav .back'));
		document.querySelector('.cook-nav .back').click();
		out.backToIngs = [shown('.cook-ings-part'), shown('.cook-step')];
		out.view = document.querySelector('.cook').dataset.view;
		const text = document.querySelector('.cook-text');
		out.fontStep = 0;
		document.querySelector('.cook-tabs button[value=steps]').click();
		out.fontStep = parseFloat(getComputedStyle(document.querySelector('.cook-text')).fontSize);
		out.fontIng = parseFloat(getComputedStyle(document.querySelector('.cook-tick')).fontSize);
		out.tapHeight = Math.round(document.querySelector('.cook-nav .button:not(.secondary)').getBoundingClientRect().height);
		out.noWide = document.documentElement.scrollWidth <= document.documentElement.clientWidth;
	`)
	check("ingsFirst", []bool{true, false, true, false})
	check("start", "Go: step 1")
	check("stepsNext", []bool{false, true, false, true})
	check("backToIngs", []bool{true, false})
	check("view", "ings")
	check("fontStep", 24)
	check("fontIng", 20)
	check("noWide", true)
	if h, _ := got["tapHeight"].(float64); h < 56 {
		t.Errorf("the primary button is %v px high, want at least 56", h)
	}
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
