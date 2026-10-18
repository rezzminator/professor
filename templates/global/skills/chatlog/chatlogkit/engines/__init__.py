"""Engines: how one transcript file or store becomes a Parsed. load() reads a JSONL transcript (Claude
or Codex, detected from its first records); opencode.load() reads a session of the OpenCode store."""
import json
import os

from ..model import SKIPPED, Parsed
from . import claude, codex

DETECT_WINDOW = 80
CLAUDE, CODEX, OPENCODE = "claude", "codex", "opencode"


class Failure(Exception):
    """A transcript that cannot be read; the message is what the digest prints after FAILED."""


def detect(obj):
    if obj is None:
        return ""
    if codex.detect(obj):
        return CODEX
    if claude.detect(obj):
        return CLAUDE
    return ""


def load(path):
    """A JSONL transcript as a Parsed. A line holding no JSON object is an unparseable record; an
    empty file, a file without one object, or one whose first records name no engine is a Failure."""
    p = Parsed(path, "")
    try:
        with open(path, "rb") as f:
            data = f.read()
    except OSError as e:
        raise Failure(f"cannot read {path}: {e}")
    buffered, objects, number = [], 0, 0
    unaccounted = []

    def handle(line, obj):
        if obj is None:
            p.mark(line, SKIPPED, "unparseable line")
            p.unparsed.append(line)
        elif p.engine == CODEX:
            codex.record(p, line, obj)
        else:
            claude.record(p, line, obj)
        if line not in p.disp:
            unaccounted.append(line)

    for raw in data.split(b"\n"):
        number += 1
        trimmed = raw.strip(b" \t\r\v\f")
        if not trimmed:
            continue
        p.records += 1
        obj = None
        if trimmed[:1] == b"{":
            try:
                value = json.loads(trimmed.decode("utf-8"))
                obj = value if isinstance(value, dict) else None
            except (UnicodeDecodeError, ValueError):
                obj = None
        if obj is not None:
            objects += 1
        if p.engine:
            handle(number, obj)
        elif len(buffered) < DETECT_WINDOW:
            buffered.append((number, obj))
            found = detect(obj)
            if found:
                p.engine = found
                for held in buffered:
                    handle(*held)
                buffered = []
    if p.records == 0:
        raise Failure(f"empty transcript: {path}")
    if objects == 0:
        raise Failure(f"no line of {path} parsed as a JSON object ({p.records} lines)")
    if not p.engine:
        raise Failure("no record names its engine: neither a Claude nor a Codex transcript")
    for line in unaccounted:
        p.mark(line, SKIPPED, "unaccounted")
    p.bytes = len(data)
    if p.engine == CLAUDE:
        claude.finish(p)
    else:
        codex.finish(p)
    return p
