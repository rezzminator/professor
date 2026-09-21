package action

import (
	"errors"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// SandboxedCodexRequest is the launch-only portion of a Codex seat. The
// caller owns the complete -c pin set; this builder owns the invariant that
// the TUI starts in workspace-write rather than the fleet's bypass mode.
type SandboxedCodexRequest struct {
	CWD    string
	Config []string
	Binary string
}

// SandboxedCodexRun builds a Codex TUI command whose writable workspace is
// CWD. It deliberately sits beside HeadlessRun instead of changing that
// function: existing fleet callers rely on HeadlessRun's bypass semantics.
func SandboxedCodexRun(request SandboxedCodexRequest) (HeadlessPlan, error) {
	if request.CWD == "" {
		return HeadlessPlan{}, errors.New("a sandboxed Codex chat requires a project directory")
	}
	if hasNUL(request.CWD) {
		return HeadlessPlan{}, errors.New("action values cannot contain NUL")
	}

	arguments := []string{
		"--strict-config",
		"--sandbox", "workspace-write",
		"--cd", request.CWD,
	}
	for _, override := range request.Config {
		if override == "" || hasNUL(override) || !strings.Contains(override, "=") {
			return HeadlessPlan{}, errors.New("a sandboxed Codex config override must be a non-empty key=value")
		}
		arguments = append(arguments, "-c", override)
	}

	var command strings.Builder
	// The seat's process-tree gate requires the tmux pane root to BE the Codex
	// launcher. zsh and bash exec a sole trailing command on their own; dash
	// (/bin/sh — cron's SHELL, hence tmux's default-shell there) keeps itself
	// as pane root and the gate correctly rejects the seat. exec makes the
	// pane root the launcher by construction under every POSIX shell.
	command.WriteString("exec ")
	command.WriteString(headlessHygiene)
	command.WriteByte(' ')
	command.WriteString(binaryWord(request.Binary, pfmengine.MustLookup(pfmengine.Codex).Binary, request.Binary != ""))
	for _, argument := range arguments {
		command.WriteByte(' ')
		command.WriteString(Quote(argument))
	}
	return HeadlessPlan{
		Run:    command.String(),
		Binary: binaryWord(request.Binary, pfmengine.MustLookup(pfmengine.Codex).Binary, false),
	}, nil
}
