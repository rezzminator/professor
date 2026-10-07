package cmdparse

import "testing"

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
