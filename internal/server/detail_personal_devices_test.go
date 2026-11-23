package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPersonalDevicesOverviewListsRowsThatOpenTheDetailPage(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	pairedPersonalDevice(t, b, personNamed(t, b, "Mama"), "Mama's tablet")
	body := getEN(b, "/personal-devices").body
	id := personalDeviceIDRe.FindStringSubmatch(body)
	if id == nil {
		t.Fatalf("no row links to a personal device page\n%s", body)
	}
	for _, want := range []string{`<ul class="list-rows">`, "Mama&#39;s tablet", "For ", `class="marker"`, "Mama", "Last seen "} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	noDestructiveForm(t, "/personal-devices", body)
}

func TestPersonalDeviceDetailShowsFactsInWordsAndConfirmsRemovingByName(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	pairedPersonalDevice(t, b, personNamed(t, b, "Mama"), "Mama's tablet")
	pairedPersonalDevice(t, b, personNamed(t, b, "Mia"), "Mia's tablet")
	h, _ := b.db.HomeHousehold()
	personalDevices, _ := b.db.PersonalDevices(h.ID)
	rights := map[string]string{"Mama's tablet": "See and change everything", "Mia's tablet": "See everything, only tick off own tasks and list items"}
	for _, ph := range personalDevices {
		r := getEN(b, "/personal-devices/"+ph.ID)
		if r.status != http.StatusOK {
			t.Fatalf("GET = %d", r.status)
		}
		name := strings.ReplaceAll(ph.Name, "'", "&#39;")
		for _, want := range []string{
			`<nav class="crumbs" aria-label="Breadcrumb">`, `<a href="/personal-devices">Personal devices</a>`, "<h1>" + name + "</h1>", "<dt>For</dt>", "<dt>Allowed</dt>", rights[ph.Name],
			"<dt>Paired on</dt>", "<dt>Last seen</dt>", `<details class="danger">`, "Remove personal device", `action="/personal-devices/` + ph.ID + `/revoke"`,
			"Yes, remove “" + name + "”", "No, change nothing",
		} {
			if !strings.Contains(r.body, want) {
				t.Errorf("%s: detail lacks %q\n%s", ph.Name, want, r.body)
			}
		}
	}
	if c := b.get("/personal-devices/nope"); c.status != http.StatusNotFound {
		t.Errorf("unknown personal device = %d", c.status)
	}
}

func TestRemovingAPersonalDeviceNamesItInTheSuccessLine(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	pairedPersonalDevice(t, b, personNamed(t, b, "Mama"), "Old tablet")
	id := personalDeviceIDRe.FindStringSubmatch(b.get("/personal-devices").body)[1]
	if r := b.post("/personal-devices/"+id+"/revoke", url.Values{}); r.status != http.StatusSeeOther || r.location != "/personal-devices" {
		t.Fatalf("revoke = %d %q", r.status, r.location)
	}
	if body := getEN(b, "/personal-devices").body; !strings.Contains(body, "“Old tablet” removed.") {
		t.Errorf("success line misses the name\n%s", body)
	}
}
