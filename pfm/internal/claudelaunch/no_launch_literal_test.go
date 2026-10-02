package claudelaunch

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Readers and the two documented headless argv owners may name only the
// literal they consume. An entry that goes stale fails with the same sweep.
type launchLiteralAllowance struct {
	reason   string
	literals []string
}

var launchLiteralReaders = map[string]launchLiteralAllowance{
	"cmd/pfm/chat_inject_resume.go":  {"reads process identity", []string{"CLAUDE_CODE_SESSION_ID="}},
	"cmd/pfm/chat_new_command.go":    {"reads caller session identity", []string{"CLAUDE_CODE_SESSION_ID"}},
	"cmd/pfm/chat_reload_command.go": {"reads caller session identity", []string{"CLAUDE_CODE_SESSION_ID"}},
	"cmd/pfm/chat_satellite_command.go": {"reads parent identity and parses branch input", []string{
		"CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_SESSION_ID is not set and no transcript path was given",
		"usage: pfm chat branch [--engine claude|codex] [--session-id ID] [--cwd DIR] ",
		"pfm chat branch: no ambient session id; pass --engine and --session-id",
		"pfm chat branch: --session-id is required and must be one safe line",
	}},
	"internal/action/synth.go": {"parses launcher argv and strips OpenCode port", []string{
		"--session-id", "--session-id=", "--fork-session", "--resume", "CLAUDE_CODE_SSE_PORT",
	}},
	"internal/chat/find.go": {"reads session identity", []string{"CLAUDE_CODE_SESSION_ID"}},
	"internal/doctor/harness_prompt.go": {"fixed headless capture argv and drift diagnosis", []string{
		"--mcp-config", "the CLI answered from the real endpoint and ignored ANTHROPIC_BASE_URL",
	}},
	"internal/doctor/spawn_audit.go": {"diagnoses parsed launch values", []string{
		"argv carries --system-prompt-file and registry payload",
		"lean prompt armed in the --settings payload and registry hooks",
		"no --system-prompt-file prompt material", "CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT",
		"lean prompt missing from the --settings payload", "missing --settings outputStyle default",
	}},
	"internal/engine/builtin.go": {"descriptor reads session identity", []string{"CLAUDE_CODE_SESSION_ID"}},
	"internal/gather/agents.go": {
		"reads process argv", []string{"--resume", "--session-id", "--session-id=", "--resume="},
	},
	"internal/headless/run/opencode_config.go": {
		"parses OpenCode argv", []string{"--dangerously-skip-permissions"},
	},
	"internal/headless/run/run.go": {"documented headless argv owner", []string{
		"--system-prompt-file", "--settings", "--settings=",
	}},
	"internal/inject/engine.go": {"reads caller session identity", []string{"CLAUDE_CODE_SESSION_ID"}},
	"internal/kill/manager.go":  {"reads caller session identity", []string{"CLAUDE_CODE_SESSION_ID"}},
	"internal/mockengine/claude.go": {
		"mock argv",
		[]string{
			"--settings", "--system-prompt-file", "--session-id", "--mcp-config",
			"--resume", "--fork-session", "CLAUDE_CODE_SESSION_ID=",
		},
	},
	"internal/resolve/whoami.go": {"reads caller session identity", []string{"CLAUDE_CODE_SESSION_ID"}},
	"internal/statusline/render.go": {"reads engine telemetry", []string{
		"ANTHROPIC_MODEL", "CLAUDE_CODE_AUTO_COMPACT_WINDOW",
	}},
	"internal/testjail/launch_shell.go": {"pins the launch shell", []string{"CLAUDE_CODE_SHELL"}},
	"internal/testjail/testjail.go":     {"sets fixture identity", []string{"CLAUDE_CODE_SESSION_ID"}},
}

var launchEnvFamily = regexp.MustCompile(
	`(?:CLAUDE_CODE_|ANTHROPIC_|ENABLE_PROMPT_CACHING_|FORCE_PROMPT_CACHING_)[A-Z0-9_]*`,
)

var launchFlags = []string{
	"--settings", "--mcp-config", "--session-id", "--fork-session",
	"--system-prompt-file", "--allow-dangerously-skip-permissions",
	"--dangerously-skip-permissions", "--resume",
}

func launchLiteral(value string) bool {
	if launchEnvFamily.MatchString(value) {
		return true
	}
	for _, flag := range launchFlags {
		if strings.Contains(value, flag) {
			return true
		}
	}
	return false
}

func TestNoLaunchLiteralOutsideRegistry(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate launch sweep source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	seen := map[string]map[string]bool{}
	var hits []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel == "internal/claudelaunch" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				hits = append(hits, fmt.Sprintf("%s:%d: unquote: %v", rel, positions.Position(literal.Pos()).Line, err))
				return true
			}
			if !launchLiteral(value) {
				return true
			}
			if allowance, allowed := launchLiteralReaders[rel]; allowed &&
				strings.TrimSpace(allowance.reason) != "" && slices.Contains(allowance.literals, value) {
				if seen[rel] == nil {
					seen[rel] = map[string]bool{}
				}
				seen[rel][value] = true
				return true
			}
			hits = append(hits, fmt.Sprintf("%s:%d: %q", rel, positions.Position(literal.Pos()).Line, value))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("launch literal sweep of %s failed — a failure to look, not a clean result: %v", root, err)
	}
	for file, allowance := range launchLiteralReaders {
		for _, literal := range allowance.literals {
			if strings.TrimSpace(allowance.reason) == "" || !seen[file][literal] {
				hits = append(hits, fmt.Sprintf("stale allow-list entry %s: %q (%s)", file, literal, allowance.reason))
			}
		}
	}
	if len(hits) != 0 {
		sort.Strings(hits)
		for _, hit := range hits {
			t.Error(hit)
		}
		t.Fatalf("launch literal sweep: %d findings", len(hits))
	}
}
