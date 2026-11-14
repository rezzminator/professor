package cmdparse

import (
	"maps"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// shells are the programs whose -c STRING the parse reads as shell.
var shells = strings.Fields("bash sh zsh dash ksh")

// shellScript finds the -c STRING of a shell (shells): -c alone or inside a
// flag cluster (-lc, -ec), the string being the first operand after the
// flags; -o/-O and --rcfile/--init-file take the next word as their value.
func shellScript(base string, rest []arg) (arg, bool) {
	if !slices.Contains(shells, base) {
		return arg{}, false
	}
	command := false
	for i := 0; i < len(rest); i++ {
		a := rest[i].text
		switch {
		case a == "--" || a == "-":
			if command && i+1 < len(rest) {
				return rest[i+1], true
			}
			return arg{}, false
		case a == "--rcfile" || a == "--init-file":
			i++
		case strings.HasPrefix(a, "--"):
		case len(a) > 1 && (a[0] == '-' || a[0] == '+'):
			if a[0] == '-' && strings.ContainsRune(a[1:], 'c') {
				command = true
			}
			if strings.ContainsAny(a[1:], "oO") {
				i++
			}
		default:
			if command {
				return rest[i], true
			}
			return arg{}, false
		}
	}
	return arg{}, false
}

// innerShell handles `bash -c STRING`. A literal STRING is parsed as shell
// in place: the bash part itself stays, then the string's own parts follow.
// The inner shell is a child process: its variables end with it, and its
// parse sees only the call's seeded variables. Its Part.Env is every
// assignment in force at the outer command, exported or not, since one to an
// already-exported name (CLAUDE_CONFIG_DIR) reaches the child. A STRING the parse cannot
// know stays an ordinary part marked Unread; one that does not parse is one
// error part, and one nested deeper than maxShellDepth is one unparsed,
// Bounded part.
func (p *callParser) innerShell(program string, rest []arg, script arg, env map[string]string) {
	p.emit(Part{Program: program, Args: argTexts(rest), Env: env, Unread: !script.resolved})
	if !script.resolved {
		return
	}
	vars, arrays, outer := p.vars, p.arrays, p.env
	p.vars, p.arrays = map[string][]string{}, map[string][]string{}
	for name, val := range p.base {
		p.vars[name] = []string{val}
	}
	p.env = maps.Clone(env)
	if p.env == nil {
		p.env = map[string]string{}
	}
	defer func() { p.vars, p.arrays, p.env = vars, arrays, outer }()
	p.nested(script.text)
}

// evalWords handles `eval WORDS`: the words joined by spaces are parsed as
// shell in place, in the calling shell, so its variables stay. A word the
// parse cannot know leaves the string unparsed and the eval part Unread.
func (p *callParser) evalWords(program string, rest []arg, env map[string]string) {
	resolved := !slices.ContainsFunc(rest, func(a arg) bool { return !a.resolved })
	p.emit(Part{Program: program, Args: argTexts(rest), Env: env, Unread: !resolved})
	if resolved && len(rest) > 0 {
		p.nested(strings.Join(argTexts(rest), " "))
	}
}

// nested parses text, a -c string or an eval string, as shell in place: one
// error part when it does not parse, one unparsed, Bounded part past
// maxShellDepth.
func (p *callParser) nested(text string) {
	if p.shellDepth >= maxShellDepth {
		p.emit(Part{Status: StatusUnparsed, Bounded: true})
		return
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(text), "")
	if err != nil {
		p.emit(Part{Status: StatusError})
		return
	}
	p.shellDepth++
	defer func() { p.shellDepth-- }()
	syntax.Walk(file, p.visit)
}
