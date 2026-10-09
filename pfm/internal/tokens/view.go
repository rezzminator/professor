package tokens

import (
	"math"
	"regexp"
	"strings"
)

// findingCount is the page's findings: fat context, small/poll calls,
// rewrites, reads, injections, and the dearer-model excess.
const findingCount = 6

func round2(value float64) float64 { return fixedNum(value, 2) }

func round2All(values []float64) []float64 {
	out := make([]float64, len(values))
	for i, value := range values {
		out[i] = round2(value)
	}
	return out
}

// bucketize folds a run's per-call series into at most 80 buckets: the peak
// context, then each money column summed and rounded.
func bucketize(series [][]float64) []any {
	const buckets = 80
	n := len(series)
	nb := min(n, buckets)
	out := make([]any, 0, nb)
	for b := range nb {
		from, to := b*n/nb, (b+1)*n/nb
		ctx := 0.0
		var acc [findingCount]float64
		for i := from; i < to; i++ {
			ctx = math.Max(ctx, series[i][0])
			for k := range findingCount {
				acc[k] += series[i][k+1]
			}
		}
		row := []float64{ctx}
		for _, value := range acc {
			row = append(row, round(value))
		}
		out = append(out, row)
	}
	return out
}

// modelStat is one side of a same-job, two-model comparison.
type modelStat struct {
	model                           string
	n, med, sum, calls, peakK, wall float64
}

func (s modelStat) json() *object {
	return newObject().set("model", s.model).set("n", s.n).set("med", s.med).set("sum", s.sum).set("calls", s.calls).
		set("peakK", s.peakK).set("wall", s.wall)
}

func statOf(model string, rs []*run) modelStat {
	usd := mapOf(rs, func(r *run) float64 { return r.usd })
	return modelStat{
		model: model, n: float64(len(rs)), med: round2(quantile(usd, 0.5)),
		sum:   round2(sumOf(rs, func(r *run) float64 { return r.usd })),
		calls: medianOf(rs, func(r *run) float64 { return r.calls }),
		peakK: round(medianOf(rs, func(r *run) float64 { return r.ctxPeak / 1000 })),
		wall:  round(medianOf(rs, wallMinutes)),
	}
}

type comparison struct {
	agentType   string
	cheap, dear modelStat
	flagged     bool
	excess      float64
}

func (c comparison) json() *object {
	return newObject().set("type", c.agentType).set("cheap", c.cheap.json()).set("dear", c.dear.json()).
		set("flagged", c.flagged).set("excess", c.excess)
}

// inputRate is a model's input rate at no context, nil when unpriced.
func (a *auditor) inputRate(model string) *float64 {
	_, rates, found := a.prices.rates(model, 0)
	if !found {
		return nil
	}
	return rates.In
}

// compareModels flags the same job run on two models when the cheaper-per-token
// model has the dearer median run.
func (a *auditor) compareModels(agents []*run) []comparison {
	byType := newObject()
	members := map[string]*object{}
	runsOf := map[[2]string][]*run{}
	for _, r := range agents {
		agentType := firstText(r.agentType, "(untyped)")
		model := modelStem(r.model)
		if members[agentType] == nil {
			members[agentType] = newObject()
			byType.set(agentType, true)
		}
		members[agentType].set(model, true)
		runsOf[[2]string{agentType, model}] = append(runsOf[[2]string{agentType, model}], r)
	}
	var out []comparison
	for _, agentType := range byType.ordered() {
		var models []string
		for _, model := range members[agentType].ordered() {
			if len(runsOf[[2]string{agentType, model}]) >= 5 {
				models = append(models, model)
			}
		}
		for _, cheapModel := range models {
			for _, dearModel := range models {
				cheapRuns, dearRuns := runsOf[[2]string{agentType, cheapModel}], runsOf[[2]string{agentType, dearModel}]
				cheapRate, dearRate := a.inputRate(cheapRuns[0].model), a.inputRate(dearRuns[0].model)
				if cheapRate == nil || dearRate == nil || !(*cheapRate < *dearRate) {
					continue
				}
				cheap, dear := statOf(cheapModel, cheapRuns), statOf(dearModel, dearRuns)
				flagged := cheap.med >= 1.5*dear.med
				excess := 0.0
				if flagged {
					excess = round2(cheap.sum - float64(cheap.n*dear.med))
					for _, r := range cheapRuns {
						r.f[5] = math.Max(0, r.usd-dear.med)
						r.cmp = agentType
					}
					for _, r := range dearRuns {
						r.cmp = agentType
					}
				}
				out = append(
					out,
					comparison{agentType: agentType, cheap: cheap, dear: dear, flagged: flagged, excess: excess},
				)
			}
		}
	}
	stableSort(out, func(left, right comparison) float64 {
		if difference := right.excess - left.excess; difference != 0 {
			return difference
		}
		return right.cheap.sum + right.dear.sum - left.cheap.sum - left.dear.sum
	})
	return out
}

// viewText shortens a path or command for the page: inside the project,
// worktree-free, at most 64 characters.
func viewText(text, projectPath string) string {
	text = worktreePrefix.ReplaceAllString(strings.Replace(text, projectPath+"/", "", 1), "")
	switch {
	case utf16Len(text) <= 64:
		return text
	case jsSpaces.MatchString(text):
		return utf16Slice(text, 0, 63) + "…"
	}
	return "…" + utf16Tail(text, 63)
}

var worktreePrefix = regexp.MustCompile(`^\.worktrees/[^/]+/`)

// writeView writes --view: the compact per-project dataset the page draws
// (findings, runs, timeline).
func (a *auditor) writeView(t *totals) error {
	view := newObject().set("v", 2).set("host", a.opts.hostLabel).set("at", a.now).set("hours", a.hours).
		set("tl0", a.since).set("stepMin", a.hours*60/48).set("total", round2(t.total))
	var projects, hidden []any
	paths := a.projectsByUSD(t)
	for _, path := range paths {
		pv := t.projects[path]
		if pv.usd < t.total*0.03 {
			hidden = append(hidden, []any{path, round2(pv.usd)})
			continue
		}
		projects = append(projects, a.viewProject(t, path, pv))
	}
	view.set("projects", orEmpty(projects)).set("hidden", orEmpty(hidden))
	size, err := writeOutput(a.opts.view, stringify(view, 0))
	if err != nil {
		return err
	}
	var names []string
	for _, p := range projects {
		item := p.(*object)
		name, _ := item.get("name")
		runs, _ := item.get("runs")
		names = append(names, name.(string)+" "+num(float64(len(runs.([]*object))))+" runs")
	}
	a.print(
		"page data → " + a.opts.view + " (" + fixed(float64(size)/1e3, 0) + " KB · " + strings.Join(names, " · ") + ")",
	)
	return nil
}

func orEmpty(items []any) []any {
	if items == nil {
		return []any{}
	}
	return items
}

func (a *auditor) viewProject(t *totals, path string, pv *project) *object {
	var runs, agents []*run
	for _, r := range a.runs {
		if r.project == path {
			runs = append(runs, r)
			if r.kind == kindAgent {
				agents = append(agents, r)
			}
		}
	}
	compare := a.compareModels(agents)
	fin := make([]float64, findingCount)
	for k := range fin {
		fin[k] = sumOf(runs, func(r *run) float64 { return r.f[k] })
	}
	tl := make([]float64, 48)
	tlf := make([][]float64, findingCount)
	for k := range tlf {
		tlf[k] = make([]float64, 48)
	}
	for _, r := range runs {
		for b := range 48 {
			tl[b] += r.tl[b]
			for k := range 5 {
				tlf[k][b] += r.tlf[k][b]
			}
			if r.f[5] != 0 && r.usd != 0 {
				tlf[5][b] += r.tl[b] * r.f[5] / r.usd
			}
		}
	}
	out := a.pickRuns(runs, agents, compare)
	outUSD, maxCalls, maxPeak := 0.0, math.Inf(-1), math.Inf(-1)
	outRows := make([]*object, len(out))
	for i, r := range out {
		row := a.viewRun(t, r, path)
		outRows[i] = row
		if usd, _ := row.get("usd"); usd != nil {
			outUSD += usd.(float64)
		}
		maxCalls = math.Max(maxCalls, r.calls)
		maxPeak = math.Max(maxPeak, round(r.ctxPeak/1000))
	}
	tlfRows := make([]any, len(tlf))
	for k, row := range tlf {
		tlfRows[k] = round2All(row)
	}
	return newObject().set("path", path).set("name", jsBasename(path)).set("usd", round2(pv.usd)).
		set("mainUsd", round2(pv.main)).set("agentUsd", round2(pv.agent)).set("nMain", len(runs)-len(agents)).
		set("nAgent", len(agents)).set("fin", round2All(fin)).set("tl", round2All(tl)).set("tlf", tlfRows).
		set("ev", a.evidence(runs, agents, compare, path)).set("runs", outRows).
		set("rest", newObject().set("n", len(runs)-len(out)).set("usd", round2(pv.usd-outUSD))).
		set("maxCalls", maxCalls).set("maxPk", maxPeak)
}

// pickRuns is which runs travel with the page: the costliest, the worst per
// finding, and both sides of a flagged comparison.
func (a *auditor) pickRuns(runs, agents []*run, compare []comparison) []*run {
	picked := map[*run]bool{}
	var order []*run
	add := func(r *run) {
		if !picked[r] {
			picked[r] = true
			order = append(order, r)
		}
	}
	byUSD := func(rs []*run) []*run {
		sorted := append([]*run(nil), rs...)
		stableSort(sorted, func(left, right *run) float64 { return right.usd - left.usd })
		return sorted
	}
	for _, r := range firstN(byUSD(runs), 30) {
		add(r)
	}
	for k := range findingCount {
		sorted := append([]*run(nil), runs...)
		stableSort(sorted, func(left, right *run) float64 { return right.f[k] - left.f[k] })
		for _, r := range firstN(sorted, 8) {
			if r.f[k] > 0 {
				add(r)
			}
		}
	}
	for k := range compare {
		c := &compare[k]
		if !c.flagged {
			continue
		}
		for _, model := range []string{c.cheap.model, c.dear.model} {
			var side []*run
			for _, r := range agents {
				if r.cmp == c.agentType && modelStem(r.model) == model {
					side = append(side, r)
				}
			}
			for _, r := range firstN(byUSD(side), 10) {
				add(r)
			}
		}
	}
	return byUSD(order)
}

func (a *auditor) viewRun(t *totals, r *run, path string) *object {
	agentType := r.agentType
	if agentType == "" {
		agentType = kindAgent
		if r.kind == kindMain {
			agentType = "chat"
		}
	}
	familyTitle := ""
	if r.kind == kindAgent {
		if f := t.families[r.sid]; f != nil && f.title != "" {
			familyTitle = f.title
		} else {
			familyTitle = utf16Slice(r.sid, 0, 8)
		}
	}
	var usd, repeat any
	if !r.unpriced {
		usd = round2(r.usd)
	}
	if r.topRepeat != nil {
		repeat = []any{viewText(r.topRepeat.cmd, path), r.topRepeat.n}
	}
	type share struct {
		name  string
		value float64
	}
	shares := make([]share, categoryCount)
	for i, name := range categories {
		short, _, _ := strings.Cut(name, " (")
		shares[i] = share{short, round2(r.cats[i])}
	}
	stableSort(shares, func(left, right share) float64 { return right.value - left.value })
	cats := make([]any, 0, 5)
	for _, s := range firstN(shares, 5) {
		cats = append(cats, []any{s.name, s.value})
	}
	slot := func(at float64) float64 { return fixedNum((at-a.since)/(a.hours*3600e3)*48, 2) }
	tests := 0.0
	if testBash, ok := r.bash.get(categories[catBashTest]); ok {
		tests = testBash.(*object).number("n")
	}
	return newObject().set("k", r.kind).set("ty", agentType).set("m", modelStem(r.model)).set("ef", r.effort).
		set("ti", utf16Slice(r.title, 0, 80)).set("fam", familyTitle).set("usd", usd).set("f", round2All(r.f[:])).
		set("poll", round2(r.pollUSD)).set("calls", r.calls).set("pk", round(r.ctxPeak/1000)).
		set("c0", round(r.ctxFirst/1000)).set("mn", round(r.ctxSum/r.calls/1000)).set("wall", round(wallMinutes(r))).
		set("tool", round(r.toolWaitMs/60e3)).set("t0", slot(r.t0)).set("t1", slot(r.t1)).set("at", r.t0).
		set("errs", r.errs).set("tests", tests).set("rer", r.rereadN).set("rw", r.rewrites.n).set("resets", r.resets).
		set("cmp", r.cmp).set("rep", repeat).set("cats", cats).set("prof", bucketize(r.series))
}

// evidence is a project's page evidence: where its dollars went by call
// depth, its loops, its carried files, reads, injections and rewrite causes.
func (a *auditor) evidence(runs, agents []*run, compare []comparison, path string) *object {
	type band struct {
		name                   string
		limit, usd, calls, ctx float64
	}
	depth := []*band{
		{name: "calls 1–50", limit: 50},
		{name: "calls 51–150", limit: 150},
		{name: "calls 151–300", limit: 300},
		{name: "calls 301+", limit: math.Inf(1)},
	}
	for _, r := range agents {
		for i, call := range r.series {
			for _, d := range depth {
				if float64(i) < d.limit {
					d.usd += call[1] / 1000
					d.calls++
					d.ctx += call[0]
					break
				}
			}
		}
	}
	depthRows := make([]*object, len(depth))
	for i, d := range depth {
		meanK := 0.0
		if d.calls != 0 {
			meanK = round(d.ctx / d.calls)
		}
		depthRows[i] = newObject().set("name", d.name).
			set("usd", round2(d.usd)).
			set("calls", d.calls).
			set("meanK", meanK)
	}
	over200 := 0
	repeats, attach, causes := newObject(), newObject(), newObject()
	for _, r := range runs {
		for _, call := range r.series {
			if call[0] > 200 {
				over200++
			}
		}
		for _, repeat := range r.repeats {
			g := repeats.childWith(repeat.cmd, "n", "runs")
			g.bump("n", repeat.n)
			g.bump("runs", 1)
		}
		for _, item := range r.attach.entries() {
			g := attach.childWith(item.key, "n", "chars")
			g.bump("n", item.child().number("n"))
			g.bump("chars", item.child().number("chars"))
		}
		for _, item := range r.rewrites.by.entries() {
			g := causes.childWith(r.kind+" · "+item.key, "usd", "n")
			g.bump("usd", item.value.(float64))
			g.bump("n", r.rewrites.nBy.number(item.key))
		}
	}
	var repeatRows, fileRows, attachRows, causeRows []any
	for _, item := range firstN(sortedEntries(repeats, "n"), 4) {
		repeatRows = append(
			repeatRows,
			[]any{viewText(item.key, path), item.child().number("n"), item.child().number("runs")},
		)
	}
	for _, l := range a.global.landings {
		owner := int(l.run)
		if owner >= len(a.runs) || a.runs[owner].project != path || (l.tool != toolRead && l.tool != toolBash) {
			continue
		}
		var usd any
		if l.usd != nil {
			usd = round2(*l.usd)
		}
		fileRows = append(fileRows, []any{viewText(l.target, path), usd, l.tokK, l.after, l.tool})
		if len(fileRows) == 5 {
			break
		}
	}
	injected := 0.0
	for _, item := range attach.entries() {
		injected += item.child().number("chars")
	}
	for _, item := range firstN(sortedEntries(attach, "chars"), 5) {
		g := item.child()
		attachRows = append(attachRows, []any{item.key, g.number("n"), round(g.number("chars") / 4 / g.number("n"))})
	}
	perAgentInjK := 0.0
	if len(agents) != 0 {
		perAgentInjK = round(injected/4/float64(len(runs))/100) / 10
	}
	for _, item := range firstN(sortedEntries(causes, "usd"), 4) {
		causeRows = append(causeRows, []any{item.key, round2(item.child().number("usd")), item.child().number("n")})
	}
	readUSD := make([]float64, 0, 3)
	for _, k := range []int{catReadFirst, catReadAgain, catBashRead} {
		readUSD = append(readUSD, round2(sumOf(runs, func(r *run) float64 { return r.cats[k] })))
	}
	compareRows := make([]*object, 0, 4)
	cList := firstN(compare, 4)
	for k := range cList {
		c := &cList[k]
		compareRows = append(compareRows, c.json())
	}
	runsOver200 := 0
	for _, r := range runs {
		if r.ctxPeak > 200000 {
			runsOver200++
		}
	}
	return newObject().set("depth", depthRows).set("over200Calls", over200).
		set("calls", sumOf(runs, func(r *run) float64 { return r.calls })).set("runsOver200", runsOver200).
		set("smallN", sumOf(runs, func(r *run) float64 { return r.smallN })).
		set("pollUsd", round2(sumOf(runs, func(r *run) float64 { return r.pollUSD }))).
		set("pollN", sumOf(runs, func(r *run) float64 { return r.pollN })).set("repeats", orEmpty(repeatRows)).
		set("files", orEmpty(fileRows)).
		set("rereadUsd", round2(sumOf(runs, func(r *run) float64 { return r.cats[catReadAgain] }))).
		set("rereadN", sumOf(runs, func(r *run) float64 { return r.rereadN })).set("readUsd", readUSD).
		set("attach", orEmpty(attachRows)).set("perAgentInjK", perAgentInjK).set("causes", orEmpty(causeRows)).
		set("compare", compareRows)
}
