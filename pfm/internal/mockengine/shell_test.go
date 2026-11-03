package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

func TestStopShellTasksJoinsHooksRegisteredByCompletion(t *testing.T) {
	done := make(chan struct{})
	close(done)
	var calls []string
	proc := &process{}
	proc.background = []shellTask{{done: done, cancel: func() {
		calls = append(calls, "shell")
		proc.backgroundMu.Lock()
		proc.background = append(
			proc.background,
			shellTask{done: done, cancel: func() { calls = append(calls, "hook") }},
		)
		proc.backgroundMu.Unlock()
	}}}
	proc.stopShellTasks()
	if want := []string{"shell", "hook"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("shutdown=%q want=%q", calls, want)
	}
}

func TestBackgroundShellCompletesSubagentAfterMainStop(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gate := filepath.Join(fix.work, "hold")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(fix.root, "events")
	stopPayload := filepath.Join(fix.root, "subagent-stop.json")
	writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), map[string]any{
		hookSubagentStart: commandHook(`printf 'SubagentStart\n' >> "` + trace + `"`),
		hookStop:          commandHook(`printf 'Stop\n' >> "` + trace + `"`),
		hookSubagentStop:  commandHook(`cat > "` + stopPayload + `"; printf 'SubagentStop\n' >> "` + trace + `"`),
	})
	input := json.RawMessage(
		`{"command":"printf '%s' \"$$\" > child.pid; while test -f hold; do sleep 0.02; done; printf completed"}`,
	)
	fix.write(
		Scenario{
			SessionID: fixtureSession,
			Steps: []Step{
				{
					Type:      StepBackgroundAgent,
					Name:      "researcher",
					AgentID:   "agent-researcher",
					AgentType: "general-purpose",
					Input:     input,
				},
				{Type: StepTurn, Reply: "main complete"},
			},
		},
	)
	session := fix.startTUI(engineClaude, nil, nil)
	session.waitFrame("composer", func(s string) bool { return strings.Contains(s, "❯") })
	session.typeLine("research")
	session.waitFrame("main complete", func(s string) bool { return strings.Contains(s, "⏺ main complete") })
	if got := readFile(t, trace); got != "SubagentStart\nStop\n" {
		t.Fatalf("%s: trace=%q want SubagentStart then Stop", trace, got)
	}
	pidPath := filepath.Join(fix.work, "child.pid")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if raw, err := os.ReadFile(pidPath); err == nil && len(raw) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: child never started", pidPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
	pid, err := strconv.Atoi(readFile(t, pidPath))
	if err != nil {
		t.Fatal(err)
	}
	stat, err := gather.NewProcFS("/proc").Stat(pid)
	if err != nil || stat.ParentPID != os.Getpid() {
		t.Fatalf("child %d: stat=%+v err=%v want parent=%d", pid, stat, err, os.Getpid())
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		raw, err := os.ReadFile(trace)
		if err == nil && string(raw) == "SubagentStart\nStop\nSubagentStop\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: completion trace=%q err=%v", trace, raw, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(readFile(t, stopPayload)), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["agent_id"] != "agent-researcher" || payload["last_assistant_message"] != "completed" {
		t.Fatalf("%s: payload=%v", stopPayload, payload)
	}
	if _, err := gather.NewProcFS("/proc").Stat(pid); !os.IsNotExist(err) {
		t.Fatalf("completed child %d stat=%v want gone", pid, err)
	}
}
