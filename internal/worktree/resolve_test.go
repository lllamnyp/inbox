package worktree

import (
	"os"
	"path/filepath"
	"testing"
)

// fabricate a primary clone with one linked worktree checked out on branch,
// plus the on-disk shape git actually writes: the worktree's .git is a file
// pointing into <primary>/.git/worktrees/<name>.
func fakeWorktree(t *testing.T, primary, name, branch string) string {
	t.Helper()
	gitdir := filepath.Join(primary, ".git", "worktrees", name)
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/"+branch+"\n"), 0o644); err != nil {
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

	// The numbered convention wins when it exists.
	numbered := fakeWorktree(t, primary, "repo-42", "feat/freedompay")
	path, exists = NewScanner().Resolve(prURL, "org/repo", 42, "feat/freedompay")
	if !exists || path != numbered {
		t.Errorf("Resolve = %q, %v; want %q, true", path, exists, numbered)
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
	if _, ok := worktreeBranch(wt, primary); ok {
		t.Error("worktree of a different primary matched")
	}
	if _, ok := worktreeBranch(wt, other); !ok {
		t.Error("worktree of its own primary did not match")
	}
}
