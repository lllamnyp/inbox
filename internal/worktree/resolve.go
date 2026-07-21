// Package worktree maps PRs to their per-PR git worktrees and Claude Code
// sessions, following the ~/Cloud/dev/<git-server>/<org>/<repo>-<num>
// convention.
package worktree

import (
	"net/url"
	"os"
	"path/filepath"
	"sort"
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

// sessionFreshness is the "live-looking" window: no process detection, an
// mtime within the last hour is enough.
const sessionFreshness = time.Hour

// ClaudeSession finds the newest Claude Code session in a worktree's own
// project dir (~/.claude/projects/<encoded-cwd>/, <uuid>.jsonl files). This
// is the fallback association for transcripts predating pr-link records;
// the SessionIndex is the primary mechanism.
func ClaudeSession(worktreePath string) (id string, mtime time.Time, ok bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", time.Time{}, false
	}
	enc := encodeProjectDir(worktreePath)
	dir := filepath.Join(home, ".claude", "projects", enc)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", time.Time{}, false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if id == "" || info.ModTime().After(mtime) {
			mtime = info.ModTime()
			id = strings.TrimSuffix(e.Name(), ".jsonl")
		}
	}
	return id, mtime, id != ""
}

// Scanner resolves PRs to worktrees, memoizing the sibling-directory scan so
// one poll pays the filesystem walk once per repository, not once per PR.
type Scanner struct {
	byBranch map[string]map[string]string // primary -> checked-out branch -> worktree path
}

func NewScanner() *Scanner {
	return &Scanner{byBranch: map[string]map[string]string{}}
}

// worktreeInfo reports whether path is a linked worktree of primary, and if
// so which branch it has checked out ("" for a detached HEAD). Pure file
// reads: a linked worktree's .git is a file
// "gitdir: <primary>/.git/worktrees/<name>", which simultaneously proves
// repo identity — a sibling that happens to share the name prefix but is its
// own repository (cozyportal-ui vs cozyportal) has a .git *directory* and
// never matches.
func worktreeInfo(path, primary string) (branch string, linked bool) {
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
	branch, _ = strings.CutPrefix(strings.TrimSpace(string(h)), "ref: refs/heads/")
	if branch == strings.TrimSpace(string(h)) {
		branch = "" // detached HEAD
	}
	return branch, true
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
		if branch, linked := worktreeInfo(path, primary); linked && branch != "" {
			m[branch] = path
		}
	}
	return m
}

// Resolve finds the worktree for a PR. Branch equality is the strongest
// evidence and covers numbered and feature-named worktrees alike — it's how
// worktrees named before the PR existed (repo-freedompay, not repo-867) get
// picked up. The numbered path (<repo>-<num>) alone is weak evidence: the
// number might refer to something other than this PR (an external ticket —
// same-repo issue numbers can't collide, issues and PRs share a sequence),
// so it only wins when its git state can't contradict the PR: a linked
// worktree parked on a detached HEAD (mid-engage), or on the PR's head
// branch modulo a fork-collision prefix (gh pr checkout may create
// owner/branch). A numbered worktree on an unrelated branch is not this
// PR's worktree.
func (s *Scanner) Resolve(prURL, repoWithOwner string, number int, headRef string) (path string, exists bool) {
	primary, wt := Paths(prURL, repoWithOwner, number)
	if headRef != "" {
		if p, ok := s.branchWorktrees(primary)[headRef]; ok {
			return p, true
		}
	}
	if Exists(wt) {
		if branch, linked := worktreeInfo(wt, primary); linked && branchCompatible(branch, headRef) {
			return wt, true
		}
	}
	return wt, false
}

// branchCompatible reports whether a numbered worktree's checked-out branch
// is consistent with the PR's head ref: detached, exact match, or a
// fork-collision-prefixed variant.
func branchCompatible(branch, headRef string) bool {
	return branch == "" || branch == headRef ||
		(headRef != "" && strings.HasSuffix(branch, "/"+headRef))
}

// Annotate fills the worktree/session fields of a derived PR row from the
// naming/branch scan plus the transcript index.
func (s *Scanner) Annotate(p *derive.PR, ix *SessionIndex) {
	primary, _ := Paths(p.URL, p.Repo, p.Number)
	path, exists := s.Resolve(p.URL, p.Repo, p.Number, p.HeadRefName)

	var sessions []SessionInfo
	if ix != nil {
		sessions = ix.SessionsFor(p.Key())
	}

	// A pr-linked session's cwd can reveal a worktree the naming convention
	// and branch scan miss (orchestrators create repo-pr-review-123, and
	// may leave it on a detached HEAD or an oddly named branch).
	if !exists {
		for _, si := range sessions { // newest first
			if si.CWD != "" && si.CWD != primary && Exists(si.CWD) {
				path, exists = si.CWD, true
				break
			}
		}
	}
	p.WorktreePath, p.WorktreeExists = path, exists

	refs := make([]derive.SessionRef, 0, len(sessions)+1)
	seen := map[string]bool{}
	for _, si := range sessions {
		refs = append(refs, derive.SessionRef{
			ID:         si.ID,
			CWD:        si.CWD,
			LastActive: si.Mtime,
			Fresh:      time.Since(si.Mtime) <= sessionFreshness,
		})
		seen[si.ID] = true
	}
	// Fallback for transcripts without pr-link records: newest session in
	// the worktree's own project dir.
	if id, mtime, ok := ClaudeSession(path); ok && !seen[id] {
		refs = append(refs, derive.SessionRef{
			ID:         id,
			CWD:        path,
			LastActive: mtime,
			Fresh:      time.Since(mtime) <= sessionFreshness,
		})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].LastActive.After(refs[j].LastActive) })

	p.ClaudeSessions = refs
	p.ClaudeSessionID = ""
	p.SessionFresh = false
	if len(refs) > 0 {
		p.ClaudeSessionID = refs[0].ID
		for _, r := range refs {
			if r.Fresh {
				p.SessionFresh = true
			}
		}
	}
}
