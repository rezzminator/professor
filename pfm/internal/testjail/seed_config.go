package testjail

import (
	"encoding/json"
	"fmt"
	"maps"
)

// seedConfig is every jail home's pfm.config.json: claude.systemPrompt
// production, as no jailed home builds the fleet prompt a professor launch
// injects; a test of the prompt policy writes its own config.
const seedConfig = "{\"version\":2,\"claude\":{\"systemPrompt\":\"production\"}}\n"

// cleanHomeConfig is CleanHome's pfm.config.json: seedConfig plus ask.engine.
const cleanHomeConfig = "{\"version\":2,\"ask\":{\"engine\":\"claude\"},\"claude\":{\"systemPrompt\":\"production\"}}\n"

// seedConfigWith is seedConfig with extra's top-level keys set, as JSON.
func seedConfigWith(extra map[string]any) ([]byte, error) {
	seeded := map[string]any{}
	if err := json.Unmarshal([]byte(seedConfig), &seeded); err != nil {
		return nil, fmt.Errorf("parse the jail seed config %q: %w", seedConfig, err)
	}
	maps.Copy(seeded, extra)
	content, err := json.Marshal(seeded)
	if err != nil {
		return nil, fmt.Errorf("encode the jail seed config with %v: %w", extra, err)
	}
	return content, nil
}
