"""Writes: the files a run wrote. Tool writes come from the engine (Event.writes); a shell command's
come from shell_writes() reading its redirections, tee, sed/perl -i and cp/mv/install targets. The
`writes` verb prints each file's state on disk now."""
import json
import os
import re
import subprocess
import time

PATHLIKE = re.compile(r"^[\w./~@+$-][\w./~@+:=${}-]*$")


def shell_writes(cmd):
    """Files a shell command writes: redirections, tee, sed/perl -i, cp/mv/install targets.
    Heredoc bodies and quoted scripts are dropped first, so code text never reads as a path.
    A leading `cd DIR` in a chain prefixes the relative paths after it."""
    s = cmd
    while True:
        h = re.search(r"<<-?\s*['\"]?(\w+)['\"]?", s)
        if not h:
            break
        end = re.search(r"(?:^|\s)" + re.escape(h.group(1)) + r"(?=\s|$)", s[h.end():])
        s = s[:h.start()] + " " + (s[h.end() + end.end():] if end else "")
    # a command substitution is a path no parse knows: kept as $CMDSUB, so it resolves UNRESOLVED
    s = re.sub(r"\$\([^()]*\)", "$CMDSUB", s)
    s = re.sub(r"'[^']*'", " '' ", s)
    s = re.sub(r'"([^"]*)"', lambda m: m.group(1) if PATHLIKE.match(m.group(1)) else " '' ", s)
    out, base, env = [], "", {}

    def expand(p):
        p = re.sub(r"\$\{?(\w+)\}?", lambda m: env.get(m.group(1), m.group(0)), p)
        return p

    for seg in re.split(r"&&|\|\||;|\|", s):
        toks = seg.split()
        if not toks:
            continue
        while toks and re.fullmatch(r"\w+=\S*", toks[0]):
            k, v = toks[0].split("=", 1)
            env[k] = expand(v.strip("'\""))
            toks = toks[1:]
            seg = seg.split(None, 1)[1] if len(seg.split(None, 1)) > 1 else ""
        if not toks:
            continue
        if toks[0] == "cd" and len(toks) > 1:
            toks[1] = expand(toks[1])
            base = toks[1] if toks[1].startswith(("/", "~", "$")) else os.path.join(base, toks[1])
            continue
        got = []
        for m in re.finditer(r"(?<![0-9&<>=-])>>?\s*([^\s<>]+)", seg):
            got.append(m.group(1))
        cmd0 = os.path.basename(toks[0])
        rest = [t for t in toks[1:] if t != "''"]
        if cmd0 == "tee":
            got += [t for t in rest if not t.startswith("-")]
        elif cmd0 in ("sed", "gsed", "perl") and any(re.fullmatch(r"-[a-zA-Z]*i\S*", t) for t in toks[1:]):
            got += [t for t in rest if not t.startswith("-") and t not in ("-e",)]
        elif cmd0 in ("cp", "mv", "install", "ln") and len([t for t in rest if not t.startswith("-")]) >= 2:
            got.append([t for t in rest if not t.startswith("-")][-1])
        for p in got:
            if p.startswith("&") or p.startswith("/dev/") or not PATHLIKE.match(p):
                continue
            p = expand(p)
            if base and not p.startswith(("/", "~", "$")):
                p = os.path.join(base, p)
            out.append(p)
    return list(dict.fromkeys(out))


JS_CMD = re.compile(r"""tools\.exec_command\(\s*\{\s*["']?cmd["']?\s*:\s*("(?:[^"\\]|\\.)*")""")


def event_writes(ev):
    """The paths a call wrote: the engine's own list, else a shell command's parse; [] for none."""
    if ev.writes is not None:
        return list(ev.writes)
    if ev.cmd:
        return shell_writes(ev.text)
    # a Codex code-mode `exec` call runs its shell commands as JS string literals
    out = []
    for m in JS_CMD.finditer(ev.text or "") if ev.name == "exec" else ():
        try:
            out += shell_writes(json.loads(m.group(1)))
        except ValueError:
            continue
    return list(dict.fromkeys(out))


def file_state(ap):
    """A written file's state on disk now, as the `writes` verb prints it."""
    if not os.path.exists(ap):
        return "GONE"
    st = os.stat(ap)
    state = f"exists {st.st_size} B · mtime {time.strftime('%Y-%m-%d %H:%M', time.localtime(st.st_mtime))}"
    d = ap if os.path.isdir(ap) else os.path.dirname(ap)
    # The repo's own config never runs a program here (core.fsmonitor), and no inherited GIT_* steers it.
    env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
    try:
        p = subprocess.run(["git", "-c", "core.fsmonitor=false", "--no-optional-locks", "-C", d, "status",
                            "--porcelain", "--ignore-submodules", "--", ap],
                           capture_output=True, text=True, env=env, timeout=10)
    except (OSError, subprocess.SubprocessError):
        return state + " · git ?"
    if p.returncode != 0:
        return state + " · git ?"
    return state + " · git " + (p.stdout.strip()[:2].strip() or "clean")


def resolve_writes(kit):
    """{absolute path or `path\tUNRESOLVED`: [D refs]} of a prepared kit's writes."""
    cwd = kit.get("cwd") or ""
    seen = {}
    for w in kit["writes"]:
        for p in w["paths"]:
            ap = os.path.expanduser(p.replace("$HOME", "~", 1) if p.startswith("$HOME") else p)
            if "$" in ap:
                seen.setdefault(ap + "\tUNRESOLVED", []).append(f"D{w['D']}")
                continue
            if not os.path.isabs(ap) and cwd:
                ap = os.path.normpath(os.path.join(cwd, ap))
            seen.setdefault(ap, []).append(f"D{w['D']}")
    return seen
