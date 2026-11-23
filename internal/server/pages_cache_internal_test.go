package server

import (
	"html/template"
	"io"
	"testing"

	"github.com/hejmo-home/hejmo/internal/i18n"
)

// The cache of language template sets is keyed by the source set too, so a
// server given other pages never receives another server's cached copy.
func TestPagesForKeepsServersWithOtherPagesApart(t *testing.T) {
	bundle := i18n.MustLoad()
	a := &server{pages: template.Must(template.New("x").Parse(`A`)), i18n: bundle}
	b := &server{pages: template.Must(template.New("x").Parse(`B`)), i18n: bundle}
	ta, err := a.pagesFor("en")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := b.pagesFor("en")
	if err != nil {
		t.Fatal(err)
	}
	if ta == tb {
		t.Fatal("two servers with different pages share one cached template set")
	}
}

// Rendering only executes cached copies: the shared parsed set stays unexecuted
// and can still be cloned afterwards, process-wide.
func TestRenderingLeavesTheSharedPageSetCloneable(t *testing.T) {
	s := &server{pages: parsePages(), i18n: i18n.MustLoad()}
	// The execute error is ignored on purpose: with nil data the page may fail
	// midway; what matters is only that it ran on a cached copy, not on the shared set.
	_ = s.execute(io.Discard, "familyView", "en", nil)
	if _, err := parsePages().Clone(); err != nil {
		t.Fatalf("shared page set no longer cloneable: %v", err)
	}
}
