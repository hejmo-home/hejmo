package server_test

import (
	"io/fs"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/web"
)

var timeInputRe = regexp.MustCompile(`<input[^>]*type="time"[^>]*>`)

// Ticket 6.3 design pass: every time field is a capsule (input.tcap), so the
// shared picker and the pill look apply to all of them.
func TestEveryTimeFieldIsATimeCapsule(t *testing.T) {
	f := newFamilyBox(t, "eink_bw")
	pages := map[string]string{
		"household":      getEN(f.box, "/settings/household").body,
		"display":        f.get("/displays/" + f.displayID(t)).body,
		"calendar event": getEN(f.box, "/calendar/new").body,
	}
	for name, body := range pages {
		inputs := timeInputRe.FindAllString(body, -1)
		if len(inputs) == 0 {
			t.Errorf("%s: no time field found:\n%s", name, body)
		}
		for _, in := range inputs {
			if !strings.Contains(in, `class="tcap"`) {
				t.Errorf("%s: time field is not a capsule: %s", name, in)
			}
		}
	}
}

func TestPageHandsTheTimeFormatAndPickerTextsToTheScript(t *testing.T) {
	b := signedInBox(t)
	body := b.do("GET", "/settings/household", nil, map[string]string{"Accept-Language": "en"}).body
	for _, want := range []string{`src="/static/form.js" defer data-hour="Hour"`, `data-minute="Minute"`, `data-done="Done" data-time-format="24h"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}})
	if body := b.do("GET", "/calendar", nil, nil).body; !strings.Contains(body, `data-time-format="12h"`) {
		t.Error("the page does not hand the 12-hour format to the script")
	}
	de := b.do("GET", "/settings/household", nil, map[string]string{"Accept-Language": "de"}).body
	for _, want := range []string{`data-hour="Stunde"`, `data-minute="Minute"`, `data-done="Fertig"`} {
		if !strings.Contains(de, want) {
			t.Errorf("German page lacks %q", want)
		}
	}
}

// The picker is a progressive enhancement: the script builds div.tpick below
// the capsule. It keeps the native picker on touch devices and works with the
// 12-hour format.
func TestFormScriptHoldsThePickerAndTheMealPlanBehaviour(t *testing.T) {
	js, err := fs.ReadFile(web.Static, "static/form.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tpick", "aria-expanded", "Escape", "pointer: fine", "timeFormat", "AM", "PM", "done", "requestSubmit", ".mrow textarea", "field-sizing", ".savebar .dirty"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("form.js lacks %q", want)
		}
	}
}

// Frame fixes found while rendering the design pass (and moved on with ticket
// 111): on a phone the navigation must not widen the page, and the "Back to
// Settings" link sits inside main, in the content column, instead of being a
// grid item of the frame that broke the 1024 px layout.
func TestFrameLayoutCoversTheBackLinkAndNarrowScreens(t *testing.T) {
	css, err := fs.ReadFile(web.Static, "static/hejmo.css")
	if err != nil {
		t.Fatal(err)
	}
	c := string(css)
	if !strings.Contains(c, ".frame > * { min-width: 0; }") || !strings.Contains(c, ".frame { grid-template-columns: minmax(0, 1fr); }") {
		t.Error("the frame's children may widen the page on a 375 px phone")
	}
	if nav := regexp.MustCompile(`(?m)^\.frame-nav ul \{([^}]*)\}`).FindStringSubmatch(c); nav == nil || !strings.Contains(nav[1], "flex-wrap: wrap") || strings.Contains(nav[1], "overflow-x") {
		t.Error("the navigation must wrap, not scroll sideways")
	}
	b := signedInBox(t)
	body := b.get("/settings/household").body
	back, main := strings.Index(body, `<a class="back" href="/settings">`), strings.Index(body, `<main id="content">`)
	if back < 0 || main < 0 || back < main {
		t.Errorf("the Back to Settings link must sit inside main, before the content (%d, %d)", back, main)
	}
	if !strings.Contains(body, `class="frame framed has-nav"`) {
		t.Error("the household page is not a frame with navigation")
	}
}
