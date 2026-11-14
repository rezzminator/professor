package mockengine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// claudeSnapshotDump is what Claude Code keeps of the owner's interactive zsh for its tool shells: every
// function but the `_`-prefixed helpers (completion's, and a shim's own), the aliases, and PATH; it
// opens by dropping every alias, so a function an alias shadows defines cleanly.
const claudeSnapshotDump = `print -r -- "unalias -a 2>/dev/null || true"
for f in ${(ok)functions}; do [[ $f == _* ]] || typeset -f -- "$f"; done
alias -L
print -r -- "export PATH=${(q)PATH}"`

// claudeSnapshotRun is how a Claude tool shell runs a command: a fresh, non-interactive zsh that
// sources the snapshot first.
const claudeSnapshotRun = `source "$1" && eval "$2"`

// toolShellArgv is the argv a Claude Bash tool call runs command with under a zsh login shell: a
// fresh, non-interactive zsh replaying the snapshot. Under any other login shell the fake runs sh
// (shellArgv): it snapshots zsh alone.
func toolShellArgv(zsh, snapshot, command string) []string {
	return []string{zsh, "-c", claudeSnapshotRun, "zsh", snapshot, command}
}

// toolShell is the argv this session's Bash tool runs command with, its zsh snapshot taken at the
// first call as Claude Code takes it: the owner's interactive zsh dumped into
// {config}/shell-snapshots/snapshot-zsh-{session}.sh.
func (session *claudeSession) toolShell(command string) ([]string, error) {
	login := session.proc.env("SHELL")
	if filepath.Base(login) != "zsh" {
		return shellArgv(command)
	}
	if session.snapshot == "" {
		path := filepath.Join(session.configDir, "shell-snapshots", "snapshot-zsh-"+session.sessionID+".sh")
		bounded, cancel := context.WithTimeout(session.proc.ctx, 10*time.Second)
		defer cancel()
		dump := exec.CommandContext(bounded, login, "-i", "-c", claudeSnapshotDump)
		dump.Dir, dump.Env = session.proc.cwd, session.environ
		// In a session of its own the interactive zsh has no terminal to take: on the chat's terminal it
		// would make itself the foreground job, and on macOS the engine reading its keys in the background
		// is stopped (SIGTTIN) with no one to continue it.
		dump.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		var stdout, stderr bytes.Buffer
		dump.Stdout, dump.Stderr = &stdout, &stderr
		if err := dump.Run(); err != nil {
			return nil, fmt.Errorf("snapshot the owner's shell %s: %w: %s", login, err, stderr.String())
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create the shell snapshot directory of %s: %w", path, err)
		}
		if err := os.WriteFile(path, stdout.Bytes(), 0o600); err != nil {
			return nil, fmt.Errorf("write the shell snapshot %s: %w", path, err)
		}
		session.snapshot = path
	}
	return toolShellArgv(login, session.snapshot, command), nil
}

// shellArgv runs a tool call's command with sh, as Codex's tool shell does.
func shellArgv(command string) ([]string, error) {
	shell, err := deps.Resolve("sh")
	if err != nil {
		return nil, fmt.Errorf("perform Bash: sh not found: %w", err)
	}
	return []string{shell, "-c", command}, nil
}
