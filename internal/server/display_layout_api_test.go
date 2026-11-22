package server_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// 180 (ADR 0015: API parity with the web form): the layout of a display through
// the API, contract-checked against openapi.yaml.
func TestTheLayoutOfADisplayWorksThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	_, _, people := c.call(http.MethodGet, "/people", nil)
	mama := people[0].(map[string]any)["id"].(string)
	code := codeOn(t, newScreen(c.b).get("/display", "en").body)
	_, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Kitchen", "type": "screen"})
	id := shown["id"].(string)

	l, _ := shown["layout"].(map[string]any)
	if l == nil || l["whole_family"] != true || l["rotate_daily"] != false || l["orientation"] != "landscape" || l["list_id"] != nil {
		t.Fatalf("default layout = %v", shown["layout"])
	}
	blocks, _ := l["blocks"].([]any)
	var got []string
	for _, b := range blocks {
		m := b.(map[string]any)
		got = append(got, m["id"].(string)+":"+m["zone"].(string)+":"+map[bool]string{true: "on", false: "off"}[m["enabled"].(bool)])
	}
	want := "clock:header:on weather:header:on events:columns:on routines:columns:off tasks:columns:on meals:footer:on tomorrow:footer:on list:footer:off"
	if j := join(got); j != want {
		t.Fatalf("blocks = %s", j)
	}

	status, patched, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"layout": map[string]any{
		"whole_family": false, "people": []string{mama}, "rotate_daily": true,
		"blocks": []map[string]any{{"id": "weather", "enabled": false}, {"id": "routines", "enabled": true}},
	}})
	if status != http.StatusOK {
		t.Fatalf("patch = %d %v", status, patched)
	}
	l = patched["layout"].(map[string]any)
	if l["whole_family"] != false || l["rotate_daily"] != true || len(l["people"].([]any)) != 1 {
		t.Fatalf("patched = %v", l)
	}
	first := l["blocks"].([]any)[0].(map[string]any)
	if first["id"] != "weather" || first["enabled"] != false {
		t.Errorf("the order of the blocks sent is kept: %v", l["blocks"])
	}
	// Only the fields sent change.
	_, again, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"layout": map[string]any{"rotate_daily": false}})
	l = again["layout"].(map[string]any)
	if l["rotate_daily"] != false || l["whole_family"] != false {
		t.Errorf("partial = %v", l)
	}
	for _, bad := range []string{
		`{"layout":{"people":[]}}`, `{"layout":{"people":["nobody"]}}`, `{"layout":{"blocks":[{"id":"sparkle","enabled":true}]}}`,
		`{"layout":{"list_id":"nope"}}`,
	} {
		if status := rawCall(t, c, http.MethodPatch, "/displays/"+id, bad); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", bad, status)
		}
	}
}

// Q84: copy a layout from another display, through the API.
func TestCopyingALayoutWorksThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	pair := func(name string) string {
		code := codeOn(t, newScreen(c.b).get("/display", "en").body)
		_, d, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": name, "type": "screen"})
		return d["id"].(string)
	}
	a, b := pair("Kitchen"), pair("Hall")
	_, _, people := c.call(http.MethodGet, "/people", nil)
	mama := people[0].(map[string]any)["id"].(string)
	c.call(http.MethodPatch, "/displays/"+a, map[string]any{"layout": map[string]any{"whole_family": false, "people": []string{mama}}})
	status, got, _ := c.call(http.MethodPost, "/displays/"+b+"/layout", map[string]any{"from": a})
	if status != http.StatusOK {
		t.Fatalf("copy = %d %v", status, got)
	}
	l := got["layout"].(map[string]any)
	if l["whole_family"] != false || len(l["people"].([]any)) != 1 || got["name"] != "Hall" {
		t.Errorf("copied = %v", got)
	}
	status, errBody, _ := c.call(http.MethodPost, "/displays/"+b+"/layout", map[string]any{"from": "00000000-0000-0000-0000-000000000000"})
	if status != http.StatusNotFound || !strings.Contains(fmt.Sprint(errBody["error"]), "from") {
		t.Errorf("unknown source = %d %v, want 404 naming the from display", status, errBody)
	}
	if status, _, _ := c.call(http.MethodPost, "/displays/"+b+"/layout", map[string]any{"from": b}); status != http.StatusUnprocessableEntity {
		t.Errorf("a display copying itself = %d, want 422", status)
	}
}

// The block "one list" needs a list to show.
func TestTheListBlockNeedsAList(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	code := codeOn(t, newScreen(c.b).get("/display", "en").body)
	_, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Kitchen", "type": "screen"})
	id := shown["id"].(string)
	_, _, lists := c.call(http.MethodGet, "/lists", nil)
	for _, l := range lists {
		c.call(http.MethodDelete, "/lists/"+l.(map[string]any)["id"].(string), nil)
	}
	if status := rawCall(t, c, http.MethodPatch, "/displays/"+id, `{"layout":{"blocks":[{"id":"list","enabled":true}]}}`); status != http.StatusUnprocessableEntity {
		t.Errorf("list block without a list = %d, want 422", status)
	}
	_, made, _ := c.call(http.MethodPost, "/lists", map[string]any{"name": "Packing", "color": "green", "symbol": "square-filled"})
	status, got, _ := c.call(http.MethodPatch, "/displays/"+id, map[string]any{"layout": map[string]any{"blocks": []map[string]any{{"id": "list", "enabled": true}}}})
	if status != http.StatusOK || got["layout"].(map[string]any)["list_id"] != made["id"] {
		t.Errorf("with a list the block takes it: %d %v", status, got["layout"])
	}
}

// An explicit empty list_id removes the choice; the block "list" then has no list
// unless it is switched on in the same change.
func TestRemovingTheListOfAnActiveListBlockIsRefused(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	code := codeOn(t, newScreen(c.b).get("/display", "en").body)
	_, shown, _ := c.call(http.MethodPost, "/displays", map[string]any{"code": code, "name": "Kitchen", "type": "screen"})
	id := shown["id"].(string)
	_, made, _ := c.call(http.MethodPost, "/lists", map[string]any{"name": "Packing", "color": "green", "symbol": "square-filled"})
	c.call(http.MethodPatch, "/displays/"+id, map[string]any{"layout": map[string]any{"list_id": made["id"], "blocks": []map[string]any{{"id": "list", "enabled": true}}}})
	if status := rawCall(t, c, http.MethodPatch, "/displays/"+id, `{"layout":{"list_id":""}}`); status != http.StatusUnprocessableEntity {
		t.Errorf("removing the list of an active block = %d, want 422", status)
	}
}

func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out
}
