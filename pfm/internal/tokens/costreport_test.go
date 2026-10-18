package tokens

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/costledger"
)

const (
	ledgerSession = "00000000-0000-4000-8000-0000000000a1"
	otherSession  = "00000000-0000-4000-8000-0000000000b2"
	mainAgent     = "a0000000000000002"
)

// ledgerNow is 26 minutes after the last call of the sanitized 15-call sample.
var ledgerNow = time.Date(2026, 10, 9, 19, 0, 0, 0, time.UTC)

// sampleRows are the 15 rows of the sanitized probe ledger.
func sampleRows(t *testing.T) []costledger.Row {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "costledger", "testdata", "sample-15.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []costledger.Row
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var row costledger.Row
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("decode the sample row %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 15 {
		t.Fatalf("the sample holds %d rows, want 15", len(rows))
	}
	return rows
}

// callLine is one assistant transcript line carrying the request id the
// ledger row of the same call carries.
func callLine(requestID, at string) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"cwd":"/w/demo","requestId":%q,"message":{"id":"msg_%s",`+
		`"model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":2,`+
		`"output_tokens":10,"cache_read_input_tokens":1000,"cache_creation_input_tokens":0,"cache_creation":`+
		`{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}}}`, at, requestID, requestID)
}

// heldBy is the sample's transcripts: the agent id owning each held request,
// "" the main chat. Requests 7 to 10 (web search and summary helpers) are in
// no transcript: they are the side calls.
func heldBy(row costledger.Row) (string, bool) {
	switch row.QuerySource {
	case "web_search_tool", "agent_summary":
		return "", false
	}
	return row.AgentID, true
}

type sessionOptions struct {
	costState *float64 // the main transcript's cost-state line, none when nil
	// exitAt is the stamp of the last line before the cost-state, the exit it records: after every
	// call of the sample unless set.
	exitAt string
	// reopened: the chat ran on after the cost-state, a stamped line follows it: the next launch's
	// first line, at reopenAt (after every call of the sample unless set)
	reopened bool
	reopenAt string
	ledger   bool
	// stale is the agent whose transcript carries calls days before the window
	stale string
	// staleMain: the main thread's calls are days before the window, so no main run is in it
	staleMain bool
}

// writeSample writes the sample session: its ledger under ledgerDir when
// asked, and the transcripts holding every call but the side calls under
// root, the agents' with their meta files.
func writeSample(t *testing.T, root, ledgerDir string, opts sessionOptions) {
	t.Helper()
	rows := sampleRows(t)
	if opts.ledger {
		var content strings.Builder
		for index := range rows {
			raw, err := json.Marshal(&rows[index])
			if err != nil {
				t.Fatal(err)
			}
			content.Write(raw)
			content.WriteString("\n")
		}
		if err := os.MkdirAll(ledgerDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(ledgerDir, ledgerSession+".jsonl"),
			[]byte(content.String()),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	lines := map[string][]string{}
	for index := range rows {
		row := &rows[index]
		owner, held := heldBy(*row)
		if !held {
			continue
		}
		at := row.TS
		if (owner != "" && owner == opts.stale) || (owner == "" && opts.staleMain) {
			at = "2026-10-05T10:00:00.000Z"
		}
		lines[owner] = append(lines[owner], callLine(row.RequestID, at))
	}
	project := filepath.Join(root, "-w-demo")
	mainLines := lines[""]
	if opts.costState != nil {
		exitAt := opts.exitAt
		if exitAt == "" {
			exitAt = "2026-10-09T18:40:00.000Z"
		}
		mainLines = append(mainLines, fmt.Sprintf(`{"type":"system","timestamp":%q}`, exitAt),
			fmt.Sprintf(`{"type":"cost-state","totalCostUSD":%v}`, *opts.costState))
		if opts.reopened {
			reopenAt := opts.reopenAt
			if reopenAt == "" {
				reopenAt = "2026-10-09T18:50:00.000Z"
			}
			mainLines = append(mainLines, fmt.Sprintf(`{"type":"system","timestamp":%q}`, reopenAt))
		}
	}
	writeTranscript(t, filepath.Join(project, ledgerSession+".jsonl"), mainLines...)
	for owner, owned := range lines {
		if owner == "" {
			continue
		}
		base := filepath.Join(project, ledgerSession, "subagents", "agent-"+owner)
		writeTranscript(t, base+".jsonl", owned...)
		meta := `{"agentType":"general-purpose"}`
		if owner != mainAgent {
			meta = `{"agentType":"general-purpose","parentAgentId":"` + mainAgent + `"}`
		}
		writeTranscript(t, base+".meta.json", meta)
	}
}

// writeMainChat writes a chat with one call and no ledger.
func writeMainChat(t *testing.T, root, session string) {
	t.Helper()
	writeTranscript(
		t,
		filepath.Join(root, "-w-demo", session+".jsonl"),
		callLine("req_other", "2026-10-09T18:30:00.000Z"),
	)
}

// ledgerAudit audits root against ledgerDir at ledgerNow.
func ledgerAudit(t *testing.T, root, ledgerDir string, args ...string) (int, string, string) {
	t.Helper()
	all := append([]string{"--prices", fixture(t, "model-cost.json")}, args...)
	return auditWith(t, Options{
		ClaudeRoots: []string{root}, LedgerDir: ledgerDir, Clock: clock.NewFake(ledgerNow),
	}, all...)
}

// rowFields are the fields of the report line holding label, the cost and the
// share its last two.
func rowFields(t *testing.T, stdout, label string) []string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, label) {
			return strings.Fields(line)
		}
	}
	t.Fatalf("no report line holds %q:\n%s", label, stdout)
	return nil
}

func tail2(fields []string) string { return strings.Join(fields[len(fields)-2:], " ") }

func TestCostLedgerReportsTotalSideCallsAndTheSplit(t *testing.T) {
	root, ledgerDir := t.TempDir(), t.TempDir()
	writeSample(t, root, ledgerDir, sessionOptions{ledger: true})
	code, stdout, stderr := ledgerAudit(t, root, ledgerDir)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	for _, want := range []string{
		"\n== COST LEDGER ",
		"chat " + ledgerSession,
		"session total $1.29 (15 calls)",
		"side calls $0.15 (4 calls no transcript holds)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, noLedgerLine) {
		t.Errorf("stdout carries the no-ledger line for a chat that has a ledger:\n%s", stdout)
	}
	rows := []struct{ label, want string }{
		{"main thread", "$0.34 26.3%"},
		{"agent " + mainAgent, "$0.95 73.7%"},
		{"agent a0000000000000001", "$0.08 6.1%"},
		{"agent a0000000000000003", "$0.09 6.8%"},
		{"repl_main_thread", "$0.34 26.3%"},
		{"agent:builtin:general-purpose", "$0.80 62.0%"},
		{"web_search_tool", "$0.15 11.5%"},
		{"agent_summary", "<$0.01 0.1%"},
	}
	for _, row := range rows {
		if got := tail2(rowFields(t, stdout, row.label)); got != row.want {
			t.Errorf("row %q ends %q, want %q", row.label, got, row.want)
		}
	}
}

func TestCostLedgerOutCarriesTheMicrosOfEveryRow(t *testing.T) {
	root, ledgerDir := t.TempDir(), t.TempDir()
	writeSample(t, root, ledgerDir, sessionOptions{ledger: true})
	out := filepath.Join(t.TempDir(), "audit.json")
	if code, _, stderr := ledgerAudit(t, root, ledgerDir, "--out", out); code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		CostLedger []struct {
			SID         string `json:"sid"`
			Micros      int64  `json:"micros"`
			Rows        int    `json:"rows"`
			UnknownCost int    `json:"unknownCost"`
			Side        struct {
				Micros int64 `json:"micros"`
				Calls  int   `json:"calls"`
			} `json:"side"`
			ByAgent []struct {
				Agent  string `json:"agent"`
				Depth  int    `json:"depth"`
				Micros int64  `json:"micros"`
			} `json:"byAgent"`
			BySource []struct {
				Source string `json:"source"`
				Micros int64  `json:"micros"`
			} `json:"bySource"`
		} `json:"costLedger"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.CostLedger) != 1 {
		t.Fatalf("costLedger holds %d sessions, want 1", len(document.CostLedger))
	}
	got := document.CostLedger[0]
	if got.SID != ledgerSession || got.Micros != 1294100 || got.Rows != 15 || got.UnknownCost != 0 ||
		got.Side.Micros != 151144 || got.Side.Calls != 4 {
		t.Errorf("session %q micros %d rows %d unknown %d side %+v; want %s 1294100 15 0 {151144 4}",
			got.SID, got.Micros, got.Rows, got.UnknownCost, got.Side, ledgerSession)
	}
	agents := map[string]int64{}
	var topLevel int64
	for _, agent := range got.ByAgent {
		agents[agent.Agent] = agent.Micros
		if agent.Depth == 0 {
			topLevel += agent.Micros
		}
	}
	for agent, want := range map[string]int64{
		"": 340251, mainAgent: 953849, "a0000000000000001": 78574, "a0000000000000003": 87840,
	} {
		if agents[agent] != want {
			t.Errorf("agent %q micros %d, want %d", agent, agents[agent], want)
		}
	}
	if topLevel != got.Micros {
		t.Errorf("main plus the top-level agent trees is %d micros, want the session total %d", topLevel, got.Micros)
	}
	sources := map[string]int64{}
	var sourceSum int64
	for _, source := range got.BySource {
		sources[source.Source] = source.Micros
		sourceSum += source.Micros
	}
	for source, want := range map[string]int64{
		"repl_main_thread": 340251, "agent:builtin:general-purpose": 802705, "web_search_tool": 149292,
		"agent_summary": 1852,
	} {
		if sources[source] != want {
			t.Errorf("query_source %q micros %d, want %d", source, sources[source], want)
		}
	}
	if sourceSum != got.Micros {
		t.Errorf("the query_source rows sum to %d micros, want the session total %d", sourceSum, got.Micros)
	}
}

func TestCostLedgerSetsTheTotalAgainstClaudeCodes(t *testing.T) {
	cost := func(value float64) *float64 { return &value }
	cases := []struct {
		name      string
		costState *float64
		want      []string
		not       []string
	}{
		{
			"no cost-state line", nil,
			[]string{"no cost-state line"},
			[]string{"session total minus Claude Code", "before ledger"},
		},
		{
			"equal", cost(1.2941),
			[]string{"Claude Code says $1.29"},
			[]string{"session total minus Claude Code", "before ledger"},
		},
		{
			"under half a cent", cost(1.2985),
			[]string{"Claude Code says $1.30"},
			[]string{"session total minus Claude Code", "before ledger"},
		},
		{
			"ledger below Claude Code: the rest came before the ledger",
			cost(1.40),
			[]string{
				"Claude Code says $1.40",
				"session total $1.40 (15 calls)",
				"before ledger $0.11 (not attributed)",
			},
			[]string{"session total minus Claude Code"},
		},
		{
			"ledger above Claude Code", cost(1.1),
			[]string{"session total minus Claude Code: +$0.19", "session total $1.29 (15 calls)"},
			[]string{"before ledger"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, ledgerDir := t.TempDir(), t.TempDir()
			writeSample(t, root, ledgerDir, sessionOptions{ledger: true, costState: c.costState})
			code, stdout, stderr := ledgerAudit(t, root, ledgerDir)
			if code != 0 {
				t.Fatalf("exit %d stderr %q", code, stderr)
			}
			for _, want := range c.want {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			for _, not := range c.not {
				if strings.Contains(stdout, not) {
					t.Errorf("stdout holds %q:\n%s", not, stdout)
				}
			}
		})
	}
}

// A chat that began before its ledger: Claude Code restored its total on resume, so the
// cost-state of the exit before the ledger holds money no row does. The report names it as its
// own line, attributed to no agent, and the session total (the main status line's one dollar
// amount) is the rows plus that line.
func TestCostLedgerNamesWhatCameBeforeTheLedgerAndAddsItToTheSessionTotal(t *testing.T) {
	cost := 76.5
	root, ledgerDir := t.TempDir(), t.TempDir()
	writeSample(t, root, ledgerDir, sessionOptions{
		ledger: true, costState: &cost, exitAt: "2026-10-09T18:00:00.000Z", reopened: true,
		reopenAt: "2026-10-09T18:20:00.000Z",
	})
	code, stdout, stderr := ledgerAudit(t, root, ledgerDir)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	for _, want := range []string{
		"session total $77.79 (15 calls)",
		"before ledger $76.50 (not attributed)",
		"Claude Code's last cost-state says $76.50 (written at an earlier exit; the chat has run since)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "session total minus Claude Code") {
		t.Errorf("a cost-state the chat ran past is no total to set the ledger against:\n%s", stdout)
	}
	for _, row := range []struct{ label, want string }{
		{"main thread", "$0.34 0.4%"},
		{"agent " + mainAgent, "$0.95 1.2%"},
		{"before ledger (not attributed)", "$76.50 98.3%"},
		{"repl_main_thread", "$0.34 0.4%"},
	} {
		if got := tail2(rowFields(t, stdout, row.label)); got != row.want {
			t.Errorf("row %q ends %q, want %q", row.label, got, row.want)
		}
	}
	out := filepath.Join(t.TempDir(), "audit.json")
	if code, _, stderr := ledgerAudit(t, root, ledgerDir, "--out", out); code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		CostLedger []struct {
			Micros       int64 `json:"micros"`
			BeforeLedger int64 `json:"beforeLedgerMicros"`
			Session      int64 `json:"sessionMicros"`
		} `json:"costLedger"`
	}
	if err := json.Unmarshal(raw, &document); err != nil || len(document.CostLedger) != 1 {
		t.Fatalf("costLedger = %+v, %v", document.CostLedger, err)
	}
	got := document.CostLedger[0]
	if got.Micros != 1294100 || got.BeforeLedger != 76_500_000 || got.Session != 77_794_100 {
		t.Errorf("micros %d beforeLedgerMicros %d sessionMicros %d, want 1294100 76500000 77794100",
			got.Micros, got.BeforeLedger, got.Session)
	}
}

// A chat whose main-thread calls all fall before the window still reports its whole session total:
// the cost-states are read from its main transcript, not only from a run of the window.
func TestCostLedgerReadsTheCostStatesOfAMainTranscriptOutsideTheWindow(t *testing.T) {
	cost := 76.5
	root, ledgerDir := t.TempDir(), t.TempDir()
	writeSample(t, root, ledgerDir, sessionOptions{
		ledger: true, costState: &cost, exitAt: "2026-10-09T18:00:00.000Z", reopened: true,
		reopenAt: "2026-10-09T18:20:00.000Z", staleMain: true,
	})
	code, stdout, stderr := ledgerAudit(t, root, ledgerDir)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	for _, want := range []string{
		"session total $77.79 (15 calls)",
		"before ledger $76.50 (not attributed)",
		"Claude Code's last cost-state says $76.50",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

// A chat the ledger covers from its first call has nothing before it: a resume whose earlier
// launch was fully ledgered, the cost-state matching the rows up to its exit.
func TestCostLedgerHealthyResumeHasNothingBeforeTheLedger(t *testing.T) {
	cost := 1.2941
	root, ledgerDir := t.TempDir(), t.TempDir()
	writeSample(t, root, ledgerDir, sessionOptions{ledger: true, costState: &cost, reopened: true})
	code, stdout, stderr := ledgerAudit(t, root, ledgerDir)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "session total $1.29 (15 calls)") || strings.Contains(stdout, "before ledger") {
		t.Errorf("a healthy resume reports a session total of $1.29 and no before-ledger line:\n%s", stdout)
	}
}

func TestCostLedgerHeldSetCountsTranscriptsOutsideTheWindow(t *testing.T) {
	root, ledgerDir := t.TempDir(), t.TempDir()
	// the first helper agent's calls are days old: no run of this window, but its transcript still holds its rows
	writeSample(t, root, ledgerDir, sessionOptions{ledger: true, stale: "a0000000000000001"})
	code, stdout, stderr := ledgerAudit(t, root, ledgerDir)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if want := "side calls $0.15 (4 calls no transcript holds)"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
}

func TestCostLedgerHeldSetIncludesOldMtimeTranscripts(t *testing.T) {
	root, ledgerDir := t.TempDir(), t.TempDir()
	writeSample(t, root, ledgerDir, sessionOptions{ledger: true, stale: "a0000000000000001"})
	file := filepath.Join(root, "-w-demo", ledgerSession, "subagents", "agent-a0000000000000001.jsonl")
	old := ledgerNow.Add(-48 * time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "audit.json")
	code, stdout, stderr := ledgerAudit(t, root, ledgerDir, "--out", out)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if want := "side calls $0.15 (4 calls no transcript holds)"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Scan struct {
			SkippedOld int `json:"skippedOld"`
		} `json:"scan"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.Scan.SkippedOld != 1 {
		t.Errorf("skippedOld = %d, want 1", document.Scan.SkippedOld)
	}
}

func TestLedgerTranscriptsRetainOwnershipRoot(t *testing.T) {
	first, second, ledgerDir := t.TempDir(), t.TempDir(), t.TempDir()
	writeSample(t, first, ledgerDir, sessionOptions{})
	writeSample(t, second, ledgerDir, sessionOptions{})
	meta := filepath.Join(second, "-w-demo", ledgerSession, "subagents", "agent-"+mainAgent+".meta.json")
	writeTranscript(t, meta, `{"agentType":"stale-copy","parentAgentId":"wrong-parent"}`)
	a, _, _ := testAuditor(t, "--root", first, "--root", second)
	if err := a.collectRuns(); err != nil {
		t.Fatal(err)
	}
	types, parents, problems := a.agentMeta(ledgerSession)
	if len(problems) != 0 || types[mainAgent] != "general-purpose" || parents[mainAgent] != "" {
		t.Fatalf(
			"ledger metadata types=%v parents=%v problems=%v; want ownership-selected root",
			types,
			parents,
			problems,
		)
	}
	if a.scan.dupFiles == 0 {
		t.Fatal("fixture did not exercise duplicate transcript ownership")
	}
}

func TestCostLedgerAbsentIsOneLineAndNoSection(t *testing.T) {
	root, ledgerDir := t.TempDir(), t.TempDir()
	writeSample(t, root, ledgerDir, sessionOptions{})
	code, stdout, stderr := ledgerAudit(t, root, ledgerDir)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "\n"+noLedgerLine+"\n") {
		t.Errorf("stdout lacks the line %q:\n%s", noLedgerLine, stdout)
	}
	if strings.Contains(stdout, "COST LEDGER") {
		t.Errorf("a chat with no ledger got a section:\n%s", stdout)
	}
	out := filepath.Join(t.TempDir(), "audit.json")
	if code, _, stderr := ledgerAudit(t, root, ledgerDir, "--out", out); code != 0 {
		t.Fatalf("--out exit %d stderr %q", code, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"costLedger"`) {
		t.Errorf("--out carries a costLedger member without any ledger")
	}
}

func TestCostLedgerMixedChatsAndTheSessionSelector(t *testing.T) {
	root, ledgerDir := t.TempDir(), t.TempDir()
	writeSample(t, root, ledgerDir, sessionOptions{ledger: true})
	writeMainChat(t, root, otherSession)
	t.Run("both chats", func(t *testing.T) {
		code, stdout, stderr := ledgerAudit(t, root, ledgerDir)
		if code != 0 {
			t.Fatalf("exit %d stderr %q", code, stderr)
		}
		for _, want := range []string{"chat " + ledgerSession, noLedgerLine + " for 1 of 2 chats"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout lacks %q:\n%s", want, stdout)
			}
		}
	})
	t.Run("the chat with a ledger", func(t *testing.T) {
		code, stdout, stderr := ledgerAudit(t, root, ledgerDir, "--session", "00000000-0000-4000-8000-0000000000a")
		if code != 0 {
			t.Fatalf("exit %d stderr %q", code, stderr)
		}
		if !strings.Contains(stdout, "chat "+ledgerSession) || strings.Contains(stdout, "not measured") {
			t.Errorf("--session of the ledgered chat:\n%s", stdout)
		}
	})
	t.Run("the chat without one", func(t *testing.T) {
		code, stdout, stderr := ledgerAudit(t, root, ledgerDir, "--session", otherSession)
		if code != 0 {
			t.Fatalf("exit %d stderr %q", code, stderr)
		}
		if strings.Contains(stdout, "COST LEDGER") || !strings.Contains(stdout, "\n"+noLedgerLine+"\n") {
			t.Errorf("--session of the chat with no ledger:\n%s", stdout)
		}
	})
}

func TestCostLedgerUnreadableIsLoudNeverAbsent(t *testing.T) {
	root, ledgerDir := t.TempDir(), t.TempDir()
	writeSample(t, root, ledgerDir, sessionOptions{})
	// a directory where the ledger file belongs: it opens, it cannot be read
	if err := os.MkdirAll(filepath.Join(ledgerDir, ledgerSession+".jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := ledgerAudit(t, root, ledgerDir)
	if code != 1 {
		t.Errorf("exit %d, want 1: a ledger that could not be read is a read error", code)
	}
	if want := "cost ledger unreadable for chat " + ledgerSession + ": "; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
	if strings.Contains(stdout, noLedgerLine) {
		t.Errorf("a ledger that failed to read reads as no ledger:\n%s", stdout)
	}
}

func TestCostLedgerNamesUnknownCostsAndTornLines(t *testing.T) {
	root, ledgerDir := t.TempDir(), t.TempDir()
	writeSample(t, root, ledgerDir, sessionOptions{ledger: true})
	path := filepath.Join(ledgerDir, ledgerSession+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unknown := costledger.Row{
		RequestID: "req_unknown", TS: "2026-10-09T18:40:00.000Z", SessionID: ledgerSession,
		QuerySource: "compact", Model: "claude-sonnet-5", Join: costledger.JoinLog,
	}
	line, err := json.Marshal(unknown)
	if err != nil {
		t.Fatal(err)
	}
	torn := string(raw) + string(line) + "\n" + `{"request_id":"req_torn","ts":` + "\n"
	if err := os.WriteFile(path, []byte(torn), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := ledgerAudit(t, root, ledgerDir)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	for _, want := range []string{
		"session total $1.29 (16 calls)",
		"1 call with no cost in the ledger, not in the total",
		"1 torn ledger line skipped",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
}

// TestCostLedgerAgentCycleStillSumsToTheTotal feeds a ledger whose two agents
// name each other as parent: neither loops the report nor drops its dollars.
func TestCostLedgerAgentCycleStillSumsToTheTotal(t *testing.T) {
	root, ledgerDir := t.TempDir(), t.TempDir()
	micros := func(value int64) *int64 { return &value }
	var content strings.Builder
	for i, agent := range []struct{ id, parent string }{{"a1", "a2"}, {"a2", "a1"}, {"", ""}} {
		raw, err := json.Marshal(costledger.Row{
			RequestID: fmt.Sprintf("req_%d", i), TS: "2026-10-09T18:30:00.000Z", SessionID: ledgerSession,
			AgentID: agent.id, ParentAgentID: agent.parent, QuerySource: "repl_main_thread", Model: "claude-sonnet-5",
			CostUSDMicros: micros(int64(1000 * (i + 1))), Join: costledger.JoinLog,
		})
		if err != nil {
			t.Fatal(err)
		}
		content.Write(raw)
		content.WriteString("\n")
	}
	if err := os.WriteFile(
		filepath.Join(ledgerDir, ledgerSession+".jsonl"),
		[]byte(content.String()),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	writeTranscript(
		t,
		filepath.Join(root, "-w-demo", ledgerSession+".jsonl"),
		callLine("req_2", "2026-10-09T18:30:00.000Z"),
	)
	out := filepath.Join(t.TempDir(), "audit.json")
	if code, _, stderr := ledgerAudit(t, root, ledgerDir, "--out", out); code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		CostLedger []struct {
			Micros  int64 `json:"micros"`
			ByAgent []struct {
				Depth  int   `json:"depth"`
				Micros int64 `json:"micros"`
			} `json:"byAgent"`
		} `json:"costLedger"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.CostLedger) != 1 || document.CostLedger[0].Micros != 6000 {
		t.Fatalf("costLedger %+v, want one session of 6000 micros", document.CostLedger)
	}
	var topLevel int64
	for _, agent := range document.CostLedger[0].ByAgent {
		if agent.Depth == 0 {
			topLevel += agent.Micros
		}
	}
	if topLevel != 6000 {
		t.Errorf("top-level rows sum to %d micros, want 6000", topLevel)
	}
}
