package statusline

import (
	"encoding/json"
	"fmt"
	"math"
)

func (data *input) UnmarshalJSON(raw []byte) error {
	type plain input
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var presence struct {
		Context struct {
			Usage   json.RawMessage `json:"current_usage"`
			Percent *float64        `json:"used_percentage"`
		} `json:"context_window"`
	}
	if err := json.Unmarshal(raw, &presence); err != nil {
		return err
	}
	var counters struct {
		Input   *int64 `json:"input_tokens"`
		Read    *int64 `json:"cache_read_input_tokens"`
		Created *int64 `json:"cache_creation_input_tokens"`
	}
	if len(presence.Context.Usage) > 0 {
		if err := json.Unmarshal(presence.Context.Usage, &counters); err != nil {
			return err
		}
	}
	decoded.ContextWindow.PercentageKnown = presence.Context.Percent != nil
	decoded.ContextWindow.CurrentUsage.Known = counters.Input != nil || counters.Read != nil || counters.Created != nil
	*data = input(decoded)
	return nil
}

// sanitizeInput degrades every out-of-range value of the payload to unknown and
// returns one note per value, so the line still draws every other segment: a
// bad counter reads as absent, a bad quota window reads ERR, never a lost line.
func sanitizeInput(data *input) []string {
	var notes []string
	window := &data.ContextWindow
	usage := &window.CurrentUsage
	if usage.InputTokens < 0 || usage.CacheReadInputTokens < 0 || usage.CacheCreationInputTokens < 0 {
		notes = append(notes, "negative current_usage counters: context tokens unknown")
		usage.InputTokens, usage.CacheReadInputTokens, usage.CacheCreationInputTokens = 0, 0, 0
		usage.Known = false
	}
	if window.ContextWindowSize < 0 {
		notes = append(notes, fmt.Sprintf("negative context_window_size %d: window unknown", window.ContextWindowSize))
		window.ContextWindowSize = 0
	}
	if window.UsedPercentage < 0 || window.UsedPercentage > 100 || math.IsNaN(window.UsedPercentage) {
		notes = append(notes, fmt.Sprintf("context percentage %v outside 0..100: unknown", window.UsedPercentage))
		window.UsedPercentage, window.PercentageKnown = 0, false
	}
	if data.Cost.TotalCostUSD < 0 {
		notes = append(notes, fmt.Sprintf("negative cost %v: payload cost unknown", data.Cost.TotalCostUSD))
		data.Cost.TotalCostUSD = 0
	}
	if data.Cost.TotalDurationMS < 0 {
		notes = append(notes, fmt.Sprintf("negative duration %d: unknown", data.Cost.TotalDurationMS))
		data.Cost.TotalDurationMS = 0
	}
	for i, limit := range data.RateLimits.Scoped {
		if limit.Percent != nil && (*limit.Percent < 0 || *limit.Percent > 100 || math.IsNaN(*limit.Percent)) {
			notes = append(notes, fmt.Sprintf("invalid scoped %s quota %v", limit.Kind, *limit.Percent))
			data.RateLimits.Scoped[i].Percent = nil
			// Only the Fable window draws; another model's bad limit is no row's.
			data.RateLimits.scopedFailed = data.RateLimits.scopedFailed || limit.FableWeekly()
		}
	}
	for name, w := range data.RateLimits.Windows {
		if w.UsedPercentage < 0 || w.UsedPercentage > 100 || w.ResetsAt < 0 {
			notes = append(notes, fmt.Sprintf("invalid %s quota", name))
			data.RateLimits.Windows[name] = rateWindow{Failure: true}
		}
	}
	return notes
}

// UnmarshalJSON keeps a task whose fields cannot be read as its id alone with
// decodeErr set: that row shows the fault and its sibling rows render whole.
func (task *subagentTask) UnmarshalJSON(raw []byte) error {
	type plain subagentTask
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		var ident struct {
			ID string `json:"id"`
		}
		// A wrong-typed id leaves ident.ID empty: the task has no row, and err says why.
		_ = json.Unmarshal(raw, &ident)
		*task = subagentTask{ID: ident.ID, decodeErr: err}
		return nil
	}
	var present struct {
		Tokens *int64 `json:"tokenCount"`
	}
	if err := json.Unmarshal(raw, &present); err != nil {
		return err
	}
	decoded.tokenKnown = present.Tokens != nil
	*task = subagentTask(decoded)
	return nil
}
