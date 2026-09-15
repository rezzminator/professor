package engine

import "path/filepath"

// OutputStyleDefaultSettings is the --settings payload every Claude launch
// carries to disable Claude Code's own output style. Claude Code has no
// "none" style; the built-in "default" style appends nothing, so naming it
// is the off switch — see Claude's LaunchArgs below for why.
const OutputStyleDefaultSettings = `{"outputStyle":"default"}`

func init() {
	Register(Descriptor{
		ID: Claude, Name: "Claude Code", Short: "Claude", LongName: "claude",
		Binary: "claude", BinaryPathHints: []string{"/claude/versions/"},
		SocketPrefix: "cc-",
		SessionEnv:   "CLAUDE_CODE_SESSION_ID",
		HomeEnv:      "CLAUDE_CONFIG_DIR",
		// Claude Code caps WebSearch at 200 calls per session; past it every
		// search returns a refusal and a research chat stops mid-task. The
		// CLI accepts any digits-only integer >= 1 with no ceiling, and a
		// malformed value silently reverts to 200 — so this is the largest
		// integer JavaScript holds exactly, spelled as plain digits.
		LaunchEnv: []string{"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION=9007199254740991"},
		// pfm stages its own system prompt (--system-prompt-file); Claude
		// Code's own output style (a project or user "outputStyle" setting,
		// e.g. a checked-in .claude/settings.json) would otherwise apply a
		// second persona on top of it. Claude Code has no "none" style —
		// the built-in "default" style appends nothing, so naming it via
		// --settings is the off switch. --settings on the command line
		// outranks project and user settings files, so this wins regardless
		// of what a project or account has configured.
		LaunchArgs:   []string{"--settings", OutputStyleDefaultSettings},
		RootEnv:      "PFM_CLAUDE_ROOTS",
		DefaultRoots: claudeDefaultRoots,
	})
	Register(Descriptor{
		ID: Codex, Name: "Codex", Short: "Codex", LongName: "codex",
		Binary: "codex", BinaryPathHints: nil,
		SocketPrefix: "cx-",
		SessionEnv:   "CODEX_THREAD_ID",
		HomeEnv:      "CODEX_HOME",
		RootEnv:      "PFM_CODEX_ROOT",
		DefaultRoots: func(home string) []string { return []string{filepath.Join(home, ".codex")} },
	})
	Register(Descriptor{
		ID:              Opencode,
		Name:            "OpenCode",
		Short:           "OpenCode",
		LongName:        "opencode",
		Binary:          "opencode",
		BinaryPathHints: nil,
		SocketPrefix:    "ox-",
		SessionEnv:      "", // OpenCode exports no session variable; whoami uses SocketPrefix
		HomeEnv:         "", // none today
		RootEnv:         "PFM_OPENCODE_ROOT",
		DefaultRoots:    func(home string) []string { return []string{filepath.Join(home, ".local", "share", "opencode")} },
	})
}

func claudeDefaultRoots(home string) []string {
	return []string{filepath.Join(home, ".claude", "projects")}
}
