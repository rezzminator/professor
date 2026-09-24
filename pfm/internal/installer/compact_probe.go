package installer

import (
	"encoding/json"
	"strconv"
)

const (
	// compactWindowProbeName names doctor's window row in the hook report.
	compactWindowProbeName = "compact-window"
	// compactWindowAbsent is the row's got value when no window is set.
	compactWindowAbsent = "absent"
)

// probeCompactWindow proves the window pfm install writes while both
// auto-compact thresholds are set (docs/design/context/compaction.md § What
// pfm install writes): env.CLAUDE_CODE_AUTO_COMPACT_WINDOW holds the lower
// threshold, or the row is hook-drift naming what the file holds. An absent
// or unreadable file adds no row: probeClaudeSettings already reports it.
func probeCompactWindow(file, target string, window int) []HookProbeResult {
	raw, absent, err := readHookFile(file)
	if err != nil || absent {
		return nil
	}
	var document map[string]any
	if json.Unmarshal(raw, &document) != nil {
		return nil
	}
	hook := ExpectedHook{Target: target, File: file, Event: "env", Name: compactWindowProbeName}
	want := strconv.Itoa(window)
	got, present := currentCompactEnvValue(document)
	if !present {
		got = compactWindowAbsent
	}
	if got == want {
		return []HookProbeResult{{Hook: hook, State: stateOK}}
	}
	return []HookProbeResult{{Hook: hook, State: stateHookDrift, What: autoCompactWindowEnv, Want: want, Got: got}}
}
