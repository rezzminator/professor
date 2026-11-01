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

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type engineGuardHookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command    string `json:"command"`
		FilePath   string `json:"file_path"`
		OpenCodeFP string `json:"filePath"`
		PatchText  string `json:"patchText"`
		Workdir    string `json:"workdir"`
	} `json:"tool_input"`
	Cwd string `json:"cwd"`
}

// The tool, flag and program names the guard reads more than once.
const (
	engineGuardApplyPatch = "apply_patch"
	engineGuardBashTool   = "bash"
	engineGuardFlag       = "--engine"
)

// engineGuardKind is what a tool call hands the guard to read.
type engineGuardKind int

const (
	engineGuardShell engineGuardKind = iota + 1
	engineGuardPatch
	engineGuardFile
)

// engineGuardTools are the write tools each engine names in its hook
// payload: Codex sends its shell as Bash and its edits as apply_patch (the
// patch in tool_input.command); OpenCode's plugin sends its tool ids, older
// builds' multiedit and patch included.
var engineGuardTools = map[pfmengine.ID]map[string]engineGuardKind{
	pfmengine.Codex: {bashTool: engineGuardShell, engineGuardApplyPatch: engineGuardPatch},
	pfmengine.OpenCode: {
		engineGuardBashTool: engineGuardShell, engineGuardApplyPatch: engineGuardPatch,
		"edit": engineGuardFile, "write": engineGuardFile, "multiedit": engineGuardFile,
		"patch": engineGuardPatch,
	},
}

const (
	engineGuardGitBan = "Codex and OpenCode never write git: only Claude does. Git reads (status, log, diff," +
		" show, blame, ls-files) stay open; return the change to the user."
	engineGuardFileBan = "Codex and OpenCode never change framework files (any CLAUDE.md or AGENTS.md, .claude/," +
		" .codex/, .opencode/, .git/, the Claude, Codex and OpenCode config dirs, the Professor clone's" +
		" templates/): only Claude does. Return the exact change to the user."
	engineGuardBlindLine = "it runs code or applies a patch the guard cannot read: pass code inline (-c, -e)," +
		" as a script file or a heredoc, and a patch as a file or a heredoc."
	engineGuardUnreadable = "engine-guard could not read this command, and it names a framework file:" +
		" split it into simple commands with literal paths."
	// engineGuardMaxRead bounds a script or patch file the guard reads.
	engineGuardMaxRead = 1 << 20
)

var (
	// engineGuardNames are the framework files known by name anywhere.
	engineGuardNames = strings.Fields("claude.md agents.md agents.override.md opencode.json opencode.jsonc")
	// engineGuardDirs are the framework directories known by name anywhere.
	engineGuardDirs = strings.Fields(".claude .codex .opencode .git")
	// engineGuardMentionNames name a framework file in a command the guard
	// could not read in full.
	engineGuardMentionNames = append(append(strings.Fields(pcmGuardTemplates+" opencode"), engineGuardNames...),
		engineGuardDirs...)
	// engineGuardTouches are the programs whose every operand is a file they
	// change or create: editors (an editor run with none waits on a terminal
	// Codex can type into with no hook), mode and owner changes, directories.
	engineGuardEditors = strings.Fields("vi vim nvim nano emacs ed ex pico micro joe mcedit")
	engineGuardTouches = append(strings.Fields("chmod chown chgrp mkdir rmdir shred sponge"), engineGuardEditors...)
	// engineGuardPatchHeaders lead a line naming a file a patch writes: the
	// apply_patch envelope's, git's and patch(1)'s Index line. A unified or
	// context diff's ---/+++/*** line counts only in its header pair
	// (engineGuardDiffHeader), so a body line never reads as a target.
	engineGuardPatchHeaders = []string{
		"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: ",
		"rename from ", "rename to ", "copy to ", "Index: ",
	}
	// engineGuardPatchValued are patch(1)'s flags taking a value.
	engineGuardPatchValued = strings.Fields("-i --input -o --output -p --strip -d --directory -r --reject-file" +
		" -B --prefix -D --ifdef -F --fuzz -V --version-control -z --suffix -Y --basename-prefix")
	// engineGuardGitApplyValued are git apply's flags taking a separate value.
	engineGuardGitApplyValued = strings.Fields("--directory --exclude --include --whitespace --build-fake-ancestor")
	// engineGuardGitPathValued are git checkout's and restore's flags taking a
	// value that is no path they write.
	engineGuardGitPathValued = strings.Fields("-s --source -b -B --orphan --conflict --pathspec-from-file")
	// engineGuardGitReads are the git subcommands that only read, the engine
	// mode's allowlist beside the subcommands gitGuardBlocks judges by mode.
	engineGuardGitReads = strings.Fields("status log diff show blame annotate ls-files ls-tree ls-remote rev-parse" +
		" rev-list cat-file grep describe shortlog merge-base for-each-ref name-rev show-ref show-branch" +
		" check-ignore check-attr check-ref-format var help version whatchanged diff-tree diff-files diff-index" +
		" range-diff count-objects verify-commit verify-tag cherry request-pull")
	// engineGuardGitReflogReads are git reflog's reading forms.
	engineGuardGitReflogReads = []string{"", gitGuardVerbShow, "exists"}
)

// EngineGuard is the PreToolUse hook of a Codex or OpenCode chat, main loop
// and every sub-agent alike: it denies every git write (gitGuardCheck in
// engine mode: no gitter, no stash but list and show) and every write to a
// framework file (the pcm-guard judge in engine mode: no pcm or themer).
// Only Claude, under git-guard and pcm-guard, changes either. A read or
// decode failure and a parser error are fail-open to stderr, as on Claude.
func EngineGuard(args []string, input io.Reader, stdout, stderr io.Writer) int {
	id, err := engineGuardEngine(args)
	if err != nil {
		fmt.Fprintf(
			stderr,
			"pfm internal engine-guard: %v\nusage: pfm internal engine-guard %s codex|opencode\n",
			err, engineGuardFlag,
		)
		return 2
	}
	raw, err := io.ReadAll(input)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal engine-guard: read hook payload (fail-open): %v\n", err)
		return 0
	}
	if len(raw) == 0 {
		return 0
	}
	var hook engineGuardHookInput
	if err := json.Unmarshal(raw, &hook); err != nil {
		fmt.Fprintf(stderr, "pfm internal engine-guard: decode hook payload (fail-open): %v\n", err)
		return 0
	}
	kind := engineGuardTools[id][hook.ToolName]
	if kind == 0 {
		return 0
	}
	cwd := hook.Cwd
	if !filepath.IsAbs(cwd) {
		cwd = ""
	}
	// No agent: an engine caller's agent_type is never an exemption, and
	// engineClass reports no theme, so every framework path lands in pcmOnly.
	judge := &pcmGuardJudge{scope: pcmGuardNewScope(stderr, true)}
	var reasons []string
	switch kind {
	case engineGuardShell:
		command := hook.ToolInput.Command
		if workdir := hook.ToolInput.Workdir; workdir != "" {
			cwd = gitGuardJoin(cwd, workdir)
		}
		reasons = append(reasons, gitGuardCheck(command, cwd, true, stderr))
		if err := judge.bash(command, cwd, cmdparse.Script{}, 0); err != nil {
			fmt.Fprintf(stderr, "pfm internal engine-guard: parse command (fail-open): %v\n", err)
		}
		read := append([]string{command}, judge.read...)
		judge.unreadable = judge.unreadable &&
			slices.ContainsFunc(read, func(text string) bool { return pcmGuardMentions(text, engineGuardMentionNames) })
	case engineGuardPatch:
		for _, target := range engineGuardPatchTargets(cmp.Or(hook.ToolInput.PatchText, hook.ToolInput.Command)) {
			judge.path(filepath.Clean(gitGuardJoin(cmp.Or(cwd, pcmGuardUnknownRoot), target)))
		}
	case engineGuardFile:
		if target := cmp.Or(hook.ToolInput.OpenCodeFP, hook.ToolInput.FilePath); target != "" {
			judge.path(filepath.Clean(gitGuardJoin(cmp.Or(cwd, pcmGuardUnknownRoot), target)))
		}
	}
	if len(judge.pcmOnly) > 0 || len(judge.blind) > 0 || judge.unreadable {
		reasons = append(reasons, judge.reason())
	}
	reason := strings.Join(slices.DeleteFunc(reasons, func(r string) bool { return r == "" }), "\n")
	if reason == "" {
		return 0
	}
	if err := json.NewEncoder(stdout).Encode(preToolUseDenyResponse(reason)); err != nil {
		fmt.Fprintf(stderr, "pfm internal engine-guard: write deny: %v\n", err)
		return 1
	}
	return 0
}

// engineGuardEngine reads `--engine codex|opencode`, the only engines the
// guard judges.
func engineGuardEngine(args []string) (pfmengine.ID, error) {
	if len(args) != 2 || args[0] != engineGuardFlag {
		return "", fmt.Errorf("want %s codex|opencode, got %q", engineGuardFlag, args)
	}
	id, err := pfmengine.Parse(args[1])
	if err != nil {
		return "", err
	}
	if _, ok := engineGuardTools[id]; !ok {
		return "", fmt.Errorf("engine %q has no engine guard (codex or opencode)", args[1])
	}
	return id, nil
}

// engineGuardRoots are the directories and files an engine caller may not
// write at all: the Codex home (CODEX_HOME, else ~/.codex), the OpenCode
// config dir (OPENCODE_CONFIG_DIR, XDG_CONFIG_HOME/opencode, ~/.config/opencode)
// and the pfm binary the hooks run, as written and physical.
func engineGuardRoots(home string) []string {
	openCode := pfmengine.MustLookup(pfmengine.OpenCode).LongName
	dirs := []string{storeGuardGet("CODEX_HOME"), storeGuardGet("OPENCODE_CONFIG_DIR")}
	if xdg := storeGuardGet("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		dirs = append(dirs, filepath.Join(xdg, openCode))
	}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, "."+pfmengine.MustLookup(pfmengine.Codex).LongName),
			filepath.Join(home, ".config", openCode), filepath.Join(home, ".local", "bin", "pfm"))
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, exe)
	}
	var roots []string
	for _, dir := range dirs {
		if filepath.IsAbs(dir) {
			roots = append(roots, filepath.Clean(dir), paths.PhysicalPath(dir))
		}
	}
	return roots
}

// engineClass reports a framework path: a framework file or directory by
// name, the clone's templates/, or anything under a config dir or root.
func (scope pcmGuardScope) engineClass(path string) bool {
	if slices.Contains(engineGuardNames, strings.ToLower(filepath.Base(path))) {
		return true
	}
	if in, _ := scope.templates(path); in {
		return true
	}
	for _, root := range append(slices.Clone(scope.configs), scope.roots...) {
		if _, ok := pcmGuardUnder(path, root); ok {
			return true
		}
	}
	for _, elem := range strings.Split(path, string(filepath.Separator)) {
		if slices.Contains(engineGuardDirs, strings.ToLower(elem)) {
			return true
		}
	}
	return false
}

// enginePart judges what one Bash part writes that the Claude reading leaves
// to git-guard or does not read: the files an editor or a mode change
// touches, the paths git checkout and restore write, the files a patch
// writes, and the code a script file or a shebang program runs. A shell,
// interpreter or editor left reading a terminal is blind: Codex types into
// it (write_stdin) with no hook.
func (judge *pcmGuardJudge) enginePart(part *cmdparse.Part, base, dir string, depth int) {
	written := strings.TrimSpace(part.Program + " " + strings.Join(part.Args, " "))
	switch {
	case slices.Contains(engineGuardTouches, base):
		operands, _ := pcmGuardSplit(part.Args)
		for _, operand := range operands {
			judge.word(operand, dir)
		}
		if len(operands) == 0 && slices.Contains(engineGuardEditors, base) {
			judge.blindTo(written)
		}
	case base == "git":
		repoDir, sub, rest := gitGuardSplit(part.Args, dir)
		switch sub {
		case "checkout", "restore":
			for _, operand := range gitGuardOperands(rest, engineGuardGitPathValued...) {
				judge.word(operand, repoDir)
			}
		case gitGuardVerbApply:
			files := slices.DeleteFunc(gitGuardOperands(rest, engineGuardGitApplyValued...),
				func(f string) bool { return f == "-" })
			_, values := pcmGuardSplit(rest, engineGuardGitApplyValued...)
			judge.diffs(part, files, repoDir, gitGuardJoin(repoDir, values["--directory"]), written)
		}
	case base == "patch":
		operands, values := pcmGuardSplit(part.Args, engineGuardPatchValued...)
		// patch changes into -d's directory before anything else; a `<`
		// redirection is the shell's, read where the command runs.
		at := gitGuardJoin(dir, cmp.Or(values["-d"], values["--directory"]))
		var files []string
		if input := cmp.Or(values["-i"], values["--input"]); input != "" {
			files = append(files, gitGuardJoin(at, input))
		}
		if len(operands) > 0 {
			judge.word(operands[0], at)
		}
		if len(operands) > 1 {
			files = append(files, gitGuardJoin(at, operands[1]))
		}
		if output := cmp.Or(values["-o"], values["--output"]); output != "" {
			judge.word(output, at)
		}
		judge.diffs(part, files, dir, at, written)
	case base == engineGuardApplyPatch:
		judge.diffs(part, nil, dir, dir, written)
	}
	if lang := pcmGuardLang(base); lang != "" {
		judge.engineScript(part, base, lang, dir, depth, written)
	} else if strings.Contains(part.Program, "/") {
		judge.shebang(part.Program, part.Args, dir, depth)
	}
}

// engineScript judges the script file a shell or interpreter part runs: its
// first operand naming a regular file (later operands are the script's
// arguments), else a `<` redirection's file. A part with no code, operand,
// heredoc or here-string reads its program from a terminal or a pipe the
// guard cannot see, and is blind.
func (judge *pcmGuardJudge) engineScript(part *cmdparse.Part, base, lang, dir string, depth int, written string) {
	if len(pcmGuardCode(part, base)) > 0 || engineGuardOnlyAsks(part.Args) ||
		(lang == pcmGuardSh && engineGuardShellString(part.Args)) {
		return
	}
	operands, _ := pcmGuardSplit(part.Args)
	sources := slices.Clone(operands)
	for _, r := range part.Redirs {
		if r.Op == "<" {
			sources = append(sources, r.Target)
		}
	}
	read := false
	for _, source := range sources {
		if text, ok := judge.readFile(source, dir); ok {
			script := cmdparse.Script{}
			script.Path, _ = judge.resolve(source, dir)
			if at := slices.Index(part.Args, source); at >= 0 {
				script.Args = part.Args[at+1:]
			}
			judge.run(lang, text, dir, script, depth)
			read = true
			break
		}
	}
	// A module run (python -m) names no file: an accepted open way, as a
	// build tool is. Any other script the guard cannot read (not yet
	// written, a directory) is blind, as an unread patch is.
	module := lang == pcmGuardPython && slices.Contains(part.Args, "-m")
	bodies, words := pcmGuardStdin(part)
	if !read && !module && len(bodies)+len(words) == 0 {
		judge.blindTo(written)
	}
}

// engineGuardShellString reports a shell given its program as a -c string
// before any operand: cmdparse reads that string as parts of its own.
func engineGuardShellString(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if slices.Contains([]string{"-o", "+o", "-O", "+O"}, a) {
			i++ // the option name it sets
			continue
		}
		if !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+") {
			return false
		}
		if !strings.HasPrefix(a, "--") && strings.ContainsRune(a[1:], 'c') {
			return true
		}
	}
	return false
}

// engineGuardOnlyAsks reports an interpreter asked for its version or help
// before any operand: a flag after the script is the script's own.
func engineGuardOnlyAsks(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return false
		}
		if slices.Contains([]string{"--version", "-V", "--help", "-h"}, a) {
			return true
		}
	}
	return false
}

// shebang judges a program run by its path when it is a script: its #! line
// names the interpreter (directly or through env). A compiled program is not
// read past its first two bytes.
func (judge *pcmGuardJudge) shebang(program string, args []string, dir string, depth int) {
	abs, ok := judge.resolve(program, dir)
	if !ok || !engineGuardIsScript(abs) {
		return
	}
	text, ok := judge.readFile(program, dir)
	if !ok {
		judge.blindTo(program)
		return
	}
	line, _, _ := strings.Cut(text[2:], "\n")
	fields := strings.Fields(line)
	if len(fields) > 1 && filepath.Base(fields[0]) == envCommand {
		fields = slices.DeleteFunc(fields[1:], func(f string) bool { return strings.HasPrefix(f, "-") })
	}
	if len(fields) > 0 {
		if lang := pcmGuardLang(filepath.Base(fields[0])); lang != "" {
			judge.run(lang, text, dir, cmdparse.Script{Path: abs, Args: args}, depth)
		}
	}
}

// engineGuardIsScript reports a regular file that opens with #!.
func engineGuardIsScript(abs string) bool {
	if info, err := os.Stat(abs); err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := os.Open(abs)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	head := make([]byte, 2)
	n, _ := io.ReadFull(file, head)
	return n == 2 && string(head) == "#!"
}

// run judges code the script file holds, as program judges inline code; the
// shell code reads `$0` and its arguments from script.
func (judge *pcmGuardJudge) run(lang, code, dir string, script cmdparse.Script, depth int) {
	if lang == pcmGuardSh {
		judge.shell(code, dir, script, depth)
		return
	}
	judge.interpret(lang, []string{code}, dir)
}

// diffs judges every file the patches a part applies write: the patch files
// named, else its heredocs, here-strings and `<` file. A patch the guard
// cannot read (stdin from a pipe, a file not yet written) is blind. A diff
// path is judged under root as written and without its first element (git's
// a/ and b/); the patch files are read from dir.
func (judge *pcmGuardJudge) diffs(part *cmdparse.Part, files []string, dir, root, written string) {
	var texts []string
	for _, file := range files {
		text, ok := judge.readFile(file, dir)
		if !ok {
			judge.blindTo(written)
			return
		}
		texts = append(texts, text)
	}
	if len(files) == 0 {
		bodies, words := pcmGuardStdin(part)
		texts = append(texts, bodies...)
		texts = append(texts, words...)
		for _, r := range part.Redirs {
			if r.Op != "<" {
				continue
			}
			text, ok := judge.readFile(r.Target, dir)
			if !ok {
				judge.blindTo(written)
				return
			}
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		judge.blindTo(written)
		return
	}
	for _, text := range texts {
		for _, target := range engineGuardPatchTargets(text) {
			judge.word(target, root)
			if _, rest, ok := strings.Cut(target, "/"); ok && rest != "" {
				judge.word(rest, root)
			}
		}
	}
}

// readFile reads a regular file a Bash word names in dir, bounded by
// engineGuardMaxRead; past the bound it is unread, and each caller holds
// its part blind, whatever the file names. What it reads
// joins the texts a mention is looked for in.
func (judge *pcmGuardJudge) readFile(word, dir string) (string, bool) {
	abs, ok := judge.resolve(word, dir)
	if !ok {
		return "", false
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	if info.Size() > engineGuardMaxRead {
		return "", false
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", false
	}
	judge.read = append(judge.read, string(raw))
	return string(raw), true
}

func (judge *pcmGuardJudge) blindTo(written string) {
	if named := "`" + written + "`"; !slices.Contains(judge.blind, named) {
		judge.blind = append(judge.blind, named)
	}
}

// engineGuardPatchTargets are the paths a patch names on its header lines:
// the apply_patch envelope's file lines, a unified or context diff's
// ---/+++/*** lines (up to a tab, /dev/null dropped), git's diff --git,
// rename and copy lines.
func engineGuardPatchTargets(patch string) []string {
	var targets []string
	add := func(target string) {
		target = strings.Trim(strings.TrimSpace(target), `"`)
		if target != "" && target != "/dev/null" && !slices.Contains(targets, target) {
			targets = append(targets, target)
		}
	}
	lines := strings.Split(patch, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	for i, line := range lines {
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			for _, field := range strings.Fields(rest) {
				add(field)
			}
			continue
		}
		header := ""
		for _, h := range engineGuardPatchHeaders {
			if strings.HasPrefix(line, h) {
				header = h
				break
			}
		}
		if header == "" {
			header = engineGuardDiffHeader(lines, i)
		}
		if header != "" {
			target, _, _ := strings.Cut(line[len(header):], "\t")
			add(target)
		}
	}
	return targets
}

// engineGuardDiffHeader is the header lines[i] opens when it is half of a
// unified (---, then +++) or context (***, then ---) diff's file header
// pair, "" otherwise.
func engineGuardDiffHeader(lines []string, i int) string {
	at := func(j int, prefix string) bool {
		return j >= 0 && j < len(lines) && strings.HasPrefix(lines[j], prefix)
	}
	switch {
	case at(i, "--- ") && (at(i+1, "+++ ") || at(i-1, "*** ")):
		return "--- "
	case at(i, "+++ ") && at(i-1, "--- "):
		return "+++ "
	case at(i, "*** ") && at(i+1, "--- "):
		return "*** "
	}
	return ""
}

// engineReason is the engine deny message: the framework paths and the ban,
// the blind parts, then the unreadable-command line.
func (judge *pcmGuardJudge) engineReason() string {
	var lines []string
	if len(judge.pcmOnly) > 0 {
		lines = append(
			lines,
			"engine-guard blocked a write to "+strings.Join(judge.pcmOnly, ", ")+". "+engineGuardFileBan,
		)
	}
	if len(judge.blind) > 0 {
		lines = append(lines, "engine-guard blocked "+strings.Join(judge.blind, ", ")+": "+engineGuardBlindLine)
	}
	if judge.unreadable {
		lines = append(lines, engineGuardUnreadable)
	}
	return strings.Join(lines, "\n")
}
