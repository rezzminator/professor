// Package agentlauncher owns the stable launcher script every pfm-written
// macOS launchd agent runs as ProgramArguments[0].
//
// Background Task Management registers a launch item by the code identity of
// the program launchd starts. pfm is ad-hoc signed, so every build is a new
// identity: an agent naming pfm directly was dropped and re-registered after
// each rebuild, and each new item posted an "App Background Activity"
// advisory. The launcher is a POSIX sh script whose bytes depend only on the
// pfm path, so it is written once and never again; it execs pfm, which keeps
// the pid, so KeepAlive, StartInterval and WatchPaths act on the same process.
package agentlauncher

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Mode is the launcher's permission bits.
const Mode fs.FileMode = 0o755

// Program is where the launcher for home lives.
func Program(home string) string { return paths.AgentLauncher(home) }

// ExecTarget is the installed pfm the launcher execs: the canonical
// ~/.local/bin/pfm every other installed surface names.
func ExecTarget(home string) string { return filepath.Join(home, ".local", "bin", "pfm") }

// Content is the launcher's bytes for the pfm at pfm. The path is single-quoted
// for sh, so no character in it is ever interpreted.
func Content(pfm string) []byte {
	quoted := "'" + strings.ReplaceAll(pfm, "'", `'\''`) + "'"
	return []byte("#!/bin/sh\n" +
		"# Stable launchd entry for pfm's LaunchAgents (written by pfm install, never edited).\n" +
		"# It execs pfm so launchd keeps one pid, while Background Task Management sees\n" +
		"# this unchanging file instead of a pfm binary that is a new identity per build.\n" +
		"exec " + quoted + " \"$@\"\n")
}

// UpToDate reports whether the launcher at Program(home) is a regular file holding
// exactly Content(ExecTarget(home)) with Mode: the one test install, doctor and
// the theme follower share, so none rewrites a launcher another left right.
func UpToDate(home string) (bool, error) {
	path := Program(home)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("agent launcher %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != Mode {
		return false, nil
	}
	existing, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read agent launcher %s: %w", path, err)
	}
	return bytes.Equal(existing, Content(ExecTarget(home))), nil
}

// Stage publishes the launcher atomically (new inode, never a torn script a
// starting agent could read).
func Stage(home string) error {
	path := Program(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create agent launcher directory %s: %w", filepath.Dir(path), err)
	}
	if err := atomicfile.Write(path, Content(ExecTarget(home)), Mode); err != nil {
		return fmt.Errorf("write agent launcher %s: %w", path, err)
	}
	return nil
}

// Ensure stages the launcher only when UpToDate is false and reports whether it
// wrote: an up-to-date launcher is not touched, so its inode and mtime hold.
func Ensure(home string) (bool, error) {
	current, err := UpToDate(home)
	if err != nil || current {
		return false, err
	}
	return true, Stage(home)
}

// Remove deletes the launcher, then its own pfm directory when that is a real,
// empty directory. The libexec directory above it is shared with other tools
// and never removed, and a symlinked pfm directory is left as it is.
func Remove(home string) error {
	path := Program(home)
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove agent launcher %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("agent launcher directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
		return fmt.Errorf("remove agent launcher directory %s: %w", dir, err)
	}
	return nil
}
