package digest

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/jsvalue"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

var (
	injectedPrefixes = []string{
		"# AGENTS.md instructions", "<environment_context>", "<user_instructions>", "<INSTRUCTIONS>",
		"<permissions instructions>",
	}
	patchFile     = regexp.MustCompile(`(?m)^\*{3} (Update|Add|Delete) File: (.+)$`)
	readOnlyCmds  = []string{"read", "search", "list_files"}
	duplicateKind = []string{"UserMessage", "AgentMessage", "Reasoning"}
)

const execFolded = "response_item/custom_tool_call exec script (shown as its actions)"

// codexRecord parses one Codex rollout record.
func (p *parsed) codexRecord(n int, raw []byte) {
	record, _ := transcript.DecodeCodexRecord(raw)
	kind := typeLabel(record.Type)
	payload, _ := record.DecodePayload()
	if payload == nil {
		payload = &transcript.CodexPayload{}
	}
	payloadKind := ""
	if pyvalue.Bool(payload.Type) {
		payloadKind = pyvalue.Str(payload.Type)
	}
	ts := stampOf(record.Timestamp)
	if !pyvalue.Bool(record.Timestamp) {
		ts = stampOf(payload.Timestamp)
	}
	p.stamp(ts)
	label := kind
	if payloadKind != "" {
		label = kind + "/" + payloadKind
	}
	switch {
	case kind == "session_meta":
		p.setMetaOr("session", member(record.Payload, "id"), member(record.Payload, "session_id"))
		p.setMetaValue("cwd", member(record.Payload, "cwd"))
		p.setMetaValue("cli", member(record.Payload, "cli_version"))
		p.mark(n, dispHeader, kind)
	case kind == "turn_context":
		settings := member(payload.CollaborationMode, "settings")
		if !isObject(settings) {
			settings = nil
		}
		p.setMetaOr("model", payload.Model, member(settings, "model"))
		p.setMetaOr("effort", payload.Effort, member(settings, "reasoning_effort"))
		p.mark(n, dispHeader, kind)
	case kind == "world_state":
		p.mark(n, dispHeader, kind)
	case kind == "compacted" || payloadKind == "compacted":
		p.add(&event{line: n, ts: ts, kind: kindNote, text: compactedNote}, label)
	case kind == codexResponseItem && payloadKind == "message":
		p.codexMessage(n, ts, label, payload)
	case kind == codexResponseItem && (payloadKind == "function_call" || payloadKind == "custom_tool_call"):
		name := stringOr(payload.Name, "?")
		input := payload.Input
		if payloadKind == "function_call" {
			input = payload.Arguments
		}
		ev := &event{line: n, ts: ts, kind: kindCall, name: name, text: codexTarget(name, input)}
		ev.raw = pyvalue.Dumps(input)
		if text, ok := pyvalue.String(input); ok {
			ev.raw = text
		}
		ev.cmd = name == "exec_command" || name == "shell" || name == "local_shell"
		p.calls[pyvalue.Repr(payload.CallID)] = ev
		if name == "exec" {
			p.wrappers = append(p.wrappers, ev)
		}
		p.add(ev, label)
	case kind == codexResponseItem && (payloadKind == "function_call_output" || payloadKind == "custom_tool_call_output"):
		p.codexOutput(n, ts, label, payload)
	case kind == codexEventMsg && payloadKind == "item_completed":
		p.codexItemCompleted(n, ts, label, payload)
	case kind == codexEventMsg && payloadKind == "task_complete":
		last := ""
		if pyvalue.Bool(payload.LastAgentMessage) {
			last = pyvalue.Strip(pyvalue.Str(payload.LastAgentMessage))
		}
		turn := p.events
		for index := len(p.events) - 1; index >= 0; index-- {
			if p.events[index].kind == kindPrompt {
				turn = p.events[index:]
				break
			}
		}
		final := false
		for _, ev := range turn {
			final = final || ev.kind == kindFinal
		}
		if last != "" && !final {
			p.add(&event{line: n, ts: ts, kind: kindFinal, text: last}, label)
		} else {
			p.mark(n, dispSkipped, label+" (its message is the reply above)")
		}
	case kind == codexEventMsg && payloadKind == "turn_aborted":
		p.add(&event{line: n, ts: ts, kind: kindNote, text: "TURN ABORTED — " + stringOr(payload.Reason, "")}, label)
	case kind == codexEventMsg && payloadKind == "error":
		p.add(&event{line: n, ts: ts, kind: kindNote, text: "ENGINE ERROR — " + stringOr(payload.Message, "")}, label)
	default:
		p.mark(n, dispSkipped, label)
	}
}

// setMetaOr sets key, unless set, to value's str() when it is truthy, else to
// fallback as setMetaValue reads it (Python's `value or fallback`).
func (p *parsed) setMetaOr(key string, value, fallback json.RawMessage) {
	if pyvalue.Bool(value) {
		p.setMeta(key, pyvalue.Str(value))
		return
	}
	p.setMetaValue(key, fallback)
}

func (p *parsed) codexMessage(n int, ts, label string, payload *transcript.CodexPayload) {
	role := stringOr(payload.Role, "?")
	text := pyvalue.Strip(transcript.FlatContent(payload.Content))
	switch {
	case role == transcript.RoleAssistant && text != "":
		kind := kindReply
		if stampOf(payload.Phase) == "final_answer" {
			kind = kindFinal
		}
		p.add(&event{line: n, ts: ts, kind: kind, text: text}, label)
	case role == transcript.RoleUser && text != "" && !hasPrefixAny(text, injectedPrefixes):
		p.add(&event{line: n, ts: ts, kind: kindPrompt, text: text}, label)
	case role == transcript.RoleUser && text != "":
		p.mark(n, dispSkipped, label+":user (injected context)")
	default:
		p.mark(n, dispSkipped, label+":"+role)
	}
}

func (p *parsed) codexOutput(n int, ts, label string, payload *transcript.CodexPayload) {
	key := pyvalue.Repr(payload.CallID)
	ev := p.calls[key]
	if ev == nil {
		p.mark(n, dispSkipped, label+" (no matching call)")
		return
	}
	delete(p.calls, key)
	text, failed := transcript.CodexToolOutput(payload.Output)
	ev.setResult(text, failed, ts)
	for index, wrapper := range p.wrappers {
		if wrapper != ev {
			continue
		}
		p.wrappers = append(p.wrappers[:index:index], p.wrappers[index+1:]...)
		if ev.children != 0 {
			ev.hidden = true
			p.mark(ev.line, dispSkipped, execFolded)
			p.mark(n, dispSkipped, strings.Replace(execFolded, "custom_tool_call", "custom_tool_call_output", 1))
			return
		}
		break
	}
	p.mark(n, dispRendered, label)
}

func (p *parsed) codexItemCompleted(n int, ts, label string, payload *transcript.CodexPayload) {
	item := payload.Item
	if !isObject(item) {
		item = nil
	}
	kind := stringOr(member(item, "type"), "?")
	actions, known := codexItem(n, ts, item)
	if !known {
		note := ""
		if slices.Contains(duplicateKind, kind) {
			note = " (duplicate of its response_item)"
		}
		p.mark(n, dispSkipped, label+":"+kind+note)
		return
	}
	for _, action := range actions {
		p.add(action, label+":"+kind)
	}
	if len(p.wrappers) != 0 {
		p.wrappers[len(p.wrappers)-1].children += len(actions)
	}
}

// codexItem is the calls one completed item shows: a command, each changed
// file, an MCP call; known is false for any other item.
func codexItem(n int, ts string, item json.RawMessage) (actions []*event, known bool) {
	get := func(key string) json.RawMessage { return member(item, key) }
	status := get("status")
	unfinished := !pyvalue.IsNull(status) && stampOf(status) != "completed"
	switch stampOf(get("type")) {
	case "CommandExecution":
		ev := &event{line: n, ts: ts, kind: kindCall, name: "exec", text: commandLine(get("command"))}
		var parsedCmd []json.RawMessage
		_ = json.Unmarshal(get("parsed_cmd"), &parsedCmd)
		ev.cmd = len(parsedCmd) == 0
		for _, part := range parsedCmd {
			if isObject(part) && !slices.Contains(readOnlyCmds, stampOf(member(part, "type"))) {
				ev.cmd = true
			}
		}
		output := ""
		if aggregated := get("aggregated_output"); pyvalue.Bool(aggregated) {
			output = pyvalue.Str(aggregated)
		} else {
			var streams []string
			for _, key := range []string{"stdout", "stderr"} {
				if text, ok := pyvalue.String(get(key)); ok && text != "" {
					streams = append(streams, text)
				}
			}
			output = strings.Join(streams, "\n")
		}
		code := get("exit_code")
		nonzero := transcript.NonzeroExit(code)
		ev.setResult(output, nonzero || unfinished, ts)
		if nonzero {
			ev.sub = "EXIT " + pyvalue.Str(code)
		}
		if duration := get("duration"); isObject(duration) && member(duration, "secs") != nil {
			seconds := decodeNumber(member(duration, "secs")) + decodeNumber(member(duration, "nanos"))/1e9
			ev.dur = &seconds
		}
		return []*event{ev}, true
	case "FileChange":
		changes, _ := jsvalue.InsertionMembers(get("changes"))
		for _, change := range changes {
			value := change.Value
			if !isObject(value) {
				value = nil
			}
			diff := ""
			if text := firstTruthy(member(value, "unified_diff"), member(value, "content")); text != nil {
				diff = pyvalue.Str(text)
			}
			plus, minus := 0, 0
			for _, line := range pyvalue.SplitLines(diff) {
				switch {
				case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
					plus++
				case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
					minus++
				}
			}
			target := change.Key + " (" + stringOr(member(value, "type"), "?") + " +" + strconv.Itoa(plus) + " -" +
				strconv.Itoa(minus) + ")"
			ev := &event{line: n, ts: ts, kind: kindCall, name: "edit", text: target}
			ev.setResult(diff, unfinished, ts)
			actions = append(actions, ev)
		}
		return actions, true
	case "McpToolCall":
		name := "mcp__" + stringOr(get("server"), "?") + "__" + stringOr(get("tool"), "?")
		ev := &event{line: n, ts: ts, kind: kindCall, name: name, text: pyvalue.Dumps(get("arguments"))}
		result := get("result")
		if !isObject(result) {
			result = nil
		}
		text := transcript.FlatContent(member(result, "content"))
		if text == "" && pyvalue.Bool(get("error")) {
			text = pyvalue.Str(get("error"))
		}
		ev.setResult(text, stampOf(status) == "failed" || pyvalue.Bool(member(result, "isError")), ts)
		return []*event{ev}, true
	}
	return nil, false
}

// decodeNumber is a JSON number's value, 0 for any other form.
func decodeNumber(raw json.RawMessage) float64 {
	var value float64
	if json.Unmarshal(raw, &value) != nil {
		return 0
	}
	return value
}

// commandLine is a command as one line: an argv ending in `-c SCRIPT` or
// `-lc SCRIPT` as the script, any other argv joined by spaces.
func commandLine(command json.RawMessage) string {
	var argv []json.RawMessage
	if json.Unmarshal(command, &argv) == nil && argv != nil {
		if len(argv) >= 3 {
			if flag := stampOf(argv[len(argv)-2]); flag == "-lc" || flag == "-c" {
				return pyvalue.Str(argv[len(argv)-1])
			}
		}
		parts := make([]string, len(argv))
		for index, part := range argv {
			parts[index] = pyvalue.Str(part)
		}
		return strings.Join(parts, " ")
	}
	if !pyvalue.Bool(command) {
		return ""
	}
	return pyvalue.Str(command)
}

// codexTarget is what a Codex tool call acts on: a patch by its files, a
// command by its line, other arguments as their JSON.
func codexTarget(name string, raw json.RawMessage) string {
	text, isText := pyvalue.String(raw)
	if isText && (name == "apply_patch" || strings.HasPrefix(pyvalue.LStrip(text), "*** Begin Patch")) {
		var files []string
		for _, match := range patchFile.FindAllStringSubmatch(text, -1) {
			files = append(files, strings.ToLower(match[1])+" "+match[2])
		}
		return "patch " + strings.Join(files, ", ")
	}
	data := raw
	if isText {
		if !json.Valid([]byte(text)) {
			return text
		}
		data = json.RawMessage(text)
	}
	if isObject(data) {
		if command := firstTruthy(member(data, "cmd"), member(data, "command")); command != nil {
			return commandLine(command)
		}
		return pyvalue.Dumps(data)
	}
	if isText {
		return text
	}
	return stringOr(raw, "None")
}
