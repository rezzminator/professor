package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

type claudeEnvelope struct {
	Result     json.RawMessage `json:"result"`
	Structured json.RawMessage `json:"structured_output"`
	Usage      json.RawMessage `json:"usage"`
	ModelUsage json.RawMessage `json:"modelUsage"`
	TotalCost  json.RawMessage `json:"total_cost_usd"`
	IsError    bool            `json:"is_error"`
}

// parseModelUsage sums Claude's per-model `modelUsage` totals — the whole session, every API
// turn. The envelope's `usage` block is the LAST turn only, while `total_cost_usd` is the sum: a
// seat that took a second turn (a structured-output retry, a tool call) reported ~2k input tokens
// against a cost that says 60k. Nil when the block is absent or empty.
func parseModelUsage(raw json.RawMessage) (*TokenUsage, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil, nil
	}
	var models map[string]struct {
		Input         int `json:"inputTokens"`
		Output        int `json:"outputTokens"`
		CacheRead     int `json:"cacheReadInputTokens"`
		CacheCreation int `json:"cacheCreationInputTokens"`
	}
	if err := json.Unmarshal(raw, &models); err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, nil
	}
	usage := &TokenUsage{}
	for name, m := range models {
		if m.Input < 0 || m.Output < 0 || m.CacheRead < 0 || m.CacheCreation < 0 {
			return nil, fmt.Errorf("modelUsage %s: negative token count", name)
		}
		usage.Input += m.Input
		usage.Output += m.Output
		usage.CachedInput += m.CacheRead
		usage.CacheCreation += m.CacheCreation
	}
	return usage, nil
}

func parseOutput(result *Result, request Request) error {
	switch request.Engine {
	case pfmengine.Claude:
		var envelope claudeEnvelope
		if err := json.Unmarshal([]byte(result.Stdout), &envelope); err != nil {
			return fmt.Errorf("parse Claude JSON envelope: %w", err)
		}
		result.IsError = envelope.IsError
		if len(envelope.Result) > 0 && string(envelope.Result) != jsonNull {
			if err := json.Unmarshal(envelope.Result, &result.Answer); err != nil {
				// a result that is not a string is still the model's answer: keep its JSON text
				result.Answer = string(envelope.Result)
				result.noteUnreadable("Claude result as text, so the answer is its JSON", err)
			}
		}
		result.StructuredOutput = append(json.RawMessage(nil), envelope.Structured...)
		var err error
		if result.Usage, err = parseTokenUsage(envelope.Usage); err != nil {
			result.noteUnreadable("Claude usage", err)
		}
		if whole, err := parseModelUsage(envelope.ModelUsage); err != nil {
			// the usage block is the last turn only: standing alone it would understate the run
			result.Usage = nil
			result.noteUnreadable("Claude modelUsage", err)
		} else if whole != nil {
			result.Usage = whole // every turn, not the last one
		}
		if result.TotalCostUSD, err = parseCost(envelope.TotalCost); err != nil {
			result.noteUnreadable("Claude cost", err)
		}
		if result.IsError {
			return fmt.Errorf("headless envelope reported an error for Claude")
		}
	case pfmengine.Codex:
		if err := parseCodexJSONL(result, request); err != nil {
			return err
		}
	case pfmengine.OpenCode:
		if err := parseOpenCodeJSONL(result, request); err != nil {
			return err
		}
	default:
		return fmt.Errorf("engine %s does not support normalized output", request.Engine)
	}
	if request.Schema != nil {
		if len(result.StructuredOutput) == 0 {
			return fmt.Errorf("%w: engine did not return structured_output", ErrStructuredOutput)
		}
		if err := validateInstance(request.Schema, result.StructuredOutput); err != nil {
			return fmt.Errorf("%w: %v", ErrStructuredOutput, err)
		}
		if result.Answer == "" {
			result.Answer = string(result.StructuredOutput)
		}
	} else if strings.TrimSpace(result.Answer) == "" {
		return fmt.Errorf("%s headless output contains no answer", request.Engine)
	}
	return nil
}

func parseCodexJSONL(result *Result, request Request) error {
	var terminal bool
	for lineNo, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Type    string          `json:"type"`
			Message string          `json:"message"`
			Item    json.RawMessage `json:"item"`
			Usage   json.RawMessage `json:"usage"`
			Error   json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			result.noteUnreadable(fmt.Sprintf("Codex JSONL event %d", lineNo+1), err)
			continue
		}
		if event.Type == "" {
			result.noteUnreadable(
				fmt.Sprintf("Codex JSONL event %d", lineNo+1), errors.New("the event has no type"))
			continue
		}
		switch event.Type {
		case jsonEventError:
			// Codex uses error events for retries as well as failures. Only a
			// subsequent terminal success proves that the engine recovered.
			if terminal {
				result.Answer = ""
			}
			terminal = false
			message := event.Message
			if message == "" {
				message = "Codex reported an error"
			}
			result.Diagnostics = append(result.Diagnostics, message)
		case "turn.failed", "thread.failed":
			return fmt.Errorf("headless event %s from Codex: %s", event.Type, event.Error)
		case "turn.started":
			terminal = false
			result.Answer = ""
		case "turn.completed":
			if len(event.Error) != 0 && string(event.Error) != jsonNull {
				return fmt.Errorf("turn.completed event from Codex contains an error")
			}
			terminal = true
			var err error
			if result.Usage, err = parseTokenUsage(event.Usage); err != nil {
				result.noteUnreadable(
					fmt.Sprintf("Codex usage in event %d", lineNo+1), err)
			}
		case "item.completed":
			var item struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(event.Item, &item); err != nil {
				result.noteUnreadable(
					fmt.Sprintf("Codex completed item in event %d", lineNo+1), err)
				continue
			}
			if item.Type == "agent_message" {
				result.Answer = item.Text
			} else if item.Type == jsonEventError && item.Message != "" {
				result.Diagnostics = append(result.Diagnostics, item.Message)
			}
		}
	}
	if !terminal {
		return fmt.Errorf("missing terminal success event in headless output from Codex")
	}
	if strings.TrimSpace(result.Answer) == "" {
		return fmt.Errorf("headless output from Codex contains no completed answer")
	}
	if request.Schema != nil {
		result.StructuredOutput = json.RawMessage(result.Answer)
	}
	return nil
}

// unreadableItem is the one diagnostic for an engine output item pfm could not decode. The caller
// leaves that item out, or its one value unknown, and the run goes on: "could not read" never
// reads as "the engine sent nothing".
func unreadableItem(what string, err error) string {
	return fmt.Sprintf("could not read %s: %v", what, err)
}

func (result *Result) noteUnreadable(what string, err error) {
	result.Diagnostics = append(result.Diagnostics, unreadableItem(what, err))
}

func parseTokenUsage(raw json.RawMessage) (*TokenUsage, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	usage := &TokenUsage{}
	known := false
	for _, field := range []struct {
		names  []string
		target *int
	}{
		{[]string{"input_tokens", "prompt_tokens"}, &usage.Input},
		{[]string{"cached_input_tokens", "cache_read_input_tokens", "cached_tokens"}, &usage.CachedInput},
		{[]string{"cache_creation_input_tokens", "cache_write_input_tokens"}, &usage.CacheCreation},
		{[]string{"output_tokens", "completion_tokens"}, &usage.Output},
	} {
		for _, name := range field.names {
			value, present := values[name]
			if !present || string(value) == jsonNull {
				continue
			}
			if err := json.Unmarshal(value, field.target); err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if *field.target < 0 {
				return nil, fmt.Errorf("%s must be nonnegative", name)
			}
			known = true
			break
		}
	}
	if !known {
		return nil, nil
	}
	return usage, nil
}

func parseCost(raw json.RawMessage) (*float64, error) {
	if len(raw) == 0 || string(raw) == jsonNull {
		return nil, nil
	}
	var cost float64
	if err := json.Unmarshal(raw, &cost); err != nil {
		return nil, err
	}
	if cost < 0 {
		return nil, fmt.Errorf("cost must be nonnegative")
	}
	return &cost, nil
}

func validateSchema(raw json.RawMessage) error {
	if strings.TrimSpace(string(raw)) == jsonNull {
		return fmt.Errorf("invalid output schema: null is not a JSON Schema")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("invalid output schema: %w", err)
	}
	if _, err := schema.Resolve(nil); err != nil {
		return fmt.Errorf("invalid output schema: %w", err)
	}
	return nil
}

func validateInstance(schemaRaw, instanceRaw json.RawMessage) error {
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaRaw, &schema); err != nil {
		return err
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return err
	}
	var instance any
	if err := json.Unmarshal(instanceRaw, &instance); err != nil {
		return err
	}
	return resolved.Validate(instance)
}

// failureDiagnostics is what a failed engine run leaves in the receipt: the stderr and stdout
// tails, and — for a Claude envelope on stdout — the error line the envelope carried.
func failureDiagnostics(result Result) []string {
	var lines []string
	if tail := boundedTail(result.Stderr, 1024); tail != "" {
		lines = append(lines, "stderr: "+tail)
	}
	if tail := boundedTail(result.Stdout, 1024); tail != "" {
		lines = append(lines, "stdout: "+tail)
	}
	var envelope claudeEnvelope
	if err := json.Unmarshal([]byte(result.Stdout), &envelope); err == nil && envelope.IsError {
		var text string
		if json.Unmarshal(envelope.Result, &text) == nil && text != "" {
			lines = append(lines, "engine error: "+boundedTail(text, 512))
		}
	}
	return lines
}

func boundedTail(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}
