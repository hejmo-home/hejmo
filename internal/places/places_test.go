package places_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/places"
	"github.com/hejmo-home/hejmo/internal/region"
)

// tsv builds a small list in the format of places.tsv.gz (without gzip):
// id, name, ascii, alternates, lat, lon, population, country, region, admin2, parent.
func tsv(lines ...string) string {
	return "# GeoNames extract of 2026-11-15, test\n" + strings.Join(lines, "\n") + "\n"
}

func list(t *testing.T, lines ...string) *places.List {
	t.Helper()
	l, err := places.Parse(strings.NewReader(tsv(lines...)))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func names(ps []places.Place) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

const (
	muenchen = "1\tMünchen\tMuenchen\tMunich|Monaco di Baviera\t48.1374\t11.5755\t1505005\tDE\tDE-BY\tUpper Bavaria\t"
	pasing   = "2\tPasing\tPasing\t\t48.1415\t11.456\t35728\tDE\tDE-BY\tUpper Bavaria\t1"
	wien     = "3\tWien\tWien\tVienna\t48.2085\t16.3721\t1691468\tAT\tAT-9\tWien Stadt\t"
	strasse  = "4\tStraßberg\tStrassberg\t\t48.0\t9.0\t900\tDE\tDE-BW\tZollernalbkreis\t"
	zuerich  = "5\tZürich\tZuerich\tZurich|Zurigo\t47.3667\t8.55\t415367\tCH\tCH-ZH\tBezirk Zürich\t"
)

func TestSearchIsCaseAndDiacriticInsensitive(t *testing.T) {
	l := list(t, muenchen, pasing, wien, strasse, zuerich)
	for q, want := range map[string]string{
		"münchen": "München", "MUENCHEN": "München", "Munchen": "München", "muen": "München", // ü = ue, and ü = u
		"Munich": "München", "monaco": "München", // alternate names
		"zürich": "Zürich", "zuerich": "Zürich", "zurich": "Zürich", "zurigo": "Zürich",
		"strassb": "Straßberg", "straßb": "Straßberg", "STRASSBERG": "Straßberg",
		"vienna": "Wien", "WIEN": "Wien",
	} {
		got, _ := l.Search(q, "en")
		if len(got) == 0 || got[0].Name != want {
			t.Errorf("Search(%q) = %v, want %q first", q, names(got), want)
		}
	}
}

func TestSearchStartsAtTwoLetters(t *testing.T) {
	l := list(t, muenchen, pasing)
	if got, more := l.Search("m", "en"); len(got) != 0 || more {
		t.Errorf("one letter found %v", names(got))
	}
	if got, _ := l.Search(" m ", "en"); len(got) != 0 {
		t.Errorf("one letter with spaces found %v", names(got))
	}
	if got, _ := l.Search("mu", "en"); len(got) != 1 {
		t.Errorf("two letters found %v", names(got))
	}
	if got, _ := l.Search("", "en"); len(got) != 0 {
		t.Errorf("nothing typed found %v", names(got))
	}
}

func TestPrefixMatchesComeBeforeWordStartMatchesThenLargerPlaces(t *testing.T) {
	l := list(t,
		"10\tBad Neustadt\tBad Neustadt\t\t50\t10\t15000\tDE\tDE-BY\t\t",                     // word start, large
		"11\tNeustadt am Rübenberge\tNeustadt am Ruebenberge\t\t52\t9\t44000\tDE\tDE-NI\t\t", // prefix, larger
		"12\tNeustadt bei Coburg\tNeustadt bei Coburg\t\t50\t11\t15500\tDE\tDE-BY\t\t",       // prefix, smaller
		"13\tTitisee-Neustadt\tTitisee-Neustadt\t\t47\t8\t11000\tDE\tDE-BW\t\t",              // word start after a hyphen
		"14\tNeustadt\tNeustadt\t\t53\t12\t0\tDE\tDE-BB\t\t",                                 // prefix, no population
	)
	got, more := l.Search("neustadt", "en")
	want := []string{"Neustadt am Rübenberge", "Neustadt bei Coburg", "Neustadt", "Bad Neustadt", "Titisee-Neustadt"}
	if more || strings.Join(names(got), "|") != strings.Join(want, "|") {
		t.Errorf("Search = %v (more %v), want %v", names(got), more, want)
	}
}

func TestSearchReturnsAtMostFiveAndSaysThereAreMore(t *testing.T) {
	var lines []string
	for i := 1; i <= 7; i++ {
		lines = append(lines, "2"+string(rune('0'+i))+"\tNeustadt "+string(rune('A'+i))+"\tNeustadt\t\t50\t10\t"+strings.Repeat("1", i)+"00\tDE\tDE-BY\t\t")
	}
	l := list(t, lines...)
	got, more := l.Search("neustadt", "de")
	if len(got) != 5 || !more {
		t.Fatalf("got %d, more %v, want 5 and more", len(got), more)
	}
	if got[0].Name != "Neustadt H" || got[4].Name != "Neustadt D" {
		t.Errorf("the five largest should come first: %v", names(got))
	}
	if got, more := l.Search("neustadt a", "de"); len(got) != 0 || more {
		t.Errorf("no match: %v %v", names(got), more)
	}
	if got, more := list(t, lines[:5]...).Search("neustadt", "de"); len(got) != 5 || more {
		t.Errorf("exactly five is not more: %d %v", len(got), more)
	}
}

func TestResultCarriesIDNameCoordinatesAndRegionCode(t *testing.T) {
	l := list(t, muenchen)
	got, _ := l.Search("münchen", "en")
	p := got[0]
	if p.ID != 1 || p.Name != "München" || p.Lat != 48.1374 || p.Lon != 11.5755 || p.RegionCode != "DE-BY" {
		t.Errorf("place = %+v", p)
	}
	one, ok := l.Get(1, "de")
	if !ok || one.Name != "München" || one.RegionCode != "DE-BY" || one.Lat != 48.1374 {
		t.Errorf("Get = %+v %v", one, ok)
	}
	if _, ok := l.Get(99, "de"); ok {
		t.Error("Get found an unknown id")
	}
	if _, ok := l.Get(0, "de"); ok {
		t.Error("Get found id 0")
	}
}

func TestRegionLabelIsInThePageLanguage(t *testing.T) {
	l := list(t, muenchen, pasing, wien, zuerich)
	cases := []struct{ q, lang, want string }{
		{"münchen", "en", "Bayern, Germany"},
		{"münchen", "de", "Bayern, Deutschland"},
		{"pasing", "en", "district of München, Bayern, Germany"},
		{"pasing", "de", "Stadtteil von München, Bayern, Deutschland"},
		{"zürich", "de", "Schweiz"}, // Zürich is also the canton: a part that repeats the name is left out
		{"wien", "en", "Austria"},
	}
	for _, c := range cases {
		got, _ := l.Search(c.q, c.lang)
		if len(got) != 1 {
			t.Fatalf("%q: %v", c.q, names(got))
		}
		if got[0].Region != c.want {
			t.Errorf("Search(%q, %s).Region = %q, want %q", c.q, c.lang, got[0].Region, c.want)
		}
	}
	if p, _ := l.Get(2, "en"); p.Region != "district of München, Bayern, Germany" {
		t.Errorf("Get region = %q", p.Region)
	}
}

func TestDistrictOfTheSameNameAsItsTownIsNotRepeated(t *testing.T) {
	l := list(t, muenchen, "6\tMünchen\tMuenchen\t\t48.1\t11.5\t0\tDE\tDE-BY\tUpper Bavaria\t1")
	got, _ := l.Search("münchen", "en")
	if len(got) != 2 {
		t.Fatalf("%v", names(got))
	}
	if got[0].Region == got[1].Region {
		t.Errorf("two places named München read the same: %q", got[0].Region)
	}
}

func TestAdmin2IsAddedOnlyWhenTwoResultsShareNameAndRegion(t *testing.T) {
	l := list(t,
		"30\tNeustadt\tNeustadt\t\t49.6\t11.0\t900\tDE\tDE-BY\tMiddle Franconia\t",
		"31\tNeustadt\tNeustadt\t\t48.8\t11.8\t800\tDE\tDE-BY\tLower Bavaria\t",
		"32\tNeustadt am Main\tNeustadt am Main\t\t50\t9.5\t700\tDE\tDE-BY\tLower Franconia\t",
		"33\tNeustadt\tNeustadt\t\t53\t8\t600\tDE\tDE-NI\tWeser-Ems\t",
	)
	got, _ := l.Search("neustadt", "en")
	regions := map[int]string{}
	for _, p := range got {
		regions[p.ID] = p.Region
	}
	want := map[int]string{30: "Middle Franconia, Bayern, Germany", 31: "Lower Bavaria, Bayern, Germany", 32: "Bayern, Germany", 33: "Niedersachsen, Germany"}
	for id, w := range want {
		if regions[id] != w {
			t.Errorf("place %d region = %q, want %q", id, regions[id], w)
		}
	}
}

func TestSearchFindsTheNameInPlainPunctuation(t *testing.T) {
	l := list(t, "40\tGarmisch-Partenkirchen\tGarmisch-Partenkirchen\t\t47.5\t11.1\t26022\tDE\tDE-BY\t\t", "41\tSt. Gallen\tSt. Gallen\tSaint Gallen|Sankt Gallen\t47.4\t9.4\t76000\tCH\tCH-SG\t\t")
	for q, want := range map[string]string{"garmisch partenkirchen": "Garmisch-Partenkirchen", "partenkirchen": "Garmisch-Partenkirchen", "garmisch-p": "Garmisch-Partenkirchen", "st gallen": "St. Gallen", "st. gallen": "St. Gallen", "gallen": "St. Gallen", "sankt": "St. Gallen"} {
		if got, _ := l.Search(q, "en"); len(got) != 1 || got[0].Name != want {
			t.Errorf("Search(%q) = %v, want %q", q, names(got), want)
		}
	}
}

func TestParseRefusesBrokenLines(t *testing.T) {
	for _, bad := range []string{"1\tonly\tthree\n", "x\tA\tA\t\t1\t1\t1\tDE\tDE-BY\t\t\n", "1\tA\tA\t\tnorth\t1\t1\tDE\tDE-BY\t\t\n"} {
		if _, err := places.Parse(strings.NewReader("# header\n" + bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := places.Parse(strings.NewReader("")); err == nil {
		t.Error("accepted an empty list")
	}
}

// The embedded list.

func TestEmbeddedListHasKnownPlaces(t *testing.T) {
	l := places.Embedded()
	for _, c := range []struct{ q, name, region string }{
		{"München", "München", "DE-BY"}, {"Pasing", "Pasing", "DE-BY"}, {"Wien", "Wien", "AT-9"},
		{"Zürich", "Zürich", "CH-ZH"}, {"Schmedeswurtherwesterdeich", "Schmedeswurtherwesterdeich", "DE-SH"},
	} {
		got, _ := l.Search(c.q, "en")
		if len(got) == 0 || got[0].Name != c.name || got[0].RegionCode != c.region {
			t.Errorf("Search(%q) = %+v, want %s in %s first", c.q, got, c.name, c.region)
		}
	}
	got, _ := l.Search("pasing", "en")
	if got[0].Region != "district of München, Bayern, Germany" {
		t.Errorf("Pasing region = %q", got[0].Region)
	}
	if p, ok := l.Get(2867714, "de"); !ok || p.Name != "München" || p.Region != "Bayern, Deutschland" {
		t.Errorf("Get(München) = %+v %v", p, ok)
	}
	if got, _ := l.Search("munich", "en"); len(got) == 0 || got[0].Name != "München" {
		t.Errorf("the English name finds München: %v", names(got))
	}
}

func TestEmbeddedListSaysWhichDumpItIs(t *testing.T) {
	if d := places.Embedded().Date(); !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(d) {
		t.Errorf("dump date = %q", d)
	}
}

func TestEmbeddedListStaysUnderThreeMegabytesGzipped(t *testing.T) {
	if n := places.EmbeddedSize(); n > 3<<20 {
		t.Errorf("places.tsv.gz is %d bytes, the limit is 3 MB (the budget is 2 MB)", n)
	}
}

func TestEveryRegionCodeIsInTheSetupRegionList(t *testing.T) {
	for _, code := range places.Embedded().RegionCodes() {
		if _, ok := region.Lookup(code); !ok {
			t.Errorf("region code %q is not in the setup region list", code)
		}
	}
}

func TestEveryDistrictParentExistsInTheList(t *testing.T) {
	l := places.Embedded()
	if l.Len() < 15000 {
		t.Errorf("only %d places", l.Len())
	}
	if bad := l.OrphanParents(); bad != 0 {
		t.Errorf("%d districts name a parent that is not in the list", bad)
	}
}

// CC BY 4.0: the attribution ships next to the data and names the same dump.
func TestLicenceFileNamesTheDumpOfTheEmbeddedList(t *testing.T) {
	b, err := os.ReadFile("data/LICENSE-GeoNames.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := "Contains data from GeoNames (https://www.geonames.org), licensed CC BY 4.0, extract of " + places.Embedded().Date() + ", filtered to DE/AT/CH and reformatted."
	if !strings.Contains(string(b), want) {
		t.Errorf("LICENSE-GeoNames.txt does not say %q:\n%s", want, b)
	}
}

func TestNoticeFileListsTheGeoNamesExtract(t *testing.T) {
	b, err := os.ReadFile("../../NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	want := "extract of " + places.Embedded().Date() + ", filtered to DE/AT/CH and reformatted."
	if !strings.Contains(string(b), "GeoNames") || !strings.Contains(string(b), "CC BY 4.0") || !strings.Contains(string(b), want) {
		t.Errorf("NOTICE does not credit the GeoNames extract (%q):\n%s", want, b)
	}
}
