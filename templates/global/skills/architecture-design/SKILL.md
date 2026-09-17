---
name: architecture-design
description: Lays out agent-maintainable codebases — `$architecture-design <feature|LLM call|project|path>`, "where should X live", before /wave:refine on a feature touching 3+ directories, every new LLM call; greenfield designs a tree, brownfield measures then migrates; `integration <project>` designs the integration-test suite as lanes. Returns a design document; edits no code.
---

# Architecture Design

Design for a reader whose context resets every session: one change lands in one directory, the first grep finds the name, nothing is re-implemented under a second name. The design covers the reader's whole path — the protocol files it loads, the brief it receives, the evidence it consults, the source it edits, the receipts it writes — since a hop spent before the first source read costs the same as a hop inside `src/`. The output is a design document handed to `architect` for review, then to /wave:refine or the builder.

## Inputs

- target: a feature, an LLM call, a project, or a path
- mode: greenfield (nothing exists) or brownfield (a tree exists — measure before designing)
- mode `integration <project>`: the target is the integration-test suite — follow `references/integration-design.md` in place of § Procedure; § Hand-off applies unchanged
- the project's CLAUDE.md and the live tree (`ls`, `git ls-files` — never recalled)
- evidence and baselines: the project's architecture playbook or epic evidence record, when one exists (`docs/epics/{epic}/playbook.md`)

## Metrics — what good means

- dirs-per-change: directories touched by one change → one dominant directory
- first-edit hops, source-only: navigation calls from the first source-file read to the first edit → ≤ 3
- orientation hops: protocol, brief, ledger and evidence reads before the first source-file read → reported beside the source-only number; the brief (step 11) is the lever that moves it
- re-reads: one file opened ≥ 2× in a session → ≤ 2
- zero-result searches per session → ≈ 0
- new file written without a prior search for its concept → 0

## Procedure

1. Partition by unit of change. List every concept the target contains; group by what changes together (a prompt + its schema + its call + its store + its test co-change; two features that never co-change are two units). Each unit owns one directory.
2. Order the tree by flow. Where units form a pipeline, prefix the directories so `ls` prints the execution order; support packages sit beside the flow, named by what they are (`llm/`, `transcript/`, `guards/`, `db/`). A directory named by negation (`utils`, `helpers`, `common`, `misc`, `shared`) is rejected — each file in it is placed with the unit that changes with it, or in a package named by its content.
3. Fix the anatomy. Every unit directory holds the same file set, so a missing file is a visible gap rather than a search. For an LLM call: `README.md prompt.md call.py input.py verify.py node.py store.py` — prompt text, typed hyperparameters, schema and call code in the one directory; the path is the registry. README is hand-written, ≤ 20 lines: what the unit does, what feeds it, what it feeds.
4. Name grep-true. One canonical term per concept, identical in filename, exported identifier, call-site variable, wire key and test name; the glossary lists each term. Generic identifiers (`create`, `handler`, `process`, `data`, `helpers`) are replaced by the concept's word; file names in a flow carry their order.
5. Façade the cross-cutting. Metrics, LLM invoke, feature flags, AI-provenance marker, erasure and consent guards, logging: one module each; units call the façade, never the primitive.
6. Remove parallel lists. The path is the registry, or one source generates every mirror and CI regenerates-and-diffs. A hand-maintained twin (a JSON registry beside a directory listing, a DI container list, a lint allowlist holding path strings, two locale files without a key-parity check) is redesigned out. Tooling that agents write repeatedly — a test runner, a receipt recorder, a result parser, a declared-vs-executed census — is a versioned script in the project's `scripts/`; a seat authoring its own copy is the same twin in the operational tree.
7. Mirror the tests. `tests/<same relative path as the source>`, one test file per source file; a flat catch-all test directory is not admitted.
8. Set ceilings and ratchet them. A line ceiling per file, source and test, from the project CLAUDE.md; absent one, propose it under § Open rulings. Generated output splits per type or operation. The over-ceiling list is committed as a baseline the check compares against, and it only shrinks: a listed file touched by a wave is split in that wave; a file over the ceiling is split before it is placed under a write guard.
9. Align across projects. The same concept lives at the same relative path in every project (`env`, `config`, `tests`, `generated`); a primitive two projects need lives in the roster's wire-contract/schema hub (when it has one) and is vendored, never re-typed. A change crossing a wire boundary starts at the hub's consumer index (`{the hub's consumer-index command}`, named in the root CLAUDE.md § Architecture), which enumerates the producer and consumer anchors the change must cover; the design names that index as hop 1 for every consuming project's build hand.
10. Brownfield: measure first, then plan. Run § Checks against the tree; list giant files (`wc -l`), twin registries (files always co-edited), sibling directories owning one concept, LLM calls split across directories, flat test directories, negation-named packages, one-line re-export modules, and — from the agents' own transcripts — where the orientation hops go (which protocol, ledger and evidence files every seat opens before its first source read). Write the migration as ordered steps; rebuild in one worktree from a reuse manifest when more than half the tree moves, incremental otherwise; every step names the metric it moves.
11. Design the brief. A task carries every fact its hand depends on as a quoted `file:line` anchor, the exact commands to run, and the consumer list from step 9 — a pointer to a ledger, walk-notes or evidence directory in place of the fact is a gap. The hand's first command is its target file.
12. Fix the work tree's anatomy. The wave's spec, ledger, receipts and evidence live under one brief-named root with a fixed file set (`SPEC.md`, `STATE.md`, `receipts/`, `evidence/`), at most three levels deep; evidence a later wave needs is quoted into its spec (step 11), never reached by path.

## Output — the design document

Written to the path the brief names (an epic, wave or RND directory), sections in this order:

1. Units of change — table: unit · concepts it owns · directory · co-change evidence
2. Tree — the full target tree, one line per file, the fixed anatomy visible
3. Glossary — concept → canonical term → where it appears (file, identifier, wire key, test)
4. Façades — concern → module → the primitive it hides
5. Registries — every derived artifact → its source → the regeneration command
6. Checks — the mechanical assertions below, instantiated for this tree, runnable as a CI step
7. Metrics — the baseline today and the target after the build; hops reported as the pair source-only · orientation
8. Migration (brownfield) — ordered steps, rebuild vs incremental, what dies
9. Brief template — the anchor, command and consumer-list slots every task of this tree carries (step 11)
10. Open rulings — every number or trade-off the user decides (ceilings, rebuild vs incremental, which twins die first)

## Checks — skeleton every design instantiates

```sh
P=<project>; CEIL=<ceiling>
git -C $P ls-files '*.py' '*.ts' '*.tsx' | xargs wc -l | awk -v c=$CEIL '$1>c && $2!="total"'   # size ceiling
find $P -type d \( -name utils -o -name helpers -o -name common -o -name misc \)               # negation-named dirs
grep -rl '^export .* from' --include='*.ts' $P/src | xargs wc -l | awk '$1==1'                  # one-line re-exports
for f in $(git -C $P ls-files 'src/**/*.ts' 'src/**/*.py'); do                                 # mirrored tests
  t="tests/${f#src/}"; [ -e "$P/$t" ] || [ -e "$P/${t%.*}.test.${t##*.}" ] || echo "no test: $f"; done
grep -rn '<primitive>(' $P/src | grep -v '<facade module>'                                     # primitive outside façade
find $P -name 'config.json' -path '*prompts*' -o -name 'REGISTRY.json'                          # literal registries
comm -13 <(sort $P/<ceiling-baseline>) <(git -C $P ls-files '*.py' '*.ts' '*.tsx' | xargs wc -l | awk -v c=$CEIL '$1>c && $2!="total"{print $2}' | sort)   # ratchet: any line = a new over-ceiling file
find <work-root> -name 'run.py' -o -name '*.mjs' -o -name '*.sh' | grep -v '/scripts/'          # runner authored outside the tree's scripts/
```

Adapt the globs and the primitive names to the tree; a check that cannot be expressed as shell is listed under § Open rulings, never asserted in prose.

## Hand-off

`architect` reviews the document (placement law, literal lists shadowing derived constants, coupled edits). A design that changes what an LLM receives or returns goes through the RND gate. Code moves through /wave:refine and the project's builder; this skill writes the design only.
