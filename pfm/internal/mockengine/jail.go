package mockengine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"hostops/pfm/internal/atomicfile"
)

// seat is the mock's presence on a jailed host: the /proc entry
// internal/gather/procfs.go reads (cmdline, stat, environ, fd links) and, for
// Claude, the sid crumb pfm-statusline's breadcrumb would have written
// (internal/statusline/render.go:668-686). A real engine has both for free;
// a jail (PFM_PROC_ROOT, PFM_SID_DIR) only sees what somebody writes there.
type seat struct {
	procDir string
}

// bindSeat lays the seat down per the scenario's jail block; empty fields
// bind nothing. transcript is the Claude JSONL or the Codex rollout.
func bindSeat(proc *process, engine, transcript string) (*seat, error) {
	bound := &seat{}
	jail := proc.script.Jail
	if jail.ProcRoot != "" {
		dir := filepath.Join(jail.ProcRoot, strconv.Itoa(proc.pid))
		if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o700); err != nil {
			return nil, fmt.Errorf("create proc entry: %w", err)
		}
		cmdline := strings.Join(append([]string{engine}, proc.args...), "\x00") + "\x00"
		// /proc/<pid>/stat after the "(comm) " prefix: state, ppid, then the
		// fields procfs.go:198-222 skips to the start tick at index 19.
		stat := fmt.Sprintf(
			"%d (%s) S %d 1 1 0 -1 0 0 0 0 0 0 0 0 0 0 0 20 0 100\n", proc.pid, engine, os.Getppid(),
		)
		for name, content := range map[string]string{
			"cmdline": cmdline, "stat": stat, "environ": strings.Join(os.Environ(), "\x00") + "\x00",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				return nil, fmt.Errorf("write proc %s: %w", name, err)
			}
		}
		if engine == engineCodex && transcript != "" {
			// gather/codexproc.go finds a Codex seat's rollout on its open
			// descriptor table; the real process holds the file open.
			if err := os.Symlink(
				transcript,
				filepath.Join(dir, "fd", "7"),
			); err != nil &&
				!errors.Is(err, fs.ErrExist) {
				return nil, fmt.Errorf("link rollout descriptor: %w", err)
			}
		}
		bound.procDir = dir
	}
	if jail.SIDDir != "" && engine == engineClaude && transcript != "" {
		socket, _, _ := strings.Cut(proc.env("TMUX"), ",")
		socket = filepath.Base(socket)
		if socket != "." && socket != "" {
			if err := os.MkdirAll(jail.SIDDir, 0o700); err != nil {
				return nil, fmt.Errorf("create sid dir: %w", err)
			}
			if err := atomicfile.Write(filepath.Join(jail.SIDDir, socket), []byte(transcript), 0o600); err != nil {
				return nil, fmt.Errorf("write sid crumb: %w", err)
			}
			if pane := proc.env("TMUX_PANE"); pane != "" {
				if err := atomicfile.Write(
					filepath.Join(jail.SIDDir, socket+"."+pane),
					[]byte(transcript),
					0o600,
				); err != nil {
					return nil, fmt.Errorf("write pane crumb: %w", err)
				}
			}
		}
	}
	return bound, nil
}

// release removes the proc entry so a dead chat stops looking alive to the
// scan that has to notice it died. Crumbs stay: pfm sweeps them itself. A
// failed removal is returned, never dropped: an entry left behind is exactly
// the "alive" lie the scan under test must not be fed.
func (bound *seat) release() error {
	if bound == nil || bound.procDir == "" {
		return nil
	}
	if err := os.RemoveAll(bound.procDir); err != nil {
		return fmt.Errorf("release proc entry %s: %w", bound.procDir, err)
	}
	return nil
}
