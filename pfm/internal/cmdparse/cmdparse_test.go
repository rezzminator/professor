package cmdparse

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

// testCwd is the directory every test call runs in; the parse never reads it.
const testCwd = "/tmp/cmdparse-test"

// parseOne runs ParseBatch on one call.
func parseOne(t *testing.T, command string) []Part {
	t.Helper()
	got, err := ParseBatch([]Call{{ID: "c1", Command: command, Cwd: testCwd}})
	if err != nil {
		t.Fatalf("ParseBatch(%q): %v", command, err)
	}
	return got["c1"]
}

// step is one part's program and arguments, no arguments read as nil.
type step struct {
	Program string
	Args    []string
}

// assertSteps fails unless every part is ok and unbounded and the parts run
// exactly want, in order.
func assertSteps(t *testing.T, parts []Part, want []step) {
	t.Helper()
	var got []step
	for _, p := range parts {
		if p.Status != StatusOK || p.Bounded {
			t.Fatalf("part %+v is not ok and unbounded; parts = %+v", p, parts)
		}
		s := step{Program: p.Program}
		if len(p.Args) > 0 {
			s.Args = p.Args
		}
		got = append(got, s)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parts = %q\nwant    %q", got, want)
	}
}

func TestShellParseErrorIsOneErrorPart(t *testing.T) {
	t.Parallel()
	parts := parseOne(t, `echo "unterminated`)
	if !reflect.DeepEqual(parts, []Part{{Status: StatusError}}) {
		t.Fatalf("parts = %+v, want one error part", parts)
	}
}

func TestNodeEvalIsUnparsed(t *testing.T) {
	t.Parallel()
	parts := parseOne(t, `node -e "require('fs').readFileSync('x.js')"`)
	if len(parts) != 1 || parts[0].Program != "node" || parts[0].Status != StatusUnparsed {
		t.Fatalf("parts = %+v, want one unparsed node part", parts)
	}
}

func TestParseBatchRejectsRelativeCwd(t *testing.T) {
	t.Parallel()
	if _, err := ParseBatch([]Call{{ID: "a", Command: "true", Cwd: "rel"}}); err == nil {
		t.Fatal("ParseBatch accepted a relative cwd")
	}
}

// TestLiteralVariablesResolve: a literal assignment resolves a later
// expansion, the program word included.
func TestLiteralVariablesResolve(t *testing.T) {
	t.Parallel()
	assertSteps(t, parseOne(t, `G=git; $G push origin "$B"`), []step{{"git", []string{"push", "origin", `"$B"`}}})
	// unset changes only Part.Env: the values the git guard reads stay bound.
	unsetThenGit := []step{{"unset", []string{"D"}}, {"git", []string{"commit"}}}
	assertSteps(t, parseOne(t, `D=commit; unset D; git $D`), unsetThenGit)
}

// TestForLoopItemsBind: a for loop binds its variable to its items as
// written; a glob item is never expanded, quoted or not.
func TestForLoopItemsBind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command string
		want    []step
	}{
		{"literal items", `for f in a b; do grep x "$f"; done`, []step{{"grep", []string{"x", "a", "b"}}}},
		{
			"a glob item stays as written",
			`for f in *.md; do grep x "$f"; done`,
			[]step{{"grep", []string{"x", "*.md"}}},
		},
		{
			"a quoted glob item stays as written",
			`for f in "*.md"; do cat "$f"; done`,
			[]step{{"cat", []string{"*.md"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertSteps(t, parseOne(t, tc.command), tc.want)
		})
	}
}

// TestUnquotedBackslashEscapes: an unquoted `\X` is X, as the shell passes
// it; single quotes keep the backslash.
func TestUnquotedBackslashEscapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command string
		want    []step
	}{
		{"an escaped space joins the word", `cat x\ y`, []step{{"cat", []string{"x y"}}}},
		{"an escaped glob character is literal", `cat \*.md`, []step{{"cat", []string{"*.md"}}}},
		{"single quotes keep the backslash", `cat 'x\ y'`, []step{{"cat", []string{`x\ y`}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertSteps(t, parseOne(t, tc.command), tc.want)
		})
	}
}

// TestWordsExpandingToNothing: a command whose every word expands to nothing
// runs no program.
func TestWordsExpandingToNothing(t *testing.T) {
	t.Parallel()
	for _, command := range []string{`present=()&"${present[*]}"0`, `x=(); "${x[@]}" > out`} {
		for _, part := range parseOne(t, command) {
			if part.Program != "" {
				t.Errorf("%q: part %+v runs a program, want none", command, part)
			}
			if part.Status != StatusOK {
				t.Errorf("%q: part %+v has status %q, want %q", command, part, part.Status, StatusOK)
			}
		}
	}
}

// TestEmptyArrayCallParsesWithoutPanic: a call whose every word expands to
// nothing once panicked on its missing program word; it parses.
func TestEmptyArrayCallParsesWithoutPanic(t *testing.T) {
	t.Parallel()
	for _, command := range []string{`e=(); "${e[@]}"`, `"${empty[@]}"`} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%q: parse panicked: %v", command, r)
				}
			}()
			parseOne(t, command)
		}()
	}
}

// A command over maxCommandBytes is one unparsed, Bounded part: no shell
// parse is attempted. One of exactly maxCommandBytes parses as any other.
func TestOversizeCommandIsOneUnparsedPart(t *testing.T) {
	t.Parallel()
	pad := func(n int) string {
		cmd := "cat x;"
		return cmd + strings.Repeat(" ", n-len(cmd))
	}
	assertSteps(t, parseOne(t, pad(maxCommandBytes)), []step{{"cat", []string{"x"}}})
	over := parseOne(t, pad(maxCommandBytes+1))
	if want := []Part{{Status: StatusUnparsed, Bounded: true}}; !reflect.DeepEqual(over, want) {
		t.Fatalf("parts = %+v\nwant    %+v", over, want)
	}
}

// envOf is the Env of the first part running program, and whether one ran.
func envOf(parts []Part, program string) (map[string]string, bool) {
	for _, p := range parts {
		if p.Program == program {
			return p.Env, true
		}
	}
	return nil, false
}

func TestPartEnv(t *testing.T) {
	t.Parallel()
	home := map[string]string{"HOME": "/h"}
	cases := []struct {
		name    string
		command string
		vars    map[string]string
		want    map[string]string
	}{
		{"none", `claude x`, nil, nil},
		{"command prefix", `CLAUDE_CONFIG_DIR=/s claude x`, nil, map[string]string{"CLAUDE_CONFIG_DIR": "/s"}},
		{"prefix does not persist", `CLAUDE_CONFIG_DIR=/s true; claude`, nil, nil},
		{"export before", `export CLAUDE_CONFIG_DIR=/s; claude x`, nil, map[string]string{"CLAUDE_CONFIG_DIR": "/s"}},
		{
			"declare -x before",
			`declare -x CLAUDE_CONFIG_DIR=/s; claude`,
			nil,
			map[string]string{"CLAUDE_CONFIG_DIR": "/s"},
		},
		{"plain assign before", `CLAUDE_CONFIG_DIR=/s; claude`, nil, map[string]string{"CLAUDE_CONFIG_DIR": "/s"}},
		{"env wrapper", `env CLAUDE_CONFIG_DIR=/s claude x`, nil, map[string]string{"CLAUDE_CONFIG_DIR": "/s"}},
		{"sudo wrapper", `sudo -E CLAUDE_CONFIG_DIR=/s claude`, nil, map[string]string{"CLAUDE_CONFIG_DIR": "/s"}},
		{"plain sudo wrapper", `sudo CLAUDE_CONFIG_DIR=/s claude`, nil, map[string]string{"CLAUDE_CONFIG_DIR": "/s"}},
		{
			"prefix through a wrapper",
			`cd /tmp && CLAUDE_CONFIG_DIR=/s timeout 60 claude`,
			nil,
			map[string]string{"CLAUDE_CONFIG_DIR": "/s"},
		},
		{
			"seeded HOME",
			`CLAUDE_CONFIG_DIR=$HOME/.claude claude`,
			home,
			map[string]string{"CLAUDE_CONFIG_DIR": "/h/.claude"},
		},
		{
			"seeded HOME quoted",
			`CLAUDE_CONFIG_DIR="$HOME/.claude" claude`,
			home,
			map[string]string{"CLAUDE_CONFIG_DIR": "/h/.claude"},
		},
		{
			"seeded HOME braced",
			`CLAUDE_CONFIG_DIR=${HOME}/.claude claude`,
			home,
			map[string]string{"CLAUDE_CONFIG_DIR": "/h/.claude"},
		},
		{
			"seeded HOME partly quoted",
			`CLAUDE_CONFIG_DIR="$HOME"/.claude claude`,
			home,
			map[string]string{"CLAUDE_CONFIG_DIR": "/h/.claude"},
		},
		{
			"variable chain",
			`S=$HOME/.claude; CLAUDE_CONFIG_DIR=$S claude`,
			home,
			map[string]string{"CLAUDE_CONFIG_DIR": "/h/.claude", "S": "/h/.claude"},
		},
		{
			"unknown variable keeps source",
			`CLAUDE_CONFIG_DIR=$X/.claude claude`,
			nil,
			map[string]string{"CLAUDE_CONFIG_DIR": "$X/.claude"},
		},
		{"unset", `export CLAUDE_CONFIG_DIR=/s; unset CLAUDE_CONFIG_DIR; claude`, nil, nil},
		{"env -u", `export CLAUDE_CONFIG_DIR=/s; env -u CLAUDE_CONFIG_DIR claude`, nil, nil},
		{"env --unset=", `export CLAUDE_CONFIG_DIR=/s; env --unset=CLAUDE_CONFIG_DIR claude`, nil, nil},
		{"env -i clears", `export A=1 CLAUDE_CONFIG_DIR=/s; env -i B=2 claude`, nil, map[string]string{"B": "2"}},
		{
			"bash -c inherits",
			`CLAUDE_CONFIG_DIR=/s bash -c 'claude plugin list'`,
			nil,
			map[string]string{"CLAUDE_CONFIG_DIR": "/s"},
		},
		{
			"export reaches sh -c",
			`export CLAUDE_CONFIG_DIR=/s; sh -c 'claude plugin list'`,
			nil,
			map[string]string{"CLAUDE_CONFIG_DIR": "/s"},
		},
		{
			"bash -c double-quoted expands outside",
			`bash -c "CLAUDE_CONFIG_DIR=$HOME/.claude claude"`,
			home,
			map[string]string{"CLAUDE_CONFIG_DIR": "/h/.claude"},
		},
		{
			"bash -lc",
			`bash -lc 'CLAUDE_CONFIG_DIR=/s claude'`,
			nil,
			map[string]string{"CLAUDE_CONFIG_DIR": "/s"},
		},
		{
			"bash -c keeps the seeded HOME",
			`bash -c 'CLAUDE_CONFIG_DIR=$HOME/.claude claude'`,
			home,
			map[string]string{"CLAUDE_CONFIG_DIR": "/h/.claude"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseBatch([]Call{{ID: "c1", Command: tc.command, Cwd: testCwd, Vars: tc.vars}})
			if err != nil {
				t.Fatalf("ParseBatch(%q): %v", tc.command, err)
			}
			env, ran := envOf(got["c1"], "claude")
			if !ran {
				t.Fatalf("no claude part in %+v", got["c1"])
			}
			if !reflect.DeepEqual(env, tc.want) {
				t.Fatalf("Env = %v, want %v", env, tc.want)
			}
		})
	}
}

// TestRedirectsRideTheirParts: a statement's redirections ride its part; a
// compound command's ride every part inside it; a statement that runs no
// program is one program-less part holding them; a heredoc keeps its body.
func TestRedirectsRideTheirParts(t *testing.T) {
	t.Parallel()
	parts := parseOne(t, "F=out.md; echo a > $F; { echo b; echo c; } >> log 2>&1; > bare; tee x <<'EOF'\nbody\nEOF")
	type seen struct {
		Program string
		Redirs  []Redirect
	}
	var got []seen
	for _, p := range parts {
		got = append(got, seen{p.Program, p.Redirs})
	}
	group := []Redirect{{Op: ">>", Target: "log"}, {Op: ">&", Target: "1"}}
	want := []seen{
		{"echo", []Redirect{{Op: ">", Target: "out.md"}}},
		{"echo", group},
		{"echo", group},
		{"", []Redirect{{Op: ">", Target: "bare"}}},
		{"tee", []Redirect{{Op: "<<", Target: "EOF", Body: "body\n"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parts = %+v\nwant    %+v", got, want)
	}
}
