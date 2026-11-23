package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// Ticket 296: the REST endpoints of the address book, checked against api/openapi.yaml by the client.

func TestContactsAPIListsPeopleAsContactsAndMakesReadsAndChangesOfOthers(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	mia := c.addPerson(t, "Mia", "child")
	status, _, list := c.api.call(http.MethodGet, "/contacts", nil)
	if status != http.StatusOK || len(list) != 2 {
		t.Fatalf("list = %d %v", status, list)
	}
	first := list[0].(map[string]any)
	if first["name"] != "Mama" || first["person_id"] == nil || len(first["groups"].([]any)) != 1 {
		t.Errorf("Mama's contact = %v", first)
	}
	status, got, _ := c.api.call(http.MethodPost, "/contacts", map[string]any{
		"name": "Oma Gerda", "full_name": "Gerda Müller",
		"phones":   []any{map[string]any{"kind": "cell", "value": " 0171 3456 789 "}, map[string]any{"kind": "custom", "label": "Gartenhaus", "value": "0160 111 2223"}, map[string]any{"kind": "home", "value": ""}},
		"emails":   []any{map[string]any{"kind": "home", "value": "gerda@example.de"}},
		"address":  map[string]any{"street": " ", "city": ""},
		"birthday": map[string]any{"day": 14, "month": 3},
		"note":     "Hat einen Schlüssel.",
	})
	if status != http.StatusCreated || got["person_id"] != nil || got["created_by"] == nil {
		t.Fatalf("create = %d %v", status, got)
	}
	phones := got["phones"].([]any)
	if len(phones) != 2 || phones[0].(map[string]any)["value"] != "0171 3456 789" || phones[1].(map[string]any)["label"] != "Gartenhaus" {
		t.Errorf("phones = %v", phones)
	}
	if b := got["birthday"].(map[string]any); b["year"] != nil || b["day"] != float64(14) {
		t.Errorf("birthday = %v", b)
	}
	id := got["id"].(string)
	if status, _, list := c.api.call(http.MethodGet, "/contacts?q=0171", nil); status != http.StatusOK || len(list) != 1 {
		t.Errorf("search = %d %v", status, list)
	}
	status, got, _ = c.api.call(http.MethodPut, "/contacts/"+id, map[string]any{"name": "Oma Gerda", "phones": []any{}, "groups": []any{}})
	if status != http.StatusOK || len(got["phones"].([]any)) != 0 || got["full_name"] != "" || got["updated_by"] == nil {
		t.Errorf("replace = %d %v", status, got)
	}
	if status, _, _ := c.api.call(http.MethodDelete, "/contacts/"+id, nil); status != http.StatusNoContent {
		t.Errorf("delete = %d", status)
	}
	if status, _, _ := c.api.call(http.MethodGet, "/contacts/"+id, nil); status != http.StatusNotFound {
		t.Errorf("deleted = %d", status)
	}
	// A person's contact keeps its name and Familie and cannot be deleted.
	status, got, _ = c.api.call(http.MethodPut, "/contacts/"+mia, map[string]any{"name": "Anders", "full_name": "Mia Maier"})
	if status != http.StatusOK || got["name"] != "Mia" || len(got["groups"].([]any)) != 1 {
		t.Errorf("person's contact = %d %v", status, got)
	}
	if status, _, _ := c.api.call(http.MethodDelete, "/contacts/"+mia, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("deleting a person's contact = %d", status)
	}
}

func TestContactsAPIRefusesWhatTheFormRefuses(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	c.add(t, contactForm("Kinderarzt", "", nil, nil))
	many := []any{}
	for i := 0; i < 11; i++ {
		many = append(many, map[string]any{"kind": "cell", "value": "0171 000"})
	}
	for what, body := range map[string]map[string]any{
		"taken, ignoring case":     {"name": "kinderarzt"},
		"a person's name":          {"name": "MAMA"},
		"no name":                  {"name": " "},
		"41 characters":            {"name": strings.Repeat("x", 41)},
		"bad number":               {"name": "A", "phones": []any{map[string]any{"kind": "cell", "value": "089 23x4"}}},
		"two digits":               {"name": "A", "phones": []any{map[string]any{"kind": "cell", "value": "12"}}},
		"custom without a label":   {"name": "A", "phones": []any{map[string]any{"kind": "custom", "value": "0171 123"}}},
		"unknown kind":             {"name": "A", "phones": []any{map[string]any{"kind": "pager", "value": "0171 123"}}},
		"no dot in the domain":     {"name": "A", "emails": []any{map[string]any{"kind": "home", "value": "a@example"}}},
		"eleven numbers":           {"name": "A", "phones": many},
		"31 February":              {"name": "A", "birthday": map[string]any{"day": 31, "month": 2, "year": 2018}},
		"29 February, common year": {"name": "A", "birthday": map[string]any{"day": 29, "month": 2, "year": 2025}},
		"year 1899":                {"name": "A", "birthday": map[string]any{"day": 1, "month": 1, "year": 1899}},
		"future":                   {"name": "A", "birthday": map[string]any{"day": 1, "month": 1, "year": 2999}},
		"long note":                {"name": "A", "note": strings.Repeat("n", 1001)},
		"unknown group":            {"name": "A", "groups": []any{"nope"}},
	} {
		if status, got, _ := c.api.call(http.MethodPost, "/contacts", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s: status = %d %v, want 422", what, status, got)
		}
	}
	// A request that is no JSON is 400; the child's session gets 403 even to read.
	if status, _, _ := c.api.call(http.MethodGet, "/contacts/nope", nil); status != http.StatusNotFound {
		t.Errorf("unknown id = %d", status)
	}
}

func TestContactsAPIIsForParentsOnly(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	kid := childSession(t, b)
	api := newAPIClient(t, kid)
	api.cookie = true
	for _, call := range []struct{ method, path string }{{http.MethodGet, "/contacts"}, {http.MethodGet, "/contact-groups"}} {
		if status, _, _ := api.call(call.method, call.path, nil); status != http.StatusForbidden {
			t.Errorf("child %s %s = %d, want 403", call.method, call.path, status)
		}
	}
	if status, _, _ := api.call(http.MethodPost, "/contacts", map[string]any{"name": "X"}); status != http.StatusForbidden {
		t.Errorf("child POST = %d, want 403", status)
	}
}

func TestContactGroupsAPIHasTheFiveGroupsAndOwnGroupsCanBeMadeRenamedAndDeleted(t *testing.T) {
	t.Parallel()
	c := newCtBox(t)
	status, _, list := c.api.call(http.MethodGet, "/contact-groups", nil)
	if status != http.StatusOK || len(list) != 5 {
		t.Fatalf("groups = %d %v", status, list)
	}
	var keys []string
	for _, g := range list {
		keys = append(keys, g.(map[string]any)["shipped_key"].(string))
	}
	if strings.Join(keys, ",") != "familie,kinderbetreuung_schule,arztpraxen,notfall,sonstige" {
		t.Errorf("keys = %v", keys)
	}
	if fam := list[0].(map[string]any); len(fam["contacts"].([]any)) != 1 {
		t.Errorf("Familie = %v", fam)
	}
	status, g, _ := c.api.call(http.MethodPost, "/contact-groups", map[string]any{"name": "Fußball-Eltern"})
	if status != http.StatusCreated || g["name"] != "Fußball-Eltern" || g["shipped_key"] != nil {
		t.Fatalf("create = %d %v", status, g)
	}
	id := g["id"].(string)
	for _, name := range []string{"fußball-eltern", "Familie", "Emergency", " "} {
		if status, _, _ := c.api.call(http.MethodPost, "/contact-groups", map[string]any{"name": name}); status != http.StatusUnprocessableEntity {
			t.Errorf("group %q = %d, want 422", name, status)
		}
	}
	status, got, _ := c.api.call(http.MethodPost, "/contacts", map[string]any{"name": "Anna", "groups": []any{id}})
	if status != http.StatusCreated || len(got["groups"].([]any)) != 1 {
		t.Fatalf("contact in the group = %d %v", status, got)
	}
	if status, g, _ := c.api.call(http.MethodPatch, "/contact-groups/"+id, map[string]any{"name": "Fußball U12"}); status != http.StatusOK || g["name"] != "Fußball U12" || len(g["contacts"].([]any)) != 1 {
		t.Errorf("rename = %d %v", status, g)
	}
	fam := list[0].(map[string]any)["id"].(string)
	if status, _, _ := c.api.call(http.MethodPatch, "/contact-groups/"+fam, map[string]any{"name": "X"}); status != http.StatusUnprocessableEntity {
		t.Errorf("renaming a group of Hejmo = %d", status)
	}
	if status, _, _ := c.api.call(http.MethodDelete, "/contact-groups/"+fam, nil); status != http.StatusUnprocessableEntity {
		t.Errorf("deleting a group of Hejmo = %d", status)
	}
	if status, _, _ := c.api.call(http.MethodDelete, "/contact-groups/"+id, nil); status != http.StatusNoContent {
		t.Errorf("delete = %d", status)
	}
	if _, got, _ := c.api.call(http.MethodGet, "/contacts/"+got["id"].(string), nil); len(got["groups"].([]any)) != 0 {
		t.Errorf("the contact kept the deleted group: %v", got)
	}
}
