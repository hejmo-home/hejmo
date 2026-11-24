package contactimport

import (
	"slices"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/store"
	"github.com/hejmo-home/hejmo/internal/vcard"
)

// Ticket 300: what a contact file brings, found out before anything is saved. The example family: Mama, Papa,
// Sebastian, Mia, Oma Gerda (a contact outside the household), Kinderarzt.

func num(kind contact.Kind, v string) contact.Line { return contact.Line{Kind: kind, Value: v} }

func hejmo() []store.Contact {
	return []store.Contact{
		{ID: "mama", PersonID: "mama", HejmoName: "Mama", FullName: "Katharina Maier-Schmidt", Numbers: []contact.Line{num(contact.Cell, "0176 2345 6789")}, Emails: []contact.Line{num(contact.Home, "kathi@example.de")}, HasPhoto: true, BdayDay: 17, BdayMonth: 5, BdayYear: 1984},
		{ID: "papa", PersonID: "papa", HejmoName: "Papa", FullName: "Thomas Maier"},
		{ID: "ben", PersonID: "ben", HejmoName: "Ben", FullName: "Ben Maier"},
		{ID: "oma", HejmoName: "Oma Gerda", FullName: "Gerda Müller", Street: "Am Erlenbach 7", City: "Rosenheim", Postcode: "83022",
			Numbers:   []contact.Line{num(contact.Cell, "0171 3456 789"), num(contact.Home, "08031 87 65 43")},
			BdayDay:   14,
			BdayMonth: 3, HasPhoto: true,
			Relations: []store.Relation{{Word: "Oma", WholeFamily: true}}, GroupIDs: []string{"g-notfall"}},
		{ID: "arzt", HejmoName: "Kinderarzt", FullName: "Kinderarztpraxis Dr. Yılmaz", Numbers: []contact.Line{num(contact.Work, "089 1234 5670")}},
	}
}

func card(n int, name string, numbers []string, emails []string) vcard.Card {
	c := vcard.Card{Number: n, Line: n * 10, Full: name}
	for _, v := range numbers {
		c.Numbers = append(c.Numbers, num(contact.Cell, v))
	}
	for _, v := range emails {
		c.Emails = append(c.Emails, num(contact.Home, v))
	}
	return c
}

func dupFor(t *testing.T, p Plan, cardNo int) Dup {
	t.Helper()
	for _, d := range p.Dups {
		if d.Cards[0].Number == cardNo {
			return d
		}
	}
	t.Fatalf("card %d is no possible duplicate: %+v", cardNo, p)
	return Dup{}
}

// Acceptance: duplicates are found by phone number or e-mail, not only by name.
func TestADuplicateIsFoundByTheSameNumberWrittenAnotherWayWhateverTheName(t *testing.T) {
	t.Parallel()
	p := Build([]vcard.Card{card(1, "Gerda aus dem Garten", []string{"+49 171 3456789", "0160 999 1"}, nil)}, hejmo(), "DE")
	d := dupFor(t, p, 1)
	if d.Existing.ID != "oma" || d.Preselect != "merge" || len(d.Matches) != 1 {
		t.Fatalf("dup = %+v", d)
	}
	m := d.Matches[0]
	if m.Kind != MatchNumber || m.Hejmo.Value != "0171 3456 789" || m.File != "+49 171 3456789" || m.Code != "+49" {
		t.Errorf("match = %+v: the reason names Hejmo's spelling and says the file wrote it with +49", m)
	}
}

func TestADuplicateIsFoundByTheSameEmailAddressWithoutRegardToCase(t *testing.T) {
	t.Parallel()
	p := Build([]vcard.Card{card(1, "Dr. Yılmaz", []string{"0160 999 2"}, []string{" Kathi@Example.DE "})}, hejmo(), "DE")
	d := dupFor(t, p, 1)
	if d.Existing.ID != "mama" || d.Matches[0].Kind != MatchEmail || d.Preselect != "merge" {
		t.Errorf("dup = %+v", d)
	}
}

func TestAnEmailAndANumberMatchGiveBothReasons(t *testing.T) {
	t.Parallel()
	c := card(1, "Praxis", []string{"+49 89 12345670", "0160 999 3"}, []string{"praxis@example.de"})
	arzt := hejmo()
	arzt[4].Emails = []contact.Line{num(contact.Work, "PRAXIS@example.de")}
	d := dupFor(t, Build([]vcard.Card{c}, arzt, "DE"), 1)
	if len(d.Matches) != 2 || d.Matches[0].Kind != MatchNumber || d.Matches[1].Kind != MatchEmail {
		t.Errorf("matches = %+v", d.Matches)
	}
}

// Acceptance: empty values never match.
func TestEmptyValuesNeverMatch(t *testing.T) {
	t.Parallel()
	existing := hejmo()
	existing = append(existing, store.Contact{ID: "leer", HejmoName: "Ohne Nummer", Numbers: []contact.Line{num(contact.Cell, "  ")}, Emails: []contact.Line{num(contact.Home, "")}})
	c := card(1, "Auch ohne", []string{"", " ", "-", "()"}, []string{"", "  "})
	p := Build([]vcard.Card{c}, existing, "DE")
	if len(p.Dups) != 0 || len(p.Same) != 0 || len(p.News) != 1 {
		t.Errorf("a card without values matched: %+v", p)
	}
	// Two cards with only empty values do not match each other either.
	p = Build([]vcard.Card{card(1, "Eins", []string{"x"}, []string{""}), card(2, "Zwei", []string{"-"}, []string{" "})}, nil, "DE")
	if len(p.Dups) != 0 || len(p.News) != 2 {
		t.Errorf("empty values matched within the file: %+v", p)
	}
}

func TestOnlyTheSameNameIsASuggestionThatPreselectsNothing(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"gerda müller", "Oma Gerda"} {
		d := dupFor(t, Build([]vcard.Card{card(1, name, []string{"0160 777 8888"}, nil)}, hejmo(), "DE"), 1)
		if d.Existing.ID != "oma" || d.Preselect != "" || len(d.Matches) != 1 || d.Matches[0].Kind != MatchName {
			t.Errorf("%q: dup = %+v", name, d)
		}
	}
	// A number match wins over a name match elsewhere.
	d := dupFor(t, Build([]vcard.Card{card(1, "Oma Gerda", []string{"089 1234 5670", "0160 999 4"}, nil)}, hejmo(), "DE"), 1)
	if d.Existing.ID != "arzt" || d.Matches[0].Kind != MatchNumber {
		t.Errorf("number beat name: %+v", d)
	}
	// Different people with similar names are different.
	if p := Build([]vcard.Card{card(1, "Gerda Müllner", []string{"0160 1"}, nil)}, hejmo(), "DE"); len(p.Dups) != 0 {
		t.Errorf("a similar name matched: %+v", p.Dups)
	}
}

func TestACardThatBringsNothingNewIsAlreadyInHejmoWithItsName(t *testing.T) {
	t.Parallel()
	c := card(1, "Gerda Müller", []string{"+49 171 3456789", "08031/876543"}, nil)
	p := Build([]vcard.Card{c}, hejmo(), "DE")
	if len(p.Dups) != 0 || len(p.Same) != 1 || p.Same[0].Name != "Oma Gerda" {
		t.Errorf("plan = %+v", p)
	}
}

func TestMergingAddsNewValuesFillsEmptyFieldsAddsAYearAndNeverOverwrites(t *testing.T) {
	t.Parallel()
	c := card(1, "Gerda Müller", []string{"0171 3456 789", "08031 40 12 77"}, []string{"gerda@example.de"})
	c.Nickname = "Omi"
	c.Street, c.City, c.Postcode = "Am Erlenbach 9", "Rosenheim", "83022"
	c.BdayDay, c.BdayMonth, c.BdayYear = 14, 3, 1950
	c.Note = "Mag Rosen"
	c.Photo = []byte("jpeg")
	d := dupFor(t, Build([]vcard.Card{c}, hejmo(), "DE"), 1)
	m := d.Merge
	if len(m.AddNumbers) != 1 || m.AddNumbers[0].Value != "08031 40 12 77" || len(m.AddEmails) != 1 || m.AddFull != "" || m.AddAddress || !m.AddYear || m.AddBirthday || !m.AddNote || m.AddPhoto {
		t.Errorf("merge = %+v", m)
	}
	if !m.PhotoChoice || m.FileAddress != "Am Erlenbach 9, 83022 Rosenheim" {
		t.Errorf("photo choice %v, differing address %q", m.PhotoChoice, m.FileAddress)
	}
	ops, sum := d.apply("Kontakte Papa.vcf", nil)
	in := ops.Input
	if ops.MergeInto != "oma" || in.HejmoName != "Oma Gerda" || in.FullName != "Gerda Müller" || in.Street != "Am Erlenbach 7" || in.Note != "Mag Rosen" || in.BdayYear != 1950 {
		t.Errorf("input = %+v", in)
	}
	if len(in.Numbers) != 3 || in.Numbers[2].Value != "08031 40 12 77" || len(in.Emails) != 1 || len(in.Relations) != 1 || len(in.GroupIDs) != 1 || in.GroupIDs[0] != "g-notfall" || in.ImportFile != "Kontakte Papa.vcf" {
		t.Errorf("input lines = %+v", in)
	}
	if in.PhotoChange != store.KeepPicture {
		t.Error("the photo changed although the parent keeps the current one")
	}
	if sum != "Oma Gerda" {
		t.Errorf("name = %q", sum)
	}
	if ops, _ := d.apply("f.vcf", d.Cards); ops.Input.PhotoChange != store.SetPicture || string(ops.Input.Photo.JPEG) != "jpeg" || ops.Input.Photo.File != "f.vcf" {
		t.Errorf("the photo of the file was not set: %+v", ops.Input)
	}
}

func TestAPhotoIsAddedWhenHejmoHasNoneAndNeedsNoChoice(t *testing.T) {
	t.Parallel()
	c := card(1, "Kinderarzt", []string{"089 1234 5670"}, nil)
	c.Photo = []byte("jpeg")
	d := dupFor(t, Build([]vcard.Card{c}, hejmo(), "DE"), 1)
	if !d.Merge.AddPhoto || d.Merge.PhotoChoice {
		t.Errorf("merge = %+v", d.Merge)
	}
	if ops, _ := d.apply("f.vcf", nil); ops.Input.PhotoChange != store.SetPicture {
		t.Error("the photo was not added")
	}
}

func TestAHouseholdPersonOnlyGetsNumbersAddressAndPhoto(t *testing.T) {
	t.Parallel()
	c := card(1, "Katharina Maier", []string{"0176 23456789", "089 8899 0011"}, []string{"kathi.neu@example.de"})
	c.Nickname = "Kathi"
	c.Street = "Neue Straße 1"
	c.BdayDay, c.BdayMonth, c.BdayYear = 1, 2, 1980
	d := dupFor(t, Build([]vcard.Card{c}, hejmo(), "DE"), 1)
	if d.Existing.ID != "mama" || d.Preselect != "merge" || strings.Join(d.Choices, ",") != "merge,skip" {
		t.Fatalf("dup = %+v", d)
	}
	m := d.Merge
	if len(m.AddNumbers) != 1 || len(m.AddEmails) != 0 || m.AddFull != "" || m.AddBirthday || m.AddYear || !m.AddAddress || !m.LockedEmails || !m.LockedBirthday {
		t.Errorf("merge = %+v", m)
	}
	ops, _ := d.apply("f.vcf", nil)
	in := ops.Input
	if len(in.Emails) != 1 || in.Emails[0].Value != "kathi@example.de" || in.BdayDay != 17 || in.FullName != "Katharina Maier-Schmidt" || in.HejmoName != "Mama" || len(in.Relations) != 0 {
		t.Errorf("a person changed: %+v", in)
	}
	// A name-only match with a person offers merge or skip too, and preselects nothing.
	n := dupFor(t, Build([]vcard.Card{card(1, "Papa", []string{"0170 1234 5678"}, nil)}, hejmo(), "DE"), 1)
	if n.Existing.ID != "papa" || n.Preselect != "" || strings.Join(n.Choices, ",") != "merge,skip" {
		t.Errorf("person by name = %+v", n)
	}
}

func TestADuplicateOutsideTheHouseholdMayAlsoBeCreatedOnItsOwn(t *testing.T) {
	t.Parallel()
	d := dupFor(t, Build([]vcard.Card{card(1, "Oma Gerda", []string{"0160 777 8888"}, nil)}, hejmo(), "DE"), 1)
	if strings.Join(d.Choices, ",") != "merge,new,skip" || d.NewName != "Oma Gerda 2" || d.NewNote == nil || d.NewNote.Kind != NameTaken {
		t.Errorf("dup = %+v", d)
	}
}

func TestNewContactsAreNamedByNicknameThenFullNameAndExplainWhenTheNameIsChanged(t *testing.T) {
	t.Parallel()
	ben := card(1, "Ben Krüger", []string{"0151 2200 9911"}, nil)
	ben.Nickname = "Ben"
	lang := card(2, "Zahnarztpraxis Dr. Römer & Partner, Kieferorthopädie und Prophylaxe", []string{"089 3322 1100"}, nil)
	nick := card(3, "Tante Susanne Maier", []string{"0172 4455 6600"}, nil)
	nick.Nickname = "Tante Susanne"
	nobody := card(4, "", []string{"0160 000 111"}, nil)
	nobody.Org = "Musikschule"
	same1, same2 := card(5, "Anna Alt", []string{"0170 1"}, nil), card(6, "", []string{"0170 2"}, nil)
	same1.Nickname, same2.Nickname = "Anna", "ANNA"
	p := Build([]vcard.Card{ben, lang, nick, nobody, same1, same2}, hejmo(), "DE")
	got := map[int]New{}
	for _, n := range p.News {
		got[n.Card.Number] = n
	}
	if n := got[1]; n.Name != "Ben Krüger" || n.Note == nil || n.Note.Kind != NameTaken || n.Note.Wanted != "Ben" || !n.Note.ByPerson {
		t.Errorf("Ben = %+v %+v", n, n.Note)
	}
	if n := got[2]; n.Name != "Zahnarztpraxis Dr. Römer & Partner" || n.Note == nil || n.Note.Kind != NameLong {
		t.Errorf("long = %q %+v", n.Name, n.Note)
	}
	if n := got[3]; n.Name != "Tante Susanne" || n.Note != nil {
		t.Errorf("nickname = %q %+v", n.Name, n.Note)
	}
	if n := got[4]; n.Name != "Musikschule" || n.Note != nil {
		t.Errorf("organisation = %q", n.Name)
	}
	if got[5].Name != "Anna" || got[6].Name != "Anna 2" || got[6].Note == nil || got[6].Note.Kind != NameTaken || got[6].Note.ByPerson {
		t.Errorf("same names = %q, %q", got[5].Name, got[6].Name)
	}
	for _, n := range p.News {
		if contact.Runes(n.Name) > contact.MaxName {
			t.Errorf("%q is longer than 40 characters", n.Name)
		}
	}
}

func TestTwoCardsOfTheFileWithTheSameNumberAreOneQuestionAndNothingIsMergedSilently(t *testing.T) {
	t.Parallel()
	a := card(1, "Herr Brandl", []string{"0160 5544 3322"}, nil)
	a.Photo = []byte("jpeg")
	b := card(2, "Sepp Brandl", []string{"0160 5544 3322", "089 700 12 34"}, nil)
	other := card(3, "Frau Ganz Anders", []string{"0160 1"}, nil)
	p := Build([]vcard.Card{a, b, other}, hejmo(), "DE")
	if len(p.Dups) != 1 || len(p.News) != 1 || p.News[0].Card.Number != 3 {
		t.Fatalf("plan = %+v", p)
	}
	d := p.Dups[0]
	if !d.InFile || d.Existing != nil || len(d.Cards) != 2 || d.Preselect != "merge" || strings.Join(d.Choices, ",") != "merge,new,skip" || d.Matches[0].Kind != MatchNumber {
		t.Fatalf("dup = %+v", d)
	}
	op, _ := d.apply("f.vcf", nil)
	in := op.Input
	if op.MergeInto != "" || in.HejmoName != "Herr Brandl" || len(in.Numbers) != 2 || in.PhotoChange != store.SetPicture || string(in.Photo.JPEG) != "jpeg" {
		t.Errorf("merged = %+v", in)
	}
	ops := d.applyNew("f.vcf")
	if len(ops) != 2 || ops[0].Input.HejmoName != "Herr Brandl" || ops[1].Input.HejmoName != "Sepp Brandl" {
		t.Errorf("two contacts = %+v", ops)
	}
}

func TestACardWithTheSameNameAsAnEarlierNewCardIsAlsoAskedAbout(t *testing.T) {
	t.Parallel()
	p := Build([]vcard.Card{card(1, "Familie Nguyễn", []string{"089 5566 1234"}, nil), card(2, "familie nguyễn", []string{"0176 1"}, nil)}, nil, "DE")
	if len(p.Dups) != 1 || p.Dups[0].Preselect != "" || p.Dups[0].Matches[0].Kind != MatchName {
		t.Errorf("plan = %+v", p)
	}
}

func TestUnreadableCardsAreListedWithTheirReason(t *testing.T) {
	t.Parallel()
	cut := card(2, "Rudi Wagn", nil, nil)
	cut.Problem = vcard.ProblemCut
	empty := card(1, "", nil, nil)
	empty.Problem = vcard.ProblemEmpty
	p := Build([]vcard.Card{empty, cut, card(3, "Gut", []string{"0170 9"}, nil)}, nil, "DE")
	if len(p.Unread) != 2 || p.Unread[0].Card.Number != 1 || p.Unread[1].Problem != vcard.ProblemCut || len(p.News) != 1 {
		t.Errorf("plan = %+v", p)
	}
}

func TestADecisionsPlanMakesTheOpsAndNamesTheContactsThatWereMadeAndCompleted(t *testing.T) {
	t.Parallel()
	cards := []vcard.Card{
		card(1, "Gerda Müller", []string{"0171 3456 789", "08031 40 12 77"}, nil),   // merge
		card(2, "Kinderarzt Neu", []string{"089 1234 5670", "089 1234 5679"}, nil),  // skip
		card(3, "Ben Krüger", []string{"0151 2200 9911"}, nil),                      // new, taken
		card(4, "Fahrdienst Schwimmverein", nil, []string{"fahrdienst@sv.example"}), // new, not taken
		card(5, "Oma Gerda", []string{"0160 777 8888"}, nil),                        // name only: own contact
		card(6, "Katharina Maier-Schmidt", []string{"0176 23456789", "089 9"}, nil), // person, merge
	}
	cards[2].Nickname = "Ben"
	p := Build(cards, hejmo(), "DE")
	dec := Decisions{Dup: map[string]string{"d1": "merge", "d2": "skip", "d5": "new", "d6": "merge"}, Skip: map[string]bool{"n4": true}, Groups: []string{"g-sonstige"}}
	if miss := p.Missing(dec); len(miss) != 0 {
		t.Fatalf("missing = %v", miss)
	}
	ops, res := p.Ops(dec, "Kontakte Papa.vcf")
	if len(ops) != 4 {
		t.Fatalf("ops = %d: %+v", len(ops), ops)
	}
	if strings.Join(res.New, ",") != "Ben Krüger,Oma Gerda 2" || strings.Join(res.Completed, ",") != "Oma Gerda,Mama" {
		t.Errorf("result = %+v", res)
	}
	for _, op := range ops {
		if op.Input.ImportFile != "Kontakte Papa.vcf" {
			t.Errorf("op without the file: %+v", op)
		}
		if op.MergeInto == "" && (len(op.Input.GroupIDs) != 1 || op.Input.GroupIDs[0] != "g-sonstige") {
			t.Errorf("a new contact is in %v, want the chosen group", op.Input.GroupIDs)
		}
		if op.MergeInto != "" && len(op.Input.GroupIDs) > 1 {
			t.Errorf("a completed contact got new groups: %v", op.Input.GroupIDs)
		}
	}
	// A name-only match has nothing preselected, so a missing choice stops; a number match defaults to merging.
	if miss := p.Missing(Decisions{Dup: map[string]string{"d1": "merge"}}); strings.Join(miss, ",") != "d5" {
		t.Errorf("missing = %v, want d5 only", miss)
	}
	if miss := p.Missing(Decisions{Dup: map[string]string{"d5": "person"}}); strings.Join(miss, ",") != "d5" {
		t.Errorf("an unknown choice counts as missing: %v", miss)
	}
	if miss := p.Missing(Decisions{Dup: map[string]string{"d5": "skip"}}); len(miss) != 0 {
		t.Errorf("missing = %v", miss)
	}
}

// Merging never deletes: two cards of the file that match the same contact are one question and one operation, so
// the second cannot rewrite the contact without what the first added.
func TestTwoCardsThatMatchTheSameContactAreOneDupAndOneMergeThatKeepsBothCardsValues(t *testing.T) {
	t.Parallel()
	a := card(1, "Gerda", []string{"0171 3456789"}, []string{"gerda@example.de"})
	b := card(2, "Oma", []string{"08031 87 65 43"}, []string{"oma@example.de"})
	p := Build([]vcard.Card{a, b}, hejmo(), "DE")
	if len(p.Dups) != 1 || len(p.Dups[0].Cards) != 2 || p.Dups[0].Existing.ID != "oma" || p.Dups[0].Key != "d1" || p.Dups[0].Preselect != ChoiceMerge {
		t.Fatalf("plan = %+v", p)
	}
	ops, _ := p.Ops(Decisions{}, "f.vcf")
	if len(ops) != 1 || ops[0].MergeInto != "oma" {
		t.Fatalf("ops = %+v, want one merge into oma", ops)
	}
	var got []string
	for _, l := range ops[0].Input.Emails {
		got = append(got, l.Value)
	}
	if strings.Join(got, ",") != "gerda@example.de,oma@example.de" {
		t.Errorf("e-mail addresses = %v, want both", got)
	}
	// The own contacts a "new" choice makes get a name each.
	ops, res := p.Ops(Decisions{Dup: map[string]string{"d1": "new"}}, "f.vcf")
	if len(ops) != 2 || ops[0].Input.HejmoName == ops[1].Input.HejmoName || len(res.New) != 2 {
		t.Errorf("new = %+v", ops)
	}
}

// A card that shares a number only with a card that matched a contact is asked about with it, in either order.
func TestACardThatSharesANumberOnlyWithACardOfAContactIsInThatQuestion(t *testing.T) {
	t.Parallel()
	for _, swap := range []bool{false, true} {
		a := card(1, "Gerda", []string{"0171 3456789", "0160 111 222"}, nil)
		b := card(2, "Frau Müller", []string{"0160 111 222"}, nil)
		cards := []vcard.Card{a, b}
		if swap {
			a.Number, b.Number = 2, 1
			cards = []vcard.Card{b, a}
		}
		p := Build(cards, hejmo(), "DE")
		if len(p.Dups) != 1 || len(p.News) != 0 || len(p.Dups[0].Cards) != 2 || p.Dups[0].Existing == nil || p.Dups[0].Existing.ID != "oma" {
			t.Fatalf("swap=%v plan = %+v", swap, p)
		}
		if p.Dups[0].Cards[0].Number != 1 || p.Dups[0].Key != "d1" {
			t.Errorf("swap=%v the cards are in the order of the file: %+v", swap, p.Dups[0])
		}
	}
}

// A card that matches a contact only by name is its own question; when both are merged the contact still gets
// what both bring.
func TestTwoQuestionsMergedIntoOneContactAreOneOperationWithEverythingBothBring(t *testing.T) {
	t.Parallel()
	a := card(1, "Gerda Müller", []string{"0171 3456789"}, []string{"gerda@example.de"})
	b := card(2, "Gerda Müller", []string{"0160 777 8888"}, []string{"oma@example.de"})
	p := Build([]vcard.Card{a, b}, hejmo(), "DE")
	if len(p.Dups) != 2 {
		t.Fatalf("plan = %+v", p)
	}
	ops, res := p.Ops(Decisions{Dup: map[string]string{"d1": "merge", "d2": "merge"}}, "f.vcf")
	if len(ops) != 1 || len(res.Completed) != 1 || len(ops[0].Input.Emails) != 2 || len(ops[0].Input.Numbers) != 3 {
		t.Errorf("ops = %+v, res = %+v", ops, res)
	}
}

// A photo picked in the second of two questions that merge into one contact is not lost.
func TestAPhotoChosenInTheSecondOfTwoMergedQuestionsIsUsed(t *testing.T) {
	t.Parallel()
	a := card(1, "Gerda Müller", []string{"0160 111 1111"}, nil)
	a.Photo = []byte("AAA")
	b := card(2, "Gerda Müller", []string{"0160 222 2222"}, nil)
	b.Photo = []byte("BBB")
	p := Build([]vcard.Card{a, b}, hejmo(), "DE")
	if len(p.Dups) != 2 {
		t.Fatalf("plan = %+v", p)
	}
	dec := Decisions{Dup: map[string]string{"d1": "merge", "d2": "merge"}, Photo: map[string]string{"d2": "file"}}
	ops, _ := p.Ops(dec, "f.vcf")
	if len(ops) != 1 || ops[0].Input.Photo == nil || string(ops[0].Input.Photo.JPEG) != "BBB" {
		t.Errorf("ops = %+v, want the photo of the second card", ops)
	}
	ops, _ = p.Ops(Decisions{Dup: dec.Dup}, "f.vcf")
	if len(ops) != 1 || ops[0].Input.Photo != nil {
		t.Errorf("without a choice Hejmo keeps its photo: %+v", ops)
	}
}

// A card that is tied to the cards of a contact only by name stays out of that question, and the reasons name
// only what Hejmo has.
func TestACardTiedToAnotherNewCardOnlyByNameIsNotPulledIntoTheQuestionOfAContact(t *testing.T) {
	t.Parallel()
	a := card(1, "Gerda", []string{"0171 3456789", "0160 111 222"}, nil)
	b := card(2, "Frau Müller", []string{"0160 111 222"}, nil)
	c := card(3, "Frau Müller", nil, []string{"fm@example.de"})
	a.Numbers = append(a.Numbers, num(contact.Cell, "0160 333 444"))
	b.Numbers = append(b.Numbers, num(contact.Cell, "0160 333 444")) // only card 1 has it: a card-to-card match
	p := Build([]vcard.Card{a, b, c}, hejmo(), "DE")
	if len(p.Dups) != 1 || len(p.Dups[0].Cards) != 2 || len(p.News) != 1 || p.News[0].Card.Number != 3 {
		t.Fatalf("plan = %+v", p)
	}
	oma := hejmo()[3]
	for _, m := range p.Dups[0].Matches {
		if m.Hejmo.Value != oma.Numbers[0].Value && m.Hejmo.Value != oma.Numbers[1].Value {
			t.Errorf("reason %+v names a value Hejmo does not have", m)
		}
	}
}

// A cluster that is tied to two different contacts is not moved whole into one of them: every card goes to the
// contact it shares a number with, and cards tied only by name join nothing.
func TestACardTiedToTwoContactsDoesNotCarryItsClusterIntoTheLastOne(t *testing.T) {
	t.Parallel()
	c1 := card(1, "Gerda", []string{"0171 3456789", "0160 111 111"}, nil)
	c2 := card(2, "Praxis", []string{"089 1234 5670", "0160 222 222"}, nil)
	c3 := card(3, "Anna Berg", []string{"0160 111 111"}, nil)
	c4 := card(4, "Anna Berg", []string{"0160 222 222"}, nil)
	p := Build([]vcard.Card{c1, c2, c3, c4}, hejmo(), "DE")
	var got = map[string][]int{}
	for _, d := range p.Dups {
		for _, c := range d.Cards {
			got[d.Existing.ID] = append(got[d.Existing.ID], c.Number)
		}
	}
	if len(p.News) != 0 || !slices.Equal(got["oma"], []int{1, 3}) || !slices.Equal(got["arzt"], []int{2, 4}) {
		t.Fatalf("dups = %v, news = %+v", got, p.News)
	}
}

// The merge shows what the merged cards add, whichever card brings it.
func TestTheMergeNamesTheAddressAndNoteOfTheCardThatBringsThem(t *testing.T) {
	t.Parallel()
	a := card(1, "Praxis", []string{"089 1234 5670"}, []string{"p@example.de"})
	b := card(2, "Praxis Süd", []string{"089 1234 5670"}, nil)
	b.Street, b.City, b.Note = "Hauptstr 1", "München", "Mo-Fr"
	p := Build([]vcard.Card{a, b}, hejmo(), "DE")
	if len(p.Dups) != 1 || len(p.Dups[0].Cards) != 2 {
		t.Fatalf("plan = %+v", p)
	}
	if m := p.Dups[0].Merge; !m.AddAddress || m.Address != "Hauptstr 1, München" || !m.AddNote || m.Note != "Mo-Fr" {
		t.Errorf("merge = %+v", m)
	}
}

// A card tied to two different contacts (through numbers it shares with a card of each) belongs to neither question;
// it stays a new card.
func TestACardTiedToTwoDifferentContactsStaysANewCard(t *testing.T) {
	t.Parallel()
	p := Build([]vcard.Card{
		card(1, "Gerda", []string{"0171 3456789", "0160 111 111"}, nil),
		card(2, "Praxis", []string{"089 1234 5670", "0160 222 222"}, nil),
		card(3, "X Y", []string{"0160 111 111", "0160 222 222"}, nil),
	}, hejmo(), "DE")
	if len(p.Dups) != 2 || len(p.News) != 1 || p.News[0].Card.Number != 3 {
		t.Fatalf("card 3 must be a new card, in no question: %+v", p)
	}
	for _, d := range p.Dups {
		if len(d.Cards) != 1 {
			t.Errorf("question %s must hold only its own card: %+v", d.Key, d.Cards)
		}
	}
}

// Two new cards that share a number but are each tied to a different contact are linked to both contacts; they are
// not merged into either, they stay a question of their own (the two cards of the file).
func TestTwoCardsThatShareANumberButAreTiedToDifferentContactsAreMergedIntoNeither(t *testing.T) {
	t.Parallel()
	p := Build([]vcard.Card{
		card(1, "Gerda", []string{"0171 3456789", "0160 333 333"}, nil),
		card(2, "Praxis", []string{"089 1234 5670", "0160 444 444"}, nil),
		card(3, "X Y", []string{"0160 111 111", "0160 333 333"}, nil),
		card(4, "Z W", []string{"0160 111 111", "0160 444 444"}, nil),
	}, hejmo(), "DE")
	if len(p.Dups) != 3 {
		t.Fatalf("want three questions: %+v", p.Dups)
	}
	for _, d := range p.Dups {
		inFile := d.Existing == nil
		if inFile != (len(d.Cards) == 2) || (inFile && (d.Cards[0].Number != 3 || d.Cards[1].Number != 4)) {
			t.Errorf("question %s: want cards 1 and 2 alone with their contacts, 3 and 4 together: %+v", d.Key, d.Cards)
		}
	}
}

// Acceptance: duplicates found by phone number. A card that shares a number with two cards that share nothing
// themselves joins them into one question; no card of the three stays a preselected new contact.
func TestACardThatBridgesTwoClustersMakesThemOneQuestion(t *testing.T) {
	t.Parallel()
	p := Build([]vcard.Card{
		card(1, "A", []string{"0160 111 111"}, nil),
		card(2, "B", []string{"0160 222 222"}, nil),
		card(3, "C", []string{"0160 111 111", "0160 222 222"}, nil),
	}, hejmo(), "DE")
	if len(p.News) != 0 || len(p.Dups) != 1 || len(p.Dups[0].Cards) != 3 {
		t.Fatalf("want one question with the cards 1, 2 and 3: %+v", p)
	}
	if p.Dups[0].Preselect != ChoiceMerge {
		t.Errorf("preselect = %q", p.Dups[0].Preselect)
	}
}

// The grouping does not depend on the order of the cards in the file.
func TestTheGroupingOfCardsDoesNotDependOnTheOrderOfTheFile(t *testing.T) {
	t.Parallel()
	cards := []vcard.Card{
		card(1, "Gerda", []string{"0171 3456789", "0160 111 111"}, nil),
		card(2, "Praxis", []string{"089 1234 5670", "0160 222 222"}, nil),
		card(3, "X", []string{"0160 333 333"}, nil),
		card(4, "Y", []string{"0160 222 222", "0160 555 555"}, nil),
		card(5, "Z", []string{"0160 111 111", "0160 666 666"}, nil),
		card(6, "W", []string{"0160 555 555", "0160 666 666"}, nil),
	}
	signature := func(p Plan) string {
		var parts []string
		for _, d := range p.Dups {
			var ns []string
			for _, c := range d.Cards {
				ns = append(ns, itoa(c.Number))
			}
			slices.Sort(ns)
			id := "file"
			if d.Existing != nil {
				id = d.Existing.ID
			}
			parts = append(parts, id+":"+strings.Join(ns, ","))
		}
		for _, n := range p.News {
			parts = append(parts, "new:"+itoa(n.Card.Number))
		}
		slices.Sort(parts)
		return strings.Join(parts, " ")
	}
	want := signature(Build(slices.Clone(cards), hejmo(), "DE"))
	var perm func(k int, a []vcard.Card)
	perm = func(k int, a []vcard.Card) {
		if k == len(a) {
			if got := signature(Build(slices.Clone(a), hejmo(), "DE")); got != want {
				t.Fatalf("order %v: %q, want %q", a, got, want)
			}
			return
		}
		for i := k; i < len(a); i++ {
			a[k], a[i] = a[i], a[k]
			perm(k+1, a)
			a[k], a[i] = a[i], a[k]
		}
	}
	perm(0, slices.Clone(cards))
	// 4, 5 and 6 are chained and tied to two contacts: none joins either question.
	if !strings.Contains(want, "file:4,5,6") || !strings.Contains(want, "oma:1") || !strings.Contains(want, "arzt:2") {
		t.Errorf("grouping = %q", want)
	}
}
