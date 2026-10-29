package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// engineSyncGlobalEngine is an installer over home whose source clone is
// home/blueprint, its store home/.claude and its one Codex home home/.codex.
func engineSyncGlobalEngine(t *testing.T, mode Mode, codexHomes []string) (*engine, *bytes.Buffer, string) {
	t.Helper()
	home := t.TempDir()
	if codexHomes == nil {
		codexHomes = []string{filepath.Join(home, ".codex")}
	}
	var output bytes.Buffer
	return &engine{
		options: Options{
			Home:       home,
			ConfigDir:  filepath.Join(home, ".claude"),
			SourceRepo: filepath.Join(home, "blueprint"),
			CodexHomes: codexHomes,
			Mode:       mode,
			Stdout:     &output,
		},
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		apply:       mode != ModeDryRun,
		stamp:       "fixture",
	}, &output, home
}

const everyEventReply = `{"ok":true,"events":[` +
	`{"verb":"change","message":"link /h/.claude/agents/a.md -> /r/templates/global/agents/a.md"},` +
	`{"verb":"retire","message":"retire /h/.codex/agents/x.toml (retired generated Codex agent)",` +
	`"path":"/h/.codex/agents/x.toml"},` +
	`{"verb":"skip","message":"no Codex accounts configured — agent mirror has nothing to write"},` +
	`{"verb":"ok","message":"Codex global agents compiled=1 linked=1"}],"error":""}`

func TestWireEngineSyncGlobalRendersEveryEvent(t *testing.T) {
	installer, output, home := engineSyncGlobalEngine(t, ModeApply, nil)
	record := testjail.FakeEngineSync(t, everyEventReply, 0)
	if err := installer.wireEngineSyncGlobal(nil); err != nil {
		t.Fatalf("wireEngineSyncGlobal: %v\n%s", err, output.String())
	}
	want := "  change  link /h/.claude/agents/a.md -> /r/templates/global/agents/a.md\n" +
		"  change  retire /h/.codex/agents/x.toml (retired generated Codex agent)\n" +
		"  skip    no Codex accounts configured — agent mirror has nothing to write\n" +
		"  ok      Codex global agents compiled=1 linked=1\n"
	if output.String() != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", output.String(), want)
	}
	if report := installer.report; report.Changed != 2 || report.OK != 1 || report.Skipped != 1 {
		t.Fatalf("report = %+v, want changed=2 ok=1 skipped=1", report)
	}
	if !installer.removedPaths["/h/.codex/agents/x.toml"] {
		t.Fatalf("the retired path was not marked removed: %v", installer.removedPaths)
	}
	wantArgv := "global --home " + home + " --source-repo " + filepath.Join(home, "blueprint") +
		" --claude-config-dir " + filepath.Join(home, ".claude") + " --codex-home " + filepath.Join(home, ".codex") +
		" --json"
	if calls := testjail.EngineSyncCalls(t, record); len(calls) != 1 || calls[0] != wantArgv {
		t.Fatalf("engine-sync calls = %q, want [%q]", calls, wantArgv)
	}
}

func TestWireEngineSyncGlobalArgv(t *testing.T) {
	cases := []struct {
		name       string
		mode       Mode
		codexHomes []string
		gptAgents  bool
		adopt      bool
		want       []string
		absent     []string
	}{
		{
			name: "gpt agents on and adoption ordered", mode: ModeApply, gptAgents: true, adopt: true,
			want: []string{"--adopt", "--capability gpt-agents"},
		},
		{
			name: "neither ordered", mode: ModeApply,
			absent: []string{"--adopt", "--capability"},
		},
		{
			name: "a dry run plans over the staged future commands",
			mode: ModeDryRun,
			want: []string{"--plan", "--source-home "},
		},
		{
			name: "the empty roster", mode: ModeDryRun, codexHomes: []string{},
			want: []string{"--no-codex-home", "--plan"}, absent: []string{"--source-home", "--codex-home"},
		},
		{name: "an uninstall", mode: ModeUninstall, want: []string{"--uninstall"}, absent: []string{"--plan"}},
		{
			name: "an uninstall preview", mode: ModeUninstall, want: []string{"--plan", "--uninstall"},
			absent: []string{"--source-home"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			installer, output, _ := engineSyncGlobalEngine(t, testCase.mode, testCase.codexHomes)
			installer.options.GptAgents, installer.options.AdoptAgents = testCase.gptAgents, testCase.adopt
			if testCase.name == "an uninstall preview" {
				installer.apply = false
			}
			record := testjail.FakeEngineSync(t, `{"ok":true,"events":[],"error":""}`, 0)
			if err := installer.wireEngineSyncGlobal(nil); err != nil {
				t.Fatalf("wireEngineSyncGlobal: %v\n%s", err, output.String())
			}
			calls := testjail.EngineSyncCalls(t, record)
			if len(calls) != 1 {
				t.Fatalf("engine-sync calls = %q, want one", calls)
			}
			for _, want := range testCase.want {
				if !strings.Contains(calls[0], want) {
					t.Fatalf("argv %q lacks %q", calls[0], want)
				}
			}
			for _, absent := range testCase.absent {
				if strings.Contains(calls[0], absent) {
					t.Fatalf("argv %q carries %q", calls[0], absent)
				}
			}
			if _, staged, found := strings.Cut(calls[0], "--source-home "); found {
				staged, _, _ = strings.Cut(staged, " ")
				if _, err := os.Lstat(staged); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("the staged command source %s outlived the plan: %v", staged, err)
				}
			}
		})
	}
}

func TestWireEngineSyncGlobalConflictBlocksTheApplyAndIsAPlanError(t *testing.T) {
	reply := `{"ok":false,"events":[{"verb":"conflict","message":"CONFLICT /h/.codex/prompts/a.md"}],"error":""}`
	for _, mode := range []Mode{ModeApply, ModeDryRun} {
		installer, output, _ := engineSyncGlobalEngine(t, mode, nil)
		testjail.FakeEngineSync(t, reply, 1)
		err := installer.wireEngineSyncGlobal(nil)
		if !strings.Contains(output.String(), "  conflict CONFLICT /h/.codex/prompts/a.md\n") {
			t.Fatalf("mode %d transcript lacks the conflict:\n%s", mode, output.String())
		}
		want := "engine-sync global: CONFLICT /h/.codex/prompts/a.md"
		if mode == ModeApply && (err == nil || err.Error() != want) {
			t.Fatalf("apply error = %v, want %q", err, want)
		}
		if mode == ModeDryRun &&
			(err != nil || len(installer.planErrors) != 1 || installer.planErrors[0].Error() != want) {
			t.Fatalf("dry run error = %v planErrors = %v, want one plan error %q", err, installer.planErrors, want)
		}
	}
}

func TestWireEngineSyncGlobalWithoutTheBinaryFailsTheStep(t *testing.T) {
	t.Setenv("ENGINE_SYNC_BIN", "")
	t.Setenv("ENGINE_SYNC_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	for _, name := range []string{"Codex home configured", "clone present with empty Codex roster"} {
		for _, mode := range []Mode{ModeApply, ModeDryRun} {
			t.Run(fmt.Sprintf("%s mode-%d", name, mode), func(t *testing.T) {
				installer, _, home := engineSyncGlobalEngine(t, mode, nil)
				if strings.HasPrefix(name, "clone present") {
					installer.options.CodexHomes = []string{}
					if err := os.MkdirAll(installer.options.SourceRepo, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				err := installer.wireEngineSyncGlobal(nil)
				stable := enginesync.StablePath(installer.env(), home)
				if err == nil || !strings.HasPrefix(err.Error(), "engine-sync binary unavailable: "+stable+": ") {
					t.Fatalf("wireEngineSyncGlobal = %v, want the unavailable binary at %s", err, stable)
				}
				var unavailable *enginesync.UnavailableError
				if !errors.As(err, &unavailable) {
					t.Fatalf("wireEngineSyncGlobal = %v, want it to wrap the UnavailableError", err)
				}
				for _, repair := range []string{
					"claude plugin install engine-sync@professor",
					"start a new Claude session",
					"pfm update",
					"pfm install --yes",
				} {
					if !strings.Contains(err.Error(), repair) {
						t.Fatalf("error %q does not name the repair step %q", err, repair)
					}
				}
			})
		}
	}
}

// The adopter reads the repair in the preflight refusal that stops the install
// before any step applies, not only in the step's own error.
func TestInstallPreflightWithoutEngineSyncNamesTheRepair(t *testing.T) {
	withoutEngineSync(t)
	home := t.TempDir()
	clone := filepath.Join(home, ".professor")
	if err := os.MkdirAll(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, SourceRepo: clone,
		Runner: &fakeRunner{}, Stdout: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "preflight apply plan") ||
		!strings.Contains(err.Error(), "claude plugin install engine-sync@professor") ||
		!strings.Contains(err.Error(), "start a new Claude session") {
		t.Fatalf("Run = %v, want the preflight refusal naming the engine-sync repair\n%s", err, output.String())
	}
}

func TestBinaryOnlyInstallWithoutEngineSync(t *testing.T) {
	t.Setenv("ENGINE_SYNC_BIN", "")
	t.Setenv("ENGINE_SYNC_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	for _, mode := range []Mode{ModeDryRun, ModeApply} {
		t.Run(fmt.Sprintf("mode-%d", mode), func(t *testing.T) {
			installer, output, home := engineSyncGlobalEngine(t, mode, []string{})
			installer.options.SourceRepo = ""
			stable := enginesync.StablePath(installer.env(), home)
			if err := installer.wireEngineSyncGlobal(nil); err != nil {
				t.Fatalf("binary-only wireEngineSyncGlobal: %v\n%s", err, output.String())
			}
			if strings.Count(output.String(), "  skip    ") != 1 ||
				!strings.Contains(output.String(), "  skip    engine-sync binary unavailable: "+stable+": ") {
				t.Fatalf("want one visible missing-binary skip at %s:\n%s", stable, output.String())
			}
		})
	}
	t.Run("preview", func(t *testing.T) {
		installer, output, _ := engineSyncGlobalEngine(t, ModeDryRun, []string{})
		installer.options.SourceRepo = ""
		installer.options.MCPConfigPath = testConfigPath(t)
		installer.options.Runner = &fakeRunner{}
		if _, err := Run(context.Background(), installer.options); err != nil {
			t.Fatalf("binary-only preview: %v\n%s", err, output.String())
		}
	})
}

// pfm install and uninstall reach engine-sync global: the preflight plans,
// the apply applies, and its events land in the install's own transcript.
func TestInstallAndUninstallRunEngineSyncGlobal(t *testing.T) {
	home := t.TempDir()
	record := testjail.FakeEngineSync(t, everyEventReply, 0)
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		var output bytes.Buffer
		if _, err := Run(context.Background(), Options{
			MCPConfigPath: testConfigPath(t),
			Mode:          mode, Home: home, Stdout: &output, Runner: &fakeRunner{},
		}); err != nil {
			t.Fatalf("mode %d: %v\n%s", mode, err, output.String())
		}
		if !strings.Contains(output.String(), "  ok      Codex global agents compiled=1 linked=1\n") {
			t.Fatalf("mode %d transcript lacks engine-sync's events:\n%s", mode, output.String())
		}
	}
	var planned, applied, uninstalled bool
	for _, call := range testjail.EngineSyncCalls(t, record) {
		if !strings.HasPrefix(call, "global --home "+home+" ") {
			t.Fatalf("unexpected engine-sync call %q", call)
		}
		switch {
		case strings.Contains(call, " --uninstall"):
			uninstalled = true
		case strings.Contains(call, " --plan"):
			planned = true
		default:
			applied = true
		}
	}
	if !planned || !applied || !uninstalled {
		t.Fatalf("planned=%t applied=%t uninstalled=%t, want all three", planned, applied, uninstalled)
	}
}

// An install writes the global file before it runs engine-sync global, which
// unions the file's capabilities with its flags: an opt-out never leaves the
// previous capability in force for that run, nor for its preview, which runs
// is skipped by name.
func TestInstallWritesTheGlobalFileBeforeEngineSyncGlobal(t *testing.T) {
	for name, mode := range map[string]Mode{"apply": ModeApply, "preview": ModeDryRun} {
		t.Run(name, func(t *testing.T) { installRunsAgainstTheCapabilitiesItWrites(t, mode) })
	}
}

func installRunsAgainstTheCapabilitiesItWrites(t *testing.T, mode Mode) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	clone := filepath.Join(home, ".professor")
	writeFixture(t, engineSyncPolicySource(clone), engineSyncTemplate)
	writeFixture(
		t,
		enginesync.GlobalConfigPath(home),
		`{"version":1,"machine":{"managedBy":"professor"},"targets":{"codexHomes":[],"capabilities":["gpt-agents"]}}`+"\n",
	)
	record := testjail.FakeEngineSync(t, everyEventReply, 0)
	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), SourceRepo: clone,
		Mode: mode, Home: home, Stdout: &output, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatalf("install: %v\n%s", err, output.String())
	}
	if mode == ModeDryRun {
		if _, err := os.Stat(record); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("the preview ran engine-sync over the stale file: %v", err)
		}
		if !strings.Contains(output.String(), "engine-sync global preview skipped: this install would write ") {
			t.Fatalf("the preview did not name its skip:\n%s", output.String())
		}
		return
	}
	seen, err := os.ReadFile(record + ".config")
	if err != nil {
		t.Fatalf("engine-sync global saw no global file: %v", err)
	}
	if strings.Contains(string(seen), "gpt-agents") {
		t.Fatalf("engine-sync global ran against the stale capability list:\n%s", seen)
	}
}

func withoutEngineSync(t *testing.T) {
	t.Helper()
	t.Setenv("ENGINE_SYNC_BIN", "")
	t.Setenv("ENGINE_SYNC_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
}

// An uninstall preview with no engine-sync binary names what it will leave
// and raises no plan error, so the preflight does not refuse the teardown.
func TestWireEngineSyncGlobalUninstallPreviewWithoutTheBinarySkips(t *testing.T) {
	installer, output, _ := engineSyncGlobalEngine(t, ModeUninstall, nil)
	installer.apply = false
	writeFixture(
		t,
		engineSyncPolicySource(installer.options.SourceRepo),
		`{"version":1}`,
	)
	withoutEngineSync(t)
	if err := installer.wireEngineSyncGlobal(nil); err != nil {
		t.Fatalf("wireEngineSyncGlobal: %v", err)
	}
	if len(installer.planErrors) != 0 || len(installer.deferred) != 0 {
		t.Fatalf("planErrors=%v deferred=%v, want none", installer.planErrors, installer.deferred)
	}
	if want := "  skip    " + engineSyncAbsentText + "\n"; output.String() != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", output.String(), want)
	}
	if !installer.engineSyncAbsent {
		t.Fatal("the absent binary was not remembered for the global file step")
	}
}

// engineSyncFault points ENGINE_SYNC_BIN at an engine-sync that fails the
// named way (not one that is absent) and returns the text the fault carries.
func engineSyncFault(t *testing.T, fault string) string {
	t.Helper()
	switch fault {
	case "too old":
		t.Setenv("ENGINE_SYNC_BIN", writeScript(t, t.TempDir(), "engine-sync", "#!/bin/sh\necho 'engine-sync 0.0.1'\n"))
		return "older than required"
	case "run error":
		testjail.FakeEngineSync(t, `{"ok":false,"events":[],"error":"boom"}`, 1)
		return "boom"
	case "conflict":
		testjail.FakeEngineSync(t,
			`{"ok":false,"events":[{"verb":"conflict","message":"CONFLICT /h/.codex/prompts/a.md"}],"error":""}`, 1)
		return "CONFLICT /h/.codex/prompts/a.md"
	}
	t.Fatalf("unknown engine-sync fault %q", fault)
	return ""
}

// An uninstall whose engine-sync is present but faulty (too old, a failing
// global run, a conflict) is not a preflight refusal: the preview skips with
// the fault's text and raises no plan error, and the apply defers the failure
// so every unwire step still runs and the run exits non-zero naming it.
func TestWireEngineSyncGlobalUninstallFaultPreviewSkipsAndApplyDefers(t *testing.T) {
	for _, fault := range []string{"too old", "run error", "conflict"} {
		for _, apply := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s apply=%v", fault, apply), func(t *testing.T) {
				installer, output, _ := engineSyncGlobalEngine(t, ModeUninstall, nil)
				installer.apply = apply
				writeFixture(
					t,
					engineSyncPolicySource(installer.options.SourceRepo),
					`{"version":1}`,
				)
				want := engineSyncFault(t, fault)
				if err := installer.wireEngineSyncGlobal(nil); err != nil {
					t.Fatalf("wireEngineSyncGlobal: %v\n%s", err, output.String())
				}
				if len(installer.planErrors) != 0 {
					t.Fatalf("planErrors = %v, want none", installer.planErrors)
				}
				if !apply {
					if len(installer.deferred) != 0 {
						t.Fatalf(
							"the preview deferred %v: the preflight would refuse the uninstall",
							installer.deferred,
						)
					}
					if !regexp.MustCompile(`(?m)^  skip    .*` + regexp.QuoteMeta(want)).MatchString(output.String()) {
						t.Fatalf("the preview transcript has no skip naming %q:\n%s", want, output.String())
					}
					return
				}
				if len(installer.deferred) != 1 || !strings.Contains(installer.deferred[0].Error(), want) {
					t.Fatalf("apply deferred = %v, want one failure naming %q", installer.deferred, want)
				}
			})
		}
	}
}

// A faulty engine-sync on a whole uninstall run: the preflight lets it
// through, every unwire step runs, and the run's error names the fault.
func TestUninstallEngineSyncFaultIsNotAPreflightRefusal(t *testing.T) {
	home := t.TempDir()
	runner := &fakeRunner{manager: true}
	writeFixture(t, engineSyncPolicySource(filepath.Join(home, ".professor")), engineSyncTemplate)
	testjail.FakeEngineSync(t, `{"ok":true,"events":[],"error":""}`, 0)
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, Runner: runner,
	}); err != nil {
		t.Fatal(err)
	}
	globalFile := enginesync.GlobalConfigPath(home)
	if _, err := os.Lstat(globalFile); err != nil {
		t.Fatalf("install wrote no %s: %v", globalFile, err)
	}
	want := engineSyncFault(t, "run error")
	runner.calls = nil
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeUninstall, Home: home, Runner: runner,
	})
	if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "preflight") {
		t.Fatalf("uninstall error = %v, want the engine-sync fault %q and no preflight refusal", err, want)
	}
	if _, statErr := os.Lstat(globalFile); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("uninstall left %s after the engine-sync fault: %v", globalFile, statErr)
	}
	teardown := "disable --now"
	if schedulerIsLaunchd {
		teardown = "launchctl bootout"
	}
	if joined := strings.Join(runner.calls, "\n"); !strings.Contains(joined, teardown) {
		t.Fatalf("the unit teardown (%q) did not run after the engine-sync fault:\n%s", teardown, joined)
	}
}

// hungEngineSync answers the version probe and then blocks `global` until its
// context ends, as a wedged engine-sync does.
type hungEngineSync struct{ deps.Runner }

func (hungEngineSync) Run(ctx context.Context, argv []string, _ deps.RunOptions) (deps.RunResult, error) {
	if len(argv) > 1 && argv[1] == "version" {
		return deps.RunResult{Stdout: []byte("engine-sync 9.0.0\n")}, nil
	}
	<-ctx.Done()
	return deps.RunResult{ExitCode: -1}, ctx.Err()
}

// engine-sync global is bounded: a hung run ends at the deadline with an error
// naming engine-sync and that deadline, never a silent hang. The deadline is
// injected, so the test does not wait the real five minutes.
func TestWireEngineSyncGlobalHasADeadline(t *testing.T) {
	testjail.FakeEngineSync(t, `{"ok":true,"events":[],"error":""}`, 0)
	for _, mode := range []Mode{ModeApply, ModeDryRun} {
		t.Run(fmt.Sprintf("mode-%d", mode), func(t *testing.T) {
			installer, output, _ := engineSyncGlobalEngine(t, mode, nil)
			installer.engineSyncExec = hungEngineSync{}
			installer.engineSyncDeadline = 50 * time.Millisecond
			err := installer.wireEngineSyncGlobal(nil)
			if err == nil || !strings.Contains(err.Error(), "engine-sync global") ||
				!strings.Contains(err.Error(), "timed out after 50ms") {
				t.Fatalf(
					"wireEngineSyncGlobal = %v, want engine-sync global timed out after 50ms\n%s",
					err,
					output.String(),
				)
			}
		})
	}
	fresh, _, _ := engineSyncGlobalEngine(t, ModeApply, nil)
	if got := fresh.engineSyncGlobalDeadline(); got != 5*time.Minute {
		t.Fatalf("default engine-sync global deadline = %s, want 5m0s", got)
	}
}

// An absent binary on an uninstall apply is a deferred failure, never a
// return: pfm tears down everything it owns, then exits non-zero naming it.
func TestUninstallWithoutEngineSyncStillTearsDownEverythingPfmOwns(t *testing.T) {
	home := t.TempDir()
	runner := &fakeRunner{manager: true}
	writeFixture(t, engineSyncPolicySource(filepath.Join(home, ".professor")), engineSyncTemplate)
	testjail.FakeEngineSync(t, `{"ok":true,"events":[],"error":""}`, 0)
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, Runner: runner,
	}); err != nil {
		t.Fatal(err)
	}
	globalFile := enginesync.GlobalConfigPath(home)
	if _, err := os.Lstat(globalFile); err != nil {
		t.Fatalf("install wrote no %s: %v", globalFile, err)
	}
	command := filepath.Join(home, ".claude", "commands", "reload.md")
	if _, err := os.Lstat(command); err != nil {
		t.Fatalf("install wired no command %s: %v", command, err)
	}
	withoutEngineSync(t)
	runner.calls = nil
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeUninstall, Home: home, Stdout: &output, Runner: runner,
	})
	if err == nil || !strings.Contains(err.Error(), engineSyncAbsentText) ||
		!strings.Contains(err.Error(), "engine-sync binary unavailable: ") ||
		strings.Contains(err.Error(), "preflight") {
		t.Fatalf("uninstall error = %v, want the deferred %q", err, engineSyncAbsentText)
	}
	if _, statErr := os.Lstat(command); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("uninstall left the global command %s: %v", command, statErr)
	}
	if _, statErr := os.Lstat(globalFile); statErr != nil {
		t.Fatalf("uninstall removed %s though engine-sync was absent: %v", globalFile, statErr)
	}
	teardown := "disable --now"
	if schedulerIsLaunchd {
		teardown = "launchctl bootout"
	}
	if joined := strings.Join(runner.calls, "\n"); !strings.Contains(joined, teardown) {
		t.Fatalf(
			"the unit teardown (%q) did not run after the engine-sync step:\n%s\n%s",
			teardown,
			joined,
			output.String(),
		)
	}
	if !strings.Contains(output.String(), "summary ") {
		t.Fatalf("the uninstall did not finish:\n%s", output.String())
	}
}

// An engine-sync step that fails outright (here the old config directory
// cannot be inspected) is deferred as install defers it: the uninstall
// still unwires the scheduler and the global file, and the error comes
// back at the end. The uninstall runs without the preflight, which would have
// refused the same failure before touching anything.
func TestUninstallEngineSyncFailureStillUnwiresTheSchedulerAndTargets(t *testing.T) {
	home := t.TempDir()
	runner := &fakeRunner{manager: true}
	writeFixture(t, engineSyncPolicySource(filepath.Join(home, ".professor")), engineSyncTemplate)
	testjail.FakeEngineSync(t, `{"ok":true,"events":[],"error":""}`, 0)
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, Runner: runner,
	}); err != nil {
		t.Fatal(err)
	}
	globalFile := enginesync.GlobalConfigPath(home)
	if _, err := os.Lstat(globalFile); err != nil {
		t.Fatalf("install wrote no %s: %v", globalFile, err)
	}
	writeFixture(t, filepath.Join(home, ".config", "engine-sync"), "not a directory\n")
	options, err := normalizeInstallerOptions(Options{
		MCPConfigPath: testConfigPath(t), Mode: ModeUninstall, Home: home, Runner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	options.Stdout = &output
	installer := &engine{
		options: options, apply: true, stamp: "fixture", managedRoot: managedRootForHome(options.Home),
	}
	runner.calls = nil
	failure := errors.Join(installer.uninstall(context.Background()), errors.Join(installer.deferred...))
	if failure == nil || !strings.Contains(failure.Error(), "inspect engine-sync policy") {
		t.Fatalf("uninstall error = %v, want the engine-sync failure\n%s", failure, output.String())
	}
	if _, statErr := os.Lstat(globalFile); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("uninstall left %s after the engine-sync failure: %v", globalFile, statErr)
	}
	teardown := "disable --now"
	if schedulerIsLaunchd {
		teardown = "launchctl bootout"
	}
	if joined := strings.Join(runner.calls, "\n"); !strings.Contains(joined, teardown) {
		t.Fatalf("the unit teardown (%q) did not run after the engine-sync failure:\n%s\n%s",
			teardown, joined, output.String())
	}
}

func TestEngineSyncGlobalRequiresCompatibleVersion(t *testing.T) {
	// The first engine-sync reading the one global file is required; older builds read the retired config pair.
	for _, version := range []string{"0.1.0", "0.3.0-alpha.4", "0.3.9", "0.4.0-alpha", "0.4.0-alpha.1"} {
		t.Run(version, func(t *testing.T) {
			installer, _, _ := engineSyncGlobalEngine(t, ModeApply, nil)
			binary := filepath.Join(t.TempDir(), "engine-sync")
			script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'engine-sync " + version + "'; else echo '{\"ok\":true,\"events\":[]}'; fi\n"
			if err := testjail.WriteExecutable(binary, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv(enginesync.EnvBin, binary)
			err := installer.wireEngineSyncGlobal(nil)
			if strings.HasPrefix(version, "0.4.") {
				if err != nil {
					t.Fatalf("compatible binary: %v", err)
				}
				return
			}
			if err == nil ||
				!strings.Contains(
					err.Error(),
					"engine-sync "+version+" is older than required 0.4.0-alpha — update the plugin "+
						"(`claude plugin update engine-sync@professor`), then start a new Claude session",
				) || strings.Contains(err.Error(), "make host-install") {
				t.Fatalf("old binary = %v", err)
			}
		})
	}
}
