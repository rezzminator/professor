package sourcesweep

import "testing"

func TestSkipDirLeavesDotAndGeneratedDirectories(t *testing.T) {
	for _, tc := range []struct {
		rel  string
		want bool
	}{
		{".", false},
		{"internal/paths", false},
		{".opencode", true},
		{".arch", true},
		{"internal/.hidden", true},
		{"harness-prompts", false},
		{"harness-prompts/generated", true},
		{"harness-prompts/generatedx", false},
		{"internal/harness-prompts/generated", false},
		{"testdata", false},
	} {
		if got := SkipDir(tc.rel); got != tc.want {
			t.Errorf("SkipDir(%q) = %t, want %t", tc.rel, got, tc.want)
		}
	}
}
