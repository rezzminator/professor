package main

import (
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// callerEngine reports the engine of the chat that is CALLING pfm right now,
// derived from the two ambient session variables a chat's own shell carries.
// CLAUDE_CODE_SESSION_ID is checked FIRST: CODEX_THREAD_ID is inherited into
// shells that are not themselves a Codex chat (per pfm/CLAUDE.md § Code
// Standards, "Identity is derived where the chat is, never where the message
// is delivered" — a Claude Code chat can carry both variables), so the Claude
// session variable must win whenever it is present.
//
// getenv is injected so a caller can pin a fake environment in a test; every
// production call site passes the process environment's Get method
// (paths.Env.Get, seamed per pfm/TESTPLAN.md § Seams).
func callerEngine(getenv func(string) string) (pfmengine.ID, bool) {
	switch {
	case getenv(resolve.ClaudeSessionEnv) != "":
		return pfmengine.Claude, true
	case getenv(resolve.CodexThreadEnv) != "":
		return pfmengine.Codex, true
	default:
		return "", false
	}
}
