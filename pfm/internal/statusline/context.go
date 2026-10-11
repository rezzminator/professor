package statusline

import (
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
	percent      int
	window       int64
	humanPrompts int
	marker       string
	transcript   bool
}

func renderContextLine(runtime Runtime, data input, project string, now time.Time) (contextGauge, string, int64) {
	gauge := transcriptGauge(runtime, data, project)
	percent := gauge.percent
	l2 := urgencyEmoji(percent) + " " + makeBar(percent, 10) + " " + percentColor(percent) +
		gauge.marker + strconv.Itoa(percent) + "%" + reset
	if gauge.transcript && runtime.Engine == pfmengine.Codex && gauge.window > 0 {
		l2 += " " + cWindow + "of " + formatContextTokens(gauge.window) + reset
	}
	contextTokens := data.ContextWindow.CurrentUsage.CacheReadInputTokens +
		data.ContextWindow.CurrentUsage.CacheCreationInputTokens +
		data.ContextWindow.CurrentUsage.InputTokens
	if runtime.Engine == pfmengine.Codex && (runtime.Columns == 0 || runtime.Columns >= 100) && contextTokens > 0 {
		l2 += sep + cTokens + "🧮" + formatContextTokens(contextTokens) + reset
		if gauge.humanPrompts > 0 {
			l2 += " " + dim + "✎" + strconv.Itoa(gauge.humanPrompts) + reset
		}
	}
	if runtime.Engine != pfmengine.Codex {
		l2 += sep + sessionSpendSegment(data, runtime.Prices, readLedgerBooks(runtime, data.SessionID))
	}
	// Never width-gated: a recorded launch reports its cache window and
	// transcript readability. Codex reports no cache split.
	hit := -1
	if runtime.Engine != pfmengine.Codex {
		usage := data.ContextWindow.CurrentUsage
		hit = cacheHitPercent(usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.InputTokens)
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
func sessionSpendSegment(data input, prices *pricing.Prices, books ledgerBooks) string {
	total := data.Cost.TotalCostUSD
	var spend agentSpend
	readable := strings.TrimSpace(data.TranscriptPath) != ""
	if readable {
		var err error
		if spend, err = sessionSpend(data.TranscriptPath, prices); err != nil {
			readable = false
		}
	}
	tools := cWarn + "?" + reset
	if readable {
		tools = cTools + strconv.Itoa(spend.tools) + reset
	}
	if books.ledger.Present {
		booked := ledgerSpend(books.ledger.Rows)
		before := preLedgerUSD(books.ledger.Rows, spend)
		one := completeSpend(booked.usd, before, total)
		line := spendDollars(one, true) + spendTail(booked.in, booked.out, tools)
		if booked.partial {
			line += cWarn + "+?" + reset
		}
		return line + markerText(spendMarker(books, one, 0, false, before))
	}
	if !readable {
		return spendDollars(total, total > 0) + cWarn + "?/?/?" + reset +
			markerText(spendMarker(books, total, 0, false, 0))
	}
	logged := spend.priced > 0 || !spend.partial
	line := spendDollars(spend.usd, logged)
	if total > 0 {
		line = spendDollars(total, true)
	}
	line += spendTail(spend.in, spend.out, tools)
	if spend.partial && logged {
		line += cWarn + "+?" + reset
	}
	return line + markerText(spendMarker(books, total, spend.usd, logged, 0))
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
func transcriptGauge(runtime Runtime, data input, project string) contextGauge {
	fallback := contextGauge{percent: gaugePercent(data.ContextWindow.UsedPercentage)}
	if data.TranscriptPath == "" {
		return fallback
	}
	meta, err := transcript.ReadMeta(data.TranscriptPath, string(runtime.Engine))
	if err != nil {
		return fallback
	}
	window := contextWindow(runtime, data, meta)
	meta.ContextWindow = window
	gauge := contextGauge{
		percent:      gaugePercent(meta.ContextPercent()),
		window:       window,
		humanPrompts: meta.HumanPrompts,
		transcript:   true,
	}
	floorPath := contextFloorPath(runtime, project)
	if meta.CompactedAfterUsage {
		estimate := readContextFloor(floorPath)
		if meta.PostCompactTokens > 0 {
			estimate += meta.PostCompactTokens
		}
		if estimate > 0 {
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

func contextWindow(runtime Runtime, data input, meta transcript.Meta) int64 {
	if override, err := strconv.ParseInt(runtime.getenv("PFM_CONTEXT_WINDOW"), 10, 64); err == nil && override > 0 {
		return override
	}
	if runtime.Engine == pfmengine.Codex {
		return meta.ContextWindow
	}
	if data.ContextWindow.ContextWindowSize > 0 {
		return data.ContextWindow.ContextWindowSize
	}
	model := data.Model.ID
	if model == "" {
		model = meta.Model
	}
	if model == "" {
		model = data.Model.DisplayName
	}
	if strings.Contains(strings.ToLower(model), "haiku") {
		return 200_000
	}
	return 1_000_000
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
