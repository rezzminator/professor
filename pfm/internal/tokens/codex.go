package tokens

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// Codex CLI rollouts. COUNTING RULE: token_count carries a CUMULATIVE
// total_token_usage that RESETS on resume and on compaction, and duplicate
// events re-emit an identical cumulative. So: dedupe on the cumulative, split
// a thread into segments wherever it drops, and sum each segment's PEAK.
// Reading only the final counter undercounts a resumed thread by orders of
// magnitude; summing per-turn deltas double-counts.
var (
	tokenCountMark  = []byte(`"type":"token_count"`)
	turnContextMark = []byte(`"type":"turn_context"`)
)

// codexLine is one rollout record, decoded loosely: every member a reader
// tests is kept raw.
type codexLine struct {
	Type      string          `json:"type"`
	Timestamp json.RawMessage `json:"timestamp"`
	Payload   *codexPayload   `json:"payload"`
}

type codexPayload struct {
	Type      string                     `json:"type"`
	Model     json.RawMessage            `json:"model"`
	Info      *transcript.CodexTokenInfo `json:"info"`
	Name      json.RawMessage            `json:"name"`
	CallID    json.RawMessage            `json:"call_id"`
	Input     json.RawMessage            `json:"input"`
	Arguments json.RawMessage            `json:"arguments"`
	Output    json.RawMessage            `json:"output"`
}

// codexUsage is a thread's usage: each segment's peak, summed.
type codexUsage struct{ in, cached, out, reasoning, total int64 }

// codexPeaks is the segment-peak fold the fast scan and the full audit share.
type codexPeaks struct {
	peaks   []transcript.CodexTokenUsage
	prev    int64
	started bool
	seg     *transcript.CodexTokenUsage
}

// add folds one cumulative usage in; a repeat of the last cumulative is the
// same event written twice and counts nothing (false).
func (p *codexPeaks) add(t transcript.CodexTokenUsage) bool {
	if p.started && t.TotalTokens == p.prev {
		return false
	}
	if p.started && t.TotalTokens < p.prev {
		if p.seg != nil {
			p.peaks = append(p.peaks, *p.seg)
		}
		p.seg = nil
	}
	p.prev, p.started = t.TotalTokens, true
	if p.seg == nil || t.TotalTokens > p.seg.TotalTokens {
		p.seg = &t
	}
	return true
}

func (p *codexPeaks) done() (codexUsage, int) {
	if p.seg != nil {
		p.peaks = append(p.peaks, *p.seg)
	}
	var sum codexUsage
	for _, peak := range p.peaks {
		sum.in += peak.InputTokens
		sum.cached += peak.CachedInputTokens
		sum.out += peak.OutputTokens
		sum.reasoning += peak.ReasoningOutputTokens
		sum.total += peak.TotalTokens
	}
	return sum, len(p.peaks)
}

// modelSet is a set of model names in first-seen order.
type modelSet []string

func (set *modelSet) add(model string) {
	for _, seen := range *set {
		if seen == model {
			return
		}
	}
	*set = append(*set, model)
}

// last is the model a thread ran last, the one it is priced at.
func (set modelSet) last() string {
	if len(set) == 0 {
		return "unknown"
	}
	return set[len(set)-1]
}

// noteModels notes a thread that ran more than one model.
func (a *auditor) noteModels(file string, models modelSet) {
	if len(models) > 1 {
		a.scan.notes = append(a.scan.notes, filepath.Base(file)+" ran "+num(float64(len(models)))+" models ("+
			strings.Join(models, ", ")+`) — priced entirely at "`+models.last()+`"`)
	}
}

// fastUsage is the --codex table's view of a rollout: usage and models only.
type fastUsage struct {
	usage       codexUsage
	segments    int
	models      modelSet
	events      int
	contextPeak int64
}

// codexUsageFast decodes ONLY the complete lines carrying a token_count or
// turn_context record: rollouts reach 110 MB of tool output, and decoding
// every line costs ~15x what decoding the matches costs for an identical
// result.
func (a *auditor) codexUsageFast(file string) fastUsage {
	var (
		result fastUsage
		peaks  codexPeaks
	)
	_, _, err := transcript.ReadLines(file, 0, func(line []byte, complete bool) {
		if !complete || (!bytes.Contains(line, tokenCountMark) && !bytes.Contains(line, turnContextMark)) {
			return
		}
		var record codexLine
		if json.Unmarshal(line, &record) != nil {
			a.scan.badLines++
			return
		}
		payload := record.Payload
		if record.Type == "turn_context" {
			if payload != nil && truthy(payload.Model) {
				result.models.add(keyString(payload.Model))
			}
			return
		}
		if payload == nil || payload.Type != "token_count" || payload.Info == nil {
			return
		}
		if lastUsage := payload.Info.LastTokenUsage; lastUsage != nil {
			result.contextPeak = max(result.contextPeak, lastUsage.InputTokens)
		}
		if total := payload.Info.TotalTokenUsage; total == nil || !peaks.add(*total) {
			return
		}
		result.events++
	})
	if err != nil {
		a.scan.readErrors = append(a.scan.readErrors, file+": "+describe(err))
	}
	result.usage, result.segments = peaks.done()
	return result
}

// codexCost prices a thread at the model it ran last and its peak context;
// nil (rendered "n/a") when unpriced, never an invented $0.
func (a *auditor) codexCost(model string, usage codexUsage, window int64) *float64 {
	row, found := a.prices.table.Resolve(model)
	if found {
		if usd, ok := row.CodexCost(
			pricing.CodexUsage{Input: usage.in, Cached: usage.cached, Output: usage.out},
			window,
		); ok {
			return &usd
		}
	}
	a.scan.unpricedCalls++
	name := model
	if name == "" {
		name = noModel
	}
	a.scan.unpricedModels.bump(name, 1)
	return nil
}

// codexMeta is a rollout's session_meta, nil when it carries none; a meta
// that cannot be read is a read error, never an absent one.
func (a *auditor) codexMeta(file string) *codexmeta.Fields {
	header, err := codexmeta.ReadHeader(file)
	_, isPath := pathError(err)
	switch {
	case err == nil:
		return &header.Fields
	case errors.Is(err, codexmeta.ErrNoSessionMeta):
	case isPath:
		a.scan.readErrors = append(a.scan.readErrors, file+": "+describe(err))
	default:
		a.scan.readErrors = append(a.scan.readErrors, file+" session_meta: "+err.Error())
	}
	return nil
}

// rollouts lists the Codex home's sessions/ and archived_sessions/ rollouts
// (the home itself also holds rollout-backup-* copies that would
// double-count a thread); every place the walk failed to look is a read error.
func (a *auditor) rollouts(ctx context.Context) []codexmeta.Rollout {
	if a.codexHome == "" {
		// an empty home would walk ./sessions of wherever the command runs
		a.scan.readErrors = append(a.scan.readErrors, "no Codex home is configured; pass --codex-root DIR")
		return nil
	}
	found, errs := codexmeta.WalkRollouts(ctx, a.codexHome, codexmeta.SessionsDir, codexmeta.ArchivedSessionsDir)
	for _, err := range errs {
		if pathErr, ok := pathError(err); ok {
			a.scan.readErrors = append(a.scan.readErrors, pathErr.Path+": "+describe(err))
		} else {
			a.scan.readErrors = append(a.scan.readErrors, err.Error())
		}
	}
	return found
}

// mtimeMs is a modification time in JavaScript's fractional milliseconds.
func mtimeMs(at time.Time) float64 { return float64(at.UnixNano()) / 1e6 }

// rolloutStart is when a rollout started: its payload stamp, else its file
// name's local stamp, else fallback.
func (a *auditor) rolloutStart(meta *codexmeta.Fields, file string, fallback float64) float64 {
	if meta != nil {
		if at, ok := dateParse(meta.Timestamp, a.loc); ok && at != 0 {
			return at
		}
	}
	if _, started, stamped, ok := codexmeta.ParseRolloutName(filepath.Base(file), a.loc); ok && stamped {
		if at := float64(started.UnixMilli()); at != 0 {
			return at
		}
	}
	return fallback
}

// threadID is a rollout's thread id: its session_meta id, else the one its
// file name carries.
func threadID(meta *codexmeta.Fields, file string) string {
	if meta != nil && meta.ID != "" {
		return meta.ID
	}
	return codexmeta.RolloutID(filepath.Base(file))
}

func metaRole(meta *codexmeta.Fields) string {
	if meta == nil {
		return ""
	}
	return meta.Role()
}

// codexRow is one thread of the --codex table.
type codexRow struct {
	id, model, cwd, label string
	at                    float64
	usage                 codexUsage
	segments              int
	cost                  *float64
}

// runCodex prints one bounded row per Codex thread in the window.
func (a *auditor) runCodex(ctx context.Context) (int, error) {
	root := a.codexHome
	all := a.rollouts(ctx)
	if len(all) == 0 {
		// an empty walk that failed somewhere names where: an error never reads as absence
		cause := ""
		if len(a.scan.readErrors) != 0 {
			cause = "; READ ERRORS: " + strings.Join(a.scan.readErrors, " | ")
		}
		return 1, failure("no Codex rollouts under %s/sessions or %s/archived_sessions%s", root, root, cause)
	}
	var rows []codexRow
	byID := map[string]bool{}
	old, offProject := 0, 0
	for _, rollout := range all {
		file, modified := rollout.Path, mtimeMs(rollout.ModTime)
		if modified < a.since {
			old++
			continue
		}
		meta := a.codexMeta(file)
		cwd := ""
		if meta != nil {
			cwd = meta.CWD
		}
		if a.opts.project != "" && !strings.Contains(cwd, a.opts.project) {
			offProject++
			continue
		}
		usage := a.codexUsageFast(file)
		if usage.events == 0 {
			continue
		}
		id := threadID(meta, file)
		if byID[id] {
			a.scan.dupFiles++
			continue
		}
		byID[id] = true
		model := usage.models.last()
		a.noteModels(file, usage.models)
		label := metaRole(meta)
		if meta != nil && meta.ThreadSource == threadSubagent {
			if label == "" {
				label = "?"
			}
			label = "subagent (" + label + ")"
		} else if label == "" {
			label = kindMain
		}
		rows = append(rows, codexRow{
			id: id, at: a.rolloutStart(meta, file, modified), model: model, cwd: cwd, label: label,
			usage: usage.usage, segments: usage.segments, cost: a.codexCost(model, usage.usage, usage.contextPeak),
		})
	}
	a.scan.files = len(rows)
	if old != 0 {
		a.scan.notes = append(a.scan.notes, num(float64(old))+" rollouts older than the window")
	}
	if offProject != 0 {
		a.scan.notes = append(a.scan.notes, num(float64(offProject))+" rollouts outside --project "+a.opts.project)
	}
	sort.SliceStable(rows, func(left, right int) bool {
		if difference := orZero(rows[right].cost) - orZero(rows[left].cost); difference != 0 {
			return difference < 0
		}
		return rows[right].usage.total < rows[left].usage.total
	})
	a.printCodex(root, rows, len(all))
	// A rollout we could not read is a failure to look, not an empty thread.
	if len(a.scan.readErrors) != 0 {
		return 1, nil
	}
	return 0, nil
}

func (a *auditor) printCodex(root string, rows []codexRow, rollouts int) {
	var total codexUsage
	cost, unpriced, resumed := 0.0, 0, 0
	for k := range rows {
		row := &rows[k]
		total.in += row.usage.in
		total.cached += row.usage.cached
		total.out += row.usage.out
		total.reasoning += row.usage.reasoning
		total.total += row.usage.total
		if row.cost == nil {
			unpriced++
		} else {
			cost += *row.cost
		}
		if row.segments > 1 {
			resumed++
		}
	}
	count := func(value int64) string { return grouped(float64(value)) }
	cash := func(value *float64) string {
		if value == nil {
			return notPriced
		}
		return "$" + fixed(*value, 2)
	}
	a.print("TOKEN AUDIT · codex · last " + num(a.hours) + "h · " + root + " · " + num(float64(len(rows))) +
		" threads of " + num(float64(rollouts)) + " rollouts")
	a.print(a.gapsLine())
	a.print("\n" + padEnd("STARTED", 17) + padEnd("THREAD", 14) + padEnd("MODEL", 13) + padEnd("LABEL", 22) +
		padStart("IN", 12) + padStart("CACHED", 12) + padStart("OUT", 11) + padStart("EST USD", 10))
	rowList := firstN(rows, a.opts.top)
	for k := range rowList {
		row := &rowList[k]
		a.print(padEnd(strings.Replace(utf16Slice(isoTime(row.at), 0, 16), "T", " ", 1), 17) +
			padEnd(utf16Tail(row.id, 12), 14) + padEnd(utf16Slice(codexShort(row.model), 0, 12), 13) +
			padEnd(utf16Slice(row.label, 0, 21), 22) + padStart(count(row.usage.in), 12) +
			padStart(count(row.usage.cached), 12) + padStart(count(row.usage.out), 11) + padStart(cash(row.cost), 10))
	}
	if len(rows) > a.opts.top {
		a.print("  … " + num(float64(len(rows)-a.opts.top)) + " further threads folded into the total below")
	}
	a.print("\nTOTAL " + count(total.total) + " tok · uncached in " + count(total.in-total.cached) + " · cached in " +
		count(total.cached) + " · out " + count(total.out) + " (incl. " + count(total.reasoning) + " reasoning) · " +
		cash(&cost))
	if resumed != 0 {
		a.print(num(float64(resumed)) + " thread(s) resumed or compacted mid-run; each segment's peak is summed — " +
			"the final counter alone would undercount them.")
	}
	if unpriced != 0 {
		a.print(num(float64(unpriced)) + ` thread(s) ran an unpriced model and show "n/a" — their TOKENS are in the ` +
			"total, their DOLLARS are not.")
	}
}

var (
	codexReadCommand = jsRegexp(
		`(?:sed\s+-n\s+\S+|cat|nl(?:\s+-ba)?|head|tail|bat)\s+(?:-\w+\s+\S+\s+)*["']?([\w./@+-]+\.[\w]{1,6})["']?`,
	)
	contractFile = regexp.MustCompile(`(?:CLAUDE|AGENTS)\.md$`)
	writeStdin   = regexp.MustCompile(`write_stdin`)
	emptyChars   = jsRegexp(`chars:\s*""`)
	sleepCommand = jsRegexp(`\bsleep\s+\d`)
	scriptFailed = regexp.MustCompile(`(?m)^Script (failed|error)`)
	// JavaScript's multiline ^ also starts a line after \r, U+2028 and U+2029
	lineStarts = strings.NewReplacer("\r", "\n", " ", "\n", " ", "\n")
)

// codexOutputText is a tool output as the failure rule reads it: a string as
// itself, a list by its first block carrying text; any other form holds no
// line that could start "Script".
func codexOutputText(output json.RawMessage) string {
	if isString(output) {
		return jsString(output)
	}
	var blocks []json.RawMessage
	if len(output) == 0 || output[0] != '[' || json.Unmarshal(output, &blocks) != nil {
		return ""
	}
	for _, block := range blocks {
		var member struct {
			Text json.RawMessage `json:"text"`
		}
		if len(block) != 0 && block[0] == '{' && json.Unmarshal(block, &member) == nil && truthy(member.Text) {
			return keyString(member.Text)
		}
	}
	return ""
}

// codexAuditRun replays one rollout into a RUN-shaped row carrying every
// measure a --flight row reads.
func (a *auditor) codexAuditRun(file string, meta *codexmeta.Fields) *run {
	var fields codexmeta.Fields
	if meta != nil {
		fields = *meta
	}
	agentID := threadID(meta, file)
	role := metaRole(meta)
	agentType := role
	if agentType == "" {
		agentType = kindMain
		if fields.ThreadSource == threadSubagent {
			agentType = threadSubagent
		}
	}
	sid := fields.ParentThreadID
	if sid == "" {
		sid = fields.SessionID
	}
	r := &run{
		file: file, kind: kindAgent, engine: pricing.EngineCodex, sid: sid,
		agentID: agentID, project: foldCwd(fields.CWD), agentType: agentType, title: role, tools: newObject(),
	}
	var (
		peaks       codexPeaks
		models      modelSet
		pendingPoll bool
		malformed   float64
	)
	calls := map[string]string{}
	seenRead := map[string]int{}
	err := eachLine(file, func(raw []byte) {
		var record codexLine
		if json.Unmarshal(raw, &record) != nil {
			malformed++
			return
		}
		payload := record.Payload
		if payload == nil {
			payload = &codexPayload{}
		}
		switch {
		case record.Type == "compacted" || payload.Type == "compacted":
			r.resets++
		case record.Type == "turn_context":
			if truthy(payload.Model) {
				models.add(keyString(payload.Model))
			}
		case payload.Type == "custom_tool_call" || payload.Type == "function_call":
			input := ""
			if value := coalesce(payload.Input, payload.Arguments); value != nil {
				input = keyString(value)
			}
			name := firstOf(payload.Name)
			if name == "" {
				name = "?"
			}
			calls[keyString(payload.CallID)] = name
			r.tools.childWith(name, "n", "err").bump("n", 1)
			// the cx-measured poll rule: a write_stdin that sends NOTHING, or an explicit wait/sleep
			if (writeStdin.MatchString(input) && emptyChars.MatchString(input)) || name == "wait" ||
				sleepCommand.MatchString(input) {
				pendingPoll = true
			}
			for _, match := range codexReadCommand.FindAllStringSubmatch(input, -1) {
				target := match[1]
				seenRead[target]++
				if seenRead[target] > 1 {
					r.rereadN++
				}
				if contractFile.MatchString(target) {
					r.contractReads++
				}
			}
		case payload.Type == "custom_tool_call_output" || payload.Type == "function_call_output":
			if scriptFailed.MatchString(lineStarts.Replace(codexOutputText(payload.Output))) {
				r.failedCmds++
				r.errs++
				if name, ok := calls[keyString(payload.CallID)]; ok {
					r.tools.childWith(name, "n", "err").bump("err", 1)
				}
			}
		case payload.Type == "token_count" && payload.Info != nil:
			if total := payload.Info.TotalTokenUsage; total == nil || !peaks.add(*total) {
				return
			}
			ctx := 0.0
			if lastUsage := payload.Info.LastTokenUsage; lastUsage != nil {
				ctx = float64(lastUsage.InputTokens)
			}
			r.calls++
			if pendingPoll {
				r.pollN++
				pendingPoll = false
			}
			if r.ctxFirst == 0 {
				r.ctxFirst = ctx
			}
			r.ctxPeak = max(r.ctxPeak, ctx)
			r.ctxSum += ctx
			stamp := ""
			if truthy(record.Timestamp) {
				stamp = keyString(record.Timestamp)
			}
			if ts, ok := dateParse(stamp, a.loc); ok {
				if r.t0 == 0 {
					r.t0 = ts
				}
				r.t1 = ts
			} else {
				a.scan.noTimestamp++
			}
		}
	})
	if err != nil {
		a.scan.readErrors = append(a.scan.readErrors, file+": "+describe(err))
	}
	a.scan.badLines += int(malformed)
	usage, segments := peaks.done()
	r.segments = float64(segments)
	r.tok.in, r.tok.cr, r.tok.out = float64(max(0, usage.in-usage.cached)), float64(usage.cached), float64(usage.out)
	r.model = models.last()
	a.noteModels(file, models)
	if usd := a.codexCost(r.model, usage, int64(r.ctxPeak)); usd == nil {
		r.unpriced = true
	} else {
		r.usd, r.usdLC = *usd, *usd
	}
	return r
}
