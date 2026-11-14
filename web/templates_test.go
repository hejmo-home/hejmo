package web_test

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/web"
)

// 5.11: all user-facing text comes from the locales; templates hold markup
// and {{...}} actions only.
func TestTemplatesHoldNoLiteralText(t *testing.T) {
	action := regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	tag := regexp.MustCompile(`(?s)<[^>]*>`)
	files, err := fs.Glob(web.Templates, "templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("no templates: %v", err)
	}
	for _, f := range files {
		raw, _ := fs.ReadFile(web.Templates, f)
		text := tag.ReplaceAllString(action.ReplaceAllString(string(raw), ""), "")
		// The product name and separators like "·" are not translatable text.
		text = strings.NewReplacer("Hejmo", "", "·", "", "…", "", "&nbsp;", "").Replace(text)
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) != "" {
				t.Errorf("%s has literal text: %q", f, strings.TrimSpace(line))
			}
		}
	}
}
