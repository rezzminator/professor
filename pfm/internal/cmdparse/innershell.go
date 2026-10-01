package cmdparse

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// shellScript finds the -c STRING of bash, sh and zsh: -c alone or inside a
// flag cluster (-lc, -ec), the string being the first operand after the
// flags; -o/-O and --rcfile/--init-file take the next word as their value.
func shellScript(base string, rest []arg) (arg, bool) {
	if base != "bash" && base != "sh" && base != "zsh" {
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
// The inner shell is a child process: its variables end with it, and it
// sees no unexported variable of the outer one. A STRING the parse cannot
// know stays an ordinary part; one that does not parse is one error part,
// and one nested deeper than maxShellDepth is one unparsed, Bounded part.
func (p *callParser) innerShell(program string, rest []arg, script arg) {
	p.emit(Part{Program: program, Args: argTexts(rest)})
	if !script.resolved {
		return
	}
	if p.shellDepth >= maxShellDepth {
		p.emit(Part{Status: StatusUnparsed, Bounded: true})
		return
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script.text), "")
	if err != nil {
		p.emit(Part{Status: StatusError})
		return
	}
	vars, arrays := p.vars, p.arrays
	p.vars, p.arrays = map[string][]string{}, map[string][]string{}
	p.shellDepth++
	defer func() {
		p.vars, p.arrays = vars, arrays
		p.shellDepth--
	}()
	syntax.Walk(file, p.visit)
}
