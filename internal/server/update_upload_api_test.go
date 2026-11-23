package server_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/update/updatetest"
)

// Ticket 221 over REST (ADR 0015 parity): the same checks, the same one file
// at a time, the same yes. The client checks every request and answer against
// openapi.yaml.

func (c *apiClient) uploadUpdate(name string, data []byte) (int, map[string]any) {
	c.b.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("update_file", name)
	_, _ = part.Write(data)
	_ = mw.Close()
	raw, status, _ := c.send(http.MethodPost, "/update/file", mw.FormDataContentType(), body.Bytes())
	return status, decodeObject(c.b.t, raw)
}

func offlineUpdateAPI(t *testing.T, opts ...boxOption) *apiClient {
	t.Helper()
	c := signedInAPI(t, opts...)
	offlineOn(t, c.b)
	c.b.writeStatus("state=idle\nversion=0.2.3\n")
	return c
}

func TestTheUpdateFileSaysWhetherOneCanBeUploaded(t *testing.T) {
	t.Parallel()
	c := offlineUpdateAPI(t)
	status, got, _ := c.call(http.MethodGet, "/update/file", nil)
	if status != http.StatusOK || got["state"] != "none" || got["available"] != true {
		t.Fatalf("offline = %d %v", status, got)
	}
	online := signedInAPI(t)
	if _, got, _ := online.call(http.MethodGet, "/update/file", nil); got["available"] != false {
		t.Errorf("online = %v: the box updates itself", got)
	}
	plain := signedInAPI(t, withOldHostUpdater)
	offlineOn(t, plain.b)
	if _, got, _ := plain.call(http.MethodGet, "/update/file", nil); got["available"] != false {
		t.Errorf("beside an old host script = %v", got)
	}
}

func TestAnUpdateFileIsUploadedConfirmedAndFollowedThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := offlineUpdateAPI(t)
	data := c.b.release(updatetest.Spec{})
	status, got := c.uploadUpdate("hejmo-0.2.4-arm64.update", data)
	if status != http.StatusCreated || got["state"] != "ready" || got["version"] != "0.2.4" || got["from_version"] != "0.2.3" || got["arch"] != "arm64" ||
		got["sha256"] != sha(data) || got["released"] != "2026-11-20" || got["file_name"] != "hejmo-0.2.4-arm64.update" || got["size"] != float64(len(data)) {
		t.Fatalf("upload = %d %v", status, got)
	}
	if got["expires_at"] == nil || got["step"] != nil {
		t.Errorf("a waiting file has an end of waiting and no step: %v", got)
	}
	if strings.Contains(c.b.settingsFile(), "install") {
		t.Error("the host script was asked before the yes")
	}
	status, got, _ = c.call(http.MethodPost, "/update/file/install", nil)
	if status != http.StatusAccepted || got["state"] != "installing" || got["step"] != "verify" || got["started_by"] != "Mama" {
		t.Fatalf("install = %d %v", status, got)
	}
	if !strings.Contains(c.b.settingsFile(), "install=0.2.4\ninstall_sha256="+sha(data)+"\n") {
		t.Errorf("settings file = %q", c.b.settingsFile())
	}
	c.b.writeStatus("state=installing\nversion=0.2.3\ntarget=0.2.4\nstep=backup\ntime=" + stamp(c.b.now.Add(time.Second)) + "\n")
	if _, got, _ := c.call(http.MethodGet, "/update/file", nil); got["step"] != "backup" {
		t.Errorf("step = %v", got["step"])
	}
	// One at a time.
	if status, got, _ := c.call(http.MethodPost, "/update/file/install", nil); status != http.StatusConflict {
		t.Errorf("second install = %d %v", status, got)
	}
	if status, got := c.uploadUpdate("hejmo-0.2.4-arm64.update", data); status != http.StatusConflict || got["code"] != "busy" {
		t.Errorf("upload during an installation = %d %v", status, got)
	}
	if status, _, _ := c.call(http.MethodDelete, "/update/file", nil); status != http.StatusConflict {
		t.Errorf("delete during an installation = %d", status)
	}
	// The host script reports the end: the request goes, and the result is in GET /update.
	c.b.writeStatus("state=updated\nversion=0.2.4\ntime=" + stamp(c.b.now.Add(2*time.Minute)) + "\n")
	if _, got, _ := c.call(http.MethodGet, "/update/file", nil); got["state"] != "none" {
		t.Errorf("after the end = %v", got)
	}
	if status, got, _ := c.call(http.MethodGet, "/update", nil); status != http.StatusOK || got["state"] != "updated" || got["version"] != "0.2.4" {
		t.Errorf("update = %d %v", status, got)
	}
}

// The API answers in the language of the request: German without an Accept-Language header, like a page.
func TestARefusedFileIsAnswerdWithItsCodeAndFieldThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := offlineUpdateAPI(t, withMaxUpdateSize(1<<20))
	other := updatetest.New(t)
	for name, tc := range map[string]struct {
		file   string
		data   []byte
		status int
		code   string
		text   string
	}{
		"another signer": {"x.update", c.b.release(updatetest.Spec{Foreign: other}), http.StatusUnprocessableEntity, "signature_invalid", "nicht vom Hejmo-Projekt"},
		"unsigned":       {"x.update", c.b.release(updatetest.Spec{Unsigned: true}), http.StatusUnprocessableEntity, "no_signature", "keine Signatur"},
		"the other kind": {"x.update", c.b.release(updatetest.Spec{Arch: "amd64"}), http.StatusUnprocessableEntity, "wrong_arch", "Deine Box braucht die Datei für arm64"},
		"not newer":      {"x.update", c.b.release(updatetest.Spec{Version: "0.2.3"}), http.StatusUnprocessableEntity, "not_newer", "läuft schon mit 0.2.3"},
		"a pre-release":  {"x.update", c.b.release(updatetest.Spec{Version: "0.3.0-rc.1"}), http.StatusUnprocessableEntity, "prerelease", "Vorabversion"},
		"a bad name":     {"x.tar", c.b.release(updatetest.Spec{}), http.StatusUnprocessableEntity, "not_update_name", "endet auf „.update“"},
		"too big":        {"x.update", c.b.release(updatetest.Spec{Image: bytes.Repeat([]byte("z"), 2<<20)}), http.StatusRequestEntityTooLarge, "too_big_server", "höchstens 1 MB"},
	} {
		t.Run(name, func(t *testing.T) {
			status, got := c.uploadUpdate(tc.file, tc.data)
			if status != tc.status || got["code"] != tc.code || got["field"] != "update_file" || !strings.Contains(got["error"].(string), tc.text) {
				t.Fatalf("= %d %v", status, got)
			}
			if _, err := os.Stat(c.b.pendingFile()); err == nil || len(c.b.strays()) > 0 {
				t.Error("a refused file was kept")
			}
		})
	}
	if _, got, _ := c.call(http.MethodGet, "/update/file", nil); got["state"] != "none" {
		t.Errorf("state = %v", got["state"])
	}
}

func TestNoRoomIsAnsweredWith507(t *testing.T) {
	t.Parallel()
	c := offlineUpdateAPI(t, withFreeSpace(1<<20))
	status, got := c.uploadUpdate("x.update", c.b.release(updatetest.Spec{Image: bytes.Repeat([]byte("y"), 3<<20)}))
	if status != http.StatusInsufficientStorage || got["code"] != "no_room" {
		t.Fatalf("= %d %v", status, got)
	}
}

func TestAWaitingFileIsDeletedThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := offlineUpdateAPI(t)
	if status, _, _ := c.call(http.MethodDelete, "/update/file", nil); status != http.StatusNotFound {
		t.Errorf("delete without a file = %d", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/update/file/install", nil); status != http.StatusNotFound {
		t.Errorf("install without a file = %d", status)
	}
	if status, _ := c.uploadUpdate("hejmo-0.2.4-arm64.update", c.b.release(updatetest.Spec{})); status != http.StatusCreated {
		t.Fatalf("upload = %d", status)
	}
	if status, _, _ := c.call(http.MethodDelete, "/update/file", nil); status != http.StatusNoContent {
		t.Errorf("delete = %d", status)
	}
	if _, err := os.Stat(c.b.pendingFile()); err == nil {
		t.Error("the file stays")
	}
}

func TestUploadingNeedsTheWriteScopeOfAParent(t *testing.T) {
	t.Parallel()
	c := offlineUpdateAPI(t)
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodGet, "/update/file", nil); status != http.StatusOK {
		t.Errorf("a read token cannot look: %d", status)
	}
	if status, _ := ro.uploadUpdate("x.update", c.b.release(updatetest.Spec{})); status != http.StatusForbidden {
		t.Errorf("a read token uploaded: %d", status)
	}
	if status, _, _ := ro.call(http.MethodPost, "/update/file/install", nil); status != http.StatusForbidden {
		t.Errorf("a read token installed: %d", status)
	}
	if status, _, _ := ro.call(http.MethodDelete, "/update/file", nil); status != http.StatusForbidden {
		t.Errorf("a read token deleted: %d", status)
	}
	child := childSession(t, c.b)
	cc := newAPIClient(t, child)
	cc.cookie = true
	if status, _ := cc.uploadUpdate("x.update", c.b.release(updatetest.Spec{})); status != http.StatusForbidden {
		t.Errorf("a child uploaded: %d", status)
	}
	if _, err := os.Stat(c.b.pendingFile()); err == nil {
		t.Error("a file was kept")
	}
}
