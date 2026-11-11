package hookentry

import (
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
)

// The languages pcm-guard reads a program in.
const (
	pcmGuardPython = "python"
	pcmGuardNode   = "node"
	pcmGuardPerl   = "perl"
	pcmGuardRuby   = "ruby"
	pcmGuardSh     = "sh"
	// pcmGuardMaxShellDepth bounds shell programs read inside shell programs,
	// as cmdparse bounds nested -c strings; past it the command is unreadable.
	pcmGuardMaxShellDepth = 8
	// pcmGuardMaxValueDepth bounds how far a python name is followed to its literal.
	pcmGuardMaxValueDepth = 4
	pcmGuardQuotes        = "'\"`"
	// The write names more than one language's table spells.
	pcmGuardOpen    = "open"
	pcmGuardUnlink  = "unlink"
	pcmGuardTee     = "tee"
	pcmGuardRename  = "rename"
	pcmGuardReplace = "replace"
	// pcmGuardInto joins a copy's destination and source in one target, for
	// codeInto; no literal path holds a NUL.
	pcmGuardInto = "\x00"
)

// pcmGuardOptions is how an interpreter's command line reads before its
// program operand: the short letters taking no value (bare), taking the rest
// of their word (joined), the rest of the word or the next word (valued), and
// whose value is inline code (code); the long options likewise.
type pcmGuardOptions struct {
	bare, joined, valued, code string
	longBare, longValued       []string
}

var pcmGuardOptionTable = map[string]pcmGuardOptions{
	pcmGuardPython: {
		bare: "bBdEhIOPqsSuvVx", valued: "WX", code: "c",
		longBare: strings.Fields("--help --version"), longValued: strings.Fields("--check-hash-based-pycs"),
	},
	pcmGuardNode: {
		bare:   "hv",
		valued: "rC",
		code:   "ep",
		longBare: strings.Fields(
			"--no-warnings --no-deprecation --enable-source-maps --trace-warnings --trace-uncaught",
		),
		longValued: strings.Fields(
			"--require --import --loader --experimental-loader --conditions --input-type",
		),
	},
	pcmGuardPerl: {bare: "wWXTtnpacsU", joined: "lMmi0CdDFx", valued: "I", code: "eE"},
	pcmGuardRuby: {bare: "wvdnpacslU", joined: "WxK", valued: "rIE", code: "e"},
	pcmGuardSh: {
		bare: "exuvfhnabmBCEHPTpl", valued: "oO", code: "c",
		longBare:   strings.Fields("--norc --noprofile --posix --login --noediting --restricted --verbose"),
		longValued: strings.Fields("--rcfile --init-file"),
	},
}

// pcmGuardEscapes are the code that can run or write what its text does not
// show — a shell, an eval, a dynamic import or call, a module used as a
// value — per language; one of them sends the code to the reading before.
var pcmGuardEscapes = map[string]*regexp.Regexp{
	pcmGuardPython: regexp.MustCompile(
		`(?:^|[^.\w])(?:exec|eval|compile|getattr|setattr|delattr|globals|locals|vars|__import__)\s*\(` +
			`|\b(?:subprocess|importlib|fileinput|ctypes|pty|runpy|builtins|__builtins__|__dict__` +
			`|sqlite3|dbm|shelve|tempfile|FileHandler|basicConfig|operator|methodcaller|attrgetter` +
			`|unpack_archive|make_archive|extractall)\b|\.\s*extract\s*\(` +
			`|\bsys\s*\.\s*modules\b|\]\s*\(` +
			`|\bos\s*\.\s*(?:system|popen|exec\w*|spawn\w*|posix_spawn\w*|open|fdopen|fork\w*|chdir|fchdir|chroot)\b` +
			`|\bfrom\s+(?:os|shutil|io|codecs|gzip|bz2|lzma|tarfile|zipfile)\s+import\b` +
			`|\bimport\s+(?:os|shutil|io|codecs|gzip|bz2|lzma|tarfile|zipfile)\s+as\b` +
			`|(?:[=(,\[:]|\breturn)\s*(?:os|shutil|pathlib|io|codecs|gzip|bz2|lzma|tarfile|zipfile)\s*(?:$|[;\n),\]}])`,
	),
	pcmGuardNode: regexp.MustCompile(
		`\bchild_process\b|\b(?:exec|spawn|fork)\w*|(?:^|[^.\w])(?:eval|Function)\s*\(|\bvm\b|\bimport\s*\(` +
			`|\bprocess\s*\.\s*(?:binding|dlopen|chdir)\b|\brequire\s*\(\s*[^'"` + "`" + `\s]|\]\s*\(` +
			`|\bReflect\b|\bglobalThis\b|\bconstructor\b`,
	),
	pcmGuardPerl: regexp.MustCompile(
		`\b(?:system|exec|eval|qx|syscall|sysopen|readpipe|fork|pipe|chdir|chmod|chown|utime|INPLACE_EDIT)\b` +
			`|\$\^I|\bArchive::|` + "`" +
			`|\b(?:Path::Tiny|File::Slurp\w*|IO::File|File::Temp|Tie::File)\b` +
			`|->\s*\(|&\s*[$\{]|\*\{|\bdo\s*["'$]|\brequire\s*\$|/[msixpodualngcr]*e[msixpodualngcr]*\b`,
	),
	pcmGuardRuby: regexp.MustCompile(
		`\b(?:system|exec|spawn|fork|eval|instance_eval|class_eval|module_eval|instance_exec|class_exec` +
			`|send|__send__|public_send|method|const_get|Open3|popen\w*|syscall|load|require_relative|Pathname` +
			`|binding|chdir|reopen|sysopen|chmod|chown|lchmod|lchown|utime|lutime|mkfifo)\b|` + "`" +
			`|%x|\.\s*call\b|\.\s*\(|\b(?:include|extend|prepend)\s+FileUtils\b` +
			`|\bFileUtils\s*::|(?:[=(,\[]|\breturn)\s*(?:File|IO|Dir|FileUtils|Kernel)\s*(?:$|[;\n),\]}])`,
	),
}

var (
	// pcmGuardIdent is one bare name.
	pcmGuardIdent = regexp.MustCompile(`^[A-Za-z_]\w*$`)
	// pcmGuardPyOpen is python's builtin open; pcmGuardPyAttr every attribute read.
	pcmGuardPyOpen = regexp.MustCompile(`(?:^|[^.\w])(open)\b`)
	pcmGuardPyAttr = regexp.MustCompile(`\.\s*([A-Za-z_]\w*)\b`)
	// pcmGuardPyKeyword is one keyword argument of a python call.
	pcmGuardPyKeyword = regexp.MustCompile(`(?s)^\s*([A-Za-z_]\w*)\s*=([^=].*)$`)
	// pcmGuardPyAssign is an assignment to NAME as text, wherever it stands.
	pcmGuardPyAssign = `\bNAME\s*(?:[-+*/%&|^@]|//|\*\*|>>|<<)?=(?:[^=]|$)`
	// pcmGuardPyBinders are python's other ways to bind NAME.
	pcmGuardPyBinders = []string{
		`\bfor\s[^\n]*?\bNAME\b[^\n]*?\bin\b`,
		`\b(?:as|import|global|nonlocal|lambda|case|class|def|del)\b[^\n;]*?\bNAME\b`,
		`\bNAME\s*:=`,
	}
	// pcmGuardPyPathCalls build a path from their arguments.
	pcmGuardPyPathCalls = strings.Fields(
		"Path pathlib.Path PurePath pathlib.PurePath PosixPath pathlib.PosixPath os.path.join",
	)
	// pcmGuardPyModules are the os and shutil functions that write, each with
	// the count of its leading path arguments.
	pcmGuardPyModules = map[string]map[string]int{
		"os": {
			"remove": 1, pcmGuardUnlink: 1, "rmdir": 1, "removedirs": 1, "mkdir": 1, "makedirs": 1, "mkfifo": 1,
			"truncate": 1, pcmGuardRename: 2, "renames": 2, pcmGuardReplace: 2, "link": 2, "symlink": 2,
			"chmod": 1, "lchmod": 1, "chown": 1, "lchown": 1, "chflags": 1, "lchflags": 1, "utime": 1, "mknod": 1,
		},
		"shutil": {
			"copy": 2, "copy2": 2, "copyfile": 2, "copytree": 2, "copymode": 2, "copystat": 2, "move": 2,
			"rmtree": 1, "chown": 1,
		},
	}
	// pcmGuardPyCopies read their first path and write only the second; one
	// into marks a copy whose destination may be a directory taking the
	// source's name.
	pcmGuardPyCopies = map[string]bool{
		"shutil.copy": true, "shutil.copy2": true, "shutil.copyfile": false, "shutil.copytree": false,
		"shutil.copymode": false, "shutil.copystat": false, "os.link": false, "os.symlink": false,
	}
	// pcmGuardPyPathMethods write their receiver path, and the count of path
	// arguments after it; pcmGuardPyRenames only where the code names pathlib,
	// since str.replace is the everyday read idiom.
	pcmGuardPyPathMethods = map[string]int{
		"write_text": 0, "write_bytes": 0, "touch": 0, pcmGuardUnlink: 0, "rmdir": 0, "mkdir": 0,
		"symlink_to": 0, "hardlink_to": 0, "chmod": 0, "lchmod": 0,
	}
	pcmGuardPyRenames = map[string]int{pcmGuardRename: 1, pcmGuardReplace: 1}
	// pcmGuardPyOpeners are the modules whose open(path, mode) opens a file.
	pcmGuardPyOpeners = strings.Fields("io codecs gzip bz2 lzma tarfile")
	// pcmGuardNodeSites are node's fs writes; open is judged by its flags.
	pcmGuardNodeSites = regexp.MustCompile(`\b(writeFile|appendFile|rm|unlink|rmdir|mkdir|mkdtemp|rename|copyFile` +
		`|cp|symlink|link|truncate|chmod|chown|lchown|lchmod|utimes|lutimes|open)(?:Sync)?\b` +
		`|\b(?:create)?WriteStream\b`)
	pcmGuardNodeTwoPaths = strings.Fields("rename copyFile cp symlink link")
	// pcmGuardPerlSites are perl's writes; a sigil before one makes it a variable.
	pcmGuardPerlSites = regexp.MustCompile(`(?:^|[^$@%\w])(open|unlink|rename|mkdir|rmdir|symlink|link|truncate` +
		`|copy|move|cp|mv|rmtree|make_path|remove_tree|mkpath)\b`)
	pcmGuardPerlTwoPaths = strings.Fields("rename symlink link copy move cp mv")
	pcmGuardPerlOnePath  = strings.Fields("mkdir rmdir truncate")
	// pcmGuardRubySites are ruby's class writes and Kernel#open.
	pcmGuardRubySites = regexp.MustCompile(`\b(File|IO|Dir|FileUtils|Kernel|URI)\s*(?:\.|::)\s*(\w+[?!]?)`)
	pcmGuardRubyOpen  = regexp.MustCompile(`(?:^|[^.\w:])(open)\b`)
	// pcmGuardRubyKeyword is one keyword argument of a ruby call.
	pcmGuardRubyKeyword = regexp.MustCompile(`(?s)^\s*:?(\w+)\s*(?::\s|=>)(.*)$`)
	// pcmGuardLowOps end a call written without brackets.
	pcmGuardLowOps = strings.Fields("or and if unless do while until")
)

// pcmGuardLang names the language a program reads its code in, "" for none.
func pcmGuardLang(base string) string {
	switch {
	case strings.HasPrefix(base, pcmGuardPython):
		return pcmGuardPython
	case base == pcmGuardNode || base == "nodejs":
		return pcmGuardNode
	case base == pcmGuardPerl || base == pcmGuardRuby:
		return base
	case slices.Contains(pcmGuardShells, base):
		return pcmGuardSh
	}
	return ""
}

// program judges the code an interpreter or shell part runs in dir: its
// inline code, and its heredocs and here-strings when they are its program.
// Shell code is parsed one level deeper; a shell word the parse may not have
// known (holding a $ or `) is read by the paths it names, as before.
func (judge *pcmGuardJudge) program(part *cmdparse.Part, base, dir string, depth int) {
	lang := pcmGuardLang(base)
	if lang == "" {
		return
	}
	var bodies []string
	words := pcmGuardCode(part, base)
	if stdinBodies, stdinWords := pcmGuardStdin(part); len(stdinBodies)+len(stdinWords) > 0 &&
		judge.stdinIsCode(lang, part.Args, dir) {
		bodies, words = stdinBodies, append(words, stdinWords...)
	}
	if lang != pcmGuardSh {
		judge.interpret(lang, append(bodies, words...), dir)
		return
	}
	for _, body := range bodies {
		judge.shell(body, dir, cmdparse.Script{}, depth)
	}
	for _, word := range words {
		if strings.ContainsAny(word, "$`") {
			judge.code(word, dir)
			continue
		}
		judge.shell(word, dir, cmdparse.Script{}, depth)
	}
}

// pcmGuardStdin returns what a part's heredocs feed it, as written, and its
// here-strings as read (source text when the parse could not know one).
func pcmGuardStdin(part *cmdparse.Part) (bodies, words []string) {
	for _, r := range part.Redirs {
		switch r.Op {
		case "<<", "<<-":
			bodies = append(bodies, r.Body)
		case "<<<":
			words = append(words, r.Target)
		}
	}
	return bodies, words
}

// stdinIsCode reports whether a heredoc or here-string fed to lang's
// interpreter is its program: no program operand, the operand "-", an inline
// code flag (its stdin kept as code, the reading before), or an option the
// table does not know. It is data when the first operand names an existing
// regular file in dir, or python runs -m.
func (judge *pcmGuardJudge) stdinIsCode(lang string, args []string, dir string) bool {
	opts := pcmGuardOptionTable[lang]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-":
			return true
		case a == "--":
			return i+1 == len(args) || !judge.scriptFile(args[i+1], dir)
		case strings.HasPrefix(a, "--"):
			name, _, joined := strings.Cut(a, "=")
			switch {
			case slices.Contains(opts.longValued, name):
				if !joined {
					i++
				}
			case !slices.Contains(opts.longBare, name):
				return true
			}
		case len(a) > 1 && (a[0] == '-' || (a[0] == '+' && lang == pcmGuardSh)):
			code, data, next := opts.cluster(lang, a[1:])
			if code || data {
				return code
			}
			if next {
				i++
			}
		default:
			return !judge.scriptFile(a, dir)
		}
	}
	return true
}

// cluster reads one short-option word: code when a letter's value is inline
// code or the table does not know the letter, data for python's -m, next
// when its last letter takes the following word as its value.
func (opts pcmGuardOptions) cluster(lang, letters string) (code, data, next bool) {
	for j := range len(letters) {
		c := letters[j]
		switch {
		case strings.IndexByte(opts.code, c) >= 0:
			return true, false, false
		case lang == pcmGuardPython && c == 'm':
			return false, true, false
		case strings.IndexByte(opts.valued, c) >= 0:
			return false, false, j == len(letters)-1
		case strings.IndexByte(opts.joined, c) >= 0:
			return false, false, false
		case strings.IndexByte(opts.bare, c) < 0:
			return true, false, false
		}
	}
	return false, false, false
}

// scriptFile reports whether word names an existing regular file in dir.
func (judge *pcmGuardJudge) scriptFile(word, dir string) bool {
	abs, ok := judge.resolve(word, dir)
	if !ok {
		return false
	}
	info, err := os.Stat(abs)
	return err == nil && info.Mode().IsRegular()
}

// shell judges shell code run in dir the way a Bash command is judged, one
// level deeper; past pcmGuardMaxShellDepth it is unreadable. script is the
// file the code came from, zero for inline code.
func (judge *pcmGuardJudge) shell(code, dir string, script cmdparse.Script, depth int) {
	if depth >= pcmGuardMaxShellDepth {
		judge.unreadable = true
		return
	}
	if err := judge.bash(code, dir, script, depth+1); err != nil {
		judge.unreadable = true // a malformed batch: the code went unread
	}
}

// interpret judges an interpreter's code by its writes: nothing when it has
// none, exactly their targets when every write names a literal one, and every
// path any piece names (the reading before) when a target cannot be read or
// the code can escape to a shell, an eval or a dynamic call.
func (judge *pcmGuardJudge) interpret(lang string, pieces []string, dir string) {
	var targets []string
	for _, piece := range pieces {
		found, ok := pcmGuardWrites(lang, piece)
		if !ok {
			for _, each := range pieces {
				judge.code(each, dir)
			}
			return
		}
		targets = append(targets, found...)
	}
	for _, target := range targets {
		judge.codePath(target, dir)
	}
}

// codePath judges one literal path a program writes from dir; a $ or ` in it
// reads as the shell's expansion would, since an unquoted heredoc expands
// before the interpreter runs.
func (judge *pcmGuardJudge) codePath(target, dir string) {
	if dest, name, into := strings.Cut(target, pcmGuardInto); into {
		judge.codeInto(dest, name, dir)
		return
	}
	switch {
	case target == "":
	case strings.ContainsAny(target, "$`"):
		judge.word(target, dir)
	case filepath.IsAbs(target):
		judge.path(filepath.Clean(target))
	default:
		judge.path(filepath.Join(cmp.Or(dir, pcmGuardUnknownRoot), target))
	}
}

// codeInto judges the file a copy to dest writes: dest/name when dest ends
// in / or names an existing directory, dest itself being judged on its own.
func (judge *pcmGuardJudge) codeInto(dest, name, dir string) {
	if strings.ContainsAny(dest, "$`") {
		judge.word(dest, dir)
		return
	}
	abs := filepath.Clean(dest)
	if !filepath.IsAbs(dest) {
		abs = filepath.Join(cmp.Or(dir, pcmGuardUnknownRoot), dest)
	}
	info, err := os.Stat(abs)
	if strings.HasSuffix(dest, "/") || (err == nil && info.IsDir()) {
		judge.path(filepath.Join(abs, filepath.Base(name)))
	}
}

// pcmGuardWrites returns the literal paths lang's code writes; ok is false
// when a write target cannot be read or the code holds an escape.
func pcmGuardWrites(lang, code string) ([]string, bool) {
	scanned := code
	if lang == pcmGuardPython {
		scanned = pcmGuardPyMask(code)
	}
	if pcmGuardEscapes[lang].MatchString(scanned) {
		return nil, false
	}
	switch lang {
	case pcmGuardPython:
		return pcmGuardPythonWrites(code)
	case pcmGuardNode:
		return pcmGuardNodeWrites(code)
	case pcmGuardPerl:
		return pcmGuardPerlWrites(code)
	}
	return pcmGuardRubyWrites(code)
}

func pcmGuardNodeWrites(code string) ([]string, bool) {
	var targets []string
	for _, m := range pcmGuardNodeSites.FindAllStringSubmatchIndex(code, -1) {
		args, ok := pcmGuardCallAt(code, m[1], pcmGuardNode)
		if !ok || len(args) == 0 {
			return nil, false
		}
		name, paths := "createWriteStream", args[:1]
		if m[2] >= 0 {
			name = code[m[2]:m[3]]
		}
		switch {
		case name == pcmGuardOpen:
			if len(args) < 2 {
				continue
			}
			flags, ok := pcmGuardLiteral(pcmGuardNode, args[1])
			if !ok {
				return nil, false
			}
			if !strings.ContainsAny(flags, "wa+") {
				continue
			}
		case slices.Contains(pcmGuardNodeTwoPaths, name):
			if len(args) < 2 {
				return nil, false
			}
			paths = args[:2]
		}
		found, ok := pcmGuardLiterals(pcmGuardNode, paths)
		if !ok {
			return nil, false
		}
		targets = append(targets, found...)
	}
	return targets, true
}

func pcmGuardPerlWrites(code string) ([]string, bool) {
	var targets []string
	for _, m := range pcmGuardPerlSites.FindAllStringSubmatchIndex(code, -1) {
		name, args := code[m[2]:m[3]], pcmGuardLooseArgs(code, m[3], pcmGuardPerl)
		var found []string
		var ok bool
		switch {
		case name == pcmGuardOpen:
			found, ok = pcmGuardPerlOpen(args)
		case slices.Contains(pcmGuardPerlTwoPaths, name):
			ok = len(args) >= 2
			if ok {
				found, ok = pcmGuardLiterals(pcmGuardPerl, args[:2])
			}
		case slices.Contains(pcmGuardPerlOnePath, name):
			ok = len(args) >= 1
			if ok {
				found, ok = pcmGuardLiterals(pcmGuardPerl, args[:1])
			}
		default:
			ok = len(args) >= 1
			if ok {
				found, ok = pcmGuardLiterals(pcmGuardPerl, args)
			}
		}
		if !ok {
			return nil, false
		}
		targets = append(targets, found...)
	}
	return targets, true
}

// pcmGuardPerlOpen reads perl's open: a 2-arg mode prefix (>, >>, +<, +>) or
// a 3-arg mode holding > or + writes; a pipe is no file.
func pcmGuardPerlOpen(args []string) ([]string, bool) {
	if len(args) < 2 {
		return nil, false
	}
	mode, ok := pcmGuardLiteral(pcmGuardPerl, args[1])
	if !ok || strings.Contains(mode, "|") {
		return nil, false
	}
	if len(args) == 2 {
		spec := strings.TrimSpace(mode)
		rest := strings.TrimLeft(spec, "+<>")
		if !strings.ContainsAny(spec[:len(spec)-len(rest)], ">+") {
			return nil, true
		}
		return []string{strings.TrimSpace(rest)}, true
	}
	if !strings.ContainsAny(mode, ">+") {
		return nil, true
	}
	if len(args) > 3 {
		return nil, false
	}
	return pcmGuardLiterals(pcmGuardPerl, args[2:3])
}

func pcmGuardRubyWrites(code string) ([]string, bool) {
	var targets []string
	for _, m := range pcmGuardRubyOpen.FindAllStringSubmatchIndex(code, -1) {
		found, ok := pcmGuardRubyOpenCall(pcmGuardLooseArgs(code, m[3], pcmGuardRuby))
		if !ok {
			return nil, false
		}
		targets = append(targets, found...)
	}
	for _, m := range pcmGuardRubySites.FindAllStringSubmatchIndex(code, -1) {
		class, method := code[m[2]:m[3]], code[m[4]:m[5]]
		count := pcmGuardRubyPaths(class, method)
		if count == 0 {
			continue
		}
		args := pcmGuardLooseArgs(code, m[5], pcmGuardRuby)
		var found []string
		var ok bool
		switch {
		case count < 0:
			found, ok = pcmGuardRubyOpenCall(args)
		case class == "FileUtils" || method == "delete" || method == pcmGuardUnlink:
			positional, _, valid := pcmGuardKeywords(args, pcmGuardRubyKeyword)
			ok = valid && len(positional) > 0
			if ok {
				found, ok = pcmGuardLiterals(pcmGuardRuby, positional)
			}
		default:
			ok = len(args) >= count
			if ok {
				found, ok = pcmGuardLiterals(pcmGuardRuby, args[:count])
			}
		}
		if !ok {
			return nil, false
		}
		targets = append(targets, found...)
	}
	return targets, true
}

// pcmGuardRubyPaths is how a ruby class method writes: -1 opens by mode, n
// writes its first n arguments (FileUtils and File.delete every one), 0 none.
func pcmGuardRubyPaths(class, method string) int {
	switch class + "." + method {
	case "File.open", "File.new", "Kernel.open", "URI.open":
		return -1
	case "File.rename", "File.symlink", "File.link", "IO.copy_stream":
		return 2
	case "File.write", "File.binwrite", "File.truncate", "File.delete", "File.unlink", "IO.write", "IO.binwrite",
		"Dir.mkdir", "Dir.rmdir", "Dir.delete", "Dir.unlink":
		return 1
	}
	if class == "FileUtils" {
		return 1
	}
	return 0
}

// pcmGuardRubyOpenCall reads ruby's open(path, mode): a pipe path is no file,
// a mode holding w, a or + writes the path, no mode reads.
func pcmGuardRubyOpenCall(args []string) ([]string, bool) {
	positional, keywords, ok := pcmGuardKeywords(args, pcmGuardRubyKeyword)
	if !ok || len(positional) == 0 {
		return nil, false
	}
	if path, literal := pcmGuardLiteral(pcmGuardRuby, positional[0]); literal && strings.HasPrefix(path, "|") {
		return nil, false
	}
	mode, has := keywords["mode"]
	if !has && len(positional) > 1 {
		mode, has = positional[1], true
	}
	if !has {
		return nil, true
	}
	value, ok := pcmGuardLiteral(pcmGuardRuby, mode)
	if !ok {
		return nil, false
	}
	if !strings.ContainsAny(value, "wa+") {
		return nil, true
	}
	return pcmGuardLiterals(pcmGuardRuby, positional[:1])
}

// pcmGuardKeywords splits call arguments into positional ones and keyword
