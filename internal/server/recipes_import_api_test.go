package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/outbound"
)

// Ticket 289 (ADR 0015): the import of a recipe from a web page over the REST API, contract-checked against openapi.yaml.

func importAPI(t *testing.T) (*apiClient, *httptest.Server) {
	t.Helper()
	jpeg := testJPEG(t, 600, 400)
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pic.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(jpeg)
		case "/lentils":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(ldPage(lentilLD)))
		case "/none":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(ldPage(`{"@type":"Article"}`)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(web.Close)
	return signedInAPI(t, withRecipeFetcher(loopbackRecipes()), withFetcher(loopbackFetcher())), web
}

func TestAPIReadsAPageIntoADraftWithProposalsAndSavesItWithChoices(t *testing.T) {
	t.Parallel()
	c, web := importAPI(t)
	status, d, _ := c.call(http.MethodPost, "/recipe-imports", map[string]any{"url": web.URL + "/lentils"})
	if status != http.StatusCreated {
		t.Fatalf("POST /recipe-imports = %d %v", status, d)
	}
	id := d["id"].(string)
	rec := d["recipe"].(map[string]any)
	ings := rec["ingredients"].([]any)
	if rec["name"] != "Rote Linsensuppe mit Harissa" || rec["portions"] != float64(4) || rec["portions_status"] != "stated" || rec["category"] != "main" || rec["category_word"] != "Suppe" || rec["has_picture"] != true || len(ings) != 5 {
		t.Fatalf("draft = %v", rec)
	}
	first, harissa := ings[0].(map[string]any), ings[2].(map[string]any)
	if first["status"] != "needs_choice" || first["original"] != "250 g rote Linsen" || first["quantity"] != float64(250) || len(first["proposals"].([]any)) == 0 {
		t.Errorf("first line = %v", first)
	}
	if harissa["change"] != "range" || harissa["quantity"] != float64(1) {
		t.Errorf("a range takes the smaller number: %v", harissa)
	}
	if onion := ings[1].(map[string]any); onion["status"] != "library" || onion["article_id"] != "onions" {
		t.Errorf("onion = %v", onion)
	}
	if salt := ings[4].(map[string]any); salt["quantity"] != nil || salt["change"] != "some" {
		t.Errorf("salt = %v", salt)
	}
	if nut := rec["nutrition"].(map[string]any); nut["kcal"] != float64(380) || nut["salt"] != nil {
		t.Errorf("nutrition = %v", nut)
	}
	if status, again, _ := c.call(http.MethodGet, "/recipe-imports/"+id, nil); status != http.StatusOK || again["id"] != id {
		t.Errorf("GET draft = %d", status)
	}
	body := map[string]any{"name": "Linsensuppe", "portions": 4, "minutes": 35, "tags": []string{"Suppe"}, "steps": []string{"Kochen."},
		"ingredients": []any{
			map[string]any{"quantity": 250, "unit": "g", "name": "Linsen", "article_id": "lentils", "variety": "rot"},
			map[string]any{"quantity": 1, "name": "Zwiebel"},
			map[string]any{"quantity": 1, "unit": "tsp", "name": "Harissa"},
			map[string]any{"quantity": 150, "unit": "g", "name": "Naturjoghurt"},
		}}
	status, bad, _ := c.call(http.MethodPost, "/recipe-imports/"+id, body)
	if status != http.StatusUnprocessableEntity || bad["field"] != "ingredients[2].choice" {
		t.Fatalf("saving without a choice = %d %v", status, bad)
	}
	body["ingredients"].([]any)[2].(map[string]any)["choice"] = "own"
	body["ingredients"].([]any)[3].(map[string]any)["choice"] = "free"
	status, saved, _ := c.call(http.MethodPost, "/recipe-imports/"+id, body)
	if status != http.StatusCreated || saved["source"] != web.URL+"/lentils" || saved["source_url"] != web.URL+"/lentils" {
		t.Fatalf("save = %d %v", status, saved)
	}
	sings := saved["ingredients"].([]any)
	if sings[2].(map[string]any)["article_id"] == nil || sings[3].(map[string]any)["article_id"] != nil {
		t.Errorf("own article and free text: %v", sings)
	}
	// the source never changes
	put := map[string]any{"name": "Linsensuppe", "source": "https://elsewhere.example/", "portions": 4}
	if status, upd, _ := c.call(http.MethodPut, "/recipes/"+saved["id"].(string), put); status != http.StatusOK || upd["source"] != web.URL+"/lentils" {
		t.Errorf("PUT = %d %v", status, upd)
	}
	if status, _, _ := c.call(http.MethodGet, "/recipe-imports/"+id, nil); status != http.StatusNotFound {
		t.Errorf("the draft stays after saving: %d", status)
	}
}

func TestAPIRefusesWhatCannotBeRead(t *testing.T) {
	t.Parallel()
	c, web := importAPI(t)
	for url, field := range map[string]string{"": "url", "kein link": "url", web.URL + "/none": "url", web.URL + "/missing": "url"} {
		status, d, _ := c.call(http.MethodPost, "/recipe-imports", map[string]any{"url": url})
		if status != http.StatusUnprocessableEntity || d["field"] != field {
			t.Errorf("%q = %d %v", url, status, d)
		}
	}
	if status, d, _ := c.call(http.MethodPost, "/recipe-imports", map[string]any{"url": web.URL + "/none"}); !strings.Contains(d["error"].(string), "no recipe") {
		t.Errorf("no recipe: %d %v", status, d)
	}
	if status, _, _ := c.call(http.MethodGet, "/recipe-imports/unknown", nil); status != http.StatusNotFound {
		t.Errorf("unknown draft = %d", status)
	}
	if err := c.b.db.SetServiceEnabled(outbound.Recipes, false); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := c.call(http.MethodPost, "/recipe-imports", map[string]any{"url": web.URL + "/lentils"}); status != http.StatusConflict {
		t.Errorf("switched off = %d, want 409", status)
	}
	c.b.db.SetServiceEnabled(outbound.Recipes, true)
	c.b.db.SetOffline(true, c.b.now)
	if status, _, _ := c.call(http.MethodPost, "/recipe-imports", map[string]any{"url": web.URL + "/lentils"}); status != http.StatusConflict {
		t.Errorf("offline = %d, want 409", status)
	}
}

func TestAPIDraftExpiresAndCanBeDiscarded(t *testing.T) {
	t.Parallel()
	c, web := importAPI(t)
	_, d, _ := c.call(http.MethodPost, "/recipe-imports", map[string]any{"url": web.URL + "/lentils"})
	id := d["id"].(string)
	if status, _, _ := c.call(http.MethodDelete, "/recipe-imports/"+id, nil); status != http.StatusNoContent {
		t.Errorf("discard = %d", status)
	}
	_, d, _ = c.call(http.MethodPost, "/recipe-imports", map[string]any{"url": web.URL + "/lentils"})
	c.b.now = c.b.now.Add(61 * time.Minute)
	if status, _, _ := c.call(http.MethodGet, "/recipe-imports/"+d["id"].(string), nil); status != http.StatusGone {
		t.Errorf("an old draft = %d, want 410", status)
	}
}
