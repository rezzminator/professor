package corpussan

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

const (
	demoProj = "/tmp/demo-proj"
	demoHome = "/tmp/demo-home"
)

// prefix is one root rewritten wherever it starts a path: an absolute root
// (abs) only at a path's start, an encoded one (a projects/ dir name) at any
// word boundary.
type prefix struct {
	from, to string
	abs      bool
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// encodeDir is Claude's projects/ dir name for a path: every character that
// is not a letter or a digit becomes '-'.
func encodeDir(path string) string { return nonAlnum.ReplaceAllString(path, "-") }

func newPrefixes(project, home string) []prefix {
	ps := []prefix{
		{project, demoProj, true},
		{home, demoHome, true},
		{demoProj, demoProj, true},
		{demoHome, demoHome, true},
		{encodeDir(project), encodeDir(demoProj), false},
		{encodeDir(home), encodeDir(demoHome), false},
	}
	sort.SliceStable(ps, func(i, j int) bool { return len(ps[i].from) > len(ps[j].from) })
	return ps
}

// systemRoots are absolute paths that identify nothing and keep their place:
// only their words are filtered.
var systemRoots = []string{"/dev/", "/usr/", "/bin/", "/sbin/", "/etc/", "/opt/", "/proc/", "/sys/"}

// scrubText rewrites the paths in v and fills every word the allowlist does
// not keep; punctuation, whitespace and operators stay, so a command keeps its
// shell structure and a path its shape.
func (s *Sanitizer) scrubText(v string) string {
	var out strings.Builder
	for i := 0; i < len(v); {
		if to, n, ok := s.rootAt(v, i); ok {
			out.WriteString(to)
			if n == 0 { // a foreign root: its own '/' follows, then its words
				out.WriteByte(v[i])
				n = 1
			}
			i += n
			continue
		}
		if !isRunByte(v[i]) {
			out.WriteByte(v[i])
			i++
			continue
		}
		j := i
		for j < len(v) && isRunByte(v[j]) {
			j++
		}
		if s.keepRun(v, i, j) {
			out.WriteString(v[i:j])
		} else {
			out.WriteString(strings.Repeat("x", j-i))
		}
		i = j
	}
	return out.String()
}

// rootAt reports the rewrite of a root starting at v[i]: what to write and how
// many bytes of v it consumes.
func (s *Sanitizer) rootAt(v string, i int) (string, int, bool) {
	if i > 0 && isNameByte(v[i-1]) {
		return "", 0, false
	}
	if strings.HasPrefix(v[i:], "~/") {
		return demoHome + "/", 2, true
	}
	absStart := v[i] == '/' && i+1 < len(v) && isNameByte(v[i+1]) &&
		(i == 0 || !strings.ContainsRune("$})]/:*?", rune(v[i-1])))
	for _, p := range s.prefixes {
		if p.abs && !absStart || !strings.HasPrefix(v[i:], p.from) {
			continue
		}
		// An absolute root ends at a path boundary; an encoded one may run on
		// into a subdirectory's name (-Users-a-proj-pfm is proj/pfm).
		if end := i + len(p.from); end < len(v) && (p.abs && isNameByte(v[end]) || isRunByte(v[end])) {
			continue
		}
		return p.to, len(p.from), true
	}
	if !absStart {
		return "", 0, false
	}
	for _, root := range systemRoots {
		if strings.HasPrefix(v[i:], root) {
			return "", 0, false
		}
	}
	return demoHome, 0, true // what follows is filtered word by word
}

var (
	digitLed  = regexp.MustCompile(`^\d+[A-Za-z]{0,2}$`)
	claudeDir = regexp.MustCompile(`^claude\d*$`)
	flagName  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// keepRun decides one word v[i:j]: a denied word never survives; numbers,
// words of one or two letters, allowlisted words, Claude config dir names and
// lower-case flag names do.
func (s *Sanitizer) keepRun(v string, i, j int) bool {
	w := v[i:j]
	switch {
	case s.denied(w):
		return false
	case digitLed.MatchString(w), len(w) <= 2, s.allow[w], claudeDir.MatchString(w):
		return true
	}
	return flagName.MatchString(w) && isFlagStart(v, i)
}

// isFlagStart reports whether v[i] follows the "-" or "--" that opens a word.
func isFlagStart(v string, i int) bool {
	k := i
	for k > 0 && i-k < 2 && v[k-1] == '-' {
		k--
	}
	return k < i && (k == 0 || !isNameByte(v[k-1]) && v[k-1] != '/')
}

// isRunByte is a byte of a word: a letter, digit, underscore or any byte of a
// multi-byte character.
func isRunByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c >= 0x80
}

// isNameByte is a byte that continues a file name.
func isNameByte(c byte) bool { return isRunByte(c) || c == '.' || c == '-' || c == '~' }

// fillerOf is v with every byte but a newline replaced by 'x': the same byte
// length and line count, none of the text.
func fillerOf(v string) string {
	b := []byte(v)
	for i := range b {
		if b[i] != '\n' {
			b[i] = 'x'
		}
	}
	return string(b)
}

// skipDirs are never walked for allowlist words.
var skipDirs = setOf(".git", ".worktrees", "node_modules")

// AllowFromTree returns, sorted and unique, every word of every file and
// directory name under root (skipDirs excepted): the repository's own names,
// kept in paths and commands.
func AllowFromTree(root string) ([]string, error) {
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.IsDir() && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		for _, w := range wordsOf(d.Name()) {
			seen[w] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("corpussan: walk %s: %w", root, err)
	}
	words := make([]string, 0, len(seen))
	for w := range seen {
		words = append(words, w)
	}
	slices.Sort(words)
	return words, nil
}

// wordsOf splits a name into its words, and a word with underscores into its
// parts too.
func wordsOf(name string) []string {
	var words []string
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return r < 0x80 && !isRunByte(byte(r)) }) {
		words = append(words, w)
		if strings.Contains(w, "_") {
			words = append(words, strings.FieldsFunc(w, func(r rune) bool { return r == '_' })...)
		}
	}
	return words
}

// builtinWords are program names, shell and Python keywords and system path
// words: kept in any command or path.
var builtinWords = strings.Fields(`
if then else elif fi for in do done while until case esac function return local export declare
readonly unset set shift exit break continue eval source exec command builtin true false test
cd pwd echo printf read cat head tail sed awk grep egrep rg find ls wc sort uniq cut tr tee xargs
env timeout nice nohup time sudo bash sh zsh python python3 node npm npx go git make jq diff
mkdir rmdir rm cp mv touch chmod ln stat file du df realpath readlink dirname basename seq sleep
kill ps which type curl wget http https tar gzip gunzip unzip zip sqlite3 open less more date mktemp
import from as def class with print len range not and or is None True False lambda try except
finally raise pass yield assert global sys os json re subprocess pathlib Path glob shutil argv
run build vet fmt gofmt mod tidy work get list install status typecheck verify iso sim
commit diff log show add branch checkout switch merge rebase stash fetch pull push tag rev parse
grep blame worktree remote reset restore clean describe HEAD origin main develop
tmp private var folders dev null stdin stdout stderr usr bin sbin etc opt proc sys local share lib
homebrew Library Application Support
claude projects subagents tool results meta cache
jsonl json yaml yml toml txt log md go mod sum sh py js ts mjs html css sql db csv
HOME PATH PWD TMPDIR USER SHELL EOF PY END
count short timeout race cover coverprofile
`)
