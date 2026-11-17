package server_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
)

// Ticket 164 (ADR 0019, ADR 0015: HTML routes with API parity): the article
// library over the REST API, contract-checked against openapi.yaml.

func newArticleAPI(t *testing.T) (*apiClient, string) {
	t.Helper()
	c := signedInAPI(t, withFetcher(loopbackFetcher()))
	_, mia, _ := c.call(http.MethodPost, "/people", map[string]any{"name": "Mia", "role": "child"})
	return c, mia["id"].(string)
}

// putBytes sends a request with a binary body, checked against the contract like call.
func (c *apiClient) putBytes(path, contentType string, body []byte) (int, map[string]any) {
	t := c.b.t
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, c.b.srv.URL+"/api/v1"+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.cookie {
		req.Header.Set("Origin", c.b.srv.URL)
	}
	route, params, err := c.router.FindRoute(req)
	if err != nil {
		t.Fatalf("PUT %s is not in openapi.yaml: %v", path, err)
	}
	in := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route,
		Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
	if err := openapi3filter.ValidateRequest(context.Background(), in); err != nil {
		t.Fatalf("request violates contract: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	client := c.b.bareClient()
	if c.cookie {
		client = c.b.client
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: res.StatusCode, Header: res.Header, Options: &openapi3filter.Options{IncludeResponseStatus: true}}
	out.SetBodyBytes(raw)
	if err := openapi3filter.ValidateResponse(context.Background(), out); err != nil {
		t.Fatalf("response %d violates contract: %v\n%s", res.StatusCode, err, raw)
	}
	var obj map[string]any
	decodeJSON(t, string(raw), &obj)
	return res.StatusCode, obj
}

func TestTheLibraryListsShippedAndOwnArticlesAndSearchesByName(t *testing.T) {
	t.Parallel()
	c, _ := newArticleAPI(t)
	status, _, list := c.call(http.MethodGet, "/articles", nil)
	if status != http.StatusOK || len(list) != 248 {
		t.Fatalf("list = %d, %d articles", status, len(list))
	}
	status, a, _ := c.call(http.MethodGet, "/articles/peanut-butter", nil)
	if status != http.StatusOK || a["source"] != "hejmo" || a["name_de"] != "Erdnussbutter" || a["name_en"] != "Peanut butter" || a["dept"] != "pantry" || a["kind"] != "vegan" {
		t.Errorf("peanut butter = %d %v", status, a)
	}
	if units := a["units"].([]any); len(units) != 2 || units[0] != "piece" || units[1] != "g" {
		t.Errorf("units = %v", units)
	}
	_, _, found := c.call(http.MethodGet, "/articles?q=MUSLI", nil) // ü = u
	if len(found) != 1 {
		t.Errorf("musli = %d", len(found))
	}
	if status, _, _ := c.call(http.MethodGet, "/articles/nothing", nil); status != http.StatusNotFound {
		t.Errorf("unknown = %d", status)
	}
}

func TestAnOwnArticleIsCreatedWithASymbolChangedAndDeleted(t *testing.T) {
	t.Parallel()
	c, _ := newArticleAPI(t)
	status, a, _ := c.call(http.MethodPost, "/articles", map[string]any{"name": " Katzenfutter  Huhn ", "dept": "other", "kind": "nonfood", "allergens": []string{"milk"}, "unit1": "pack", "symbol": "🐈"})
	if status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, a)
	}
	id := a["id"].(string)
	if a["source"] != "own" || a["name_de"] != "Katzenfutter Huhn" || a["picture_source"] != "symbol" || a["symbol"] != "🐈" || a["picture"] != nil || len(a["allergens"].([]any)) != 0 || a["created_by"] == nil {
		t.Errorf("created = %v", a)
	}
	status, a, _ = c.call(http.MethodPatch, "/articles/"+id, map[string]any{"name": "Katzenfutter Rind", "unit2": "kg", "kind": "veg"})
	if status != http.StatusOK || a["name_de"] != "Katzenfutter Rind" || a["kind"] != "veg" || a["units"].([]any)[1] != "kg" || a["symbol"] != "🐈" {
		t.Errorf("patch = %d %v", status, a)
	}
	if status, _, _ := c.call(http.MethodDelete, "/articles/"+id, nil); status != http.StatusNoContent {
		t.Errorf("delete = %d", status)
	}
	if status, _, _ := c.call(http.MethodGet, "/articles/"+id, nil); status != http.StatusNotFound {
		t.Errorf("after delete = %d", status)
	}
}

func TestAnArticleThatBreaksTheRulesIsRefusedWithItsField(t *testing.T) {
	t.Parallel()
	c, _ := newArticleAPI(t)
	for name, body := range map[string]map[string]any{
		"no name":         {"dept": "other", "kind": "veg", "unit1": "piece"},
		"no kind":         {"name": "x", "dept": "other", "unit1": "piece"},
		"unit off":        {"name": "x", "kind": "veg", "unit1": "lb"},
		"same unit twice": {"name": "x", "kind": "veg", "unit1": "g", "unit2": "g"},
		"bad symbol":      {"name": "x", "kind": "veg", "unit1": "g", "symbol": "abc"},
		"both pictures":   {"name": "x", "kind": "veg", "unit1": "g", "symbol": "🐈", "picture_url": "https://example.org/a.jpg"},
	} {
		if status, _, _ := c.call(http.MethodPost, "/articles", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
	status, body, _ := c.call(http.MethodPost, "/articles", map[string]any{"name": "x", "dept": "other", "unit1": "piece"})
	if status != http.StatusUnprocessableEntity || body["field"] != "kind" {
		t.Errorf("no kind names its field: %v", body)
	}
	if _, _, list := c.call(http.MethodGet, "/articles", nil); len(list) != 248 {
		t.Errorf("a refused article was stored (%d)", len(list))
	}
}

func TestAPictureFromTheWebIsDownloadedOnceAndAPrivateAddressIsRefused(t *testing.T) {
	t.Parallel()
	srv := imageServer(t, 1200, 1200)
	c, _ := newArticleAPI(t)
	status, a, _ := c.call(http.MethodPost, "/articles", map[string]any{"name": "Hafer-Drink", "dept": "drinks", "kind": "vegan", "unit1": "l", "picture_url": srv.URL + "/a.jpg"})
	if status != http.StatusCreated || a["picture_source"] != "web" || a["picture"] == nil {
		t.Fatalf("create = %d %v", status, a)
	}
	if raw, st, ctype := c.callRaw(http.MethodGet, a["picture"].(string), nil); st != http.StatusOK || ctype != "image/jpeg" || !strings.HasPrefix(raw, "\xff\xd8") {
		t.Errorf("picture = %d %q", st, ctype)
	}
	// The page of the web address names nothing about it any more.
	for _, bad := range []string{"http://192.168.178.20/x.jpg", "http://169.254.169.254/latest/meta-data/", "ftp://example.org/x.jpg", srv.URL + "/page"} {
		c2 := signedInAPI(t) // the real fetcher: public addresses only
		status, body, _ := c2.call(http.MethodPost, "/articles", map[string]any{"name": "y", "kind": "veg", "unit1": "piece", "picture_url": bad})
		if status != http.StatusUnprocessableEntity || body["field"] != "picture_url" {
			t.Errorf("%s = %d %v", bad, status, body)
		}
	}
	c3 := signedInAPI(t, withFetcher(loopbackFetcher()))
	status, body, _ := c3.call(http.MethodPost, "/articles", map[string]any{"name": "y", "kind": "veg", "unit1": "piece", "picture_url": srv.URL + "/page"})
	if status != http.StatusUnprocessableEntity || !strings.Contains(body["error"].(string), "web page") {
		t.Errorf("web page = %d %v", status, body)
	}
}

func TestAnInvalidArticleCausesNoDownload(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	c := signedInAPI(t, withFetcher(loopbackFetcher()))
	for name, body := range map[string]map[string]any{
		"blank name":       {"name": "   ", "kind": "veg", "unit1": "piece", "picture_url": srv.URL + "/a.jpg"},
		"symbol and photo": {"name": "y", "kind": "veg", "unit1": "piece", "symbol": "🍎", "picture_url": srv.URL + "/a.jpg"},
	} {
		if status, _, _ := c.call(http.MethodPost, "/articles", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d", name, status)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d downloads for requests that were refused anyway", n)
	}
}

func TestAPhotoIsSentAsTheBodyAndComesBackScaledDown(t *testing.T) {
	t.Parallel()
	c, _ := newArticleAPI(t)
	_, a, _ := c.call(http.MethodPost, "/articles", map[string]any{"name": "Windeln", "dept": "baby", "kind": "nonfood", "unit1": "pack"})
	id := a["id"].(string)
	status, got := c.putBytes("/articles/"+id+"/picture", "image/jpeg", testJPEG(t, 2000, 1000))
	if status != http.StatusOK || got["picture_source"] != "upload" || got["picture"] == nil {
		t.Fatalf("put = %d %v", status, got)
	}
	if status, _ := c.putBytes("/articles/"+id+"/picture", "image/png", []byte("not a picture")); status != http.StatusUnprocessableEntity {
		t.Errorf("not a picture = %d", status)
	}
	if status, _ := c.putBytes("/articles/"+id+"/picture", "image/jpeg", append([]byte{0xff, 0xd8}, make([]byte, 16<<20)...)); status != http.StatusUnprocessableEntity {
		t.Errorf("over 15 MB = %d", status)
	}
	_, b, _ := c.call(http.MethodPatch, "/articles/"+id, map[string]any{"remove_picture": true})
	if b["picture"] != nil || b["picture_source"] != "" {
		t.Errorf("removed = %v", b)
	}
}

func TestShippedArticlesAreHiddenOrCopiedNeverChanged(t *testing.T) {
	t.Parallel()
	c, _ := newArticleAPI(t)
	status, a, _ := c.call(http.MethodPut, "/articles/walnuts/hidden", map[string]any{"hidden": true})
	if status != http.StatusOK || a["hidden_by_hand"] != true {
		t.Errorf("hide = %d %v", status, a)
	}
	_, a, _ = c.call(http.MethodPut, "/articles/walnuts/hidden", map[string]any{"hidden": false})
	if a["hidden_by_hand"] != false {
		t.Errorf("show = %v", a)
	}
	if status, _, _ := c.call(http.MethodPatch, "/articles/walnuts", map[string]any{"name": "x"}); status != http.StatusUnprocessableEntity {
		t.Errorf("patch shipped = %d", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/articles/walnuts", nil); status != http.StatusUnprocessableEntity {
		t.Errorf("delete shipped = %d", status)
	}
	status, cp, _ := c.call(http.MethodPost, "/articles/peanut-butter/copy", nil)
	if status != http.StatusCreated || cp["source"] != "own" || cp["dept"] != "pantry" || cp["units"].([]any)[1] != "g" {
		t.Errorf("copy = %d %v", status, cp)
	}
	_, orig, _ := c.call(http.MethodGet, "/articles/peanut-butter", nil)
	if orig["hidden_by_hand"] != true {
		t.Error("the original is not hidden after the copy")
	}
	if status, _, _ := c.call(http.MethodPut, "/articles/"+cp["id"].(string)+"/hidden", map[string]any{"hidden": true}); status != http.StatusUnprocessableEntity {
		t.Errorf("hiding an own article = %d", status)
	}
}

func TestWarningsNameThePeopleAndAreHealthDataForTheHealthScopeOnly(t *testing.T) {
	t.Parallel()
	c, mia := newArticleAPI(t)
	c.call(http.MethodPut, "/people/"+mia+"/food", map[string]any{"diet": "vegetarian", "allergens": []string{"peanuts", "nuts"}, "other": []string{"Kiwi"}})
	status, w, _ := c.call(http.MethodGet, "/articles/nut-nougat-spread/warnings", nil)
	if status != http.StatusOK {
		t.Fatalf("warnings = %d %v", status, w)
	}
	ws := w["warnings"].([]any)
	if len(ws) != 1 || ws[0].(map[string]any)["people"].([]any)[0] != mia || ws[0].(map[string]any)["allergens"].([]any)[0] != "nuts" {
		t.Errorf("warnings = %v", ws)
	}
	if w["suggested"] != true || !strings.Contains(w["note"].(string), "nicht filterbar") {
		t.Errorf("answer = %v", w)
	}
	_, w, _ = c.call(http.MethodGet, "/articles/salmon-fillet/warnings", nil)
	if w["suggested"] != true || len(w["does_not_suit"].([]any)) != 0 {
		// Mia eats vegetarian: salmon does not suit her.
		if len(w["does_not_suit"].([]any)) != 1 || w["does_not_suit"].([]any)[0] != mia {
			t.Errorf("salmon = %v", w)
		}
	}
	for _, scopes := range []string{"read", "write", "lists", "lists:read settings"} {
		tok := c.withToken(c.newToken(scopes))
		for _, path := range []string{"/articles/nut-nougat-spread/warnings", "/library/hiding"} {
			if status, _, _ := tok.call(http.MethodGet, path, nil); status != http.StatusForbidden {
				t.Errorf("scopes %q read %s: %d", scopes, path, status)
			}
		}
		// Article data without who is allergic is not health data.
		if status, _, _ := tok.call(http.MethodGet, "/articles", nil); scopes != "settings" && (strings.Contains(scopes, "lists") || scopes == "read" || scopes == "write") && status != http.StatusOK {
			t.Errorf("scopes %q cannot read the articles: %d", scopes, status)
		}
	}
	if status, _, _ := c.withToken(c.newToken("health:read")).call(http.MethodGet, "/articles/nut-nougat-spread/warnings", nil); status != http.StatusOK {
		t.Errorf("health:read = %d", status)
	}
}

func TestChildrenReadTheLibraryButChangeNothing(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	if status, _, _ := tb.child.call(http.MethodGet, "/articles", nil); status != http.StatusOK {
		t.Errorf("child list = %d", status)
	}
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/articles", map[string]any{"name": "x", "kind": "veg", "unit1": "piece"}},
		{http.MethodPut, "/articles/walnuts/hidden", map[string]any{"hidden": true}},
		{http.MethodPost, "/articles/walnuts/copy", nil},
		{http.MethodGet, "/articles/walnuts/warnings", nil},
		{http.MethodGet, "/library/hiding", nil},
		{http.MethodPut, "/library/hiding", map[string]any{"enabled": false}},
	} {
		if status, _, _ := tb.child.call(r.method, r.path, r.body); status != http.StatusForbidden {
			t.Errorf("child %s %s = %d, want 403", r.method, r.path, status)
		}
	}
}

func TestHidingNeedsConfirmationAndRecordsWhoConfirmedAndWhen(t *testing.T) {
	t.Parallel()
	c, mia := newArticleAPI(t)
	c.call(http.MethodPut, "/people/"+mia+"/food", map[string]any{"diet": "omnivore", "allergens": []string{"peanuts"}})
	_, h, _ := c.call(http.MethodGet, "/library/hiding", nil)
	if h["enabled"] != false || h["experimental"] != true || h["confirmed_by"] != nil {
		t.Errorf("default = %v", h)
	}
	status, body, _ := c.call(http.MethodPut, "/library/hiding", map[string]any{"enabled": true})
	if status != http.StatusUnprocessableEntity || body["field"] != "confirm" || !strings.Contains(body["error"].(string), "no liability") {
		t.Errorf("without confirm = %d %v", status, body)
	}
	status, h, _ = c.call(http.MethodPut, "/library/hiding", map[string]any{"enabled": true, "confirm": true, "people": []string{mia}})
	if status != http.StatusOK || h["enabled"] != true || h["everyone"] != false || h["confirmed_by"] == nil || h["confirmed_at"] == nil {
		t.Fatalf("on = %d %v", status, h)
	}
	_, w, _ := c.call(http.MethodGet, "/articles/peanut-butter/warnings", nil)
	if w["suggested"] != false || w["not_suggested_because"] != "allergy" || w["hidden_for"].([]any)[0] != mia {
		t.Errorf("hidden = %v", w)
	}
	if status, _, _ := c.call(http.MethodPut, "/library/hiding", map[string]any{"enabled": true, "confirm": true, "people": []string{"00000000-0000-4000-8000-000000000000"}}); status != http.StatusUnprocessableEntity {
		t.Errorf("unknown person = %d", status)
	}
	_, h, _ = c.call(http.MethodPut, "/library/hiding", map[string]any{"enabled": false})
	if h["enabled"] != false || h["confirmed_by"] == nil {
		t.Errorf("off = %v", h)
	}
}
