package cmdparse

import (
	"reflect"
	"testing"
)

// TestBraceExpansion: `{a,b}` in an argument expands as bash expands it, so
// `wc -l {a,b}.go` runs on two words, never one "{a,b}.go". A glob inside
// stays as written; quoted braces, a sequence and an oversized product stay
// one literal word.
func TestBraceExpansion(t *testing.T) {
	t.Parallel()
	product := `{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}.go`
	cases := []struct {
		name, command string
		want          []step
	}{
		{
			name:    "a brace list is one word per element",
			command: `wc -l {a,b}.go`,
			want:    []step{{"wc", []string{"-l", "a.go", "b.go"}}},
		},
		{
			name:    "braces then a glob",
			command: `grep -n x {a,c_*}.go`,
			want:    []step{{"grep", []string{"-n", "x", "a.go", "c_*.go"}}},
		},
		{name: "quoted braces stay literal", command: `cat "{a,b}.go"`, want: []step{{"cat", []string{"{a,b}.go"}}}},
		{name: "a sequence is not expanded", command: `cat {1..3}.go`, want: []step{{"cat", []string{"{1..3}.go"}}}},
		{
			name:    "an oversized product is not expanded",
			command: `cat ` + product,
			want:    []step{{"cat", []string{product}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertSteps(t, parseOne(t, tc.command), tc.want)
		})
	}
}

func TestScriptSelfDir(t *testing.T) {
	t.Parallel()
	const script = "/repo/scripts/run.sh"
	cases := []struct {
		name, command string
		want          []step
	}{
		{
			"dirname of $0", `python3 "$(dirname "$0")/x.py"`,
			[]step{{"dirname", []string{script}}, {"python3", []string{"/repo/scripts/x.py"}}},
		},
		{
			"dirname -- of BASH_SOURCE", `cat "$(dirname -- "${BASH_SOURCE[0]}")/a"`,
			[]step{{"dirname", []string{"--", script}}, {"cat", []string{"/repo/scripts/a"}}},
		},
		{
			"cd into it and pwd", `cat "$(cd "$(dirname "$0")/.." && pwd -P)/b"`,
			[]step{
				{"dirname", []string{script}},
				{"cd", []string{"/repo/scripts/.."}},
				{"pwd", []string{"-P"}},
				{"cat", []string{"/repo/b"}},
			},
		},
		{
			"a relative cd stays unknown", `cat "$(cd lib && pwd)/c"`,
			[]step{{"cd", []string{"lib"}}, {"pwd", nil}, {"cat", []string{"$(cd lib && pwd)/c"}}},
		},
		{
			"any other substitution stays unknown", `cat "$(basename "$0")"`,
			[]step{{"basename", []string{script}}, {"cat", []string{`$(basename "$0")`}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseBatch([]Call{{ID: "c1", Command: tc.command, Script: Script{Path: script}}})
			if err != nil {
				t.Fatalf("ParseBatch(%q): %v", tc.command, err)
			}
			assertSteps(t, got["c1"], tc.want)
		})
	}
	typed := parseOne(t, `cat "$(dirname "$0")/x"`)
	assertSteps(t, typed, []step{{"dirname", []string{`"$0"`}}, {"cat", []string{`"$(dirname "$0")/x"`}}})
}

func TestScriptPositionalArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, command string
		want          []step
	}{
		{
			"an argument and an unset one", `cp a "$D/acks/$1.md" x$2`,
			[]step{{"cp", []string{"a", "$D/acks/chat.md", "x"}}},
		},
		{"the script's own", `cp a "/s/$1"`, []step{{"cp", []string{"a", "/s/chat"}}}},
		{
			"a function's are its own", `f() { cp a "/s/$1"; }; cp b "/s/$1"`,
			[]step{{"cp", []string{"a", "/s/$1"}}, {"cp", []string{"b", "/s/chat"}}},
		},
		{
			"shift reassigns them", `shift; cp a "/s/$1"`,
			[]step{{"shift", nil}, {"cp", []string{"a", "/s/$1"}}},
		},
		{
			"set -- reassigns them", `set -- x; cp a "/s/$1"`,
			[]step{{"set", []string{"--", "x"}}, {"cp", []string{"a", "/s/$1"}}},
		},
		{
			"set -e keeps them", `set -e; cp a "/s/$1"`,
			[]step{{"set", []string{"-e"}}, {"cp", []string{"a", "/s/chat"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := Script{Path: "/s/run.sh", Args: []string{"chat"}}
			got, err := ParseBatch([]Call{{ID: "c1", Command: tc.command, Script: script}})
			if err != nil {
				t.Fatalf("ParseBatch(%q): %v", tc.command, err)
			}
			assertSteps(t, got["c1"], tc.want)
		})
	}
	assertSteps(t, parseOne(t, `cp a "/s/$1"`), []step{{"cp", []string{"a", `"/s/$1"`}}})
}

// TestQuoteSplitsBackToTheWord: what Quote emits, SplitShellWords reads back as the one word it was.
func TestQuoteSplitsBackToTheWord(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"plain", "it's", `^Test(A|B)$`, "a b", `$HOME;rm -rf x`, "two\nlines", ""} {
		if words, err := SplitShellWords(Quote(s)); err != nil || !reflect.DeepEqual(words, []string{s}) {
			t.Fatalf("SplitShellWords(%s) = %q (%v), want [%q]", Quote(s), words, err, s)
		}
	}
}

// TestSplitShellWords: quotes and backslashes go as the shell removes them,
// nothing expands, and an unquoted operator or comment is refused.
func TestSplitShellWords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		line    string
		want    []string
		wantErr string
	}{
		{line: "", want: []string{}},
		{line: ` a\ b  'c d'"e" `, want: []string{"a b", "c de"}},
		{line: "one\\\ntwo three", want: []string{"onetwo", "three"}},
		{line: `"C:\dir \"q\" \\ \$x"`, want: []string{`C:\dir "q" \ $x`}},
		{line: `$HOME "$(pwd)" ~/x {a,b} *.go`, want: []string{"$HOME", "$(pwd)", "~/x", "{a,b}", "*.go"}},
		{line: "TMUX= exec tmux 'it'\"'\"'s'", want: []string{"TMUX=", "exec", "tmux", "it's"}},
		{line: "issue#12", want: []string{"issue#12"}},
		{line: "fix it #12", wantErr: `split shell words: unquoted comment "#12"`},
		{line: "a; b", wantErr: "split shell words: 1:2: `;` is not a valid word"},
		{line: `"open`, wantErr: "split shell words: 1:1: reached EOF without closing quote `\"`"},
	} {
		words, err := SplitShellWords(tc.line)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("SplitShellWords(%q) = %q, %v; want error %q", tc.line, words, err, tc.wantErr)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(words, tc.want) {
			t.Fatalf("SplitShellWords(%q) = %q, %v; want %q", tc.line, words, err, tc.want)
		}
	}
}
