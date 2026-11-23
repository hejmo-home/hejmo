package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hejmo-home/hejmo/internal/update"
	"github.com/hejmo-home/hejmo/internal/update/updatetest"
)

// The three steps of the release workflow, with the virtual Sigstore as cosign.
func TestTheReleaseStepsMakeAFileTheBoxTakes(t *testing.T) {
	dir := t.TempDir()
	at := func(n string) string { return filepath.Join(dir, n) }
	if err := os.WriteFile(at("image.tar"), []byte("layers of an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"manifest", "-version", "0.2.4", "-arch", "arm64", "-image", at("image.tar"), "-released", "2026-11-20", "-out", at("hejmo-update.json")}, &out, nil); err != nil {
		t.Fatal(err)
	}
	manifest, _ := os.ReadFile(at("hejmo-update.json"))
	sig := updatetest.New(t)
	// cosign sign-blob signs exactly these bytes.
	if err := os.WriteFile(at("hejmo-update.sigstore.json"), sig.Bundle(t, update.SignerPrefix+"0.2.4", update.SignerIssuer, manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"pack", "-manifest", at("hejmo-update.json"), "-bundle", at("hejmo-update.sigstore.json"), "-image", at("image.tar"), "-out", at("hejmo-0.2.4-arm64.update")}, &out, nil); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"check", "-arch", "arm64", at("hejmo-0.2.4-arm64.update")}, &out, sig.Verifier(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Hejmo 0.2.4 for arm64") || !strings.Contains(out.String(), update.SignerPrefix+"0.2.4") {
		t.Errorf("output: %s", out.String())
	}
	// Checked as the other kind of box, the file is refused.
	if err := run([]string{"check", "-arch", "amd64", at("hejmo-0.2.4-arm64.update")}, &out, sig.Verifier(t)); err == nil {
		t.Error("a file for another architecture passed the check")
	}
	// The box's own root does not know this Sigstore: a release is checked against the real one.
	if err := run([]string{"check", "-arch", "arm64", at("hejmo-0.2.4-arm64.update")}, &out, nil); err == nil {
		t.Error("a file signed by a foreign Sigstore passed the box's own root")
	}
	if err := run([]string{"manifest", "-version", "0.2", "-arch", "arm64", "-image", at("image.tar"), "-out", at("x.json")}, &out, nil); err == nil {
		t.Error("a version without three numbers was taken")
	}
	if err := run(nil, &out, nil); err == nil {
		t.Error("no command was accepted")
	}
}
