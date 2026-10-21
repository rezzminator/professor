package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

const rootProjectKey = "root"

type contextGauge struct {
	model         string
	transcriptErr bool
	tokens        int64
	known         bool
	capacityState string
	cacheHit      int
	percent       int
	percentKnown  bool
	window        int64
	humanPrompts  int
	marker        string
	transcript    bool
	// scanned is what the gauge's pass over the transcript billed, nil when
	// that pass did not complete: the spend segment reuses it instead of
	// reading the same file again.
	scanned *agentSpend
}

func renderContextLine(runtime Runtime, data input, project string, now time.Time) (contextGauge, string, int64) {
	var mainScan *spendScan
	if runtime.Engine != pfmengine.Codex {
		mainScan = newSpendScan(runtime.Prices)
	}
	gauge := transcriptGauge(runtime, data, project, mainScan)
	percent := gauge.percent
	label := "?%"
	if (gauge.window > 0 && gauge.known) || gauge.percentKnown {
		label = strconv.Itoa(percent) + "%"
	} else if !gauge.known && data.ContextWindow.UsedPercentage > 0 {
		label = strconv.Itoa(percent) + "%"
	}
	count := "?"
	if gauge.known {
		count = formatContextTokens(gauge.tokens)
	}
	l2 := urgencyEmoji(
		percent,
	) + " " + makeBar(
		percent,
		10,
	) + " " + percentColor(
		percent,
	) + gauge.marker + label + " " + count + reset
	if gauge.capacityState != "" {
		l2 += " " + cWarn + "ctx " + gauge.capacityState + reset
	}
	if gauge.transcript && runtime.Engine == pfmengine.Codex && gauge.window > 0 {
		l2 += " " + cWindow + "of " + formatContextTokens(gauge.window) + reset
	}
	if runtime.Engine == pfmengine.Codex && gauge.humanPrompts > 0 {
		l2 += " " + dim + "✎" + strconv.Itoa(gauge.humanPrompts) + reset
	}
	contextTokens := gauge.tokens
	if runtime.Engine != pfmengine.Codex {
		l2 += sep + sessionSpendSegment(data, runtime.Prices, readLedgerBooks(runtime, data.SessionID), gauge.scanned)
	}
	// Never width-gated: a recorded launch reports its cache window and
	// transcript readability. Codex reports no cache split.
	hit := -1
	if runtime.Engine != pfmengine.Codex {
		usage := data.ContextWindow.CurrentUsage
		hit = cacheHitPercent(usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.InputTokens)
	}
	if openAIModel(gauge.model) || openAIModel(data.Model.ID) ||
		(data.Model.ID == "" && openAIModel(data.Model.DisplayName)) {
		text := unmeasuredCacheText(gauge.cacheHit)
		if gauge.transcriptErr {
			text = cBad + "💾!" + reset
		}
		return gauge, l2 + sep + text, contextTokens
	}
	return gauge, l2 + cacheWindowSegment(
		runtime,
		now,
		data.TranscriptPath,
		hit,
		data.PromptCache,
		data.SessionID,
	), contextTokens
}

// sessionSpendSegment is the main line's "💰$4.68/6.5M/31K/46 sink failed": exactly ONE dollar
// amount in every state, then prompt tokens, output tokens and tool calls. With a cost ledger the
// amount is the complete session total (completeSpend): the ledger's total — Claude Code's own
// price of every API call of the session, the ones no transcript records included — plus the spend
// the ledger never saw (preLedgerUSD), unless Claude Code's payload is ahead of that by more than the
// tolerance; the tokens are the ledger's sums. That total is the main thread's rows plus every
// top-level sub-agent row plus the before-ledger spend, and `pfm tokens`' session total. Without one the amount is Claude Code's cost.total_cost_usd and the tokens are the
// transcripts', this chat's and every sub-agent's, priced at the served table; with no Claude Code
// figure the logged price leads. Tool calls are always the transcripts'. A floor carries "+?" (a
// call whose cost never arrived, a model the table cannot price); a transcript that cannot be
// read is "?" in its place. A state worth flagging ends in one marker with no amount in it
// (spendMarker); the amounts behind a marker are `pfm tokens`'.
func sessionSpendSegment(data input, prices *pricing.Prices, books ledgerBooks, scanned *agentSpend) string {
	total := data.Cost.TotalCostUSD
	var spend agentSpend
	readable := strings.TrimSpace(data.TranscriptPath) != ""
	if readable {
		var err error
		if spend, err = sessionSpend(data.TranscriptPath, prices, scanned); err != nil {
			readable = false
		}
	}
	tools := cWarn + "?" + reset
	if readable {
		tools = cTools + strconv.Itoa(spend.tools) + reset
	}
	if books.ledger.Present {
		native := ledgerSpend(books.ledger.Rows)
		booked := spend.withLedger(native)
		before := preLedgerUSD(books.ledger.Rows, spend)
		if supplement := booked.usd - native.usd; supplement > 0 {
			var ambiguous bool
			before, ambiguous = beforeGatewayOverlap(books.ledger.Rows, spend, supplement)
			booked.partial = booked.partial || ambiguous
		}
		one := completeSpend(booked.usd, before, total)
		line := spendDollars(one, true) + spendTail(booked.in, booked.out, tools)
		if booked.partial {
			line += cWarn + "+?" + reset
		}
		return line + markerText(spendMarker(books, one, 0, false, before)) + booked.gateway.note()
	}
	if !readable {
		return spendDollars(total, total > 0) + cWarn + "?/?/?" + reset +
			markerText(spendMarker(books, total, 0, false, 0))
	}
	logged := spend.priced > 0 || !spend.partial
	line := spendDollars(spend.usd, logged)
	if total > 0 {
		if spend.gateway.present {
			total = max(total, spend.usd)
		}
		line = spendDollars(total, true)
	}
	line += spendTail(spend.in, spend.out, tools)
	if spend.partial && logged {
		line += cWarn + "+?" + reset
	}
	return line + markerText(spendMarker(books, total, spend.usd, logged, 0)) + spend.gateway.note()
}

// spendTail is "1.0M/5.1K/46": prompt tokens, output tokens, tool calls.
func spendTail(in, out int64, tools string) string {
	return cTokens + formatContextTokens(in) + reset + cMuted + "/" + reset +
		cTokens + formatContextTokens(out) + reset + cMuted + "/" + reset + tools
}

// markerText renders a spendMarker, "" for none.
func markerText(marker string) string {
	if marker == "" {
		return ""
	}
	return cWarn + " " + marker + reset
}

// spendDollars is "💰$4.68/" coloured by size, or "💰$?/" when unknown.
func spendDollars(usd float64, known bool) string {
	if !known {
		return cWarn + "💰$?" + reset + cMuted + "/" + reset
	}
	color := dim
	if usd >= 10 {
		color = red
	} else if usd >= 2 {
		color = yellow
	}
	return color + "💰" + fmt.Sprintf("$%.2f", usd) + reset + cMuted + "/" + reset
}

// transcriptGauge makes the transcript authoritative whenever it can be read.
// The harness percentage is retained only for absent or unreadable transcripts.
func transcriptGauge(runtime Runtime, data input, project string, scan *spendScan) contextGauge {
	usage := data.ContextWindow.CurrentUsage
	fallback := contextGauge{
		model:    data.Model.ID,
		percent:  gaugePercent(data.ContextWindow.UsedPercentage),
		tokens:   usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens,
		cacheHit: -1,
	}
	fallback.known = usage.Known || fallback.tokens > 0
	fallback.percentKnown = !openAIModel(data.Model.ID) &&
		(data.ContextWindow.PercentageKnown || data.ContextWindow.UsedPercentage > 0)
	fallback.window, fallback.capacityState = resolvedContextWindow(runtime, data, transcript.Meta{})
	if fallback.known && fallback.window > 0 {
		fallback.percent = gaugePercent(float64(fallback.tokens) * 100 / float64(fallback.window))
	}
	if data.TranscriptPath == "" {
		return fallback
	}
	activity := agentActivity{cacheHit: -1}
	meta, err := transcript.ReadMetaObserving(data.TranscriptPath, string(runtime.Engine), func(line []byte) {
		if scan != nil {
			scan.line(line)
		}
		// Reuse the completed-call cache rule without another transcript read.
		var record struct {
			Type      string `json:"type"`
			Sidechain bool   `json:"isSidechain"`
			Message   struct {
				Usage *transcriptUsage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &record) == nil && record.Type == entryAssistant && !record.Sidechain &&
			record.Message.Usage != nil &&
			record.Message.Usage.reportsCache {
			u := record.Message.Usage
			if hit := cacheHitPercent(u.CacheRead, u.CacheCreation, u.Input); hit >= 0 {
				activity.cacheHit = hit
			}
		}
	})
	if err != nil {
		fallback.transcriptErr = true
		return fallback
	}
	fallback.cacheHit = activity.cacheHit
	fallback.model = currentModel(data.Model.ID, meta.Model)
	if data.Model.ID == "" {
		fallback.window, fallback.capacityState = resolvedContextWindow(runtime, data, meta)
		if fallback.known && fallback.window > 0 {
			fallback.percent = gaugePercent(float64(fallback.tokens) * 100 / float64(fallback.window))
		}
	}
	if scan != nil {
		scanned := scan.total()
		fallback.scanned = &scanned
	}
	// A readable transcript without a usable current request is not a zero gauge.
	if !meta.ContextKnown {
		return fallback
	}
	if data.Model.ID != "" && meta.Model != "" &&
		canonicalContextModel(data.Model.ID) != canonicalContextModel(meta.Model) {
		return fallback
	}
	window, state := resolvedContextWindow(runtime, data, meta)
	meta.ContextWindow = window
	gauge := fallback
	gauge.window, gauge.capacityState = window, state
	gauge.percentKnown = window > 0
	gauge.tokens, gauge.known = meta.ContextTokens, true
	gauge.percent = gaugePercent(meta.ContextPercent())
	gauge.humanPrompts, gauge.transcript = meta.HumanPrompts, true
	floorPath := contextFloorPath(runtime, project)
	if meta.CompactedAfterUsage {
		estimate := readContextFloor(floorPath) + meta.PostCompactTokens
		if estimate > 0 {
			gauge.tokens = estimate
			meta.ContextTokens = estimate
		}
		gauge.percent = gaugePercent(meta.ContextPercent())
		gauge.marker = "~"
		return gauge
	}
	if meta.ContextTokens > 0 {
		floor := readContextFloor(floorPath)
		if floor == 0 || meta.ContextTokens < floor {
			writeContextFloor(floorPath, meta.ContextTokens)
		}
	}
	return gauge
}

func resolvedContextWindow(runtime Runtime, data input, meta transcript.Meta) (int64, string) {
	if override, err := strconv.ParseInt(runtime.getenv("PFM_CONTEXT_WINDOW"), 10, 64); err == nil && override > 0 {
		return override, ""
	}
	if runtime.Engine == pfmengine.Codex && meta.ContextWindow > 0 {
		return meta.ContextWindow, ""
	}
	model := currentModel(data.Model.ID, meta.Model)
	if model == "" {
		model = data.Model.DisplayName
	}
	if openAIModel(model) {
		return runtime.catalog().capacity(model)
	}
	if data.ContextWindow.ContextWindowSize > 0 {
		return data.ContextWindow.ContextWindowSize, ""
	}
	return 0, "unknown"
}

func contextWindow(runtime Runtime, data input, meta transcript.Meta) int64 {
	window, _ := resolvedContextWindow(runtime, data, meta)
	return window
}

func contextFloorPath(runtime Runtime, project string) string {
	cacheRoot := runtime.getenv("XDG_CACHE_HOME")
	if cacheRoot == "" {
		cacheRoot = filepath.Join(runtime.Home, ".cache")
	}
	engineID := runtime.Engine
	if engineID == "" {
		engineID = pfmengine.Claude
	}
	harness := pfmengine.MustLookup(engineID).LongName
	return filepath.Join(cacheRoot, "pfm-statusline", harness+"-"+sanitizeProject(project)+".txt")
}

func sanitizeProject(project string) string {
	if strings.TrimSpace(project) == "" {
		return rootProjectKey
	}
	project = filepath.ToSlash(filepath.Clean(project))
	var sanitized strings.Builder
	separator := false
	for _, character := range project {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			sanitized.WriteRune(character)
			separator = false
			continue
		}
		if sanitized.Len() > 0 && !separator {
			sanitized.WriteByte('-')
			separator = true
		}
	}
	name := strings.Trim(sanitized.String(), "-")
	if name == "" {
		return rootProjectKey
	}
	if len(name) > 80 {
		name = strings.TrimRight(name[:80], "-")
		if name == "" {
			return rootProjectKey
		}
	}
	return name
}

func readContextFloor(path string) int64 {
	body, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "statusline: read context floor %s: %v\n", path, err)
		}
		return 0
	}
	floor, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "statusline: parse context floor %s: %v\n", path, err)
		return 0
	}
	if floor <= 0 {
		fmt.Fprintf(os.Stderr, "statusline: context floor %s is non-positive: %d\n", path, floor)
		return 0
	}
	return floor
}

func writeContextFloor(path string, floor int64) {
	if floor <= 0 {
		return
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "statusline: create context floor directory %s: %v\n", directory, err)
		return
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "statusline: secure context floor directory %s: %v\n", directory, err)
		return
	}
	if err := atomicfile.Write(path, []byte(strconv.FormatInt(floor, 10)+"\n"), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "statusline: write context floor %s: %v\n", path, err)
	}
}

// gaugePercent is a context or rate-limit share as the statusline shows it:
// rounded to the nearest whole percent, so 69.1K of 1.0M reads 7%, never a
// truncated 6%; a window not yet full stops at 99, so 100% always means full.
func gaugePercent(share float64) int {
	rounded := int(math.Round(share))
	if rounded >= 100 && share < 100 {
		return 99
	}
	return rounded
}
