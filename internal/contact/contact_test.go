package contact_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/contact"
)

func TestNumberKeepsWhatWasTypedAndNamesTheFirstBadCharacter(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"112", "116117", "0171 3456 789", "+49 (0)89 1234-56/7", "089.1234.56", "+491711234567"} {
		if code, bad := contact.CheckNumber(ok); code != "" {
			t.Errorf("CheckNumber(%q) = %q %q, want valid", ok, code, bad)
		}
	}
	for _, c := range []struct{ in, code, bad string }{
		{"089 23x4 5678", "char", "x"},
		{"089 23 + 45", "char", "+"}, // + only at the start
		{"0171#5", "char", "#"},
		{"12", "digits", ""},
		{"+- ()", "digits", ""},
		{"abc 12", "char", "a"},
	} {
		if code, bad := contact.CheckNumber(c.in); code != c.code || bad != c.bad {
			t.Errorf("CheckNumber(%q) = %q %q, want %q %q", c.in, code, bad, c.code, c.bad)
		}
	}
}

func TestTelHrefHoldsOnlyDigitsAndALeadingPlus(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"0171 3456 789": "tel:01713456789", "+49 (0)89 1234-56": "tel:+498912345" + "6", "+49 (0) 171/3456-789": "tel:+491713456789", "+49(0)171 3456789": "tel:+491713456789", "0(0)89": "tel:0089", "089/12.34": "tel:0891234"} {
		if got := contact.TelHref(in); got != want {
			t.Errorf("TelHref(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmailNeedsOneAtAndADotInTheDomain(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"gerda.mueller@example.de", "a@b.co", "x+y@sub.example.org"} {
		if code := contact.CheckEmail(ok); code != "" {
			t.Errorf("CheckEmail(%q) = %q", ok, code)
		}
	}
	for in, want := range map[string]string{
		"familie.schulz@example": "dot", "nodomain": "at", "a@@b.de": "at", "a@b@c.de": "at", "@b.de": "at", "a@.de": "dot", "a@b.": "dot", "a b@c.de": "space",
	} {
		if got := contact.CheckEmail(in); got != want {
			t.Errorf("CheckEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBirthdayNeedsDayAndMonthAndRejectsDatesThatCannotExist(t *testing.T) {
	t.Parallel()
	today := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		d, m, y int
		want    string
	}{
		{0, 0, 0, ""},  // no birthday
		{14, 3, 0, ""}, // year unknown
		{14, 3, 1956, ""},
		{29, 2, 0, ""}, // a leap day without a year is possible
		{29, 2, 2024, ""},
		{29, 2, 2025, "date"}, // not a leap year
		{31, 2, 2018, "date"},
		{31, 4, 0, "date"},
		{14, 0, 0, "pair"},
		{0, 3, 0, "pair"},
		{0, 0, 1956, "pair"}, // a year alone is no birthday
		{14, 3, 1899, "year"},
		{14, 3, 2027, "year"},
		{24, 11, 2026, "future"},
		{23, 11, 2026, ""},
		{32, 1, 0, "date"},
		{1, 13, 0, "date"},
	} {
		if got := contact.CheckBirthday(c.d, c.m, c.y, today); got != c.want {
			t.Errorf("CheckBirthday(%d,%d,%d) = %q, want %q", c.d, c.m, c.y, got, c.want)
		}
	}
}

func TestCleanDropsEmptyLinesAndKeepsTheTypedValue(t *testing.T) {
	t.Parallel()
	in := []contact.Line{
		{Kind: contact.Cell, Value: "  0171 3456 789 "},
		{Kind: contact.Home, Value: "   "},
		{Kind: contact.Custom, Label: " Gartenhaus ", Value: "0160 111 2223"},
		{Kind: contact.Work, Value: ""},
	}
	got := contact.CleanLines(in)
	if len(got) != 2 || got[0].Value != "0171 3456 789" || got[1].Label != "Gartenhaus" {
		t.Fatalf("CleanLines = %+v", got)
	}
}

// Ticket 297: the initials that stand in for a missing photo.
func TestInitialsAreTheFirstLettersOfTheFirstTwoWordsOfTheHejmoName(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Oma Gerda":                      "OG",
		"Kita Sonnenschein":              "KS",
		"Kinderarzt":                     "K",
		"Anna (Mama von Lukas)":          "AM",
		"Ärztlicher Bereitschaftsdienst": "ÄB",
		"  frau   müller ":               "FM",
		"Dr. Müller":                     "DM",
		"- 112 Notruf":                   "N",
		"(Notruf) 112 Giftnotruf":        "NG",
		"öffentlich":                     "Ö",
		"Charlotte-Sophie":               "C",
		"":                               "",
		"112":                            "",
	} {
		if got := contact.Initials(in); got != want {
			t.Errorf("Initials(%q) = %q, want %q", in, got, want)
		}
	}
}

// Ticket 297, acceptance: the photo is ready for the phone in vCard 3.0
// (base64) and 4.0 (data URI) form.
func TestPhotoIsReadyForVCard3AsBase64AndVCard4AsDataURI(t *testing.T) {
	t.Parallel()
	jpg := make([]byte, 5000)
	for i := range jpg {
		jpg[i] = byte(i * 7)
	}
	jpg[0], jpg[1] = 0xff, 0xd8
	unfold := func(p string) string { return strings.ReplaceAll(p, "\r\n ", "") }

	v3 := contact.PhotoVCard3(jpg)
	if !strings.HasPrefix(v3, "PHOTO;ENCODING=b;TYPE=JPEG:") {
		t.Errorf("3.0 = %.60q", v3)
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(unfold(v3), "PHOTO;ENCODING=b;TYPE=JPEG:"))
	if err != nil || !bytes.Equal(got, jpg) {
		t.Errorf("3.0 does not carry the photo back: %v", err)
	}
	v4 := contact.PhotoVCard4(jpg)
	if !strings.HasPrefix(v4, "PHOTO:data:image/jpeg;base64,") {
		t.Errorf("4.0 = %.60q", v4)
	}
	got, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(unfold(v4), "PHOTO:data:image/jpeg;base64,"))
	if err != nil || !bytes.Equal(got, jpg) {
		t.Errorf("4.0 does not carry the photo back: %v", err)
	}
	for _, v := range []string{v3, v4} {
		lines := strings.Split(v, "\r\n")
		if len(lines) < 10 {
			t.Fatalf("a 5 KB photo is folded into %d lines", len(lines))
		}
		for i, l := range lines {
			if len(l) > 75 {
				t.Errorf("line %d is %d octets, the limit is 75", i, len(l))
			}
			if i > 0 && !strings.HasPrefix(l, " ") {
				t.Errorf("line %d does not start with the folding space", i)
			}
		}
		if strings.Contains(v, "\n") && !strings.Contains(v, "\r\n") || strings.HasSuffix(v, "\r\n") {
			t.Error("lines are separated by CRLF and the property has no line end of its own")
		}
	}
	if contact.PhotoVCard3(nil) != "" || contact.PhotoVCard4(nil) != "" {
		t.Error("no photo, no PHOTO line")
	}
}
