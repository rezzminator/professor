package hookentry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"hostops/pfm/internal/installer"
)

type rrDirHookInput struct {
	Cwd string `json:"cwd"`
}

// The three lines the rr agent prompt reads. One state, one wording: a found
// ledger, the fallback ledger, and a failed look are never the same sentence.
const (
	rrDirFound    = "RR-DIR: %s"
	rrDirFallback = "RR-DIR: %s (fallback: no .professor/ above %s)"
	rrDirError    = "RR-DIR-ERROR: %s"
)

// RRDir is the fail-open SubagentStart hook for the rr agents: it resolves
// the directory a research answer is saved into and hands it to the starting
// agent as additionalContext, so the agent never probes the filesystem for
// it. The directory is {root}/.professor/RR for the nearest ancestor of the
// spawn cwd holding a .professor directory, else the blueprint clone's own
// ledger. It creates nothing and exits 0 on every path — a hook must never
// block a spawn — but a failed look is SAID, as an RR-DIR-ERROR line in the
// agent's context and on stderr, never rendered as a missing line.
func RRDir(input io.Reader, stdout, stderr io.Writer, home string) int {
	line, err := rrDirLine(input, home)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal rr-dir: %v\n", err)
		line = fmt.Sprintf(rrDirError, err)
	}
	response := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "SubagentStart", "additionalContext": line,
	}}
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		fmt.Fprintf(stderr, "pfm internal rr-dir: write hook response (fail-open): %v\n", err)
	}
	return 0
}

func rrDirLine(input io.Reader, home string) (string, error) {
	raw, err := io.ReadAll(input)
	if err != nil {
		return "", fmt.Errorf("read hook payload: %w", err)
	}
	var hook rrDirHookInput
	if err := json.Unmarshal(raw, &hook); err != nil {
		return "", fmt.Errorf("decode hook payload: %w", err)
	}
	if !filepath.IsAbs(hook.Cwd) {
		return "", fmt.Errorf("hook payload cwd %q is not an absolute path", hook.Cwd)
	}
	cwd := filepath.Clean(hook.Cwd)
	// The blueprint clone conventionally lives at {home}/.professor — a
	// directory NAMED like a ledger that is not one: {clone}/RR is the clone's
	// repo root. Resolved up front so the walk can step over it; a clone that
	// cannot be resolved only matters once the walk needs the fallback.
	clone, cloneErr := "", error(nil)
	if home == "" {
		cloneErr = fmt.Errorf("no home directory to resolve the blueprint clone from")
	} else {
		clone, cloneErr = installer.GlobalSourceRepo(home)
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		ledger := filepath.Join(dir, ".professor")
		info, err := os.Stat(ledger)
		switch {
		case err == nil && info.IsDir() && cloneErr == nil && ledger == clone:
		case err == nil && info.IsDir():
			return fmt.Sprintf(rrDirFound, filepath.Join(ledger, "RR")), nil
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("inspect %s: %w", ledger, err)
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	if cloneErr != nil {
		return "", fmt.Errorf("no .professor/ above %s; resolve the blueprint clone: %w", cwd, cloneErr)
	}
	return fmt.Sprintf(rrDirFallback, filepath.Join(clone, ".professor", "RR"), cwd), nil
}
