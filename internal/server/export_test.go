package server

import (
	"html/template"
	"strings"

	"github.com/hejmo-home/hejmo/internal/familyview"
	"github.com/hejmo-home/hejmo/internal/i18n"
)

// RenderFamilyView renders the markup of a family view model in a language,
// with the template the display page uses. Tests and the design preview call it.
func RenderFamilyView(lang string, v familyview.View) (string, error) {
	bundle := i18n.MustLoad()
	tpl, err := parsePages().Clone()
	if err != nil {
		return "", err
	}
	tpl = tpl.Funcs(template.FuncMap{"t": func(key string) string { return bundle.T(lang, key) }})
	var b strings.Builder
	if err := tpl.ExecuteTemplate(&b, "familyView", v); err != nil {
		return "", err
	}
	return b.String(), nil
}
