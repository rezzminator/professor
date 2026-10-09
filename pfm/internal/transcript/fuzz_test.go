package transcript

import (
	"encoding/json"
	"testing"
	"unicode/utf8"
)

func FuzzFlattenPromptText(f *testing.F) {
	seeds := [][]byte{
		[]byte(`"plain string"`),
		[]byte("\"line one\\n\\tline two\""),
		[]byte(`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`),
		[]byte(`[{"type":"tool_result","content":[{"type":"text","text":"secret"}]}]`),
		[]byte(`[{"type":"container","content":[{"type":"text","text":"nested"}]}]`),
		[]byte(`[["deeply",["nested"]],{"type":"text","text":"top"}]`),
		[]byte(`{"type":"text","text":"object, not array"}`),
		[]byte(`null`),
		[]byte(`[`),
		{'"', 0xff, 0xfe, '"'},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		output := FlattenPromptText(json.RawMessage(input))
		if !utf8.ValidString(output) {
			t.Fatalf("FlattenPromptText returned invalid UTF-8: %x", output)
		}
	})
}
