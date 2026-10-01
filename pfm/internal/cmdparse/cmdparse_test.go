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
