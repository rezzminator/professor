package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// claudeGlobalConfig is where Claude Code keeps its global config: inside CLAUDE_CONFIG_DIR when set,
// else $HOME/.claude.json.
func claudeGlobalConfig(proc *process) string {
	if dir := proc.env("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(proc.env("HOME"), ".claude.json")
}

// startClaudeConfig is what a Claude Code that loads its config does at start, logged in or not: it
// backs the global config up into {config dir}/backups (the directory created when missing), then
// writes the config back with numStartups one higher.
func startClaudeConfig(proc *process) error {
	path := claudeGlobalConfig(proc)
	document := map[string]any{}
	content, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read %s: %w", path, err)
	default:
		if err := json.Unmarshal(content, &document); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		backups := filepath.Join(claudeConfigDir(proc), "backups")
		if err := os.MkdirAll(backups, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", backups, err)
		}
		backup := filepath.Join(backups, ".claude.json.backup."+strconv.FormatInt(time.Now().UnixMilli(), 10))
		if err := atomicfile.Write(backup, content, 0o600); err != nil {
			return fmt.Errorf("back up %s: %w", path, err)
		}
	}
	startups, _ := document["numStartups"].(float64)
	document["numStartups"] = startups + 1
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := atomicfile.Write(path, encoded, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// answerDoctor is an engine's self-doctor: it checks nothing, and Claude's loads its config like any start.
func answerDoctor(proc *process) int {
	if proc.engine == engineClaude {
		if err := startClaudeConfig(proc); err != nil {
			fmt.Fprintf(proc.stderr, "mock-engine: %v\n", err)
			return ExitUsage
		}
	}
	fmt.Fprintf(proc.stdout, "mock-engine: %s doctor — fixture engine, nothing checked\n", proc.engine)
	return 0
}
