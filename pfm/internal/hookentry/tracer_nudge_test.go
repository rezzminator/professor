package hookentry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

const wantTracerNudgeText = "TRACER-NUDGE: %d tool calls and no tracer yet. Finish the overview (file and symbol names, no body read), then send tracer your numbered questions (step 2 Trace); a brief's TRACE run: drill its nodes; nothing to trace: write TRACE skipped — {reason} to run.md."

func tracerNudgePayload(agentID, sessionID, tool, toolInput string) string {
	return fmt.Sprintf(
		`{"session_id":%q,"agent_id":%q,"agent_type":"flights-foreman","hook_event_name":"PostToolUse","tool_name":%q,"tool_input":%s}`,
		sessionID,
		agentID,
		tool,
		toolInput,
	)
}

func runTracerNudge(t *testing.T, dir, payload string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := TracerNudge(strings.NewReader(payload), &stdout, &stderr, dir)
	return stdout.String(), stderr.String(), code
}

func wantNudge(n int) string {
	return `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"` + fmt.Sprintf(
		wantTracerNudgeText,
		n,
	) + `"}}` + "\n"
}

func TestTracerNudgeCountsUntracedCallsAndNudgesFromTheSeventh(t *testing.T) {
	dir := t.TempDir()
	read := tracerNudgePayload("agent-a", "sess-1", "Read", `{"file_path":"/x"}`)
	for call := 1; call <= 8; call++ {
		stdout, stderr, code := runTracerNudge(t, dir, read)
		if code != 0 || stderr != "" {
			t.Fatalf("call %d: code=%d stderr=%q", call, code, stderr)
		}
		want := ""
		if call >= 7 {
			want = wantNudge(call)
		}
		if stdout != want {
			t.Fatalf("call %d: stdout=%q, want %q", call, stdout, want)
		}
	}
}

func TestTracerNudgeTracedFormsSilenceEveryLaterCall(t *testing.T) {
	for _, test := range []struct{ name, tool, input string }{
		{"Agent tracer", "Agent", `{"subagent_type":"tracer","prompt":"q"}`},
		{"Task tracer", "Task", `{"subagent_type":"tracer"}`},
		{"Bash tracetree", "Bash", `{"command":"python3 ~/x/tracetree.py show RUN"}`},
		{"TRACE skipped in a Write", "Write", `{"file_path":"/f/run.md","content":"TRACE skipped — brief holds every line"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			read := tracerNudgePayload("agent-a", "s", "Read", `{"file_path":"/x"}`)
			for range 8 {
				runTracerNudge(t, dir, read)
			}
			if stdout, _, _ := runTracerNudge(
				t,
				dir,
				tracerNudgePayload("agent-a", "s", test.tool, test.input),
			); stdout != "" {
				t.Fatalf("traced call stdout=%q, want nothing", stdout)
			}
			for range 3 {
				if stdout, _, _ := runTracerNudge(t, dir, read); stdout != "" {
					t.Fatalf("call after traced stdout=%q, want nothing", stdout)
				}
			}
		})
	}
}

func TestTracerNudgeUntracedLookalikesStayUntraced(t *testing.T) {
	for _, test := range []struct{ name, tool, input string }{
		{"Agent other type", "Agent", `{"subagent_type":"Explore"}`},
		{"Bash without tracetree", "Bash", `{"command":"ls"}`},
		{"tool_input not an object", "Read", `"just a string"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for range 6 {
				runTracerNudge(t, dir, tracerNudgePayload("agent-a", "s", test.tool, test.input))
			}
			stdout, _, _ := runTracerNudge(t, dir, tracerNudgePayload("agent-a", "s", test.tool, test.input))
			if stdout != wantNudge(7) {
				t.Fatalf("7th call stdout=%q, want the nudge", stdout)
			}
		})
	}
}

func TestTracerNudgeKeysAnEmptyAgentIDBySessionID(t *testing.T) {
	dir := t.TempDir()
	read := tracerNudgePayload("", "main-sess", "Read", `{}`)
	for range 6 {
		runTracerNudge(t, dir, read)
	}
	if stdout, _, _ := runTracerNudge(t, dir, read); stdout != wantNudge(7) {
		t.Fatalf("stdout=%q, want the nudge keyed by session_id", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "main-sess.calls")); err != nil {
		t.Fatalf("calls file for the session_id key: %v", err)
	}
	// A different agent key counts on its own.
	if stdout, _, _ := runTracerNudge(t, dir, tracerNudgePayload("other", "main-sess", "Read", `{}`)); stdout != "" {
		t.Fatalf("a fresh key printed %q, want nothing", stdout)
	}
}

func TestTracerNudgeErrorsAreLoudAndExitZero(t *testing.T) {
	for _, test := range []struct{ name, payload string }{
		{"bad key", tracerNudgePayload("../evil", "s", "Read", `{}`)},
		{"overlong key", tracerNudgePayload(strings.Repeat("a", 129), "s", "Read", `{}`)},
		{"no key at all", `{"tool_name":"Read","tool_input":{}}`},
		{"undecodable payload", `not-json`},
	} {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, code := runTracerNudge(t, t.TempDir(), test.payload)
			if code != 0 {
				t.Fatalf("code=%d, want 0", code)
			}
			if !strings.HasPrefix(stderr, "pfm internal tracer-nudge: ") {
				t.Fatalf("stderr=%q, want the pfm internal tracer-nudge line", stderr)
			}
			if !strings.Contains(stdout, "TRACER-NUDGE-ERROR: ") ||
				!strings.Contains(stdout, `"hookEventName":"PostToolUse"`) {
				t.Fatalf("stdout=%q, want additionalContext carrying TRACER-NUDGE-ERROR", stdout)
			}
		})
	}
}

func TestTracerNudgeAStateWriteFailureIsLoud(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runTracerNudge(
		t,
		filepath.Join(blocker, "state"),
		tracerNudgePayload("a", "s", "Read", `{}`),
	)
	if code != 0 || !strings.Contains(stderr, "pfm internal tracer-nudge: ") ||
		!strings.Contains(stdout, "TRACER-NUDGE-ERROR: ") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestTracerNudgeStateDirIsPerUser(t *testing.T) {
	root := t.TempDir()
	got := TracerNudgeStateDir(&paths.MapEnv{Values: map[string]string{paths.EnvHome: root}})
	if want := filepath.Join(root, "tmp", "pfm-tracer-nudge-"+strconv.Itoa(os.Getuid())); got != want {
		t.Fatalf("state dir=%q, want %q", got, want)
	}
	got = TracerNudgeStateDir(&paths.MapEnv{})
	if want := filepath.Join(os.TempDir(), "pfm-tracer-nudge-"+strconv.Itoa(os.Getuid())); got != want {
		t.Fatalf("state dir=%q, want %q", got, want)
	}
}

// A state directory or file another user could have planted in a shared /tmp
// is refused loudly, never followed.
func TestTracerNudgeRefusesStateItDoesNotOwn(t *testing.T) {
	for _, test := range []struct {
		name  string
		plant func(t *testing.T, stateDir, victim string) string
	}{
		{"symlinked state directory", func(t *testing.T, stateDir, _ string) string {
			if err := os.Symlink(t.TempDir(), stateDir); err != nil {
				t.Fatal(err)
			}
			return stateDir
		}},
		{"symlinked calls file", func(t *testing.T, stateDir, victim string) string {
			mkdirPrivate(t, stateDir)
			if err := os.Symlink(victim, filepath.Join(stateDir, "agent-a.calls")); err != nil {
				t.Fatal(err)
			}
			return stateDir
		}},
		{"FIFO calls file", func(t *testing.T, stateDir, _ string) string {
			mkdirPrivate(t, stateDir)
			if err := syscall.Mkfifo(filepath.Join(stateDir, "agent-a.calls"), 0o600); err != nil {
				t.Fatal(err)
			}
			return stateDir
		}},
		{"symlinked traced mark", func(t *testing.T, stateDir, victim string) string {
			mkdirPrivate(t, stateDir)
			if err := os.Symlink(victim, filepath.Join(stateDir, "agent-a.traced")); err != nil {
				t.Fatal(err)
			}
			return stateDir
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			victim := filepath.Join(root, "victim")
			if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			dir := test.plant(t, filepath.Join(root, "state"), victim)
			stdout, stderr, code := runTracerNudge(t, dir, tracerNudgePayload("agent-a", "s", "Read", `{}`))
			if code != 0 || !strings.HasPrefix(stderr, "pfm internal tracer-nudge: ") ||
				!strings.Contains(stdout, "TRACER-NUDGE-ERROR: ") {
				t.Fatalf("code=%d stdout=%q stderr=%q, want a loud refusal", code, stdout, stderr)
			}
			if body, err := os.ReadFile(victim); err != nil || string(body) != "keep" {
				t.Fatalf("victim=%q err=%v, want it untouched", body, err)
			}
		})
	}
}

func mkdirPrivate(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestTracerNudgeConcurrentCallsKeepTheTracedMark(t *testing.T) {
	for round := range 50 {
		dir := t.TempDir()
		key := fmt.Sprintf("agent-%d", round)
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				payload := tracerNudgePayload(key, "s", "Read", `{}`)
				// Two tracers sent in one message race for the O_EXCL mark;
				// the loser must stay silent, never TRACER-NUDGE-ERROR.
				if i%2 == 1 {
					payload = tracerNudgePayload(key, "s", "Agent", `{"subagent_type":"tracer"}`)
				}
				if _, stderr, code := runTracerNudge(t, dir, payload); code != 0 || stderr != "" {
					t.Errorf("round %d call %d: code=%d stderr=%q", round, i, code, stderr)
				}
			}()
		}
		wg.Wait()
		if _, err := os.Stat(filepath.Join(dir, key+".traced")); err != nil {
			t.Fatalf("round %d: traced mark lost: %v", round, err)
		}
		if stdout, _, _ := runTracerNudge(t, dir, tracerNudgePayload(key, "s", "Read", `{}`)); stdout != "" {
			t.Fatalf("round %d: stdout=%q after traced, want nothing", round, stdout)
		}
	}
}
