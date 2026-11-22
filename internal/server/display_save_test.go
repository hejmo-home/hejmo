package server_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/hejmo-home/hejmo/internal/store"
)

func shown(t *testing.T, f *familyBox) store.Display {
	t.Helper()
	ds, err := f.db.Displays(f.householdID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("displays = %v, %v", ds, err)
	}
	return ds[0]
}

// #104 leftover: a database error while looking a display up is a 500, not a 404.
func TestDisplayPageWithADatabaseErrorIsA500NotA404(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "screen")
	id := f.displayID(t)
	if r := f.get("/displays/" + id); r.status != http.StatusOK {
		t.Fatalf("display page = %d, want 200", r.status)
	}
	if _, err := f.db.SQL().Exec(`ALTER TABLE displays RENAME COLUMN theme TO theme_gone`); err != nil {
		t.Fatal(err)
	}
	if r := f.get("/displays/" + id); r.status != http.StatusInternalServerError {
		t.Fatalf("display page with a broken database = %d, want 500", r.status)
	}
	if r := f.post("/displays/"+id+"/unpair", url.Values{}); r.status != http.StatusInternalServerError {
		t.Fatalf("unpair with a broken database = %d, want 500", r.status)
	}
	if r := f.get("/displays/nonsense"); r.status != http.StatusInternalServerError {
		t.Fatalf("unknown display with a broken database = %d, want 500", r.status)
	}
}

// An unknown display stays a 404 while the database is fine.
func TestUnknownDisplayIsA404(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "screen")
	if r := f.get("/displays/nonsense"); r.status != http.StatusNotFound {
		t.Fatalf("unknown display = %d, want 404", r.status)
	}
}

// #104 leftover: the display theme and show_tomorrow are saved together, so a
// failed save changes neither.
func TestDisplayThemeAndShowTomorrowAreSavedTogether(t *testing.T) {
	t.Parallel()
	f := newFamilyBox(t, "screen")
	before := shown(t, f)
	if _, err := f.db.SQL().Exec(`CREATE TRIGGER no_theme BEFORE UPDATE OF theme ON displays BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"theme": {"dark"}}
	if !before.ShowTomorrow {
		form.Set("show_tomorrow", "1") // the switch is turned on; without the field it is turned off
	}
	if r := f.saveSettings(t, form); r.status != http.StatusInternalServerError {
		t.Fatalf("save that cannot store the theme = %d, want 500", r.status)
	}
	if got := shown(t, f); got.ShowTomorrow != before.ShowTomorrow || got.Theme != before.Theme {
		t.Fatalf("a failed save left a part behind: show_tomorrow %v -> %v, theme %q -> %q", before.ShowTomorrow, got.ShowTomorrow, before.Theme, got.Theme)
	}
}
