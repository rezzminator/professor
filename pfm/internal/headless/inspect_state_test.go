package headless

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const (
	parentThread = "11111111-1111-4111-8111-111111111111"
	childThread  = "22222222-2222-4222-8222-222222222222"
	grandThread  = "33333333-3333-4333-8333-333333333333"
	otherThread  = "44444444-4444-4444-8444-444444444444"

	codexUserLine      = `{"type":"event_msg","payload":{"type":"user_message","message":"go"}}`
	codexAssistantLine = `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`
	codexToolLine      = `{"type":"response_item","payload":{"type":"function_call","name":"spawn_agent","arguments":"{}"}}`
	codexAbortLine     = `{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-0001","reason":"interrupted"}}`
)

func subagentMeta(id, parent string) string {
	return `{"type":"session_meta","payload":{"id":"` + id +
		`","thread_source":"subagent","parent_thread_id":"` + parent + `"}}`
}

// writeRollout writes one Codex rollout into sessions/{day}/ and stamps its mtime.
func writeRollout(t *testing.T, root, day, id string, stamp time.Time, lines ...string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(day))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-01-01T00-00-00-"+id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return path
}

func inspectCodexParent(t *testing.T, path string) (Status, error) {
	t.Helper()
	return Inspect(context.Background(), Chat{
		Name: "seat", ID: parentThread, Engine: pfmengine.Codex, Path: path, Live: true,
	}, time.Now())
}

// A Codex parent sits at its prompt (assistant spoke last) while its
// sub-agent works in a rollout of its own: that is working, not idle. Claude's
// sidechain rule already says so for Claude; Codex's lineage lives in the
// sub-agent's session_meta, so the rule follows parent_thread_id.
func TestACodexParentWithANewerSubagentRolloutIsWorking(t *testing.T) {
	parentStamp := time.Now().Add(-90 * time.Second)
	newer := parentStamp.Add(60 * time.Second)
	older := parentStamp.Add(-30 * time.Second)
	const day = "2026/01/01"
	for _, testCase := range []struct {
		name  string
		build func(t *testing.T, root string)
		tail  string
		want  string
	}{
		{"newer sub-agent of the parent", func(t *testing.T, root string) {
			writeRollout(t, root, day, childThread, newer, subagentMeta(childThread, parentThread), codexAssistantLine)
		}, codexAssistantLine, StateWorking},
		{"sub-agent written into a later day", func(t *testing.T, root string) {
			writeRollout(t, root, "2026/01/02", childThread, newer, subagentMeta(childThread, parentThread))
		}, codexAssistantLine, StateWorking},
		{"sub-agent named by the nested source shape", func(t *testing.T, root string) {
			writeRollout(t, root, day, childThread, newer,
				`{"type":"session_meta","payload":{"id":"`+childThread+
					`","source":{"subagent":{"thread_spawn":{"parent_thread_id":"`+parentThread+`"}}}}}`)
		}, codexAssistantLine, StateWorking},
		{"nested sub-agent whose own parent is older", func(t *testing.T, root string) {
			writeRollout(t, root, day, childThread, older, subagentMeta(childThread, parentThread))
			writeRollout(t, root, day, grandThread, newer, subagentMeta(grandThread, childThread))
		}, codexAssistantLine, StateWorking},
		{"turn ended on an error", func(t *testing.T, root string) {
			writeRollout(t, root, day, childThread, newer, subagentMeta(childThread, parentThread))
		}, `{"type":"event_msg","payload":{"type":"task_complete","error":{"message":"x","codex_error_info":"server_error"}}}`, StateWorking},
		{"older sub-agent", func(t *testing.T, root string) {
			writeRollout(t, root, day, childThread, older, subagentMeta(childThread, parentThread))
		}, codexAssistantLine, StateIdle},
		{"newer sub-agent of another parent", func(t *testing.T, root string) {
			writeRollout(t, root, day, childThread, newer, subagentMeta(childThread, otherThread))
		}, codexAssistantLine, StateIdle},
		{"nested chain that never reaches the parent", func(t *testing.T, root string) {
			writeRollout(t, root, day, childThread, older, subagentMeta(childThread, otherThread))
			writeRollout(t, root, day, grandThread, newer, subagentMeta(grandThread, childThread))
		}, codexAssistantLine, StateIdle},
		{"newer rollout that is a user thread", func(t *testing.T, root string) {
			writeRollout(t, root, day, childThread, newer,
				`{"type":"session_meta","payload":{"id":"`+childThread+`","thread_source":"user"}}`)
		}, codexAssistantLine, StateIdle},
		{"newer sub-agent in an earlier day", func(t *testing.T, root string) {
			writeRollout(t, root, "2025/12/31", childThread, newer, subagentMeta(childThread, parentThread))
		}, codexAssistantLine, StateIdle},
		{"rollout with no first line yet", func(t *testing.T, root string) {
			dir := filepath.Join(root, day)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "rollout-2026-01-01T00-00-00-"+childThread+".jsonl")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, newer, newer); err != nil {
				t.Fatal(err)
			}
		}, codexAssistantLine, StateIdle},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			testCase.build(t, root)
			parent := writeRollout(t, root, day, parentThread, parentStamp, codexUserLine, testCase.tail)
			status, err := inspectCodexParent(t, parent)
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}
			wantState := testCase.want
			if status.State != wantState {
				t.Fatalf("state = %q, want %q (%#v)", status.State, wantState, status)
			}
			if status.State == StateWorking && (status.IdleSeconds != 0 || status.Error != "") {
				t.Fatalf("working seat kept idle=%d error=%q", status.IdleSeconds, status.Error)
			}
		})
	}
}

// Codex writes sub-agent rollouts only into sessions/YYYY/MM/DD: a parent
// outside that shape has no sub-agents to find, and its verdict is the
// transcript's.
func TestACodexParentOutsideADayDirectoryKeepsItsTranscriptVerdict(t *testing.T) {
	path := writeChat(t, codexUserLine, codexAssistantLine)
	status, err := inspectCodexParent(t, path)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if status.State != StateIdle {
		t.Fatalf("state = %q, want idle", status.State)
	}
}

// A sub-agent rollout whose first line cannot be read as session_meta is a
// loud inspect error, never a silent "no sub-agent".
func TestAnUndecodableNewerCodexRolloutIsAnInspectError(t *testing.T) {
	root := t.TempDir()
	stamp := time.Now().Add(-90 * time.Second)
	writeRollout(t, root, "2026/01/01", childThread, stamp.Add(time.Minute), `{"not":"a session_meta"}`)
	parent := writeRollout(t, root, "2026/01/01", parentThread, stamp, codexUserLine, codexAssistantLine)
	status, err := inspectCodexParent(t, parent)
	if err == nil || !strings.Contains(err.Error(), "decode Codex rollout header") {
		t.Fatalf("Inspect() = %#v, %v; want the decode failure named", status, err)
	}
}

// A turn its human interrupted ended: the chat waits at its prompt, so it is
// idle, not working on the tool call the interrupt cut short.
func TestAnInterruptedTurnIsIdleOnBothEngines(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		engine pfmengine.ID
		lines  []string
	}{
		{"codex turn_aborted", pfmengine.Codex, []string{codexUserLine, codexToolLine, codexAbortLine}},
		{
			"claude interrupt marker", pfmengine.Claude,
			[]string{userLine("go"), toolLine("Bash"), `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeChat(t, testCase.lines...)
			stamp := time.Now().Add(-90 * time.Second)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			status, err := Inspect(context.Background(),
				Chat{Name: "seat", Engine: testCase.engine, Path: path, Live: true}, time.Now())
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}
			if status.State != StateIdle || status.IdleSeconds < 89 || status.PendingTool != "" {
				t.Fatalf("status = %#v, want idle ~90s with no pending tool", status)
			}
		})
	}
}

// PendingTool and QuietSeconds are the evidence internal/chat's pane rule
// reads: the tool a live chat is stuck on and how long its transcript has been
// quiet, kept even while IdleSeconds is zeroed for a working chat.
func TestInspectReportsThePendingToolAndTheQuietTime(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		engine      pfmengine.ID
		lines       []string
		live        bool
		wantTool    string
		wantQuiet   bool
		wantIdleSec bool
	}{
		{"claude tool call", pfmengine.Claude, []string{userLine("go"), toolLine("Bash")}, true, "Bash", true, false},
		{"codex tool call", pfmengine.Codex, []string{codexUserLine, codexToolLine}, true, "spawn_agent", true, false},
		{"answered", pfmengine.Claude, []string{userLine("go"), assistantLine("done")}, true, "", true, true},
		{"dead chat", pfmengine.Claude, []string{userLine("go"), toolLine("Bash")}, false, "", false, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeChat(t, testCase.lines...)
			stamp := time.Now().Add(-90 * time.Second)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			status, err := Inspect(context.Background(),
				Chat{Name: "seat", Engine: testCase.engine, Path: path, Live: testCase.live}, time.Now())
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}
			if status.PendingTool != testCase.wantTool {
				t.Fatalf("PendingTool = %q, want %q", status.PendingTool, testCase.wantTool)
			}
			if got := status.QuietSeconds >= 89; got != testCase.wantQuiet {
				t.Fatalf("QuietSeconds = %d, want quiet=%v", status.QuietSeconds, testCase.wantQuiet)
			}
			if got := status.IdleSeconds > 0; got != testCase.wantIdleSec {
				t.Fatalf("IdleSeconds = %d, want nonzero=%v", status.IdleSeconds, testCase.wantIdleSec)
			}
		})
	}
}

func TestABlockedChatIsAliveAndKeepsItsState(t *testing.T) {
	status := Status{Name: "seat", State: StateBlocked, Engine: pfmengine.Claude}
	if !status.Alive() {
		t.Fatalf("Alive() = false for %q", status.State)
	}
	if !strings.Contains(status.Line(), "\tblocked\t") {
		t.Fatalf("Line() = %q, want the blocked state", status.Line())
	}
}

const (
	codexTurnStartLine    = `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-0001"}}`
	codexTurnCompleteLine = `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-0001","last_agent_message":null}}`
)

// A live Codex seat is idle only once its rollout's newest turn record ends the
// turn: the assistant commentary Codex writes between tool calls is newest
// through every quiet gap of a turn still running.
func TestACodexTurnIsIdleOnlyAfterItsEndRecord(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"commentary mid-turn", []string{codexTurnStartLine, codexUserLine, codexAssistantLine}, StateWorking},
		{"task_complete", []string{codexTurnStartLine, codexUserLine, codexAssistantLine, codexTurnCompleteLine}, StateIdle},
		{"turn_aborted", []string{codexTurnStartLine, codexUserLine, codexAssistantLine, codexAbortLine}, StateIdle},
		{"task_complete after a tool call", []string{codexTurnStartLine, codexUserLine, codexToolLine, codexTurnCompleteLine}, StateIdle},
		{"a prompt after the turn ended", []string{codexTurnStartLine, codexAssistantLine, codexTurnCompleteLine, codexUserLine}, StateWorking},
		{"no turn record keeps the newest-entry rule", []string{codexUserLine, codexAssistantLine}, StateIdle},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeChat(t, testCase.lines...)
			stamp := time.Now().Add(-10 * time.Minute)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			status, err := Inspect(context.Background(),
				Chat{Name: "seat", Engine: pfmengine.Codex, Path: path, Live: true}, time.Now())
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}
			if status.State != testCase.want {
				t.Fatalf("State = %q, want %q (status %#v)", status.State, testCase.want, status)
			}
			if status.State == StateIdle && (status.IdleSeconds < 599 || status.PendingTool != "") {
				t.Fatalf("status = %#v, want idle ~600s with no pending tool", status)
			}
			if status.State == StateWorking && status.IdleSeconds != 0 {
				t.Fatalf("IdleSeconds = %d on a working seat", status.IdleSeconds)
			}
		})
	}
}
