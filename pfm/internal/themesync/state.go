package themesync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// lockFileName is the flock file that serialises set, sync and off.
const lockFileName = "pfm-theme.lock"

// herdrSnapshot is what Off needs to put the user's Herdr theme tables back:
// the replaced span verbatim, or the exact text appended when there was none.
type herdrSnapshot struct {
	Path    string `json:"path,omitempty"`
	Span    string `json:"span"`
	Added   string `json:"added"`
	Backup  string `json:"backup"`
	Created bool   `json:"created,omitempty"`
}

// themeState is state.json: what is in force, at which generation, the
// derived theme, and one snapshot per user file the sync edited. A field the
// struct no longer names (the retired palette, mode and claude) is ignored on
// read and dropped on the next write.
type themeState struct {
	Enabled    bool              `json:"enabled"`
	Appearance Appearance        `json:"appearance"`
	Generation int               `json:"generation"`
	Source     *SourceInfo       `json:"source,omitempty"`
	Accounts   map[string]string `json:"accounts,omitempty"`
	Applied    *Applied          `json:"applied,omitempty"`
	LastFlip   *Flip             `json:"lastFlip,omitempty"`
	LastLog    string            `json:"lastLog,omitempty"`
	Herdr      *herdrSnapshot    `json:"herdr,omitempty"`
	VSCode     map[string]string `json:"vscode,omitempty"`
	Due        map[string]bool   `json:"due,omitempty"`
	// OffPending marks an off that left a surface unrestored, a tmux server or
	// the follower among them, which keep no snapshot here to show it.
	OffPending bool `json:"offPending,omitempty"`
}

// stateStore is the state directory: state.json, backups/ and the lock.
type stateStore struct{ dir string }

func (store stateStore) statePath() string { return filepath.Join(store.dir, StateFileName) }

// load reads state.json. A missing file is (zero, false, nil); an unreadable
// or unparseable one is an error, never an empty state.
func (store stateStore) load() (themeState, bool, error) {
	content, err := os.ReadFile(store.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return themeState{}, false, nil
	}
	if err != nil {
		return themeState{}, false, fmt.Errorf("themesync: read %s: %w", store.statePath(), err)
	}
	var state themeState
	if err := json.Unmarshal(content, &state); err != nil {
		return themeState{}, false, fmt.Errorf("themesync: parse %s: %w", store.statePath(), err)
	}
	return state, true, nil
}

// save replaces state.json atomically; a state already on disk byte for byte
// is not written again.
func (store stateStore) save(state themeState) error {
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("themesync: encode state: %w", err)
	}
	content = append(content, '\n')
	if current, readErr := os.ReadFile(store.statePath()); readErr == nil && bytes.Equal(current, content) {
		return nil
	}
	if err := atomicfile.Write(store.statePath(), content, 0o600); err != nil {
		return fmt.Errorf("themesync: write %s: %w", store.statePath(), err)
	}
	return nil
}

// lock takes the exclusive flock; the returned release unlocks and closes it.
func (store stateStore) lock() (release func() error, err error) {
	if err := os.MkdirAll(store.dir, 0o700); err != nil {
		return nil, fmt.Errorf("themesync: create %s: %w", store.dir, err)
	}
	path := filepath.Join(store.dir, lockFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("themesync: open %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return nil, errors.Join(fmt.Errorf("themesync: lock %s: %w", path, err), file.Close())
	}
	return func() error {
		return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
	}, nil
}
