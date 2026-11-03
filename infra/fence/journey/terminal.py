#!/usr/bin/env python3
"""Owner input goes to terminals; reads never consume engine output."""
import json
import base64
import os
import shlex
import sys

import herdr
import terminal_pty
from observation import screen, terminal
from runtime import command, read_json


def keys(target, values):
    if target["backend"] == "herdr":
        return herdr.request(os.environ["HERDR_SOCKET_PATH"], "pane.send_keys",
                             {"pane_id": target["pane"], "keys": values})
    names = {"enter": "Enter", "escape": "Escape", "ctrl+b": "C-b", "right": "Right",
             "left": "Left", "up": "Up", "down": "Down", "tab": "Tab", "backspace": "BSpace",
             "ctrl+o": "C-o", "ctrl+u": "C-u"}
    translated = [names.get(value, value) for value in values]
    if target["backend"] == "pty":
        sequences = {"Enter": "\r", "Escape": "\x1b", "C-b": "\x02", "Right": "\x1b[C",
                     "Left": "\x1b[D", "Up": "\x1b[A", "Down": "\x1b[B", "Tab": "\t",
                     "BSpace": "\x7f", "C-o": "\x0f", "C-u": "\x15"}
        return text(target, "".join(sequences.get(value, value) for value in translated))
    if target["backend"] != "tmux":
        raise ValueError(f"TERMINAL-BACKEND: {target['backend']}")
    command(["tmux", "-S", target["socket"], "send-keys", "-t", target["pane"],
             *translated])


def text(target, value):
    if target["backend"] == "pty":
        return terminal_pty.request(target, "write", base64.b64encode(value.encode()).decode())
    if target["backend"] == "herdr":
        return herdr.request(os.environ["HERDR_SOCKET_PATH"], "pane.send_text",
                             {"pane_id": target["pane"], "text": value})
    if target["backend"] != "tmux":
        raise ValueError(f"TERMINAL-BACKEND: {target['backend']}")
    command(["tmux", "-S", target["socket"], "send-keys", "-l", "-t", target["pane"], value])


def cli_command(arguments):
    """Scope native engine-tool CLI input without inheriting the owner's typed door."""
    return shlex.join(["env", "PFM_DOOR=cli", *arguments])


def type_command(target, arguments, receipt=None):
    # Export before invoking the shim so its public child and hooks receive this beat.
    exports = " ".join(f"{key}={shlex.quote(os.environ[key])}" for key in ("PFM_RUN", "PFM_BEAT"))
    line = f"export {exports} PFM_DOOR=typed; " + shlex.join(arguments)
    if receipt:
        from beatstate import receipt_dispatch
        output = shlex.quote(receipt + ".out")
        status = shlex.quote(receipt + ".status")
        line += f" >{output} 2>&1; printf '%s\\n' \"$?\" >{status}"
        receipt_dispatch(target, arguments, receipt, line)
    text(target, line)
    keys(target, ["enter"])
    if receipt:
        receipt_dispatch(target, arguments, receipt, line, sent=True)


def main(operation, target_file=None, *arguments):
    if operation == "create":
        print(json.dumps(terminal_pty.create(target_file, *arguments), sort_keys=True))
        return
    target = read_json(target_file)
    if operation == "close":
        terminal_pty.request(target, "close")
    elif operation == "keys":
        keys(target, list(arguments))
    elif operation == "type":
        text(target, arguments[0])
        keys(target, ["enter"])
    elif operation == "command":
        type_command(target, arguments)
    elif operation == "receipt":
        type_command(target, arguments[1:], arguments[0])
    elif operation == "read":
        print(json.dumps(terminal(target), sort_keys=True))
    elif operation == "screen":
        print(screen(target))
    else:
        raise ValueError(f"TERMINAL-OPERATION: {operation}")


if __name__ == "__main__":
    try:
        main(*sys.argv[1:])
    except (OSError, ValueError, RuntimeError, KeyError) as error:
        print(f"TERMINAL-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
