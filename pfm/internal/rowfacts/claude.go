package rowfacts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// claudeScanWidths are the tail sizes tried in turn. The picker's first refresh
// reads every row's transcript, so the first read is sized to what nearly all of
// them need: measured over 400 real transcripts, the newest decisive record
// sat within 16 KiB of the end for four in five, and within 256 KiB for all.
// A turn that finishes on a very large tool result takes the wider reads.
var claudeScanWidths = []int64{16 << 10, 256 << 10, 2 << 20}

// claudeRecord is the part of a transcript line the facts come from.
type claudeRecord struct {
	Message *struct {
		Model      string          `json:"model"`
		StopReason *string         `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

var (
	assistantMark = []byte(`"type":"assistant"`)
	userMark      = []byte(`"type":"user"`)
)

// finishedStops are the stop reasons that end a Claude turn; any other reason
// (tool_use) or none at all (a message still streaming) leaves the turn open.
var finishedStops = map[string]bool{"end_turn": true, "stop_sequence": true, "max_tokens": true, "refusal": true}

// claudeTurn decides from the newest assistant or user record whether a turn is
// open. A user record closes it only when it is Claude's own marker for a turn
// that ended without an answer — an interrupt, a local slash command — and
// otherwise (a prompt, a tool result) the assistant owes an answer.
func claudeTurn(line []byte, record claudeRecord, isAssistant bool) (working bool) {
	if record.Message == nil {
		return false
	}
	if isAssistant {
		return record.Message.StopReason == nil || !finishedStops[*record.Message.StopReason]
	}
	content := bytes.TrimSpace(record.Message.Content)
	switch {
	case bytes.HasPrefix(content, []byte(`"<command-name>`)),
		bytes.HasPrefix(content, []byte(`"<local-command`)),
		bytes.Contains(line, []byte(`[Request interrupted by user`)):
		return false
	}
	return true
}

// scanClaude walks a tail from its end: the newest assistant or user record
// decides whether the chat is mid-turn, and the newest assistant record that
// names a real model names the model. It reports whether both are settled.
func scanClaude(data []byte, tail *cachedFile, decided *bool) (settled bool) {
	linesBackward(data, func(line []byte) bool {
		isAssistant := bytes.Contains(line, assistantMark)
		if !isAssistant && !bytes.Contains(line, userMark) {
			return false
		}
		var record claudeRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return false // a line cut by a crash says nothing; the one before it will
		}
		if !*decided {
			tail.working = claudeTurn(line, record, isAssistant)
			*decided = true
		}
		if tail.model == "" && isAssistant && record.Message != nil &&
			record.Message.Model != "" && record.Message.Model != "<synthetic>" {
			tail.model = record.Message.Model
		}
		return *decided && tail.model != ""
	})
	return *decided && tail.model != ""
}

// readClaudeTail reads one Claude transcript's tail facts.
func readClaudeTail(path string) (cachedFile, error) {
	var tail cachedFile
	decided := false
	err := scanTail(path, claudeScanWidths, func(data []byte) bool { return scanClaude(data, &tail, &decided) })
	if err != nil {
		return cachedFile{}, err
	}
	return tail, nil
}

// claudeFacts is a Claude row's facts. The statusline's record is the live
// model and effort when it left one; the transcript names the model otherwise.
// Only a live seat can be working, and only a live seat has agents at work.
func (reader *Reader) claudeFacts(row *compose.Row, nowNS int64) (Facts, error) {
	tail, err := reader.load(row.Path, readClaudeTail)
	if err != nil {
		return Facts{}, err
	}
	record, recordErr := reader.sessionRecord(row.ID)
	facts := Facts{Model: tail.model, Effort: record.Level}
	if record.Model != "" {
		facts.Model = record.Model
	}
	if recordErr != nil {
		err = recordErr
	}
	if !row.Kind.IsLiveSeat() {
		return facts, err
	}
	facts.Working = tail.working && nowNS-tail.modNS <= workingFreshNS
	agents, agentsErr := reader.workingAgents(row.Path, nowNS)
	facts.AgentsWorking = agents
	if agentsErr != nil && err == nil {
		err = agentsErr
	}
	return facts, err
}

// workingAgents counts the sub-agents of a Claude chat that are mid-turn: its
// transcript's subagents directory holds one file per agent the session ever
// ran, and an agent is working when its own transcript says its turn is open
// and it wrote within agentFreshNS.
func (reader *Reader) workingAgents(transcript string, nowNS int64) (int, error) {
	dir := strings.TrimSuffix(transcript, filepath.Ext(transcript)) + string(filepath.Separator) + "subagents"
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("list sub-agents in %s: %w", dir, err)
	}
	working, examined := 0, 0
	var firstErr error
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("stat sub-agent %s: %w", name, err)
			}
			continue
		}
		if nowNS-info.ModTime().UnixNano() > agentFreshNS {
			continue
		}
		examined++
		if examined > maxAgents {
			break
		}
		tail, err := reader.load(filepath.Join(dir, name), readClaudeTail)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if tail.working {
			working++
		}
	}
	return working, firstErr
}
