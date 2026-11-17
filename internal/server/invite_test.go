package server_test

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/auth"
	"github.com/hejmo-home/hejmo/internal/server"
	"github.com/hejmo-home/hejmo/internal/store"
)

// Ticket 145: a parent invites an adult who has no account. The invited
// parent opens the link on their own device and chooses username and password
// themselves.

var inviteTokenRe = regexp.MustCompile(`[A-HJKMNP-Z2-9]{4}(?:-[A-HJKMNP-Z2-9]{4}){4}`)

type inviteEnv struct {
	b    *box
	papa store.Person
	mia  store.Person
}

// newInviteBox returns a box with Mama signed in, Papa (an adult without
// account) and Mia (a child).
func newInviteBox(t *testing.T, opts ...boxOption) *inviteEnv {
	t.Helper()
	b := signedInBox(t, opts...)
	h, err := b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	papa, err := b.db.AddPerson(h.ID, store.Person{Name: "Papa", Role: store.RoleParent, Color: "#2F5E7A", Shape: "circle-empty"})
	if err != nil {
		t.Fatal(err)
	}
	mia, err := b.db.AddPerson(h.ID, store.Person{Name: "Mia", Role: store.RoleChild, Color: "#9A7420", Shape: "square-empty"})
	if err != nil {
		t.Fatal(err)
	}
	return &inviteEnv{b: b, papa: papa, mia: mia}
}

func (e *inviteEnv) en(method, path string, form url.Values) resp {
	e.b.t.Helper()
	if method == http.MethodPost && form == nil {
		form = url.Values{}
	}
	return e.b.do(method, path, form, map[string]string{"Accept-Language": "en"})
}

func (e *inviteEnv) page() string { return "/people/" + e.papa.ID + "/invitation" }

// create has the signed-in parent create an invitation for Papa and returns
// the token that is shown once.
func (e *inviteEnv) create() string {
	e.b.t.Helper()
	r := e.en(http.MethodPost, e.page(), nil)
	tok := inviteTokenRe.FindString(r.body)
	if r.status != http.StatusOK || tok == "" {
		e.b.t.Fatalf("create invitation = %d, no token\n%s", r.status, r.body)
	}
	return tok
}

// acceptForm is what Papa sends to create the account.
func acceptForm(username, password string) url.Values {
	return url.Values{"username": {username}, "password": {password}}
}

// acceptAndSetUpCodes runs Papa's whole way through the wizard in a fresh
// browser and returns his one-time secret. Papa is signed in afterwards.
func (e *inviteEnv) acceptAndSetUpCodes(token string) string {
	e.b.t.Helper()
	e.b.logout()
	if r := e.en(http.MethodPost, "/invite/"+token+"/account", acceptForm("papa", "papas long password")); r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		e.b.t.Fatalf("accept = %d %q\n%s", r.status, r.location, r.body)
	}
	secret := secretOn(e.b.t, e.en(http.MethodGet, "/setup/totp", nil).body)
	code, _ := auth.TOTPCode(secret, e.b.now)
	if r := e.en(http.MethodPost, "/setup/totp", url.Values{"code": {code}}); r.status != http.StatusOK {
		e.b.t.Fatalf("confirm codes = %d\n%s", r.status, r.body)
	}
	return secret
}

// --- the inviting parent ---

func TestAdultWithoutAccountShowsHowToInviteAndChildrenGetNoInvitation(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	r := e.en(http.MethodGet, e.page(), nil)
	for _, want := range []string{
		"<h1>Invite Papa</h1>", `class="tile invite-how"`, "<b>once</b>", "<b>7 days</b>", "You never see Papa&#39;s password.",
		`action="` + e.page() + `"`, ">Create invitation</button>",
	} {
		if r.status != http.StatusOK || !strings.Contains(r.body, want) {
			t.Fatalf("invite page = %d, lacks %q\n%s", r.status, want, r.body)
		}
	}
	if strings.Count(r.body, `class="button"`) != 1 {
		t.Errorf("the page needs exactly one primary action\n%s", r.body)
	}
	for _, id := range []string{e.mia.ID, "no-such-person"} {
		if got := e.en(http.MethodGet, "/people/"+id+"/invitation", nil); got.status != http.StatusNotFound {
			t.Errorf("invitation page of %s = %d, want 404", id, got.status)
		}
		if got := e.en(http.MethodPost, "/people/"+id+"/invitation", nil); got.status != http.StatusNotFound {
			t.Errorf("creating for %s = %d, want 404", id, got.status)
		}
	}
	e.b.logout()
	if got := e.en(http.MethodGet, e.page(), nil); got.status != http.StatusSeeOther || !strings.HasPrefix(got.location, "/login") {
		t.Errorf("signed out = %d %q, want a redirect to sign in", got.status, got.location)
	}
}

func TestPeopleOverviewAndPersonPageSayWhoCanSignIn(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	overview := e.en(http.MethodGet, "/people", nil).body
	for _, want := range []string{"Adult · That&#39;s you", "Adult · <b class=\"acc-state\">No account yet</b> · <a href=\"" + e.page() + "\">Invite Papa</a>"} {
		if !strings.Contains(overview, want) {
			t.Errorf("people overview lacks %q\n%s", want, overview)
		}
	}
	person := e.en(http.MethodGet, "/people/"+e.papa.ID, nil).body
	for _, want := range []string{`<h2 class="rows-head">Sign-in</h2>`, `<ul class="list-rows">`, `href="` + e.page() + `"`, "<b>No account yet</b>", "Tap here to invite Papa."} {
		if !strings.Contains(person, want) {
			t.Errorf("person page lacks %q\n%s", want, person)
		}
	}
	if strings.Count(person, `class="button"`) != 1 {
		t.Errorf("Save must stay the only primary action on the person page")
	}
	if child := e.en(http.MethodGet, "/people/"+e.mia.ID, nil).body; strings.Contains(child, "Sign-in") || strings.Contains(child, "No account yet") {
		t.Errorf("a child's page offers an account\n%s", child)
	}
	mama := personNamed(t, e.b, "Mama")
	if own := e.en(http.MethodGet, "/people/"+mama.ID, nil).body; !strings.Contains(own, `href="/account"`) || strings.Contains(own, "/invitation") {
		t.Errorf("Mama's own row must lead to her account page, not an invitation\n%s", own)
	}
	if r := e.en(http.MethodGet, "/people/"+mama.ID+"/invitation", nil); r.status != http.StatusSeeOther || r.location != "/account" {
		t.Errorf("own invitation page = %d %q, want a redirect to /account", r.status, r.location)
	}
}

func TestCreatingAnInvitationShowsQRCodeAndLinkOnceAndStoresOnlyAHash(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	r := e.en(http.MethodPost, e.page(), nil)
	for _, want := range []string{
		"Invitation for Papa created.", `class="once invite-once"`, `<div class="pair-qr"><svg`, "QR code of the invitation for Papa",
		`class="valid"`, "Works once, until <b>Saturday, 21.11.2026, 13:00</b>.", "data-invite-link", `data-copy="http`, "Whoever has the link", `<a class="button" href="` + e.page() + `">Done</a>`,
	} {
		if !strings.Contains(r.body, want) {
			t.Errorf("created page lacks %q\n%s", want, r.body)
		}
	}
	if r.status != http.StatusOK || !strings.Contains(r.body, "http://127.0.0.1/i/") {
		t.Errorf("a box with its own CA must lead over the plain-HTTP trust page: %d\n%s", r.status, r.body)
	}
	if !strings.Contains(strings.ToLower(r.body), "own certificate") {
		t.Error("a box with its own CA must tell what Papa's phone does first")
	}
	tok := inviteTokenRe.FindString(r.body)
	h, _ := e.b.db.HomeHousehold()
	if inv, err := e.b.db.InvitationByHash(auth.HashInvitationToken(tok)); err != nil || inv.PersonID != e.papa.ID || inv.HouseholdID != h.ID {
		t.Fatalf("invitation not stored by hash: %+v, %v", inv, err)
	}
	if got := e.headerOfPost(e.page(), "Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("the page with the link is cacheable: %q", got)
	}
	// Later the link is gone: only the state in words.
	later := e.en(http.MethodGet, e.page(), nil).body
	if strings.Contains(later, tok) || strings.Contains(later, "data-invite-link") {
		t.Errorf("the link is shown again\n%s", later)
	}
	for _, want := range []string{"<h1>Invitation for Papa</h1>", `<span class="flag open">`, "Open</span> Papa has not created an account yet.", "<dt>Valid until</dt>", "Saturday, 21.11.2026, 13:00, once", "<dt>Created</dt>", "14.11.2026, 13:00 by you", "Only visible when created", ">Create new link</button>", "the old one stops working.", `<details class="danger">`, "Withdraw invitation"} {
		if !strings.Contains(later, want) {
			t.Errorf("state page lacks %q\n%s", want, later)
		}
	}
	// the overview names the open invitation
	if o := e.en(http.MethodGet, "/people", nil).body; !strings.Contains(o, "Invitation open") || !strings.Contains(o, "View invitation") {
		t.Errorf("people overview does not name the open invitation\n%s", o)
	}
}

func TestANewLinkReplacesTheOldOne(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	first := e.create()
	e.b.now = e.b.now.Add(time.Hour)
	second := e.create()
	if first == second {
		t.Fatal("same link twice")
	}
	e.b.logout()
	if r := e.en(http.MethodGet, "/invite/"+first, nil); r.status != http.StatusGone || !strings.Contains(r.body, "This invitation is no longer valid") || !strings.Contains(r.body, "replaced it with a new link") {
		t.Errorf("old link = %d\n%s", r.status, r.body)
	}
	if r := e.en(http.MethodGet, "/invite/"+second, nil); r.status != http.StatusOK || !strings.Contains(r.body, "invites you to Hejmo") {
		t.Errorf("new link = %d\n%s", r.status, r.body)
	}
}

func TestWithdrawingAsksFirstAndMakesTheLinkUseless(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	if r := e.en(http.MethodGet, e.page()+"/withdraw", nil); r.status != http.StatusNotFound && r.status != http.StatusMethodNotAllowed {
		t.Errorf("withdrawing by GET = %d", r.status)
	}
	if !strings.Contains(e.en(http.MethodGet, e.page(), nil).body, `<details class="danger">`) {
		t.Fatal("withdrawing is not behind a confirmation")
	}
	if r := e.en(http.MethodPost, e.page()+"/withdraw", nil); r.status != http.StatusSeeOther || r.location != e.page() {
		t.Fatalf("withdraw = %d %q", r.status, r.location)
	}
	after := e.en(http.MethodGet, e.page(), nil).body
	for _, want := range []string{"Invitation withdrawn. The link no longer works.", "<h1>Invite Papa</h1>", ">Create invitation</button>"} {
		if !strings.Contains(after, want) {
			t.Errorf("after withdrawing, page lacks %q\n%s", want, after)
		}
	}
	e.b.logout()
	if r := e.en(http.MethodGet, "/invite/"+tok, nil); r.status != http.StatusGone || !strings.Contains(r.body, "no longer valid") {
		t.Errorf("withdrawn link = %d\n%s", r.status, r.body)
	}
	if r := e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("papa", "papas long password")); r.status == http.StatusSeeOther {
		t.Errorf("a withdrawn link created an account: %d", r.status)
	}
	if _, err := e.b.db.AccountByUsername("papa"); err == nil {
		t.Error("an account exists")
	}
}

func TestLinkExpiresAfterSevenDaysAndTheParentSeesIt(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.now = e.b.now.Add(7*24*time.Hour - time.Minute)
	e.b.logout()
	if r := e.en(http.MethodGet, "/invite/"+tok, nil); r.status != http.StatusOK {
		t.Fatalf("a minute before expiry = %d", r.status)
	}
	e.b.now = e.b.now.Add(2 * time.Minute)
	r := e.en(http.MethodGet, "/invite/"+tok, nil)
	if r.status != http.StatusGone || !strings.Contains(r.body, "This invitation has expired") || !strings.Contains(r.body, "Saturday, 21.11.2026, 13:00") || !strings.Contains(r.body, "Please ask Mama for a new invitation.") {
		t.Errorf("expired link = %d\n%s", r.status, r.body)
	}
	if r := e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("papa", "papas long password")); r.status == http.StatusSeeOther {
		t.Errorf("an expired link created an account")
	}
	// Mama signs in again and sees it in words, with one action.
	e.signInMama()
	page := e.en(http.MethodGet, e.page(), nil).body
	for _, want := range []string{`<span class="flag warn">`, "Expired</span> on Saturday, 21.11.2026, 13:00. The link no longer works.", ">Create new invitation</button>"} {
		if !strings.Contains(page, want) {
			t.Errorf("expired state lacks %q\n%s", want, page)
		}
	}
	if strings.Contains(page, `<details class="danger">`) {
		t.Error("an expired invitation needs no withdrawing")
	}
}

func (e *inviteEnv) signInMama() {
	e.b.t.Helper()
	acc, err := e.b.db.AccountByUsername("mama")
	if err != nil {
		e.b.t.Fatal(err)
	}
	code, _ := auth.TOTPCode(acc.TOTPSecret, e.b.now)
	e.b.logout()
	if r := e.b.post("/login", url.Values{"username": {"mama"}, "password": {"correct horse battery"}, "code": {code}}); r.status != http.StatusSeeOther {
		e.b.t.Fatalf("Mama signs in = %d", r.status)
	}
}

// --- the invited parent ---

func TestInvitedParentOpensTheLinkAndSeesWhoInvitesAndWhoTheyAre(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.logout()
	r := e.en(http.MethodGet, "/invite/"+tok, nil)
	for _, want := range []string{
		`<div class="wizard">`, "<h1>Mama invites you to Hejmo</h1>", "You are registered as", `class="invitee"`, `data-shape="circle-empty"`, "<span>Papa</span>",
		"about 5 minutes", "2FAS", "The invitation works once, until <b>Saturday, 21.11.2026, 13:00</b>.", `href="/invite/` + tok + `/account"`, "Create account",
	} {
		if r.status != http.StatusOK || !strings.Contains(r.body, want) {
			t.Fatalf("welcome = %d, lacks %q\n%s", r.status, want, r.body)
		}
	}
	if strings.Contains(r.body, "Papa</a>") || strings.Contains(r.body, `name="name"`) {
		t.Error("the name must not be editable")
	}
	form := e.en(http.MethodGet, "/invite/"+tok+"/account", nil).body
	for _, want := range []string{"<h1>Your account</h1>", `name="username"`, `value="papa"`, `name="password"`, `minlength="10"`, `autocomplete="new-password"`, "Create account", "Accepting the invitation: step 2 of 3"} {
		if !strings.Contains(form, want) {
			t.Errorf("account form lacks %q\n%s", want, form)
		}
	}
	for _, not := range []string{`name="pin"`, `name="region"`, `name="home_name"`} {
		if strings.Contains(form, not) {
			t.Errorf("the account form asks for %s, which belongs to the household", not)
		}
	}
	if strings.Contains(form, tok) && strings.Count(form, tok) > 1 {
		t.Log("token appears more than once on the form") // the action contains it once
	}
}

func TestAcceptingTheInvitationGoesThroughTheWizardAndLandsSignedInOnHome(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.logout()
	e.en(http.MethodGet, "/invite/"+tok, nil)
	r := e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("papa", "papas long password"))
	if r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		t.Fatalf("accept = %d %q\n%s", r.status, r.location, r.body)
	}
	acc, err := e.b.db.AccountByPerson(e.papa.ID)
	if err != nil || acc.Username != "papa" || acc.PasswordHash == "" || strings.Contains(acc.PasswordHash, "papas long password") || acc.TOTPConfirmed {
		t.Fatalf("account = %+v, %v", acc, err)
	}
	// Until the code is confirmed Papa reaches nothing else.
	if home := e.b.get("/"); home.status != http.StatusSeeOther || home.location != "/login" {
		t.Errorf("home before the code = %d %q", home.status, home.location)
	}
	totp := e.en(http.MethodGet, "/setup/totp", nil).body
	for _, want := range []string{"Accepting the invitation: step 3 of 3", `data-count="3"`, "Your account", "(done)", `data-totp-secret="`} {
		if !strings.Contains(totp, want) {
			t.Errorf("one-time code page lacks %q\n%s", want, totp)
		}
	}
	secret := secretOn(t, totp)
	if r := e.en(http.MethodPost, "/setup/totp", url.Values{"code": {"000000"}}); r.status != http.StatusUnprocessableEntity {
		t.Errorf("wrong code = %d", r.status)
	}
	code, _ := auth.TOTPCode(secret, e.b.now)
	codes := e.en(http.MethodPost, "/setup/totp", url.Values{"code": {code}})
	if codes.status != http.StatusOK || !strings.Contains(codes.body, "Done, to Hejmo") || !strings.Contains(codes.body, "Accepting the invitation: step 3 of 3") || strings.Count(codes.body, "data-recovery-code=") != 10 {
		t.Fatalf("recovery codes = %d\n%s", codes.status, codes.body)
	}
	home := e.en(http.MethodGet, "/", nil)
	for _, want := range []string{"Welcome, Papa. Your account is ready, you are signed in.", "The parent PIN for the displays comes from Mama.", "Hello Papa"} {
		if home.status != http.StatusOK || !strings.Contains(home.body, want) {
			t.Fatalf("home = %d, lacks %q\n%s", home.status, want, home.body)
		}
	}
	if again := e.en(http.MethodGet, "/", nil); strings.Contains(again.body, "Welcome, Papa") {
		t.Error("the welcome line shows twice")
	}
	// The link is used up; a second account is never created.
	if r := e.en(http.MethodGet, "/invite/"+tok, nil); r.status != http.StatusGone || !strings.Contains(r.body, "This invitation was already used") || !strings.Contains(r.body, `href="/login"`) {
		t.Errorf("used link = %d\n%s", r.status, r.body)
	}
	e.b.logout()
	if r := e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("papa2", "another long password")); r.status == http.StatusSeeOther {
		t.Errorf("reusing the link redirected: %q", r.location)
	}
	if _, err := e.b.db.AccountByUsername("papa2"); err == nil {
		t.Error("a used link created a second account")
	}
}

func TestInvitedParentChoosesTheirOwnPasswordAndKeepsTheUsernameOnError(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.logout()
	short := e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("papa", "short"))
	if short.status != http.StatusUnprocessableEntity || !strings.Contains(short.body, `value="papa"`) || !strings.Contains(short.body, `id="err-password"`) || strings.Contains(short.body, "short\"") {
		t.Errorf("short password = %d\n%s", short.status, short.body)
	}
	if !strings.Contains(short.body, "Please enter it again") {
		t.Error("the hint does not say the password was not kept")
	}
	taken := e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("MAMA", "papas long password"))
	if taken.status != http.StatusUnprocessableEntity || !strings.Contains(taken.body, `value="MAMA"`) || !strings.Contains(taken.body, `id="err-username"`) || !strings.Contains(taken.body, "for example papa") {
		t.Errorf("taken username = %d\n%s", taken.status, taken.body)
	}
	empty := e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("  ", "papas long password"))
	if empty.status != http.StatusUnprocessableEntity || !strings.Contains(empty.body, `id="err-username"`) {
		t.Errorf("empty username = %d", empty.status)
	}
	if _, err := e.b.db.AccountByPerson(e.papa.ID); err == nil {
		t.Fatal("a refused form created an account")
	}
	if r := e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("papa", "papas long password")); r.status != http.StatusSeeOther {
		t.Errorf("the link must stay open after errors: %d", r.status)
	}
}

func TestChosenLanguageBecomesTheAccountLanguage(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.logout()
	if r := e.b.do(http.MethodPost, "/language", url.Values{"lang": {"en"}, "next": {"/invite/" + tok}}, nil); r.status != http.StatusSeeOther {
		t.Fatalf("language switch = %d", r.status)
	}
	if r := e.b.do(http.MethodPost, "/invite/"+tok+"/account", acceptForm("papa", "papas long password"), map[string]string{"Accept-Language": "de"}); r.status != http.StatusSeeOther {
		t.Fatalf("accept = %d", r.status)
	}
	if acc, _ := e.b.db.AccountByPerson(e.papa.ID); acc.Language != "en" {
		t.Errorf("account language = %q, want en", acc.Language)
	}
}

func TestUnknownOrCutOffLinksTellNothingAndAreRateLimited(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.logout()
	for _, bad := range []string{"K7QX-M2PD-9WRT-4HNA-ZC6E", tok[:len(tok)-3], "nonsense"} {
		r := e.en(http.MethodGet, "/invite/"+bad, nil)
		if r.status != http.StatusNotFound || !strings.Contains(r.body, "The box does not know this link") || strings.Contains(r.body, "Papa") || strings.Contains(r.body, "Mama") || strings.Contains(r.body, "2026") {
			t.Errorf("%q = %d\n%s", bad, r.status, r.body)
		}
	}
	for i := 0; i < 12; i++ {
		e.en(http.MethodGet, "/invite/"+inviteGuess(i), nil)
	}
	// Too many wrong tries: even the right link is refused with the same page.
	if r := e.en(http.MethodGet, "/invite/"+tok, nil); r.status != http.StatusTooManyRequests || !strings.Contains(r.body, "The box does not know this link") || strings.Contains(r.body, "Papa") {
		t.Errorf("after many wrong tries = %d\n%s", r.status, r.body)
	}
	e.b.now = e.b.now.Add(11 * time.Minute)
	if r := e.en(http.MethodGet, "/invite/"+tok, nil); r.status != http.StatusOK {
		t.Errorf("after the wait = %d", r.status)
	}
}

func inviteGuess(i int) string {
	digits := "23456789"
	c := string(digits[i%len(digits)])
	return strings.Repeat(c, 4) + "-" + strings.Repeat(string(digits[(i+1)%len(digits)]), 4) + "-2222-3333-4444"
}

func TestSignedInAsSomeoneElseTheInvitationStaysOpenAndSignOutComesBack(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	r := e.en(http.MethodGet, "/invite/"+tok, nil) // Mama scans Papa's code on her own phone
	for _, want := range []string{"<h1>This invitation is for Papa</h1>", "Invited is", "You are signed in on this device as", "Mama", `value="/invite/` + tok + `"`, "Sign out and let Papa continue", `href="/"`} {
		if r.status != http.StatusOK || !strings.Contains(r.body, want) {
			t.Fatalf("signed in as someone else = %d, lacks %q\n%s", r.status, want, r.body)
		}
	}
	if r := e.en(http.MethodGet, "/invite/"+tok+"/account", nil); r.status == http.StatusOK && strings.Contains(r.body, `name="password"`) {
		t.Error("the account form opens for someone who is signed in as another person")
	}
	if out := e.en(http.MethodPost, "/logout", url.Values{"next": {"/invite/" + tok}}); out.status != http.StatusSeeOther || out.location != "/invite/"+tok {
		t.Fatalf("logout = %d %q", out.status, out.location)
	}
	if out := e.b.post("/logout", url.Values{"next": {"https://evil.example/"}}); out.location != "/login" {
		t.Errorf("logout with a foreign next = %q", out.location)
	}
	if r := e.en(http.MethodGet, "/invite/"+tok, nil); !strings.Contains(r.body, "invites you to Hejmo") {
		t.Errorf("the invitation did not stay open\n%s", r.body)
	}
}

// ADR 0017: the second parent can sign in and reset the first parent's one-time codes.
func TestAcceptedParentSignsInAndResetsTheFirstParentsOneTimeCodes(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	secret := e.acceptAndSetUpCodes(tok)
	e.b.logout()
	code, _ := auth.TOTPCode(secret, e.b.now)
	if r := e.b.post("/login", url.Values{"username": {"papa"}, "password": {"papas long password"}, "code": {code}}); r.status != http.StatusSeeOther {
		t.Fatalf("Papa signs in = %d", r.status)
	}
	if page := e.en(http.MethodGet, "/account", nil).body; !strings.Contains(page, "Mama") || !strings.Contains(page, `href="/account/reset/`) {
		t.Fatalf("Papa cannot reach the reset of Mama's codes\n%s", page)
	}
	mama := personNamed(t, e.b, "Mama")
	r := e.en(http.MethodPost, "/account/reset/"+mama.ID, url.Values{"password": {"papas long password"}, "code": {code}})
	if r.status != http.StatusSeeOther || r.location != "/account" {
		t.Fatalf("reset = %d\n%s", r.status, r.body)
	}
	if acc, _ := e.b.db.AccountByPerson(mama.ID); acc.TOTPConfirmed || !acc.TOTPReset {
		t.Errorf("Mama's codes were not reset: %+v", acc)
	}
}

func TestParentStopsBeforeTheOneTimeCodeAndFinishesAtTheNextSignIn(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.logout()
	e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("papa", "papas long password"))
	e.b.logout() // Papa closes the browser here
	if r := e.en(http.MethodGet, "/invite/"+tok, nil); r.status != http.StatusGone || !strings.Contains(r.body, "already used") {
		t.Fatalf("the link must count as used: %d\n%s", r.status, r.body)
	}
	if r := e.b.post("/login", url.Values{"username": {"papa"}, "password": {"wrong password!"}, "code": {""}}); r.status != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", r.status)
	}
	r := e.b.post("/login", url.Values{"username": {"papa"}, "password": {"papas long password"}, "code": {""}})
	if r.status != http.StatusSeeOther || r.location != "/setup/totp" {
		t.Fatalf("sign in without code = %d %q, want the one-time code setup", r.status, r.location)
	}
	if home := e.b.get("/"); home.status != http.StatusSeeOther {
		t.Errorf("home before the code = %d, want nothing reachable", home.status)
	}
	// Mama sees it in words.
	e.signInMama()
	page := e.en(http.MethodGet, e.page(), nil).body
	for _, want := range []string{"<h1>Papa&#39;s sign-in</h1>", `<span class="flag warn">`, "One-time code missing", "At the next sign-in Papa sets it up", "<dt>Username</dt>", "papa"} {
		if !strings.Contains(page, want) {
			t.Errorf("sign-in page lacks %q\n%s", want, page)
		}
	}
	if strings.Contains(page, "Create new link") {
		t.Error("an accepted invitation offers a new link")
	}
}

func TestAcceptedInvitationPageShowsWhenAndThroughWhomAndLinksToTheReset(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.now = e.b.now.Add(31 * time.Hour)
	e.acceptAndSetUpCodes(tok)
	e.signInMama()
	page := e.en(http.MethodGet, e.page(), nil).body
	for _, want := range []string{
		"<h1>Papa&#39;s sign-in</h1>", "Can sign in", "<dt>Username</dt>", "<dt>Account created</dt>", "15.11.2026, 20:00 through your invitation", "<dt>One-time code</dt>", "Set up, 10 recovery codes",
		`<a href="/account/reset/` + e.papa.ID + `">Reset Papa&#39;s one-time codes</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("sign-in page lacks %q\n%s", want, page)
		}
	}
	if strings.Contains(page, `class="button"`) || strings.Contains(page, "Create new link") {
		t.Error("a finished invitation has no primary action")
	}
	overview := e.en(http.MethodGet, "/people", nil).body
	if !strings.Contains(overview, "Can sign in") || strings.Contains(overview, "Invite Papa") {
		t.Errorf("people overview does not say Papa can sign in\n%s", overview)
	}
	if r := e.en(http.MethodPost, e.page(), nil); r.status != http.StatusConflict && r.status != http.StatusSeeOther {
		t.Errorf("inviting someone who has an account = %d", r.status)
	}
	if r := e.en(http.MethodPost, e.page()+"/withdraw", nil); r.status == http.StatusNotFound {
		t.Log("withdrawing a used link is a 404") // nothing open
	}
}

func TestOnlyAParentCanInvite(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	phone := pairedPhone(t, e.b, e.mia, "Mia's phone") // a child's device session
	if r := phone.get(e.page(), "en"); r.status != http.StatusForbidden {
		t.Errorf("a child opens the invitation page = %d, want 403", r.status)
	}
	if r := phone.post(e.page(), url.Values{}); r.status != http.StatusForbidden {
		t.Errorf("a child creates an invitation = %d, want 403", r.status)
	}
	if _, err := e.b.db.LatestInvitation(homeID(t, e.b), e.papa.ID); err == nil {
		t.Error("an invitation exists")
	}
}

// A box with a public certificate has no trust step: the link is an HTTPS link.
func TestBoxWithAPublicCertificateGivesAnHTTPSLink(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	e.b.public.name = "hejmo.example.org"
	r := e.en(http.MethodPost, e.page(), nil)
	tok := inviteTokenRe.FindString(r.body)
	if !strings.Contains(r.body, "https://hejmo.example.org/invite/"+tok) || strings.Contains(r.body, "own certificate") {
		t.Errorf("link with a public certificate\n%s", r.body)
	}
}

// With the box's own CA the QR code leads over plain HTTP to the trust page
// first, like phone pairing (5.13), and then on to the invitation.
func TestShortPlainHTTPAddressLeadsThroughTheTrustPageToTheInvitation(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.logout()
	plain := e.b.plainServer()
	res := getPlain(t, plain.URL+"/i/"+tok)
	for _, want := range []string{"<h1>Mama invites you to Hejmo</h1>", "Accepting the invitation: step 1 of 3", `data-count="3"`, "Trust the box", `data-continue-link`, "Continue to the invitation", "/invite/" + tok} {
		if res.status != http.StatusOK || !strings.Contains(res.body, want) {
			t.Fatalf("trust page for the invitation = %d, lacks %q\n%s", res.status, want, res.body)
		}
	}
	// Nothing but reading is allowed over plain HTTP; the account form is HTTPS only.
	if r := getPlain(t, plain.URL+"/invite/"+tok+"/account"); r.status != http.StatusPermanentRedirect {
		t.Errorf("the account form over plain HTTP = %d, want a redirect to HTTPS", r.status)
	}
	// A dead link says so right there.
	e.signInMama()
	e.en(http.MethodPost, e.page()+"/withdraw", nil)
	if r := getPlain(t, plain.URL+"/i/"+tok); r.status != http.StatusGone || !strings.Contains(r.body, "no longer valid") {
		t.Errorf("withdrawn short link = %d\n%s", r.status, r.body)
	}
	if r := getPlain(t, plain.URL+"/i/not-a-token"); r.status != http.StatusNotFound {
		t.Errorf("unknown short link = %d", r.status)
	}
}

func TestShortAddressRedirectsToTheInvitationWhenThePublicNameIsUsed(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.logout()
	e.b.public.name = "hejmo.example.org"
	r := e.b.get("/i/" + tok)
	if r.status != http.StatusSeeOther || r.location != "https://hejmo.example.org/invite/"+tok {
		t.Errorf("short address = %d %q", r.status, r.location)
	}
}

func homeID(t *testing.T, b *box) string {
	t.Helper()
	h, err := b.db.HomeHousehold()
	if err != nil {
		t.Fatal(err)
	}
	return h.ID
}

// headerOfPost creates (again) and returns one response header.
func (e *inviteEnv) headerOfPost(path, header string) string {
	e.b.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.b.srv.URL+path, strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", e.b.srv.URL)
	res, err := e.b.client.Do(req)
	if err != nil {
		e.b.t.Fatal(err)
	}
	defer res.Body.Close()
	return res.Header.Get(header)
}

// getPlain reads a page over plain HTTP without following redirects.
func getPlain(t *testing.T, url string) resp {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Accept-Language", "en")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), string(raw)}
}

// Without the box's own CA there is no trust step: two steps instead of three.
func TestBoxWithAPublicCertificateHasTwoStepsAndNoTrustPage(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	e.b.public.name = "hejmo.example.org"
	tok := e.create()
	e.b.logout()
	if form := e.en(http.MethodGet, "/invite/"+tok+"/account", nil).body; !strings.Contains(form, "Accepting the invitation: step 1 of 2") || !strings.Contains(form, `data-count="2"`) {
		t.Errorf("account form\n%s", form)
	}
	e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("papa", "papas long password"))
	if totp := e.en(http.MethodGet, "/setup/totp", nil).body; !strings.Contains(totp, "Accepting the invitation: step 2 of 2") || !strings.Contains(totp, `data-count="2"`) {
		t.Errorf("one-time code page\n%s", totp)
	}
}

// A link that dies between opening the form and sending it shows its own
// state, not "used".
func TestLinkThatExpiresWhileTheFormIsOpenShowsExpiredNotUsed(t *testing.T) {
	t.Parallel()
	e := newInviteBox(t)
	tok := e.create()
	e.b.logout()
	calls, expireAt := 0, 3 // the link runs out after the check that opened the form
	handler := server.New(server.Options{DB: e.b.db, PKI: e.b.pki, Public: e.b.public, Now: func() time.Time {
		calls++
		at := e.b.now
		if calls == expireAt {
			e.b.now = e.b.now.Add(8 * 24 * time.Hour)
		}
		return at
	}})
	e.b.handler = handler
	e.b.srv.Close()
	e.b.srv = e.b.serveTLS()
	r := e.en(http.MethodPost, "/invite/"+tok+"/account", acceptForm("papa", "papas long password"))
	if r.status != http.StatusGone || !strings.Contains(r.body, "This invitation has expired") || strings.Contains(r.body, "already been used") {
		t.Errorf("link expired while the form was open = %d %q\n%s", r.status, r.location, r.body)
	}
}
