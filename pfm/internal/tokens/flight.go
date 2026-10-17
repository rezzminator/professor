package tokens

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// ledgerRow is one agent a flight's ledger says it spawned. hasDateOnly is
// false for a run.md row, which carries no such field.
type ledgerRow struct {
	taskID, agentType, agentID, round, engine string
	at                                        float64
	dateOnly, hasDateOnly                     bool
}

// names is a ledger row whose engine column admits engine: that engine, a
// seat (either engine), or "?" (unrecorded).
// names reports whether the row may belong to engine. A model-qualified engine
// ("claude-opus", "codex-gpt") names its family, the part before the first dash.
func (row ledgerRow) names(engine string) bool {
	family, _, _ := strings.Cut(row.engine, "-")
	return family == engine || row.engine == "?" || row.engine == "seat"
}

func (row ledgerRow) json() *object {
	out := newObject().set("taskId", row.taskID).set("agentType", row.agentType).set("agentId", row.agentID).
		set("round", row.round).set("at", row.at)
	if row.hasDateOnly {
		out.set("dateOnly", row.dateOnly)
	}
	return out.set("engine", row.engine)
}

// flightPlan is the ledger a flight leaves behind, parsed BEFORE the scan
// because it, not --since, sets the window. agents.tsv is the primary key —
// append-only, tab-separated, one row per spawn: task-id · agent-type ·
// agent-id · round · spawn-time (ISO) · engine (claude|codex|seat); an
// optional header line is tolerated. With no agents.tsv, run.md's header
// instant and its "{id} CLAIMED · {agent} · {time}" lines are the fallback,
// and EVERY row it yields is matched by window: run.md carries no agent id.
type flightPlan struct {
	rows             []ledgerRow
	start            float64
	baseline, source string
	gaps             []string
}

var (
	ledgerHeader  = regexp.MustCompile(`(?i)^(task[-_ ]?id|unit)$`)
	clockStamp    = regexp.MustCompile(`T\d{2}:\d{2}`)
	runBaseline   = jsRegexp(`baseline\s+([0-9a-f]{7,40})`)
	runInstant    = regexp.MustCompile(`(\d{4}-\d{2}-\d{2}T[\d:.]+(?:Z|[+-]\d{2}:?\d{2})?)`)
	claimedLine   = jsRegexp(`^(\S+)\s+CLAIMED\s+·\s+(.*)$`)
	requestedTail = jsRegexp(`\s+requested.*$`)
	nonAlnum      = regexp.MustCompile(`[^a-z0-9]+`)
)

// loadFlightPlan reads --flight DIR's ledger and sets the window from it.
func (a *auditor) loadFlightPlan() error {
	given := a.opts.flight
	dir, err := filepath.Abs(given)
	if err != nil {
		return failure("--flight %s is not readable: %v", given, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return failure("--flight %s is not readable: %s", given, describe(err))
	}
	if !info.IsDir() {
		return usageError("--flight %s is not a directory", given)
	}
	plan := &flightPlan{}
	a.flight = plan
	tsvPath := filepath.Join(dir, "agents.tsv")
	tsv, err := os.ReadFile(tsvPath)
	switch {
	case err == nil:
		if err := plan.readLedger(tsvPath, string(tsv), a.loc); err != nil {
			return err
		}
	case errors.Is(err, fs.ErrNotExist):
		if err := plan.readRunLog(dir, a.loc); err != nil {
			return err
		}
	default:
		return failure("%s is not readable: %s", tsvPath, describe(err))
	}
	var times []float64
	for _, at := range append([]float64{plan.start}, plan.ats()...) {
		if at > 0 {
			times = append(times, at)
		}
	}
	if len(times) == 0 {
		return failure(
			"%s: no parseable spawn time in agents.tsv and no run.md header instant — the flight cannot be dated",
			dir,
		)
	}
	plan.start = times[0]
	for _, at := range times[1:] {
		plan.start = math.Min(plan.start, at)
	}
	a.since = plan.start - 10*60e3 // 10 minutes of slack: a transcript's first call precedes its ledger row
	a.hours = math.Max(1, (a.now-a.since)/3600e3)
	return nil
}

func (plan *flightPlan) ats() []float64 {
	out := make([]float64, len(plan.rows))
	for i, row := range plan.rows {
		out[i] = row.at
	}
	return out
}

func (plan *flightPlan) readLedger(tsvPath, tsv string, loc *time.Location) error {
	plan.source = "agents.tsv"
	lines := strings.Split(tsv, "\n")
	nonEmpty := 0
	for i, line := range lines {
		if jsTrim(line) == "" {
			continue
		}
		nonEmpty++
		fields := strings.Split(line, "\t")
		for k := range fields {
			fields[k] = jsTrim(fields[k])
		}
		if i == 0 && ledgerHeader.MatchString(fields[0]) {
			continue // optional header
		}
		field := func(k int) string {
			if k < len(fields) {
				return fields[k]
			}
			return ""
		}
		lineName := "agents.tsv line " + num(float64(i+1))
		if len(fields) < 3 || fields[0] == "" || fields[2] == "" {
			plan.gaps = append(plan.gaps, lineName+": "+num(float64(len(fields)))+
				" field(s), want task-id/agent-type/agent-id/round/spawn/engine — row skipped")
			continue
		}
		spawn := field(4)
		at, parsed := math.NaN(), false
		if spawn != "" {
			at, parsed = dateParse(spawn, loc)
			if !parsed {
				plan.gaps = append(plan.gaps, lineName+`: spawn time "`+spawn+`" did not parse`)
			}
		}
		// A date without a clock parses to midnight: it may date the flight, never open a match window.
		dateOnly := spawn != "" && !clockStamp.MatchString(spawn)
		if dateOnly && parsed {
			plan.gaps = append(plan.gaps, lineName+`: spawn time "`+spawn+
				`" has no clock — the row matches by id or agent path only, never by window`)
		}
		if !parsed {
			at = 0
		}
		engine := strings.ToLower(field(5))
		if engine == "" {
			engine = "?"
		}
		plan.rows = append(plan.rows, ledgerRow{
			taskID: fields[0], agentType: field(1), agentID: fields[2], round: field(3), at: at,
			dateOnly: dateOnly, hasDateOnly: true, engine: engine,
		})
	}
	if len(plan.rows) == 0 {
		return failure("%s yielded no usable rows (%d non-empty lines) — it exists but this report would cover NOTHING",
			tsvPath, nonEmpty)
	}
	return nil
}

func (plan *flightPlan) readRunLog(dir string, loc *time.Location) error {
	runPath := filepath.Join(dir, "run.md")
	content, err := os.ReadFile(runPath)
	if err != nil {
		return failure("%s: no agents.tsv, and %s is not readable either: %s", dir, runPath, describe(err))
	}
	plan.source = "run.md (no agents.tsv — every row matched by window)"
	lines := strings.Split(string(content), "\n")
	head := lines[0]
	instant := runInstant.FindStringSubmatch(head)
	if instant == nil {
		return failure("%s line 1 carries no parseable flight instant — refusing to invent a window", runPath)
	}
	start, ok := dateParse(instant[1], loc)
	if baseline := runBaseline.FindStringSubmatch(head); baseline != nil {
		plan.baseline = baseline[1]
	}
	if !ok {
		return failure(`%s line 1 instant "%s" did not parse`, runPath, instant[1])
	}
	plan.start = start
	for _, line := range lines {
		match := claimedLine.FindStringSubmatch(jsTrim(line))
		if match == nil {
			continue
		}
		rest := match[2]
		at := start
		if stamp := runInstant.FindStringSubmatch(rest); stamp != nil {
			if at, ok = dateParse(stamp[1], loc); !ok {
				at = math.NaN()
			}
		}
		agentType, _, _ := strings.Cut(rest, "·")
		plan.rows = append(plan.rows, ledgerRow{
			taskID: match[1], agentType: requestedTail.ReplaceAllString(jsTrim(agentType), ""), at: at, engine: "?",
		})
	}
	if len(plan.rows) == 0 {
		return failure(
			`%s has a header but no "{id} CLAIMED · {agent}" line — this report would cover NOTHING`,
			runPath,
		)
	}
	return nil
}

func normRole(text string) string {
	return jsTrim(nonAlnum.ReplaceAllString(strings.ToLower(text), " "))
}

// roleMatch is a transcript role and a ledger agent type naming one job.
func roleMatch(left, right string) bool {
	x, y := normRole(left), normRole(right)
	if x == "" || y == "" {
		return false
	}
	return strings.Contains(x, y) || strings.Contains(y, x) || lastWord(x) == lastWord(y)
}

func lastWord(text string) string { return text[strings.LastIndex(text, " ")+1:] }

// codexCandidate is a Codex thread a ledger row may name; its ids are THIS
// thread's own: session_id and parent_thread_id name the parent on a
// sub-agent thread, and would hand a child the orchestrator's row.
type codexCandidate struct {
	file, role, agentPath string
	meta                  *codexmeta.Fields
	at                    float64
	ids                   []string
}

func (a *auditor) codexCandidates(ctx context.Context) []*codexCandidate {
	var out []*codexCandidate
	for _, rollout := range a.rollouts(ctx) {
		file, modified := rollout.Path, mtimeMs(rollout.ModTime)
		id, started, stamped, named := codexmeta.ParseRolloutName(filepath.Base(rollout.Path), a.loc)
		nameAt := 0.0
		if named {
			nameAt = math.NaN()
			if stamped {
				nameAt = float64(started.UnixMilli())
			}
		}
		if modified < a.since && nameAt < a.since {
			continue
		}
		meta := a.codexMeta(file)
		candidate := &codexCandidate{
			file: file,
			meta: meta,
			at:   a.rolloutStart(meta, file, modified),
			role: metaRole(meta),
		}
		if meta != nil {
			candidate.agentPath = meta.AgentPath
			candidate.ids = appendNonEmpty(candidate.ids, meta.ID, meta.ContextWindow.WindowID)
		}
		if named {
			candidate.ids = appendNonEmpty(candidate.ids, id)
		}
		out = append(out, candidate)
	}
	return out
}

func appendNonEmpty(list []string, values ...string) []string {
	for _, value := range values {
		if value != "" {
			list = append(list, value)
		}
	}
	return list
}

// flightRow is one ledger row and the transcript it matched.
type flightRow struct {
	led ledgerRow
	run *run
	how string
}

// flightMatch is what matching the ledger against the transcripts found.
type flightMatch struct {
	matched               []flightRow
	unmatched, mismatches []string
	takenClaude           map[string]bool
	takenCodex            map[string]bool
}

func distance(at, target float64) float64 { return math.Abs(at - target) }

// matchLedger pairs each ledger row with its transcript: by agent id, then
// (Codex) by agent path, then by this row's window and role.
func (a *auditor) matchLedger(candidates []*codexCandidate, audit func(*codexCandidate) *run) *flightMatch {
	const windowBefore, windowAfter = 10 * 60e3, 180 * 60e3
	var claudeAgents []*run
	byAgentID := map[string]*run{}
	for _, r := range a.runs {
		if r.kind == kindAgent {
			claudeAgents = append(claudeAgents, r)
			byAgentID[r.agentID] = r
		}
	}
	codexByID := map[string]*codexCandidate{}
	for _, candidate := range candidates {
		for _, id := range candidate.ids {
			if _, seen := codexByID[id]; !seen {
				codexByID[id] = candidate
			}
		}
	}
	m := &flightMatch{takenClaude: map[string]bool{}, takenCodex: map[string]bool{}}
	takeCodex := func(c *codexCandidate) *run {
		m.takenCodex[c.file] = true
		return audit(c)
	}
	for _, led := range a.flight.rows {
		wantClaude := led.names(pricing.EngineClaude)
		wantCodex := led.names(pricing.EngineCodex)
		var found *run
		how := ""
		if r, ok := byAgentID[led.agentID]; led.agentID != "" && wantClaude && ok && !m.takenClaude[led.agentID] {
			found, how = r, "id"
			m.takenClaude[led.agentID] = true
		}
		if c, ok := codexByID[led.agentID]; found == nil && led.agentID != "" && wantCodex && ok &&
			!m.takenCodex[c.file] {
			found, how = takeCodex(c), "id"
		}
		if found == nil && strings.HasPrefix(led.agentID, "/") && wantCodex {
			// A Codex orchestrator knows its sub-agent by agent path ("/root/fix_1a"), never by thread
			// id. A path is unique inside one thread tree only, so the spawn nearest the row's time wins.
			var byPath []*codexCandidate
			for _, c := range candidates {
				if c.agentPath != "" && c.agentPath == led.agentID && !m.takenCodex[c.file] {
					byPath = append(byPath, c)
				}
			}
			// with no time on the row, the latest spawn
			if pick := nearest(byPath, func(c *codexCandidate) float64 {
				if led.at != 0 && !math.IsNaN(led.at) && !led.dateOnly {
					return distance(c.at, led.at)
				}
				return -c.at
			}); pick != nil {
				found, how = takeCodex(pick), "path"
			}
		}
		if found == nil && led.at != 0 && !math.IsNaN(led.at) && !led.dateOnly {
			found, how = a.matchWindow(m, led, claudeAgents, candidates, takeCodex, windowBefore, windowAfter)
		}
		if found == nil {
			agentType, agentID := led.agentType, led.agentID
			if agentType == "" {
				agentType = "?"
			}
			if agentID == "" {
				agentID = noModel
			}
			m.unmatched = append(m.unmatched, "LEDGER ROW "+led.taskID+" · "+agentType+" · id "+agentID+" · engine "+
				led.engine+" · no transcript found in the window")
			continue
		}
		m.matched = append(m.matched, flightRow{led: led, run: found, how: how})
	}
	return m
}

// matchWindow falls back to THIS row's window and type when the id form did
// not match (or there is none).
func (a *auditor) matchWindow(m *flightMatch, led ledgerRow, claudeAgents []*run, candidates []*codexCandidate,
	takeCodex func(*codexCandidate) *run, before, after float64,
) (*run, string) {
	lo, hi := led.at-before, led.at+after
	var claude, claudeRole []*run
	if led.names(pricing.EngineClaude) {
		for _, r := range claudeAgents {
			if !m.takenClaude[r.agentID] && r.t0 >= lo && r.t0 <= hi {
				claude = append(claude, r)
				if roleMatch(firstText(r.agentType, r.title), led.agentType) {
					claudeRole = append(claudeRole, r)
				}
			}
		}
	}
	var codex, codexRole []*codexCandidate
	if led.names(pricing.EngineCodex) {
		for _, c := range candidates {
			if !m.takenCodex[c.file] && c.at >= lo && c.at <= hi {
				codex = append(codex, c)
				if roleMatch(c.role, led.agentType) {
					codexRole = append(codexRole, c)
				}
			}
		}
	}
	nearestRun := func(rs []*run) *run {
		return nearest(rs, func(r *run) float64 { return distance(r.t0, led.at) })
	}
	nearestCodex := func(cs []*codexCandidate) *codexCandidate {
		return nearest(cs, func(c *codexCandidate) float64 { return distance(c.at, led.at) })
	}
	pickClaude := nearestRun(claudeRole)
	if pickClaude == nil {
		pickClaude = nearestRun(claude)
	}
	pickCodex := nearestCodex(codexRole)
	if pickCodex == nil {
		pickCodex = nearestCodex(codex)
	}
	if pickCodex != nil && (pickClaude == nil || distance(pickCodex.at, led.at) < distance(pickClaude.t0, led.at)) {
		if !roleMatch(pickCodex.role, led.agentType) {
			m.mismatches = append(m.mismatches, led.taskID+"→"+firstText(pickCodex.role, "?"))
		}
		return takeCodex(pickCodex), "window"
	}
	if pickClaude == nil {
		return nil, ""
	}
	m.takenClaude[pickClaude.agentID] = true
	if !roleMatch(firstText(pickClaude.agentType, pickClaude.title), led.agentType) {
		m.mismatches = append(m.mismatches, led.taskID+"→"+firstText(pickClaude.agentType, "?"))
	}
	return pickClaude, "window"
}

// firstText is a || b for strings.
func firstText(text, fallback string) string {
	if text != "" {
		return text
	}
	return fallback
}

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

// nearest is the item distanceOf ranks first, the earliest of a tie; the zero
// value when there is none. items is sorted in place.
func nearest[T any](items []T, distanceOf func(T) float64) T {
	stableSort(items, func(left, right T) float64 { return distanceOf(left) - distanceOf(right) })
	if len(items) == 0 {
		var none T
		return none
	}
	return items[0]
}
