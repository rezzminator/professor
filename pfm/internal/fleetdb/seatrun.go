package fleetdb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// SeatRun is the private launch recipe read by seat-host, not inherited shell state.
// With EnvDelta, Env and Unset are pfm's part of the engine's environment, which
// the seat host applies over its own (Environ); without it, a recipe an older pfm
// wrote, Env is the engine's whole environment.
type SeatRun struct {
	CostLedger bool     `json:"cost_ledger"`
	Binary     string   `json:"binary"`
	Args       []string `json:"args"`
	Env        []string `json:"env"`
	Unset      []string `json:"unset,omitempty"`
	EnvDelta   bool     `json:"env_delta,omitempty"`
	CWD        string   `json:"cwd"`
}

// WithEnvDelta is run with env and unset as its environment in the delta form,
// the one form every recipe pfm writes takes (claudelaunch.Launch.Recipe).
func (run SeatRun) WithEnvDelta(env, unset []string) SeatRun {
	run.Env, run.Unset, run.EnvDelta = env, unset, true
	return run
}

// Environ is the engine's environment over host, the seat host's own: host
// without Unset and every name Env assigns, then Env (paths.ApplyEnv); a recipe
// with no EnvDelta is its stored Env, host unread.
func (run SeatRun) Environ(host []string) []string {
	if !run.EnvDelta {
		return run.Env
	}
	return paths.ApplyEnv(host, run.Unset, run.Env)
}

var seatIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (s *Store) seatRunPath(id string) (string, error) {
	if !seatIDPattern.MatchString(id) {
		return "", fmt.Errorf("seat run: invalid seat id %q", id)
	}
	if sqlitedb.InMemory(s.path) {
		return "", fmt.Errorf("seat run %q: the in-memory shared state has no directory for its recipe", id)
	}
	return filepath.Join(filepath.Dir(s.path), "seats", id+".json"), nil
}

// WriteSeatRun atomically stores a launch recipe with private permissions.
func (s *Store) WriteSeatRun(id string, run SeatRun) error {
	path, err := s.seatRunPath(id)
	if err != nil {
		return err
	}
	if run.Binary == "" {
		return fmt.Errorf("write seat run %q: empty binary", id)
	}
	body, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("encode seat run %q: %w", id, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create seat run directory: %w", err)
	}
	if err := atomicfile.Write(path, body, 0o600); err != nil {
		return fmt.Errorf("write seat run %q: %w", id, err)
	}
	return nil
}

// ReadSeatRun returns a launch recipe; missing and malformed recipes are errors.
func (s *Store) ReadSeatRun(id string) (SeatRun, error) {
	path, err := s.seatRunPath(id)
	if err != nil {
		return SeatRun{}, err
	}
	body, err := readSeatRecipe(path)
	if err != nil {
		return SeatRun{}, fmt.Errorf("read seat run %q: %w", id, err)
	}
	var run SeatRun
	if err := json.Unmarshal(body, &run); err != nil {
		return run, fmt.Errorf("decode seat run %q: %w", id, err)
	}
	if run.Binary == "" {
		return run, fmt.Errorf("seat run %q: empty binary", id)
	}
	return run, nil
}

// DeleteSeatRun removes an ended seat's private launch recipe.
func (s *Store) DeleteSeatRun(id string) error {
	path, err := s.seatRunPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete seat run %q: %w", id, err)
	}
	return nil
}

func readSeatRecipe(path string) (body []byte, returnErr error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open regular seat recipe: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat seat recipe: %w", err)
	}
	const maxRecipeBytes = 1 << 20
	if !info.Mode().IsRegular() || info.Size() > maxRecipeBytes {
		return nil, fmt.Errorf("seat recipe is not a regular file within %d bytes", maxRecipeBytes)
	}
	body, err = io.ReadAll(io.LimitReader(file, maxRecipeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded seat recipe: %w", err)
	}
	if len(body) > maxRecipeBytes {
		return nil, fmt.Errorf("seat recipe exceeds %d bytes", maxRecipeBytes)
	}
	return body, nil
}
