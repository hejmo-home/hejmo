package install

import (
	"encoding/json"
	"os"
	"regexp"
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
			[]string{"## Requirements", "## Install", "## First steps", "## HTTPS", "## Trust the box's own CA", "## Away from home: WireGuard with the FRITZ!Box", "## Displays and phones", "## Automatic updates"},
			[]string{"experimental", "not yet tested on E-Ink devices", "tested in emulation", "03:30", "cosign", "notify only"}},
		"de": {"docs/install.de.md",
			[]string{"## Voraussetzungen", "## Installation", "## Erste Schritte", "## HTTPS", "## Der eigenen CA vertrauen", "## Von unterwegs: WireGuard mit der FRITZ!Box", "## Displays und Handys", "## Automatische Updates"},
			[]string{"experimentell", "noch nicht auf E-Ink-Geräten getestet", "in der Emulation getestet", "03:30", "cosign", "nur benachrichtigen"}},
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

// Ticket 216 (decision Q92): the chapter the "Unterwegs?" links of the web
// interface lead to.
var (
	headingAnchorDrop = regexp.MustCompile(`[^a-z0-9 \-äöüß]`)
	bareAddress       = regexp.MustCompile(`https?://[^\s)\]>]+`)
	markdownLink      = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`)
)

// anchorOf is the anchor GitHub gives a heading.
func anchorOf(heading string) string {
	return strings.ReplaceAll(headingAnchorDrop.ReplaceAllString(strings.ToLower(strings.TrimSpace(strings.TrimLeft(heading, "# "))), ""), " ", "-")
}

// chapter returns the text of the chapter with this heading, up to the next "## ".
func chapter(t *testing.T, body, heading string) string {
	t.Helper()
	i := strings.Index(body, heading+"\n")
	if i < 0 {
		t.Fatalf("no chapter %q", heading)
	}
	rest := body[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func TestWireGuardChapterIsInBothGuidesAndEveryAddressIsALink(t *testing.T) {
	for _, g := range []struct{ file, heading string }{
		{"docs/install.md", "## Away from home: WireGuard with the FRITZ!Box"},
		{"docs/install.de.md", "## Von unterwegs: WireGuard mit der FRITZ!Box"},
	} {
		text := chapter(t, read(t, g.file), g.heading)
		for _, want := range []string{"WireGuard", "FRITZ!OS 7.50", "(http://fritz.box)", "(https://apps.apple.com/app/wireguard/id1441195209)", "(https://play.google.com/store/apps/details?id=com.wireguard.android)", "hejmo.fritz.box"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the chapter lacks %q", g.file, want)
			}
		}
		if len(markdownLink.FindAllString(text, -1)) < 4 {
			t.Errorf("%s: the chapter has fewer than four links", g.file)
		}
		if bare := bareAddress.FindAllString(markdownLink.ReplaceAllString(text, ""), -1); len(bare) > 0 {
			t.Errorf("%s: addresses outside a link, every address must be clickable: %v", g.file, bare)
		}
		// The chapter sits before "Displays and phones", behind the trust chapter it refers to.
		body := read(t, g.file)
		if strings.Index(body, g.heading) < strings.Index(body, "## Trust the box's own CA\n")+strings.Index(body, "## Der eigenen CA vertrauen\n")+1 {
			t.Errorf("%s: the chapter is not behind the trust chapter", g.file)
		}
	}
}

// Product owner's addition on #216 (17/11/2026): the chapter says how to
// recognise a shared address (DS-Lite, CGNAT), that IPv6 often still works,
// and else to ask the provider for a public IPv4 address.
func TestWireGuardChapterExplainsSharedAddresses(t *testing.T) {
	for _, g := range []struct {
		file, heading string
		want          []string
	}{
		{"docs/install.md", "## Away from home: WireGuard with the FRITZ!Box",
			[]string{"Online Monitor", "DS-Lite", "100.64.0.0/10", "IPv6", "public IPv4 address"}},
		{"docs/install.de.md", "## Von unterwegs: WireGuard mit der FRITZ!Box",
			[]string{"Online-Monitor", "DS-Lite", "100.64.0.0/10", "IPv6", "öffentlichen IPv4-Adresse"}},
	} {
		text := chapter(t, read(t, g.file), g.heading)
		for _, w := range g.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s: the chapter lacks %q", g.file, w)
			}
		}
	}
}

// The links of the web interface point at the chapter's anchor, in the guide of the same language.
func TestTheWebInterfaceLinksToTheWireGuardChapter(t *testing.T) {
	for lang, g := range map[string]struct{ file, heading string }{
		"de": {"docs/install.de.md", "## Von unterwegs: WireGuard mit der FRITZ!Box"},
		"en": {"docs/install.md", "## Away from home: WireGuard with the FRITZ!Box"},
	} {
		var locale map[string]string
		if err := json.Unmarshal([]byte(read(t, "internal/i18n/locales/"+lang+".json")), &locale); err != nil {
			t.Fatal(err)
		}
		want := "https://github.com/hejmo-home/hejmo/blob/main/" + g.file + "#" + anchorOf(g.heading)
		if got := locale["https.vpn.url"]; got != want {
			t.Errorf("%s: https.vpn.url = %q, want %q", lang, got, want)
		}
	}
	if got := anchorOf("## Von unterwegs: WireGuard mit der FRITZ!Box"); got != "von-unterwegs-wireguard-mit-der-fritzbox" {
		t.Errorf("anchor = %q", got)
	}
}
