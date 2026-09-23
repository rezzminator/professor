package cmdparse

import (
	"path/filepath"
	"regexp"
	"strings"
)

// wrapper is one program that runs another: the flags it takes before that
// program, and what else stands between them.
type wrapper struct {
	valueShort string          // short flags whose value is the next word when not attached
	valueLong  map[string]bool // long flags whose value is the next word when not given with =
	lookup     string          // short flags that turn the call into a lookup that runs nothing
	assigns    bool            // NAME=VALUE words may precede the program
	operand    bool            // one operand precedes the program (timeout's duration)
	dash       bool            // a lone "-" is a flag (env's -i)
}

var wrappers = map[string]wrapper{
	"command": {lookup: "vV"},
	"builtin": {},
	"exec":    {valueShort: "a"},
	"env": {
		valueShort: "uCS", assigns: true, dash: true,
		valueLong: map[string]bool{"--unset": true, "--chdir": true, "--split-string": true},
	},
	"timeout": {valueShort: "ks", operand: true, valueLong: map[string]bool{"--kill-after": true, "--signal": true}},
	"nice":    {valueShort: "n", valueLong: map[string]bool{"--adjustment": true}},
	"nohup":   {},
	"time":    {valueShort: "fo", valueLong: map[string]bool{"--format": true, "--output": true}},
	"sudo": {
		valueShort: "ugCDprtUTR", assigns: true,
		valueLong: map[string]bool{
			"--user": true, "--group": true, "--close-from": true, "--chdir": true, "--prompt": true,
			"--role": true, "--type": true, "--other-user": true, "--command-timeout": true, "--host": true,
		},
	},
	"xargs": {
		valueShort: "IndPLsEa",
		valueLong: map[string]bool{
			"--max-args": true, "--max-procs": true, "--delimiter": true, "--max-lines": true,
			"--max-chars": true, "--arg-file": true, "--process-slot-var": true,
		},
	},
}

var assignWord = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// unwrapProgram strips wrappers, repeatedly, down to the program they run
// and its arguments; a program is matched by its base name, so
// /usr/bin/time is time. runs is false when a wrapper runs nothing that can
// be named (`command -v X`, a bare `env`); args is then the command as
// written.
func unwrapProgram(args []arg) (program []arg, runs bool) {
	for {
		w, ok := wrappers[filepath.Base(args[0].text)]
		if !ok {
			return args, true
		}
		skip, lookup := w.skip(args[1:])
		if lookup || skip >= len(args)-1 {
			return args, false
		}
		args = args[1+skip:]
	}
}

// skip returns how many of a wrapper's arguments precede the program it
// runs, and whether one of them makes the call a lookup.
func (w wrapper) skip(args []arg) (int, bool) {
	flags, operandSeen := true, false
	for i := 0; i < len(args); i++ {
		a := args[i].text
		switch {
		case flags && a == "--":
			flags = false
		case flags && w.dash && a == "-":
		case flags && strings.HasPrefix(a, "--"):
			name, _, hasValue := strings.Cut(a, "=")
			if !hasValue && w.valueLong[name] {
				i++
			}
		case flags && len(a) > 1 && a[0] == '-':
			if w.lookup != "" && strings.ContainsAny(a[1:], w.lookup) {
				return i, true
			}
			for j := 1; j < len(a); j++ {
				if strings.IndexByte(w.valueShort, a[j]) < 0 {
					continue
				}
				if j == len(a)-1 {
					i++
				}
				break
			}
		case w.assigns && assignWord.MatchString(a):
		case w.operand && !operandSeen:
			operandSeen = true
		default:
			return i, false
		}
	}
	return len(args), false
}

// programFile attributes a program given as a path (`./run.sh`,
// `scripts/x.sh`, an absolute path inside the directory) that is a regular
// file under the directory its part runs in, after any `cd`, as an exec. A
// bare name, a path outside that directory (/bin/ls), or any path once a
// `cd` left the directory unknown attributes nothing.
func (p *callParser) programFile(program arg) []FileRef {
	if !strings.Contains(program.text, "/") || p.dir == "" {
		return nil
	}
	path, ok := p.file(program)
	if !ok {
		return nil
	}
	rel, err := filepath.Rel(p.dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil
	}
	return []FileRef{{Path: path, Action: ActionExec, Exists: true}}
}
