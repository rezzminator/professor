package tokens

import (
	"context"
	"math"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// flightTokens is a token count as a metrics row shows it.
func flightTokens(value float64) string {
	switch {
	case value >= 1e6:
		return fixed(value/1e6, 1) + "M"
	case value >= 1000:
		return num(round(value/1000)) + "K"
	}
	return num(round(value))
}

func flightCash(r *run) string {
	if r.unpriced {
		return notPriced
	}
	return "$" + fixed(r.usd, 2)
}

func flightMinutes(r *run) string {
	if r.t1 != 0 && r.t0 != 0 {
		return num(round((r.t1-r.t0)/60e3)) + "m"
	}
	return "?"
}

func flightGrowth(r *run) string {
	if r.calls == 0 {
		return "?"
	}
	return flightTokens(math.Max(0, r.ctxPeak-r.ctxFirst) / r.calls)
}

func ledgerType(row flightRow) string {
	return firstText(firstText(row.led.agentType, row.run.agentType), "?")
}

// flightFold is the totals over a set of matched rows.
type flightFold struct {
	n, calls, usd, unpriced, in, cr, out, fail, poll, reread, contract, compact, over, peak, lc float64
	advisor                                                                                     advisorSpend // part of usd; not a data-block member
}

func foldRows(rows []flightRow) flightFold {
	var f flightFold
	for _, row := range rows {
		r := row.run
		f.n++
		f.calls += r.calls
		f.usd += r.usd
		f.advisor.merge(r.advisor)
		if r.unpriced {
			f.unpriced++
		}
		f.in += r.tok.in + r.tok.cw5 + r.tok.cw1
		f.cr += r.tok.cr
		f.out += r.tok.out
		f.fail += r.failedCmds
		f.poll += r.pollN
		f.reread += r.rereadN
		f.contract += r.contractReads
		f.compact += r.resets
		if r.calls > callCap(firstText(row.led.agentType, r.agentType)) {
			f.over++
		}
		f.peak = math.Max(f.peak, r.ctxPeak)
		if r.usdLC != 0 {
			f.lc += r.usdLC
		} else {
			f.lc += r.usd
		}
	}
	return f
}

// flightFoldKeys are flightFold's JSON members in the order metrics.md's
// data block writes them.
var flightFoldKeys = [...]string{
	"n",
	"calls",
	"usd",
	"unpriced",
	"in",
	"cr",
	"out",
	"fail",
	"poll",
	"reread",
	"contract",
	"compact",
	"over",
	"peak",
	"lc",
}

func (f flightFold) json() *object {
	values := [len(flightFoldKeys)]float64{
		f.n,
		f.calls,
		f.usd,
		f.unpriced,
		f.in,
		f.cr,
		f.out,
		f.fail,
		f.poll,
		f.reread,
		f.contract,
		f.compact,
		f.over,
		f.peak,
		f.lc,
	}
	o := newObject()
	for i, key := range flightFoldKeys {
		o.set(key, values[i])
	}
	return o
}

// unledgered is every transcript under the flight's session or thread with no
// ledger row: a hole in the ledger, never a drop. It is priced, so a child
// foreman, a lander or a skill-spawned review never vanishes from the spend.
func (a *auditor) unledgered(m *flightMatch, candidates []*codexCandidate, audit func(*codexCandidate) *run) []*run {
	parents := map[string]bool{}
	for _, row := range m.matched {
		if row.run.sid != "" {
			parents[row.run.sid] = true
		}
	}
	var out []*run
	for _, r := range a.runs {
		if r.kind != kindAgent || m.takenClaude[r.agentID] || !parents[r.sid] || !(r.t0 >= a.since) {
			continue
		}
		out = append(out, r)
		m.unmatched = append(m.unmatched, "TRANSCRIPT "+firstText(r.agentType, kindAgent)+" · claude · "+
			utf16Slice(r.agentID, 0, 12)+" · "+utf16Slice(isoTime(r.t0), 0, 16)+" · "+flightCash(r)+
			" · ran under this flight's session with no ledger row")
	}
	for _, c := range candidates {
		parent := ""
		if c.meta != nil {
			parent = firstText(c.meta.ParentThreadID, c.meta.SessionID)
		}
		if m.takenCodex[c.file] || !parents[parent] {
			continue
		}
		r := audit(c)
		out = append(out, r)
		id := jsUndefined
		if len(c.ids) != 0 {
			id = c.ids[0]
		}
		m.unmatched = append(
			m.unmatched,
			"TRANSCRIPT "+firstText(c.role, pricing.EngineCodex)+" · codex · "+utf16Tail(id, 12)+
				" · "+utf16Slice(isoTime(c.at), 0, 16)+" · "+flightCash(r)+" · ran under this flight's thread with no ledger row",
		)
	}
	return out
}

// runFlight writes one row per agent the flight spawned, bounded whatever
// its size, to metrics.md and stdout.
func (a *auditor) runFlight(ctx context.Context) (int, error) {
	const maxRows, maxUnmatched = 60, 15
	plan := a.flight
	dir, err := filepath.Abs(a.opts.flight)
	if err != nil {
		return 1, failure("--flight %s: %v", a.opts.flight, err)
	}
	// Codex candidates: meta only. A full audit costs a whole-file decode, so it runs on matches.
	candidates := a.codexCandidates(ctx)
	audited := map[string]*run{}
	audit := func(c *codexCandidate) *run {
		if r, ok := audited[c.file]; ok {
			return r
		}
		r := a.codexAuditRun(c.file, c.meta)
		audited[c.file] = r
		return r
	}
	m := a.matchLedger(candidates, audit)
	unledgered := a.unledgered(m, candidates, audit)
	var ledgeredUSD float64
	unpricedUnledgered := 0.0
	for _, r := range unledgered {
		if r.unpriced {
			unpricedUnledgered++
		} else {
			ledgeredUSD += r.usd
		}
	}
	end := plan.start
	for _, row := range m.matched {
		end = math.Max(end, row.run.t1)
	}
	startISO, endISO := isoTime(plan.start), isoTime(end)
	count := func(engine, how string) string {
		n := 0
		for _, row := range m.matched {
			if (engine != "" && row.run.engine == engine) || (how != "" && row.how == how) {
				n++
			}
		}
		return num(float64(n))
	}
	baseline := ""
	if plan.baseline != "" {
		baseline = " · baseline " + plan.baseline
	}
	out := []string{
		"# " + filepath.Base(dir) + " · metrics",
		"",
		"source " + plan.source + baseline + " · window " + startISO + " → " + endISO,
		num(float64(len(m.matched))) + " matched of " + num(float64(len(plan.rows))) + " ledger rows · claude " +
			count(
				pricing.EngineClaude,
				"",
			) + " · codex " + count(pricing.EngineCodex, "") + " · by id " + count("", "id") + " · by agent path " +
			count("", "path") + " · by window " + count("", "window"),
		"",
		"| task | agent | engine | model | calls | wall | ctx0 | peak | +/call | input | cached | output | ttl | $ | fail | " +
			"poll | reread | contract | compact | cap | matched |",
		"|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|",
	}
	sorted := append([]flightRow(nil), m.matched...)
	stableSort(sorted, func(left, right flightRow) float64 { return right.run.usd - left.run.usd })
	for _, row := range firstN(sorted, maxRows) {
		r := row.run
		limit := callCap(firstText(row.led.agentType, r.agentType))
		capText := "ok/" + num(limit)
		if r.calls > limit {
			capText = "OVER " + num(limit)
		}
		out = append(out, "| "+row.led.taskID+" | "+ledgerType(row)+" | "+r.engine+" | "+modelStem(codexShort(r.model))+
			" | "+num(r.calls)+" | "+flightMinutes(r)+" | "+flightTokens(r.ctxFirst)+" | "+flightTokens(r.ctxPeak)+" | "+
			flightGrowth(r)+" | "+flightTokens(r.tok.in+r.tok.cw5+r.tok.cw1)+" | "+flightTokens(r.tok.cr)+" | "+
			flightTokens(
				r.tok.out,
			)+" | "+ttlMix(r.tok.cw5, r.tok.cw1)+" | "+flightCash(r)+" | "+num(r.failedCmds)+" | "+
			num(
				r.pollN,
			)+" | "+num(r.rereadN)+" | "+num(r.contractReads)+" | "+num(r.resets)+" | "+capText+" | "+row.how+" |")
	}
	if len(sorted) > maxRows {
		out = append(out, "| … | "+num(float64(len(sorted)-maxRows))+
			" further agents folded into the totals below | | | | | | | | | | | | | | | | | | | |")
	}
	out = append(
		out,
		"",
		"## totals by agent type",
		"",
		"| agent type | n | calls | input | cached | output | $ | fail | poll | reread | contract | compact | over cap |",
		"|---|---|---|---|---|---|---|---|---|---|---|---|---|",
	)
	byType, members := newObject(), map[string][]flightRow{}
	for _, row := range m.matched {
		key := firstText(firstText(row.led.agentType, row.run.agentType), "(untyped)")
		byType.set(key, true)
		members[key] = append(members[key], row)
	}
	types := byType.ordered()
	stableSort(
		types,
		func(left, right string) float64 { return foldRows(members[right]).usd - foldRows(members[left]).usd },
	)
	for _, key := range firstN(types, 12) {
		f := foldRows(members[key])
		usd := "$" + fixed(f.usd, 2)
		if f.unpriced != 0 {
			usd += " + " + num(f.unpriced) + " n/a"
		}
		out = append(out, "| "+key+" | "+num(f.n)+" | "+num(f.calls)+" | "+flightTokens(f.in)+" | "+flightTokens(f.cr)+
			" | "+flightTokens(f.out)+" | "+usd+" | "+num(f.fail)+" | "+num(f.poll)+" | "+num(f.reread)+" | "+
			num(f.contract)+" | "+num(f.compact)+" | "+num(f.over)+" |")
	}
	total := foldRows(m.matched)
	unpricedNote := ""
	if total.unpriced != 0 {
		unpricedNote = " + " + num(total.unpriced) +
			` unpriced agent(s) at "n/a" (their tokens ARE above, their dollars are NOT)`
	}
	out = append(out, "", "## flight total", "",
		num(total.n)+" agents · "+num(total.calls)+" calls · peak context "+flightTokens(total.peak)+" · input "+
			flightTokens(total.in)+" · cached "+flightTokens(total.cr)+" · output "+flightTokens(total.out)+" · **$"+
			fixed(total.usd, 2)+"**"+advisorNote(total.advisor)+unpricedNote,
		"failed commands "+num(total.fail)+" · poll calls "+num(total.poll)+" · re-reads "+num(total.reread)+
			" · contract-file reads "+num(total.contract)+" · compactions "+num(total.compact)+" · over cap "+
			num(total.over)+" of "+num(total.n))
	if len(unledgered) != 0 {
		unpriced := ""
		if unpricedUnledgered != 0 {
			unpriced = " + " + num(unpricedUnledgered) + " n/a"
		}
		out = append(out, "unledgered "+num(float64(len(unledgered)))+" transcript(s) under this flight's session · $"+
			fixed(ledgeredUSD, 2)+unpriced+" — the flight's spend is $"+fixed(total.usd+ledgeredUSD, 2)+
			" (see the unmatched section)")
	}
	out = append(out, "", "## three most expensive agents", "")
	for _, row := range firstN(sorted, 3) {
		r := row.run
		out = append(out, "- "+flightCash(r)+" · "+row.led.taskID+" · "+ledgerType(row)+" · "+r.engine+" · "+
			num(r.calls)+" calls · peak "+flightTokens(r.ctxPeak)+" · "+flightMinutes(r))
	}
	out = append(out, "", "## unmatched", "")
	if len(m.unmatched) == 0 {
		out = append(
			out,
			"none — every ledger row found a transcript and every transcript in the window has a ledger row",
		)
	} else {
		for _, line := range firstN(m.unmatched, maxUnmatched) {
			out = append(out, "- "+line)
		}
		if len(m.unmatched) > maxUnmatched {
			out = append(out, "- … and "+num(float64(len(m.unmatched)-maxUnmatched))+" more")
		}
	}
	// one aggregate note, not one per row: a 200-agent flight must not bury the gaps line
	if len(m.mismatches) != 0 {
		more := ""
		if len(m.mismatches) > 5 {
			more = ", …"
		}
		plan.gaps = append(plan.gaps, num(float64(len(m.mismatches)))+
			" window match(es) whose transcript role differs from the ledger's agent type ("+
			strings.Join(firstN(m.mismatches, 5), ", ")+more+")")
	}
	a.scan.notes = append(a.scan.notes, plan.gaps...)
	if len(m.unmatched) != 0 {
		a.scan.notes = append(a.scan.notes, num(float64(len(m.unmatched)))+" UNMATCHED (see the unmatched section)")
	}
	ratio := notPriced
	if total.usd != 0 {
		ratio = fixed(total.lc/total.usd, 2)
	}
	ledger := a.flightLedgerLines(m.matched)
	out = append(out, "", a.gapsLine(), "")
	out = append(out, ledger...)
	out = append(out, "",
		"cross-check: at the published context tiers (per-model rate in the pfm model-cost table — an estimate) this "+
			"flight reads $"+fixed(total.lc, 2)+" ("+ratio+"x the headline). The harness writes its own cost-state line "+
			"only for a main chat, and a flight's agents are sub-runs, so these dollars are UNCHECKED against the harness "+
			"on this host.")
	dest := filepath.Join(dir, "metrics.md")
	if a.opts.metricsOut != "" {
		if dest, err = filepath.Abs(a.opts.metricsOut); err != nil {
			return 1, failure("--metrics-out %s: %v", a.opts.metricsOut, err)
		}
	}
	text := strings.Join(out, "\n")
	if _, err := writeOutput(dest, text+"\n"); err != nil {
		return 1, err
	}
	a.print(text)
	a.print("\nmetrics → " + dest + " (" + num(float64(len(out))) + " lines)")
	if a.opts.out != "" {
		if err := a.writeFlightOut(dir, startISO, endISO, total, sorted, m.unmatched, unledgered, ledgeredUSD,
			unpricedUnledgered); err != nil {
			return 1, err
		}
	}
	// UNMATCHED is a reported condition, not a failure. A read error is: it means we failed
	// to LOOK, and a report built over a root we could not read must not read as success.
	if len(a.scan.readErrors) != 0 {
		return 1, nil
	}
	return 0, nil
}

func (a *auditor) writeFlightOut(dir, startISO, endISO string, total flightFold, sorted []flightRow, unmatched []string,
	unledgered []*run, usd, unpriced float64,
) error {
	rows := make([]*object, len(sorted))
	for i, row := range sorted {
		r := row.run
		var cost any
		if !r.unpriced {
			cost = fixedNum(r.usd, 4)
		}
		rows[i] = row.led.json().set("engine", r.engine).set("how", row.how).set("model", r.model).
			set("calls", r.calls).set("wallMs", r.t1-r.t0).set("ctxFirst", r.ctxFirst).set("ctxPeak", r.ctxPeak).
			set("tok", r.tok.json()).set("usd", cost).set("failedCmds", r.failedCmds).set("pollN", r.pollN).
			set("rereadN", r.rereadN).set("contractReads", r.contractReads).set("compactions", r.resets).
			set("overCap", r.calls > callCap(firstText(row.led.agentType, r.agentType)))
		if r.advisor.calls != 0 {
			rows[i].set("advisor", r.advisor.json())
		}
	}
	unmatchedList := append([]string{}, unmatched...)
	document := newObject().set("v", 1).set("flight", filepath.Base(dir)).set("source", a.flight.source).
		set("baseline", a.flight.baseline).set("window", []string{startISO, endISO}).set("scan", a.scan.json()).
		set("total", total.json()).set("rows", rows).set("unmatched", unmatchedList).
		set("unledgered", newObject().set("n", len(unledgered)).set("usd", fixedNum(usd, 4)).set("unpriced", unpriced))
	if sessions, ok := a.ledgerJSON(); ok {
		document.set("costLedger", sessions)
	}
	if _, err := writeOutput(a.opts.out, stringify(document, 1)); err != nil {
		return err
	}
	a.print("full data → " + a.opts.out)
	return nil
}
