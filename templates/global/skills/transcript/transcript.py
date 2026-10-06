#!/usr/bin/env python3
"""Mechanical reader of Claude Code and Codex session transcripts.

One pass parses a transcript into events (prompts, replies, tool calls with
their results, notes); filters select; a renderer prints one compact line per
event, its transcript line number first. Every record not rendered is counted
by type and named in the SKIPPED line: an unparsed record never disappears.

Verbs: show (the digest), counts (per-tool table), types (every record type and
its disposition), locate (the resolved path), agents (a session's sub-agents). A failure prints
`TRANSCRIPT FAILED — {reason}` on stderr and exits 2.
"""
import argparse
import glob
import json
import os
import re
import sys
from collections import Counter
from datetime import datetime, timedelta, timezone

FAILED = "TRANSCRIPT FAILED — "
KINDS = ("prompt", "reply", "call", "error", "note", "final")
DEFAULT_ONLY = "prompt,reply,call,note,final"
READ_ONLY_CMD = {"read", "search", "list_files"}
INJECTED = ("# AGENTS.md instructions", "<environment_context>", "<user_instructions>",
            "<INSTRUCTIONS>", "<permissions instructions>")
NOTIFICATION = ("[SYSTEM NOTIFICATION", "<task-notification>", "<system-reminder>")
PLUGIN_PROMPT = re.compile(r"^The \S+ plugin sent a message:")
CODEX_TYPES = ("session_meta", "response_item", "event_msg", "turn_context", "world_state")
CLAUDE_TYPES = ("user", "assistant", "attachment", "system", "summary")


class Failure(Exception):
    pass


def fail(msg):
    raise Failure(msg)


# ---------------------------------------------------------------- resolution

def claude_roots():
    bases = [os.environ["CLAUDE_CONFIG_DIR"]] if os.environ.get("CLAUDE_CONFIG_DIR") else []
    bases += [os.path.expanduser("~/.claude")] + sorted(glob.glob(os.path.expanduser("~/.cc/*")))
    return unique(os.path.join(b, "projects") for b in bases if os.path.isdir(os.path.join(b, "projects")))


def codex_homes():
    bases = [os.environ["CODEX_HOME"]] if os.environ.get("CODEX_HOME") else []
    bases += [os.path.expanduser("~/.codex")] + sorted(glob.glob(os.path.expanduser("~/.cc/*/codex")))
    return unique(b for b in bases if os.path.isdir(b))


def unique(items):
    out = []
    for item in items:
        if item not in out:
            out.append(item)
    return out


def resolve(target, extra_roots):
    if os.path.isfile(target):
        return target
    if re.fullmatch(r"/root(?:/[0-9A-Za-z_-]+)+", target):
        return pick(target, *by_agent_path(target, extra_roots), extra_roots)
    if os.path.isabs(target) or target.startswith(("~", ".")) or target.endswith(".jsonl"):
        fail(f"no such transcript file: {target}")
    tid = target[6:] if target.startswith("agent-") else target
    hits = []
    if re.fullmatch(r"[0-9A-Za-z_-]{6,}", tid):
        for root in claude_roots():
            hits += glob.glob(f"{root}/*/{tid}*.jsonl") + glob.glob(f"{root}/*/*/subagents/agent-{tid}*.jsonl")
        for home in codex_homes():
            hits += glob.glob(f"{home}/sessions/*/*/*/rollout-*{tid}*.jsonl")
            hits += glob.glob(f"{home}/archived_sessions/rollout-*{tid}*.jsonl")
        for root in extra_roots:
            for pattern in (f"{tid}*.jsonl", f"agent-{tid}*.jsonl", f"rollout-*{tid}*.jsonl"):
                hits += glob.glob(f"{root}/**/{pattern}", recursive=True)
    if hits:
        return pick(target, hits, [], extra_roots)
    return by_name(target, extra_roots)


NAME_TAIL = 256 * 1024
TITLE_RX = re.compile(rb'"type"\s*:\s*"(custom-title|agent-name)"')
STAMP_RX = re.compile(rb'"timestamp"\s*:\s*"([^"]+)"')


def tail_bytes(path):
    with open(path, "rb") as handle:
        handle.seek(max(0, os.path.getsize(path) - NAME_TAIL))
        return handle.read()


def chat_name(tail):
    """A chat's name is its LAST custom-title record (else its last agent-name): Claude Code appends
    them over the chat's life and a renamed chat keeps its old ones. Only the file's tail is read."""
    found = {}
    for match in reversed(list(TITLE_RX.finditer(tail))):
        kind = match.group(1).decode()
        if kind in found:
            continue
        start, end = tail.rfind(b"\n", 0, match.start()) + 1, tail.find(b"\n", match.end())
        try:
            record = json.loads(tail[start:end if end >= 0 else len(tail)])
        except ValueError:
            continue
        name = record.get("customTitle" if kind == "custom-title" else "agentName") if isinstance(record, dict) else None
        if isinstance(record, dict) and record.get("type") == kind and isinstance(name, str) and name:
            found[kind] = name
            if kind == "custom-title":
                break
    return found.get("custom-title") or found.get("agent-name") or ""


def by_name(target, extra_roots):
    """A target that is no id, prefix or path is a Claude chat name (Codex names are not looked up)."""
    files = []
    for root in unique(os.path.realpath(r) for r in claude_roots()):
        files += [f for f in glob.glob(f"{root}/*/*.jsonl") if not os.path.basename(f).startswith("agent-")]
    files = unique(os.path.realpath(f) for f in files)
    wanted, hits, unreadable = target.casefold(), [], []
    for path in files:
        try:
            tail = tail_bytes(path)
        except OSError as error:
            unreadable.append(f"{path} ({error})")
            continue
        name = chat_name(tail)
        if name and name.casefold() == wanted:
            hits.append((path, tail))
    if len(hits) == 1:
        return hits[0][0]
    if hits:
        rows = []
        for path, tail in sorted(hits):
            stamps = STAMP_RX.findall(tail)
            rows.append(f"{path} ({os.path.getsize(path)} bytes, last record {stamps[-1].decode() if stamps else 'without a timestamp'})")
        fail(f"AMBIGUOUS {target} — {len(hits)} transcripts named {target!r}: " + " ".join(rows))
    searched = ", ".join(claude_roots() + codex_homes() + list(extra_roots)) or "no root exists"
    tail = f"; {len(unreadable)} session files unreadable: " + " ".join(unreadable) if unreadable else ""
    fail(f"NOT FOUND {target} — searched {searched} for an id or prefix; the name lookup over {len(files)} session files "
         f"(the last {NAME_TAIL // 1024} KB of each) found no chat whose last title is {target!r}{tail} "
         f"(a seat name resolves to its id with `pfm chat resolve {target}`, third column)")


def by_agent_path(target, extra_roots):
    """A Codex orchestrator knows its sub-agent only by agent path (`/root/{name}`), which the
    sub-agent's rollout names in its first record, session_meta."""
    files = []
    for home in codex_homes():
        files += glob.glob(f"{home}/sessions/*/*/*/rollout-*.jsonl") + glob.glob(f"{home}/archived_sessions/rollout-*.jsonl")
    for root in extra_roots:
        files += glob.glob(f"{root}/**/rollout-*.jsonl", recursive=True)
    hits, unreadable = [], []
    for path in unique(files):
        try:
            with open(path, "rb") as handle:
                first = json.loads(handle.readline() or b"null")
        except (OSError, ValueError) as error:
            unreadable.append(f"{path} ({error})")
            continue
        payload = field(first, "payload")
        spawn = field(field(field(payload, "source"), "subagent"), "thread_spawn")
        if target in (payload.get("agent_path"), spawn.get("agent_path")):
            hits.append(path)
    return hits, unreadable


def field(obj, key):
    value = obj.get(key) if isinstance(obj, dict) else None
    return value if isinstance(value, dict) else {}


def pick(target, hits, unreadable, extra_roots):
    hits = unique(os.path.realpath(h) for h in hits)
    if not hits:
        searched = ", ".join(claude_roots() + codex_homes() + list(extra_roots)) or "no root exists"
        tail = f"; {len(unreadable)} rollouts unreadable: " + " ".join(unreadable) if unreadable else ""
        fail(f"NOT FOUND {target} — searched {searched}{tail}")
    if len(hits) > 1:
        fail(f"AMBIGUOUS {target} — {len(hits)} transcripts: " + " ".join(sorted(hits)))
    if unreadable:
        sys.stderr.write(f"TRANSCRIPT WARNING — {len(unreadable)} rollouts unreadable while resolving {target}: " + " ".join(unreadable) + "\n")
    return hits[0]


# ---------------------------------------------------------------- parsing

class Event:
    def __init__(self, line, ts, kind, name="", text=""):
        self.line, self.ts, self.kind, self.name, self.text = line, ts or "", kind, name, text
        self.sub = ""
        self.raw = ""
        self.result = None
        self.err = False
        self.cmd = False
        self.dur = None
        self.children = 0
        self.hidden = False
        self.matches = []

    def full(self):
        return "\n".join(x for x in (self.text, self.raw, self.result or "") if x)


class Parse:
    def __init__(self, path):
        self.path = path
        self.bytes = os.path.getsize(path)
        self.engine = ""
        self.events = []
        self.disp = {}
        self.meta = {}
        self.records = 0
        self.t0 = self.t1 = None

    def mark(self, line, disposition, label):
        self.disp[line] = (disposition, label)

    def add(self, event, label):
        self.events.append(event)
        self.mark(event.line, "rendered", label)
        return event

    def stamp(self, ts):
        t = parse_ts(ts)
        if t:
            self.t0 = t if self.t0 is None or t < self.t0 else self.t0
            self.t1 = t if self.t1 is None or t > self.t1 else self.t1


def parse_ts(ts):
    if not isinstance(ts, str) or not ts:
        return None
    try:
        t = datetime.fromisoformat(ts.replace("Z", "+00:00"))
    except ValueError:
        return None
    return t if t.tzinfo else t.replace(tzinfo=timezone.utc)


def flat(content):
    if content is None:
        return ""
    if isinstance(content, str):
        return content
    if isinstance(content, dict):
        content = [content]
    if not isinstance(content, list):
        return str(content)
    parts = []
    for block in content:
        if isinstance(block, str):
            parts.append(block)
        elif isinstance(block, dict):
            if isinstance(block.get("text"), str):
                parts.append(block["text"])
            elif block.get("type") in ("image", "input_image"):
                parts.append("[image]")
            elif block.get("type") == "tool_reference":
                parts.append(f"[tool {block.get('tool_name', '')}]")
            else:
                parts.append(json.dumps(block, ensure_ascii=False))
    return "\n".join(parts)


def read_records(path):
    out = []
    try:
        with open(path, "rb") as handle:
            for number, raw in enumerate(handle, 1):
                raw = raw.strip()
                if not raw:
                    continue
                try:
                    record = json.loads(raw)
                except ValueError:
                    record = None
                out.append((number, record if isinstance(record, dict) else None))
    except OSError as error:
        fail(f"cannot read {path}: {error}")
    if not out:
        fail(f"empty transcript: {path}")
    if all(record is None for _, record in out):
        fail(f"no line of {path} parsed as a JSON object ({len(out)} lines)")
    return out


def detect(records):
    for _, record in records[:80]:
        if not record:
            continue
        if record.get("type") in CODEX_TYPES or "payload" in record:
            return "codex"
        if record.get("type") in CLAUDE_TYPES or "sessionId" in record:
            return "claude"
    fail("no record names its engine: neither a Claude nor a Codex transcript")


def set_result(event, text, err, ts):
    event.result = text or ""
    event.err = bool(err)
    start, end = parse_ts(event.ts), parse_ts(ts)
    if start and end and event.dur is None:
        event.dur = (end - start).total_seconds()


def shell_cmd(command):
    if isinstance(command, list):
        if len(command) >= 3 and command[-2] in ("-lc", "-c"):
            return str(command[-1])
        return " ".join(str(c) for c in command)
    return str(command or "")


def claude_target(name, inp):
    if not isinstance(inp, dict):
        return str(inp)
    if name == "Bash":
        return str(inp.get("command", ""))
    if name in ("Read", "Write", "Edit", "MultiEdit", "NotebookEdit"):
        path = str(inp.get("file_path") or inp.get("notebook_path") or "")
        if name == "Read" and (inp.get("offset") or inp.get("limit")):
            return f"{path} @{inp.get('offset', 1)}+{inp.get('limit', '')}"
        if name == "Edit":
            old, new = str(inp.get("old_string", "")), str(inp.get("new_string", ""))
            return f"{path} (-{old.count(chr(10)) + 1} +{new.count(chr(10)) + 1} lines)"
        return path
    if name in ("Grep", "Glob"):
        return " ".join(str(inp[k]) for k in ("pattern", "path", "glob") if inp.get(k))
    if name in ("Agent", "Task"):
        return f"{inp.get('subagent_type', '')}: {inp.get('description', '')}"
    for key in ("skill", "url", "query"):
        if inp.get(key):
            return str(inp[key]) + (f" {inp['args']}" if key == "skill" and inp.get("args") else "")
    return json.dumps(inp, ensure_ascii=False)


def parse_claude(P, records):
    pending = {}
    for n, o in records:
        if o is None:
            P.mark(n, "skipped", "unparseable line")
            continue
        t, ts = o.get("type") or "?", o.get("timestamp", "")
        P.stamp(ts)
        for key, source_key in (("cwd", "cwd"), ("session", "sessionId"), ("agent", "agentId")):
            if o.get(source_key):
                P.meta.setdefault(key, o[source_key])
        msg = o.get("message") if isinstance(o.get("message"), dict) else {}
        if t == "assistant":
            if msg.get("model") and msg["model"] != "<synthetic>":
                P.meta.setdefault("model", msg["model"])
            blocks = msg.get("content")
            blocks = [{"type": "text", "text": blocks}] if isinstance(blocks, str) else (blocks or [])
            last_text = max((i for i, b in enumerate(blocks) if isinstance(b, dict) and b.get("type") == "text"), default=-1)
            shown, other = False, []
            for i, b in enumerate(blocks):
                bt = b.get("type") if isinstance(b, dict) else "?"
                if bt == "text" and (b.get("text") or "").strip():
                    kind = "final" if msg.get("stop_reason") in ("end_turn", "stop_sequence") and i == last_text else "reply"
                    P.events.append(Event(n, ts, kind, text=b["text"].strip()))
                    shown = True
                elif bt == "tool_use":
                    name = b.get("name", "?")
                    ev = Event(n, ts, "call", name, claude_target(name, b.get("input")))
                    ev.raw = json.dumps(b.get("input"), ensure_ascii=False)
                    ev.cmd = name == "Bash"
                    pending[b.get("id")] = ev
                    P.events.append(ev)
                    shown = True
                else:
                    other.append(f"assistant.{bt}")
            P.mark(n, "rendered" if shown else "skipped", "assistant" if shown else (other or ["assistant.empty"])[0])
        elif t == "user":
            content = msg.get("content")
            if o.get("isCompactSummary"):
                P.add(Event(n, ts, "note", text="COMPACT SUMMARY " + flat(content)), "user.compact_summary")
                continue
            blocks = [{"type": "text", "text": content}] if isinstance(content, str) else (content or [])
            shown, label = False, "user.empty"
            for b in blocks:
                bt = b.get("type") if isinstance(b, dict) else "?"
                if bt == "tool_result":
                    ev = pending.pop(b.get("tool_use_id"), None)
                    if ev is None:
                        label = "user.tool_result (no matching call)"
                        continue
                    set_result(ev, flat(b.get("content")), b.get("is_error"), ts)
                    shown = True
                elif bt == "text" and b.get("text", "").strip():
                    text = b["text"].strip()
                    kind = "note" if o.get("isMeta") or text.startswith(NOTIFICATION) else "prompt"
                    ev = Event(n, ts, kind, text=text)
                    if kind == "prompt" and PLUGIN_PROMPT.match(text):
                        ev.sub = "plugin"
                    P.events.append(ev)
                    shown = True
                elif bt == "image":
                    P.events.append(Event(n, ts, "prompt", text="[image]"))
                    shown = True
                else:
                    label = f"user.{bt}"
            P.mark(n, "rendered" if shown else "skipped", "user" if shown else label)
        elif t == "attachment":
            att = field(o, "attachment")
            if att.get("type") == "queued_command":
                text = flat(att.get("prompt"))
                if text.strip().startswith(NOTIFICATION):
                    P.add(Event(n, ts, "note", text=text.strip()), "attachment.queued_command (notification)")
                    continue
                ev = Event(n, ts, "prompt", text=text)
                ev.sub = "queued"
                P.add(ev, "attachment.queued_command")
            else:
                P.mark(n, "skipped", f"attachment.{att.get('type', '?')}")
        elif t in ("custom-title", "agent-name"):
            name = o.get("customTitle" if t == "custom-title" else "agentName")
            if isinstance(name, str) and name:
                P.meta[t] = name
            P.mark(n, "header", t)
        elif t == "system" and o.get("subtype") == "compact_boundary":
            P.add(Event(n, ts, "note", text="COMPACTED — the context was compacted here"), "system.compact_boundary")
        elif t == "system":
            P.mark(n, "skipped", f"system.{o.get('subtype', '?')}")
        else:
            P.mark(n, "skipped", t)
    meta = re.sub(r"\.jsonl$", ".meta.json", P.path)
    if meta != P.path and os.path.isfile(meta):
        try:
            with open(meta) as handle:
                data = json.load(handle)
            P.meta["agent_type"] = data.get("agentType", "")
            P.meta["description"] = data.get("description", "")
        except (OSError, ValueError) as error:
            P.meta["meta_error"] = f"{meta}: {error}"


def codex_target(name, raw):
    if isinstance(raw, str) and (name == "apply_patch" or raw.lstrip().startswith("*** Begin Patch")):
        files = re.findall(r"^\*\*\* (Update|Add|Delete) File: (.+)$", raw, re.M)
        return "patch " + ", ".join(f"{k.lower()} {p}" for k, p in files)
    data = raw
    if isinstance(raw, str):
        try:
            data = json.loads(raw)
        except ValueError:
            return raw
    if isinstance(data, dict):
        command = data.get("cmd") or data.get("command")
        if command:
            return shell_cmd(command)
        return json.dumps(data, ensure_ascii=False)
    return str(raw)


def codex_output(output):
    text, code = flat(output), None
    if isinstance(output, str) and output.startswith("{"):
        try:
            data = json.loads(output)
            text = str(data.get("output", text))
            code = (data.get("metadata") or {}).get("exit_code")
        except (ValueError, AttributeError):
            pass
    head = text[:400]
    match = re.search(r"(?:Process exited with code|Exit code:?)\s*(-?\d+)", head)
    if code is None and match:
        code = int(match.group(1))
    err = code not in (None, 0) or head.startswith(("Script failed", "Script error")) \
        or "apply_patch verification failed" in head
    return text, err


def parse_codex(P, records):
    calls, wrappers = {}, []
    for n, o in records:
        if o is None:
            P.mark(n, "skipped", "unparseable line")
            continue
        t = o.get("type") or "?"
        p = o.get("payload") if isinstance(o.get("payload"), dict) else {}
        pt = p.get("type") or ""
        ts = o.get("timestamp") or p.get("timestamp", "")
        P.stamp(ts)
        label = f"{t}/{pt}" if pt else t
        if t == "session_meta":
            P.meta.setdefault("session", p.get("id") or p.get("session_id", ""))
            P.meta.setdefault("cwd", p.get("cwd", ""))
            P.meta.setdefault("cli", p.get("cli_version", ""))
            P.mark(n, "header", t)
        elif t == "turn_context":
            settings = (p.get("collaboration_mode") or {}).get("settings") or {}
            P.meta.setdefault("model", p.get("model") or settings.get("model", ""))
            P.meta.setdefault("effort", p.get("effort") or settings.get("reasoning_effort", ""))
            P.mark(n, "header", t)
        elif t == "world_state":
            P.mark(n, "header", t)
        elif t == "compacted" or pt == "compacted":
            P.add(Event(n, ts, "note", text="COMPACTED — the context was compacted here"), label)
        elif t == "response_item" and pt == "message":
            role = p.get("role", "?")
            text = flat(p.get("content")).strip()
            if role == "assistant" and text:
                P.add(Event(n, ts, "final" if p.get("phase") == "final_answer" else "reply", text=text), label)
            elif role == "user" and text and not text.startswith(INJECTED):
                P.add(Event(n, ts, "prompt", text=text), label)
            elif role == "user" and text:
                P.mark(n, "skipped", f"{label}:user (injected context)")
            else:
                P.mark(n, "skipped", f"{label}:{role}")
        elif t == "response_item" and pt in ("function_call", "custom_tool_call"):
            name = p.get("name", "?")
            raw = p.get("arguments") if pt == "function_call" else p.get("input")
            ev = Event(n, ts, "call", name, codex_target(name, raw))
            ev.raw = raw if isinstance(raw, str) else json.dumps(raw, ensure_ascii=False)
            ev.cmd = name in ("exec_command", "shell", "local_shell")
            calls[p.get("call_id")] = ev
            if name == "exec":
                wrappers.append(ev)
            P.add(ev, label)
        elif t == "response_item" and pt in ("function_call_output", "custom_tool_call_output"):
            ev = calls.pop(p.get("call_id"), None)
            if ev is None:
                P.mark(n, "skipped", f"{label} (no matching call)")
                continue
            text, err = codex_output(p.get("output"))
            set_result(ev, text, err, ts)
            if ev in wrappers:
                wrappers.remove(ev)
                if ev.children:
                    ev.hidden = True
                    folded = "response_item/custom_tool_call exec script (shown as its actions)"
                    P.mark(ev.line, "skipped", folded)
                    P.mark(n, "skipped", folded.replace("custom_tool_call", "custom_tool_call_output"))
                    continue
            P.mark(n, "rendered", label)
        elif t == "event_msg" and pt == "item_completed":
            item = p.get("item") or {}
            kind = item.get("type", "?")
            actions = codex_item(n, ts, item)
            if actions is None:
                duplicate = kind in ("UserMessage", "AgentMessage", "Reasoning")
                P.mark(n, "skipped", f"{label}:{kind}" + (" (duplicate of its response_item)" if duplicate else ""))
                continue
            for action in actions:
                P.add(action, f"{label}:{kind}")
            if wrappers:
                wrappers[-1].children += len(actions)
        elif t == "event_msg" and pt == "task_complete":
            last = str(p.get("last_agent_message") or "").strip()
            prompts = [i for i, e in enumerate(P.events) if e.kind == "prompt"]
            turn = P.events[prompts[-1] if prompts else 0:]
            if last and not any(e.kind == "final" for e in turn):
                P.add(Event(n, ts, "final", text=last), label)
            else:
                P.mark(n, "skipped", f"{label} (its message is the reply above)")
        elif t == "event_msg" and pt == "turn_aborted":
            P.add(Event(n, ts, "note", text=f"TURN ABORTED — {p.get('reason', '')}"), label)
        elif t == "event_msg" and pt == "error":
            P.add(Event(n, ts, "note", text=f"ENGINE ERROR — {p.get('message', '')}"), label)
        else:
            P.mark(n, "skipped", label)


def codex_item(n, ts, item):
    kind = item.get("type")
    if kind == "CommandExecution":
        ev = Event(n, ts, "call", "exec", shell_cmd(item.get("command")))
        parsed = item.get("parsed_cmd") if isinstance(item.get("parsed_cmd"), list) else []
        ev.cmd = not parsed or any(isinstance(x, dict) and x.get("type") not in READ_ONLY_CMD for x in parsed)
        output = item.get("aggregated_output") or "\n".join(
            x for x in (item.get("stdout"), item.get("stderr")) if isinstance(x, str) and x)
        code, status = item.get("exit_code"), item.get("status")
        set_result(ev, str(output or ""), code not in (0, None) or status not in ("completed", None), ts)
        if code not in (0, None):
            ev.sub = f"EXIT {code}"
        duration = item.get("duration") or {}
        if isinstance(duration, dict) and "secs" in duration:
            ev.dur = duration.get("secs", 0) + duration.get("nanos", 0) / 1e9
        return [ev]
    if kind == "FileChange":
        out = []
        for path, change in (item.get("changes") or {}).items():
            change = change if isinstance(change, dict) else {}
            diff = str(change.get("unified_diff") or change.get("content") or "")
            plus = sum(1 for ln in diff.splitlines() if ln.startswith("+") and not ln.startswith("+++"))
            minus = sum(1 for ln in diff.splitlines() if ln.startswith("-") and not ln.startswith("---"))
            ev = Event(n, ts, "call", "edit", f"{path} ({change.get('type', '?')} +{plus} -{minus})")
            set_result(ev, diff, item.get("status") not in ("completed", None), ts)
            out.append(ev)
        return out
    if kind == "McpToolCall":
        name = f"mcp__{item.get('server', '?')}__{item.get('tool', '?')}"
        ev = Event(n, ts, "call", name, json.dumps(item.get("arguments"), ensure_ascii=False))
        result = item.get("result") if isinstance(item.get("result"), dict) else {}
        text = flat(result.get("content")) or str(item.get("error") or "")
        set_result(ev, text, item.get("status") == "failed" or result.get("isError"), ts)
        return [ev]
    return None


def parse(path):
    records = read_records(path)
    P = Parse(path)
    P.records = len(records)
    P.engine = detect(records)
    (parse_codex if P.engine == "codex" else parse_claude)(P, records)
    for n, _ in records:
        P.disp.setdefault(n, ("skipped", "unaccounted"))
    return P


# ---------------------------------------------------------------- filtering

def window_time(spec, P):
    rel = re.fullmatch(r"([+-])(\d+)([smh])", spec)
    if rel:
        span = timedelta(**{{"s": "seconds", "m": "minutes", "h": "hours"}[rel.group(3)]: int(rel.group(2))})
        if P.t0 is None:
            fail(f"--since/--until {spec}: the transcript carries no timestamps")
        return P.t0 + span if rel.group(1) == "+" else P.t1 - span
    if re.fullmatch(r"\d{1,2}:\d{2}(:\d{2})?", spec):
        if P.t0 is None:
            fail(f"--since/--until {spec}: the transcript carries no timestamps")
        parts = [int(x) for x in spec.split(":")] + [0]
        try:
            return P.t0.replace(hour=parts[0], minute=parts[1], second=parts[2], microsecond=0)
        except ValueError:
            pass
    t = parse_ts(spec)
    if t is None:
        fail(f"--since/--until: unreadable time {spec!r} (HH:MM[:SS] UTC, an ISO time, -15m from the end, +5m from the start)")
    return t


def line_bounds(spec):
    span = re.fullmatch(r"(\d+)(?:-(\d*))?", spec)
    if not span:
        fail(f"--lines {spec!r}: FROM-TO, FROM- or one line number")
    low = int(span.group(1))
    high = low if span.group(2) is None else (int(span.group(2)) if span.group(2) else float("inf"))
    if low > high:
        fail(f"--lines {spec}: FROM is after TO")
    return low, high


def select(P, a):
    only = {k.strip() for k in a.only.split(",") if k.strip()}
    unknown = only - set(KINDS)
    if unknown:
        fail(f"--only: unknown kind {', '.join(sorted(unknown))} (kinds: {', '.join(KINDS)})")
    events = [e for e in P.events if not e.hidden]
    events = [e for e in events if (e.kind != "call" and e.kind in only)
              or (e.kind == "call" and ("call" in only or ("error" in only and e.err)))]
    if a.tool:
        names = {x.strip().lower() for x in ",".join(a.tool).split(",") if x.strip()}
        events = [e for e in events if e.kind != "call" or e.name.lower() in names]
    if a.lines:
        low, high = line_bounds(a.lines)
        events = [e for e in events if low <= e.line <= high]
    if a.since or a.until:
        lo = window_time(a.since, P) if a.since else None
        hi = window_time(a.until, P) if a.until else None
        kept = []
        for e in events:
            t = parse_ts(e.ts)
            if t is None or (lo and t < lo) or (hi and t > hi):
                continue
            kept.append(e)
        events = kept
    if a.grep:
        try:
            rx = re.compile(a.grep, re.I if a.ignore_case else 0)
        except re.error as error:
            fail(f"--grep {a.grep!r}: {error}")
        kept = []
        for e in events:
            lines = [ln for ln in e.full().splitlines() if rx.search(ln)]
            if lines:
                e.matches = lines
                kept.append(e)
        events = kept
    if a.first:
        events = events[:a.first]
    if a.last:
        events = events[-a.last:]
    return events


# ---------------------------------------------------------------- rendering

def one_line(text):
    return re.sub(r"\s+", " ", text or "").strip()


def cap(text, limit):
    return text if limit <= 0 or len(text) <= limit else text[:limit] + f"…[+{len(text) - limit}]"


def size(n):
    return f"{n}B" if n < 1000 else f"{n / 1000:.1f}K" if n < 1_000_000 else f"{n / 1_000_000:.1f}M"


def clock(ts):
    t = parse_ts(ts)
    return t.strftime("%H:%M:%S") if t else "--:--:--"


def tail_lines(text, count, width=200):
    lines = [ln.rstrip() for ln in (text or "").splitlines() if ln.strip()]
    return [cap(ln, width) for ln in lines[-count:]] if count > 0 else []


def render_call(e, a):
    head = f"L{e.line} {clock(e.ts)} {e.name} {cap(one_line(e.text), a.width)}"
    if e.result is None:
        return [head + " → NO RESULT"]
    status = "ERR" if e.err else "ok"
    if e.sub:
        status = f"ERR {e.sub}" if e.err else e.sub
    head += f" → {status} {size(len(e.result))}"
    if e.dur is not None and e.dur >= 5:
        head += f" · {e.dur:.0f}s"
    body = []
    mode = a.results
    if mode == "full":
        body = [ln for ln in e.result.splitlines()]
    elif mode.startswith("tail:"):
        body = tail_lines(e.result, int(mode[5:]))
    elif e.err and mode != "none":
        body = tail_lines(e.result, a.err_lines)
    elif mode == "brief" and e.cmd:
        last = tail_lines(e.result, 1, 140)
        if last:
            head += f" ‹{last[0]}›"
    return [head] + [f"    | {ln}" for ln in body]


def render_event(e, a):
    if e.kind == "call":
        lines = render_call(e, a)
    elif e.kind == "final":
        lines = [f"L{e.line} {clock(e.ts)} FINAL"] + ["  " + ln for ln in cap(e.text, a.final).splitlines()]
    else:
        tag = {"prompt": "PROMPT", "reply": "SAY", "note": "NOTE"}[e.kind] + (f"({e.sub})" if e.sub else "")
        limit = min(a.text, 300) if e.kind == "note" and a.text > 0 else a.text
        lines = [f"L{e.line} {clock(e.ts)} {tag} {cap(one_line(e.text), limit)}"]
    lines += [f"    ~ {cap(one_line(m), 200)}" for m in e.matches[:3]]
    return lines


def header(P, a, shown=None):
    m = P.meta
    who = m.get("session", "?")
    if m.get("agent"):
        who = f"agent {m['agent']} of session {who}"
    role = " · ".join(x for x in (m.get("agent_type"), m.get("description")) if x)
    model = " ".join(x for x in (m.get("model"), m.get("effort")) if x) or "model ?"
    title = m.get("custom-title") or m.get("agent-name")
    span = "no timestamps"
    if P.t0:
        span = f"{P.t0:%Y-%m-%d %H:%M:%S}Z → {P.t1:%H:%M:%S}Z · {int((P.t1 - P.t0).total_seconds())}s wall"
    out = [f"TRANSCRIPT {P.engine} · {who} · {model}" + (f" · {role}" if role else ""),
           f"FILE {P.path} · {P.bytes} bytes · {P.records} records · cwd {m.get('cwd', '?')} (shown as ./)",
           *([f"NAME {title}"] if title else []),
           f"SPAN {span}"]
    calls = [e for e in P.events if e.kind == "call" and not e.hidden]
    per, errs = Counter(e.name for e in calls), Counter(e.name for e in calls if e.err)
    tally = " · ".join(f"{k} {v}" + (f" ({errs[k]} err)" if errs[k] else "") for k, v in per.most_common())
    unanswered = sum(1 for e in calls if e.result is None)
    out.append(f"CALLS {len(calls)}, {sum(errs.values())} err, {unanswered} without result: {tally or 'none'}")
    if m.get("meta_error"):
        out.append(f"META UNREADABLE {m['meta_error']}")
    if shown is not None:
        total = sum(1 for e in P.events if not e.hidden)
        out.append(f"FILTER only {a.only}" + (f" · tool {','.join(a.tool)}" if a.tool else "")
                   + (f" · since {a.since}" if a.since else "") + (f" · until {a.until}" if a.until else "")
                   + (f" · grep {a.grep!r}" if a.grep else "") + (f" · lines {a.lines}" if a.lines else "") + (f" · first {a.first}" if a.first else "")
                   + (f" · last {a.last}" if a.last else "")
                   + f" · results {a.results} · text {a.text} · shown {shown} of {total} events")
    return out


def skipped_line(P):
    skipped = Counter(label for d, label in P.disp.values() if d == "skipped")
    head = Counter(label for d, label in P.disp.values() if d == "header")
    line = f"SKIPPED {sum(skipped.values())} records" + (": " + " · ".join(f"{k} {v}" for k, v in skipped.most_common()) if skipped else "")
    if head:
        line += f" | HEADER {sum(head.values())}: " + " · ".join(f"{k} {v}" for k, v in head.most_common())
    return line


def shorten(text, P):
    cwd = (P.meta.get("cwd") or "").replace("file://", "").rstrip("/")
    if len(cwd) > 1:
        text = text.replace(cwd + "/", "./")
    home = os.path.expanduser("~")
    if len(home) > 1:
        text = text.replace(home + "/", "~/")
    return text


def cmd_show(P, a):
    events = select(P, a)
    lines = header(P, a, len(events))
    for e in events:
        lines += render_event(e, a)
    for e in reversed(P.events):
        if e.kind == "final":
            break
        if e.kind in ("prompt", "reply", "call"):
            lines.append("UNFINISHED — the last turn has no final reply: the run was cut off or is still running")
            break
    lines.append(skipped_line(P))
    body = lines[:1] + [shorten(ln, P) for ln in lines[1:]]
    body[1] = lines[1]
    return "\n".join(body) + "\n"


def cmd_counts(P, a):
    events = select(P, a)
    calls = [e for e in events if e.kind == "call"]
    rows = {}
    for e in calls:
        r = rows.setdefault(e.name, [0, 0, 0, 0, 0])
        r[0] += 1
        r[1] += e.err
        r[2] += len(e.result or "")
        r[3] = max(r[3], len(e.result or ""))
        r[4] += e.result is None
    lines = header(P, a) + ["TOOL | CALLS | ERR | RESULT TOTAL | RESULT MAX | NO RESULT"]
    for name, r in sorted(rows.items(), key=lambda kv: -kv[1][0]):
        lines.append(f"{name} | {r[0]} | {r[1]} | {size(r[2])} | {size(r[3])} | {r[4]}")
    kinds = Counter(e.kind for e in events)
    lines.append("EVENTS " + " · ".join(f"{k} {v}" for k, v in kinds.most_common()))
    lines.append(skipped_line(P))
    return "\n".join(lines) + "\n"


def cmd_types(P, a):
    for flag, given in (("--only", a.only != DEFAULT_ONLY), ("--tool", a.tool), ("--grep", a.grep),
                        ("--since", a.since), ("--until", a.until), ("--first", a.first), ("--last", a.last)):
        if given:
            fail(f"types counts records: {flag} does not apply (only --lines does)")
    low, high = line_bounds(a.lines) if a.lines else (0, float("inf"))
    groups = {}
    for n, (d, label) in P.disp.items():
        if low <= n <= high:
            groups.setdefault(d, Counter())[label] += 1
    total = sum(sum(c.values()) for c in groups.values())
    lines = [f"RECORDS {total} in {P.path} ({P.engine})"]
    for d in ("rendered", "header", "skipped"):
        c = groups.get(d, Counter())
        lines.append(f"{d} {sum(c.values())}: " + (" · ".join(f"{k} {v}" for k, v in c.most_common()) or "none"))
    return "\n".join(lines) + "\n"


def agent_meta(path):
    meta = re.sub(r"\.jsonl$", ".meta.json", path)
    if not os.path.isfile(meta):
        return f"META ERROR missing {meta}"
    try:
        with open(meta) as handle:
            data = json.load(handle)
    except (OSError, ValueError) as error:
        return f"META ERROR {meta}: {error}"
    if not isinstance(data, dict):
        return f"META ERROR {meta}: not a JSON object"
    return f"{data.get('agentType') or 'type ?'} · {data.get('description') or 'no description'}"


def agent_row(path):
    """One streamed pass: records, first and last timestamp, tool_use blocks; no digest is built."""
    records = calls = 0
    first = last = None
    try:
        with open(path, "rb") as handle:
            for raw in handle:
                if not raw.strip():
                    continue
                records += 1
                try:
                    o = json.loads(raw)
                except ValueError:
                    continue
                if not isinstance(o, dict):
                    continue
                t = parse_ts(o.get("timestamp"))
                if t:
                    first = t if first is None or t < first else first
                    last = t if last is None or t > last else last
                msg = o.get("message") if o.get("type") == "assistant" and isinstance(o.get("message"), dict) else {}
                blocks = msg.get("content") if isinstance(msg.get("content"), list) else []
                calls += sum(1 for b in blocks if isinstance(b, dict) and b.get("type") == "tool_use")
    except OSError as error:
        return None, f"{os.path.basename(path)} · READ ERROR {error}"
    aid = re.sub(r"^agent-|\.jsonl$", "", os.path.basename(path))
    span = f"{first:%Y-%m-%d %H:%M:%S}Z → {last:%Y-%m-%d %H:%M:%S}Z" if first else "no timestamps"
    return first, (f"{aid} · {agent_meta(path)} · {span} · {records} records · "
                   f"{size(os.path.getsize(path))} · {calls} calls")


def cmd_agents(path):
    name = os.path.basename(path)
    if os.path.basename(os.path.dirname(path)) == "subagents" or name.startswith(("agent-", "rollout-")):
        fail(f"agents takes a top-level Claude session; {path} is a sub-agent or Codex transcript")
    sid = re.sub(r"\.jsonl$", "", name)
    folder = os.path.join(os.path.dirname(path), sid, "subagents")
    lines = [f"SESSION {sid} · {path}"]
    if not os.path.isdir(folder):
        return "\n".join(lines + [f"AGENTS 0 — no subagents directory at {folder}"]) + "\n"
    rows = [agent_row(p) for p in glob.glob(os.path.join(folder, "agent-*.jsonl"))]
    far = datetime.max.replace(tzinfo=timezone.utc)
    rows.sort(key=lambda r: (r[0] or far, r[1]))
    return "\n".join(lines + [f"AGENTS {len(rows)}"] + [r[1] for r in rows]) + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser(prog="transcript.py", description="Mechanical reader of Claude Code and Codex transcripts.")
    ap.add_argument("verb", choices=("show", "counts", "types", "locate", "agents"))
    ap.add_argument("target", help="a transcript path, a session id or its prefix, a Claude agent id, a Codex agent path (/root/{name}), or a Claude chat name (its last title)")
    ap.add_argument("--root", action="append", default=[], help="an extra directory searched for the id")
    ap.add_argument("--only", default=DEFAULT_ONLY, help=f"event kinds, comma-separated: {', '.join(KINDS)}; 'error' alone keeps only failed calls")
    ap.add_argument("--tool", action="append", default=[], help="keep only calls of these tool names (comma or repeated)")
    ap.add_argument("--since", default="", help="HH:MM[:SS] UTC, an ISO time, +5m from the start, -15m from the end")
    ap.add_argument("--until", default="")
    ap.add_argument("--grep", default="", help="keep events whose full text (input, result, prose) matches; the matching lines print under each")
    ap.add_argument("-i", "--ignore-case", action="store_true")
    ap.add_argument("--first", type=int, default=0)
    ap.add_argument("--last", type=int, default=0, help="the last N events after every other filter")
    ap.add_argument("--results", default="brief", help="brief (size, a command's last line, a failure's tail) | none | tail:N | full")
    ap.add_argument("--err-lines", type=int, default=12, help="lines of a failed call's output tail (default 12)")
    ap.add_argument("--text", type=int, default=500, help="characters of a prompt or reply, 0 = whole (default 500)")
    ap.add_argument("--final", type=int, default=0, help="characters of the final reply, 0 = whole (the default: it is the run's own conclusion)")
    ap.add_argument("--lines", default="", help="transcript line range FROM-TO, FROM- or one line: the events those records produced")
    ap.add_argument("--width", type=int, default=180, help="characters of a call's target (default 180)")
    ap.add_argument("--out", default="", help="write the digest here and print its path and size")
    # `--since -15m` is a value, but argparse before 3.13 reads a leading dash as the next option.
    raw, argv = list(sys.argv[1:] if argv is None else argv), []
    while raw:
        x = raw.pop(0)
        if x in ("--since", "--until") and raw and raw[0].startswith("-"):
            x = f"{x}={raw.pop(0)}"
        argv.append(x)
    a = ap.parse_args(argv)
    try:
        if not re.fullmatch(r"brief|none|full|tail:\d+", a.results):
            fail(f"--results {a.results!r}: brief, none, full or tail:N")
        sys.stdout.reconfigure(errors="backslashreplace")
        path = resolve(a.target, a.root)
        if a.verb == "locate":
            sys.stdout.write(path + "\n")
            return 0
        if a.verb == "agents":
            sys.stdout.write(cmd_agents(path))
            return 0
        P = parse(path)
        text = {"show": cmd_show, "counts": cmd_counts, "types": cmd_types}[a.verb](P, a)
        if a.out:
            try:
                os.makedirs(os.path.dirname(os.path.abspath(a.out)), exist_ok=True)
                with open(a.out, "w", errors="backslashreplace") as handle:
                    handle.write(text)
            except OSError as error:
                fail(f"--out {a.out}: {error}")
            sys.stdout.write(f"WROTE {a.out} · {len(text.encode(errors='backslashreplace'))} bytes · {text.count(chr(10))} lines\n")
        else:
            sys.stdout.write(text)
        return 0
    except Failure as error:
        sys.stderr.write(FAILED + str(error) + "\n")
        return 2
    except Exception as error:
        sys.stderr.write(FAILED + f"internal error: {type(error).__name__}: {error}\n")
        return 2


if __name__ == "__main__":
    sys.exit(main())
