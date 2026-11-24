package contact_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/contact"
)

func byName(a, b string) int { return strings.Compare(a, b) }

// Ticket 301: a contact stands once, in the first section that fits; the sections are Notfall, Familie, the rest.
func TestKidSectionsAssignEachContactOnceInTheFirstSectionThatFits(t *testing.T) {
	t.Parallel()
	in := []contact.KidCandidate{
		{ID: "mama", Name: "Mama", Person: true, Visible: true, Numbers: 1},
		{ID: "papa", Name: "Papa", Person: true, Visible: true, Numbers: 1, Notfall: true}, // a person in Notfall: first section wins
		{ID: "notruf", Name: "Notruf", Visible: true, Numbers: 2, Notfall: true},
		{ID: "gift", Name: "Giftnotruf", Visible: true, Numbers: 1, Notfall: true, Familie: true},
		{ID: "oma", Name: "Oma", Visible: true, Numbers: 1, Familie: true},
		{ID: "arzt", Name: "Kinderarzt", Visible: true, Numbers: 1},
	}
	got := contact.KidSections(in, "", byName)
	if want := []string{"papa", "notruf", "gift"}; !slices.Equal(got.Emergency, want) {
		t.Errorf("Emergency = %v, want %v (the order of the input is kept)", got.Emergency, want)
	}
	if want := []string{"mama", "oma"}; !slices.Equal(got.Family, want) {
		t.Errorf("Family = %v, want %v", got.Family, want)
	}
	if want := []string{"arzt"}; !slices.Equal(got.Other, want) {
		t.Errorf("Other = %v, want %v", got.Other, want)
	}
}

func TestKidSectionsOrderPeopleFirstThenGroupFamilieAndOthersAToZ(t *testing.T) {
	t.Parallel()
	in := []contact.KidCandidate{
		{ID: "mia", Name: "Mia", Person: true, Visible: true, Numbers: 1},
		{ID: "mama", Name: "Mama", Person: true, Visible: true, Numbers: 1},
		{ID: "opa", Name: "Opa", Visible: true, Numbers: 1, Familie: true},
		{ID: "oma", Name: "Oma", Visible: true, Numbers: 1, Familie: true},
		{ID: "z", Name: "Zahnarzt", Visible: true, Numbers: 1},
		{ID: "k", Name: "Kita", Visible: true, Numbers: 1},
	}
	got := contact.KidSections(in, "", byName)
	if want := []string{"mia", "mama", "oma", "opa"}; !slices.Equal(got.Family, want) {
		t.Errorf("Family = %v, want people in given order, then Familie A to Z: %v", got.Family, want)
	}
	if want := []string{"k", "z"}; !slices.Equal(got.Other, want) {
		t.Errorf("Other = %v, want %v", got.Other, want)
	}
}

func TestKidSectionsLeaveOutHiddenOwnAndNumberlessContacts(t *testing.T) {
	t.Parallel()
	in := []contact.KidCandidate{
		{ID: "mia", Name: "Mia", Person: true, Visible: true, Numbers: 1},
		{ID: "hidden", Name: "Hidden", Visible: false, Numbers: 1},
		{ID: "nonumber", Name: "NoNumber", Visible: true, Numbers: 0, Notfall: true},
		{ID: "ok", Name: "Ok", Visible: true, Numbers: 1},
	}
	got := contact.KidSections(in, "mia", byName)
	if len(got.Emergency) != 0 || len(got.Family) != 0 || !slices.Equal(got.Other, []string{"ok"}) {
		t.Errorf("sections = %+v, want only ok in Other", got)
	}
	if got.Empty() {
		t.Error("Empty() true with a contact in Other")
	}
	if !contact.KidSections(nil, "", byName).Empty() {
		t.Error("Empty() false without contacts")
	}
}
