package cmdparse

import "testing"

// TestBraceExpansion: `{a,b}` in an argument expands as bash expands it
// before any glob, so `wc -l {a,b}.go` reads two files, never one missing
// "{a,b}.go". A sequence and an oversized product are left unknown.
func TestBraceExpansion(t *testing.T) {
	t.Parallel()
	cwd := fixture(t, "a.go", "b.go", "c_test.go")
	cases := []struct {
		name, command string
		want          []FileRef
	}{
		{
			name:    "a brace list names each file",
			command: `wc -l {a,b}.go`,
			want:    []FileRef{ref(cwd, "a.go", ActionReadWhole, ""), ref(cwd, "b.go", ActionReadWhole, "")},
		},
		{
			name:    "braces then a glob",
			command: `grep -n x {a,c_*}.go`,
			want:    []FileRef{ref(cwd, "a.go", ActionSearch, ""), ref(cwd, "c_test.go", ActionSearch, "")},
		},
		{
			name:    "a missing file inside braces",
			command: `cat {a,gone}.go`,
			want:    []FileRef{ref(cwd, "a.go", ActionReadWhole, ""), missing(cwd, "gone.go", ActionReadWhole, "")},
		},
		{name: "quoted braces stay literal", command: `cat "{a,b}.go"`},
		{name: "a sequence is not expanded", command: `cat {1..3}.go`},
		{name: "an oversized product is not expanded", command: `cat {a,b}{a,b}{a,b}{a,b}{a,b}{a,b}{a,b}.go`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertFiles(t, parseOne(t, cwd, tc.command, nil), tc.want)
		})
	}
}
