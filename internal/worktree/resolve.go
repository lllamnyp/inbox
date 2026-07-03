// Package worktree maps PRs to their per-PR git worktrees and Claude Code
// sessions, following the ~/Cloud/dev/<git-server>/<org>/<repo>-<num>
// convention.
package worktree

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lllamnyp/inbox/internal/derive"
)

// DevRoot is where local clones live; override with $INBOX_DEV_ROOT.
func DevRoot() string {
	if v := os.Getenv("INBOX_DEV_ROOT"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Cloud", "dev")
}

// hostOf derives the git server from the PR URL, so cross-server PRs land
// under the right directory instead of a hardcoded github.com.
func hostOf(prURL string) string {
	u, err := url.Parse(prURL)
	if err != nil || u.Host == "" {
		return "github.com"
	}
	return u.Host
}

// Paths computes the primary clone and the per-PR worktree for a PR.
func Paths(prURL, repoWithOwner string, number int) (primary, wt string) {
	primary = filepath.Join(DevRoot(), hostOf(prURL), filepath.FromSlash(repoWithOwner))
	wt = primary + "-" + strconv.Itoa(number)
	return primary, wt
}

// Exists reports whether path is a git repository or linked worktree
// (worktrees have a .git file rather than a directory).
func Exists(path string) bool {
	fi, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
}

// encodeProjectDir mirrors Claude Code's cwd encoding: every character
// outside [a-zA-Z0-9] becomes "-" (so "github.com" -> "github-com", and the
// leading "/" yields the leading "-").
func encodeProjectDir(path string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, path)
}

// ClaudeSession finds the newest Claude Code session for a worktree.
// Sessions are <uuid>.jsonl files under ~/.claude/projects/<encoded-cwd>/.
// A session is "fresh" (live-looking) if its mtime is within the last hour —
// no process detection, the mtime signal is enough.
func ClaudeSession(worktreePath string) (id string, fresh bool, ok bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, false
	}
	enc := encodeProjectDir(worktreePath)
	dir := filepath.Join(home, ".claude", "projects", enc)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, false
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if id == "" || info.ModTime().After(newest) {
			newest = info.ModTime()
			id = strings.TrimSuffix(e.Name(), ".jsonl")
		}
	}
	if id == "" {
		return "", false, false
	}
	return id, time.Since(newest) <= time.Hour, true
}

// Scanner resolves PRs to worktrees, memoizing the sibling-directory scan so
// one poll pays the filesystem walk once per repository, not once per PR.
type Scanner struct {
	byBranch map[string]map[string]string // primary -> checked-out branch -> worktree path
}

func NewScanner() *Scanner {
	return &Scanner{byBranch: map[string]map[string]string{}}
}

// worktreeBranch returns the branch checked out at path, iff path is a
// linked worktree of primary. Pure file reads: a linked worktree's .git is a
// file "gitdir: <primary>/.git/worktrees/<name>", which simultaneously
// proves repo identity — a sibling that happens to share the name prefix but
// is its own repository (cozyportal-ui vs cozyportal) has a .git *directory*
// and never matches.
func worktreeBranch(path, primary string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return "", false
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
	if !ok {
		return "", false
	}
	gitdir = filepath.Clean(gitdir)
	if !strings.HasPrefix(gitdir, filepath.Join(primary, ".git", "worktrees")+string(os.PathSeparator)) {
		return "", false
	}
	h, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return "", false
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(h)), "ref: refs/heads/")
	return branch, ok // detached HEAD -> no branch to match
}

// branchWorktrees maps checked-out branch -> path across primary's linked
// worktrees following the sibling naming convention (<repo>-<anything>).
func (s *Scanner) branchWorktrees(primary string) map[string]string {
	if m, ok := s.byBranch[primary]; ok {
		return m
	}
	m := map[string]string{}
	s.byBranch[primary] = m
	entries, err := os.ReadDir(filepath.Dir(primary))
	if err != nil {
		return m
	}
	prefix := filepath.Base(primary) + "-"
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		path := filepath.Join(filepath.Dir(primary), e.Name())
		if branch, ok := worktreeBranch(path, primary); ok {
			m[branch] = path
		}
	}
	return m
}

// Resolve finds the worktree for a PR: the numbered convention
// (<repo>-<num>) first, then any sibling worktree whose checked-out branch
// is the PR's head branch — which is how feature worktrees named before the
// PR existed (repo-freedompay, not repo-867) get picked up.
func (s *Scanner) Resolve(prURL, repoWithOwner string, number int, headRef string) (path string, exists bool) {
	primary, wt := Paths(prURL, repoWithOwner, number)
	if Exists(wt) {
		return wt, true
	}
	if headRef != "" {
		if p, ok := s.branchWorktrees(primary)[headRef]; ok {
			return p, true
		}
	}
	return wt, false
}

// Annotate fills the worktree/session fields of a derived PR row.
func (s *Scanner) Annotate(p *derive.PR) {
	p.WorktreePath, p.WorktreeExists = s.Resolve(p.URL, p.Repo, p.Number, p.HeadRefName)
	if id, fresh, ok := ClaudeSession(p.WorktreePath); ok {
		p.ClaudeSessionID = id
		p.SessionFresh = fresh
	} else {
		p.ClaudeSessionID = ""
		p.SessionFresh = false
	}
}
