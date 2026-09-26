package cmdparse

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"mvdan.cc/sh/v3/syntax"
)

// attribute applies the readers table to one simple command. A program the
// table does not know attributes every argument that is a file as unknown.
func (p *callParser) attribute(program string, args []arg) []FileRef {
	switch program {
	case "[", "test":
		// A file test asks whether a file exists; it reads nothing.
		return nil
	case "cat", "wc":
		return p.named(operands(args), ActionReadWhole, "")
	case "head", "tail":
		return p.headTail(program, args)
	case "sed":
		return p.sed(args)
	case "grep", "egrep", "fgrep", "rg":
		return p.search(program, args)
	case "ls", "du", "stat", "file", "realpath", "readlink":
		// Metadata names a file without reading it.
		return p.plain(operands(args), ActionStat)
	case "find":
		return p.plain(findStarts(args), ActionStat)
	}
	return p.plain(args, ActionUnknown)
}

// operands drops flags (everything up to `--` that starts with "-").
func operands(args []arg) []arg {
	var out []arg
	flags := true
	for _, a := range args {
		switch {
		case flags && a.text == "--":
			flags = false
		case flags && strings.HasPrefix(a.text, "-"):
		default:
			out = append(out, a)
		}
	}
	return out
}

// findStarts returns find's starting points: after its leading -H/-L/-P
// (and -D/-O with their values), the words before the first expression
// word. A word inside the expression is a pattern or a test, never a file
// find was given.
func findStarts(args []arg) []arg {
	i := 0
	for i < len(args) {
		switch a := args[i].text; {
		case a == "-H" || a == "-L" || a == "-P":
			i++
		case a == "-D" || a == "-O":
			i += 2
		case strings.HasPrefix(a, "-O"):
			i++
		default:
			var out []arg
			for _, s := range args[i:] {
				if strings.HasPrefix(s.text, "-") || s.text == "(" || s.text == "!" || s.text == "," {
					break
				}
				out = append(out, s)
			}
			return out
		}
	}
	return nil
}

// plain attributes the arguments that are existing regular files: what an
// unknown program, a metadata look or an exec names. A word that merely looks
// like a path is never a file.
func (p *callParser) plain(args []arg, action string) []FileRef {
	return p.refs(args, action, "", false)
}

// named attributes the file operands of a known reader or writer and a
// redirect target: one that does not exist at parse time is still attributed,
// with Exists false, so history keeps its deleted and scratch files. A glob
// that matched nothing and `-` (stdin) are never a missing file.
func (p *callParser) named(args []arg, action, rng string) []FileRef {
	return p.refs(args, action, rng, true)
}

func (p *callParser) refs(args []arg, action, rng string, missingOK bool) []FileRef {
	var files []FileRef
	for _, a := range args {
		if !a.resolved {
			continue
		}
		allowMissing := missingOK && !p.existingOnly && !a.unmatched && a.text != "-"
		if path, exists, ok := p.lookup(a.text, allowMissing); ok {
			files = append(files, FileRef{Path: path, Action: action, Range: rng, Exists: exists})
		}
	}
	return files
}

var digits = regexp.MustCompile(`^[+-]?\d+$`)

// headTail reads `-n N`, `-nN`, `--lines=N`, `-N` and the byte forms `-c N`;
// with none of them the reader's default of 10 lines is the range.
func (p *callParser) headTail(program string, args []arg) []FileRef {
	count, bytesMode := "10", false
	var files []arg
	flags := true
	for i := 0; i < len(args); i++ {
		a := args[i].text
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i].text
			}
			return ""
		}
		switch {
		case !flags || a == "-" || !strings.HasPrefix(a, "-"):
			files = append(files, args[i])
		case a == "--":
			flags = false
		case a == "-n" || a == "--lines":
			count, bytesMode = next(), false
		case a == "-c" || a == "--bytes":
			count, bytesMode = next(), true
		case strings.HasPrefix(a, "--lines="):
			count, bytesMode = strings.TrimPrefix(a, "--lines="), false
		case strings.HasPrefix(a, "--bytes="):
			count, bytesMode = strings.TrimPrefix(a, "--bytes="), true
		case strings.HasPrefix(a, "-n") && digits.MatchString(a[2:]):
			count, bytesMode = a[2:], false
		case strings.HasPrefix(a, "-c") && digits.MatchString(a[2:]):
			count, bytesMode = a[2:], true
		case digits.MatchString(a[1:]):
			count, bytesMode = a[1:], false
		}
	}
	var rng string
	switch {
	case program == "head":
		rng = "1," + count
	case strings.HasPrefix(count, "+"):
		rng = strings.TrimPrefix(count, "+") + ",$"
	default:
		rng = "-" + strings.TrimPrefix(count, "-")
	}
	if bytesMode {
		rng = "bytes " + rng
	}
	return p.named(files, ActionReadRange, rng)
}

var sedRange = regexp.MustCompile(`^\s*(\d+|\$)(?:\s*,\s*(\d+|\$))?\s*p\s*(?:;\s*\d*\s*q\s*)?;?\s*$`)

// sed: `-i` is a write; `-n` with one `A,Bp` (or `Ap`) script is a read of
// that range; any other script reads the whole file.
func (p *callParser) sed(args []arg) []FileRef {
	var scripts []string
	var files []arg
	quiet, inPlace, flags := false, false, true
	for i := 0; i < len(args); i++ {
		a := args[i].text
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i].text
			}
			return ""
		}
		switch {
		case !flags || a == "-" || !strings.HasPrefix(a, "-"):
			if len(scripts) == 0 {
				scripts = append(scripts, a)
				continue
			}
			files = append(files, args[i])
		case a == "--":
			flags = false
		case a == "--quiet" || a == "--silent":
			quiet = true
		case strings.HasPrefix(a, "--expression="):
			scripts = append(scripts, strings.TrimPrefix(a, "--expression="))
		case a == "--expression":
			scripts = append(scripts, next())
		case a == flagFile:
			next()
		case strings.HasPrefix(a, "--in-place"):
			inPlace = true
		case strings.HasPrefix(a, "--"):
		case a == "-i":
			// BSD sed takes the next word as the backup suffix; `-i ''` is
			// its in-place spelling.
			inPlace = true
			if i+1 < len(args) && args[i+1].text == "" {
				i++
			}
		default:
			for j := 1; j < len(a); j++ {
				c, rest := a[j], a[j+1:]
				switch c {
				case 'n':
					quiet = true
					continue
				case 'i':
					inPlace = true
				case 'e':
					if rest == "" {
						rest = next()
					}
					scripts = append(scripts, rest)
				case 'f', 'l':
					if rest == "" {
						next()
					}
				default:
					continue
				}
				break
			}
		}
	}
	switch {
	case inPlace:
		return p.named(files, ActionWrite, "")
	case quiet && len(scripts) == 1 && sedRange.MatchString(scripts[0]):
		m := sedRange.FindStringSubmatch(scripts[0])
		rng := m[1]
		if m[2] != "" {
			rng += "," + m[2]
		}
		return p.named(files, ActionReadRange, rng)
	}
	return p.named(files, ActionReadWhole, "")
}

// flagFile is the long flag sed, grep and rg share for reading their
// script or patterns from a file.
const flagFile = "--file"

// Flags of grep and rg that take the next word as their value.
var (
	grepValueShort  = "efABCmdD"
	rgValueShort    = "efgtTABCmMjE"
	searchValueLong = map[string]bool{
		"--regexp": true, flagFile: true, "--after-context": true, "--before-context": true,
		"--context": true, "--max-count": true, "--label": true, "--include": true, "--exclude": true,
		"--exclude-dir": true, "--glob": true, "--iglob": true, "--type": true, "--type-not": true,
		"--max-columns": true, "--threads": true, "--encoding": true, "--sort": true, "--sortr": true,
		"--color": true, "--colors": true, "--pre": true, "--type-add": true, "--max-depth": true,
	}
)

// search: the first operand is the pattern unless -e/-f/--regexp/--file
// gave one; every other operand that is a file is searched.
func (p *callParser) search(program string, args []arg) []FileRef {
	valueShort := grepValueShort
	if program == "rg" {
		valueShort = rgValueShort
	}
	var files []arg
	havePattern, flags := false, true
	for i := 0; i < len(args); i++ {
		a := args[i].text
		switch {
		case !flags || a == "-" || !strings.HasPrefix(a, "-"):
			if !havePattern {
				havePattern = true
				continue
			}
			files = append(files, args[i])
		case a == "--":
			flags = false
		case strings.HasPrefix(a, "--"):
			name, _, hasValue := strings.Cut(a, "=")
			if name == "--regexp" || name == flagFile {
				havePattern = true
			}
			if !hasValue && searchValueLong[name] {
				i++
			}
		default:
			for j := 1; j < len(a); j++ {
				c := a[j]
				if !strings.ContainsRune(valueShort, rune(c)) {
					continue
				}
				if c == 'e' || c == 'f' {
					havePattern = true
				}
				if j == len(a)-1 {
					i++
				}
				break
			}
		}
	}
	return p.named(files, ActionSearch, "")
}

// redirFiles attributes `>`, `>>`, `>|`, `&>`, `&>>` as writes and `<` as a
// whole read. Descriptor duplications, heredocs and /dev/* targets
// (/dev/null, /dev/stderr) name no file.
func (p *callParser) redirFiles(redirs []*syntax.Redirect) []FileRef {
	var files []FileRef
	for _, r := range redirs {
		var action string
		switch r.Op {
		case syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll:
			action = ActionWrite
		case syntax.RdrIn:
			action = ActionReadWhole
		default:
			continue
		}
		if r.Word == nil {
			continue
		}
		for _, a := range p.args(r.Word) {
			if a.resolved && strings.HasPrefix(filepath.Clean(a.text), "/dev/") {
				continue
			}
			files = append(files, p.named([]arg{a}, action, "")...)
		}
	}
	return files
}

// glob expands an unquoted glob against the current directory; no match (or a
// malformed pattern, or an unknown directory) leaves the word as written, as
// the shell does, and reports it unmatched: a pattern is never a missing file.
func (p *callParser) glob(val string, isGlob bool) (vals []string, unmatched bool) {
	if !isGlob || !strings.ContainsAny(val, "*?[") {
		return []string{val}, false
	}
	pattern := val
	if !filepath.IsAbs(pattern) {
		if p.dir == "" {
			return []string{val}, true
		}
		pattern = filepath.Join(p.dir, pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return []string{val}, true
	}
	if filepath.IsAbs(val) {
		return matches, false
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, relErr := filepath.Rel(p.dir, m)
		if relErr != nil {
			out = append(out, m)
			continue
		}
		out = append(out, rel)
	}
	return out, false
}

// file reports whether a resolved argument is an existing regular file.
func (p *callParser) file(a arg) (string, bool) {
	if !a.resolved {
		return "", false
	}
	path, _, ok := p.lookup(a.text, false)
	return path, ok
}

// lookup resolves a value against the current directory (a relative value
// attributes nothing when a `cd` left it unknown) and reports whether it names
// a file: an existing regular file (exists true) or, when missingOK, a path
// that does not exist (exists false). An existing non-regular file, or a
// path through a non-directory, names none. A stat failure other than "no
// such file" is kept as a note on the part, never read as absence.
func (p *callParser) lookup(val string, missingOK bool) (path string, exists, ok bool) {
	if val == "" || strings.ContainsAny(val, "\x00\n") {
		return "", false, false
	}
	if placeholder(val) {
		missingOK = false
	}
	path = val
	if !filepath.IsAbs(path) {
		if p.dir == "" {
			return "", false, false
		}
		path = filepath.Join(p.dir, path)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	switch {
	case err == nil:
		return path, true, info.Mode().IsRegular()
	case errors.Is(err, fs.ErrNotExist):
		return path, false, missingOK
	case errors.Is(err, syscall.ENOTDIR), errors.Is(err, syscall.ENAMETOOLONG):
		return "", false, false
	default:
		p.notes = append(p.notes, fmt.Sprintf("stat %s: %v", path, err))
		return "", false, false
	}
}

// placeholder reports text that still carries an expansion nobody performed:
// a `$` or a backquote the shell left in a Python string, a glob character,
// braces (xargs's and find's `{}`, a quoted or unexpandable `{a,b}`), a
// leading `~` no shell expanded. Such text names a file only when that exact
// file exists, never as a missing one.
func placeholder(val string) bool {
	open := strings.IndexByte(val, '{')
	return strings.ContainsAny(val, "$`*?[") || strings.HasPrefix(val, "~") ||
		(open >= 0 && strings.IndexByte(val[open:], '}') >= 0)
}

// mergeFiles drops duplicates and an unknown entry for a path that also has
// a known action, keeping first-seen order.
func mergeFiles(files []FileRef) []FileRef {
	known := map[string]bool{}
	for _, f := range files {
		if f.Action != ActionUnknown {
			known[f.Path] = true
		}
	}
	seen := map[FileRef]bool{}
	var out []FileRef
	for _, f := range files {
		if seen[f] || (f.Action == ActionUnknown && known[f.Path]) {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

var openFuncs = map[string]bool{"open": true, "io.open": true, "builtins.open": true, "codecs.open": true}

func isPathCtor(fn string) bool {
	return fn == "Path" || fn == "PurePath" || strings.HasSuffix(fn, ".Path") || strings.HasSuffix(fn, ".PurePath")
}

func modeAction(mode string) string {
	if strings.ContainsAny(mode, "wax") {
		return ActionWrite
	}
	return ActionReadWhole
}

func strAt(args []*string, i int) string {
	if i < len(args) && args[i] != nil {
		return *args[i]
	}
	return ""
}

// pyFiles attributes one scanned snippet: open(), Path(...) and its
// read/write methods (a path that does not exist yet included, Exists false),
// subprocess argv through the readers table, then every other string constant
// that is an existing file as unknown.
func (p *callParser) pyFiles(res PyResult) []FileRef {
	var files []FileRef
	add := func(val, action string) {
		// Python never expands `~` itself; a `~/…` string is expanduser's input.
		if strings.HasPrefix(val, "~/") && p.call.Home != "" {
			val = p.call.Home + val[1:]
		}
		if path, exists, ok := p.lookup(val, action != ActionUnknown); ok {
			files = append(files, FileRef{Path: path, Action: action, Exists: exists})
		}
	}
	for _, c := range res.Calls {
		fn := c.Func
		recvFn, method, isMethod := strings.Cut(fn, "().")
		switch {
		case openFuncs[fn]:
			mode := strAt(c.Args, 1)
			if m, ok := c.Kwargs["mode"]; ok {
				mode = m
			}
			add(strAt(c.Args, 0), modeAction(mode))
		case isPathCtor(fn):
			add(strAt(c.Args, 0), ActionUnknown)
		case isMethod && isPathCtor(recvFn):
			switch method {
			case "read_text", "read_bytes":
				add(strAt(c.Recv, 0), ActionReadWhole)
			case "write_text", "write_bytes":
				add(strAt(c.Recv, 0), ActionWrite)
			case "open":
				mode := strAt(c.Args, 0)
				if m, ok := c.Kwargs["mode"]; ok {
					mode = m
				}
				add(strAt(c.Recv, 0), modeAction(mode))
			default:
				add(strAt(c.Recv, 0), ActionUnknown)
			}
		case strings.HasPrefix(fn, "subprocess.") && len(c.Argv) > 0:
			args := make([]arg, 0, len(c.Argv)-1)
			for _, v := range c.Argv[1:] {
				args = append(args, arg{text: v, resolved: true})
			}
			files = append(files, p.attribute(filepath.Base(c.Argv[0]), args)...)
		}
	}
	for _, s := range res.Strings {
		add(s, ActionUnknown)
	}
	return files
}
