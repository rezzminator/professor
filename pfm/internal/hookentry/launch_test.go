package hookentry

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestLaunchPassThroughPredicate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		arguments []string
		tmux      string
		forced    bool
		want      bool
	}{
		{name: "interactive empty argv"},
		{name: "resume stays interactive", arguments: []string{"--resume", "session-id"}},
		{name: "print short", arguments: []string{"-p", "hello"}, want: true},
		{name: "print long anywhere", arguments: []string{"--model", "opus", "--print", "hello"}, want: true},
		{name: "output format separate", arguments: []string{"--output-format", "json"}, want: true},
		{name: "output format equals", arguments: []string{"--output-format=stream-json"}, want: true},
		{name: "help short", arguments: []string{"-h"}, want: true},
		{name: "help long", arguments: []string{"--help"}, want: true},
		{name: "version short", arguments: []string{"-v"}, want: true},
		{name: "version long", arguments: []string{"--version"}, want: true},
		{name: "agents subcommand", arguments: []string{"agents", "--json"}, want: true},
		{name: "subcommand after flags", arguments: []string{"--verbose", "doctor"}, want: true},
		{name: "option value is first nonflag", arguments: []string{"--model", "opus", "agents"}},
		{name: "ordinary prompt", arguments: []string{"hello"}},
		{name: "already in claude socket", tmux: "/tmp/tmux-1000/cc-1-2-3,123,0", want: true},
		{name: "already in codex socket", tmux: "/tmp/tmux-1000/cx-1-2-3,123,0", want: true},
		{name: "unmanaged tmux", tmux: "/tmp/tmux-1000/vsct,123,0"},
		{name: "forced", forced: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := launchPassThrough(test.arguments, test.tmux, test.forced); got != test.want {
				t.Fatalf(
					"launchPassThrough(%q, %q, %t) = %t, want %t",
					test.arguments,
					test.tmux,
					test.forced,
					got,
					test.want,
				)
			}
		})
	}
}

func TestLaunchPassthroughSessionEnvironment(t *testing.T) {
	accountDir := t.TempDir()
	wantNames := claudelaunch.SessionEnv(config.ClaudePrefs{AutoCompactWindow: 50000})
	for _, entry := range wantNames {
		name, _, _ := strings.Cut(entry, "=")
		t.Setenv(name, "inherited")
	}
	t.Setenv("PFM_KEEP", "kept")
	previousExec := LaunchExec
	t.Cleanup(func() { LaunchExec = previousExec })
	machine := config.Runtime{Config: config.Config{
		Claude: config.ClaudePrefs{AutoCompactWindow: 100000},
		Accounts: []config.Account{
			{ID: 2, ConfigDir: accountDir, Claude: &config.ClaudePrefs{AutoCompactWindow: 50000}},
		},
	}}
	for _, test := range []struct {
		name, ambient, tmux, forced string
		args                        []string
		wantWindow                  int64
		session                     bool
	}{
		{"print account override", accountDir + "/.", "", "", []string{"-p", "hi"}, 50000, true},
		{"forced resume", accountDir, "", "1", []string{"--resume", "X"}, 50000, true},
		{"pfm socket", accountDir, "/tmp/tmux-1000/cc-1-2-3,123,0", "", []string{"--resume", "X"}, 50000, true},
		{"unmatched ambient", "", "", "", []string{"-p", "hi"}, 100000, true},
		{"plugin", accountDir, "", "", []string{"plugin", "install", "x"}, 0, false},
		{"mcp", accountDir, "", "", []string{"mcp", "list"}, 0, false},
		{"config", accountDir, "", "", []string{"config"}, 0, false},
		{"agents query", accountDir, "", "", []string{"agents", "--json"}, 0, false},
		{"version", accountDir, "", "", []string{"--version"}, 0, false},
		{"short version", accountDir, "", "", []string{"-v"}, 0, false},
		{"help", accountDir, "", "", []string{"-h"}, 0, false},
		{"long help", accountDir, "", "", []string{"--help"}, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", test.ambient)
			t.Setenv("TMUX", test.tmux)
			t.Setenv("PFM_LAUNCH_PASSTHROUGH", test.forced)
			inherited := os.Environ()
			var got []string
			LaunchExec = func(_ string, _, environment []string) error {
				got = append([]string(nil), environment...)
				return nil
			}
			var stderr bytes.Buffer
			if code := Launch(
				append([]string{"--real", "/bin/echo", "--"}, test.args...),
				&bytes.Buffer{},
				&stderr,
				machine,
				paths.OSEnv{},
			); code != 0 {
				t.Fatalf("Launch code=%d stderr=%q", code, stderr.String())
			}
			if !test.session {
				if !reflect.DeepEqual(got, inherited) {
					t.Fatalf("non-session env changed: got=%q want=%q", got, inherited)
				}
				return
			}
			for _, want := range claudelaunch.SessionEnv(config.ClaudePrefs{AutoCompactWindow: test.wantWindow}) {
				if count := countEnvironmentEntry(got, want); count != 1 {
					t.Errorf("%q occurs %d times in exec env", want, count)
				}
				name, _, _ := strings.Cut(want, "=")
				if count := countEnvironmentEntry(got, name+"=inherited"); count != 0 {
					t.Errorf("inherited %q survived %d times in exec env", name, count)
				}
			}
			if countEnvironmentEntry(got, "PFM_KEEP=kept") != 1 {
				t.Errorf("unrelated env lost from exec: %q", got)
			}
		})
	}
}

func countEnvironmentEntry(environment []string, want string) int {
	count := 0
	for _, entry := range environment {
		if entry == want {
			count++
		}
	}
	return count
}

func TestReadLaunchStatusRejectsMissingAndInvalidFiles(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "status")
	_, err := readLaunchStatus(path)
	if err == nil || !strings.Contains(err.Error(), "launcher status file missing") {
		t.Fatalf("readLaunchStatus missing error=%v", err)
	}
	if err := os.WriteFile(path, []byte("not-a-status\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = readLaunchStatus(path)
	if err == nil || !strings.Contains(err.Error(), "launcher status file invalid") {
		t.Fatalf("readLaunchStatus invalid error=%v", err)
	}
}

func TestAttachArgumentsBindNestedSeatToItsPane(t *testing.T) {
	t.Parallel()
	base := []string{"tmux", "-S", "/s/cc-1", "wait-for", "-S", "start", ";", "attach-session", "-t", "cc-1"}
	if got := attachArguments("/s/cc-1", "start", "cc-1", false); !reflect.DeepEqual(got, base) {
		t.Fatalf("plain terminal: attachArguments = %q, want %q", got, base)
	}
	nested := append(append([]string{}, base...), ";", "set-option", "-t", "cc-1", "destroy-unattached", "on")
	if got := attachArguments("/s/cc-1", "start", "cc-1", true); !reflect.DeepEqual(got, nested) {
		t.Fatalf("nested tmux: attachArguments = %q, want %q", got, nested)
	}
}
