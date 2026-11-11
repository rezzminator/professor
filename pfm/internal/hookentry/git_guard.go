package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

type gitGuardHookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	Cwd       string `json:"cwd"`
	AgentType string `json:"agent_type"`
	// AgentID is set only inside a sub-agent; empty means the main chat.
	AgentID string `json:"agent_id"`
}

const (
	// gitGuardExemptAgent is the one writer; only its exact agent_type is exempt.
	gitGuardExemptAgent = "gitter"
	gitGuardWorktreeSh  = ".claude/scripts/worktree.sh"
	gitGuardOnlyGitter  = `Only gitter writes this: spawn Agent(subagent_type: "gitter") with the repo path and the exact change.` +
		" Never retry, rephrase, split or wrap the denied command to get past the guard."
	gitGuardUnreadable = "could not read this command; split it so each git call is its own simple command."
	gitGuardBounded    = "could not read this command in full: it passes a parse bound (over 64 KiB, or a `-c`" +
		" string nested past 8 levels). Shorten the command; write long content to a file with the Write tool."
	gitGuardNoSetupHint = "gitter creates and removes worktrees (Phase SETUP)."
	// gitGuardLost names a directory change the guard cannot follow before a
	// main chat's run of the worktree script by a relative path.
	gitGuardLost = "git-guard cannot follow `%s` to the worktree script it runs:" +
		" run the script by its absolute path, without the directory change."
	// gitGuardScriptHelpers is the directory beside the worktree script whose
	// files an adopter's script sources as its verbs.
	gitGuardScriptHelpers = "worktree"
	gitGuardStashHint     = "To park only your own files, " + "`git stash push -- <path>...`" +
		" is allowed (restore with " + "`git stash pop`" +
		"); a whole-tree stash moves every other agent's uncommitted work."
)

// The sub-verbs git's own words share across the tables below.
const (
	gitGuardVerbList    = "list"
	gitGuardVerbShow    = "show"
	gitGuardVerbApply   = "apply"
	gitGuardVerbForeach = "foreach"
	gitGuardFlagList    = "--list"
)

// The reading sub-verbs, per command ("" is the bare command), and the
// config writes and reads.
var (
	// gitGuardScriptRunners run a script named as an argument.
	gitGuardScriptRunners = strings.Fields("bash sh zsh dash ksh source .")
	gitGuardRemoteReads   = []string{"", gitGuardVerbShow, "get-url"}
	// gitGuardSubmoduleReads; foreach reads only when the command it runs
	// does (gitGuardForeach).
	gitGuardSubmoduleReads = []string{"", "status", "summary"}
	gitGuardNotesReads     = []string{"", gitGuardVerbShow, gitGuardVerbList}
	// gitGuardForeachFlags are submodule foreach's own flags before its command.
	gitGuardForeachFlags = []string{"--quiet", "-q", "--recursive", "--"}
	gitGuardConfigWrites = []string{
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

// gitGuardBlock is one blocked part: the command as its words read, and,
// for a worktree write, the directory its repository is found from; stash
// marks a blocked git stash, whose deny names the path stash.
type gitGuardBlock struct {
	command  string
	worktree bool
	stash    bool
	repoDir  string
	// lost is the directory change, as written, that left a main chat's
	// relative worktree script unfound (gitGuardLost).
	lost string
}

// GitGuard is the PreToolUse hook on Bash that denies a write to shared git
// state (worktrees, history, branches and tags, remotes, the index, wide
// working-tree destruction, repository settings) to every caller but the
// gitter agent; a repo's worktree script is the main chat's (no agent_id)
// alone, run while git tracks it and its worktree/ helpers unchanged from
// HEAD (gitGuardPart). A read or decode failure is fail-open to stderr; a command
// that mentions git (gitGuardMentionsGit) but does not parse is denied, and
// so is every command holding git or worktree.sh that hits a parse bound.
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
	if hook.ToolName != bashTool || hook.AgentType == gitGuardExemptAgent {
		return 0
	}
	reason := gitGuardCheck(hook.ToolInput.Command, hook.Cwd, false, hook.AgentID == "", stderr)
	if reason == "" {
		return 0
	}
	if err := json.NewEncoder(stdout).Encode(preToolUseDenyResponse(reason)); err != nil {
		fmt.Fprintf(stderr, "pfm internal git-guard: write deny: %v\n", err)
		return 1
	}
	return 0
}

// gitGuardCheck judges one Bash command run in cwd and returns its deny
// reason, "" to allow. engine is the Codex and OpenCode reading
// (EngineGuard): a whole stash is a write too, and the reason names no
// gitter, since no git writer runs there. mainChat is a Claude main chat,
// which gitGuardPart lets run the committed worktree script, found from the
// directory the command's cd parts lead to. A parser error is fail-open to
// stderr.
func gitGuardCheck(command, cwd string, engine, mainChat bool, stderr io.Writer) string {
	home := storeGuardGet(guardHomeEnv)
	parsed, err := cmdparse.ParseBatch([]cmdparse.Call{{
		ID: "git-guard", Command: command, Vars: map[string]string{guardHomeEnv: home},
	}})
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal git-guard: parse command (fail-open): %v\n", err)
		return ""
	}
	var blocks []gitGuardBlock
	unreadable, bounded := false, false
	parts := parsed["git-guard"]
	// dir follows the command's cd parts (gitGuardChdir) until one it cannot
	// follow, lost, after which a relative worktree script is unfound.
	dir, lost := cwd, ""
	for i := range parts {
		part := &parts[i]
		if part.Bounded {
			// A bound leaves no read to tell a git call from the text around
			// it, so a command naming git or worktree.sh is denied.
			bounded = bounded || strings.Contains(command, "git") || strings.Contains(command, "worktree.sh")
			continue
		}
		if part.Status == cmdparse.StatusError {
			unreadable = unreadable || gitGuardMentionsGit(command)
			continue
		}
		// An unread -c string runs commands no part shows.
		unreadable = unreadable || (part.Unread && gitGuardMentionsGit(command))
		if block, ok := gitGuardPart(part, dir, lost, engine, mainChat, stderr); ok {
			blocks = append(blocks, block)
		}
		if lost == "" && slices.Contains(pcmGuardChdirs, filepath.Base(part.Program)) {
			if next, ok := gitGuardChdir(part, dir, home); ok {
				dir = next
			} else {
				lost = strings.TrimSpace(part.Program + " " + strings.Join(part.Args, " "))
			}
		}
	}
	if len(blocks) == 0 && !unreadable && !bounded {
		return ""
	}
	return gitGuardReason(blocks, unreadable, bounded, engine)
}

// gitGuardMentionsGit reports a command naming git as a program could: the
// token git with the string's edge or a non-word byte (anything outside
// [A-Za-z0-9_]) on each side, so `"git"`, `git;`, `git<` and git before a
// tab match while digits, legit and mygit do not; or the worktree script. A
// command that never does reaches git only through obfuscation, so a part of
// it the parser could not read is not the git guard's to refuse.
func gitGuardMentionsGit(command string) bool {
	if strings.Contains(command, gitGuardWorktreeSh) {
		return true
	}
	for from := 0; ; {
		i := strings.Index(command[from:], "git")
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len("git")
		if (start == 0 || !gitGuardWordByte(command[start-1])) &&
			(end == len(command) || !gitGuardWordByte(command[end])) {
			return true
		}
		from = end
	}
}

// gitGuardWordByte reports a word byte: [A-Za-z0-9_].
func gitGuardWordByte(b byte) bool {
	return b == '_' || ('0' <= b && b <= '9') || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

// gitGuardPart decides one parsed part: a git call, or a run of a repo's
// worktree script, as the program or under a shell, with any verb or none.
// The script is a Claude main chat's alone, and only while it and its
// worktree/ helpers are committed unchanged (gitGuardScriptCommitted), so no
// caller can widen it by editing it first; every other caller goes to gitter.
// dir is the directory the part runs in; lost, when set, the directory
// change that left dir unknown, so only an absolute script path is found.
func gitGuardPart(
	part *cmdparse.Part, dir, lost string, engine, mainChat bool, stderr io.Writer,
) (gitGuardBlock, bool) {
	written := strings.TrimSpace(part.Program + " " + strings.Join(part.Args, " "))
	isScript := func(word string) bool { return strings.HasSuffix(word, gitGuardWorktreeSh) }
	script := ""
	if isScript(part.Program) {
		script = part.Program
	} else if slices.Contains(gitGuardScriptRunners, filepath.Base(part.Program)) {
		if i := slices.IndexFunc(part.Args, isScript); i >= 0 {
			script = part.Args[i]
		}
	}
	if script != "" {
		found := filepath.IsAbs(script) || lost == ""
		abs := gitGuardJoin(dir, script)
		if mainChat && !engine && found && filepath.IsAbs(abs) && gitGuardScriptCommitted(abs) {
			return gitGuardBlock{}, false
		}
		block := gitGuardBlock{command: written, worktree: true, repoDir: dir}
		if mainChat && !engine && !found {
			block.lost = lost
		}
		return block, true
	}
	if filepath.Base(part.Program) != "git" {
		return gitGuardBlock{}, false
	}
	repoDir, sub, rest := gitGuardSplit(part.Args, dir)
	blocked, worktree := gitGuardBlocks(sub, rest, repoDir, engine, stderr)
	return gitGuardBlock{command: written, worktree: worktree, stash: sub == "stash", repoDir: repoDir}, blocked
}

// gitGuardChdir is the directory a lasting `cd` run in dir moves to: home
// for a bare cd or `~`, a literal path joined onto dir. ok is false for one
// it cannot follow: a pushd or popd, a cd that is not Lasting (a subshell, a
// -c string, a condition), `cd -`, or a target the parse could not know.
// shortcut: CDPATH is not read, so a relative target CDPATH would redirect
// is judged against dir; read it if a relative cd ever reaches a script.
func gitGuardChdir(part *cmdparse.Part, dir, home string) (string, bool) {
	if filepath.Base(part.Program) != "cd" || !part.Lasting {
		return "", false
	}
	operands, _ := pcmGuardSplit(part.Args)
	target := home
	if len(operands) > 0 {
		target = operands[0]
	}
	if target == "-" || strings.ContainsAny(target, "$`") {
		return "", false
	}
	if target == "~" || strings.HasPrefix(target, "~/") {
		target = home + target[1:]
	}
	if !filepath.IsAbs(target) && !filepath.IsAbs(dir) {
		return "", false
	}
	return filepath.Clean(gitGuardJoin(dir, target)), true
}

// gitGuardScriptCommitted reports a worktree script git tracks unchanged
// from HEAD (engineGuardCommitted) whose worktree/ helper directory, when one
// sits beside it, holds no modified, staged, untracked or ignored file. Any
// failure (no repo, git missing, an unreadable directory) is uncommitted.
func gitGuardScriptCommitted(abs string) bool {
	if !engineGuardCommitted(abs) {
		return false
	}
	helpers := filepath.Join(filepath.Dir(abs), gitGuardScriptHelpers)
	info, err := os.Stat(helpers)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir()) {
		return true
	}
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), engineGuardGitTimeout)
	defer cancel()
	status, err := obs.Runner(deps.RealRunner{}).Run(ctx, []string{
		"git", "-C", helpers, "status", "--porcelain", "--untracked-files=all", "--ignored", "--", ".",
	}, deps.RunOptions{})
	return err == nil && status.ExitCode == 0 && strings.TrimSpace(string(status.Stdout)) == ""
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
// whether that write is a worktree write; engine blocks every stash but its
// list and show. A subcommand no case judges is allowed only when the read
// allowlist names it (engineGuardGitReads).
func gitGuardBlocks(
	sub string, rest []string, repoDir string, engine bool, stderr io.Writer,
) (blocked, worktree bool) {
	first := gitGuardFirstOperand(rest)
	switch sub {
	case "worktree":
		w := first != gitGuardVerbList
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
		return !slices.Contains(gitGuardRemoteReads, first), false
	case gitGuardVerbApply:
		readOnly := gitGuardHas(rest, "--check", "--stat", "--numstat", "--summary")
		return gitGuardHas(rest, "--index", "--cached", "-3", "--3way") && !readOnly, false
	case "hash-object":
		return gitGuardHas(rest, "-w"), false
	case "stash":
		if engine {
			return first != gitGuardVerbList && first != gitGuardVerbShow, false
		}
		return gitGuardStash(rest, repoDir), false
	case "config":
		return gitGuardConfigWrite(rest), false
	case "notes":
		return !slices.Contains(gitGuardNotesReads, first), false
	case "submodule":
		if first == gitGuardVerbForeach {
			return gitGuardForeach(rest, repoDir, engine, stderr), false
		}
		return !slices.Contains(gitGuardSubmoduleReads, first), false
	case "reflog":
		return !slices.Contains(engineGuardGitReflogReads, first), false
	}
	// Git reads by allowlist: a subcommand no case above judges and no read
	// names (archive, bisect, sparse-checkout, init, an alias) is blocked.
	return sub != "" && !slices.Contains(engineGuardGitReads, sub), false
}

// gitGuardForeach blocks a submodule foreach whose command, its words after
// foreach and foreach's own flags joined as the shell string git runs, the
// same judge would deny.
func gitGuardForeach(rest []string, repoDir string, engine bool, stderr io.Writer) bool {
	words := rest[slices.Index(rest, gitGuardVerbForeach)+1:]
	for len(words) > 0 && slices.Contains(gitGuardForeachFlags, words[0]) {
		words = words[1:]
	}
	return gitGuardCheck(strings.Join(words, " "), repoDir, engine, false, stderr) != ""
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

// gitGuardConfigWrite reports a config call that is not a read: a write
// flag; else, outside a legacy read flag, a subcommand (git 2.46: a first
// operand with no dot, never a key) other than get and list, or a key given
// a value.
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
	return len(operands) != 1 || !strings.Contains(operands[0], ".")
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
// "*", a path that resolves to the repository's top level, or an all-star
// glob (gitGuardAllStarGlob) whose literal prefix is empty, ".", or resolves
// to the top level.
func gitGuardAnyWide(paths []string, repoDir string) bool {
	top := ""
	resolvesToTop := func(p string) bool {
		if repoDir == "" {
			return false
		}
		if top == "" {
			top = gitGuardTopLevel(repoDir)
		}
		return top != "" && filepath.Clean(gitGuardJoin(repoDir, p)) == top
	}
	for _, p := range paths {
		switch strings.TrimSuffix(p, "/") {
		case ".", "", ":", ":/", ":/.", "*", ":(top)":
			return true
		}
		if prefix, ok := gitGuardAllStarGlob(p); ok && (prefix == "" || prefix == "." || resolvesToTop(prefix)) {
			return true
		}
		if resolvesToTop(p) {
			return true
		}
	}
	return false
}

// gitGuardAllStarGlob returns a pathspec's literal prefix and reports whether
// at least one element follows it and every one is all-star: only `*` and
// `?`, at least one `*`. The pathspec loses one leading magic prefix
// (`:(…)`, `:/` or a lone `:`) and a leading `./`, then a trailing `/`, and
// splits into elements on `/`; the literal prefix is the elements before the
// first holding `*` or `?`.
func gitGuardAllStarGlob(p string) (string, bool) {
	switch {
	case strings.HasPrefix(p, ":("):
		if end := strings.IndexByte(p, ')'); end >= 0 {
			p = p[end+1:]
		}
	case strings.HasPrefix(p, ":/"):
		p = p[len(":/"):]
	case strings.HasPrefix(p, ":"):
		p = p[len(":"):]
	}
	elems := strings.Split(strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/"), "/")
	first := slices.IndexFunc(elems, func(e string) bool { return strings.ContainsAny(e, "*?") })
	if first < 0 {
		return "", false
	}
	for _, e := range elems[first:] {
		if strings.Trim(e, "*?") != "" || !strings.Contains(e, "*") {
			return "", false
		}
	}
	return strings.Join(elems[:first], "/"), true
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
// named, the path-stash line when a stash was blocked, the unreadable-command
// line, and the parse-bound line. The bound line alone sends nobody to
// gitter: a shortened command is read and judged like any other. engine
// swaps the gitter instruction for the engine ban and drops the gitter ways.
func gitGuardReason(blocks []gitGuardBlock, unreadable, bounded, engine bool) string {
	route := gitGuardOnlyGitter
	if engine {
		route = engineGuardGitBan
	}
	var lines []string
	if len(blocks) > 0 {
		named := make([]string, 0, len(blocks))
		for _, block := range blocks {
			named = append(named, "`"+block.command+"`")
		}
		lines = append(lines, "git-guard blocked "+strings.Join(named, ", ")+".", route)
	}
	for _, block := range blocks {
		if engine || !block.worktree {
			continue
		}
		hint := gitGuardNoSetupHint
		if top := gitGuardTopLevel(block.repoDir); top != "" {
			script := filepath.Join(top, filepath.FromSlash(gitGuardWorktreeSh))
			if info, err := os.Stat(script); err == nil && !info.IsDir() {
				hint = "worktrees go through " + script + " — the main chat's alone, run while git tracks it (and its" +
					" worktree/ helpers) unchanged from HEAD; every other caller goes to gitter."
			}
		}
		if !slices.Contains(lines, hint) {
			lines = append(lines, hint)
		}
	}
	for _, block := range blocks {
		if !engine && block.stash && !slices.Contains(lines, gitGuardStashHint) {
			lines = append(lines, gitGuardStashHint)
		}
		if lost := fmt.Sprintf(gitGuardLost, block.lost); block.lost != "" && !slices.Contains(lines, lost) {
			lines = append(lines, lost)
		}
	}
	if unreadable {
		// No route line: splitting is this deny's fix, and the route forbids splitting.
		lines = append(lines, "git-guard "+gitGuardUnreadable)
	}
	if bounded {
		lines = append(lines, "git-guard "+gitGuardBounded)
	}
	return strings.Join(lines, "\n")
}
