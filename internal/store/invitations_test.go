package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/store"
)

var inviteT0 = time.Date(2026, 11, 15, 20, 15, 0, 0, time.UTC)

func householdWithPapa(t *testing.T) (db *store.DB, h store.Household, mama, papa, mia store.Person) {
	t.Helper()
	db, h = householdForDisplays(t)
	add := func(name string, role store.Role) store.Person {
		p, err := db.AddPerson(h.ID, store.Person{Name: name, Role: role})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	mama, papa, mia = add("Mama", store.RoleParent), add("Papa", store.RoleParent), add("Mia", store.RoleChild)
	if err := db.CreateAccount(store.Account{PersonID: mama.ID, Username: "mama", PasswordHash: "x", TOTPSecret: "S"}); err != nil {
		t.Fatal(err)
	}
	return db, h, mama, papa, mia
}

func TestInvitationIsOpenForSevenDays(t *testing.T) {
	db, h, mama, papa, _ := householdWithPapa(t)
	inv, err := db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-1", inviteT0)
	if err != nil {
		t.Fatal(err)
	}
	if want := inviteT0.Add(7 * 24 * time.Hour); !inv.ExpiresAt.Equal(want) {
		t.Fatalf("expires %v, want %v", inv.ExpiresAt, want)
	}
	if got := inv.State(inviteT0.Add(7*24*time.Hour - time.Second)); got != store.InvitationOpen {
		t.Fatalf("state just before expiry = %q, want open", got)
	}
	if got := inv.State(inviteT0.Add(7 * 24 * time.Hour)); got != store.InvitationExpired {
		t.Fatalf("state at expiry = %q, want expired", got)
	}
	found, err := db.InvitationByHash("hash-1")
	if err != nil || found.ID != inv.ID || found.PersonID != papa.ID || found.CreatedBy != mama.ID {
		t.Fatalf("by hash = %+v, %v", found, err)
	}
	if _, err := db.InvitationByHash("unknown"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown hash error = %v, want ErrNotFound", err)
	}
}

func TestOnlyAdultsWithoutAccountCanBeInvited(t *testing.T) {
	db, h, mama, papa, mia := householdWithPapa(t)
	if _, err := db.CreateInvitation(h.ID, mia.ID, mama.ID, "h-child", inviteT0); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("inviting a child = %v, want ErrInvalid", err)
	}
	if _, err := db.CreateInvitation(h.ID, mama.ID, mama.ID, "h-self", inviteT0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("inviting someone with an account = %v, want ErrConflict", err)
	}
	if _, err := db.CreateInvitation(h.ID, "no-such-person", mama.ID, "h-none", inviteT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("inviting an unknown person = %v, want ErrNotFound", err)
	}
	if _, err := db.CreateInvitation(h.ID, papa.ID, mama.ID, "h-ok", inviteT0); err != nil {
		t.Fatal(err)
	}
}

func TestANewInvitationReplacesTheOpenOne(t *testing.T) {
	db, h, mama, papa, _ := householdWithPapa(t)
	first, _ := db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-1", inviteT0)
	later := inviteT0.Add(time.Hour)
	second, err := db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-2", later)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := db.InvitationByHash("hash-1")
	if old.ID != first.ID || old.State(later) != store.InvitationWithdrawn || !old.WithdrawnAt.Equal(later) {
		t.Fatalf("old invitation = %+v (state %q), want withdrawn at %v", old, old.State(later), later)
	}
	cur, err := db.LatestInvitation(h.ID, papa.ID)
	if err != nil || cur.ID != second.ID || cur.State(later) != store.InvitationOpen {
		t.Fatalf("latest = %+v, %v", cur, err)
	}
	if _, err := db.AcceptInvitation("hash-1", later, store.Account{Username: "papa", PasswordHash: "x", TOTPSecret: "S"}); !errors.Is(err, store.ErrInvitationNotOpen) {
		t.Fatalf("accepting the replaced link = %v, want ErrInvitationNotOpen", err)
	}
	if _, err := db.AccountByPerson(papa.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an account exists after refusing: %v", err)
	}
}

func TestWithdrawingMakesTheLinkUseless(t *testing.T) {
	db, h, mama, papa, _ := householdWithPapa(t)
	if err := db.WithdrawInvitation(h.ID, papa.ID, inviteT0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("withdrawing nothing = %v, want ErrNotFound", err)
	}
	if _, err := db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-1", inviteT0); err != nil {
		t.Fatal(err)
	}
	at := inviteT0.Add(time.Hour)
	if err := db.WithdrawInvitation(h.ID, papa.ID, at); err != nil {
		t.Fatal(err)
	}
	inv, _ := db.InvitationByHash("hash-1")
	if inv.State(at) != store.InvitationWithdrawn {
		t.Fatalf("state = %q, want withdrawn", inv.State(at))
	}
	if _, err := db.AcceptInvitation("hash-1", at, store.Account{Username: "papa", PasswordHash: "x", TOTPSecret: "S"}); !errors.Is(err, store.ErrInvitationNotOpen) {
		t.Fatalf("accepting a withdrawn link = %v, want ErrInvitationNotOpen", err)
	}
	if err := db.WithdrawInvitation(h.ID, papa.ID, at); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("withdrawing twice = %v, want ErrNotFound", err)
	}
}

func TestAcceptingCreatesTheAccountOnceAndUsesTheLink(t *testing.T) {
	db, h, mama, papa, _ := householdWithPapa(t)
	if _, err := db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-1", inviteT0); err != nil {
		t.Fatal(err)
	}
	at := inviteT0.Add(26 * time.Hour)
	inv, err := db.AcceptInvitation("hash-1", at, store.Account{Username: "Papa", PasswordHash: "ph", TOTPSecret: "SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.PersonID != papa.ID || !inv.AcceptedAt.Equal(at) || inv.State(at) != store.InvitationUsed {
		t.Fatalf("accepted = %+v", inv)
	}
	acc, err := db.AccountByPerson(papa.ID)
	if err != nil || acc.Username != "Papa" || acc.PasswordHash != "ph" || acc.TOTPConfirmed {
		t.Fatalf("account = %+v, %v", acc, err)
	}
	// reuse of a used link never creates a second account
	if _, err := db.AcceptInvitation("hash-1", at, store.Account{Username: "papa2", PasswordHash: "ph", TOTPSecret: "S"}); !errors.Is(err, store.ErrInvitationNotOpen) {
		t.Fatalf("reusing = %v, want ErrInvitationNotOpen", err)
	}
	if _, err := db.AccountByUsername("papa2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second account exists: %v", err)
	}
	// a used invitation cannot be withdrawn or replaced, and the person has an account now
	if err := db.WithdrawInvitation(h.ID, papa.ID, at); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("withdrawing a used link = %v, want ErrNotFound", err)
	}
	if _, err := db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-2", at); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("inviting again = %v, want ErrConflict", err)
	}
}

func TestExpiredLinkCannotBeAccepted(t *testing.T) {
	db, h, mama, papa, _ := householdWithPapa(t)
	db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-1", inviteT0)
	late := inviteT0.Add(7*24*time.Hour + time.Minute)
	if _, err := db.AcceptInvitation("hash-1", late, store.Account{Username: "papa", PasswordHash: "x", TOTPSecret: "S"}); !errors.Is(err, store.ErrInvitationNotOpen) {
		t.Fatalf("accepting late = %v, want ErrInvitationNotOpen", err)
	}
}

func TestTakenUsernameKeepsTheLinkOpen(t *testing.T) {
	db, h, mama, papa, _ := householdWithPapa(t)
	db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-1", inviteT0)
	_, err := db.AcceptInvitation("hash-1", inviteT0, store.Account{Username: "MAMA", PasswordHash: "x", TOTPSecret: "S"})
	var fe *store.FieldError
	if !errors.As(err, &fe) || fe.Field != "username" {
		t.Fatalf("taken username = %v, want a username FieldError", err)
	}
	inv, _ := db.InvitationByHash("hash-1")
	if inv.State(inviteT0) != store.InvitationOpen {
		t.Fatalf("state = %q, want still open", inv.State(inviteT0))
	}
	if _, err := db.AcceptInvitation("hash-1", inviteT0, store.Account{Username: "papa", PasswordHash: "x", TOTPSecret: "S"}); err != nil {
		t.Fatalf("retry with a free name: %v", err)
	}
}

func TestAnAcceptedInvitationIsFoundForThePersonAndNamesWhoInvited(t *testing.T) {
	db, h, mama, papa, _ := householdWithPapa(t)
	if _, err := db.LatestInvitation(h.ID, papa.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("no invitation yet = %v, want ErrNotFound", err)
	}
	db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-1", inviteT0)
	db.AcceptInvitation("hash-1", inviteT0, store.Account{Username: "papa", PasswordHash: "x", TOTPSecret: "S"})
	inv, err := db.LatestInvitation(h.ID, papa.ID)
	if err != nil || inv.State(inviteT0) != store.InvitationUsed || inv.CreatedBy != mama.ID {
		t.Fatalf("latest = %+v, %v", inv, err)
	}
	open, err := db.OpenInvitations(h.ID, inviteT0)
	if err != nil || len(open) != 0 {
		t.Fatalf("open = %v, %v", open, err)
	}
}

func TestOpenInvitationsListsOnlyUnexpiredOpenOnes(t *testing.T) {
	db, h, mama, papa, _ := householdWithPapa(t)
	oma, _ := db.AddPerson(h.ID, store.Person{Name: "Oma", Role: store.RoleParent})
	db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-1", inviteT0)
	db.CreateInvitation(h.ID, oma.ID, mama.ID, "hash-2", inviteT0.Add(-8*24*time.Hour))
	open, err := db.OpenInvitations(h.ID, inviteT0)
	if err != nil || len(open) != 1 || open[0].PersonID != papa.ID {
		t.Fatalf("open = %+v, %v", open, err)
	}
}

func TestTwoRequestsAcceptingTheSameLinkCreateOneAccount(t *testing.T) {
	db, h, mama, papa, _ := householdWithPapa(t)
	if _, err := db.CreateInvitation(h.ID, papa.ID, mama.ID, "hash-race", inviteT0); err != nil {
		t.Fatal(err)
	}
	at := inviteT0.Add(time.Hour)
	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, name := range []string{"papa-a", "papa-b"} {
		go func() {
			<-start
			_, err := db.AcceptInvitation("hash-race", at, store.Account{Username: name, PasswordHash: "ph", TOTPSecret: "S"})
			errs <- err
		}()
	}
	close(start)
	var ok int
	for range 2 {
		if err := <-errs; err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d of 2 simultaneous accepts succeeded, want exactly 1", ok)
	}
	n := 0
	for _, name := range []string{"papa-a", "papa-b"} {
		if _, err := db.AccountByUsername(name); err == nil {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d accounts exist for the person, want 1", n)
	}
}
