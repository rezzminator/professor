package mockengine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// TestClaudeBackgroundJobContinuesUnderANewSession: a background_job step moves the chat to a new session — the
// old transcript ends on the continued-in record naming it, the successor holds the conversation, the title and
// the turn's reply, the daemon is started for it on the chat's config dir — while the host's statusline keeps
// the launch session and the host fires no hook for the job's session.
func TestClaudeBackgroundJobContinuesUnderANewSession(t *testing.T) {
	var started [2]string
	restore := startBackgroundJob
	startBackgroundJob = func(_ *process, configDir, successor string) error {
		started = [2]string{configDir, successor}
		return nil
	}
	t.Cleanup(func() { startBackgroundJob = restore })
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.installHooks()
	fix.write(Scenario{BusyMS: intPtr(0)})
	tui := fix.startTUI("claude", claudeArgs("--session-id", fixtureSession, "--name", "worker 7"), nil)
	tui.waitFrame("composer", func(s string) bool { return strings.Contains(s, "❯") })
	prompt := `go mock-engine: [{"type":"background_job"},{"type":"turn","reply":"job-reply"}]`
	tui.typeLine(prompt)
	tui.waitFrame("reply", func(s string) bool { return strings.Contains(s, "⏺ job-reply") })
	tui.typeLine("/exit")
	if code := tui.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, tui.stderr.String())
	}
	if started[0] != fix.configDir || !pfmengine.IsUUID(started[1]) {
		t.Fatalf("background job started as (config, session) %q, want (%s, a session UUID)", started, fix.configDir)
	}
	launch, successor := fix.claudeTranscript(fixtureSession), fix.claudeTranscript(started[1])
	lines := strings.Split(strings.TrimSpace(readFile(t, launch)), "\n")
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if got := [3]any{last["type"], last["continuedInSessionId"], last["sessionId"]}; got !=
		[3]any{"continued-in", started[1], fixtureSession} {
		t.Fatalf("last record of %s (type, continuedInSessionId, sessionId) = %v", launch, got)
	}
	got := [2][]string{entries(t, launch), entries(t, successor)}
	want := [2][]string{{"user:" + prompt}, {"user:" + prompt, "assistant:job-reply"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries (launch %s, successor %s) = %q, want %q", launch, successor, got, want)
	}
	if title, err := claudeTranscriptTitle(successor); title != "worker 7" || err != nil {
		t.Fatalf("successor title = %q (%v), want worker 7", title, err)
	}
	statusLines := fix.recorded("statusline")
	var input map[string]any
	if err := json.Unmarshal([]byte(statusLines[len(statusLines)-1]), &input); err != nil {
		t.Fatal(err)
	}
	if input["session_id"] != fixtureSession || input["transcript_path"] != launch {
		t.Fatalf("host statusline after the job names (%v, %v), want (%s, %s)",
			input["session_id"], input["transcript_path"], fixtureSession, launch)
	}
	fired := [3]int{len(fix.recorded("UserPromptSubmit")), len(fix.recorded("Stop")), len(fix.recorded("SessionEnd"))}
	if fired != [3]int{1, 0, 0} {
		t.Fatalf("hooks fired (UserPromptSubmit, Stop, SessionEnd) = %v, want [1 0 0]", fired)
	}
}

// TestServeBackgroundJobHoldsItsRecordWhileTheHostLives: the daemon's per-process record is the shape pfm's
// gather reads (its pid, the session, kind bg, its start as pfm reads it), held while the host lives and the
// daemon runs, removed when it is stopped or finds its host gone.
func TestServeBackgroundJobHoldsItsRecordWhileTheHostLives(t *testing.T) {
	const session = "d3333333-3333-4333-8333-333333333333"
	stat, err := gather.NewProcFS("/proc").Stat(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"pid": float64(os.Getpid()), "sessionId": session, "kind": "bg",
		"procStart": strconv.FormatUint(stat.StartTime, 10),
	}
	for _, test := range []struct {
		name string
		host int
	}{
		{"stopped while its host lives", os.Getpid()},
		{"host gone", 1<<22 + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "sessions", strconv.Itoa(os.Getpid())+".json")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- serveBackgroundJob(ctx, dir, session, test.host) }()
			if test.host == os.Getpid() {
				var got map[string]any
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) && !reflect.DeepEqual(got, want) {
					got = nil
					if content, err := os.ReadFile(path); err == nil {
						_ = json.Unmarshal(content, &got)
					}
					time.Sleep(20 * time.Millisecond)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("record %s = %v, want %v", path, got, want)
				}
				cancel()
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("serveBackgroundJob did not return")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("record %s after the job ended: %v, want it removed", path, err)
			}
		})
	}
}
