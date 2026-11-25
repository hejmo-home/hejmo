package server_test

import (
	"fmt"
	"math"
	"net/url"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 298 (review of PR #316): on a phone the head of a contact page stacks photo, name, full name,
// relations and photo link in one column. A contact without relations has an empty "rels" row; with a
// row-gap that row still added two gaps, so the photo link sat 24 px below the full name. The space
// between the lines is 10 px, whatever lines are missing, so the stylesheet is measured, not read.
const contactHeadProbe = `(() => {
  const head = document.querySelector(".ct-head");
  const box = s => { const e = head.querySelector(s); return e ? e.getBoundingClientRect() : null; };
  const h1 = box("h1"), full = box(".ct-full"), rels = box(".ct-rels"), link = box(".ph-link");
  return JSON.stringify({h1: h1 && [h1.top, h1.bottom], full: full && [full.top, full.bottom], rels: rels && [rels.top, rels.bottom], link: link && [link.top, link.bottom]});
})()`

func TestTheContactHeadOnAPhoneHasTenPixelsBetweenItsLinesWhateverIsMissing(t *testing.T) {
	t.Parallel()
	chrome := requireChrome(t)
	f := newLayoutFixture(t)
	by := f.ids[0]
	var ids [3]string
	for i, in := range []store.ContactInput{
		{HejmoName: "Praxis Dr. Yılmaz", FullName: "Kinderarztpraxis Dr. Elif Yılmaz", Numbers: []contact.Line{{Kind: contact.Work, Value: "089 1234 5670"}}},
		{HejmoName: "Bereitschaftsdienst", Numbers: []contact.Line{{Kind: contact.Work, Value: "089 1234 5671"}}},
		{HejmoName: "Oma Gerda", FullName: "Gerda Müller", Numbers: []contact.Line{{Kind: contact.Cell, Value: "0171 3456 789"}},
			Relations: []store.Relation{{Word: "Oma", PersonIDs: []string{f.ids[2]}}}},
	} {
		c, err := f.db.CreateContact(f.householdID, by, in)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = c.ID
	}
	site := siteOf("app", f.box, f.client)
	ctx := startChrome(t, chrome)
	base, _ := url.Parse(site.chromeBase())
	for _, c := range site.cookies {
		if _, err := chromedp.Call(ctx, network.SetCookie, network.SetCookieParams{Name: c.Name, Value: c.Value, URL: base.String()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{Width: 375, Height: 812, DeviceScaleFactor: 1}); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"full name, no relations", "no full name, no relations", "full name and a relation"} {
		t.Run(name, func(t *testing.T) {
			if err := chromedp.Do(ctx, chromedp.Navigate(site.chromeBase()+"/contacts/"+ids[i])); err != nil {
				t.Fatal(err)
			}
			got, err := chromedp.Run(ctx, chromedp.Evaluate[string](contactHeadProbe))
			if err != nil {
				t.Fatal(err)
			}
			var m map[string][]float64
			if err := jsonUnmarshal(got, &m); err != nil {
				t.Fatal(err)
			}
			// The lines present, top to bottom; between each two neighbours there are 10 px.
			var prev []float64
			prevName := ""
			for _, k := range []string{"h1", "full", "rels", "link"} {
				cur := m[k]
				if cur == nil {
					continue
				}
				if prev != nil {
					if gap := cur[0] - prev[1]; math.Abs(gap-10) > 0.5 {
						t.Errorf("%s: the gap between %s and %s is %.1f px, want 10 (%s)", name, prevName, k, gap, fmt.Sprint(m))
					}
				}
				prev, prevName = cur, k
			}
			if m["h1"] == nil || m["link"] == nil {
				t.Errorf("%s: the head lacks h1 or the photo link: %v", name, m)
			}
		})
	}
}
