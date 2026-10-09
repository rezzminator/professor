package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code keeps a session's sub-agents beside its transcript:
// <session>.jsonl, then <session>/subagents/agent-<id>.jsonl with its
// agent-<id>.meta.json; a workflow nests further down the same directory
// (subagents/workflows/wf_*/agent-<id>.jsonl).
const (
	subagentsDirName = "subagents"
	subagentPrefix   = "agent-"
	transcriptExt    = ".jsonl"
	subagentMetaExt  = ".meta.json"
)

// SubagentMeta is a sub-agent's meta file: its type, the description it was
// spawned with, its spawn depth and the agent that spawned it (empty for the
// session's own).
type SubagentMeta struct {
	AgentType     string `json:"agentType"`
	Description   string `json:"description"`
	SpawnDepth    int    `json:"spawnDepth"`
	ParentAgentID string `json:"parentAgentId"`
}

// SubagentsDir is the directory holding a session's sub-agent transcripts.
func SubagentsDir(sessionTranscript string) string {
	return filepath.Join(strings.TrimSuffix(sessionTranscript, transcriptExt), subagentsDirName)
}

// SubagentTranscript is agent id's transcript in a subagents directory.
func SubagentTranscript(dir, id string) string {
	return filepath.Join(dir, subagentPrefix+id+transcriptExt)
}

// SubagentMetaPath is the meta file beside a sub-agent transcript.
func SubagentMetaPath(transcriptPath string) string {
	return strings.TrimSuffix(transcriptPath, transcriptExt) + subagentMetaExt
}

// SubagentID is the agent id a subagents-directory entry's name carries when
// it is a transcript (agent-<id>.jsonl).
func SubagentID(name string) (string, bool) {
	return subagentNameID(name, transcriptExt)
}

// SubagentMetaID is the agent id a meta file's name carries
// (agent-<id>.meta.json).
func SubagentMetaID(name string) (string, bool) {
	return subagentNameID(name, subagentMetaExt)
}

func subagentNameID(name, ext string) (string, bool) {
	if !strings.HasPrefix(name, subagentPrefix) || !strings.HasSuffix(name, ext) {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(name, subagentPrefix), ext), true
}

// IsSubagentTranscript reports a sub-agent's transcript: an agent-<id>.jsonl
// inside a subagents directory, at any depth below it. A directory named
// subagents above the transcript root makes no main chat an agent.
func IsSubagentTranscript(path string) bool {
	_, named := SubagentID(filepath.Base(path))
	_, inside := SessionDir(path)
	return named && inside
}

// SessionDir is the session directory a transcript inside a subagents
// directory sits under: the directory holding the nearest subagents
// directory above it. ok is false outside any subagents directory.
func SessionDir(path string) (dir string, ok bool) {
	sep := string(filepath.Separator)
	at := strings.LastIndex(path, sep+subagentsDirName+sep)
	if at < 0 {
		return "", false
	}
	if at == 0 {
		return sep, true
	}
	return path[:at], true
}

// TranscriptStem is a transcript's file name without its extension.
func TranscriptStem(path string) string {
	return strings.TrimSuffix(filepath.Base(path), transcriptExt)
}

// TranscriptSession is the session a transcript belongs to: its session
// directory's name for a sub-agent, else its own stem.
func TranscriptSession(path string) string {
	if IsSubagentTranscript(path) {
		dir, _ := SessionDir(path)
		return filepath.Base(dir)
	}
	return TranscriptStem(path)
}

// ReadSubagentMeta reads one meta file; a missing one is an error wrapping
// fs.ErrNotExist.
func ReadSubagentMeta(path string) (SubagentMeta, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SubagentMeta{}, fmt.Errorf("read sub-agent meta %s: %w", path, err)
	}
	var meta SubagentMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return SubagentMeta{}, fmt.Errorf("parse sub-agent meta %s: %w", path, err)
	}
	return meta, nil
}
