package callmeter

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// LongField is the size above which a field not on the keep list is stored as
// its byte length.
const LongField = 4096

// droppedFields are never stored: each becomes {name}_bytes.
var droppedFields = map[string]bool{
	"content":    true,
	"old_string": true,
	"new_string": true,
	"edits":      true,
	"new_source": true,
	"prompt":     true,
}

// keptFields are stored whatever their size: file paths, search patterns and
// (for Bash) the command and its description.
var keptFields = map[string]bool{
	"file_path":     true,
	"notebook_path": true,
	"path":          true,
	"pattern":       true,
	"glob":          true,
}

// SanitizeInput is the stored form of a tool input: a JSON object keeping a
// Bash command and description, file paths, search patterns and the Read
// range, with every dropped field (and any other field whose value exceeds
// LongField bytes) replaced by its byte length under {name}_bytes. A string's
// length is its UTF-8 bytes; any other value's is its JSON encoding's.
func SanitizeInput(tool string, raw json.RawMessage) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", fmt.Errorf("sanitize %s input: not a JSON object: %w", tool, err)
	}
	if fields == nil {
		return "", fmt.Errorf("sanitize %s input: input is null", tool)
	}
	out := make(map[string]any, len(fields))
	for name, value := range fields {
		keep := keptFields[name] || (tool == "Bash" && (name == "command" || name == "description"))
		size, err := valueBytes(value)
		if err != nil {
			return "", fmt.Errorf("sanitize %s input field %q: %w", tool, name, err)
		}
		if droppedFields[name] || (!keep && size > LongField) {
			out[name+"_bytes"] = size
			continue
		}
		out[name] = value
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("sanitize %s input: encode: %w", tool, err)
	}
	return string(encoded), nil
}

func valueBytes(value json.RawMessage) (int, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return 0, fmt.Errorf("decode string: %w", err)
		}
		return len(s), nil
	}
	return len(trimmed), nil
}
