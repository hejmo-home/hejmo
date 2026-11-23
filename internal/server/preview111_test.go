package server_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/caldavclient"
	"github.com/hejmo-home/hejmo/internal/calsync"
	"github.com/hejmo-home/hejmo/internal/i18n"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/web"
)

// TestWritePreview111 writes the self-contained preview page for the product
// owner's approval of ticket 111 (detail pages for displays, device tokens,
// calendar connections, subscribed calendars and personal devices, ADR 0017). Every
// frame is a real page of the box, rendered by the real templates in German
// for the eight-person family, at 1024 x 768, 768 x 1024 and 375 px wide, in
// light, dark and black-and-white.
// Run: HEJMO_WRITE_PREVIEW=../../docs/design/preview-111.html go test ./internal/server -run WritePreview111
func TestWritePreview111(t *testing.T) {
	t.Parallel()
	out := os.Getenv("HEJMO_WRITE_PREVIEW")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW to the output path to write the preview")
	}
	css, _ := web.Static.ReadFile("static/hejmo.css")
	formJS, _ := web.Static.ReadFile("static/form.js")
	copyJS, _ := web.Static.ReadFile("static/token-copy.js")
	font, _ := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")

	de := map[string]string{"Accept-Language": "de"}
	// The box's clock: 15/11/2026, 07:42:30 in Munich.
	local, _ := time.LoadLocation("Europe/Berlin")
	moment := func(day, h, m, s int) string {
		return time.Date(2026, 11, day, h, m, s, 0, local).UTC().Format(time.RFC3339)
	}

	b := signedInBox(t)
	b.now = time.Date(2026, 11, 15, 7, 42, 30, 0, local)
	h, _ := b.db.HomeHousehold()
	for _, p := range []struct{ name, role string }{{"Papa", "parent"}, {"Sebastian", "child"}, {"Mia", "child"}, {"Jonas", "child"}, {"Lotta", "child"}, {"Ben", "child"}, {"Emma", "child"}} {
		if _, err := b.db.AddPerson(h.ID, store.Person{Name: p.name, Role: store.Role(p.role)}); err != nil {
			t.Fatal(err)
		}
	}
	people, _ := b.db.People(h.ID)
	personID := map[string]string{}
	for _, p := range people {
		personID[p.Name] = p.ID
	}
	sql := func(query string, args ...any) {
		t.Helper()
		if _, err := b.db.SQL().Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	page := func(path string) string {
		t.Helper()
		r := b.do(http.MethodGet, path, nil, de)
		if r.status != http.StatusOK && r.status != http.StatusUnprocessableEntity && r.status != http.StatusBadGateway {
			t.Fatalf("%s = %d\n%s", path, r.status, r.body)
		}
		return r.body
	}
	post := func(path string, form url.Values, want int) resp {
		t.Helper()
		r := b.do(http.MethodPost, path, form, de)
		if r.status != want {
			t.Fatalf("POST %s = %d, want %d\n%s", path, r.status, want, r.body)
		}
		return r
	}

	// ---- displays ----
	kitchen := pairedDisplay(t, b, "Küche", "eink_color")
	nursery := pairedDisplay(t, b, "Kinderzimmer Sebastian und Jonas (oben, neben dem Hochbett)", "eink_bw")
	hall := pairedDisplay(t, b, "Flur", "screen")
	sql(`UPDATE displays SET paired_at = ?, last_seen_at = ? WHERE id = ?`, moment(2, 18, 0, 0), moment(15, 7, 42, 13), kitchen)
	sql(`UPDATE displays SET paired_at = ?, last_seen_at = ? WHERE id = ?`, moment(2, 18, 5, 0), moment(14, 21, 5, 44), nursery)
	sql(`UPDATE displays SET paired_at = ?, last_seen_at = ? WHERE id = ?`, moment(3, 9, 0, 0), moment(15, 7, 41, 58), hall)
	displaysOverview := page("/displays")
	kitchenPage := page("/displays/" + kitchen)
	post("/displays/"+nursery+"/settings", url.Values{"show_tomorrow": {"on"}}, http.StatusSeeOther)
	nurserySaved := page("/displays/" + nursery)
	bad := post("/displays/"+kitchen+"/settings", url.Values{"show_tomorrow": {"on"}, "quiet": {"on"}, "quiet_from": {"21:00"}, "quiet_to": {"21:00"}, "kiosk_url": {"192.168.1.20:8080"}, "theme": {"auto"}, "sunrise_offset": {"0"}, "sunset_offset": {"0"}}, http.StatusUnprocessableEntity)
	displayUnpair := kitchenPage

	// A box with a single display, unpaired: success line and empty state.
	fresh := signedInBox(t)
	only := pairedDisplay(t, fresh, "Flur", "screen")
	fresh.do(http.MethodPost, "/displays/"+only+"/unpair", url.Values{}, de)
	displaysEmpty := fresh.do(http.MethodGet, "/displays", nil, de).body

	// ---- device tokens ----
	tokenIDs := map[string]string{}
	for _, tk := range []struct{ name, owner string }{{"iPhone Mama", "Mama"}, {"iPad Pro im Arbeitszimmer von Papa (Kalender-App)", "Papa"}, {"DAVx5 auf dem Pixel", "Papa"}} {
		id, _ := newDeviceToken(t, b, tk.name)
		tokenIDs[tk.name] = id
		sql(`UPDATE api_tokens SET person_id = ?, created_at = ? WHERE id = ?`, personID[tk.owner], moment(4, 19, 20, 11), id)
	}
	sql(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, moment(15, 7, 12, 40), tokenIDs["iPhone Mama"])
	sql(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, moment(14, 22, 31, 5), tokenIDs["iPad Pro im Arbeitszimmer von Papa (Kalender-App)"])
	tokensOverview := page("/caldav")
	ipad := page("/caldav/tokens/" + tokenIDs["iPad Pro im Arbeitszimmer von Papa (Kalender-App)"])
	davx := page("/caldav/tokens/" + tokenIDs["DAVx5 auf dem Pixel"])
	once := b.do(http.MethodPost, "/caldav/tokens", url.Values{"name": {"Handy Charlotte-Sophie"}}, de)
	secret := deviceTokenRe.FindStringSubmatch(once.body)[1]
	onceBody := strings.ReplaceAll(once.body, secret, "hjm_7QeK2rXv9LmP4tWz8bNc3Ys6")

	tokenFresh := signedInBox(t)
	lastID, _ := newDeviceToken(t, tokenFresh, "iPhone Mama")
	tokenFresh.do(http.MethodPost, "/caldav/tokens/"+lastID+"/revoke", url.Values{}, de)
	tokensEmpty := tokenFresh.do(http.MethodGet, "/caldav", nil, de).body

	// ---- connections and subscribed calendars ----
	icloudBase, nextBase := "https://p12-caldav.icloud.com/1234/calendars/", "https://cloud.familie-maier.de/remote.php/dav/calendars/papa/"
	cal := func(base, slug string) string { return base + slug + "/" }
	remotes := map[string]previewRemote{
		"https://caldav.icloud.com": {found: []caldavclient.Calendar{
			{URL: cal(icloudBase, "family"), Name: "Familie"}, {URL: cal(icloudBase, "school"), Name: "Schule Mia"}, {URL: cal(icloudBase, "football"), Name: "Fußball Sebastian"},
			{URL: cal(icloudBase, "birthdays"), Name: "Geburtstage"}, {URL: cal(icloudBase, "reminders"), Name: "Erinnerungen"}},
			counts: map[string]int{cal(icloudBase, "family"): 214, cal(icloudBase, "school"): 38, cal(icloudBase, "football"): 61, cal(icloudBase, "birthdays"): 52, cal(icloudBase, "reminders"): 0}},
		"https://cloud.familie-maier.de/remote.php/dav": {found: []caldavclient.Calendar{{URL: cal(nextBase, "work"), Name: "Arbeit Papa"}},
			counts: map[string]int{cal(nextBase, "work"): 87}},
	}
	b.sync.SetDialer(func(serverURL, _, _ string) (calsync.Remote, error) { return remotes[serverURL], nil })
	post("/connections", url.Values{"provider": {"icloud"}, "username": {"mama.maier-schmidt@icloud.com"}, "password": {"preview-password-1"}}, http.StatusOK)
	post("/connections", url.Values{"provider": {"caldav"}, "url": {"https://cloud.familie-maier.de/remote.php/dav"}, "username": {"papa@nextcloud.familie-maier.de"}, "password": {"preview-password-2"}}, http.StatusOK)
	conns, _ := b.db.Connections(h.ID)
	icloud, nextcloud := conns[0].ID, conns[1].ID
	sql(`UPDATE calendar_connections SET created_at = ?`, moment(2, 18, 30, 0))
	post("/connections/"+icloud+"/calendars", url.Values{"interval": {"15"},
		"shown":                             {cal(icloudBase, "family"), cal(icloudBase, "school"), cal(icloudBase, "football"), cal(icloudBase, "birthdays"), cal(icloudBase, "reminders")},
		"calendar":                          {cal(icloudBase, "family"), cal(icloudBase, "school"), cal(icloudBase, "football")},
		"name_" + cal(icloudBase, "family"): {"Familie"}, "name_" + cal(icloudBase, "school"): {"Schule Mia"}, "name_" + cal(icloudBase, "football"): {"Fußball Sebastian"},
		"people_" + cal(icloudBase, "school"): {personID["Mia"]}, "people_" + cal(icloudBase, "football"): {personID["Sebastian"]}}, http.StatusSeeOther)
	post("/connections/"+nextcloud+"/calendars", url.Values{"interval": {"15"}, "shown": {cal(nextBase, "work")}, "calendar": {cal(nextBase, "work")},
		"name_" + cal(nextBase, "work"): {"Arbeit Papa"}, "people_" + cal(nextBase, "work"): {personID["Papa"]}}, http.StatusSeeOther)

	feeds := map[string]string{
		"Schulferien Bayern": "https://example.org/schulferien.ics",
		"Abfallkalender München-Pasing (Restmüll, Papier, Bio)": "https://example.org/abfall.ics",
		"Fußballverein FC Pasing U11: Training und Spiele":      "https://example.org/fcp-u11.ics",
	}
	for name, u := range feeds {
		b.feeds.set(u, feedOf("UID:a\r\nSUMMARY:Termin\r\nDTSTART:20261120T170000Z"), nil)
		form := url.Values{"name": {name}, "url": {u}, "interval": {"60"}}
		if strings.HasPrefix(name, "Fußball") {
			form["people"] = []string{personID["Sebastian"], personID["Jonas"]}
		}
		post("/calendars", form, http.StatusSeeOther)
	}
	synced, _ := b.db.SyncedCalendars(h.ID)
	runAt := map[string]string{"Familie": moment(15, 7, 40, 12), "Schule Mia": moment(15, 7, 40, 11), "Fußball Sebastian": moment(15, 7, 40, 12), "Arbeit Papa": moment(15, 7, 40, 12),
		"Schulferien Bayern": moment(15, 6, 0, 3), "Abfallkalender München-Pasing (Restmüll, Papier, Bio)": moment(15, 6, 0, 5), "Fußballverein FC Pasing U11: Training und Spiele": moment(15, 7, 41, 2)}
	runs := map[string][4]int{"Familie": {214, 2, 1, 0}, "Schule Mia": {38, 0, 0, 0}, "Fußball Sebastian": {61, 3, 0, 1}, "Arbeit Papa": {87, 0, 0, 0}, "Fußballverein FC Pasing U11: Training und Spiele": {38, 2, 1, 0}}
	subs := map[string]store.SyncedCalendar{}
	for _, c := range synced {
		when := runAt[c.Name]
		r := runs[c.Name]
		sql(`UPDATE calendar_sync SET last_success_at = ?, last_attempt_at = ?, last_run_events = ?, last_run_new = ?, last_run_changed = ?, last_run_removed = ? WHERE calendar_id = ?`, when, when, r[0], r[1], r[2], r[3], c.ID)
		subs[c.Name] = c
	}
	// Papa's Nextcloud refused the login at 22:10:05 the evening before; the football club's feed did not answer.
	if err := b.db.FailSync(h.ID, subs["Arbeit Papa"].ID, i18n.Msg("sync.fail.login"), time.Date(2026, 11, 14, 22, 10, 5, 0, local), time.Date(2026, 11, 15, 22, 10, 5, 0, local)); err != nil {
		t.Fatal(err)
	}
	sql(`UPDATE calendar_sync SET last_success_at = ? WHERE calendar_id = ?`, moment(14, 20, 0, 0), subs["Arbeit Papa"].ID)
	football := subs["Fußballverein FC Pasing U11: Training und Spiele"]
	if err := b.db.FailSync(h.ID, football.ID, i18n.Msg("sync.fail.fetch", "detail", "der Server antwortet nicht"), b.now.Add(-time.Minute), b.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	sql(`UPDATE calendar_sync SET last_success_at = ? WHERE calendar_id = ?`, moment(13, 6, 0, 0), football.ID)

	countsOf := func(connID string) map[string]countJSON {
		r := b.do(http.MethodGet, "/api/v1/calendar-connections/"+connID+"/event-counts", nil, de)
		var answers []countJSON
		if err := json.Unmarshal([]byte(r.body), &answers); err != nil {
			t.Fatalf("counts: %v\n%s", err, r.body)
		}
		byURL := map[string]countJSON{}
		for _, a := range answers {
			byURL[a.URL] = a
		}
		return byURL
	}
	// The page asks for the counts after it is shown; the preview runs no such script, so it fills them in from the real answer.
	withCounts := func(body string, by map[string]countJSON, unknown string) string {
		return countCellRe.ReplaceAllStringFunc(body, func(m string) string {
			u := countCellRe.FindStringSubmatch(m)[1]
			a, ok := by[u]
			if !ok {
				return fmt.Sprintf(`<span class="hint count" data-count-for="%s" data-state="unknown">%s</span>`, u, unknown)
			}
			return fmt.Sprintf(`<span class="hint count" data-count-for="%s" data-state="%s">%s</span>`, u, a.State, a.Text)
		})
	}
	counts := countsOf(icloud)
	connectionsOverview := page("/calendars")
	icloudPage := withCounts(page("/connections/"+icloud), counts, "unbekannt")
	icloudDisconnect := icloudPage

	// The server now refuses every login.
	refuse := errRemote{err: &caldavclient.StatusError{Code: http.StatusUnauthorized, Status: "401 Unauthorized"}}
	b.sync.SetDialer(func(string, string, string) (calsync.Remote, error) { return refuse, nil })
	refusedPage := withCounts(page("/connections/"+nextcloud), nil, "Anzahl der Termine unbekannt: der Server hat die Anmeldung abgelehnt")
	wrongPassword := b.do(http.MethodPost, "/connections/"+nextcloud+"/credentials", url.Values{"password": {"nicht-das-richtige"}}, de)
	if wrongPassword.status != http.StatusUnprocessableEntity {
		t.Fatalf("wrong password = %d", wrongPassword.status)
	}
	refusedForm := withCounts(wrongPassword.body, nil, "Anzahl der Termine unbekannt: der Server hat die Anmeldung abgelehnt")

	// "Jetzt abgleichen" on the football club's calendar works again.
	b.feeds.set(feeds["Fußballverein FC Pasing U11: Training und Spiele"], feedOf("UID:a\r\nSUMMARY:Training\r\nDTSTART:20261120T170000Z"), nil)
	b.now = time.Date(2026, 11, 15, 7, 41, 2, 0, local)
	post("/calendars/"+football.ID+"/sync", url.Values{}, http.StatusSeeOther)
	sql(`UPDATE calendar_sync SET last_run_events = 38, last_run_new = 2, last_run_changed = 1, last_run_removed = 0 WHERE calendar_id = ?`, football.ID)
	sql(`UPDATE calendars SET remote_url = ? WHERE id = ?`, "webcal://www.fc-pasing.de/kalender/u11-training-und-spiele.ics", football.ID)
	calendarDetail := page("/calendars/" + football.ID)
	// (the overview was taken before that sync, with the problem flag)
	b.now = time.Date(2026, 11, 15, 7, 42, 30, 0, local)

	// ---- personal devices ----
	personalDeviceNames := []struct{ owner, name string }{{"Mama", "iPhone Mama"}, {"Papa", "Papas Pixel"}, {"Sebastian", "Sebastians Handy"}, {"Mia", "Mias altes iPhone aus der Schublade"},
		{"Jonas", "Jonas’ Handy"}, {"Lotta", "Lottas Handy"}, {"Ben", "Bens Tablet"}, {"Emma", "Emmas Handy"}}
	for _, ph := range personalDeviceNames {
		p, _ := func() (store.Person, bool) {
			for _, x := range people {
				if x.Name == ph.owner {
					return x, true
				}
			}
			return store.Person{}, false
		}()
		pairedPersonalDevice(t, b, p, ph.name)
	}
	personalDevices, _ := b.db.PersonalDevices(h.ID)
	var bens string
	for i, ph := range personalDevices {
		sql(`UPDATE personal_devices SET created_at = ?, last_seen_at = ? WHERE id = ?`, moment(1, 20, 0, 0), moment(15, 7, 40-i, 2), ph.ID)
		if ph.Name == "Bens Tablet" {
			bens = ph.ID
			sql(`UPDATE personal_devices SET last_seen_at = ? WHERE id = ?`, moment(12, 18, 30, 0), ph.ID)
		}
	}
	personalDevicesOverview := page("/personal-devices")
	personalDeviceDetail := page("/personal-devices/" + bens)

	// ---- the page ----
	type frame struct {
		theme string
		w, h  int
	}
	var frames []frame
	for _, size := range [][2]int{{1024, 768}, {768, 1024}, {375, 812}} {
		for _, th := range []string{"light", "dark", "bw"} {
			frames = append(frames, frame{th, size[0], size[1]})
		}
	}
	open := func(html string) string {
		return strings.ReplaceAll(html, `<details class="danger">`, `<details class="danger" open>`)
	}
	var body strings.Builder
	var docs []string
	docOf := map[string]int{}
	host := regexp.MustCompile(`https://127\.0\.0\.1(:[0-9]+)?`)
	add := func(title, note, html string) {
		html = host.ReplaceAllString(html, "https://hejmo.familie-maier.de") // the box of the sample family, not the test server
		idx, seen := docOf[html]
		if !seen {
			idx = len(docs)
			docOf[html] = idx
			docs = append(docs, html)
		}
		fmt.Fprintf(&body, "<h2>%s</h2><p class=\"note\">%s</p><div class=\"row\">\n", title, note)
		for _, f := range frames {
			label := map[string]string{"light": "Hell", "dark": "Dunkel", "bw": "Schwarz-Weiß"}[f.theme]
			fmt.Fprintf(&body, `<figure><figcaption>%s, %d x %d</figcaption><iframe data-doc="%d" data-theme="%s" width="%d" height="%d" title="%s"></iframe></figure>`+"\n", label, f.w, f.h, idx, f.theme, f.w, f.h, title)
		}
		body.WriteString("</div>\n")
	}
	add("1. Displays: ruhige Zeilen, die das Display öffnen", "Name, Typ und wann es zuletzt da war; ein Display, das seit 15 Minuten nichts von sich hören ließ, sagt es und was zu tun ist. Nichts zum Umschalten oder Entfernen auf der Übersicht.", displaysOverview)
	add("2. Displays nach dem Entkoppeln des letzten: Erfolgszeile und leerer Zustand", "Die Erfolgszeile nennt das Display.", displaysEmpty)
	add("3. Display: alles zu einem Display, ein Speichern", "Karten: wo und was, was es zeigt, Ruhemodus (Zeiten nur bei „An“), Mehr Optionen mit Kiosk-Zustand in der Zusammenfassung.", kitchenPage)
	add("4. Display: gespeichert; Schwarz-Weiß-Display, Ruhemodus aus", "Ein Schwarz-Weiß-Display hat keine Hell/Dunkel-Wahl, nur einen Satz.", nurserySaved)
	add("5. Display: Fehler am Feld, Eingaben bleiben", "Ruhemodus mit gleicher Von-/Bis-Zeit und eine Kiosk-Adresse ohne http://; Mehr Optionen öffnet sich von selbst.", bad.body)
	add("6. Display: Entkoppeln fragt zuerst, in Worten", "Roter Rahmen, der Name auf dem roten Knopf, „Nein, nichts ändern“.", open(displayUnpair))
	add("7. Kalender-Apps: Gerätetoken als ruhige Zeilen", "Für wen, wann zuletzt benutzt (mit Sekunden) und eine Warnung bei einem Token, das noch nie benutzt wurde.", tokensOverview)
	add("8. Gerätetoken: Fakten, Entziehen nur hier", "Das Token selbst zeigt die Box nie wieder an.", ipad)
	add("9. Gerätetoken: Entziehen fragt zuerst", "", open(davx))
	add("10. Neues Gerätetoken: einmal sichtbar, mit allem, was die App braucht", "Erfolgszeile mit dem Gerätenamen, das Token in Festschrift, Kopieren, Serveradresse, Benutzername und Passwort-Hinweis, dann „Fertig“.", strings.ReplaceAll(onceBody, " hidden>", ">"))
	add("11. Kalender-Apps nach dem Entziehen des letzten Tokens: Erfolgszeile und leerer Zustand", "", tokensEmpty)
	add("12. Kalender-Verbindungen: Verbindungen und abonnierte Kalender als ruhige Zeilen", "Dienst, Kalender auf der Box mit Namen, letzter Abgleich mit Sekunden oder ein Problem in Worten.", connectionsOverview)
	add("13. Verbindung: eine Seite pro Verbindung", "Fakten, Kalender auswählen (ein ausgeschalteter Kalender zeigt nur seinen Schalter), Anmeldedaten, Verbindung trennen.", icloudPage)
	add("14. Verbindung: der Server hat die Anmeldung abgelehnt", "Die Fehlerzeile nennt die Zeit und springt zum Passwortfeld; der Abschnitt Anmeldedaten ist offen, der Fehler steht am Feld.", refusedForm)
	add("15. Verbindung: Trennen nennt die Kalender", "", open(icloudDisconnect))
	add("16. Abonnierter Kalender: Stand, Jetzt abgleichen, Entfernen", "Nach „Jetzt abgleichen“ steht Zeit, Anzahl und was sich geändert hat in der Erfolgszeile.", calendarDetail)
	add("17. Handys: acht Personen, je eine Zeile", "", personalDevicesOverview)
	add("18. Handy: Entfernen fragt zuerst", "Die Rechte des Handys in Worten.", open(personalDeviceDetail))
	_ = refusedPage

	var jsDocs strings.Builder
	for i, h := range docs {
		h = regexp.MustCompile(`(?s)<script src="[^"]*"[^>]*></script>`).ReplaceAllString(h, "")
		h = regexp.MustCompile(`(?s)<link rel="stylesheet"[^>]*>`).ReplaceAllString(h, "")
		h = strings.ReplaceAll(h, " autofocus", "")
		bodyAt := strings.Index(h, "<body")
		inner := h[bodyAt:strings.LastIndex(h, "</body>")]
		fmt.Fprintf(&jsDocs, "<template id=\"doc%d\">%s</template>\n", i, inner)
	}
	doc := `<!doctype html>
<!-- Generated by TestWritePreview111 from the box's real pages and templates (ticket 111). The stylesheet, scripts and font are inlined copies of web/static and do not follow later changes. No external requests. -->
<html lang="de"><head><meta charset="utf-8"><title>Hejmo preview 111: Detailseiten für Displays, Token, Verbindungen, Kalender und Handys</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:900px}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<h1>Hejmo 111 Detailseiten: Vorschau mit der Beispielfamilie (acht Personen): 1024 x 768, 768 x 1024 und 375 px, hell, dunkel und Schwarz-Weiß</h1>
<noscript><p>Diese Seite braucht JavaScript nur, um die echten Seiten in ihre Rahmen zu setzen.</p></noscript>
` + body.String() + `
<template id="css">` + string(css) + `</template>
<template id="js">` + string(formJS) + `</template>
<template id="js2">` + string(copyJS) + `</template>
` + jsDocs.String() + `
<script>
(function () {
  var font = Uint8Array.from(atob("` + base64.StdEncoding.EncodeToString(font) + `"), function (c) { return c.charCodeAt(0); });
  var fontUrl = URL.createObjectURL(new Blob([font], { type: "font/ttf" }));
  var css = document.getElementById("css").innerHTML.replace("/static/fonts/Figtree-wght.ttf", fontUrl).replace(/&amp;/g, "&").replace(/&gt;/g, ">").replace(/&lt;/g, "<");
  var js = document.getElementById("js").textContent;
  var js2 = document.getElementById("js2").textContent;
  document.querySelectorAll("iframe[data-doc]").forEach(function (f) {
    var inner = document.getElementById("doc" + f.getAttribute("data-doc")).innerHTML;
    f.srcdoc = '<!doctype html><html lang="de" data-theme="' + f.getAttribute("data-theme") + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + css + '</style></head>' + inner + '<script>' + js + '<\/script></html>';
  });
})();
</script>
</body></html>
`
	if err := os.WriteFile(out, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}
