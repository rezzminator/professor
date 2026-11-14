package fakegit

import (
	"fmt"
	"strings"
	"testing"
)

// TestFakeGitHunks: a diff's changes pair lines along the one longest common
// subsequence, a replacement among equal lines staying put, and refuse a
// pairing git's heuristics would choose; a hunk side prints as git prints it;
// the hunk header's context is the nearest line opening with an ASCII letter,
// "_" or "$", trailing white space dropped.
func TestFakeGitHunks(t *testing.T) {
	const ambiguous = "a diff whose lines pair more than one way is not modelled"
	const slides = "a diff whose change could slide along equal lines is not modelled"
	for _, tc := range []struct {
		name, a, b, want string
	}{
		{"one replacement", "x\ny\nz\n", "x\nY\nz\n", "[{1 2 1 2}]"},
		{
			"two replacements",
			"1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n",
			"1\nB\n3\n4\n5\n6\n7\n8\nI\n10\n",
			"[{1 2 1 2} {8 9 8 9}]",
		},
		{"a replacement among equal lines", "b\nb\nb\n", "b\nX\nb\n", "[{1 2 1 2}]"},
		{"lines pairing two ways", "x\ny\n", "y\nx\n", ambiguous},
		{"an insertion that slides", "x\n", "x\nx\n", slides},
		{"a deletion that slides", "p\nq\nq\nr\n", "p\nq\nr\n", slides},
	} {
		changes, err := fakeGitChanges(fakeGitLines(tc.a), fakeGitLines(tc.b))
		got := fmt.Sprint(changes)
		if err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("%s: changes %s, want %s", tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct {
		start, count int
		want         string
	}{{0, 0, "0,0"}, {4, 1, "5"}, {1, 7, "2,7"}} {
		if got := fakeGitRange(tc.start, tc.count); got != tc.want {
			t.Errorf("range(%d, %d) = %q, want %q", tc.start, tc.count, got, tc.want)
		}
	}
	for _, tc := range []struct{ above, want string }{
		{"func alpha() {  \n\tbody\n", " func alpha() {"},
		{"$var\n\u00e9 sigma\n", " $var"},
		{"\tbody\n", ""},
		{strings.Repeat("a", 79) + "  b\n", " " + strings.Repeat("a", 79)},
	} {
		if got := fakeGitFuncname(fakeGitLines(tc.above)); got != tc.want {
			t.Errorf("funcname above %q = %q, want %q", tc.above, got, tc.want)
		}
	}
}
