#!/usr/bin/env python3
"""check-plugin-workflows.py [ROOT] — every plugin keeps the one-check CI shape.

For each submodule `.gitmodules` lists under ROOT (default: the current directory),
its `.github/workflows/ci.yml` and `publish.yml` must keep the shape
`.claude/rules/ci-workflows.md` and `templates/global/plugin-ci/` set:

  ci.yml       triggered only by `pull_request` into `main`, types opened,
               synchronize and reopened (no push, no workflow_dispatch); jobs
               `check` and `release`; a `ci` job with `if: always()` needing
               every other job; `check`'s matrix carries `macos-15`, or the
               check job holds a comment line naming `macos-15` saying why not.
  publish.yml  triggered only by `pull_request` `closed` into `main`; every job
               needing no other is gated on `github.event.pull_request.merged == true`.
  any other    at most one more workflow, triggered by `workflow_dispatch` alone.

Output: `PASS {path}` or one `FAIL {path}: {rule}` per broken rule, then
`plugin workflows: N checked, M failed`. Exit 0 all pass, 1 a plugin lost the
shape, 2 could not look (no .gitmodules entry, a submodule not checked out, an
unreadable or unparsable file, PyYAML missing): `NOT CHECKED …`, never a pass.
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    print("NOT CHECKED: PyYAML is missing (python3-yaml); no workflow was read", file=sys.stderr)
    sys.exit(2)

CI_TYPES = {"opened", "synchronize", "reopened"}
MERGED = "github.event.pull_request.merged == true"


class CouldNotLook(Exception):
    pass


def plugin_paths(root: Path) -> list[str]:
    gitmodules = root / ".gitmodules"
    if not gitmodules.is_file():
        raise CouldNotLook(f"no .gitmodules under {root}")
    run = subprocess.run(
        ["git", "config", "--file", str(gitmodules), "--get-regexp", r"^submodule\..*\.path$"],
        capture_output=True, text=True, cwd=root,
    )
    if run.returncode not in (0, 1):
        raise CouldNotLook(f"git config could not read {gitmodules}: {run.stderr.strip()}")
    paths = [line.split(" ", 1)[1] for line in run.stdout.splitlines() if " " in line]
    if not paths:
        raise CouldNotLook(f"{gitmodules} lists no submodule path")
    return paths


def load(path: Path) -> tuple[dict, str]:
    try:
        text = path.read_text(encoding="utf-8")
        data = yaml.safe_load(text)
    except (OSError, UnicodeDecodeError, yaml.YAMLError) as err:
        raise CouldNotLook(f"{path}: {err}") from err
    if not isinstance(data, dict):
        raise CouldNotLook(f"{path}: not a YAML mapping")
    return data, text


def triggers(data: dict) -> object:
    # YAML 1.1 reads a bare `on:` key as the boolean true.
    return data.get("on", data.get(True))


def pull_request_only(data: dict, types: set[str]) -> list[str]:
    on = triggers(data)
    if not isinstance(on, dict) or set(on) != {"pull_request"}:
        found = sorted(map(str, on)) if isinstance(on, dict) else on
        return [f"triggers must be pull_request alone, found {found}"]
    pr = on["pull_request"] or {}
    if not isinstance(pr, dict):
        return [f"pull_request must be a mapping of branches and types, found {pr!r}"]
    broken = []
    if set(pr) != {"branches", "types"}:
        broken.append(f"pull_request must set exactly branches and types, found {sorted(pr)}")
    if pr.get("branches") != ["main"]:
        broken.append(f"pull_request branches must be [main], found {pr.get('branches')}")
    if not isinstance(pr.get("types"), list) or set(pr["types"]) != types:
        broken.append(f"pull_request types must be {sorted(types)}, found {pr.get('types')}")
    return broken


def strings(value: object) -> list[str]:
    if isinstance(value, dict):
        return [s for v in value.values() for s in strings(v)]
    if isinstance(value, list):
        return [s for v in value for s in strings(v)]
    return [value] if isinstance(value, str) else []


def job_block(text: str, job: str) -> str:
    match = re.search(rf"^  {re.escape(job)}:\n(.*?)(?=^  \S|^\S|\Z)", text, re.M | re.S)
    return match.group(1) if match else ""


def check_ci(data: dict, text: str) -> list[str]:
    broken = pull_request_only(data, CI_TYPES)
    jobs = data.get("jobs") or {}
    if not isinstance(jobs, dict) or not all(isinstance(j, dict) for j in jobs.values()):
        return broken + ["ci.yml jobs must be a mapping of job mappings"]
    for name in ("check", "release", "ci"):
        if name not in jobs:
            broken.append(f"ci.yml has no `{name}` job")
    if "ci" in jobs:
        ci = jobs["ci"] or {}
        if str(ci.get("if", "")).replace("${{", "").replace("}}", "").strip() != "always()":
            broken.append(f"ci job must run if: always(), found {ci.get('if')!r}")
        needs = ci.get("needs") or []
        needs = {needs} if isinstance(needs, str) else set(needs)
        missing = sorted(set(jobs) - {"ci"} - needs)
        if missing:
            broken.append(f"ci job must need every other job, missing {missing}")
    if "check" in jobs:
        matrix = ((jobs["check"] or {}).get("strategy") or {}).get("matrix")
        commented = any(
            line.lstrip().startswith("#") and "macos-15" in line
            for line in job_block(text, "check").splitlines()
        )
        if "macos-15" not in strings(matrix) and not commented:
            broken.append("check matrix lacks macos-15 and no comment line in the check job says why")
    return broken


def check_publish(data: dict) -> list[str]:
    broken = pull_request_only(data, {"closed"})
    jobs = data.get("jobs") or {}
    if not isinstance(jobs, dict) or not all(isinstance(j, dict) for j in jobs.values()):
        return broken + ["publish.yml jobs must be a mapping of job mappings"]
    roots = [name for name, job in jobs.items() if not (job or {}).get("needs")]
    if not roots:
        broken.append("publish.yml has no job")
    for name in roots:
        if MERGED not in str((jobs[name] or {}).get("if", "")):
            broken.append(f"publish job `{name}` must be gated on {MERGED}")
    return broken


def check_others(workflows: Path) -> list[str]:
    if not workflows.is_dir():
        return []
    try:
        others = sorted(p for p in workflows.iterdir()
                        if p.suffix in (".yml", ".yaml") and p.name not in ("ci.yml", "publish.yml"))
    except OSError as err:
        raise CouldNotLook(f"{workflows}: {err}") from err
    broken = []
    if len(others) > 1:
        broken.append(f"at most one workflow beside ci.yml and publish.yml, found {[p.name for p in others]}")
    for path in others:
        on = triggers(load(path)[0])
        # GitHub takes the triggers as a name, a list of names or a mapping.
        names = {on} if isinstance(on, str) else set(map(str, on)) if isinstance(on, (dict, list)) else None
        if names != {"workflow_dispatch"}:
            found = sorted(names) if names is not None else on
            broken.append(f"{path.name}: triggers must be workflow_dispatch alone, found {found}")
    return broken


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    try:
        paths = plugin_paths(root)
    except CouldNotLook as err:
        print(f"NOT CHECKED: {err}")
        return 2
    failed = unchecked = 0
    for rel in paths:
        plugin = root / rel
        if not plugin.is_dir() or not any(plugin.iterdir()):
            print(f"NOT CHECKED {rel}: submodule not checked out")
            unchecked += 1
            continue
        broken = []
        try:
            for name, check in (("ci.yml", check_ci), ("publish.yml", check_publish)):
                path = plugin / ".github/workflows" / name
                if not path.is_file():
                    broken.append(f"{name} is missing")
                    continue
                data, text = load(path)
                broken += [f"{name}: {b}" for b in (check(data, text) if name == "ci.yml" else check(data))]
            broken += check_others(plugin / ".github/workflows")
        except CouldNotLook as err:
            print(f"NOT CHECKED {rel}: {err}")
            unchecked += 1
            continue
        for b in broken:
            print(f"FAIL {rel}: {b}")
        if broken:
            failed += 1
        else:
            print(f"PASS {rel}")
    print(f"plugin workflows: {len(paths) - unchecked} checked, {failed} failed, {unchecked} not checked")
    return 2 if unchecked else (1 if failed else 0)


if __name__ == "__main__":
    sys.exit(main(sys.argv))
