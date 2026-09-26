// Package cmdparse turns one Bash call's command string into its simple
// commands and the files each one touches: the shell through
// mvdan.cc/sh/v3/syntax, embedded Python through Python's own ast module in
// one python3 process per batch. It never writes a parser of its own and it
// does not import callmeter, whose store caches what this package returns.
// Design: docs/design/hooks/callmeter.md § Parsing a command.
package cmdparse

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Part languages.
const (
	LangSh     = "sh"
	LangPython = "python"
	LangNode   = "node"
)

// Part statuses (command_parts.parse_status).
const (
	StatusOK                = "ok"
	StatusError             = "error"
	StatusUnparsed          = "unparsed"
	StatusPythonUnavailable = "python-unavailable"
)

// File actions.
const (
	ActionReadWhole = "read-whole"
	ActionReadRange = "read-range"
	ActionSearch    = "search"
	ActionWrite     = "write"
	ActionExec      = "exec"
	ActionUnknown   = "unknown"
	// ActionStat is a metadata look (ls, du, stat, file, find, realpath,
	// readlink): the file was named, never read, so no read count includes it.
	ActionStat = "stat"
)

// Version names this parser's behaviour. A stored call parsed by an older
// version is parsed again, so a fix reaches every call still in the window;
// raise it with every change to the parts or files a command parses to.
const Version = 2

// Call is one Bash tool call: its tool_use_id, its command string, the
// absolute directory it ran in, and the home directory a leading unquoted
// `~` expands to (empty: a `~` path attributes nothing).
type Call struct {
	ID, Command, Cwd, Home string
}

// FileRef is one file a part touched. Path is absolute. Exists is false for a
// path a known reader or writer named that did not exist at parse time (a
// scratch file since deleted, a write target not yet created); an unknown
// program's argument is attributed only as an existing regular file.
// Range is set for read-range only, in the reader's own terms: "A,B" or "A"
// for sed and head ("1,N"), "-N" for tail's last N lines, "K,$" for
// `tail -n +K`; a byte count is prefixed "bytes ".
type FileRef struct {
	Path   string
	Action string
	Range  string
	Exists bool
}

// Part is one simple command of a call, in source order. Program is the
// command word as written, after unwrapping any wrapper (`timeout 60 env
// X=1 grep -n f x` is Program "grep", Args [-n f x]); a wrapper that runs
// nothing nameable (`command -v X`) stays the Program with its own Args and
// attributes no file. Program is empty for a part that only carries the
// redirections of a compound command or a bare `> file`. Error holds the
// parser's message for StatusError, the cause for StatusPythonUnavailable,
// and any file-stat failure met while attributing (the part stays ok).
// Conditional is true for a part inside an if/elif/else branch or a case
// arm, or on the right-hand side of && or ||; an if's own condition is not.
type Part struct {
	Seq         int
	Lang        string
	Program     string
	Args        []string
	Files       []FileRef
	Status      string
	Error       string
	Conditional bool
}

// ParseBatch parses every call and returns its parts keyed by Call.ID. The
// shell pass runs per call; every Python snippet of the batch goes to py in
// one Analyze. A nil py is Python3{} (python3 on PATH). When py fails, every
// Python part of the batch is StatusPythonUnavailable with the cause in
// Error. The error return is for a malformed batch (duplicate id, relative
// cwd) or a cancelled ctx, never for a command that did not parse.
func ParseBatch(ctx context.Context, calls []Call, py PythonRunner) (map[string][]Part, error) {
	if py == nil {
		py = Python3{}
	}
	out := make(map[string][]Part, len(calls))
	parsers := make(map[string]*callParser, len(calls))
	var snippets []Snippet
	for _, call := range calls {
		if _, dup := out[call.ID]; dup {
			return nil, fmt.Errorf("cmdparse: duplicate call id %q in batch", call.ID)
		}
		if !filepath.IsAbs(call.Cwd) {
			return nil, fmt.Errorf("cmdparse: call %q: cwd %q is not absolute", call.ID, call.Cwd)
		}
		p := parseCall(call)
		parsers[call.ID] = p
		out[call.ID] = p.parts
		snippets = append(snippets, p.snippets...)
	}
	if len(snippets) == 0 {
		return out, nil
	}
	results, err := py.Analyze(ctx, snippets)
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("cmdparse: python batch of %d snippets: %w", len(snippets), ctx.Err())
	}
	byID := make(map[string]PyResult, len(results))
	for _, r := range results {
		byID[r.ID] = r
	}
	for _, call := range calls {
		p := parsers[call.ID]
		for snipID, pp := range p.pyParts {
			part := &p.parts[pp.idx]
			if err != nil {
				part.Status = StatusPythonUnavailable
				part.Error = err.Error()
				continue
			}
			res, ok := byID[snipID]
			if !ok {
				part.Status = StatusPythonUnavailable
				part.Error = fmt.Sprintf("cmdparse: %s: %v", snipID, errNoResult)
				continue
			}
			if res.Error != "" {
				part.Status = StatusError
				part.Error = res.Error
				continue
			}
			part.Status = StatusOK
			// The snippet's strings resolve in the directory its part ran in.
			p.dir = pp.dir
			part.Files = mergeFiles(append(part.Files, p.pyFiles(res)...))
			p.flushNotes(part)
		}
		out[call.ID] = p.parts
	}
	return out, nil
}

// callParser walks one call's syntax tree in execution order, keeping the
// literal values assigned so far and the directory a literal `cd` moved to.
type callParser struct {
	call Call
	vars map[string][]string
	// unmatched marks, per value of vars[NAME] or element of arrays[NAME],
	// a glob that matched nothing and was bound as written (`for f in *.log`,
	// `A=(*.log)`): never a missing file.
	unmatched map[string][]bool
	arrays    map[string][]string // literal indexed arrays: NAME=(a b c)
	parts     []Part
	snippets  []Snippet
	pyParts   map[string]pyPart // snippet id → its part
	notes     []string          // stat failures for the part being built
	printer   *syntax.Printer
	cond      int // depth of branches, case arms and && / || right-hand sides
	// dir is the absolute directory relative paths resolve against: the
	// call's cwd, moved by each literal `cd`. Empty after a `cd` to a target
	// the parse cannot know: a relative path then attributes nothing.
	dir string
	// existingOnly makes a known reader attribute existing files only, as
	// an unknown program does (its operands come through xargs).
	existingOnly bool
}

// pyPart is a Python part awaiting its scan: where it sits among the parts
// and the directory it ran in.
type pyPart struct {
	idx int
	dir string
}

func parseCall(call Call) *callParser {
	p := &callParser{
		call:      call,
		vars:      map[string][]string{},
		unmatched: map[string][]bool{},
		arrays:    map[string][]string{},
		pyParts:   map[string]pyPart{},
		dir:       call.Cwd,
		printer:   syntax.NewPrinter(),
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(call.Command), "")
	if err != nil {
		p.parts = []Part{{Seq: 0, Lang: LangSh, Status: StatusError, Error: err.Error()}}
		return p
	}
	syntax.Walk(file, p.visit)
	return p
}

func (p *callParser) visit(node syntax.Node) bool {
	switch x := node.(type) {
	case *syntax.Stmt:
		switch cmd := x.Cmd.(type) {
		case *syntax.CallExpr:
			p.walkNested(cmd.Assigns, cmd.Args, x.Redirs)
			p.callStmt(cmd, x.Redirs)
			return false
		case *syntax.DeclClause:
			p.walkNested(cmd.Args, nil, x.Redirs)
			p.declStmt(cmd, x.Redirs)
			return false
		}
		if len(x.Redirs) > 0 {
			p.redirectOnly(x.Redirs)
		}
	case *syntax.Subshell:
		p.subshell(func() { p.walkStmts(x.Stmts) })
		return false
	case *syntax.CmdSubst:
		p.subshell(func() { p.walkStmts(x.Stmts) })
		return false
	case *syntax.BinaryCmd:
		if x.Op == syntax.Pipe || x.Op == syntax.PipeAll {
			// Each side of a pipe runs in a subshell of its own.
			p.subshell(func() { syntax.Walk(x.X, p.visit) })
			p.subshell(func() { syntax.Walk(x.Y, p.visit) })
			return false
		}
		if x.Op != syntax.AndStmt && x.Op != syntax.OrStmt {
			return true
		}
		syntax.Walk(x.X, p.visit)
		p.conditional(func() { syntax.Walk(x.Y, p.visit) })
		return false
	case *syntax.IfClause:
		// The first condition always runs; the branches, and an elif's
		// condition, run only when a condition decides so.
		p.walkStmts(x.Cond)
		p.conditional(func() {
			p.walkStmts(x.Then)
			if x.Else != nil {
				syntax.Walk(x.Else, p.visit)
			}
		})
		return false
	case *syntax.CaseClause:
		syntax.Walk(x.Word, p.visit)
		p.conditional(func() {
			for _, item := range x.Items {
				syntax.Walk(item, p.visit)
			}
		})
		return false
	case *syntax.ForClause:
		iter, ok := x.Loop.(*syntax.WordIter)
		if !ok {
			return true
		}
		p.walkNested(nil, iter.Items, nil)
		// Loop items expand as any argument does, globs included.
		var vals []string
		var unmatched []bool
		resolved := true
	items:
		for _, item := range iter.Items {
			for _, a := range p.args(item) {
				if !a.resolved {
					resolved = false
					break items
				}
				vals = append(vals, a.text)
				unmatched = append(unmatched, a.unmatched)
			}
		}
		p.setVar(iter.Name.Value, vals, unmatched, resolved && len(iter.Items) > 0)
		for _, stmt := range x.Do {
			syntax.Walk(stmt, p.visit)
		}
		return false
	}
	return true
}

// conditional walks inside a branch that may not run.
func (p *callParser) conditional(walk func()) {
	p.cond++
	defer func() { p.cond-- }()
	walk()
}

// subshell walks code whose `cd` ends with it.
func (p *callParser) subshell(walk func()) {
	dir := p.dir
	defer func() { p.dir = dir }()
	walk()
}

func (p *callParser) walkStmts(stmts []*syntax.Stmt) {
	for _, stmt := range stmts {
		syntax.Walk(stmt, p.visit)
	}
}

// walkNested visits the command substitutions inside a command's words
// before the command itself, the order the shell runs them in.
func (p *callParser) walkNested(assigns []*syntax.Assign, words []*syntax.Word, redirs []*syntax.Redirect) {
	for _, a := range assigns {
		syntax.Walk(a, p.visit)
	}
	for _, w := range words {
		syntax.Walk(w, p.visit)
	}
	for _, r := range redirs {
		syntax.Walk(r, p.visit)
	}
}

// setVar binds name to vals, unmatched marking which of them is a glob
// that matched nothing (nil: none); !ok unbinds it.
func (p *callParser) setVar(name string, vals []string, unmatched []bool, ok bool) {
	delete(p.arrays, name)
	delete(p.unmatched, name)
	if !ok {
		delete(p.vars, name)
		return
	}
	p.vars[name] = vals
	for _, u := range unmatched {
		if u {
			p.unmatched[name] = unmatched
			break
		}
	}
}

func (p *callParser) assign(a *syntax.Assign) {
	if a.Name != nil && a.Array != nil && a.Index == nil && !a.Append {
		p.assignArray(a.Name.Value, a.Array)
		return
	}
	if a.Name == nil || a.Index != nil || a.Array != nil || a.Append || a.Value == nil {
		if a.Name != nil {
			p.setVar(a.Name.Value, nil, nil, false)
		}
		return
	}
	w := p.eval(a.Value)
	p.setVar(a.Name.Value, w.vals, w.unmatched, w.ok && len(w.vals) == 1 && !w.glob)
}

func (p *callParser) emit(part Part) int {
	part.Seq = len(p.parts)
	part.Conditional = p.cond > 0
	if part.Status == "" && part.Lang != LangPython {
		part.Status = StatusOK
	}
	part.Files = mergeFiles(part.Files)
	p.flushNotes(&part)
	p.parts = append(p.parts, part)
	return part.Seq
}

func (p *callParser) flushNotes(part *Part) {
	if len(p.notes) == 0 {
		return
	}
	if part.Error != "" {
		p.notes = append([]string{part.Error}, p.notes...)
	}
	part.Error = strings.Join(p.notes, "; ")
	p.notes = nil
}

func (p *callParser) callStmt(cmd *syntax.CallExpr, redirs []*syntax.Redirect) {
	if len(cmd.Args) == 0 {
		for _, a := range cmd.Assigns {
			p.assign(a)
		}
		if len(redirs) > 0 {
			p.redirectOnly(redirs)
		}
		return
	}
	var args []arg
	for _, w := range cmd.Args {
		args = append(args, p.args(w)...)
	}
	written := args
	args, runs := unwrapProgram(args)
	program, rest := args[0].text, args[1:]
	texts := argTexts(rest)
	if !runs {
		p.emit(Part{Lang: LangSh, Program: program, Args: texts, Files: p.redirFiles(redirs)})
		return
	}
	lead := p.programFile(args[0])
	base := filepath.Base(program)
	if script, ok := shellScript(base, rest); ok {
		p.innerShell(program, rest, script, lead, redirs)
		return
	}
	switch {
	case base == "cd":
		p.emit(Part{Lang: LangSh, Program: program, Args: texts, Files: append(lead, p.redirFiles(redirs)...)})
		p.cd(rest)
	case remoteRun(base, rest):
		// The arguments name paths on another machine.
		p.emit(Part{Lang: LangSh, Program: program, Args: texts, Files: append(lead, p.redirFiles(redirs)...)})
	case pythonName.MatchString(base):
		p.python(program, rest, lead, redirs)
	case (base == "node" || base == "nodejs") && hasAny(texts, "-e", "--eval", "-p", "--print"):
		files := slices.Concat(lead, p.redirFiles(redirs))
		p.emit(Part{Lang: LangNode, Program: program, Args: texts, Status: StatusUnparsed, Files: files})
	default:
		// Under xargs the words after the program are not its operands as
		// written (a replace string `{}`, input appended): only an existing
		// file is attributed.
		p.existingOnly = underXargs(written[:len(written)-len(args)])
		attributed := p.attribute(base, rest)
		p.existingOnly = false
		files := append(append(lead, attributed...), p.redirFiles(redirs)...)
		p.emit(Part{Lang: LangSh, Program: program, Args: texts, Files: files})
	}
}

// underXargs reports whether the wrapper words unwrapProgram stripped
// include xargs.
func underXargs(stripped []arg) bool {
	for _, a := range stripped {
		if filepath.Base(a.text) == "xargs" {
			return true
		}
	}
	return false
}

func (p *callParser) declStmt(cmd *syntax.DeclClause, redirs []*syntax.Redirect) {
	var texts []string
	for _, a := range cmd.Args {
		p.assign(a)
		texts = append(texts, p.source(a))
	}
	p.emit(Part{Lang: LangSh, Program: cmd.Variant.Value, Args: texts, Files: p.redirFiles(redirs)})
}

func (p *callParser) redirectOnly(redirs []*syntax.Redirect) {
	if files := p.redirFiles(redirs); len(files) > 0 {
		p.emit(Part{Lang: LangSh, Files: files})
	}
}

var pythonName = regexp.MustCompile(`^python(3(\.\d+)?)?$`)

// python handles python, python3 and python3.X: `-c CODE` and a heredoc or
// here-string on stdin become a Python snippet; `python3 script.py` is an
// exec of script.py; anything else is an ordinary shell part. lead is the
// interpreter's own exec attribution when it was given as a path under cwd.
func (p *callParser) python(program string, rest []arg, lead []FileRef, redirs []*syntax.Redirect) {
	texts := argTexts(rest)
	files := slices.Concat(lead, p.redirFiles(redirs))
	code, haveCode, stdinCode := "", false, true
	for i := 0; i < len(rest); i++ {
		a := rest[i].text
		switch {
		case a == "-c" && i+1 < len(rest):
			code, haveCode = p.text(rest[i+1].word), true
		case a == "-m":
			stdinCode = false
		case a == "-W" || a == "-X":
			i++
			continue
		case a == "-" || strings.HasPrefix(a, "-"):
			continue
		default:
			stdinCode = false
			files = append(files, p.plain(rest[i:i+1], ActionExec)...)
			files = append(files, p.plain(rest[i+1:], ActionUnknown)...)
		}
		if haveCode || !stdinCode {
			break
		}
	}
	if !haveCode && stdinCode {
		for _, r := range redirs {
			switch r.Op {
			case syntax.Hdoc, syntax.DashHdoc:
				code, haveCode = p.text(r.Hdoc), r.Hdoc != nil
			case syntax.WordHdoc:
				code, haveCode = p.text(r.Word), true
			}
		}
	}
	if !haveCode {
		p.emit(Part{Lang: LangSh, Program: program, Args: texts, Files: files})
		return
	}
	id := fmt.Sprintf("%s#%d", p.call.ID, len(p.parts))
	idx := p.emit(Part{Lang: LangPython, Program: program, Args: texts, Files: files})
	p.pyParts[id] = pyPart{idx: idx, dir: p.dir}
	p.snippets = append(p.snippets, Snippet{ID: id, Code: code})
}

func hasAny(texts []string, want ...string) bool {
	for _, t := range texts {
		for _, w := range want {
			if t == w {
				return true
			}
		}
	}
	return false
}
