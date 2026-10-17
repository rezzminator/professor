package enginesync

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func fakeRunner(t *testing.T, stdout, stderr string, code int) (Runner, string) {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "engine-sync")
	fakeBinary(t, binary, stdout, stderr, code)
	return Runner{Env: &paths.MapEnv{Values: map[string]string{EnvBin: binary}}, Home: "/h"}, binary
}

func TestMirror(t *testing.T) {
	cases := []struct {
		name    string
		engine  string
		action  string
		stdout  string
		code    int
		want    MirrorResult
		wantErr string
	}{
		{
			name:   "a clean check",
			engine: EngineCodex,
			action: ActionCheck,
			stdout: `{"ok":true,"problems":[],"rebuildable":[],"warnings":[],"wrote":0,"unchanged":0,"deleted":0,"error":""}`,
			want:   MirrorResult{OK: true, Problems: []string{}, Rebuildable: []string{}, Warnings: []string{}},
		},
		{
			name: "findings keep the rebuildable multiset", engine: EngineOpenCode, action: ActionCheck, code: 1,
			stdout: `{"ok":false,"problems":["STALE a","STALE a","CONFLICT b"],"rebuildable":["STALE a","STALE a"],` +
				`"warnings":["w"],"wrote":0,"unchanged":0,"deleted":0,"error":""}`,
			want: MirrorResult{
				Problems: []string{"STALE a", "STALE a", "CONFLICT b"}, Rebuildable: []string{"STALE a", "STALE a"},
				Warnings: []string{"w"},
			},
		},
		{
			name:   "a build counts",
			engine: EngineOpenCode,
			action: ActionBuild,
			stdout: `{"ok":true,"problems":[],"rebuildable":[],"warnings":[],"wrote":3,"unchanged":2,"deleted":1,"error":""}`,
			want: MirrorResult{
				OK:          true,
				Problems:    []string{},
				Rebuildable: []string{},
				Warnings:    []string{},
				Wrote:       3,
				Unchanged:   2,
				Deleted:     1,
			},
		},
		{
			name: "a run failure is an error", engine: EngineCodex, action: ActionBuild, code: 1,
			stdout:  `{"ok":false,"problems":[],"rebuildable":[],"warnings":[],"error":"read /r: permission denied"}`,
			wantErr: "engine-sync codex build /r: read /r: permission denied",
		},
		{
			name: "a usage error is an error", engine: EngineCodex, action: ActionCheck, code: 2,
			wantErr: "exit 2: usage",
		},
		{
			name: "a reply that is not JSON is an error", engine: EngineCodex, action: ActionCheck,
			stdout: "CODEX CHECK PASS\n", wantErr: "decode reply",
		},
		{
			name: "an exit that disagrees with ok is an error", engine: EngineCodex, action: ActionCheck, code: 1,
			stdout: `{"ok":true}`, wantErr: "exit 1 disagrees with its reply's ok=true",
		},
		{
			name:    "a failing reply that names no problem is an error",
			engine:  EngineCodex,
			action:  ActionCheck,
			code:    1,
			stdout:  `{"ok":false,"problems":[],"rebuildable":[],"warnings":[],"error":""}`,
			wantErr: "fails naming no problem",
		},
		{
			name: "a passing reply with problems is an error", engine: EngineOpenCode, action: ActionCheck,
			stdout:  `{"ok":true,"problems":["STALE a"],"rebuildable":["STALE a"],"warnings":[],"error":""}`,
			wantErr: "passes with 1 problem(s)",
		},
		{
			name:   "a rebuildable line beyond the problems is an error",
			engine: EngineCodex,
			action: ActionCheck,
			code:   1,
			stdout: `{"ok":false,"problems":["STALE a","CONFLICT b"],"rebuildable":["STALE a","STALE a"],` +
				`"warnings":[],"error":""}`,
			wantErr: `rebuildable "STALE a" is not among its problems`,
		},
		{
			name:    "an unknown engine is refused",
			engine:  "claude",
			action:  ActionCheck,
			wantErr: `unknown engine "claude"`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner, binary := fakeRunner(t, testCase.stdout, "usage\n", testCase.code)
			got, err := runner.Mirror(context.Background(), testCase.engine, testCase.action, "/r")
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("Mirror error = %v, want it to contain %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Mirror: %v", err)
			}
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("Mirror = %#v, want %#v", got, testCase.want)
			}
			want := []string{testCase.engine, testCase.action, "/r", "--home", "/h", "--json"}
			if argv := recordedArgv(t, binary); !reflect.DeepEqual(argv, want) {
				t.Fatalf("argv = %q, want %q", argv, want)
			}
		})
	}
}

func TestMirrorPassesEveryHostCapability(t *testing.T) {
	runner, binary := fakeRunner(t, `{"ok":true,"problems":[],"rebuildable":[],"warnings":[]}`, "", 0)
	runner.Capabilities = []string{CapabilityGptAgents, "other"}
	if _, err := runner.Mirror(context.Background(), EngineCodex, ActionBuild, "/r"); err != nil {
		t.Fatalf("Mirror: %v", err)
	}
	want := []string{
		EngineCodex, ActionBuild, "/r", "--home", "/h",
		"--capability", CapabilityGptAgents, "--capability", "other", "--json",
	}
	if argv := recordedArgv(t, binary); !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
}

func TestHostCapabilities(t *testing.T) {
	if got := HostCapabilities(true); !reflect.DeepEqual(got, []string{"gpt-agents"}) {
		t.Fatalf("HostCapabilities(true) = %q, want [gpt-agents]", got)
	}
	if got := HostCapabilities(false); len(got) != 0 {
		t.Fatalf("HostCapabilities(false) = %q, want none", got)
	}
}

func TestGlobalArgs(t *testing.T) {
	cases := []struct {
		name         string
		options      GlobalOptions
		capabilities []string
		want         []string
	}{
		{
			name:         "the adoption order and the host capability",
			options:      GlobalOptions{CodexHomes: []string{"/h/.codex"}, Adopt: true},
			capabilities: []string{"gpt-agents"},
			want: []string{
				"global",
				"--home",
				"/h",
				"--codex-home",
				"/h/.codex",
				"--adopt",
				"--capability",
				"gpt-agents",
				"--json",
			},
		},
		{
			name: "an apply over two Codex homes",
			options: GlobalOptions{
				SourceRepo: "/h/.professor", ClaudeConfigDirs: []string{"/h/.claude"},
				CodexHomes: []string{"/h/.codex", "/h/.codex-2"},
			},
			want: []string{
				"global", "--home", "/h", "--source-repo", "/h/.professor", "--claude-config-dir", "/h/.claude",
				"--codex-home", "/h/.codex", "--codex-home", "/h/.codex-2", "--json",
			},
		},
		{
			name: "a plan with the empty roster and a staged source home",
			options: GlobalOptions{
				SourceRepo: "/r", ClaudeConfigDirs: []string{"/c"}, SourceHome: "/tmp/plan", Plan: true,
			},
			want: []string{
				"global", "--home", "/h", "--source-repo", "/r", "--claude-config-dir", "/c", "--no-codex-home",
				"--source-home", "/tmp/plan", "--plan", "--json",
			},
		},
		{
			name:    "an uninstall",
			options: GlobalOptions{CodexHomes: []string{"/h/.codex"}, Uninstall: true},
			want:    []string{"global", "--home", "/h", "--codex-home", "/h/.codex", "--uninstall", "--json"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := Runner{Home: "/h", Capabilities: testCase.capabilities}.GlobalArgs(testCase.options)
			if err != nil || !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("GlobalArgs = %q, %v; want %q", got, err, testCase.want)
			}
		})
	}
}

func TestGlobal(t *testing.T) {
	cases := []struct {
		name    string
		stdout  string
		code    int
		want    GlobalResult
		wantErr string
	}{
		{
			name: "every verb",
			stdout: `{"ok":true,"events":[{"verb":"change","message":"link a -> b"},` +
				`{"verb":"retire","message":"retire /h/.codex/agents/x.toml (r)","path":"/h/.codex/agents/x.toml"},` +
				`{"verb":"skip","message":"s"},{"verb":"ok","message":"Codex global agents compiled=1 linked=1"}],"error":""}`,
			want: GlobalResult{OK: true, Events: []Event{
				{Verb: VerbChange, Message: "link a -> b"},
				{Verb: VerbRetire, Message: "retire /h/.codex/agents/x.toml (r)", Path: "/h/.codex/agents/x.toml"},
				{Verb: VerbSkip, Message: "s"},
				{Verb: VerbOK, Message: "Codex global agents compiled=1 linked=1"},
			}},
		},
		{
			name:   "a conflict is a result, not an error",
			code:   1,
			stdout: `{"ok":false,"events":[{"verb":"conflict","message":"CONFLICT /h/.codex/prompts/a.md"}],"error":""}`,
			want:   GlobalResult{Events: []Event{{Verb: VerbConflict, Message: "CONFLICT /h/.codex/prompts/a.md"}}},
		},
		{
			name: "a run failure is an error", code: 1,
			stdout:  `{"ok":false,"events":[],"error":"read /r/templates/global/agents: permission denied"}`,
			wantErr: "engine-sync global: read /r/templates/global/agents: permission denied",
		},
		{
			name: "an unknown verb is an error", stdout: `{"ok":true,"events":[{"verb":"wrote","message":"m"}]}`,
			wantErr: `unknown verb "wrote"`,
		},
		{
			name:    "a retire without a path is an error",
			stdout:  `{"ok":true,"events":[{"verb":"retire","message":"m"}]}`,
			wantErr: "names no path",
		},
		{
			name:    "a path off a retire is an error",
			stdout:  `{"ok":true,"events":[{"verb":"change","message":"m","path":"/p"}]}`,
			wantErr: "set on retire only",
		},
		{name: "a usage error is an error", code: 2, wantErr: "exit 2"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner, _ := fakeRunner(t, testCase.stdout, "", testCase.code)
			got, err := runner.Global(context.Background(), GlobalOptions{CodexHomes: []string{"/h/.codex"}})
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("Global error = %v, want it to contain %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("Global = %#v, %v; want %#v", got, err, testCase.want)
			}
		})
	}
}

func TestMachineDoctor(t *testing.T) {
	cases := []struct {
		name         string
		options      MachineDoctorOptions
		capabilities []string
		stdout       string
		code         int
		want         []Row
		argv         []string
		wantErr      string
	}{
		{
			name:         "the host capability reaches the doctor",
			options:      MachineDoctorOptions{ClaudeAbsent: true},
			capabilities: []string{"gpt-agents"},
			stdout:       `{"ok":true,"rows":[{"level":"ok","message":"SKIPPED sol — requires gpt-agents"}]}`,
			want:         []Row{{Level: LevelOK, Message: "SKIPPED sol — requires gpt-agents"}},
			argv: []string{
				"doctor", "--machine", "--home", "/h", "--claude-absent", "--capability", "gpt-agents", "--json",
			},
		},
		{
			name: "a failing row",
			options: MachineDoctorOptions{
				SourceRepo: "/h/.professor", ClaudeConfigDir: "/h/.claude", CodexHomes: []string{"/h/.codex"},
			},
			code: 1,
			stdout: `{"ok":false,"rows":[{"level":"ok","message":"global-agents store dir=/h/.claude state=linked"},` +
				`{"level":"fail","message":"global-agents source=/h/.codex/agents state=MISSING names=a"}]}`,
			want: []Row{
				{Level: LevelOK, Message: "global-agents store dir=/h/.claude state=linked"},
				{Level: LevelFail, Message: "global-agents source=/h/.codex/agents state=MISSING names=a"},
			},
			argv: []string{
				"doctor",
				"--machine",
				"--home",
				"/h",
				"--source-repo",
				"/h/.professor",
				"--claude-config-dir",
				"/h/.claude",
				"--codex-home",
				"/h/.codex",
				"--json",
			},
		},
		{
			name:    "the default source with Claude absent",
			options: MachineDoctorOptions{ClaudeAbsent: true},
			stdout:  `{"ok":true,"rows":[{"level":"warn","message":"global-agents source=/h/.claude/agents state=NO-SOURCES"}]}`,
			want:    []Row{{Level: LevelWarn, Message: "global-agents source=/h/.claude/agents state=NO-SOURCES"}},
			argv:    []string{"doctor", "--machine", "--home", "/h", "--claude-absent", "--json"},
		},
		{
			name:    "a configuration error retains its file and key",
			code:    1,
			stdout:  `{"ok":false,"rows":[],"configSources":[],"error":"/h/.claude/engine-sync.json: machine.modelMap is removed; use codex.modelMap"}`,
			wantErr: "/h/.claude/engine-sync.json: machine.modelMap is removed; use codex.modelMap",
		},
		{
			name: "an unknown level is an error", stdout: `{"ok":true,"rows":[{"level":"info","message":"m"}]}`,
			wantErr: `unknown level "info"`,
		},
		{
			name: "ok over a failing row is an error", stdout: `{"ok":true,"rows":[{"level":"fail","message":"m"}]}`,
			wantErr: "disagrees with its rows",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner, binary := fakeRunner(t, testCase.stdout, "", testCase.code)
			runner.Capabilities = testCase.capabilities
			got, err := runner.MachineDoctor(context.Background(), testCase.options)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("MachineDoctor error = %v, want it to contain %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("MachineDoctor = %#v, %v; want %#v", got, err, testCase.want)
			}
			if argv := recordedArgv(t, binary); !reflect.DeepEqual(argv, testCase.argv) {
				t.Fatalf("argv = %q, want %q", argv, testCase.argv)
			}
		})
	}
}
