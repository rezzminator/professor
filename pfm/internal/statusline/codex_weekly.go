package statusline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

type codexUsageCache struct {
	Primary   *codexWindow `json:"primary"`
	Secondary *codexWindow `json:"secondary"`
	PlanType  string       `json:"planType"`
}

type codexWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins int64   `json:"windowDurationMins"`
	ResetsAt           int64   `json:"resetsAt"`
}

// codexWeeklyLine shares the Codex renderer's cache and detached refresher.
// Stretch the bar to the caller's available columns, even without Claude quotas.
// If columns are unknown, reuse the Claude row's width rather than inventing one.
func codexWeeklyLine(runtime Runtime, now time.Time, claudeLine string) string {
	path := filepath.Join(runtime.CacheDir, fmt.Sprintf("cc-gpt-usage-%d.json", runtime.UID))
	armRefresh(runtime, RefreshKindCodex, path, 5*time.Minute)
	window, age, err := readCodexWeekly(path, now)
	label := dim + "codex-7d: no data" + reset
	percent := 0
	if err != nil {
		fmt.Fprintf(runtime.Warn, "statusline: Codex weekly cache %s: %v\n", path, err)
		label = red + "codex-7d: error" + reset
	} else if window != nil {
		percent = gaugePercent(window.UsedPercent)
		label = percentColor(percent) + "codex-7d-used:" + strconv.Itoa(percent) + "%" + reset +
			resetCountdown(now, window.ResetsAt)
		if age > 5*time.Minute {
			label += " " + yellow + "stale " + formatDuration(age.Milliseconds()) + reset
		}
	}
	columns := runtime.Columns
	if columns <= 0 {
		columns = ansi.StringWidth(claudeLine)
	}
	if columns <= 0 {
		return label
	}
	width := columns - ansi.StringWidth(label) - 1
	if width <= 0 {
		return ansi.Truncate(label, columns, "")
	}
	bar := makeBar(percent, width)
	if window == nil || err != nil {
		bar = dim + strings.Repeat("▱", width) + reset
	}
	return bar + " " + label
}

// Missing and unreadable are different visible states. File age follows the
// existing refresher's mtime rule, including caches from before ts was written.
func readCodexWeekly(path string, now time.Time) (*codexWindow, time.Duration, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("read cache: %w", err)
	}
	// An absent or null percent is unknown, not a measured zero. Reuse the
	// shared window fields while decoding this required value with presence.
	type weeklyWindow struct {
		codexWindow
		UsedPercent *float64 `json:"usedPercent"`
	}
	var usage struct {
		Primary   *weeklyWindow `json:"primary"`
		Secondary *weeklyWindow `json:"secondary"`
	}
	if err := json.Unmarshal(body, &usage); err != nil {
		return nil, 0, fmt.Errorf("decode cache: %w", err)
	}
	var window *codexWindow
	// Slots are not durations: a weekly-only plan reports its week as primary.
	// Prefer primary deterministically if both slots describe a seven-day window.
	for _, candidate := range []*weeklyWindow{usage.Primary, usage.Secondary} {
		if candidate == nil || candidate.WindowDurationMins != 7*24*60 {
			continue
		}
		if candidate.UsedPercent == nil {
			return nil, 0, fmt.Errorf("weekly window has no used percent")
		}
		window = &candidate.codexWindow
		window.UsedPercent = *candidate.UsedPercent
		if window.UsedPercent < 0 || window.UsedPercent > 100 || window.ResetsAt <= 0 {
			return nil, 0, fmt.Errorf("invalid weekly window")
		}
		break
	}
	if window == nil {
		return nil, 0, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("stat cache: %w", err)
	}
	return window, now.Sub(info.ModTime()), nil
}
