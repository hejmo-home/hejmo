// Command foodlist builds the food data that ships inside the box
// (internal/nutrition/data/foods.tsv.gz) from two public databases, so the
// box can calculate the nutrition values of a recipe offline (ticket 288):
//
//   - BLS 4.0 (Bundeslebensmittelschlüssel, Max Rubner-Institut, CC BY 4.0):
//     BLS_4_0_Daten_2025_DE.xlsx and BLS_4_0_Data_2025_EN.xlsx in the -bls
//     directory;
//   - USDA FoodData Central (CC0): the zip files of Foundation Foods and SR
//     Legacy in the -usda directory.
//
// It is run by hand (or with `go generate ./internal/nutrition`), never by the
// box at run time. The input files are read, never executed or unpacked to
// disk, and are never part of the repository; only the extract is. The
// extract has the seven values of the EU nutrition declaration per 100 g
// (energy in kJ and kcal, fat, of which saturates, carbohydrate, of which
// sugars, protein, salt) and the names. A value the source does not give
// stays empty: it is never written as 0.
//
//	go run ./tools/foodlist -bls DIR -usda DIR -out internal/nutrition/data/foods.tsv.gz
package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The eight numbers of a food, in the order of the file.
const (
	kJ = iota
	kcal
	fat
	saturates
	carbs
	sugars
	protein
	salt
	numValues
)

// food is one line of the extract.
type food struct {
	Source string // "bls" or "usda"
	ID     string // the BLS code or the FDC id
	De, En string
	v      [numValues]float64
	has    [numValues]bool
}

// cols are the eight values as the file writes them; a missing one is empty.
func (f food) cols() []string {
	out := make([]string, numValues)
	for i := range out {
		if f.has[i] {
			out[i] = strconv.FormatFloat(round3(f.v[i]), 'f', -1, 64)
		}
	}
	return out
}

func round3(v float64) float64 { return math.Round(v*1000+1e-9) / 1000 }

// fill derives the one energy value that is missing from the other (1 kcal is
// 4.184 kJ); nothing else is ever derived or guessed.
func (f *food) fill() {
	switch {
	case f.has[kcal] && !f.has[kJ]:
		f.v[kJ], f.has[kJ] = math.Round(f.v[kcal]*4.184), true
	case f.has[kJ] && !f.has[kcal]:
		f.v[kcal], f.has[kcal] = math.Round(f.v[kJ]/4.184), true
	}
}

func main() {
	bls := flag.String("bls", "", "directory with the BLS 4.0 files (BLS_4_0_Daten_2025_DE.xlsx, BLS_4_0_Data_2025_EN.xlsx)")
	usda := flag.String("usda", "", "directory with the USDA FoodData Central zip files (Foundation Foods, SR Legacy)")
	out := flag.String("out", "internal/nutrition/data/foods.tsv.gz", "file to write")
	flag.Parse()
	if *bls == "" || *usda == "" {
		fmt.Fprintln(os.Stderr, "foodlist: -bls and -usda are required")
		os.Exit(2)
	}
	if err := run(*bls, *usda, *out); err != nil {
		fmt.Fprintln(os.Stderr, "foodlist:", err)
		os.Exit(1)
	}
}

func run(blsDir, usdaDir, out string) error {
	b, err := readBLS(blsDir)
	if err != nil {
		return fmt.Errorf("BLS: %w", err)
	}
	u, err := readUSDA(usdaDir)
	if err != nil {
		return fmt.Errorf("USDA: %w", err)
	}
	all := append(b, u...)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Source != all[j].Source {
			return all[i].Source < all[j].Source
		}
		return all[i].ID < all[j].ID
	})
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression) // no name, no time: the same input gives the same bytes
	if err := write(zw, all); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(out), "LICENSE-Nutrition.txt"), []byte(licenceText), 0o644); err != nil {
		return err
	}
	fmt.Printf("foodlist: %d BLS foods, %d USDA foods, %d bytes gzipped\n", len(b), len(u), buf.Len())
	return nil
}

const header = `# Hejmo food data: the seven values of the EU nutrition declaration per 100 g (ticket 288), made by tools/foodlist.
# Sources: Max Rubner-Institut (2025): Bundeslebensmittelschlüssel (BLS), Version 4.0 - Deutsche Nährstoffdatenbank. Karlsruhe.
# DOI 10.25826/Data20251217-134202-0, licence CC BY 4.0; and U.S. Department of Agriculture, Agricultural Research Service,
# FoodData Central (Foundation Foods, SR Legacy), licence CC0 1.0. An empty value is missing in the source, never 0.
# columns: source, id, German name, English name, kJ, kcal, fat g, of which saturates g, carbohydrate g, of which sugars g, protein g, salt g
`

const licenceText = `Nutrition values of Hejmo (internal/nutrition/data/foods.tsv.gz)

Contains data from the Bundeslebensmittelschlüssel (BLS), Version 4.0, German Nutrient Database:
Max Rubner-Institut (2025): Bundeslebensmittelschlüssel (BLS), Version 4.0 - Deutsche Nährstoffdatenbank. Karlsruhe.
DOI: 10.25826/Data20251217-134202-0. Publisher: Max Rubner-Institut (MRI), Karlsruhe. https://blsdb.de
Licence: Creative Commons Attribution 4.0 International (CC BY 4.0), https://creativecommons.org/licenses/by/4.0/legalcode
Changes: reduced to the seven values of the EU nutrition declaration per 100 g edible portion (energy in kJ and kcal, fat,
saturated fat, available carbohydrate, sugars, protein, salt) and the food names; values in the data file are kept as given.

Contains data from USDA FoodData Central (Foundation Foods, SR Legacy): U.S. Department of Agriculture, Agricultural
Research Service. https://fdc.nal.usda.gov
Licence: CC0 1.0 Universal (public domain), https://creativecommons.org/publicdomain/zero/1.0/
Changes: reduced to the same seven values and the food names. Salt is sodium x 2.5; carbohydrate is "by summation" where
given, otherwise "by difference" less fibre where given; where only one of kJ and kcal is given the other is calculated
(1 kcal = 4.184 kJ). A value the source does not give is left empty, never set to 0.

The files are made by tools/foodlist from the files of the sources; they are not part of this repository.
`

func write(w io.Writer, foods []food) error {
	bw := bufio.NewWriter(w)
	bw.WriteString(header)
	for _, f := range foods {
		name := func(s string) string { return strings.Join(strings.Fields(strings.ReplaceAll(s, "\t", " ")), " ") }
		fmt.Fprintf(bw, "%s\t%s\t%s\t%s\t%s\n", f.Source, f.ID, name(f.De), name(f.En), strings.Join(f.cols(), "\t"))
	}
	return bw.Flush()
}

// ---- BLS ----

// blsColumns are the components Hejmo takes, by the code the header of the
// value column starts with, and where they go.
var blsColumns = map[string]int{"ENERCJ": kJ, "ENERCC": kcal, "FAT": fat, "FASAT": saturates, "CHO": carbs, "SUGAR": sugars, "PROT625": protein, "NACL": salt}

func readBLS(dir string) ([]food, error) {
	en := map[string]string{}
	err := readSheet(filepath.Join(dir, "BLS_4_0_Data_2025_EN.xlsx"), func(n int, row []string) error {
		if n > 1 && len(row) > 2 && row[0] != "" {
			en[row[0]] = row[2]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var foods []food
	colOf := map[int]int{} // sheet column -> value
	err = readSheet(filepath.Join(dir, "BLS_4_0_Daten_2025_DE.xlsx"), func(n int, row []string) error {
		if n == 1 {
			for i, h := range row {
				code, _, _ := strings.Cut(h, " ")
				if k, ok := blsColumns[code]; ok && strings.HasSuffix(h, "]") {
					colOf[i] = k
				}
			}
			codes := make([]string, 0, len(blsColumns))
			for code := range blsColumns {
				codes = append(codes, code)
			}
			sort.Strings(codes) // the same message every time
			for _, code := range codes {
				k := blsColumns[code]
				found := false
				for _, v := range colOf {
					found = found || v == k
				}
				if !found {
					return fmt.Errorf("no column %s in the German data file", code)
				}
			}
			return nil
		}
		if len(row) < 2 || row[0] == "" {
			return nil
		}
		f := food{Source: "bls", ID: row[0], De: row[1], En: en[row[0]]}
		for i, k := range colOf {
			if i < len(row) {
				if v, ok := blsNumber(row[i]); ok {
					f.v[k], f.has[k] = v, true
				}
			}
		}
		f.fill()
		foods = append(foods, f)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(foods) == 0 {
		return nil, errors.New("no foods in the German data file")
	}
	return foods, nil
}

// blsNumber reads a value: "-" and empty are missing, never 0; a decimal comma is accepted.
func blsNumber(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" || s == "-" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, false
	}
	return v, true
}

// ---- the sheet of an xlsx file, read as a stream ----

// readSheet calls fn for every row of the first worksheet with the cells as
// text (numbers as written in the file), starting with row 1.
func readSheet(file string, fn func(n int, row []string) error) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	sheet := byName["xl/worksheets/sheet1.xml"]
	if sheet == nil {
		return errors.New("no xl/worksheets/sheet1.xml in " + filepath.Base(file))
	}
	var shared []string
	if f := byName["xl/sharedStrings.xml"]; f != nil {
		rc, err := f.Open()
		if err != nil {
			return err
		}
		shared, err = readShared(rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	rc, err := sheet.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := xml.NewDecoder(bufio.NewReaderSize(rc, 1<<20))
	var row []string
	n := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "row":
			row = row[:0]
			n++
			var rowEnd bool
			for !rowEnd {
				t, err := dec.Token()
				if err != nil {
					return err
				}
				switch e := t.(type) {
				case xml.StartElement:
					if e.Name.Local != "c" {
						continue
					}
					var ref, typ string
					for _, a := range e.Attr {
						switch a.Name.Local {
						case "r":
							ref = a.Value
						case "t":
							typ = a.Value
						}
					}
					text, err := readCell(dec, typ, shared)
					if err != nil {
						return err
					}
					col := columnIndex(ref)
					for len(row) <= col {
						row = append(row, "")
					}
					row[col] = text
				case xml.EndElement:
					rowEnd = e.Name.Local == "row"
				}
			}
			if err := fn(n, row); err != nil {
				return err
			}
		}
	}
}

// readCell reads up to the end of a cell: the value of a number, the shared
// string or the inline string of a text.
func readCell(dec *xml.Decoder, typ string, shared []string) (string, error) {
	var val, inline strings.Builder
	var in string
	for {
		t, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch e := t.(type) {
		case xml.StartElement:
			in = e.Name.Local
		case xml.CharData:
			switch in {
			case "v":
				val.Write(e)
			case "t":
				inline.Write(e)
			}
		case xml.EndElement:
			if e.Name.Local == "c" {
				switch typ {
				case "s":
					i, err := strconv.Atoi(strings.TrimSpace(val.String()))
					if err != nil || i < 0 || i >= len(shared) {
						return "", nil
					}
					return shared[i], nil
				case "inlineStr":
					return inline.String(), nil
				}
				return strings.TrimSpace(val.String()), nil
			}
			in = ""
		}
	}
}

// readShared reads the shared strings; a rich text string is its runs joined.
func readShared(r io.Reader) ([]string, error) {
	dec := xml.NewDecoder(bufio.NewReaderSize(r, 1<<20))
	var out []string
	var cur strings.Builder
	inT, inSI := false, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				inT = true
			case "rPh": // phonetic hints are no text of the cell
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		case xml.CharData:
			if inSI && inT {
				cur.Write(e)
			}
		case xml.EndElement:
			switch e.Name.Local {
			case "t":
				inT = false
			case "si":
				out = append(out, cur.String())
				inSI = false
			}
		}
	}
}

// columnIndex is the zero-based column of a cell reference such as "AB12".
func columnIndex(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A') + 1
	}
	return n - 1
}

// ---- USDA ----

// The FoodData Central nutrient ids Hejmo takes.
const (
	nKcal       = "1008"
	nKJ         = "1062"
	nFat        = "1004"
	nSaturates  = "1258"
	nCarbDiff   = "1005"
	nCarbSum    = "1050"
	nFibre      = "1079"
	nSugarTotal = "2000"
	nSugarNLEA  = "1063"
	nProtein    = "1003"
	nSodium     = "1093"
)

// usdaFiles are the zip files read and the data type of the foods taken from each.
var usdaFiles = []struct{ zip, dataType string }{
	{"FoodData_Central_foundation_food_csv_", "foundation_food"},
	{"FoodData_Central_sr_legacy_food_csv_", "sr_legacy_food"},
}

func readUSDA(dir string) ([]food, error) {
	var out []food
	for _, u := range usdaFiles {
		matches, _ := filepath.Glob(filepath.Join(dir, u.zip+"*.zip"))
		if len(matches) != 1 {
			return nil, fmt.Errorf("want exactly one %s*.zip in %s, found %d", u.zip, dir, len(matches))
		}
		foods, err := readUSDAZip(matches[0], u.dataType)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(matches[0]), err)
		}
		out = append(out, foods...)
	}
	return out, nil
}

func readUSDAZip(file, dataType string) ([]food, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	open := func(name string) (io.ReadCloser, error) {
		for _, f := range zr.File {
			if path.Base(f.Name) == name {
				return f.Open()
			}
		}
		return nil, errors.New("no " + name)
	}
	byID := map[string]*food{}
	var order []string
	err = eachCSV(open, "food.csv", func(r map[string]string) {
		if r["data_type"] == dataType {
			byID[r["fdc_id"]] = &food{Source: "usda", ID: r["fdc_id"], En: r["description"]}
			order = append(order, r["fdc_id"])
		}
	})
	if err != nil {
		return nil, err
	}
	raw := map[string]map[string]float64{} // fdc id -> nutrient id -> amount
	err = eachCSV(open, "food_nutrient.csv", func(r map[string]string) {
		if _, ok := byID[r["fdc_id"]]; !ok {
			return
		}
		v, err := strconv.ParseFloat(r["amount"], 64)
		if err != nil || v < 0 || math.IsNaN(v) {
			return
		}
		m := raw[r["fdc_id"]]
		if m == nil {
			m = map[string]float64{}
			raw[r["fdc_id"]] = m
		}
		m[r["nutrient_id"]] = v
	})
	if err != nil {
		return nil, err
	}
	var out []food
	for _, id := range order {
		f, m := byID[id], raw[id]
		set := func(k int, v float64) { f.v[k], f.has[k] = v, true }
		if v, ok := m[nKcal]; ok {
			set(kcal, v)
		}
		if v, ok := m[nKJ]; ok {
			set(kJ, v)
		}
		if v, ok := m[nFat]; ok {
			set(fat, v)
		}
		if v, ok := m[nSaturates]; ok {
			set(saturates, v)
		}
		switch sum, okSum := m[nCarbSum]; {
		case okSum:
			set(carbs, sum)
		default:
			if diff, ok := m[nCarbDiff]; ok {
				if fibre, ok := m[nFibre]; ok {
					diff = math.Max(0, diff-fibre)
				}
				set(carbs, diff)
			}
		}
		if v, ok := m[nSugarTotal]; ok {
			set(sugars, v)
		} else if v, ok := m[nSugarNLEA]; ok {
			set(sugars, v)
		}
		if v, ok := m[nProtein]; ok {
			set(protein, v)
		}
		if v, ok := m[nSodium]; ok {
			set(salt, v*2.5/1000)
		}
		f.fill()
		if f.has[kcal] || f.has[kJ] { // a food without energy says too little to be offered
			out = append(out, *f)
		}
	}
	return out, nil
}

// eachCSV calls fn with every row of a CSV file of the zip, keyed by header.
func eachCSV(open func(string) (io.ReadCloser, error), name string, fn func(map[string]string)) error {
	rc, err := open(name)
	if err != nil {
		return err
	}
	defer rc.Close()
	r := csv.NewReader(bufio.NewReaderSize(rc, 1<<20))
	r.ReuseRecord = false
	head, err := r.Read()
	if err != nil {
		return err
	}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		row := make(map[string]string, len(head))
		for i, h := range head {
			if i < len(rec) {
				row[h] = rec[i]
			}
		}
		fn(row)
	}
}
