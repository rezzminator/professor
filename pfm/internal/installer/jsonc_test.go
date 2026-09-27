package installer

import (
	"testing"
)

// TestSanitizeJSONCToleratesConsecutiveTrailingCommas covers the one gap a
// single-comma lookahead leaves: two (or more) trailing commas in a row —
// two edits landing on the same spot, or a block moved and re-punctuated —
// where the first comma is followed by another comma, not directly by
// whitespace-then-bracket.
func TestSanitizeJSONCToleratesConsecutiveTrailingCommas(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"double trailing comma in a nested object":  `{"a": {"x": 1,,},"b":2}`,
		"triple trailing comma in a nested object":  `{"a": {"x": 1,,,},"b":2}`,
		"double trailing comma in an array":         `{"a": [1, 2,,],"b":2}`,
		"double trailing comma spread across lines": "{\"a\": {\"x\": 1,\n  ,\n  },\"b\":2}",
	} {
		t.Run(name, func(t *testing.T) {
			document, err := decodeJSONCObject([]byte(content))
			if err != nil {
				t.Fatalf("decodeJSONCObject: %v", err)
			}
			if _, ok := document["b"].(float64); !ok {
				t.Fatalf("decoded document missing sibling key: %#v", document)
			}
			if _, err := parseJSONCObject([]byte(content), 0); err != nil {
				t.Fatalf("parseJSONCObject: %v", err)
			}
		})
	}
}
