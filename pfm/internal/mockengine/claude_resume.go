package mockengine

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const claudeCustomTitle = "custom-title"

func latestClaudeSession(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read continued sessions %s: %w", directory, err)
	}
	var latest string
	var modified time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return "", fmt.Errorf("stat continued session %s: %w", path, err)
		}
		if !info.ModTime().After(modified) {
			continue
		}
		eligible, err := claudeContinueEligible(path)
		if err != nil {
			return "", err
		}
		if eligible {
			latest, modified = strings.TrimSuffix(entry.Name(), ".jsonl"), info.ModTime()
		}
	}
	if latest == "" {
		return "", errors.New("No conversation found to continue") //nolint:staticcheck // Native Claude wording.
	}
	return latest, nil
}

func claudeContinueEligible(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("read continued session %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	for {
		var record claudeRecord
		if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
			return false, nil
		} else if err != nil {
			return false, fmt.Errorf("decode continued session %s: %w", path, err)
		}
		if record.Type != roleUser || record.IsSidechain || record.IsMeta || record.Message == nil {
			continue
		}
		prompt, ok := record.Message.Content.(string)
		if !ok {
			continue
		}
		word, _, _ := strings.Cut(strings.TrimSpace(prompt), " ")
		return record.PromptSource != "sdk" && !strings.HasPrefix(record.Entrypoint, "sdk-") && word != "/loop", nil
	}
}

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
