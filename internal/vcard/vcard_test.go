package vcard

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/contact"
)

// Ticket 300: the parser reads vCard 2.1 (quoted-printable with a charset), 3.0 and 4.0, keeps every number
// and e-mail address of every card, reads embedded photos, never follows a photo link and ignores relations.

func crlf(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }

func values(ls []contact.Line) []string {
	var out []string
	for _, l := range ls {
		out = append(out, string(l.Kind)+"|"+l.Label+"|"+l.Value)
	}
	return out
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

func one(t *testing.T, data []byte) Card {
	t.Helper()
	cards, err := Parse(data)
	if err != nil || len(cards) != 1 {
		t.Fatalf("Parse = %d cards, %v", len(cards), err)
	}
	return cards[0]
}

func TestVCard21QuotedPrintableWithCharsetsIsDecoded(t *testing.T) {
	t.Parallel()
	// ISO-8859-1 bytes escaped as quoted-printable, UTF-8 bytes escaped as quoted-printable, a soft line break,
	// and a raw ISO-8859-1 byte in a value that has a CHARSET.
	in := crlf("BEGIN:VCARD\nVERSION:2.1\n" +
		"N;CHARSET=ISO-8859-1;ENCODING=QUOTED-PRINTABLE:M=FCller;Gerda;;;\n" +
		"FN;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:Gerda M=C3=BCller\n" +
		"NICKNAME;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:Oma =C3=96ma\n" +
		"TEL;CELL;PREF:0171 3456 789\nTEL;HOME:08031 87 65 43\n" +
		"EMAIL;INTERNET;HOME:gerda.mueller@example.de\n" +
		"ADR;HOME;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:;;Am Erlenbach 7;Rosenheim;;83022;Deutschland\n" +
		"NOTE;CHARSET=UTF-8;ENCODING=QUOTED-PRINTABLE:Gru=C3=9F aus=\n Rosenheim=0D=0Azweite Zeile\n" +
		"END:VCARD\n")
	c := one(t, in)
	if c.Full != "Gerda Müller" || c.Nickname != "Oma Öma" {
		t.Errorf("names = %q, %q", c.Full, c.Nickname)
	}
	eq(t, "numbers", values(c.Numbers), []string{"cell||0171 3456 789", "home||08031 87 65 43"})
	eq(t, "emails", values(c.Emails), []string{"home||gerda.mueller@example.de"})
	if c.Street != "Am Erlenbach 7" || c.City != "Rosenheim" || c.Postcode != "83022" || c.Country != "Deutschland" {
		t.Errorf("address = %q %q %q %q", c.Street, c.Postcode, c.City, c.Country)
	}
	if c.Note != "Gruß aus Rosenheim\nzweite Zeile" {
		t.Errorf("note = %q", c.Note)
	}
}

func TestVCard21NameWithoutFNComesFromTheStructuredNameInItsCharset(t *testing.T) {
	t.Parallel()
	in := crlf("BEGIN:VCARD\nVERSION:2.1\nN;CHARSET=ISO-8859-1;ENCODING=QUOTED-PRINTABLE:M=FCller;Gerda;;Dr.;\nEND:VCARD\n")
	if c := one(t, in); c.Full != "Dr. Gerda Müller" {
		t.Errorf("full name = %q", c.Full)
	}
	// A raw Latin-1 byte (not valid UTF-8) in a value with a CHARSET.
	raw := append([]byte("BEGIN:VCARD\r\nVERSION:2.1\r\nFN;CHARSET=ISO-8859-1:M"), 0xFC)
	raw = append(raw, []byte("ller\r\nEND:VCARD\r\n")...)
	if c := one(t, raw); c.Full != "Müller" {
		t.Errorf("raw latin-1 = %q", c.Full)
	}
}

func TestVCard30ReadsTypesEscapesFoldingAndBirthday(t *testing.T) {
	t.Parallel()
	in := crlf("BEGIN:VCARD\nVERSION:3.0\nFN:Mama Maier\\, Katharina\nN:Maier-Schmidt;Katharina;;;\nNICKNAME:Mama\n" +
		"TEL;TYPE=CELL,VOICE:+49 176 2345 6789\nTEL;TYPE=WORK:089 4455 6677-12\nTEL;TYPE=HOME,FAX:089 8899 0011\n" +
		"EMAIL;TYPE=INTERNET,HOME:kathi.maier@example.de\nEMAIL;TYPE=WORK:kathi@arbeit.example\n" +
		"ORG:Beispiel GmbH;Abteilung\nTITLE:Leiterin\n" +
		"BDAY:1984-05-17\nNOTE:Eine lange Zeile, die\n  umgebrochen wurde\nEND:VCARD\n")
	c := one(t, in)
	if c.Full != "Mama Maier, Katharina" || c.Nickname != "Mama" {
		t.Errorf("names = %q, %q", c.Full, c.Nickname)
	}
	eq(t, "numbers", values(c.Numbers), []string{"cell||+49 176 2345 6789", "work||089 4455 6677-12", "custom|Fax|089 8899 0011"})
	eq(t, "emails", values(c.Emails), []string{"home||kathi.maier@example.de", "work||kathi@arbeit.example"})
	if c.BdayDay != 17 || c.BdayMonth != 5 || c.BdayYear != 1984 {
		t.Errorf("birthday = %d.%d.%d", c.BdayDay, c.BdayMonth, c.BdayYear)
	}
	if c.Note != "Eine lange Zeile, die umgebrochen wurde" {
		t.Errorf("note = %q", c.Note)
	}
	if c.Org != "Beispiel GmbH" || c.Title != "Leiterin" {
		t.Errorf("org, title = %q, %q", c.Org, c.Title)
	}
}

func TestVCard40ReadsQuotedTypesBirthdayWithoutYearAndUriNumbers(t *testing.T) {
	t.Parallel()
	in := crlf("BEGIN:VCARD\nVERSION:4.0\nFN:Papa Maier\nTEL;TYPE=\"cell,voice\";PREF=1:tel:+49-151-9876-5432\n" +
		"TEL;VALUE=uri;TYPE=work:tel:+49-89-1234-5670;ext=12\nEMAIL;TYPE=home:thomas@example.de\nBDAY:--0314\nEND:VCARD\n")
	c := one(t, in)
	eq(t, "numbers", values(c.Numbers), []string{"cell||+49-151-9876-5432", "work||+49-89-1234-5670"})
	if c.BdayDay != 14 || c.BdayMonth != 3 || c.BdayYear != 0 {
		t.Errorf("birthday = %d.%d.%d, want 14.3. without a year", c.BdayDay, c.BdayMonth, c.BdayYear)
	}
	for in, want := range map[string][3]int{"19500314": {14, 3, 1950}, "--03-14": {14, 3, 0}, "1604-03-14": {14, 3, 0}, "1950-03-14T00:00:00Z": {14, 3, 1950}, "garbage": {0, 0, 0}, "1950-13-40": {0, 0, 0}} {
		c := one(t, crlf("BEGIN:VCARD\nVERSION:3.0\nFN:X\nBDAY:"+in+"\nEND:VCARD\n"))
		if got := [3]int{c.BdayDay, c.BdayMonth, c.BdayYear}; got != want {
			t.Errorf("BDAY %q = %v, want %v", in, got, want)
		}
	}
}

// Acceptance: multi-card files keep all phone numbers and e-mail addresses per card.
func TestAMultiCardFileKeepsEveryNumberAndEmailOfEveryCard(t *testing.T) {
	t.Parallel()
	in := crlf("BEGIN:VCARD\nVERSION:3.0\nFN:Eins\nTEL;TYPE=CELL:0171 1\nTEL;TYPE=HOME:0171 2\nTEL;TYPE=WORK:0171 3\nTEL:0171 4\n" +
		"EMAIL:a1@example.de\nEMAIL:a2@example.de\nEMAIL:a3@example.de\nEND:VCARD\n" +
		"BEGIN:VCARD\nVERSION:2.1\nFN:Zwei\nTEL;CELL:0172 1\nTEL;WORK:0172 2\nEMAIL;HOME:b1@example.de\nEMAIL;WORK:b2@example.de\nEND:VCARD\n" +
		"BEGIN:VCARD\nVERSION:4.0\nFN:Drei\nTEL:0173 1\nTEL:0173 2\nTEL:0173 3\nEMAIL:c1@example.de\nEND:VCARD\n")
	cards, err := Parse(in)
	if err != nil || len(cards) != 3 {
		t.Fatalf("Parse = %d cards, %v", len(cards), err)
	}
	counts := [][2]int{{4, 3}, {2, 2}, {3, 1}}
	for i, c := range cards {
		if len(c.Numbers) != counts[i][0] || len(c.Emails) != counts[i][1] {
			t.Errorf("card %d (%s) has %d numbers and %d e-mail addresses, want %v", i+1, c.Full, len(c.Numbers), len(c.Emails), counts[i])
		}
		if c.Number != i+1 {
			t.Errorf("card %d is numbered %d", i+1, c.Number)
		}
	}
}

func TestAppleGroupLabelsBecomeOwnLabelsAndCaseAndBlankLinesDoNotMatter(t *testing.T) {
	t.Parallel()
	in := crlf("\xef\xbb\xbfbegin:vcard\n\nversion:3.0\nfn:Oma Gerda\nitem1.TEL;type=pref:0160 111 2223\nitem1.X-ABLabel:Gartenhaus\n" +
		"item2.TEL:0160 999\nitem2.X-ABLabel:_$!<Mobile>!$_\nitem3.EMAIL;type=INTERNET:x@example.de\nitem3.X-ABLabel:Verein\n" +
		"X-ABRELATEDNAMES;type=pref:Papa\nRELATED;TYPE=friend:urn:uuid:123\nitem4.X-ABRELATEDNAMES:Mia\nend:vcard\n")
	c := one(t, in)
	eq(t, "numbers", values(c.Numbers), []string{"custom|Gartenhaus|0160 111 2223", "cell||0160 999"})
	eq(t, "emails", values(c.Emails), []string{"custom|Verein|x@example.de"})
}

func TestEmbeddedPhotosAreReadInEveryVersionAndAWebLinkIsNeverFollowed(t *testing.T) {
	t.Parallel()
	pic := []byte("\xff\xd8\xff\xe0 not really a JPEG but bytes")
	b64 := base64.StdEncoding.EncodeToString(pic)
	fold := func(s string) string { return s[:30] + "\n " + s[30:] }
	v21 := one(t, crlf("BEGIN:VCARD\nVERSION:2.1\nFN:A\nPHOTO;ENCODING=BASE64;JPEG:"+fold(b64)+"\n\nEND:VCARD\n"))
	v30 := one(t, crlf("BEGIN:VCARD\nVERSION:3.0\nFN:B\nPHOTO;ENCODING=b;TYPE=JPEG:"+fold(b64)+"\nEND:VCARD\n"))
	v40 := one(t, crlf("BEGIN:VCARD\nVERSION:4.0\nFN:C\nPHOTO:data:image/jpeg;base64,"+fold(b64)+"\nEND:VCARD\n"))
	for name, c := range map[string]Card{"2.1": v21, "3.0": v30, "4.0": v40} {
		if string(c.Photo) != string(pic) || c.PhotoLink {
			t.Errorf("%s: photo = %q, link %v", name, c.Photo, c.PhotoLink)
		}
	}
	for _, line := range []string{"PHOTO;VALUE=uri:https://example.com/x.jpg", "PHOTO;VALUE=URL:http://example.com/x.jpg", "PHOTO:https://example.com/x.jpg"} {
		c := one(t, crlf("BEGIN:VCARD\nVERSION:3.0\nFN:D\n"+line+"\nEND:VCARD\n"))
		if len(c.Photo) != 0 || !c.PhotoLink {
			t.Errorf("%q: photo %d bytes, link %v: a link is noted, never read", line, len(c.Photo), c.PhotoLink)
		}
	}
	if c := one(t, crlf("BEGIN:VCARD\nVERSION:3.0\nFN:E\nPHOTO;ENCODING=b:!!!notbase64\nEND:VCARD\n")); len(c.Photo) != 0 {
		t.Error("broken base64 gave a photo")
	}
}

func TestCardsThatCannotBeReadAreReportedWithTheirNumberAndLine(t *testing.T) {
	t.Parallel()
	in := crlf("BEGIN:VCARD\nVERSION:3.0\nFN:Gut\nTEL:0171 1\nEND:VCARD\n" + // 1
		"BEGIN:VCARD\nVERSION:3.0\nNOTE:nur eine Notiz\nEND:VCARD\n" + // 2: no name, number or e-mail address
		"BEGIN:VCARD\nVERSION:3.0\nFN:Rudi Wagn\nTEL:0171 9\n" + // 3: cut off by the next card
		"BEGIN:VCARD\nVERSION:3.0\nFN:Nach der Lücke\nEND:VCARD\n" + // 4
		"BEGIN:VCARD\nVERSION:3.0\nFN:Ende fehlt\nTEL:0171 8") // 5: cut off by the end of the file
	cards, err := Parse(in)
	if err != nil || len(cards) != 5 {
		t.Fatalf("Parse = %d cards, %v", len(cards), err)
	}
	want := []Problem{"", ProblemEmpty, ProblemCut, "", ProblemCut}
	for i, c := range cards {
		if c.Problem != want[i] {
			t.Errorf("card %d problem = %q, want %q", i+1, c.Problem, want[i])
		}
	}
	if cards[2].Line != 10 || cards[2].Full != "Rudi Wagn" || cards[4].Line != 18 {
		t.Errorf("cut cards: line %d (%q), line %d", cards[2].Line, cards[2].Full, cards[4].Line)
	}
	if cards[0].Line != 1 || cards[1].Line != 6 {
		t.Errorf("lines = %d, %d", cards[0].Line, cards[1].Line)
	}
}

func TestFilesThatAreNoContactFilesOrEmptyAreRefused(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		data []byte
		want error
	}{
		"picture":    {[]byte("\xff\xd8\xff\xe0\x00\x10JFIF"), ErrNotVCard},
		"text":       {[]byte("Hello world\nthis is a letter"), ErrNotVCard},
		"html":       {[]byte("<html><body>BEGIN:VCARD</body></html>"), ErrNotVCard},
		"empty":      {nil, ErrEmpty},
		"blank":      {[]byte(" \r\n\r\n"), ErrEmpty},
		"only begin": {[]byte("BEGIN:VCARD\r\n"), nil},
	} {
		_, err := Parse(c.data)
		if err != c.want {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
}

func TestLongNumbersListsAndNamesAreKeptWhole(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Viele\r\n")
	for i := 0; i < 14; i++ {
		fmt.Fprintf(&b, "TEL:0171 %04d\r\n", i)
	}
	b.WriteString("TEL:0171 0000\r\nEND:VCARD\r\n")
	if c := one(t, []byte(b.String())); len(c.Numbers) != 14 {
		t.Errorf("%d numbers: every distinct number of a card is kept, the same number twice once", len(c.Numbers))
	}
}
