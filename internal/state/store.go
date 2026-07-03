// Package state persists the inbox as one human-readable JSON file with
// atomic writes (temp file + rename).
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/lllamnyp/inbox/internal/derive"
)

type State struct {
	MyLogin    string                `json:"my_login"`
	LastPollAt time.Time             `json:"last_poll_at"`
	PRs        map[string]*derive.PR `json:"prs"`
}

// DefaultPath is $XDG_DATA_HOME/inbox/state.json, falling back to
// ~/.local/share/inbox/state.json.
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "inbox", "state.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "inbox", "state.json"), nil
}

// Load reads the state file; a missing file yields an empty state.
func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &State{PRs: map[string]*derive.PR{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.PRs == nil {
		s.PRs = map[string]*derive.PR{}
	}
	return &s, nil
}

// Save writes atomically: temp file in the target directory, then rename.
func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
