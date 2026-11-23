package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
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
