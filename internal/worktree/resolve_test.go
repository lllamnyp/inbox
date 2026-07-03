package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

// fabricate a primary clone with one linked worktree checked out on branch
// ("" = detached), plus the on-disk shape git actually writes: the
// worktree's .git is a file pointing into <primary>/.git/worktrees/<name>.
func fakeWorktree(t *testing.T, primary, name, branch string) string {
	t.Helper()
	gitdir := filepath.Join(primary, ".git", "worktrees", name)
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	head := "ref: refs/heads/" + branch + "\n"
	if branch == "" {
		head = "e83c5163316f89bfbde7d9ab23ca2e25604af290\n"
	}
	if err := os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(filepath.Dir(primary), name)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestResolveNamedWorktreeByBranch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("INBOX_DEV_ROOT", root)
	primary := filepath.Join(root, "github.com", "org", "repo")
	if err := os.MkdirAll(filepath.Join(primary, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	named := fakeWorktree(t, primary, "repo-freedompay", "feat/freedompay")

	// A same-prefix sibling that is its own repository (.git directory, not
	// file) must never match, even if it were on the same branch name.
	trap := filepath.Join(root, "github.com", "org", "repo-ui")
	if err := os.MkdirAll(filepath.Join(trap, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	prURL := "https://github.com/org/repo/pull/42"

	path, exists := NewScanner().Resolve(prURL, "org/repo", 42, "feat/freedompay")
	if !exists || path != named {
		t.Errorf("Resolve = %q, %v; want %q, true", path, exists, named)
	}

	// No worktree holds this branch: fall back to the numbered convention.
	path, exists = NewScanner().Resolve(prURL, "org/repo", 42, "other-branch")
	if exists || path != primary+"-42" {
		t.Errorf("Resolve = %q, %v; want %q, false", path, exists, primary+"-42")
	}
}

func TestNumberedWorktreeMustNotContradictThePR(t *testing.T) {
	root := t.TempDir()
	t.Setenv("INBOX_DEV_ROOT", root)
	primary := filepath.Join(root, "github.com", "org", "repo")
	if err := os.MkdirAll(filepath.Join(primary, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	prURL := "https://github.com/org/repo/pull/42"

	// repo-42 exists but holds unrelated work (numbered after an external
	// ticket, not this PR): the name alone must not attach it.
	fakeWorktree(t, primary, "repo-42", "fix/some-issue-work")
	path, exists := NewScanner().Resolve(prURL, "org/repo", 42, "feat/pr-branch")
	if exists {
		t.Errorf("numbered worktree on an unrelated branch attached: %q", path)
	}

	// A detached numbered worktree (mid-engage) is compatible.
	fakeWorktree(t, primary, "repo-43", "")
	path, exists = NewScanner().Resolve("https://github.com/org/repo/pull/43", "org/repo", 43, "feat/pr-branch")
	if !exists || path != filepath.Join(root, "github.com", "org", "repo-43") {
		t.Errorf("detached numbered worktree not attached: %q, %v", path, exists)
	}

	// A fork-collision-prefixed branch (gh pr checkout's owner/branch form)
	// is compatible on the numbered path.
	fakeWorktree(t, primary, "repo-44", "alice/feat/pr-branch")
	path, exists = NewScanner().Resolve("https://github.com/org/repo/pull/44", "org/repo", 44, "feat/pr-branch")
	if !exists || path != filepath.Join(root, "github.com", "org", "repo-44") {
		t.Errorf("prefixed-branch numbered worktree not attached: %q, %v", path, exists)
	}
}

func TestWorktreeBranchRejectsForeignRepos(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "repo")
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(filepath.Join(primary, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// a worktree linked to a *different* primary
	wt := fakeWorktree(t, other, "repo-x", "main")
	if _, linked := worktreeInfo(wt, primary); linked {
		t.Error("worktree of a different primary matched")
	}
	if branch, linked := worktreeInfo(wt, other); !linked || branch != "main" {
		t.Errorf("worktree of its own primary: branch=%q linked=%v, want main/true", branch, linked)
	}
}
