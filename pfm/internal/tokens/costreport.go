package tokens

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/costledger"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// The cost ledger is the one record of every API call a Claude session made,
// side calls (web search and fetch helpers, summaries, suggestions) included.
// Its total is the number the main status line shows, so the report reads it
// beside the transcript estimate: it never replaces the estimate's
// per-run dollars, and a chat with no ledger keeps today's output and one
// line saying the side calls were not measured.
const (
	noLedgerLine = "side calls not measured (no cost ledger)"
	// differenceFloor is the ledger-minus-Claude-Code gap, in micro-dollars, from half a cent on:
	// below it the two totals agree to the cent they both print.
	differenceFloor = 5000
	mainThreadLabel = "main thread"
	// beforeLedgerLabel names the share of the session the ledger began after: spend that no
	// agent's row holds, only Claude Code's own totals (the cost-state lines of the transcript).
	beforeLedgerLabel = "before ledger (not attributed)"
)

var requestIDKey = []byte(`"requestId"`)

// ledgerChat is one chat the report looks for a ledger of.
type ledgerChat struct{ sid, title string }

// agentLine is one row of the per-agent split. micros of an agent row cover
// the agent and every agent below it; the main thread's cover its own calls.
type agentLine struct {
	id, label string
	depth     int
	micros    int64
	rows      int
	unknown   int
}

// sourceLine is one row of the per-query_source split.
type sourceLine struct {
	source  string
	micros  int64
	rows    int
	unknown int
}

// sessionLedger is one chat's ledger set against its transcripts.
type sessionLedger struct {
	chat         ledgerChat
	faults       int
	total        costledger.Totals
	side         costledger.Totals
	cc           *float64
	ccClosed     bool  // no line follows the last cost-state: cc is the chat's final total, not a floor
	before       int64 // micro-dollars spent before the ledger began (costledger.BeforeLedger)
	agents       []agentLine
	sources      []sourceLine
	unreliable   []string // why the side-call split may be wrong, never silently dropped
	observations bool     // missing request ids prevent unique-call accounting
}

// ledgerReport is what looking for the ledger of every chat found.
type ledgerReport struct {
	chats        int
	unmeasured   int // chats with no ledger
	measured     []*sessionLedger
	failed       []string // a ledger that could not be read, named
	unattributed costledger.Ledger
}

// measureLedgers reads the ledger of every chat, once per run.
func (a *auditor) measureLedgers(chats []ledgerChat) *ledgerReport {
	report := &ledgerReport{chats: len(chats)}
	chats = a.includeTelemetryChats(chats, report)
	for _, chat := range chats {
		if a.ledgerDir == "" || !costledger.ValidSessionID(chat.sid) {
			report.unmeasured++
			continue
		}
		ledger, err := costledger.ForSession(a.ledgerDir, chat.sid)
		if err != nil {
			a.scan.readErrors = append(a.scan.readErrors, "cost ledger of "+chat.sid+": "+err.Error())
			report.failed = append(report.failed, "cost ledger unreadable for chat "+chat.sid+": "+err.Error())
			continue
		}
		if !ledger.Present {
			report.unmeasured++
			continue
		}
		report.measured = append(report.measured, a.measureSession(chat, ledger))
	}
	a.ledgers = report
	return report
}

// includeTelemetryChats finds sessions with calls but no selected transcript. Filters that
// need transcript metadata cannot attribute these sessions, so they remain outside that selection.
func (a *auditor) includeTelemetryChats(chats []ledgerChat, report *ledgerReport) []ledgerChat {
	if a.ledgerDir == "" {
		return chats
	}
	fail := func(label string, err error) {
		message := label + ": " + err.Error()
		a.scan.readErrors = append(a.scan.readErrors, message)
		report.failed = append(report.failed, message)
	}
	unattributed, err := costledger.Unattributed(a.ledgerDir)
	if err != nil {
		fail("unattributed telemetry unreadable", err)
	} else {
		unattributed.Rows = a.ledgerWindow(unattributed.Rows)
		report.unattributed = unattributed
	}
	if a.opts.project != "" || a.opts.family != "" || (a.opts.familyRxSource != "" && a.opts.familyRxSource != ".") {
		return chats
	}
	ids, err := costledger.SessionIDs(a.ledgerDir)
	if err != nil {
		fail("cost ledger directory unreadable", err)
		return chats
	}
	seen := map[string]bool{}
	for _, chat := range chats {
		seen[chat.sid] = true
	}
	for _, sid := range ids {
		if seen[sid] ||
			(a.opts.session != "" && !strings.HasPrefix(sid, a.opts.session)) {
			continue
		}
		ledger, err := costledger.ForSession(a.ledgerDir, sid)
		if err != nil {
			fail("cost ledger of "+sid, err)
			continue
		}
		if len(a.ledgerWindow(ledger.Rows)) == 0 {
			if ledger.Faults > 0 {
				fail(
					"cost ledger of "+sid,
					fmt.Errorf("%d malformed records; no calls dated in the selected window", ledger.Faults),
				)
			}
			continue
		}
		chats = append(chats, ledgerChat{sid: sid, title: "telemetry only (no selected transcript)"})
		report.chats++
	}
	return chats
}

func (a *auditor) ledgerWindow(rows []costledger.Row) []costledger.Row {
	var selected []costledger.Row
	for index := range rows {
		row := &rows[index]
		at, ok := dateParse(row.TS, a.loc)
		if !ok {
			message := fmt.Sprintf(
				"telemetry timestamp %q for session %q is invalid; observation excluded from the selected window",
				row.TS,
				row.SessionID,
			)
			if !slices.Contains(a.scan.readErrors, message) {
				a.scan.readErrors = append(a.scan.readErrors, message)
			}
			continue
		}
		if at >= a.since && at <= a.now {
			selected = append(selected, *row)
		}
	}
	return selected
}

// measureSession joins one chat's ledger to its transcripts.
func (a *auditor) measureSession(chat ledgerChat, ledger costledger.Ledger) *sessionLedger {
	held, problems := a.heldRequests(chat.sid)
	side := ledger.Side(func(requestID string) bool { return held[requestID] })
	session := &sessionLedger{
		chat: chat, faults: ledger.Faults, total: costledger.Sum(ledger.Rows), side: costledger.Sum(side),
		unreliable: problems,
	}
	missingRequest := 0
	for index := range ledger.Rows {
		if ledger.Rows[index].RequestID == "" {
			missingRequest++
		}
	}
	if missingRequest != 0 {
		session.observations = true
		session.unreliable = append(session.unreliable, strconv.Itoa(missingRequest)+
			" observations without request_id cannot be reconciled against transcripts; "+
			"uncorrelated log/span observations and retries may overlap")
	}
	states, found := a.mainCostStates(chat.sid)
	if found {
		if n := len(states.States()); n != 0 {
			cc := states.States()[n-1].USD
			session.cc = &cc
		}
		session.ccClosed = states.Closed()
		session.before = costledger.BeforeLedger(ledger.Rows, states.States())
	}
	types, parents, problems := a.agentMeta(chat.sid)
	session.unreliable = append(session.unreliable, problems...)
	session.agents = agentLines(ledger.Rows, types, parents)
	session.sources = sourceLines(ledger.Rows)
	return session
}

// mainCostStates is the cost-state lines of a chat's main transcript: from its run when the window
// holds one, else read from the file itself (a chat whose main-thread calls all fall before --since
// or outside --project still has its total, and the session total stays the main line's figure).
// found is false when no main transcript of the chat was walked.
func (a *auditor) mainCostStates(sid string) (anchors costledger.Anchors, found bool) {
	for _, r := range a.runs {
		if r.kind == kindMain && r.sid == sid {
			return r.anchors, true
		}
	}
	for _, file := range a.transcripts {
		if transcript.IsSubagentTranscript(file) || transcript.TranscriptSession(file) != sid {
			continue
		}
		err := eachLine(file, func(raw []byte) {
			line, decoded := transcript.DecodeClaudeRecord(raw)
			if !decoded {
				return
			}
			if truthy(line.Timestamp) {
				anchors.Stamp(keyString(line.Timestamp))
			}
			if kind, _ := line.Kind(); kind == "cost-state" {
				costState(&anchors, &line)
			}
		})
		if err != nil {
			a.scan.readErrors = append(a.scan.readErrors, file+": "+describe(err))
		}
		return anchors, true
	}
	return anchors, false
}

// heldRequests is every requestId a transcript of the session records, the
// main chat's and every sub-agent's, however old: a row is held by any
// transcript, not only those the window replays. A line that could not be
// decoded may hide a held request, so it is counted and named.
func (a *auditor) heldRequests(sid string) (map[string]bool, []string) {
	held := map[string]bool{}
	var problems []string
	for _, file := range a.transcripts {
		if transcript.TranscriptSession(file) != sid {
			continue
		}
		torn := 0
		err := eachLine(file, func(line []byte) {
			if !bytes.Contains(line, requestIDKey) {
				return
			}
			var record struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(line, &record) != nil {
				torn++
				return
			}
			var id string
			if json.Unmarshal(record.RequestID, &id) == nil && id != "" {
				held[id] = true
			}
		})
		if err != nil {
			a.scan.readErrors = append(a.scan.readErrors, file+": "+describe(err))
			problems = append(problems, "transcript "+file+" unreadable: "+describe(err))
		}
		if torn != 0 {
			problems = append(problems, num(float64(torn))+" undecodable "+countWord(torn, "line", "lines")+" in "+file)
		}
	}
	return held, problems
}

// agentMeta is every sub-agent's type and parent from the meta files beside
// its transcript; a missing meta file is just no type, an unreadable one a
// named problem.
func (a *auditor) agentMeta(sid string) (types, parents map[string]string, problems []string) {
	types, parents = map[string]string{}, map[string]string{}
	for _, file := range a.transcripts {
		if transcript.TranscriptSession(file) != sid || !transcript.IsSubagentTranscript(file) {
			continue
		}
		id, ok := transcript.SubagentID(filepath.Base(file))
		if !ok {
			continue
		}
		meta, err := transcript.ReadSubagentMeta(transcript.SubagentMetaPath(file))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			a.scan.readErrors = append(a.scan.readErrors, err.Error())
			problems = append(problems, err.Error())
			continue
		}
		types[id], parents[id] = meta.AgentType, meta.ParentAgentID
	}
	return types, parents, problems
}

// agentLines is the per-agent split: the main thread's own calls, each
// top-level agent as a tree (itself and everything it spawned, children
// indented below), the calls of an unnamed agent ("?") last. The top-level rows
// and the main thread sum to the ledger total, nested rows are a view inside
// their parent's. A parent is the ledger's own, else the meta file's; a parent
// that is no agent with calls, or a loop of parents, makes the agent top-level.
func agentLines(rows []costledger.Row, types, metaParents map[string]string) []agentLine {
	type node struct {
		id, parent string
		own        costledger.Totals
		children   []*node
		tree       costledger.Totals
	}
	var mainRows, unknownRows []costledger.Row
	nodes := map[string]*node{}
	var order []*node
	for index := range rows {
		row := &rows[index]
		switch row.AgentID {
		case "":
			mainRows = append(mainRows, *row)
			continue
		case costledger.UnknownAgent:
			unknownRows = append(unknownRows, *row)
			continue
		}
		n := nodes[row.AgentID]
		if n == nil {
			n = &node{id: row.AgentID, parent: firstText(row.ParentAgentID, metaParents[row.AgentID])}
			nodes[row.AgentID] = n
			order = append(order, n)
		} else if n.parent == "" {
			n.parent = firstText(row.ParentAgentID, metaParents[row.AgentID])
		}
		n.own = addTotals(n.own, costledger.Sum(rows[index:index+1]))
	}
	for _, n := range order {
		if nodes[n.parent] == nil || n.parent == n.id {
			n.parent = ""
		}
	}
	// a parent chain that never reaches the top is a loop: its first agent in call order becomes top-level
	for _, n := range order {
		at := n
		for steps := 0; at != nil && at.parent != ""; steps++ {
			if steps > len(order) {
				n.parent = ""
				break
			}
			at = nodes[at.parent]
		}
	}
	var top []*node
	for _, n := range order {
		if n.parent == "" {
			top = append(top, n)
		} else {
			nodes[n.parent].children = append(nodes[n.parent].children, n)
		}
	}
	var fold func(n *node) costledger.Totals
	fold = func(n *node) costledger.Totals {
		n.tree = n.own
		for _, child := range n.children {
			n.tree = addTotals(n.tree, fold(child))
		}
		return n.tree
	}
	for _, n := range top {
		fold(n)
	}
	byTree := func(list []*node) {
		stableSort(list, func(left, right *node) float64 { return float64(right.tree.Micros - left.tree.Micros) })
	}
	byTree(top)
	mainTotals := costledger.Sum(mainRows)
	lines := []agentLine{{
		label: mainThreadLabel, micros: mainTotals.Micros, rows: mainTotals.Rows, unknown: mainTotals.UnknownCost,
	}}
	var walk func(n *node, depth int)
	walk = func(n *node, depth int) {
		label := "agent " + n.id
		if types[n.id] != "" {
			label += " " + types[n.id]
		}
		lines = append(lines, agentLine{
			id: n.id, label: label, depth: depth, micros: n.tree.Micros, rows: n.tree.Rows, unknown: n.tree.UnknownCost,
		})
		byTree(n.children)
		for _, child := range n.children {
			walk(child, depth+1)
		}
	}
	for _, n := range top {
		walk(n, 0)
	}
	if len(unknownRows) != 0 {
		unnamed := costledger.Sum(unknownRows)
		lines = append(lines, agentLine{
			id:      costledger.UnknownAgent,
			label:   "agent not named in the log (?)",
			micros:  unnamed.Micros,
			rows:    unnamed.Rows,
			unknown: unnamed.UnknownCost,
		})
	}
	return lines
}

func addTotals(left, right costledger.Totals) costledger.Totals {
	return costledger.Totals{
		Micros:      left.Micros + right.Micros,
		UnknownCost: left.UnknownCost + right.UnknownCost,
		Rows:        left.Rows + right.Rows,
	}
}

// sourceLines is the per-query_source split, dearest first.
func sourceLines(rows []costledger.Row) []sourceLine {
	var lines []sourceLine
	at := map[string]int{}
	for index := range rows {
		source := firstText(rows[index].QuerySource, noModel)
		i, seen := at[source]
		if !seen {
			i = len(lines)
			at[source] = i
			lines = append(lines, sourceLine{source: source})
		}
		sum := costledger.Sum(rows[index : index+1])
		lines[i].micros += sum.Micros
		lines[i].rows += sum.Rows
		lines[i].unknown += sum.UnknownCost
	}
	stableSort(lines, func(left, right sourceLine) float64 { return float64(right.micros - left.micros) })
	return lines
}

func countWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// microsDollars is a ledger amount as the report's $ column writes it; a cost
// under a cent is named, never rendered $0.00.
func microsDollars(micros int64) string {
	switch {
	case micros < 0:
		return "-" + microsDollars(-micros)
	case micros > 0 && micros < 5000:
		return "<$0.01"
	}
	return dollars(float64(micros) / 1e6)
}

// ccMicros is Claude Code's own total in micro-dollars.
func ccMicros(cc float64) int64 { return int64(math.Round(cc * 1e6)) }

// sessionMicros is the session's total, the number the main status line shows: the ledger's rows
// plus what came before the ledger.
func (s *sessionLedger) sessionMicros() int64 { return s.total.Micros + s.before }

// difference is the session total minus Claude Code's, when the transcript's last cost-state is the
// chat's final total (nothing ran after it) and the two differ by half a cent or more. What the
// ledger lacks of a cost-state is not a difference, it is the before-ledger line; what this
// names is a ledger above Claude Code's total, or rows logged after its last exit.
func (s *sessionLedger) difference() (int64, bool) {
	if s.cc == nil || !s.ccClosed {
		return 0, false
	}
	diff := s.sessionMicros() - ccMicros(*s.cc)
	return diff, diff >= differenceFloor || diff <= -differenceFloor
}

// lines is the chat's block: the total, the side calls, the Claude Code total
// and any difference, then the split by agent and by query_source with each
// row's share of the session total. top bounds the top-level agent rows.
func (s *sessionLedger) lines(top int) []string {
	unit := func(n int) string {
		if s.observations {
			return countWord(n, "observation", "observations")
		}
		return countWord(n, "call", "calls")
	}
	title := firstText(s.chat.title, "(untitled)")
	out := []string{
		"  chat " + s.chat.sid + " · " + strings.TrimRight(cut(title, 50), " "),
		"    session total " + microsDollars(s.sessionMicros()) + " (" + num(float64(s.total.Rows)) + " " +
			unit(s.total.Rows) + ") — the main status line's number",
	}
	if s.before >= differenceFloor {
		out = append(out, "    before ledger "+microsDollars(s.before)+" (not attributed)")
	}
	out = append(
		out,
		"    side calls "+microsDollars(s.side.Micros)+" ("+num(float64(s.side.Rows))+" "+
			unit(s.side.Rows)+" no transcript holds)",
	)
	if n := s.total.UnknownCost; n != 0 {
		out = append(
			out,
			"    "+num(float64(n))+" "+unit(n)+" with no cost in the ledger, not in the total",
		)
	}
	if s.faults != 0 {
		out = append(out, "    "+num(float64(s.faults))+" torn ledger "+countWord(s.faults, "line", "lines")+" skipped")
	}
	for _, problem := range s.unreliable {
		out = append(out, "    side calls may be overstated: "+problem)
	}
	switch {
	case s.cc == nil:
		out = append(out, "    Claude Code's own total: no cost-state line in this chat's transcript")
	case !s.ccClosed:
		out = append(
			out,
			"    Claude Code's last cost-state says "+dollars(*s.cc)+
				" (written at an earlier exit; the chat has run since)",
		)
	default:
		out = append(out, "    Claude Code says "+dollars(*s.cc)+" (the transcript's last cost-state line)")
		if diff, differs := s.difference(); differs {
			sign := "+"
			if diff < 0 {
				sign = "-"
			}
			magnitude := diff
			if magnitude < 0 {
				magnitude = -magnitude
			}
			out = append(out, "    session total minus Claude Code: "+sign+microsDollars(magnitude))
		}
	}
	row := func(indent int, label string, micros int64, unknown int) string {
		line := "      " + strings.Repeat("  ", indent) + cut(label, 56-2*indent) +
			padStart(microsDollars(micros), 8) + padStart(pctText(float64(micros), float64(s.sessionMicros())), 7)
		if unknown != 0 {
			line += " · " + num(float64(unknown)) + " with no cost"
		}
		return line
	}
	out = append(out, "    by agent (cost, share of the session total)")
	hidden, hiddenMicros, shown := 0, int64(0), 0
	folding := false
	for _, agent := range s.agents {
		if agent.depth == 0 && agent.id != "" && agent.id != costledger.UnknownAgent {
			folding = shown >= top
			if folding {
				hidden++
				hiddenMicros += agent.micros
				continue
			}
			shown++
		} else if folding && agent.depth > 0 {
			continue // an agent folded into "more" takes its children with it
		}
		out = append(out, row(agent.depth, agent.label, agent.micros, agent.unknown))
	}
	if hidden != 0 {
		out = append(
			out,
			row(0, "… "+num(float64(hidden))+" more "+countWord(hidden, "agent", "agents"), hiddenMicros, 0),
		)
	}
	if s.before >= differenceFloor {
		out = append(out, row(0, beforeLedgerLabel, s.before, 0))
	}
	out = append(out, "    by query_source")
	for _, source := range s.sources {
		out = append(out, row(0, source.source, source.micros, source.unknown))
	}
	if s.before >= differenceFloor {
		out = append(out, row(0, beforeLedgerLabel, s.before, 0))
	}
	return out
}

// json is the chat's costLedger member of --out: every amount in micro-dollars.
func (s *sessionLedger) json() *object {
	agents := make([]*object, len(s.agents))
	for i, agent := range s.agents {
		agents[i] = newObject().set("agent", agent.id).set("label", agent.label).set("depth", agent.depth).
			set("micros", float64(agent.micros)).set("rows", agent.rows).set("unknownCost", agent.unknown)
	}
	sources := make([]*object, len(s.sources))
	for i, source := range s.sources {
		sources[i] = newObject().set("source", source.source).set("micros", float64(source.micros)).
			set("rows", source.rows).set("unknownCost", source.unknown)
	}
	var cc, diff any
	if s.cc != nil {
		cc = float64(ccMicros(*s.cc))
		if gap, differs := s.difference(); differs {
			diff = float64(gap)
		}
	}
	return newObject().set("sid", s.chat.sid).set("micros", float64(s.total.Micros)).set("rows", s.total.Rows).
		set("beforeLedgerMicros", float64(s.before)).set("sessionMicros", float64(s.sessionMicros())).
		set("unknownCost", s.total.UnknownCost).set("faults", s.faults).set("countsAreObservations", s.observations).
		set("side", newObject().set("micros", float64(s.side.Micros)).set("calls", s.side.Rows).
			set("unknownCost", s.side.UnknownCost)).
		set("claudeCodeMicros", cc).
		set("differenceMicros", diff).set("unreliable", append([]string{}, s.unreliable...)).
		set("byAgent", agents).set("bySource", sources)
}

// ledgerJSON is the costLedger member --out adds, only when a ledger exists.
func (a *auditor) ledgerJSON() ([]*object, bool) {
	if a.ledgers == nil || len(a.ledgers.measured) == 0 {
		return nil, false
	}
	sessions := make([]*object, len(a.ledgers.measured))
	for i, s := range a.ledgers.measured {
		sessions[i] = s.json()
	}
	return sessions, true
}

func (a *auditor) unattributedJSON() *object {
	if a.ledgers == nil || !a.ledgers.unattributed.Present {
		return nil
	}
	ledger := a.ledgers.unattributed
	total := costledger.Sum(ledger.Rows)
	sources := []*object{}
	for _, source := range sourceLines(ledger.Rows) {
		sources = append(sources, newObject().set("source", source.source).set("observations", source.rows).
			set("micros", float64(source.micros)).set("unknownCost", source.unknown))
	}
	return newObject().set("scope", "host-wide window, no session; not additive to selected chats").
		set("observations", total.Rows).set("micros", float64(total.Micros)).
		set("unknownCost", total.UnknownCost).set("faults", ledger.Faults).
		set("inputTokens", float64(total.InputTokens)).set("cacheReadTokens", float64(total.CacheReadTokens)).
		set("cacheCreationTokens", float64(total.CacheCreationTokens)).set("outputTokens", float64(total.OutputTokens)).
		set("mayOverlap", true).set("bySource", sources)
}

// body is the report's cost-ledger lines for the chats measured:
// one block per chat with a ledger (at most top, the dearest first), each
// unreadable ledger named, and the one line for the chats with none. With no
// ledger anywhere it is the line alone.
func (r *ledgerReport) body(top int, heading string) []string {
	if len(r.measured) == 0 && len(r.failed) == 0 && len(r.unattributed.Rows) == 0 && r.unattributed.Faults == 0 {
		return []string{noLedgerLine}
	}
	stableSort(r.measured, func(left, right *sessionLedger) float64 {
		return float64(right.sessionMicros() - left.sessionMicros())
	})
	out := []string{heading}
	for _, s := range firstN(r.measured, top) {
		out = append(out, s.lines(top)...)
	}
	if len(r.measured) > top {
		out = append(
			out,
			"  … "+num(float64(len(r.measured)-top))+" more "+countWord(len(r.measured)-top, "chat", "chats")+
				" with a ledger (--top)",
		)
	}
	if len(r.unattributed.Rows) != 0 || r.unattributed.Faults != 0 {
		total := costledger.Sum(r.unattributed.Rows)
		out = append(out, "  unattributed telemetry (no session, host-wide window; not assigned to selected chats)",
			"    "+strconv.Itoa(total.Rows)+" observations, "+microsDollars(total.Micros)+
				" reported cost; "+strconv.Itoa(total.UnknownCost)+" without cost; "+
				strconv.Itoa(r.unattributed.Faults)+" malformed rows",
			"    tokens input="+strconv.FormatInt(total.InputTokens, 10)+
				" cached="+strconv.FormatInt(total.CacheReadTokens, 10)+
				" cache-write="+strconv.FormatInt(total.CacheCreationTokens, 10)+
				" output="+strconv.FormatInt(total.OutputTokens, 10),
			"    observations may overlap or repeat; not a unique-call count or an additive spend total")
		for _, source := range sourceLines(r.unattributed.Rows) {
			out = append(out, "    "+source.source+": "+strconv.Itoa(source.rows)+" observations, "+
				microsDollars(source.micros))
		}
	}
	out = append(out, r.failed...)
	if r.unmeasured != 0 {
		out = append(out, noLedgerLine+" for "+strconv.Itoa(r.unmeasured)+" of "+strconv.Itoa(r.chats)+" chats")
	}
	return out
}

// reportCostLedger prints the section of the text report: the chats of the
// window, dearest first.
func (a *auditor) reportCostLedger(t *totals) {
	chats := make([]ledgerChat, len(t.famList))
	for i, f := range t.famList {
		chats[i] = ledgerChat{sid: f.sid, title: f.title}
	}
	report := a.measureLedgers(chats)
	heading := "\n== COST LEDGER (every API call the ledger recorded, side calls included; the chats with a ledger, dearest first)"
	for _, line := range report.body(a.opts.top, heading) {
		a.print(line)
	}
}
