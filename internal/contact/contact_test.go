package contact_test

import (
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
	for in, want := range map[string]string{"0171 3456 789": "tel:01713456789", "+49 (0)89 1234-56": "tel:+4908912345" + "6", "089/12.34": "tel:0891234"} {
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
	if !contact.AddressEmpty("", " ", "", "\t") || contact.AddressEmpty("", "", "Rosenheim", "") {
		t.Error("AddressEmpty")
	}
}
