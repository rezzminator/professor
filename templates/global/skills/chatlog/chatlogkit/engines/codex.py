"""Codex rollouts: `~/.codex/sessions/**/rollout-*.jsonl`. record() maps one rollout record into the
Parsed event model; line numbers are the file's own."""
import json
import re

from ..model import (ABSENT, CALL, COMPACTED_NOTE, FINAL, HEADER, NOTE, PROMPT, REPLY, SKIPPED, Event,
                     dumps, flat_content, get, pystr, truthy)

INJECTED = ("# AGENTS.md instructions", "<environment_context>", "<user_instructions>", "<INSTRUCTIONS>",
            "<permissions instructions>")
PATCH_BEGIN = "*** Begin Patch"
PATCH_FILE = re.compile(r"^\*{3} (Update|Add|Delete) File: (.+)$", re.M)
READ_ONLY = ("read", "search", "list_files")
DUPLICATE = ("UserMessage", "AgentMessage", "Reasoning")
TYPES = ("session_meta", "response_item", "event_msg", "turn_context", "world_state")
EXEC_FOLDED = "response_item/custom_tool_call exec script (shown as its actions)"
EXIT_CODE = re.compile(r"(?:Process exited with code|Exit code:?)[\s\x85\xa0\x1c-\x1f]*(-?[0-9]+)")
SHELL_TOOLS = ("exec_command", "shell", "local_shell")
SPAWN_TOOLS = ("spawn_agent",)
NA = {"NOTICE": "Codex has no task-notification record",
      "HOOK": "the kit renders no Codex hook record",
      "COMPACT-SUMMARY": "the summary a Codex `compacted` record carries is not rendered (pfm's digest "
                         "shows the COMPACTED note only)"}


def detect(obj):
    return obj.get("type") in TYPES or obj.get("payload") is not None


def finish(p):
    p.na.update(NA)


def _label(v):
    return pystr(v) if truthy(v) else "?"


def _stamp_of(v):
    return v if isinstance(v, str) else ""


def _set_or(p, key, value, fallback):
    if truthy(value):
        p.set_meta(key, pystr(value))
    else:
        p.set_meta_value(key, fallback)


def record(p, n, obj):
    kind = _label(obj.get("type"))
    payload = obj.get("payload") if isinstance(obj.get("payload"), dict) else {}
    pk = pystr(payload["type"]) if truthy(payload.get("type")) else ""
    ts = _stamp_of(obj.get("timestamp")) if truthy(obj.get("timestamp")) else _stamp_of(payload.get("timestamp"))
    p.stamp(ts)
    label = kind + "/" + pk if pk else kind
    if kind == "session_meta":
        _set_or(p, "session", payload.get("id"), get(payload, "session_id"))
        p.set_meta_value("cwd", get(payload, "cwd"))
        p.set_meta_value("cli", get(payload, "cli_version"))
        p.mark(n, HEADER, kind)
    elif kind == "turn_context":
        settings = get(get(payload, "collaboration_mode"), "settings")
        settings = settings if isinstance(settings, dict) else {}
        _set_or(p, "model", payload.get("model"), get(settings, "model"))
        _set_or(p, "effort", payload.get("effort"), get(settings, "reasoning_effort"))
        p.mark(n, HEADER, kind)
    elif kind == "world_state":
        p.mark(n, HEADER, kind)
    elif kind == "compacted" or pk == "compacted":
        ev = Event(n, ts, NOTE, text=COMPACTED_NOTE)
        ev.mech.add("COMPACTION")
        p.add(ev, label)
    elif kind == "response_item" and pk == "message":
        _message(p, n, ts, label, payload)
    elif kind == "response_item" and pk in ("function_call", "custom_tool_call"):
        name = pystr(payload["name"]) if "name" in payload else "?"
        inp = payload.get("arguments" if pk == "function_call" else "input")
        ev = Event(n, ts, CALL, name=name, text=target(name, inp),
                   raw=inp if isinstance(inp, str) else dumps(inp))
        ev.cmd = name in SHELL_TOOLS
        if name in SPAWN_TOOLS:
            ev.mech.add("SPAWN")
        if name == "apply_patch" or ev.cmd:
            files = patch_files(inp)
            if files is not None:
                ev.writes = files
        p.calls[repr(payload.get("call_id"))] = ev
        if name == "exec":
            p.wrappers.append(ev)
        p.add(ev, label)
    elif kind == "response_item" and pk in ("function_call_output", "custom_tool_call_output"):
        _output(p, n, ts, label, payload)
    elif kind == "event_msg" and pk == "item_completed":
        _item_completed(p, n, ts, label, payload)
    elif kind == "event_msg" and pk == "task_complete":
        last = pystr(payload["last_agent_message"]).strip() if truthy(payload.get("last_agent_message")) else ""
        turn = p.events
        for i in range(len(p.events) - 1, -1, -1):
            if p.events[i].kind == PROMPT:
                turn = p.events[i:]
                break
        if last and not any(e.kind == FINAL for e in turn):
            p.add(Event(n, ts, FINAL, text=last), label)
        else:
            p.mark(n, SKIPPED, label + " (its message is the reply above)")
    elif kind == "event_msg" and pk == "turn_aborted":
        reason = pystr(payload["reason"]) if "reason" in payload else ""
        p.add(Event(n, ts, NOTE, text="TURN ABORTED — " + reason), label)
    elif kind == "event_msg" and pk == "error":
        message = pystr(payload["message"]) if "message" in payload else ""
        p.add(Event(n, ts, NOTE, text="ENGINE ERROR — " + message), label)
    else:
        p.mark(n, SKIPPED, label)


def _message(p, n, ts, label, payload):
    role = pystr(payload["role"]) if "role" in payload else "?"
    text = flat_content(payload.get("content")).strip()
    if role == "assistant" and text:
        kind = FINAL if _stamp_of(payload.get("phase")) == "final_answer" else REPLY
        p.add(Event(n, ts, kind, text=text), label)
    elif role == "user" and text and not text.startswith(INJECTED):
        p.add(Event(n, ts, PROMPT, text=text), label)
    elif role == "user" and text:
        p.mark(n, SKIPPED, label + ":user (injected context)")
    else:
        p.mark(n, SKIPPED, label + ":" + role)


def tool_output(output):
    """A Codex tool output as (text, failed): a JSON-object string by its "output" member, a nonzero
    exit code (metadata.exit_code, else "Process exited with code N" / "Exit code: N" in the first 400
    characters), "Script failed"/"Script error" or an apply_patch verification failure."""
    text = flat_content(output)
    exit_code = None
    if isinstance(output, str) and output.startswith("{"):
        try:
            wrapped = json.loads(output)
        except ValueError:
            wrapped = None
        if isinstance(wrapped, dict):
            if "output" in wrapped:
                text = pystr(wrapped["output"])
            meta = wrapped.get("metadata")
            if isinstance(meta, dict):
                exit_code = meta.get("exit_code")
    head = text[:400]
    if exit_code is None:
        m = EXIT_CODE.search(head)
        if m:
            exit_code = int(m.group(1))
    failed = (nonzero_exit(exit_code) or head.startswith("Script failed") or head.startswith("Script error")
              or "apply_patch verification failed" in head)
    return text, failed


def nonzero_exit(code):
    """An exit code that names a failure: any value but an absent one, null, false or a zero."""
    return code is not None and code is not ABSENT and (isinstance(code, (str, list, dict)) or bool(code))


def _output(p, n, ts, label, payload):
    key = repr(payload.get("call_id"))
    ev = p.calls.pop(key, None)
    if ev is None:
        p.mark(n, SKIPPED, label + " (no matching call)")
        return
    text, failed = tool_output(payload.get("output"))
    ev.set_result(text, failed, ts)
    for i, w in enumerate(p.wrappers):
        if w is ev:
            del p.wrappers[i]
            if ev.children:
                ev.hidden = True
                p.mark(ev.line, SKIPPED, EXEC_FOLDED)
                p.mark(n, SKIPPED, EXEC_FOLDED.replace("custom_tool_call", "custom_tool_call_output", 1))
                return
            break
    p.mark(n, "rendered", label)


def _item_completed(p, n, ts, label, payload):
    item = payload.get("item") if isinstance(payload.get("item"), dict) else {}
    kind = pystr(item["type"]) if "type" in item else "?"
    actions = _item(n, ts, item)
    if actions is None:
        note = " (duplicate of its response_item)" if kind in DUPLICATE else ""
        p.mark(n, SKIPPED, label + ":" + kind + note)
        return
    for a in actions:
        p.add(a, label + ":" + kind)
    if p.wrappers:
        p.wrappers[-1].children += len(actions)


def _item(n, ts, item):
    """The calls one completed item shows (a command, each changed file, an MCP call); None for any
    other item."""
    status = item.get("status")
    unfinished = status is not None and _stamp_of(status) != "completed"
    t = _stamp_of(item.get("type"))
    if t == "CommandExecution":
        ev = Event(n, ts, CALL, name="exec", text=command_line(item.get("command")))
        parsed_cmd = item.get("parsed_cmd") if isinstance(item.get("parsed_cmd"), list) else []
        ev.cmd = not parsed_cmd
        for part in parsed_cmd:
            if isinstance(part, dict) and _stamp_of(part.get("type")) not in READ_ONLY:
                ev.cmd = True
        if truthy(item.get("aggregated_output")):
            output = pystr(item["aggregated_output"])
        else:
            output = "\n".join(item[k] for k in ("stdout", "stderr") if isinstance(item.get(k), str) and item[k])
        code = item.get("exit_code")
        bad = nonzero_exit(code)
        ev.set_result(output, bad or unfinished, ts)
        if bad:
            ev.sub = "EXIT " + pystr(code)
        dur = item.get("duration")
        if isinstance(dur, dict) and "secs" in dur:
            ev.dur = _num(dur.get("secs")) + _num(dur.get("nanos")) / 1e9
        return [ev]
    if t == "FileChange":
        out = []
        changes = item.get("changes") if isinstance(item.get("changes"), dict) else {}
        for path, value in changes.items():
            value = value if isinstance(value, dict) else {}
            diff = ""
            for k in ("unified_diff", "content"):
                if truthy(value.get(k)):
                    diff = pystr(value[k])
                    break
            plus = minus = 0
            for line in diff.splitlines():
                if line.startswith("+") and not line.startswith("+++"):
                    plus += 1
                elif line.startswith("-") and not line.startswith("---"):
                    minus += 1
            kind = pystr(value["type"]) if "type" in value else "?"
            ev = Event(n, ts, CALL, name="edit", text=f"{path} ({kind} +{plus} -{minus})")
            ev.writes = [path]
            ev.set_result(diff, unfinished, ts)
            out.append(ev)
        return out
    if t == "McpToolCall":
        server = pystr(item["server"]) if "server" in item else "?"
        tool = pystr(item["tool"]) if "tool" in item else "?"
        ev = Event(n, ts, CALL, name=f"mcp__{server}__{tool}", text=dumps(item.get("arguments")))
        result = item.get("result") if isinstance(item.get("result"), dict) else {}
        text = flat_content(result.get("content"))
        if text == "" and truthy(item.get("error")):
            text = pystr(item["error"])
        ev.set_result(text, _stamp_of(status) == "failed" or truthy(result.get("isError")), ts)
        return [ev]
    return None


def _num(v):
    return v if isinstance(v, (int, float)) and not isinstance(v, bool) else 0


def command_line(command):
    """A command as one line: an argv ending in `-c SCRIPT` or `-lc SCRIPT` as the script, any other
    argv joined by spaces."""
    if isinstance(command, list):
        if len(command) >= 3 and _stamp_of(command[-2]) in ("-lc", "-c"):
            return pystr(command[-1])
        return " ".join(pystr(x) for x in command)
    return pystr(command) if truthy(command) else ""


def patch_files(inp):
    """The files a call's patch names, None for a call carrying no patch. The patch is the freeform
    apply_patch input, the JSON apply_patch tool's `input` member, or a shell command's apply_patch text."""
    texts = []
    data = inp
    if isinstance(inp, str):
        try:
            data = json.loads(inp)
        except ValueError:
            data = None
    if isinstance(data, dict):
        for k in ("input", "cmd", "command"):
            v = data.get(k)
            if isinstance(v, list):
                v = "\n".join(x for x in v if isinstance(x, str))
            if isinstance(v, str):
                texts.append(v)
    if isinstance(inp, str):
        texts.append(inp)
    for text in texts:
        if PATCH_BEGIN in text:
            return [m.group(2) for m in PATCH_FILE.finditer(text)]
    return None


def target(name, raw):
    """What a Codex tool call acts on: a patch by its files, a command by its line, other arguments
    as their JSON."""
    is_text = isinstance(raw, str)
    if is_text and (name == "apply_patch" or raw.lstrip().startswith("*** Begin Patch")):
        files = [m.group(1).lower() + " " + m.group(2) for m in PATCH_FILE.finditer(raw)]
        return "patch " + ", ".join(files)
    data = raw
    if is_text:
        try:
            data = json.loads(raw)
        except ValueError:
            return raw
    if isinstance(data, dict):
        for k in ("cmd", "command"):
            if truthy(data.get(k)):
                return command_line(data[k])
        return dumps(data)
    if is_text:
        return raw
    return "None" if raw is None else pystr(raw)
