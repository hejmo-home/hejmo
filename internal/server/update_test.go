package server_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const releaseNotes = "https://github.com/hejmo-home/hejmo/releases/tag/v0.2.0"

func (b *box) writeStatus(content string) {
	b.t.Helper()
	if err := os.WriteFile(filepath.Join(b.updateDir, "update-status"), []byte(content), 0o644); err != nil {
		b.t.Fatal(err)
	}
}

func (b *box) settingsFile() string {
	data, _ := os.ReadFile(filepath.Join(b.updateDir, "update-settings"))
	return string(data)
}

// 7.12: Settings: on (default), off, notify only. The box hands the choice to
// the host script through the settings file.
func TestSettingsScreenSavesTheAutoUpdateMode(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	body := b.get("/settings/household").body
	if !strings.Contains(body, `name="auto_update"`) || !strings.Contains(body, `value="on" checked`) {
		t.Fatalf("auto-update not offered with on as default:\n%s", body)
	}
	for _, v := range []string{"on", "off", "notify"} {
		if !strings.Contains(body, `name="auto_update" value="`+v+`"`) {
			t.Errorf("no choice %q", v)
		}
	}
	form := func(mode string) url.Values {
		return url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}, "auto_update": {mode}}
	}
	if r := b.post("/settings/household", form("notify")); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
	if got := b.settingsFile(); got != "mode=notify\n" {
		t.Fatalf("settings file = %q", got)
	}
	if body := b.get("/settings/household").body; !strings.Contains(body, `value="notify" checked`) {
		t.Fatalf("notify not selected:\n%s", body)
	}
	// A form without the field (older clients) leaves the mode alone.
	b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}})
	if body := b.get("/settings/household").body; !strings.Contains(body, `value="notify" checked`) {
		t.Fatalf("mode changed by a form without it:\n%s", body)
	}
	if r := b.post("/settings/household", form("sometimes")); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("bad mode = %d, want 422", r.status)
	}
	if got := b.settingsFile(); got != "mode=notify\n" {
		t.Fatalf("settings file after refusal = %q", got)
	}
}

func TestAutoUpdateModeWorksThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, got, _ := c.call(http.MethodGet, "/settings", nil)
	if status != http.StatusOK || got["auto_update"] != "on" {
		t.Fatalf("get = %d %v", status, got)
	}
	status, got, _ = c.call(http.MethodPatch, "/settings", map[string]any{"auto_update": "off"})
	if status != http.StatusOK || got["auto_update"] != "off" || got["time_format"] != "24h" {
		t.Fatalf("patch = %d %v", status, got)
	}
	if c.b.settingsFile() != "mode=off\n" {
		t.Fatalf("settings file = %q", c.b.settingsFile())
	}
	if status, _, _ := c.call(http.MethodPatch, "/settings", map[string]any{"auto_update": "maybe"}); status != http.StatusUnprocessableEntity {
		t.Fatalf("bad mode = %d, want 422", status)
	}
}

func TestAfterAnUpdateTheWebInterfaceShowsWhatIsNewOnce(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	if body := b.get("/").body; strings.Contains(body, "data-update-notice") {
		t.Fatal("notice without any update")
	}
	b.writeStatus("state=updated\nversion=0.2.0\ntime=2026-11-16T03:30:12Z\n")
	body := b.get("/").body
	if !strings.Contains(body, `data-update-notice="updated"`) || !strings.Contains(body, "0.2.0") || !strings.Contains(body, releaseNotes) {
		t.Fatalf("no what's-new notice:\n%s", body)
	}
	if r := b.post("/update/dismiss", url.Values{}); r.status != http.StatusSeeOther {
		t.Fatalf("dismiss = %d", r.status)
	}
	if strings.Contains(b.get("/").body, "data-update-notice") || strings.Contains(b.get("/settings/household").body, "data-update-notice") {
		t.Fatal("notice shown again after dismissing")
	}
	// The next event is a new notice.
	b.writeStatus("state=updated\nversion=0.2.1\ntime=2026-11-17T03:30:09Z\n")
	if !strings.Contains(b.get("/").body, "0.2.1") {
		t.Fatal("a later update is not announced")
	}
}

func TestAfterARollbackParentsSeeTheReasonInBothLanguages(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	b.writeStatus("state=rolled_back\nversion=0.1.0\ntarget=0.2.0\nreason=health_check_failed\ntime=2026-11-16T03:31:00Z\n")
	for lang, want := range map[string]string{
		"en": "did not answer after the update",
		"de": "antwortete nach dem Update nicht",
	} {
		r := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": lang})
		if !strings.Contains(r.body, `data-update-notice="rolled_back"`) || !strings.Contains(r.body, want) || !strings.Contains(r.body, "0.2.0") || !strings.Contains(r.body, "0.1.0") {
			t.Fatalf("%s: rollback notice lacks the reason %q:\n%s", lang, want, r.body)
		}
	}
	// Also on the settings page, where parents look for it.
	if !strings.Contains(b.get("/settings/household").body, `data-update-notice="rolled_back"`) {
		t.Fatal("no notice on the settings page")
	}
}

func TestARejectedImageIsReportedWithItsReason(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	b.writeStatus("state=rejected\nversion=0.1.0\ntarget=0.2.0\nreason=signature_invalid\ntime=2026-11-16T03:31:00Z\n")
	body := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": "en"}).body
	if !strings.Contains(body, `data-update-notice="rejected"`) || !strings.Contains(body, "signature") {
		t.Fatalf("no rejection notice:\n%s", body)
	}
}

func TestNotifyOnlyTellsParentsThatAnUpdateIsAvailable(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	b.post("/settings/household", url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}, "auto_update": {"notify"}})
	b.writeStatus("state=available\nversion=0.1.0\ntarget=0.2.0\ntime=2026-11-16T03:30:05Z\n")
	body := b.get("/").body
	if !strings.Contains(body, `data-update-notice="available"`) || !strings.Contains(body, "0.2.0") {
		t.Fatalf("no available notice:\n%s", body)
	}
	// An available notice is not dismissed away: it stays until the update is installed.
	b.post("/update/dismiss", url.Values{})
	if !strings.Contains(b.get("/").body, `data-update-notice="available"`) {
		t.Fatal("availability notice vanished by dismissing")
	}
	// Once the family switches auto-update on, a stale report is not shown.
	b.post("/settings/household", url.Values{"time_format": {"24h"}, "temperature_unit": {"c"}, "auto_update": {"on"}})
	if strings.Contains(b.get("/").body, `data-update-notice="available"`) {
		t.Fatal("stale availability shown with auto-update on")
	}
}

func TestChildrenSeeNoUpdateNotices(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	child := childSession(t, b)
	b.writeStatus("state=rolled_back\nversion=0.1.0\ntarget=0.2.0\nreason=start_failed\ntime=2026-11-16T03:31:00Z\n")
	if strings.Contains(child.get("/").body, "data-update-notice") {
		t.Fatal("a child sees the update notice")
	}
	if r := child.post("/update/dismiss", url.Values{}); r.status != http.StatusForbidden {
		t.Fatalf("child dismiss = %d, want 403", r.status)
	}
}

func TestUpdateStatusWorksThroughTheAPI(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t)
	status, got, _ := c.call(http.MethodGet, "/update", nil)
	if status != http.StatusOK || got["mode"] != "on" || got["state"] != "idle" || got["notice"] != false || got["managed"] != true {
		t.Fatalf("empty = %d %v", status, got)
	}
	c.b.writeStatus("state=rolled_back\nversion=0.1.0\ntarget=0.2.0\nreason=start_failed\ntime=2026-11-16T03:31:00Z\n")
	status, got, _ = c.call(http.MethodGet, "/update", nil)
	if status != http.StatusOK || got["state"] != "rolled_back" || got["version"] != "0.1.0" || got["target"] != "0.2.0" || got["reason"] != "start_failed" || got["notice"] != true {
		t.Fatalf("rolled back = %d %v", status, got)
	}
	ro := c.withToken(c.newToken("read"))
	if status, _, _ := ro.call(http.MethodPost, "/update/dismiss", nil); status != http.StatusForbidden {
		t.Fatalf("read token dismisses = %d, want 403", status)
	}
	if status, _, _ := c.call(http.MethodPost, "/update/dismiss", nil); status != http.StatusNoContent {
		t.Fatalf("dismiss = %d", status)
	}
	if _, got, _ := c.call(http.MethodGet, "/update", nil); got["notice"] != false {
		t.Fatalf("notice after dismissing: %v", got)
	}
}

// A plain Docker Compose install has no updater: the box must not promise updates.
func TestWithoutAHostUpdaterTheSettingIsNotOffered(t *testing.T) {
	t.Parallel()
	b := signedInBox(t, withoutHostUpdater)
	body := b.get("/settings/household").body
	if strings.Contains(body, "auto_update") || strings.Contains(body, "03:30") {
		t.Fatalf("auto-update offered without a host updater:\n%s", body)
	}
	if !strings.Contains(body, `name="time_format"`) {
		t.Fatal("the other settings are gone")
	}
	// Saving the rest of the form still works.
	if r := b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}}); r.status != http.StatusSeeOther {
		t.Fatalf("save = %d", r.status)
	}
}

// The API must not promise updates either: it says whether an updater is there.
func TestAPISaysWhetherAHostUpdaterManagesTheBox(t *testing.T) {
	t.Parallel()
	c := signedInAPI(t, withoutHostUpdater)
	if status, got, _ := c.call(http.MethodGet, "/update", nil); status != http.StatusOK || got["managed"] != false {
		t.Fatalf("plain Compose install = %d %v, want managed false", status, got)
	}
}

func TestWithAHostUpdaterTheSettingIsOffered(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	if !strings.Contains(b.get("/settings/household").body, `name="auto_update"`) {
		t.Fatal("auto-update not offered")
	}
}

func TestFailedRollbackDoesNotSayTheBoxStillRunsThePreviousVersion(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	b.writeStatus("state=failed\nversion=0.1.0\ntarget=0.2.0\nreason=rollback_failed\ntime=2026-11-16T03:31:00Z\n")
	for lang, bad := range map[string]string{"en": "still running the previous version", "de": "läuft weiter mit der vorherigen Version"} {
		r := b.do(http.MethodGet, "/", nil, map[string]string{"Accept-Language": lang})
		if !strings.Contains(r.body, `data-update-notice="failed"`) || strings.Contains(r.body, bad) {
			t.Fatalf("%s: contradictory notice:\n%s", lang, r.body)
		}
		if strings.Contains(r.body, "Reason:") || strings.Contains(r.body, "Grund:") {
			t.Fatalf("%s: the notice says the same thing twice:\n%s", lang, r.body)
		}
	}
}

func TestMissingBoxReasonIsTranslated(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	b.writeStatus("state=failed\nversion=0.1.0\ntarget=0.2.0\nreason=box_not_running\ntime=2026-11-16T03:31:00Z\n")
	body := b.get("/").body
	if strings.Contains(body, "update.reason") || strings.Contains(body, "backup") {
		t.Fatalf("notice:\n%s", body)
	}
}

func TestSavingSettingsToleratesAFailingSettingsFile(t *testing.T) {
	t.Parallel()
	b := signedInBox(t)
	if err := os.RemoveAll(b.updateDir); err != nil {
		t.Fatal(err)
	}
	r := b.post("/settings/household", url.Values{"time_format": {"12h"}, "temperature_unit": {"c"}, "auto_update": {"off"}})
	if r.status != http.StatusSeeOther {
		t.Fatalf("save = %d, want 303 for a change that was stored", r.status)
	}
	if !strings.Contains(b.get("/settings/household").body, `value="off" checked`) {
		t.Fatal("the change was not stored")
	}
}
