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

// Annotate fills the worktree/session fields of a derived PR row.
func Annotate(p *derive.PR) {
	_, wt := Paths(p.URL, p.Repo, p.Number)
	p.WorktreePath = wt
	p.WorktreeExists = Exists(wt)
	if id, fresh, ok := ClaudeSession(wt); ok {
		p.ClaudeSessionID = id
		p.SessionFresh = fresh
	} else {
		p.ClaudeSessionID = ""
		p.SessionFresh = false
	}
}
