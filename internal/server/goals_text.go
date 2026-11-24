package server

import (
	"html"
	"html/template"
	"strconv"

	"github.com/hejmo-home/hejmo/internal/i18n"
)

// formatHTML is tf for a text that holds its own markup (a bold name): the
// values are escaped, the text of the locale is trusted.
func formatHTML(b *i18n.Bundle, lang, key string, kv ...string) template.HTML {
	esc := make([]string, len(kv))
	for i, v := range kv {
		if i%2 == 1 {
			v = html.EscapeString(v)
		}
		esc[i] = v
	}
	return template.HTML(b.Format(lang, i18n.Msg(key, esc...))) // #nosec G203 -- values escaped above, the text is ours
}

// minus is the sign of a negative number of stars (U+2212, as the design shows it).
const minus = "−"

// signed writes n with a real minus sign.
func signed(n int) string {
	if n < 0 {
		return minus + strconv.Itoa(-n)
	}
	return strconv.Itoa(n)
}

// pluralHTML picks key+".one" for 1 and -1, else key+".other", and fills {n}.
func pluralHTML(b *i18n.Bundle, lang, key string, n int) template.HTML {
	form := ".other"
	if n == 1 || n == -1 {
		form = ".one"
	}
	return formatHTML(b, lang, key+form, "n", signed(n))
}
