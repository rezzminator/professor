package statusline

import (
	"errors"
	"fmt"
	"io/fs"
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
		l2 += sep + sessionSpendSegment(data, runtime.Prices)
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

// sessionSpendSegment is the main line's "💰$4.68/6.5M/31K/46 +$1.87 unlogged".
// The dollars are Claude Code's cost.total_cost_usd: the whole session at list
// price, including the requests it makes that no transcript records (prompt
// suggestions, summaries, titles). The tokens and tool calls are this chat's
// transcript and every sub-agent's, priced at the served table the same way
// as each sub-agent row; whatever the total holds beyond that is named
// "+$X unlogged", so the rows and the line add up. A logged price above the
// total is pfm's own error and renders "logged $X > total" in warning.
// Without a Claude Code figure the logged price leads; a floor carries "+?";
// a transcript that cannot be read is "?/?/?" after the total.
func sessionSpendSegment(data input, prices *pricing.Prices) string {
	total := data.Cost.TotalCostUSD
	if strings.TrimSpace(data.TranscriptPath) == "" {
		return spendDollars(total, total > 0) + cWarn + "?/?/?" + reset
	}
	spend, err := sessionSpend(data.TranscriptPath, prices)
	if err != nil {
		return spendDollars(total, total > 0) + cWarn + "?/?/?" + reset
	}
	logged := spend.priced > 0 || !spend.partial
	line := spendDollars(spend.usd, logged)
	if total > 0 {
		line = spendDollars(total, true)
	}
	line += cTokens + formatContextTokens(spend.in) + reset + cMuted + "/" + reset +
		cTokens + formatContextTokens(spend.out) + reset + cMuted + "/" + reset +
		cTools + strconv.Itoa(spend.tools) + reset
	if spend.partial && logged {
		line += cWarn + "+?" + reset
	}
	if total <= 0 || !logged {
		return line
	}
	switch gap := total - spend.usd; {
	case gap >= 0.005:
		line += cMuted + fmt.Sprintf(" +$%.2f unlogged", gap) + reset
	case gap <= -0.005:
		line += cWarn + fmt.Sprintf(" logged $%.2f > total", spend.usd) + reset
	}
	return line
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
	fallback := contextGauge{percent: int(data.ContextWindow.UsedPercentage)}
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
		percent:      int(meta.ContextPercent()),
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
		gauge.percent = int(meta.ContextPercent())
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
