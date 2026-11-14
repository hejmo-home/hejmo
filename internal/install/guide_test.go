package install

import (
	"os"
	"strings"
	"testing"
)

func read(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile("../../" + rel)
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(data)
}

const installCommand = "curl -fsSL https://raw.githubusercontent.com/hejmo-home/hejmo/main/install.sh | sudo bash"

func TestInstallGuideExistsInBothLanguagesAndIsLinkedFromReadme(t *testing.T) {
	readme := read(t, "README.md")
	guides := map[string]struct {
		file     string
		sections []string // one heading per topic the ticket asks for
		notes    []string // the honesty markers of ADR 0018
	}{
		"en": {"docs/install.md",
			[]string{"## Requirements", "## Install", "## First steps", "## HTTPS", "## Trust the box's own CA", "## Displays and phones"},
			[]string{"experimental", "not yet tested on E-Ink devices", "tested in emulation"}},
		"de": {"docs/install.de.md",
			[]string{"## Voraussetzungen", "## Installation", "## Erste Schritte", "## HTTPS", "## Der eigenen CA vertrauen", "## Displays und Handys"},
			[]string{"experimentell", "noch nicht auf E-Ink-Geräten getestet", "in der Emulation getestet"}},
	}
	for lang, g := range guides {
		t.Run(lang, func(t *testing.T) {
			if !strings.Contains(readme, "("+g.file+")") {
				t.Errorf("README.md does not link %s", g.file)
			}
			body := read(t, g.file)
			if !strings.Contains(body, installCommand) {
				t.Errorf("%s lacks the install command %q", g.file, installCommand)
			}
			for _, h := range g.sections {
				if !strings.Contains(body, h+"\n") {
					t.Errorf("%s lacks the heading %q", g.file, h)
				}
			}
			for _, n := range g.notes {
				if !strings.Contains(body, n) {
					t.Errorf("%s lacks the statement %q", g.file, n)
				}
			}
		})
	}
}

func TestGuidesPointToEachOther(t *testing.T) {
	if !strings.Contains(read(t, "docs/install.md"), "(install.de.md)") {
		t.Error("docs/install.md does not link the German guide")
	}
	if !strings.Contains(read(t, "docs/install.de.md"), "(install.md)") {
		t.Error("docs/install.de.md does not link the English guide")
	}
}
