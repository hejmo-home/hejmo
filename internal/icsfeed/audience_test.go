package icsfeed_test

import (
	"testing"

	"github.com/hejmo-home/hejmo/internal/icsfeed"
)

func TestImportReadsTheAudienceAndStripsTheReadableLine(t *testing.T) {
	data := feed(vevent("UID:a", "SUMMARY:Swimming", "DTSTART:20261120T150000Z", "DTEND:20261120T160000Z",
		"DESCRIPTION:Bring towels\\n\\nFür: Mia\\, Sebastian", "X-HEJMO-AUDIENCE:person:p1,group:g1"))
	res, err := icsfeed.Parse(data, icsfeed.Options{Zone: berlin, Now: now, AudienceLines: []string{"For: ", "Für: "}})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	e := res.Items[0].Event
	if e.Description != "Bring towels" || len(e.People) != 1 || e.People[0] != "p1" || len(e.Groups) != 1 || e.Groups[0] != "g1" {
		t.Fatalf("event = %+v", e)
	}
}

func TestImportKeepsALastLineThatIsNotTheAudienceLine(t *testing.T) {
	data := feed(vevent("UID:a", "SUMMARY:Swimming", "DTSTART:20261120T150000Z", "DTEND:20261120T160000Z",
		"DESCRIPTION:Bring towels", "X-HEJMO-AUDIENCE:person:p1"))
	res, _ := icsfeed.Parse(data, icsfeed.Options{Zone: berlin, Now: now, AudienceLines: []string{"For: "}})
	if got := res.Items[0].Event.Description; got != "Bring towels" {
		t.Fatalf("description = %q", got)
	}
}

func TestImportOfADescriptionWithOnlyTheAudienceLineIsEmpty(t *testing.T) {
	data := feed(vevent("UID:a", "SUMMARY:Swimming", "DTSTART:20261120T150000Z", "DTEND:20261120T160000Z",
		"DESCRIPTION:For: Mia", "X-HEJMO-AUDIENCE:person:p1"))
	res, _ := icsfeed.Parse(data, icsfeed.Options{Zone: berlin, Now: now, AudienceLines: []string{"For: "}})
	if got := res.Items[0].Event.Description; got != "" {
		t.Fatalf("description = %q", got)
	}
}
