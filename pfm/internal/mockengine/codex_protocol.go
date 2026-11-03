package mockengine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// codexInvocation is the Codex command line pfm emits (internal/action for
// the TUI, internal/reload/reload.go:638 `resume "<id>"`) plus the doors the
// mock refuses by name.
type codexInvocation struct {
	subcommand string
	model      string
	threadID   string
	resumed    bool
	prompt     string
	ephemeral  bool
}

var codexValueFlags = map[string]bool{
	flagModel: true, "-m": true, "-c": true, "--config": true, "--sandbox": true, "-s": true, "-a": true,
	"--ask-for-approval": true, "--profile": true, "-p": true, "--cd": true, "-C": true, "--image": true, "-i": true,
	"--output-schema": true, "--color": true,
}

const (
	jsonRPCCodeKey    = "code"
	jsonRPCMessageKey = "message"
)

func parseCodexArgs(args []string) codexInvocation {
	var call codexInvocation
	for index := 0; index < len(args); {
		argument := args[index]
		if !strings.HasPrefix(argument, "-") {
			switch {
			case call.subcommand == "" && call.prompt == "" &&
				(argument == sourceResume || argument == "exec" || argument == "app-server" || argument == "mcp-server"):
				call.subcommand = argument
			case call.subcommand == sourceResume && call.threadID == "":
				call.threadID = argument
				call.resumed = true
			case call.prompt == "":
				call.prompt = argument
			}
			index++
			continue
		}
		name, inline, hasInline := strings.Cut(argument, "=")
		value, next := inline, index+1
		if codexValueFlags[name] && !hasInline && next < len(args) {
			value = args[next]
			next++
		}
		if name == flagModel || name == "-m" {
			call.model = value
		}
		if name == "--ephemeral" {
			call.ephemeral = true
		}
		index = next
	}
	return call
}

func codexAppServer(proc *process) int {
	if len(proc.script.RateLimits) == 0 {
		warn(proc.stderr, "codex app-server JSON-RPC is unpinned — supply rate_limits in the scenario")
		return ExitUnpinned
	}
	if err := proc.retainAppServerStdout(); err != nil {
		warn(proc.stderr, "%v", err)
		return ExitUsage
	}
	scanner := bufio.NewScanner(proc.stdin)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	encoder := json.NewEncoder(proc.stdout)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		reply := map[string]any{"jsonrpc": "2.0"}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			warn(proc.stderr, "decode app-server request: %v", err)
			reply["id"] = nil
			reply[keyError] = map[string]any{
				jsonRPCCodeKey:    -32700,
				jsonRPCMessageKey: "parse app-server request: " + err.Error(),
			}
		} else {
			if len(request.ID) == 0 {
				continue
			}
			reply["id"] = request.ID
			switch request.Method {
			case "initialize":
				reply["result"] = map[string]any{}
			case "account/rateLimits/read":
				reply["result"] = map[string]any{"rateLimits": proc.script.RateLimits}
			case "hooks/list", "config/value/write":
				result, err := codexHookRPC(proc, request.Method, request.Params)
				if err != nil {
					warn(proc.stderr, "%s: %v", request.Method, err)
					reply[keyError] = map[string]any{jsonRPCCodeKey: -32602, jsonRPCMessageKey: err.Error()}
				} else {
					reply["result"] = result
				}
			default:
				reply[keyError] = map[string]any{
					jsonRPCCodeKey:    -32601,
					jsonRPCMessageKey: "unknown method " + request.Method,
				}
			}
		}
		if err := encoder.Encode(reply); err != nil {
			warn(proc.stderr, "write app-server reply: %v", err)
			return ExitUsage
		}
	}
	if err := scanner.Err(); err != nil {
		warn(proc.stderr, "read app-server requests: %v", err)
		return ExitUsage
	}
	return 0
}

func (session *codexSession) headless() int {
	proc := session.proc
	prompt := session.call.prompt
	if prompt == "" {
		content, err := io.ReadAll(io.LimitReader(proc.stdin, 1<<20))
		if err != nil {
			warn(proc.stderr, "read exec prompt: %v", err)
			return ExitUsage
		}
		prompt = strings.TrimSpace(string(content))
	}
	if err := session.start(false); err != nil {
		warn(proc.stderr, "start exec: %v", err)
		return ExitUsage
	}
	if !session.call.ephemeral {
		if err := session.recordUser(prompt); err != nil {
			warn(proc.stderr, "record exec prompt: %v", err)
			return ExitUsage
		}
		if err := session.rename(firstWords(prompt)); err != nil {
			warn(proc.stderr, "name exec session: %v", err)
			return ExitUsage
		}
	}
	blocked, err := session.prompt(prompt)
	if err != nil || blocked != "" {
		warn(proc.stderr, "prompt refused: %s: %v", blocked, err)
		return ExitUnpinned
	}
	encoder := json.NewEncoder(proc.stdout)
	for _, event := range []map[string]any{{keyType: "thread.started", "thread_id": session.threadID}, {keyType: "turn.started"}} {
		if err := encoder.Encode(event); err != nil {
			warn(proc.stderr, "write exec start: %v", err)
			return ExitUsage
		}
	}
	running := proc.script.forPrompt(prompt)
	step := running.next()
	for !step.terminal() {
		var effectErr error
		switch step.Type {
		case StepToolCall:
			_, effectErr = session.tool(step)
		case StepCompact:
			effectErr = session.compact(step)
		case StepBackgroundAgent:
			_, effectErr = session.background(step)
		case StepMCP:
			effectErr = session.mcp(step)
		case StepHook:
			effectErr = session.hook(step)
		case StepPermission:
			effectErr = session.requestAttention(step)
			if effectErr == nil {
				approved, decided := session.attentionDecision()
				if !decided {
					effectErr = fmt.Errorf("codex exec requires an interactive approval for %s", step.Tool)
				} else {
					effectErr = session.answerAttention(step, approved)
					if !approved && effectErr == nil {
						effectErr = fmt.Errorf("codex exec approval denied for %s", step.Tool)
					}
				}
			}
		default:
			effectErr = fmt.Errorf("codex exec does not support scripted %s", step.Type)
		}
		if effectErr != nil {
			warn(proc.stderr, "%v", effectErr)
			return ExitUnpinned
		}
		step = running.next()
	}
	if step.Type == StepCrash {
		if step.Reply != "" {
			fmt.Fprintln(proc.stderr, step.Reply)
		}
		return step.ExitCode
	}
	if step.Type == StepExit {
		return step.ExitCode
	}
	if step.Type != StepTurn {
		warn(proc.stderr, "codex exec does not support scripted %s", step.Type)
		return ExitUnpinned
	}
	reply, busyMS, usage := running.turnReply(step)
	if !sleepOrCancel(proc.ctx, time.Duration(busyMS)*time.Millisecond) {
		warn(proc.stderr, "exec cancelled: %v", proc.ctx.Err())
		return ExitUsage
	}
	if step.Error != "" {
		if err := session.turnError(step); err != nil {
			warn(proc.stderr, "failed turn: %v", err)
			return ExitUsage
		}
		if err := encoder.Encode(
			map[string]any{keyType: "turn.failed", keyError: map[string]string{payloadMessage: step.Error}},
		); err != nil {
			warn(proc.stderr, "write error turn: %v", err)
		}
		return 1
	}
	if len(step.Structured) > 0 {
		reply = string(step.Structured)
	}
	if err := session.recordAssistant(reply, usage); err != nil {
		warn(proc.stderr, "record exec reply: %v", err)
		return ExitUsage
	}
	if err := session.stop(); err != nil {
		warn(proc.stderr, "Stop: %v", err)
		return ExitUnpinned
	}
	for _, event := range []map[string]any{
		{keyType: "item.completed", "item": map[string]any{keyType: "agent_message", keyText: reply}},
		{keyType: "turn.completed", "usage": map[string]int64{codexInputTokens: usage.Input, codexOutputTokens: usage.Output, "cached_input_tokens": usage.CacheRead, "cache_creation_input_tokens": usage.CacheCreation}},
	} {
		if err := encoder.Encode(event); err != nil {
			warn(proc.stderr, "write exec result: %v", err)
			return ExitUsage
		}
	}
	return 0
}
