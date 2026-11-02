package headless

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
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

// compress leaves the rollout at path compressed, as Codex leaves a finished one.
func compress(t *testing.T, path string) {
	t.Helper()
	if _, err := codexmeta.CompressRollout(path); err != nil {
		t.Fatal(err)
	}
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
		{"newer compressed sub-agent of the parent", func(t *testing.T, root string) {
			compress(t, writeRollout(t, root, day, childThread, newer, subagentMeta(childThread, parentThread)))
		}, codexAssistantLine, StateWorking},
		{"nested sub-agent whose own parent is compressed", func(t *testing.T, root string) {
			compress(t, writeRollout(t, root, day, childThread, older, subagentMeta(childThread, parentThread)))
			writeRollout(t, root, day, grandThread, newer, subagentMeta(grandThread, childThread))
		}, codexAssistantLine, StateWorking},
		{"compressed sub-agent shadowed by its plain twin", func(t *testing.T, root string) {
			compress(t, writeRollout(t, root, day, childThread, newer, subagentMeta(childThread, parentThread)))
			writeRollout(t, root, day, childThread, older, subagentMeta(childThread, parentThread))
		}, codexAssistantLine, StateIdle},
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

// A rollout whose first line is not a session_meta says nothing of this
// parent's lineage: it is logged with its path and skipped, and the real
// sub-agent listed after it still holds the parent at working.
func TestAnUndecodableNewerCodexRolloutIsSkippedAndLogged(t *testing.T) {
	root := t.TempDir()
	stamp := time.Now().Add(-90 * time.Second)
	undecodable := writeRollout(t, root, "2026/01/01", childThread, stamp.Add(time.Minute), `{"not":"a session_meta"}`)
	writeRollout(t, root, "2026/01/01", grandThread, stamp.Add(time.Minute), subagentMeta(grandThread, parentThread))
	parent := writeRollout(t, root, "2026/01/01", parentThread, stamp, codexUserLine, codexAssistantLine)
	ctx, recorder := obs.Test(t)
	status, err := Inspect(ctx, Chat{
		Name: "seat", ID: parentThread, Engine: pfmengine.Codex, Path: parent, Live: true,
	}, time.Now())
	if err != nil || status.State != StateWorking {
		t.Fatalf("Inspect() = %q, %v; want working, nil", status.State, err)
	}
	var warned []string
	for _, record := range recorder.Records() {
		if record.Message == "inspect: Codex rollout header could not be decoded; not counted as a sub-agent" {
			path, _ := record.Field("path")
			warned = append(warned, record.Level+" "+path.(string))
		}
	}
	if want := []string{"WARN " + undecodable}; !slices.Equal(warned, want) {
		t.Fatalf("warnings = %q, want %q", warned, want)
	}
}

// A rollout removed between the day listing and its read wrote nothing this
// parent can wait on: it is no header and no error.
func TestAVanishedCodexRolloutHasNoHeader(t *testing.T) {
	_, ok, err := readCodexHeader(context.Background(), filepath.Join(t.TempDir(), "rollout-gone.jsonl"))
	if ok || err != nil {
		t.Fatalf("readCodexHeader() ok=%v err=%v; want false, nil", ok, err)
	}
}

// A sidechain or rollout removed between its listing and its stat wrote nothing
// newer: it is skipped. Any other stat failure is a read error, never "no
// sub-agent".
func TestAFileRemovedSinceItsListingIsSkipped(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "2026", "08", "16", "rollout-2026-08-16T12-00-00-"+parentThread+".jsonl")
	child := filepath.Join(root, "2026", "08", "16", "rollout-2026-08-16T12-05-00-"+childThread+".jsonl")
	sidechain := filepath.Join(root, "2026", "08", "16", parentThread, "subagents", "agent-a.jsonl")
	for _, path := range []string{parent, child, sidechain} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	parentModified := time.Now().Add(-time.Hour).UnixNano()
	denied := errors.New("permission denied")
	for _, testCase := range []struct {
		name       string
		statErr    error
		wantClaude bool
		wantErrs   [2]string
	}{
		{"still there", nil, true, [2]string{"", ""}},
		{"removed since the listing", fs.ErrNotExist, false, [2]string{"", ""}},
		{"unreadable", denied, false, [2]string{
			"inspect Claude sidechain " + sidechain + ": permission denied",
			"inspect Codex rollout " + child + ": permission denied",
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			lstatListed = func(path string) (fs.FileInfo, error) {
				if testCase.statErr != nil && (path == sidechain || path == child) {
					return nil, testCase.statErr
				}
				return os.Lstat(path)
			}
			t.Cleanup(func() { lstatListed = os.Lstat })
			gotClaude, claudeErr := newerClaudeSidechain(parent, parentThread, parentModified)
			_, codexErr := newerCodexSubagent(context.Background(), parent, parentThread, parentModified)
			gotErrs := [2]string{}
			for i, err := range []error{claudeErr, codexErr} {
				if err != nil {
					gotErrs[i] = err.Error()
				}
			}
			if gotClaude != testCase.wantClaude || gotErrs != testCase.wantErrs {
				t.Fatalf("Claude sidechain newer=%v, errors=%q; want newer=%v, errors=%q",
					gotClaude, gotErrs, testCase.wantClaude, testCase.wantErrs)
			}
		})
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
	// The rollout's last write is 10 minutes old; a seat socket minted after it
	// is a resume whose process never ran the open turn.
	resumedSeat := fmt.Sprintf("cx-%d-4242-1717", time.Now().Add(-5*time.Minute).Unix())
	seatOlderThanTheTurn := fmt.Sprintf("cx-%d-4242-1717", time.Now().Add(-20*time.Minute).Unix())
	for _, testCase := range []struct {
		name   string
		lines  []string
		want   string
		socket string
	}{
		{"commentary mid-turn", []string{codexTurnStartLine, codexUserLine, codexAssistantLine}, StateWorking, ""},
		{"an open turn the resumed seat never ran", []string{codexTurnStartLine, codexUserLine, codexToolLine}, StateIdle, resumedSeat},
		{"an open turn written after the seat was born", []string{codexTurnStartLine, codexUserLine, codexToolLine}, StateWorking, seatOlderThanTheTurn},
		{"task_complete", []string{codexTurnStartLine, codexUserLine, codexAssistantLine, codexTurnCompleteLine}, StateIdle, ""},
		{"turn_aborted", []string{codexTurnStartLine, codexUserLine, codexAssistantLine, codexAbortLine}, StateIdle, ""},
		{"task_complete after a tool call", []string{codexTurnStartLine, codexUserLine, codexToolLine, codexTurnCompleteLine}, StateIdle, ""},
		{"a prompt after the turn ended", []string{codexTurnStartLine, codexAssistantLine, codexTurnCompleteLine, codexUserLine}, StateWorking, ""},
		{"no turn record keeps the newest-entry rule", []string{codexUserLine, codexAssistantLine}, StateIdle, ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeChat(t, testCase.lines...)
			stamp := time.Now().Add(-10 * time.Minute)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			status, err := Inspect(
				context.Background(),
				Chat{
					Name:   "seat",
					Engine: pfmengine.Codex,
					Path:   path,
					Live:   true,
					Socket: testCase.socket,
				},
				time.Now(),
			)
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
