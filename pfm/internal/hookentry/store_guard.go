package hookentry

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	storeGuardEnv    = "CLAUDE_CONFIG_DIR"
	storeGuardReason = "Drop the CLAUDE_CONFIG_DIR override"
	storeGuardDeny   = storeGuardReason + " and run `claude` as it is: this chat's own config dir already reaches the" +
		" shared plugin store (its plugins/ and settings.json link into ~/.claude), and a claude run on the store" +
		" itself writes .claude.json and backups/ into it."
)

// storeGuardGet reads the hook process's own environment through the paths seam.
var storeGuardGet = paths.OSEnv{}.Get

// bashTool is the tool_name of the Bash PreToolUse matcher every guard hook answers.
const bashTool = "Bash"

type storeGuardHookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	Cwd string `json:"cwd"`
}

// StoreGuard is the PreToolUse hook on Bash that denies a claude run whose
// CLAUDE_CONFIG_DIR is the shared store (~/.claude) or inside it, however the
// variable reaches it: a command prefix, an earlier assignment or export, an
// env or sudo wrapper, a bash -c string. It is fail-open, with one stderr line
// per cause, for an unreadable payload, an unknown HOME, a part that does not
// parse, a value it cannot resolve (an expansion, a path relative to a cd),
// a claude word inside a command it cannot read (eval, a shell fed from a pipe
// or heredoc, env -S) and a program other than claude run on the store (a
// function, a script). An override on a chat whose own CLAUDE_CONFIG_DIR
// already is the store changes nothing and passes in silence.
func StoreGuard(input io.Reader, stdout, stderr io.Writer) int {
	raw, err := io.ReadAll(input)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal store-guard: read hook payload (fail-open): %v\n", err)
		return 0
	}
	if len(raw) == 0 {
		return 0
	}
	var hook storeGuardHookInput
	if err := json.Unmarshal(raw, &hook); err != nil {
		fmt.Fprintf(stderr, "pfm internal store-guard: decode hook payload (fail-open): %v\n", err)
		return 0
	}
	command := hook.ToolInput.Command
	if hook.ToolName != bashTool || !strings.Contains(command, storeGuardEnv) {
		return 0
	}
	home := storeGuardGet("HOME")
	if home == "" {
		fmt.Fprintln(stderr, "pfm internal store-guard: HOME is empty (fail-open)")
		return 0
	}
	store := paths.PhysicalPath(claudelaunch.ClaudeStore(home))
	if dir, ok := storeGuardResolve(storeGuardGet(storeGuardEnv), home, ""); ok && storeGuardInStore(store, dir) {
		return 0
	}
	cwd := hook.Cwd
	if !filepath.IsAbs(cwd) {
		cwd = string(filepath.Separator)
	}
	parsed, err := cmdparse.ParseBatch([]cmdparse.Call{{
		ID: "store-guard", Command: command, Cwd: cwd, Vars: map[string]string{"HOME": home},
	}})
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal store-guard: parse command (fail-open): %v\n", err)
		return 0
	}
	parts := parsed["store-guard"]
	if storeGuardDenies(parts, store, home, hook.Cwd, storeGuardClaudeWord.MatchString(command), stderr) {
		if err := json.NewEncoder(stdout).Encode(preToolUseDenyResponse(storeGuardDeny)); err != nil {
			return 1
		}
	}
	return 0
}

// storeGuardClaudeWord finds claude as a word of the command text, never as
// a path element such as ~/.claude.
var storeGuardClaudeWord = regexp.MustCompile(`(^|[^A-Za-z0-9_.-])claude([^A-Za-z0-9_.-]|$)`)

// storeGuardOpaque runs a command string the parse cannot read: eval, a shell
// with no -c string (fed from a pipe or heredoc), source, and an env the
// parse could not unwrap (env -S).
var storeGuardOpaque = map[string]bool{
	"eval": true, "source": true, ".": true, "env": true,
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true,
}

// storeGuardInert runs nothing else, so the store in its environment is no
// claude run.
var storeGuardInert = map[string]bool{
	"echo": true, "printf": true, "true": true, "false": true, ":": true, "test": true, "[": true,
	"export": true, "declare": true, "typeset": true, "local": true, "readonly": true, "unset": true,
	"cd": true, "pushd": true, "popd": true, "pwd": true, "printenv": true,
}

// storeGuardDenies reads the parts in order: true for a claude run on the
// store; every part it cannot judge writes one fail-open line to stderr.
func storeGuardDenies(parts []cmdparse.Part, store, home, cwd string, claudeWord bool, stderr io.Writer) bool {
	claude := pfmengine.MustLookup(pfmengine.Claude).Binary
	moved := false
	for _, part := range parts {
		if part.Status != cmdparse.StatusOK {
			fmt.Fprintln(stderr, "pfm internal store-guard: could not parse command (fail-open)")
			continue
		}
		base := filepath.Base(part.Program)
		value, set := part.Env[storeGuardEnv]
		switch {
		case base == claude:
			if !set {
				continue
			}
			partCwd := cwd
			if moved {
				partCwd = ""
			}
			dir, ok := storeGuardResolve(value, home, partCwd)
			if !ok {
				storeGuardFailOpen(stderr, "cannot resolve %s=%s", storeGuardEnv, value)
				continue
			}
			if storeGuardInStore(store, dir) {
				return true
			}
		case base == "cd" || base == "pushd" || base == "popd":
			moved = true
		case storeGuardOpaque[base] && claudeWord && !storeGuardRunsString(base, part.Args):
			storeGuardFailOpen(stderr, "cannot read the command %s runs", base)
		case set && !storeGuardInert[base] && !storeGuardRunsString(base, part.Args):
			if dir, ok := storeGuardResolve(value, home, cwd); ok && storeGuardInStore(store, dir) {
				storeGuardFailOpen(stderr, "%s runs on the store and may run claude unseen", base)
			}
		}
	}
	return false
}

// storeGuardFailOpen writes the one stderr line of a call the guard lets
// through without judging it.
func storeGuardFailOpen(stderr io.Writer, format string, args ...any) {
	fmt.Fprintf(stderr, "pfm internal store-guard: "+format+" (fail-open)\n", args...)
}

// storeGuardRunsString reports a shell given a -c string, whose parts the
// parse already walked.
func storeGuardRunsString(base string, args []string) bool {
	if !storeGuardOpaque[base] || base == "eval" || base == "source" || base == "." || base == "env" {
		return false
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
			return true
		}
	}
	return false
}

// storeGuardResolve turns a CLAUDE_CONFIG_DIR value into a physical path: the
// quotes and a leading ~, $HOME or ${HOME} go, a relative path joins cwd. ok
// is false for an empty value, one still holding an expansion, and a relative
// path with no absolute cwd.
func storeGuardResolve(value, home, cwd string) (string, bool) {
	value = strings.TrimSpace(value)
	if n := len(value); n >= 2 && (value[0] == '"' || value[0] == '\'') && value[n-1] == value[0] {
		value = value[1 : n-1]
	}
	for _, prefix := range []string{"${HOME}", "$HOME", "~"} {
		if rest, found := strings.CutPrefix(value, prefix); found && (rest == "" || rest[0] == '/') {
			value = home + rest
			break
		}
	}
	if value == "" || strings.ContainsAny(value, "$`") {
		return "", false
	}
	if !filepath.IsAbs(value) {
		if !filepath.IsAbs(cwd) {
			return "", false
		}
		value = filepath.Join(cwd, value)
	}
	return paths.PhysicalPath(value), true
}

// storeGuardInStore is claudelaunch.InspectConfigDir's store test over a
// physical path: the store or a path inside it, letter case folded, since a
// case-insensitive volume (macOS) opens ~/.CLAUDE as the store.
func storeGuardInStore(store, dir string) bool {
	dir, store = strings.ToLower(dir), strings.ToLower(store)
	return dir == store || strings.HasPrefix(dir, store+string(filepath.Separator))
}

// BashGuard runs the Bash PreToolUse guard the subcommand names: store-guard
// or, for any other name, git-guard.
func BashGuard(name string, input io.Reader, stdout, stderr io.Writer) int {
	if name == "store-guard" {
		return StoreGuard(input, stdout, stderr)
	}
	return GitGuard(input, stdout, stderr)
}

// preToolUseDenyResponse builds the PreToolUse deny hookSpecificOutput
// payload shared by every PreToolUse deny hook in this package.
func preToolUseDenyResponse(reason string) map[string]any {
	return map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": reason,
	}}
}
