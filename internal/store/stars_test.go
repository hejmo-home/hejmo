package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/task"
)

func (f taskFixture) starTask(t *testing.T, stars int) task.Task {
	t.Helper()
	k, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "Tidy room", Repeat: task.Daily, Date: "2026-11-14", Stars: stars})
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func (f taskFixture) balance(t *testing.T, person string) int {
	t.Helper()
	acc, err := f.db.StarAccount(f.h.ID, person, 100)
	if err != nil {
		t.Fatal(err)
	}
	return acc.Balance
}

func TestTaskStarValueRoundTrips(t *testing.T) {
	f := newTaskFixture(t)
	k := f.starTask(t, 3)
	got, err := f.db.Task(f.h.ID, k.ID)
	if err != nil || got.Stars != 3 {
		t.Fatalf("Task = %+v, %v", got, err)
	}
	if _, err := f.db.AddTask(f.h.ID, task.Task{PersonID: f.mia.ID, Title: "x", Repeat: task.Daily, Date: "2026-11-14", Stars: -1}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("negative stars = %v, want ErrInvalid", err)
	}
}

// Acceptance: Given a task worth 3 stars, when Mia ticks it off, then her
// balance rises by 3 and a log entry is created.
func TestTickingATaskCreditsItsStarsAndLogsThem(t *testing.T) {
	f := newTaskFixture(t)
	k := f.starTask(t, 3)
	if err := f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", true, f.mia.ID, taskNow); err != nil {
		t.Fatal(err)
	}
	acc, err := f.db.StarAccount(f.h.ID, f.mia.ID, 10)
	if err != nil || acc.Balance != 3 || len(acc.Entries) != 1 {
		t.Fatalf("account = %+v, %v", acc, err)
	}
	e := acc.Entries[0]
	if e.Delta != 3 || e.TaskID != k.ID || e.TaskTitle != "Tidy room" || e.Day != "2026-11-14" || e.BookedBy != f.mia.ID || !e.At.Equal(taskNow) {
		t.Fatalf("entry = %+v", e)
	}
}

// Acceptance: Given a ticked task, when it is unticked, then the stars are
// deducted and logged.
func TestUntickingDeductsAndLogsAndTickingTwiceCreditsOnce(t *testing.T) {
	f := newTaskFixture(t)
	k := f.starTask(t, 3)
	tick := func(done bool) {
		t.Helper()
		if err := f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", done, "", taskNow); err != nil {
			t.Fatal(err)
		}
	}
	tick(true)
	tick(true) // harmless
	if b := f.balance(t, f.mia.ID); b != 3 {
		t.Fatalf("after two ticks = %d, want 3", b)
	}
	tick(false)
	tick(false) // harmless
	acc, _ := f.db.StarAccount(f.h.ID, f.mia.ID, 10)
	if acc.Balance != 0 || len(acc.Entries) != 2 || acc.Entries[0].Delta != -3 || acc.Entries[1].Delta != 3 {
		t.Fatalf("account = %+v", acc)
	}
	if acc.Entries[0].BookedBy != "" {
		t.Errorf("a display books without a person, got %q", acc.Entries[0].BookedBy)
	}
}

// Unticking returns what was credited, even if the value changed meanwhile
// (no edit exists yet, so the stored credit is the source of truth).
func TestUntickingDeductsWhatWasCredited(t *testing.T) {
	f := newTaskFixture(t)
	k := f.starTask(t, 2)
	_ = f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", true, "", taskNow)
	if _, err := f.db.SQL().Exec(`UPDATE tasks SET stars = 5 WHERE id = ?`, k.ID); err != nil {
		t.Fatal(err)
	}
	_ = f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", false, "", taskNow)
	if b := f.balance(t, f.mia.ID); b != 0 {
		t.Fatalf("balance = %d, want 0", b)
	}
}

func TestATaskWorthNoStarsLogsNothing(t *testing.T) {
	f := newTaskFixture(t)
	k := f.starTask(t, 0)
	_ = f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", true, "", taskNow)
	acc, _ := f.db.StarAccount(f.h.ID, f.mia.ID, 10)
	if acc.Balance != 0 || len(acc.Entries) != 0 || acc.Shown {
		t.Fatalf("account = %+v", acc)
	}
}

// Acceptance: Given a parent books -5 with note "redeemed: ice cream", then
// the balance and the log show it.
func TestParentBooksStarsByHandWithANote(t *testing.T) {
	f := newTaskFixture(t)
	k := f.starTask(t, 8)
	_ = f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", true, "", taskNow)
	e, err := f.db.BookStars(f.h.ID, f.mia.ID, -5, "redeemed: ice cream", f.mama.ID, taskNow.Add(time.Hour))
	if err != nil || e.ID == "" || e.Delta != -5 || e.Note != "redeemed: ice cream" || e.BookedBy != f.mama.ID {
		t.Fatalf("BookStars = %+v, %v", e, err)
	}
	acc, _ := f.db.StarAccount(f.h.ID, f.mia.ID, 10)
	if acc.Balance != 3 || len(acc.Entries) != 2 || acc.Entries[0].Note != "redeemed: ice cream" || acc.Entries[0].BookedByName != "Mama" {
		t.Fatalf("account = %+v", acc)
	}
	if _, err := f.db.BookStars(f.h.ID, f.mia.ID, 0, "x", f.mama.ID, taskNow); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("zero booking = %v, want ErrInvalid", err)
	}
	if _, err := f.db.BookStars(f.h.ID, "nobody", 1, "x", f.mama.ID, taskNow); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown person = %v, want ErrNotFound", err)
	}
}

// Acceptance: Star bookings are stored per person, not per household or
// membership: the log survives leaving a household and has no household column.
func TestStarBookingsBelongToThePersonNotTheHousehold(t *testing.T) {
	f := newTaskFixture(t)
	_, _ = f.db.BookStars(f.h.ID, f.mia.ID, 4, "gift", f.mama.ID, taskNow)
	rows, err := f.db.SQL().Query(`PRAGMA table_info(star_log)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, name)
	}
	hasPerson := false
	for _, c := range cols {
		if c == "household_id" || c == "membership_id" {
			t.Errorf("star_log has column %q", c)
		}
		hasPerson = hasPerson || c == "person_id"
	}
	if !hasPerson {
		t.Fatalf("star_log lacks person_id: %v", cols)
	}
	// Removing the membership does not touch the account.
	if _, err := f.db.SQL().Exec(`DELETE FROM household_members WHERE person_id = ?`, f.mia.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = f.db.SQL().QueryRow(`SELECT COALESCE(SUM(delta), 0) FROM star_log WHERE person_id = ?`, f.mia.ID).Scan(&n)
	if n != 4 {
		t.Fatalf("balance after leaving the household = %d, want 4", n)
	}
}

func TestDeletingATaskKeepsItsStarBookings(t *testing.T) {
	f := newTaskFixture(t)
	k := f.starTask(t, 2)
	_ = f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", true, "", taskNow)
	if err := f.db.DeleteTask(f.h.ID, k.ID); err != nil {
		t.Fatal(err)
	}
	acc, _ := f.db.StarAccount(f.h.ID, f.mia.ID, 10)
	if acc.Balance != 2 || len(acc.Entries) != 1 || acc.Entries[0].TaskID != "" || acc.Entries[0].TaskTitle != "Tidy room" {
		t.Fatalf("account = %+v", acc)
	}
}

// ADR 0018: stars show only for people with at least one task worth stars or a
// star booking.
func TestStarsShowOnlyForPeopleWithStarTasksOrBookings(t *testing.T) {
	f := newTaskFixture(t)
	shown := func() map[string]store.StarSummary {
		m, err := f.db.StarSummaries(f.h.ID)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if s := shown(); s[f.mia.ID].Shown || s[f.mama.ID].Shown {
		t.Fatalf("nobody has stars yet: %+v", s)
	}
	f.starTask(t, 0)
	if shown()[f.mia.ID].Shown {
		t.Fatal("a task worth 0 stars does not show stars")
	}
	f.starTask(t, 1)
	if s := shown(); !s[f.mia.ID].Shown || s[f.mia.ID].Balance != 0 || s[f.mama.ID].Shown {
		t.Fatalf("a star task shows stars for Mia only: %+v", s)
	}
	_, _ = f.db.BookStars(f.h.ID, f.mama.ID, 2, "bonus", f.mama.ID, taskNow)
	if s := shown(); !s[f.mama.ID].Shown || s[f.mama.ID].Balance != 2 {
		t.Fatalf("a booking shows stars: %+v", s)
	}
}

// Acceptance (#122): changing a task's star value leaves credited ticks and
// the star log as they are; unticking deducts the credited value.
func TestSetTaskStarsLeavesCreditedTicksAndTheLogAlone(t *testing.T) {
	f := newTaskFixture(t)
	k := f.starTask(t, 2)
	_ = f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", true, "", taskNow)
	before, _ := f.db.StarAccount(f.h.ID, f.mia.ID, 10)
	if err := f.db.SetTaskStars(f.h.ID, k.ID, 5); err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.Task(f.h.ID, k.ID)
	if got.Stars != 5 {
		t.Fatalf("stars = %d, want 5", got.Stars)
	}
	if n, ok, err := f.db.TaskDoneStars(k.ID, "2026-11-14"); err != nil || !ok || n != 2 {
		t.Fatalf("credited = %d, %v, %v; want 2", n, ok, err)
	}
	after, _ := f.db.StarAccount(f.h.ID, f.mia.ID, 10)
	if after.Balance != before.Balance || len(after.Entries) != len(before.Entries) {
		t.Fatalf("log changed: %+v -> %+v", before, after)
	}
	_ = f.db.SetTaskDone(f.h.ID, k.ID, "2026-11-14", false, "", taskNow)
	if b := f.balance(t, f.mia.ID); b != 0 {
		t.Fatalf("balance after untick = %d, want 0 (credited value deducted, not the new one)", b)
	}
	if _, ok, _ := f.db.TaskDoneStars(k.ID, "2026-11-14"); ok {
		t.Fatal("task still done")
	}
}

func TestSetTaskStarsRefusesOutOfRangeAndUnknownTasks(t *testing.T) {
	f := newTaskFixture(t)
	k := f.starTask(t, 2)
	for _, bad := range []int{-1, task.MaxStars + 1} {
		err := f.db.SetTaskStars(f.h.ID, k.ID, bad)
		var inv *task.InvalidError
		if !errors.Is(err, store.ErrInvalid) || !errors.As(err, &inv) || inv.Field != "stars" {
			t.Errorf("%d = %v, want ErrInvalid on field stars", bad, err)
		}
	}
	if err := f.db.SetTaskStars(f.h.ID, k.ID, 0); err != nil {
		t.Errorf("0 = %v", err)
	}
	if err := f.db.SetTaskStars(f.h.ID, "nope", 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown = %v, want ErrNotFound", err)
	}
	if err := f.db.SetTaskStars("other-household", k.ID, 1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("foreign household = %v, want ErrNotFound", err)
	}
}
