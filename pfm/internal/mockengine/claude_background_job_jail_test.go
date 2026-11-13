package mockengine

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
