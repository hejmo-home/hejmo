package contactsync_test

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"

	"github.com/hejmo-home/hejmo/internal/contact"
	"github.com/hejmo-home/hejmo/internal/contactsync"
	"github.com/hejmo-home/hejmo/internal/store"
)

func lines(t *testing.T, c store.Contact, v contactsync.Version) string {
	t.Helper()
	text, err := contactsync.Text(contactsync.Encode(c, nil, v))
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestHejmoNameIsWrittenAsNickname(t *testing.T) {
	t.Parallel()
	c := store.Contact{ID: "c1", HejmoName: "Oma Gerda", FullName: "Gerda Müller"}
	got := lines(t, c, contactsync.V3)
	for _, want := range []string{"NICKNAME:Oma Gerda\r\n", "FN:Gerda Müller\r\n", "N:Müller;Gerda;;;\r\n", "UID:c1\r\n", "VERSION:3.0\r\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("card misses %q:\n%s", want, got)
		}
	}
}

func TestBirthdayWithoutYear(t *testing.T) {
	t.Parallel()
	c := store.Contact{ID: "c1", HejmoName: "Mia", BdayDay: 14, BdayMonth: 3}
	if got := lines(t, c, contactsync.V3); !strings.Contains(got, "BDAY;X-APPLE-OMIT-YEAR=1604:1604-03-14\r\n") {
		t.Errorf("3.0:\n%s", got)
	}
	if got := lines(t, c, contactsync.V4); !strings.Contains(got, "BDAY:--0314\r\n") {
		t.Errorf("4.0:\n%s", got)
	}
	c.BdayYear = 2019
	for _, v := range []contactsync.Version{contactsync.V3, contactsync.V4} {
		if got := lines(t, c, v); !strings.Contains(got, "BDAY:2019-03-14\r\n") {
			t.Errorf("%s with year:\n%s", v, got)
		}
	}
}

func decode(t *testing.T, text string) vcard.Card {
	t.Helper()
	card, err := vcard.NewDecoder(strings.NewReader(text)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return card
}

func TestBirthdayReading(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, line       string
		day, month, year int
	}{
		{"icloud 1604 with mark", "BDAY;X-APPLE-OMIT-YEAR=1604:1604-03-14", 14, 3, 0},
		{"1604 alone", "BDAY:1604-03-14", 14, 3, 0},
		{"4.0 without year", "BDAY:--0314", 14, 3, 0},
		{"with year", "BDAY:1985-03-14", 14, 3, 1985},
		{"with a time keeps the day", "BDAY:1985-03-14T23:30:00-05:00", 14, 3, 1985},
		{"basic with a time", "BDAY:19850314T000000Z", 14, 3, 1985},
		{"year before 1900", "BDAY:1850-03-14", 14, 3, 0},
	}
	for _, tc := range cases {
		card := decode(t, "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\n"+tc.line+"\r\nEND:VCARD\r\n")
		in := contactsync.Decode(card)
		if in.BdayDay != tc.day || in.BdayMonth != tc.month || in.BdayYear != tc.year {
			t.Errorf("%s: got %d.%d.%d", tc.name, in.BdayDay, in.BdayMonth, in.BdayYear)
		}
	}
}

func TestLabelsRoundTrip(t *testing.T) {
	t.Parallel()
	c := store.Contact{ID: "c1", HejmoName: "Opa", Numbers: []contact.Line{
		{Kind: contact.Cell, Value: "+49 170 1"},
		{Kind: contact.Work, Value: "089 2"},
		{Kind: contact.Custom, Label: "Opa Handy", Value: "0171 3"},
		{Kind: contact.Custom, Label: "HomeFAX", Value: "089 4"},
		{Kind: contact.Other, Value: "089 5"},
	}, Emails: []contact.Line{{Kind: contact.Home, Value: "opa@example.org"}, {Kind: contact.Custom, Label: "Verein", Value: "v@example.org"}}}
	for _, v := range []contactsync.Version{contactsync.V3, contactsync.V4} {
		text := lines(t, c, v)
		in := contactsync.Decode(decode(t, text))
		if len(in.Numbers) != 5 || len(in.Emails) != 2 {
			t.Fatalf("%s: %+v\n%s", v, in, text)
		}
		for i, l := range in.Numbers {
			if l != c.Numbers[i] {
				t.Errorf("%s number %d: %+v, want %+v\n%s", v, i, l, c.Numbers[i], text)
			}
		}
		for i, l := range in.Emails {
			if l != c.Emails[i] {
				t.Errorf("%s email %d: %+v, want %+v", v, i, l, c.Emails[i])
			}
		}
	}
	if !strings.Contains(lines(t, c, contactsync.V3), "X-ABLabel:_$!<HomeFAX>!$_") {
		t.Error("a known Apple label is written in Apple's form")
	}
}

func TestAppleCardIsRead(t *testing.T) {
	t.Parallel()
	text := "BEGIN:VCARD\r\nVERSION:3.0\r\nN:Müller;Gerda;;;\r\nFN:Gerda Müller\r\nNICKNAME:Oma Gerda\r\nitem1.TEL;type=pref:+49 89 1\r\nitem1.X-ABLabel:_$!<Mobile>!$_\r\n" +
		"TEL;type=WORK;type=VOICE:089 2\r\nitem2.EMAIL;type=INTERNET:\r\nitem2.X-ABLabel:_$!<Home>!$_\r\nEMAIL;type=INTERNET;type=HOME:oma@example.org\r\n" +
		"item3.URL:https://example.org\r\nitem3.X-ABLabel:_$!<HomePage>!$_\r\nORG:Acme;Sales\r\nNOTE:Hallo\\nWelt\r\nEND:VCARD\r\n"
	in := contactsync.Decode(decode(t, text))
	if in.Full != "Gerda Müller" || in.Nickname != "Oma Gerda" || in.Note != "Hallo\nWelt" {
		t.Fatalf("%+v", in)
	}
	if len(in.Numbers) != 2 || in.Numbers[0].Kind != contact.Cell || in.Numbers[1].Kind != contact.Work {
		t.Errorf("numbers %+v", in.Numbers)
	}
	if len(in.Emails) != 1 || in.Emails[0].Value != "oma@example.org" {
		t.Errorf("empty values never come through: %+v", in.Emails)
	}
	// What was kept comes back out.
	out := lines(t, store.Contact{ID: "c1", HejmoName: "Oma Gerda", Extra: in.Extra}, contactsync.V3)
	if !strings.Contains(out, "ORG:Acme;Sales") || !strings.Contains(out, "URL:https://example.org") || !strings.Contains(out, "X-ABLabel:_$!<HomePage>!$_") {
		t.Errorf("extras lost:\n%s", out)
	}
}

func TestETagChangesExactlyWithTheContent(t *testing.T) {
	t.Parallel()
	a := store.Contact{ID: "c1", HejmoName: "Mia", Numbers: []contact.Line{{Kind: contact.Cell, Value: "1"}}}
	b := a
	if contactsync.ETag(a) != contactsync.ETag(b) {
		t.Fatal("same content, other ETag")
	}
	b.Numbers = []contact.Line{{Kind: contact.Cell, Value: "2"}}
	if contactsync.ETag(a) == contactsync.ETag(b) {
		t.Fatal("changed content, same ETag")
	}
}

func personContact() store.Contact {
	return store.Contact{ID: "p1", PersonID: "p1", HejmoName: "Mama", FullName: "Anna Muster", BdayDay: 1, BdayMonth: 2, BdayYear: 1985, Note: "n",
		Emails:  []contact.Line{{Kind: contact.Home, Value: "mama@example.org"}},
		Numbers: []contact.Line{{Kind: contact.Cell, Value: "1", E164: "+491"}}, GroupIDs: []string{"g"}}
}

func TestPhoneChangesOfAHouseholdPerson(t *testing.T) {
	t.Parallel()
	cur := personContact()
	in := contactsync.Incoming{Full: "Other Name", Nickname: "Other", BdayDay: 9, BdayMonth: 9, Note: "x",
		Emails:  []contact.Line{{Kind: contact.Home, Value: "evil@example.org"}},
		Numbers: []contact.Line{{Kind: contact.Cell, Value: "2"}}, HasAddress: true, Street: "Weg 1", City: "Pasing"}
	got, changed := contactsync.Apply(&cur, in, nil)
	if !changed {
		t.Fatal("a new number is a change")
	}
	if got.Numbers[0].Value != "2" || got.Street != "Weg 1" || got.City != "Pasing" {
		t.Errorf("phone and address come from the phone: %+v", got)
	}
	if got.Emails[0].Value != "mama@example.org" || got.FullName != "Anna Muster" || got.BdayYear != 1985 || got.Note != "n" || got.HejmoName != "Mama" || len(got.GroupIDs) != 1 {
		t.Errorf("identity data stays: %+v", got)
	}
}

func TestAnUnchangedCardChangesNothing(t *testing.T) {
	t.Parallel()
	cur := store.Contact{ID: "c1", HejmoName: "Oma Gerda", FullName: "Gerda Müller", BdayDay: 3, BdayMonth: 4,
		Numbers: []contact.Line{{Kind: contact.Cell, Value: "1", E164: "+491"}}, Street: "Weg 1"}
	text, _ := contactsync.Text(contactsync.Encode(cur, nil, contactsync.V3))
	in := contactsync.Decode(decode(t, text))
	if _, changed := contactsync.Apply(&cur, in, nil); changed {
		t.Fatal("what the box sent, sent back, is no change")
	}
	// A card without a nickname keeps the Hejmo name; the full name equal to it adds nothing.
	cur2 := store.Contact{ID: "c2", HejmoName: "Oma"}
	if _, changed := contactsync.Apply(&cur2, contactsync.Incoming{Full: "Oma"}, nil); changed {
		t.Fatal("FN equal to the Hejmo name is no change")
	}
	got, changed := contactsync.Apply(&cur2, contactsync.Incoming{Full: "Oma Gerda"}, nil)
	if !changed || got.FullName != "Oma Gerda" || got.HejmoName != "Oma" {
		t.Fatalf("%+v %v", got, changed)
	}
}

func TestPhoneCreatesAndChangesFurtherContacts(t *testing.T) {
	t.Parallel()
	others := []store.Contact{{ID: "p1", PersonID: "p1", HejmoName: "Mama"}}
	got, changed := contactsync.Apply(nil, contactsync.Incoming{Full: "Mama"}, others)
	if !changed || got.HejmoName == "Mama" || got.HejmoName == "" {
		t.Fatalf("a taken name is changed: %+v", got)
	}
	long := strings.Repeat("x", 300)
	got, _ = contactsync.Apply(nil, contactsync.Incoming{Nickname: "Oma", Full: long, Note: long + long + long + long,
		Numbers: []contact.Line{{Kind: contact.Custom, Label: long, Value: "1"}}}, nil)
	if got.HejmoName != "Oma" || contact.Runes(got.FullName) > contact.MaxFullName || contact.Runes(got.Note) > contact.MaxNote || contact.Runes(got.Numbers[0].Label) > contact.MaxOwnLabel {
		t.Errorf("limits are kept without refusing the card: %d %d", contact.Runes(got.FullName), contact.Runes(got.Note))
	}
	cur := store.Contact{ID: "c1", HejmoName: "Oma", Relations: []store.Relation{{Word: "Oma"}}, ForChildren: true, HideBirthday: true}
	got, changed = contactsync.Apply(&cur, contactsync.Incoming{Nickname: "Großmutter", Full: "Gerda"}, nil)
	if !changed || got.HejmoName != "Großmutter" || !got.ForChildren || !got.HideBirthday || len(got.Relations) != 1 {
		t.Fatalf("%+v", got)
	}
	if _, changed = contactsync.Apply(nil, contactsync.Incoming{}, nil); changed {
		t.Error("a card with nothing in it makes no contact")
	}
}

// Phones save groups as cards of their own: iOS as X-ADDRESSBOOKSERVER-KIND, vCard 4.0 and DAVx5 as KIND.
func TestGroupCardsAreRecognisedInBothForms(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"4.0 KIND":        "BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:group\r\nFN:Familie\r\nMEMBER:urn:uuid:1\r\nEND:VCARD\r\n",
		"4.0 KIND, upper": "BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:GROUP\r\nFN:Familie\r\nEND:VCARD\r\n",
		"iOS":             "BEGIN:VCARD\r\nVERSION:3.0\r\nN:Familie;;;;\r\nFN:Familie\r\nX-ADDRESSBOOKSERVER-KIND:group\r\nX-ADDRESSBOOKSERVER-MEMBER:urn:uuid:1\r\nEND:VCARD\r\n",
	} {
		if !contactsync.IsGroup(decode(t, text)) {
			t.Errorf("%s is a group", name)
		}
	}
	for name, text := range map[string]string{
		"plain":      "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Gerda\r\nEND:VCARD\r\n",
		"individual": "BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:individual\r\nFN:Gerda\r\nEND:VCARD\r\n",
		"org":        "BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:org\r\nFN:Acme\r\nEND:VCARD\r\n",
	} {
		if contactsync.IsGroup(decode(t, text)) {
			t.Errorf("%s is no group", name)
		}
	}
}

// What a phone saved that Hejmo does not model goes back as it came: raw value and parameters, the first
// address with its TYPE, label and region (CODING_STANDARDS 2).
func TestWhatAPhoneSavedComesBackAsReceived(t *testing.T) {
	t.Parallel()
	text := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Gerda Müller\r\nNICKNAME:Oma Gerda\r\n" +
		"ORG:Firma\\; Abteilung;Team\r\nCATEGORIES:Family,Friends\r\nURL;TYPE=pref:https://example.org/a,b\r\n" +
		"item1.ADR;TYPE=HOME;TYPE=pref:;;Weg 1;Pasing;Bayern;80689;DE\r\nitem1.X-ABLabel:Zuhause\r\nEND:VCARD\r\n"
	in := contactsync.DecodeRaw(decode(t, text), []byte(text))
	cur := store.Contact{ID: "c1", HejmoName: "Oma Gerda", FullName: "Gerda Müller", Street: "Weg 1", City: "Pasing", Postcode: "80689", Country: "DE", Extra: in.Extra}
	out := lines(t, cur, contactsync.V3)
	for _, want := range []string{"ORG:Firma\\; Abteilung;Team\r\n", "CATEGORIES:Family,Friends\r\n", "URL;TYPE=pref:https://example.org/a,b\r\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("card misses %q:\n%s", want, out)
		}
	}
	m := regexp.MustCompile(`(?m)^(\w+)\.ADR;TYPE=HOME;TYPE=pref:;;Weg 1;Pasing;Bayern;80689;DE\r$`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, m[1]+".X-ABLabel:Zuhause\r\n") {
		t.Errorf("the first address keeps TYPE, region and label:\n%s", out)
	}
	if _, changed := contactsync.Apply(&cur, in, nil); changed {
		t.Error("what the box sent, sent back, is no change")
	}
	// A change of the street in Hejmo changes only the street.
	cur.Street = "Neu 2"
	if out := lines(t, cur, contactsync.V3); !strings.Contains(out, "ADR;TYPE=HOME;TYPE=pref:;;Neu 2;Pasing;Bayern;80689;DE\r\n") {
		t.Errorf("only the street changes:\n%s", out)
	}
	// Without an address in Hejmo, no address and no orphaned label go out.
	cur.Street, cur.City, cur.Postcode, cur.Country = "", "", "", ""
	if out := lines(t, cur, contactsync.V3); strings.Contains(out, "ADR") || strings.Contains(out, "Zuhause") {
		t.Errorf("no address, no label:\n%s", out)
	}
}

// Numbers and e-mail addresses over the limit of ten stay on the card: Hejmo keeps them raw and sends them
// back, so the phone does not delete them on its next sync (CODING_STANDARDS 2).
func TestLinesOverTheLimitComeBackAsReceived(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Gerda Müller\r\nN:Müller;Gerda;;;\r\n")
	for i := 1; i <= 12; i++ {
		b.WriteString("TEL;TYPE=CELL:+49 170 000" + strconv.Itoa(1000+i) + "\r\n")
	}
	for i := 1; i <= 11; i++ {
		b.WriteString("EMAIL;TYPE=INTERNET;TYPE=WORK:m" + strconv.Itoa(i) + "@example.org\r\n")
	}
	b.WriteString("item1.TEL:+49 89 99999\r\nitem1.X-ABLabel:Büro\r\nEND:VCARD\r\n")
	text := b.String()
	in := contactsync.DecodeRaw(decode(t, text), []byte(text))
	cur := store.Contact{ID: "c1", HejmoName: "Gerda", FullName: "Gerda Müller", Extra: in.Extra}
	got, _ := contactsync.Apply(nil, in, nil)
	cur.Numbers, cur.Emails = got.Numbers, got.Emails
	if len(cur.Numbers) != contact.MaxLines || len(cur.Emails) != contact.MaxLines {
		t.Fatalf("Hejmo models %d numbers and %d addresses", len(cur.Numbers), len(cur.Emails))
	}
	out := lines(t, cur, contactsync.V3)
	for i := 1; i <= 12; i++ {
		if want := "+49 170 000" + strconv.Itoa(1000+i) + "\r\n"; !strings.Contains(out, want) {
			t.Errorf("number %d is lost:\n%s", i, out)
		}
	}
	for i := 1; i <= 11; i++ {
		if want := "m" + strconv.Itoa(i) + "@example.org\r\n"; !strings.Contains(out, want) {
			t.Errorf("address %d is lost:\n%s", i, out)
		}
	}
	m := regexp.MustCompile(`(?m)^(\w+)\.TEL:\+49 89 99999\r$`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, m[1]+".X-ABLabel:Büro\r\n") {
		t.Errorf("a number over the limit keeps its label:\n%s", out)
	}
	if _, changed := contactsync.Apply(&cur, in, nil); changed {
		t.Error("what the box sent, sent back, is no change")
	}
	// Decoding what the box sent gives the same again: nothing grows or shrinks.
	again := contactsync.DecodeRaw(decode(t, out), []byte(out))
	if again.Extra != in.Extra {
		t.Errorf("the kept lines change on a second round:\n%s\n%s", in.Extra, again.Extra)
	}
}

// Hejmo does not store N: the phone's own N goes back while the full name is unchanged, and is made from the
// full name only when that was changed in Hejmo (CODING_STANDARDS 2).
func TestStructuredNameComesBackAsReceived(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ fn, n string }{
		"prefix and middle name": {"Dr. Hans von Berg", "von Berg;Hans;Peter;Dr.;"},
		"one word":               {"Max", ";Max;;;"},
	} {
		text := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:" + c.fn + "\r\nN:" + c.n + "\r\nEND:VCARD\r\n"
		in := contactsync.DecodeRaw(decode(t, text), []byte(text))
		cur := store.Contact{ID: "c1", HejmoName: "Hans", FullName: c.fn, Extra: in.Extra}
		if out := lines(t, cur, contactsync.V3); !strings.Contains(out, "N:"+c.n+"\r\n") || strings.Count(out, "\nN:") != 1 {
			t.Errorf("%s: N comes back as received:\n%s", name, out)
		}
		if _, changed := contactsync.Apply(&cur, in, nil); changed {
			t.Errorf("%s: what the box sent, sent back, is no change", name)
		}
		cur.FullName = "Anna Berg"
		if out := lines(t, cur, contactsync.V3); !strings.Contains(out, "N:Berg;Anna;;;\r\n") || strings.Count(out, "\nN:") != 1 {
			t.Errorf("%s: a name changed in Hejmo makes a new N:\n%s", name, out)
		}
	}
}

// A phone cannot reach the e-mail addresses or the displayed name of a household person through the lines Hejmo
// keeps as received: E-mail lines over the limit and N stay out of extra for them (ticket 299, ADR 0021).
func TestPhoneCannotChangeAHouseholdPersonThroughKeptLines(t *testing.T) {
	t.Parallel()
	cur := personContact()
	var b strings.Builder
	b.WriteString("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Anna Muster\r\nN:Other;Name;;;\r\nEMAIL;TYPE=HOME:mama@example.org\r\n")
	for i := 1; i <= 10; i++ {
		b.WriteString("EMAIL;TYPE=WORK:x" + strconv.Itoa(i) + "@evil.example\r\n")
	}
	b.WriteString("ORG:Firma\r\nEND:VCARD\r\n")
	text := b.String()
	in := contactsync.DecodeRaw(decode(t, text), []byte(text))
	got, _ := contactsync.Apply(&cur, in, nil)
	cur.Extra = *got.Extra
	out := lines(t, cur, contactsync.V3)
	if strings.Contains(out, "evil.example") || strings.Count(out, "\nEMAIL") != 1 {
		t.Errorf("e-mail addresses of a household person stay as they are:\n%s", out)
	}
	if strings.Contains(out, "N:Other") || !strings.Contains(out, "N:Muster;Anna;;;\r\n") {
		t.Errorf("the name of a household person is not the phone's N:\n%s", out)
	}
	if !strings.Contains(out, "ORG:Firma\r\n") {
		t.Errorf("other kept lines still come back:\n%s", out)
	}
}
