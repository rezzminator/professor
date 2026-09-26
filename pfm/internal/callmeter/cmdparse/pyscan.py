# cmdparse Python scanner: run as `python3 -c {this file}` by cmdparse.
# stdin:  a JSON list of {"id": str, "code": str}
# stdout: a JSON list of {"id", "calls", "strings", "error"}, same order.
# A snippet that does not parse carries its error and no calls; any other
# failure exits non-zero with the traceback on stderr, so the Go side marks
# the batch python-unavailable with the cause instead of reading silence.
import ast
import json
import shlex
import sys


def dotted(node):
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Attribute):
        base = dotted(node.value)
        return base + "." + node.attr if base else node.attr
    if isinstance(node, ast.Call):
        base = dotted(node.func)
        return base + "()" if base else ""
    return ""


def const_str(node):
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        return node.value
    return None


def argv_of(node):
    items = None
    if isinstance(node, (ast.List, ast.Tuple)):
        items = [const_str(e) for e in node.elts]
        if any(i is None for i in items):
            return None
        return items
    text = const_str(node)
    if text is None:
        return None
    try:
        return shlex.split(text)
    except ValueError:
        return [text]


def scan(code):
    tree = ast.parse(code)
    calls = []
    strings = []
    seen = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Constant) and isinstance(node.value, str):
            if node.value not in seen:
                seen.add(node.value)
                strings.append(node.value)
        if not isinstance(node, ast.Call):
            continue
        entry = {
            "func": dotted(node.func),
            "args": [const_str(a) for a in node.args],
            "kwargs": {},
            "recv": None,
            "argv": None,
        }
        for kw in node.keywords:
            if kw.arg is not None and const_str(kw.value) is not None:
                entry["kwargs"][kw.arg] = const_str(kw.value)
        if isinstance(node.func, ast.Attribute) and isinstance(node.func.value, ast.Call):
            entry["recv"] = [const_str(a) for a in node.func.value.args]
        if entry["func"].startswith("subprocess.") and node.args:
            entry["argv"] = argv_of(node.args[0])
        calls.append(entry)
    return calls, strings


def main():
    snippets = json.load(sys.stdin)
    out = []
    for snip in snippets:
        result = {"id": snip["id"], "calls": [], "strings": [], "error": ""}
        try:
            result["calls"], result["strings"] = scan(snip["code"])
        except SyntaxError as exc:
            result["error"] = "SyntaxError: %s (line %s)" % (exc.msg, exc.lineno)
        except (ValueError, RecursionError, MemoryError) as exc:
            result["error"] = "%s: %s" % (type(exc).__name__, exc)
        out.append(result)
    json.dump(out, sys.stdout)


main()
