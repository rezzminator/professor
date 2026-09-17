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

	"hostops/pfm/internal/atomicfile"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/transcript"
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
		l2 += " " + dim + "of " + formatContextTokens(gauge.window) + reset
	}
	contextTokens := data.ContextWindow.CurrentUsage.CacheReadInputTokens +
		data.ContextWindow.CurrentUsage.CacheCreationInputTokens +
		data.ContextWindow.CurrentUsage.InputTokens
	if (runtime.Columns == 0 || runtime.Columns >= 100) && contextTokens > 0 {
		l2 += sep + dim + "🧮" + formatContextTokens(contextTokens)
		if gauge.humanPrompts > 0 {
			l2 += " ✎" + strconv.Itoa(gauge.humanPrompts)
		}
		l2 += reset
	}
	// Never width-gated: an absent cache timer is indistinguishable from an
	// expired one. The segment reports transcript readability itself.
	return gauge, l2 + cacheWindowSegment(runtime, now, data.TranscriptPath), contextTokens
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
