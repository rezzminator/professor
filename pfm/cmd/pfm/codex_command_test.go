package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// forwardCases are every forwarded action of both engines at each exit code
// the interface defines; testForwarders runs them through pfm's own argv.
var forwardCases = []struct {
	name   string
	args   []string
	stdout string
	code   int
}{
	{name: "codex build", args: []string{"codex", "build", ".", "--model", "opus=x"}, stdout: "CODEX BUILD PASS"},
	{name: "codex check finding", args: []string{"codex", "check", "--home", "/h"}, code: 1},
	{name: "codex agents", args: []string{"codex", "agents", "--home", "/h"}, stdout: "CODEX AGENTS PASS"},
	{name: "codex usage", args: []string{"codex", "generate"}, code: 2},
	{
		name: "opencode build", args: []string{"opencode", "build", "."},
		stdout: "OPENCODE BUILD PASS wrote=0 unchanged=1 deleted=0",
	},
	{name: "opencode check finding", args: []string{"opencode", "check"}, code: 1},
	{name: "opencode doctor", args: []string{"opencode", "doctor", "--home", "/h"}, stdout: "OPENCODE DOCTOR PASS"},
}

func testForwarders(t *testing.T, engine string) {
	for _, testCase := range forwardCases {
		if testCase.args[0] != engine {
			continue
		}
		t.Run(testCase.name, func(t *testing.T) {
			root := jailTest(t)
			record := testjail.FakeEngineSync(t, testCase.stdout, testCase.code)
			var stdout, stderr bytes.Buffer
			if code := run(testCase.args, &stdout, &stderr); code != testCase.code {
				t.Fatalf("pfm %q exit = %d, want %d; stderr=%q", testCase.args, code, testCase.code, stderr.String())
			}
			calls, want := testjail.EngineSyncCalls(t, record), []string{strings.Join(testCase.args, " ")}
			if !slices.Equal(calls, want) {
				t.Fatalf("engine-sync calls = %q, want %q", calls, want)
			}
			home, err := os.ReadFile(record + ".home")
			if err != nil || strings.TrimSpace(string(home)) != filepath.Join(root, "home") {
				t.Fatalf(
					"engine-sync HOME = %q (%v), want pfm's resolved home %q",
					home,
					err,
					filepath.Join(root, "home"),
				)
			}
			if want := testCase.stdout + "\n"; stdout.String() != want {
				t.Fatalf("stdout = %q, want %q", stdout.String(), want)
			}
			wantStderr := "pfm " + engine + ": deprecated, runs engine-sync " + strings.Join(testCase.args, " ") +
				"\nusage: engine-sync\n"
			if stderr.String() != wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), wantStderr)
			}
		})
	}
}

func TestCodexCommandForwardsToEngineSync(t *testing.T) { testForwarders(t, "codex") }

func TestCodexCommandWithoutEngineSyncIsExitOne(t *testing.T) {
	root := jailTest(t)
	t.Setenv("ENGINE_SYNC_BIN", "")
	t.Setenv("ENGINE_SYNC_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"codex", "check", "."}, &stdout, &stderr); code != 1 {
		t.Fatalf("pfm codex check without engine-sync exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	stable := filepath.Join(root, "home", ".local", "share", "engine-sync", "bin", "engine-sync")
	if !strings.Contains(stderr.String(), "pfm codex: engine-sync binary unavailable: "+stable+": ") {
		t.Fatalf("stderr = %q does not name the unavailable binary at %s", stderr.String(), stable)
	}
}
