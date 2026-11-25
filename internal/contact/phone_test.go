package contact

import "testing"

// Ticket 300 (ADR 0026): numbers are compared in E.164 with the home country as region; numbers that cannot
// be parsed are compared by their digits; empty values never match.

func TestE164ReadsANumberWithTheHomeCountryAsRegion(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, region, want string }{
		{"+49 171 3456789", "DE", "+491713456789"},
		{"0171 3456 789", "DE", "+491713456789"},
		{"0049-171-3456789", "DE", "+491713456789"},
		{"0171/3456789", "DE", "+491713456789"},
		{"+49 (0)171 3456789", "DE", "+491713456789"},
		{"0664 1234567", "AT", "+436641234567"},
		{"+43 664 1234567", "DE", "+436641234567"}, // another country, written with its code
		{"116117", "DE", ""},                       // a service number cannot be dialled from abroad
		{"112", "DE", ""},
		{"", "DE", ""},
		{"   ", "DE", ""},
		{"abc", "DE", ""},
	} {
		if got := E164(c.in, c.region); got != c.want {
			t.Errorf("E164(%q, %s) = %q, want %q", c.in, c.region, got, c.want)
		}
	}
}

func TestNumberKeyMatchesTheSameNumberWrittenInManyWaysAndNeverAnEmptyOne(t *testing.T) {
	t.Parallel()
	same := [][]string{
		{"+49 171 3456789", "0171 3456 789", "0049-171-3456789", "(0171) 3456789"},
		{"089 1234 5670", "+49 89 12345670", "089/12345670"},
		{"116117", "116 117", "116-117"},    // unparseable: the digits
		{"0176 23456789", "0176 2345 6789"}, // the same, spaced differently
	}
	for _, group := range same {
		want := NumberKey(group[0], "DE")
		if want == "" {
			t.Fatalf("NumberKey(%q) is empty", group[0])
		}
		for _, v := range group[1:] {
			if got := NumberKey(v, "DE"); got != want {
				t.Errorf("NumberKey(%q) = %q, want %q (as %q)", v, got, want, group[0])
			}
		}
	}
	if NumberKey("0171 3456 789", "DE") == NumberKey("0171 3456 780", "DE") {
		t.Error("different numbers have the same key")
	}
	if NumberKey("116117", "DE") == NumberKey("116118", "DE") {
		t.Error("different service numbers have the same key")
	}
	for _, empty := range []string{"", " ", "-", "()", "n/a"} {
		if k := NumberKey(empty, "DE"); k != "" {
			t.Errorf("NumberKey(%q) = %q, want none: empty values never match", empty, k)
		}
	}
}

func TestEmailKeyIgnoresCaseAndSpacesAndEmptyValuesHaveNone(t *testing.T) {
	t.Parallel()
	if EmailKey(" Gerda.Mueller@Example.DE ") != EmailKey("gerda.mueller@example.de") {
		t.Error("e-mail addresses differ by case")
	}
	if EmailKey("a@example.de") == EmailKey("b@example.de") {
		t.Error("different addresses match")
	}
	for _, empty := range []string{"", "  ", "\t"} {
		if EmailKey(empty) != "" {
			t.Errorf("EmailKey(%q) is not empty", empty)
		}
	}
}
