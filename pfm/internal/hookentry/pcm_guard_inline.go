package hookentry

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
)

// The inline code an interpreter call carries (python -c, node -e, --eval) and its reading by the
// paths it names.

// pcmGuardCodeFlags are the short flags whose value is inline code, per
// interpreter; python names every python* program.
var pcmGuardCodeFlags = map[string]string{
	pcmGuardPython: "c", pcmGuardNode: "ep", "nodejs": "ep", pcmGuardPerl: "eE", pcmGuardRuby: "e",
}

// pcmGuardCode returns the inline code an interpreter runs: the value of a
// code flag (alone or in a short cluster: -c, -e, -pe, the code the next
// word or joined to it: -c'code'), --eval and --print. A shell's -c string
// (bash, sh, zsh, dash, ksh) is no code here: cmdparse parses it into parts of its own, or marks
// its part Unread when it cannot know the string (PcmGuard's bash). Its
// heredocs and here-strings are pcmGuardStdin's.
func pcmGuardCode(part *cmdparse.Part, base string) []string {
	shell := slices.Contains(pcmGuardShells, base)
	if strings.HasPrefix(base, pcmGuardPython) {
		base = pcmGuardPython
	}
	flags, interpreter := pcmGuardCodeFlags[base]
	if !shell && !interpreter {
		return nil
	}
	var code []string
	for i, a := range part.Args {
		if name, value, ok := strings.Cut(a, "="); ok && !shell && slices.Contains(pcmGuardLongCode, name) {
			code = append(code, value)
			continue
		}
		if flags != "" && len(a) > 1 && a[0] == '-' && a[1] != '-' {
			if j := strings.IndexAny(a[1:], flags); j >= 0 {
				if joined := a[j+2:]; joined != "" {
					code = append(code, joined) // -c'code', -pe'code': the value joined to its flag
				} else if i+1 < len(part.Args) {
					code = append(code, part.Args[i+1])
				}
				continue
			}
		}
		if i+1 < len(part.Args) && interpreter && !shell && slices.Contains(pcmGuardLongCode, a) {
			code = append(code, part.Args[i+1])
		}
	}
	return code
}

// code judges code by the paths it names: every path-shaped token (holding
// a /, named CLAUDE.md, or naming an existing file) is judged as a write. A
// token naming an existing directory counts only as a whole quoted literal
// ('templates'), the one form a directory operation takes it in; the same word
// inside prose ("FAIL templates steps") names no path. It is the reading of
// code whose writes pcmGuardJudge.interpret cannot read.
func (judge *pcmGuardJudge) code(code, dir string) {
	literals := map[string]bool{}
	for _, match := range pcmGuardWholeLiteral.FindAllStringSubmatch(code, -1) {
		literals[match[2]] = true
	}
	tokens := strings.FieldsFunc(code, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("'\"`()[]{};,=<>|&$!#*?\\", r)
	})
	for _, token := range tokens {
		abs, ok := judge.resolve(token, dir)
		if !ok {
			continue
		}
		named := false
		if !pcmGuardOnUnknownRoot(abs) {
			info, err := os.Lstat(abs)
			named = err == nil && (!info.IsDir() || literals[token])
		}
		if strings.Contains(token, "/") || strings.EqualFold(filepath.Base(token), "CLAUDE.md") || named {
			judge.path(abs)
		}
	}
}

// pcmGuardWholeLiteral is one quoted string whose whole text is a single word.
var pcmGuardWholeLiteral = regexp.MustCompile("(['\"`])([^'\"`\\s]+)['\"`]")
