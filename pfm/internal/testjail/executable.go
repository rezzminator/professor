package testjail

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// WriteExecutable writes a file this process or one of its children may later
// execute: a fake engine, a wrapper script, a stub on a jailed PATH. It holds
// the read side of syscall.ForkLock while the file is open for writing, which
// keeps every fork of this process out of that window. A child forked inside it
// inherits the write descriptor until its own exec closes it, and executing the
// file meanwhile fails with ETXTBSY ("text file busy"); parallel tests fork
// constantly. Every test write whose mode may carry an exec bit goes through
// here — arch-check C26 holds the rule. A package this one imports cannot
// import it back; such a package takes the same lock around its own write and
// points here.
func WriteExecutable(path string, body []byte, mode os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, body, mode)
}

// WriteLoggedOutClaude writes a fake `claude` at path that writes to disk the
// way a logged-out real binary does. Each run first appends its config dir
// (the registry's Claude home variable, or UNSET) as one line to record, then
// writes first-run state into that dir; with the variable unset it falls back
// to $HOME/.claude/backups and $HOME/.claude.json, as the real binary does.
// It answers --version and `doctor --help`; doctor is the shell run for
// `doctor` itself, a healthy answer when empty.
func WriteLoggedOutClaude(path, record, doctor string) error {
	if doctor == "" {
		doctor = "printf 'healthy\\n'"
	}
	script := strings.NewReplacer(
		"{VAR}", pfmengine.MustLookup(pfmengine.Claude).HomeEnv,
		"{RECORD}", record,
		"{DOCTOR}", doctor,
	).Replace(`#!/bin/sh
dir="${{VAR}:-}"
printf '%s\n' "${dir:-UNSET}" >> '{RECORD}'
if [ -n "$dir" ]; then
  /bin/mkdir -p "$dir/backups" && : > "$dir/.claude.json"
else
  /bin/mkdir -p "$HOME/.claude/backups" && : > "$HOME/.claude.json"
fi
case "$1" in
--version) printf '2.1.238 (Claude Code)\n' ;;
doctor) if [ "$2" = "--help" ]; then printf 'usage: claude doctor\n'; else {DOCTOR}; fi ;;
esac
exit 0
`)
	return WriteExecutable(path, []byte(script), 0o700)
}

// AssertClaudeRanInThrowawayHomes fails t unless the fake WriteLoggedOutClaude
// wrote nothing into home's Claude store or $HOME, every run it recorded had
// its own config dir directly under sid, and each of those dirs is gone. It
// returns the number of recorded runs.
func AssertClaudeRanInThrowawayHomes(t *testing.T, home, sid, record string) int {
	t.Helper()
	return assertRanInThrowawayHomes(t, pfmengine.MustLookup(pfmengine.Claude).Binary,
		[]string{filepath.Join(home, ".claude", "backups"), filepath.Join(home, ".claude.json")}, sid, record)
}

// WriteLoggedOutCodex writes a fake `codex` at path that writes to disk the
// way the real binary does on every run: its launcher's tmp/arg0 dir and its
// log database, in the registry's Codex home variable, or $HOME/.codex when it
// is unset. Each run first appends that variable (or UNSET) as one line to
// record. It answers --version and `doctor --help`; doctor is the shell run
// for `doctor --summary`, and empty means the real binary's answer in a home
// with no login: every row ok but `[XX] auth`, exit 1.
func WriteLoggedOutCodex(path, record, doctor string) error {
	if doctor == "" {
		doctor = "printf 'Codex Doctor v0.159.0 · linux-x86_64\\n  [ok] install      consistent\\n" +
			"  [XX] auth         no Codex credentials were found - Run codex login\\n20 ok | 0 warn | 1 fail\\n'; exit 1"
	}
	script := strings.NewReplacer(
		"{VAR}", pfmengine.MustLookup(pfmengine.Codex).HomeEnv,
		"{RECORD}", record,
		"{DOCTOR}", doctor,
	).Replace(`#!/bin/sh
dir="${{VAR}:-}"
printf '%s\n' "${dir:-UNSET}" >> '{RECORD}'
home="${dir:-$HOME/.codex}"
/bin/mkdir -p "$home/tmp/arg0/codex-arg0fake" && : > "$home/logs_2.sqlite"
case "$1" in
--version) printf 'codex-cli 0.159.0\n' ;;
doctor) if [ "$2" = "--help" ]; then printf 'usage: codex doctor\n'; else {DOCTOR}; fi ;;
esac
exit 0
`)
	return WriteExecutable(path, []byte(script), 0o700)
}

// AssertCodexRanInThrowawayHomes is AssertClaudeRanInThrowawayHomes for the
// fake WriteLoggedOutCodex: neither of its writes in $HOME/.codex, every run
// in its own Codex home directly under sid, each gone.
func AssertCodexRanInThrowawayHomes(t *testing.T, home, sid, record string) int {
	t.Helper()
	return assertRanInThrowawayHomes(t, pfmengine.MustLookup(pfmengine.Codex).Binary, []string{
		filepath.Join(home, ".codex", "tmp", "arg0", "codex-arg0fake"), filepath.Join(home, ".codex", "logs_2.sqlite"),
	}, sid, record)
}

func assertRanInThrowawayHomes(t *testing.T, name string, written []string, sid, record string) int {
	t.Helper()
	for _, path := range written {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("a %s probe wrote %s (lstat err=%v): it ran in the real home", name, path, err)
		}
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read the fake %s's run record: %v", name, err)
	}
	runs := strings.Fields(string(raw))
	for _, dir := range runs {
		if filepath.Dir(dir) != sid {
			t.Errorf("a %s probe ran with home %q, want a throwaway dir under %s", name, dir, sid)
			continue
		}
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Errorf("the throwaway %s home %s outlived its probe (lstat err=%v)", name, dir, err)
		}
	}
	return len(runs)
}
