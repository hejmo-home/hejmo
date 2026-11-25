package recipe

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hejmo-home/hejmo/internal/library"
)

// Proposal is an article of the library that may be what an imported line means, with the rest of the
// text as its variety ("rote Linsen" is Linsen · rot).
type Proposal struct {
	Article library.Article
	Variety string
}

// Propose offers up to max articles whose name occurs in the text, singular or plural, ignoring case and
// umlauts, as a word or as the end of a compound ("Naturjoghurt" is Joghurt · Natur). The longest names come
// first. The rest of the text is the variety; a German adjective ending is taken off ("rote" is "rot").
func Propose(text string, all []library.Article, lang string, max int) []Proposal {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return nil
	}
	l := library.German
	if lang == "en" {
		l = library.English
	}
	type hit struct {
		p   Proposal
		len int
	}
	var hits []hit
	for _, a := range all {
		names := []string{a.De, a.En}
		if lang == "en" {
			names = []string{a.En, a.De}
		}
		for _, name := range names {
			if name == "" {
				continue
			}
			if v, ok := varietyOf(text, name, l, lang); ok {
				if utf8.RuneCountInString(v) > MaxVariety {
					v = ""
				}
				hits = append(hits, hit{Proposal{a, v}, utf8.RuneCountInString(name)})
				break
			}
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return b.len - a.len })
	var out []Proposal
	for _, h := range hits {
		if len(out) == max {
			break
		}
		out = append(out, h.p)
	}
	return out
}

// varietyOf finds name in text: as the words of the name, or as the end of one word. It returns the rest.
func varietyOf(text, name string, l library.Lang, lang string) (string, bool) {
	words := strings.Fields(text)
	nameWords := strings.Fields(name)
	// the words of the name, in a row
	for i := 0; i+len(nameWords) <= len(words); i++ {
		ok := true
		for j, nw := range nameWords {
			w := strings.Trim(words[i+j], ",;:()")
			if j == len(nameWords)-1 {
				ok = ok && library.SameName(w, nw, l)
			} else {
				ok = ok && library.Fold(w) == library.Fold(nw)
			}
		}
		if ok {
			rest := slices.Concat(words[:i], words[i+len(nameWords):])
			return restText(rest, lang), true
		}
	}
	// the end of a compound word
	if len(nameWords) == 1 && utf8.RuneCountInString(name) >= 4 {
		fn := library.Fold(name)
		for i, w := range words {
			wr := []rune(strings.Trim(w, ",;:()"))
			fw := library.Fold(string(wr))
			if len(fw) > len(fn) && strings.HasSuffix(fw, fn) && len([]rune(fw)) == len(wr) {
				prefix := string(wr[:len(wr)-utf8.RuneCountInString(name)])
				prefix = strings.TrimRight(prefix, "-")
				if prefix == "" {
					continue
				}
				rs := []rune(prefix)
				rs[0] = unicode.ToUpper(rs[0])
				rest := slices.Concat(words[:i], words[i+1:])
				return strings.TrimSpace(strings.Join(append([]string{string(rs)}, rest...), " ")), true
			}
		}
	}
	return "", false
}

// restText is what is left of the text as a variety. In German a lone adjective loses its ending.
func restText(rest []string, lang string) string {
	for i, w := range rest {
		rest[i] = strings.Trim(w, ",;:")
	}
	if lang != "en" && len(rest) == 1 {
		w := rest[0]
		for _, end := range []string{"en", "er", "es", "em", "e"} {
			if cut, ok := strings.CutSuffix(w, end); ok && utf8.RuneCountInString(cut) >= 3 && utf8.RuneCountInString(w) <= 6 {
				return cut
			}
		}
	}
	return strings.Join(rest, " ")
}
