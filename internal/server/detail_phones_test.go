package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPhonesOverviewListsRowsThatOpenTheDetailPage(t *testing.T) {
	b := signedInBox(t)
	pairedPhone(t, b, personNamed(t, b, "Mama"), "Mama's phone")
	body := getEN(b, "/phones").body
	id := phoneIDRe.FindStringSubmatch(body)
	if id == nil {
		t.Fatalf("no row links to a phone page\n%s", body)
	}
	for _, want := range []string{`<ul class="list-rows">`, "Mama&#39;s phone", "For ", `class="marker"`, "Mama", "Last seen "} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	noDestructiveForm(t, "/phones", body)
}

func TestPhoneDetailShowsFactsInWordsAndConfirmsRemovingByName(t *testing.T) {
	b := signedInBox(t)
	pairedPhone(t, b, personNamed(t, b, "Mama"), "Mama's phone")
	pairedPhone(t, b, personNamed(t, b, "Mia"), "Mia's phone")
	h, _ := b.db.HomeHousehold()
	phones, _ := b.db.Phones(h.ID)
	rights := map[string]string{"Mama's phone": "See and change everything", "Mia's phone": "See everything, only tick off own tasks and list items"}
	for _, ph := range phones {
		r := getEN(b, "/phones/"+ph.ID)
		if r.status != http.StatusOK {
			t.Fatalf("GET = %d", r.status)
		}
		name := strings.ReplaceAll(ph.Name, "'", "&#39;")
		for _, want := range []string{
			`<nav class="crumbs" aria-label="Breadcrumb">`, `<a href="/phones">Phones</a>`, "<h1>" + name + "</h1>", "<dt>For</dt>", "<dt>Allowed</dt>", rights[ph.Name],
			"<dt>Paired on</dt>", "<dt>Last seen</dt>", `<details class="danger">`, "Remove phone", `action="/phones/` + ph.ID + `/revoke"`,
			"Yes, remove “" + name + "”", "No, change nothing",
		} {
			if !strings.Contains(r.body, want) {
				t.Errorf("%s: detail lacks %q\n%s", ph.Name, want, r.body)
			}
		}
	}
	if c := b.get("/phones/nope"); c.status != http.StatusNotFound {
		t.Errorf("unknown phone = %d", c.status)
	}
}

func TestRemovingAPhoneNamesItInTheSuccessLine(t *testing.T) {
	b := signedInBox(t)
	pairedPhone(t, b, personNamed(t, b, "Mama"), "Old phone")
	id := phoneIDRe.FindStringSubmatch(b.get("/phones").body)[1]
	if r := b.post("/phones/"+id+"/revoke", url.Values{}); r.status != http.StatusSeeOther || r.location != "/phones" {
		t.Fatalf("revoke = %d %q", r.status, r.location)
	}
	if body := getEN(b, "/phones").body; !strings.Contains(body, "“Old phone” removed.") {
		t.Errorf("success line misses the name\n%s", body)
	}
}
