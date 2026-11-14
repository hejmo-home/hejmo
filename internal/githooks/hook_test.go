// Package githooks tests the repository's git hooks in .githooks.
package githooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Mama Test", "GIT_COMMITTER_EMAIL=mama@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func signOffs(t *testing.T, dir, rev string) int {
	return strings.Count(git(t, dir, "log", "-1", "--format=%B", rev), "Signed-off-by: Mama Test <mama@example.com>")
}

func TestHookSignsOffEveryCommitOnceIncludingMergesAndWorktrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	hooks, err := filepath.Abs("../../.githooks")
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "repo")
	git(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	git(t, repo, "config", "core.hooksPath", hooks)

	commit := func(dir, file string, extra ...string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", file)
		git(t, dir, append([]string{"commit", "-q", "-m", "Add " + file}, extra...)...)
	}

	commit(repo, "a")
	if n := signOffs(t, repo, "HEAD"); n != 1 {
		t.Errorf("plain commit: %d sign-offs, want 1", n)
	}
	commit(repo, "b", "-s") // -s and the hook together must not double it
	if n := signOffs(t, repo, "HEAD"); n != 1 {
		t.Errorf("commit -s: %d sign-offs, want 1", n)
	}
	git(t, repo, "commit", "-q", "--amend", "--no-edit")
	if n := signOffs(t, repo, "HEAD"); n != 1 {
		t.Errorf("amend: %d sign-offs, want 1", n)
	}

	// A second worktree of the same clone uses the same hook configuration.
	wt := filepath.Join(filepath.Dir(repo), "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "feature", wt)
	commit(wt, "c")
	if n := signOffs(t, wt, "HEAD"); n != 1 {
		t.Errorf("commit in a worktree: %d sign-offs, want 1", n)
	}

	commit(repo, "d")
	git(t, repo, "merge", "-q", "--no-ff", "--no-edit", "feature")
	if n := signOffs(t, repo, "HEAD"); n != 1 {
		t.Errorf("merge commit: %d sign-offs, want 1", n)
	}
}
