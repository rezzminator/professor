---
name: quality:llm-codebase
description: Lays out an agent-maintained tree — `/quality:llm-codebase <feature|LLM call|project|path>`, "where should X live", "set up lint/format/clone/naming gates", before `/flights:spec` on a feature touching 3+ directories, every new LLM call; greenfield designs, brownfield measures then migrates. Returns a design document with its gates; edits no code. Integration tests → /quality:integration-suite.
argument-hint: <feature|LLM call|project|path>
---

# LLM Codebase

Design for a reader whose context resets every session: one change lands in one directory, the first grep finds the name, nothing is re-implemented under a second name, and a script — never a reviewer's memory — holds every law that can be counted. The design covers the reader's whole path — the orientation files it loads, the brief it receives, the evidence it consults, the source it edits, the receipts it writes — since a hop spent before the first source read costs the same as a hop inside the source tree. Any language, any project size; the examples name file types only to be concrete.

## Contents

1. Inputs
2. Metrics — what good means
3. Procedure — twelve steps, tree to brief
4. Mechanical gates — the protocol, the check classes, adoption
5. Output — the design document
6. Checks — the script skeleton
7. Hand-off

## Inputs

- target: a feature, an LLM call, a project, or a path
- mode: greenfield (nothing exists) or brownfield (a tree exists — measure before designing)
- the project's orientation file (CLAUDE.md or equivalent) and the live tree (`ls`, `git ls-files` — read, never recalled)
- evidence and baselines the project already keeps: an architecture playbook, earlier measurements, the agents' own session transcripts

## Metrics — what good means

- dirs-per-change: directories touched by one change → one dominant directory
- first-edit hops, source-only: navigation calls from the first source-file read to the first edit → ≤ 3
- orientation hops: orientation, brief, progress and evidence reads before the first source-file read → reported beside the source-only number; the brief (step 11) is the lever that moves it
- re-reads: files opened ≥ 2× in one session → ≤ 2 files
- zero-result searches per session → ≈ 0
- new file written without a prior search for its concept → 0
- new violations per change, per check of § Mechanical gates → 0; baselined violations → shrinking

Measure, never estimate. dirs-per-change: `git log --no-merges --name-only` over a stated window — distinct directories per commit, as median · p75 · p90; co-change (step 1) from the same log — file pairs sharing commits. The hop, re-read and search metrics are counted from the agent runtime's session transcripts (its tool-call records: reads, searches, edits) for sessions that edited the target; the owner names where they are kept. A metric with no data source is reported `UNMEASURED — <what is missing>` and its target stands as a design goal; greenfield marks expected co-change "assumed".

## Procedure

Brownfield runs step 10's measurement before step 1, with the skeleton's pattern-free checks (size, negation directories, test mirror, duplicate functions); the façade and spelling checks join once steps 4–5 name their patterns. Steps 1–9 then design the target tree, and step 10 writes the migration. Greenfield skips step 10.

1. Partition by unit of change. List every concept the target contains; group by what changes together (a prompt + its schema + its call + its store + its test co-change; two features that never co-change are two units). Each unit owns one directory.
2. Order the tree by flow. Where units form a pipeline, prefix the directories so `ls` prints the execution order (`s1_ingest/`, `s2_parse/` — a leading letter where the language's module names cannot start with a digit); support packages sit beside the flow, named by what they are (`llm/`, `transcript/`, `guards/`, `db/`). A directory named by negation (`utils`, `helpers`, `common`, `misc`, `shared`) is rejected — each file in it is placed with the unit that changes with it, or in a package named by its content.
3. Fix the anatomy. Every unit directory holds the same file set, so a missing file is a visible gap rather than a search. For an LLM call, e.g.: `README.md` · `prompt.md` (prompt text) · `input.*` (typed input and hyperparameters) · `call.*` (the invocation, through the LLM façade) · `verify.*` (output schema and validation) · `store.*` (persistence) · the file wiring the unit into its caller (`node.*` in a graph runtime, `handler.*` behind a route) — all in the one directory; the path is the registry. README is hand-written, ≤ 20 lines: what the unit does, what feeds it, what it feeds.
4. Name grep-true. One canonical term per concept, identical in filename, exported identifier, call-site variable, wire key, environment variable and test name; the glossary lists each term with its rejected variants (the glossary feeds the spelling census of § Mechanical gates). Generic identifiers (`create`, `handler`, `process`, `data`, `helpers`) are replaced by the concept's word; file names in a flow carry their order.
5. Façade the cross-cutting. Each mechanism the project has — process execution, database open, atomic file write, environment reads, clock, metrics, LLM invoke, feature flags, logging, the test scratch root, and any domain guard every unit must pass (consent, erasure, provenance) — gets one module; units call the façade, never the primitive. A second surface (an API, a tool server, a UI) calls the unit's typed function; assembling the first surface's argv or parsing its printed output is a primitive like any other, counted per file.
6. Remove parallel lists. The path is the registry, or one source generates every mirror. A generated mirror is built at install or build time and stays out of version control; where a consumer must read it from a fresh clone, it is tracked and CI regenerates-and-diffs it. A hand-maintained twin (a JSON registry beside a directory listing, a DI container list, a lint allowlist holding path strings, two locale files without a key-parity check, a usage text beside a dispatch table) is redesigned out. Tooling that agents write repeatedly — a test runner, a receipt recorder, a result parser, a declared-vs-executed census — is a versioned script in the project's `scripts/`; an agent session writing its own copy under its work root is the same twin.
7. Mirror the tests. `tests/<same relative path as the source>`, the language's same-stem convention, or its in-file test module — one test home per source file, and the mirror check looks for that home (a file, or the test-module marker inside the source); a flat catch-all test directory is not admitted; one scratch-root helper (the test jail) owns every temp path a test creates.
8. Gate mechanically. Every law of steps 1–7 that a script can count becomes a check under § Mechanical gates, instantiated for this tree; ceilings and budgets come from the project's orientation file, and absent one are proposed under § Open rulings, each with the count of files over it at two candidate values (e.g. 800 lines → 12 files over, 600 → 31).
9. Align across projects. In a repository or product made of several projects, the same concept lives at the same relative path in every project (`env`, `config`, `tests`, `generated`); a primitive two projects need lives in one contract or schema package, which the others import or vendor and never re-type. A change crossing a wire boundary starts at that package's consumer index — the producer and consumer anchors per contract — and the design names that index as hop 1 of every consuming project's brief. A single-project target lists this step as not applicable.
10. Brownfield: measure first, then plan. Run § Checks in measure mode against the tree; list giant files, twin registries (files always co-edited), sibling directories owning one concept, LLM calls split across directories, flat test directories, negation-named packages, one-line re-export modules, same-named functions with different bodies, concepts with more than one spelling, and — from the agents' own transcripts — where the orientation hops go (which orientation, progress and evidence files every session opens before its first source read). Write the migration as ordered steps; rebuild in one worktree from a reuse manifest (one line per file: old path → new path · kept | rewritten | dropped) when more than half the tree moves, incremental otherwise; every step names the metric or the check baseline it moves. A step that moves a path re-anchors, in the same step, every document and queued spec citing it.
11. Design the brief — the task text a building agent receives. A task carries every fact the builder depends on as a quoted `file:line` anchor, the exact commands to run, and the consumer list from step 9 — a pointer to a progress file, notes or an evidence directory in place of the fact is a gap. The builder's first command opens its target file.
12. Fix the work tree's anatomy. One piece of work keeps its files under one root named after its brief, with a fixed file set — `SPEC.md` (the brief), `STATE.md` (progress), `receipts/` (captured gate and test output), `evidence/` (measurements and traces) — at most three levels deep; evidence a later piece of work needs is quoted into its spec (step 11), never reached by path.

## Mechanical gates

A law a script can count is enforced by that script on every change; prose repeats none of it.

### Protocol

- One script per repository holds every check that needs only a shell, git and POSIX tools — the free-function layer of Duplicates, Spelling census, Façade, Size, Structure, Registry parity, Orientation truth — reading the files git tracks or would add (`git ls-files -co --exclude-standard`, minus paths deleted in the work tree) under a pinned locale, so it runs identically in the edit loop, in a clean-environment run and in CI, and judges a new file before its first commit. In a repository of several projects it stays one file, run once per project root, with one baseline directory per project.
- Every check prints exactly one line: `CHECK <id> PASS|FAIL|ERROR <detail>`. PASS: the enumerator ran and found nothing beyond the baseline. FAIL: a violation the baseline lacks, named. ERROR: the enumerator could not run — baseline missing, a file unreadable, nothing parsed where something must parse. Exit 0 all PASS · 1 any FAIL · 2 any ERROR. In `--measure` mode the status is `MEASURE <count>`.
- Baselines are committed in one directory and only shrink. Two shapes: a list (a new entry fails) and counts per key, e.g. per file (a count above its baseline fails). `--measure` keeps only entries still violating and lowers counts to today's; it adds no entry and raises no count. With no baseline present, `--measure` writes the first one. A new exception is a hand edit to the baseline, named in its commit. A PASS names the entries the tree has already fixed, so the shrink gets locked.
- The script has its own tests, each failure path watched failing before its check existed.
- One gate target composes the chain and is the only definition of "ready to merge": format check → lint on changed lines → compiler or type check → these checks → tests → the same chain in a clean environment (a container or CI runner with its own home and no developer state).
- Format, Lint, the token-based clone detector, Coverage, Suite wall time and Publication are links of that chain run by their own tools, each under the same three-state contract. Tool versions are pinned in one versions file that the local install and the clean-environment image both read; a missing or crashing tool, or a scan that matched no files, is ERROR naming the tool and its install command — never a skipped link.

### Check classes

Instantiate every class that applies; a class that does not apply is listed as such in the design document.

- Format: one formatter configuration, no per-file options, import ordering and line wrapping included; a check mode that names the unformatted files; run once per platform where the formatter loads only the current platform's files. Where formatters rewrite each other's output, the rewrite mode runs until a pass is a no-op (three passes at most, then fails naming the fight); a formatter that did not run reports ERROR — no file was checked.
- Lint, two views: the whole-tree view is the burn-down list; the gate view reports findings only on lines changed since the merge base, so a strict linter set lands on an old tree without a flag day; where the linter has no changed-lines mode, the gate filters its machine-readable output against the line ranges of `git diff -U0 <merge base>`. Enable the linter's equivalent of each: unhandled errors · static-analysis bugs · unused and ineffectual code · naming style · a string literal spelled three times (an unnamed constant) · shape-based clone detection that survives renamed variables · a rule that every suppression names its linter and its reason — an item no linter of the language offers moves to the token-based layer or to § Open rulings. Report every finding (no per-linter cap), lint test files too, and exempt them only from the clone and repeated-literal rules.
- Duplicates, three layers: shape-based clones inside the typed language (the linter above) · a token-based clone detector over the scripts, configuration and template languages, compared against a committed fingerprint baseline (a clone the baseline lacks fails; a removed clone is a shrink the next measure locks in) · one free function per name across files (a top-level declaration, never a method), compared case-folded; excluded by an explicit list in the script: language-mandated names (`main`, `init`), names an interface makes every unit define (a plugin entry point, a framework hook), and files that define one identifier once per platform by design.
- Spelling census — consistent names for connected entities across modules: for each glossary concept, a pattern of its rejected variants counted per file. Instances: a product, engine or vendor name and its abbreviations · one environment-variable prefix, so a grep for the prefix finds every knob · one identifier for a shared directory or home · a storage file name spelled as a literal at most once · wire keys matching the identifiers that carry them.
- One façade per mechanism: for each façade of step 5, the primitive's call pattern outside the façade's module, counted per file.
- Size: a line ceiling for source files and one for test files, offenders listed (one ceiling where tests live inside the source file); a small fixed slack lets a baselined file absorb the import line a moved symbol costs, while logic growth fails. A line budget for the entry-point package, lowered as each extraction lands, keeps dispatch thin. Generated output over the ceiling is split per type or operation at the generator.
- Structure: negation-named directories · a stated purpose per package or module (doc comment or README) · a test file per source file, untested sources listed · one test scratch-root helper, hand-rolled temp roots counted per file. · scripts under a work root (step 12) outside `scripts/`, listed
- Registry parity: every dispatched command, route, job or tool appears in the usage, help or schema that describes it — checked by script until one table generates both.
- Orientation truth: every package, document, command, agent and environment variable the orientation files cite exists, and a cited variable is read by production code.
- Coverage: total and per-package floors, raised only after the measured number reaches the next integer.
- Suite wall time: a budget per test package and per suite with a tolerance factor, lowered by measure; an unbudgeted package fails; a run containing a failed test is reported as failed, never timed.
- Publication: a scan of every changed file for secrets, machine-absolute paths and the terms of one committed pattern file, reporting the count of files scanned beside its verdict.

### Adoption on an existing tree

Measure, commit the baselines, and gate "no new violation" from the first day; each later change shrinks the entries of the files it touches and re-measures. A change that adds logic to an over-ceiling file splits it first; a change that only re-points an import rides the slack.

## Output — the design document

Written to the path the request names; absent one, `<the project's design-doc directory, else docs/design>/<target>-layout.md`, stated in the reply. Sections in this order:

1. Units of change — table: unit · concepts it owns · directory · co-change evidence
2. Tree — the full target tree, one line per file, the fixed anatomy visible; for several projects, the aligned paths and the contract package with its consumer index (step 9)
3. Glossary — concept → canonical term → rejected variants → where it appears (file, identifier, wire key, environment variable, test)
4. Façades — concern → module → the primitive it hides
5. Registries — every derived artifact → its source → the regeneration command → tracked or built
6. Gates — table: check id · class · what it asserts · what its broken state reports · baseline file; then the script itself, and the gate target's chain
7. Metrics — the baseline today and the target after the build; hops reported as the pair source-only · orientation; per-check counts; the named gaps of the measurement (sessions or history excluded, counts that are heuristics)
8. Migration (brownfield) — ordered steps, rebuild vs incremental, what dies, which baseline each step shrinks
9. Brief template and work tree — the anchor, command and consumer-list slots every task carries (step 11); the work root's fixed file set (step 12)
10. Open rulings — every number or trade-off the owner decides (ceilings, budgets, rebuild vs incremental, which twins die first, which linters join the gate view); each ruling states the recommendation first, then the alternative with its measured cost

## Checks — the script skeleton

```sh
#!/usr/bin/env bash
set -uo pipefail; export LC_ALL=C
P=<project root>; BASE=$P/<baseline dir>; MODE=${1:-check}; rc=0
case $MODE in check|--measure) ;; *) echo "usage: $0 [--measure]" >&2; exit 2;; esac
say() { printf 'CHECK %-22s %-7s %s\n' "$1" "$2" "$3"; case $2 in FAIL) [ $rc -ge 1 ] || rc=1;; ERROR) rc=2;; esac; return 0; }
cd "$P" || { say setup ERROR "cannot cd $P"; exit 2; }
T=$(mktemp -d) || { say setup ERROR "mktemp failed"; exit 2; }; trap 'rm -rf "$T"' EXIT; mkdir -p "$BASE"
all() { git ls-files -co --exclude-standard | while read -r f; do [ -f "$f" ] && echo "$f"; done; }   # tracked + new, minus deleted
src() { all | grep -E '<source pattern>' | grep -vE '<test pattern>'; }
[ "$(src | wc -l)" -gt 0 ] || { say setup ERROR "no sources listed under $P"; exit 2; }
# g <out> <grep args…>: grep over the sources; grep status 2 = could not read = ERROR, never an empty result
g() { local out=$1; shift; grep "$@" $(src) /dev/null > "$out"; [ $? -le 1 ]; }
by_file() { cut -d: -f1 | sort | uniq -c | awk '{print $2" "$1}'; }

# list ratchet: <id> <baseline name> <file of current offenders> — FAIL on a line the baseline lacks
ratchet() { local id=$1 b=$BASE/$2.txt cur=$3 new gone note=""; sort -u "$cur" -o "$cur"
  if [ "$MODE" = --measure ]; then
    if [ -f "$b" ]; then comm -12 "$b" "$cur" > "$T/m" && mv "$T/m" "$b"; else cp "$cur" "$b"; fi
    say "$id" MEASURE "$(wc -l < "$b") entries"; return; fi
  [ -f "$b" ] || { say "$id" ERROR "baseline $b missing — cannot tell new from old"; return; }
  new=$(comm -13 "$b" "$cur"); gone=$(comm -23 "$b" "$cur" | wc -l); [ "$gone" -gt 0 ] && note="; $gone fixed — run --measure to lock the shrink"
  if [ -n "$new" ]; then say "$id" FAIL "new: $(echo $new)"; else say "$id" PASS "$(wc -l < "$cur") baselined, 0 new$note"; fi; }
# count ratchet: <id> <baseline name> <file of "<key> <n>" lines> [slack] — FAIL on a new key or n above baseline+slack; measure lowers n, never raises
ratchet_counts() { local id=$1 b=$BASE/$2.txt cur=$3 slack=${4:-0} over; sort -u "$cur" -o "$cur"
  if [ "$MODE" = --measure ]; then
    if [ -f "$b" ]; then awk 'FILENAME==ARGV[1]{base[$1]=$2; next} ($1 in base){print $1, ($2<base[$1] ? $2 : base[$1])}' "$b" "$cur" | sort -u > "$T/m" && mv "$T/m" "$b"; else cp "$cur" "$b"; fi
    say "$id" MEASURE "$(awk '{s+=$2} END{print s+0}' "$b") in $(wc -l < "$b") keys"; return; fi
  [ -f "$b" ] || { say "$id" ERROR "baseline $b missing — cannot tell new from old"; return; }
  over=$(awk -v s="$slack" 'FILENAME==ARGV[1]{base[$1]=$2; next} !($1 in base){print $1" (new "$2")"; next} $2>base[$1]+s{print $1" ("base[$1]"->"$2")"}' "$b" "$cur")
  if [ -n "$over" ]; then say "$id" FAIL "$(echo $over)"; else say "$id" PASS "$(awk '{s+=$2} END{print s+0}' "$cur") in $(wc -l < "$cur") keys, none above baseline"; fi; }

src | while read -r f; do n=$(wc -l < "$f"); [ "$n" -gt <ceiling> ] && echo "$f $n"; done > "$T/size"; ratchet_counts size-src ceiling-src "$T/size" <slack>
all | awk -F/ '{p=""; for(i=1;i<NF;i++){p=p (i>1?"/":"") $i; print p}}' | sort -u | grep -iE '(^|/)([^/]*util[^/]*|helpers|common|misc|shared)$' > "$T/neg"; ratchet negation-dirs negation-dirs "$T/neg"
src | while read -r f; do [ -e "<test home for $f>" ] || echo "$f"; done > "$T/mirror"; ratchet test-mirror untested-sources "$T/mirror"
if g "$T/raw" -nE '<primitive call pattern>'; then grep -v '^<facade module>/' "$T/raw" | by_file > "$T/prim"; ratchet_counts facade-<name> facade-<name> "$T/prim"; else say facade-<name> ERROR "grep could not read sources"; fi
if g "$T/raw" -nE '<rejected spellings of one concept>'; then by_file < "$T/raw" > "$T/sp"; ratchet_counts spelling-<concept> spelling-<concept> "$T/sp"; else say spelling-<concept> ERROR "grep could not read sources"; fi
if g "$T/raw" -nE '<top-level function declaration: column 0 is free, an indented one is a method>'; then
  <print "name file" per hit> | awk '{print tolower($1)" "$2}' | grep -vE '^(<excluded names>) ' | sort -u | awk '{f[$1]=f[$1]" "$2; c[$1]++} END{for(n in c) if(c[n]>1) print n":"f[n]}' > "$T/dup"; ratchet dup-functions dup-functions "$T/dup"
else say dup-functions ERROR "grep could not read sources"; fi
<parse dispatched names> | sort -u > "$T/disp"; <parse usage or help names> | sort -u > "$T/use"
if [ ! -s "$T/disp" ]; then say registry-parity ERROR "no dispatched names parsed"
elif [ ! -s "$T/use" ]; then say registry-parity ERROR "no usage names parsed"
else comm -23 "$T/disp" "$T/use" > "$T/par"; ratchet registry-parity undocumented "$T/par"; fi
if [ ! -f "<orientation file>" ]; then say orientation-truth ERROR "<orientation file> missing"
else for n in $(<command listing the names the orientation file cites>); do <exists, and a cited variable is read by production code> || echo "$n"; done > "$T/dang"; ratchet orientation-truth dangling "$T/dang"; fi
if find <work root> -type f \( -name 'run.*' -o -name '*.sh' \) -not -path '*/scripts/*' > "$T/run"; then ratchet stray-runners stray-runners "$T/run"; else say stray-runners ERROR "find failed under <work root>"; fi
exit $rc
```

Adapt the globs, patterns and parsers to the tree; a check that cannot be expressed as a script is listed under § Open rulings, never asserted in prose. A tree with spaces in its paths swaps `$(src)` for a NUL-separated list.

## Hand-off

The design document is reviewed before anything is built: placement law, literal lists shadowing derived constants, coupled edits, checks whose broken state would read as PASS. In a project installed from this blueprint, `flights-speccer`'s reconcile phase performs that review, a design that changes what an LLM receives or returns goes through the project's RND gate, and code moves through `/flights:spec` and the project's executor. Elsewhere, the owner assigns the review and the build; this command writes the design only.
