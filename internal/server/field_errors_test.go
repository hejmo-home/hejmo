package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/auth"
)

// inputTag returns the opening <input ...> tag with the given name, or "".
func inputTag(page, name string) string {
	re := regexp.MustCompile(`<input [^>]*name="` + regexp.QuoteMeta(name) + `"[^>]*>`)
	return re.FindString(page)
}

// formOf returns the <form> of a page that posts to action, from its opening
// tag to </form>. A renamed action fails the test instead of silently
// checking the whole page.
func formOf(t *testing.T, page, action string) string {
	t.Helper()
	i := strings.Index(page, `action="`+action+`"`)
	if i < 0 {
		t.Fatalf("no form with action %q on the page:\n%s", action, page)
	}
	form := page[i:]
	if j := strings.Index(form, "</form>"); j >= 0 {
		form = form[:j]
	}
	return form
}

// ADR 0017, ticket 5.16: a form error is shown at its field; the other
// entries stay filled in, passwords never come back.
func TestFormErrorsAppearAtTheFieldAndKeepOtherEntries(t *testing.T) {
	t.Parallel()
	tb := newTaskBox(t)
	b := tb.parent.b
	b.feeds.set("https://example.org/a.ics", feedOf(), nil)

	cases := []struct {
		name   string
		path   string
		form   url.Values
		status int
		field  string            // the field that carries the error
		kept   map[string]string // other entries that must stay filled in
	}{
		{"task without title", "/tasks", url.Values{"person": {tb.miaID}, "title": {" "}, "emoji": {"🪥"}, "repeat": {"daily"}, "date": {"2026-11-14"}},
			http.StatusUnprocessableEntity, "title", map[string]string{"emoji": "🪥", "date": "2026-11-14"}},
		{"once task without date", "/tasks", url.Values{"person": {tb.miaID}, "title": {"Read"}, "emoji": {"📖"}, "repeat": {"once"}},
			http.StatusUnprocessableEntity, "date", map[string]string{"title": "Read", "emoji": "📖"}},
		{"calendar with a bad address", "/calendars", url.Values{"name": {"Holidays"}, "url": {"not an address"}, "color": {"#B4583A"}, "interval": {"60"}},
			http.StatusUnprocessableEntity, "url", map[string]string{"name": "Holidays", "url": "not an address"}},
		{"calendar without name", "/calendars", url.Values{"name": {""}, "url": {"https://example.org/a.ics"}, "color": {"#B4583A"}, "interval": {"60"}},
			http.StatusUnprocessableEntity, "name", map[string]string{"url": "https://example.org/a.ics"}},
		{"token without name", "/caldav/tokens", url.Values{"name": {""}}, http.StatusUnprocessableEntity, "name", nil},
		{"display with a wrong code", "/displays", url.Values{"code": {"000000"}, "name": {"Hallway"}, "type": {"eink_bw"}},
			http.StatusUnprocessableEntity, "code", map[string]string{"name": "Hallway", "code": "000000"}},
	}
	for _, c := range cases {
		r := b.do(http.MethodPost, c.path, c.form, map[string]string{"Accept-Language": "en"})
		if r.status != c.status {
			t.Errorf("%s: status = %d, want %d", c.name, r.status, c.status)
			continue
		}
		// The calendars page also holds the connection form (a "url" input
		// of its own); look only at the form this case posted to.
		page := formOf(t, r.body, c.path)
		tag := inputTag(page, c.field)
		if !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `aria-describedby="err-`+c.field) {
			t.Errorf("%s: field %q is not marked invalid: %q", c.name, c.field, tag)
		}
		if !strings.Contains(r.body, `id="err-`+c.field+`"`) || strings.Count(r.body, `class="ferr"`) != 1 {
			t.Errorf("%s: want exactly one error message with id err-%s", c.name, c.field)
		}
		if strings.Contains(r.body, `<p class="error"`) {
			t.Errorf("%s: error also shown as a generic message above the form", c.name)
		}
		for name, value := range c.kept {
			if !strings.Contains(inputTag(page, name), `value="`+value+`"`) {
				t.Errorf("%s: %q does not keep %q: %q", c.name, name, value, inputTag(page, name))
			}
		}
		for name := range c.form {
			if name != c.field && inputTag(page, name) != "" && strings.Contains(inputTag(page, name), "aria-invalid") {
				t.Errorf("%s: %q is marked invalid too", c.name, name)
			}
		}
	}
}

func TestSetupErrorsAppearAtTheFieldAndNeverReturnPasswords(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	form := validSetupForm()
	form.Set("password", "short")
	r := b.do(http.MethodPost, "/setup", form, map[string]string{"Accept-Language": "en"})
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	if tag := inputTag(r.body, "password"); !strings.Contains(tag, `aria-invalid="true"`) || strings.Contains(tag, "short") {
		t.Errorf("password field = %q", tag)
	}
	// The picked place stays confirmed (its id travels along).
	if !strings.Contains(inputTag(r.body, "place_id"), `value="`+idPasing+`"`) || !strings.Contains(r.body, "<b>Pasing</b>") {
		t.Errorf("the picked place is lost: %q", inputTag(r.body, "place_id"))
	}
	for name, value := range map[string]string{"name": "Mama", "username": "mama"} {
		if !strings.Contains(inputTag(r.body, name), `value="`+value+`"`) {
			t.Errorf("%s not kept: %q", name, inputTag(r.body, name))
		}
	}
	if strings.Contains(r.body, "2468") {
		t.Error("the PIN came back in the form")
	}
	form = validSetupForm()
	form.Set("pin", "12")
	if r := b.post("/setup", form); !strings.Contains(inputTag(r.body, "pin"), `aria-invalid="true"`) {
		t.Errorf("pin field = %q", inputTag(r.body, "pin"))
	}
}

func TestSetupCodeErrorAppearsAtTheCodeField(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	b.post("/setup", validSetupForm())
	r := b.post("/setup/totp", url.Values{"code": {"000000"}})
	if r.status != http.StatusUnprocessableEntity || !strings.Contains(inputTag(r.body, "code"), `aria-invalid="true"`) {
		t.Fatalf("code error = %d %q", r.status, inputTag(r.body, "code"))
	}
}

func TestSignInErrorKeepsTheUsernameButNotThePassword(t *testing.T) {
	t.Parallel()
	b, secret := configuredBox(t)
	code, _ := auth.TOTPCode(secret, b.now)
	r := b.post("/login", url.Values{"username": {"mama"}, "password": {"wrong password"}, "code": {code}})
	if r.status != http.StatusUnauthorized {
		t.Fatalf("status = %d", r.status)
	}
	if !strings.Contains(inputTag(r.body, "username"), `value="mama"`) {
		t.Errorf("username = %q", inputTag(r.body, "username"))
	}
	if tag := inputTag(r.body, "password"); !strings.Contains(tag, `aria-invalid="true"`) || strings.Contains(r.body, "wrong password") {
		t.Errorf("password field = %q", tag)
	}
}
