# Epic templates

An epic is an initiative-level persistent context at `docs/epics/{name}/`. It survives across conversations: a fresh chat says "Load epic {name}" and picks up where the last one left off. Epic files are **current-state consolidations**, never append-only logs — each update rewrites or merges into the relevant section, and git history keeps the superseded versions.

## Structure

```
docs/epics/{name}/
├── manifest.md          ← anchor: frontmatter + narrative + decisions + load index
├── update.md            ← work doc: current state + per-area delivered
├── {topic}.md           ← optional topic files (RND results, RR reports, POC notes), each registered in manifest ## Files
└── archive/             ← superseded material (cold — never auto-loaded)
```

**Load protocol:** read `manifest.md` + `update.md`, then open topic files from `## Files` (fall back to `ls`) only as the task requires. Never read `archive/`.

**Ownership:** the Professor owns the lifecycle and narrative (`## Vision & Scope`, `status:`, topic files, epic creation/deletion). The main-loop session consolidates shipped work (`/wave:live` W5, `/wave:orchestrator` O3) and session work (on "save the epic" / at a milestone) into `update.md` + the manifest's working sections per § Consolidation contract.

## Consolidation contract

Governs every epic write. Sections named here are created on first write, so older epics converge on their next update. Epic files are working context, not reference clusters — skip the `/quality:doc` load.

1. **Resolve the epic:** the name given; else the `docs/epics/*/manifest.md` with `status: IN_PROGRESS` whose scope matches the work; no unambiguous match → list candidates and ask the user.
2. **Consolidate** — for a session save, walk the ENTIRE conversation, not just recent turns; for a wave, the merged diff + report:
   - Work state — done (with evidence: paths, SHAs, test results), in-flight position, ordered next steps → `update.md` (`## State of work` rewritten, `## Delivered` merged per subsection; a later ship that supersedes earlier work rewrites the subsection — replaced designs vanish, git history keeps them).
   - Decisions with rationale, user rulings included → manifest `## Key Decisions` (deduped).
   - Gotchas, failed attempts, surprises → `## Discoveries` (deduped); items awaiting the user → `## Open Questions`.
   - One `## Progress Log` milestone line; new epic files registered in `## Files`; add to `pipelines:`/`waves:` as applicable; bump `updated:`.
3. **Completeness pass:** the bar is a fresh session given only "Load epic {name}" continues seamlessly — no re-reading the old chat, no re-asking the user, no re-discovering gotchas.
4. **Report** which epic was saved into and the continuation line: `Load epic {name}`.

Bulky superseded artifacts move to `archive/` — loads never read it.

---

## `manifest.md`

```markdown
---
epic: { kebab-case-name }
status: PLANNING | IN_PROGRESS | SHIPPED
created: { YYYY-MM-DD }
updated: { YYYY-MM-DD }
pipelines: []
waves: []
---

# {Epic Name}

## Vision & Scope

{What this initiative is and what "done" means. Professor-owned.}

## Key Decisions

{Each decision with its why, deduped. Sharpen an existing entry over adding a near-duplicate.}

## Progress Log

{Exactly ONE line per milestone — substance lives in update.md and Key Decisions, never here:}

- {YYYY-MM-DD} — {pipeline|wave|session}: {one sentence} ({SHA})

## Discoveries

{Gotchas, failed attempts, surprises learned the hard way. Deduped.}

## Open Questions

{Items awaiting a user ruling.}

## Files

{The load index — one-line hook per topic file in the epic dir:}

- `{topic}.md` — {what it holds}
```

---

## `update.md`

The epic's work doc — current-state, rewritten/merged each consolidation.

```markdown
# {Epic Name} — Work

## State of work

{REWRITTEN every consolidation: what is live, the exact in-flight position, and ordered
next steps precise enough to execute — paths, commands, expected outcomes.}

## Delivered

### {Feature / area A}

{What exists NOW: behavior, key files/symbols, merge SHAs woven in as facts. When a later
ship supersedes earlier work, rewrite this subsection — replaced designs vanish.}

### {Feature / area B}

{…}
```
