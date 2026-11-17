package server_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/calendar"
	"github.com/hejmo-home/hejmo/internal/publiccert"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
	"github.com/hejmo-home/hejmo/web"
)

var (
	scriptTagRe = regexp.MustCompile(`(?s)<script[^>]*>.*?</script>`)
	skipLinkRe  = regexp.MustCompile(`<a class="skip"[^>]*>[^<]*</a>`)
)

// bodyOf is what a page puts inside <body>, without scripts (the preview
// recreates the page script's states by hand) and without the skip link.
func bodyOf(t *testing.T, page string) string {
	t.Helper()
	i := strings.Index(page, "<body")
	j := strings.LastIndex(page, "</body>")
	if i < 0 || j < 0 {
		t.Fatalf("no body in\n%s", page)
	}
	i += strings.Index(page[i:], ">") + 1
	return skipLinkRe.ReplaceAllString(scriptTagRe.ReplaceAllString(page[i:j], ""), "")
}

var flagRe = regexp.MustCompile(`/static/flags/(\w+)\.svg`)

// inlineFlags puts the flag images into the markup, since the preview is one file.
func inlineFlags(t *testing.T, html string) string {
	t.Helper()
	return flagRe.ReplaceAllStringFunc(html, func(m string) string {
		raw, err := web.Static.ReadFile("static" + strings.TrimPrefix(m, "/static"))
		if err != nil {
			t.Fatal(err)
		}
		return "data:image/svg+xml," + strings.ReplaceAll(url.QueryEscape(string(raw)), "+", "%20")
	})
}

// kitchenBox is a signed-in parent (Mama) with one paired display, "Küche".
func kitchenBox(t *testing.T) *box {
	t.Helper()
	b := signedInBox(t)
	form := pairForm(codeOn(t, newScreen(b).get("/display", "en").body))
	form.Set("type", "eink_color")
	form.Set("name", "Küche")
	if r := b.post("/displays", form); r.status != http.StatusSeeOther {
		t.Fatalf("pair = %d", r.status)
	}
	return b
}

// reveal removes "hidden" from the element carrying attr; conceal adds it.
func reveal(html, attr string) string {
	re := regexp.MustCompile(`(` + regexp.QuoteMeta(attr) + `[^>]*?) hidden`)
	return re.ReplaceAllString(html, "$1")
}
func conceal(html, attr string) string {
	return strings.Replace(html, attr, attr+" hidden", 1)
}

// TestWriteDesignPreview133 writes the self-contained preview of ticket 133
// (first-run flow and trust page): every screen is the real page served by a
// test box, in light, dark and black-and-white at 1024, 768 and 375 px. The
// states a page script sets in the browser (busy button, check lines, a
// downloaded certificate) are applied to the markup by hand, as the script does.
// Run: HEJMO_WRITE_PREVIEW_133=../../docs/design/preview-133.html go test ./internal/server -run WriteDesignPreview133
func TestWriteDesignPreview133(t *testing.T) {
	t.Parallel()
	out := os.Getenv("HEJMO_WRITE_PREVIEW_133")
	if out == "" {
		t.Skip("set HEJMO_WRITE_PREVIEW_133 to the output path to write the preview")
	}
	css, err := web.Static.ReadFile("static/hejmo.css")
	if err != nil {
		t.Fatal(err)
	}
	font, err := web.Static.ReadFile("static/fonts/Figtree-wght.ttf")
	if err != nil {
		t.Fatal(err)
	}
	style := strings.Replace(string(css), `url("/static/fonts/Figtree-wght.ttf")`, "url(data:font/ttf;base64,"+base64.StdEncoding.EncodeToString(font)+")", 1)

	type doc struct{ Title, Note, Body string }
	var docs []doc
	add := func(title, note, body string) { docs = append(docs, doc{title, note, inlineFlags(t, body)}) }
	get := func(b *box, path, ua string) string {
		r := b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": "de", "User-Agent": ua})
		if r.status != http.StatusOK {
			t.Fatalf("GET %s = %d\n%s", path, r.status, r.body)
		}
		return bodyOf(t, r.body)
	}
	submit := func(b *box, path string, f url.Values, status int) string {
		r := b.do(http.MethodPost, path, f, map[string]string{"Accept-Language": "de", "Origin": b.srv.URL})
		if r.status != status {
			t.Fatalf("POST %s = %d, want %d\n%s", path, r.status, status, r.body)
		}
		return bodyOf(t, r.body)
	}
	// The place picker (screens 12 to 15) is the designer's drawing: the data
	// source is not decided (#86, #129), so there is no real page to serve yet.
	drawn := func(name string) string {
		raw, err := os.ReadFile("testdata/preview133/" + name + ".html")
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	fresh := func() *box { b := newFreshBox(t); return b }
	f := fresh()
	f.public.name = ""
	welcome := get(f, "/setup", "")
	add("Start: Willkommen", "Die erste Seite der neuen Box, noch ohne HTTPS: was kommt, wie lange es dauert, was man braucht, eine Hauptaktion. Neu: Sprachumschalter oben rechts; jede App und deSEC sind Links.", welcome)
	add("Start: Sprache wählen (offen)", "Der Sprachumschalter aus #134 sitzt auf allen Seiten der Einrichtung oben rechts, vor dem ersten Satz. Vor der Anmeldung bleibt der Name auch am Handy sichtbar.", strings.Replace(welcome, `<details class="lang">`, `<details class="lang" open>`, 1))
	add("Schritt 1: Weg wählen", "Drei Auswahlkarten, kein Weg ist vorausgewählt.", get(f, "/setup/https", ""))
	empty := f.do(http.MethodGet, "/setup/https?way=public", nil, map[string]string{"Accept-Language": "de", "Host": "hejmo.local"})
	add("Schritt 1: Öffentlicher Name, leer", "Die Box wurde über hejmo.local geöffnet: die Adresse ist leer, „Mehr Optionen“ ist offen. Neu: jede deSEC-Seite ist ein Link (Konto, Domains, Token Management).", bodyOf(t, empty.body))
	form := get(f, "/setup/https?way=public", "")
	busy := strings.Replace(form, `data-busy-text="Zertifikat wird geholt …"`, `data-busy-text="Zertifikat wird geholt …" disabled`, 1)
	busy = strings.Replace(busy, ">Zertifikat holen</button>", ">Zertifikat wird geholt …</button>", 1)
	add("Schritt 1: Die Box arbeitet", "Das Holen dauert bis zu 3 Minuten: Knopf ausgegraut, Hinweis in Worten, keine Animation (das Formular-Skript setzt diesen Zustand). Neu: jede deSEC-Seite ist ein Link (Konto, Domains, Token Management).", reveal(busy, `data-hook="busy"`))
	f.public.failure = errString("obtain certificate: dns says no")
	pf := publicForm()
	pf.Set("name", "familie.dedyn.io")
	add("Fehler: Zertifikat nicht bekommen", "Fehlertext oben und am Tokenfeld; Name, Adresse, Zertifizierungsstelle und E-Mail bleiben. Neu: jede deSEC-Seite ist ein Link (Konto, Domains, Token Management).", submit(f, "/setup/https/public", pf, http.StatusBadGateway))

	g := fresh()
	g.public.failure = nil
	long := publicForm()
	long.Set("name", "familie-mueller-luedenscheid.dedyn.io")
	g.post("/setup/https/public", long)
	check := get(g, "/setup/https/check", "")
	failed := reveal(reveal(conceal(check, `data-hook="waiting"`), `data-hook="fail"`), `data-rebind-help`)
	failed = strings.Replace(failed, "Zuletzt: –", "Zuletzt: 14:32:05", 1)
	add("Fehler: öffentlicher Name nicht erreichbar", "Der Browser erreicht die Box unter dem neuen Namen nicht (meist DNS-Rebind-Schutz); die Seite prüft alle 5 Sekunden. Langer Name zum Test der Zeilenumbrüche. Neu: der Name für die Router-Ausnahme hat „Kopieren“ (er wird im Router eingetippt, nicht geöffnet).", failed)
	okc := reveal(reveal(conceal(conceal(check, `data-hook="waiting"`), `data-rebind-help`), `data-hook="ok"`), `data-continue-link`)
	add("Schritt 1: öffentlicher Name erreichbar", "Erfolg in Worten, eine Hauptaktion, die den nächsten Schritt nennt.", okc)

	h := fresh()
	add("Schritt 1: Zertifikat der Box erstellen", "Der zweite Weg: groß die Adresse, für die das Zertifikat gilt.", get(h, "/setup/https?way=own", ""))
	h.post("/setup/https", url.Values{})
	trust := func(ua string) string { return get(h, "/ca", ua) } // detected from the User-Agent
	add("Der Box vertrauen: iPhone erkannt, wartet", "Das erkannte Gerät ist ausgewählt und in Worten bestätigt; vier nummerierte Schritte; nur das Laden ist eine Hauptaktion; die Seite prüft selbst.", trust(iPhone))
	android := trust(androidUA)
	android = strings.Replace(android, `<li><div class="s"><b>Zertifikat laden</b>`, `<li data-done><div class="s"><b>Zertifikat laden</b>`, 1)
	android = strings.Replace(android, `<a class="button" href="/ca/hejmo-ca.crt" data-download`, `<a class="button secondary" href="/ca/hejmo-ca.crt" data-download`, 1)
	android = strings.Replace(android, ">Zertifikat laden</a>", ">Noch einmal laden</a>", 1)
	android = conceal(reveal(android, `data-hook="fail"`), `data-hook="waiting"`)
	android = strings.Replace(android, `<details class="more" data-fp>`, `<details class="more" data-fp open>`, 1)
	add("Fehler: Zertifikat noch nicht vertraut (Android)", "Nach dem Laden und der Rückkehr vertraut das Gerät noch nicht: der Fehler steht im Schritt, der meist fehlt; der Download ist nur noch ein Nebenknopf.", android)
	mac := trust(macUA)
	mac = strings.ReplaceAll(mac, `<li><div class="s"`, `<li data-done><div class="s"`)
	mac = conceal(reveal(reveal(mac, `data-hook="ok"`), `data-continue-link`), `data-hook="waiting"`)
	mac = strings.Replace(mac, `<a class="button" href="/ca/hejmo-ca.crt" data-download`, `<a class="button secondary" href="/ca/hejmo-ca.crt" data-download`, 1)
	add("Der Box vertrauen: Mac, geschafft", "Der Mac vertraut der Box: Erfolg in Worten, die Hauptaktion ist jetzt „Weiter zu Schritt 2“.", mac)

	b := newBox(t)
	add("Schritt 2: Dein Konto, Ort wird getippt", "Heimatort als Ortsauswahl (Produktentscheidung 15/11, Quelle noch offen in #86): schon beim Tippen erscheinen bis zu 5 passende Orte mit Region in Worten; der erste ist mit der Tastatur markiert, aber noch nicht gewählt.", drawn("place-typing"))
	add("Schritt 2: viele Orte mit langem Namen", "Gleich heißende Orte: Name fett, Region in der zweiten Zeile, so lassen sie sich unterscheiden. Nie „+3“: es stehen höchstens 5 da, und der Satz darüber sagt, dass es mehr gibt und wie man weiter eingrenzt.", drawn("place-many"))
	add("Schritt 2: Ort gewählt", "Bestätigter Zustand in Worten und mit Haken: „München, Bayern, Deutschland“ und „Gewählt“; „Ändern“ öffnet wieder das Feld. Das Bundesland wird aus dem Ort übernommen und sagt das.", drawn("place-chosen"))
	add("Fehler: Ort nicht gefunden", "Keine Vorschläge: der Fehler steht am Feld, der getippte Text bleibt, der Satz sagt, was man tun kann.", drawn("place-missing"))
	bad := validSetupForm()
	bad.Set("password", "kurz")
	bad.Set("home_name", "Pasing-Obermenzing")
	add("Fehler: Passwort zu kurz", "Der Fehler steht am Feld; alles andere bleibt, auch der gewählte Ort (langer Name zum Test der Umbrüche).", submit(b, "/setup", bad, http.StatusUnprocessableEntity))
	b.post("/setup", validSetupForm())
	add("Schritt 3: Einmalcode am Computer", "Am Computer steht der QR-Code im Mittelpunkt; der Schlüssel liegt in Vierergruppen unter „Mehr Optionen“. Neu: jede App und jeder Store ist ein Link (Produktentscheidung 15/11).", get(b, "/setup/totp", ""))
	wrong := b.do(http.MethodPost, "/setup/totp", url.Values{"code": {"482913"}}, map[string]string{"Accept-Language": "de", "User-Agent": androidUA, "Origin": b.srv.URL})
	add("Fehler: falscher Code (am Handy)", "Auf dem Handy kommt zuerst der Knopf, der die Authenticator-App öffnet; der Fehler nennt die zwei echten Ursachen. Neu: das Handy wurde als Android erkannt, also führen die Links zu Google Play.", bodyOf(t, wrong.body))
	secret := secretOn(t, get(b, "/setup/totp", ""))
	rec := submit(b, "/setup/totp", url.Values{"code": {totpNow(b, secret)}}, http.StatusOK)
	add("Schritt 3: Wiederherstellungscodes sichern", "Nummerierte, gut lesbare Codes; „Drucken“ druckt nur die Codes; weiter erst nach dem Häkchen.", rec)

	add("Schritt 4: Erste Schritte, ganz neu", "Nach dem Assistenten: Erfolgszeile und die Karte „Erste Schritte“ mit 6 Schritten in sinnvoller Reihenfolge. Jeder sagt in Worten „Noch offen“, „Erledigt“ oder „Übersprungen“; nur der nächste offene hat einen Hauptknopf; jeder lässt sich überspringen.", get(b, "/", ""))

	// Eight people, the first steps half done: Papa is entered but has no phone,
	// the tasks are skipped, the kitchen display is connected.
	half := kitchenBox(t)
	for _, n := range []string{"Papa", "Sebastian", "Mia", "Konstantin", "Charlotte-Sophie", "Maximilian", "Friederike"} {
		role := "child"
		if n == "Papa" {
			role = "parent"
		}
		half.post("/people", url.Values{"name": {n}, "role": {role}})
	}
	half.post("/first-steps/skip", url.Values{"step": {"tasks"}})
	get(half, "/", "") // the success line of the last change is shown once
	add("Erste Schritte: halb fertig (8 Personen, einer übersprungen)", "Erledigt nennt, was da ist: alle 8 Personen als Chips mit Zeichen und Namen, nie „+4“. Ein halb erledigter Schritt zeigt seine Teile mit Haken und Punkt in Worten. Übersprungen hat einen gestrichelten Kreis und „Doch machen“. Der Hauptknopf steht beim nächsten offenen Schritt.", get(half, "/", ""))

	// Everything done: phones, more calendars with their audiences, tasks, a meal plan.
	hh, _ := half.db.HomeHousehold()
	people, _ := half.db.People(hh.ID)
	byName := map[string]store.Person{}
	for _, p := range people {
		byName[p.Name] = p
	}
	half.post("/first-steps/undo", url.Values{"step": {"tasks"}})
	pairedPhone(t, half, byName["Mama"], "iPhone")
	pairedPhone(t, half, byName["Papa"], "Android")
	half.feeds.set("https://example.org/familie.ics", feedOf(), nil)
	half.post("/calendars", url.Values{"name": {"Familie"}, "url": {"https://example.org/familie.ics"}, "color": {"#2F5E7A"}, "interval": {"60"}})
	for _, c := range []struct {
		name string
		who  []string
	}{{"Arbeit", []string{"Papa"}}, {"Schule und Kita", []string{"Sebastian", "Mia", "Konstantin", "Charlotte-Sophie"}}} {
		var ids []string
		for _, n := range c.who {
			ids = append(ids, byName[n].ID)
		}
		if _, err := half.db.CreateCalendar(store.Calendar{HouseholdID: hh.ID, Name: c.name, Kind: "ics", Color: "#B4583A", Audience: calendar.Audience{People: ids}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"Sebastian", "Mia", "Konstantin", "Charlotte-Sophie", "Maximilian", "Friederike"} {
		if _, err := half.db.AddTask(hh.ID, task.Task{PersonID: byName[n].ID, Title: "Zähne putzen", Repeat: task.Daily, Date: "2026-11-14", Stars: 1}); err != nil {
			t.Fatal(err)
		}
	}
	half.post("/meals", url.Values{"week": {"2026-11-09"}, mealField("2026-11-14", "dinner"): {"Brotzeit"}})
	get(half, "/", "")
	add("Erste Schritte: alles erledigt", "Wenn alle 6 erledigt oder übersprungen sind: Erfolg in Worten, eine Zusammenfassung, für wen welcher Kalender ist, und „Erste Schritte schließen“ als einzige Hauptaktion.", get(half, "/", ""))

	// The day: eight people, events, tasks, meals; after the steps are closed.
	fam := newFamilyBox(t, "eink_color")
	for _, n := range []string{"Konstantin", "Charlotte-Sophie", "Maximilian", "Friederike"} {
		p, err := fam.db.AddPerson(fam.householdID, store.Person{Name: n, Role: store.RoleChild})
		if err != nil {
			t.Fatal(err)
		}
		fam.people[n] = p
	}
	fam.now = fam.at(14, 8, 0)
	fam.post("/first-steps/hide", url.Values{})
	empty24 := bodyOf(t, fam.page("de", "/").body)
	fam.event(t, "Flohmarkt am Pasinger Marienplatz", fam.at(14, 11, 0), fam.at(14, 13, 0), false)
	fam.event(t, "Yoga im Pasinger Fabrik-Saal", fam.at(14, 9, 0), fam.at(14, 10, 0), false, "Mama")
	fam.event(t, "Zahnarzt Dr. Schönberger-Wittelsbach", fam.at(14, 8, 15), fam.at(14, 9, 0), false, "Papa")
	fam.event(t, "Elternabend 3b", fam.at(14, 19, 30), fam.at(14, 21, 0), false, "Papa")
	fam.event(t, "Fußballtraining FC Pasing", fam.at(14, 14, 0), fam.at(14, 15, 0), false, "Sebastian")
	fam.event(t, "Schwimmkurs Seepferdchen", fam.at(14, 15, 30), fam.at(14, 16, 30), false, "Mia")
	fam.event(t, "Kindergeburtstag bei Leopoldine", fam.at(14, 10, 0), fam.at(14, 12, 0), false, "Charlotte-Sophie")
	for _, c := range []struct {
		who   string
		tasks []bool
	}{{"Sebastian", []bool{true, true, true, false}}, {"Mia", []bool{true, true, false, false}}, {"Konstantin", []bool{true, true, true, true}}, {"Charlotte-Sophie", []bool{false, false, false, false}}, {"Maximilian", []bool{true, false, false}}} {
		for i, done := range c.tasks {
			fam.addTask(t, c.who, []string{"Zähne putzen", "Anziehen", "Schulranzen packen", "Zimmer aufräumen"}[i], 1+i/3, done)
		}
	}
	fam.post("/meals", url.Values{"week": {"2026-11-09"}, mealField("2026-11-14", "lunch"): {"Kartoffelsuppe mit Würstchen"}, mealField("2026-11-14", "dinner"): {"Brotzeit"}})
	fam.post("/first-steps/show", url.Values{}) // the line "Erste Schritte ausgeblendet" is the last change
	fam.post("/first-steps/hide", url.Values{})
	add("Start, gefüllt: Heute bei euch", "Ersetzt „Die Familienansicht kommt mit dem Display“. Nach dem Schließen zeigt die Startseite den heutigen Tag: Familientermine mit „Ganze Familie“ in Worten, dann jede Person mit Zeichen und Namen, ihren Terminen und „2 von 4 Aufgaben“ mit Sternen; leere Personen sagen „Keine Termine“. Darunter das Essen und die Notiz.", bodyOf(t, fam.page("de", "/").body))
	add("Start, leerer Tag", "Leerer Zustand: ein Satz statt 8 leerer Zeilen, eine Aktion; das Essen sagt „noch nichts geplant“ mit Link.", empty24)
	mia := childOf(t, fam, "Mia")
	add("Start für ein Kind (Mia)", "Kinder sahen bisher den alten Platzhalter. Jetzt: das eigene Zeichen groß neben dem Namen, der eigene Tag, Aufgaben mit Kästchen und Sternen als Bild und Zahl, ein Knopf zum Abhaken. Kein Notizfeld, keine Einstellungen.", bodyOf(t, mia.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "de"}).body))
	_ = publiccert.LetsEncrypt

	js, err := json.Marshal(docs)
	if err != nil {
		t.Fatal(err)
	}
	html := `<!doctype html>
<!-- Generated by TestWriteDesignPreview133 from the real templates, served by a test box with the example family (ticket 133). Screens 12 to 15 (the place picker) are the designer's drawing from testdata/preview133: the data source is open (#86), so no real page exists yet. The stylesheet and font are inlined copies of web/static and do not follow later changes. Open with ?doc=N&theme=light|dark|bw to see one screen on its own. -->
<html lang="de"><head><meta charset="utf-8"><title>Hejmo preview #133 Einrichtung von null</title>
<style>
body{margin:0;padding:16px;background:#888;font-family:system-ui,sans-serif;color:#fff}
h1{font-size:22px}h2{font-size:18px;margin:28px 0 4px}.note{margin:0 0 10px;max-width:980px;line-height:1.4}
.row{display:flex;flex-wrap:wrap;gap:24px;align-items:flex-start}
figure{margin:0 0 12px}figcaption{margin-bottom:6px;font-weight:700}
iframe{border:0;background:#fff;display:block}
</style></head><body>
<script>
const CSS = ` + "`" + strings.ReplaceAll(strings.ReplaceAll(style, "\\", "\\\\"), "`", "\\`") + "`" + `;
const DOCS = ` + string(js) + `;
function docHTML(i, theme) {
  return '<!doctype html><html lang="de" data-theme="' + theme + '"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>' + CSS + '</style></head><body class="web">' + DOCS[i].Body + '</body></html>';
}
const q = new URLSearchParams(location.search);
if (q.has('doc')) {
  document.open(); document.write(docHTML(+q.get('doc'), q.get('theme') || 'light')); document.close();
} else {
  const sizes = [['light', 1024, 768], ['dark', 1024, 768], ['bw', 1024, 768], ['light', 768, 1024], ['bw', 768, 1024], ['light', 375, 812], ['dark', 375, 812], ['bw', 375, 812]];
  const names = { light: 'Hell', dark: 'Dunkel', bw: 'Schwarz-weiß' };
  let html = '<h1>Hejmo #133 Einrichtung von null: Start, sichere Verbindung, der Box vertrauen, Konto, Einmalcode, Wiederherstellungscodes, Heimatort mit Ortsauswahl, Sprachumschalter, erste Schritte mit 6 Schritten, die gefüllte Startseite und die Startseite eines Kindes. Hell, dunkel und schwarz-weiß; 1024 × 768, 768 × 1024 hochkant, 375 px Handy. Erzeugt aus den echten Seiten, Beispielfamilie Mama, Papa, Sebastian, Mia und vier weitere Kinder.</h1><p class="note">Jede Seite lässt sich in ihrem Rahmen scrollen. Einzelne Seite groß: diese Datei mit ?doc=Nummer&amp;theme=light|dark|bw öffnen.</p>';
  DOCS.forEach((d, i) => {
    html += '<h2>' + i + '. ' + d.Title + '</h2><p class="note">' + d.Note + '</p><div class="row">';
    sizes.forEach(([t, w, h]) => { html += '<figure><figcaption>' + names[t] + ', ' + w + ' × ' + h + '</figcaption><iframe data-doc="' + i + '" data-theme="' + t + '" width="' + w + '" height="' + h + '" title="' + d.Title + '"></iframe></figure>'; });
    html += '</div>';
  });
  document.body.insertAdjacentHTML('beforeend', html);
  document.querySelectorAll('iframe[data-doc]').forEach(f => { f.srcdoc = docHTML(+f.dataset.doc, f.dataset.theme); });
}
</script>
<noscript><p>Diese Seite braucht JavaScript nur, um die Seiten in ihre Rahmen zu setzen.</p></noscript>
</body></html>
`
	if err := os.WriteFile(out, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
}
