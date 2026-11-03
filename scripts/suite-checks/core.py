"""Shared readers and the three-state check protocol; all repository access is read-only."""

import csv
import fnmatch
import hashlib
import io
import re
import subprocess
from collections import Counter
from datetime import date
from pathlib import Path


class Unavailable(Exception):
    """The check could not inspect its inputs."""


class Violation(Exception):
    """Inspected inputs violate the contract."""


def require(condition, detail):
    if not condition:
        raise Violation(detail)


def nonempty(items, detail):
    if not items:
        raise Unavailable(detail)
    return items


def evaluate(context, name, check):
    try:
        return "PASS", check(context)
    except Violation as error:
        return "FAIL", str(error)
    except (Unavailable, OSError, UnicodeError, csv.Error, ValueError) as error:
        return "ERROR", str(error)
    except Exception as error:
        return "ERROR", f"{name} reader failed: {type(error).__name__}: {error}"


def entries(text):
    return [line for line in text.splitlines() if line.strip() and not line.startswith("#")]


def tier(row):
    return row.split(":", 1)[0] if row.startswith("tier-") else "live"


def signature_key(signature):
    # Source identity and statement bytes stay bound without copying fixture paths or transcripts.
    return "sha256:" + hashlib.sha256(signature.encode()).hexdigest() if "\t" in signature else signature


class Context:
    def __init__(self, root, suite=None, pfm=None, close=False):
        self.root = Path(root).resolve()
        self.suite = Path(suite) if suite else self.root / "docs/design/pfm-integration-suite"
        self.spell = self.root / "scripts/suite-checks"
        self.runs = self.suite / "runs"
        self.pfm = Path(pfm) if pfm else self.root / "pfm/bin/pfm"
        self.close = close
        self._base = None
        self._files = None

    def path(self, path):
        path = Path(path)
        return path if path.is_absolute() else self.root / path

    def read(self, path, nonempty=False):
        path = self.path(path)
        try:
            result = self.bytes(path).decode()
        except (OSError, UnicodeError) as error:
            raise Unavailable(f"unreadable {path}: {error}") from error
        if nonempty and not result.strip():
            raise Unavailable(f"empty {path}")
        return result

    def bytes(self, path):
        path = self.path(path)
        try:
            return path.read_bytes()
        except OSError as error:
            raise Unavailable(f"unreadable {path}: {error}") from error

    def lines(self, path, nonempty=False):
        result = entries(self.read(path))
        if nonempty and not result:
            raise Unavailable(f"empty entries in {path}")
        return result

    def tsv(self, path, columns=None, header=None, text=None):
        rows = list(csv.reader(io.StringIO(self.read(path) if text is None else text), delimiter="\t"))
        rows = [row for row in rows if row and not row[0].startswith("#")]
        if header and rows and (rows[0] == header or rows[0][0] == header):
            rows = rows[1:]
        if columns is not None:
            for number, row in enumerate(rows, 1):
                if len(row) != columns:
                    raise Unavailable(f"malformed {path} row {number}: expected {columns} fields, got {len(row)}")
        return rows

    def command(self, args, cwd=None):
        if args[0] == "git":
            args = ["bash", "-c", 'source "$1" && shift && repo_git "$@"', "suite-checks",
                    str(Path(__file__).resolve().parents[2] / "pfm/scripts/repo-git.sh"), *args[1:]]
        try:
            result = subprocess.run(args, cwd=cwd or self.root, text=True,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=180)
        except (OSError, subprocess.TimeoutExpired) as error:
            raise Unavailable(f"could not run {args[0]}: {error}") from error
        if result.returncode:
            raise Unavailable(f"command failed ({result.returncode}): {' '.join(map(str, args))}: {result.stderr.strip()}")
        return result.stdout

    def base(self):
        if self._base is None:
            self._base = nonempty(self.command(["git", "merge-base", "HEAD", "main"]).strip(),
                                  "merge base unreadable")
        return self._base

    def at(self, path):
        path = self.path(path)
        relative = str(path.relative_to(self.root))
        listing = self.command(["git", "ls-tree", self.base(), "--", relative])
        if not listing:
            return self.read(path)
        return self.command(["git", "show", f"{self.base()}:{relative}"])

    def policy(self, path, excuses=False):
        old, new = Counter(entries(self.at(path))), Counter(self.lines(path))
        return list((old & new if excuses else old | new).elements())

    def policy_rows(self, path, columns=None, header=None, excuses=False):
        old = Counter(tuple(row) for row in self.tsv(path, columns, header, self.at(path)))
        new = Counter(tuple(row) for row in self.tsv(path, columns, header))
        return [list(row) for row in (old & new if excuses else old | new).elements()]

    def value(self, key):
        path = self.suite / "design.md"
        readings = []
        for text in (self.read(path), self.at(path)):
            match = re.search(rf"^{re.escape(key)}: ([0-9]+)$", text, re.M)
            if not match:
                raise Unavailable(f"{key} not declared in {path}")
            readings.append(int(match[1]))
        return min(readings)

    def setting(self, key):
        match = re.search(rf"^{re.escape(key)}: (.+)$", self.read(self.suite / "design.md"), re.M)
        if not match:
            raise Unavailable(f"{key} not declared")
        return match[1]

    def files(self):
        if self._files is None:
            excludes = [row.split("\t")[0] for row in self.policy(self.suite / "exclusions.txt", True)
                        if row.endswith("\tscope")]
            paths = self.command(["git", "ls-files", "-z", "-co", "--exclude-standard"]).split("\0")
            self._files = sorted({path for path in paths if path and self.path(path).is_file()
                                  and not any(fnmatch.fnmatchcase(path, glob) for glob in excludes)})
        return self._files

    def records(self, filename, pattern):
        result = {}
        for line in self.read(self.suite / filename).splitlines():
            fields = line.split(" · ")
            if len(fields) < 2 or not re.fullmatch(pattern, fields[0]):
                continue
            key = fields[0]
            require(key not in result, f"duplicate {filename} id: {key}")
            record = {"ids": fields[-1].split(), "writer": fields[-1], "text": line}
            for field in fields[1:]:
                name, separator, value = field.partition(":")
                if separator:
                    record[name] = value
            result[key] = record
        return nonempty(result, f"DERIVE-FAILED: no rows read from {filename}")

    def beats(self):
        return self.records("beats.md", r"(?:tier-(?:unit|hermetic):|[a-z][a-z0-9-]*/)[^ ]+")

    def landscape(self):
        return self.records("landscape.md", r"[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*")

    def mapping(self):
        return nonempty(self.tsv(self.suite / "map.tsv", 3, "landscape_id"), "DERIVE-FAILED: empty map.tsv")

    def references(self):
        return {parts[0]: parts[1:] for row in self.policy(self.suite / "references.tsv", True)
                if len(parts := row.split("\t")) == 3}

    def ratchet(self, name, signatures, scanned):
        nonempty(scanned, "zero files scanned")
        path = self.spell / f"{name}.baseline"
        limits = []
        for text in (self.read(path), self.at(path)):
            ceilings = [(day, int(count)) for day, count in
                        re.findall(r"^# ceiling (\d{4}-\d{2}-\d{2}) (\d+)$", text, re.M)
                        if day <= date.today().isoformat()]
            nonempty(ceilings, f"baseline {path} has no ceiling in force")
            limits.append(max(ceilings)[1])
        baseline = Counter(self.policy(path, True))
        originals = {signature_key(signature): signature for signature in signatures}
        excess = Counter(signature_key(signature) for signature in signatures) - baseline
        require(not excess, "beyond baseline: " + "; ".join(f"{originals[sig]} (+{n})" for sig, n in excess.items()))
        require(baseline.total() <= min(limits), f"baseline holds {baseline.total()}, above ceiling {min(limits)}")
        return f"{scanned} scanned, {len(signatures)} signatures, ceiling {min(limits)}"

    def body(self, row):
        if tier(row) == "live":
            from source_analysis import lane_beats
            bodies = [declaration["text"] for declaration in lane_beats(self)
                      if declaration["row"] in (row, row.split("@", 1)[0])]
            return "\n".join(nonempty(bodies, f"no literal beat declaration for {row}"))
        paths = [p for p in self.files() if p.endswith(("_test.go", ".txtar"))]
        token = row.split(":", 1)[1]
        matches = [(path, self.read(path)) for path in paths if token in self.read(path)]
        nonempty(matches, f"no test body for {row}")
        return "\n".join(text for _, text in matches)

    def subject(self):
        # SHA-256 over the index's path/mode/blob records; no temporary index or git write.
        excluded = [line.split("\t")[0] for line in self.policy(self.suite / "exclusions.txt", True)
                    if line.endswith("\thash")]
        records = self.command(["git", "ls-files", "--stage", "-z"]).split("\0")
        kept = [record for record in records if record and
                not any(fnmatch.fnmatchcase(record.split("\t", 1)[-1], pat) for pat in excluded)]
        return hashlib.sha256("\0".join(sorted(kept)).encode()).hexdigest()

    def dirty(self):
        excluded = [line.split("\t")[0] for line in self.policy(self.suite / "exclusions.txt", True)
                    if line.endswith("\thash")]
        def included(paths):
            return sorted({path for path in paths if path and
                           not any(fnmatch.fnmatchcase(path, pattern) for pattern in excluded)})
        changed = included(self.command(["git", "diff", "--name-only", "-z"]).split("\0"))
        untracked = included(self.command(["git", "ls-files", "--others", "--exclude-standard", "-z"]).split("\0"))
        diff = self.command(["git", "diff", "--binary", "--", *changed]).encode() if changed else b""
        for path in untracked:
            diff += f"untracked {path}\0".encode() + self.bytes(path) + b"\0"
        return hashlib.sha256(diff).hexdigest() if diff else ""
