package nutrition

import (
	"math"
	"strconv"
	"strings"
)

// Format words one value for a page, in the language of the page: kcal and kJ
// whole with a thousands separator; grams whole from 10 g, one decimal below
// that and "unter 0,5 g" ("under 0.5 g") below half a gram instead of 0;
// salt always with one decimal and "unter 0,1 g" below that.
func Format(k Key, v float64, lang string) string {
	en := lang == "en"
	switch k {
	case KJ:
		return thousands(math.Round(v), en) + " kJ"
	case KCal:
		return thousands(math.Round(v), en) + " kcal"
	case Salt:
		if v < 0.1 {
			return under(en, "0,1", "0.1")
		}
		return decimal(math.Round(v*10)/10, 1, en) + " g"
	}
	if v < 0.5 {
		return under(en, "0,5", "0.5")
	}
	if r := math.Round(v*10) / 10; r < 10 {
		return decimal(r, 1, en) + " g"
	}
	return thousands(math.Round(v), en) + " g"
}

func under(en bool, de, eng string) string {
	if en {
		return "under " + eng + " g"
	}
	return "unter " + de + " g"
}

func decimal(v float64, digits int, en bool) string {
	s := strconv.FormatFloat(v, 'f', digits, 64)
	if !en {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// thousands writes a whole number with the separator of the language.
func thousands(v float64, en bool) string {
	s := strconv.FormatFloat(v, 'f', 0, 64)
	sep := "."
	if en {
		sep = ","
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + sep + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
