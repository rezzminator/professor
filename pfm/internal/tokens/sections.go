package tokens

import (
	"fmt"
	"strings"
)

// printErr writes one stderr line.
func (a *auditor) printErr(line string) { fmt.Fprintln(a.stderr, line) }

// projectsByUSD is the project paths, dearest first.
func (a *auditor) projectsByUSD(t *totals) []string {
	paths := t.byProject.ordered()
	stableSort(paths, func(left, right string) float64 { return t.projects[right].usd - t.projects[left].usd })
	return paths
}

// runUSD never renders ignorance as zero dollars.
func runUSD(r *run) string {
	if r.unpriced {
		return notPriced
	}
	return dollars(r.usd)
}

func (a *auditor) runLine(r *run) string {
	name := r.agentType
	if name == "" {
		name = kindAgent
	}
	name += ": " + r.title
	if r.kind == kindMain {
		title := r.title
		if title == "" {
			title = utf16Slice(r.sid, 0, 8)
		}
		name = "MAIN " + title
	}
	repeat := ""
	if r.topRepeat != nil {
		repeat = " · " + num(r.topRepeat.n) + "× \"" + utf16Slice(r.topRepeat.cmd, 0, 40) + "\""
	}
	return "  " + padStart(runUSD(r), 9) + "  " + cut(name, 44) + " " + cut(modelStem(r.model), 12) + " " +
		padStart(num(r.calls), 5) + " calls · ctx " + padStart(num(round(r.ctxFirst/1000)), 3) + "→" +
		padStart(num(round(r.ctxPeak/1000)), 3) + "K · " + padStart(num(round(wallMinutes(r))), 4) + "m wall/" +
		padStart(num(round(r.toolWaitMs/60e3)), 4) + "m in tools · rw " + padStart(dollars(r.rewrites.usd), 6) +
		" · errs " + padStart(num(r.errs), 3) + " · reread " + padStart(num(r.rereadN), 3) + " · poll " +
		padStart(dollars(r.pollUSD), 6) + " · ttl " + ttlMix(r.tok.cw5, r.tok.cw1) + repeat
}

func (a *auditor) reportTools() {
	g := a.global
	a.print("\n== 10 · TOOLS (results entering context; ~4 chars per token)")
	for _, item := range firstN(sortedEntries(g.tools, "chars"), 10) {
		v := item.child()
		a.print("  " + padStart(millions(v.number("chars")/4), 7) + " tok " + padStart(num(v.number("n")), 6) + "× · " +
			padStart(num(round(v.number("chars")/4/v.number("n"))), 6) + " tok each · " +
			padStart(pctText(v.number("err"), v.number("n")), 6) + " failed · " + item.key)
	}
	for _, item := range sortedEntries(g.bash, "chars") {
		v := item.child()
		a.print(
			"    " + padStart(millions(v.number("chars")/4), 7) + " tok " + padStart(num(v.number("n")), 6) + "× · " +
				padStart(pctText(v.number("err"), v.number("n")), 6) + " failed · " +
				padStart(num(round(v.number("waitMs")/3600e3*10)/10)+"h", 7) + " waiting · " + item.key,
		)
	}
	a.print("  harness injections by type:")
	for _, item := range firstN(sortedEntries(g.attach, "chars"), 7) {
		v := item.child()
		a.print(
			"    " + padStart(millions(v.number("chars")/4), 7) + " tok " + padStart(num(v.number("n")), 6) + "× · " +
				padStart(num(round(v.number("chars")/4/v.number("n"))), 6) + " tok each · " + item.key,
		)
	}
}

func (a *auditor) reportLoops() {
	g := a.global
	a.print("\n== 11 · LOOPS")
	a.print("  the same shell command, repeated inside one run:")
	for _, item := range firstN(sortedEntries(g.repeats, "n"), 8) {
		v := item.child()
		a.print("  " + padStart(num(v.number("n")), 5) + "× in " + padStart(num(v.number("runs")), 3) + " runs (max " +
			padStart(
				num(v.number("maxInRun")),
				3,
			) + " in one) · " + cut(leadingPath.ReplaceAllString(item.key, ""), 110))
	}
	a.print("  the same file, read again while already in context:")
	for _, item := range firstN(sortedEntries(g.rereads, "chars"), 8) {
		v := item.child()
		a.print("  " + padStart(num(v.number("n")), 5) + " re-reads · " +
			padStart(num(round(v.number("chars")/4000))+"K", 6) + " tok · " +
			cut(leadingPath.ReplaceAllString(item.key, ""), 100))
	}
	a.print("  single tool results that were then carried the longest (estimate: tokens × later calls × read price):")
	for _, l := range firstN(g.landings, 8) {
		usd := notPriced
		if l.usd != nil {
			usd = dollars(*l.usd)
		}
		a.print("  " + padStart(usd, 8) + " · " + padStart(num(l.tokK)+"K", 5) + " tok carried " +
			padStart(num(l.after), 4) + " calls · " + l.tool + " · " + cut(l.target, 80))
	}
}

// familyDrill matches a chat title OR a session id OR any sub-agent's spawn
// description or type, so a family an agent orchestrated — which has no title
// at all — is still reachable by name.
func (a *auditor) familyDrill(t *totals) {
	needle := strings.ToLower(a.opts.family)
	var f *family
	for _, candidate := range t.famList {
		match := strings.Contains(strings.ToLower(candidate.title), needle) ||
			strings.HasPrefix(strings.ToLower(candidate.sid), needle)
		for _, r := range candidate.agentRuns {
			match = match || strings.Contains(strings.ToLower(r.title), needle) ||
				strings.Contains(strings.ToLower(r.agentType), needle)
		}
		if match {
			f = candidate
			break
		}
	}
	a.print("\n== 13 · FAMILY DRILL-DOWN \"" + a.opts.family + "\"")
	if f == nil {
		var titles []string
		for _, candidate := range firstN(t.famList, 15) {
			title := candidate.title
			if title == "" {
				title = utf16Slice(candidate.sid, 0, 8)
			}
			titles = append(titles, title)
		}
		a.print(
			"  NOT FOUND among " + num(
				float64(len(t.famList)),
			) + " families — titles seen: " + strings.Join(
				titles,
				" | ",
			),
		)
		return
	}
	a.print("  " + f.title + " · " + f.project + " · own " + dollars(f.own) + " · " + num(f.nAgents) + " agents " +
		dollars(f.agents) + " · rewrites " + dollars(f.rewrites))
	for _, main := range a.runs {
		if main.kind != kindMain || main.sid != f.sid {
			continue
		}
		by := newObject()
		for _, item := range main.rewrites.by.entries() {
			by.set(item.key, fixedNum(item.value.(float64), 2))
		}
		a.print("  main loop: " + num(main.calls) + " calls · ctx " + num(round(main.ctxFirst/1000)) + "→" +
			num(round(main.ctxPeak/1000)) + "K · mean " + num(round(main.ctxSum/main.calls/1000)) + "K · resets " +
			num(main.resets) + " · rewrites " + num(main.rewrites.n) + "× " + dollars(main.rewrites.usd) + " " +
			stringify(by, 0))
		a.print("  main loop paid for: " + categoryMix(main.cats, main.usd, 6))
		break
	}
	byModel := newObject()
	members := map[string][]*run{}
	for _, r := range f.agentRuns {
		name := r.agentType
		if name == "" {
			name = kindAgent
		}
		key := name + " · " + modelStem(r.model)
		byModel.set(key, true)
		members[key] = append(members[key], r)
	}
	keys := byModel.ordered()
	usdOf := func(key string) float64 { return sumOf(members[key], func(r *run) float64 { return r.usd }) }
	stableSort(keys, func(left, right string) float64 { return usdOf(right) - usdOf(left) })
	for _, key := range keys {
		rs := members[key]
		var cats [categoryCount]float64
		for _, r := range rs {
			for i, value := range r.cats {
				cats[i] += value
			}
		}
		total := usdOf(key)
		a.print("  " + key + ": " + num(float64(len(rs))) + " runs · " + dollars(total) + " · median " +
			dollars(medianOf(rs, func(r *run) float64 { return r.usd })) + " · median calls " +
			num(medianOf(rs, func(r *run) float64 { return r.calls })) + " · median peak " +
			num(
				round(medianOf(rs, func(r *run) float64 { return r.ctxPeak / 1000 })),
			) + "K · median wall " +
			num(round(medianOf(rs, wallMinutes))) + "m")
		a.print("     paid for: " + categoryMix(cats, total, 6))
	}
	a.print("  costliest agents:")
	sorted := append([]*run(nil), f.agentRuns...)
	stableSort(sorted, func(left, right *run) float64 { return right.usd - left.usd })
	for _, r := range firstN(sorted, 14) {
		a.print("  " + padStart(dollars(r.usd), 8) + " " + cut(modelStem(r.model), 11) + " " +
			utf16Slice(isoTime(r.t0), 5, 16) + " " + padStart(num(r.calls), 5) + " calls · ctx " +
			padStart(num(round(r.ctxFirst/1000)), 3) + "→" + padStart(num(round(r.ctxPeak/1000)), 3) + "K · " +
			padStart(num(round(wallMinutes(r))), 4) + "m wall/" + padStart(num(round(r.toolWaitMs/60e3)), 4) +
			"m tools · tests " + padStart(num(r.testRuns()), 3) + " · errs " + padStart(num(r.errs), 3) + " · reread " +
			padStart(num(r.rereadN), 3) + " · rw " + padStart(dollars(r.rewrites.usd), 6) + " · " + cut(r.title, 40))
	}
}

// crossCheck sets the estimate against the harness's own running total, only
// for chats that lie wholly inside the window.
func (a *auditor) crossCheck(t *totals) {
	type checked struct {
		f    *family
		main *run
	}
	var qualifying []checked
	for _, f := range t.famList {
		var main *run
		for _, r := range a.runs {
			if r.kind == kindMain && r.sid == f.sid {
				main = r
				break
			}
		}
		if main == nil || main.harnessUSD == nil || !(main.firstTS >= a.since) {
			continue
		}
		inside := true
		for _, agent := range f.agentRuns {
			inside = inside && agent.firstTS >= a.since
		}
		if inside {
			qualifying = append(qualifying, checked{f, main})
		}
	}
	a.print("\n== CROSS-CHECK (this estimate vs the harness's own cost-state line, chats wholly inside the window)")
	if len(qualifying) == 0 {
		a.print("  no chat qualifies — the estimate is UNCHECKED on this host")
		return
	}
	var mine, mineOwn, theirs, atTiers float64
	for _, item := range qualifying {
		mine += item.f.own + item.f.agents
		mineOwn += item.f.own
		theirs += *item.main.harnessUSD
		agents := 0.0
		for _, agent := range item.f.agentRuns {
			agents += agent.usdLC
		}
		atTiers += item.main.usdLC + agents
	}
	a.print("  at the published context tiers (per-model rate in the pfm model-cost table — an estimate): " +
		dollars(atTiers) + " (" + fixed(atTiers/theirs, 2) + "x)")
	a.print("  " + num(float64(len(qualifying))) + " chats · harness says " + dollars(theirs) + " · this audit says " +
		dollars(mine) + " with agents (" + fixed(mine/theirs, 2) + "x) / " + dollars(mineOwn) + " main loops only (" +
		fixed(mineOwn/theirs, 2) + "x)")
}
