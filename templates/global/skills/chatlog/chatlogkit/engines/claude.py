"""Claude Code transcripts: `~/.claude/projects/**.jsonl` and `subagents/agent-*.jsonl` with their
`.meta.json`. record() maps one JSONL record into the Parsed event model; the line numbers are the
file's own, as `pfm transcript show --lines` counts them."""
import json
import os
import re

from ..model import (CALL, COMPACTED_NOTE, FINAL, HEADER, NOTE, PROMPT, REPLY, SKIPPED, Event,
                     dumps, flat_content, member, pystr)

NOTIFICATION_PREFIXES = ("[SYSTEM NOTIFICATION", "<task-notification>", "<system-reminder>")
PLUGIN_PROMPT = re.compile(r"^The \S+ plugin sent a message:")
NOTIF_PLUMBING = re.compile(r"\s*<(output-file|tool-use-id)>.*?</\1>", re.S)


def notification_text(t):
    """A task notification from its own tag on, the harness wrapper before it and its output-file and
    tool-use-id plumbing cut, so the digest's character cut keeps the status and summary."""
    t = t.strip()
    if "<task-notification>" not in t:
        return t
    return NOTIF_PLUMBING.sub("", t[t.index("<task-notification>"):])
TYPES = ("user", "assistant", "attachment", "system", "summary")
WRITE_FILE_TOOLS = ("Write", "Edit", "MultiEdit", "NotebookEdit")
NA = {}  # Claude has every mechanical concept


def detect(obj):
    return obj.get("type") in TYPES or obj.get("sessionId") is not None


def record(p, n, obj):
    kind = obj.get("type") or "?"
    kind = kind if isinstance(kind, str) else pystr(kind)
    ts = obj.get("timestamp") if isinstance(obj.get("timestamp"), str) else ""
    p.stamp(ts)
    for key, name in (("cwd", "cwd"), ("sessionId", "session"), ("agentId", "agent")):
        if obj.get(key):
            p.set_meta(name, pystr(obj[key]))
    message = obj.get("message") if isinstance(obj.get("message"), dict) else {}
    if kind == "assistant":
        _assistant(p, n, ts, message)
    elif kind == "user":
        _user(p, n, ts, obj, message)
    elif kind == "attachment":
        _attachment(p, n, ts, obj.get("attachment"))
    elif kind in ("custom-title", "agent-name"):
        title = obj.get("customTitle") if kind == "custom-title" else obj.get("agentName")
        if isinstance(title, str) and title:
            p.title = title
        p.mark(n, HEADER, kind)
    elif kind == "system" and obj.get("subtype") == "compact_boundary":
        ev = Event(n, ts, NOTE, text=COMPACTED_NOTE + _compact_meta(obj.get("compactMetadata")))
        ev.mech.add("COMPACTION")
        p.add(ev, "system.compact_boundary")
    elif kind == "system" and obj.get("subtype") == "stop_hook_summary" and _stop_hook_text(obj):
        ev = Event(n, ts, NOTE, text=_stop_hook_text(obj))
        ev.mech.add("HOOK")
        p.add(ev, "system.stop_hook_summary (errors, context or stop)")
    elif kind == "system":
        sub = obj.get("subtype")
        p.mark(n, SKIPPED, "system." + ("?" if sub is None else pystr(sub)))
    else:
        p.mark(n, SKIPPED, kind)


def finish(p):
    """A sub-agent transcript's `.meta.json` beside it feeds the header."""
    meta = p.path[:-len(".jsonl")] + ".meta.json" if p.path.endswith(".jsonl") else p.path
    if meta == p.path or not os.path.isfile(meta):
        return
    try:
        with open(meta, encoding="utf-8") as f:
            data = json.load(f)
        if not isinstance(data, dict):
            raise ValueError("not a JSON object")
    except (OSError, ValueError) as e:
        p.meta["meta_error"] = f"{meta}: {e}"
        return
    p.meta["agent_type"] = pystr(data.get("agentType") or "")
    p.meta["description"] = pystr(data.get("description") or "")


def blocks_of(content):
    """A message content as the reader walks it: a string is one text block, a list element by
    element, an object one non-block per member, anything else nothing."""
    if isinstance(content, str):
        return [{"type": "text", "text": content}]
    if isinstance(content, list):
        return content
    if isinstance(content, dict):
        return [None] * len(content)
    return []


def block_type(b):
    return "?" if not isinstance(b, dict) else ("None" if "type" not in b else pystr(b["type"]))


def block_text(b):
    t = member(b, "text")
    return t if isinstance(t, str) else None


def _assistant(p, n, ts, message):
    model = message.get("model")
    if model and model != "<synthetic>":
        p.set_meta("model", pystr(model))
    blocks = blocks_of(message.get("content"))
    last_text = -1
    for i, b in enumerate(blocks):
        t = block_text(b)
        if isinstance(b, dict) and b.get("type") == "text" and t is not None and t.strip():
            last_text = i
    stop = message.get("stop_reason") if isinstance(message.get("stop_reason"), str) else ""
    shown, other = False, []
    for i, b in enumerate(blocks):
        t = block_text(b)
        if isinstance(b, dict) and b.get("type") == "text" and t is not None and t.strip():
            kind = FINAL if stop in ("end_turn", "stop_sequence") and i == last_text else REPLY
            p.events.append(Event(n, ts, kind, text=t.strip()))
            shown = True
        elif isinstance(b, dict) and b.get("type") == "tool_use":
            name = pystr(b["name"]) if "name" in b else "?"
            inp = b.get("input")
            ev = Event(n, ts, CALL, name=name, text=target(name, inp), raw=dumps(inp), cmd=name == "Bash")
            if name in WRITE_FILE_TOOLS and isinstance(inp, dict):
                path = inp.get("file_path") or inp.get("notebook_path")
                ev.writes = [pystr(path)] if path else []
            p.pending[pystr(b.get("id"))] = ev
            p.events.append(ev)
            shown = True
        else:
            other.append("assistant." + block_type(b))
    if shown:
        p.mark(n, "rendered", "assistant")
    else:
        p.mark(n, SKIPPED, other[0] if other else "assistant.empty")


def _user(p, n, ts, obj, message):
    content = message.get("content")
    if obj.get("isCompactSummary"):
        ev = Event(n, ts, NOTE, text="COMPACT SUMMARY " + flat_content(content))
        ev.mech.add("COMPACT-SUMMARY")
        p.add(ev, "user.compact_summary")
        return
    shown, label = False, "user.empty"
    for b in blocks_of(content):
        t = block_text(b)
        bt = block_type(b)
        if isinstance(b, dict) and b.get("type") == "tool_result":
            ev = p.pending.pop(pystr(b.get("tool_use_id")), None)
            if ev is None:
                label = "user.tool_result (no matching call)"
                continue
            ev.set_result(flat_content(b.get("content")), bool(b.get("is_error")), ts)
            shown = True
        elif isinstance(b, dict) and b.get("type") == "text" and t is not None and t.strip():
            t = t.strip()
            is_note = bool(obj.get("isMeta")) or t.startswith(NOTIFICATION_PREFIXES)
            if is_note:
                t = notification_text(t)
            ev = Event(n, ts, NOTE if is_note else PROMPT, text=t)
            if is_note and t.startswith("<task-notification>"):
                ev.mech.add("NOTICE")
            if not is_note and PLUGIN_PROMPT.match(t):
                ev.sub = "plugin"
            p.events.append(ev)
            shown = True
        elif isinstance(b, dict) and b.get("type") == "image":
            p.events.append(Event(n, ts, PROMPT, text="[image]"))
            shown = True
        else:
            label = "user." + bt
    if shown:
        p.mark(n, "rendered", "user")
    else:
        p.mark(n, SKIPPED, label)


def _compact_meta(meta):
    """The compact_boundary's own facts: trigger (auto|manual), tokens before and after, duration."""
    if not isinstance(meta, dict):
        return " · no compactMetadata"
    parts = [f"{key} {pystr(meta[key])}" for key in ("trigger", "preTokens", "postTokens", "durationMs")
             if key in meta]
    return " · " + " · ".join(parts) if parts else " · compactMetadata empty"


def _stop_hook_text(obj):
    """A stop_hook_summary worth a line: one with errors, added context or a prevented continuation."""
    errs, ctx = obj.get("hookErrors") or [], obj.get("hookAdditionalContext") or []
    if not (errs or ctx or obj.get("preventedContinuation")):
        return ""
    return (f"HOOK Stop summary · errors {pystr(errs)} · context {pystr(ctx)} · prevented "
            f"{pystr(obj.get('preventedContinuation'))} {pystr(obj.get('stopReason') or '')}").strip()


HOOK_ATTACHMENTS = ("hook_additional_context", "hook_success", "hook_non_blocking_error",
                    "hook_blocking_error", "hook_cancelled", "hook_error_during_execution",
                    "hook_stopped_continuation")


def _hook(att):
    """One hook record as a line: event, hook, outcome, and what it injected or printed."""
    kind = pystr(att.get("type"))[len("hook_"):]
    where = pystr(att.get("hookName") or att.get("hookEvent") or "?")
    body = att.get("content")
    if isinstance(body, list):
        body = " | ".join(pystr(x) for x in body)
    body = body or att.get("stderr") or att.get("stdout") or att.get("blockingError") or ""
    if kind == "cancelled":
        body = (f"{pystr(att.get('command') or '')} · timed out {pystr(att.get('timedOut'))} after "
                f"{pystr(att.get('durationMs'))} ms").strip()
    return f"HOOK {where} {kind}: {pystr(body)}".rstrip(": ")


def _attachment(p, n, ts, att):
    att = att if isinstance(att, dict) else {}
    if att.get("type") in HOOK_ATTACHMENTS and not (att.get("type") == "hook_success"
                                                     and not (att.get("content") or att.get("stdout"))):
        ev = Event(n, ts, NOTE, text=_hook(att))
        ev.mech.add("HOOK")
        p.add(ev, "attachment." + pystr(att["type"]))
        return
    if att.get("type") == "prompt_snapshot":
        text = flat_content(att.get("systemPrompt"))
        if text.strip():
            ev = Event(n, ts, NOTE, text="PROMPT SNAPSHOT\n" + text, sub="prompt_snapshot")
            ev.mech.add("PROMPT-SNAPSHOT")
            p.add(ev, "attachment.prompt_snapshot")
            return
    if att.get("type") != "queued_command":
        p.mark(n, SKIPPED, "attachment." + ("?" if "type" not in att else pystr(att["type"])))
        return
    text = flat_content(att.get("prompt"))
    if text.strip().startswith(NOTIFICATION_PREFIXES):
        ev = Event(n, ts, NOTE, text=notification_text(text))
        if "<task-notification>" in ev.text:
            ev.mech.add("NOTICE")
        p.add(ev, "attachment.queued_command (notification)")
        return
    p.add(Event(n, ts, PROMPT, text=text, sub="queued"), "attachment.queued_command")


def target(name, inp):
    """What a Claude tool call acts on, as one line names it."""
    if not isinstance(inp, dict):
        return "None" if inp is None else pystr(inp)
    g = inp.get
    if name == "Bash":
        return pystr(g("command", ""))
    if name in ("Read", "Write", "Edit", "MultiEdit", "NotebookEdit"):
        path = pystr(g("file_path") or g("notebook_path") or "")
        if name == "Read" and (g("offset") or g("limit")):
            return f"{path} @{pystr(g('offset', 1))}+{pystr(g('limit', ''))}"
        if name == "Edit":
            old, new = pystr(g("old_string", "")), pystr(g("new_string", ""))
            return f"{path} (-{old.count(chr(10)) + 1} +{new.count(chr(10)) + 1} lines)"
        return path
    if name in ("Grep", "Glob"):
        return " ".join(pystr(g(k)) for k in ("pattern", "path", "glob") if g(k))
    if name in ("Agent", "Task"):
        return f"{pystr(g('subagent_type', ''))}: {pystr(g('description', ''))}"
    for key in ("skill", "url", "query"):
        if g(key):
            t = pystr(g(key))
            if key == "skill" and g("args"):
                t += " " + pystr(g("args"))
            return t
    return dumps(inp)
