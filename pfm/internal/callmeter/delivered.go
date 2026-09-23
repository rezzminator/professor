package callmeter

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// DeliveredBytes is the byte length of a tool result's content as the model
// received it: a string's UTF-8 bytes, or the summed text of a content-block
// array's "text" blocks (a non-text block contributes nothing — the model
// never read it as text). Absent, empty or null content is 0 bytes. Any other
// JSON shape (an object, a number, a boolean, malformed JSON) cannot be
// measured this way and is an error naming the shape, never a silent 0 or a
// guess at the raw encoding's length.
func DeliveredBytes(raw json.RawMessage) (int64, error) {
	trimmed := bytes.TrimSpace(raw)
	switch {
	case len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")):
		return 0, nil
	case trimmed[0] == '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return 0, fmt.Errorf("decode tool result text: %w", err)
		}
		return int64(len(text)), nil
	case trimmed[0] == '[':
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(trimmed, &blocks); err != nil {
			return 0, fmt.Errorf("decode tool result content blocks: %w", err)
		}
		var size int64
		for _, block := range blocks {
			if block.Type == "text" {
				size += int64(len(block.Text))
			}
		}
		return size, nil
	default:
		return 0, fmt.Errorf("unmeasurable tool result shape: %s", shapeName(trimmed))
	}
}

// shapeName names the JSON shape at trimmed's first byte, for an error that
// tells a reader what it could not measure.
func shapeName(trimmed []byte) string {
	switch trimmed[0] {
	case '{':
		return "object"
	case 't', 'f':
		return "boolean"
	default:
		if trimmed[0] == '-' || (trimmed[0] >= '0' && trimmed[0] <= '9') {
			return "number"
		}
		return "unrecognized JSON"
	}
}
