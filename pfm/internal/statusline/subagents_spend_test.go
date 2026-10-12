package statusline

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/costledger"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
			name:        "a lone agent: its own two responses, the repeated one once",
			transcripts: map[string][]string{"p": agentTranscriptLines},
			parents:     map[string]string{"p": ""},
			want:        "Σ$0.01/2.0K/35/2",
		},
		{
			name: "a streamed response counts its last line, the final output",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {
				`{"type":"assistant","message":{"id":"s-1","model":"claude-sonnet-5-5","usage":{"input_tokens":1000,"output_tokens":8}}}`,
				`{"type":"assistant","message":{"id":"s-1","model":"claude-sonnet-5-5","usage":{"input_tokens":1000,"output_tokens":100}}}`,
				turnEnded,
			}},
			parents: map[string]string{"p": "", "c1": "p"},
			want:    "Σ$0.01/3.0K/135/2",
		},
		{
			name: "a child and a grandchild add in",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {childResponse, turnEnded}, "g1": {grandchildResponse, turnEnded},
			},
			parents: map[string]string{"p": "", "c1": "p", "g1": "c1"},
			want:    "Σ$8.01/1.0M/135/2",
		},
		{
			name:        "a model the table cannot price leaves a floor",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {unpricedResponse, turnEnded}},
			parents:     map[string]string{"p": "", "c1": "p"},
			want:        "Σ$0.01/2.5K/85/2+?",
		},
		{
			name:        "a child transcript that cannot be read leaves a floor and its cause",
			transcripts: map[string][]string{"p": agentTranscriptLines},
			parents:     map[string]string{"p": "", "c1": "p"},
			want:        "Σ$0.01/2.0K/35/2+?",
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
	got, err := RenderSubagents([]byte(payload), subagentNow, t.TempDir(), nil, "", noEnv, "", &strings.Builder{})
	if err != nil {
		t.Fatal(err)
	}
	var row subagentRow
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &row); err != nil {
		t.Fatalf("row is not JSON: %v: %q", err, got)
	}
	if content := stripANSICodes(row.Content); !strings.Contains(content, "1.0K/1.0M Σ$?/2.0K/35/2│") {
		t.Fatalf("content = %q, want Σ$?/2.0K/35/2 after the gauge", content)
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
		// a gpt model through the model-gateway: Anthropic-shaped usage, priced at its Codex row on both surfaces
		line("m4", "gpt-5.6-sol", 1000, 100, 2000, 400, 0),
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
	if err != nil || spend.partial || spend.priced != 6 || spend.advisorUSD == 0 {
		t.Fatalf("statusline spend = %+v, %v; want five priced responses and one priced advisor", spend, err)
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
	if audit.Calls != 5 || math.Abs(total-spend.usd) > 1e-12 || total == 0 ||
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

const (
	// ledgerSampleSession is the session of costledger's sample-15.jsonl: 15 rows, $1.2941 — the
	// main thread $0.340251 and the top-level agent a..02 with its children a..01 and a..03
	// $0.953849 (a..02 alone $0.787435, a..01 $0.078574, a..03 $0.08784).
	ledgerSampleSession = "00000000-0000-4000-8000-0000000000a1"
	ledgerAgentRoot     = "a0000000000000002"
	ledgerAgentFirst    = "a0000000000000001"
	ledgerAgentSecond   = "a0000000000000003"
)

// useLedgerSample copies the sample ledger into a fresh ledger directory and returns it; a render
// reaches it only through the env ledgerEnv builds.
func useLedgerSample(t *testing.T) string {
	t.Helper()
	sample, err := os.ReadFile(filepath.Join("..", "costledger", "testdata", "sample-15.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ledgerSampleSession+".jsonl"), sample, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func ledgerTask(id string) string {
	return `{"id":"` + id + `","type":"local_agent","status":"running","startTime":1790244000000,` +
		`"model":"claude-opus-5-5","contextWindowSize":1000000,"tokenCount":1000}`
}

// renderLedgerRows renders the tasks of one session and returns each row's content, ANSI stripped.
func renderLedgerRows(t *testing.T, ledgerDir, sessionID, session string, ids ...string) map[string]string {
	t.Helper()
	tasks := make([]string, 0, len(ids))
	for _, id := range ids {
		tasks = append(tasks, ledgerTask(id))
	}
	payload := `{"session_id":` + jsonText(sessionID) + `,"transcript_path":` + jsonText(session) +
		`,"cwd":"/work/repo","tasks":[` + strings.Join(tasks, ",") + `]}`
	var warn bytes.Buffer
	got, err := RenderSubagents(
		[]byte(payload),
		subagentNow,
		t.TempDir(),
		testPrices(t),
		"",
		ledgerEnv(ledgerDir),
		"",
		&warn,
	)
	if err != nil {
		t.Fatalf("RenderSubagents: %v", err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		var row subagentRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("row is not JSON: %v: %q (warn %q)", err, line, warn.String())
		}
		rows[row.ID] = stripANSICodes(row.Content)
	}
	return rows
}

// With a ledger a row's dollars and tokens are the ledger rows of that agent and every agent below
// it, side calls included, the tree taken from the rows' parent_agent_id (agent a..03 has no meta
// file at all) and the meta files. The main line's one dollar amount is the ledger total, and it is
// the main thread's own rows plus every top-level row.
func TestRenderSubagentsPricesRowsFromTheLedger(t *testing.T) {
	ledgerDir := useLedgerSample(t)
	session := nestedSession(t,
		map[string][]string{ledgerAgentRoot: agentTranscriptLines, ledgerAgentFirst: agentTranscriptLines},
		map[string]string{ledgerAgentRoot: "", ledgerAgentFirst: ledgerAgentRoot})
	rows := renderLedgerRows(
		t,
		ledgerDir,
		ledgerSampleSession,
		session,
		ledgerAgentRoot,
		ledgerAgentFirst,
		ledgerAgentSecond,
	)
	for id, want := range map[string]string{
		ledgerAgentRoot:   "Σ$0.95/740.0K/3.2K/",
		ledgerAgentFirst:  "Σ$0.08/224.3K/1.2K/2",
		ledgerAgentSecond: "Σ$0.09/224.5K/1.4K/?",
	} {
		if !strings.Contains(rows[id], "1.0K/1.0M "+want) {
			t.Errorf("row %s = %q, want the gauge followed by %q", id, rows[id], want)
		}
	}

	var mainRows float64
	ledger, err := costledger.ForSession(ledgerDir, ledgerSampleSession)
	if err != nil || len(ledger.Rows) != 15 {
		t.Fatalf("sample ledger = %d rows, %v; want 15", len(ledger.Rows), err)
	}
	for _, row := range ledger.Rows {
		if row.AgentID == "" {
			mainRows += float64(*row.CostUSDMicros) / 1e6
		}
	}
	data := input{SessionID: ledgerSampleSession}
	runtime := Runtime{
		Home:   t.TempDir(),
		Engine: pfmengine.Claude,
		Prices: testPrices(t),
		Env: map[string]string{
			paths.EnvCostLedgerDir:     ledgerDir,
			claudelaunch.EnvCostLedger: "on",
		},
	}
	_, line, _ := renderContextLine(runtime, data, "", subagentNow)
	amount := regexp.MustCompile(`\$(\d+\.\d{2})`)
	totalText := amount.FindStringSubmatch(stripANSICodes(line))
	topText := amount.FindStringSubmatch(rows[ledgerAgentRoot])
	if totalText == nil || topText == nil {
		t.Fatalf("no dollar amount: main line %q, root row %q", stripANSICodes(line), rows[ledgerAgentRoot])
	}
	total, _ := strconv.ParseFloat(totalText[1], 64)
	top, _ := strconv.ParseFloat(topText[1], 64)
	if math.Abs(mainRows+top-total) > 0.0051 {
		t.Errorf("main rows $%.4f + top-level row $%.2f != ledger total $%.2f on the main line", mainRows, top, total)
	}
}

// An agent the ledger has no row for yet (its calls reach the ledger up to 20 s late) keeps today's
// transcript pricing; an advisor consulted by a ledger-priced agent stays its own "adv $X".
func TestRenderSubagentsLedgerRowFallsBackAndKeepsTheAdvisor(t *testing.T) {
	dir := useLedgerSample(t)
	if err := costledger.Append(dir, costledger.Row{
		RequestID: "req_adv", TS: "2026-10-09T18:40:00.000Z", SessionID: ledgerSampleSession, AgentID: "adv1",
		QuerySource: "agent:builtin:general-purpose", Model: "claude-sonnet-5-5", InputTokens: 10, OutputTokens: 20,
		CostUSDMicros: micros(1_000_000), Join: costledger.JoinLogSpan,
	}); err != nil {
		t.Fatal(err)
	}
	session := nestedSession(t,
		map[string][]string{"fresh": agentTranscriptLines, "adv1": {advisorResponse("claude-opus-5-5")}},
		map[string]string{"fresh": "", "adv1": ""})
	rows := renderLedgerRows(t, dir, ledgerSampleSession, session, "fresh", "adv1")
	if want := "1.0K/1.0M Σ$0.01/2.0K/35/2"; !strings.Contains(rows["fresh"], want) {
		t.Errorf("row without ledger rows = %q, want transcript pricing %q", rows["fresh"], want)
	}
	if want := "1.0K/1.0M Σ$1.00/10/20/0 adv $0.48"; !strings.Contains(rows["adv1"], want) {
		t.Errorf("ledger-priced row = %q, want %q", rows["adv1"], want)
	}
}

// A sub-agent that ran through the model-gateway's chatgpt route leaves a
// Claude Code transcript whose usage the gateway wrote Anthropic-shaped
// (input_tokens = total - cached - written): the row prices it at its Codex
// row's rates, input and a cache write at in, cache read at cached, output at
// out (the tiers are pricing's TestResponseRequestsCostParts).
func TestRenderSubagentsSpendPricesAGatewayGptAtItsCodexRow(t *testing.T) {
	gpt := func(usage string) []string {
		return []string{
			`{"type":"assistant","message":{"id":"g-1","model":"gpt-6-sol","usage":` + usage + `}}`, turnEnded,
		}
	}
	const task = `{"id":"p","type":"local_agent","status":"completed","startTime":1790244000000,` +
		`"model":"gpt-6-sol","contextWindowSize":1000000,"tokenCount":1000,"label":"orchestrate"}`
	for _, tc := range []struct {
		name  string
		lines []string
		want  string // what follows the gauge, ANSI stripped
	}{
		{
			// 100K+10K at $2, 150K read at $0.2, 1M out at $10: 0.22 + 0.03 + 10.
			name: "the base tier, a cache write billed as input",
			lines: gpt(`{"input_tokens":100000,"output_tokens":1000000,"cache_read_input_tokens":150000,` +
				`"cache_creation_input_tokens":10000}`),
			want: "Σ$10.25/260.0K/1.0M/0",
		},
		{
			name: "a model no row prices stays unpriced",
			lines: []string{
				`{"type":"assistant","message":{"id":"g-1","model":"gpt-nonesuch-9","usage":` +
					`{"input_tokens":1000,"output_tokens":100}}}`, turnEnded,
			},
			want: "Σ$?/1.0K/100/0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := nestedSession(t, map[string][]string{"p": tc.lines}, map[string]string{"p": ""})
			// the row is read inside the minute after the turn ended, before a finished row collapses
			got, _ := renderSubagentAt(t, session, task, subagentStart.Add(90*time.Second))
			if !strings.Contains(got, "1.0K/1.0M "+tc.want+"│") {
				t.Fatalf("content = %q, want the gauge followed by %q", got, tc.want)
			}
		})
	}
}

// noEnv is an empty closed environment: a render under it can reach no ledger the test did not build.
var noEnv paths.Env = &paths.MapEnv{Values: map[string]string{}}

// ledgerEnv is the closed environment whose cost ledger directory is dir.
func ledgerEnv(dir string) paths.Env {
	return &paths.MapEnv{Values: map[string]string{paths.EnvCostLedgerDir: dir}}
}

// The sub-agent rows resolve the ledger from the env they are handed, never the process's: a decoy
// ledger in the process environment (the real one a test must not reach) is not read, the injected
// one is. The same ledger is read by the main line through its Runtime, so the totals add up.
func TestRenderSubagentsReadsTheLedgerUnderTheInjectedEnv(t *testing.T) {
	decoy := t.TempDir()
	t.Setenv(paths.EnvCostLedgerDir, decoy)
	ledgerDir := useLedgerSample(t)
	session := nestedSession(t,
		map[string][]string{ledgerAgentRoot: agentTranscriptLines},
		map[string]string{ledgerAgentRoot: ""})
	rows := renderLedgerRows(t, ledgerDir, ledgerSampleSession, session, ledgerAgentRoot)
	if want := "$0.95/740.0K/3.2K/"; !strings.Contains(rows[ledgerAgentRoot], want) {
		t.Fatalf("row = %q, want the injected ledger's %q", rows[ledgerAgentRoot], want)
	}
}
