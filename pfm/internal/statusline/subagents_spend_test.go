package statusline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/pricing"
)

func TestBilledUsageFoldsIterationsAndSplitsWritesByTTL(t *testing.T) {
	cases := []struct {
		name  string
		usage billedUsage
		want  pricing.ClaudeUsage
	}{
		{
			name:  "a write with no breakdown is the 5-minute default",
			usage: billedUsage{Input: 1, Output: 2, CacheRead: 3, CacheCreation: 40},
			want:  pricing.ClaudeUsage{Input: 1, Output: 2, CacheRead: 3, Write5m: 40},
		},
		{
			name: "the breakdown splits; what it does not cover is 5-minute",
			usage: billedUsage{CacheCreation: 100, Breakdown: &struct {
				Write5m int64 `json:"ephemeral_5m_input_tokens"`
				Write1h int64 `json:"ephemeral_1h_input_tokens"`
			}{Write5m: 10, Write1h: 60}},
			want: pricing.ClaudeUsage{Write5m: 40, Write1h: 60},
		},
		{
			name: "an empty top level carries its counts in iterations",
			usage: billedUsage{Iterations: []billedUsage{
				{Input: 5, Output: 7, CacheCreation: 9},
				{Input: 1, CacheRead: 300},
			}},
			want: pricing.ClaudeUsage{Input: 6, Output: 7, CacheRead: 300, Write5m: 9},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.usage.billed(); got != tc.want {
				t.Fatalf("billed() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A row's spend is the agent's own billing plus every agent's below it, at
// any depth; a floor it cannot vouch for says "+?", never a quiet low number.
func TestRenderSubagentsSpendCoversTheWholeTree(t *testing.T) {
	const (
		// $3000e-6 on sonnet-5-5: 1000 in at $2, 100 out at $10.
		childResponse = `{"type":"assistant","message":{"id":"c-1","model":"claude-sonnet-5-5",` +
			`"usage":{"input_tokens":1000,"output_tokens":100}}}`
		// $8 on opus-5-5: one million tokens written to the 1-hour cache at $8.
		grandchildResponse = `{"type":"assistant","message":{"id":"g-1","model":"claude-opus-5-5","usage":` +
			`{"cache_creation_input_tokens":1000000,"cache_creation":{"ephemeral_1h_input_tokens":1000000}}}}`
		unpricedResponse = `{"type":"assistant","message":{"id":"u-1","model":"claude-nonesuch-9",` +
			`"usage":{"input_tokens":500,"output_tokens":50}}}`
	)
	cases := []struct {
		name        string
		transcripts map[string][]string
		parents     map[string]string
		want        string // what follows the gauge, ANSI stripped
		warn        string
	}{
		{
			name:        "solo: its own two responses, the repeated one once",
			transcripts: map[string][]string{"p": agentTranscriptLines},
			parents:     map[string]string{"p": ""},
			want:        "$0.01/2.0K/35/2",
		},
		{
			name: "a streamed response counts its last line, the final output",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {
				`{"type":"assistant","message":{"id":"s-1","model":"claude-sonnet-5-5","usage":{"input_tokens":1000,"output_tokens":8}}}`,
				`{"type":"assistant","message":{"id":"s-1","model":"claude-sonnet-5-5","usage":{"input_tokens":1000,"output_tokens":100}}}`,
				turnEnded,
			}},
			parents: map[string]string{"p": "", "c1": "p"},
			want:    "$0.01/3.0K/135/2",
		},
		{
			name: "a child and a grandchild add in",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {childResponse, turnEnded}, "g1": {grandchildResponse, turnEnded},
			},
			parents: map[string]string{"p": "", "c1": "p", "g1": "c1"},
			want:    "$8.01/1.0M/135/2",
		},
		{
			name:        "a model the table cannot price leaves a floor",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {unpricedResponse, turnEnded}},
			parents:     map[string]string{"p": "", "c1": "p"},
			want:        "$0.01/2.5K/85/2+?",
		},
		{
			name:        "a child transcript that cannot be read leaves a floor and its cause",
			transcripts: map[string][]string{"p": agentTranscriptLines},
			parents:     map[string]string{"p": "", "c1": "p"},
			want:        "$0.01/2.0K/35/2+?",
			warn:        "open sub-agent transcript",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := nestedSession(t, tc.transcripts, tc.parents)
			got, warned := renderOneSubagent(t, session, parentTask)
			if !strings.Contains(got, "1.0K/1.0M "+tc.want+"│") {
				t.Fatalf("content = %q, want the gauge followed by %q", got, tc.want)
			}
			if tc.warn != "" && !strings.Contains(warned, tc.warn) {
				t.Fatalf("warn = %q, want %q", warned, tc.warn)
			}
		})
	}
}

// With no price table every row still counts its tokens; the dollars say "$?".
func TestRenderSubagentsSpendWithoutPrices(t *testing.T) {
	session := nestedSession(t, map[string][]string{"p": agentTranscriptLines}, map[string]string{"p": ""})
	payload := `{"transcript_path":` + jsonText(session) + `,"cwd":"/work/repo","tasks":[` + parentTask + `]}`
	got, err := RenderSubagents([]byte(payload), subagentNow, t.TempDir(), nil, &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	var row subagentRow
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &row); err != nil {
		t.Fatalf("row is not JSON: %v: %q", err, got)
	}
	if content := stripANSICodes(row.Content); !strings.Contains(content, "1.0K/1.0M $?/2.0K/35/2│") {
		t.Fatalf("content = %q, want $?/2.0K/35/2 after the gauge", content)
	}
}
