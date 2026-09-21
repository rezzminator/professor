package index

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/store"
)

type codexRecord struct {
	Type           string          `json:"type"`
	ID             string          `json:"id"`
	CWD            string          `json:"cwd"`
	SessionID      string          `json:"session_id"`
	ParentThread   string          `json:"parent_thread"`
	ParentThreadID string          `json:"parent_thread_id"`
	ThreadSource   string          `json:"thread_source"`
	Payload        codexPayload    `json:"payload"`
	Message        json.RawMessage `json:"message"`
}

type codexPayload struct {
	Type           string          `json:"type"`
	ID             string          `json:"id"`
	CWD            string          `json:"cwd"`
	SessionID      string          `json:"session_id"`
	ParentThread   string          `json:"parent_thread"`
	ParentThreadID string          `json:"parent_thread_id"`
	ThreadSource   string          `json:"thread_source"`
	Message        json.RawMessage `json:"message"`
	Role           string          `json:"role"`
	Content        json.RawMessage `json:"content"`
}

func parseCodexRolloutFile(
	file diskFile,
	start int64,
	rollout store.Rollout,
) (store.Rollout, int64, error) {
	// A delta starts from a previously classified row. On a full parse the
	// session_meta line normally establishes the source before messages, but
	// the fallback remains user for older files without thread_source.
	sourceKnown := start > 0
	parsedOffset, bytesRead, err := readCompleteLines(file.Path, start, func(line []byte) {
		if !relevantCodexLine(line) {
			return
		}

		var record codexRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return
		}
		if value := firstNonEmptyCodexField(record.CWD, record.Payload.CWD); value != "" {
			rollout.CWD = value
		}
		if value := firstNonEmptyCodexField(record.SessionID, record.Payload.SessionID); value != "" {
			rollout.SessionID = value
		}
		if value := firstNonEmptyCodexField(
			record.ParentThread,
			record.ParentThreadID,
			record.Payload.ParentThread,
			record.Payload.ParentThreadID,
		); value != "" {
			rollout.ParentThread = value
			// Newer Codex fork/subagent metadata may encode source as an
			// object instead of thread_source. A parent thread is independent
			// proof that this is not the top-level interactive rollout. An
			// explicit thread_source:user below still wins for older schemas
			// that legitimately carry both fields.
			rollout.UserThread = false
			sourceKnown = true
		}
		if source := firstNonEmptyCodexField(record.ThreadSource, record.Payload.ThreadSource); source != "" {
			rollout.UserThread = source == messageRoleUser
			sourceKnown = true
		}

		if record.Type == "session_meta" {
			if header, err := codexmeta.Decode(line); err == nil && header.Kind != codexmeta.Unknown {
				rollout.UserThread = header.Kind == codexmeta.User
				sourceKnown = true
				if header.ParentThreadID != "" {
					rollout.ParentThread = header.ParentThreadID
				}
			}
		}

		// A submitted prompt is the response_item user message — the record
		// every Codex era writes. The user_message event is extinct in current
		// rollouts (app-server threads never wrote it; the TUI stopped by
		// 0.14x), and where it does appear it pairs with a response_item, so
		// counting it too would double-count. Queued injects delivered
		// mid-turn write ONLY the response_item.
		if record.Type == "response_item" && record.Payload.Type == "message" &&
			record.Payload.Role == messageRoleUser {
			prompt := naming.FlattenPromptText(record.Payload.Content)
			if prompt == "" || protocolPrompt(prompt) {
				return
			}
			if !sourceKnown {
				rollout.UserThread = true
			}
			rollout.PromptCount++
			if rollout.FirstPrompt == "" {
				rollout.FirstPrompt = prompt
			}
			return
		}
		if record.Type != "user_message" && record.Payload.Type != "user_message" {
			return
		}
		// Legacy user_message event: seeds the name for eras whose
		// response_items predate the block shapes FlattenPromptText reads;
		// the count is taken from the paired response_item above.
		if !sourceKnown {
			rollout.UserThread = true
		}
		message := record.Payload.Message
		if len(message) == 0 {
			message = record.Message
		}
		if prompt := naming.FlattenPromptText(message); rollout.FirstPrompt == "" && prompt != "" {
			rollout.FirstPrompt = prompt
		}
	})
	if err != nil {
		return store.Rollout{}, bytesRead, err
	}

	// The filename UUID is the rollout's stable identity. Later event records
	// may carry a parent/session/event ID; accepting one here collapses a user
	// rollout and its fork/subagent into one SQLite primary key, forcing the
	// displaced file to be fully reparsed on every warm pass.
	rollout.ID = file.ID
	rollout.Path = file.Path
	rollout.Size = file.Size
	rollout.MTimeNS = file.MTimeNS
	rollout.ParsedOffset = parsedOffset
	return rollout, bytesRead, nil
}

func relevantCodexLine(line []byte) bool {
	return bytes.Contains(line, []byte(`"session_meta"`)) ||
		bytes.Contains(line, []byte(`"thread_source"`)) ||
		bytes.Contains(line, []byte(`"session_id"`)) ||
		bytes.Contains(line, []byte(`"parent_thread`)) ||
		bytes.Contains(line, []byte(`"user_message"`)) ||
		bytes.Contains(line, []byte(`"role":"user"`))
}

// protocolPrompt reports whether a user-role message is Codex protocol
// furniture rather than something a person (or an orchestrating chat) typed:
// the per-thread instruction preamble and the environment context Codex
// re-sends on the user role.
func protocolPrompt(prompt string) bool {
	for _, prefix := range []string{
		"# AGENTS.md instructions",
		"<permissions instructions",
		"<user_instructions",
		"<environment_context",
		"<ENVIRONMENT_CONTEXT",
	} {
		if strings.HasPrefix(prompt, prefix) {
			return true
		}
	}
	return false
}

func firstNonEmptyCodexField(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
