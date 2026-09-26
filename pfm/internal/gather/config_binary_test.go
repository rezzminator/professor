package gather

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfiguredBinaryBasenamesReachLiveDetectors(t *testing.T) {
	customClaude := "/opt/tools/claude enterprise"
	customCodex := "/opt/tools/codex safe"
	claudeSession := "11111111-1111-4111-8111-111111111111"
	codexHome := t.TempDir()
	rollout := filepath.Join(codexHome, "sessions", "2026", "rollout-configured.jsonl")
	writeRolloutMeta(t, rollout, "user", "")
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		101: {stat: ProcStat{ParentPID: 1}},
		200: {
			cmdline: []string{customClaude, "--session-id", claudeSession},
			environ: map[string]string{
				"CLAUDE_CONFIG_DIR":        "/jail/home/account-42",
				"ENABLE_PROMPT_CACHING_1H": "1",
			},
			stat: ProcStat{ParentPID: 100, StartTime: 42},
		},
		300: {
			cmdline: []string{customCodex},
			fdLinks: []FDLink{{FD: 3, Target: rollout}},
			stat:    ProcStat{ParentPID: 101},
		},
	}}
	panes := []ProbePane{
		{Socket: "cc-configured", PaneID: "%1", PID: 100, TTY: "/dev/ttys001"},
		{Socket: "cx-configured", PaneID: "%2", PID: 101},
	}

	if !IsClaudeCommand([]string{customClaude}, customClaude) {
		t.Fatal("IsClaudeCommand rejected the configured basename")
	}
	if !IsCodexCommand([]string{customCodex}, customCodex) {
		t.Fatal("IsCodexCommand rejected the configured basename")
	}

	claudeProcesses, err := DetectClaudeProcesses(proc, panes, customClaude)
	if err != nil {
		t.Fatalf("DetectClaudeProcesses() error = %v", err)
	}
	wantClaude := []ClaudeProcess{{
		PID: 200, PanePID: 100, Socket: "cc-configured", PaneID: "%1", TTY: "/dev/ttys001",
		ConfigDir: "/jail/home/account-42",
	}}
	if !reflect.DeepEqual(claudeProcesses, wantClaude) {
		t.Fatalf("DetectClaudeProcesses() = %#v, want %#v", claudeProcesses, wantClaude)
	}

	agents, _, err := DetectAgents(proc, "/jail/home", panes, customClaude)
	if err != nil {
		t.Fatalf("DetectAgents() error = %v", err)
	}
	if len(agents) != 1 || agents[0].SessionID != claudeSession || agents[0].ConfigDir != "/jail/home/account-42" {
		t.Fatalf("DetectAgents() = %#v", agents)
	}

	codex, err := DetectCodexThreads(proc, codexHome, panes, nil, customCodex)
	if err != nil {
		t.Fatalf("DetectCodexThreads() error = %v", err)
	}
	wantCodex := []LiveCodex{{
		PID: 300, PanePID: 101, Socket: "cx-configured", PaneID: "%2",
		RolloutPath: rollout, ThreadID: "configured", RolloutHeld: true,
	}}
	if !reflect.DeepEqual(codex, wantCodex) {
		t.Fatalf("DetectCodexThreads() = %#v, want %#v", codex, wantCodex)
	}
}

func TestClaudeProcessUnreadableEnvironmentStaysUnknown(t *testing.T) {
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: {stat: ProcStat{ParentPID: 1}},
		200: {
			cmdline: []string{"/opt/claude"}, environErr: true,
			stat: ProcStat{ParentPID: 100},
		},
	}}
	panes := []ProbePane{{Socket: "cc-unreadable", PaneID: "%1", PID: 100}}
	processes, err := DetectClaudeProcesses(proc, panes)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != 1 || !processes[0].ConfigUnreadable {
		t.Fatalf("processes = %#v, want unreadable environment", processes)
	}
}
