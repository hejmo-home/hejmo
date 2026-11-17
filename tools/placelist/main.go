// Command placelist builds the list of places that ships inside the box
// (internal/places/data/places.tsv.gz) from the GeoNames dumps for Germany,
// Austria and Switzerland.
//
// It is run by hand (or with `go generate ./internal/places`), never by the box
// at run time. It downloads from download.geonames.org/export/dump (cached in
// -cache, so a second run needs no network):
//
//	DE.zip, AT.zip, CH.zip                the places of each country
//	alternatenames/DE.zip, AT, CH         their names by language (de, en, fr, it)
//	admin1CodesASCII.txt, admin2Codes.txt names of the states and districts
//
// Filter: feature class P (populated places) with at least 500 inhabitants,
// plus every PPLX (a district of a town) whatever its population; PPLH, PPLQ and
// PPLW (historical, abandoned, destroyed) are dropped. One addition to that
// rule: PPLF (a farm village) is kept whatever its population, because GeoNames
// gives such municipalities (Schmedeswurtherwesterdeich) no population. The data is CC BY 4.0
// (see internal/places/data/LICENSE-GeoNames.txt).
package main

import (
	"archive/zip"
	"bufio"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	baseURL       = "https://download.geonames.org/export/dump/"
	minPopulation = 500
)

var countries = []string{"DE", "AT", "CH"}

// filterText says in the file header what was kept.
const filterText = "feature class P with population >= 500, plus every PPLX and PPLF; without PPLH, PPLQ, PPLW"

// dropped are the feature codes of places that are gone or not lived in.
var dropped = map[string]bool{"PPLH": true, "PPLQ": true, "PPLW": true}

// admin1 maps the GeoNames admin1 code of a country to its ISO 3166-2 region
// code: the 51 regions of the setup region select (internal/region). Germany
// and Austria use numbers in GeoNames, Switzerland the canton letters.
var admin1 = map[string]string{
	"DE.01": "DE-BW", "DE.02": "DE-BY", "DE.03": "DE-HB", "DE.04": "DE-HH", "DE.05": "DE-HE", "DE.06": "DE-NI",
	"DE.07": "DE-NW", "DE.08": "DE-RP", "DE.09": "DE-SL", "DE.10": "DE-SH", "DE.11": "DE-BB", "DE.12": "DE-MV",
	"DE.13": "DE-SN", "DE.14": "DE-ST", "DE.15": "DE-TH", "DE.16": "DE-BE",
	"AT.01": "AT-1", "AT.02": "AT-2", "AT.03": "AT-3", "AT.04": "AT-4", "AT.05": "AT-5", "AT.06": "AT-6",
	"AT.07": "AT-7", "AT.08": "AT-8", "AT.09": "AT-9",
	"CH.AG": "CH-AG", "CH.AI": "CH-AI", "CH.AR": "CH-AR", "CH.BE": "CH-BE", "CH.BL": "CH-BL", "CH.BS": "CH-BS",
	"CH.FR": "CH-FR", "CH.GE": "CH-GE", "CH.GL": "CH-GL", "CH.GR": "CH-GR", "CH.JU": "CH-JU", "CH.LU": "CH-LU",
	"CH.NE": "CH-NE", "CH.NW": "CH-NW", "CH.OW": "CH-OW", "CH.SG": "CH-SG", "CH.SH": "CH-SH", "CH.SO": "CH-SO",
	"CH.SZ": "CH-SZ", "CH.TG": "CH-TG", "CH.TI": "CH-TI", "CH.UR": "CH-UR", "CH.VD": "CH-VD", "CH.VS": "CH-VS",
	"CH.ZG": "CH-ZG", "CH.ZH": "CH-ZH",
}

// record is one place of the list.
type record struct {
	ID         int
	Name       string
	ASCII      string
	Alt        []string // de, en, fr, it spellings that differ from Name, for search only
	Lat, Lon   string   // rounded to 4 decimals
	Population int
	Country    string
	Region     string // ISO 3166-2
	Admin2     string // name of the admin2 area ("" if none)
	Parent     int    // PPLX only: id of the town it belongs to, 0 if none
}

// raw is a GeoNames line of the country file that passed the filter.
type raw struct {
	record
	code                 string // feature code
	a1, a2, a3, a4       string
	geoName, geoASCII    string
	geoAlternateNamesRaw string
}

// opener opens a source file by its name relative to the cache directory:
// "DE.txt", "alternatenames/DE.txt", "admin1CodesASCII.txt", "admin2Codes.txt".
type opener func(name string) (io.ReadCloser, error)

func main() {
	out := flag.String("out", "internal/places/data/places.tsv.gz", "file to write")
	cache := flag.String("cache", filepath.Join(os.TempDir(), "hejmo-geonames"), "directory for the downloaded dumps")
	flag.Parse()
	if err := run(*out, *cache); err != nil {
		fmt.Fprintln(os.Stderr, "placelist:", err)
		os.Exit(1)
	}
}

func run(out, cache string) error {
	date, err := download(cache)
	if err != nil {
		return err
	}
	recs, err := build(zipOpener(cache))
	if err != nil {
		return err
	}
	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := write(f, date, recs); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(out), "LICENSE-GeoNames.txt"), []byte(licence(date)), 0o644); err != nil {
		return err
	}
	st, _ := os.Stat(out)
	fmt.Printf("placelist: %d places, %d bytes gzipped, GeoNames dump of %s\n", len(recs), st.Size(), date)
	return nil
}

// downloads are the files fetched from GeoNames, relative to baseURL.
func downloads() []string {
	l := []string{"admin1CodesASCII.txt", "admin2Codes.txt"}
	for _, c := range countries {
		l = append(l, c+".zip", "alternatenames/"+c+".zip")
	}
	return l
}

// download fetches what is not cached yet (the cache keeps the server's
// modification time of each file) and returns the date of the country dumps:
// the newest modification time of DE.zip, AT.zip and CH.zip.
func download(cache string) (string, error) {
	var newest time.Time
	for _, name := range downloads() {
		dest := filepath.Join(cache, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if _, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
			fmt.Println("placelist: downloading", name)
			if err := fetch(baseURL+name, dest); err != nil {
				return "", err
			}
		}
		st, err := os.Stat(dest)
		if err != nil {
			return "", err
		}
		if slices.Contains(countries, strings.TrimSuffix(name, ".zip")) && st.ModTime().After(newest) {
			newest = st.ModTime()
		}
	}
	return newest.UTC().Format("2006-01-02"), nil
}

func fetch(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		return os.Chtimes(dest, t, t)
	}
	return nil
}

// licence is the attribution that CC BY 4.0 asks for, next to the data.
func licence(date string) string {
	return "Contains data from GeoNames (https://www.geonames.org), licensed CC BY 4.0, extract of " + date + ", filtered to DE/AT/CH and reformatted.\n\n" +
		"places.tsv.gz is an extract of the GeoNames dumps for Germany, Austria and Switzerland\n" +
		"(https://download.geonames.org/export/dump/), made by tools/placelist: " + filterText + ".\n" +
		"Changes: filtered, reduced to the columns of the file, coordinates rounded to four decimals,\n" +
		"names by language taken from the alternate names, parent town of districts added, regions\n" +
		"mapped to ISO 3166-2.\n\n" +
		"GeoNames is licensed under the Creative Commons Attribution 4.0 licence:\n" +
		"https://creativecommons.org/licenses/by/4.0/legalcode\n"
}

// zipOpener reads the files of the cache: a ".txt" name that has a zip next to
// it ("DE.txt" in "DE.zip") is read from inside the zip.
func zipOpener(cache string) opener {
	return func(name string) (io.ReadCloser, error) {
		path := filepath.Join(cache, filepath.FromSlash(name))
		if !strings.HasSuffix(name, ".txt") || strings.HasPrefix(filepath.Base(name), "admin") {
			return os.Open(path)
		}
		zr, err := zip.OpenReader(strings.TrimSuffix(path, ".txt") + ".zip")
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name == filepath.Base(name) {
				rc, err := f.Open()
				if err != nil {
					zr.Close()
					return nil, err
				}
				return &zipFile{rc, zr}, nil
			}
		}
		zr.Close()
		return nil, fmt.Errorf("%s is not in its zip", name)
	}
}

type zipFile struct {
	io.ReadCloser
	zr *zip.ReadCloser
}

func (z *zipFile) Close() error { z.ReadCloser.Close(); return z.zr.Close() }

// lines calls fn with the tab-separated columns of every line of a file.
func lines(open opener, name string, fn func(cols []string) error) error {
	rc, err := open(name)
	if err != nil {
		return err
	}
	defer rc.Close()
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if sc.Text() == "" {
			continue
		}
		if err := fn(strings.Split(sc.Text(), "\t")); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return sc.Err()
}

// build reads the sources and returns the kept places, sorted by id.
func build(open opener) ([]record, error) {
	admin2 := map[string]string{} // "DE.02.091" -> name
	if err := lines(open, "admin2Codes.txt", func(c []string) error {
		if len(c) >= 2 {
			admin2[c[0]] = c[1]
		}
		return nil
	}); err != nil {
		return nil, err
	}
	admin1Names := map[string]bool{}
	if err := lines(open, "admin1CodesASCII.txt", func(c []string) error {
		admin1Names[c[0]] = true
		return nil
	}); err != nil {
		return nil, err
	}
	for k := range admin1 {
		if !admin1Names[k] {
			return nil, fmt.Errorf("admin1 code %s of the region table is not in admin1CodesASCII.txt", k)
		}
	}

	var all []*raw
	for _, cc := range countries {
		kept, err := readCountry(open, cc, admin2)
		if err != nil {
			return nil, err
		}
		if err := addNames(open, cc, kept); err != nil {
			return nil, err
		}
		all = append(all, kept...)
	}
	setParents(all)
	recs := make([]record, 0, len(all))
	for _, r := range all {
		recs = append(recs, r.record)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })
	return recs, nil
}

// readCountry applies the filter to one country file.
func readCountry(open opener, cc string, admin2 map[string]string) ([]*raw, error) {
	var kept []*raw
	err := lines(open, cc+".txt", func(c []string) error {
		if len(c) < 15 || c[6] != "P" || dropped[c[7]] {
			return nil
		}
		pop, _ := strconv.Atoi(c[14])
		if c[7] != "PPLX" && c[7] != "PPLF" && pop < minPopulation {
			return nil
		}
		id, err := strconv.Atoi(c[0])
		if err != nil {
			return fmt.Errorf("geonameid %q: %w", c[0], err)
		}
		lat, err1 := strconv.ParseFloat(c[4], 64)
		lon, err2 := strconv.ParseFloat(c[5], 64)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("place %d: coordinates %q %q", id, c[4], c[5])
		}
		if c[8] != cc {
			return nil // a place of a neighbouring country listed in this file
		}
		region, ok := admin1[cc+"."+c[10]]
		if !ok {
			return fmt.Errorf("place %d (%s): admin1 code %s.%s is not in the region table", id, c[1], cc, c[10])
		}
		r := &raw{code: c[7], a1: c[10], a2: c[11], a3: c[12], a4: c[13], geoName: c[1], geoASCII: c[2], geoAlternateNamesRaw: c[3]}
		r.ID, r.Name, r.ASCII = id, clean(c[1]), clean(c[2])
		r.Lat, r.Lon = round4(lat), round4(lon)
		r.Population, r.Country, r.Region = pop, cc, region
		if c[11] != "" && c[11] != "00" {
			r.Admin2 = clean(admin2[cc+"."+c[10]+"."+c[11]])
		}
		kept = append(kept, r)
		return nil
	})
	return kept, err
}

// addNames sets the display name (the preferred German name, else GeoNames'
// own) and the alternate spellings of the kept places from the language file.
func addNames(open opener, cc string, kept []*raw) error {
	byID := make(map[int]*raw, len(kept))
	for _, r := range kept {
		byID[r.ID] = r
	}
	german := map[int]string{}
	alts := map[int][]string{}
	err := lines(open, "alternatenames/"+cc+".txt", func(c []string) error {
		if len(c) < 4 {
			return nil
		}
		id, err := strconv.Atoi(c[1])
		if err != nil || byID[id] == nil {
			return nil
		}
		lang, name := c[2], clean(c[3])
		flag := func(i int) bool { return len(c) > i && c[i] == "1" }
		if name == "" || flag(6) || flag(7) { // colloquial and historic spellings are not offered
			return nil
		}
		if lang != "de" && lang != "en" && lang != "fr" && lang != "it" {
			return nil
		}
		if lang == "de" && flag(4) {
			german[id] = name
		}
		alts[id] = append(alts[id], name)
		return nil
	})
	if err != nil {
		return err
	}
	for _, r := range kept {
		if g, ok := german[r.ID]; ok && g != r.Name {
			r.Name = g
			r.ASCII = asciiOf(g)
		}
		r.Alt = alternates(r, append(alts[r.ID], r.geoName, r.geoASCII))
	}
	return nil
}

// alternates keeps the distinct spellings that differ from the name and its
// ASCII form, in a stable order.
func alternates(r *raw, names []string) []string {
	seen := map[string]bool{strings.ToLower(r.Name): true, strings.ToLower(r.ASCII): true}
	var out []string
	for _, n := range names {
		n = clean(n)
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// asciiOf writes a German name the way GeoNames writes its ASCII names.
func asciiOf(s string) string {
	return strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "Ä", "Ae", "Ö", "Oe", "Ü", "Ue", "ß", "ss",
		"é", "e", "è", "e", "ê", "e", "à", "a", "â", "a", "ç", "c", "î", "i", "ô", "o", "û", "u", "ì", "i", "ò", "o", "ù", "u").Replace(s)
}

// setParents gives every PPLX the largest other place of the same country and
// admin1-4 codes that is not a PPLX itself (Pasing -> München). A district
// whose codes below the state are empty has none: every town of the state would
// match.
func setParents(all []*raw) {
	type key struct{ cc, a1, a2, a3, a4 string }
	best := map[key]*raw{}
	for _, r := range all {
		if r.code == "PPLX" {
			continue
		}
		k := key{r.Country, r.a1, r.a2, r.a3, r.a4}
		if b := best[k]; b == nil || r.Population > b.Population || (r.Population == b.Population && r.ID < b.ID) {
			best[k] = r
		}
	}
	for _, r := range all {
		if r.code != "PPLX" || (r.a3 == "" && r.a4 == "" && (r.a2 == "" || r.a2 == "00")) {
			continue
		}
		if p := best[key{r.Country, r.a1, r.a2, r.a3, r.a4}]; p != nil {
			r.Parent = p.ID
		}
	}
}

// clean removes what would break the tab-separated file.
func clean(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\t", " ", "|", " ").Replace(s)), " ")
}

func round4(v float64) string {
	return strconv.FormatFloat(math.Round(v*1e4)/1e4, 'f', -1, 64)
}

// columns are the fields of a line of places.tsv.gz.
const columns = "id name ascii alternates(|) lat lon population country region admin2 parent"

// write writes the gzipped TSV: one header line (a comment that gives the
// GeoNames dump date and the filter), then one line per place.
func write(w io.Writer, date string, recs []record) error {
	zw, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(zw)
	fmt.Fprintf(bw, "# GeoNames extract of %s, DE AT CH, %s; columns: %s\n", date, filterText, columns)
	for _, r := range recs {
		parent := ""
		if r.Parent != 0 {
			parent = strconv.Itoa(r.Parent)
		}
		fmt.Fprintf(bw, "%d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n", r.ID, r.Name, r.ASCII, strings.Join(r.Alt, "|"), r.Lat, r.Lon, r.Population, r.Country, r.Region, r.Admin2, parent)
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}
