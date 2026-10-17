package hookentry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// writeRequests writes a transcript of n model requests, each one response
// split over two assistant lines, with a compaction halfway.
func writeRequests(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := range n {
		if i == n/2 {
			b.WriteString(`{"type":"system","subtype":"compact_boundary"}` + "\n")
			b.WriteString(
				`{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"summary"}}` + "\n",
			)
		}
		for range 2 {
			fmt.Fprintf(&b, `{"type":"assistant","requestId":"req_%d","message":{"id":"msg_%d",`+
				`"model":"claude-opus-5-5","usage":{"input_tokens":3,"output_tokens":5}}}`+"\n", i, i)
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func callCapPayload(t *testing.T, transcriptPath, agentID, agentType string) string {
	t.Helper()
	payload := map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Read", "transcript_path": transcriptPath}
	if agentID != "" {
		payload["agent_id"] = agentID
	}
	if agentType != "" {
		payload["agent_type"] = agentType
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCallCapHookInjectsTheRealCountNearTheCap(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "sess.jsonl")
	agentFile := filepath.Join(dir, "sess", "subagents", "agent-a1.jsonl")
	writeRequests(t, session, 230)
	foreman := "flights-foreman"
	for _, test := range []struct {
		name            string
		requests        int    // requests in the agent's own file; -1 leaves it absent
		payload         string // "" builds one from path, id and agentType
		path, id, agent string
		want            []string // every substring the context carries; nil is no output
		notWanted       string
	}{
		{name: "below four fifths of the cap is silent", requests: 199, path: session, id: "a1", agent: foreman},
		{
			name: "at four fifths the count arrives", requests: 200, path: session, id: "a1", agent: foreman,
			want: []string{"200 of your 250 model requests", "50 left"}, notWanted: "FAILED",
		},
		{
			name: "at the cap the stop arrives", requests: 250, path: session, id: "a1", agent: foreman,
			want: []string{"250 model requests", "cap of 250", "FAILED {flight}: cap", "handoff"},
		},
		{
			name: "past the cap the stop still arrives", requests: 263, path: session, id: "a1", agent: "foreman-pro",
			want: []string{"263 model requests", "cap of 250", "FAILED {flight}: cap"},
		},
		{
			name: "a lander at its cap is told its own manual's return", requests: 200, path: session, id: "a1", agent: "flights-lander",
			want: []string{"cap of 200", "FAIL {flight}: cap", "ledger as it stands"}, notWanted: "FAILED",
		},
		{
			name: "any other capped agent is told the executor's return", requests: 150, path: session, id: "a1",
			agent: "flights-mechanical-executor",
			want:  []string{"cap of 150", "FAILED {id}: cap", "what is left"}, notWanted: "{flight}",
		},
		{
			name: "a transcript_path already the agent's file is read as is", requests: 240, path: agentFile, id: "a1", agent: foreman,
			want: []string{"240 of your 250 model requests"},
		},
		{
			name: "no agent_id reads transcript_path itself", requests: 240, path: session, agent: foreman,
			want: []string{"230 of your 250"}, notWanted: "240 of your",
		},
		{
			name: "no agent_type is an unknown cap carrying the count", requests: 240, path: session, id: "a1",
			want: []string{"240 model requests", "cap is unknown"}, notWanted: "FAILED",
		},
		{
			name: "an absent agent transcript is an unknown count, never silence", requests: -1, path: session, id: "a1", agent: foreman,
			want: []string{"count is unknown", "cap is 250"},
		},
		{
			name: "a malformed payload is an unknown count", requests: 240, payload: `{"tool_name":`,
			want: []string{"count is unknown"},
		},
		{
			name: "a payload field of the wrong type keeps the agent_type beside it", requests: 240,
			payload: `{"tool_name":"Read","transcript_path":"x.jsonl","agent_id":7,"agent_type":"flights-foreman"}`,
			want:    []string{"count is unknown", "cap is 250"},
		},
		{
			name: "a payload naming no transcript is an unknown count", requests: 240, payload: `{"tool_name":"Read","agent_type":"flights-foreman"}`,
			want: []string{"count is unknown", "cap is 250"},
		},
		{
			name: "an agent_id that is no id is an unknown count, never a path", requests: 240, path: session, id: "../a1", agent: foreman,
			want: []string{"count is unknown"}, notWanted: "240 of your",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Each case rewrites one agent file, never appending to it: a tally saved by
			// the case before would resume over a file no agent writes, so each case
			// counts from an empty tally directory.
			t.Setenv(paths.EnvHome, t.TempDir())
			_ = os.Remove(agentFile)
			if test.requests >= 0 {
				writeRequests(t, agentFile, test.requests)
			}
			payload := test.payload
			if payload == "" {
				payload = callCapPayload(t, test.path, test.id, test.agent)
			}
			var stdout, stderr bytes.Buffer
			code := CallCapHook(nil, strings.NewReader(payload), &stdout, &stderr)
			if code != 0 {
				t.Fatalf("code=%d stderr=%q, want 0", code, stderr.String())
			}
			if test.want == nil {
				if stdout.Len() != 0 {
					t.Fatalf("stdout=%q, want no output", stdout.String())
				}
				return
			}
			var response struct {
				HookSpecificOutput struct {
					HookEventName      string `json:"hookEventName"`
					AdditionalContext  string `json:"additionalContext"`
					PermissionDecision string `json:"permissionDecision"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
				t.Fatalf("stdout=%q is no hook response: %v", stdout.String(), err)
			}
			out := response.HookSpecificOutput
			if out.HookEventName != "PreToolUse" || out.PermissionDecision != "" {
				t.Fatalf("hookSpecificOutput=%+v, want a PreToolUse context that decides nothing", out)
			}
			for _, want := range test.want {
				if !strings.Contains(out.AdditionalContext, want) {
					t.Fatalf("additionalContext=%q, want it to contain %q", out.AdditionalContext, want)
				}
			}
			if test.notWanted != "" && strings.Contains(out.AdditionalContext, test.notWanted) {
				t.Fatalf("additionalContext=%q, want no %q", out.AdditionalContext, test.notWanted)
			}
		})
	}
}

func TestCallCapHookRefusesAnArgument(t *testing.T) {
	for _, args := range [][]string{{"--cap", "10"}, {"extra"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := CallCapHook(args, strings.NewReader(`{"tool_name":"Read"}`), &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "usage: pfm internal call-cap\n") {
				t.Fatalf(
					"code=%d stdout=%q stderr=%q, want 1, no output and the usage",
					code,
					stdout.String(),
					stderr.String(),
				)
			}
		})
	}
}

// appendRequests appends requests from up to to as one agent would: whole
// lines after the ones already written.
func appendRequests(t *testing.T, path string, from, to int) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	for i := from; i < to; i++ {
		fmt.Fprintf(file, `{"type":"assistant","requestId":"req_%d","message":{"id":"msg_%d",`+
			`"model":"claude-opus-5-5","usage":{"input_tokens":3,"output_tokens":5}}}`+"\n", i, i)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func callCapContext(t *testing.T, payload string) (context, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := CallCapHook(nil, strings.NewReader(payload), &out, &errOut); code != 0 {
		t.Fatalf("code=%d stderr=%q, want 0", code, errOut.String())
	}
	if out.Len() == 0 {
		return "", errOut.String()
	}
	var response callCapResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("stdout=%q is no hook response: %v", out.String(), err)
	}
	return response.HookSpecificOutput.AdditionalContext, errOut.String()
}

// TestCallCapHookResumesItsTally proves the hook keeps one tally per
// transcript and reads only what the agent appended since: a key planted in
// the saved tally survives into the next count, which a read from the start
// could not hold apart from the transcript. A corrupt tally is a full read,
// never a wrong count, and a tally's birth prunes tallies a week old.
func TestCallCapHookResumesItsTally(t *testing.T) {
	home, err := paths.Home()
	if err != nil {
		t.Fatal(err)
	}
	dir := paths.CallCapTallyDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale, fresh := filepath.Join(dir, "stale.json"), filepath.Join(dir, "fresh.json")
	for _, path := range []string{stale, fresh} {
		if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	week := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(stale, week, week); err != nil {
		t.Fatal(err)
	}
	agentFile := filepath.Join(t.TempDir(), "sess", "subagents", "agent-a9.jsonl")
	writeRequests(t, agentFile, 100)
	payload := callCapPayload(t, agentFile, "a9", "flights-foreman")

	if context, stderr := callCapContext(t, payload); context != "" || stderr != "" {
		t.Fatalf("100 requests gave context %q, stderr %q; want silence", context, stderr)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stat %s = %v, want a week-old tally pruned at a tally's birth", stale, err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("stat %s = %v, want a fresh tally kept", fresh, err)
	}
	sum := sha256.Sum256([]byte(agentFile))
	tallyPath := filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
	raw, err := os.ReadFile(tallyPath)
	if err != nil {
		t.Fatal(err)
	}
	var tally transcript.RequestTally
	if err := json.Unmarshal(raw, &tally); err != nil {
		t.Fatalf("tally %s = %q: %v", tallyPath, raw, err)
	}
	info, err := os.Stat(agentFile)
	if err != nil || tally.Offset != info.Size() || len(tally.Keys) != 100 {
		t.Fatalf("tally offset %d keys %d, want %d and 100 (%v)", tally.Offset, len(tally.Keys), info.Size(), err)
	}

	tally.Keys["message:planted"] = true
	planted, err := json.Marshal(tally)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tallyPath, planted, 0o600); err != nil {
		t.Fatal(err)
	}
	appendRequests(t, agentFile, 100, 210)
	if context, _ := callCapContext(t, payload); !strings.Contains(context, "211 of your 250") {
		t.Fatalf("context %q, want the planted key resumed beside 210 requests: 211 of your 250", context)
	}

	if err := os.WriteFile(tallyPath, []byte(`{"offset":`), 0o600); err != nil {
		t.Fatal(err)
	}
	context, stderr := callCapContext(t, payload)
	if !strings.Contains(context, "210 of your 250") || !strings.Contains(stderr, "tally") {
		t.Fatalf(
			"corrupt tally gave context %q, stderr %q; want a full read, 210 of your 250, and the tally named",
			context,
			stderr,
		)
	}

	// A run that read nothing new leaves the tally file as it is.
	hour := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(tallyPath, hour, hour); err != nil {
		t.Fatal(err)
	}
	if context, _ := callCapContext(t, payload); !strings.Contains(context, "210 of your 250") {
		t.Fatalf("context %q, want 210 of your 250 with nothing appended", context)
	}
	if info, err = os.Stat(tallyPath); err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(hour) {
		t.Fatalf("tally %s mtime %v, want %v: untouched when nothing was appended", tallyPath, info.ModTime(), hour)
	}
}
