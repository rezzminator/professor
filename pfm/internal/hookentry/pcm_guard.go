package hookentry

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type pcmGuardHookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
	Cwd       string `json:"cwd"`
	AgentType string `json:"agent_type"`
}

const (
	// pcmGuardWriter writes every prompt file but a theme, pcmGuardThemer
	// only a theme; only their exact agent_type is exempt.
	pcmGuardWriter = "pcm"
	pcmGuardThemer = "themer"
	pcmGuardRoute  = "pcm-guard: Only %[1]s writes %[2]s: spawn Agent(subagent_type: %[1]q) with the exact change;" +
		" a sub-agent returns the change as a proposal; %[3]s"
	// pcm ships machine-global, so a missing one is a stale install; themer is
	// Professor's project agent, so a missing one is another project's file.
	pcmGuardPcmTail = "pcm is machine-global: no pcm registered means this host's install is stale" +
		" → run pfm install --yes."
	pcmGuardThemerTail = "no themer registered here → ask the user to dispatch themer from the project that owns" +
		" the file."
	pcmGuardUnreadable = "pcm-guard could not read this command, and it names a prompt file (CLAUDE.md, .claude," +
		" templates): split it into simple commands with literal paths; a prompt-file write goes to pcm:" +
		` spawn Agent(subagent_type: "pcm") with the exact change.`
	// pcmGuardUnknownRoot stands for a directory the parse could not know
	// (after `cd "$X"`, or before an expansion), so only the names after it
	// are judged.
	pcmGuardUnknownRoot = "/__pcm-guard-unknown__"
	pcmGuardPushd       = "pushd"
	pcmGuardPopd        = "popd"
	pcmGuardDash        = "dash"
)

var (
	// pcmGuardEditTools carry the path they write in tool_input.
	pcmGuardEditTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}
	// pcmGuardConfigProtected are the prompt directories of a Claude config dir.
	pcmGuardConfigProtected = strings.Fields("agents commands rules skills")
	// pcmGuardShells run a heredoc fed to them as code; pcmGuardChdirs move
	// the directory a later relative word joins.
	pcmGuardShells = strings.Fields("sh bash zsh dash ksh")
	pcmGuardChdirs = strings.Fields("cd pushd popd")
	// pcmGuardMaxDirs bounds the directories one call is judged in; past it
	// the command counts as unreadable.
	pcmGuardMaxDirs = 32
	// pcmGuardLongCode are the long flags whose value is inline code.
	pcmGuardLongCode = strings.Fields("--eval --print")
	// pcmGuardIntoFlags name a copy's target directory; -S is its backup suffix.
	pcmGuardIntoFlags = strings.Fields("-t --target-directory -S --suffix")
	// pcmGuardWriteOps are the redirections that write their target.
	pcmGuardWriteOps = []string{">", ">>", ">|", "<>", "&>", "&>>"}
	// pcmGuardCopyValued are the flags taking a value, per copying program.
	pcmGuardCopyValued = map[string][]string{
		"cp":      pcmGuardIntoFlags,
		"mv":      pcmGuardIntoFlags,
		"ln":      pcmGuardIntoFlags,
		"install": append(strings.Fields("-m --mode -o --owner -g --group"), pcmGuardIntoFlags...),
		"rsync":   strings.Fields("-e --rsh -f --filter -T --temp-dir -B --block-size -M --exclude --include"),
	}
)

// pcmGuardScope is what a path is judged against: home, the Professor clone
// (physical, "" when unresolved) and the Claude config dirs, as written and
// physical.
type pcmGuardScope struct {
	home, clone string
	configs     []string
}

// pcmGuardJudge collects one call's verdict: the paths only pcm writes, the
// themes only themer writes, and whether a write target could not be read.
type pcmGuardJudge struct {
	scope              pcmGuardScope
	agent              string
	pcmOnly, themeOnly []string
	unreadable         bool
}

// PcmGuard is the PreToolUse hook on the edit tools and Bash that denies a
// write to a prompt file — any CLAUDE.md, anything under a .claude/ directory
// (in a Claude config dir only agents/, commands/, rules/ and skills/), the
// Professor clone's templates/ — to every caller but the pcm agent, and a
// write under templates/themes/ to every caller but themer. It keeps no
// state: the payload's exact agent_type is the exemption. A read or decode
// failure and a parser error are fail-open to stderr; a write target or a
// part it cannot read is denied when the command names a prompt file.
func PcmGuard(input io.Reader, stdout, stderr io.Writer) int {
	raw, err := io.ReadAll(input)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal pcm-guard: read hook payload (fail-open): %v\n", err)
		return 0
	}
	if len(raw) == 0 {
		return 0
	}
	var hook pcmGuardHookInput
	if err := json.Unmarshal(raw, &hook); err != nil {
		fmt.Fprintf(stderr, "pfm internal pcm-guard: decode hook payload (fail-open): %v\n", err)
		return 0
	}
	editing := slices.Contains(pcmGuardEditTools, hook.ToolName)
	if !editing && hook.ToolName != bashTool {
		return 0
	}
	cwd := hook.Cwd
	if !filepath.IsAbs(cwd) {
		cwd = ""
	}
	judge := &pcmGuardJudge{scope: pcmGuardNewScope(stderr), agent: hook.AgentType}
	if editing {
		if target := cmp.Or(hook.ToolInput.FilePath, hook.ToolInput.NotebookPath); target != "" {
			if !filepath.IsAbs(target) {
				target = filepath.Join(cmp.Or(cwd, pcmGuardUnknownRoot), target)
			}
			judge.path(filepath.Clean(target))
		}
	} else if err := judge.bash(hook.ToolInput.Command, cwd); err != nil {
		fmt.Fprintf(stderr, "pfm internal pcm-guard: parse command (fail-open): %v\n", err)
		return 0
	}
	judge.unreadable = judge.unreadable && pcmGuardMentions(hook.ToolInput.Command, hook.AgentType)
	if len(judge.pcmOnly) == 0 && len(judge.themeOnly) == 0 && !judge.unreadable {
		return 0
	}
	if err := json.NewEncoder(stdout).Encode(preToolUseDenyResponse(judge.reason())); err != nil {
		fmt.Fprintf(stderr, "pfm internal pcm-guard: write deny: %v\n", err)
		return 1
	}
	return 0
}

// pcmGuardNewScope reads home, the clone the way pfm install finds it
// (paths.GlobalSourceRepo) and the config dirs: ~/.claude and the chat's
// CLAUDE_CONFIG_DIR. What it cannot read leaves that root unguarded, with
// one stderr line.
func pcmGuardNewScope(stderr io.Writer) pcmGuardScope {
	home := storeGuardGet("HOME")
	scope := pcmGuardScope{home: home}
	dirs := []string{storeGuardGet(storeGuardEnv)}
	if home != "" {
		dirs = append(dirs, claudelaunch.ClaudeStore(home))
	}
	for _, dir := range dirs {
		if filepath.IsAbs(dir) {
			scope.configs = append(scope.configs, filepath.Clean(dir), paths.PhysicalPath(dir))
		}
	}
	if home == "" {
		fmt.Fprintln(
			stderr,
			"pfm internal pcm-guard: HOME is empty; ~/.claude and the Professor clone are judged by name only (fail-open)",
		)
		return scope
	}
	clone, err := paths.GlobalSourceRepo(home)
	if err != nil {
		fmt.Fprintf(
			stderr,
			"pfm internal pcm-guard: resolve the Professor clone; its templates/ is unguarded (fail-open): %v\n",
			err,
		)
		return scope
	}
	scope.clone = paths.PhysicalPath(clone)
	return scope
}

// class judges one absolute path as written and with its symlinks resolved
// through the deepest existing ancestor: the first protected form decides,
// theme when it lies under a templates/themes/.
func (scope pcmGuardScope) class(abs string) (protected, theme bool) {
	for _, form := range []string{abs, paths.PhysicalPath(abs)} {
		if protected, theme = scope.classOne(form); protected {
			return protected, theme
		}
	}
	return false, false
}

func (scope pcmGuardScope) classOne(path string) (protected, theme bool) {
	if strings.EqualFold(filepath.Base(path), "CLAUDE.md") {
		return true, false
	}
	if rel, ok := pcmGuardUnder(path, scope.clone); ok {
		elems := strings.Split(rel, string(filepath.Separator))
		if len(elems) > 2 && elems[0] == ".worktrees" {
			// A flight worktree of the clone carries the same templates.
			elems = elems[2:]
		}
		if elems[0] == "templates" {
			return true, len(elems) > 1 && elems[1] == "themes"
		}
	}
	for _, config := range scope.configs {
		if rel, ok := pcmGuardUnder(path, config); ok {
			first, _, _ := strings.Cut(rel, string(filepath.Separator))
			return slices.Contains(pcmGuardConfigProtected, first), false
		}
	}
	for _, elem := range strings.Split(path, string(filepath.Separator)) {
		if strings.EqualFold(elem, ".claude") {
			return true, false
		}
	}
	return false, false
}

// pcmGuardUnder returns path relative to root ("" for root itself) when
// path is root or inside it, letter case folded as storeGuardInStore does.
func pcmGuardUnder(path, root string) (string, bool) {
	if root == "" || len(path) < len(root) || !strings.EqualFold(path[:len(root)], root) {
		return "", false
	}
	if len(path) == len(root) {
		return "", true
	}
	if path[len(root)] != filepath.Separator {
		return "", false
	}
	return path[len(root)+1:], true
}

// path records one absolute path the call writes when the caller may not.
func (judge *pcmGuardJudge) path(abs string) {
	protected, theme := judge.scope.class(abs)
	switch {
	case !protected:
	case theme && judge.agent != pcmGuardThemer:
		if !slices.Contains(judge.themeOnly, abs) {
			judge.themeOnly = append(judge.themeOnly, abs)
		}
	case !theme && judge.agent != pcmGuardWriter:
		if !slices.Contains(judge.pcmOnly, abs) {
			judge.pcmOnly = append(judge.pcmOnly, abs)
		}
	}
}

// word judges one path word of a Bash call; a glob is expanded, or kept as
// written when it matches nothing, as the shell does.
func (judge *pcmGuardJudge) word(word, dir string) {
	abs, ok := judge.resolve(word, dir)
	if !ok {
		judge.unreadable = true
		return
	}
	if strings.ContainsAny(abs, "*?[") {
		if matches, err := filepath.Glob(abs); err == nil && len(matches) > 0 {
			for _, match := range matches {
				judge.path(match)
			}
			return
		}
	}
	judge.path(abs)
}

// resolve makes a Bash word absolute: a leading ~ is home, a relative word
// joins dir. A word holding an expansion the parse could not know keeps only
// its literal tail after the last expansion, on pcmGuardUnknownRoot; with no
// such tail ok is false.
func (judge *pcmGuardJudge) resolve(word, dir string) (string, bool) {
	if i := strings.LastIndexAny(word, "$`"); i >= 0 {
		slash := strings.IndexByte(word[i:], filepath.Separator)
		if slash < 0 {
			return "", false
		}
		tail := strings.NewReplacer(`"`, "", `'`, "").Replace(word[i+slash:])
		return filepath.Join(pcmGuardUnknownRoot, tail), true
	}
	if home := judge.scope.home; home != "" && (word == "~" || strings.HasPrefix(word, "~/")) {
		word = home + word[1:]
	}
	if !filepath.IsAbs(word) {
		word = filepath.Join(cmp.Or(dir, pcmGuardUnknownRoot), word)
	}
	return filepath.Clean(word), true
}

// bash judges every part of a Bash command in order. The flat part list
// does not say where a cd stops applying (a subshell, an inner shell, popd),
// so a relative word is judged in every directory the call may stand in: the
// cwd and each directory a cd, pushd or popd may have moved it to.
func (judge *pcmGuardJudge) bash(command, cwd string) error {
	parsed, err := cmdparse.ParseBatch([]cmdparse.Call{{
		ID: "pcm-guard", Command: command, Cwd: cmp.Or(cwd, string(filepath.Separator)),
		Vars: map[string]string{"HOME": judge.scope.home},
	}})
	if err != nil {
		return err
	}
	dirs := []string{cwd}
	for _, part := range parsed["pcm-guard"] {
		if part.Bounded || part.Status == cmdparse.StatusError {
			judge.unreadable = true
			continue
		}
		base := filepath.Base(part.Program)
		words, codes := pcmGuardWords(&part, base), pcmGuardCode(&part, base)
		_, copying := pcmGuardCopyValued[base]
		for _, dir := range dirs {
			for _, word := range words {
				judge.word(word, dir)
			}
			for _, code := range codes {
				judge.code(code, dir)
			}
			if copying {
				judge.copies(base, part.Args, dir)
			}
		}
		if slices.Contains(pcmGuardChdirs, base) {
			for _, dir := range dirs {
				if next, ok := judge.chdir(base, part.Args, dir); ok && !slices.Contains(dirs, next) {
					dirs = append(dirs, next)
				}
			}
			if len(dirs) > pcmGuardMaxDirs {
				judge.unreadable = true
				dirs = dirs[:pcmGuardMaxDirs]
			}
		}
	}
	return nil
}

// chdir is the directory a cd, pushd or popd run in dir moves to: home for
// a bare cd, unknown ("") for `cd -`, a popd (its stack may predate the call)
// or a word the parse could not know; ok is false for a bare pushd, which
// swaps back to a directory already judged.
func (judge *pcmGuardJudge) chdir(base string, args []string, dir string) (string, bool) {
	operands, _ := pcmGuardSplit(args)
	switch {
	case base == pcmGuardPopd:
		return "", true
	case len(operands) == 0 && base == pcmGuardPushd:
		return "", false
	case len(operands) == 0:
		return judge.scope.home, true
	}
	if abs, ok := judge.resolve(operands[0], dir); ok && operands[0] != "-" {
		return abs, true
	}
	return "", true
}

// pcmGuardWords returns the words one part writes, as written: its write
// redirections, and the file operands of tee, rm, unlink, touch, truncate,
// an in-place sed or perl, and dd's of=. A git part's own words are
// git-guard's, so only its redirections count.
func pcmGuardWords(part *cmdparse.Part, base string) []string {
	var words []string
	for _, r := range part.Redirs {
		fdDup := r.Op == ">&" && r.Target != "-" && strings.Trim(r.Target, "0123456789") != ""
		if slices.Contains(pcmGuardWriteOps, r.Op) || fdDup {
			words = append(words, r.Target)
		}
	}
	var operands []string
	switch base {
	case "tee", "rm", "unlink":
		operands, _ = pcmGuardSplit(part.Args)
	case "touch":
		operands, _ = pcmGuardSplit(part.Args, "-r", "--reference", "-d", "--date", "-t")
	case "truncate":
		operands, _ = pcmGuardSplit(part.Args, "-s", "--size", "-r", "--reference")
	case "sed":
		if pcmGuardInPlace(part.Args) {
			var values map[string]string
			operands, values = pcmGuardSplit(part.Args, "-e", "--expression", "-f", "--file", "-l", "--line-length")
			_, e := values["-e"]
			_, expression := values["--expression"]
			_, f := values["-f"]
			_, file := values["--file"]
			if !e && !expression && !f && !file && len(operands) > 0 {
				operands = operands[1:] // the script
			}
		}
	case "perl":
		if pcmGuardInPlace(part.Args) {
			operands, _ = pcmGuardSplit(part.Args, "-e", "-E")
		}
	case "dd":
		for _, a := range part.Args {
			if target, ok := strings.CutPrefix(a, "of="); ok {
				operands = append(operands, target)
			}
		}
	}
	return append(words, operands...)
}

// pcmGuardInPlace reports sed's or perl's in-place flag: -i alone, with a
// suffix or in a short cluster, or --in-place[=suffix].
func pcmGuardInPlace(args []string) bool {
	return gitGuardHasShortFlag(args, 'i', "--in-place") ||
		slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "--in-place=") })
}

// copies judges what cp, mv, ln, install and rsync write: the destination,
// or each source's name inside it when it is a directory (-t, a trailing /,
// or an existing directory), and for mv each source it removes.
func (judge *pcmGuardJudge) copies(base string, args []string, dir string) {
	sources, values := pcmGuardSplit(args, pcmGuardCopyValued[base]...)
	dest, into := values["-t"]
	if target, ok := values["--target-directory"]; ok {
		dest, into = target, true
	}
	if !into {
		switch {
		case len(sources) == 1 && base == "ln":
			dest, into = ".", true // ln TARGET links its name in the cwd
		case len(sources) < 2:
			return
		default:
			dest, sources = sources[len(sources)-1], sources[:len(sources)-1]
		}
	}
	if base == "mv" {
		for _, source := range sources {
			judge.word(source, dir)
		}
	}
	abs, ok := judge.resolve(dest, dir)
	if !ok {
		judge.unreadable = true
		return
	}
	if strings.ContainsAny(abs, "*?[") {
		// The shell expands the destination; its last match is the place.
		if matches, err := filepath.Glob(abs); err == nil && len(matches) > 0 {
			abs = matches[len(matches)-1]
			dest = abs + strings.Repeat("/", len(dest)-len(strings.TrimRight(dest, "/")))
		}
	}
	if !into {
		info, err := os.Stat(abs)
		into = strings.HasSuffix(dest, "/") || (err == nil && info.IsDir())
	}
	if !into {
		judge.word(dest, dir)
		return
	}
	for _, source := range sources {
		judge.word(filepath.Base(strings.TrimRight(source, "/")), abs)
	}
}

// pcmGuardCodeFlags are the short flags whose value is inline code, per
// interpreter; python names every python* program.
// dash and ksh are here because cmdparse parses only a bash, sh or zsh -c
// string (cmdparse.shellScript), so theirs is read as code.
var pcmGuardCodeFlags = map[string]string{
	"python": "c", "node": "ep", "nodejs": "ep", "perl": "eE", "ruby": "e", pcmGuardDash: "c", "ksh": "c",
}

// pcmGuardCode returns the inline code an interpreter runs: a heredoc or
// here-string body fed to python, node, perl, ruby or a shell, and the value
// of a code flag (alone or in a short cluster: -c, -e, -pe, the code the next
// word or joined to it: -c'code'), --eval and --print. A bash, sh or zsh -c
// string is no code here: cmdparse parses it into parts of its own.
func pcmGuardCode(part *cmdparse.Part, base string) []string {
	shell := slices.Contains(pcmGuardShells, base)
	if strings.HasPrefix(base, "python") {
		base = "python"
	}
	flags, interpreter := pcmGuardCodeFlags[base]
	if !shell && !interpreter {
		return nil
	}
	var code []string
	for _, r := range part.Redirs {
		switch r.Op {
		case "<<", "<<-":
			code = append(code, r.Body)
		case "<<<":
			code = append(code, r.Target)
		}
	}
	for i, a := range part.Args {
		if name, value, ok := strings.Cut(a, "="); ok && !shell && slices.Contains(pcmGuardLongCode, name) {
			code = append(code, value)
			continue
		}
		if flags != "" && len(a) > 1 && a[0] == '-' && a[1] != '-' {
			if j := strings.IndexAny(a[1:], flags); j >= 0 {
				if joined := a[j+2:]; joined != "" {
					code = append(code, joined) // -c'code', -pe'code': the value joined to its flag
				} else if i+1 < len(part.Args) {
					code = append(code, part.Args[i+1])
				}
				continue
			}
		}
		if i+1 < len(part.Args) && interpreter && !shell && slices.Contains(pcmGuardLongCode, a) {
			code = append(code, part.Args[i+1])
		}
	}
	return code
}

// code judges inline code by the paths it names: every path-shaped token
// (holding a /, named CLAUDE.md, or naming an existing file) is judged as a
// write. shortcut: a read-only script naming a prompt file is denied too;
// reading what the code does would need its language's parser.
func (judge *pcmGuardJudge) code(code, dir string) {
	tokens := strings.FieldsFunc(code, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("'\"`()[]{};,=<>|&$!#*?\\", r)
	})
	for _, token := range tokens {
		abs, ok := judge.resolve(token, dir)
		if !ok {
			continue
		}
		_, err := os.Lstat(abs)
		if strings.Contains(token, "/") || strings.EqualFold(filepath.Base(token), "CLAUDE.md") || err == nil {
			judge.path(abs)
		}
	}
}

// pcmGuardSplit returns args' operands and the values of the flags named in
// valued: a long flag as --flag=v or --flag v, a short one alone or in a
// short cluster, its value the rest of the word (-tDIR) or the next word
// (-vt DIR).
func pcmGuardSplit(args []string, valued ...string) ([]string, map[string]string) {
	var operands []string
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return append(operands, args[i+1:]...), values
		case a == "-" || !strings.HasPrefix(a, "-"):
			operands = append(operands, a)
		case strings.HasPrefix(a, "--"):
			name, value, has := strings.Cut(a, "=")
			if slices.Contains(valued, name) {
				if !has && i+1 < len(args) {
					i++
					value = args[i]
				}
				values[name] = value
			}
		default:
			// getopt: the first valued letter of a cluster takes the rest of
			// the word (-tDIR, -e's/a/b/'), or the next word when it ends it.
			for j := 1; j < len(a); j++ {
				name := "-" + a[j:j+1]
				if !slices.Contains(valued, name) {
					continue
				}
				if j+1 < len(a) {
					values[name] = a[j+1:]
				} else if i+1 < len(args) {
					i++
					values[name] = args[i]
				}
				break
			}
		}
	}
	return operands, values
}

// pcmGuardMentions reports whether a command the guard could not read in
// full names a file the caller may not write: for pcm only a theme, for
// everyone else CLAUDE.md, .claude or templates.
func pcmGuardMentions(command, agent string) bool {
	lower := strings.ToLower(command)
	if agent == pcmGuardWriter {
		return strings.Contains(lower, "themes")
	}
	return strings.Contains(lower, "claude.md") || strings.Contains(lower, ".claude") ||
		strings.Contains(lower, "templates")
}

// reason is the one deny message: the paths only pcm writes and the route to
// pcm, the themes only themer writes and the route to themer, then the
// unreadable-command line.
func (judge *pcmGuardJudge) reason() string {
	var lines []string
	if len(judge.pcmOnly) > 0 {
		lines = append(
			lines,
			fmt.Sprintf(pcmGuardRoute, pcmGuardWriter, strings.Join(judge.pcmOnly, ", "), pcmGuardPcmTail),
		)
	}
	if len(judge.themeOnly) > 0 {
		lines = append(
			lines,
			fmt.Sprintf(pcmGuardRoute, pcmGuardThemer, strings.Join(judge.themeOnly, ", "), pcmGuardThemerTail),
		)
	}
	if judge.unreadable {
		lines = append(lines, pcmGuardUnreadable)
	}
	return strings.Join(lines, "\n")
}
