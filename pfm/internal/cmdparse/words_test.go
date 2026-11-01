package cmdparse

import (
	"os/exec"
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

// TestQuoteSurvivesAShell: what Quote emits, bash reads back as the one word
// it was, and so does SplitShellWords.
func TestQuoteSurvivesAShell(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"plain", "it's", `^Test(A|B)$`, "a b", `$HOME;rm -rf x`, "two\nlines", ""} {
		out, err := exec.Command("bash", "-c", "printf %s "+Quote(s)).Output()
		if err != nil || string(out) != s {
			t.Fatalf("Quote(%q) = %s round-trips through bash to %q (%v)", s, Quote(s), out, err)
		}
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
