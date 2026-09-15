package seat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type rolloutTurnState string

const (
	rolloutTurnPending  rolloutTurnState = "pending"
	rolloutTurnComplete rolloutTurnState = "complete"
	rolloutTurnAborted  rolloutTurnState = "aborted"
)

type rolloutEvent struct {
	Type    string `json:"type"`
	Payload struct {
		Type             string `json:"type"`
		TurnID           string `json:"turn_id"`
		Model            string `json:"model"`
		Effort           string `json:"effort"`
		LastAgentMessage string `json:"last_agent_message"`
	} `json:"payload"`
}

type rolloutTurnEvidence struct {
	State   rolloutTurnState
	Started bool
	Model   string
	Effort  string
}

// inspectRolloutTurn uses Codex's explicit turn lifecycle rather than the
// latest visible assistant message. Commentary such as "I'll inspect that"
// is an assistant record too, but it is not a completed seat; task_complete
// is the durable boundary written after the final answer.
func inspectRolloutTurn(ctx context.Context, path string) (rolloutTurnState, error) {
	evidence, err := inspectRolloutTurnEvidence(ctx, path)
	return evidence.State, err
}

// inspectRolloutTurnEvidence exposes the task_started boundary separately
// from a still-pending turn. That distinction is the durable proof that an
// Enter actually submitted a Dream brief; pane pixels cannot prove it once a
// large composer has scrolled its first line outside the capture viewport.
func inspectRolloutTurnEvidence(
	ctx context.Context,
	path string,
) (evidence rolloutTurnEvidence, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return rolloutTurnEvidence{State: rolloutTurnPending}, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close Codex rollout %s: %w", path, err))
		}
	}()

	reader := bufio.NewReaderSize(file, 64<<10)
	activeTurn := ""
	evidence = rolloutTurnEvidence{State: rolloutTurnPending}
	for {
		if err := ctx.Err(); err != nil {
			return evidence, err
		}
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 && !errors.Is(readErr, io.EOF) {
			var event rolloutEvent
			if err := json.Unmarshal(line, &event); err != nil {
				return evidence, fmt.Errorf("parse Codex rollout lifecycle: %w", err)
			}
			if event.Type == "event_msg" {
				switch event.Payload.Type {
				case "task_started":
					if event.Payload.TurnID == "" {
						return evidence, errors.New("Codex task_started event has no turn_id")
					}
					activeTurn = event.Payload.TurnID
					evidence.Started = true
					evidence.State = rolloutTurnPending
					evidence.Model = ""
					evidence.Effort = ""
				case "task_complete":
					if activeTurn != "" && event.Payload.TurnID == activeTurn {
						if strings.TrimSpace(event.Payload.LastAgentMessage) == "" {
							return evidence, errors.New("Codex task_complete event has no final assistant message")
						}
						evidence.State = rolloutTurnComplete
					}
				case "turn_aborted":
					if activeTurn != "" && event.Payload.TurnID == activeTurn {
						evidence.State = rolloutTurnAborted
					}
				}
			}
			// Current Codex rollouts state the effective model and reasoning
			// effort on turn_context, not on the final assistant record. Bind
			// those facts to the exact task_started turn so an unrelated or
			// older turn cannot satisfy the seat law. This scanner deliberately
			// walks the whole file: long seats routinely push turn_context more
			// than the generic transcript metadata tail-window from the end.
			if event.Type == "turn_context" && activeTurn != "" &&
				event.Payload.TurnID == activeTurn {
				evidence.Model = event.Payload.Model
				evidence.Effort = event.Payload.Effort
			}
		}
		if errors.Is(readErr, io.EOF) {
			// Ignore an unterminated tail: the live writer may be between bytes.
			return evidence, nil
		}
		if readErr != nil {
			return evidence, readErr
		}
	}
}
