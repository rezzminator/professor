package tokens

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/pricing"
)

func ptr(value float64) *float64 { return &value }

// replayOne replays one transcript of lines under a fresh project directory.
func replayOne(t *testing.T, a *auditor, lines ...string) *run {
	t.Helper()
	file := writeTranscript(t, filepath.Join(t.TempDir(), "-w-demo", "s1.jsonl"), lines...)
	r, err := a.audit(file, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("replay returned no run")
	}
	return r
}

func TestReplayPricesEachBillingColumnAtItsRate(t *testing.T) {
	a, _, _ := testAuditor(t)
	// sonnet-5: in 2, out 10, hit 0.2, w5m 2.5 per million tokens
	r := replayOne(t, a, assistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", 1000, 100, 2000, 400))
	if math.Abs(r.usd-0.0044) > 1e-12 || r.unpriced {
		t.Errorf("usd %v unpriced %v, want 0.002 + 0.001 + 0.0004 + 0.001 = 0.0044", r.usd, r.unpriced)
	}
	if r.tok != (tokenCounts{in: 1000, out: 100, cw5: 400, cr: 2000}) || r.calls != 1 {
		t.Errorf("tokens %+v calls %v", r.tok, r.calls)
	}
}

func TestReplayRendersUnknownRatesAsUnpricedWithTokensKept(t *testing.T) {
	cases := []struct {
		name, line string
		edit       func(a *auditor)
	}{
		{
			name: "a Codex row never prices a Claude transcript",
			line: assistant("m1", "gpt-5.6-sol", "2026-09-21T05:00:00Z", 1000, 100, 0, 0),
		},
		{
			name: "an unpublished one-hour write rate",
			line: strings.NewReplacer(`"cache_creation_input_tokens":0`, `"cache_creation_input_tokens":300`,
				`"ephemeral_1h_input_tokens":0`, `"ephemeral_1h_input_tokens":300`).
				Replace(assistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", 1000, 100, 0, 0)),
			edit: func(a *auditor) {
				for i := range a.prices.table.Rows {
					if a.prices.table.Rows[i].Key == "claude-sonnet-5" {
						a.prices.table.Rows[i].W1h = nil
					}
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, _, _ := testAuditor(t)
			if c.edit != nil {
				c.edit(a)
			}
			r := replayOne(t, a, c.line)
			if !r.unpriced || r.usd != 0 {
				t.Errorf("unpriced %v usd %v; want unpriced, never $0", r.unpriced, r.usd)
			}
			if r.tok.in != 1000 || r.tok.out != 100 || r.calls != 1 || a.scan.unpricedCalls != 1 {
				t.Errorf(
					"tokens %+v calls %v unpriced calls %d; want the tokens kept",
					r.tok,
					r.calls,
					a.scan.unpricedCalls,
				)
			}
		})
	}
}

// An advised response's top level sums two executor requests: its context is
// the last request's, and each request is priced at its own tier.
func TestReplayPricesAnAdvisedResponseRequestByRequest(t *testing.T) {
	a, _, _ := testAuditor(t)
	for i := range a.prices.table.Rows {
		if row := &a.prices.table.Rows[i]; row.Key == "claude-sonnet-5" {
			long := row.Rates
			long.In, long.Hit, long.W5m = ptr(4), ptr(0.4), ptr(5)
			row.Long = &pricing.Long{Above: 100000, Rates: long}
		}
	}
	r := replayOne(t, a, advisorAssistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", "claude-opus-5-5"))
	if r.ctxPeak != 2+87829+890 {
		t.Errorf(
			"context peak %v; want the last request's %v, never the summed %v",
			r.ctxPeak,
			2+87829+890,
			4+175106+1442,
		)
	}
	if executor := r.usd - r.advisor.usd; math.Abs(executor-advisorExecutorUSD) > 1e-12 {
		t.Errorf("executor usd %v; want %v, each request under 100K at the base tier", executor, advisorExecutorUSD)
	}
}

// An iteration type pfm does not know is counted by type, never ignored: the
// response's own counts stand.
func TestReplayCountsUnknownIterationTypes(t *testing.T) {
	a, _, _ := testAuditor(t)
	r := replayOne(t, a, `{"type":"assistant","timestamp":"2026-09-21T05:00:00Z","cwd":"/w/demo","sessionId":"s1",`+
		`"message":{"id":"m1","model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}],"usage":`+
		`{"input_tokens":1000,"output_tokens":100,"iterations":[`+
		`{"input_tokens":900,"output_tokens":40,"type":"message","model":"claude-fable-5-1"},`+
		`{"input_tokens":1000,"output_tokens":100,"type":"fallback_message","model":"claude-sonnet-5"}]}}}`)
	if got := a.scan.unknownIterations; got.number("fallback_message") != 1 || len(got.ordered()) != 1 {
		t.Errorf("unknown iterations %v; want one fallback_message", got.ordered())
	}
	if math.Abs(r.usd-(1000*2.0+100*10.0)/1e6) > 1e-12 || r.calls != 1 {
		t.Errorf("usd %v calls %v; want the top level priced as one call", r.usd, r.calls)
	}
}
