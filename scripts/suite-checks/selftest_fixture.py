"""Run-unique fixtures; the git read seam avoids creating or mutating any repository."""

from datetime import datetime, timezone
import csv
import io
import json
from pathlib import Path

from core import Context, Unavailable, evaluate


ROW = "chat-birth/new-from-cli@linux+tmux+project+claude"
ID = "chat.new"
LANE = "infra/fence/journey/lanes/chat-birth.sh"


class FixtureContext(Context):
    def __init__(self, root, **kwargs):
        super().__init__(root, **kwargs)
        self.before = {}
        self.changes = ""
        self.git_unreadable = False

    def command(self, args, cwd=None):
        if args[0] != "git":
            return super().command(args, cwd)
        if self.git_unreadable:
            raise Unavailable("merge base unreadable")
        if args[1] == "merge-base":
            return "fixture-base\n"
        if args[1] == "ls-tree":
            return args[-1] if args[-1] in self.before else ""
        if args[1] == "show":
            return self.before[args[2].split(":", 1)[1]]
        if args[1] == "diff":
            return "" if "--name-only" in args else self.changes
        if args[1] == "ls-files":
            if "--others" in args:
                return ""
            files = sorted(str(path.relative_to(self.root)) for path in self.root.rglob("*") if path.is_file())
            if "--stage" in args:
                return "\0".join(f"100644 {'0' * 40} 0\t{path}" for path in files) + "\0"
            return "\0".join(files) + "\0"
        raise AssertionError(f"unexpected fixture git read: {args}")


def put(root, path, text):
    destination = Path(root) / path
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(text)
    return destination


def seed(root):
    c = FixtureContext(root)
    suite = c.suite.relative_to(root)
    spell = c.spell.relative_to(root)
    design = ("target-sequence-ms: 1000\nbudget-headroom-pct: 20\nsize-ceiling: 400\n"
              "clone-threshold-tokens: 80\nschedule-window-days: 7\nlive-os: linux\n"
              "blocking-command-live: infra/fence/journey/run.sh --root reuse\n"
              "double-run-paths: pfm/internal/config/\n")
    values = {
        "design.md": design,
        "landscape.md": f"{ID} · opens a chat · surface:chat · kind:success · entry:cli · op:chat.new · ruled:no · needs:none · today:unit · pfm/cmd/pfm/ops.go:1\n",
        "beats.md": f"{ROW} · do:open a chat · assert:name equals scout · source:fixture · breaks-if:wrong name · live-because:process boundary · cost_ms:10 · spends:none · forecast-factors:engine=claude · {ID}\n",
        "map.tsv": f"landscape_id\trow\treason\n{ID}\t{ROW}\tlive-only\n",
        "pending.txt": ROW + "\n",
        "dimensions.tsv": "os\tlinux\tpfm/cmd/pfm/ops.go:1\nterminal\ttmux\tpfm/cmd/pfm/ops.go:1\nplace\tproject\tpfm/cmd/pfm/ops.go:1\nengine\tclaude\tpfm/cmd/pfm/ops.go:1\n",
        "cuts.tsv": "", "references.tsv": "fixture\tfixture.txt\tname\n",
        "exclusions.txt": "scripts/suite-checks/corpus-*\tscope\ndocs/*\thash\n",
        "roster.txt": "owner\n", "skip-reasons.txt": "unobservable-file-mode\n",
        "unobservable.txt": "generated-id\n", "ruled-copy.tsv": "none\n",
        "gaps.tsv": "", "budgets.tsv": "", "escapes.tsv": "", "retired.tsv": "",
        "bites.tsv": "", "flakes.tsv": "", "self-tests.txt": "",
        "forecast-src.tsv": "fixture\tC1\t1\tengine=claude\twrong-state\tUNHELD\n",
        "forecast.tsv": f"forecast\tcell\trank\tfactors\tworst_outcome\theld\tbeat\nfixture\tC1\t1\tengine=claude\twrong-state\tUNHELD\t{ROW}\n",
    }
    for name, text in values.items():
        put(root, suite / name, text)
    put(root, "fixture.txt", "name: scout\n")
    put(root, "pfm/cmd/pfm/ops.go", "package main\n")
    put(root, "pfm/internal/chat/new_test.go", 'package chat\nfunc TestNew(t *testing.T) { if got != "scout" { t.Fatal(got) } }\n')
    put(root, LANE, f'beat {ROW}\nassert chat.new name scout "$actual"\n')
    for name, text in {"exclusion.go": "^internal\\.\n", "own-identifiers.txt": "example-product\n",
                       "assertion.sh": "^[[:space:]]*assert \n", "third-party-rules.txt": "",
                       "test-patterns.txt": "_test\\.go$\n", "config-keys.txt": "backend\n"}.items():
        put(root, spell / name, text)
    binary = put(root, "pfm/bin/pfm", "#!/bin/sh\nprintf 'chat.new\\n'\n")
    binary.chmod(0o700)
    return c


def judge(c, name, check, expected, detail=None):
    actual = evaluate(c, name, check)
    if detail is not None:
        assert actual == (expected, detail), f"expected {(expected, detail)!r}, actual {actual!r}, root {c.root}"
    else:
        assert actual[0] == expected, f"expected {expected!r}, actual {actual!r}, root {c.root}"
    return actual


def write_run(c, name, rows=None, **headers):
    fields = {"command": "fixture", "mode": "sequence", "boot": "process", "runner_pid": "10",
              "order": "canonical", "config": "base", "root": "root", "tree": c.subject(),
              "dirty": "", "start": datetime.now(timezone.utc).isoformat(), "load": "fixture"}
    fields.update(headers)
    fields.setdefault("tree_end", fields["tree"])
    fields.setdefault("dirty_end", fields["dirty"])
    records = rows if rows is not None else [[ROW, "PASS", ID, "scout", "chat.new", "", "10", "0"]]
    text = "\n".join(f"{key}: {value}" for key, value in fields.items())
    text += "\nrow\tverdict\tassertions-by-id\texpected\tops\tgoldens\twall_ms\tt+s\n"
    table = io.StringIO()
    csv.writer(table, delimiter="\t", lineterminator="\n").writerows(records)
    text += table.getvalue()
    events = [{"run": Path(name).stem, "beat": row[0], "op": "action.start", "action": op, "pid": 11}
              for row in records for op in row[4].split(",") if op]
    put(c.root, c.runs / (Path(name).stem + ".activity.jsonl"),
        "\n".join(json.dumps(event) for event in events) + "\n")
    return put(c.root, c.suite / "runs" / name, text)
