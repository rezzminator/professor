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

// The main line's dollars come from the same table and transcripts as its
// tokens and every sub-agent row, so the line and the rows can be compared;
// Claude Code's own figure follows as "cc $…" only when it disagrees by more
// than a tenth, or when pfm cannot price the session itself.
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
			name: "claude code overprices: its figure follows", child: childResponse, claudeCost: 37.31,
			want: "💰$8.00/1.0M/100/1 cc $37.31",
		},
		{
			name: "claude code agrees within a tenth: no second figure", child: childResponse, claudeCost: 8.5,
			want: "💰$8.00/1.0M/100/1",
		},
		{
			name: "no price table: unpriced, claude code's figure follows", child: childResponse, noPrices: true,
			claudeCost: 37.31, want: "💰$?/1.0M/100/1 cc $37.31",
		},
		{
			name: "an unpriced model leaves a floor", child: unpricedResponse, claudeCost: 8.01,
			want: "💰$8.00/1.0M/50/0+?",
		},
		{
			name: "no transcript: claude code's figure is all there is", noSession: true, claudeCost: 3.42,
			want: "💰$?/?/?/? cc $3.42",
		},
		{
			name: "claude code reports nothing: no second figure", child: childResponse,
			want: "💰$8.00/1.0M/100/1",
		},
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
