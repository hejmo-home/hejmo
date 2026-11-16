package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/store"
)

// taskBox is a signed-in parent (Mama) with a child, Mia, who has her own API
// token (children cannot sign in, ADR 0015).
type taskBox struct {
	parent, child *apiClient
	miaID, mamaID string
}

func newTaskBox(t *testing.T) taskBox {
	t.Helper()
	c := signedInAPI(t)
	_, me, _ := c.call(http.MethodGet, "/me", nil)
	_, mia, _ := c.call(http.MethodPost, "/people", map[string]any{"name": "Mia", "role": "child"})
	const secret = "hejmo_test_child_token"
	if _, err := c.b.db.CreateAPIToken(store.APIToken{Name: "Mia", Hash: auth.HashToken(secret), PersonID: mia["id"].(string), Scopes: "write"}); err != nil {
		t.Fatal(err)
	}
	return taskBox{parent: c, child: c.withToken(secret), miaID: mia["id"].(string), mamaID: me["id"].(string)}
}

func (tb taskBox) newDaily(t *testing.T, person string) string {
	t.Helper()
	status, k, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": person, "title": "Brush teeth", "emoji": "🪥", "repeat": "daily"})
	if status != http.StatusCreated {
		t.Fatalf("create = %d %v", status, k)
	}
	return k["id"].(string)
}

func taskState(t *testing.T, c *apiClient, id, date string) map[string]any {
	t.Helper()
	path := "/tasks"
	if date != "" {
		path += "?date=" + date
	}
	status, _, list := c.call(http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d", status)
	}
	for _, x := range list {
		if m := x.(map[string]any); m["id"] == id {
			return m
		}
	}
	t.Fatalf("task %s not listed: %v", id, list)
	return nil
}

func TestParentCreatesTasksOfEveryRepetition(t *testing.T) {
	tb := newTaskBox(t)
	status, k, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "Piano", "emoji": "🎹", "repeat": "weekly", "weekdays": []string{"MO", "WE"}, "part_of_day": "evening"})
	if status != http.StatusCreated || k["repeat"] != "weekly" || k["part_of_day"] != "evening" || k["date"] != "2026-11-14" || k["due"] != false {
		t.Fatalf("weekly = %d %v", status, k) // 14/11/2026 is a Saturday
	}
	status, k, _ = tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "Library", "repeat": "once", "date": "2026-11-14"})
	if status != http.StatusCreated || k["due"] != true || k["done"] != false {
		t.Fatalf("once = %d %v", status, k)
	}
}

func TestTaskInputIsValidated(t *testing.T) {
	tb := newTaskBox(t)
	for name, body := range map[string]map[string]any{
		"no title":      {"person_id": tb.miaID, "title": " ", "repeat": "daily"},
		"unknown ppl":   {"person_id": "00000000-0000-4000-8000-000000000000", "title": "x", "repeat": "daily"},
		"once no date":  {"person_id": tb.miaID, "title": "x", "repeat": "once"},
		"weekly no day": {"person_id": tb.miaID, "title": "x", "repeat": "weekly"},
		"double day":    {"person_id": tb.miaID, "title": "x", "repeat": "weekly", "weekdays": []string{"MO", "MO"}},
	} {
		if status, _, _ := tb.parent.call(http.MethodPost, "/tasks", body); status != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, status)
		}
	}
}

// The contract client refuses these before they are sent, so the server's own
// limits are checked with raw requests.
func TestTaskLimitsOfTheContractAreEnforced(t *testing.T) {
	tb := newTaskBox(t)
	for name, body := range map[string]string{
		"long title": `{"person_id":"` + tb.miaID + `","title":"` + strings.Repeat("x", 500) + `","repeat":"daily"}`,
		"long emoji": `{"person_id":"` + tb.miaID + `","title":"x","emoji":"` + strings.Repeat("🪥", 17) + `","repeat":"daily"}`,
	} {
		if got := rawCall(t, tb.parent, http.MethodPost, "/tasks", body); got != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d, want 422", name, got)
		}
	}
}

// Acceptance: Given a daily task for Mia, when it is ticked off today, then it
// is open again tomorrow.
func TestDailyTaskTickedOffTodayIsOpenAgainTomorrow(t *testing.T) {
	tb := newTaskBox(t)
	id := tb.newDaily(t, tb.miaID)
	status, k, _ := tb.child.call(http.MethodPut, "/tasks/"+id+"/done", map[string]any{"done": true})
	if status != http.StatusOK || k["done"] != true {
		t.Fatalf("tick = %d %v", status, k)
	}
	if got := taskState(t, tb.parent, id, ""); got["done"] != true {
		t.Fatalf("today = %v", got)
	}
	tb.parent.b.now = tb.parent.b.now.Add(24 * time.Hour)
	if got := taskState(t, tb.parent, id, ""); got["done"] != false || got["due"] != true {
		t.Fatalf("tomorrow = %v, want open again", got)
	}
	// Ticking off for today and unticking again.
	tb.parent.b.now = tb.parent.b.now.Add(-24 * time.Hour)
	if status, k, _ := tb.child.call(http.MethodPut, "/tasks/"+id+"/done", map[string]any{"done": false}); status != http.StatusOK || k["done"] != false {
		t.Fatalf("untick = %d %v", status, k)
	}
}

// Acceptance: Given a child, when they tick off their task, then it is
// allowed; deleting it is refused.
func TestChildMayTickOffOwnTaskButNotDeleteOrCreate(t *testing.T) {
	tb := newTaskBox(t)
	own, other := tb.newDaily(t, tb.miaID), tb.newDaily(t, tb.mamaID)
	if status, _, _ := tb.child.call(http.MethodPut, "/tasks/"+own+"/done", map[string]any{"done": true}); status != http.StatusOK {
		t.Fatalf("own tick = %d", status)
	}
	if status, _, _ := tb.child.call(http.MethodDelete, "/tasks/"+own, nil); status != http.StatusForbidden {
		t.Fatalf("child delete = %d, want 403", status)
	}
	if got := taskState(t, tb.parent, own, ""); got["done"] != true {
		t.Fatal("refused delete must keep the task")
	}
	if status, _, _ := tb.child.call(http.MethodPut, "/tasks/"+other+"/done", map[string]any{"done": true}); status != http.StatusForbidden {
		t.Fatalf("tick of another's task = %d, want 403", status)
	}
	if status, _, _ := tb.child.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "x", "repeat": "daily"}); status != http.StatusForbidden {
		t.Fatalf("child create = %d, want 403", status)
	}
	if status, _, list := tb.child.call(http.MethodGet, "/tasks", nil); status != http.StatusOK || len(list) != 2 {
		t.Fatalf("child read = %d %v", status, list)
	}
}

// Acceptance: Given a parent on the go, when they confirm a child's task, then
// it is stored as done.
func TestParentConfirmsAChildsTask(t *testing.T) {
	tb := newTaskBox(t)
	id := tb.newDaily(t, tb.miaID)
	if status, k, _ := tb.parent.call(http.MethodPut, "/tasks/"+id+"/done", map[string]any{"done": true}); status != http.StatusOK || k["done"] != true {
		t.Fatalf("confirm = %d %v", status, k)
	}
	if got := taskState(t, tb.child, id, ""); got["done"] != true {
		t.Fatalf("child sees %v", got)
	}
}

func TestTickRefusesTasksNotDueTodayAndUnknownTasks(t *testing.T) {
	tb := newTaskBox(t)
	_, k, _ := tb.parent.call(http.MethodPost, "/tasks", map[string]any{"person_id": tb.miaID, "title": "Later", "repeat": "once", "date": "2026-11-20"})
	if status, _, _ := tb.parent.call(http.MethodPut, "/tasks/"+k["id"].(string)+"/done", map[string]any{"done": true}); status != http.StatusUnprocessableEntity {
		t.Fatalf("not due = %d, want 422", status)
	}
	if status, _, _ := tb.parent.call(http.MethodPut, "/tasks/00000000-0000-4000-8000-000000000000/done", map[string]any{"done": true}); status != http.StatusNotFound {
		t.Fatalf("unknown = %d, want 404", status)
	}
}

func TestParentDeletesTasksAndFiltersByPerson(t *testing.T) {
	tb := newTaskBox(t)
	a, b := tb.newDaily(t, tb.miaID), tb.newDaily(t, tb.mamaID)
	if status, _, list := tb.parent.call(http.MethodGet, "/tasks?person="+tb.miaID, nil); status != http.StatusOK || len(list) != 1 || list[0].(map[string]any)["id"] != a {
		t.Fatalf("filter = %d %v", status, list)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/tasks/"+b, nil); status != http.StatusNoContent {
		t.Fatalf("delete = %d", status)
	}
	if status, _, _ := tb.parent.call(http.MethodDelete, "/tasks/"+b, nil); status != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", status)
	}
}

// rawCall sends a body without the contract client, which would refuse it.
func rawCall(t *testing.T, c *apiClient, method, path, body string) int {
	t.Helper()
	status, _ := rawCallBody(t, c, method, path, body)
	return status
}

// rawCallBody is rawCall that also returns the answer body.
func rawCallBody(t *testing.T, c *apiClient, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, c.b.srv.URL+"/api/v1"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	client := c.b.bareClient()
	if c.cookie {
		req.Header.Set("Origin", c.b.srv.URL)
		client = c.b.client
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(out)
}

func TestMalformedJSONIsA400OnBothTaskWrites(t *testing.T) {
	tb := newTaskBox(t)
	id := tb.newDaily(t, tb.miaID)
	if got := rawCall(t, tb.parent, http.MethodPost, "/tasks", "{"); got != http.StatusBadRequest {
		t.Errorf("create = %d, want 400", got)
	}
	if got := rawCall(t, tb.parent, http.MethodPut, "/tasks/"+id+"/done", "{"); got != http.StatusBadRequest {
		t.Errorf("done = %d, want 400", got)
	}
	if got := rawCall(t, tb.parent, http.MethodPut, "/tasks/"+id+"/done", "{}"); got != http.StatusUnprocessableEntity {
		t.Errorf("done missing = %d, want 422", got)
	}
}

func TestTaskListRefusesAPersonThatIsNoUUID(t *testing.T) {
	tb := newTaskBox(t)
	if got := rawCall(t, tb.parent, http.MethodGet, "/tasks?person=mia", ""); got != http.StatusUnprocessableEntity {
		t.Errorf("person=mia = %d, want 422", got)
	}
}

// Acceptance (#122): PATCH /tasks/{id} changes the star value, nothing else.
func TestPatchTaskChangesTheStarValueOnly(t *testing.T) {
	tb := newTaskBox(t)
	id := tb.newDaily(t, tb.miaID)
	status, got, _ := tb.parent.call(http.MethodPatch, "/tasks/"+id, map[string]any{"stars": 4})
	if status != http.StatusOK || got["stars"] != float64(4) || got["title"] != "Brush teeth" || got["id"] != id {
		t.Fatalf("patch = %d %v", status, got)
	}
	if st := taskState(t, tb.parent, id, ""); st["stars"] != float64(4) {
		t.Fatalf("stored = %v", st)
	}
	status, got, _ = tb.parent.call(http.MethodPatch, "/tasks/"+id, map[string]any{"stars": 0})
	if status != http.StatusOK || got["stars"] != float64(0) {
		t.Fatalf("patch 0 = %d %v", status, got)
	}
}

func TestPatchTaskRefusesBadBodiesAndUnknownTasks(t *testing.T) {
	tb := newTaskBox(t)
	id := tb.newDaily(t, tb.miaID)
	// Well-formed but invalid values: 422 with the field, like POST /tasks. The
	// contract client would refuse some of these, so they go out raw.
	for name, body := range map[string]string{
		"too many": `{"stars":101}`, "negative": `{"stars":-1}`, "missing": `{}`, "quoted number": `{"stars":"3"}`, "fraction": `{"stars":2.5}`, "null": `{"stars":null}`,
	} {
		status, out := rawCallBody(t, tb.parent, http.MethodPatch, "/tasks/"+id, body)
		if status != http.StatusUnprocessableEntity || !strings.Contains(out, `"field":"stars"`) {
			t.Errorf("%s = %d %s, want 422 naming the field stars", name, status, out)
		}
	}
	// Bad JSON and unknown fields stay 400.
	for name, body := range map[string]string{
		"other field": `{"stars":2,"title":"x"}`, "unknown field": `{"colour":"red"}`, "bad json": `{`,
		"array body": `[1]`, "string body": `"x"`, "wrong type then other field": `{"title":"x","stars":"3"}`,
	} {
		if got := rawCall(t, tb.parent, http.MethodPatch, "/tasks/"+id, body); got != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, got)
		}
	}
	// POST /tasks answers the same value the same way.
	if got := rawCall(t, tb.parent, http.MethodPost, "/tasks", `{"person_id":"`+tb.miaID+`","title":"x","repeat":"daily","stars":101}`); got != http.StatusUnprocessableEntity {
		t.Errorf("POST stars=101 = %d, want 422", got)
	}
	if st := taskState(t, tb.parent, id, ""); st["stars"] != float64(1) {
		t.Fatalf("a refused patch changed the value: %v", st)
	}
	if status, _, _ := tb.parent.call(http.MethodPatch, "/tasks/00000000-0000-0000-0000-000000000000", map[string]any{"stars": 2}); status != http.StatusNotFound {
		t.Errorf("unknown = %d", status)
	}
	if status, _, _ := tb.child.call(http.MethodPatch, "/tasks/"+id, map[string]any{"stars": 2}); status != http.StatusForbidden {
		t.Errorf("child = %d, want 403", status)
	}
}
