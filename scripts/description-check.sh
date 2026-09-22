#!/usr/bin/env bash
set -euo pipefail

# Frontmatter + description-budget gate over every tracked markdown file.
#
# Why a gate and not a rule: `/quality:description` states the law, but a prompt
# rule cannot detect a `description:` whose unquoted `: ` breaks the YAML — the
# Claude Code parser is lenient and registers the command anyway, while a
# stricter runtime silently drops it. `rumdl check` does not look at frontmatter
# validity either. This is the only thing that does.
#
# What this reports when IT is broken, each on its own exit code and message:
#   exit 2  python3 or PyYAML is missing — TOOLCHAIN-MISSING, never a pass
#   exit 3  the scan matched no files at all — the SCAN is broken, not the tree
#   exit 4  git could not locate or list the repository — no frontmatter was parsed
#   exit 1  at least one frontmatter does not parse (the failure it exists for)
#   exit 0  every frontmatter parses; the budget ledger is INFO only
#
# The char budget is reported, never enforced: `/quality:description` § Budget
# allows a justified 400-char tier, and a justification is not machine-readable.

usage() {
  echo "usage: description-check.sh [--files <path>...]" >&2
  echo "  no args: every tracked *.md in the repo" >&2
}

# Inside the dev fence a worktree's .git file names a host path the container
# cannot see; dev.sh hands the real git dir and work tree over as a pair, the
# same route scripts/leak-check.sh takes.
if [[ -n "${PFM_DEV_REPO_GIT_DIR:-}" || -n "${PFM_DEV_REPO_WORK_TREE:-}" ]]; then
  if [[ -z "${PFM_DEV_REPO_GIT_DIR:-}" || -z "${PFM_DEV_REPO_WORK_TREE:-}" ]]; then
    echo "description-check: PFM_DEV_REPO_GIT_DIR and PFM_DEV_REPO_WORK_TREE must be set together; no frontmatter was parsed" >&2
    exit 4
  fi
  repo_git() {
    git --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree="$PFM_DEV_REPO_WORK_TREE" \
      -c safe.directory="$PFM_DEV_REPO_WORK_TREE" "$@"
  }
else
  repo_git() { git "$@"; }
fi
if ! repo_root="$(repo_git rev-parse --show-toplevel)" || [[ -z "$repo_root" ]]; then
  echo "description-check: SCAN-BROKEN — git could not locate the repository; no frontmatter was parsed" >&2
  exit 4
fi
cd "$repo_root"

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  usage
  exit 0
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "description-check: TOOLCHAIN-MISSING — python3 is absent; no frontmatter was parsed" >&2
  exit 2
fi
if ! python3 -c 'import yaml' >/dev/null 2>&1; then
  echo "description-check: TOOLCHAIN-MISSING — PyYAML is absent; no frontmatter was parsed" >&2
  exit 2
fi

if ! list_file="$(mktemp)"; then
  echo "description-check: SCAN-BROKEN — mktemp failed; no frontmatter was parsed" >&2
  exit 4
fi
trap 'rm -f "$list_file"' EXIT

if [[ "${1:-}" == "--files" ]]; then
  shift
  printf '%s\n' "$@" > "$list_file"
else
  if ! repo_git ls-files '*.md' > "$list_file"; then
    echo "description-check: SCAN-BROKEN — git ls-files failed; no frontmatter was parsed" >&2
    exit 4
  fi
fi

python3 - "$list_file" <<'PY'
import pathlib, re, sys, yaml

# A template's frontmatter still carries its install placeholders, and a token in
# value position ({project}-testing-manual) opens a YAML flow mapping that the
# adopter's substituted file never contains. Parse the raw text first; only when
# THAT fails do we retry with the tokens filled, so this stays strict for every
# file that has no placeholder to blame. docs/PLACEHOLDERS.md rules which tokens
# are legal; the placeholder-registry gate enforces that, not this one.
TOKEN = re.compile(r"\{[A-Za-z][A-Za-z0-9_]*\}")


def parse_front(front):
    """Return (mapping, error, substituted). error is None when it parsed."""
    try:
        return yaml.safe_load(front), None, False
    except yaml.YAMLError as raw_err:
        if not TOKEN.search(front):
            return None, str(raw_err).split("\n")[0], False
        try:
            filled = yaml.safe_load(TOKEN.sub("placeholder", front))
        except yaml.YAMLError as filled_err:
            return None, (
                f"{str(filled_err).split(chr(10))[0]} (still unparseable with its "
                "placeholders filled — not a placeholder artifact)"
            ), True
        return filled, None, True


listing = pathlib.Path(sys.argv[1]).read_text().split()
scanned = withfront = 0
broken, ledger = [], []
for name in listing:
    path = pathlib.Path(name)
    if not path.is_file():
        continue
    scanned += 1
    text = path.read_text(errors="replace")
    if not text.startswith("---\n"):
        continue
    end = text.find("\n---\n", 3)
    if end == -1:
        broken.append((name, "frontmatter fence is never closed"))
        continue
    withfront += 1
    front = text[4:end + 1]
    parsed, err, _ = parse_front(front)
    if err is not None:
        broken.append((name, err))
        continue
    if not isinstance(parsed, dict):
        broken.append((name, "frontmatter is not a mapping"))
        continue
    description = parsed.get("description")
    if isinstance(description, str):
        ledger.append((len(description.strip()), name))

if scanned == 0:
    print("description-check: NOTHING SCANNED — the file list was empty; the SCAN is broken, not the tree")
    sys.exit(3)

total = sum(n for n, _ in ledger)
print(f"description-check: {scanned} tracked .md, {withfront} with frontmatter, {len(ledger)} descriptions, {total} chars total")
if ledger:
    ledger.sort(reverse=True)
    over = [(n, f) for n, f in ledger if n > 400]
    print(f"  budget: {len(over)} over the 400-char tier · heaviest:")
    for n, f in ledger[:5]:
        print(f"    {n:5}  {f}")
if broken:
    print(f"description-check: {len(broken)} frontmatter block(s) do NOT parse as YAML — a lenient harness registers them, a strict one drops them:")
    for f, why in broken:
        print(f"    {f} :: {why}")
    sys.exit(1)
print("description-check: every frontmatter parses")
PY
