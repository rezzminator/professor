package testjail

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// engineSyncVersion is the engine-sync verb the runner answers without a
// record, as the stand-in does.
const engineSyncVersion = "version"

// FakeEngineSyncRunner is FakeEngineSync played in process: ENGINE_SYNC_BIN,
// for this test, names an executable written never to run, and the returned
// runner, handed to the code under test through its package's runner seam,
// answers each call of that binary as the stand-in does and writes the same
// record files beside the returned record; every other argv answers from the
// embedded FakeRunner's scripts. A path that runs the binary itself, past the
// runner, still appends its argv to the record but exits 97, so the escape
// fails the call. The test stays serial (t.Setenv).
func FakeEngineSyncRunner(t *testing.T, reply string, code int) (string, *EngineSyncRunner) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "engine-sync")
	record := filepath.Join(dir, "argv")
	escape := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + record + "'\n" +
		"echo 'engine-sync stand-in ran outside its runner' >&2\nexit 97\n"
	if err := WriteExecutableNeverRun(binary, []byte(escape), 0o755); err != nil {
		t.Fatalf("write the engine-sync stand-in %s: %v", binary, err)
	}
	t.Setenv(envEngineSyncBin, binary)
	runner := &EngineSyncRunner{FakeRunner: &deps.FakeRunner{}, binary: binary, record: record}
	runner.Script(
		[]string{binary, engineSyncVersion},
		deps.RunResult{Stdout: []byte("engine-sync 9.0.0-testjail\n")},
		nil,
	)
	runner.Script(
		[]string{binary},
		deps.RunResult{Stdout: []byte(reply + "\n"), Stderr: []byte("usage: engine-sync\n"), ExitCode: code},
		nil,
	)
	return record, runner
}

// EngineSyncRunner is the runner FakeEngineSyncRunner returns: a FakeRunner
// whose every engine-sync call but `version` first writes FakeEngineSync's
// record files.
type EngineSyncRunner struct {
	*deps.FakeRunner
	binary, record string
}

// Run records an engine-sync call as the stand-in does, then answers from the
// FakeRunner's scripts.
func (runner *EngineSyncRunner) Run(ctx context.Context, argv []string, opts deps.RunOptions) (deps.RunResult, error) {
	if len(argv) > 0 && argv[0] == runner.binary && (len(argv) == 1 || argv[1] != engineSyncVersion) {
		if err := runner.recordCall(argv[1:], opts.Env); err != nil {
			return deps.RunResult{ExitCode: -1}, err
		}
	}
	return runner.FakeRunner.Run(ctx, argv, opts)
}

// recordCall writes what FakeEngineSync's stand-in writes for one call: the
// argv line, the child's HOME, ENGINE_SYNC_WORKBENCH_MARKER and
// ENGINE_SYNC_DISABLE, and a copy of the global file under its home (--home,
// else HOME). A nil env is the process's, as a child inherits it.
func (runner *EngineSyncRunner) recordCall(args, env []string) error {
	if env == nil {
		env = os.Environ()
	}
	lookup := func(name string) string {
		value := ""
		for _, entry := range env {
			if found, ok := strings.CutPrefix(entry, name+"="); ok {
				value = found
			}
		}
		return value
	}
	file, err := os.OpenFile(runner.record, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open the engine-sync record %s: %w", runner.record, err)
	}
	_, writeErr := file.WriteString(strings.Join(args, " ") + "\n")
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("append to the engine-sync record %s: %w", runner.record, err)
	}
	home := lookup("HOME")
	for _, field := range []struct{ suffix, value string }{
		{".home", home},
		{".marker", lookup("ENGINE_SYNC_WORKBENCH_MARKER")},
		{".disable", lookup("ENGINE_SYNC_DISABLE")},
	} {
		if err := os.WriteFile(runner.record+field.suffix, []byte(field.value+"\n"), 0o600); err != nil {
			return fmt.Errorf("write the engine-sync record %s%s: %w", runner.record, field.suffix, err)
		}
	}
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--home" {
			home = args[index+1]
		}
	}
	config := filepath.Join(home, ".claude", "engine-sync.json")
	info, err := os.Stat(config)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	raw, err := os.ReadFile(config)
	if err != nil {
		return fmt.Errorf("read the global file %s for the engine-sync record: %w", config, err)
	}
	if err := os.WriteFile(runner.record+".config", raw, 0o600); err != nil {
		return fmt.Errorf("write the engine-sync record %s.config: %w", runner.record, err)
	}
	return nil
}
