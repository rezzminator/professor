#!/usr/bin/env python3
"""Inventory agent headers, not rendered bodies; one scanner for repo consumers."""
import argparse
import json
import pathlib
import re
import sys
import stat

try:
    import yaml
except ModuleNotFoundError as error:
    print(f"GAP install (agent source inventory is missing: PyYAML — {error})", file=sys.stderr)
    sys.exit(3)


def frontmatter(path):
    text = path.read_text()
    if not text.startswith("---\n"):
        return {}, text
    end = text.find("\n---\n", 4)
    if end < 0:
        raise ValueError(f"{path}: frontmatter fence is never closed")
    front = text[4:end]
    try:
        value = yaml.safe_load(front)
    except yaml.YAMLError:
        # Match the compiler's one compatibility retry for Claude's single-line values.
        lines = front.splitlines()
        for i, line in enumerate(lines):
            match = re.match(r"^([A-Za-z][A-Za-z0-9_-]*):\s*(\S.*)$", line)
            if not match:
                continue
            try:
                one = yaml.safe_load(line)
                if isinstance(one, dict) and len(one) == 1:
                    continue
            except yaml.YAMLError:
                pass
            lines[i] = match[1] + ": " + json.dumps(match[2].strip())
        try:
            value = yaml.safe_load("\n".join(lines))
        except yaml.YAMLError as error:
            raise ValueError(f"{path}: FRONTMATTER-INVALID — {error}") from error
    if not isinstance(value, dict):
        raise ValueError(f"{path}: frontmatter is not a mapping")
    return value, text[end + 5:]


def inventory(directory, project=False, codex=False):
    rows = []
    for path in sorted(directory.iterdir()):
        # engine-sync skips README.md alone; every other .md is a source.
        if path.suffix != ".md" or path.name == "README.md":
            continue
        info = path.stat()  # A dangling/unreadable source is an error, never an absent agent.
        if stat.S_ISDIR(info.st_mode) and not path.is_symlink():
            continue
        if not stat.S_ISREG(info.st_mode):
            raise ValueError(f"{path}: agent source is not a regular markdown file")
        front, _ = frontmatter(path)
        if project and ("from" in front or "abstract" in front):
            raise ValueError(f"{path}: from/abstract declarations are machine scope only")
        if "abstract" in front:
            raise ValueError(f"{path}: abstract is retired — a shared file lives below a subdirectory, with no name and no from")
        parent = front.get("from")
        if "from" in front and (not isinstance(parent, str) or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", parent)):
            raise ValueError(f"{path}: from must name an agent")
        if parent is not None and front.get("name", path.stem) != path.stem:
            raise ValueError(f"{path}: a declaration's name must equal its file base {path.stem!r}")
        kind = "declaration" if parent is not None else "original"
        engines = front.get("engines", {})
        if codex and isinstance(engines, dict) and engines.get("codex") is False:
            continue
        rows.append({"name": path.stem, "path": str(path), "kind": kind,
                     "from": parent, "model": front.get("model")})
    by_name = {row["name"]: row for row in rows}
    for row in rows:
        root = row
        seen = []
        while root["from"] is not None and root["from"] != "general-purpose":
            if root["name"] in seen:
                raise ValueError("from cycle: " + " -> ".join(seen + [root["name"]]))
            seen.append(root["name"])
            if root["from"] not in by_name:
                # A scoped source scan need not have the declaration's parent beside it.
                break
            root = by_name[root["from"]]
        row["role"] = "" if root["from"] == "general-purpose" else root["from"] or root["name"]
    return rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=pathlib.Path)
    parser.add_argument("--project", action="store_true")
    parser.add_argument("--codex", action="store_true", help="filter already rendered Claude agents for Codex")
    parser.add_argument("--names", action="store_true", help="print agent names, one per line")
    parser.add_argument("--declarations", action="store_true", help="print declaration names, one per line")
    args = parser.parse_args()
    try:
        rows = inventory(args.directory, args.project, args.codex)
        if args.declarations or args.names:
            print("\n".join(row["name"] for row in rows if
                            (row["kind"] == "declaration" or not args.declarations)))
        else:
            print(json.dumps(rows))
    except OSError as error:
        print(f"agent-source-inventory: ERROR — {error}", file=sys.stderr)
        return 2
    except (ValueError, yaml.YAMLError) as error:
        print(f"agent-source-inventory: REJECTED — {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
