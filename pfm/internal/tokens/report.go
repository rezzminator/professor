package tokens

import (
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// writeOutput replaces path whole, its directory created; the bytes written.
func writeOutput(path, content string) (int, error) {
	absolute, err := filepath.Abs(path)
	if err == nil {
		err = atomicfile.Write(absolute, []byte(content), 0o644)
	}
	if err != nil {
		return 0, failure("write %s: %v", path, err)
	}
	return len(content), nil
}

// project is one project's totals.
type project struct {
	usd, main, agent, runs, rewrites float64
	advisor                          advisorSpend
	cats                             [categoryCount]float64
}

// family is a main chat plus every sub-agent it spawned.
type family struct {
	sid, title, project            string
	own, agents, nAgents, rewrites float64
	calls, peakK, t0, t1           float64
	agentRuns                      []*run
}

// group is the agent runs of one project, agent type and model.
type group struct {
	k                                     string
	n, usd, med, p90, max                 float64
	calls, peakK, wall, errs, poll, small float64
	tests, rereads                        float64
	ttl                                   string
}

// totals are the aggregates every section reads.
type totals struct {
	total     float64
	byProject *object // project path → *project, in property order
	projects  map[string]*project
	families  map[string]*family
	famList   []*family
	groupRows []group
}

// quantile is the p-quantile of values: the sorted value at floor(p·n).
func quantile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	stableSort(sorted, func(left, right float64) float64 { return left - right })
	return sorted[min(len(sorted)-1, int(math.Floor(p*float64(len(sorted)))))]
}

func wallMinutes(r *run) float64 { return (r.t1 - r.t0) / 60e3 }

// jsBasename is path.basename: "" for "" and "/", trailing slashes ignored.
func jsBasename(path string) string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return ""
	}
	return path[strings.LastIndex(path, "/")+1:]
}

// mapOf is value of every item, in order.
func mapOf[T any](items []T, value func(T) float64) []float64 {
	out := make([]float64, len(items))
	for i, item := range items {
		out[i] = value(item)
	}
	return out
}

// sumOf is value summed over items, in order.
func sumOf[T any](items []T, value func(T) float64) float64 {
	total := 0.0
	for _, item := range items {
		total += value(item)
	}
	return total
}

// medianOf is the median of value over items.
func medianOf[T any](items []T, value func(T) float64) float64 {
	return quantile(mapOf(items, value), 0.5)
}

func (r *run) testRuns() float64 {
	if bash, ok := r.bash.get(categories[catBashTest]); ok {
		return bash.(*object).number("n")
	}
	return 0
}

func (a *auditor) aggregate() *totals {
	g := a.global
	t := &totals{
		total:     g.usd.sum(),
		byProject: newObject(), projects: map[string]*project{}, families: map[string]*family{},
	}
	familyOrder := newObject()
	for _, r := range a.runs {
		p := t.projects[r.project]
		if p == nil {
			p = &project{}
			t.projects[r.project] = p
			t.byProject.set(r.project, true)
		}
		p.usd += r.usd
		if r.kind == kindMain {
			p.main += r.usd
		} else {
			p.agent += r.usd
		}
		p.runs++
		p.rewrites += r.rewrites.usd
		p.advisor.merge(r.advisor)
		for i, value := range r.cats {
			p.cats[i] += value
		}
		f := t.families[r.sid]
		if f == nil {
			f = &family{sid: r.sid, project: r.project, t0: math.Inf(1)}
			t.families[r.sid] = f
			familyOrder.set(r.sid, true)
		}
		if r.kind == kindMain {
			f.title, f.project = r.title, r.project
			f.own += r.usd
			f.peakK = round(r.ctxPeak / 1000)
		} else {
			f.agents += r.usd
			f.nAgents++
			f.agentRuns = append(f.agentRuns, r)
		}
		f.rewrites += r.rewrites.usd
		f.calls += r.calls
		f.t0, f.t1 = math.Min(f.t0, r.t0), math.Max(f.t1, r.t1)
	}
	for _, sid := range familyOrder.ordered() {
		t.famList = append(t.famList, t.families[sid])
	}
	stableSort(t.famList, func(left, right *family) float64 {
		return right.own + right.agents - (left.own + left.agents)
	})
	groups := newObject()
	members := map[string][]*run{}
	for _, r := range a.runs {
		if r.kind != kindAgent {
			continue
		}
		agentType := r.agentType
		if agentType == "" {
			agentType = "(untyped)"
		}
		key := jsBasename(r.project) + " · " + agentType + " · " + modelStem(r.model)
		groups.set(key, true)
		members[key] = append(members[key], r)
	}
	for _, key := range groups.ordered() {
		rs := members[key]
		n := float64(len(rs))
		usd := mapOf(rs, func(r *run) float64 { return r.usd })
		peak := math.Inf(-1)
		for _, value := range usd {
			peak = math.Max(peak, value)
		}
		var w5, w1 float64
		for _, r := range rs {
			w5, w1 = w5+r.tok.cw5, w1+r.tok.cw1
		}
		t.groupRows = append(t.groupRows, group{
			k:     key,
			n:     n,
			usd:   sumOf(rs, func(r *run) float64 { return r.usd }),
			med:   quantile(usd, 0.5),
			p90:   quantile(usd, 0.9),
			max:   peak,
			calls: medianOf(rs, func(r *run) float64 { return r.calls }),
			peakK: medianOf(rs, func(r *run) float64 { return r.ctxPeak / 1000 }),
			wall:  medianOf(rs, wallMinutes),
			errs:  sumOf(rs, func(r *run) float64 { return r.errs }) / n,
			poll:  sumOf(rs, func(r *run) float64 { return r.pollUSD }),
			small: sumOf(rs, func(r *run) float64 { return r.smallUSD }),
			tests: sumOf(
				rs,
				(*run).testRuns,
			) / n,
			rereads: sumOf(rs, func(r *run) float64 { return r.rereadN }) / n,
			ttl:     ttlMix(w5, w1),
		})
	}
	stableSort(t.groupRows, func(left, right group) float64 { return right.usd - left.usd })
	return t
}

// sortedEntries is Object.entries(o) sorted by a field of each member object, largest first.
func sortedEntries(o *object, field string) []entry {
	entries := o.entries()
	stableSort(entries, func(left, right entry) float64 {
		return right.child().number(field) - left.child().number(field)
	})
	return entries
}

// advisorNote is " · advisor $X" for a run set that consulted an advisor
// ("n/a" when the table could not price a consultation), "" for none.
func advisorNote(advisor advisorSpend) string {
	if advisor.calls == 0 {
		return ""
	}
	return " · advisor " + advisor.cash()
}

// meanContext is a run's mean context per call, 0 for a run that billed only
// advisor consultations.
func meanContext(r *run) float64 {
	if r.calls == 0 {
		return 0
	}
	return r.ctxSum / r.calls
}

func firstN[T any](items []T, n int) []T { return items[:max(0, min(n, len(items)))] }

// categoryMix is the top categories of cats as "name share" pairs.
func categoryMix(cats [categoryCount]float64, total float64, n int) string {
	type share struct {
		name  string
		value float64
	}
	shares := make([]share, categoryCount)
	for i, name := range categories {
		shares[i] = share{name, cats[i]}
	}
	stableSort(shares, func(left, right share) float64 { return right.value - left.value })
	var parts []string
	for _, s := range firstN(shares, n) {
		name, _, _ := strings.Cut(s.name, " (")
		parts = append(parts, name+" "+pctText(s.value, total))
	}
	return strings.Join(parts, " · ")
}

var (
	depthDigit  = jsRegexp(` \d · `)
	leadingPath = jsRegexp(`^\S*/`)
)

func (a *auditor) runReport(t *totals) (int, error) {
	g, scan, top := a.global, a.scan, a.opts.top
	total := t.total
	pct := func(value float64) string { return pctText(value, total) }
	heading := func(text string) { a.print("\n== " + text) }
	mains, agents := 0, 0
	for _, r := range a.runs {
		if r.kind == kindMain {
			mains++
		} else {
			agents++
		}
	}
	a.print("TOKEN AUDIT · last " + num(a.hours) + "h · host " + a.host + " · " + isoTime(a.now))
	a.print("scanned " + strings.Join(scan.roots, ", ") + " · " + num(float64(scan.files)) + " transcripts (" +
		num(
			float64(mains),
		) + " main, " + num(float64(agents)) + " agent runs with priced calls) · " + num(g.calls) + " API calls")
	a.print(a.gapsLine())

	heading("1 · TOTAL " + dollars(total) + " by billing class")
	for _, class := range []struct {
		usd, tok float64
		name     string
	}{
		{g.usd.cr, g.tok.cr, "re-reading cached context (per-model read rate)"},
		{g.usd.cw5, g.tok.cw5, "writing context to the 5-minute cache (1.25x)"},
		{g.usd.cw1, g.tok.cw1, "writing context to the 1-hour cache (2x)"},
		{g.usd.out, g.tok.out, "output tokens"},
		{g.usd.in, g.tok.in, "uncached input (1x)"},
	} {
		a.print("  " + padStart(dollars(class.usd), 9) + " " + padStart(pct(class.usd), 6) + "  " + class.name + " · " +
			millions(class.tok) + " tok")
	}
	if g.advisor.calls != 0 {
		share, unpriced := pct(g.advisor.usd), ""
		if g.advisor.unpriced != 0 {
			share, unpriced = notPriced, " · "+num(g.advisor.unpriced)+" unpriced (tokens counted, dollars n/a)"
		}
		a.print("  " + padStart(g.advisor.cash(), 9) + " " + padStart(share, 6) +
			"  of which advisor spend (each consultation at its own model's rates) · " + num(g.advisor.calls) +
			"× consulted · " + millions(g.advisor.tok.sum()) + " tok" + unpriced)
	}
	mainUSD := 0.0
	for _, r := range a.runs {
		if r.kind == kindMain {
			mainUSD += r.usd
		}
	}
	a.print("  main chat loops " + dollars(mainUSD) + " (" + pct(mainUSD) + ") · sub-agents " + dollars(total-mainUSD) +
		" (" + pct(total-mainUSD) + ") · thinking " + millions(g.think) + " of " + millions(g.tok.out-g.advisor.tok.out) +
		" output tok")
	for _, item := range g.tier.entries() {
		v := item.child()
		a.print("  cache writes by " + item.key + " runs: 5-minute " + dollars(v.number("cw5")) + " · 1-hour " +
			dollars(v.number("cw1")))
	}

	heading("2 · BY PROJECT")
	byUSD := a.projectsByUSD(t)
	for _, path := range firstN(byUSD, 8) {
		v := t.projects[path]
		a.print("  " + padStart(dollars(v.usd), 9) + " " + padStart(pct(v.usd), 6) + "  " + cut(path, 44) + " main " +
			dollars(
				v.main,
			) + " · agents " + dollars(v.agent) + " · " + num(v.runs) + " runs · rewrites " + dollars(v.rewrites) +
			advisorNote(v.advisor))
	}

	heading("3 · BY KIND · MODEL · EFFORT")
	for _, item := range firstN(sortedEntries(g.modelEffort, "usd"), top) {
		v := item.child()
		a.print("  " + padStart(dollars(v.number("usd")), 9) + " " + padStart(pct(v.number("usd")), 6) + "  " +
			padStart(num(v.number("calls")), 6) + " calls · " +
			padStart(dollars(v.number("usd")/v.number("calls")*100)+"/100 calls", 16) + " · " + item.key)
	}

	heading("4 · WHAT THE MONEY PAID FOR (every dollar traced to the context that caused it)")
	type catRow struct {
		name  string
		value float64
	}
	var catRows []catRow
	for i, name := range categories {
		if g.cats[i] > total*0.002 {
			catRows = append(catRows, catRow{name, g.cats[i]})
		}
	}
	if g.advisor.usd > total*0.002 {
		catRows = append(
			catRows,
			catRow{"advisor consultations (a second model's whole context and answer)", g.advisor.usd},
		)
	}
	stableSort(catRows, func(left, right catRow) float64 { return right.value - left.value })
	for _, row := range catRows {
		a.print("  " + padStart(dollars(row.value), 9) + " " + padStart(pct(row.value), 6) + "  " + row.name)
	}
	for _, path := range firstN(byUSD, 3) {
		v := t.projects[path]
		sum := 0.0
		for _, value := range v.cats {
			sum += value
		}
		a.print("  · " + path + ": " + categoryMix(v.cats, sum, 5))
	}

	heading("5 · HOW BIG WAS THE CONTEXT WHEN THE MONEY WAS SPENT")
	for _, band := range bands {
		if value, ok := g.bands.get(band.name); ok {
			b := value.(*object)
			a.print("  " + padStart(dollars(b.number("usd")), 9) + " " + padStart(pct(b.number("usd")), 6) + "  " +
				padStart(num(b.number("calls")), 6) + " calls · " +
				padStart(dollars(b.number("usd")/b.number("calls")*100), 7) + "/100 calls · context " + band.name)
		}
	}
	a.print("  and how deep into a run it was spent (a run's context only grows, so late calls are the dear ones):")
	depthEntries := g.callIdx.entries()
	sort.SliceStable(
		depthEntries,
		func(left, right int) bool { return depthEntries[left].key < depthEntries[right].key },
	)
	for _, item := range depthEntries {
		v := item.child()
		a.print("  " + padStart(dollars(v.number("usd")), 9) + " " + padStart(pct(v.number("usd")), 6) + "  " +
			padStart(num(v.number("calls")), 6) + " calls · mean context " +
			padStart(num(round(v.number("ctx")/v.number("calls")/1000)), 4) + "K · " +
			depthDigit.ReplaceAllString(item.key, " · "))
	}
	a.print("  calls that moved almost nothing yet re-read the whole context:")
	for _, item := range g.small.entries() {
		v := item.child()
		a.print("  " + padStart(dollars(v.number("usd")), 9) + " " + padStart(pct(v.number("usd")), 6) + "  " +
			padStart(num(v.number("n")), 6) + " calls · " + item.key +
			" · small steps (under ~400 tokens came in, under 250 went out)")
	}
	for _, item := range g.poll.entries() {
		v := item.child()
		a.print("  " + padStart(dollars(v.number("usd")), 9) + " " + padStart(pct(v.number("usd")), 6) + "  " +
			padStart(num(v.number("n")), 6) + " calls · " + item.key +
			" · answering a shell command the run repeated 8+ times (polling, log-peeking)")
	}

	heading("6 · FULL CACHE REWRITES (the whole context re-billed at write price)")
	rewriteTotal := 0.0
	for _, item := range g.rewrites.entries() {
		rewriteTotal += item.child().number("usd")
	}
	a.print("  " + dollars(rewriteTotal) + " total (" + pct(rewriteTotal) + ")")
	for _, item := range sortedEntries(g.rewrites, "usd") {
		v := item.child()
		over := ""
		if v.number("over60m") != 0 {
			over = " · " + num(v.number("over60m")) + " after >60m"
		}
		a.print("  " + padStart(dollars(v.number("usd")), 9) + "  " + padStart(num(v.number("n")), 4) + "× · avg " +
			padStart(num(round(v.number("tokK")/v.number("n")))+"K", 5) + " tok" + over + " · " + item.key)
	}
	for _, item := range firstN(sortedEntries(g.rewriteTool, "usd"), 5) {
		v := item.child()
		a.print("    the long call was: " + item.key + " · " + num(v.number("n")) + "× · " + dollars(v.number("usd")))
	}

	heading("7 · TOP " + num(float64(top)) + " CHAT FAMILIES (a chat + the agents it spawned)")
	for _, f := range firstN(t.famList, top) {
		name := f.title
		if name == "" {
			name = utf16Slice(f.sid, 0, 8)
		}
		base := jsBasename(f.project)
		if base == "" {
			base = f.project
		}
		a.print("  " + padStart(dollars(f.own+f.agents), 9) + " " + padStart(pct(f.own+f.agents), 6) + "  " +
			cut(name, 34) + " " + cut(base, 12) + " own " + padStart(dollars(f.own), 8) + " · " +
			padStart(num(f.nAgents), 3) + " agents " + padStart(dollars(f.agents), 8) + " · rewrites " +
			padStart(dollars(f.rewrites), 7) + " · peak " + num(f.peakK) + "K")
	}

	heading("8 · AGENT GROUPS (project · agent type · model) — same job, different cost?")
	a.print(
		"  " + padStart("total", 9) + " " + padStart("n", 4) + " " + padStart("median", 8) + " " + padStart("p90", 8) +
			" " + padStart("max", 8) + " " + padStart("calls", 6) + " " + padStart("peakK", 6) + " " + padStart("wall m", 7) +
			" " + padStart("errs", 5) + " " + padStart("tests", 6) + " " + padStart("reread", 7) + " " + padStart("poll$", 6) +
			" " + padStart("small$", 6) + " " + padStart("ttl", 7) + "  group (calls/peak/wall = median, errs/tests/reread = " +
			"per run, poll/small = share of group $, ttl = where its cache writes went)",
	)
	rowList := firstN(t.groupRows, top+6)
	for k := range rowList {
		row := &rowList[k]
		a.print(
			"  " + padStart(dollars(row.usd), 9) + " " + padStart(num(row.n), 4) + " " + padStart(dollars(row.med), 8) +
				" " + padStart(dollars(row.p90), 8) + " " + padStart(dollars(row.max), 8) + " " + padStart(num(row.calls), 6) +
				" " + padStart(num(round(row.peakK)), 6) + " " + padStart(num(round(row.wall)), 7) + " " +
				padStart(
					fixed(row.errs, 1),
					5,
				) + " " + padStart(fixed(row.tests, 1), 6) + " " + padStart(fixed(row.rereads, 1), 7) +
				" " + padStart(pctText(row.poll, row.usd), 6) + " " + padStart(pctText(row.small, row.usd), 6) + " " +
				padStart(row.ttl, 7) + "  " + row.k,
		)
	}

	heading("9 · TOP " + num(float64(top+3)) + " SINGLE RUNS")
	topRuns := append([]*run(nil), a.runs...)
	stableSort(topRuns, func(left, right *run) float64 { return right.usd - left.usd })
	for _, r := range firstN(topRuns, top+3) {
		a.print(a.runLine(r))
	}
	var agentRuns []*run
	for _, r := range a.runs {
		if r.kind == kindAgent {
			agentRuns = append(agentRuns, r)
		}
	}
	if len(agentRuns) != 0 {
		agentUSD := sumOf(agentRuns, func(r *run) float64 { return r.usd })
		sorted := append([]*run(nil), agentRuns...)
		stableSort(sorted, func(left, right *run) float64 { return right.usd - left.usd })
		top10 := int(math.Max(1, round(float64(len(agentRuns))*0.1)))
		var over []*run
		for _, r := range agentRuns {
			if r.calls > 150 {
				over = append(over, r)
			}
		}
		a.print("  concentration: the costliest 10% of agent runs (" + num(float64(top10)) + " of " +
			num(float64(len(agentRuns))) + ") spent " +
			pctText(sumOf(firstN(sorted, top10), func(r *run) float64 { return r.usd }), agentUSD) +
			" of all agent dollars · runs over 150 calls: " + num(float64(len(over))) + " spending " +
			pctText(sumOf(over, func(r *run) float64 { return r.usd }), agentUSD))
	}
	a.reportTools()
	a.reportLoops()
	heading("12 · BUSIEST HOURS (UTC)")
	type hour struct {
		key string
		usd float64
	}
	var hours []hour
	for _, item := range g.hourly.entries() {
		sum := 0.0
		for _, project := range item.child().entries() {
			sum += project.value.(float64)
		}
		hours = append(hours, hour{item.key, sum})
	}
	stableSort(hours, func(left, right hour) float64 { return right.usd - left.usd })
	for _, h := range firstN(hours, 6) {
		a.print("  " + h.key + "h " + padStart(dollars(h.usd), 8))
	}
	if a.opts.family != "" {
		a.familyDrill(t)
	}
	a.crossCheck(t)
	a.reportCostLedger(t)
	if a.opts.out != "" {
		if err := a.writeOut(t, topRuns); err != nil {
			return 1, err
		}
	}
	if a.opts.view != "" {
		if err := a.writeView(t); err != nil {
			return 1, err
		}
	}
	if a.opts.briefs != "" {
		if err := a.writeBriefs(t); err != nil {
			return 1, err
		}
	}
	// A root, directory or file we could not READ is not an empty result — it is a failure to
	// look, and a report built over one must never exit as though it had covered everything.
	if n := len(scan.readErrors); n != 0 {
		a.printErr(errPrefix + num(float64(n)) + " read error(s); this report is INCOMPLETE — " +
			strings.Join(firstN(scan.readErrors, 5), " | "))
		return 1, nil
	}
	return 0, nil
}

// slimRun is a run as --out writes it: every member in the run's own order,
// its file relative to the first root, categories as a sparse object, the
// per-call series only for the costliest 25, and the page-only members
// (timeline buckets, brief text, landed tokens) left out.
func (a *auditor) slimRun(r *run, i int) *object {
	file := r.file
	if len(a.scan.roots) != 0 {
		if relative, err := filepath.Rel(a.scan.roots[0], r.file); err == nil {
			file = relative
		}
	}
	cats := newObject()
	for k, name := range categories {
		if value := fixedNum(r.cats[k], 4); value > 0 {
			cats.set(name, value)
		}
	}
	var usd, harness, topRepeat any
	if !r.unpriced {
		usd = fixedNum(r.usd, 4)
	}
	if r.harnessUSD != nil {
		harness = *r.harnessUSD
	}
	if r.topRepeat != nil {
		topRepeat = newObject().set("cmd", r.topRepeat.cmd).set("n", r.topRepeat.n)
	}
	var series any = undefined{}
	if i < 25 {
		series = seriesJSON(r.series)
	}
	repeats := make([]any, len(r.repeats))
	for k, repeat := range r.repeats {
		repeats[k] = []any{repeat.cmd, repeat.n}
	}
	out := newObject().set("file", file).set("kind", r.kind).set("sid", r.sid).set("project", r.project).
		set("engine", r.engine).
		set("agentId", r.agentID).set("tok", r.tok.json()).set("contractReads", r.contractReads).
		set("failedCmds", r.failedCmds).set("unpriced", r.unpriced).set("title", r.title).set("agentType", r.agentType).
		set("depth", r.depth).
		set("calls", r.calls).set("usd", usd).set("usdBy", r.usdBy.json()).set("models", r.models).
		set("efforts", r.efforts).set("t0", r.t0).set("t1", r.t1).set("activeMs", r.activeMs).
		set("toolWaitMs", r.toolWaitMs).set("ctxFirst", r.ctxFirst).set("ctxPeak", r.ctxPeak).set("ctxSum", r.ctxSum).
		set("bands", r.bands).
		set("rewrites", newObject().set("n", r.rewrites.n).set("usd", r.rewrites.usd).set("by", r.rewrites.by).
			set("nBy", r.rewrites.nBy)).
		set("resets", r.resets).set("tools", r.tools).set("bash", r.bash).set("errs", r.errs).set("rereadN", r.rereadN).
		set("rereadChars", r.rereadChars).set("cats", cats).set("series", series).set("harnessUsd", harness).
		set("topRepeat", topRepeat).set("B", r.stats.json()).set("distinctRead", r.distinctRead).
		set("smallUsd", r.smallUSD).set("smallN", r.smallN).set("pollUsd", r.pollUSD).set("pollN", r.pollN).
		set("usdLC", r.usdLC).set("f", r.f[:]).set("attach", r.attach).set("repeats", repeats).
		set("firstTs", r.firstTS).set("model", r.model).set("effort", r.effort).set("i", i)
	if r.advisor.calls != 0 {
		out.set("advisor", r.advisor.json())
	}
	return out
}

func seriesJSON(series [][]float64) []any {
	out := make([]any, len(series))
	for i, row := range series {
		out[i] = row
	}
	return out
}

func (stats briefStats) json() *object {
	return newObject().set("tests", stats.tests).set("testFails", stats.testFails).set("testCmd", stats.testCmd).
		set("readsBeforeEdit", stats.readsBeforeEdit).set("firstEditCall", stats.firstEditCall).
		set("edits", stats.edits).set("editFiles", stats.editFiles)
}

func (row landingRow) json() *object {
	var usd any
	if row.usd != nil {
		usd = *row.usd
	}
	return newObject().set("usd", usd).set("tokK", row.tokK).set("after", row.after).set("tool", row.tool).
		set("target", row.target).set("run", row.run)
}

func (p *project) json() *object {
	out := newObject().set("usd", p.usd).set(kindMain, p.main).set(kindAgent, p.agent).set("runs", p.runs).
		set("rewrites", p.rewrites).set("cats", p.cats[:])
	if p.advisor.calls != 0 {
		out.set("advisor", p.advisor.usdJSON())
	}
	return out
}

func (f *family) json() *object {
	return newObject().set("sid", f.sid).set("title", f.title).set("project", f.project).set("own", f.own).
		set("agents", f.agents).set("nAgents", f.nAgents).set("rewrites", f.rewrites).set("calls", f.calls).
		set("peakK", f.peakK).set("t0", f.t0).set("t1", f.t1)
}

func (g group) json() *object {
	return newObject().set("k", g.k).set("n", g.n).set("usd", g.usd).set("med", g.med).set("p90", g.p90).
		set("max", g.max).set("calls", g.calls).set("peakK", g.peakK).set("wall", g.wall).set("errs", g.errs).
		set("poll", g.poll).set("small", g.small).set("tests", g.tests).set("rereads", g.rereads).set("ttl", g.ttl)
}

// writeOut writes --out: the full data behind the text report as JSON.
func (a *auditor) writeOut(t *totals, ranked []*run) error {
	g := a.global
	runs := make([]*object, len(ranked))
	for i, r := range ranked {
		runs[i] = a.slimRun(r, i)
	}
	cats := newObject()
	for i, name := range categories {
		cats.set(name, g.cats[i])
	}
	landings := make([]*object, len(g.landings))
	for i, row := range g.landings {
		landings[i] = row.json()
	}
	byProject := newObject()
	for _, path := range t.byProject.ordered() {
		byProject.set(path, t.projects[path].json())
	}
	families := make([]*object, len(t.famList))
	for i, f := range t.famList {
		families[i] = f.json()
	}
	groups := make([]*object, len(t.groupRows))
	for i := range t.groupRows {
		row := &t.groupRows[i]
		groups[i] = row.json()
	}
	document := newObject().set("v", 1).set("pricing", a.prices.catalog()).set("host", a.host).
		set("at", isoTime(a.now)).set("hours", a.hours).set("scan", a.scan.json()).set("total", t.total).
		set("usd", g.usd.json()).set("tok", g.tok.json().set("think", g.think)).set("calls", g.calls).set("cats", cats).
		set("bands", g.bands).set("modelEffort", g.modelEffort).set("rewrites", g.rewrites).
		set("rewriteTool", g.rewriteTool).set("tools", g.tools).set("bash", g.bash).set("attach", g.attach).
		set("hourly", g.hourly).set("repeats", fromEntries(firstN(sortedEntries(g.repeats, "n"), 60))).
		set("rereads", fromEntries(firstN(sortedEntries(g.rereads, "chars"), 60))).set("landings", landings).
		set("byProject", byProject).set("families", families).set("groups", groups).set("runs", runs)
	if g.advisor.calls != 0 {
		document.set("advisor", g.advisor.json())
	}
	if sessions, ok := a.ledgerJSON(); ok {
		document.set("costLedger", sessions)
	}
	if unattributed := a.unattributedJSON(); unattributed != nil {
		document.set("unattributedTelemetry", unattributed)
	}
	size, err := writeOutput(a.opts.out, stringify(document, 0))
	if err != nil {
		return err
	}
	a.print("\nfull data → " + a.opts.out + " (" + fixed(float64(size)/1e6, 1) + " MB)")
	return nil
}
