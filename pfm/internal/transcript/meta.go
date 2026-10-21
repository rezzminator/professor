package transcript

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// metaMaxLine bounds memory while leaving room for large tool and prompt
// records. ReadMeta must scan the whole file because HumanPrompts is a lifetime
// count, but it never needs to retain more than one JSONL record.
const metaMaxLine = 32 << 20

// Meta is what a transcript says about the run itself, as opposed to what was
// said in it. Every field is best-effort: a transcript that does not state a
// value leaves it zero, and a caller must report it as unknown rather than
// invent one.
type Meta struct {
	Model               string
	ContextKnown        bool
	ContextTokens       int64
	ContextWindow       int64
	SizeBytes           int64
	ModifiedUnixNS      int64
	HumanPrompts        int
	CompactedAfterUsage bool
	PostCompactTokens   int64
	// CodexTurn is where the newest Codex turn record leaves the rollout:
	// inside a turn or after one ended. Codex writes assistant commentary
	// between the tool calls of one turn, so only these records tell a turn
	// that ended from a quiet gap inside one.
	CodexTurn CodexTurnState
}

// CodexTurnState is what a Codex rollout's turn records say about its newest turn.
type CodexTurnState int

const (
	// CodexTurnUnrecorded is a rollout that holds no turn record at all.
	CodexTurnUnrecorded CodexTurnState = iota
	// CodexTurnOpen follows a task_started, or a user_message after a turn ended.
	CodexTurnOpen
	// CodexTurnEnded follows a task_complete or a turn_aborted.
	CodexTurnEnded
)

// ContextPercent is the share of the context window in use, or 0 when the
// transcript never stated a window.
func (meta Meta) ContextPercent() float64 {
	if meta.ContextWindow <= 0 || meta.ContextTokens <= 0 {
		return 0
	}
	percent := float64(meta.ContextTokens) / float64(meta.ContextWindow) * 100
	if percent > 100 {
		return 100
	}
	return percent
}

// contextOf is the context a Claude usage block reports: its prompt tokens.
// A context is one request's, never a sum, so a response that billed several
// requests (counts only in iterations, or an advisor consulted between two)
// reports its last request, the one that ran last; an advisor's request is
// another model's and never this one's.
func contextOf(usage pricing.UsageBlock) int64 {
	requests := usage.Requests()
	if len(requests) == 0 {
		return 0
	}
	return requests[len(requests)-1].Context()
}

type metaRecord struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsMeta           bool            `json:"isMeta"`
	IsSidechain      bool            `json:"isSidechain"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Usage            json.RawMessage `json:"usage"`
	Message          struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   json.RawMessage `json:"usage"`
	} `json:"message"`
	CompactMetadata struct {
		PostTokens int64 `json:"postTokens"`
	} `json:"compactMetadata"`
	Payload struct {
		Type  string `json:"type"`
		Model string `json:"model"`
		Info  *struct {
			LastTokenUsage *struct {
				TotalTokens *int64 `json:"total_tokens"`
			} `json:"last_token_usage"`
			ModelContextWindow int64 `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

type metaScanState struct {
	claudeUsageSeen bool
}

// ReadMeta streams a transcript for the run's own facts.
func ReadMeta(path, engine string) (Meta, error) {
	return ReadMetaObserving(path, engine, nil)
}

// ReadMetaObserving is ReadMeta that also hands every line it scans to observe
// (nil for none), so a caller needing a second fact of the same file takes it
// in this one read. A scan error leaves observe having seen only a prefix.
func ReadMetaObserving(path, engine string, observe func(line []byte)) (meta Meta, returnErr error) {
	info, err := os.Stat(path)
	if err != nil {
		return Meta{}, err
	}
	meta = Meta{
		SizeBytes:      info.Size(),
		ModifiedUnixNS: info.ModTime().UnixNano(),
	}
	noteWholeRead(path)
	file, err := os.Open(path)
	if err != nil {
		return meta, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript %s: %w", path, err))
		}
	}()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), metaMaxLine)
	state := metaScanState{}
	for scanner.Scan() {
		applyMeta(&meta, &state, scanner.Bytes(), engine)
		if observe != nil {
			observe(scanner.Bytes())
		}
	}
	if err := scanner.Err(); err != nil {
		return meta, fmt.Errorf("scan transcript %s: %w", path, err)
	}
	return meta, nil
}

func applyMeta(meta *Meta, state *metaScanState, line []byte, engine string) {
	var parsed metaRecord
	if err := json.Unmarshal(line, &parsed); err != nil {
		return
	}
	if engine == string(pfmengine.Codex) {
		if parsed.Payload.Model != "" {
			meta.Model = parsed.Payload.Model
		}
		switch parsed.Payload.Type {
		case "task_started":
			meta.CodexTurn = CodexTurnOpen
		case "task_complete", "turn_aborted":
			meta.CodexTurn = CodexTurnEnded
		case codexUserMessageType:
			// A prompt after a turn ended opens the next one even before its
			// task_started lands.
			if meta.CodexTurn == CodexTurnEnded {
				meta.CodexTurn = CodexTurnOpen
			}
		}
		if parsed.Payload.Type == "token_count" {
			// Codex emits both the current context-window use and a lifetime
			// total. The latter grows for the whole thread and can exceed the
			// window many times over; it cannot stand in for absent occupancy.
			info := parsed.Payload.Info
			if info == nil {
				return
			}
			if info.LastTokenUsage != nil && info.LastTokenUsage.TotalTokens != nil &&
				*info.LastTokenUsage.TotalTokens >= 0 {
				meta.ContextTokens = *info.LastTokenUsage.TotalTokens
				meta.ContextKnown = true
			}
			if window := info.ModelContextWindow; window > 0 {
				meta.ContextWindow = window
			}
		}
		return
	}
	if parsed.IsSidechain {
		return
	}
	if parsed.Type == RoleUser && !parsed.IsMeta && !parsed.IsCompactSummary {
		prompt := FlattenPromptText(parsed.Message.Content)
		if prompt != "" && !naming.IsJunkPrompt(prompt) {
			meta.HumanPrompts++
		}
	}
	if parsed.Subtype == "compact_boundary" {
		if state.claudeUsageSeen {
			meta.CompactedAfterUsage = true
			meta.PostCompactTokens = parsed.CompactMetadata.PostTokens
		}
		return
	}
	if parsed.Type != "assistant" || parsed.Message.Model == "<synthetic>" {
		return
	}
	// "<synthetic>" is the harness's own marker on records it wrote itself
	// (interrupts, tool errors); it is not a model anybody chose.
	if parsed.Message.Model != "" {
		if meta.Model != "" && meta.Model != parsed.Message.Model {
			// A new model's streaming record must not relabel the previous
			// model's prompt counters as its own completed request.
			meta.ContextKnown = false
		}
		meta.Model = parsed.Message.Model
	}
	used, known := usableContext(parsed.Usage)
	if !known {
		used, known = usableContext(parsed.Message.Usage)
	}
	if known {
		meta.ContextTokens = used
		meta.ContextKnown = true
		state.claudeUsageSeen = true
		meta.CompactedAfterUsage = false
		meta.PostCompactTokens = 0
	}
}

// usableContext distinguishes an explicitly empty prompt from output-only,
// synthetic or absent usage. Iteration selection remains pricing's contract.
func usableContext(raw json.RawMessage) (int64, bool) {
	var usage pricing.UsageBlock
	if json.Unmarshal(raw, &usage) != nil {
		return 0, false
	}
	var fields struct {
		Input      *int64            `json:"input_tokens"`
		Read       *int64            `json:"cache_read_input_tokens"`
		Created    *int64            `json:"cache_creation_input_tokens"`
		Iterations []json.RawMessage `json:"iterations"`
	}
	if json.Unmarshal(raw, &fields) != nil {
		return 0, false
	}
	for _, counter := range []*int64{fields.Input, fields.Read, fields.Created} {
		if counter != nil && *counter < 0 {
			return 0, false
		}
	}
	if contextOf(usage) > 0 {
		return contextOf(usage), true
	}
	// Advisor-only iterations describe another model, not an empty executor prompt.
	if len(fields.Iterations) > 0 && len(usage.Requests()) == 0 {
		return 0, false
	}
	return 0, fields.Input != nil || fields.Read != nil || fields.Created != nil
}
