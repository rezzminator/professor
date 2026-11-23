package run

import (
	"encoding/json"
	"os"
)

// openCodeAssistantMarker is the file the OpenCode plugin writes once the
// run's assistant messages are known, or its error.
type openCodeAssistantMarker struct {
	SessionID     string          `json:"sessionID"`
	UserMessageID string          `json:"userMessageID"`
	AssistantIDs  []string        `json:"assistantIDs"`
	Error         json.RawMessage `json:"error"`
}

// openCodeMarkerLooked is the test seam of the marker wait: nil in
// production, else called after each read of the marker with its error.
var openCodeMarkerLooked func(markerErr error)

// readOpenCodeMarker is one look of the marker wait: the marker's bytes, or
// the read's error (os.ErrNotExist while the plugin has written none).
func readOpenCodeMarker(path string) ([]byte, error) {
	body, err := os.ReadFile(path)
	if openCodeMarkerLooked != nil {
		openCodeMarkerLooked(err)
	}
	return body, err
}
