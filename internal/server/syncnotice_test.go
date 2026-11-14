package server_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

const syncNotice = `data-sync-notice`

// staleCalendar adds an ICS calendar that started syncing 25 hours before the
// family box's clock and never succeeded.
func staleCalendar(t *testing.T, f *familyBox) store.SyncedCalendar {
	t.Helper()
	c, err := f.db.CreateICSCalendar(f.householdID, store.NewICS{
		Name: "School", URL: "https://school.example/feed.ics", Color: "#3366aa",
		IntervalMinutes: 60, Now: f.at(13, 12, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var headerRe = regexp.MustCompile(`(?s)<header class="fv-header">.*?</header>`)

// 5.10: Given a stale synced calendar, then the header shows the info symbol and text (German and English).
func TestSyncNoticeShowsInTheHeaderInBothLanguages(t *testing.T) {
	f := newFamilyBox(t, "eink_bw")
	if page := f.view(t, "en", f.at(14, 13, 0)); strings.Contains(page, syncNotice) {
		t.Fatal("notice shown without any synced calendar")
	}
	staleCalendar(t, f)
	for lang, text := range map[string]string{
		"en": "Calendar not up to date – please open the web interface",
		"de": "Kalender nicht aktuell – bitte Weboberfläche öffnen",
	} {
		page := f.view(t, lang, f.at(14, 13, 0))
		header := headerRe.FindString(page)
		if !strings.Contains(header, syncNotice) || !strings.Contains(header, text) || !strings.Contains(header, "<svg") {
			t.Fatalf("%s: header lacks the info symbol and text:\n%s", lang, header)
		}
	}
	// Not yet 24 hours since the start of syncing: no notice.
	if page := f.view(t, "en", f.at(14, 11, 59)); strings.Contains(page, syncNotice) {
		t.Fatal("notice shown before 24 hours")
	}
}

// 5.10: The symbol does not appear in the person columns.
func TestSyncNoticeIsNotInThePersonColumns(t *testing.T) {
	f := newFamilyBox(t, "eink_bw")
	staleCalendar(t, f)
	page := f.view(t, "en", f.at(14, 13, 0))
	if !strings.Contains(page, syncNotice) {
		t.Fatal("no notice")
	}
	for _, col := range columnRe.FindAllString(page, -1) {
		if strings.Contains(col, syncNotice) || strings.Contains(col, "<svg") {
			t.Fatalf("notice in a column:\n%s", col)
		}
	}
}

// 5.10: Given the next successful sync, then the symbol disappears within 10 seconds.
// Runs with the box's default live interval, so it checks the real promise.
func TestSyncNoticeDisappearsWithin10SecondsOfTheNextSync(t *testing.T) {
	l := newLiveBox(t, 0)
	cal := staleCalendar(t, l.familyBox)
	l.clock.set(l.at(14, 13, 0))
	_, _, page := l.get("/display")
	if !strings.Contains(page, syncNotice) {
		t.Fatalf("no notice on a stale calendar:\n%s", page)
	}
	s := l.open(t)
	s.first(t)
	start := time.Now()
	if err := l.db.CompleteSync(l.householdID, cal.ID, nil, nil, l.clock.now(), l.clock.now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.next(t, 10*time.Second); !ok {
		t.Fatal("no new view announced within 10 seconds")
	}
	_, _, view := l.get("/display/view")
	if strings.Contains(view, syncNotice) {
		t.Fatalf("notice still in the view %v after the sync", time.Since(start))
	}
}
