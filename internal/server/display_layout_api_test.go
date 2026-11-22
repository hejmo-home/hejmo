package server_test

import (
	"net/http"
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
		`{"layout":{"list_id":"nope"}}`, `{"layout":{"blocks":[{"id":"list","enabled":true}]}}`,
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
	if status, _, _ := c.call(http.MethodPost, "/displays/"+b+"/layout", map[string]any{"from": "00000000-0000-0000-0000-000000000000"}); status != http.StatusNotFound {
		t.Errorf("unknown source = %d", status)
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
