package cmdparse

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestShellAttribution(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		files   []string
		command string
		want    func(cwd string) []FileRef
	}{
		{
			name:    "compound with a variable",
			files:   []string{"notes.txt"},
			command: `F=notes.txt; wc -l "$F" && grep -n func "$F"`,
			want: func(cwd string) []FileRef {
				return []FileRef{
					ref(cwd, "notes.txt", ActionReadWhole, ""),
					ref(cwd, "notes.txt", ActionSearch, ""),
				}
			},
		},
		{
			// A test asks whether a file exists; it reads nothing. Counting it
			// made every guarded read (`if [ -f x ]; then cat x; fi`) certain.
			name:    "file tests read nothing",
			files:   []string{"a.go"},
			command: `[ -f a.go ] && [[ -s a.go ]]; test -e a.go`,
			want:    func(string) []FileRef { return nil },
		},
		{
			name:    "head piped to tail",
			files:   []string{"a.go"},
			command: `head -n 40 a.go | tail -n 10`,
			want: func(cwd string) []FileRef {
				return []FileRef{ref(cwd, "a.go", ActionReadRange, "1,40")}
			},
		},
		{
			name:    "sed line range",
			files:   []string{"a.go"},
			command: `sed -n '10,20p' a.go`,
			want: func(cwd string) []FileRef {
				return []FileRef{ref(cwd, "a.go", ActionReadRange, "10,20")}
			},
		},
		{
			name:    "for loop over two files",
			files:   []string{"a.go", "b.go"},
			command: `for f in a.go b.go; do cat "$f"; done`,
			want: func(cwd string) []FileRef {
				return []FileRef{ref(cwd, "a.go", ActionReadWhole, ""), ref(cwd, "b.go", ActionReadWhole, "")}
			},
		},
		{
			name:    "glob, redirections and command substitution",
			files:   []string{"a.go", "b.go", "in.txt", "out.txt"},
			command: `wc -l $(ls *.go) < in.txt > out.txt`,
			want: func(cwd string) []FileRef {
				return []FileRef{
					ref(cwd, "a.go", ActionStat, ""),
					ref(cwd, "b.go", ActionStat, ""),
					ref(cwd, "in.txt", ActionReadWhole, ""),
					ref(cwd, "out.txt", ActionWrite, ""),
				}
			},
		},
		{
			// Metadata is not a read: the corpus counted every `ls` and `du`
			// as a read of the file it named.
			name:    "metadata commands stat",
			files:   []string{"a.go", "b.go", "c.go"},
			command: `ls -l a.go; du -sh b.go; stat -c %s c.go; file a.go; realpath b.go; readlink -f c.go`,
			want: func(cwd string) []FileRef {
				return []FileRef{
					ref(cwd, "a.go", ActionStat, ""),
					ref(cwd, "b.go", ActionStat, ""),
					ref(cwd, "c.go", ActionStat, ""),
					ref(cwd, "a.go", ActionStat, ""),
					ref(cwd, "b.go", ActionStat, ""),
					ref(cwd, "c.go", ActionStat, ""),
				}
			},
		},
		{
			// find's operands are its starting points; what follows the first
			// expression word is a pattern, never a file.
			name:    "find stats its starting points only",
			files:   []string{"x", "a.go"},
			command: `find . -name x; find -L a.go -newer x`,
			want: func(cwd string) []FileRef {
				return []FileRef{ref(cwd, "a.go", ActionStat, "")}
			},
		},
		{
			name:    "redirects to /dev and fd duplications attribute nothing",
			files:   []string{"x"},
			command: `cat x > /dev/null 2>&1; cat x >/dev/stderr; cat x >&2`,
			want: func(cwd string) []FileRef {
				return []FileRef{
					ref(cwd, "x", ActionReadWhole, ""),
					ref(cwd, "x", ActionReadWhole, ""),
					ref(cwd, "x", ActionReadWhole, ""),
				}
			},
		},
		{
			name:    "redirect targets",
			files:   []string{"x", "o1", "o2", "o3", "o4"},
			command: "cat < x; echo a > o1; echo b >> o2; echo c &> o3; echo d >| o4; cat <<EOF\nx\nEOF",
			want: func(cwd string) []FileRef {
				return []FileRef{
					ref(cwd, "x", ActionReadWhole, ""),
					ref(cwd, "o1", ActionWrite, ""),
					ref(cwd, "o2", ActionWrite, ""),
					ref(cwd, "o3", ActionWrite, ""),
					ref(cwd, "o4", ActionWrite, ""),
				}
			},
		},
		{
			name:    "python script is an exec",
			files:   []string{"script.py"},
			command: `python3 script.py`,
			want: func(cwd string) []FileRef {
				return []FileRef{ref(cwd, "script.py", ActionExec, "")}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cwd := fixture(t, tc.files...)
			parts := parseOne(t, cwd, tc.command, nil)
			for _, p := range parts {
				if p.Status != StatusOK {
					t.Fatalf("part %+v: status %q, want ok", p, p.Status)
				}
			}
			assertFiles(t, parts, tc.want(cwd))
		})
	}
}

// A known reader or writer naming a path that does not exist yet (a scratch
// file, a heredoc's write target) attributes it with Exists false; the corpus
// lost every `cat > f <<EOF` write. An unknown program's words, a metadata
// look and a glob that matched nothing stay unattributed.
func TestMissingFiles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command string
		want    func(cwd string) []FileRef
	}{
		{"heredoc write target", "cat > f <<EOF\nx\nEOF", func(cwd string) []FileRef {
			return []FileRef{missing(cwd, "f", ActionWrite, "")}
		}},
		{"append target", "cat >> f", func(cwd string) []FileRef {
			return []FileRef{missing(cwd, "f", ActionWrite, "")}
		}},
		{"sed range of a gone file", "sed -n 1,5p gone.txt", func(cwd string) []FileRef {
			return []FileRef{missing(cwd, "gone.txt", ActionReadRange, "1,5")}
		}},
		{
			"readers and input redirect",
			"head -n 3 a; grep x b; wc -l c; sed -i s/a/b/ d; cat < e",
			func(cwd string) []FileRef {
				return []FileRef{
					missing(cwd, "a", ActionReadRange, "1,3"),
					missing(cwd, "b", ActionSearch, ""),
					missing(cwd, "c", ActionReadWhole, ""),
					missing(cwd, "d", ActionWrite, ""),
					missing(cwd, "e", ActionReadWhole, ""),
				}
			},
		},
		{"a missing file beside an existing one", "cat have.txt gone.txt", func(cwd string) []FileRef {
			return []FileRef{ref(cwd, "have.txt", ActionReadWhole, ""), missing(cwd, "gone.txt", ActionReadWhole, "")}
		}},
		{"unknown program", "foo gone.txt", func(string) []FileRef { return nil }},
		{"metadata look", "ls gone", func(string) []FileRef { return nil }},
		{"glob that matched nothing", "cat *.nomatch", func(string) []FileRef { return nil }},
		{"stdin and a directory", "cat - sub; tail -n 2 -", func(string) []FileRef { return nil }},
		{"dev targets", "cat x > /dev/nowhere-such", func(cwd string) []FileRef {
			return []FileRef{missing(cwd, "x", ActionReadWhole, "")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cwd := fixture(t, "have.txt")
			if err := os.Mkdir(filepath.Join(cwd, "sub"), 0o700); err != nil {
				t.Fatalf("mkdir sub: %v", err)
			}
			assertFiles(t, parseOne(t, cwd, tc.command, nil), tc.want(cwd))
		})
	}
}

// TestTildeAndPlaceholders covers the false missing files a 30-day backfill
// found: a `~` joined to the cwd as a literal directory (756 rows), and text
// the shell never expanded taken as a path — a `$X` inside a Python string,
// xargs's `{}`, a Python glob pattern.
func TestTildeAndPlaceholders(t *testing.T) {
	t.Parallel()
	home := fixture(t, ".zsh_history")
	cwd := fixture(t)
	hist := filepath.Join(home, ".zsh_history")
	cases := []struct {
		name, home, command string
		want                []FileRef
	}{
		{
			name:    "a leading tilde is the home directory",
			home:    home,
			command: `tail -n 5 ~/.zsh_history ~/gone.txt; F=~/.zsh_history; wc -l "$F"`,
			want: []FileRef{
				{Path: hist, Action: ActionReadRange, Range: "-5", Exists: true},
				{Path: filepath.Join(home, "gone.txt"), Action: ActionReadRange, Range: "-5"},
				{Path: hist, Action: ActionReadWhole, Exists: true},
			},
		},
		{name: "no home: a tilde path attributes nothing", command: `cat ~/.zsh_history`},
		{name: "another user's home attributes nothing", home: home, command: `cat ~root/.profile`},
		{
			name:    "xargs placeholder is never a file",
			home:    home,
			command: `ls | xargs -I{} sh -c 'git show HEAD:{} > /tmp/q/{}'`,
		},
		{name: "Python string with an unexpanded $X", home: home, command: `python3 -c "open('$P/package.json')"`},
		{name: "Python glob pattern", home: home, command: `python3 -c "open('tmp/*/package.json')"`},
		{
			name:    "Python expanduser string",
			home:    home,
			command: `python3 -c "import os; print(open(os.path.expanduser('~/.zsh_history')).read())"`,
			want:    []FileRef{{Path: hist, Action: ActionUnknown, Exists: true}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseBatch(context.Background(),
				[]Call{{ID: "c1", Command: tc.command, Cwd: cwd, Home: tc.home}},
				Python3{Runner: deps.RealRunner{}})
			if err != nil {
				t.Fatalf("ParseBatch(%q): %v", tc.command, err)
			}
			assertFiles(t, got["c1"], tc.want)
		})
	}
}
