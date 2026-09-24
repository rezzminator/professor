package compactgate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const (
	gateMainLimit = 150000
	gateSubLimit  = 100000
)

var gateNow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// restingAge is the default fixture age: a recent write, inside activeWindow.
const restingAge = 5 * time.Second

// gateTranscript is one fixture transcript: the usage its last assistant line
// records, the bytes written after it, and how long ago it was last written.
type gateTranscript struct {
	usage int
	tail  int
	age   time.Duration
}

// writeGateTranscript writes a transcript whose estimate is exactly
// usage + tail/bytesPerToken and whose mtime is gateNow minus age.
func writeGateTranscript(t *testing.T, path string, spec gateTranscript) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	body.WriteString(`{"type":"user","message":{"content":"earlier turn"}}` + "\n")
	usageLine := `{"type":"assistant","message":{"usage":{"input_tokens":1000,` +
		`"cache_read_input_tokens":%d,"cache_creation_input_tokens":1000,"output_tokens":500}}}` + "\n"
	fmt.Fprintf(&body, usageLine, spec.usage-2000)
	if spec.tail > 0 {
		prefix, suffix := `{"type":"user","message":{"content":"`, `"}}`+"\n"
		body.WriteString(prefix + strings.Repeat("x", spec.tail-len(prefix)-len(suffix)) + suffix)
	}
	if err := os.WriteFile(path, body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	age := spec.age
	if age == 0 {
		age = restingAge
	}
	stamp := gateNow.Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func gatePayloadFor(t *testing.T, transcript, trigger string) *bytes.Reader {
	t.Helper()
	encoded, err := json.Marshal(map[string]string{
		"session_id":          "11111111-2222-3333-4444-555555555555",
		"transcript_path":     transcript,
		"cwd":                 filepath.Dir(transcript),
		"hook_event_name":     "PreCompact",
		"trigger":             trigger,
		"custom_instructions": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(encoded)
}

func TestGateCompaction(t *testing.T) {
	both := config.ClaudePrefs{AutoCompactMain: gateMainLimit, AutoCompactSubagent: gateSubLimit}
	cases := []struct {
		name    string
		prefs   config.ClaudePrefs
		trigger string
		main    gateTranscript
		subs    map[string]gateTranscript
		want    int
		// wantReason is the exact block line; empty for an allow.
		wantReason string
	}{
		{
			name:       "main below its threshold is blocked",
			prefs:      both,
			trigger:    "auto",
			main:       gateTranscript{usage: 90000, age: time.Second},
			want:       2,
			wantReason: "pfm compact-gate: blocked auto-compaction of the main chat at ~90000 tokens, below its 150000-token threshold",
		},
		{
			name: "main above its threshold is allowed", prefs: both, trigger: "auto",
			main: gateTranscript{usage: 160000, age: time.Second}, want: 0,
		},
		{
			name:    "active sub-agent below its threshold is blocked",
			prefs:   both,
			trigger: "auto",
			main:    gateTranscript{usage: 200000, age: 30 * time.Second},
			subs: map[string]gateTranscript{
				"agent-old":  {usage: 120000, age: 20 * time.Second},
				"agent-busy": {usage: 50000, age: 5 * time.Second},
			},
			want:       2,
			wantReason: "pfm compact-gate: blocked auto-compaction of the sub-agent agent-busy at ~50000 tokens, below its 100000-token threshold",
		},
		{
			name: "active sub-agent above its threshold is allowed", prefs: both, trigger: "auto",
			main: gateTranscript{usage: 90000, age: 30 * time.Second},
			subs: map[string]gateTranscript{"agent-busy": {usage: 120000, age: 5 * time.Second}},
			want: 0,
		},
		{
			// A live orchestrator run of ~10 parallel executors: a sub-agent
			// spawned a second earlier was named for another's attempt; below
			// half the window it cannot be the one compacting.
			name:  "a sub-agent too small to be compacting is passed over for the next active one",
			prefs: both, trigger: "auto",
			main: gateTranscript{usage: 90000, age: 30 * time.Second},
			subs: map[string]gateTranscript{
				"agent-big":     {usage: 120000, age: 10 * time.Second},
				"agent-newborn": {usage: 6000, age: time.Second},
			},
			want: 0,
		},
		{
			name:  "sub-agents too small to be compacting leave the main chat the party",
			prefs: both, trigger: "auto",
			main: gateTranscript{usage: 160000, age: 30 * time.Second},
			subs: map[string]gateTranscript{"agent-newborn": {usage: 6000, age: time.Second}},
			want: 0,
		},
		{
			name: "sub-agents all idle past 60 s leave the fresh main as the party", prefs: both, trigger: "auto",
			main: gateTranscript{usage: 160000, age: time.Second},
			subs: map[string]gateTranscript{"agent-done": {usage: 50000, age: 2 * time.Minute}},
			want: 0,
		},
		{
			name:       "idle sub-agents and a main below its threshold judge main",
			prefs:      both,
			trigger:    "auto",
			main:       gateTranscript{usage: 110000, age: time.Second},
			subs:       map[string]gateTranscript{"agent-done": {usage: 120000, age: 2 * time.Minute}},
			want:       2,
			wantReason: "pfm compact-gate: blocked auto-compaction of the main chat at ~110000 tokens, below its 150000-token threshold",
		},
		{
			name:    "an active sub-agent older than the main transcript leaves main the party",
			prefs:   both,
			trigger: "auto",
			main:    gateTranscript{usage: 160000, age: time.Second},
			subs:    map[string]gateTranscript{"agent-busy": {usage: 50000, age: 10 * time.Second}},
			want:    0,
		},
		{
			name: "manual compaction is never blocked", prefs: both, trigger: "manual",
			main: gateTranscript{usage: 90000, age: time.Second}, want: 0,
		},
		{
			name:    "unset thresholds never block",
			prefs:   config.ClaudePrefs{AutoCompactMain: gateMainLimit},
			trigger: "auto",
			main:    gateTranscript{usage: 90000, age: time.Second},
			want:    0,
		},
		{
			name: "bytes after the last usage line count toward the estimate", prefs: both, trigger: "auto",
			main: gateTranscript{usage: 140000, tail: 80000, age: time.Second}, want: 0,
		},
		{
			name:       "the same usage without the unbilled tail is blocked",
			prefs:      both,
			trigger:    "auto",
			main:       gateTranscript{usage: 140000, age: time.Second},
			want:       2,
			wantReason: "pfm compact-gate: blocked auto-compaction of the main chat at ~140000 tokens, below its 150000-token threshold",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mainPath := filepath.Join(dir, "session.jsonl")
			writeGateTranscript(t, mainPath, tc.main)
			for name, spec := range tc.subs {
				writeGateTranscript(t, filepath.Join(dir, "session", "subagents", name+".jsonl"), spec)
			}
			var stderr bytes.Buffer
			got := GateCompaction(
				context.Background(),
				gatePayloadFor(t, mainPath, tc.trigger),
				&stderr,
				tc.prefs,
				clock.NewFake(gateNow),
			)
			if got != tc.want {
				t.Fatalf("exit = %d, want %d (stderr %q)", got, tc.want, stderr.String())
			}
			wantStderr := ""
			if tc.wantReason != "" {
				wantStderr = tc.wantReason + "\n"
			}
			if stderr.String() != wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), wantStderr)
			}
		})
	}
}

// TestGateCompactionFailsOpenAndLogsTheCause pins the fail-open law: a read
// failure allows the compaction and the activity log names why.
func TestGateCompactionFailsOpenAndLogsTheCause(t *testing.T) {
	both := config.ClaudePrefs{AutoCompactMain: gateMainLimit, AutoCompactSubagent: gateSubLimit}
	cases := []struct {
		name    string
		payload func(t *testing.T) *bytes.Reader
		wantErr string
	}{
		{
			name: "unreadable transcript",
			payload: func(t *testing.T) *bytes.Reader {
				return gatePayloadFor(t, filepath.Join(t.TempDir(), "missing.jsonl"), "auto")
			},
			wantErr: "open transcript",
		},
		{
			name:    "payload that does not decode",
			payload: func(*testing.T) *bytes.Reader { return bytes.NewReader([]byte("not json")) },
			wantErr: "invalid character",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			var stderr bytes.Buffer
			if got := GateCompaction(ctx, tc.payload(t), &stderr, both, clock.NewFake(gateNow)); got != 0 {
				t.Fatalf("exit = %d, want 0 (fail-open); stderr %q", got, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want nothing on an allow", stderr.String())
			}
			for _, record := range recorder.Records() {
				cause, found := record.Field(obs.FieldErr)
				decision, _ := record.Field("decision")
				if found && decision == "allow" && strings.Contains(fmt.Sprint(cause), tc.wantErr) {
					return
				}
			}
			t.Fatalf("no allow record carries an err naming %q; log:\n%s", tc.wantErr, recorder.Raw())
		})
	}
}

// After a compaction the older usage lines describe a context that no longer
// exists: the estimate restarts from the compact_boundary's postTokens.
func TestGateCompactionEstimatesFromTheLastCompactBoundary(t *testing.T) {
	main := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"assistant","message":{"usage":{"input_tokens":1000,"cache_read_input_tokens":198000,` +
		`"cache_creation_input_tokens":1000}}}` + "\n" +
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto","preTokens":200000,` +
		`"postTokens":20000}}` + "\n"
	if err := os.WriteFile(main, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := gateNow.Add(-restingAge)
	if err := os.Chtimes(main, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	both := config.ClaudePrefs{AutoCompactMain: gateMainLimit, AutoCompactSubagent: gateSubLimit}
	var stderr bytes.Buffer
	got := GateCompaction(context.Background(), gatePayloadFor(t, main, "auto"), &stderr, both, clock.NewFake(gateNow))
	if got != blockExit || !strings.Contains(stderr.String(), "~200") {
		t.Fatalf("exit %d stderr %q: want a block at ~20000 tokens (the boundary's postTokens), not the stale 200000",
			got, stderr.String())
	}
}

// The live main chat of 2026-09-24: 127728 tokens recorded plus a 118399-byte
// unbilled tail, which Claude Code counted as 141752 at compaction — below the
// 150000 threshold, so the gate must block. At 4 bytes per token the estimate
// read ~157K and let it compact early.
func TestGateCompactionEstimateMatchesClaudeCodesOwnCount(t *testing.T) {
	main := filepath.Join(t.TempDir(), "session.jsonl")
	writeGateTranscript(t, main, gateTranscript{usage: 127728, tail: 118399})
	both := config.ClaudePrefs{AutoCompactMain: gateMainLimit, AutoCompactSubagent: gateSubLimit}
	var stderr bytes.Buffer
	got := GateCompaction(context.Background(), gatePayloadFor(t, main, "auto"), &stderr, both, clock.NewFake(gateNow))
	if got != blockExit || !strings.Contains(stderr.String(), "~142") {
		t.Fatalf("exit %d stderr %q: want a block at ~142K (Claude Code counted 141752)", got, stderr.String())
	}
}
