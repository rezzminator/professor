package callmeter

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

// RequestUsage is the model request that issued one tool call, as the
// transcript records it.
type RequestUsage struct {
	MessageID     string
	TS            int64 // the entry's timestamp, Unix ms UTC
	ContextTokens int64 // input + cache read + cache creation tokens
	OutputTokens  int64
}

// SubagentTranscriptPath is the transcript of sub-agent agentID of the chat
// whose transcript is transcriptPath.
func SubagentTranscriptPath(transcriptPath, agentID string) string {
	return filepath.Join(strings.TrimSuffix(transcriptPath, ".jsonl"), "subagents", "agent-"+agentID+".jsonl")
}

// ConfigDirOf is the Claude config dir a transcript belongs to: the nearest
// ancestor directory holding projects/, symlinks resolved; "" when no ancestor
// is named projects. A config dir that cannot be resolved (no longer on disk)
// is returned cleaned but unresolved.
func ConfigDirOf(transcriptPath string) string {
	dir := filepath.Dir(filepath.Clean(transcriptPath))
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		if filepath.Base(dir) == "projects" {
			return ProjectsHome(parent)
		}
		dir = parent
	}
}

// ProjectsHome is the config dir whose projects/ physically holds dir's
// transcripts: dir/projects resolved through symlinks, then its parent.
// Accounts that share one projects/ (a symlink, one file per chat) are one
// config dir, so a chat keeps one history and one set of metrics whichever
// account runs it — the hook and a report's --config-dir both name
// the same dir. With no resolvable projects/ it is dir resolved, or dir
// cleaned when dir is not on disk.
func ProjectsHome(dir string) string {
	dir = filepath.Clean(dir)
	if projects, err := filepath.EvalSymlinks(filepath.Join(dir, "projects")); err == nil {
		return filepath.Dir(projects)
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

type transcriptEntry struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

type assistantMessage struct {
	timestamp  string          // its entry's, set by agentMessage
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	StopReason *string         `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
	Usage      *struct {
		InputTokens              int64 `json:"input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
	} `json:"usage"`
}

// toolUse is both a content block type and the stop reason of a message that
// calls tools: a message that stops with it is never an agent's last.
const toolUse = "tool_use"

type contentBlock struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// requestTailWindow is the transcript tail FindRequests reads first; each miss
// widens it fourfold until the whole file is read.
const requestTailWindow = 1 << 20

// FindRequests scans the transcript at path for the assistant entries whose
// content holds a tool_use block with one of toolUseIDs, and returns each
// found id's request. The hook asks for the request it just saw, at the end of
// a transcript that grows to tens of MB, so the scan reads the last MB first
// and widens only while an id is missing; a line is decoded only when it
// mentions a wanted id. An id not yet on disk is absent from the map; a file
// that cannot be read, or a malformed line holding a wanted id, is an error
// naming the path and the line's byte offset. A final line with no newline
// that does not parse is a writer mid-append and is skipped.
func FindRequests(path string, toolUseIDs []string) (found map[string]RequestUsage, err error) {
	found = map[string]RequestUsage{}
	wanted := map[string]bool{}
	for _, id := range toolUseIDs {
		wanted[id] = true
	}
	if len(wanted) == 0 {
		return found, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close transcript %s: %w", path, closeErr))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat transcript %s: %w", path, err)
	}
	size := info.Size()
	for window := int64(requestTailWindow); ; window *= 4 {
		start := max(0, size-window)
		found = map[string]RequestUsage{}
		if err := scanRequests(file, path, start, size, wanted, found); err != nil {
			return nil, err
		}
		if len(found) == len(wanted) || start == 0 {
			return found, nil
		}
	}
}

// scanRequests reads [start, end) of the transcript line by line; a start
// inside the file drops the first, partial line.
func scanRequests(
	file *os.File,
	path string,
	start, end int64,
	wanted map[string]bool,
	found map[string]RequestUsage,
) error {
	reader := bufio.NewReaderSize(io.NewSectionReader(file, start, end-start), 1<<16)
	offset := start
	if start > 0 {
		skipped, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read transcript %s at byte %d: %w", path, offset, err)
		}
		offset += int64(len(skipped))
	}
	for len(found) < len(wanted) {
		raw, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("read transcript %s at byte %d: %w", path, offset, readErr)
		}
		partial := errors.Is(readErr, io.EOF)
		if mentionsWanted(raw, wanted, found) {
			if err := scanEntry(raw, wanted, found); err != nil && !partial {
				return fmt.Errorf("transcript %s at byte %d: %w", path, offset, err)
			}
		}
		offset += int64(len(raw))
		if partial {
			return nil
		}
	}
	return nil
}

// mentionsWanted is the cheap pre-check before a line is decoded: whether it
// holds any wanted id not found yet.
func mentionsWanted(raw []byte, wanted map[string]bool, found map[string]RequestUsage) bool {
	for id := range wanted {
		if _, done := found[id]; !done && bytes.Contains(raw, []byte(id)) {
			return true
		}
	}
	return false
}

func scanEntry(raw []byte, wanted map[string]bool, found map[string]RequestUsage) error {
	var entry transcriptEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return fmt.Errorf("malformed entry: %w", err)
	}
	if entry.Type != "assistant" || len(entry.Message) == 0 {
		return nil
	}
	var message assistantMessage
	if err := json.Unmarshal(entry.Message, &message); err != nil {
		return fmt.Errorf("malformed assistant message: %w", err)
	}
	var blocks []contentBlock
	if err := json.Unmarshal(message.Content, &blocks); err != nil {
		return fmt.Errorf("assistant message %q: content is not a block list: %w", message.ID, err)
	}
	for _, block := range blocks {
		if block.Type != toolUse || !wanted[block.ID] {
			continue
		}
		if _, done := found[block.ID]; done {
			continue
		}
		if message.Usage == nil {
			return fmt.Errorf("assistant message %q holding %s has no usage", message.ID, block.ID)
		}
		at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if err != nil {
			return fmt.Errorf("assistant message %q: timestamp %q: %w", message.ID, entry.Timestamp, err)
		}
		found[block.ID] = RequestUsage{
			MessageID:     message.ID,
			TS:            at.UnixMilli(),
			ContextTokens: message.Usage.InputTokens + message.Usage.CacheReadInputTokens + message.Usage.CacheCreationInputTokens,
			OutputTokens:  message.Usage.OutputTokens,
		}
	}
	return nil
}

// AgentTotals is what a sub-agent's own transcript says it spent.
type AgentTotals struct {
	TotalTokens int64  // over distinct assistant message ids: input + cache read + cache creation + output
	ToolUses    int64  // distinct tool_use blocks
	Model       string // the last model its messages name; "" when none does
	// Final: the last assistant entry ends the agent's turn (a stop_reason
	// set and not tool_use), so the transcript holds the turn's last message.
	Final bool
	// LastTimestamp is the last assistant entry's timestamp as written (RFC
	// 3339): the agent's stop when Final.
	LastTimestamp string
}

// ReadAgentTotals sums the sub-agent transcript at path. One model message is
// split across several entries sharing its id, each repeating its usage, so a
// message's usage counts once (its last entry's, the final count). A file that
// cannot be read, a malformed line or an assistant message with no id is an
// error naming the path and line; a final line with no newline that does not
// parse is a writer mid-append and is skipped.
func ReadAgentTotals(path string) (AgentTotals, error) {
	var totals AgentTotals
	file, err := os.Open(path)
	if err != nil {
		return totals, fmt.Errorf("read agent transcript: %w", err)
	}
	usage := map[string]int64{}
	var order []string
	toolUses := map[string]bool{}
	reader := bufio.NewReaderSize(file, 1<<16)
	for line := 1; ; line++ {
		raw, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return totals, errors.Join(
				fmt.Errorf("read agent transcript %s line %d: %w", path, line, readErr),
				file.Close(),
			)
		}
		partial := errors.Is(readErr, io.EOF)
		if strings.TrimSpace(string(raw)) != "" {
			message, err := agentMessage(raw)
			switch {
			case err != nil && !partial:
				return totals, errors.Join(fmt.Errorf("agent transcript %s line %d: %w", path, line, err), file.Close())
			case err == nil && message != nil:
				if _, seen := usage[message.ID]; !seen {
					order = append(order, message.ID)
				}
				if message.Usage != nil {
					u := message.Usage
					usage[message.ID] = u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens + u.OutputTokens
				}
				if message.Model != "" && message.Model != "<synthetic>" {
					totals.Model = message.Model
				}
				totals.Final = message.StopReason != nil && *message.StopReason != toolUse
				totals.LastTimestamp = message.timestamp
				var blocks []contentBlock
				if err := json.Unmarshal(message.Content, &blocks); err != nil {
					return totals, errors.Join(
						fmt.Errorf("agent transcript %s line %d: message %q content is not a block list: %w",
							path, line, message.ID, err),
						file.Close(),
					)
				}
				for _, block := range blocks {
					if block.Type == toolUse && block.ID != "" {
						toolUses[block.ID] = true
					}
				}
			}
		}
		if partial {
			break
		}
	}
	if err := file.Close(); err != nil {
		return totals, fmt.Errorf("close agent transcript %s: %w", path, err)
	}
	for _, id := range order {
		totals.TotalTokens += usage[id]
	}
	totals.ToolUses = int64(len(toolUses))
	return totals, nil
}

// agentMessage decodes one transcript line; nil, nil when it is not an
// assistant message.
func agentMessage(raw []byte) (*assistantMessage, error) {
	var entry transcriptEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, fmt.Errorf("malformed entry: %w", err)
	}
	if entry.Type != "assistant" || len(entry.Message) == 0 {
		return nil, nil
	}
	var message assistantMessage
	if err := json.Unmarshal(entry.Message, &message); err != nil {
		return nil, fmt.Errorf("malformed assistant message: %w", err)
	}
	if message.ID == "" {
		return nil, errors.New("assistant message carries no id")
	}
	message.timestamp = entry.Timestamp
	return &message, nil
}
