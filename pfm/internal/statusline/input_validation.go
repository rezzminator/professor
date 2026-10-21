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

func validateInput(data input) error {
	usage := data.ContextWindow.CurrentUsage
	if usage.InputTokens < 0 || usage.CacheReadInputTokens < 0 || usage.CacheCreationInputTokens < 0 ||
		data.ContextWindow.ContextWindowSize < 0 ||
		data.ContextWindow.TotalInputTokens < 0 ||
		data.ContextWindow.TotalOutputTokens < 0 {
		return fmt.Errorf("negative context counters")
	}
	if data.ContextWindow.UsedPercentage < 0 || data.ContextWindow.UsedPercentage > 100 ||
		math.IsNaN(data.ContextWindow.UsedPercentage) {
		return fmt.Errorf("context percentage outside 0..100")
	}
	if data.Cost.TotalCostUSD < 0 || data.Cost.TotalDurationMS < 0 {
		return fmt.Errorf("negative cost or duration")
	}
	for _, limit := range data.RateLimits.Scoped {
		if limit.Percent != nil && (*limit.Percent < 0 || *limit.Percent > 100 || math.IsNaN(*limit.Percent)) {
			return fmt.Errorf("invalid scoped %s quota", limit.Kind)
		}
	}
	for name, w := range data.RateLimits.Windows {
		if w.UsedPercentage < 0 || w.UsedPercentage > 100 || w.ResetsAt < 0 {
			return fmt.Errorf("invalid %s quota", name)
		}
	}
	return nil
}

func (task *subagentTask) UnmarshalJSON(raw []byte) error {
	type plain subagentTask
	var decoded plain
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
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
