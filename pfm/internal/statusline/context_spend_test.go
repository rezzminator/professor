package statusline

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// The main line's dollars are Claude Code's whole-session total; the share no
// transcript logged is named beside it, so the line and the rows — priced
// from the transcripts at the same table — add up.
func TestSessionSpendSegmentPricesFromTheTable(t *testing.T) {
	const (
		// $8 on opus-5-5: one million tokens written to the 1-hour cache at $8.
		mainResponse = `{"type":"assistant","message":{"id":"m-1","model":"claude-opus-5-5","usage":` +
			`{"cache_creation_input_tokens":1000000,"cache_creation":{"ephemeral_1h_input_tokens":1000000}}}}`
		// $0.003 on sonnet-5-5: 1000 in at $2, 100 out at $10.
		childResponse = `{"type":"assistant","message":{"id":"c-1","model":"claude-sonnet-5-5",` +
			`"content":[{"type":"tool_use","id":"t1","name":"Read"}],"usage":{"input_tokens":1000,"output_tokens":100}}}`
		unpricedResponse = `{"type":"assistant","message":{"id":"u-1","model":"claude-nonesuch-9",` +
			`"usage":{"input_tokens":500,"output_tokens":50}}}`
	)
	session := func(t *testing.T, child string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "session.jsonl")
		dir := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(mainResponse+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(transcript.SubagentTranscript(dir, "c1"), []byte(child+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			transcript.SubagentMetaPath(transcript.SubagentTranscript(dir, "c1")),
			[]byte(`{"agentType":"rr-pro"}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		return path
	}
	cases := []struct {
		name       string
		child      string
		noPrices   bool
		noSession  bool
		claudeCost float64
		want       string
	}{
		{
			name: "the total holds requests no transcript logged", child: childResponse, claudeCost: 37.31,
			want: "💰$37.31/1.0M/100/1 +$29.31 unlogged",
		},
		{
			name: "the total matches the logs", child: childResponse, claudeCost: 8.003,
			want: "💰$8.00/1.0M/100/1",
		},
		{
			name: "a logged price above the total is pfm's error", child: childResponse, claudeCost: 5,
			want: "💰$5.00/1.0M/100/1 logged $8.00 > total",
		},
		{
			name: "no claude code figure: the logged price leads", child: childResponse,
			want: "💰$8.00/1.0M/100/1",
		},
		{
			name: "no price table: the total, no gap", child: childResponse, noPrices: true, claudeCost: 37.31,
			want: "💰$37.31/1.0M/100/1",
		},
		{
			name: "an unpriced model leaves a floor", child: unpricedResponse, claudeCost: 9,
			want: "💰$9.00/1.0M/50/0+? +$1.00 unlogged",
		},
		{
			name: "no transcript: the total alone", noSession: true, claudeCost: 3.42,
			want: "💰$3.42/?/?/?",
		},
		{name: "no transcript, no total", noSession: true, want: "💰$?/?/?/?"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var data input
			data.Cost.TotalCostUSD = tc.claudeCost
			if !tc.noSession {
				data.TranscriptPath = session(t, tc.child)
			}
			var prices *pricing.Prices
			if !tc.noPrices {
				prices = testPrices(t)
			}
			got := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(sessionSpendSegment(data, prices), "")
			if got != tc.want {
				t.Fatalf("segment = %q, want %q", got, tc.want)
			}
		})
	}
}

// The context gauge divides by the window Claude Code reports for the model
// it runs; the model-name guess serves only a payload that names none.
func TestContextWindowTakesTheHarnessSize(t *testing.T) {
	cases := []struct {
		name  string
		size  int64
		model string
		want  int64
	}{
		{name: "reported 200K on an opus", size: 200_000, model: "claude-opus-5-5", want: 200_000},
		{name: "reported 1M on a haiku", size: 1_000_000, model: "claude-haiku-5-5", want: 1_000_000},
		{name: "unreported haiku guesses 200K", model: "claude-haiku-5-5", want: 200_000},
		{name: "unreported opus guesses 1M", model: "claude-opus-5-5", want: 1_000_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var data input
			data.ContextWindow.ContextWindowSize = tc.size
			data.Model.ID = tc.model
			if got := contextWindow(Runtime{Env: map[string]string{}}, data, transcript.Meta{}); got != tc.want {
				t.Fatalf("contextWindow = %d, want %d", got, tc.want)
			}
		})
	}
}
