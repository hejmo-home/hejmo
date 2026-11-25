package server_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/hejmo-home/hejmo/internal/recipe"
)

// The script of the meal plan (ticket 281) in a real browser: suggestions open
// from two letters, the arrow keys and Enter choose, a recipe swaps its box in
// place of the field with the household's portions, the stepper says in words
// what the portions mean, and the save bar says that something is not saved.
// Needs Chrome (see layout_render_test.go).
func TestMealPlanScriptSuggestsChoosesARecipeAndStepsThePortions(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	mb := newMealBox(t)
	curry := mb.curry(t)
	mb.add(t, recipe.Recipe{Name: "Kichererbsen-Curry mit Spinat"})
	site := siteOf("app", mb.b, mb.b.client)
	ctx := startChrome(t, chrome)
	base, _ := url.Parse(site.chromeBase())
	for _, c := range append(site.cookies, newCookie(langCookieForTest, "en")) {
		if _, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{Name: c.Name, Value: c.Value, URL: base.String()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: 1024, Height: 900, DeviceScaleFactor: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := chromedp.Call(ctx, network.ClearBrowserCache, cdp.Empty{}); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(site.chromeBase()+"/meals")); err != nil {
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
	wait := `const until = async (f) => { for (let i = 0; i < 60 && !f(); i++) await new Promise(r => setTimeout(r, 50)); return f(); };`
	cell := dinnerCell
	// Typing two letters opens the list; one letter does not.
	got := eval(`(async () => { ` + wait + `
		const t = document.getElementById("` + cell + `");
		t.focus(); t.value = "C"; t.dispatchEvent(new Event("input", {bubbles: true}));
		await new Promise(r => setTimeout(r, 400));
		const none = !document.querySelector(".sugg");
		t.value = "Curry"; t.dispatchEvent(new Event("input", {bubbles: true}));
		await until(() => document.querySelector(".sugg li[role=option]"));
		return JSON.stringify({none, options: document.querySelectorAll(".sugg li[role=option]").length, expanded: t.getAttribute("aria-expanded"), role: t.getAttribute("role")});
	})()`)
	if got != `{"none":true,"options":3,"expanded":"true","role":"combobox"}` {
		t.Fatalf("after typing = %s, want the list of two recipes and the typed text", got)
	}
	// The arrow keys move through the options; Enter chooses the first, which is a recipe.
	got = eval(`(async () => {
		const t = document.getElementById("` + cell + `");
		const key = (k) => t.dispatchEvent(new KeyboardEvent("keydown", {key: k, bubbles: true, cancelable: true}));
		key("ArrowDown"); key("ArrowDown"); key("ArrowUp");
		const selected = [...document.querySelectorAll(".sugg li[role=option]")].map(l => l.getAttribute("aria-selected") === "true");
		key("Enter");
		const row = document.querySelector('[data-day="` + today + `"] .mrow[data-mt="dinner"]');
		return JSON.stringify({selected, swapped: !!document.querySelector("#` + cell + `"), box: !!row.querySelector(".rcp-entry"), has: row.classList.contains("has-rcp"),
			recipe: row.querySelector('input[name="` + cell + `.recipe"]').value, portions: row.querySelector('input[name="` + cell + `.portions"]').value,
			words: row.querySelector(".for").textContent, name: row.querySelector(".rcp-name").textContent,
			dirty: !document.querySelector(".savebar .dirty").hidden, sugg: !!document.querySelector(".sugg")});
	})()`)
	for _, want := range []string{`"selected":[true,false,false]`, `"swapped":false`, `"box":true`, `"has":true`, `"recipe":"` + curry.ID + `"`, `"portions":"4"`, `"words":"for all 4"`, `"name":"Erdnuss-Curry mit Süßkartoffeln"`, `"dirty":true`, `"sugg":false`} {
		if !strings.Contains(got, want) {
			t.Errorf("after choosing: %s lacks %s", got, want)
		}
	}
	// The stepper changes the portions and what they mean in words; it stops at 1.
	got = eval(`(async () => {
		const row = document.querySelector('[data-day="` + today + `"] .mrow[data-mt="dinner"]');
		const click = (s) => row.querySelector('button[data-step="' + s + '"]').click();
		const read = () => row.querySelector(".portions input").value + " " + row.querySelector(".for").textContent;
		click("up"); const up = read();
		click("down"); click("down"); click("down"); click("down"); click("down"); const low = read();
		row.querySelector(".portions input").value = "4"; row.querySelector(".portions input").dispatchEvent(new Event("input", {bubbles: true}));
		return JSON.stringify([up, low, read()]);
	})()`)
	if got != `["5 you are 4","1 you are 4","4 for all 4"]` {
		t.Errorf("stepper = %s", got)
	}
	// "Keep" (the last option) leaves the typed text in the field as free text and closes the list.
	got = eval(`(async () => { ` + wait + `
		const t = document.getElementById("` + mealField(today, "snack") + `");
		t.focus(); t.value = "Curry"; t.dispatchEvent(new Event("input", {bubbles: true}));
		await until(() => t.closest(".mrow").querySelector(".sugg li[role=option]"));
		const key = (k) => t.dispatchEvent(new KeyboardEvent("keydown", {key: k, bubbles: true, cancelable: true}));
		key("ArrowUp"); key("Enter");
		return JSON.stringify({value: t.value, open: !!t.closest(".mrow").querySelector(".sugg"), field: !!document.getElementById(t.id), box: !!t.closest(".mrow").querySelector(".rcp-entry")});
	})()`)
	if got != `{"value":"Curry","open":false,"field":true,"box":false}` {
		t.Errorf("keeping the text = %s", got)
	}
	// Another recipe: the first option.
	got = eval(`(async () => { ` + wait + `
		const t = document.getElementById("` + mealField(today, "lunch") + `");
		t.focus(); t.value = "Kicher"; t.dispatchEvent(new Event("input", {bubbles: true}));
		await until(() => t.closest(".mrow").querySelector(".sugg li[role=option]"));
		const key = (k) => t.dispatchEvent(new KeyboardEvent("keydown", {key: k, bubbles: true, cancelable: true}));
		const row = t.closest(".mrow"); key("ArrowDown"); key("Enter");
		return JSON.stringify({recipe: !!row.querySelector(".rcp-entry"), still: !!document.getElementById(t.id)});
	})()`)
	if got != `{"recipe":true,"still":false}` {
		t.Errorf("choosing the other recipe = %s", got)
	}
}
