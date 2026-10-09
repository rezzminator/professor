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
		// a response that consulted the advisor: priced at the advisor's model on both surfaces
		strings.Replace(strings.Replace(advisorResponse("claude-opus-5-5"), `{"type":"assistant",`,
			`{"type":"assistant","timestamp":"2026-09-21T05:00:00Z","cwd":"/w/demo","sessionId":"s1",`, 1),
			"claude-sonnet-5-5", "claude-sonnet-5", 1),
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spend, err := readTranscriptSpend(path, prices)
	if err != nil || spend.partial || spend.priced != 5 || spend.advisorUSD == 0 {
		t.Fatalf("statusline spend = %+v, %v; want four priced responses and one priced advisor", spend, err)
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
		USD     map[string]float64 `json:"usd"`
		Calls   int                `json:"calls"`
		Advisor struct {
			USD float64 `json:"usd"`
		} `json:"advisor"`
	}
	if err := json.Unmarshal(data, &audit); err != nil {
		t.Fatal(err)
	}
	total := 0.0
	for _, usd := range audit.USD {
		total += usd
	}
	if audit.Calls != 4 || math.Abs(total-spend.usd) > 1e-12 || total == 0 ||
		math.Abs(audit.Advisor.USD-spend.advisorUSD) > 1e-12 {
		t.Errorf("pfm tokens $%v (advisor $%v) over %d calls, statusline $%v (advisor $%v); want one price",
			total, audit.Advisor.USD, audit.Calls, spend.usd, spend.advisorUSD)
	}
}

// advisorResponse is a Sonnet response that consulted the Opus advisor once,
// as the harness records it: the top level is the executor's two message
// iterations, the advisor iteration is billed to the advisor model beside them.
func advisorResponse(advisorModel string) string {
	model := ""
	if advisorModel != "" {
		model = `,"model":"` + advisorModel + `"`
	}
	return `{"type":"assistant","requestId":"req_a1","message":{"id":"a1","model":"claude-sonnet-5-5","usage":` +
		`{"input_tokens":4,"cache_creation_input_tokens":1442,"cache_read_input_tokens":175106,"output_tokens":1240,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":552,"ephemeral_1h_input_tokens":0},"iterations":[` +
		`{"input_tokens":2,"output_tokens":26,"cache_read_input_tokens":87277,"cache_creation_input_tokens":552,"type":"message"},` +
		`{"input_tokens":177,"output_tokens":1422,"cache_read_input_tokens":0,"cache_creation_input_tokens":89334,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":89334,"ephemeral_1h_input_tokens":0},"type":"advisor_message"` + model + `},` +
		`{"input_tokens":2,"output_tokens":1214,"cache_read_input_tokens":87829,"cache_creation_input_tokens":890,"type":"message"}]}}}`
}

// An advisor consultation is priced at the advisor's own model and counted in
// the agent's spend, apart from the executor's own dollars.
func TestReadTranscriptSpendPricesTheAdvisorAtItsOwnModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-x.jsonl")
	if err := os.WriteFile(path, []byte(advisorResponse("claude-opus-5-5")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	spend, err := readTranscriptSpend(path, testPrices(t))
	if err != nil {
		t.Fatalf("readTranscriptSpend: %v", err)
	}
	executor := (4*2.0 + 1240*10.0 + 175106*0.2 + 1442*2.5) / 1e6
	advisor := (177*4.0 + 1422*20.0 + 89334*5.0) / 1e6
	if math.Abs(spend.advisorUSD-advisor) > 1e-12 || math.Abs(spend.usd-(executor+advisor)) > 1e-12 {
		t.Errorf(
			"usd %v advisor %v; want %v = executor %v + advisor %v",
			spend.usd,
			spend.advisorUSD,
			executor+advisor,
			executor,
			advisor,
		)
	}
	if spend.in != 4+1442+175106+177+89334 || spend.out != 1240+1422 || spend.partial {
		t.Errorf(
			"in/out %d/%d partial %v; want the advisor's tokens counted with the executor's",
			spend.in,
			spend.out,
			spend.partial,
		)
	}
	plain := spendSegment(agentActivity{spend: agentSpend{usd: 1.5, advisorUSD: 0.25, priced: 1}})
	if got := stripANSICodes(plain); !strings.Contains(got, "$1.50/0/0/0 adv $0.25") {
		t.Errorf("segment %q; want the advisor's dollars shown as advisor spend", got)
	}
	if got := stripANSICodes(
		spendSegment(agentActivity{spend: agentSpend{usd: 1.5, priced: 1}}),
	); strings.Contains(
		got,
		"adv",
	) {
		t.Errorf("segment %q; want no advisor mark without advisor spend", got)
	}
}

// An advisor model the table cannot price leaves the sum a floor, never a
// quiet low number, and the executor's own dollars are still counted.
func TestReadTranscriptSpendMarksAnUnpricedAdvisorAsAFloor(t *testing.T) {
	for _, model := range []string{"claude-nonesuch-9", ""} {
		path := filepath.Join(t.TempDir(), "agent-x.jsonl")
		if err := os.WriteFile(path, []byte(advisorResponse(model)+"\n"), 0o600); err != nil {
			t.Fatalf("write transcript: %v", err)
		}
		spend, err := readTranscriptSpend(path, testPrices(t))
		if err != nil {
			t.Fatalf("readTranscriptSpend: %v", err)
		}
		executor := (4*2.0 + 1240*10.0 + 175106*0.2 + 1442*2.5) / 1e6
		if !spend.partial || spend.advisorUSD != 0 || math.Abs(spend.usd-executor) > 1e-12 {
			t.Errorf("advisor model %q: %+v; want a partial sum of the executor's %v", model, spend, executor)
		}
		if got := stripANSICodes(spendSegment(agentActivity{spend: spend})); !strings.Contains(got, " adv n/a") ||
			strings.Contains(got, "adv $") {
			t.Errorf("advisor model %q: segment %q; want the advisor's dollars as n/a, never $0", model, got)
		}
	}
}

// An iteration type pfm does not know may have billed what the top level does
// not carry: the sum is a floor, never a quiet total.
func TestReadTranscriptSpendMarksAnUnknownIterationAsAFloor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-x.jsonl")
	line := `{"type":"assistant","requestId":"req_f1","message":{"id":"f1","model":"claude-sonnet-5-5","usage":` +
		`{"input_tokens":1000,"output_tokens":100,"iterations":[` +
		`{"input_tokens":900,"output_tokens":40,"type":"message","model":"claude-fable-5-1"},` +
		`{"input_tokens":1000,"output_tokens":100,"type":"fallback_message","model":"claude-sonnet-5-5"}]}}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	spend, err := readTranscriptSpend(path, testPrices(t))
	if err != nil {
		t.Fatalf("readTranscriptSpend: %v", err)
	}
	if !spend.partial || spend.priced != 1 || math.Abs(spend.usd-(1000*2.0+100*10.0)/1e6) > 1e-12 {
		t.Errorf("%+v; want the top level priced and the sum a floor", spend)
	}
}
