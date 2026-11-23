// Package release tests the contract of the release and preview workflows in
// .github/workflows (ADR 0018): what a tag publishes, what a main build must
// never look like. The real proof is a test tag run; these tests keep the
// guardrails from drifting.
package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"
)

func load(t *testing.T, name string) (map[string]any, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not valid YAML: %v", name, err)
	}
	return doc, string(raw)
}

// triggers returns the "on" block (YAML 1.2 keeps the key as a string).
func triggers(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	on, ok := doc["on"].(map[string]any)
	if !ok {
		t.Fatalf("workflow has no map-valued on: block: %v", doc["on"])
	}
	return on
}

func strs(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			out = append(out, x.(string))
		}
	}
	return out
}

func TestReleaseRunsOnVersionTagsOnly(t *testing.T) {
	doc, _ := load(t, "release.yml")
	push := triggers(t, doc)["push"].(map[string]any)
	if _, ok := push["branches"]; ok {
		t.Error("release must not run on branch pushes")
	}
	tags := strs(push["tags"])
	if len(tags) == 0 {
		t.Fatal("release has no tag filter")
	}
	for _, p := range tags {
		if !strings.HasPrefix(p, "v") {
			t.Errorf("tag pattern %q does not start with v", p)
		}
	}
	if len(triggers(t, doc)) != 1 {
		t.Error("release must have the tag push as its only trigger")
	}
}

func TestReleaseBuildsSignsAndPublishes(t *testing.T) {
	doc, raw := load(t, "release.yml")
	// Write permissions are scoped to the jobs that need them, not the workflow.
	if top, _ := doc["permissions"].(map[string]any); top["packages"] != nil || top["id-token"] != nil {
		t.Error("top-level permissions must stay read-only; scope writes to jobs")
	}
	granted := map[string]bool{}
	for _, j := range doc["jobs"].(map[string]any) {
		perms, _ := j.(map[string]any)["permissions"].(map[string]any)
		for k, v := range perms {
			if v == "write" {
				granted[k] = true
			}
		}
	}
	for _, p := range []string{"packages", "id-token", "contents"} {
		if !granted[p] {
			t.Errorf("no job has %s: write (needed for ghcr push, keyless signing, release)", p)
		}
	}
	for _, want := range []string{
		"linux/amd64,linux/arm64",             // both architectures
		"ghcr.io",                             // registry
		"cosign sign",                         // keyless signing
		"cosign verify",                       // the pipeline proves its own signature
		"--certificate-identity",              // verification pinned to this workflow
		"release.yml@refs/tags/",              // identity is the release workflow on a tag
		"--certificate-oidc-issuer",           // GitHub OIDC
		"token.actions.githubusercontent.com", // issuer value
		"gh release create",                   // release notes
		"--generate-notes",                    // merged PRs since last tag
		"--verify-tag",                        // never creates a tag itself
		"--prerelease",                        // suffix tags are pre-releases
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}
	if strings.Contains(raw, "--yes=false") || strings.Contains(raw, "COSIGN_PRIVATE_KEY") {
		t.Error("signing must be keyless")
	}
}

func TestPreviewRunsOnMainAndNeverLooksLikeARelease(t *testing.T) {
	doc, raw := load(t, "preview.yml")
	on := triggers(t, doc)
	push := on["push"].(map[string]any)
	if got := strs(push["branches"]); len(got) != 1 || got[0] != "main" {
		t.Errorf("preview branches = %v, want [main]", got)
	}
	if _, ok := push["tags"]; ok {
		t.Error("preview must not run on tags")
	}
	for _, bad := range []string{"value=latest", "latest=true", "latest=auto", "type=semver", "type=ref,event=tag", "gh release", "cosign sign"} {
		if strings.Contains(raw, bad) {
			t.Errorf("preview.yml must not contain %q", bad)
		}
	}
	if !strings.Contains(raw, "flavor: latest=false") {
		t.Error("preview metadata step must set flavor latest=false")
	}
	conc, _ := doc["concurrency"].(map[string]any)
	if conc == nil || conc["cancel-in-progress"] != true {
		t.Error("preview needs a concurrency group with cancel-in-progress so :preview never lands on an older commit")
	}
	if !strings.Contains(raw, "preview-") {
		t.Error("preview images must carry a preview- tag")
	}
	if !strings.Contains(raw, "linux/amd64,linux/arm64") {
		t.Error("preview builds both architectures")
	}
}

// Expressions must not be interpolated into run: scripts (script injection);
// they go through env instead.
func TestReleaseRunScriptsDoNotInterpolateRefName(t *testing.T) {
	_, raw := load(t, "release.yml")
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "TAG:") {
			continue
		}
		if strings.Contains(line, "github.ref_name") {
			t.Errorf("github.ref_name used outside env: %q", trimmed)
		}
	}
}

// Ticket 221: a published version also carries one update file per
// architecture, for boxes in offline mode. The workflow builds them from the
// image it signed, signs the manifest with the same identity, checks the file
// as the box and the host script will, and attaches it to the release.
func TestReleaseBuildsSignedUpdateFilesForPublishedVersionsOnly(t *testing.T) {
	doc, raw := load(t, "release.yml")
	jobs := doc["jobs"].(map[string]any)
	job, ok := jobs["update-files"].(map[string]any)
	if !ok {
		t.Fatal("release.yml has no job update-files")
	}
	if !strings.Contains(job["if"].(string), "contains(github.ref, '-')") {
		t.Errorf("update files are for published versions only, if = %v", job["if"])
	}
	perms := job["permissions"].(map[string]any)
	if perms["id-token"] != "write" || perms["contents"] != "read" {
		t.Errorf("the job signs keyless and writes nothing else: %v", perms)
	}
	var arches []string
	for _, a := range job["strategy"].(map[string]any)["matrix"].(map[string]any)["arch"].([]any) {
		arches = append(arches, a.(string))
	}
	if len(arches) != 2 || arches[0] != "arm64" || arches[1] != "amd64" {
		t.Errorf("matrix = %v, want both architectures", arches)
	}
	for _, want := range []string{
		"cosign-release: v2.5.3",                           // the version the host script checks with
		"cosign sign-blob --yes --new-bundle-format",       // keyless, in the format both checks read
		"cosign verify-blob",                               // the pipeline checks its own signature as the host script will
		"--trusted-root internal/update/trusted_root.json", // with the root the install script keeps
		"go run ./tools/mkupdate check",                    // and as the box does
		"skopeo copy --override-os linux --override-arch",  // the image of that architecture, from the signed digest
		"hejmo-$VERSION-$ARCH.update",                      // the name the page tells parents to download
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}
	notes := jobs["notes"].(map[string]any)
	needs := strs(notes["needs"])
	if len(needs) != 2 || needs[0] != "image" || needs[1] != "update-files" {
		t.Errorf("the release waits for the update files: needs = %v", needs)
	}
	if !strings.Contains(raw, "$flags $files") {
		t.Error("the release does not carry the update files")
	}
	// The signer of the update files is the signer of the image: one identity to trust.
	for _, name := range []string{"image", "update-files"} {
		if p, _ := jobs[name].(map[string]any)["permissions"].(map[string]any); p["id-token"] != "write" {
			t.Errorf("job %s does not sign", name)
		}
	}
}
