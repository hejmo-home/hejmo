package server_test

import (
	"bytes"
	"crypto/sha256"
	tls_ "crypto/tls"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/update/updatetest"
)

// Ticket 221 (Q154): in offline mode a parent uploads an update file; the box
// checks it, asks for a yes and hands it to the host script, which installs it
// with backup, health check and rollback. The design is docs/design/preview-221.html.

const (
	uploadPath  = "/settings/connections/update"
	confirmPath = "/settings/connections/update/install"
)

// ---- helpers

// postUpdate posts a multipart form like the upload page does without script.
func (b *box) postUpdate(name string, data []byte, header map[string]string) resp {
	b.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if name != "" || data != nil {
		part, _ := mw.CreateFormFile("update_file", name)
		_, _ = part.Write(data)
	}
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, b.srv.URL+uploadPath, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", b.srv.URL)
	req.Header.Set("Accept-Language", "en")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), string(out)}
}

func (b *box) postEN(path string, form url.Values) resp {
	return b.do(http.MethodPost, path, form, map[string]string{"Accept-Language": "en"})
}

// offlineUpdateBox is a signed-in box in offline mode whose host script reported version 0.2.3.
func offlineUpdateBox(t *testing.T, opts ...boxOption) *box {
	t.Helper()
	b := signedInBox(t, opts...)
	offlineOn(t, b)
	b.writeStatus("state=idle\nversion=0.2.3\n")
	return b
}

func (b *box) release(spec updatetest.Spec) []byte { return b.sig().File(b.t, spec) }

func (b *box) pendingFile() string { return filepath.Join(b.updateDir, "update-file") }

func (b *box) strays() []string {
	entries, _ := os.ReadDir(b.updateDir)
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".update-upload") {
			out = append(out, e.Name())
		}
	}
	return out
}

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// uploadGood uploads a good file and returns its bytes.
func uploadGood(t *testing.T, b *box) []byte {
	t.Helper()
	data := b.release(updatetest.Spec{})
	if r := b.postUpdate("hejmo-0.2.4-arm64.update", data, nil); r.status != http.StatusSeeOther || r.location != confirmPath {
		t.Fatalf("upload = %d %q\n%s", r.status, r.location, r.body)
	}
	return data
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ---- who may

func TestOnlyParentsOpenTheUploadPageAndUpload(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	child := childSession(t, b)
	for _, path := range []string{uploadPath, confirmPath} {
		if r := getEN(child, path); r.status != http.StatusForbidden {
			t.Errorf("child GET %s = %d", path, r.status)
		}
	}
	if r := child.postUpdate("hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{}), nil); r.status != http.StatusForbidden {
		t.Errorf("child upload = %d", r.status)
	}
	for _, path := range []string{confirmPath, uploadPath + "/discard"} {
		if r := child.postEN(path, url.Values{}); r.status != http.StatusForbidden {
			t.Errorf("child POST %s = %d", path, r.status)
		}
	}
	if _, err := os.Stat(b.pendingFile()); err == nil {
		t.Error("a child left a file on the box")
	}
}

func TestSignedOutVisitorsAreSentToSignIn(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	b.logout()
	if r := b.get(uploadPath); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Errorf("GET = %d %q", r.status, r.location)
	}
	if r := b.postUpdate("x.update", []byte("x"), nil); r.status != http.StatusSeeOther || !strings.HasPrefix(r.location, "/login") {
		t.Errorf("POST = %d %q", r.status, r.location)
	}
}

// ---- the ways in

func TestTheConnectionsPageOffersTheUploadAndKeepsTheUSBWay(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	page := getEN(b, "/settings/connections").body
	mustContain(t, page, `id="updates"`, `<a class="button secondary" href="/settings/connections/update">`, "Upload an update file",
		"The box checks the signature, saves your data and installs the new version.", "Without a browser it also works with a USB stick and a command:", "install.md#update-without-the-internet")
	// "Save" stays the page's one primary action.
	if strings.Count(page, `class="button"`) != 1 {
		t.Errorf("the page has more than one primary button:\n%s", page)
	}
	// Without a host script nothing could install the file: the button is not offered, the way by hand stays.
	b = signedInBox(t, withoutHostUpdater)
	offlineOn(t, b)
	page = getEN(b, "/settings/connections").body
	mustNotContain(t, page, `href="/settings/connections/update"`)
	mustContain(t, page, "install.md#update-without-the-internet")
	// Nor beside a host script that cannot install a file.
	b = signedInBox(t, withOldHostUpdater)
	offlineOn(t, b)
	mustNotContain(t, getEN(b, "/settings/connections").body, `href="/settings/connections/update"`)
}

func TestTheReminderLeadsToTheUpload(t *testing.T) {
	t.Parallel()
	b, secret := configuredBox(t)
	signInLater(t, b, secret, b.now)
	offlineOn(t, b)
	b.writeStatus("state=updated\nversion=0.2.3\ntime=" + stamp(b.now) + "\n")
	installed, _ := b.db.OutboundBox()
	signInLater(t, b, secret, installed.UpdatedAt.AddDate(0, 0, 91))
	page := getEN(b, "/").body
	mustContain(t, page, "Not updated for 91 days.", `<p class="upd-links"><a class="link" href="/settings/connections/update">Upload an update file</a></p>`)
	mustNotContain(t, page, "install.md#update-without-the-internet") // one way forward, not two
	// Without a host script the reminder keeps the guide.
	b2, secret2 := configuredBox(t, withoutHostUpdater)
	signInLater(t, b2, secret2, b2.now)
	offlineOn(t, b2)
	i2, _ := b2.db.OutboundBox()
	signInLater(t, b2, secret2, i2.UpdatedAt.AddDate(0, 0, 91))
	page = getEN(b2, "/").body
	mustContain(t, page, "Not updated for 91 days.", "install.md#update-without-the-internet")
	mustNotContain(t, page, "/settings/connections/update")
}

// ---- the page

func TestTheUploadPageShowsWhatRunsAndWhichFileTheBoxNeeds(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	r := getEN(b, uploadPath)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d", r.status)
	}
	mustContain(t, r.body, `<h1>Upload an update file</h1>`,
		`href="/settings/connections">Connections to the outside</a>`, `aria-current="page">Upload an update file</span>`,
		"It installs only files that the Hejmo project has signed.",
		"<dt>Installed</dt><dd>Hejmo 0.2.3 from", "<dt>Your box</dt><dd>arm64 (for example a Raspberry Pi)</dd>",
		`<p class="fname">hejmo-<i>version</i>-arm64.update</p>`, "Choose a version without “rc” in its name.",
		`<form method="post" action="/settings/connections/update" enctype="multipart/form-data" class="upd-form"`,
		`<input id="update_file" type="file" name="update_file" accept=".update"`, "At most 500 MB. Over HTTPS only.",
		"Upload and check", "Nothing is installed until you confirm.",
		`src="/static/update-upload.js"`, `data-max-bytes="524288000"`)
	mustNotContain(t, r.body, `aria-invalid`)
	if got := getDE(b, uploadPath).body; !strings.Contains(got, "Update-Datei hochladen") || !strings.Contains(got, "Hochladen und prüfen") || !strings.Contains(got, "arm64 (zum Beispiel ein Raspberry Pi)") {
		t.Errorf("German page:\n%s", got)
	}
}

func getDE(b *box, path string) resp {
	return b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": "de"})
}

func TestTheUploadPageSaysSoWhenTheBoxUpdatesItself(t *testing.T) {
	t.Parallel()
	b := signedInBox(t) // online: the nightly check is on
	r := getEN(b, uploadPath)
	mustContain(t, r.body, "The box fetches updates from the internet itself", `href="/settings/household"`, "View automatic updates")
	mustNotContain(t, r.body, `type="file"`)
	// A file cannot be put past the nightly check.
	if r := b.postUpdate("hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{}), nil); r.status != http.StatusConflict {
		t.Errorf("upload while online = %d", r.status)
	}
	if _, err := os.Stat(b.pendingFile()); err == nil || len(b.strays()) > 0 {
		t.Error("a file was kept while the box updates itself")
	}
	// Switching the update check off, without offline mode, opens it.
	b.post("/settings/connections", url.Values{"weather": {"on"}, "calendars": {"on"}, "ics": {"on"}, "recipes": {"on"}, "images": {"on"}})
	mustContain(t, getEN(b, uploadPath).body, `type="file"`)
}

func TestTheUploadPageSaysSoWithoutAHostScript(t *testing.T) {
	t.Parallel()
	b := signedInBox(t, withoutHostUpdater)
	offlineOn(t, b)
	r := getEN(b, uploadPath)
	mustContain(t, r.body, "No update program runs on this device", "install.md#update-without-the-internet", "Unknown: the box knows its version only from the update program")
	mustNotContain(t, r.body, `type="file"`)
	if r := b.postUpdate("hejmo-0.2.4-arm64.update", []byte("x"), nil); r.status != http.StatusConflict {
		t.Errorf("upload without a host script = %d", r.status)
	}
}

func TestTheUploadPageSaysSoBesideAnOldHostScript(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t, withOldHostUpdater)
	r := getEN(b, uploadPath)
	mustContain(t, r.body, "The update program on this device is older and cannot install a file", "install.md#update-without-the-internet")
	mustNotContain(t, r.body, `type="file"`)
	if r := b.postUpdate("hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{}), nil); r.status != http.StatusConflict {
		t.Errorf("upload beside an old host script = %d", r.status)
	}
}

// Plain HTTP never reaches the page: the box sends everything to HTTPS once it has a certificate.
func TestUploadsAreNeverTakenOverPlainHTTP(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	plain := b.plainServer()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("update_file", "hejmo-0.2.4-arm64.update")
	_, _ = part.Write(b.release(updatetest.Spec{}))
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, plain.URL+uploadPath, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := b.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusPermanentRedirect || !strings.HasPrefix(res.Header.Get("Location"), "https://") {
		t.Fatalf("plain HTTP = %d %q, want a redirect to HTTPS", res.StatusCode, res.Header.Get("Location"))
	}
	if _, err := os.Stat(b.pendingFile()); err == nil {
		t.Error("a file was kept")
	}
}

func TestOverPlainHTTPTheFormIsReplacedByTheHTTPSAddress(t *testing.T) {
	t.Parallel()
	if !server.UploadNeedsHTTPS(httpRequest(false)) || server.UploadNeedsHTTPS(httpRequest(true)) {
		t.Error("only a request over TLS may upload")
	}
}

// ---- upload and check

func TestAGoodFileIsKeptAndAsksForConfirmation(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	data := uploadGood(t, b)
	kept, err := os.ReadFile(b.pendingFile())
	if err != nil || !bytes.Equal(kept, data) {
		t.Fatalf("the file is not kept as it came: %v", err)
	}
	if len(b.strays()) > 0 {
		t.Errorf("temporary files left: %v", b.strays())
	}
	// Nothing is handed to the host script before the yes.
	if got := b.settingsFile(); strings.Contains(got, "install") {
		t.Errorf("the settings file asks for an installation already: %q", got)
	}
	up, ok, _ := b.db.UpdateUpload()
	if !ok || up.State != store.UploadReady || up.Version != "0.2.4" || up.FromVersion != "0.2.3" || up.SHA256 != sha(data) || up.Released != "2026-11-20" {
		t.Errorf("upload = %+v", up)
	}
	// Coming back to the upload page within the hour shows the confirmation again.
	if r := b.get(uploadPath); r.status != http.StatusSeeOther || r.location != confirmPath {
		t.Errorf("upload page with a file waiting = %d %q", r.status, r.location)
	}
}

func TestTheConfirmationSaysWhatWillChange(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	pairedDisplay(t, b, "Kitchen", "eink_bw")
	pairedDisplay(t, b, "Hallway", "eink_bw")
	uploadGood(t, b)
	r := getEN(b, confirmPath)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d", r.status)
	}
	mustContain(t, r.body, `<h1>Install Hejmo 0.2.4</h1>`,
		`aria-current="page">Install Hejmo 0.2.4</span>`, `href="/settings/connections/update">Upload an update file</a>`,
		`<p class="sig-ok" role="status">`, "Signature checked: genuinely from the Hejmo project.", "Signed by the release workflow of github.com/hejmo-home/hejmo for version 0.2.4.",
		`<span class="fname">hejmo-0.2.4-arm64.update</span>, 1 MB`, "<dt>New version</dt><dd>Hejmo 0.2.4 from 20.11.2026</dd>", "<dt>Installed</dt><dd>Hejmo 0.2.3 from",
		`href="https://github.com/hejmo-home/hejmo/releases/tag/v0.2.4"`, "Release notes 0.2.4",
		`<section class="cost"`, "The box is unreachable for about 2 minutes.", `<span class="devs"><b>Displays:</b> Kitchen, Hallway</span>`,
		"The box saves your data first.", "goes back to Hejmo 0.2.3 with that data", "There is no way back to 0.2.3 afterwards.",
		`action="/settings/connections/update/install"`, "Install now", `formaction="/settings/connections/update/discard"`, "No, do not install",
		"Without an answer the box deletes it after an hour.")
	if strings.Count(r.body, `class="button"`) != 1 {
		t.Errorf("one primary button expected:\n%s", r.body)
	}
	de := getDE(b, confirmPath).body
	mustContain(t, de, "Hejmo 0.2.4 installieren", "Signatur geprüft: echt vom Hejmo-Projekt.", "Jetzt installieren", "Nein, nicht installieren", "Während der Installation")
}

func TestWithoutDisplaysTheConfirmationNamesNone(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	uploadGood(t, b)
	r := getEN(b, confirmPath)
	mustContain(t, r.body, "The box is unreachable for about 2 minutes.", "Devices and browsers briefly show “not reachable”.")
	mustNotContain(t, r.body, `class="devs"`, "Displays keep showing")
}

func TestARefusedFileGivesAFieldErrorInWordsAndIsDeleted(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	other := updatetest.New(t)
	good := b.release(updatetest.Spec{})
	for name, tc := range map[string]struct {
		file     string
		data     []byte
		contains []string
	}{
		"another signer":   {"hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{Foreign: other}), []string{"The signature of this file is not from the Hejmo project.", "installed nothing and deleted the file"}},
		"a wrong identity": {"hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{Identity: "https://github.com/someone/else/.github/workflows/release.yml@refs/tags/v0.2.4"}), []string{"The signature of this file is not from the Hejmo project."}},
		"no signature":     {"hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{Unsigned: true}), []string{"This file has no signature", "installs only signed files"}},
		"the other kind":   {"hejmo-0.2.4-amd64.update", b.release(updatetest.Spec{Arch: "amd64"}), []string{"This file is for amd64 (PC or mini PC).", "Your box needs the file for arm64: hejmo-0.2.4-arm64.update."}},
		"the same version": {"hejmo-0.2.3-arm64.update", b.release(updatetest.Spec{Version: "0.2.3"}), []string{"This file holds Hejmo 0.2.3. The box already runs 0.2.3.", "older or the same version"}},
		"an older version": {"hejmo-0.1.9-arm64.update", b.release(updatetest.Spec{Version: "0.1.9"}), []string{"This file holds Hejmo 0.1.9. The box already runs 0.2.3."}},
		"a pre-release":    {"hejmo-0.2.5-rc.1-arm64.update", b.release(updatetest.Spec{Version: "0.2.5-rc.1"}), []string{"0.2.5-rc.1 is a pre-release. The box installs only published versions."}},
		"a swapped image":  {"hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{SwapImage: []byte("another")}), []string{"This file is damaged or not a Hejmo update file."}},
		"not an archive":   {"hejmo-0.2.4-arm64.update", []byte("this is a text file"), []string{"This file is damaged or not a Hejmo update file."}},
		"another file":     {"backup-november.tar", good, []string{"That is not a Hejmo update file. Its name ends in “.update”."}},
		"no file chosen":   {"", nil, []string{"Choose the update file first."}},
	} {
		t.Run(name, func(t *testing.T) {
			r := b.postUpdate(tc.file, tc.data, nil)
			if r.status != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d\n%s", r.status, r.body)
			}
			mustContain(t, r.body, `<div class="f invalid">`, `aria-invalid="true"`, `aria-describedby="err-update_file hint-update_file"`, `<p class="ferr" id="err-update_file" role="alert">`)
			mustContain(t, r.body, tc.contains...)
			if _, err := os.Stat(b.pendingFile()); err == nil {
				t.Error("a refused file was kept")
			}
			if len(b.strays()) > 0 {
				t.Errorf("temporary files left: %v", b.strays())
			}
			if _, ok, _ := b.db.UpdateUpload(); ok {
				t.Error("a refused file waits for confirmation")
			}
			if strings.Contains(b.settingsFile(), "install") {
				t.Error("a refused file was handed to the host script")
			}
		})
	}
}

func TestTheFieldErrorIsInGermanToo(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	r := b.postUpdate("hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{Unsigned: true}), map[string]string{"Accept-Language": "de"})
	mustContain(t, r.body, "Diese Datei hat keine Signatur", "Die Box installiert nur signierte Dateien von der Release-Seite.")
}

func TestAFileOverTheLimitOrWithoutRoomIsRefused(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t, withMaxUpdateSize(1<<20))
	r := b.postUpdate("hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{Image: bytes.Repeat([]byte("x"), 2<<20)}), nil)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	mustContain(t, r.body, "“hejmo-0.2.4-arm64.update” is too big. At most 1 MB are allowed.")
	if len(b.strays()) > 0 {
		t.Errorf("a partial file is left: %v", b.strays())
	}
	// Room for the file, its copy for the host script, the new image and the data backup.
	b = offlineUpdateBox(t, withFreeSpace(1<<20))
	data := b.release(updatetest.Spec{Image: bytes.Repeat([]byte("y"), 3<<20)})
	r = b.postUpdate("hejmo-0.2.4-arm64.update", data, nil)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", r.status)
	}
	mustContain(t, r.body, "There is not enough room on the box: 1 MB free, 15 MB needed.")
	if _, err := os.Stat(b.pendingFile()); err == nil || len(b.strays()) > 0 {
		t.Error("a file was kept without room")
	}
}

// ---- confirm, install, discard

func TestConfirmingHandsTheFileToTheHostScriptThroughFixedKeys(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	data := uploadGood(t, b)
	r := b.postEN(confirmPath, url.Values{})
	if r.status != http.StatusSeeOther || r.location != confirmPath {
		t.Fatalf("confirm = %d %q", r.status, r.location)
	}
	want := "mode=on\noffline=yes\ninstall=0.2.4\ninstall_sha256=" + sha(data) + "\ninstall_time=" + stamp(b.now) + "\n"
	if got := b.settingsFile(); got != want {
		t.Errorf("settings file = %q, want %q", got, want)
	}
	up, _, _ := b.db.UpdateUpload()
	if up.State != store.UploadInstalling || up.StartedBy != "Mama" || !up.StartedAt.Equal(b.now) {
		t.Errorf("upload = %+v", up)
	}
	// The container only ever writes the settings file and the file itself, never anything the script would run.
	entries, _ := os.ReadDir(b.updateDir)
	for _, e := range entries {
		if e.Name() != "update-settings" && e.Name() != "update-file" && e.Name() != "update-status" {
			t.Errorf("unexpected file in the data volume: %s", e.Name())
		}
	}
	// A second yes changes nothing.
	b.now = b.now.Add(time.Minute)
	b.postEN(confirmPath, url.Values{})
	if got := b.settingsFile(); got != want {
		t.Errorf("settings file after a second yes = %q", got)
	}
}

func TestAFileThatChangedAfterTheCheckIsNotHandedOver(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	uploadGood(t, b)
	if err := os.WriteFile(b.pendingFile(), []byte("something else"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := b.postEN(confirmPath, url.Values{})
	if r.status != http.StatusSeeOther || r.location != uploadPath {
		t.Fatalf("confirm = %d %q", r.status, r.location)
	}
	if strings.Contains(b.settingsFile(), "install") {
		t.Error("a changed file was handed over")
	}
	if _, err := os.Stat(b.pendingFile()); err == nil {
		t.Error("the changed file stays")
	}
}

func TestInstallingShowsTheStepTheHostScriptReports(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	uploadGood(t, b)
	b.postEN(confirmPath, url.Values{})
	started := b.now.Add(2 * time.Second)

	// The script has not answered yet: the first step runs.
	page := getEN(b, confirmPath)
	mustContain(t, page.body, `<h1>Installing Hejmo 0.2.4</h1>`, `aria-current="page">Installing Hejmo 0.2.4</span>`, "Keep this page open.", "Started at 13:00:00.",
		`<li data-state="busy">`, "<b>Check signature</b><span class=\"st\">Running now, usually under a minute</span>",
		`<li data-state="wait">`, "<b>Save data</b>", "<b>Start new version</b><span class=\"st\">Next. The box is then unreachable for about 2 minutes.</span>",
		`data-progress-url="/api/v1/update/file"`, `src="/static/update-upload.js"`)
	mustNotContain(t, page.body, "Install now", "No, do not install")

	b.writeStatus("state=installing\nversion=0.2.3\ntarget=0.2.4\nstep=backup\ntime=" + stamp(started) + "\n")
	page = getEN(b, confirmPath)
	mustContain(t, page.body, `<b>Check signature</b><span class="st">Done: genuinely from the Hejmo project</span>`, `<b>Save data</b><span class="st">Running now</span>`)

	b.writeStatus("state=installing\nversion=0.2.3\ntarget=0.2.4\nstep=start\nbackup=" + stamp(started.Add(19*time.Second)) + "\ntime=" + stamp(started.Add(20*time.Second)) + "\n")
	page = getEN(b, confirmPath)
	mustContain(t, page.body, `<b>Save data</b><span class="st">Done at 13:00:21</span>`, "Running now. The box is not reachable, as intended. This page asks every 5 seconds.", `id="gone" hidden`, "The box has not answered for 10 minutes. Check whether it has power.")

	b.writeStatus("state=installing\nversion=0.2.3\ntarget=0.2.4\nstep=health\nbackup=" + stamp(started.Add(19*time.Second)) + "\ntime=" + stamp(started.Add(60*time.Second)) + "\n")
	page = getEN(b, confirmPath)
	mustContain(t, page.body, `<b>Start new version</b><span class="st">Done</span>`, `<b>Check that it answers</b><span class="st">Running now</span>`)
	// The German page says the same.
	mustContain(t, getDE(b, confirmPath).body, "Hejmo 0.2.4 wird installiert", "Signatur prüfen", "Daten sichern", "Neue Version starten", "Läuft gerade")
}

func TestAnotherParentSeesWhoStartedTheInstallation(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	up := store.UpdateUpload{Version: "0.2.4", FromVersion: "0.2.3", Arch: "arm64", FileName: "hejmo-0.2.4-arm64.update", Size: 1 << 20, SHA256: strings.Repeat("a", 64), Released: "2026-11-20", Signer: "x", UploadedAt: b.now}
	if err := b.db.SaveUpdateUpload(up); err != nil {
		t.Fatal(err)
	}
	if ok, _ := b.db.StartUpdateUpload("Papa", b.now); !ok {
		t.Fatal("not started")
	}
	r := getEN(b, uploadPath)
	if r.status != http.StatusSeeOther || r.location != confirmPath {
		t.Fatalf("upload page during an installation = %d %q", r.status, r.location)
	}
	page := getEN(b, confirmPath)
	mustContain(t, page.body, "Papa started the installation at 13:00:00. Wait until it is done; this page reloads by itself.")
	mustNotContain(t, page.body, "Install now", "Keep this page open.", `type="file"`)
	// One update at a time: no second file, no second yes.
	if r := b.postUpdate("hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{Version: "0.2.5"}), nil); r.status != http.StatusConflict {
		t.Errorf("a second upload = %d", r.status)
	}
	if got, _, _ := b.db.UpdateUpload(); got.Version != "0.2.4" || got.StartedBy != "Papa" {
		t.Errorf("upload = %+v", got)
	}
	if len(b.strays()) > 0 {
		t.Errorf("temporary files left: %v", b.strays())
	}
}

func TestSayingNoDeletesTheFile(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	uploadGood(t, b)
	r := b.postEN(uploadPath+"/discard", url.Values{})
	if r.status != http.StatusSeeOther || r.location != uploadPath {
		t.Fatalf("discard = %d %q", r.status, r.location)
	}
	if _, err := os.Stat(b.pendingFile()); err == nil {
		t.Error("the file stays")
	}
	if _, ok, _ := b.db.UpdateUpload(); ok {
		t.Error("the upload stays")
	}
	mustContain(t, getEN(b, uploadPath).body, "The file was deleted.", `type="file"`)
	// Saying no to nothing is harmless; saying no while it installs is not possible.
	if r := b.postEN(uploadPath+"/discard", url.Values{}); r.status != http.StatusSeeOther {
		t.Errorf("discard without a file = %d", r.status)
	}
	uploadGood(t, b)
	b.postEN(confirmPath, url.Values{})
	b.postEN(uploadPath+"/discard", url.Values{})
	if up, ok, _ := b.db.UpdateUpload(); !ok || up.State != store.UploadInstalling {
		t.Errorf("an installation was cancelled: %+v", up)
	}
	if _, err := os.Stat(b.pendingFile()); err != nil {
		t.Error("the file of a running installation was deleted")
	}
}

func TestAFileNobodyAnswersIsDeletedAfterAnHour(t *testing.T) {
	t.Parallel()
	b, secret := configuredBox(t)
	signInLater(t, b, secret, b.now)
	offlineOn(t, b)
	b.writeStatus("state=idle\nversion=0.2.3\n")
	uploadGood(t, b)
	signInLater(t, b, secret, b.now.Add(59*time.Minute))
	if r := b.get(confirmPath); r.status != http.StatusOK {
		t.Fatalf("after 59 minutes = %d", r.status)
	}
	signInLater(t, b, secret, b.now.Add(2*time.Minute)) // 61 minutes
	if r := b.get(confirmPath); r.status != http.StatusSeeOther || r.location != uploadPath {
		t.Errorf("after 61 minutes = %d %q", r.status, r.location)
	}
	if _, err := os.Stat(b.pendingFile()); err == nil {
		t.Error("the file is still there")
	}
	// A yes that comes too late starts nothing.
	uploadGood(t, b)
	signInLater(t, b, secret, b.now.Add(2*time.Hour))
	b.postEN(confirmPath, url.Values{})
	if strings.Contains(b.settingsFile(), "install") {
		t.Error("an expired file was handed over")
	}
}

// ---- the result

func TestAfterAnInstallationTheSuccessLineShowsOnceAndTheRequestIsGone(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	installedAt, _ := b.db.OutboundBox()
	b.now = installedAt.UpdatedAt.Add(time.Hour) // the box's own install date is its real creation time
	uploadGood(t, b)
	b.postEN(confirmPath, url.Values{})
	done := b.now.Add(2 * time.Minute)
	b.writeStatus("state=updated\nversion=0.2.4\nbackup=" + stamp(b.now.Add(20*time.Second)) + "\ntime=" + stamp(done) + "\n")
	b.now = done.Add(time.Minute)
	berlin, _ := time.LoadLocation("Europe/Berlin")
	r := getEN(b, uploadPath)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d %q", r.status, r.location)
	}
	mustContain(t, r.body, `<p class="msg ok" role="status" data-success>`, "Hejmo was updated to version 0.2.4.", `href="https://github.com/hejmo-home/hejmo/releases/tag/v0.2.4"`,
		"<dt>Installed</dt><dd>Hejmo 0.2.4 from 20.11.2026, installed on "+done.In(berlin).Format("02.01.2006")+" at "+done.In(berlin).Format("15:04")+"</dd>", `type="file"`)
	if strings.Index(r.body, `data-success`) > strings.Index(r.body, "<h1>") {
		t.Error("the success line comes after the heading")
	}
	if got := b.settingsFile(); strings.Contains(got, "install") {
		t.Errorf("the request stays in the settings file: %q", got)
	}
	if _, err := os.Stat(b.pendingFile()); err == nil {
		t.Error("the update file stays on the box")
	}
	if _, ok, _ := b.db.UpdateUpload(); ok {
		t.Error("the upload stays in the database")
	}
	if box, _ := b.db.OutboundBox(); !box.UpdatedAt.Equal(done) || box.InstalledVersion != "0.2.4" {
		t.Errorf("box = %+v", box) // the 90 days start again
	}
	// Shown once.
	mustNotContain(t, getEN(b, uploadPath).body, "data-success")
	mustNotContain(t, getEN(b, "/").body, "data-update-notice")
}

func TestAfterARollbackTheNoticeNamesTheReasonAndHowItWent(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	uploadGood(t, b)
	b.postEN(confirmPath, url.Values{})
	failed := b.now.Add(3 * time.Minute)
	b.writeStatus("state=rolled_back\nversion=0.2.3\ntarget=0.2.4\nreason=health_check_failed\nbackup=" + stamp(b.now.Add(31*time.Second)) + "\ntime=" + stamp(failed) + "\n")
	b.now = failed.Add(time.Minute)
	r := getEN(b, uploadPath)
	mustContain(t, r.body, `<div class="notice lead-in" role="status">`, "<b>The update to 0.2.4 did not work.</b>",
		"The new version did not answer after starting. The box went back to Hejmo 0.2.3 with your data from 13:00.", "so nothing is missing",
		`<form method="post" action="/update/dismiss"><input type="hidden" name="back" value="/settings/connections/update">`,
		`<h2 id="ran">How it went</h2>`, `<li data-state="done">`, `<li data-state="fail">`, "<b>Check that it answers</b>", "Failed: no answer after 2 minutes. That is why it went back to 0.2.3.", `type="file"`)
	if got := b.settingsFile(); strings.Contains(got, "install") {
		t.Errorf("the request stays in the settings file: %q", got)
	}
	if _, ok, _ := b.db.UpdateUpload(); ok {
		t.Error("the upload stays")
	}
	// "Understood" hides the notice and stays on the page.
	r = b.postEN("/update/dismiss", url.Values{"back": {"/settings/connections/update"}})
	if r.status != http.StatusSeeOther || r.location != uploadPath {
		t.Fatalf("dismiss = %d %q", r.status, r.location)
	}
	mustNotContain(t, getEN(b, uploadPath).body, "did not work", "How it went")
	// Only a page of the box can be the way back.
	if r := b.postEN("/update/dismiss", url.Values{"back": {"https://evil.example/"}}); r.location != "/" {
		t.Errorf("dismiss went to %q", r.location)
	}
	// The failing step depends on the reason.
	b.writeStatus("state=failed\nversion=0.2.3\ntarget=0.2.4\nreason=backup_failed\ntime=" + stamp(b.now) + "\n")
	page := getEN(b, uploadPath).body
	mustContain(t, page, "<b>The update to 0.2.4 was not installed.</b>", "could not save your data", "Nothing was changed", "<b>Save data</b>")
	mustNotContain(t, page, "<b>Start new version</b>")
}

func TestWhenGoingBackFailedTooThePageHasNoForm(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	b.writeStatus("state=failed\nversion=0.2.3\ntarget=0.2.4\nreason=rollback_failed\nbackup=" + stamp(b.now) + "\ntime=" + stamp(b.now) + "\n")
	r := getEN(b, uploadPath)
	mustContain(t, r.body, `<div class="error lead-in" role="alert">`, "The update failed, and so did going back to 0.2.3.", "/opt/hejmo/state/pre-update.db", "<dd>Unknown: the box reports no version</dd>")
	mustNotContain(t, r.body, `type="file"`, `enctype="multipart/form-data"`, "Upload and check")
	if r := b.postUpdate("hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{Version: "0.2.5"}), nil); r.status != http.StatusConflict {
		t.Errorf("upload after a failed rollback = %d", r.status)
	}
}

func TestAnInstallationThatNeverReportsIsGivenUpAfterTwoHours(t *testing.T) {
	t.Parallel()
	b, secret := configuredBox(t)
	signInLater(t, b, secret, b.now)
	offlineOn(t, b)
	b.writeStatus("state=idle\nversion=0.2.3\n")
	uploadGood(t, b)
	b.postEN(confirmPath, url.Values{})
	signInLater(t, b, secret, b.now.Add(119*time.Minute))
	if r := b.get(uploadPath); r.status != http.StatusSeeOther || r.location != confirmPath {
		t.Fatalf("after 119 minutes = %d %q", r.status, r.location)
	}
	signInLater(t, b, secret, b.now.Add(2*time.Minute))
	if r := b.get(uploadPath); r.status != http.StatusOK {
		t.Fatalf("after 121 minutes = %d %q", r.status, r.location)
	}
	if strings.Contains(b.settingsFile(), "install") {
		t.Error("the request stays for good")
	}
}

func TestAStatusFromBeforeTheInstallationIsNotItsResult(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	b.writeStatus("state=updated\nversion=0.2.3\ntime=" + stamp(b.now.Add(-time.Hour)) + "\n") // an older event
	uploadGood(t, b)
	b.postEN(confirmPath, url.Values{})
	if up, ok, _ := b.db.UpdateUpload(); !ok || up.State != store.UploadInstalling {
		t.Fatalf("the old report ended the installation: %+v", up)
	}
	// Nor is a report about another version.
	b.writeStatus("state=updated\nversion=0.2.9\ntime=" + stamp(b.now.Add(time.Minute)) + "\n")
	b.get(confirmPath)
	if up, ok, _ := b.db.UpdateUpload(); !ok || up.State != store.UploadInstalling {
		t.Fatalf("another version's report ended the installation: %+v", up)
	}
}

func TestTheSettingsFileKeepsTheRequestWhileTheParentsChangeSettings(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	uploadGood(t, b)
	b.postEN(confirmPath, url.Values{})
	want := b.settingsFile()
	b.post("/settings/connections", url.Values{"offline": {"on"}, "update_reminder_days": {"60"}})
	if got := b.settingsFile(); got != want {
		t.Errorf("settings file = %q, want %q", got, want)
	}
}

func TestARestartedBoxSettlesAnInstallationThatEnded(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	uploadGood(t, b)
	b.postEN(confirmPath, url.Values{})
	b.writeStatus("state=updated\nversion=0.2.4\ntime=" + stamp(b.now.Add(2*time.Minute)) + "\n")
	_ = server.New(server.Options{DB: b.db, PKI: b.pki, Public: b.public, UpdateDir: b.updateDir, UpdateInstall: true, Now: func() time.Time { return b.now.Add(3 * time.Minute) }}) // the new version starts
	if got := b.settingsFile(); strings.Contains(got, "install") {
		t.Errorf("settings file after the restart = %q", got)
	}
	if _, ok, _ := b.db.UpdateUpload(); ok {
		t.Error("the upload stays after the restart")
	}
	if _, err := os.Stat(b.pendingFile()); err == nil {
		t.Error("the file stays after the restart")
	}
}

func TestAStartingBoxDeletesWhatAnInterruptedUploadLeft(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	for name, data := range map[string]string{".update-upload-123": "partial", "update-file": "a file nobody asked to keep"} {
		if err := os.WriteFile(filepath.Join(b.updateDir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = server.New(server.Options{DB: b.db, PKI: b.pki, Public: b.public, UpdateDir: b.updateDir, UpdateInstall: true, Now: func() time.Time { return b.now }})
	if _, err := os.Stat(b.pendingFile()); err == nil || len(b.strays()) > 0 {
		t.Error("leftovers stay on the box")
	}
}

func TestAnUploadedFileIsNeverRunOrInterpolated(t *testing.T) {
	t.Parallel()
	// The name the browser sends is shown as plain text, never used as a path.
	b := offlineUpdateBox(t)
	name := `../../etc/<script>alert(1)</script>.update`
	r := b.postUpdate(name, b.release(updatetest.Spec{Unsigned: true}), nil)
	mustNotContain(t, r.body, "<script>alert(1)")
	entries, _ := os.ReadDir(filepath.Dir(b.updateDir))
	for _, e := range entries {
		if strings.Contains(e.Name(), "etc") {
			t.Errorf("a file named by the browser was written: %s", e.Name())
		}
	}
	b.postUpdate(name, b.release(updatetest.Spec{}), nil)
	if kept, _, _ := b.db.UpdateUpload(); strings.ContainsAny(kept.FileName, "/\\") || strings.Contains(kept.FileName, "..") {
		t.Errorf("file name kept as %q", kept.FileName)
	}
	if got := getEN(b, confirmPath).body; strings.Contains(got, "<script>alert(1)") {
		t.Errorf("the name is not escaped:\n%s", got)
	}
}

func httpRequest(tls bool) *http.Request {
	r, _ := http.NewRequest(http.MethodPost, "http://hejmo.local"+uploadPath, nil)
	if tls {
		r.TLS = &tls_.ConnectionState{}
	}
	return r
}

// A file takes minutes to arrive over a slow network; nobody else's page waits for it.
func TestAnUploadThatIsStillComingInDoesNotHoldUpOtherPages(t *testing.T) {
	t.Parallel()
	b := offlineUpdateBox(t)
	data := b.release(updatetest.Spec{})
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	started := make(chan struct{})
	finished := make(chan resp, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, b.srv.URL+uploadPath, pr)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Origin", b.srv.URL)
		res, err := b.client.Do(req)
		if err != nil {
			finished <- resp{status: -1, body: err.Error()}
			return
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		finished <- resp{res.StatusCode, res.Header.Get("Location"), string(out)}
	}()
	go func() {
		part, _ := mw.CreateFormFile("update_file", "hejmo-0.2.4-arm64.update")
		_, _ = part.Write(data[:len(data)/2])
		close(started)
	}()
	<-started
	time.Sleep(100 * time.Millisecond) // the first half is with the box now
	done := make(chan resp, 1)
	go func() { done <- getEN(b, uploadPath) }()
	select {
	case r := <-done:
		if r.status != http.StatusOK {
			t.Errorf("page during an upload = %d", r.status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a page waited for someone else's upload")
	}
	// The connection breaks: half a file is no file.
	_ = pw.CloseWithError(io.ErrUnexpectedEOF)
	r := <-finished
	if r.status == http.StatusSeeOther {
		t.Errorf("a broken upload was taken: %d", r.status)
	}
	if _, err := os.Stat(b.pendingFile()); err == nil {
		t.Error("half a file was kept")
	}
}
