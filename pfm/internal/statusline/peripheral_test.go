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
	for _, tc := range []struct {
		name    string
		tmuxDir func(home string) string
		want    string
	}{
		// A socket path that is a file cannot be listed: the fleet is unknown.
		{name: "unreadable socket directory", want: "● sol-6.1·effort unknown │ work │ git! │ fleet!\n🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? │ 💾– │ ⏳ 0s\nChatGPT quota missing\n", tmuxDir: func(home string) string {
			path := filepath.Join(home, "not-a-directory")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		// No socket directory means no tmux server ever ran: an empty fleet.
		{name: "absent socket directory", want: "● sol-6.1·effort unknown │ work │ git! │ · ·\n🟢 ▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱ ?% ? │ 💾– │ ⏳ 0s\nChatGPT quota missing\n", tmuxDir: func(home string) string { return filepath.Join(home, "absent") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parityRuntime(t)
			r.Engine = pfmengine.Codex
			r.Command = failedStatusCommand{}
			r.TmuxDir = tc.tmuxDir(r.Home)
			got, err := Render(context.Background(), []byte(`{"model":{"id":"gpt-6.1-sol"},"cwd":"/work"}`), r)
			if err != nil {
				t.Fatal(err)
			}
			if plain := stripANSICodes(got); plain != tc.want {
				t.Errorf("render = %q, want %q", plain, tc.want)
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
	if got, want := stripANSICodes(gitSegment(context.Background(), r, "/fixture")), "git!"; got != want {
		t.Fatalf("failed branch read segment = %q, want %q", got, want)
	}
}

func TestCodexQuotaMissingErrorUnknownStaleAndZero(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		stale            bool
	}{
		{name: "missing", want: "ChatGPT quota missing"},
		{name: "malformed", body: `{`, want: "ChatGPT quota error"},
		{name: "no window answered draws nothing", body: `{}`},
		{name: "missing percent", body: `{"primary":{"windowDurationMins":300,"resetsAt":1900000000}}`, want: "ChatGPT quota error"},
		{name: "unknown reset and length keep the reading", body: `{"primary":{"usedPercent":30,"windowDurationMins":0}}`, want: "▰▱▱▱▱ ?-used:30%"},
		{name: "invalid percent costs only its window", body: `{"primary":{"usedPercent":101,"windowDurationMins":10080,"resetsAt":1900000000},"secondary":{"usedPercent":30,"windowDurationMins":300,"resetsAt":1900000000}}`, want: "7d-used:error │ ▰▱▱▱▱ 5h-used:30% ↻1157d9h"},
		{name: "every window invalid reads as an error", body: `{"primary":{"usedPercent":-1,"windowDurationMins":300,"resetsAt":1900000000}}`, want: "5h-used:error"},
		{name: "zero", body: `{"primary":{"usedPercent":0,"windowDurationMins":300,"resetsAt":1900000000}}`, want: "▱▱▱▱▱ 5h-used:0% ↻1157d9h"},
		{name: "stale", body: `{"primary":{"usedPercent":30,"windowDurationMins":300,"resetsAt":1900000000}}`, want: "▰▱▱▱▱ 5h-used:30% ↻1157d9h stale", stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parityRuntime(t)
			r.Warn = &bytes.Buffer{}
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
			if got := stripANSICodes(codexQuotaSegment(r, r.now())); got != tc.want {
				t.Errorf("quota segment = %q, want %q", got, tc.want)
			}
		})
	}
}
