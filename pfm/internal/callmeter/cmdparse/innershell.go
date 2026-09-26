package cmdparse

import (
	"fmt"
	"path/filepath"
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
// in place: the bash part itself stays (its program file and redirections,
// never the string as a file), then the string's own parts follow at the
// same conditional depth, starting in the current directory. The inner shell
// is a child process: its `cd` and its variables end with it, and it sees no
// unexported variable of the outer one. A STRING the parse cannot know
// stays an ordinary part; one that does not parse is one error part.
func (p *callParser) innerShell(program string, rest []arg, script arg, lead []FileRef, redirs []*syntax.Redirect) {
	if !script.resolved {
		files := append(append(lead, p.attribute(filepath.Base(program), rest)...), p.redirFiles(redirs)...)
		p.emit(Part{Lang: LangSh, Program: program, Args: argTexts(rest), Files: files})
		return
	}
	p.emit(Part{Lang: LangSh, Program: program, Args: argTexts(rest), Files: append(lead, p.redirFiles(redirs)...)})
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script.text), "")
	if err != nil {
		p.emit(Part{Lang: LangSh, Status: StatusError, Error: fmt.Sprintf("%s -c: %v", filepath.Base(program), err)})
		return
	}
	dir, vars, unmatched, arrays := p.dir, p.vars, p.unmatched, p.arrays
	p.vars, p.unmatched, p.arrays = map[string][]string{}, map[string][]bool{}, map[string][]string{}
	defer func() { p.dir, p.vars, p.unmatched, p.arrays = dir, vars, unmatched, arrays }()
	syntax.Walk(file, p.visit)
}

// cd moves the directory later parts resolve against. A literal relative
// target joins the current directory, an absolute one replaces it; `cd`
// alone, `cd -`, `cd ~…` and a target the parse cannot know leave the
// directory unknown until a later literal absolute `cd` or the end of the
// enclosing subshell.
func (p *callParser) cd(rest []arg) {
	ops := operands(rest)
	if len(ops) != 1 || !ops[0].resolved || ops[0].text == "" || strings.HasPrefix(ops[0].text, "~") {
		p.dir = ""
		return
	}
	target := ops[0].text
	switch {
	case filepath.IsAbs(target):
		p.dir = filepath.Clean(target)
	case p.dir != "":
		p.dir = filepath.Join(p.dir, target)
	}
}

// remoteRun reports a program whose arguments name paths on another machine
// or inside a container: ssh and mosh, and the exec (or run) verb of docker,
// podman, kubectl and oc.
func remoteRun(base string, rest []arg) bool {
	switch base {
	case "ssh", "mosh":
		return true
	case "docker", "podman", "kubectl", "oc":
		for _, a := range rest {
			switch {
			case a.text == "--":
				return false
			case a.text == "exec", a.text == "run" && (base == "docker" || base == "podman"):
				return true
			}
		}
	}
	return false
}
