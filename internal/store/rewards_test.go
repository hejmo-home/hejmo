package store_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/reward"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 291: the reward catalogue against a real SQLite file.

type rewardFixture struct {
	db                         *store.DB
	h                          store.Household
	mama, papa, sebastian, mia store.Person
}

func newRewardFixture(t *testing.T) rewardFixture {
	t.Helper()
	db, _ := openTemp(t)
	h, mama := seedParent(t, db)
	add := func(name string, role store.Role) store.Person {
		p, err := db.AddPerson(h.ID, store.Person{Name: name, Role: role})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	return rewardFixture{db, h, mama, add("Papa", store.RoleParent), add("Sebastian", store.RoleChild), add("Mia", store.RoleChild)}
}

func eis() store.Reward {
	return store.Reward{Name: "Eis aus der Eisdiele", Symbol: "🍦", Price: 8, Limit: reward.Limit{Period: reward.Week, Count: 1}}
}

func TestARewardIsStoredAndReadBackWithItsAudience(t *testing.T) {
	f := newRewardFixture(t)
	in := eis()
	in.PersonIDs = []string{f.sebastian.ID, f.mia.ID}
	got, err := f.db.CreateReward(f.h.ID, f.mama.ID, in, nil)
	if err != nil || got.ID == "" {
		t.Fatalf("create = %+v %v", got, err)
	}
	back, err := f.db.Reward(f.h.ID, got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != in.Name || back.Symbol != "🍦" || back.Price != 8 || back.Limit != in.Limit || back.CreatedBy != f.mama.ID || back.CreatedAt.IsZero() || back.Archived() || back.HasPicture {
		t.Errorf("read back = %+v", back)
	}
	if !reflect.DeepEqual(sorted(back.PersonIDs), sorted(in.PersonIDs)) {
		t.Errorf("audience = %v, want %v", back.PersonIDs, in.PersonIDs)
	}
	whole, err := f.db.CreateReward(f.h.ID, f.mama.ID, store.Reward{Name: "Kino", Price: 30}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole.PersonIDs) != 0 || whole.Symbol != reward.DefaultSymbol || whole.Limit.Period != reward.NoLimit {
		t.Errorf("whole family reward = %+v", whole)
	}
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestRewardsComeSortedByPriceThenName(t *testing.T) {
	f := newRewardFixture(t)
	for _, r := range []store.Reward{{Name: "zebra", Price: 5}, {Name: "Affe", Price: 5}, {Name: "Kino", Price: 30}, {Name: "Eis", Price: 2}} {
		if _, err := f.db.CreateReward(f.h.ID, f.mama.ID, r, nil); err != nil {
			t.Fatal(err)
		}
	}
	list, err := f.db.Rewards(f.h.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range list {
		names = append(names, r.Name)
	}
	if want := []string{"Eis", "Affe", "zebra", "Kino"}; !reflect.DeepEqual(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}

func TestARewardWithABadFieldIsRefused(t *testing.T) {
	f := newRewardFixture(t)
	for name, in := range map[string]store.Reward{
		"name":  {Name: " ", Price: 5},
		"price": {Name: "Eis", Price: 1000},
		"limit": {Name: "Eis", Price: 5, Limit: reward.Limit{Period: reward.Week}},
	} {
		if _, err := f.db.CreateReward(f.h.ID, f.mama.ID, in, nil); !errors.Is(err, store.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := f.db.CreateReward(f.h.ID, f.mama.ID, store.Reward{Name: "Eis", Price: 5, PersonIDs: []string{"nobody"}}, nil); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("a person outside the household: err = %v, want ErrInvalid", err)
	}
}

func TestAPictureIsStoredAndASymbolReplacesIt(t *testing.T) {
	f := newRewardFixture(t)
	pic := &store.ArticlePicture{JPEG: []byte("\xff\xd8jpeg\xff\xd9"), Source: "upload", FromW: 3000, FromH: 2000, FromBytes: 900000}
	r, err := f.db.CreateReward(f.h.ID, f.mama.ID, eis(), pic)
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasPicture || r.PicSource != "upload" || r.PictureAt.IsZero() {
		t.Fatalf("reward = %+v", r)
	}
	b, err := f.db.RewardPicture(f.h.ID, r.ID)
	if err != nil || string(b) != string(pic.JPEG) {
		t.Fatalf("picture = %q %v", b, err)
	}
	r.Symbol = "🎬"
	r, err = f.db.UpdateReward(f.h.ID, r, store.RemovePicture, nil)
	if err != nil || r.HasPicture || r.PicSource != "symbol" || r.Symbol != "🎬" {
		t.Fatalf("after removing = %+v %v", r, err)
	}
	if _, err := f.db.RewardPicture(f.h.ID, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("picture err = %v, want ErrNotFound", err)
	}
	if _, err := f.db.CreateReward(f.h.ID, f.mama.ID, eis(), &store.ArticlePicture{JPEG: []byte("x"), Source: "web"}); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("a web picture: err = %v, want ErrInvalid", err)
	}
}

// Acceptance: a reward that was ever redeemed is archived, not deleted (Y).
func TestARewardThatWasRedeemedCanOnlyBeArchived(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateReward(f.h.ID, f.mama.ID, eis(), nil)
	if err := f.db.InsertRedemption(r.ID, f.mia.ID, "confirmed", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := f.db.DeleteReward(f.h.ID, r.ID); !errors.Is(err, store.ErrRedeemed) {
		t.Fatalf("delete = %v, want ErrRedeemed", err)
	}
	if _, err := f.db.Reward(f.h.ID, r.ID); err != nil {
		t.Fatalf("the reward must stay: %v", err)
	}
	archived, err := f.db.ArchiveReward(f.h.ID, r.ID, time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC))
	if err != nil || !archived.Archived() || archived.ArchivedReason != "" {
		t.Fatalf("archive = %+v %v", archived, err)
	}
	restored, err := f.db.RestoreReward(f.h.ID, r.ID, nil)
	if err != nil || restored.Archived() {
		t.Fatalf("restore = %+v %v", restored, err)
	}
}

func TestARewardNeverRedeemedCanBeDeletedAndOpenRequestsDoNotBlockIt(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateReward(f.h.ID, f.mama.ID, eis(), nil)
	if err := f.db.InsertRedemption(r.ID, f.mia.ID, "declined", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := f.db.DeleteReward(f.h.ID, r.ID); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if _, err := f.db.Reward(f.h.ID, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := f.db.DeleteReward(f.h.ID, r.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

func TestAnArchivedRewardCannotBeChangedAndFactsCountPerPerson(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateReward(f.h.ID, f.mama.ID, eis(), nil)
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	for i, who := range []store.Person{f.sebastian, f.sebastian, f.mia} {
		if err := f.db.InsertRedemption(r.ID, who.ID, "confirmed", base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.db.InsertRedemption(r.ID, f.mia.ID, "open", base.Add(48*time.Hour))
	facts, err := f.db.RewardFacts(f.h.ID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Count != 3 || len(facts.ByPerson) != 2 || facts.ByPerson[0].PersonID != f.sebastian.ID || facts.ByPerson[0].Count != 2 || facts.LastBy != f.mia.ID || !facts.Last.Equal(base.Add(2*time.Hour)) {
		t.Errorf("facts = %+v", facts)
	}
	if _, err := f.db.ArchiveReward(f.h.ID, r.ID, base); err != nil {
		t.Fatal(err)
	}
	r.Name = "Anders"
	if _, err := f.db.UpdateReward(f.h.ID, r, store.KeepPicture, nil); !errors.Is(err, store.ErrArchived) {
		t.Errorf("update of an archived reward = %v, want ErrArchived", err)
	}
}

func TestRedemptionTimesAreConfirmedOnesPerRewardAndPerson(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateReward(f.h.ID, f.mama.ID, eis(), nil)
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	_ = f.db.InsertRedemption(r.ID, f.mia.ID, "confirmed", at)
	_ = f.db.InsertRedemption(r.ID, f.mia.ID, "declined", at)
	_ = f.db.InsertRedemption(r.ID, f.sebastian.ID, "open", at)
	times, err := f.db.ConfirmedRedemptions(f.h.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := times[r.ID][f.mia.ID]; len(got) != 1 || !got[0].Equal(at) {
		t.Errorf("mia = %v", got)
	}
	if len(times[r.ID][f.sebastian.ID]) != 0 {
		t.Errorf("an open request does not count: %v", times[r.ID][f.sebastian.ID])
	}
}

func TestRedemptionCountsIncludePeopleWhoHaveLeft(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateReward(f.h.ID, f.mama.ID, eis(), nil)
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	_ = f.db.InsertRedemption(r.ID, f.mia.ID, "confirmed", at)
	_ = f.db.InsertRedemption(r.ID, f.sebastian.ID, "confirmed", at)
	_ = f.db.InsertRedemption(r.ID, f.sebastian.ID, "declined", at)
	if err := f.db.ExecUnchecked(`DELETE FROM people WHERE id = ?`, f.mia.ID); err != nil {
		t.Fatal(err)
	}
	counts, err := f.db.ConfirmedRedemptionCounts(f.h.ID)
	if err != nil || counts[r.ID] != 2 {
		t.Fatalf("counts = %v %v, want 2 (the one who left still counts)", counts, err)
	}
}

// Acceptance: when the last person a reward is meant for is removed, the
// reward is archived with its reason in the same transaction, never "whole family".
func TestRemovingTheOnlyPersonOfARewardArchivesIt(t *testing.T) {
	f := newRewardFixture(t)
	only, _ := f.db.CreateReward(f.h.ID, f.mama.ID, store.Reward{Name: "Kuscheltier", Price: 40, PersonIDs: []string{f.mia.ID}}, nil)
	two, _ := f.db.CreateReward(f.h.ID, f.mama.ID, store.Reward{Name: "Spieleabend", Price: 12, PersonIDs: []string{f.mia.ID, f.sebastian.ID}}, nil)
	whole, _ := f.db.CreateReward(f.h.ID, f.mama.ID, store.Reward{Name: "Kino", Price: 30}, nil)
	if err := f.db.ExecUnchecked(`DELETE FROM people WHERE id = ?`, f.mia.ID); err != nil {
		t.Fatal(err)
	}
	got, err := f.db.Reward(f.h.ID, only.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Archived() || got.ArchivedReason != "audience_gone" || got.ArchivedFor != "Mia" {
		t.Errorf("sole audience: %+v", got)
	}
	if len(got.PersonIDs) != 0 {
		t.Errorf("an archived reward keeps no audience of the removed person: %v", got.PersonIDs)
	}
	if two, _ = f.db.Reward(f.h.ID, two.ID); two.Archived() || !reflect.DeepEqual(two.PersonIDs, []string{f.sebastian.ID}) {
		t.Errorf("two people: %+v", two)
	}
	if whole, _ = f.db.Reward(f.h.ID, whole.ID); whole.Archived() {
		t.Errorf("whole family: %+v", whole)
	}
	// Seen by a child, the archived reward is gone.
	list, _ := f.db.RewardsFor(f.h.ID, f.sebastian.ID)
	for _, r := range list {
		if r.ID == only.ID {
			t.Errorf("an archived reward must not be offered: %+v", r)
		}
	}
	// The last one leaving also archives it.
	if err := f.db.ExecUnchecked(`DELETE FROM people WHERE id = ?`, f.sebastian.ID); err != nil {
		t.Fatal(err)
	}
	if two, _ = f.db.Reward(f.h.ID, two.ID); !two.Archived() || two.ArchivedReason != "audience_gone" {
		t.Errorf("after the second person left: %+v", two)
	}
}

func TestAPersonLeavingTheHouseholdArchivesTheirRewardsToo(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateReward(f.h.ID, f.mama.ID, store.Reward{Name: "Kuscheltier", Price: 40, PersonIDs: []string{f.mia.ID}}, nil)
	if err := f.db.ExecUnchecked(`DELETE FROM household_members WHERE person_id = ?`, f.mia.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.db.Reward(f.h.ID, r.ID); !got.Archived() || got.ArchivedReason != "audience_gone" {
		t.Errorf("reward = %+v", got)
	}
}

// An archived reward whose audience is gone is offered again only with a new audience.
func TestRestoringAnAudienceGoneRewardNeedsANewAudience(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateReward(f.h.ID, f.mama.ID, store.Reward{Name: "Kuscheltier", Price: 40, PersonIDs: []string{f.mia.ID}}, nil)
	_ = f.db.ExecUnchecked(`DELETE FROM people WHERE id = ?`, f.mia.ID)
	if _, err := f.db.RestoreReward(f.h.ID, r.ID, nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("restore without an audience = %v, want ErrInvalid", err)
	}
	if got, _ := f.db.Reward(f.h.ID, r.ID); !got.Archived() {
		t.Fatal("the failed restore must change nothing")
	}
	got, err := f.db.RestoreReward(f.h.ID, r.ID, &store.Audience{PersonIDs: []string{f.sebastian.ID}})
	if err != nil || got.Archived() || got.ArchivedReason != "" || !reflect.DeepEqual(got.PersonIDs, []string{f.sebastian.ID}) {
		t.Fatalf("restore = %+v %v", got, err)
	}
}

// A reward archived by hand and meant for one person also needs a new audience
// when that person leaves: restoring it must never offer it to the whole family.
func TestRestoringAHandArchivedRewardWhoseAudienceLeftNeedsANewAudience(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateReward(f.h.ID, f.mama.ID, store.Reward{Name: "Kuscheltier", Price: 40, PersonIDs: []string{f.mia.ID}}, nil)
	if _, err := f.db.ArchiveReward(f.h.ID, r.ID, time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := f.db.ExecUnchecked(`DELETE FROM people WHERE id = ?`, f.mia.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.db.Reward(f.h.ID, r.ID)
	if !got.Archived() || got.ArchivedReason != "audience_gone" {
		t.Fatalf("reward = %+v, want archived with reason audience_gone", got)
	}
	if _, err := f.db.RestoreReward(f.h.ID, r.ID, nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("restore without an audience = %v, want ErrInvalid", err)
	}
}

func TestRestoringAHandArchivedRewardWhoseMemberLeftNeedsANewAudience(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateReward(f.h.ID, f.mama.ID, store.Reward{Name: "Kuscheltier", Price: 40, PersonIDs: []string{f.mia.ID}}, nil)
	_, _ = f.db.ArchiveReward(f.h.ID, r.ID, time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC))
	if err := f.db.ExecUnchecked(`DELETE FROM household_members WHERE person_id = ?`, f.mia.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.RestoreReward(f.h.ID, r.ID, nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("restore without an audience = %v, want ErrInvalid", err)
	}
}

func TestRewardsForAChildAreTheirAudienceAndNotArchived(t *testing.T) {
	f := newRewardFixture(t)
	mk := func(r store.Reward) store.Reward {
		out, err := f.db.CreateReward(f.h.ID, f.mama.ID, r, nil)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	mk(store.Reward{Name: "Kino", Price: 30})
	mk(store.Reward{Name: "Nur Mia", Price: 5, PersonIDs: []string{f.mia.ID}})
	mk(store.Reward{Name: "Nur Sebastian", Price: 6, PersonIDs: []string{f.sebastian.ID}})
	gone := mk(store.Reward{Name: "Weg", Price: 1})
	_, _ = f.db.ArchiveReward(f.h.ID, gone.ID, time.Now())
	list, err := f.db.RewardsFor(f.h.ID, f.sebastian.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range list {
		names = append(names, r.Name)
	}
	if want := []string{"Nur Sebastian", "Kino"}; !reflect.DeepEqual(names, want) {
		t.Errorf("sebastian sees %v, want %v", names, want)
	}
}

func TestChangingARewardKeepsItsRedemptionsAndChangesTheAudience(t *testing.T) {
	f := newRewardFixture(t)
	r, _ := f.db.CreateReward(f.h.ID, f.mama.ID, store.Reward{Name: "Kino", Price: 30, PersonIDs: []string{f.mia.ID}}, nil)
	r.Name, r.Price, r.PersonIDs, r.Limit = "Kino mit Popcorn", 35, nil, reward.Limit{Period: reward.Month, Count: 2}
	got, err := f.db.UpdateReward(f.h.ID, r, store.KeepPicture, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Kino mit Popcorn" || got.Price != 35 || len(got.PersonIDs) != 0 || got.Limit.Count != 2 {
		t.Errorf("updated = %+v", got)
	}
	if _, err := f.db.UpdateReward(f.h.ID, store.Reward{ID: "nope", Name: "x", Price: 1}, store.KeepPicture, nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown reward = %v", err)
	}
}

func TestRewardVisibleTo(t *testing.T) {
	archived := time.Now()
	cases := []struct {
		name  string
		r     store.Reward
		child bool
		want  bool
	}{
		{"adult sees archived", store.Reward{ArchivedAt: archived}, false, true},
		{"adult sees unmeant", store.Reward{PersonIDs: []string{"x"}}, false, true},
		{"child sees meant", store.Reward{PersonIDs: []string{"kid"}}, true, true},
		{"child sees whole household", store.Reward{}, true, true},
		{"child misses unmeant", store.Reward{PersonIDs: []string{"x"}}, true, false},
		{"child misses archived", store.Reward{ArchivedAt: archived}, true, false},
	}
	for _, c := range cases {
		if got := c.r.VisibleTo("kid", c.child); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
