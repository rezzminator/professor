package mockengine

import (
	"fmt"
	"path/filepath"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// argvEngine is the engine a run plays: argv[0]'s basename when it names one, else the EnvEngine
// override, else Claude when argv[0] is one of Claude's native builds, named by their version in its
// versions directory (~/.local/share/claude/versions/2.1.270), as pfm runs a native install. A version
// name anywhere else selects nothing.
func argvEngine(argv0, override string) (string, error) {
	engine := filepath.Base(argv0)
	switch engine {
	case engineClaude, engineCodex, engineOpenCode:
		return engine, nil
	}
	switch override {
	case engineClaude, engineCodex, engineOpenCode:
		return override, nil
	}
	if strings.HasSuffix(filepath.Dir(argv0), string(filepath.Separator)+gather.ClaudeVersionsDir("")) &&
		pfmengine.MatchCommand(pfmengine.Claude, []string{engine}, true) {
		return engineClaude, nil
	}
	return "", fmt.Errorf("argv[0] %q names no engine (claude|codex|opencode) and %s=%q selects none",
		engine, EnvEngine, override)
}
