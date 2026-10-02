// Package cmdparse turns one Bash call's command string into its simple
// commands, each with the program it runs and that program's arguments,
// through mvdan.cc/sh/v3/syntax. It never writes a parser of its own. Its
// owner is the git guard (internal/hookentry/git_guard.go), which reads every
// part and denies a git command whose part did not parse or hit a parse bound.
// Design: docs/design/hooks/git-guard.md § How it reads a command.
package cmdparse

import (
	"fmt"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Part statuses.
const (
	StatusOK       = "ok"
	StatusError    = "error"
	StatusUnparsed = "unparsed"
)

// The bounds of one call's parse, so no command can hold its caller. A
// command over maxCommandBytes is one unparsed part, never handed to the
// shell parser; a literal -c string nested deeper than maxShellDepth is one
// unparsed part in place of its parse. Every part a bound cut short carries
// Bounded.
const (
	maxCommandBytes = 65536
	maxShellDepth   = 8
)

// Call is one Bash tool call: its tool_use_id, its command string and the
// absolute directory it ran in.
type Call struct {
	ID, Command, Cwd string
}

// Part is one simple command of a call, in source order. Program is the
// command word as written, after unwrapping any wrapper (`timeout 60 env
// X=1 grep -n f x` is Program "grep", Args [-n f x]); a wrapper that runs
// nothing nameable (`command -v X`) stays the Program with its own Args.
// Args are the words after expansion of the literal variables and brace
// lists the parse knows; a glob is never expanded, so an unquoted `*`
// arrives as "*". A word the parse cannot know arrives as its source text.
// Status is StatusError for a command, or a -c string, that did not parse,
// and StatusUnparsed for a part whose code is not shell (`node -e`) or that
// a bound cut short.
// Bounded is true for a part a parse bound cut short: the command over
// maxCommandBytes, or the -c string nested past maxShellDepth. Its words are
// not what the shell would run, so a caller deciding on them treats it as
// unread.
type Part struct {
	Program string
	Args    []string
	Status  string
	Bounded bool
}

// ParseBatch parses every call and returns its parts keyed by Call.ID. The
// error return is for a malformed batch (duplicate id, relative cwd), never
// for a command that did not parse.
func ParseBatch(calls []Call) (map[string][]Part, error) {
	out := make(map[string][]Part, len(calls))
	for _, call := range calls {
		if _, dup := out[call.ID]; dup {
			return nil, fmt.Errorf("cmdparse: duplicate call id %q in batch", call.ID)
		}
		if !filepath.IsAbs(call.Cwd) {
			return nil, fmt.Errorf("cmdparse: call %q: cwd %q is not absolute", call.ID, call.Cwd)
		}
		out[call.ID] = parseCall(call).parts
	}
	return out, nil
}

// callParser walks one call's syntax tree in execution order, keeping the
// literal values assigned so far.
type callParser struct {
	vars    map[string][]string
	arrays  map[string][]string // literal indexed arrays: NAME=(a b c)
	parts   []Part
	printer *syntax.Printer
	// shellDepth counts the literal -c strings the walk is inside, up to
	// maxShellDepth.
	shellDepth int
}

func parseCall(call Call) *callParser {
	p := &callParser{
		vars:    map[string][]string{},
		arrays:  map[string][]string{},
		printer: syntax.NewPrinter(),
	}
	if len(call.Command) > maxCommandBytes {
		p.parts = []Part{{Status: StatusUnparsed, Bounded: true}}
		return p
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(call.Command), "")
	if err != nil {
		p.parts = []Part{{Status: StatusError}}
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
			p.callStmt(cmd)
			return false
		case *syntax.DeclClause:
			p.walkNested(cmd.Args, nil, x.Redirs)
			p.declStmt(cmd)
			return false
		}
	case *syntax.ForClause:
		iter, ok := x.Loop.(*syntax.WordIter)
		if !ok {
			return true
		}
		p.walkNested(nil, iter.Items, nil)
		var vals []string
		resolved := true
	items:
		for _, item := range iter.Items {
			for _, a := range p.args(item) {
				if !a.resolved {
					resolved = false
					break items
				}
				vals = append(vals, a.text)
			}
		}
		p.setVar(iter.Name.Value, vals, resolved && len(iter.Items) > 0)
		for _, stmt := range x.Do {
			syntax.Walk(stmt, p.visit)
		}
		return false
	}
	return true
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

// setVar binds name to vals; !ok unbinds it.
func (p *callParser) setVar(name string, vals []string, ok bool) {
	delete(p.arrays, name)
	if !ok {
		delete(p.vars, name)
		return
	}
	p.vars[name] = vals
}

func (p *callParser) assign(a *syntax.Assign) {
	if a.Name != nil && a.Array != nil && a.Index == nil && !a.Append {
		p.assignArray(a.Name.Value, a.Array)
		return
	}
	if a.Name == nil || a.Index != nil || a.Array != nil || a.Append || a.Value == nil {
		if a.Name != nil {
			p.setVar(a.Name.Value, nil, false)
		}
		return
	}
	w := p.eval(a.Value)
	p.setVar(a.Name.Value, w.vals, w.ok && len(w.vals) == 1)
}

func (p *callParser) emit(part Part) {
	if part.Status == "" {
		part.Status = StatusOK
	}
	p.parts = append(p.parts, part)
}

func (p *callParser) callStmt(cmd *syntax.CallExpr) {
	if len(cmd.Args) == 0 {
		for _, a := range cmd.Assigns {
			p.assign(a)
		}
		return
	}
	var args []arg
	for _, w := range cmd.Args {
		args = append(args, p.args(w)...)
	}
	if len(args) == 0 {
		// Every word expanded to nothing (`"${empty[@]}"`): no program runs.
		// unwrapProgram needs a word.
		return
	}
	args, runs := unwrapProgram(args)
	program, rest := args[0].text, args[1:]
	texts := argTexts(rest)
	if !runs {
		p.emit(Part{Program: program, Args: texts})
		return
	}
	base := filepath.Base(program)
	if script, ok := shellScript(base, rest); ok {
		p.innerShell(program, rest, script)
		return
	}
	if (base == "node" || base == "nodejs") && hasAny(texts, "-e", "--eval", "-p", "--print") {
		p.emit(Part{Program: program, Args: texts, Status: StatusUnparsed})
		return
	}
	p.emit(Part{Program: program, Args: texts})
}

func (p *callParser) declStmt(cmd *syntax.DeclClause) {
	var texts []string
	for _, a := range cmd.Args {
		p.assign(a)
		texts = append(texts, p.source(a))
	}
	p.emit(Part{Program: cmd.Variant.Value, Args: texts})
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
