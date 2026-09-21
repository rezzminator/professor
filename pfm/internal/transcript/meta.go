package transcript

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/naming"
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
	ContextTokens       int64
	ContextWindow       int64
	SizeBytes           int64
	ModifiedUnixNS      int64
	HumanPrompts        int
	CompactedAfterUsage bool
	PostCompactTokens   int64
}

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

type usageRecord struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

func (usage usageRecord) contextTokens() int64 {
	return usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
}

type metaRecord struct {
	Type             string      `json:"type"`
	Subtype          string      `json:"subtype"`
	IsMeta           bool        `json:"isMeta"`
	IsSidechain      bool        `json:"isSidechain"`
	IsCompactSummary bool        `json:"isCompactSummary"`
	Usage            usageRecord `json:"usage"`
	Message          struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   usageRecord     `json:"usage"`
	} `json:"message"`
	CompactMetadata struct {
		PostTokens int64 `json:"postTokens"`
	} `json:"compactMetadata"`
	Payload struct {
		Type  string `json:"type"`
		Model string `json:"model"`
		Info  struct {
			LastTokenUsage struct {
				TotalTokens int64 `json:"total_tokens"`
			} `json:"last_token_usage"`
			TotalTokenUsage struct {
				TotalTokens int64 `json:"total_tokens"`
			} `json:"total_token_usage"`
			ModelContextWindow int64 `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

type metaScanState struct {
	claudeUsageSeen bool
}

// ReadMeta streams a transcript for the run's own facts.
func ReadMeta(path, engine string) (meta Meta, returnErr error) {
	info, err := os.Stat(path)
	if err != nil {
		return Meta{}, err
	}
	meta = Meta{
		SizeBytes:      info.Size(),
		ModifiedUnixNS: info.ModTime().UnixNano(),
	}
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
		if parsed.Payload.Type == "token_count" {
			// Codex emits both the current context-window use and a lifetime
			// total. The latter grows for the whole thread and can exceed the
			// window many times over; it is only a compatibility fallback for
			// older rollout records that predate last_token_usage.
			if current := parsed.Payload.Info.LastTokenUsage.TotalTokens; current > 0 {
				meta.ContextTokens = current
			} else if total := parsed.Payload.Info.TotalTokenUsage.TotalTokens; total > 0 {
				meta.ContextTokens = total
			}
			if window := parsed.Payload.Info.ModelContextWindow; window > 0 {
				meta.ContextWindow = window
			}
		}
		return
	}
	if parsed.IsSidechain {
		return
	}
	if parsed.Type == RoleUser && !parsed.IsMeta && !parsed.IsCompactSummary {
		prompt := naming.FlattenPromptText(parsed.Message.Content)
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
	if parsed.Type != "assistant" {
		return
	}
	// "<synthetic>" is the harness's own marker on records it wrote itself
	// (interrupts, tool errors); it is not a model anybody chose.
	if parsed.Message.Model != "" && parsed.Message.Model != "<synthetic>" {
		meta.Model = parsed.Message.Model
	}
	usage := parsed.Usage
	if usage.contextTokens() <= 0 {
		usage = parsed.Message.Usage
	}
	// Claude's occupancy is what entered the model: direct input plus both
	// cache classes. Output belongs to the response and is not in that gauge.
	if used := usage.contextTokens(); used > 0 {
		meta.ContextTokens = used
		state.claudeUsageSeen = true
		meta.CompactedAfterUsage = false
		meta.PostCompactTokens = 0
	}
}
