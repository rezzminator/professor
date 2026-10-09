package tokens

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// advisorAssistant is a Sonnet record whose response consulted an advisor once,
// the usage block as the harness writes it: the top level is the executor's two
// message iterations, the advisor iteration is billed to the advisor's model.
func advisorAssistant(id, model, at, advisorModel string) string {
	named := ""
	if advisorModel != "" {
		named = `,"model":"` + advisorModel + `"`
	}
	return `{"type":"assistant","timestamp":"` + at + `","cwd":"/w/demo","sessionId":"s1","message":{"id":"` + id +
		`","model":"` + model + `","content":[{"type":"text","text":"ok"}],"usage":` +
		`{"input_tokens":4,"cache_creation_input_tokens":1442,"cache_read_input_tokens":175106,"output_tokens":1240,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":552,"ephemeral_1h_input_tokens":0},"iterations":[` +
		`{"input_tokens":2,"output_tokens":26,"cache_read_input_tokens":87277,"cache_creation_input_tokens":552,"type":"message"},` +
		`{"input_tokens":177,"output_tokens":1422,"cache_read_input_tokens":0,"cache_creation_input_tokens":89334,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":89334,"ephemeral_1h_input_tokens":0},"type":"advisor_message"` + named + `},` +
		`{"input_tokens":2,"output_tokens":1214,"cache_read_input_tokens":87829,"cache_creation_input_tokens":890,"type":"message"}]}}}`
}

const (
	// sonnet-5: in 2, out 10, hit 0.2, w5m 2.5; opus-5-5: in 4, out 20, w5m 5 per million tokens.
	advisorExecutorUSD = (4*2.0 + 1240*10.0 + 175106*0.2 + 1442*2.5) / 1e6
	advisorAdvisorUSD  = (177*4.0 + 1422*20.0 + 89334*5.0) / 1e6
)

func TestReplayPricesTheAdvisorAtItsOwnModelAndBooksItApart(t *testing.T) {
	a, _, _ := testAuditor(t)
	r := replayOne(t, a, advisorAssistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", "claude-opus-5-5"))
	if math.Abs(r.usd-(advisorExecutorUSD+advisorAdvisorUSD)) > 1e-12 || r.unpriced {
		t.Errorf(
			"usd %v unpriced %v; want executor %v + advisor %v",
			r.usd,
			r.unpriced,
			advisorExecutorUSD,
			advisorAdvisorUSD,
		)
	}
	if math.Abs(r.advisor.usd-advisorAdvisorUSD) > 1e-12 || r.advisor.calls != 1 ||
		r.advisor.tok != (tokenCounts{in: 177, out: 1422, cw5: 89334}) {
		t.Errorf(
			"advisor %+v; want %v over one consultation of 177 in, 1422 out, 89334 written",
			r.advisor,
			advisorAdvisorUSD,
		)
	}
	if r.calls != 1 || r.model != "claude-sonnet-5" || r.models.number("claude-sonnet-5") <= 0 ||
		len(r.models.ordered()) != 1 {
		t.Errorf(
			"calls %v model %q models %v; want the one executor call, the advisor never a model of the run",
			r.calls,
			r.model,
			r.models.ordered(),
		)
	}
	if want := (tokenCounts{in: 4 + 177, out: 1240 + 1422, cw5: 1442 + 89334, cr: 175106}); r.tok != want {
		t.Errorf("tokens %+v; want %+v with the advisor's counted", r.tok, want)
	}
	g := a.global
	if math.Abs(g.advisor.usd-advisorAdvisorUSD) > 1e-12 || g.advisor.calls != 1 {
		t.Errorf("global advisor %+v; want the run's", g.advisor)
	}
	columns := g.usd.in + g.usd.out + g.usd.cw5 + g.usd.cw1 + g.usd.cr
	if math.Abs(columns-r.usd) > 1e-12 {
		t.Errorf("billing columns sum to %v; want the run's %v with the advisor in them", columns, r.usd)
	}
	key := "main · opus-5-5 · advisor"
	if entry, found := g.modelEffort.get(
		key,
	); !found ||
		math.Abs(entry.(*object).number("usd")-advisorAdvisorUSD) > 1e-12 {
		t.Errorf("modelEffort lacks %q at the advisor's dollars: %v", key, g.modelEffort.ordered())
	}
}

// advisorOnlyAssistant is a Sonnet record whose executor iterations billed
// nothing while its Opus advisor billed one consultation.
func advisorOnlyAssistant(id, at string) string {
	return `{"type":"assistant","timestamp":"` + at + `","cwd":"/w/demo","sessionId":"s1",` +
		`"message":{"id":"` + id + `","model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}],"usage":` +
		`{"input_tokens":0,"output_tokens":0,"iterations":[{"input_tokens":0,"output_tokens":0,"type":"message"},` +
		`{"input_tokens":177,"output_tokens":1422,"cache_creation_input_tokens":89334,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":89334,"ephemeral_1h_input_tokens":0},` +
		`"type":"advisor_message","model":"claude-opus-5-5"}]}}}`
}

// A response whose executor iterations billed nothing still billed its advisor:
// its consultation is booked, never dropped as zero usage, and the executor made
// no request, so it is no call and moves no context.
func TestReplayKeepsAResponseThatBilledOnlyItsAdvisor(t *testing.T) {
	a, _, _ := testAuditor(t)
	r := replayOne(t, a, advisorOnlyAssistant("m1", "2026-09-21T05:00:00Z"))
	if a.scan.syntheticCalls != 0 || math.Abs(r.advisor.usd-advisorAdvisorUSD) > 1e-12 ||
		math.Abs(r.usd-advisorAdvisorUSD) > 1e-12 {
		t.Errorf(
			"synthetic %d advisor %+v usd %v; want the advisor's %v booked",
			a.scan.syntheticCalls,
			r.advisor,
			r.usd,
			advisorAdvisorUSD,
		)
	}
	if r.calls != 0 || a.global.calls != 0 || r.advisor.calls != 1 {
		t.Errorf(
			"calls %v (global %v) advisor calls %v; want no executor call and one consultation",
			r.calls,
			a.global.calls,
			r.advisor.calls,
		)
	}
	// the run is kept, so its window is the consultation's, never the epoch
	at := func(stamp string) float64 {
		parsed, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			t.Fatal(err)
		}
		return float64(parsed.UnixMilli())
	}
	if r.t0 != at("2026-09-21T05:00:00Z") || r.t1 != r.t0 || r.ctxFirst != 0 {
		t.Errorf("window %v–%v first ctx %v; want the consultation's time and no context", r.t0, r.t1, r.ctxFirst)
	}
	a, _, _ = testAuditor(t)
	r = replayOne(t, a,
		advisorOnlyAssistant("m1", "2026-09-21T05:00:00Z"),
		assistant("m2", "claude-sonnet-5", "2026-09-21T05:01:00Z", 1000, 100, 3000, 400))
	if r.t0 != at("2026-09-21T05:00:00Z") || r.t1 != at("2026-09-21T05:01:00Z") || r.ctxFirst != 4400 {
		t.Errorf(
			"window %v–%v first ctx %v; want the run to open at the consultation and its first context the call's 4400",
			r.t0, r.t1, r.ctxFirst,
		)
	}
	a, _, _ = testAuditor(t)
	r = replayOne(t, a,
		assistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", 1000, 100, 2000, 400),
		advisorOnlyAssistant("m2", "2026-09-21T05:01:00Z"),
		assistant("m3", "claude-sonnet-5", "2026-09-21T05:02:00Z", 1000, 100, 3000, 400))
	if r.calls != 2 || r.resets != 0 || r.advisor.calls != 1 || r.ctxPeak != 4400 {
		t.Errorf(
			"calls %v resets %v advisor calls %v peak %v; want the two executor calls, no reset, one consultation, peak 4400",
			r.calls,
			r.resets,
			r.advisor.calls,
			r.ctxPeak,
		)
	}
}

// A transcript whose only billed response is an advisor's keeps its run, so
// the total never holds dollars no run or project shows.
func TestReportKeepsARunThatBilledOnlyAnAdvisor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	writeTranscript(t, filepath.Join(root, "-w-demo", "s1.jsonl"), advisorOnlyAssistant("m1", "2026-09-21T05:00:00Z"))
	out := filepath.Join(t.TempDir(), "audit.json")
	code, stdout, stderr := auditWith(t, Options{ClaudeRoots: []string{root}},
		"--prices", fixture(t, "model-cost.json"), "--out", out)
	if code != 0 || strings.Contains(stdout, "NaN") {
		t.Fatalf("exit %d stderr %q; want a clean report:\n%s", code, stderr, stdout)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Total float64 `json:"total"`
		Runs  []struct {
			USD float64 `json:"usd"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		t.Fatalf("decode --out: %v", err)
	}
	// a run's usd is rounded to four places, the total is not
	if math.Abs(doc.Total-advisorAdvisorUSD) > 1e-9 || len(doc.Runs) != 1 ||
		math.Abs(doc.Runs[0].USD-advisorAdvisorUSD) > 1e-4 {
		t.Errorf("total %v runs %+v; want the advisor's %v on its one run", doc.Total, doc.Runs, advisorAdvisorUSD)
	}
}

// consultationTierAndTTL is a response whose advisor consultation writes 150K
// tokens at the one-hour TTL: above the advisor row's long threshold.
const consultationTierAndTTL = `{"type":"assistant","timestamp":"2026-09-21T05:00:00Z","cwd":"/w/demo",` +
	`"sessionId":"s1","message":{"id":"m1","model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}],` +
	`"usage":{"input_tokens":1000,"output_tokens":100,"iterations":[` +
	`{"input_tokens":1000,"output_tokens":100,"type":"message"},` +
	`{"input_tokens":200,"output_tokens":1000,"cache_creation_input_tokens":150000,` +
	`"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":150000},` +
	`"type":"advisor_message","model":"claude-opus-5-5"}]}}}`

// A consultation is priced at its own model's tier and TTL: one above the
// advisor row's long threshold pays the long rates, a one-hour write the
// one-hour rate.
func TestReplayPricesAConsultationAtItsOwnTierAndTTL(t *testing.T) {
	a, _, _ := testAuditor(t)
	for i := range a.prices.table.Rows {
		if row := &a.prices.table.Rows[i]; row.Key == "claude-opus-5-5" {
			long := row.Rates
			long.In, long.Out, long.W1h = ptr(8), ptr(40), ptr(16)
			row.Long = &pricing.Long{Above: 100000, Rates: long}
		}
	}
	r := replayOne(t, a, consultationTierAndTTL)
	executor := (1000*2.0 + 100*10.0) / 1e6
	advisor := (200*8.0 + 1000*40.0 + 150000*16.0) / 1e6
	if math.Abs(r.usd-(executor+advisor)) > 1e-12 {
		t.Errorf(
			"usd %v; want executor %v + the consultation at the long tier's one-hour rate %v",
			r.usd,
			executor,
			advisor,
		)
	}
}

func TestReplayRendersAnUnpricedAdvisorAsUnpricedWithTokensKept(t *testing.T) {
	for _, model := range []string{"claude-nonesuch-9", ""} {
		a, _, _ := testAuditor(t)
		r := replayOne(t, a, advisorAssistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", model))
		if !r.unpriced || a.scan.unpricedCalls != 1 {
			t.Errorf(
				"advisor model %q: unpriced %v unpriced calls %d; want the run unpriced, never a quiet low total",
				model,
				r.unpriced,
				a.scan.unpricedCalls,
			)
		}
		if r.advisor.usd != 0 || r.advisor.calls != 1 || r.advisor.tok.out != 1422 || r.tok.out != 1240+1422 {
			t.Errorf(
				"advisor model %q: advisor %+v tokens %+v; want the advisor's tokens kept without dollars",
				model,
				r.advisor,
				r.tok,
			)
		}
	}
}

func TestReportShowsAdvisorSpendWhereCostIsReported(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	writeTranscript(t, filepath.Join(root, "-w-demo", "s1.jsonl"),
		advisorAssistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", "claude-opus-5-5"))
	out := filepath.Join(t.TempDir(), "audit.json")
	code, stdout, stderr := auditWith(t, Options{ClaudeRoots: []string{root}},
		"--prices", fixture(t, "model-cost.json"), "--out", out)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	total := advisorExecutorUSD + advisorAdvisorUSD
	for _, want := range []string{
		"== 1 · TOTAL " + dollars(total) + " by billing class",
		"  of which advisor spend (",
		"· 1× consulted ·",
		"opus-5-5 · advisor",
		"advisor " + dollars(advisorAdvisorUSD),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report lacks %q:\n%s", want, stdout)
		}
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "of which advisor spend") &&
			(!strings.Contains(line, dollars(advisorAdvisorUSD)) || !strings.Contains(line, pctText(advisorAdvisorUSD, total))) {
			t.Errorf(
				"advisor line %q; want its dollars %s and share %s",
				line,
				dollars(advisorAdvisorUSD),
				pctText(advisorAdvisorUSD, total),
			)
		}
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Total   float64               `json:"total"`
		Advisor struct{ USD float64 } `json:"advisor"`
		Runs    []struct {
			USD     float64                `json:"usd"`
			Advisor *struct{ USD float64 } `json:"advisor"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		t.Fatal(err)
	}
	if math.Abs(doc.Total-total) > 1e-9 || math.Abs(doc.Advisor.USD-advisorAdvisorUSD) > 1e-9 ||
		len(doc.Runs) != 1 || doc.Runs[0].Advisor == nil || math.Abs(doc.Runs[0].Advisor.USD-advisorAdvisorUSD) > 1e-9 {
		t.Errorf(
			"out total %v advisor %v runs %+v; want total %v and the advisor %v on the run",
			doc.Total,
			doc.Advisor,
			doc.Runs,
			total,
			advisorAdvisorUSD,
		)
	}
}

// An advisor the table cannot price is ignorance: every advisor dollar reads
// "n/a", never "$0.00", and the JSON's advisor dollars are null.
func TestReportRendersAnUnpricedAdvisorAsNotApplicable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	writeTranscript(t, filepath.Join(root, "-w-demo", "s1.jsonl"),
		advisorAssistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", "claude-nonesuch-9"))
	out := filepath.Join(t.TempDir(), "audit.json")
	code, stdout, stderr := auditWith(t, Options{ClaudeRoots: []string{root}},
		"--prices", fixture(t, "model-cost.json"), "--out", out)
	if code != 0 {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	lines := 0
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.Contains(line, "advisor") {
			continue
		}
		lines++
		if strings.Contains(line, "advisor $") ||
			strings.Contains(line, "$0.00") && strings.Contains(line, "advisor spend") {
			t.Errorf("advisor line %q shows dollars for an advisor the table cannot price", line)
		}
	}
	if !strings.Contains(stdout, notPriced+"    "+notPriced+"  of which advisor spend") ||
		!strings.Contains(stdout, "· advisor "+notPriced) {
		t.Errorf(
			"report lacks the advisor's %q on the total and run lines (%d advisor lines):\n%s",
			notPriced,
			lines,
			stdout,
		)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Advisor struct {
			USD      *float64 `json:"usd"`
			Unpriced float64  `json:"unpriced"`
		} `json:"advisor"`
		Runs []struct {
			Advisor *struct {
				USD *float64 `json:"usd"`
			} `json:"advisor"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Advisor.USD != nil || doc.Advisor.Unpriced != 1 || len(doc.Runs) != 1 || doc.Runs[0].Advisor == nil ||
		doc.Runs[0].Advisor.USD != nil {
		t.Errorf(
			"out advisor %+v runs %+v; want null advisor dollars and one unpriced consultation",
			doc.Advisor,
			doc.Runs,
		)
	}
}

func TestReportWithoutAnAdvisorMentionsNone(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	writeTranscript(t, filepath.Join(root, "-w-demo", "s1.jsonl"),
		assistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", 1000, 100, 2000, 400))
	out := filepath.Join(t.TempDir(), "audit.json")
	code, stdout, stderr := auditWith(t, Options{ClaudeRoots: []string{root}},
		"--prices", fixture(t, "model-cost.json"), "--out", out)
	content, err := os.ReadFile(out)
	if err != nil || code != 0 {
		t.Fatalf("exit %d stderr %q err %v", code, stderr, err)
	}
	if strings.Contains(stdout, "advisor") || strings.Contains(string(content), "advisor") {
		t.Errorf("a run with no advisor consultation shows advisor spend:\n%s", stdout)
	}
}

// advisedRewrite is an advised response whose first request rewrote the whole
// prefix and whose second read it back: the rewrite is the first request's.
const advisedRewrite = `{"type":"assistant","timestamp":"2026-09-21T05:01:00Z","cwd":"/w/demo","sessionId":"s1",` +
	`"message":{"id":"m2","model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}],"usage":` +
	`{"input_tokens":4,"output_tokens":100,"cache_read_input_tokens":88000,"cache_creation_input_tokens":88500,` +
	`"cache_creation":{"ephemeral_5m_input_tokens":88000,"ephemeral_1h_input_tokens":0},"iterations":[` +
	`{"input_tokens":2,"output_tokens":50,"cache_creation_input_tokens":88000,` +
	`"cache_creation":{"ephemeral_5m_input_tokens":88000,"ephemeral_1h_input_tokens":0},"type":"message"},` +
	`{"input_tokens":177,"output_tokens":1422,"cache_creation_input_tokens":89334,` +
	`"cache_creation":{"ephemeral_5m_input_tokens":89334,"ephemeral_1h_input_tokens":0},` +
	`"type":"advisor_message","model":"claude-opus-5-5"},` +
	`{"input_tokens":2,"output_tokens":50,"cache_read_input_tokens":88000,"cache_creation_input_tokens":500,` +
	`"cache_creation":{"ephemeral_5m_input_tokens":500,"ephemeral_1h_input_tokens":0},"type":"message"}]}}}`

// A prefix rewrite is the request that followed the previous call: summing it
// with the request that read the rewritten prefix back never hides it.
func TestReplayFindsAPrefixRewriteInsideAnAdvisedResponse(t *testing.T) {
	a, _, _ := testAuditor(t)
	r := replayOne(t, a,
		assistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", 1000, 100, 80000, 7000),
		advisedRewrite)
	if want := 88000 * 2.5 / 1e6; r.rewrites.n != 1 || math.Abs(r.rewrites.usd-want) > 1e-12 {
		t.Errorf("rewrites %v at %v; want one, the first request's write %v", r.rewrites.n, r.rewrites.usd, want)
	}
}

// Every executor dollar is traced to the context that caused it: an advised
// response's categories sum to its requests' dollars, each request charged
// for the context it carried, never for what entered after it.
func TestReplayTracesAnAdvisedResponseToItsRequestsDollars(t *testing.T) {
	a, _, _ := testAuditor(t)
	r := replayOne(t, a, advisorAssistant("m1", "claude-sonnet-5", "2026-09-21T05:00:00Z", "claude-opus-5-5"))
	sum := 0.0
	for _, value := range r.cats {
		sum += value
	}
	if math.Abs(sum-advisorExecutorUSD) > 1e-12 {
		t.Errorf("categories sum to %v; want the executor's %v", sum, advisorExecutorUSD)
	}
}
