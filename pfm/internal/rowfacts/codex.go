package rowfacts

import (
	"bytes"
	"encoding/json"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// codexScanWidths are the tail sizes tried in turn. The model and effort live
// in a turn's turn_context record, which can sit before a long run of tool
// output, so the last width is generous.
var codexScanWidths = []int64{256 << 10, 2 << 20, 8 << 20}

// codexRecord is the part of a rollout line the facts come from.
type codexRecord struct {
	Type    string `json:"type"`
	Payload struct {
		Type   string `json:"type"`
		Model  string `json:"model"`
		Effort string `json:"effort"`
	} `json:"payload"`
}

var (
	turnContextMark = []byte(`"turn_context"`)
	turnMarks       = [][]byte{
		[]byte(`"task_started"`), []byte(`"task_complete"`), []byte(`"turn_aborted"`), []byte(`"user_message"`),
	}
)

func hasAnyMark(line []byte, marks ...[]byte) bool {
	for _, mark := range marks {
		if bytes.Contains(line, mark) {
			return true
		}
	}
	return false
}

// scanCodex walks a rollout tail from its end: the newest turn event says
// whether a turn is open (a started turn or a user message is; a completed or
// aborted one is not) and the newest turn_context names the model and effort.
func scanCodex(data []byte, tail *cachedFile, decided *bool) (settled bool) {
	linesBackward(data, func(line []byte) bool {
		wantContext := tail.model == "" && bytes.Contains(line, turnContextMark)
		wantTurn := !*decided && hasAnyMark(line, turnMarks...)
		if !wantContext && !wantTurn {
			return false
		}
		var record codexRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return false
		}
		if wantContext && record.Type == "turn_context" && record.Payload.Model != "" {
			tail.model, tail.effort = record.Payload.Model, record.Payload.Effort
		}
		if wantTurn && record.Type == "event_msg" {
			switch record.Payload.Type {
			case "task_started", "user_message":
				tail.working, *decided = true, true
			case "task_complete", "turn_aborted":
				tail.working, *decided = false, true
			}
		}
		return *decided && tail.model != ""
	})
	return *decided && tail.model != ""
}

// readCodexTail reads one rollout's tail facts.
func readCodexTail(path string) (cachedFile, error) {
	var tail cachedFile
	decided := false
	err := scanTail(path, codexScanWidths, func(data []byte) bool { return scanCodex(data, &tail, &decided) })
	if err != nil {
		return cachedFile{}, err
	}
	return tail, nil
}

// codexFacts is a Codex row's facts. A Codex sub-agent's thread is folded into
// its parent's row by the fleet, so no agent count is read here: a Codex chat
// shows its own turn, never a gauge of agents.
func (reader *Reader) codexFacts(row *compose.Row, nowNS int64) (Facts, error) {
	tail, err := reader.load(row.Path, readCodexTail)
	if err != nil {
		return Facts{}, err
	}
	facts := Facts{Model: tail.model, Effort: tail.effort}
	facts.Working = row.Kind.IsLiveSeat() && tail.working && nowNS-tail.modNS <= workingFreshNS
	return facts, nil
}
