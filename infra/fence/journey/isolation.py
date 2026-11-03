#!/usr/bin/env python3
"""Derive every product path from one short, private run root."""
import hashlib
from functools import lru_cache
import os
from pathlib import Path
import shlex
import sys

from runtime import command, write_json


@lru_cache(maxsize=1)
def journey_os():
    facade = Path(__file__).resolve().parents[3] / 'scripts/platform.sh'
    actual = command(['bash', '-c', 'source "$1"; platform_os', 'journey-platform', str(facade)]).strip()
    try:
        return {'linux': 'linux', 'darwin': 'macos'}[actual]
    except KeyError as error:
        raise ValueError('PLATFORM-REFUSED: ' + actual) from error


def zsh_path():
    return command(["bash", "-c", "command -v zsh"]).strip()


def environment_d(receipt):
    from projects import private_home
    private_home()
    if journey_os() != "linux":
        raise ValueError("ENVIRONMENT-D-PLATFORM: requires Linux systemd")
    environment = dict(os.environ)
    removed = ("CLAUDE_CONFIG_DIR", "PFM_CLAUDE_CONFIG_DIR_DEFAULT")
    for key in removed:
        environment.pop(key, None)
    result = {"unset": list(removed)}
    command(["/usr/lib/systemd/user-environment-generators/30-systemd-environment-d-generator"],
            env=environment, capture=result)
    write_json(receipt, result)
    return result


def environment(root, run, port):
    root = Path(root)
    if not root.is_absolute() or root == Path("/"):
        raise ValueError("ROOT-REFUSED: an absolute private root is required")
    if not 1024 <= port <= 65535:
        raise ValueError("PORT-REFUSED: expected 1024..65535")
    token = hashlib.sha256(str(root).encode()).hexdigest()[:10]
    home = root / "home"
    session = "journey-" + token
    env = {
        "HOME": str(home), "PFM_HOME": str(home),
        "USER": "journey", "LOGNAME": "journey", "SHELL": zsh_path(),
        "PATH": f"{home}/.local/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin",
        "LANG": "C.UTF-8", "TERM": "xterm-256color", "IS_SANDBOX": "1",
        "JOURNEY_ROOT": str(root), "JOURNEY_CARRY": str(root / "carry.json"),
        "JOURNEY_TREE": "/worktree",
        "JOURNEY_OS": journey_os(),
        "JOURNEY_SESSION": session, "PFM_UNIT_PREFIX": "pfm-" + token,
        "JOURNEY_MCP_PORT": str(port), "PFM_RUN": run,
        "JOURNEY_SSH_PORT": str(port + 1 if port < 65535 else 1024),
        "PFM_CONFIG": str(root / "pfm.config.json"),
        "PFM_STATE_DB": str(root / "state/pfm.db"),
        "PFM_CACHE_DB": str(root / "state/cache.db"),
        "PFM_SID_DIR": str(root / "sid"),
        "PFM_TMUX_DIR": str(root / "tmux/tmux-1000"),
        "TMUX_TMPDIR": str(root / "tmux"), "TMPDIR": str(root / "tmp"),
        "XDG_CONFIG_HOME": str(home / ".config"),
        "XDG_DATA_HOME": str(home / ".local/share"),
        "XDG_STATE_HOME": str(home / ".local/state"),
        "XDG_CACHE_HOME": str(home / ".cache"),
        "XDG_RUNTIME_DIR": "/run/user/1000",
        "DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus",
        "HERDR_SOCKET_PATH": str(home / f".config/herdr/sessions/{session}/herdr.sock"),
        "PFM_LOG_LEVEL": "debug", "PFM_DEV_FENCE": "1", "PFM_PRICES_OFFLINE": "1", "PFM_UPDATE_CHECK_OFFLINE": "1",
        "PFM_BEAT": "root/prepare", "PFM_DOOR": "prelude",
        "PFM_BEAT_FILE": str(root / "beat.json"),
        "ENGINE_SYNC_BIN": "/usr/local/libexec/pfm-journey/engine-sync",
        "GOMODCACHE": "/opt/pfm-journey/gomodcache",
        "MOCK_ENGINE_SCENARIO": str(root / "scenario.json"),
    }
    if len(env["HERDR_SOCKET_PATH"].encode()) >= 104:
        raise ValueError("ROOT-REFUSED: Herdr socket path exceeds portable Unix limit")
    return env


def refuse_ambient(ambient):
    # HOME/XDG/PATH belong to the invoking shell; they are replaced, never passed through.
    keys = set(environment("/j/probe", "probe", 18377))
    owned = {key for key in keys if key.startswith(("PFM_", "JOURNEY_", "HERDR_"))}
    owned -= {"PFM_DEV_FENCE", "PFM_PRICES_OFFLINE", "PFM_LOG_LEVEL"}
    owned.update({"PFM_CLAUDE_ROOTS", "PFM_CODEX_ROOT", "PFM_OPENCODE_ROOT",
                  "PFM_TMUX_CONF", "PFM_COST_LEDGER_DIR", "PFM_WATCH_SNAPSHOT",
                  "HERDR_CONFIG_PATH", "CLAUDE_CONFIG_DIR", "CODEX_HOME",
                  "MOCK_ENGINE_SCENARIO", "PFM_BEAT", "TMUX_TMPDIR", "JOURNEY_HOSTLESS"})
    present = sorted(key for key in owned if key in ambient)
    if present:
        raise ValueError("AMBIENT-RUN-ROOT: " + ", ".join(present))


def main():
    if sys.argv[1:] == ["check"]:
        refuse_ambient(os.environ)
        return
    if len(sys.argv) != 5:
        raise ValueError("usage: isolation.py check | ROOT RUN PORT ENV-JSON")
    env = environment(sys.argv[1], sys.argv[2], int(sys.argv[3]))
    write_json(sys.argv[4], env)
    for key, value in env.items():
        print(f"export {key}={shlex.quote(value)}")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError) as error:
        print(error, file=sys.stderr)
        sys.exit(2)
