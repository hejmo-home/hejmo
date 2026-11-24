package library

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Matching a typed name with an article name (ticket 277, criterion Y): case,
// umlauts and singular or plural do not matter. German endings -n, -en, -e,
// -er, -s (with the umlaut fold, Äpfel is Apfel and Nüsse is Nuss), English
// -s, -es and -ies for -y.

// Lang says which language's plural endings a name is folded with.
type Lang string

const (
	German  Lang = "de"
	English Lang = "en"
)

var pluralEndings = map[Lang][]string{
	German:  {"en", "er", "es", "n", "e", "s"},
	English: {"es", "s"},
}

// singulars are the names a folded name may be the plural of.
func singulars(f string, lang Lang) []string {
	var out []string
	if lang == English {
		if rest, ok := strings.CutSuffix(f, "ies"); ok && utf8.RuneCountInString(rest) >= 2 {
			out = append(out, rest+"y")
		}
	}
	for _, end := range pluralEndings[lang] {
		if rest, ok := strings.CutSuffix(f, end); ok && utf8.RuneCountInString(rest) >= 2 {
			out = append(out, rest)
		}
	}
	return out
}

// SameName reports whether two names of one language are the same word,
// ignoring case, umlauts and singular or plural: "Zitronen" is "Zitrone",
// "Äpfel" is "Apfel". Only the endings of lang apply, so the German "Beeren"
// is not the English "Beer".
func SameName(a, b string, lang Lang) bool {
	fa, fb := Fold(a), Fold(b)
	if fa == "" || fb == "" {
		return false
	}
	return fa == fb || slices.Contains(singulars(fa, lang), fb) || slices.Contains(singulars(fb, lang), fa)
}

// Named reports whether the text is a name of the article: the German name
// folded with German endings or the English name folded with English ones.
func (a Article) Named(text string) bool {
	return SameName(text, a.De, German) || SameName(text, a.En, English)
}

// namedBy finds the article one of whose names is the typed text: the exact
// name (ignoring case and umlauts) before a singular or plural of it. The
// first match wins, so own articles (listed first) beat shipped ones.
func namedBy(text string, all []Article) (Article, bool) {
	t := Fold(text)
	if t == "" {
		return Article{}, false
	}
	for _, a := range all {
		if Fold(a.De) == t || Fold(a.En) == t {
			return a, true
		}
	}
	for _, a := range all {
		if a.Named(text) {
			return a, true
		}
	}
	return Article{}, false
}

func isVarietySep(r rune) bool {
	return r == ' ' || r == '\t' || r == ',' || r == '·' || r == '-' || r == ':'
}

// LeadingName splits a typed text that starts with an article's name (the
// longest name wins) and goes on after a space or one of , · - : into the
// article and the rest, which is a variety: "Mehl Type 405" is flour and
// "Type 405". The separators are trimmed off the rest. A text that is only
// the name, or goes on without a separator ("Mehltau"), has none. A text
// that is itself an article's name ("Milk chocolate") is not split either.
func LeadingName(typed string, all []Article) (Article, string, bool) {
	if _, ok := namedBy(typed, all); ok {
		return Article{}, "", false
	}
	rs := []rune(strings.TrimSpace(typed))
	var best Article
	var rest string
	found := false
	for i := 1; i < len(rs); i++ {
		if !isVarietySep(rs[i]) {
			continue
		}
		r := strings.TrimFunc(string(rs[i:]), isVarietySep)
		if r == "" {
			continue
		}
		if a, ok := namedBy(strings.TrimFunc(string(rs[:i]), isVarietySep), all); ok {
			best, rest, found = a, r, true // a later cut is a longer name
		}
	}
	return best, rest, found
}
