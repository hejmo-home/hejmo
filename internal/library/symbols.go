package library

import (
	_ "embed"
	"slices"
	"strings"
	"sync"
)

// Symbol is an emoji of the Noto Emoji set with its name in both languages.
type Symbol struct{ Emoji, De, En string }

//go:embed symbols.tsv
var symbolsText string

var (
	symbolsOnce sync.Once
	symbols     []Symbol
	symbolByKey map[string]Symbol
)

// CanonEmoji removes the variation selector, so "🌶" and "🌶️" are one symbol.
func CanonEmoji(s string) string { return strings.ReplaceAll(s, "️", "") }

func loadSymbols() {
	symbolByKey = map[string]Symbol{}
	for _, line := range strings.Split(symbolsText, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			panic("library: malformed line in symbols.tsv: " + line)
		}
		s := Symbol{f[0], f[1], f[2]}
		symbols = append(symbols, s)
		symbolByKey[CanonEmoji(s.Emoji)] = s
	}
}

// SymbolCount is the number of symbols a parent can choose from.
func SymbolCount() int { symbolsOnce.Do(loadSymbols); return len(symbols) }

// SymbolByEmoji finds a symbol by its emoji, with or without the variation selector.
func SymbolByEmoji(e string) (Symbol, bool) {
	symbolsOnce.Do(loadSymbols)
	s, ok := symbolByKey[CanonEmoji(e)]
	return s, ok
}

// SymbolName is the name of a symbol in the language of the page.
func SymbolName(s Symbol, lang string) string {
	if lang == "en" {
		return s.En
	}
	return s.De
}

// SearchSymbols finds the symbols whose German or English name holds q, ü = u.
// The names in the language of the page come first.
func SearchSymbols(q, lang string) []Symbol {
	symbolsOnce.Do(loadSymbols)
	q = Fold(q)
	if q == "" {
		return nil
	}
	var first, second []Symbol
	for _, s := range symbols {
		de, en := strings.Contains(Fold(s.De), q), strings.Contains(Fold(s.En), q)
		switch {
		case (lang == "en" && en) || (lang != "en" && de):
			first = append(first, s)
		case de || en:
			second = append(second, s)
		}
	}
	return append(first, second...)
}

// DeptSymbols are the symbols to suggest for a department: its own, then those
// of the shipped articles in it, each once.
func DeptSymbols(dept string) []string {
	out := []string{DeptSymbol(dept)}
	for _, a := range Shipped() {
		if a.Dept != dept || a.Symbol == "" {
			continue
		}
		if !slices.ContainsFunc(out, func(e string) bool { return CanonEmoji(e) == CanonEmoji(a.Symbol) }) {
			out = append(out, a.Symbol)
		}
	}
	return out
}
