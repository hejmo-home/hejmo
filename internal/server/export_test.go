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

// CountRecoveryVerifies counts recovery code verifications until the returned
// function is called, which stops counting and returns the number so far.
// Tests use it to check that sign-in does the same work whatever the outcome.
func CountRecoveryVerifies() func() int {
	n := 0
	orig := verifyRecoveryCode
	verifyRecoveryCode = func(hash, code string) bool { n++; return orig(hash, code) }
	return func() int { verifyRecoveryCode = orig; return n }
}

// RenderOTPPreview renders the real /otp or recovery codes page template for
// the design preview. state is "form" (two other parents), "error" (wrong
// confirmation at the first parent), "done" (the first parent was reset) or
// "codes" (the recovery codes page).
func RenderOTPPreview(lang, state string) (string, error) {
	bundle := i18n.MustLoad()
	tpl, err := parsePages().Clone()
	if err != nil {
		return "", err
	}
	tpl = tpl.Funcs(template.FuncMap{"t": func(key string) string { return bundle.T(lang, key) }})
	fr := frame{SignedIn: true}
	for _, it := range navItems {
		fr.Items = append(fr.Items, navLink{it.Href, it.Label, false})
	}
	data := map[string]any{"Lang": lang, "Frame": fr}
	name := "otp.html"
	if state == "codes" {
		name = "recovery_codes.html"
		data["Codes"] = []string{"K7QD2-M9XPA", "H3WTR-4NCEF", "Y8JMB-6ZQKD", "P2VXA-7RTGH", "C9DNE-3KWLU", "T5FQH-8MYBJ", "X4ZRG-2PVDS", "N6BKA-9HJTC", "E7UWM-5QXFL", "R3LGP-4CNYZ"}
	} else {
		v := otpPage{Unused: 7, Parents: []otherParent{{"papa", "Papa", "papa"}, {"oma", "Oma", "oma"}}}
		switch state {
		case "error":
			v.Error, v.ErrorFor = "otp.error.confirm", "papa"
		case "done":
			v.ResetName = "Papa"
			v.DoneText = bundle.Format(lang, i18n.Msg("otp.reset.done", "name", "Papa"))
			v.Parents = v.Parents[1:]
		}
		v.LeftText = bundle.Format(lang, i18n.Msg("otp.recovery.left", "count", "7"))
		data["Page"] = v
	}
	var b strings.Builder
	if err := tpl.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}
