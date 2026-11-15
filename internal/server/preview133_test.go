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

	"github.com/hejmo-home/hejmo/internal/publiccert"
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
	add := func(title, note, body string) { docs = append(docs, doc{title, note, body}) }
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

	fresh := func() *box { b := newFreshBox(t); return b }
	f := fresh()
	f.public.name = ""
	add("Start: Willkommen", "Die erste Seite der neuen Box, noch ohne HTTPS: was kommt, wie lange es dauert, was man braucht, eine Hauptaktion.", get(f, "/setup", ""))
	add("Schritt 1: Weg wählen", "Zwei Auswahlkarten, der empfohlene Weg ist vorausgewählt.", get(f, "/setup/https", ""))
	empty := f.do(http.MethodGet, "/setup/https?way=public", nil, map[string]string{"Accept-Language": "de", "Host": "hejmo.local"})
	add("Schritt 1: Öffentlicher Name, leer", "Die Box wurde über hejmo.local geöffnet: die Adresse ist leer, „Mehr Optionen“ ist offen.", bodyOf(t, empty.body))
	form := get(f, "/setup/https?way=public", "")
	busy := strings.Replace(form, `data-busy-text="Zertifikat wird geholt …"`, `data-busy-text="Zertifikat wird geholt …" disabled`, 1)
	busy = strings.Replace(busy, ">Zertifikat holen</button>", ">Zertifikat wird geholt …</button>", 1)
	add("Schritt 1: Die Box arbeitet", "Das Holen dauert bis zu 3 Minuten: Knopf ausgegraut, Hinweis in Worten, keine Animation (das Formular-Skript setzt diesen Zustand).", reveal(busy, `data-hook="busy"`))
	f.public.failure = errString("obtain certificate: dns says no")
	pf := publicForm()
	pf.Set("name", "familie.dedyn.io")
	add("Fehler: Zertifikat nicht bekommen", "Fehlertext oben und am Tokenfeld; Name, Adresse, Zertifizierungsstelle und E-Mail bleiben.", submit(f, "/setup/https/public", pf, http.StatusBadGateway))

	g := fresh()
	g.public.failure = nil
	long := publicForm()
	long.Set("name", "familie-mueller-luedenscheid.dedyn.io")
	g.post("/setup/https/public", long)
	check := get(g, "/setup/https/check", "")
	failed := reveal(reveal(conceal(check, `data-hook="waiting"`), `data-hook="fail"`), `data-rebind-help`)
	failed = strings.Replace(failed, "Zuletzt: –", "Zuletzt: 14:32:05", 1)
	add("Fehler: öffentlicher Name nicht erreichbar", "Der Browser erreicht die Box unter dem neuen Namen nicht (meist DNS-Rebind-Schutz); die Seite prüft alle 5 Sekunden. Langer Name zum Test der Zeilenumbrüche.", failed)
	okc := reveal(reveal(conceal(conceal(check, `data-hook="waiting"`), `data-rebind-help`), `data-hook="ok"`), `data-continue-link`)
	add("Schritt 1: öffentlicher Name erreichbar", "Erfolg in Worten, eine Hauptaktion, die den nächsten Schritt nennt.", okc)

	h := fresh()
	add("Schritt 1: Zertifikat der Box erstellen", "Der zweite Weg: groß die Adresse, für die das Zertifikat gilt.", get(h, "/setup/https?way=own", ""))
	h.post("/setup/https", url.Values{})
	trust := func(ua, _ string) string { return get(h, "/ca", ua) } // detected from the User-Agent
	ios := trust(iPhone, "ios")
	add("Der Box vertrauen: iPhone erkannt, wartet", "Das erkannte Gerät ist ausgewählt und in Worten bestätigt; vier nummerierte Schritte; nur das Laden ist eine Hauptaktion; die Seite prüft selbst.", ios)
	android := trust(androidUA, "android")
	android = strings.Replace(android, `<li><div class="s"><b>Zertifikat laden</b>`, `<li data-done><div class="s"><b>Zertifikat laden</b>`, 1)
	android = strings.Replace(android, `<a class="button" href="/ca/hejmo-ca.crt" data-download`, `<a class="button secondary" href="/ca/hejmo-ca.crt" data-download`, 1)
	android = strings.Replace(android, ">Zertifikat laden</a>", ">Noch einmal laden</a>", 1)
	android = conceal(reveal(android, `data-hook="fail"`), `data-hook="waiting"`)
	android = strings.Replace(android, `<details class="more" data-fp>`, `<details class="more" data-fp open>`, 1)
	add("Fehler: Zertifikat noch nicht vertraut (Android)", "Nach dem Laden und der Rückkehr vertraut das Gerät noch nicht: der Fehler steht im Schritt, der meist fehlt; der Download ist nur noch ein Nebenknopf.", android)
	mac := trust(macUA, "mac")
	mac = strings.ReplaceAll(mac, `<li><div class="s"`, `<li data-done><div class="s"`)
	mac = conceal(reveal(reveal(mac, `data-hook="ok"`), `data-continue-link`), `data-hook="waiting"`)
	mac = strings.Replace(mac, `<a class="button" href="/ca/hejmo-ca.crt" data-download`, `<a class="button secondary" href="/ca/hejmo-ca.crt" data-download`, 1)
	add("Der Box vertrauen: Mac, geschafft", "Der Mac vertraut der Box: Erfolg in Worten, die Hauptaktion ist jetzt „Weiter zu Schritt 2“.", mac)

	b := newBox(t)
	add("Schritt 2: Dein Konto", "Eine Seite, drei Abschnitte; Hinweise unter den Feldern; Passwort mit „Anzeigen“; Bundesländer nach Land gruppiert.", get(b, "/setup", ""))
	bad := validSetupForm()
	bad.Set("password", "kurz")
	bad.Set("home_name", "Garmisch-Partenkirchen")
	add("Fehler: Passwort zu kurz", "Der Fehler steht am Feld; alles andere bleibt ausgefüllt.", submit(b, "/setup", bad, http.StatusUnprocessableEntity))
	b.post("/setup", validSetupForm())
	add("Schritt 3: Einmalcode am Computer", "Am Computer steht der QR-Code im Mittelpunkt; der Schlüssel liegt in Vierergruppen unter „Mehr Optionen“.", get(b, "/setup/totp", ""))
	wrong := b.do(http.MethodPost, "/setup/totp", url.Values{"code": {"482913"}}, map[string]string{"Accept-Language": "de", "User-Agent": androidUA, "Origin": b.srv.URL})
	add("Fehler: falscher Code (am Handy)", "Auf dem Handy kommt zuerst der Knopf, der die Authenticator-App öffnet; der Fehler nennt die zwei echten Ursachen.", bodyOf(t, wrong.body))
	secret := secretOn(t, get(b, "/setup/totp", ""))
	rec := submit(b, "/setup/totp", url.Values{"code": {totpNow(b, secret)}}, http.StatusOK)
	add("Schritt 3: Wiederherstellungscodes sichern", "Nummerierte, gut lesbare Codes; „Drucken“ druckt nur die Codes; weiter erst nach dem Häkchen.", rec)

	home := get(b, "/", "")
	add("Schritt 4: Erste Schritte auf der Startseite", "Nach dem Assistenten: Erfolgszeile und die Karte „Erste Schritte“; nur die nächste offene Aufgabe hat einen Hauptknopf.", home)

	p := newPeopleBox(t)
	for _, n := range []string{"Papa", "Sebastian", "Mia", "Konstantin", "Charlotte-Sophie", "Maximilian", "Friederike"} {
		role := "child"
		if n == "Papa" {
			role = "parent"
		}
		p.add(t, n, role)
	}
	add("Erste Schritte: Familie eingetragen (8 Personen)", "Erledigte Aufgaben nennen alle Personen mit Zeichen und Namen; der Hauptknopf wandert weiter.", bodyOf(t, p.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "de"}).body))
	_ = publiccert.LetsEncrypt

	js, err := json.Marshal(docs)
	if err != nil {
		t.Fatal(err)
	}
	html := `<!doctype html>
<!-- Generated by TestWriteDesignPreview133 from the real templates, served by a test box with the example family (ticket 133). The stylesheet and font are inlined copies of web/static and do not follow later changes. Open with ?doc=N&theme=light|dark|bw to see one screen on its own. -->
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
  let html = '<h1>Hejmo #133 Einrichtung von null: Start, sichere Verbindung, der Box vertrauen, Konto, Einmalcode, Wiederherstellungscodes, erste Schritte. Hell, dunkel und schwarz-weiß; 1024 × 768, 768 × 1024 hochkant, 375 px Handy. Erzeugt aus den echten Seiten, Beispielfamilie Mama, Papa, Sebastian, Mia und vier weitere Kinder.</h1><p class="note">Jede Seite lässt sich in ihrem Rahmen scrollen. Einzelne Seite groß: diese Datei mit ?doc=Nummer&amp;theme=light|dark|bw öffnen.</p>';
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
