package worktree

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SessionInfo describes one Claude Code session transcript: where it ran and
// which PRs it dealt with.
type SessionInfo struct {
	ID    string
	CWD   string
	Mtime time.Time
	PRs   []string // "owner/repo#num" keys, first-seen order
}

type fileCache struct {
	size  int64
	mtime time.Time
	info  SessionInfo
}

// SessionIndex maps PRs to the Claude Code sessions that dealt with them.
// Claude Code writes a "pr-link" record ({prNumber, prRepository, ...}) into
// the session transcript every time the session touches a PR — the same data
// the agents view's PR column shows. That beats any path-naming heuristic:
// it survives arbitrary worktree names (repo-pr-review-123) and captures
// sessions that handle several PRs at once.
//
// Transcripts under ~/.claude/projects are scanned only for project dirs
// below the dev root, and each file is re-parsed only when its (size, mtime)
// changes, so the first poll pays for the full walk (~sub-second per few
// hundred MB) and later polls touch only active sessions. Refresh is safe to
// call from the poll goroutine while the UI reads via SessionsFor.
type SessionIndex struct {
	projectsDir string
	prefix      string // encoded-dir prefix filter (dev root)

	mu    sync.Mutex
	files map[string]fileCache     // transcript path -> parsed
	byPR  map[string][]SessionInfo // PR key -> sessions, newest first
}

func NewSessionIndex() *SessionIndex {
	ix := &SessionIndex{
		files: map[string]fileCache{},
		byPR:  map[string][]SessionInfo{},
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ix // degenerate: Refresh is a no-op
	}
	ix.projectsDir = filepath.Join(home, ".claude", "projects")
	ix.prefix = encodeProjectDir(DevRoot()) + "-"
	return ix
}

// Refresh rescans changed transcripts and rebuilds the PR mapping.
func (ix *SessionIndex) Refresh() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.projectsDir == "" {
		return
	}
	dirs, err := os.ReadDir(ix.projectsDir)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), ix.prefix) {
			continue
		}
		dir := filepath.Join(ix.projectsDir, d.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			fi, err := e.Info()
			if err != nil {
				continue
			}
			seen[path] = true
			if c, ok := ix.files[path]; ok && c.size == fi.Size() && c.mtime.Equal(fi.ModTime()) {
				continue
			}
			info := scanTranscript(path)
			info.ID = strings.TrimSuffix(e.Name(), ".jsonl")
			info.Mtime = fi.ModTime()
			ix.files[path] = fileCache{size: fi.Size(), mtime: fi.ModTime(), info: info}
		}
	}
	for path := range ix.files {
		if !seen[path] {
			delete(ix.files, path)
		}
	}

	byPR := map[string][]SessionInfo{}
	for _, c := range ix.files {
		for _, key := range c.info.PRs {
			byPR[key] = append(byPR[key], c.info)
		}
	}
	for _, ss := range byPR {
		sort.Slice(ss, func(i, j int) bool { return ss[i].Mtime.After(ss[j].Mtime) })
	}
	ix.byPR = byPR
}

// SessionsFor returns the sessions that pr-linked this PR, newest first.
func (ix *SessionIndex) SessionsFor(key string) []SessionInfo {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return slices.Clone(ix.byPR[key])
}

// scanTranscript extracts the session's cwd and pr-linked PRs from one
// transcript. Lines are pre-filtered with byte search; JSON decoding only
// happens on candidate lines, and cwd only until first found.
func scanTranscript(path string) SessionInfo {
	var info SessionInfo
	f, err := os.Open(path)
	if err != nil {
		return info
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // tool-result lines get huge
	seen := map[string]bool{}
	for sc.Scan() {
		line := sc.Bytes()
		if info.CWD == "" && bytes.Contains(line, []byte(`"cwd":"`)) {
			var rec struct {
				CWD string `json:"cwd"`
			}
			if json.Unmarshal(line, &rec) == nil && rec.CWD != "" {
				info.CWD = rec.CWD
			}
		}
		if !bytes.Contains(line, []byte(`"type":"pr-link"`)) {
			continue
		}
		var rec struct {
			Type   string `json:"type"`
			Number int    `json:"prNumber"`
			Repo   string `json:"prRepository"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Type != "pr-link" || rec.Repo == "" || rec.Number <= 0 {
			continue
		}
		key := rec.Repo + "#" + strconv.Itoa(rec.Number)
		if !seen[key] {
			seen[key] = true
			info.PRs = append(info.PRs, key)
		}
	}
	return info
}
