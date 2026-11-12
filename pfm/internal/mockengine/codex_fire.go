package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"uuid"

	"github.com/BurntSushi/toml"
)

// codexTrusted is the trustStatus of a handler whose hooks.state holds its current hash; the keys are
// the hook input's and the rollout's spellings.
const (
	codexTrusted = "trusted"
	keyModel     = "model"
	keyCallID    = "call_id"
	keyOutput    = "output"
)

// codexHomeDir is the Codex home the process runs on: CODEX_HOME, else ~/.codex.
func codexHomeDir(proc *process) string {
	if home := proc.env("CODEX_HOME"); home != "" {
		return home
	}
	return filepath.Join(proc.env("HOME"), ".codex")
}

// readCodexHookState reads a Codex home's config.toml, the store of its hooks.state trust; an absent file
// is an empty config.
func readCodexHookState(home string) ([]byte, map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("read native hook state: %w", err)
	}
	config := map[string]any{}
	if _, err := toml.Decode(string(raw), &config); err != nil {
		return nil, nil, fmt.Errorf("parse native hook state: %w", err)
	}
	return raw, config, nil
}

// loadCodexHooks is the hooks.json handlers a Codex session fires. Codex runs a handler only while
// config.toml's hooks.state holds it enabled and trusted at its current hash (codexNativeHooks, the
// state hooks/list reports): pfm's own handlers stay silent until `pfm install` records their trust,
// and one its operator disabled never runs.
func loadCodexHooks(home string) (hookSet, error) {
	resolved, err := filepath.EvalSymlinks(home)
	if errors.Is(err, fs.ErrNotExist) {
		return hookSet{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve Codex home %s: %w", home, err)
	}
	_, config, err := readCodexHookState(resolved)
	if err != nil {
		return nil, err
	}
	native, err := codexNativeHooks(resolved, config)
	if err != nil {
		return nil, err
	}
	trusted := map[string]bool{}
	for index := range native {
		if hook := &native[index]; hook.Enabled && hook.TrustStatus == codexTrusted {
			trusted[codexHandlerKey(hook.EventName, hook.Matcher, hook.Command)] = true
		}
	}
	set, _, err := loadHookDocument(filepath.Join(resolved, "hooks.json"))
	if err != nil {
		return nil, err
	}
	for event, entries := range set {
		var kept []hookEntry
		for _, entry := range entries {
			matcher := ""
			if entry.matcher != nil {
				matcher = entry.matcher.String()
			}
			var handlers []hookHandler
			for _, handler := range entry.handlers {
				if trusted[codexHandlerKey(event, matcher, handler.command)] {
					handlers = append(handlers, handler)
				}
			}
			if len(handlers) > 0 {
				entry.handlers = handlers
				kept = append(kept, entry)
			}
		}
		set[event] = kept
	}
	return set, nil
}

// codexHandlerKey names one handler across hooks.json's spelling ("PreToolUse", matcher "*") and
// hooks/list's ("preToolUse", the raw matcher).
func codexHandlerKey(event, matcher, command string) string {
	if matcher == "*" {
		matcher = ""
	}
	return strings.ToLower(event[:1]) + event[1:] + "\x00" + matcher + "\x00" + command
}

// fire runs the session's trusted handlers of event with Codex's hook input: the thread, its rollout,
// its directory and model, then the event's own fields.
func (session *codexSession) fire(event, subject string, fields map[string]any) ([]hookAnswer, error) {
	payload := map[string]any{
		"hook_event_name": event, "session_id": session.threadID, "transcript_path": session.rollout,
		keyCWD: session.proc.cwd, keyModel: session.model,
	}
	maps.Copy(payload, fields)
	proc, ctx := session.proc, hookContext(session.proc.ctx, event)
	return session.hooks.fire(ctx, event, subject, payload, proc.cwd, session.environ, proc.stderr)
}

// foldContext writes each answer's additionalContext into history as a developer message, as Codex does.
func (session *codexSession) foldContext(answers []hookAnswer) error {
	for index := range answers {
		additional := answers[index].Specific.AdditionalContext
		if additional == "" {
			continue
		}
		if err := session.write(codexRecord{Type: recordResponseItem, Payload: codexMessage{
			Type:    payloadMessage,
			Role:    roleDeveloper,
			Content: []codexContent{{Type: blockInputText, Text: additional}},
		}}); err != nil {
			return err
		}
	}
	return nil
}

// prompt fires UserPromptSubmit: a handler's block refuses the prompt with its reason, and its
// additionalContext joins the history.
func (session *codexSession) prompt(text string) (string, error) {
	session.mcpError = ""
	answers, err := session.fire(hookUserPromptSubmit, "", map[string]any{"prompt": text})
	if err != nil {
		return "", err
	}
	for index := range answers {
		if blocked, reason := answers[index].blocks(); blocked {
			return reason, nil
		}
	}
	return "", session.foldContext(answers)
}

// tool plays a tool_call: the function_call record, PreToolUse, the call performed when the step says
// so and no handler denied it, PostToolUse, then the function_call_output, whose output is the deny
// reason of a refused call; it returns that reason, "" when allowed.
func (session *codexSession) tool(step Step) (string, error) {
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	callID := "call_" + uuid.NewV4().String()[:8]
	if err := session.write(codexRecord{Type: recordResponseItem, Payload: map[string]any{
		keyType: "function_call", "name": step.Tool, "arguments": string(input), keyCallID: callID,
	}}); err != nil {
		return "", err
	}
	call := map[string]any{"tool_name": step.Tool, "tool_input": input, "tool_use_id": callID}
	answers, err := session.fire(hookPreToolUse, step.Tool, call)
	if err != nil {
		return "", err
	}
	output, denied := "done", ""
	for index := range answers {
		if refused, reason := answers[index].denies(); refused {
			output, denied = reason, reason
			break
		}
	}
	if denied == "" {
		if step.Perform {
			if output, _, err = performTool(session.proc, step.Tool, input, nil); err != nil {
				return "", err
			}
		}
		call["tool_response"] = output
		if _, err := session.fire(hookPostToolUse, step.Tool, call); err != nil {
			return "", err
		}
	}
	return denied, session.write(codexRecord{Type: recordResponseItem, Payload: map[string]any{
		keyType: "function_call_output", keyCallID: callID, keyOutput: output,
	}})
}

// stop fires Stop at the turn's end. A handler's block would have Codex continue the turn, a beat this
// fake does not play: its answers are not read.
func (session *codexSession) stop() error {
	_, err := session.fire(hookStop, "", map[string]any{"stop_hook_active": false})
	return err
}
