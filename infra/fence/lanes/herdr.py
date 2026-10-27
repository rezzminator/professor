#!/usr/bin/env python3
"""Lane H fixture glue; real Herdr transport, explicitly fake provider."""
import json
import os
from pathlib import Path
import socket
import sys


def prepare(source, destination):
    root = Path(destination)
    root.mkdir(parents=True, exist_ok=True)
    text = Path(source).read_text()
    patches = {
        "set -u": "set -u\nstty -ixon -ixoff || { echo 'fake PTY setup failed' >&2; exit 1; }",
        "settings='' plugin='' debug=''": '''settings="$CLAUDE_CONFIG_DIR/settings.json" plugin="$H_FIXTURE/plugin" debug="$H_FIXTURE/debug-$$.log"
fixture_sid='' fixture_name=''
for ((i=1; i<=$#; i++)); do
  case "${!i}" in
    --session-id|--resume) j=$((i+1)); fixture_sid=${!j} ;;
    --name) j=$((i+1)); fixture_name=${!j} ;;
  esac
done''',
        'sid="fake-$$-$(date +%s)"': '''if [ "${settings:0:1}" = '{' ]; then
  printf '%s\\n' "$settings" > "$H_FIXTURE/settings-$$.json"
  settings="$H_FIXTURE/settings-$$.json"
fi
sid="${fixture_sid:-$(cat /proc/sys/kernel/random/uuid)}"''',
        '  [ -z "$line" ] && continue': '''  [ -z "$line" ] && continue
  case "$line" in H_EXEC\\ *)
    bash -c "${line#H_EXEC }" > "$H_FIXTURE/exec-$sid.out" 2>&1
    printf '%s\\n' "$?" > "$H_FIXTURE/exec-$sid.rc"
    continue ;;
  esac''',
        'mode=${FAKE_CLAUDE_MODE:-normal}': 'mode=${FAKE_CLAUDE_MODE:-normal}\nexport PLUGIN_LOG_DIR="$H_FIXTURE/plugin-logs"',
        'hook() { # <event> <json>': '''status_line() {
  local command
  command=$(jq -er 'select(.statusLine.type == "command") | .statusLine.command' "$settings") || return 1
  jq -nc --arg s "$sid" --arg t "$transcript" --arg c "$PWD" --arg n "$fixture_name" \\
    '{session_id:$s,transcript_path:$t,cwd:$c,session_name:$n,model:{id:"claude-haiku-fake",display_name:"Fake"},workspace:{current_dir:$c},context_window:{context_window_size:200000},cost:{}}' |
    sh -c "$command" > "$H_FIXTURE/status-$sid.out"
}
hook() { # <event> <json>''',
        '  stop\n': '''  stop
  status_line || { echo "fake statusLine.command failed" >&2; exit 1; }
''',
        "\ndrop=''\n": '''
status_line || { echo "fake startup statusLine.command failed" >&2; exit 1; }
drop=''
''',
        'while IFS= read -r line; do': '''bind '\"\\C-s\": unix-line-discard'
while IFS= read -e -r -p '❯ ' line; do
  line=${line//$'\\033[200~'/}
  line=${line//$'\\033[201~'/}
  line=${line//$'\\023'/}''',
        'now_iso() {': '''if [ -n "$fixture_name" ]; then
  jq -nc --arg s "$sid" --arg n "$fixture_name" '{type:"custom-title",sessionId:$s,customTitle:$n}' >> "$transcript"
fi
now_iso() {''',
    }
    for old, new in patches.items():
        if text.count(old) != 1:
            raise ValueError(f"plugin fake source drift: expected one {old!r}")
        text = text.replace(old, new)
    # Claude transcript records carry the real fixture cwd, including the first
    # record. Without it an actual tmux session is indexed as an orphan agent.
    text = text.replace('--arg s "$sid"', '--arg cwd "$PWD" --arg s "$sid"')
    text = text.replace('{type:', '{cwd:$cwd,type:')
    # The production composer guard accepts Claude's marker, not a shell prompt.
    text = text.replace("\\n> '", "\\n❯ '").replace("printf '> '", "printf '❯ '")
    # A resumed provider appends to its existing transcript, never truncates it.
    text = text.replace(': > "$transcript"', 'touch "$transcript"')
    # A real interrupted turn: write a producer user record, then await input.
    marker = '  jq -nc --arg cwd "$PWD" --arg s "$sid" --argjson c "$ctx"'
    if text.count(marker) != 1:
        raise ValueError("fake assistant producer drift")
    text = text.replace(marker, '''  if [ "$line" = H_LOSS_SECRET ]; then
    printf 'H loss fixture waiting\\n'
    read -r _
    exit 0
  fi
''' + marker)
    # readline must own its prompt so draft stashing redraws the marker too.
    text = text.replace("printf 'fake claude ready\\n❯ '", "printf 'fake claude ready\\n'")
    text = text.replace("printf 'READY\\n❯ '", "printf 'READY\\n'")
    text = text.replace("printf '❯ '", ":")
    (root / "fake.sh").write_text(text)
    plugin = root / "plugin/.claude-plugin"
    plugin.mkdir(parents=True, exist_ok=True)
    (plugin / "plugin.json").write_text('{"name":"herdr-lane-fake"}\n')


def request(path, method, params):
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(10)
        connection.connect(path)
        connection.sendall((json.dumps({"id": "lane-H", "method": method, "params": params}) + "\n").encode())
        data = bytearray()
        while not data.endswith(b"\n"):
            part = connection.recv(65536)
            if not part:
                raise ValueError(f"{method}: response ended before newline: {data!r}")
            data.extend(part)
            if len(data) > 8 * 1024 * 1024:
                raise ValueError(f"{method}: response exceeds 8MiB")
    response = json.loads(data)
    print(json.dumps(response, sort_keys=True))
    if response.get("error") is not None or response.get("ok") is False:
        return 1
    if "result" not in response:
        raise ValueError(f"{method}: unrecognized response envelope")
    return 0


def main():
    if len(sys.argv) == 4 and sys.argv[1] == "prepare":
        prepare(sys.argv[2], sys.argv[3])
        return 0
    if len(sys.argv) == 5 and sys.argv[1] == "request":
        return request(sys.argv[2], sys.argv[3], json.loads(sys.argv[4]))
    raise ValueError("usage: herdr.py prepare SOURCE DIR | request SOCKET METHOD JSON")


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, KeyError) as error:
        print(f"lane-H: {error}", file=sys.stderr)
        sys.exit(2)
