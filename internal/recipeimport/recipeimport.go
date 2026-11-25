// Package recipeimport reads a recipe out of a web page (ADR 0021, ticket
// 289): the schema.org/Recipe objects of its JSON-LD, the ingredient lines
// with their quantities, the steps as plain text and the nutrition values the
// page states. It is pure: it fetches nothing (see Fetcher) and stores nothing.
// Make or buy (ADR 0021): own parser; go-recipe was reference only.
package recipeimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ErrNoRecipe means the page holds no schema.org/Recipe.
var ErrNoRecipe = errors.New("recipeimport: no recipe on the page")

// Nutrition is what the page states per portion; nil is "not stated", never 0.
type Nutrition struct {
	KCal, KJ, Fat, Saturates, Carbs, Sugars, Protein, Salt *float64
}

// Found is one recipe of a page as the page wrote it.
type Found struct {
	Name string
	// Portions is the number of portions Hejmo takes: the page's own number, or the number found in a
	// yield that is no portion count (a tray of 12 pieces), or 0 when the page says nothing usable.
	// PortionsGiven says the page named portions (or people); YieldText is the page's yield as written.
	Portions      int
	PortionsGiven bool
	YieldText     string
	Minutes       int
	// Category is one of the six keys; when the page's word is none of them it is "main" and
	// CategoryWord is the word the page used.
	Category, CategoryWord string
	Ingredients            []Line
	Steps                  []string
	Keywords               []string
	ImageURL               string
	Nutrition              *Nutrition
	Lang                   string // "de" or "en" and so on, "" when the page does not say
}

// Extract returns every Recipe object in the JSON-LD of the page, in page order.
func Extract(page []byte) ([]Found, error) {
	var out []Found
	for _, raw := range jsonLD(page) {
		var v any
		dec := json.NewDecoder(bytes.NewReader(raw))
		if err := dec.Decode(&v); err != nil {
			continue
		}
		collect(v, &out, 0)
	}
	if len(out) == 0 {
		return nil, ErrNoRecipe
	}
	return out, nil
}

func jsonLD(page []byte) [][]byte {
	var out [][]byte
	z := html.NewTokenizer(bytes.NewReader(page))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "script" || !hasAttr {
				continue
			}
			ld := false
			for {
				k, v, more := z.TagAttr()
				if string(k) == "type" && strings.Contains(strings.ToLower(string(v)), "ld+json") {
					ld = true
				}
				if !more {
					break
				}
			}
			if ld && z.Next() == html.TextToken {
				out = append(out, bytes.TrimSpace(z.Text()))
			}
		}
	}
}

func collect(v any, out *[]Found, depth int) {
	if depth > 8 {
		return
	}
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			collect(e, out, depth+1)
		}
	case map[string]any:
		if isType(x["@type"], "Recipe") {
			*out = append(*out, build(x))
			return
		}
		if g, ok := x["@graph"]; ok {
			collect(g, out, depth+1)
		}
	}
}

func isType(t any, want string) bool {
	switch x := t.(type) {
	case string:
		return strings.EqualFold(x, want) || strings.HasSuffix(x, "/"+want)
	case []any:
		for _, e := range x {
			if isType(e, want) {
				return true
			}
		}
	}
	return false
}

// text turns a JSON value into a string: a string, a number, or the first of a list.
func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		for _, e := range x {
			if s := text(e); s != "" {
				return s
			}
		}
	}
	return ""
}

func texts(v any) []string {
	switch x := v.(type) {
	case string, float64:
		return []string{text(x)}
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, texts(e)...)
		}
		return out
	}
	return nil
}

func build(m map[string]any) Found {
	f := Found{Name: Plain(text(m["name"]))}
	f.Portions, f.PortionsGiven, f.YieldText = parseYield(texts(m["recipeYield"]))
	f.Minutes = minutesOf(m)
	for _, w := range texts(m["recipeCategory"]) {
		if c := CategoryOf(w); c != "" {
			f.Category, f.CategoryWord = c, ""
			break
		}
		if f.CategoryWord == "" {
			f.CategoryWord = strings.TrimSpace(w)
		}
	}
	if f.Category == "" {
		f.Category = "main"
	}
	switch k := m["keywords"].(type) {
	case string:
		f.Keywords = splitKeywords(k)
	case []any:
		for _, e := range texts(k) {
			f.Keywords = append(f.Keywords, splitKeywords(e)...)
		}
	}
	f.Lang = langOf(text(m["inLanguage"]))
	f.ImageURL = imageOf(m["image"])
	for _, l := range texts(m["recipeIngredient"]) {
		if l = strings.Join(strings.Fields(Plain(l)), " "); l != "" {
			f.Ingredients = append(f.Ingredients, ParseLine(l))
		}
	}
	f.Steps = steps(m["recipeInstructions"], "")
	f.Nutrition = nutritionOf(m["nutrition"])
	return f
}

func langOf(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_"); i > 0 {
		s = s[:i]
	}
	return s
}

func splitKeywords(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.Join(strings.Fields(Plain(p)), " "); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func imageOf(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		for _, e := range x {
			if s := imageOf(e); s != "" {
				return s
			}
		}
	case map[string]any:
		if s := imageOf(x["url"]); s != "" {
			return s
		}
		return imageOf(x["contentUrl"])
	}
	return ""
}

var isoDuration = regexp.MustCompile(`(?i)^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

func duration(s string) int {
	m := isoDuration.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
	return n(1)*24*60 + n(2)*60 + n(3)
}

func minutesOf(m map[string]any) int {
	n := duration(text(m["totalTime"]))
	if n == 0 {
		n = duration(text(m["prepTime"])) + duration(text(m["cookTime"]))
	}
	if n > 9999 {
		return 0
	}
	return n
}

var (
	portionRe = regexp.MustCompile(`(?i)^\s*(\d+)\s*(?:portion(?:en)?|person(?:en)?|pers\.?|port\.?|servings?|people)?\s*$`)
	portionWd = regexp.MustCompile(`(?i)^\s*\d+\s*(?:portion(?:en)?|person(?:en)?|pers\.?|port\.?|servings?|people)\s*$`)
	bracketNr = regexp.MustCompile(`\((?:[^)\d]*)(\d+)`)
	firstNr   = regexp.MustCompile(`\d+`)
)

// parseYield reads the page's yield. A number alone or with a word for portions or people is the number of
// portions. Anything else (a tray, 12 pieces) is no portion count: Hejmo takes the number in brackets, else
// the first number, and says so.
func parseYield(vals []string) (n int, given bool, written string) {
	inRange := func(v int) int {
		if v < 1 || v > 99 {
			return 0
		}
		return v
	}
	var first string
	for _, v := range vals {
		v = strings.Join(strings.Fields(Plain(v)), " ")
		if v == "" {
			continue
		}
		if first == "" {
			first = v
		}
		if m := portionRe.FindStringSubmatch(v); m != nil {
			if x, _ := strconv.Atoi(m[1]); inRange(x) > 0 && (portionWd.MatchString(v) || len(vals) == 1 || m[0] == m[1]) {
				return x, true, v
			}
		}
	}
	if first == "" {
		return 0, false, ""
	}
	if m := bracketNr.FindStringSubmatch(first); m != nil {
		x, _ := strconv.Atoi(m[1])
		return inRange(x), false, first
	}
	x, _ := strconv.Atoi(firstNr.FindString(first))
	return inRange(x), false, first
}

var categories = map[string]string{
	"hauptgericht": "main", "hauptspeise": "main", "main": "main", "main course": "main", "main dish": "main", "mains": "main", "dinner": "main", "lunch": "main", "abendessen": "main", "mittagessen": "main",
	"beilage": "side", "beilagen": "side", "side": "side", "side dish": "side", "sides": "side",
	"dessert": "dessert", "nachtisch": "dessert", "nachspeise": "dessert", "desserts": "dessert", "süßspeise": "dessert",
	"backen": "baking", "kuchen": "baking", "gebäck": "baking", "baking": "baking", "baked goods": "baking", "torte": "baking",
	"frühstück": "breakfast", "breakfast": "breakfast", "brunch": "breakfast",
	"snack": "snack", "snacks": "snack", "zwischenmahlzeit": "snack", "appetizer": "snack", "vorspeise": "snack",
}

// CategoryOf maps the word of a page to one of the six categories, "" when it is none of them.
func CategoryOf(word string) string {
	return categories[strings.ToLower(strings.TrimSpace(word))]
}

func steps(v any, prefix string) []string {
	switch x := v.(type) {
	case string:
		var out []string
		for _, l := range plainLines(x) {
			out = append(out, prefix+l)
		}
		return out
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, steps(e, prefix)...)
		}
		return out
	case map[string]any:
		if isType(x["@type"], "HowToSection") {
			p := prefix
			if n := strings.Join(strings.Fields(Plain(text(x["name"]))), " "); n != "" {
				p = prefix + n + ": "
			}
			return steps(x["itemListElement"], p)
		}
		t := text(x["text"])
		if strings.TrimSpace(t) == "" {
			t = text(x["name"])
		}
		if s := strings.Join(plainLines(t), " "); s != "" {
			return []string{prefix + s}
		}
	}
	return nil
}

// amountRe is a number with the unit that follows it. A thousands point or comma (one to three digits,
// then groups of exactly three, the first not 0) is no decimal sign: "1.250 kJ" is 1250 kJ.
var amountRe = regexp.MustCompile(`([1-9]\d{0,2}(?:[.,]\d{3})+|\d+(?:[.,]\d+)?)\s*([\p{L}]*)`)

var thousandsRe = regexp.MustCompile(`^[1-9]\d{0,2}(?:[.,]\d{3})+$`)

type amount struct {
	val  float64
	unit string // lower case, "" when none follows the number
}

// amounts reads every number of s with its own unit.
func amounts(s string) []amount {
	var out []amount
	for _, m := range amountRe.FindAllStringSubmatch(strings.ToLower(s), -1) {
		num := m[1]
		if thousandsRe.MatchString(num) {
			num = strings.NewReplacer(".", "", ",", "").Replace(num)
		}
		v, err := strconv.ParseFloat(strings.Replace(num, ",", ".", 1), 64)
		if err == nil {
			out = append(out, amount{v, m[2]})
		}
	}
	return out
}

func nutritionOf(v any) *Nutrition {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	grams := func(key string) *float64 {
		as := amounts(text(m[key]))
		if len(as) == 0 {
			return nil
		}
		v := as[0].val
		if as[0].unit == "mg" {
			v /= 1000
		}
		v = round(v, 2)
		return &v
	}
	n := &Nutrition{Fat: grams("fatContent"), Saturates: grams("saturatedFatContent"), Carbs: grams("carbohydrateContent"), Sugars: grams("sugarContent"), Protein: grams("proteinContent")}
	if na := grams("sodiumContent"); na != nil {
		s := round(*na*2.5, 2)
		n.Salt = &s
	}
	var kcal, kj *float64
	for _, a := range amounts(text(m["calories"])) {
		v := a.val
		switch {
		case a.unit == "kj" && kj == nil:
			kj = &v
		case a.unit != "kj" && kcal == nil:
			kcal = &v
		}
	}
	switch {
	case kcal != nil && kj == nil:
		j := round(*kcal*4.184, 0)
		kj = &j
	case kj != nil && kcal == nil:
		c := round(*kj/4.184, 0)
		kcal = &c
	}
	n.KCal, n.KJ = kcal, kj
	if n.KCal == nil && n.Fat == nil && n.Saturates == nil && n.Carbs == nil && n.Sugars == nil && n.Protein == nil && n.Salt == nil {
		return nil
	}
	return n
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Floor(v*p+0.5) / p
}

// Plain turns text that may hold HTML into plain text: tags and what is inside script and style are
// dropped, then entities are decoded (once, last), so "&amp;amp;" is "&amp;" and "&lt;b&gt;" stays text.
func Plain(s string) string { return strings.Join(plainLines(s), " ") }

func plainLines(s string) []string {
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(s))
	skip := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style":
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
			case "br", "p", "li", "div", "tr", "ol", "ul", "h1", "h2", "h3", "h4":
				b.WriteString("\n")
			}
		}
	}
	var out []string
	for _, l := range strings.Split(b.String(), "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			out = append(out, l)
		}
	}
	return out
}
