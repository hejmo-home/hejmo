package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"strings"
	"testing"
)

// A GeoNames country line has 19 tab-separated columns:
// id, name, ascii, alternatenames, lat, lon, class, code, country, cc2,
// admin1..admin4, population, elevation, dem, timezone, modified.
func gn(id, name, ascii, alt, lat, lon, code, cc, a1, a2, a3, a4, pop string) string {
	return strings.Join([]string{id, name, ascii, alt, lat, lon, "P", code, cc, "", a1, a2, a3, a4, pop, "", "", "Europe/Berlin", "2020-01-01"}, "\t")
}

// An alternate name line: id, geonameid, language, name, preferred, short, colloquial, historic, from, to.
func alt(geonameid, lang, name, flags string) string {
	f := strings.Split(strings.ReplaceAll(flags+"    ", " ", "-"), "")
	for i := range f {
		if f[i] == "-" {
			f[i] = ""
		}
	}
	return strings.Join([]string{"1", geonameid, lang, name, f[0], f[1], f[2], f[3], "", ""}, "\t")
}

func fixture() map[string]string {
	de := []string{
		gn("1", "Munich", "Munich", "Muenchen,München,Munich", "48.13743", "11.57549", "PPLA", "DE", "02", "091", "09162", "09162000", "1505005"),
		gn("2", "Pasing", "Pasing", "", "48.14146", "11.45599", "PPLX", "DE", "02", "091", "09162", "09162000", "35728"),
		gn("3", "Obermenzing", "Obermenzing", "", "48.17", "11.47", "PPLX", "DE", "02", "091", "09162", "09162000", "0"),
		gn("4", "Schmedeswurtherwesterdeich", "Schmedeswurtherwesterdeich", "", "53.92036", "9.01969", "PPLF", "DE", "10", "00", "01051", "01051103", "0"),
		gn("5", "Hamlet", "Hamlet", "", "50", "10", "PPL", "DE", "02", "091", "09999", "09999000", "499"),
		gn("6", "Old Village", "Old Village", "", "50", "10", "PPLH", "DE", "02", "091", "09999", "09999001", "9000"),
		gn("7", "Gone", "Gone", "", "50", "10", "PPLQ", "DE", "02", "091", "09999", "09999002", "9000"),
		gn("8", "Destroyed", "Destroyed", "", "50", "10", "PPLW", "DE", "02", "091", "09999", "09999003", "9000"),
		gn("9", "Lonely District", "Lonely District", "", "50", "10", "PPLX", "DE", "02", "00", "", "", "0"),
		gn("10", "Smaller Neighbour", "Smaller Neighbour", "", "48.2", "11.4", "PPL", "DE", "02", "091", "09162", "09162000", "800"),
		strings.Replace(gn("11", "Stream", "Stream", "", "50", "10", "PPL", "DE", "02", "091", "09162", "09162000", "9000"), "\tP\t", "\tH\t", 1),
	}
	at := []string{
		gn("20", "Vienna", "Vienna", "Wien", "48.20849", "16.37208", "PPLC", "AT", "09", "900", "", "", "1691468"),
		gn("21", "Favoriten", "Favoriten", "", "48.17", "16.38", "PPLX", "AT", "09", "900", "", "", "0"),
	}
	ch := []string{
		gn("30", "Zürich", "Zuerich", "Zurich", "47.36667", "8.55", "PPLA", "CH", "ZH", "112", "261", "", "415367"),
	}
	alts := map[string][]string{
		"DE": {alt("1", "de", "Muenchen", ""), alt("1", "de", "München", "1"), alt("1", "en", "Munich", ""), alt("1", "fr", "Munich", ""), alt("1", "it", "Monaco di Baviera", ""),
			alt("1", "es", "Múnich", ""), alt("1", "de", "Monachia", "   1"), alt("1", "bar", "Minga", ""), alt("2", "de", "Pasing", "1")},
		"AT": {alt("20", "de", "Wien", "1"), alt("20", "en", "Vienna", "")},
		"CH": {},
	}
	files := map[string]string{
		"DE.txt": strings.Join(de, "\n") + "\n", "AT.txt": strings.Join(at, "\n") + "\n", "CH.txt": strings.Join(ch, "\n") + "\n",
		"admin1CodesASCII.txt": admin1File(),
		"admin2Codes.txt":      "DE.02.091\tUpper Bavaria\tUpper Bavaria\t1\nCH.ZH.112\tBezirk Zürich\tBezirk Zurich\t2\nAT.09.900\tVienna\tVienna\t3\n",
	}
	for cc, l := range alts {
		files["alternatenames/"+cc+".txt"] = strings.Join(l, "\n") + "\n"
	}
	return files
}

// admin1File lists every code of the region table, as GeoNames does.
func admin1File() string {
	var b strings.Builder
	for k := range admin1 {
		b.WriteString(k + "\tName\tName\t1\n")
	}
	return b.String()
}

func open(files map[string]string) opener {
	return func(name string) (io.ReadCloser, error) {
		s, ok := files[name]
		if !ok {
			return nil, os.ErrNotExist
		}
		return io.NopCloser(strings.NewReader(s)), nil
	}
}

func buildFixture(t *testing.T) []record {
	t.Helper()
	recs, err := build(open(fixture()))
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func byID(recs []record) map[int]record {
	m := map[int]record{}
	for _, r := range recs {
		m[r.ID] = r
	}
	return m
}

func TestFilterKeepsPopulatedPlacesEveryDistrictAndFarmVillages(t *testing.T) {
	got := byID(buildFixture(t))
	for _, id := range []int{1, 2, 3, 4, 9, 10, 20, 21, 30} {
		if _, ok := got[id]; !ok {
			t.Errorf("place %d dropped", id)
		}
	}
	for id, why := range map[int]string{5: "under 500 inhabitants", 6: "PPLH historical", 7: "PPLQ abandoned", 8: "PPLW destroyed", 11: "not feature class P"} {
		if _, ok := got[id]; ok {
			t.Errorf("place %d kept (%s)", id, why)
		}
	}
}

func TestRecordCarriesGermanNameRegionAndRoundedCoordinates(t *testing.T) {
	got := byID(buildFixture(t))
	m := got[1]
	if m.Name != "München" || m.Region != "DE-BY" || m.Country != "DE" || m.Admin2 != "Upper Bavaria" {
		t.Errorf("München = %+v", m)
	}
	if m.Lat != "48.1374" || m.Lon != "11.5755" || m.Population != 1505005 {
		t.Errorf("München coordinates/population = %q %q %d", m.Lat, m.Lon, m.Population)
	}
	if got[20].Name != "Wien" || got[20].Region != "AT-9" || got[30].Region != "CH-ZH" || got[4].Region != "DE-SH" {
		t.Errorf("regions: %+v %+v %+v", got[20], got[30], got[4])
	}
	if got[4].Admin2 != "" {
		t.Errorf("admin2 code 00 must give no name: %q", got[4].Admin2)
	}
}

func TestAlternateNamesAreDeEnFrItThatDifferFromTheName(t *testing.T) {
	m := byID(buildFixture(t))[1]
	want := "Minga"
	if strings.Contains(strings.Join(m.Alt, "|"), want) {
		t.Errorf("Bavarian (bar) spelling kept: %v", m.Alt)
	}
	for _, w := range []string{"Munich", "Monaco di Baviera"} {
		if !contains(m.Alt, w) {
			t.Errorf("alternate %q missing from %v", w, m.Alt)
		}
	}
	if m.ASCII != "Muenchen" {
		t.Errorf("ascii name = %q, want Muenchen", m.ASCII)
	}
	for _, bad := range []string{"München", "Muenchen", "Múnich", "Monachia"} {
		if contains(m.Alt, bad) {
			t.Errorf("%q must not be an alternate: %v (same as the name, other language, or historic)", bad, m.Alt)
		}
	}
	if len(byID(buildFixture(t))[2].Alt) != 0 {
		t.Errorf("Pasing has no alternates: %v", byID(buildFixture(t))[2].Alt)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestDistrictParentIsTheLargestOtherPlaceWithTheSameAdminCodes(t *testing.T) {
	got := byID(buildFixture(t))
	if got[2].Parent != 1 || got[3].Parent != 1 {
		t.Errorf("Pasing/Obermenzing parents = %d, %d, want München (1) and not the smaller neighbour (10)", got[2].Parent, got[3].Parent)
	}
	if got[21].Parent != 20 {
		t.Errorf("Favoriten parent = %d, want Wien (20)", got[21].Parent)
	}
	if got[9].Parent != 0 {
		t.Errorf("a district without admin2-4 codes has no parent: %d", got[9].Parent)
	}
	if got[1].Parent != 0 || got[10].Parent != 0 {
		t.Errorf("only PPLX have a parent")
	}
}

func TestAnUnknownAdmin1CodeFailsTheBuild(t *testing.T) {
	f := fixture()
	f["DE.txt"] += gn("40", "Nowhere", "Nowhere", "", "50", "10", "PPL", "DE", "99", "", "", "", "9000") + "\n"
	if _, err := build(open(f)); err == nil || !strings.Contains(err.Error(), "DE.99") {
		t.Fatalf("err = %v, want one naming DE.99", err)
	}
}

func TestWrittenFileHasHeaderAndRoundTripsThroughTSV(t *testing.T) {
	var buf bytes.Buffer
	if err := write(&buf, "2026-11-15", buildFixture(t)); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if !strings.HasPrefix(lines[0], "# ") || !strings.Contains(lines[0], "2026-11-15") || !strings.Contains(lines[0], "PPLX") || !strings.Contains(lines[0], "population >= 500") {
		t.Errorf("header = %q", lines[0])
	}
	if len(lines) != 1+9 {
		t.Errorf("lines = %d, want header and 9 places", len(lines))
	}
	if cols := strings.Count(lines[1], "\t") + 1; cols != 11 {
		t.Errorf("columns = %d in %q", cols, lines[1])
	}
}

func TestLicenceNamesTheExtractDateAndTheFilter(t *testing.T) {
	l := licence("2026-11-15")
	for _, want := range []string{
		"Contains data from GeoNames (https://www.geonames.org), licensed CC BY 4.0, extract of 2026-11-15, filtered to DE/AT/CH and reformatted.",
		"creativecommons.org/licenses/by/4.0",
		filterText,
	} {
		if !strings.Contains(l, want) {
			t.Errorf("licence misses %q:\n%s", want, l)
		}
	}
}
