# /quality:claude-md

`/quality:claude-md` is the law for a project's orientation file — `CLAUDE.md`, `AGENTS.md`, `GEMINI.md` or any engine's equivalent: the one text every session and every sub-agent reads before anything else. It fixes the file's spine, what earns a line in each section, the budget, and a gate that certifies a file; `write` drafts or restructures one to the law. Any project, any stack — nothing here assumes one framework.

## Contents

- [What it is for](#what-it-is-for)
- [The two metrics](#the-two-metrics)
- [Evidence](#evidence)
- [The spine](#the-spine)
- [Title and stake](#title-and-stake)
- [Vocabulary](#vocabulary)
- [Runtime](#runtime)
- [Rules](#rules)
- [Density — how a line carries more](#density--how-a-line-carries-more)
- [Budget](#budget)
- [Placement by scope](#placement-by-scope)
- [What never goes in](#what-never-goes-in)
- [Headings are an API](#headings-are-an-api)
- [Modes](#modes)
- [The truth script](#the-truth-script)
- [Relations](#relations)
- [Decisions](#decisions)

## What it is for

The orientation file is paid on every load: each main session and each sub-agent spawn reads it whole before its first tool call, so one line costs its bytes times every load for the life of the project. And it is the reader's only map: when it cannot say where a thing lives or how to run it, every reader spends round-trips — `ls`, grep, open, reject — rediscovering the same fact. The law designs for both at once.

## The two metrics

- Compact: bytes per load, measured with `wc -c` against § Budget.
- Useful: round-trips from a spoken term to its source. Target one — the reader finds the term in the file, and its next call opens the source. Measured over a sample of real asks (commit subjects, session transcripts): the reads spent before the first read of the right source file, the orientation-hops count `/quality:llm-codebase` defines.

Every rule below serves one of the two. A rule that serves neither is cut.

## Evidence

- Anthropic, Claude Code memory docs: "Size: target under 200 lines per CLAUDE.md file. Longer files consume more context and reduce adherence." · "write instructions that are concrete enough to verify … 'API handlers live in `src/api/handlers/`' instead of 'Keep files organized'" · "if two rules contradict each other, Claude may pick one arbitrarily" · "Keep it to facts Claude should hold in every session: build commands, conventions, project layout, 'always do X' rules." ([memory](https://code.claude.com/docs/en/memory))
- Same page, loading: files above the working directory load at launch, files in subdirectories "load on demand when Claude reads files in those directories"; `@path` imports "load at launch", so splitting by import "doesn't reduce context"; `.claude/rules/*.md` with a `paths:` frontmatter apply "only when Claude is working with files matching the specified patterns". Claude Code reads `AGENTS.md` only where no `CLAUDE.md` exists.
- ETH Zurich, [arXiv 2602.11988](https://arxiv.org/abs/2602.11988): "Repository overviews, although popular and recommended by model providers, are not helpful"; LLM-generated context files reduce success "by up to 3%" while "increasing inference cost by over 20%"; developer-written files improve success; "Human curation of context files proved essential".
- [arXiv 2601.20404](https://arxiv.org/abs/2601.20404): with `AGENTS.md` present, median agent runtime fell 28.64% and output tokens 16.58% (10 repositories, 124 pull requests).
- [arXiv 2511.12884](https://arxiv.org/abs/2511.12884): what developers write — test procedures 75.9%, implementation details 70.8%, architecture 68.1%; security 14.8% and performance 14.5% rarely.

What the evidence decides: a directory tour is the content that measurably fails, so the map is keyed by term, not by directory; the file is curated by a person, so `write` ends with its term list for the owner to strike; build and test commands earn their place (the content developers reach for first), and so does the sacred ground they most often forget.

## The spine

```
# {Project} — {what it is, one line}
{1–3 lines: what a careless edit here costs}

# Vocabulary
- {term}: {gloss} · `{source path}`[, `{path}`…] [· design `{doc path}`]
- {artifact}: {gloss} · generated from `{source}` by `{command}`
## Path vars
- `$VAR`: `{path}`

# Runtime
## {engine}
## {environment}

# Rules
## {sacred ground}
## {domain}
```

- Order is define-before-use: Runtime and Rules cite Vocabulary terms bare and never re-explain them, so each term costs its bytes once.
- `#` marks a spine section, `##` its children; a markdown linter's single-title and heading-level rules are exempted for the orientation file.
- A top-level section outside the spine exists only when its content fits no spine section and passes § Rules admission — typically a protocol every reader runs first (a sub-agent's first move), placed where that reader meets it.

## Title and stake

The first line names the project and says what it is in one clause. At most three lines follow, stating what a careless edit costs here — the one fact that changes how every reader weighs every action ("`templates/` is shipped source: each prompt line there is an adopter's production code"). No history, no feature list.

## Vocabulary

The project's nouns, each mapped to its source: the table that turns the words people use into a path in one lookup, and the one registry of canonical names.

### Entry grammar

- `- {term}: {gloss} · \`{path}\``, one line. Several homes: comma-separated paths. A design or reference doc: `· design \`{doc path}\`` after the source.
- The term is spelled as people and code spell it; a term that is a code identifier or a file name is written verbatim.
- The gloss says what the thing is or does in the fewest words that prevent a misreading; it never restates what its path already says.
- The value is the thing's source. A generated artifact names its source and its generator instead: `- {artifact}: {gloss} · generated from \`{source}\` by \`{command}\``.
- Paths are full — from the repository root, or from the file's own directory in a child file — never a fragment relative to an earlier mention. A path outside the tree is rooted at `$HOME` or `~`, never machine-absolute. A variable segment is braced: `.worktrees/{flight}/`, `releases/v{X.Y.Z}.md`.

### Admission

A term earns a line when people or prompts in the project use it and at least one of these holds:

- its home is not found by one `ls` or grep of the term itself;
- it means something here other than its everyday meaning;
- it is a top-level component (§ Closed world).

Excluded: a directory tour (path → contents — `ls` answers it); file-by-file descriptions; a single command, skill or agent the harness already indexes from its own `description:` — a family or tier of them is a term, a member is not; anything whose gloss the path already says.

### Closed world

Every top-level directory the repository tracks (a child file: every directory under its own) is covered by an entry whose path is that directory or one inside it. The Vocabulary is then complete at the top level: a term the lookup does not find is not a component of this project, and the reader stops searching instead of starting.

Closed world outranks the admission exclusions: a top-level directory whose name explains itself keeps its entry, and its gloss says only what the name does not.

### One term per concept

The Vocabulary spelling is the only spelling — in this file, in every prompt, doc, commit subject and identifier. A second name found elsewhere is renamed to the entry's term, or the entry is renamed end to end. Where `/quality:llm-codebase` has written a glossary, its canonical terms and homes are these entries; its rejected variants stay in the glossary, where the spelling census reads them.

### Path vars

`## Path vars` sits under `# Vocabulary` — the same name → path shape — and exists only when the project's prompts substitute `$VAR` tokens: `- \`$VAR\`: \`{path}\``.

## Runtime

Everything that executes the project's work, one `##` per executor. Each carries only what a reader cannot discover and needs before acting.

### Engines — one contract

- Exactly one orientation file is hand-edited. Every other engine reads it natively, imports it, or reads a mirror generated from it — never a second hand-edited copy, which drifts on the first edit. Claude Code reads `AGENTS.md` only where no `CLAUDE.md` exists, so a repository carrying both loads one per engine, not both.
- An engine gets a `##` only when it has project-specific mechanics: what it reads, how its mirror is generated and checked, its hooks or guards, its deltas from the shared contract. A project run by one engine with no such mechanics has no engine section.

### Environments

- One `##` per place the code builds, tests or runs — the local loop, an isolated container, CI, production.
- Each carries the exact command lines, copy-pasteable and run once at write time; the single entry point where one exists ("build and test only through `{script}`"); the ports, services and variables a command needs that the reader cannot discover; where scratch output goes.

## Rules

- Admission: the code cannot show it; no mechanism enforces it — or a hook does, and the rule is the one line naming the route so the reader does not spend a call on the deny; its absence causes a mistake; it is concrete enough to verify.
- The sacred-ground `##` comes first — secrets, personal data, publication, whatever the project marks sacred — and is the only place `NEVER` and `MUST` appear.
- Every other rule is phrased positively and names the tool, command or path to use, not only the thing to avoid.
- Grouped by domain under `##`; one rule per bullet; a bullet over two lines is two rules or a paragraph.
- Stated once across everything loaded with the file: a rule the harness's system or persona layer, or a parent orientation file, already carries is cut here.

## Density — how a line carries more

- One fact per line; prose only in the stake.
- Bullets, not tables: a bullet line is greppable by its term (`- {term}:`) and edits locally; a table adds a header, a separator row and a pipe per cell.
- A term defined in the Vocabulary is cited bare everywhere else.
- A path is its own explanation — never describe what its name already says.
- Compact notation for sets: `{status|build|test}` for a command's modes, `·` between fields.
- Bold only for a sacred-ground rule's opening words.
- Every line passes the `/quality:prompt` cut test; the prose law applies to every line.

## Budget

- A root orientation file: 200 lines and 16 KB. The line figure is the harness's published target; the byte figure is those 200 lines at 80 characters, because a line has no length limit and a file can meet 200 lines while carrying several times the bytes.
- A child file: the same budget, holding only its delta.
- Measured with `wc -l -c`; an import (`@path`) counts toward the file that imports it, since it expands at launch.

## Placement by scope

- The root file carries only what binds the whole repository — two or more of its projects.
- A child orientation file in a project directory carries that project's delta and loads only when the reader works there; a rule scoped to one project moves down into it.
- A rule bound to a file pattern goes to a path-scoped rule file where the engine supports one (Claude Code: `.claude/rules/*.md` with `paths:`).
- A child never restates its parent.

## What never goes in

| Content | Its home |
| --- | --- |
| Directory tour, file-by-file description | nothing — `ls` answers it |
| Roster of commands, skills or agents | their own `description:` — the harness indexes them |
| A procedure one task uses | a command or skill, loaded on match |
| Rationale, history, incidents, dates | a design doc or commit message |
| Voice, persona | the engine's persona or system-prompt layer |
| An invariant a machine can check | a hook, lint or test; the rule stays until one enforces it, then shrinks to the route |
| Reference data (schemas, API contracts) | `docs/`, cited from a Vocabulary entry |

## Headings are an API

Other prompts cite sections by name (`CLAUDE.md § Rules`). A heading renamed or removed while a citer remains is a dangling pointer: grep the tree for `§ {heading}` and for the bare heading text before renaming, and retarget every citer in the same pass.

## Modes

### Law — no argument

Loaded before any write to an orientation file; the reader applies the law as it writes.

### `check [path…]`

Certifies each file, read-only: runs § The truth script and the checks below, and emits `APPROVED: {path}`, `REJECTED: {path} — checks {n,…}`, or `UNREAD: {path} — {error}` when the file could not be read — never a verdict for a file it did not read. An `ERROR` line fails every check it left unrun. No path: the hand-edited orientation file at the repository root.

1. Spine: a spine section missing where it has content, out of order, or a top-level section that fits a spine section or fails admission.
2. Budget: over 200 lines or over 16 KB.
3. Path truth: a `MISSING`, `ABSOLUTE` or `ERROR` line from the truth script.
4. Closed world: an `UNCOVERED` line from the truth script.
5. Admission: a tour or file-by-file entry, a single self-indexed command, skill or agent, a gloss that restates its path.
6. One term: one concept under two names, or a term used elsewhere in a spelling its entry does not use.
7. Runtime: a command whose entry point in the tree (script, build target, package script) is missing — `check` runs no command, `write` runs each; a generated artifact without its generator; a second hand-edited orientation file.
8. Rules: enforced by a mechanism and not a route line; `NEVER`/`MUST` outside sacred ground; unverifiable; scoped narrower than the file; restating a co-loaded file.
9. Current state: a date, an incident, "now / no longer / recently", a history.
10. Citers: a heading another prompt cites is missing.
11. Prose: the `/quality:prompt` pre-commit self-check fails.

### `write <path>`

Drafts a new file or restructures an existing one to the law:

1. Read the stream: the target, its parent orientation files, and every co-loaded layer that is readable; list the headings other files cite.
2. Gather candidate terms: top-level directories (`git ls-files | cut -d/ -f1 -s | sort -u`), commit scopes (`git log --format=%s -n 500 | sed -n 's/^[a-z]*(\([^)]*\)).*/\1/p' | tr ',' '\n' | sort | uniq -c | sort -rn`), the terms the existing file and prompts use, design-doc names.
3. Admit each by § Admission; locate each home by grep, never by memory.
4. Runtime: find the engines (which orientation files and engine directories exist) and the environments (build files, dev scripts, CI workflows, container files); run each command once — one that cannot run is reported unverified, never written as verified.
5. Carry every existing rule over: kept, moved (child file, path rule, command, doc), merged, or cut with a named reason — the `/quality:prompt` cut discipline; nothing is lost silently.
6. Retarget the citers of every renamed heading in the same pass.
7. Run `check`; report each move and cut in one line, then the admitted term list for the owner to strike.

## The truth script

Embedded in the command, run by `check` and by `write`'s final step: saved to a scratch file and run as `bash {script} {orientation file}`. Its broken states print `ERROR`, never silence: an unreadable file, a file outside git, no path extracted, no Vocabulary section.

```bash
f=${1:?usage: <orientation file>}
[ -r "$f" ] || { echo "ERROR: cannot read $f"; exit 2; }
d=$(dirname "$f")
r=$(git -C "$d" rev-parse --show-toplevel 2>/dev/null) || { echo "ERROR: $f is not inside a git repo"; exit 2; }
ps=$(grep -o '`[^` ]*/[^` ]*`' "$f" | tr -d '`' | grep -v -e '[{}$*<>|…]' -e '://' -e '^~' -e '^/' | sort -u)
[ -n "$ps" ] || echo "ERROR: no path extracted from $f — path truth not checked"
printf '%s\n' "$ps" | while IFS= read -r p; do [ -z "$p" ] || [ -e "$d/$p" ] || [ -e "$r/$p" ] || echo "MISSING: $p"; done
grep -nE '/(Users|home)/[A-Za-z0-9_.-]' "$f" | sed 's/^/ABSOLUTE: /'
v=$(awk '/^# Vocabulary/{on=1;next} /^# /{on=0} on' "$f")
if [ -z "$v" ]; then echo "ERROR: no '# Vocabulary' section in $f — closed world not checked"
else (cd "$d" && git ls-files) | cut -d/ -f1 -s | sort -u | while IFS= read -r t; do printf '%s\n' "$v" | grep -qF "\`$t/" || echo "UNCOVERED: $t/"; done; fi
echo "checked $(printf '%s\n' "$ps" | grep -c .) paths · $(wc -l < "$f" | tr -d ' ') lines · $(wc -c < "$f" | tr -d ' ') bytes"
```

- Path truth: every backticked token holding a `/` and no placeholder, URL or home prefix must exist relative to the file's directory or the repository root.
- Absolute: a machine-absolute path with a real name after `/Users/` or `/home/`; a rule that names the pattern with `…` passes.
- Closed world: each top-level directory git tracks under the file's directory must appear as the start of a backticked path inside `# Vocabulary`.

## Relations

- `/quality:prompt` governs every line's prose; this law governs the orientation file's shape and admission, and runs the prompt law's self-check as check 11.
- `/quality:doc` governs reference docs; the orientation file cites them from Vocabulary entries and never holds their content.
- `/quality:description` governs the `description:` of this command.
- `/quality:llm-codebase` owns the glossary and its spelling census; the Vocabulary holds the glossary's canonical terms and homes (§ One term per concept), and its orientation-truth check and this law's truth script test the same fact from two sides.
- In a framework install that routes prompt-file edits through a change manager, that manager's CLAUDE.md conventions point here instead of restating them.

## Decisions

- Scope is a project's orientation file. A user-level file (`~/.claude/CLAUDE.md`) maps no repository, so closed world and the truth script do not apply to it; its prose still answers to `/quality:prompt`.
- Name `quality:claude-md`: the file name people type. The body covers every engine's equivalent. Rejected: `quality:orientation`, which nobody types; `quality:agents-md`, which names one engine.
- A term-keyed Vocabulary instead of a directory tour: the tour is the content measured not to help (§ Evidence); the term is the key the reader arrives with.
- `# Rules`, not `# MANDATORY Rules`: a MANDATORY heading tells the model every rule beneath it is an invariant and it over-triggers on the ordinary ones (`/quality:prompt` anti-pattern 8); the sacred-ground `##` placed first carries the weight where it belongs.
- Runtime covers environments as well as engines, so the law holds for a web application (`## Local`, `## CI`, `## Production`) as much as for an agent framework.
- 16 KB beside 200 lines: the published line target read at an 80-character line; without a byte figure the line cap is met by long lines.
- Path vars under Vocabulary: the same shape, and defined before any rule uses them.
- Bullets over tables for entries: greppable by term, local edits, fewer bytes.
- Full paths only: a fragment relative to an earlier mention makes the reader rebuild the path — the round-trip the file exists to save.
- The truth script is embedded in the command, not shipped as a file: one file to install on every engine, no executable bit to keep.
- `write` ends with the term list for the owner to strike: generated context files measurably cost success; curation is the difference.
