// Package securitydocs holds tests that keep docs/security/threat-model.md and
// the security jobs in .github/workflows/ci.yml honest (ADR 0018, Measurement).
package securitydocs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
)

func root(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..")
}

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type workflow struct {
	Jobs map[string]struct {
		ContinueOnError bool `yaml:"continue-on-error"`
		Steps           []struct {
			Name            string `yaml:"name"`
			Run             string `yaml:"run"`
			Uses            string `yaml:"uses"`
			ContinueOnError bool   `yaml:"continue-on-error"`
			With            map[string]any
		}
	}
}

func loadWorkflow(t *testing.T) workflow {
	t.Helper()
	var w workflow
	if err := yaml.Unmarshal([]byte(read(t, ".github/workflows/ci.yml")), &w); err != nil {
		t.Fatal(err)
	}
	return w
}

// Acceptance: CI runs govulncheck, gosec and a container image scan, and none
// of them may be advisory (continue-on-error would let a finding pass).
func TestCISecurityJobsExistAndFail(t *testing.T) {
	w := loadWorkflow(t)
	for job, mustRun := range map[string]string{
		"govulncheck": "govulncheck",
		"gosec":       "gosec",
		"image-scan":  "trivy",
	} {
		j, ok := w.Jobs[job]
		if !ok {
			t.Errorf("ci.yml has no job %q", job)
			continue
		}
		if j.ContinueOnError {
			t.Errorf("job %q must not be continue-on-error", job)
		}
		found := false
		for _, s := range j.Steps {
			if s.ContinueOnError {
				t.Errorf("job %q: step %q must not be continue-on-error", job, s.Name)
			}
			if strings.Contains(s.Run, mustRun) || strings.Contains(s.Uses, mustRun) {
				found = true
			}
		}
		if !found {
			t.Errorf("job %q never runs %s", job, mustRun)
		}
	}
}

// The agreed thresholds are written into the workflow, not left to defaults.
func TestCIThresholds(t *testing.T) {
	ci := read(t, ".github/workflows/ci.yml")
	for _, want := range []string{
		"-severity high",          // gosec: high severity fails the build
		"severity: HIGH,CRITICAL", // trivy
		"exit-code: \"1\"",
	} {
		if !strings.Contains(ci, want) {
			t.Errorf("ci.yml lacks threshold %q", want)
		}
	}
}

var (
	threatHead = regexp.MustCompile(`(?m)^### (T\d+)\. (.+)$`)
	checkLine  = regexp.MustCompile(`(?m)^- \*\*(Check|Accepted risk):\*\* (.+)$`)
	ciRef      = regexp.MustCompile("`ci:([a-z-]+)`")
	testRef    = regexp.MustCompile("`test:([^`]+)`")
	issueRef   = regexp.MustCompile(`#\d+`)
)

// Acceptance: docs/security/threat-model.md exists and each threat names its
// check or accepted risk.
func TestThreatModel(t *testing.T) {
	doc := read(t, "docs/security/threat-model.md")
	w := loadWorkflow(t)

	for _, area := range []string{"Home network", "Displays", "Phones", "DNS records at deSEC", "Auto-update script", "MCP"} {
		if !regexp.MustCompile(`(?m)^## ` + regexp.QuoteMeta(area) + `\s*$`).MatchString(doc) {
			t.Errorf("threat model has no section %q", area)
		}
	}

	heads := threatHead.FindAllStringSubmatchIndex(doc, -1)
	if len(heads) < 6 {
		t.Fatalf("expected at least one threat per area, found %d", len(heads))
	}
	for i, h := range heads {
		id := doc[h[2]:h[3]]
		end := len(doc)
		if i+1 < len(heads) {
			end = heads[i+1][0]
		}
		body := doc[h[1]:end]
		// An area heading ends a threat's body.
		if k := strings.Index(body, "\n## "); k >= 0 {
			body = body[:k]
		}
		m := checkLine.FindAllStringSubmatch(body, -1)
		if len(m) == 0 {
			t.Errorf("%s names neither a Check nor an Accepted risk", id)
			continue
		}
		for _, line := range m {
			text := line[2]
			if line[1] == "Accepted risk" {
				if len(strings.Fields(text)) < 5 {
					t.Errorf("%s: accepted risk needs a reason", id)
				}
				continue
			}
			refs := 0
			for _, r := range ciRef.FindAllStringSubmatch(text, -1) {
				refs++
				if _, ok := w.Jobs[r[1]]; !ok {
					t.Errorf("%s: ci job %q does not exist", id, r[1])
				}
			}
			for _, r := range testRef.FindAllStringSubmatch(text, -1) {
				refs++
				if _, err := os.Stat(filepath.Join(root(t), r[1])); err != nil {
					t.Errorf("%s: test file %q does not exist", id, r[1])
				}
			}
			// A check that does not exist yet must be tracked by a ticket.
			if issueRef.MatchString(text) {
				refs++
			}
			if refs == 0 {
				t.Errorf("%s: check %q names no CI job, test or ticket", id, text)
			}
		}
	}
}
