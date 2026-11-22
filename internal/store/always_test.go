package store_test

import (
	"errors"
	"testing"

	"github.com/hejmo-home/hejmo/internal/always"
	"github.com/hejmo-home/hejmo/internal/list"
	"github.com/hejmo-home/hejmo/internal/store"
)

func milkRule(listID string) always.Rule {
	return always.Rule{ArticleID: "milk", Enabled: true, ListID: listID, Weekday: 1, Every: 1, Start: "2026-11-16", Qty: list.NewQuantity(3, "l"), Lang: "de"}
}

func TestARuleIsStoredPerArticleAndSinceIsTheDayItWasSaved(t *testing.T) {
	db, h, l := shopping(t)
	saved, err := db.SaveAlwaysRule(h.ID, milkRule(l.ID), "2026-11-16")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Since != "2026-11-16" || saved.ListName != "Einkauf" || saved.Qty.Base != 3000 {
		t.Errorf("saved = %+v", saved)
	}
	// Saving the same rule later keeps the day it started; changing the day starts again.
	again, _ := db.SaveAlwaysRule(h.ID, milkRule(l.ID), "2026-11-18")
	if again.Since != "2026-11-16" {
		t.Errorf("since after an unchanged save = %s", again.Since)
	}
	r := milkRule(l.ID)
	r.Weekday = 3
	moved, _ := db.SaveAlwaysRule(h.ID, r, "2026-11-18")
	if moved.Since != "2026-11-18" {
		t.Errorf("since after a change = %s", moved.Since)
	}
	got, ok, err := db.AlwaysRule(h.ID, "milk")
	if err != nil || !ok || got.Weekday != 3 {
		t.Errorf("rule = %+v %v %v", got, ok, err)
	}
	if _, ok, _ := db.AlwaysRule(h.ID, "eggs"); ok {
		t.Error("eggs have no rule")
	}
}

func TestARuleNeedsAnExistingListAndValidFields(t *testing.T) {
	db, h, _ := shopping(t)
	if _, err := db.SaveAlwaysRule(h.ID, milkRule("nope"), "2026-11-16"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown list: %v", err)
	}
	l := mustList(t, db, h, "Drogerie", list.Manual)
	r := milkRule(l.ID)
	r.Every = 0
	if _, err := db.SaveAlwaysRule(h.ID, r, "2026-11-16"); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("every 0: %v", err)
	}
}

func TestSwitchingARuleOffKeepsItsFieldsAndDeletingTheListPausesIt(t *testing.T) {
	db, h, l := shopping(t)
	_, _ = db.SaveAlwaysRule(h.ID, milkRule(l.ID), "2026-11-16")
	if err := db.DisableAlwaysRule(h.ID, "milk"); err != nil {
		t.Fatal(err)
	}
	r, _, _ := db.AlwaysRule(h.ID, "milk")
	if r.Enabled || r.Weekday != 1 || r.ListID != l.ID {
		t.Errorf("off rule = %+v", r)
	}
	_, _ = db.SaveAlwaysRule(h.ID, milkRule(l.ID), "2026-11-20")
	if err := db.DeleteList(h.ID, l.ID); err != nil {
		t.Fatal(err)
	}
	r, _, _ = db.AlwaysRule(h.ID, "milk")
	if !r.Paused() || r.ListName != "Einkauf" || r.Weekday != 1 {
		t.Errorf("paused rule = %+v", r)
	}
}

func TestARenamedListIsRenamedInTheRule(t *testing.T) {
	db, h, l := shopping(t)
	_, _ = db.SaveAlwaysRule(h.ID, milkRule(l.ID), "2026-11-16")
	l.Name = "Wocheneinkauf"
	if _, err := db.UpdateList(h.ID, l); err != nil {
		t.Fatal(err)
	}
	r, _, _ := db.AlwaysRule(h.ID, "milk")
	if r.ListName != "Wocheneinkauf" {
		t.Errorf("name = %q", r.ListName)
	}
}

func TestDeletingAnOwnArticleDeletesItsRuleAndACopyCarriesIt(t *testing.T) {
	db, h, l := shopping(t)
	mama, _ := db.People(h.ID)
	a, err := db.CreateArticle(h.ID, mama[0].ID, katzenfutter(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := milkRule(l.ID)
	r.ArticleID = a.ID
	_, _ = db.SaveAlwaysRule(h.ID, r, "2026-11-16")
	if err := db.CopyAlwaysRule(h.ID, a.ID, "copy-id"); err != nil {
		t.Fatal(err)
	}
	if c, ok, _ := db.AlwaysRule(h.ID, "copy-id"); !ok || c.Weekday != 1 {
		t.Errorf("copy = %+v %v", c, ok)
	}
	if err := db.DeleteArticle(h.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.AlwaysRule(h.ID, a.ID); ok {
		t.Error("the rule of a deleted article stays")
	}
}

func TestTheHouseholdTimeDefaultsTo3AndTheHolidayIsStored(t *testing.T) {
	db, h, _ := shopping(t)
	s, err := db.AlwaysSettings(h.ID)
	if err != nil || s.At != "03:00" || s.Holiday.On() {
		t.Fatalf("defaults = %+v %v", s, err)
	}
	want := always.Settings{At: "07:30", Holiday: always.Holiday{From: "2026-12-19", To: "2027-01-03"}}
	if err := db.SetAlwaysSettings(h.ID, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.AlwaysSettings(h.ID); got != want {
		t.Errorf("settings = %+v", got)
	}
	if err := db.SetAlwaysSettings(h.ID, always.Settings{At: "7:30"}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("bad time: %v", err)
	}
}

func intoShow() list.Show { return show("l", "ml") }

func TestARuleCreatesAnEntryWithItsOrigin(t *testing.T) {
	db, h, l := shopping(t)
	res, err := db.AddItemFromRule(h.ID, l.ID, store.Item{Text: "Milch", ArticleID: "milk", Qty: list.NewQuantity(3, "l")}, intoShow())
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan.Action != list.AddedNew {
		t.Errorf("action = %s", res.Plan.Action)
	}
	es, _ := db.Entries(h.ID, l.ID)
	if len(es) != 1 || !es[0].Origin.Auto || es[0].Origin.Part != 3000 || es[0].Origin.Prior != 0 {
		t.Errorf("entries = %+v", es)
	}
}

func TestARuleAddsItsQuantityToAnOpenEntryAndRemembersTheSplit(t *testing.T) {
	db, h, l := shopping(t)
	_, _ = db.AddItem(h.ID, l.ID, store.Item{Text: "Milch", ArticleID: "milk", Qty: list.NewQuantity(1, "l")}, intoShow())
	res, _ := db.AddItemFromRule(h.ID, l.ID, store.Item{Text: "Milch", ArticleID: "milk", Qty: list.NewQuantity(3, "l")}, intoShow())
	if res.Plan.Action != list.AddedTo {
		t.Fatalf("action = %s", res.Plan.Action)
	}
	es, _ := db.Entries(h.ID, l.ID)
	if len(es) != 1 || es[0].Qty.Base != 4000 || !es[0].Origin.Auto || es[0].Origin.Prior != 1000 || es[0].Origin.Part != 3000 {
		t.Errorf("entries = %+v", es)
	}
	// A quantity changed by hand ends the split and the line with it.
	if _, err := db.SetQuantity(h.ID, l.ID, es[0].ID, list.NewQuantity(5, "l"), "l", intoShow()); err != nil {
		t.Fatal(err)
	}
	es, _ = db.Entries(h.ID, l.ID)
	if es[0].Origin.Auto || es[0].Origin.Part != 0 || es[0].Origin.Prior != 0 {
		t.Errorf("origin after a manual change = %+v", es[0].Origin)
	}
}

func TestARuleWithoutQuantityLeavesAnOpenEntryAndATickedOneDoesNotCount(t *testing.T) {
	db, h, l := shopping(t)
	e, _ := db.AddEntry(h.ID, l.ID, "Vollkornbrot")
	res, _ := db.AddItemFromRule(h.ID, l.ID, store.Item{Text: "Vollkornbrot"}, intoShow())
	if res.Plan.Action != list.Exists {
		t.Errorf("open entry: %s", res.Plan.Action)
	}
	es, _ := db.Entries(h.ID, l.ID)
	if len(es) != 1 || es[0].Origin.Auto {
		t.Errorf("entries = %+v", es)
	}
	_ = db.SetEntryDone(h.ID, l.ID, e.ID, true)
	res, _ = db.AddItemFromRule(h.ID, l.ID, store.Item{Text: "Vollkornbrot"}, intoShow())
	if res.Plan.Action != list.AddedNew {
		t.Errorf("ticked entry: %s", res.Plan.Action)
	}
	if es, _ = db.Entries(h.ID, l.ID); len(es) != 2 {
		t.Errorf("entries = %d", len(es))
	}
}

func TestTheRunsOfADayAreRememberedOnceAndForgottenWhenOld(t *testing.T) {
	db, h, l := shopping(t)
	run := store.AlwaysRun{ArticleID: "milk", ListID: l.ID, Day: "2026-11-16", Text: "Milch", Action: "added", Part: list.NewQuantity(3, "l"), Total: list.NewQuantity(4, "l")}
	if err := db.RecordAlwaysRun(h.ID, run); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordAlwaysRun(h.ID, run); err != nil {
		t.Fatalf("twice: %v", err)
	}
	runs, _ := db.AlwaysRuns(h.ID, "2026-11-16")
	if len(runs) != 1 || runs[0].Text != "Milch" || runs[0].Total.Base != 4000 {
		t.Errorf("runs = %+v", runs)
	}
	if err := db.PruneAlwaysRuns(h.ID, "2026-11-30"); err != nil {
		t.Fatal(err)
	}
	if runs, _ = db.AlwaysRuns(h.ID, "2026-11-16"); len(runs) != 0 {
		t.Errorf("old runs = %+v", runs)
	}
}

func TestTheDayTheRunsWereMadeIsRemembered(t *testing.T) {
	db, h, _ := shopping(t)
	if d, _ := db.AlwaysLastRun(h.ID); d != "" {
		t.Errorf("last run = %q", d)
	}
	_ = db.SetAlwaysLastRun(h.ID, "2026-11-16")
	if d, _ := db.AlwaysLastRun(h.ID); d != "2026-11-16" {
		t.Errorf("last run = %q", d)
	}
}
