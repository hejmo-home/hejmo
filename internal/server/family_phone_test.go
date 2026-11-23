package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/layout"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/web"
)

// threeTileView is a family view with four people, a busy day and all three
// footer tiles: meals, tomorrow with the put-out pill, and a list of 8 entries.
// The family strip is filled too (2 entries today, 1 tomorrow): it is the worst
// case for the first screen, because it pushes the columns down.
func threeTileView() familyview.View {
	d := func(h, m int) time.Time { return time.Date(2026, 11, 14, h, m, 0, 0, berlinTZ) }
	people := []familyview.Person{
		{ID: "mama", Name: "Mama", Slot: 1, Shape: "circle-filled"}, {ID: "papa", Name: "Papa", Slot: 2, Shape: "circle-empty"},
		{ID: "seb", Name: "Sebastian", Slot: 3, Shape: "square-filled"}, {ID: "mia", Name: "Mia", Slot: 4, Shape: "square-empty"}}
	var occ []calendar.Occurrence
	for _, p := range people {
		for i := 0; i < 5; i++ {
			occ = append(occ, calendar.Occurrence{EventID: fmt.Sprintf("%s%d", p.ID, i), CalendarID: "c", Title: fmt.Sprintf("Appointment %d of %s", i+1, p.Name),
				Start: d(9+i, 0), End: d(9+i, 45), People: []string{p.ID}})
		}
	}
	v := familyview.Build(familyview.Input{I18n: i18n.MustLoad(), Now: d(8, 0), Lang: "en", Region: "DE-BY", People: people, Occurrences: occ})
	v.Columns[0].Progress = &familyview.Progress{Done: 1, Total: 4}
	v.Meal = &familyview.MealTile{Label: "Meals today", Entries: []familyview.MealEntry{{Mealtime: "lunch", Dish: "Tomato soup"}, {Mealtime: "dinner", Dish: "Pasta bake"}}}
	v.TomorrowTile = &familyview.Tile{Label: "Tomorrow", Alert: true, Lines: []familyview.Line{{Kind: familyview.LineWaste, Text: "Paper bin"}, {Kind: familyview.LineWaste, Text: "Residual waste"}}}
	v.List = &familyview.ListTile{ID: "l1", Name: "Shopping list", More: 2, Entries: []string{"Milk", "Bread", "Apples", "Pasta", "Tomatoes", "Cheese", "Rice", "Eggs"}}
	v.Strip = []familyview.StripItem{{Kind: familyview.KindEvent, Title: "School trip to the museum", AllDay: true}, {Kind: familyview.KindEvent, Title: "Grandma visits", AllDay: true}}
	v.StripTomorrow = []familyview.StripItem{{Kind: familyview.KindEvent, Title: "Dentist", AllDay: true}}
	v.FooterOrder = []layout.BlockID{"meals", "tomorrow", "list"}
	return v
}

// Ticket 208: on a phone the family view scrolls as one page and each visible
// column shows its first three entries on the first screen; wall displays keep
// the fixed layout.
func TestFamilyViewOnPhonesAndWallDisplays(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	markup, err := server.RenderFamilyView("en", threeTileView())
	if err != nil {
		t.Fatal(err)
	}
	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/static/hejmo.css"></head><body><div class="family-page"><div data-view>%s</div></div></body></html>`, markup)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(startChrome(t, chrome), 2*time.Minute)
	defer cancel()
	probe := `(() => {
		const col = [...document.querySelectorAll(".fv .column")].filter((c) => c.getBoundingClientRect().left < innerWidth - 1);
		const lowest = col.map((c) => Math.max(...[...c.querySelectorAll(".events > .event")].slice(0, 3).map((e) => e.getBoundingClientRect().bottom)));
		const events = col.map((c) => c.querySelectorAll(".events > .event").length);
		return JSON.stringify({
			columns: col.length, lowest, events, inner: innerHeight, pageWidth: document.documentElement.scrollWidth, innerWidth,
			bodyOverflow: getComputedStyle(document.body).overflowY, below: getComputedStyle(document.querySelector(".fv-below")).display,
			footerTop: Math.round(document.querySelector(".fv-footer").getBoundingClientRect().top), tile: document.querySelector(".fv-footer .tile").getBoundingClientRect().width,
			bodyPadding: getComputedStyle(document.body).paddingLeft, clock: getComputedStyle(document.querySelector("[data-clock]")).display,
		});
	})()`
	for _, c := range []struct {
		w, h  int
		phone bool
	}{{375, 812, true}, {375, 667, true}, {812, 375, true}, {1024, 768, false}, {768, 1024, false}} {
		t.Run(fmt.Sprintf("%dx%d", c.w, c.h), func(t *testing.T) {
			if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: int64(c.w), Height: int64(c.h), DeviceScaleFactor: 1}); err != nil {
				t.Fatal(err)
			}
			if err := chromedp.Do(ctx, chromedp.Navigate(srv.URL+"/")); err != nil {
				t.Fatal(err)
			}
			got, err := chromedp.Run(ctx, chromedp.Evaluate[string](probe))
			if err != nil {
				t.Fatal(err)
			}
			var m struct {
				Columns, Inner, PageWidth, InnerWidth, FooterTop int
				Lowest                                           []float64
				Events                                           []int
				BodyOverflow, Below, BodyPadding, Clock          string
				Tile                                             float64
			}
			if err := json.Unmarshal([]byte(got), &m); err != nil {
				t.Fatalf("%q: %v", got, err)
			}
			if m.PageWidth > m.InnerWidth {
				t.Errorf("the page is %d px wide in a %d px viewport", m.PageWidth, m.InnerWidth)
			}
			if !c.phone {
				if m.BodyOverflow != "hidden" || m.Below != "none" || m.BodyPadding != "28px" || m.Clock == "none" {
					t.Errorf("wall display changed: %s", got)
				}
				return
			}
			if m.BodyOverflow == "hidden" || m.Below == "none" || m.BodyPadding != "14px" || (c.w < 600) != (m.Clock == "none") {
				t.Errorf("phone layout missing: %s", got)
			}
			if m.Columns < 2 {
				t.Errorf("%d visible columns: %s", m.Columns, got)
			}
			for i, low := range m.Lowest {
				// A landscape phone is too short for three entries on the first screen.
				if c.h > 500 && low > float64(m.Inner) {
					t.Errorf("column %d: its third entry ends at %.0f px, below the first screen (%d px)", i+1, low, m.Inner)
				}
				if m.Events[i] != 5 {
					t.Errorf("column %d shows %d of 5 entries: a column scrolls inside", i+1, m.Events[i])
				}
			}
			if m.FooterTop <= m.Inner/2 {
				t.Errorf("the tiles start at %d px, not below the columns: %s", m.FooterTop, got)
			}
		})
	}
}

// fullWidthView is a family view of four people: the first three have
// appointments, Mia has none today.
func fullWidthView() familyview.View {
	d := func(h, m int) time.Time { return time.Date(2026, 11, 23, h, m, 0, 0, berlinTZ) }
	people := []familyview.Person{
		{ID: "mama", Name: "Mama", Slot: 1, Shape: "circle-filled"}, {ID: "papa", Name: "Papa", Slot: 2, Shape: "circle-empty"},
		{ID: "seb", Name: "Sebastian", Slot: 3, Shape: "square-filled"}, {ID: "mia", Name: "Mia", Slot: 4, Shape: "square-empty"}}
	var occ []calendar.Occurrence
	for _, p := range people[:3] {
		for i := 0; i < 3; i++ {
			occ = append(occ, calendar.Occurrence{EventID: fmt.Sprintf("%s%d", p.ID, i), CalendarID: "c", Title: fmt.Sprintf("Parents' evening %d of %s", i+1, p.Name),
				Start: d(9+i, 0), End: d(9+i, 45), People: []string{p.ID}})
		}
	}
	v := familyview.Build(familyview.Input{I18n: i18n.MustLoad(), Now: d(8, 0), Lang: "en", Region: "DE-BY", People: people, Occurrences: occ})
	for i := range v.Columns {
		v.Columns[i].Progress = &familyview.Progress{Done: 1, Total: 4}
	}
	return v
}

// Ticket 208, product owner feedback 23/11/2026: on a phone a tap on a person's
// column shows that column alone, as wide as the content; the back button and a
// swipe to the right bring all columns back with that column first in view; a
// refresh keeps the full column; the second tap on the name opens the personal
// view. On wall displays the hints and the bar are hidden and the name opens the
// personal view at once.
func TestFamilyViewFullWidthColumn(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	markup, err := server.RenderFamilyView("en", fullWidthView())
	if err != nil {
		t.Fatal(err)
	}
	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var listeners []chan struct{}
	done := make(chan struct{})
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	// The box announces a new version of the view only when the test asks for it.
	mux.HandleFunc("/display/events", func(w http.ResponseWriter, r *http.Request) {
		ch := make(chan struct{}, 4)
		mu.Lock()
		listeners = append(listeners, ch)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		for {
			select {
			case <-ch:
				fmt.Fprint(w, "event: view\ndata: v2\n\n")
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			case <-done:
				return
			}
		}
	})
	mux.HandleFunc("/trigger", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		for _, ch := range listeners {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	})
	mux.HandleFunc("/display/view", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Hejmo-View", "v2")
		fmt.Fprint(w, markup)
	})
	mux.HandleFunc("/display/person/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!DOCTYPE html><html><body><h1>personal</h1></body></html>`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/static/hejmo.css"></head><body><div class="family-page" data-display data-state="paired" data-poll-seconds="3600" data-version="v1"><div data-view>%s</div></div><script src="/static/display.js" defer></script></body></html>`, markup)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer close(done) // the open event stream would hold srv.Close

	ctx, cancel := context.WithTimeout(startChrome(t, chrome), 2*time.Minute)
	defer cancel()
	// Everything the page shows, as one JSON line. visible(): laid out at all.
	const helpers = `
		const vis = (el) => !!el && getComputedStyle(el).display !== "none" && el.getBoundingClientRect().width > 0;
		const wait = async (f) => { for (let i = 0; i < 100; i++) { if (f()) return true; await new Promise((r) => setTimeout(r, 20)); } return false; };
		const cols = () => [...document.querySelectorAll(".fv .column")];
		const state = () => {
			const g = document.querySelector(".fv-grid");
			const shown = cols().filter(vis);
			return {
				shown: shown.length, full: document.querySelector(".fv").getAttribute("data-full-person"),
				fullbar: vis(document.querySelector(".fv-fullbar")), grow: shown.filter((c) => vis(c.querySelector(".col-hint.grow"))).length,
				toPv: shown.filter((c) => vis(c.querySelector(".col-hint.to-pv"))).length,
				none: cols().filter((c) => vis(c.querySelector(".col-none"))).length,
				width: shown.length ? Math.round(shown[0].getBoundingClientRect().width) : 0, grid: Math.round(g.getBoundingClientRect().width),
				pageWidth: document.documentElement.scrollWidth, innerWidth,
				second: cols()[1].getBoundingClientRect().left - g.getBoundingClientRect().left, canScroll: g.scrollWidth > g.clientWidth + 1,
			};
		};
		const swipe = (dx, dy) => {
			const ev = (n, x, y) => document.dispatchEvent(new PointerEvent(n, { clientX: x, clientY: y, bubbles: true }));
			ev("pointerdown", 200, 300); ev("pointerup", 200 + dx, 300 + dy);
		};`
	probe := func(t *testing.T, body string) map[string]json.RawMessage {
		t.Helper()
		got, err := chromedp.Run(ctx, chromedp.Evaluate[string](`(async () => {`+helpers+body+`})()`, chromedp.EvalAwaitPromise))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(got), &m); err != nil {
			t.Fatalf("%q: %v", got, err)
		}
		return m
	}
	type st struct {
		Shown, Grow, ToPv, None, Width, Grid, PageWidth, InnerWidth int
		Full                                                        *string
		Fullbar, CanScroll                                          bool
		Second                                                      float64
	}
	decode := func(t *testing.T, m map[string]json.RawMessage, key string) st {
		t.Helper()
		var s st
		if err := json.Unmarshal(m[key], &s); err != nil {
			t.Fatalf("%s: %s: %v", key, m[key], err)
		}
		return s
	}
	for _, c := range []struct {
		w, h  int
		phone bool
	}{{375, 812, true}, {375, 667, true}, {812, 375, true}, {1024, 768, false}, {768, 1024, false}} {
		t.Run(fmt.Sprintf("%dx%d", c.w, c.h), func(t *testing.T) {
			if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: int64(c.w), Height: int64(c.h), DeviceScaleFactor: 1}); err != nil {
				t.Fatal(err)
			}
			if err := chromedp.Do(ctx, chromedp.Navigate(srv.URL+"/")); err != nil {
				t.Fatal(err)
			}
			if !c.phone {
				m := probe(t, `
					const before = state();
					const a = cols()[1].querySelector(".person-link");
					const prevented = !a.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
					return JSON.stringify({ before, prevented, bar: vis(document.querySelector(".fv-fullbar")), href: a.getAttribute("href") });`)
				b := decode(t, m, "before")
				if b.Grow != 0 || b.ToPv != 0 || b.Fullbar || b.None != 0 || b.Shown != 4 {
					t.Errorf("wall display shows phone hints: %+v", b)
				}
				if string(m["prevented"]) != "false" || string(m["href"]) != `"/display/person/papa"` {
					t.Errorf("the name must open the personal view: %s %s", m["prevented"], m["href"])
				}
				return
			}
			m := probe(t, `
				const before = state();
				cols()[1].querySelector(".events .title").click();
				const open = state();
				const hrefs = cols().filter(vis).map((c) => c.querySelector(".person-link").getAttribute("href"));
				// A left swipe and a mostly vertical one do nothing.
				swipe(-200, 0); const left = state().full; swipe(200, 150); const diagonal = state().full; swipe(63, 0); const short = state().full;
				document.querySelector("[data-full-close]").click();
				const back = state();
				cols()[1].querySelector(".events .title").click();
				swipe(100, 10);
				const swiped = state();
				// A mouse drag ends in a click on the column under the pointer: it must not reopen it.
				cols()[1].querySelector(".events .title").click();
				const stray = state().full;
				await new Promise((r) => setTimeout(r, 500));
				cols()[3].querySelector(".events").click(); // Mia: nothing planned
				const mia = state();
				document.querySelector("[data-full-close]").click();
				cols()[1].querySelector(".events .title").click();
				document.querySelector(".fv").dataset.old = "1";
				await fetch("/trigger");
				const swapped = await wait(() => !document.querySelector(".fv").dataset.old);
				return JSON.stringify({ before, open, hrefs, left, diagonal, short, back, swiped, stray, mia, swapped, after: state() });`)
			b, o, back, sw, mia, after := decode(t, m, "before"), decode(t, m, "open"), decode(t, m, "back"), decode(t, m, "swiped"), decode(t, m, "mia"), decode(t, m, "after")
			if b.Shown != 4 || b.Grow != 4 || b.Fullbar || b.ToPv != 0 {
				t.Errorf("before a tap: %+v", b)
			}
			if o.Shown != 1 || o.Full == nil || *o.Full != "2" || !o.Fullbar || o.Grow != 0 || o.ToPv != 1 {
				t.Errorf("after a tap on Papa's column: %+v", o)
			}
			if o.Width < 144 || abs(o.Width-o.Grid) > 1 || o.PageWidth > o.InnerWidth {
				t.Errorf("the full column is %d px of %d px content (page %d of %d)", o.Width, o.Grid, o.PageWidth, o.InnerWidth)
			}
			if string(m["hrefs"]) != `["/display/person/papa"]` {
				t.Errorf("the full column's name link: %s", m["hrefs"])
			}
			for _, k := range []string{"left", "diagonal", "short"} {
				if string(m[k]) != `"2"` {
					t.Errorf("%s swipe closed the column: %s", k, m[k])
				}
			}
			if back.Shown != 4 || back.Full != nil || back.Fullbar {
				t.Errorf("back button: %+v", back)
			}
			if back.CanScroll && abs(int(back.Second)) > 2 {
				t.Errorf("Papa's column is not first in view: %.0f px from the left", back.Second)
			}
			if back.Second < -1 || back.Second > float64(back.InnerWidth) {
				t.Errorf("Papa's column is out of view: %.0f px", back.Second)
			}
			if sw.Shown != 4 || sw.Full != nil || (sw.CanScroll && abs(int(sw.Second)) > 2) {
				t.Errorf("right swipe: %+v", sw)
			}
			if string(m["stray"]) != "null" {
				t.Errorf("the click after a closing swipe reopened the column: %s", m["stray"])
			}
			if mia.Shown != 1 || mia.None != 1 {
				t.Errorf("an empty day shows %d notes: %+v", mia.None, mia)
			}
			if string(m["swapped"]) != "true" || after.Shown != 1 || after.Full == nil || *after.Full != "2" || after.Width < 144 {
				t.Errorf("the full column did not survive a refresh: %+v", after)
			}
			// The second tap on the name opens the personal view.
			if _, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`document.querySelector(".column[data-full] .person-link").click(), true`)); err != nil {
				t.Fatal(err)
			}
			path, err := chromedp.Run(ctx, chromedp.Evaluate[string](`(async () => { for (let i = 0; i < 100 && location.pathname === "/"; i++) await new Promise((r) => setTimeout(r, 20)); return location.pathname; })()`, chromedp.EvalAwaitPromise))
			if err != nil {
				t.Fatal(err)
			}
			if path != "/display/person/papa" {
				t.Errorf("the second tap on the name led to %q", path)
			}
		})
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
