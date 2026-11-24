package contact_test

import (
	"reflect"
	"testing"

	"github.com/hejmo-home/hejmo/internal/contact"
)

// Ticket 298: the sentence of a relation and what the card of a person says about it.

func TestJoinNamesWritesEveryNameOut(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		names []string
		and   string
		want  string
	}{
		{[]string{"Mia"}, "und", "Mia"},
		{[]string{"Mia", "Ben"}, "und", "Mia und Ben"},
		{[]string{"Sebastian", "Mia", "Charlotte-Sophie", "Jonas", "Lotta", "Ben"}, "und", "Sebastian, Mia, Charlotte-Sophie, Jonas, Lotta und Ben"},
		{[]string{"Mia", "Ben", "Lotta"}, "and", "Mia, Ben and Lotta"},
		{nil, "und", ""},
	} {
		if got := contact.JoinNames(c.names, c.and); got != c.want {
			t.Errorf("JoinNames(%v, %q) = %q, want %q", c.names, c.and, got, c.want)
		}
	}
}

func TestRelatedLinesOfAVCard4PointToTheContactWithTheWordAsLabel(t *testing.T) {
	t.Parallel()
	got := contact.RelatedLines(contact.VCard4, []contact.Related{
		{ContactID: "1b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f01", ContactName: "Oma Gerda", Word: "Oma"},
		{ContactID: "2b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f02", ContactName: "Lea", Word: "Beste Freundin"},
		{ContactID: "3b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f03", ContactName: "Frau W.", Word: "Nachbarin"},
		{ContactID: "4b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f04", ContactName: "Dr. Y", Word: "Kinderärztin, privat; gut"},
	}, 1)
	want := []string{
		"RELATED;VALUE=uri;TYPE=kin;LABEL=Oma:urn:uuid:1b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f01",
		"RELATED;VALUE=uri;LABEL=\"Beste Freundin\":urn:uuid:2b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f02",
		"RELATED;VALUE=uri;TYPE=neighbor;LABEL=Nachbarin:urn:uuid:3b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f03",
		"RELATED;VALUE=uri;LABEL=\"Kinderärztin, privat; gut\":urn:uuid:4b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f04",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lines =\n%q\nwant\n%q", got, want)
	}
}

func TestRelatedLinesOfAVCard3UseXABRelatedNamesAndXABLabelWithItemGroups(t *testing.T) {
	t.Parallel()
	got := contact.RelatedLines(contact.VCard3, []contact.Related{
		{ContactID: "1b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f01", ContactName: "Oma Gerda", Word: "Oma"},
		{ContactID: "2b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f02", ContactName: "Müller, Gerda; Haus", Word: "Freund"},
	}, 3)
	want := []string{
		"item3.X-ABRELATEDNAMES:Oma Gerda", "item3.X-ABLabel:Oma",
		"item4.X-ABRELATEDNAMES:Müller\\, Gerda\\; Haus", "item4.X-ABLabel:Freund",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lines =\n%q\nwant\n%q", got, want)
	}
}

func TestRelatedTypeMapsOnlyTheWordsAvCardKnows(t *testing.T) {
	t.Parallel()
	for word, want := range map[string]string{
		"Freund": "friend", "Freundin": "friend", "freundin": "friend",
		"Oma": "kin", "Opa": "kin", "Tante": "kin", "Onkel": "kin", "Mutter": "kin", "Vater": "kin", "Schwester": "kin", "Bruder": "kin",
		"Nachbar": "neighbor", "Nachbarin": "neighbor",
		"Lehrerin": "", "Patentante": "", "Beste Freundin": "", "": "",
	} {
		if got := contact.RelatedType(word); got != want {
			t.Errorf("RelatedType(%q) = %q, want %q", word, got, want)
		}
	}
}

func TestARelatedLabelWithADoubleQuoteIsEncodedPerRFC6868(t *testing.T) {
	t.Parallel()
	got := contact.RelatedLines(contact.VCard4, []contact.Related{{ContactID: "1b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f01", Word: `Tante "Mimi" ^_^`}}, 1)
	want := []string{`RELATED;VALUE=uri;LABEL="Tante ^'Mimi^' ^^_^^":urn:uuid:1b4e28ba-2fa1-4d3b-a2c0-0d6e5c1a7f01`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}
