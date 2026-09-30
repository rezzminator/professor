package mockengine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const claudeCustomTitle = "custom-title"

func claudeTranscriptTitle(path string) (string, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read transcript %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	var custom, ai string
	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		var record struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
			AITitle     string `json:"aiTitle"`
		}
		if json.Unmarshal(line, &record) == nil {
			switch record.Type {
			case claudeCustomTitle:
				custom = record.CustomTitle
			case "ai-title":
				ai = record.AITitle
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("read transcript %s: %w", path, readErr)
		}
	}
	if custom != "" {
		return custom, nil
	}
	return ai, nil
}

func seedClaudeFork(parent, child, sessionID string) error {
	if parent == child {
		return fmt.Errorf("fork transcript %s equals parent", parent)
	}
	data, err := os.ReadFile(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read transcript %s: %w", parent, err)
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var record map[string]json.RawMessage
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("decode transcript %s: %w", parent, err)
		}
		if string(record["type"]) == `"`+claudeCustomTitle+`"` {
			continue
		}
		record["sessionId"], _ = json.Marshal(sessionID)
		encoded, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("encode fork of %s: %w", parent, err)
		}
		if err := appendLine(child, encoded); err != nil {
			return fmt.Errorf("seed fork %s: %w", child, err)
		}
	}
	return nil
}
