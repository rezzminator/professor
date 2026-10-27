---
name: quality:claude-md
description: MANDATORY — load before writing or editing a project's CLAUDE.md, AGENTS.md or other engine orientation file; `check [path…]` certifies each (APPROVED/REJECTED), `write <path>` drafts or restructures one. Prose → /quality:prompt; reference docs → /quality:doc.
argument-hint: "[check [path…] | write <path>]"
---

# Orientation File Law

Every session and sub-agent reads the orientation file whole before its first tool call. Each line serves one of two metrics: bytes per load (`wc -c`), or round-trips from a spoken term to its source — target one: the reader finds the term here and its next call opens the source. `/quality:prompt` governs each line's prose; this law governs the file's shape and admission.

## The spine

```
# {Project} — {what it is, one line}
{1–3 lines: what a careless edit here costs}

{precedence line, its own paragraph}

# Vocabulary
- {term}: {gloss} · `{source path}`[, `{path}`…] [· design `{doc path}`]
- {artifact}: {gloss} · generated from `{source}` by `{command}`
## Path vars
- `$VAR`: `{path}`

# Runtime
## {engine}
## {environment}

# On demand
- Before {action} on `{path}`: load `/{skill}` | read `{doc}`

# Rules
## {sacred ground}
## {domain}
```

- Order is define-before-use.
- `#` marks a spine section, `##` its child; exempt the file from the markdown linter's single-title and heading-level rules.
- A top-level section outside the spine exists only when its content fits no spine section and passes § Rules admission.

## Title, stake and precedence

Line one names the project and what it is in one clause. At most three lines follow on what a careless edit costs here — the one fact that changes how every reader weighs every action ("`services/ledger/` moves real money: a migration there runs against production on deploy"). No history, no feature list.

The precedence line follows: where Vocabulary, Runtime or On demand disagrees with the code, the code wins and the reply names each mismatch; `# Rules` binds regardless. Model it on "Where this file and the code disagree on a path, command or convention, follow the code and name each mismatch in your reply; `# Rules` binds regardless." The repository's root orientation file carries it once; a child file inherits it.

## Vocabulary

The project's nouns, each mapped to its source: one lookup from the word people use to a path, and the one registry of canonical names.

- The value is the thing's source; a generated artifact takes the second spine form, naming its source and generator.
- The term is spelled as people and code spell it; a code identifier or file name verbatim.
- The gloss says what the thing is or does in the fewest words that prevent a misreading.
- Paths are full: from the repository root, or from the file's own directory in a child file. Outside the tree: rooted at `$HOME` or `~`. A variable segment is braced: `services/{name}/`, `releases/v{X.Y.Z}.md`.
- Admission: people or prompts in the project use the term, and at least one holds — its home is not found by one `ls` or grep of the term; it means something here other than its everyday meaning; it is a top-level component.
- Closed world: every top-level directory git tracks (a child file: every directory under its own) is covered by an entry whose path is that directory or one inside it.
- Closed world outranks admission: a top-level directory whose name explains itself keeps its entry, and its gloss says only what the name does not.
- One term per concept: the entry's spelling is the only spelling — in this file, every prompt, doc, commit subject and identifier. A second name found elsewhere is renamed to the entry's term, or the entry is renamed end to end. Where `/quality:llm-codebase` wrote a glossary, its canonical terms and homes are these entries; its rejected variants stay in the glossary for the spelling census.
- `## Path vars` exists only when the project's prompts substitute `$VAR` tokens.

## Runtime

Everything that executes the project's work, one `##` per executor, each carrying only what a reader cannot discover and needs before acting.

- Engines, one contract: exactly one orientation file is hand-edited; every other engine reads it natively, imports it, or reads a mirror generated from it. Claude Code reads `AGENTS.md` only where no `CLAUDE.md` exists, so a repository carrying both loads one per engine.
- An engine gets a `##` only for project-specific mechanics: what it reads, how its mirror is generated and checked, its hooks or guards, its deltas from the shared contract.
- Environments: one `##` per place the code builds, tests or runs — the local loop, an isolated container, CI, production. Each carries the exact command lines, copy-pasteable and run once at write time; the single entry point where one exists ("build and test only through `{script}`"); the ports, services and variables a command needs that the reader cannot discover; where scratch output goes.
- An environment that builds or tests names its done-gate command (what must pass before work counts as done) and its narrowest test invocation (one file or one test).

## On demand

Procedures and reference docs one task needs, each behind the trigger that requires it.

- One trigger per line: ``- Before {action} on `{path}`: load `/{skill}` ``, or ``read `{doc}` `` in place of the load.
- A line exists only when the load or read must precede the action; the line says when, the skill's own `description:` says what.
- A skill whose `description:` already names the trigger gets no line.
- A Vocabulary `· design` pointer says where a doc lives; a trigger line says when reading it is required.

## Rules

- Admission: the code cannot show it; no mechanism enforces it — or a hook does, and the rule is one line naming the route the hook permits; its absence causes a mistake, shown by a recorded recurrence (review finding, repeated correction), sacred ground excepted; it is concrete enough to verify.
- The sacred-ground `##` comes first — secrets, personal data, publication, whatever the project marks sacred — and is the only place `NEVER` and `MUST` appear.
- Each sacred rule ends with its guard: ``· guard `{hook, guard or test path}` ``. A sacred rule keeps its line beside its guard; one with no guard stays and is reported `ADVISORY` — a named risk, not a reject.
- Every other rule is phrased positively and names the tool, command or path to use.
- A rule may carry one clause naming the cost it prevents ("— a reset loses the only crawl history"); history, incidents and dates stay out.
- Grouped by domain under `##`; one rule per bullet; a bullet over two lines is two rules or a paragraph.
- Stated once across everything loaded with the file: a rule the harness's system or persona layer, or a parent orientation file, already carries is cut here.

## Density

- One fact per line; prose only in the stake.
- Bullets, not tables.
- A Vocabulary term is cited bare everywhere else.
- A path is its own explanation: a line or gloss says only what the path's name does not.
- Compact set notation: `{status|build|test}` for a command's modes, `·` between fields.
- Bold only for a sacred-ground rule's opening words.

## Budget and placement

- 200 lines and 16,000 bytes per file, root or child, measured with `wc -l -c`; an `@path` import counts toward the file that imports it.
- 24,000 bytes per file, mirror included: Antigravity truncates a rule file past it.
- 32,768 bytes for the Codex chain — the `AGENTS.md` (`AGENTS.override.md` where present) in each directory from the git root down to the file's: Codex's `project_doc_max_bytes` default, where it stops reading.
- The root file carries only what binds the whole repository — two or more of its projects.
- A child orientation file in a project directory carries only that project's delta and loads when the reader works there; a rule scoped to one project moves down into it.
- A rule bound to a file pattern goes to a child orientation file in the directory it binds. A path-scoped rule file (Claude Code: `.claude/rules/*.md` with `paths:`) serves only where every engine reads it: a single-engine repository, or a mirror generator that carries it to each engine.

## What never goes in

- Directory tour (path → contents), file-by-file description → nothing; `ls` answers it.
- Roster of commands, skills or agents, or an entry for one of them → their own `description:`, which the harness indexes; a family or tier of them is a term.
- A procedure one task uses → a command or skill, behind an `# On demand` trigger.
- Reference data (schemas, API contracts) → `docs/`, behind an `# On demand` trigger.
- Rationale beyond a rule's one cost clause, history, incidents, dates → a design doc or commit message.
- Voice, persona → the engine's persona or system-prompt layer.
- An invariant a machine can check → a hook, lint or test; the rule stays until one enforces it.

## Headings are an API

Other prompts cite sections by name (`CLAUDE.md § Rules`). Before renaming or removing a heading, grep the tree for `§ {heading}` and the bare heading text, and retarget every citer in the same pass.

## The truth script

Save it to a scratch file and run `bash {script} {orientation file}`. A backticked token holding a `/` is a path that must exist from the file's directory or the repository root, unless it holds a placeholder character, a URL or a `~` or `/` prefix; a rule naming a machine-absolute pattern writes it with `…` (`/home/…`). Its last line is the summary; every other line opens with its kind: `ERROR`, `MISSING`, `ABSOLUTE`, `UNCOVERED`, `OVER`, `PATH-RULE`, `ADVISORY`, `CODEX`.

```bash
f=${1:?usage: <orientation file>}
[ -r "$f" ] || { echo "ERROR: cannot read $f"; exit 2; }
d=$(cd "$(dirname "$f")" && pwd -P) || { echo "ERROR: cannot enter the directory of $f"; exit 2; }
r=$(git -C "$d" rev-parse --show-toplevel 2>/dev/null) || { echo "ERROR: $f is not inside a git repo"; exit 2; }
r=$(cd "$r" && pwd -P)
ps=$(grep -o '`[^` ]*/[^` ]*`' "$f" | tr -d '`' | grep -v -e '[{}$*<>|…]' -e '://' -e '^~' -e '^/' | sort -u)
[ -n "$ps" ] || echo "ERROR: no path extracted from $f — path truth not checked"
printf '%s\n' "$ps" | while IFS= read -r p; do [ -z "$p" ] || [ -e "$d/$p" ] || [ -e "$r/$p" ] || echo "MISSING: $p"; done
grep -nE '/(Users|home)/[A-Za-z0-9_.-]' "$f" | sed 's/^/ABSOLUTE: /'
v=$(awk '/^# Vocabulary/{on=1;next} /^# /{on=0} on' "$f")
if [ -z "$v" ]; then echo "ERROR: no '# Vocabulary' section in $f — closed world not checked"
else (cd "$d" && git ls-files) | cut -d/ -f1 -s | sort -u | while IFS= read -r t; do printf '%s\n' "$v" | grep -qF "\`$t/" || echo "UNCOVERED: $t/"; done; fi
b=$(wc -c < "$f" | tr -d ' ')
[ "$b" -le 24000 ] || echo "OVER: $f is $b bytes > 24000 — Antigravity truncates it"
ds=(); s=$d; while :; do ds=("$s" "${ds[@]}"); { [ "$s" = "$r" ] || [ "$s" = / ]; } && break; s=$(dirname "$s"); done
tot=0; nf=0
for s in "${ds[@]}"; do
  m=; for n in AGENTS.override.md AGENTS.md; do [ -f "$s/$n" ] && { m=$s/$n; break; }; done
  [ -n "$m" ] || continue
  [ -r "$m" ] || { echo "ERROR: cannot read $m — Codex chain total undercounted"; continue; }
  grep -q '[^[:space:]]' "$m" || continue
  mb=$(wc -c < "$m" | tr -d ' ')
  [ "$m" -ef "$f" ] || [ "$mb" -le 24000 ] || echo "OVER: ${m#"$r"/} is $mb bytes > 24000 — Antigravity truncates it"
  [ "$tot" -lt 32768 ] && [ $((tot + mb)) -gt 32768 ] && echo "OVER: Codex chain passes 32768 bytes in ${m#"$r"/} — Codex stops reading at its line $(( $(head -c $((32768 - tot)) "$m" | wc -l) + 1 ))"
  tot=$((tot + mb)); nf=$((nf + 1))
done
if [ "$nf" -eq 0 ]; then
  if [ -d "$r/.codex" ]; then echo "ERROR: $r mirrors to Codex (.codex/) but no AGENTS.md lies between it and $d — build the mirror; chain not measured"
  else echo "CODEX: no AGENTS.md from the git root to $d — chain not measured"; fi
else (cd "$r" && git ls-files -co --exclude-standard -- '.claude/rules/*.md' '*/.claude/rules/*.md') | sed 's/^/PATH-RULE: /'; fi
g=${CODEX_HOME:-$HOME/.codex}; gm=; for n in AGENTS.override.md AGENTS.md; do [ -f "$g/$n" ] && { gm=$g/$n; break; }; done
gb=0; [ -n "$gm" ] && gb=$(wc -c < "$gm" | tr -d ' ')
sg=$(awk '/^# /{on=/^# Rules/; n=0; next} on && /^## /{n++; next} on && n == 1 && /^- /{c++} END{print c+0}' "$f")
awk '/^# /{on=/^# Rules/; n=0; next} on && /^## /{n++; next} on && n == 1 && /^- / && !/· guard `/{print NR}' "$f" | while IFS= read -r ln; do printf 'ADVISORY: line %s has no guard: %.70s\n' "$ln" "$(sed -n "${ln}p" "$f")"; done
echo "checked $(printf '%s\n' "$ps" | grep -c .) paths · $(wc -l < "$f" | tr -d ' ') lines · $b bytes · Codex chain $tot/32768 bytes in $nf files, global ${gm:+${gm/#$HOME/\~} }$gb bytes outside it · sacred rules $sg"
```

## Modes

- No argument: the law above, applied while writing.
- `check [path…]`: read-only; no path means the hand-edited orientation file at the repository root. Run the truth script, then the checks below. Emit `APPROVED: {path}`, `REJECTED: {path} — checks {n,…}`, or `UNREAD: {path} — {error}` when the file could not be read — only a file read earns a verdict — then each `ADVISORY` line beneath it. An `ERROR` line fails every check it left unrun. A user-level file outside any repository (`~/.claude/CLAUDE.md`) and a template orientation file, whose paths resolve only in the project it scaffolds, skip the truth script and checks 3 and 4.
- `write <path>`: the steps below, to the law.

Checks:

1. Spine: a spine section missing where it has content, out of order, or a top-level section that fits a spine section or fails admission; the precedence line missing from the root file or repeated in a child.
2. Budget: over 200 lines or over 16,000 bytes; an `OVER` line.
3. Path truth: a `MISSING`, `ABSOLUTE` or `ERROR` line from the truth script.
4. Closed world: an `UNCOVERED` line from the truth script.
5. Admission: a tour or file-by-file entry, an entry for one self-indexed command, skill or agent, a gloss that restates its path; a trigger line without its action and path, or for a skill whose `description:` names the same trigger.
6. One term: one concept under two names, or a term used elsewhere in a spelling its entry does not use.
7. Runtime: a command whose entry point in the tree (script, build target, package script) is missing — `check` runs no command, `write` runs each; a generated artifact without its generator; a second hand-edited orientation file; an environment that builds or tests without its done-gate command or narrowest test invocation.
8. Rules: enforced by a mechanism and neither a route line nor sacred ground; `NEVER`/`MUST` outside sacred ground; unverifiable; a generic directive — "follow best practices", "write clean code", "be careful", "be thorough", "use good judgment", "keep quality high" and any other naming no tool, path, command or threshold; rationale past one cost clause; scoped narrower than the file; a `PATH-RULE` the mirror generator does not carry to every engine; restating a co-loaded file.
9. Current state: a date, an incident, "now / no longer / recently", a history.
10. Citers: a heading another prompt cites is missing.
11. Prose: the `/quality:prompt` pre-commit self-check fails.
12. Contradiction: a fact or rule here that contradicts a co-loaded layer — a parent or child orientation file, a user-level file, the harness's system or persona layer, a mirror's engine adapter.

Write steps:

1. Read the stream: the target, its parent orientation files, every readable co-loaded layer; list the headings other files cite.
2. Gather candidate terms: top-level directories (`git ls-files | cut -d/ -f1 -s | sort -u`), commit scopes (`git log --format=%s -n 500 | sed -n 's/^[a-z]*(\([^)]*\)).*/\1/p' | tr ',' '\n' | sort | uniq -c | sort -rn`), the terms the existing file and prompts use, design-doc names.
3. Admit each by the admission bullet of § Vocabulary; locate each home by grep.
4. Runtime: find the engines (which orientation files and engine directories exist) and the environments (build files, dev scripts, CI workflows, container files); run each command once — only a command that ran is written as working; one that cannot run goes in the report as unverified.
5. Carry every existing rule over: kept, moved (child file, path rule, trigger line, command, doc), merged, or cut with a named reason — the `/quality:prompt` cut discipline.
6. Retarget the citers of every renamed heading in the same pass.
7. Run `check`; report the byte change against `HEAD` (`git show HEAD:{path} | wc -c` beside `wc -c`), naming what any growth bought; each move and cut in one line; then the admitted term list for the owner to strike.
