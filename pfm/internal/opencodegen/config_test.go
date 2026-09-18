package opencodegen

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSerializeConfigKeepsUnownedKey(t *testing.T) {
	raw := json.RawMessage(`"fixture"`)
	got := serializeOpenCodeConfig(map[string]json.RawMessage{"model": raw}, nil, false)
	if !strings.Contains(got, `"model": "fixture"`) {
		t.Fatalf("config dropped unowned key: %s", got)
	}
}
