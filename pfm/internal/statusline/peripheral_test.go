package statusline

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestParityIdentityAndFailuresVisible(t *testing.T) {
	r := parityRuntime(t)
	r.Engine = pfmengine.Codex
	r.Command = failedStatusCommand{}
	r.TmuxDir = filepath.Join(r.Home, "absent")
	got, err := Render(context.Background(), []byte(`{"model":{"id":"gpt-6.1-sol"},"cwd":"/work"}`), r)
	if err != nil {
		t.Fatal(err)
	}
	plain := stripANSICodes(got)
	for _, want := range []string{"sol-6.1", "fleet!", "git!", "quota missing"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in %q", want, plain)
		}
	}
}

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

type failedStatusCommand struct{}

func (failedStatusCommand) Output(context.Context, string, ...string) ([]byte, error) {
	return nil, fmt.Errorf("fixture command failed")
}

type branchFailureCommand struct{}

func (branchFailureCommand) Output(_ context.Context, _ string, args ...string) ([]byte, error) {
	if strings.Contains(strings.Join(args, " "), "symbolic-ref") {
		return nil, context.DeadlineExceeded
	}
	return []byte("true"), nil
}

func TestGitBranchFailureIsNotDetachedHEAD(t *testing.T) {
	r := parityRuntime(t)
	r.Command = branchFailureCommand{}
	r.Warn = &bytes.Buffer{}
	if got := stripANSICodes(gitSegment(context.Background(), r, "/fixture")); !strings.Contains(got, "git!") {
		t.Fatalf("failed branch read manufactured detached HEAD: %q", got)
	}
}

func TestCodexQuotaMissingErrorUnknownStaleAndZero(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		stale            bool
	}{
		{name: "missing", want: "quota missing"},
		{name: "malformed", body: `{`, want: "quota error"},
		{name: "unknown", body: `{}`, want: "quota unknown"},
		{name: "missing percent", body: `{"primary":{"windowDurationMins":300,"resetsAt":1900000000}}`, want: "quota error"},
		{name: "zero", body: `{"primary":{"usedPercent":0,"windowDurationMins":300,"resetsAt":1900000000}}`, want: "5h-used:0%"},
		{name: "stale", body: `{"primary":{"usedPercent":30,"windowDurationMins":300,"resetsAt":1900000000}}`, want: "stale", stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parityRuntime(t)
			path := filepath.Join(r.CacheDir, "cc-gpt-usage-0.json")
			if tc.body != "" {
				if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
				stamp := r.now()
				if tc.stale {
					stamp = stamp.Add(-6 * time.Minute)
				}
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			if got := stripANSICodes(codexQuotaSegment(r, r.now())); !strings.Contains(got, tc.want) {
				t.Fatalf("%q lacks %q", got, tc.want)
			}
		})
	}
}
