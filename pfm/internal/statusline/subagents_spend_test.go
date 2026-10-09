package statusline

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/tokens"
)

// Claude Code streams one response as several lines; a line with no message
// id still names its request, and one request is one response, billed once.
func TestReadTranscriptSpendBillsAResponseOncePerRequestID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-x.jsonl")
	line := `{"type":"assistant","requestId":"req_1","message":{"model":"claude-opus-5-5",` +
		`"usage":{"input_tokens":100,"output_tokens":7}}}`
	if err := os.WriteFile(path, []byte(line+"\n"+line+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	spend, err := readTranscriptSpend(path, nil)
	if err != nil {
		t.Fatalf("readTranscriptSpend: %v", err)
	}
	if spend.in != 100 || spend.out != 7 {
		t.Fatalf("spend in/out = %d/%d, want one response's 100/7", spend.in, spend.out)
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

// The statusline's spend and `pfm tokens` price one transcript through the one
// pricing package: the same table yields the same dollars on both surfaces.
func TestSpendAgreesWithPfmTokens(t *testing.T) {
	pricesPath, err := filepath.Abs(filepath.Join("..", "tokens", "testdata", "fixtures", "model-cost.json"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(pricesPath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := pricing.DecodeDocument(content, pricesPath)
	if err != nil {
		t.Fatal(err)
	}
	prices := &pricing.Prices{Table: doc.Table()}
	line := func(id, model string, input, output, read, write5m, write1h int) string {
		return `{"type":"assistant","timestamp":"2026-09-21T05:00:00Z","cwd":"/w/demo","sessionId":"s1",` +
			`"requestId":"req_` + id + `","message":{"id":"` + id + `","model":"` + model + `",` +
			`"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":` + strconv.Itoa(input) +
			`,"output_tokens":` + strconv.Itoa(output) + `,"cache_read_input_tokens":` + strconv.Itoa(read) +
			`,"cache_creation_input_tokens":` + strconv.Itoa(write5m+write1h) + `,"cache_creation":{"ephemeral_5m_input_tokens":` +
			strconv.Itoa(write5m) + `,"ephemeral_1h_input_tokens":` + strconv.Itoa(write1h) + `}}}}`
	}
	root := filepath.Join(t.TempDir(), "projects")
	path := filepath.Join(root, "-w-demo", "s1.jsonl")
	lines := []string{
		line("m1", "claude-sonnet-5", 1000, 100, 2000, 400, 0),
		line("m2", "claude-sonnet-5", 30, 250, 3400, 600, 200),
		line("m2", "claude-sonnet-5", 30, 250, 3400, 600, 200),
		line("m3", "claude-opus-5-5", 12, 900, 4000, 0, 1500),
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spend, err := readTranscriptSpend(path, prices)
	if err != nil || spend.partial || spend.priced != 3 {
		t.Fatalf("statusline spend = %+v, %v; want three priced responses", spend, err)
	}

	out := filepath.Join(t.TempDir(), "audit.json")
	var stdout, stderr bytes.Buffer
	code := tokens.Audit(context.Background(), tokens.Options{
		Args: []string{"--prices", pricesPath, "--out", out}, Stdout: &stdout, Stderr: &stderr,
		Clock: clock.NewFake(time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC)), Env: &paths.MapEnv{HostnameValue: "h"},
		Location: time.UTC, ClaudeRoots: []string{root},
	})
	if code != 0 {
		t.Fatalf("pfm tokens exit %d: %s", code, stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var audit struct {
		USD   map[string]float64 `json:"usd"`
		Calls int                `json:"calls"`
	}
	if err := json.Unmarshal(data, &audit); err != nil {
		t.Fatal(err)
	}
	total := 0.0
	for _, usd := range audit.USD {
		total += usd
	}
	if audit.Calls != 3 || math.Abs(total-spend.usd) > 1e-12 || total == 0 {
		t.Errorf("pfm tokens $%v over %d calls, statusline $%v; want one price", total, audit.Calls, spend.usd)
	}
}
