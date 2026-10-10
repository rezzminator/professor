package statusline

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/costledger"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

const (
	spendSession = "11111111-1111-4111-8111-111111111111"
	// $8 on opus-5-5: one million tokens written to the 1-hour cache at $8.
	spendMainResponse = `{"type":"assistant","message":{"id":"m-1","model":"claude-opus-5-5","usage":` +
		`{"cache_creation_input_tokens":1000000,"cache_creation":{"ephemeral_1h_input_tokens":1000000}}}}`
	// $0.003 on sonnet-5-5: 1000 in at $2, 100 out at $10.
	spendChildResponse = `{"type":"assistant","message":{"id":"c-1","model":"claude-sonnet-5-5",` +
		`"content":[{"type":"tool_use","id":"t1","name":"Read"}],"usage":{"input_tokens":1000,"output_tokens":100}}}`
	spendUnpricedResponse = `{"type":"assistant","message":{"id":"u-1","model":"claude-nonesuch-9",` +
		`"usage":{"input_tokens":500,"output_tokens":50}}}`
)

// spendTranscript writes a session transcript with one sub-agent beside it; lines follow the main
// thread's response in it.
func spendTranscript(t *testing.T, child string, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	dir := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := spendMainResponse + "\n" + strings.Join(append(lines, ""), "\n")
	if len(lines) == 0 {
		body = spendMainResponse + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
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

func micros(value int64) *int64 { return &value }

// stampLine is a transcript line that carries only a timestamp; costStateLine is the line Claude
// Code writes at a graceful exit.
func stampLine(at string) string { return `{"type":"user","timestamp":"` + at + `"}` }

func costStateLine(usd string) string {
	return `{"type":"cost-state","sessionId":"` + spendSession + `","totalCostUSD":` + usd + `}`
}

// costStateExiting is a cost-state whose clock fields say the launch ended at exit (startTime +
// totalDuration, Claude Code's wall clock of the exit), whatever the last stamp before it says.
func costStateExiting(usd string, exitMillis int64) string {
	return `{"type":"cost-state","sessionId":"` + spendSession + `","totalCostUSD":` + usd +
		`,"totalDuration":1000,"startTime":` + strconv.FormatInt(exitMillis-1000, 10) + `}`
}

// spendRows is a ledger of this session: the main thread's one call and the
// sub-agent c1's, $8.003 together, 1.0M prompt and 5.1K output tokens.
func spendRows() []costledger.Row {
	return []costledger.Row{
		{
			RequestID:           "req_main",
			TS:                  "2026-10-09T18:00:00.000Z",
			SessionID:           spendSession,
			QuerySource:         "repl_main_thread",
			Model:               "claude-opus-5-5",
			InputTokens:         100_000,
			CacheReadTokens:     800_000,
			CacheCreationTokens: 100_000,
			OutputTokens:        5_000,
			CostUSDMicros:       micros(8_000_000),
			Join:                costledger.JoinLogSpan,
		},
		{
			RequestID: "req_c1", TS: "2026-10-09T18:00:01.000Z", SessionID: spendSession, AgentID: "c1",
			QuerySource: "agent:builtin:general-purpose", Model: "claude-sonnet-5-5", InputTokens: 1_000,
			OutputTokens: 100, CostUSDMicros: micros(3_000), Join: costledger.JoinLogSpan,
		},
	}
}

func writeSpendLedger(t *testing.T, dir string, rows []costledger.Row) {
	t.Helper()
	for index := range rows {
		if err := costledger.Append(dir, rows[index]); err != nil {
			t.Fatalf("write ledger row %s: %v", rows[index].RequestID, err)
		}
	}
}

// The main line renders exactly ONE dollar amount in every state: the ledger's
// session total when there is a ledger, Claude Code's total when there is
// none. A state the line must flag carries a marker with no amount in it, one
// distinct text per state.
func TestMainLineRendersOneDollarAmountInEveryState(t *testing.T) {
	cases := []struct {
		name       string
		rows       []costledger.Row // nil: no ledger file
		unreadable bool             // the ledger path is a directory: it cannot be read
		marker     string           // the launch's PFM_COST_LEDGER
		child      string
		noPrices   bool
		noSession  bool
		claudeCost float64
		lines      []string // main transcript lines after the first response: stamps and cost-state lines
		want       string   // the line from the money bag on
	}{
		{
			name: "ledger equals claude code", rows: spendRows(), marker: "on", child: spendChildResponse,
			claudeCost: 8.003, want: "💰$8.00/1.0M/5.1K/1",
		},
		{
			name:       "ledger within the tolerance of claude code",
			rows:       spendRows(),
			marker:     "on",
			child:      spendChildResponse,
			claudeCost: 8.01,
			want:       "💰$8.00/1.0M/5.1K/1",
		},
		{
			name:       "ledger below claude code beyond the tolerance: its total stands, nothing is flagged",
			rows:       spendRows(),
			marker:     "on",
			child:      spendChildResponse,
			claudeCost: 9.5,
			want:       "💰$9.50/1.0M/5.1K/1",
		},
		{
			name: "ledger above claude code beyond the tolerance", rows: spendRows(), marker: "on",
			child: spendChildResponse, claudeCost: 5, want: "💰$8.00/1.0M/5.1K/1",
		},
		{
			name: "no claude code figure: the ledger leads, nothing to compare", rows: spendRows(), marker: "on",
			child: spendChildResponse, want: "💰$8.00/1.0M/5.1K/1",
		},
		{
			name: "a call whose cost never arrived leaves a floor", marker: "on", child: spendChildResponse,
			rows: append(spendRows(), costledger.Row{
				RequestID: "req_late", TS: "2026-10-09T18:00:02.000Z", SessionID: spendSession, AgentID: "c1",
				QuerySource: "repl_main_thread", Join: costledger.JoinSpan,
			}),
			claudeCost: 8.003, want: "💰$8.00/1.0M/5.1K/1+?",
		},
		{
			name: "ledger without a transcript: tools unknown", rows: spendRows(), marker: "on", noSession: true,
			claudeCost: 8.003, want: "💰$8.00/1.0M/5.1K/?",
		},
		{
			name: "ledger with the sink failed since: named", rows: spendRows(), marker: "failed",
			child: spendChildResponse, claudeCost: 8.003, want: "💰$8.00/1.0M/5.1K/1 sink failed",
		},
		{
			name: "no ledger, sink failed", marker: "failed", child: spendChildResponse, claudeCost: 37.31,
			want: "💰$37.31/1.0M/100/1 sink failed",
		},
		{
			name: "no ledger, pre-empted by the user's own telemetry", marker: "preempted", child: spendChildResponse,
			claudeCost: 37.31, want: "💰$37.31/1.0M/100/1 ledger preempted",
		},
		{
			name: "no ledger, switched off", marker: "off", child: spendChildResponse, claudeCost: 37.31,
			want: "💰$37.31/1.0M/100/1 ledger off",
		},
		{
			name: "no ledger, transcripts price below claude code", marker: "on", child: spendChildResponse,
			claudeCost: 37.31, want: "💰$37.31/1.0M/100/1 unlogged",
		},
		{
			name: "no ledger, no launch marker, transcripts below claude code", child: spendChildResponse,
			claudeCost: 37.31, want: "💰$37.31/1.0M/100/1 unlogged",
		},
		{
			name: "no ledger, transcripts match claude code", child: spendChildResponse, claudeCost: 8.003,
			want: "💰$8.00/1.0M/100/1",
		},
		{
			name: "no ledger, transcripts above claude code: its total stands", child: spendChildResponse,
			claudeCost: 5, want: "💰$5.00/1.0M/100/1",
		},
		{
			name: "no ledger, no claude code figure: the logged price leads", child: spendChildResponse,
			want: "💰$8.00/1.0M/100/1",
		},
		{
			name: "no ledger, no price table: claude code's total, no gap", child: spendChildResponse, noPrices: true,
			claudeCost: 37.31, want: "💰$37.31/1.0M/100/1",
		},
		{
			name: "no ledger, an unpriced model leaves a floor", child: spendUnpricedResponse, claudeCost: 9,
			want: "💰$9.00/1.0M/50/0+? unlogged",
		},
		{
			name: "no ledger and no transcript: the total alone", noSession: true, claudeCost: 3.42,
			want: "💰$3.42/?/?/?",
		},
		{name: "no ledger, no transcript, no total", noSession: true, want: "💰$?/?/?/?"},
		{
			name:       "resumed, the launch before fully ledgered: the ledger is the whole total, nothing is flagged",
			rows:       spendRows(),
			marker:     "on",
			child:      spendChildResponse,
			claudeCost: 8.003,
			lines: []string{
				stampLine("2026-10-09T18:00:02.000Z"),
				costStateLine("8.003"),
				stampLine("2026-10-09T19:00:00.000Z"),
			},
			want: "💰$8.00/1.0M/5.1K/1",
		},
		{
			name:       "resumed, a call just landed and the ledger lags: claude code's total leads, nothing is flagged",
			rows:       spendRows(),
			marker:     "on",
			child:      spendChildResponse,
			claudeCost: 8.6,
			lines: []string{
				stampLine("2026-10-09T18:00:02.000Z"),
				costStateLine("8.003"),
				stampLine("2026-10-09T19:00:00.000Z"),
			},
			want: "💰$8.60/1.0M/5.1K/1",
		},
		{
			name:       "resumed, the exit stamped just before the last rows: nothing is flagged",
			rows:       spendRows(),
			marker:     "on",
			child:      spendChildResponse,
			claudeCost: 8.003,
			lines: []string{
				stampLine("2026-10-09T17:59:59.000Z"),
				costStateLine("8.003"),
				stampLine("2026-10-09T19:00:00.000Z"),
			},
			want: "💰$8.00/1.0M/5.1K/1",
		},
		{
			name:       "resumed after background agents ran while the main thread was quiet: the clock fields place the exit",
			rows:       spendRows(),
			marker:     "on",
			child:      spendChildResponse,
			claudeCost: 8.003,
			lines: []string{
				stampLine(
					"2026-10-09T17:00:00.000Z",
				),
				costStateExiting("8.003", 1791569100000),
				stampLine("2026-10-09T19:00:00.000Z"),
			},
			want: "💰$8.00/1.0M/5.1K/1",
		},
		{
			name:       "a session older than its ledger: the one dollar is the complete total, the gap is named",
			rows:       spendRows(),
			marker:     "on",
			child:      spendChildResponse,
			claudeCost: 83.203,
			lines: []string{
				stampLine("2026-10-09T17:00:00.000Z"),
				costStateLine("75.2011"),
				stampLine("2026-10-09T17:59:00.000Z"),
			},
			want: "💰$83.20/1.0M/5.1K/1 pre-ledger",
		},
		{
			name:   "a session older than its ledger, claude code's total not yet read: the ledger plus the gap",
			rows:   spendRows(),
			marker: "on",
			child:  spendChildResponse,
			lines: []string{
				stampLine("2026-10-09T17:00:00.000Z"),
				costStateLine("75.2011"),
				stampLine("2026-10-09T17:59:00.000Z"),
			},
			want: "💰$83.20/1.0M/5.1K/1 pre-ledger",
		},
		{
			name: "a launch that ran with no sink between two that logged: the gap is named",
			rows: spendRows(), marker: "on", child: spendChildResponse, claudeCost: 18.003,
			lines: []string{
				stampLine("2026-10-09T17:00:00.000Z"), costStateLine("10"), stampLine("2026-10-09T17:30:00.000Z"),
				stampLine("2026-10-09T18:00:02.000Z"), costStateLine("18.003"), stampLine("2026-10-09T19:00:00.000Z"),
			},
			want: "💰$18.00/1.0M/5.1K/1 pre-ledger",
		},
		{
			name:       "a launch crashed after a healthy resume: the ledger holds what claude code lost, nothing is flagged",
			marker:     "on",
			child:      spendChildResponse,
			claudeCost: 8,
			rows: append(spendRows(), costledger.Row{
				RequestID:     "req_crash",
				TS:            "2026-10-09T19:00:00.000Z",
				SessionID:     spendSession,
				QuerySource:   "repl_main_thread",
				CostUSDMicros: micros(2_000_000),
				Join:          costledger.JoinLogSpan,
			}),
			lines: []string{
				stampLine("2026-10-09T18:00:00.500Z"),
				costStateLine("8"),
				stampLine("2026-10-09T18:50:00.000Z"),
			},
			want: "💰$10.00/1.0M/5.1K/1",
		},
		{
			name:       "resumed, a background agent ran on after the main thread's last line: its calls are that launch's, nothing is flagged",
			marker:     "on",
			child:      spendChildResponse,
			claudeCost: 9.003,
			rows: append(spendRows(), costledger.Row{
				RequestID:     "req_background",
				TS:            "2026-10-09T18:30:00.000Z",
				SessionID:     spendSession,
				AgentID:       "c1",
				QuerySource:   "agent:builtin:general-purpose",
				CostUSDMicros: micros(1_000_000),
				Join:          costledger.JoinLogSpan,
			}),
			lines: []string{
				stampLine("2026-10-09T18:00:02.000Z"),
				costStateLine("9.003"),
				stampLine("2026-10-09T19:00:00.000Z"),
			},
			want: "💰$9.00/1.0M/5.1K/1",
		},
		{
			name:       "a session older than its ledger whose launch could not log: the failure is named, not the gap",
			rows:       spendRows(),
			marker:     "failed",
			child:      spendChildResponse,
			claudeCost: 83.203,
			lines: []string{
				stampLine("2026-10-09T17:00:00.000Z"),
				costStateLine("75.2011"),
				stampLine("2026-10-09T17:59:00.000Z"),
			},
			want: "💰$83.20/1.0M/5.1K/1 sink failed",
		},
		{
			name: "a ledger that cannot be read is named, claude code's total stands", unreadable: true, marker: "on",
			child: spendChildResponse, claudeCost: 8.003, want: "💰$8.00/1.0M/100/1 ledger unreadable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ledgerDir := t.TempDir()
			switch {
			case tc.unreadable:
				if err := os.Mkdir(filepath.Join(ledgerDir, spendSession+".jsonl"), 0o700); err != nil {
					t.Fatal(err)
				}
			case tc.rows != nil:
				writeSpendLedger(t, ledgerDir, tc.rows)
			}
			data := input{SessionID: spendSession}
			data.Cost.TotalCostUSD = tc.claudeCost
			if !tc.noSession {
				data.TranscriptPath = spendTranscript(t, tc.child, tc.lines...)
			}
			runtime := Runtime{
				Home: t.TempDir(), Engine: pfmengine.Claude,
				Env: map[string]string{paths.EnvCostLedgerDir: ledgerDir, claudelaunch.EnvCostLedger: tc.marker},
			}
			if !tc.noPrices {
				runtime.Prices = testPrices(t)
			}
			_, line, _ := renderContextLine(runtime, data, "", time.Unix(1_790_000_000, 0))
			got := stripANSICodes(line)
			if count := strings.Count(got, "$"); count != 1 {
				t.Fatalf("line %q holds %d dollar signs, want exactly 1", got, count)
			}
			at := strings.Index(got, "💰")
			if at < 0 {
				t.Fatalf("line %q has no spend segment", got)
			}
			if segment, _, _ := strings.Cut(got[at:], " │"); segment != tc.want {
				t.Fatalf("spend segment %q, want %q (line %q)", segment, tc.want, got)
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
