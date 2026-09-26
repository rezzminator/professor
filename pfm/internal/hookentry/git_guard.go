package hookentry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/callmeter/cmdparse"
)

type gitGuardHookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	Cwd       string `json:"cwd"`
	AgentType string `json:"agent_type"`
}

const (
	// gitGuardExemptAgent is the one agent type that writes shared git state.
	gitGuardExemptAgent = "gitter"
	gitGuardWorktreeSh  = ".claude/scripts/worktree.sh"
	gitGuardOnlyGitter  = `Only gitter writes this: spawn Agent(subagent_type: "gitter") with the repo path and the exact change.`
	gitGuardUnreadable  = "could not read this command; split it so each git call is its own simple command."
	gitGuardNoSetupHint = "gitter creates and removes worktrees (Phase SETUP)."
	gitGuardStashHint   = "To park only your own files, " + "`git stash push -- <path>...`" +
		" is allowed (restore with " + "`git stash pop`" +
		"); a whole-tree stash moves every other agent's uncommitted work."
)

// The sub-verbs git's own words share across the tables below.
const (
	gitGuardVerbAdd    = "add"
	gitGuardVerbRemove = "remove"
	gitGuardVerbPrune  = "prune"
	gitGuardVerbUpdate = "update"
	gitGuardVerbList   = "list"
	gitGuardVerbShow   = "show"
	gitGuardFlagList   = "--list"
)

// The sub-verbs that write, per command, and the config and notes reads.
var (
	gitGuardScriptWrites   = []string{"create", gitGuardVerbRemove, gitGuardVerbPrune}
	gitGuardWorktreeWrites = []string{
		gitGuardVerbAdd, gitGuardVerbRemove, gitGuardVerbPrune, "move", "lock", "unlock", "repair",
	}
	gitGuardRemoteWrites = []string{
		gitGuardVerbAdd, gitGuardVerbRemove, "rm", "rename", "set-url", "set-head", "set-branches",
		gitGuardVerbPrune, gitGuardVerbUpdate,
	}
	gitGuardSubmoduleWrites = []string{gitGuardVerbAdd, gitGuardVerbUpdate, "deinit", "sync"}
	gitGuardNotesReads      = []string{"", gitGuardVerbShow, gitGuardVerbList}
	gitGuardConfigWrites    = []string{
		"--add", "--unset", "--unset-all", "--replace-all", "--rename-section", "--remove-section", "-e", "--edit",
	}
	gitGuardConfigReads = []string{
		"--get", "--get-all", "--get-regexp", "--get-urlmatch", gitGuardFlagList, "-l", "--show-origin",
	}
)

// The flags that make branch and tag write a ref, and the flags that put them
// in list mode, where an operand is a pattern or a commit, not a new name.
var (
	gitGuardBranchWrites = []string{
		"-d", "-D", "-m", "-M", "-c", "-C", "-f", "--delete", "--move", "--copy", "--force",
		"-u", "--set-upstream-to", "--unset-upstream", "--edit-description", "-t", "--track",
	}
	gitGuardBranchLists = []string{
		gitGuardFlagList, "-l", "-a", "--all", "-r", "--remotes", "--show-current",
		"--contains", "--no-contains", "--merged", "--no-merged", "--points-at",
	}
	gitGuardTagWrites = []string{
		"-d", "--delete", "-a", "--annotate", "-s", "--sign", "-f", "--force",
		"-m", "--message", "-F", "--file", "-u", "--local-user",
	}
	gitGuardTagLists = []string{
		"-l", gitGuardFlagList, "--contains", "--no-contains", "--points-at",
		"--merged", "--no-merged", "-v", "--verify",
	}
)

// gitGuardNoPython answers every Python snippet with no result, so the hook
// never spawns python3: a git call inside a Python snippet is not inspected
// (docs/design/hooks/git-guard.md § Named gaps).
type gitGuardNoPython struct{}

func (gitGuardNoPython) Analyze(context.Context, []cmdparse.Snippet) ([]cmdparse.PyResult, error) {
	return nil, nil
}

// gitGuardBlock is one blocked part: the command as its words read, and,
// for a worktree write, the directory its repository is found from; stash
// marks a blocked git stash, whose deny names the path stash.
type gitGuardBlock struct {
	command  string
	worktree bool
	stash    bool
	repoDir  string
}

// GitGuard is the PreToolUse hook on Bash that denies a write to shared git
// state (worktrees, history, branches and tags, remotes, the index, wide
// working-tree destruction, repository settings) to every caller but the
// gitter agent. A read or decode failure is fail-open to stderr; a command
// that mentions git but does not parse is denied.
func GitGuard(input io.Reader, stdout, stderr io.Writer) int {
	raw, err := io.ReadAll(input)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal git-guard: read hook payload (fail-open): %v\n", err)
		return 0
	}
	if len(raw) == 0 {
		return 0
	}
	var hook gitGuardHookInput
	if err := json.Unmarshal(raw, &hook); err != nil {
		fmt.Fprintf(stderr, "pfm internal git-guard: decode hook payload (fail-open): %v\n", err)
		return 0
	}
	command := hook.ToolInput.Command
	if hook.ToolName != "Bash" || hook.AgentType == gitGuardExemptAgent ||
		(!strings.Contains(command, "git") && !strings.Contains(command, "worktree.sh")) {
		return 0
	}
	cwd := hook.Cwd
	parseCwd := cwd
	if !filepath.IsAbs(parseCwd) {
		parseCwd = string(filepath.Separator)
	}
	parsed, err := cmdparse.ParseBatch(context.Background(),
		[]cmdparse.Call{{ID: "git-guard", Command: command, Cwd: parseCwd}}, gitGuardNoPython{})
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal git-guard: parse command (fail-open): %v\n", err)
		return 0
	}
	var blocks []gitGuardBlock
	unreadable := false
	parts := parsed["git-guard"]
	for i := range parts {
		part := &parts[i]
		if part.Status == cmdparse.StatusError {
			unreadable = unreadable || strings.Contains(command, "git ")
			continue
		}
		if block, ok := gitGuardPart(part, cwd); ok {
			blocks = append(blocks, block)
		}
	}
	if len(blocks) == 0 && !unreadable {
		return 0
	}
	if err := json.NewEncoder(stdout).Encode(preToolUseDenyResponse(gitGuardReason(blocks, unreadable))); err != nil {
		fmt.Fprintf(stderr, "pfm internal git-guard: write deny: %v\n", err)
		return 1
	}
	return 0
}

// gitGuardPart decides one parsed part: a git call, or a repo's worktree
// script run with create, remove or prune.
func gitGuardPart(part *cmdparse.Part, cwd string) (gitGuardBlock, bool) {
	written := strings.TrimSpace(part.Program + " " + strings.Join(part.Args, " "))
	scriptIsProgram := strings.HasSuffix(part.Program, gitGuardWorktreeSh)
	if scriptIsProgram || (len(part.Args) > 0 && strings.HasSuffix(part.Args[0], gitGuardWorktreeSh)) {
		verbs := part.Args
		if !scriptIsProgram {
			verbs = part.Args[1:]
		}
		if len(verbs) > 0 && slices.Contains(gitGuardScriptWrites, verbs[0]) {
			return gitGuardBlock{command: written, worktree: true, repoDir: cwd}, true
		}
		return gitGuardBlock{}, false
	}
	if filepath.Base(part.Program) != "git" {
		return gitGuardBlock{}, false
	}
	repoDir, sub, rest := gitGuardSplit(part.Args, cwd)
	blocked, worktree := gitGuardBlocks(sub, rest, repoDir)
	return gitGuardBlock{command: written, worktree: worktree, stash: sub == "stash", repoDir: repoDir}, blocked
}

// gitGuardSplit skips git's global options and returns the repository
// directory (-C, joined onto cwd), the subcommand and its arguments.
func gitGuardSplit(args []string, cwd string) (string, string, []string) {
	repoDir := cwd
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-C":
			if i+1 < len(args) {
				if filepath.IsAbs(args[i+1]) || repoDir == "" {
					repoDir = args[i+1]
				} else {
					repoDir = filepath.Join(repoDir, args[i+1])
				}
			}
			i += 2
		case slices.Contains([]string{"-c", "--git-dir", "--work-tree", "--namespace", "--config-env", "--super-prefix"}, a):
			i += 2
		case strings.HasPrefix(a, "-"):
			i++
		default:
			return repoDir, a, args[i+1:]
		}
	}
	return repoDir, "", nil
}

// gitGuardBlocks reports whether one git subcommand writes shared state, and
// whether that write is a worktree write.
func gitGuardBlocks(sub string, rest []string, repoDir string) (blocked, worktree bool) {
	first := gitGuardFirstOperand(rest)
	switch sub {
	case "worktree":
		w := slices.Contains(gitGuardWorktreeWrites, first)
		return w, w
	case "commit", "merge", "rebase", "cherry-pick", "revert", "am", "pull", "push", "fetch", "switch",
		"update-ref", "mv", "update-index", "gc", "prune", "filter-branch", "filter-repo", "replace":
		return true, false
	case "reset":
		// A mode moves history; paths without a mode unstage; bare reset
		// unstages everything. Every form writes the shared index or HEAD.
		return true, false
	case "add", "rm", "clean":
		return !gitGuardHasShortFlag(rest, 'n', "--dry-run"), false
	case "checkout":
		return gitGuardCheckout(rest, repoDir), false
	case "restore":
		staged := gitGuardHas(rest, "--staged", "-S")
		return staged || gitGuardAnyWide(gitGuardOperands(rest, "-s", "--source"), repoDir), false
	case "branch":
		return gitGuardRefWrite(rest, gitGuardBranchWrites, gitGuardBranchLists), false
	case "tag":
		return gitGuardRefWrite(rest, gitGuardTagWrites, gitGuardTagLists), false
	case "symbolic-ref":
		return gitGuardHas(rest, "-d", "--delete") || len(gitGuardOperands(rest)) >= 2, false
	case "remote":
		return slices.Contains(gitGuardRemoteWrites, first), false
	case "apply":
		readOnly := gitGuardHas(rest, "--check", "--stat", "--numstat", "--summary")
		return gitGuardHas(rest, "--index", "--cached", "-3", "--3way") && !readOnly, false
	case "hash-object":
		return gitGuardHas(rest, "-w"), false
	case "stash":
		return gitGuardStash(rest, repoDir), false
	case "config":
		return gitGuardConfigWrite(rest), false
	case "notes":
		return !slices.Contains(gitGuardNotesReads, first), false
	case "submodule":
		return slices.Contains(gitGuardSubmoduleWrites, first), false
	}
	return false, false
}

// gitGuardCheckout blocks a branch switch or creation and a pathspec that
// covers the whole tree; it allows restoring named paths.
func gitGuardCheckout(rest []string, repoDir string) bool {
	if gitGuardHas(rest, "-b", "-B", "--orphan", "-t", "--track", "--detach") {
		return true
	}
	if dd := slices.Index(rest, "--"); dd >= 0 {
		return gitGuardAnyWide(rest[dd+1:], repoDir)
	}
	operands := gitGuardOperands(rest)
	if gitGuardAnyWide(operands, repoDir) {
		return true
	}
	if len(operands) == 1 {
		// One operand that is not an existing path is a branch or commit.
		_, err := os.Lstat(gitGuardJoin(repoDir, operands[0]))
		return err != nil
	}
	return false
}

// gitGuardStash allows pop, apply, list, show and create, and a push naming
// its paths; it blocks a whole-tree stash, drop, clear, store and branch.
func gitGuardStash(rest []string, repoDir string) bool {
	if len(rest) == 0 {
		return true
	}
	switch rest[0] {
	case "pop", "apply", gitGuardVerbList, gitGuardVerbShow, "create":
		return false
	case "push":
		rest = rest[1:]
	default:
		if !strings.HasPrefix(rest[0], "-") {
			// save (its operand is a message, never a path), drop, clear,
			// store, branch, and anything newer.
			return true
		}
	}
	var paths []string
	if dd := slices.Index(rest, "--"); dd >= 0 {
		paths = rest[dd+1:]
	} else {
		paths = gitGuardOperands(rest, "-m", "--message", "--pathspec-from-file")
	}
	return len(paths) == 0 || gitGuardAnyWide(paths, repoDir)
}

// gitGuardConfigWrite reports a config call that is not a read.
func gitGuardConfigWrite(rest []string) bool {
	if gitGuardHas(rest, gitGuardConfigWrites...) {
		return true
	}
	if gitGuardHas(rest, gitGuardConfigReads...) {
		return false
	}
	operands := gitGuardOperands(rest, "-f", "--file", "--blob", "--type", "--default")
	if len(operands) > 0 && (operands[0] == "get" || operands[0] == gitGuardVerbList) {
		return false
	}
	return len(operands) != 1
}

// gitGuardRefWrite blocks a write flag, or a name given outside list mode.
func gitGuardRefWrite(rest, writes, lists []string) bool {
	for _, a := range rest {
		name, _, _ := strings.Cut(a, "=")
		if slices.Contains(writes, name) {
			return true
		}
	}
	for _, a := range rest {
		name, _, _ := strings.Cut(a, "=")
		if slices.Contains(lists, name) {
			return false
		}
	}
	return len(gitGuardOperands(rest)) > 0
}

// gitGuardOperands returns the arguments that are not flags, skipping the
// value after each flag named in valued.
func gitGuardOperands(rest []string, valued ...string) []string {
	var out []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--":
			return append(out, rest[i+1:]...)
		case slices.Contains(valued, a):
			i++
		case a == "-" || !strings.HasPrefix(a, "-"):
			out = append(out, a)
		}
	}
	return out
}

func gitGuardFirstOperand(rest []string) string {
	if operands := gitGuardOperands(rest, "--ref"); len(operands) > 0 {
		return operands[0]
	}
	return ""
}

// gitGuardHas reports whether any argument before "--" is one of flags,
// alone or as flag=value.
func gitGuardHas(rest []string, flags ...string) bool {
	for _, a := range rest {
		if a == "--" {
			return false
		}
		name, _, _ := strings.Cut(a, "=")
		if slices.Contains(flags, name) {
			return true
		}
	}
	return false
}

// gitGuardHasShortFlag reports the long flag, or the short letter alone or
// inside a short-flag cluster (-nd), before "--".
func gitGuardHasShortFlag(rest []string, letter rune, long string) bool {
	for _, a := range rest {
		if a == "--" {
			return false
		}
		if a == long || (len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], letter)) {
			return true
		}
	}
	return false
}

// gitGuardAnyWide reports a pathspec that covers the whole tree: ".", ":/",
// "*", or a path that resolves to the repository's top level.
func gitGuardAnyWide(paths []string, repoDir string) bool {
	top := ""
	for _, p := range paths {
		switch strings.TrimSuffix(p, "/") {
		case ".", "", ":", ":/", ":/.", "*", ":(top)":
			return true
		}
		if repoDir == "" {
			continue
		}
		if top == "" {
			top = gitGuardTopLevel(repoDir)
		}
		if top != "" && filepath.Clean(gitGuardJoin(repoDir, p)) == top {
			return true
		}
	}
	return false
}

func gitGuardJoin(dir, p string) string {
	if filepath.IsAbs(p) || dir == "" {
		return p
	}
	return filepath.Join(dir, p)
}

// gitGuardTopLevel walks up from dir to the directory holding a .git entry
// (a directory, or a worktree's .git file) without running git; empty when
// none is found.
func gitGuardTopLevel(dir string) string {
	if !filepath.IsAbs(dir) {
		return ""
	}
	for current := filepath.Clean(dir); ; {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// gitGuardReason is the one deny message: every blocked part as written, the
// gitter instruction, the worktree way for each repository a worktree write
// named, the path-stash line when a stash was blocked, and the
// unreadable-command line.
func gitGuardReason(blocks []gitGuardBlock, unreadable bool) string {
	var lines []string
	if len(blocks) > 0 {
		named := make([]string, 0, len(blocks))
		for _, block := range blocks {
			named = append(named, "`"+block.command+"`")
		}
		lines = append(lines, "git-guard blocked "+strings.Join(named, ", ")+".", gitGuardOnlyGitter)
	}
	for _, block := range blocks {
		if !block.worktree {
			continue
		}
		hint := gitGuardNoSetupHint
		if top := gitGuardTopLevel(block.repoDir); top != "" {
			script := filepath.Join(top, filepath.FromSlash(gitGuardWorktreeSh))
			if info, err := os.Stat(script); err == nil && !info.IsDir() {
				hint = "gitter creates and removes worktrees with " + script + " create|remove|prune — the only right way here."
			}
		}
		if !slices.Contains(lines, hint) {
			lines = append(lines, hint)
		}
	}
	for _, block := range blocks {
		if block.stash && !slices.Contains(lines, gitGuardStashHint) {
			lines = append(lines, gitGuardStashHint)
		}
	}
	if unreadable {
		lines = append(lines, "git-guard "+gitGuardUnreadable)
		if len(blocks) == 0 {
			lines = append(lines, gitGuardOnlyGitter)
		}
	}
	return strings.Join(lines, "\n")
}
