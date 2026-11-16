package server

import (
	"html/template"
	"strings"
	"testing"

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

// CountRecoveryVerifies counts recovery code verifications until the returned
// function is called, which stops counting and returns the number so far.
// Tests use it to check that sign-in does the same work whatever the outcome.
func CountRecoveryVerifies() func() int {
	n := 0
	orig := verifyRecoveryCode
	verifyRecoveryCode = func(hash, code string) bool { n++; return orig(hash, code) }
	return func() int { verifyRecoveryCode = orig; return n }
}

// RenderPersonalView renders the markup of a personal view model in a language,
// with the template the display page uses. The design preview calls it.
func RenderPersonalView(lang string, v familyview.PersonalView) (string, error) {
	bundle := i18n.MustLoad()
	tpl, err := parsePages().Clone()
	if err != nil {
		return "", err
	}
	tpl = tpl.Funcs(template.FuncMap{"t": func(key string) string { return bundle.T(lang, key) }})
	var b strings.Builder
	if err := tpl.ExecuteTemplate(&b, "personalView", v); err != nil {
		return "", err
	}
	return b.String(), nil
}

// CountLanguageLookups counts page language lookups until the returned
// function is called, which stops counting and returns the number so far.
func CountLanguageLookups(t *testing.T) func() int {
	n := 0
	languageLookups = func() { n++ }
	t.Cleanup(func() { languageLookups = func() {} })
	return func() int { languageLookups = func() {}; return n }
}
