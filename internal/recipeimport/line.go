package recipeimport

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/hejmo-home/hejmo/internal/recipe"
)

// Line is an ingredient line of a page read into quantity, unit and name. Original is what the page wrote.
// Head is the name before a comma or a bracket and Rest what followed (the proposal for a variety); a
// bracket that holds only a quantity is dropped from both.
type Line struct {
	Original string
	// Amount is 0 for none; Unit is a recipe unit key or "".
	Amount     float64
	Unit       string
	Head, Rest string
	// Change names an interpretation Hejmo made: "range" (the smaller number), "some" (etwas) or "zero" (0).
	Change string
}

// Name is the whole name with its rest: "Mehl, Type 405".
func (l Line) Name() string {
	if l.Rest == "" {
		return l.Head
	}
	return l.Head + ", " + l.Rest
}

var (
	vulgarQty = regexp.MustCompile(`^(\d+)?\s*([½¼¾⅓⅔⅛⅜⅝⅞⅕⅖⅗⅘⅙⅚])`)
	mixedQty  = regexp.MustCompile(`^\d+\s+\d+\s*/\s*\d+`)
	fracQty   = regexp.MustCompile(`^\d+\s*/\s*\d+`)
	thousands = regexp.MustCompile(`^\d{1,3}(?:\.\d{3})+(?:[^\d.,]|$)`)
	plainQty  = regexp.MustCompile(`^\d+(?:[.,]\d+)?`)
	rangeSep  = regexp.MustCompile(`^\s*(?:–|—|-|bis|to)\s*`)
	bracketRe = regexp.MustCompile(`\(([^()]*)\)`)
	onlyQty   = regexp.MustCompile(`^[\d.,/½¼¾\s]+\s*([\p{L}.]{0,12})$`)
)

// qty reads a quantity at the start of s: its value and what follows.
func qty(s string) (v float64, rest string, ok bool) {
	var tok string
	switch {
	case vulgarQty.MatchString(s):
		tok = vulgarQty.FindString(s)
	case mixedQty.MatchString(s):
		tok = mixedQty.FindString(s)
	case fracQty.MatchString(s):
		tok = fracQty.FindString(s)
	case thousands.MatchString(s):
		tok = thousands.FindString(s)
		if last := tok[len(tok)-1]; last < '0' || last > '9' {
			tok = tok[:len(tok)-1]
		}
		n, err := strconv.ParseFloat(strings.ReplaceAll(tok, ".", ""), 64)
		return n, s[len(tok):], err == nil
	case plainQty.MatchString(s):
		tok = plainQty.FindString(s)
		if strings.HasSuffix(tok, ".") {
			tok = tok[:len(tok)-1]
		}
	default:
		return 0, s, false
	}
	if n, err := strconv.ParseFloat(strings.Replace(tok, ",", ".", 1), 64); err == nil && !strings.ContainsAny(tok, "/ ½¼¾⅓⅔⅛⅜⅝⅞⅕⅖⅗⅘⅙⅚") {
		return n, s[len(tok):], true
	}
	if n, err := recipe.ParseAmount(strings.TrimSpace(tok)); err == nil {
		return n, s[len(tok):], true
	}
	return 0, s, false
}

var unitWords = map[string]string{
	"g": "g", "gr": "g", "gramm": "g", "kg": "kg", "ml": "ml", "l": "l", "liter": "l", "oz": "oz", "ounce": "oz", "ounces": "oz", "lb": "lb", "lbs": "lb", "pound": "lb", "pounds": "lb",
	"el": "tbsp", "essl": "tbsp", "esslöffel": "tbsp", "tbsp": "tbsp", "tablespoon": "tbsp", "tablespoons": "tbsp", "tl": "tsp", "teelöffel": "tsp", "tsp": "tsp", "teaspoon": "tsp", "teaspoons": "tsp",
	"prise": "pinch", "prisen": "pinch", "pinch": "pinch", "msp": "knifetip", "messerspitze": "knifetip", "schuss": "dash", "dash": "dash", "spritzer": "splash", "splash": "splash",
	"handvoll": "handful", "handful": "handful", "tasse": "cup", "tassen": "cup", "cup": "cup", "cups": "cup",
	"dose": "can", "dosen": "can", "can": "can", "cans": "can", "pck": "packet", "pkg": "packet", "päckchen": "packet", "packung": "packet", "packungen": "packet", "packet": "packet", "packets": "packet",
	"becher": "tub", "tub": "tub", "bund": "bunch", "bunch": "bunch", "glas": "jar", "gläser": "jar", "jar": "jar", "flasche": "bottle", "flaschen": "bottle", "bottle": "bottle",
	"stange": "stick", "stangen": "stick", "stick": "stick", "sticks": "stick", "zehe": "clove", "zehen": "clove", "clove": "clove", "cloves": "clove",
	"scheibe": "slice", "scheiben": "slice", "slice": "slice", "slices": "slice", "würfel": "cube", "cube": "cube", "cubes": "cube", "zweig": "sprig", "zweige": "sprig", "sprig": "sprig", "sprigs": "sprig",
	"stiel": "stalk", "stiele": "stalk", "stalk": "stalk", "stalks": "stalk", "kugel": "ball", "kugeln": "ball", "blatt": "leaf", "blätter": "leaf", "leaf": "leaf", "leaves": "leaf",
	"beet": "punnet", "punnet": "punnet", "topf": "pot", "pot": "pot", "stück": "piece", "stücke": "piece", "stk": "piece", "piece": "piece", "pieces": "piece", "dutzend": "dozen", "dozen": "dozen",
}

var oneWords = map[string]bool{"ein": true, "eine": true, "einen": true, "einem": true, "einer": true, "a": true, "an": true}

// ParseLine reads an ingredient line as pages write it.
func ParseLine(original string) Line {
	l := Line{Original: original}
	s := strings.Join(strings.Fields(original), " ")
	has := false // a quantity was read
	if v, rest, ok := qty(s); ok {
		l.Amount, has = v, true
		s = strings.TrimSpace(rest)
		if m := rangeSep.FindString(s); m != "" {
			if _, rest2, ok := qty(s[len(m):]); ok {
				l.Change, s = "range", strings.TrimSpace(rest2)
			}
		}
	} else if low := strings.ToLower(s); strings.HasPrefix(low, "etwas ") || strings.HasPrefix(low, "ein wenig ") || strings.HasPrefix(low, "einige ") {
		l.Change = "some"
		s = strings.TrimSpace(s[strings.Index(s, " ")+1:])
		if strings.HasPrefix(low, "ein wenig ") {
			s = strings.TrimSpace(strings.TrimPrefix(s, "wenig "))
		}
	}
	toks := strings.SplitN(s, " ", 2)
	word := strings.ToLower(strings.TrimRight(toks[0], ".,"))
	if len(toks) == 2 && !has && oneWords[word] {
		// "eine Prise Salz": the article counts as one only before a unit
		if u2 := strings.ToLower(strings.TrimRight(strings.SplitN(toks[1], " ", 2)[0], ".,")); unitWords[u2] != "" {
			l.Amount, has = 1, true
			s = toks[1]
			toks = strings.SplitN(s, " ", 2)
			word = u2
		}
	}
	if k, ok := unitWords[word]; ok && (len(toks) == 2 || has) {
		s = ""
		if len(toks) == 2 {
			s = strings.TrimSpace(toks[1])
		}
		l.Unit = k
		if low := strings.ToLower(s); strings.HasPrefix(low, "of ") || strings.HasPrefix(low, "von ") {
			s = strings.TrimSpace(s[strings.Index(s, " ")+1:])
		}
	}
	if l.Change == "" && has && l.Amount == 0 {
		l.Change, l.Unit = "zero", ""
	}
	if l.Amount == 0 {
		l.Unit = ""
	}
	// brackets
	var inBrackets []string
	s = bracketRe.ReplaceAllStringFunc(s, func(b string) string {
		inner := strings.TrimSpace(b[1 : len(b)-1])
		if m := onlyQty.FindStringSubmatch(inner); m != nil && (m[1] == "" || unitWords[strings.ToLower(strings.TrimRight(m[1], "."))] != "") {
			return ""
		}
		if inner != "" {
			inBrackets = append(inBrackets, inner)
		}
		return ""
	})
	s = strings.Join(strings.Fields(s), " ")
	head, comma, _ := strings.Cut(s, ",")
	l.Head, l.Rest = strings.Trim(strings.TrimSpace(head), " ,;"), strings.TrimSpace(comma)
	if len(inBrackets) > 0 {
		if l.Rest == "" {
			l.Rest = strings.Join(inBrackets, ", ")
		} else {
			l.Rest += " (" + strings.Join(inBrackets, ", ") + ")"
		}
	}
	if l.Head == "" {
		l.Head, l.Rest = strings.TrimSpace(original), ""
	}
	return l
}
