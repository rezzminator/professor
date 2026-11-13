package testjail

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// FakeEngineSyncRunner answers `version` unrecorded and every other call with
// the reply, writing the record files FakeEngineSync's stand-in writes.
func TestFakeEngineSyncRunnerRecordsEveryCallButVersion(t *testing.T) {
	record, runner := FakeEngineSyncRunner(t, `{"ok":false}`, 1)
	binary := os.Getenv(envEngineSyncBin)
	version, err := runner.Run(t.Context(), []string{binary, "version"}, deps.RunOptions{})
	if err != nil || string(version.Stdout) != "engine-sync 9.0.0-testjail\n" || version.ExitCode != 0 {
		t.Fatalf("version = %+v, %v; want engine-sync 9.0.0-testjail, exit 0", version, err)
	}
	env := []string{"HOME=/h", "ENGINE_SYNC_WORKBENCH_MARKER=/w/.professor", "ENGINE_SYNC_DISABLE=1"}
	build, err := runner.Run(t.Context(), []string{binary, "codex", "build", "/w", "--json"}, deps.RunOptions{Env: env})
	if err != nil || string(build.Stdout) != "{\"ok\":false}\n" || string(build.Stderr) != "usage: engine-sync\n" ||
		build.ExitCode != 1 {
		t.Fatalf("build = %+v, %v; want the reply, the usage line and exit 1", build, err)
	}
	if got, want := EngineSyncCalls(t, record), []string{"codex build /w --json"}; !slices.Equal(got, want) {
		t.Fatalf("record %s = %q, want %q", record, got, want)
	}
	for suffix, want := range map[string]string{".home": "/h\n", ".marker": "/w/.professor\n", ".disable": "1\n"} {
		raw, err := os.ReadFile(record + suffix)
		if err != nil || string(raw) != want {
			t.Fatalf("record %s%s = %q, %v; want %q", record, suffix, raw, err, want)
		}
	}
	var argvs [][]string
	for _, call := range runner.Calls() {
		argvs = append(argvs, call.Argv)
	}
	want := [][]string{{binary, "version"}, {binary, "codex", "build", "/w", "--json"}}
	if !slices.EqualFunc(argvs, want, slices.Equal) {
		t.Fatalf("runner calls = %q, want %q", argvs, want)
	}
}

// The in-process player of the default engine-sync answers each shape exactly as the script does
// (enginesync_jail_test.go runs the script itself): JSON by verb, a PASS line per mirror command,
// exit 2 on an unknown command; it starts nothing.
func TestDefaultEngineSyncPlayerAnswersEveryShape(t *testing.T) {
	cases := []struct {
		args           []string
		stdout, stderr string
		exit           int
	}{
		{args: []string{"version"}, stdout: "engine-sync 9.0.0-testjail\n"},
		{args: []string{"global", "--home", "/h", "--json"}, stdout: `{"ok":true,"events":[],"error":""}` + "\n"},
		{args: []string{"global", "--home", "/h"}},
		{args: []string{"doctor", "--machine", "--json"}, stdout: `{"ok":true,"rows":[]}` + "\n"},
		{args: []string{"doctor", "--machine"}},
		{
			args:   []string{"codex", "check", "/r", "--json"},
			stdout: `{"ok":true,"problems":[],"rebuildable":[],"warnings":[],"wrote":0,"unchanged":0,"deleted":0,"error":""}` + "\n",
		},
		{args: []string{"codex", "build", "/r"}, stdout: "CODEX BUILD PASS\n"},
		{args: []string{"opencode", "build", "/r"}, stdout: "OPENCODE BUILD PASS wrote=0 unchanged=0 deleted=0\n"},
		{args: []string{"opencode", "doctor", "/r"}, stdout: "OPENCODE DOCTOR PASS\n"},
		{args: []string{"codex"}, stderr: "engine-sync (testjail): unknown command: codex\n", exit: 2},
	}
	for _, testCase := range cases {
		argv := append([]string{"/jail/engine-sync"}, testCase.args...)
		result, err := defaultEngineSyncPlayer{}.Run(context.Background(), argv, deps.RunOptions{})
		if err != nil || string(result.Stdout) != testCase.stdout || string(result.Stderr) != testCase.stderr ||
			result.ExitCode != testCase.exit {
			t.Errorf("player %q = (stdout %q, stderr %q, exit %d, %v), want (%q, %q, %d, nil)", testCase.args,
				result.Stdout, result.Stderr, result.ExitCode, err, testCase.stdout, testCase.stderr, testCase.exit)
		}
	}
}
