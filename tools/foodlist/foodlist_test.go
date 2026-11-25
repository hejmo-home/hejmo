package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeZip makes a zip file from name -> content.
func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cell is one cell of a fixture sheet: a string (shared), a number or nothing.
type cell = any

// xlsx writes a workbook the way Excel does: shared strings for text, plain
// numbers for numbers.
func xlsx(t *testing.T, path string, rows [][]cell) {
	t.Helper()
	var shared []string
	idx := map[string]int{}
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	letters := func(i int) string {
		s := ""
		for i++; i > 0; i = (i - 1) / 26 {
			s = string(rune('A'+(i-1)%26)) + s
		}
		return s
	}
	for r, row := range rows {
		sheet.WriteString(`<row r="` + itoa(r+1) + `">`)
		for c, v := range row {
			ref := letters(c) + itoa(r+1)
			switch x := v.(type) {
			case nil:
			case string:
				i, ok := idx[x]
				if !ok {
					i = len(shared)
					idx[x] = i
					shared = append(shared, x)
				}
				sheet.WriteString(`<c r="` + ref + `" t="s"><v>` + itoa(i) + `</v></c>`)
			case float64:
				sheet.WriteString(`<c r="` + ref + `"><v>` + ftoa(x) + `</v></c>`)
			}
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	var ss strings.Builder
	ss.WriteString(`<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, s := range shared {
		ss.WriteString(`<si><t>` + strings.NewReplacer("&", "&amp;", "<", "&lt;").Replace(s) + `</t></si>`)
	}
	ss.WriteString(`</sst>`)
	writeZip(t, path, map[string]string{"xl/worksheets/sheet1.xml": sheet.String(), "xl/sharedStrings.xml": ss.String()})
}

var blsHeader = []cell{"BLS Code", "Lebensmittelbezeichnung", "Food name",
	"ENERCJ Energie (Kilojoule) [kJ/100g]", "ENERCJ Datenherkunft", "ENERCJ Referenz",
	"ENERCC Energie (Kilokalorien) [kcal/100g]", "ENERCC Datenherkunft", "ENERCC Referenz",
	"FAT Fett [g/100g]", "FAT Datenherkunft", "FAT Referenz",
	"FASAT Fettsäuren, gesättigt, gesamt [g/100g]", "FASAT Datenherkunft", "FASAT Referenz",
	"CHO Kohlenhydrate, verfügbar [g/100g]", "CHO Datenherkunft", "CHO Referenz",
	"SUGAR Zucker (Mono- und Disaccharide), gesamt [g/100g]", "SUGAR Datenherkunft", "SUGAR Referenz",
	"PROT625 Protein (Nx6,25) [g/100g]", "PROT625 Datenherkunft", "PROT625 Referenz",
	"NACL Salz (Natriumchlorid) [g/100g]", "NACL Datenherkunft", "NACL Referenz"}

func blsFixture(t *testing.T, dir string) {
	t.Helper()
	xlsx(t, filepath.Join(dir, "BLS_4_0_Daten_2025_DE.xlsx"), [][]cell{
		blsHeader,
		// Butter: sugar is not given ("-"): missing, not 0. Salt is a measured 0.
		{"M100000", "Butter", "Butter", 3000.0, "x", "r", 717.0, "x", "r", 82.0, "x", "r", 52.0, "x", "r", 0.6, "x", "r", "-", "x", "r", 0.7, "x", "r", 0.0, "x", "r"},
		// Wasser: every value is a real 0.
		{"N100000", "Wasser <Mineral>", "Water", 0.0, "x", "r", 0.0, "x", "r", 0.0, "x", "r", 0.0, "x", "r", 0.0, "x", "r", 0.0, "x", "r", 0.0, "x", "r", 0.0, "x", "r"},
	})
	xlsx(t, filepath.Join(dir, "BLS_4_0_Data_2025_EN.xlsx"), [][]cell{
		{"BLS Code", "Lebensmittelbezeichnung", "Food name"},
		{"M100000", "Butter", "Butter, salted"},
		{"N100000", "Wasser", "Water <mineral>"},
	})
}

func TestBLSValuesKeepMissingApartFromZero(t *testing.T) {
	dir := t.TempDir()
	blsFixture(t, dir)
	foods, err := readBLS(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(foods) != 2 {
		t.Fatalf("got %d foods, want 2", len(foods))
	}
	butter := foods[0]
	if butter.Source != "bls" || butter.ID != "M100000" || butter.De != "Butter" || butter.En != "Butter, salted" {
		t.Fatalf("butter = %+v", butter)
	}
	if got := butter.cols(); strings.Join(got, "|") != "3000|717|82|52|0.6||0.7|0" {
		t.Fatalf("butter values = %q: the missing sugar must stay empty, the measured salt 0 must stay 0", strings.Join(got, "|"))
	}
	if got := foods[1].cols(); strings.Join(got, "|") != "0|0|0|0|0|0|0|0" {
		t.Fatalf("water values = %q", strings.Join(got, "|"))
	}
	if foods[1].De != "Wasser <Mineral>" || foods[1].En != "Water <mineral>" {
		t.Fatalf("names are not read as written: %+v", foods[1])
	}
}

func TestBLSRefusesAFileWithoutTheColumns(t *testing.T) {
	dir := t.TempDir()
	xlsx(t, filepath.Join(dir, "BLS_4_0_Daten_2025_DE.xlsx"), [][]cell{{"BLS Code", "Lebensmittelbezeichnung", "Food name"}})
	xlsx(t, filepath.Join(dir, "BLS_4_0_Data_2025_EN.xlsx"), [][]cell{{"BLS Code", "Lebensmittelbezeichnung", "Food name"}})
	if _, err := readBLS(dir); err == nil || !strings.Contains(err.Error(), "CHO") {
		t.Fatalf("err = %v, want one naming the missing column", err)
	}
}

const usdaFoodHeader = `"fdc_id","data_type","description","food_category_id","publication_date"` + "\n"
const usdaNutHeader = `"id","fdc_id","nutrient_id","amount","data_points","derivation_id","min","max","median","footnote","min_year_acquired"` + "\n"

func usdaFixture(t *testing.T, dir string) {
	t.Helper()
	nut := func(id, fdc, nid, amount string) string {
		return `"` + id + `","` + fdc + `","` + nid + `","` + amount + `","1","1","","","","",""` + "\n"
	}
	// 100: foundation food with everything, by summation carbohydrates, no kJ.
	// 101: a sample of a foundation food that must not be taken.
	// 200: SR legacy food: carbohydrates by difference and fibre, sodium, kJ given, no sugars.
	// 201: SR legacy food with no energy at all: useless, left out.
	writeZip(t, filepath.Join(dir, "FoodData_Central_foundation_food_csv_2026-10-29.zip"), map[string]string{
		"FoodData_Central_foundation_food_csv_2026-10-29/food.csv": usdaFoodHeader +
			`"100","foundation_food","Milk, whole","1","2026-01-01"` + "\n" + `"101","sample_food","MILK, SAMPLE","1","2026-01-01"` + "\n",
		"FoodData_Central_foundation_food_csv_2026-10-29/food_nutrient.csv": usdaNutHeader +
			nut("1", "100", "1008", "61") + nut("2", "100", "1004", "3.2") + nut("3", "100", "1258", "1.9") +
			nut("4", "100", "1050", "4.8") + nut("5", "100", "1005", "5.1") + nut("6", "100", "2000", "5.0") +
			nut("7", "100", "1003", "3.3") + nut("8", "100", "1093", "43") +
			nut("9", "101", "1008", "99"),
	})
	writeZip(t, filepath.Join(dir, "FoodData_Central_sr_legacy_food_csv_2018-04.zip"), map[string]string{
		"FoodData_Central_sr_legacy_food_csv_2018-04/food.csv": usdaFoodHeader +
			`"200","sr_legacy_food","Bread, wheat","1","2019-04-01"` + "\n" + `"201","sr_legacy_food","Nothing","1","2019-04-01"` + "\n",
		"FoodData_Central_sr_legacy_food_csv_2018-04/food_nutrient.csv": usdaNutHeader +
			nut("1", "200", "1008", "250") + nut("2", "200", "1062", "1046") + nut("3", "200", "1004", "3.5") +
			nut("4", "200", "1005", "50") + nut("5", "200", "1079", "3") + nut("6", "200", "1003", "9") + nut("7", "200", "1093", "500") +
			nut("8", "201", "1003", "1"),
	})
}

func TestUSDAValues(t *testing.T) {
	dir := t.TempDir()
	usdaFixture(t, dir)
	foods, err := readUSDA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(foods) != 2 {
		t.Fatalf("got %d foods %+v, want the foundation food and the SR legacy bread only", len(foods), foods)
	}
	milk, bread := foods[0], foods[1]
	if milk.Source != "usda" || milk.ID != "100" || milk.En != "Milk, whole" || milk.De != "" {
		t.Fatalf("milk = %+v", milk)
	}
	// kJ is derived from kcal (61 x 4.184 = 255.2), carbohydrates by summation win over by difference,
	// salt is sodium x 2.5 (43 mg -> 0.1075 g).
	if got := strings.Join(milk.cols(), "|"); got != "255|61|3.2|1.9|4.8|5|3.3|0.108" {
		t.Fatalf("milk values = %q", got)
	}
	// Carbohydrates by difference include fibre: take it off (50 - 3). Sugars are missing, so empty.
	if got := strings.Join(bread.cols(), "|"); got != "1046|250|3.5||47||9|1.25" {
		t.Fatalf("bread values = %q", got)
	}
}

func TestWriteIsReproducibleAndSorted(t *testing.T) {
	dir := t.TempDir()
	blsFixture(t, dir)
	usdaFixture(t, dir)
	out1, out2 := filepath.Join(dir, "a.tsv.gz"), filepath.Join(dir, "b.tsv.gz")
	for _, out := range []string{out1, out2} {
		if err := run(dir, dir, out); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := os.ReadFile(out1)
	b, _ := os.ReadFile(out2)
	if !bytes.Equal(a, b) {
		t.Fatal("two runs on the same files differ")
	}
	zr, err := gzip.NewReader(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := io.ReadAll(zr)
	var data []string
	for _, l := range strings.Split(string(text), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			data = append(data, strings.SplitN(l, "\t", 3)[0]+" "+strings.SplitN(l, "\t", 3)[1])
		}
	}
	if got := strings.Join(data, ","); got != "bls M100000,bls N100000,usda 100,usda 200" {
		t.Fatalf("lines = %s", got)
	}
	if !strings.Contains(string(text), "Max Rubner-Institut") || !strings.Contains(string(text), "CC0") {
		t.Fatal("the header must name the sources and their licences")
	}
	if _, err := os.Stat(filepath.Join(dir, "LICENSE-Nutrition.txt")); err != nil {
		t.Fatalf("licence file next to the output missing: %v", err)
	}
}

func itoa(i int) string     { return strconv.Itoa(i) }
func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
