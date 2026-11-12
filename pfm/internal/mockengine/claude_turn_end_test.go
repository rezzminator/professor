package mockengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// entries is every entry pfm's transcript parser reads from path, as role:text[:error].
func entries(t *testing.T, path string) []string {
	t.Helper()
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, path)), "\n") {
		if entry, ok := transcript.Parse([]byte(line), string(pfmengine.Claude)); ok {
			row := entry.Role + ":" + entry.Text
			if entry.Error != "" {
				row += ":" + entry.Error
			}
			got = append(got, row)
		}
	}
	return got
}

// TestClaudeTurnEndsShortOfAReply pins how a Claude turn ends without its
// reply, each read back through pfm's own transcript parser: Escape leaves the
// interrupt record and no Stop hook; an api_error step leaves the synthetic
// error reply, no Stop hook, and the composer live for the next prompt.
func TestClaudeTurnEndsShortOfAReply(t *testing.T) {
	cases := []struct {
		name    string
		steps   []Step
		escape  bool
		shown   string
		entries []string
	}{
		{
			name:    "escape mid-turn",
			steps:   []Step{{Type: StepTurn, Reply: "never", BusyMS: 30000}},
			escape:  true,
			shown:   "⎿ Interrupted",
			entries: []string{"user:go", "assistant:[turn aborted: interrupted]"},
		},
		{
			name:    "api error with its defaults",
			steps:   []Step{{Type: StepAPIError}},
			shown:   "⎿ " + defaultAPIErrorText,
			entries: []string{"user:go", "assistant:" + defaultAPIErrorText + ":server_error"},
		},
		{
			name:    "api error with its own text and kind",
			steps:   []Step{{Type: StepAPIError, Reply: "API Error: 429 rate limited", Error: "rate_limit"}},
			shown:   "⎿ API Error: 429 rate limited",
			entries: []string{"user:go", "assistant:API Error: 429 rate limited:rate_limit"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			stopped := filepath.Join(fix.root, "stopped")
			project := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"touch ` + stopped + `"}]}]}}`
			if err := os.MkdirAll(filepath.Join(fix.work, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(fix.work, ".claude", "settings.json"),
				[]byte(project),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			fix.write(
				Scenario{SessionID: fixtureSession, Steps: append(tc.steps, Step{Type: StepTurn, Reply: "after"})},
			)
			session := fix.startTUI("claude", claudeArgs(), nil)
			session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
			session.typeLine("go")
			if tc.escape {
				session.waitFrame("the busy footer", pfmcall.IsBusy)
				session.typeRaw("\x1b")
			}
			session.waitFrame(tc.shown, func(frame string) bool {
				return !pfmcall.IsBusy(frame) && strings.Contains(frame, tc.shown)
			})
			path := fix.claudeTranscript(fixtureSession)
			if got := entries(t, path); strings.Join(got, "\n") != strings.Join(tc.entries, "\n") {
				t.Fatalf("transcript %s entries = %q, want %q", path, got, tc.entries)
			}
			if _, err := os.Stat(stopped); !os.IsNotExist(err) {
				t.Fatalf("a Stop hook ran for a turn that ended short of its reply (stat %s: %v)", stopped, err)
			}
			session.typeLine("again")
			session.waitFrame("the next reply", func(frame string) bool { return strings.Contains(frame, "⏺ after") })
			if _, err := os.Stat(stopped); err != nil {
				t.Fatalf("the next turn's Stop hook did not run: %v", err)
			}
		})
	}
}

// TestClaudeAITitleStepNamesTheChatInPfmsIndex pins the ai_title beat: pfm's
// index reads the automatic title from the transcript, the turn still replies.
func TestClaudeAITitleStepNamesTheChatInPfmsIndex(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	fix.write(Scenario{SessionID: fixtureSession})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine(
		`hello mock-engine: [{"type":"ai_title","name":"Fixing the hinge"},{"type":"turn","reply":"done"}]`,
	)
	session.waitFrame("the reply", func(frame string) bool { return strings.Contains(frame, "⏺ done") })
	rows := fix.indexedTranscripts()
	if len(rows) != 1 || rows[0].AITitle != "Fixing the hinge" || rows[0].CustomTitle != "" {
		t.Fatalf("indexed transcripts = %+v, want one row whose AI title is %q and no custom title", rows,
			"Fixing the hinge")
	}
}

// TestClaudeHeadlessAPIErrorExitsOneWithTheErrorText pins `claude -p` on a refused turn.
func TestClaudeHeadlessAPIErrorExitsOneWithTheErrorText(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "text", args: claudeArgs("-p", "go"), want: "API Error: 529 overloaded\n"},
		{
			name: "json",
			args: claudeArgs("-p", "go", "--output-format", "json"),
			want: `{"type":"result","subtype":"success","is_error":true,"duration_ms":0,"num_turns":1,` +
				`"result":"API Error: 529 overloaded","session_id":"` + fixtureSession + `","total_cost_usd":0,` +
				`"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,` +
				`"output_tokens":0},"modelUsage":null}` + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.write(Scenario{SessionID: fixtureSession, Steps: []Step{
				{Type: StepAPIError, Reply: "API Error: 529 overloaded", Error: "overloaded_error"},
			}})
			code, stdout, stderr := runOnce(fix, "claude", tc.args, "")
			if code != 1 || stdout != tc.want {
				t.Fatalf("claude -p = exit %d, stdout %q (stderr %q); want exit 1, stdout %q", code, stdout, stderr,
					tc.want)
			}
			want := []string{"user:go", "assistant:API Error: 529 overloaded:overloaded_error"}
			if got := entries(
				t,
				fix.claudeTranscript(fixtureSession),
			); strings.Join(
				got,
				"\n",
			) != strings.Join(
				want,
				"\n",
			) {
				t.Fatalf("transcript entries = %q, want %q", got, want)
			}
		})
	}
}
