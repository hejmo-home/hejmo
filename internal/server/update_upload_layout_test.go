package server_test

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/update/updatetest"
)

// updateUploadPages returns the pages of ticket 221 in the states a crawl does
// not reach, in German and English: the upload page with its field errors, the
// confirmation (with the displays of the family named), the installation at
// every step and as another parent sees it, the success line, the rollback
// notice and the failed rollback, and the pages that stand in for the form. The
// box is put back as it was.
func updateUploadPages(t *testing.T, f *layoutFixture) []layoutPage {
	t.Helper()
	b := f.box
	site := siteOf("app", b, f.client)
	var pages []layoutPage
	langs := []string{"de", "en"}
	both := func(name, path string, status int, get func(lang string) resp) {
		t.Helper()
		out := map[string]string{}
		for _, lang := range langs {
			r := get(lang)
			if r.status != status {
				t.Fatalf("%s %s = %d, want %d\n%s", name, path, r.status, status, r.body)
			}
			out[lang] = r.body
		}
		pages = append(pages, layoutPage{site: site, path: path + "?state=" + name, html: out})
	}
	view := func(name, path string, status int) {
		t.Helper()
		both(name, path, status, func(lang string) resp {
			return b.do(http.MethodGet, path, nil, map[string]string{"Accept-Language": lang})
		})
	}
	post := func(name string, status int, file string, data []byte) {
		t.Helper()
		both(name, uploadPath, status, func(lang string) resp { return b.postUpdate(file, data, map[string]string{"Accept-Language": lang}) })
	}
	status := func(s string) { b.writeStatus(s) }
	discard := func() {
		t.Helper()
		b.post(uploadPath+"/discard", url.Values{})
	}

	if err := b.db.SetOffline(true, b.now); err != nil {
		t.Fatal(err)
	}
	status("state=idle\nversion=0.2.3\n")
	view("update-form", uploadPath, http.StatusOK)

	other := updatetest.New(t)
	post("update-refused-signature", http.StatusUnprocessableEntity, "hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{Foreign: other}))
	post("update-refused-unsigned", http.StatusUnprocessableEntity, "hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{Unsigned: true}))
	post("update-refused-arch", http.StatusUnprocessableEntity, "hejmo-0.2.4-amd64.update", b.release(updatetest.Spec{Arch: "amd64"}))
	post("update-refused-old", http.StatusUnprocessableEntity, "hejmo-0.2.3-arm64.update", b.release(updatetest.Spec{Version: "0.2.3"}))
	post("update-refused-name", http.StatusUnprocessableEntity, "hejmo-sicherung-vom-november-mit-einem-sehr-langen-dateinamen-ohne-trennstellen-2026.tar", bytes.Repeat([]byte("x"), 100))
	post("update-refused-none", http.StatusUnprocessableEntity, "", nil)

	// The confirmation, with a display or two so that the cost box names them.
	pairedDisplay(t, b, "Küche", "eink_bw")
	pairedDisplay(t, b, "Kinderzimmer von Sebastian und Jonas", "eink_bw")
	if r := b.postUpdate("hejmo-0.2.4-arm64.update", b.release(updatetest.Spec{}), nil); r.status != http.StatusSeeOther {
		t.Fatalf("upload = %d\n%s", r.status, r.body)
	}
	view("update-confirm", confirmPath, http.StatusOK)
	b.post(confirmPath, url.Values{})
	view("update-installing", confirmPath, http.StatusOK)
	started := b.now.Add(time.Second)
	status("state=installing\nversion=0.2.3\ntarget=0.2.4\nstep=backup\ntime=" + stamp(started) + "\n")
	view("update-installing-backup", confirmPath, http.StatusOK)
	status("state=installing\nversion=0.2.3\ntarget=0.2.4\nstep=start\nbackup=" + stamp(started) + "\ntime=" + stamp(started) + "\n")
	view("update-installing-start", confirmPath, http.StatusOK)
	// Another parent looks at it.
	if _, err := b.db.SQL().Exec(`UPDATE update_upload SET started_by = 'Maximilian-Alexander von und zu Beispielhausen'`); err != nil {
		t.Fatal(err)
	}
	view("update-installing-other", confirmPath, http.StatusOK)

	// The ends.
	end := started.Add(time.Minute)
	status("state=rolled_back\nversion=0.2.3\ntarget=0.2.4\nreason=health_check_failed\nbackup=" + stamp(started) + "\ntime=" + stamp(end) + "\n")
	b.now = end.Add(time.Minute)
	view("update-rolled-back", uploadPath, http.StatusOK)
	status("state=failed\nversion=0.2.3\ntarget=0.2.4\nreason=rollback_failed\nbackup=" + stamp(started) + "\ntime=" + stamp(end) + "\n")
	view("update-rollback-failed", uploadPath, http.StatusOK)
	status("state=updated\nversion=0.2.4\ntime=" + stamp(end) + "\n")
	view("update-success", uploadPath, http.StatusOK)

	// The pages that stand in for the form.
	if err := b.db.SetOffline(false, b.now); err != nil {
		t.Fatal(err)
	}
	view("update-online", uploadPath, http.StatusOK)
	discard()
	return pages
}
