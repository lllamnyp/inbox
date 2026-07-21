package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lllamnyp/inbox/internal/derive"
)

func writeTranscript(t *testing.T, projects, encDir, uuid string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(projects, encDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, uuid+".jsonl")
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testIndex(projects string) *SessionIndex {
	return &SessionIndex{
		projectsDir: projects,
		prefix:      "-root-",
		files:       map[string]fileCache{},
		byPR:        map[string][]SessionInfo{},
	}
}

func TestSessionIndexPrLinks(t *testing.T) {
	projects := t.TempDir()
	writeTranscript(t, projects, "-root-github-com-org-repo-pr-review-42", "aaaa-bbbb",
		`{"type":"summary","sessionId":"aaaa-bbbb"}`,
		`{"type":"user","cwd":"/root/github.com/org/repo-pr-review-42","gitBranch":"whatever"}`,
		`{"type":"pr-link","prNumber":42,"prUrl":"https://github.com/org/repo/pull/42","prRepository":"org/repo"}`,
		`{"type":"pr-link","prNumber":42,"prUrl":"https://github.com/org/repo/pull/42","prRepository":"org/repo"}`,
		`{"type":"pr-link","prNumber":7,"prUrl":"https://github.com/org/other/pull/7","prRepository":"org/other"}`,
	)
	// outside the dev-root prefix: must be ignored
	writeTranscript(t, projects, "-elsewhere-proj", "cccc-dddd",
		`{"type":"pr-link","prNumber":42,"prRepository":"org/repo"}`,
	)

	ix := testIndex(projects)
	ix.Refresh()

	ss := ix.SessionsFor("org/repo#42")
	if len(ss) != 1 {
		t.Fatalf("SessionsFor(org/repo#42) = %d sessions, want 1", len(ss))
	}
	if ss[0].ID != "aaaa-bbbb" || ss[0].CWD != "/root/github.com/org/repo-pr-review-42" {
		t.Errorf("session = %+v", ss[0])
	}
	if len(ss[0].PRs) != 2 {
		t.Errorf("PRs = %v, want deduped [org/repo#42 org/other#7]", ss[0].PRs)
	}
	if got := ix.SessionsFor("org/other#7"); len(got) != 1 {
		t.Errorf("multi-PR session should appear under both PRs; got %d for the second", len(got))
	}
	if got := ix.SessionsFor("org/none#1"); len(got) != 0 {
		t.Errorf("unexpected sessions for unknown PR: %v", got)
	}
}

func TestSessionIndexForgetsRemovedFiles(t *testing.T) {
	projects := t.TempDir()
	path := writeTranscript(t, projects, "-root-x", "aaaa",
		`{"type":"pr-link","prNumber":1,"prRepository":"org/repo"}`,
	)
	ix := testIndex(projects)
	ix.Refresh()
	if len(ix.SessionsFor("org/repo#1")) != 1 {
		t.Fatal("setup: session not indexed")
	}
	os.Remove(path)
	ix.Refresh()
	if len(ix.SessionsFor("org/repo#1")) != 0 {
		t.Error("removed transcript still indexed")
	}
}

func TestAnnotateUsesSessionCwdAsWorktreeFallback(t *testing.T) {
	root := t.TempDir()
	t.Setenv("INBOX_DEV_ROOT", root)
	primary := filepath.Join(root, "github.com", "org", "repo")
	if err := os.MkdirAll(filepath.Join(primary, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An orchestrator-named worktree on a branch that does NOT match the
	// PR's head ref: invisible to naming and branch scans.
	wt := fakeWorktree(t, primary, "repo-pr-review-42", "orchestrator/branch")

	projects := t.TempDir()
	writeTranscript(t, projects, "-root-whatever", "eeee-ffff",
		`{"type":"user","cwd":"`+wt+`"}`,
		`{"type":"pr-link","prNumber":42,"prRepository":"org/repo"}`,
	)
	ix := testIndex(projects)
	ix.Refresh()

	p := &derive.PR{
		Repo:        "org/repo",
		Number:      42,
		URL:         "https://github.com/org/repo/pull/42",
		HeadRefName: "feat/actual-branch",
	}
	NewScanner().Annotate(p, ix)
	if !p.WorktreeExists || p.WorktreePath != wt {
		t.Errorf("worktree = %q exists=%v, want %q via session cwd", p.WorktreePath, p.WorktreeExists, wt)
	}
	if p.ClaudeSessionID != "eeee-ffff" {
		t.Errorf("session id = %q, want eeee-ffff", p.ClaudeSessionID)
	}
	if len(p.ClaudeSessions) != 1 || !p.ClaudeSessions[0].Fresh {
		t.Errorf("sessions = %+v, want one fresh entry", p.ClaudeSessions)
	}
}
