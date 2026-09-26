package config

import "fmt"

// CompactNudge is the milestone reminder's policy: whether the hook speaks at
// all, the context percentage it first speaks at, and how many points of
// context pass between reminders. A reminder, never an order — the hook only
// says the milestone is here.
type CompactNudge struct {
	Enabled bool
	Start   int
	Step    int
}

// DefaultCompactNudge is the fleet's milestone policy when the file says
// nothing: on, first at 35% of context, then every 10 points.
func DefaultCompactNudge() CompactNudge {
	return CompactNudge{Enabled: true, Start: 35, Step: 10}
}

// applyCompactNudge overlays the fields a file actually set onto base — the
// resolved top-level policy for an account, the default for the top level —
// so an account that touched only step keeps the file's enabled and start.
func applyCompactNudge(base CompactNudge, raw *rawCompactNudge, path, scope string, index int) (CompactNudge, error) {
	if raw == nil {
		return base, nil
	}
	result := base
	if raw.Enabled != nil {
		result.Enabled = *raw.Enabled
	}
	if raw.Start != nil {
		if *raw.Start < 1 || *raw.Start > 100 {
			return CompactNudge{}, fmt.Errorf(
				"config %s: %s.compactNudge.start must be 1..100 (a context percentage), got %d",
				path,
				configScope(scope, index),
				*raw.Start,
			)
		}
		result.Start = *raw.Start
	}
	if raw.Step != nil {
		if *raw.Step < 1 || *raw.Step > 100 {
			return CompactNudge{}, fmt.Errorf(
				"config %s: %s.compactNudge.step must be 1..100 (context points between reminders), got %d",
				path,
				configScope(scope, index),
				*raw.Step,
			)
		}
		result.Step = *raw.Step
	}
	return result, nil
}

func recordCompactNudgeSources(sources map[string]Source, prefix string, raw *rawCompactNudge) {
	if raw == nil {
		return
	}
	if raw.Enabled != nil {
		sources[prefix+".compactNudge.enabled"] = SourceFile
	}
	if raw.Start != nil {
		sources[prefix+".compactNudge.start"] = SourceFile
	}
	if raw.Step != nil {
		sources[prefix+".compactNudge.step"] = SourceFile
	}
}

type rawCompactNudge struct {
	Enabled *bool `json:"enabled,omitempty"`
	Start   *int  `json:"start,omitempty"`
	Step    *int  `json:"step,omitempty"`
}
