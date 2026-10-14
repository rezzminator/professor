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

// envOp is one change a wrapper makes to the environment of the program it
// runs.
type envOp struct {
	kind        int
	name, value string
}

const (
	envSet = iota
	envUnset
	envClear
)

// unwrapProgram strips wrappers, repeatedly, down to the program they run
// and its arguments; a program is matched by its base name, so
// /usr/bin/time is time. runs is false when a wrapper runs nothing that can
// be named (`command -v X`, a bare `env`); args is then the command as
// written. ops are the environment changes the wrappers made, in order.
func unwrapProgram(args []arg) (program []arg, runs bool, ops []envOp) {
	for {
		name := filepath.Base(args[0].text)
		w, ok := wrappers[name]
		if !ok {
			return args, true, ops
		}
		skip, lookup := w.skip(args[1:])
		if lookup || skip >= len(args)-1 {
			return args, false, nil
		}
		ops = append(ops, w.envOps(name, args[1:1+skip])...)
		args = args[1+skip:]
	}
}

// envOps reads the environment changes in the words a wrapper skipped:
// NAME=VALUE words, and for env -i/-/--ignore-environment and
// -u/--unset NAME.
func (w wrapper) envOps(name string, words []arg) []envOp {
	var ops []envOp
	for i := 0; i < len(words); i++ {
		a := words[i].text
		switch {
		case w.assigns && assignWord.MatchString(a):
			n, v, _ := strings.Cut(a, "=")
			ops = append(ops, envOp{kind: envSet, name: n, value: v})
		case name != "env":
		case a == "-i" || a == "-" || a == "--ignore-environment":
			ops = append(ops, envOp{kind: envClear})
		case (a == "-u" || a == "--unset") && i+1 < len(words):
			i++
			ops = append(ops, envOp{kind: envUnset, name: words[i].text})
		case strings.HasPrefix(a, "--unset="):
			ops = append(ops, envOp{kind: envUnset, name: strings.TrimPrefix(a, "--unset=")})
		case strings.HasPrefix(a, "-u") && !strings.HasPrefix(a, "--"):
			ops = append(ops, envOp{kind: envUnset, name: a[2:]})
		}
	}
	return ops
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
