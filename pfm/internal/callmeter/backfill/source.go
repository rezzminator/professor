package backfill

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// source is one transcript file and what its path says about its rows.
type source struct {
	path      string
	configDir string
	session   string // the file's own session, when its entries carry none
	agentID   string // "" for a chat transcript
	agentType *string
}

// subagentMeta reads the agent type from the agent-{id}.meta.json beside a
// sub-agent transcript: nil with no error when the file is absent.
func subagentMeta(transcript string) (*string, error) {
	path := strings.TrimSuffix(transcript, ".jsonl") + ".meta.json"
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var meta struct {
		AgentType string `json:"agentType"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return nonEmptyPtr(meta.AgentType), nil
}

// sourceOf names what a transcript's path says: a subagents/agent-{id}.jsonl
// is a sub-agent of the session whose directory holds it.
func sourceOf(path string) source {
	src := source{path: path, configDir: callmeter.ConfigDirOf(path)}
	name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	dir := filepath.Dir(path)
	if id, ok := strings.CutPrefix(name, "agent-"); ok && filepath.Base(dir) == "subagents" {
		src.agentID = id
		src.session = filepath.Base(filepath.Dir(dir))
		return src
	}
	src.session = name
	return src
}

func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
