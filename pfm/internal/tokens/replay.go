package tokens

import (
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/costledger"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// tokenCounts are a run's tokens or dollars by billing column.
type tokenCounts struct{ in, out, cw5, cw1, cr float64 }

// sum is every column added, in the columns' order.
func (t tokenCounts) sum() float64 { return t.in + t.out + t.cw5 + t.cw1 + t.cr }

func (t tokenCounts) json() *object {
	return newObject().set("in", t.in).set("out", t.out).set("cw5", t.cw5).set("cw1", t.cw1).set("cr", t.cr)
}

// repeatedCommand is a shell command one run sent again and again.
type repeatedCommand struct {
	cmd string
	n   float64
}

// timelineRow is one model call of a --timeline run, or a response that billed
// only its advisor (consultOnly: no call, no context).
type timelineRow struct {
	n, ts, ctx, out float64
	advisor         advisorSpend // the consultations the call billed; their dollars are part of usd
	consultOnly     bool
	gap, usd        *float64
	tools           []*timelineSlot
}

// run is one transcript replayed: one main chat loop, one sub-agent, or (for
// --flight) one Codex thread.
type run struct {
	file, kind, sid, project, engine, agentID string
	tok                                       tokenCounts
	contractReads, failedCmds                 float64
	unpriced                                  bool
	title, agentType                          string
	depth, calls, usd                         float64
	usdBy                                     tokenCounts
	advisor                                   advisorSpend
	models, efforts                           *object
	t0, t1, activeMs, toolWaitMs              float64
	ctxFirst, ctxPeak, ctxSum                 float64
	marked                                    bool // a call set ctxFirst
	bands                                     *object
	rewrites                                  struct {
		n, usd  float64
		by, nBy *object
	}
	resets               float64
	tools, bash          *object
	errs                 float64
	rereadN, rereadChars float64
	cats                 [categoryCount]float64
	series               [][]float64
	harnessUSD           *float64
	anchors              costledger.Anchors // every cost-state line, each placed at its exit
	topRepeat            *repeatedCommand
	brief                *string
	stats                briefStats
	distinctRead         float64
	land                 [categoryCount]float64
	smallUSD, smallN     float64
	pollUSD, pollN       float64
	usdLC                float64
	f                    [6]float64
	attach               *object
	repeats              []repeatedCommand
	tl                   [48]float64
	tlf                  [5][48]float64
	firstTS              float64
	timeline             []timelineRow
	recT0, recT1, big    float64
	model, effort        string
	cmp                  string
	segments             float64 // Codex: resumed or compacted segments
}

// landing is one tool result carried through the run's later calls.
type landing struct {
	tool, target    string
	tok, at, ctx    float64
	model, modelKey string
}

// landingRow is a landing priced: its tokens times the later calls times the
// read rate; usd nil when the read rate is unpublished.
type landingRow struct {
	usd          *float64
	tokK, after  float64
	tool, target string
	run          float64
}

// segment is a stretch of the carried context made of one category.
type segment struct {
	cat int
	tok float64
}

// replay prices a collected transcript call by call and traces every dollar
// to the context that caused it: each call reads [0, cache read) at the read
// rate, writes [cache read, cache read + write) at the write rate, and pays
// the input rate for the rest. nil when the run made no priced or counted call.
func (a *auditor) replay(file string, c *collected) *run {
	project := foldCwd(c.cwd)
	if a.opts.project != "" && !strings.Contains(project, a.opts.project) {
		return nil
	}
	r := &run{
		file: file, kind: kindMain, sid: c.sid, project: project, engine: pricing.EngineClaude, agentID: c.sid,
		title: c.title, models: newObject(), efforts: newObject(), bands: newObject(), tools: newObject(),
		bash: newObject(), harnessUSD: c.harnessUSD, anchors: c.anchors,
		brief: c.brief, stats: c.stats, attach: newObject(),
		series: [][]float64{}, repeats: []repeatedCommand{}, firstTS: math.NaN(),
	}
	r.rewrites.by, r.rewrites.nBy = newObject(), newObject()
	if c.title == "" {
		r.title = c.aiTitle
	}
	if c.isSub {
		r.kind = kindAgent
		r.agentID, _ = transcript.SubagentID(filepath.Base(file))
		r.title, r.agentType, r.depth = c.meta.Description, c.meta.AgentType, float64(c.meta.SpawnDepth)
	}
	g := a.global
	var segs []segment
	var catTok [categoryCount]float64
	have, prevOut, prefixTok := 0.0, 0.0, 0.0
	first, compactPending := true, false
	var pending []event
	var prev *previousCall
	push := func(cat int, tok float64) {
		if tok <= 0 {
			return
		}
		if n := len(segs); n != 0 && segs[n-1].cat == cat {
			segs[n-1].tok += tok
		} else {
			segs = append(segs, segment{cat, tok})
		}
		catTok[cat] += tok
		have += tok
		r.land[cat] += tok
	}
	trim := func(excess float64) {
		for _, ownOnly := range []bool{true, false} {
			for i := len(segs) - 1; i >= 0 && excess > 0; i-- {
				if ownOnly && segs[i].cat != catOwn {
					continue
				}
				cut := math.Min(segs[i].tok, excess)
				segs[i].tok -= cut
				catTok[segs[i].cat] -= cut
				have -= cut
				excess -= cut
			}
		}
		kept := segs[:0]
		for _, s := range segs {
			if s.tok > 0 {
				kept = append(kept, s)
			}
		}
		segs = kept
	}
	var landed []landing
	cmdCount := newObject()
	lastToolTS := math.NaN()
	copiedCalls := 0
	for k := range c.seq {
		e := &c.seq[k]
		if e.compact {
			compactPending = true
			continue
		}
		if e.call == "" {
			pending = append(pending, *e)
			a.countContext(r, *e, project, cmdCount, &lastToolTS)
			continue
		}
		call := c.usage[e.call]
		u := call.usage
		if math.IsNaN(r.firstTS) {
			r.firstTS = call.ts
		}
		if len(call.requests) == 0 {
			// Only the advisor billed: the executor made no request, so the response is no
			// call and moves no context; its consultations are booked at their own model.
			switch {
			case call.ts < a.since:
			case call.copied:
				a.scan.copiedCalls++
				copiedCalls++
			default:
				a.countUnknown(call)
				// the response's own model is the run's even when only its advisor billed
				r.models.bump(modelKeyOf(call), 0)
				r.efforts.bump(call.effort, 0)
				r.span(call.ts)
				advisor := a.bookAdvisors(r, call, call.ts, project)
				if c.timelineRun {
					var usd *float64
					if advisor.unpriced == 0 {
						usd = &advisor.usd
					}
					r.timeline = append(
						r.timeline,
						timelineRow{n: r.calls, ts: call.ts, consultOnly: true, usd: usd, tools: c.issued[e.call]},
					)
				}
			}
			continue
		}
		cw5, cw1 := float64(u.Write5m), float64(u.Write1h)
		cwAll := cw5 + cw1
		cr, inp, out := float64(u.CacheRead), float64(u.Input), float64(u.Output)
		// a context is one request's: the last one the response made
		ctx := float64(call.requests[len(call.requests)-1].Context())
		reset := false
		if first {
			total := 0.0
			estimates := make([]float64, len(pending))
			for i := range pending {
				p := &pending[i]
				estimates[i] = p.chars / 4
				total += estimates[i]
			}
			factor := 1.0
			if total > ctx*0.8 {
				factor = ctx * 0.8 / total
			}
			prefixTok = ctx - float64(total*factor)
			push(catPrefix, prefixTok)
			for i := range pending {
				p := &pending[i]
				push(p.cat, estimates[i]*factor)
			}
			first = false
		} else {
			fresh := ctx - have
			switch {
			case compactPending || ctx < have*0.6:
				reset = true
				r.resets++
				segs, catTok, have = nil, [categoryCount]float64{}, 0
				clear(c.seenReads)
				push(catPrefix, math.Min(prefixTok, ctx))
				push(catSummary, ctx-have)
			case fresh < 0:
				trim(-fresh)
			default:
				own := math.Min(prevOut, fresh)
				push(catOwn, own)
				rest, chars := fresh-own, 0.0
				for k := range pending {
					p := &pending[k]
					chars += p.chars
				}
				if rest > 0 && chars > 0 {
					for k := range pending {
						p := &pending[k]
						tok := rest * p.chars / chars
						push(p.cat, tok)
						if p.tool != "" && tok > 8000 {
							landed = append(
								landed,
								landing{
									tool:     p.tool,
									target:   p.target,
									tok:      tok,
									at:       r.calls,
									model:    call.model,
									modelKey: modelKeyOf(call),
									ctx:      ctx,
								},
							)
						}
					}
				} else if rest > 0 {
					push(catUnexplained, rest)
				}
			}
		}
		compactPending = false
		// a harness attachment lands after most tool results: context, never the trigger
		var trigger, longest *event
		pendChars, onlyTools := 0.0, len(pending) > 0
		for i := range pending {
			p := &pending[i]
			if p.att == "" {
				trigger = p
			}
			if longest == nil && p.dur > 0 || longest != nil && p.dur > longest.dur {
				longest = p
			}
			pendChars += p.chars
			onlyTools = onlyTools && (p.tool != "" || p.att != "")
		}
		pending, prevOut = nil, out
		ts := call.ts
		if ts < a.since {
			prev = &previousCall{ctx, ts, call.model}
			continue
		}
		// A fork marker proves inheritance even without the origin file; otherwise the index
		// proves that the named origin holds this same call. Copies still shape carried context.
		if call.copied {
			a.scan.copiedCalls++
			copiedCalls++
			prev = &previousCall{ctx, ts, call.model}
			continue
		}
		a.countUnknown(call)
		row, _, found := a.prices.rates(call.model, ctx)
		parts, priced := row.ResponseRequestsCostParts(call.requests)
		modelKey := modelKeyOf(call)
		gap := (*float64)(nil)
		if !math.IsNaN(lastToolTS) {
			value := ts - lastToolTS
			gap = &value
		}
		if !found || !priced {
			// An unpriced model is IGNORANCE, not a $0 spend: its tokens stay in every
			// token total and its run renders "n/a", exactly as the Codex side does.
			a.scan.unpricedCalls++
			unpricedName := call.model
			if unpricedName == "" {
				unpricedName = noModel
			}
			a.scan.unpricedModels.bump(unpricedName, 1)
			r.unpriced = true
			r.calls++
			g.calls++
			r.models.bump(modelKey, 0)
			r.efforts.bump(call.effort, 0)
			r.tok.add(inp, out, cw5, cw1, cr)
			g.tok.add(inp, out, cw5, cw1, cr)
			advisor := a.bookAdvisors(r, call, ts, project)
			if c.timelineRun {
				r.timeline = append(
					r.timeline,
					timelineRow{
						n:       r.calls,
						ts:      ts,
						gap:     gap,
						ctx:     ctx,
						out:     out,
						advisor: advisor,
						tools:   c.issued[e.call],
					},
				)
			}
			r.mark(ts, ctx)
			prev = &previousCall{ctx, ts, call.model}
			continue
		}
		// a Codex row bills a write at in whatever its TTL: only a Claude row's tier is unknown
		if cwAll != 0 && !call.split && row.Engine != pricing.EngineCodex {
			a.scan.tierUnknownCalls++
		}
		usd := parts.Total()
		effortKey := r.kind + " · " + modelStem(call.model) + " · effort " + call.effort
		a.bookSpend(r, parts, ctx, out, ts, effortKey, project)
		read0 := r.cats[catReadFirst] + r.cats[catReadAgain] + r.cats[catBashRead]
		inj0 := r.cats[catHarness]
		for _, request := range call.requests {
			a.attribute(r, segs, &catTok, have, row, request)
		}
		r.cats[catOutput] += parts.Output
		g.cats[catOutput] += parts.Output
		g.tok.add(inp, out, cw5, cw1, cr)
		g.think += float64(call.thinking)
		g.calls++
		r.tok.add(inp, out, cw5, cw1, cr)
		r.calls++
		advisor := a.bookAdvisors(r, call, ts, project)
		if c.timelineRun {
			value := usd + advisor.usd
			r.timeline = append(
				r.timeline,
				timelineRow{
					n:       r.calls,
					ts:      ts,
					gap:     gap,
					ctx:     ctx,
					out:     out,
					usd:     &value,
					advisor: advisor,
					tools:   c.issued[e.call],
				},
			)
		}
		depth := "4 · calls 301+"
		switch {
		case r.calls <= 50:
			depth = "1 · calls 1–50"
		case r.calls <= 150:
			depth = "2 · calls 51–150"
		case r.calls <= 300:
			depth = "3 · calls 151–300"
		}
		depthKey := r.kind + " " + depth
		g.callIdx.bump2(depthKey, "usd", usd+advisor.usd)
		g.callIdx.bump2(depthKey, "calls", 1)
		g.callIdx.bump2(depthKey, "ctx", ctx)
		small := onlyTools && pendChars < 1600 && out < 250
		poll := trigger != nil && trigger.tool == toolBash &&
			(c.allCmd[trigger.target] >= 8 || bashPoll.MatchString(trigger.target))
		if small {
			r.smallUSD += usd
			r.smallN++
			g.small.bump2(r.kind, "usd", usd)
			g.small.bump2(r.kind, "n", 1)
		}
		if poll {
			r.pollUSD += usd
			r.pollN++
			g.poll.bump2(r.kind, "usd", usd)
			g.poll.bump2(r.kind, "n", 1)
		}
		r.models.bump(modelKey, usd)
		r.efforts.bump(call.effort, usd)
		r.mark(ts, ctx)
		since := 0.0
		if prev != nil {
			since = ts - prev.ts
			if since < 5*60e3 {
				r.activeMs += since
			}
		}
		// a rewrite is the request that followed the previous call: the response's first
		first := call.requests[0]
		firstParts, _ := row.ResponseCostParts(first)
		bust := a.rewrite(r, rewriteCheck{
			prev: prev, reset: reset, model: call.model, cr: float64(first.CacheRead),
			cwAll: float64(first.Write5m + first.Write1h), gap: since,
			usd: firstParts.Write5m + firstParts.Write1h, trigger: trigger, longest: longest,
		})
		reads := r.cats[catReadFirst] + r.cats[catReadAgain] + r.cats[catBashRead] - read0
		findings := [5]float64{0, 0, bust, reads, r.cats[catHarness] - inj0}
		if ctx > 200000 {
			findings[0] = usd
		}
		if small || poll {
			findings[1] = usd
		}
		bin := a.timeBin(ts)
		series := []float64{round(ctx / 1000), fixedNum((usd+advisor.usd)*1000, 1)}
		for k, value := range findings {
			r.f[k] += value
			r.tlf[k][bin] += value
			series = append(series, fixedNum(value*1000, 1))
		}
		r.series = append(r.series, series)
		prev = &previousCall{ctx, ts, call.model}
	}
	// a run that billed only advisor consultations is kept: its dollars are in the total
	if r.calls == 0 && r.advisor.calls == 0 {
		if copiedCalls != 0 {
			a.scan.copiesOnly = append(a.scan.copiesOnly, copyOnly{file: file, sid: c.sid})
		}
		return nil
	}
	r.recT0, r.recT1, r.big = c.recT0, c.recT1, c.big
	r.distinctRead = float64(len(c.seenReads))
	a.finishRun(r, landed, cmdCount, project)
	return r
}

// attribute traces one request's dollars to the context that caused it: it
// charges every carried token as a cache read, then corrects the tail beyond the
// request's cache read to its write and uncached input rates. A request that ran
// before the response's last carried only the context below its own size:
// what entered above it is uncharged. Rates are taken as
// multiples of base, the input rate; a free model (in: 0) uses base 1 so no ratio
// divides by zero; the uncached tail weighs un1 = in/base.
func (a *auditor) attribute(
	r *run, segs []segment, catTok *[categoryCount]float64, have float64, row pricing.Row, request pricing.ClaudeUsage,
) {
	g := a.global
	rates := row.RatesAt(request.Context())
	cw5, cw1 := float64(request.Write5m), float64(request.Write1h)
	cwAll, cr := cw5+cw1, float64(request.CacheRead)
	base := orZero(rates.In)
	if base == 0 {
		base = 1
	}
	perToken, readRatio, uncached := base/1e6, orZero(readRate(row, rates))/base, orZero(rates.In)/base
	w5m, w1h := rates.W5m, rates.W1h
	if row.Engine == pricing.EngineCodex {
		// a Codex row bills a written token at in (pricing.Row.ResponseCostParts)
		w5m, w1h = rates.In, rates.In
	}
	write := orZero(w5m) / base
	if cwAll != 0 {
		write = (float64(cw5*orZero(w5m)) + float64(cw1*orZero(w1h))) / (cwAll * base)
	}
	for k := range categoryCount {
		if catTok[k] != 0 {
			value := float64(float64(catTok[k]*readRatio) * perToken)
			r.cats[k] += value
			g.cats[k] += value
		}
	}
	top := math.Min(have, float64(request.Context()))
	for i, pos := len(segs)-1, have; i >= 0 && pos > cr; i-- {
		s := segs[i]
		lo, hi := pos-s.tok, pos
		pos = lo
		carried := math.Min(hi, top)
		read := math.Max(0, math.Min(carried, cr)-lo)
		written := math.Max(0, math.Min(carried, cr+cwAll)-math.Max(lo, cr))
		tail := math.Max(0, carried-math.Max(lo, cr+cwAll))
		diff := float64(read*readRatio) + float64(written*write) + float64(tail*uncached) - float64(s.tok*readRatio)
		value := float64(diff * perToken)
		r.cats[s.cat] += value
		g.cats[s.cat] += value
	}
}

// span widens the run's window to a booking at ts.
func (r *run) span(ts float64) {
	if r.t0 == 0 {
		r.t0 = ts
	}
	r.t1 = ts
}

// bookSpend books a priced booking's dollars: the billing columns, the run's
// totals, its context band, its kind · model · effort row, its cache-write tier,
// its hour and window bin.
func (a *auditor) bookSpend(r *run, parts pricing.ClaudeParts, ctx, out, ts float64, effortKey, project string) {
	g := a.global
	usd := parts.Total()
	r.usdBy.add(parts.Input, parts.Output, parts.Write5m, parts.Write1h, parts.CacheRead)
	g.usd.add(parts.Input, parts.Output, parts.Write5m, parts.Write1h, parts.CacheRead)
	r.usd += usd
	r.usdLC += usd
	band := bandOf(ctx)
	r.bands.bump(band, usd)
	g.bands.bump2(band, "usd", usd)
	g.bands.bump2(band, "calls", 1)
	g.modelEffort.bump2(effortKey, "usd", usd)
	g.modelEffort.bump2(effortKey, "calls", 1)
	g.modelEffort.bump2(effortKey, "out", out)
	g.tier.bump2(r.kind, "cw5", parts.Write5m)
	g.tier.bump2(r.kind, "cw1", parts.Write1h)
	g.hourly.bump2(utf16Slice(isoTime(ts), 0, 13), project, usd)
	r.tl[a.timeBin(ts)] += usd
}

// countUnknown counts the call's usage iterations of a type pfm does not know.
func (a *auditor) countUnknown(call *callUsage) {
	for _, kind := range call.unknown {
		a.scan.unknownIterations.bump(kind, 1)
	}
}

// bandOf is the name of the context band a request of ctx tokens falls in.
func bandOf(ctx float64) string {
	for _, b := range bands {
		if ctx/1000 < b.below {
			return b.name
		}
	}
	return bands[len(bands)-1].name
}

// timeBin is the 48th of the window a call at ts falls in.
func (a *auditor) timeBin(ts float64) int {
	return int(math.Max(0, math.Min(47, math.Floor((ts-a.since)/(a.hours*3600e3/48)))))
}

// previousCall is the call before the current one: its context, time and model.
type previousCall struct {
	ctx, ts float64
	model   string
}

func (t *tokenCounts) add(in, out, cw5, cw1, cr float64) {
	t.in += in
	t.out += out
	t.cw5 += cw5
	t.cw1 += cw1
	t.cr += cr
}

// mark records a counted call's time and context.
func (r *run) mark(ts, ctx float64) {
	if !r.marked {
		r.ctxFirst, r.marked = ctx, true
	}
	r.span(ts)
	r.ctxPeak = math.Max(r.ctxPeak, ctx)
	r.ctxSum += ctx
}

// countContext counts one non-call event inside the window: the tool, Bash and
// injection tallies the report reads.
func (a *auditor) countContext(r *run, e event, project string, cmdCount *object, lastToolTS *float64) {
	if e.tool != "" && !math.IsNaN(e.ts) {
		*lastToolTS = e.ts
	}
	if e.ts >= a.since && e.tool != "" {
		tool := r.tools.childWith(e.tool, "n", "chars", "err")
		tool.bump("n", 1)
		tool.bump("chars", e.chars)
		if e.err {
			tool.bump("err", 1)
			r.errs++
		}
		if e.bash != noBash {
			bash := r.bash.childWith(categories[e.bash], "n", "chars", "err", "waitMs")
			bash.bump("n", 1)
			bash.bump("chars", e.chars)
			bash.bump("waitMs", e.dur)
			if e.err {
				bash.bump("err", 1)
			}
			cmdCount.bump(e.target, 1)
		}
		if e.cat == catReadAgain {
			r.rereadN++
			r.rereadChars += e.chars
			key := project + " · " + strings.Replace(e.target, project+"/", "", 1)
			a.global.rereads.bump2(key, "n", 1)
			a.global.rereads.bump2(key, "chars", e.chars)
		}
		// the project contract the agent re-opens: by Read, or by a shell read of it
		if contract.MatchString(e.target) && (e.tool == toolRead || e.bash == catBashRead) {
			r.contractReads++
		}
		if e.tool == toolBash && e.err {
			r.failedCmds++
		}
		r.toolWaitMs += e.dur
	}
	if e.ts >= a.since && e.att != "" {
		a.global.attach.bump2(e.att, "n", 1)
		a.global.attach.bump2(e.att, "chars", e.chars)
		r.attach.bump2(e.att, "n", 1)
		r.attach.bump2(e.att, "chars", e.chars)
	}
}

// rewriteCheck is what decides whether a call re-billed the whole context.
type rewriteCheck struct {
	prev                *previousCall
	reset               bool
	model               string
	cr, cwAll, gap, usd float64
	trigger, longest    *event
}

// rewrite books a full cache rewrite — the whole context re-billed at write
// price — and names its cause; it returns the rewrite's dollars, 0 for none.
func (a *auditor) rewrite(r *run, check rewriteCheck) float64 {
	prev := check.prev
	if prev == nil || check.reset || prev.ctx <= 20000 || check.cr >= prev.ctx*0.5 || check.cwAll <= prev.ctx*0.5 {
		return 0
	}
	trig := ""
	if check.trigger != nil {
		trig = check.trigger.trig
	}
	var cause string
	switch {
	case prev.model != check.model:
		cause = "model switched mid-run"
	case check.gap <= 5*60e3:
		cause = "no idle gap: prefix invalidated"
	case trig == triggerToolResult && check.longest != nil && check.longest.dur > 5*60e3:
		cause = "a " + check.longest.tool + " call ran longer than the cache lives"
	case trig == triggerToolResult:
		cause = "waited >5m on a tool or permission"
	case trig == "agent report":
		cause = "idle >5m, woken by an agent report"
	case r.kind == kindAgent:
		cause = "idle >5m in the middle of the run"
	default:
		cause = "idle >5m, then the user came back"
	}
	r.rewrites.n++
	r.rewrites.usd += check.usd
	r.rewrites.by.bump(cause, check.usd)
	r.rewrites.nBy.bump(cause, 1)
	key := r.kind + " · " + cause
	g := a.global
	g.rewrites.bump2(key, "usd", check.usd)
	g.rewrites.bump2(key, "n", 1)
	g.rewrites.bump2(key, "tokK", check.cwAll/1000)
	if check.gap > 60*60e3 {
		g.rewrites.bump2(key, "over60m", 1)
	}
	if strings.HasPrefix(cause, "a ") {
		tool := check.longest.tool
		if tool == toolBash {
			tool = categories[check.longest.bash]
		}
		g.rewriteTool.bump2(tool, "usd", check.usd)
		g.rewriteTool.bump2(tool, "n", 1)
	}
	return check.usd
}

// finishRun books what only a whole run shows: the tool results it carried
// longest, the commands it repeated, its tool totals, its main model and effort.
func (a *auditor) finishRun(r *run, landed []landing, cmdCount *object, project string) {
	g := a.global
	for _, l := range landed {
		after := r.calls - l.at
		if after <= 0 {
			continue
		}
		var usd *float64
		if row, rates, found := a.prices.rates(l.model, l.ctx); found && readRate(row, rates) != nil {
			value := l.tok * after * *readRate(row, rates) / 1e6
			usd = &value
		} else {
			a.scan.noteOnce("tool-carry read-price estimate unavailable for " + l.modelKey)
		}
		g.landings = append(g.landings, landingRow{
			usd: usd, tokK: round(l.tok / 1000), after: after, tool: l.tool,
			target: utf16Slice(l.target, 0, 90), run: float64(len(a.runs)),
		})
	}
	stableSort(g.landings, func(left, right landingRow) float64 { return orZero(right.usd) - orZero(left.usd) })
	g.landings = g.landings[:min(len(g.landings), 200)]
	for _, item := range cmdCount.insertion() {
		n := item.value.(float64)
		if n < 4 {
			continue
		}
		repeat := g.repeats.childWith(project+" · "+item.key, "n", "maxInRun", "runs")
		repeat.bump("n", n)
		repeat.bump("runs", 1)
		repeat.set("maxInRun", math.Max(repeat.number("maxInRun"), n))
		if r.topRepeat == nil || n > r.topRepeat.n {
			r.topRepeat = &repeatedCommand{cmd: utf16Slice(item.key, 0, 90), n: n}
		}
		if n >= 8 {
			r.repeats = append(r.repeats, repeatedCommand{cmd: utf16Slice(item.key, 0, 110), n: n})
		}
	}
	for _, item := range r.tools.entries() {
		tool, total := item.child(), g.tools.childWith(item.key, "n", "chars", "err")
		for _, field := range []string{"n", "chars", "err"} {
			total.bump(field, tool.number(field))
		}
	}
	for _, item := range r.bash.entries() {
		bash, total := item.child(), g.bash.childWith(item.key, "n", "chars", "err", "waitMs")
		for _, field := range []string{"n", "chars", "err", "waitMs"} {
			total.bump(field, bash.number(field))
		}
	}
	r.model, r.effort = topKey(r.models), topKey(r.efforts)
}

// topKey is the key of the largest value, the first in property order on a tie.
func topKey(o *object) string {
	entries := o.entries()
	stableSort(entries, func(left, right entry) float64 { return right.value.(float64) - left.value.(float64) })
	return entries[0].key
}

// orZero is a nullable number as JavaScript arithmetic reads it: null is 0.
func orZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

// stableSort is Array.prototype.sort with a comparator: stable, a NaN
// comparison reading as equal.
func stableSort[T any](items []T, compare func(left, right T) float64) {
	sort.SliceStable(items, func(left, right int) bool { return compare(items[left], items[right]) < 0 })
}

// modelKeyOf is the call's model as an object key: String(undefined) when the
// record named none.
func modelKeyOf(call *callUsage) string {
	if call.noModel {
		return jsUndefined
	}
	return call.model
}
