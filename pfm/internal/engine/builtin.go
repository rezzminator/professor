package engine

import (
	"path/filepath"
)

func init() {
	Register(Descriptor{
		ID: Claude, Name: "Claude Code", Short: "Claude", LongName: "claude",
		Binary: "claude", BinaryPathHints: []string{"/claude/versions/"},
		SocketPrefix: "cc-",
		SessionEnv:   "CLAUDE_CODE_SESSION_ID",
		HomeEnv:      "CLAUDE_CONFIG_DIR",
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
		ID:              OpenCode,
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
