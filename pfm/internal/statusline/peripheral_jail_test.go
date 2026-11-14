//go:build jail

package statusline

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitSegmentRealRepositoryStates(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "ordinary directory"},
		{name: "detached HEAD", want: "HEAD"},
		{name: "missing directory", want: "git!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parityRuntime(t)
			r.Command = commandRunner{}
			var warnings bytes.Buffer
			r.Warn = &warnings
			dir := t.TempDir()
			if tc.name == "detached HEAD" {
				for _, args := range [][]string{
					{"-C", dir, "init"},
					{"-C", dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture"},
					{"-C", dir, "checkout", "--detach"},
				} {
					if _, err := r.Command.Output(context.Background(), "git", args...); err != nil {
						t.Fatalf("prepare detached fixture %v: %v", args, err)
					}
				}
			}
			if tc.name == "missing directory" {
				dir = filepath.Join(dir, "missing")
			}
			got := stripANSICodes(gitSegment(context.Background(), r, dir))
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("segment = %q, want %q; warnings = %q", got, tc.want, warnings.String())
			}
			if tc.want == "git!" {
				if !strings.Contains(warnings.String(), dir) {
					t.Fatalf("failed probe must name directory: %q", warnings.String())
				}
			} else if warnings.Len() != 0 {
				t.Fatalf("healthy repository state warned: %q", warnings.String())
			}
		})
	}
}
