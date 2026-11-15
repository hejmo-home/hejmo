package server

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// wizardSteps is the number of named steps of the first setup (ticket 133):
// secure connection, your account, one-time code, first steps.
const wizardSteps = 4

// wizardStep is one part of the progress bar.
type wizardStep struct {
	N     int
	Label string // key in the locales
	State string // done, current or open
}

// wizardView is the wizard shell around a page of the first setup: the brand
// bar, "Setup · Step n of 4" and the progress of the four steps. Step 0 is the
// welcome page, which has no step number yet; -1 (see wizardPlain) is the
// shell without progress, used when a parent sets up their one-time codes again.
type wizardView struct {
	Step  int
	Steps []wizardStep
}

// wizardAt returns the shell for step n of the first setup.
func wizardAt(n int) *wizardView {
	w := &wizardView{Step: n}
	if n < 1 {
		return w
	}
	labels := []string{"setup.wizard.step.1", "setup.wizard.step.2", "setup.wizard.step.3", "setup.wizard.step.4"}
	for i, l := range labels {
		st := wizardStep{N: i + 1, Label: l, State: "open"}
		switch {
		case i+1 < n:
			st.State = "done"
		case i+1 == n:
			st.State = "current"
		}
		w.Steps = append(w.Steps, st)
	}
	return w
}

var (
	richPath = regexp.MustCompile(`\[\[(.+?)\]\]`)
	richBold = regexp.MustCompile(`\*\*(.+?)\*\*`)
	richLink = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s)]+)\)`)
)

// rich turns a text of the locales into HTML: everything is escaped first,
// then [[a › b]] becomes a menu path, **x** bold text and [text](https://…) a
// link that opens in a new tab. Nothing from a request reaches it unescaped.
func rich(s string) template.HTML {
	s = html.EscapeString(s)
	s = richPath.ReplaceAllString(s, `<span class="path">$1</span>`)
	s = richBold.ReplaceAllString(s, `<b>$1</b>`)
	s = richLink.ReplaceAllString(s, `<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>`)
	return template.HTML(strings.TrimSpace(s)) // #nosec G203 -- escaped above
}
