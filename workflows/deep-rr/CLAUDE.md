# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

RR ("Research and Report") — a standalone Claude Code skill: a deterministic background Workflow that runs a brainer-steered web crawl over a quote-pinned claim ledger and derives a cited answer. The runtime the skill loads is at the repo root: `SKILL.md` (operator interface + version pin), `workflow.js` (the engine bundle), `persist.js` (host-side artifact writer), `midrun.js` (host-side live-run inspector). Downstream projects vendor these files verbatim (Professor ships it in-tree at `workflows/deep-rr/`), so root-file shape is a public contract — e.g. the `this.DIR = 'RR/' + this.slug` line in the bundle is patched by consumers and must stay greppable.

`workflow.js` is a **build artifact** — never hand-edit it. The source is `engine/src/` (TypeScript); `cd engine && npm run build` regenerates it. `persist.js` and `midrun.js` are hand-edited plain Node CJS scripts (no build; tested from the engine suite by copying them into throwaway git repos).

## Commands (all from `engine/`)

```
npm install        # first time
npm test           # pretest runs verify.js: rebuilds to temp + byte-diffs against ../workflow.js —
                   # the suite can NEVER pass against a stale bundle; then vitest run
npx vitest run test/store.test.ts        # one file (skips the verify gate — faster mid-edit)
npx vitest run -t 'pattern'              # one test by name
npm run typecheck  # tsc --noEmit
npm run build      # bundle engine/src/ → ../workflow.js, then validate-bundle.js (sandbox contract)
npm run coverage   # thresholds: 90 lines/statements/functions, 85 branches
npm run format     # prettier over src/ + test/
```

Gates before any commit: `npm test` + `npm run typecheck` + `npm run build`, all green. Every code change commits BOTH the source and the regenerated `workflow.js`. Version lives in three places — `SKILL.md` frontmatter, `engine/package.json`, the README header — bump all three together and tag `vX.Y.Z` on release.

## The Workflow sandbox contract (why the build is custom)

The harness wraps `workflow.js` in an async function body with ambient globals (`agent`, `parallel`, `pipeline`, `log`, `phase`, `args`, `budget` — declared in `src/types/globals.d.ts`, mocked in `test/setup.ts`). Consequences enforced by `validate-bundle.js` at every build:

- Single flat file: `export const meta = {…}` (pure data literal) is the first statement; the bundle ends with a top-level `return await rr.run()`.
- No module system at runtime and **no npm dependencies** — a bare-specifier import fails the build; vendor pure JS under `src/` instead.
- Determinism: no `Date.now` / `Math.random` / argless `new Date` / `process.*` / `fetch(` / `crypto` / `Buffer` / `new URL`.

`build.js` is an acorn-AST concatenating bundler: module order is a stable topological sort of the real import graph from `engine.ts` (`meta.ts` pinned first); barrels are walked through, never bundled. Build-breaking guards: unreachable content-bearing file, import cycle, dependency ordered after its user. TypeScript is type-STRIPPED (Node `stripTypeScriptTypes`), so `src/` must avoid emit-requiring TS: no enums, no namespaces, no parameter properties — plain `type` annotations only.

## Architecture

- `engine.ts` — `ResearchReport`, the orchestrator. Owns ALL state mutations and every `files[name] =` write; agents are pure request/response. One shared claim-ingest path (`ingestClaimSeeds`) serves the scout seed and every wave.
- `brainerState.ts` — one crawl branch's full state (`BrainerState` IS a `StoreState`); `spawnBrainer` deep-copies it for the brainer tree.
- `store.ts` — pure reducers over the id-keyed rabbit-hole frontier (add/applyDeltas/resolveLookupNext/pursue/reopen).
- `runtime.ts` — `retryAgent` (every agent call, `AGENT_RETRIES` retries; a harness-null return is routed through the same ladder) + the debug I/O buffers.
- `config.ts` — THE single source of truth for every knob, cap, tier, effort, and prompt fragment. Never introduce a numeric literal anywhere else — including inside schema `description` strings.
- `agents/<name>/{index,prompts,run}.ts` — one directory per agent: descriptor + StructuredOutput schema literal / template consts + clause builders / pure dispatch. Shared schema bricks and guard clauses (`FINISH`, `WEB_ONLY`, `EMIT`) live in `agents/shared.ts`. `agents/index.ts` is a dev/test barrel only.
- `utils/` pure helpers · `types/` contracts.

## Hard-won engine rules

- The two wave paths — `runCrawl` (single-brainer) and `runOneWave` (multi-brainer) — MUST stay in behavioral parity. Any wave-loop change lands in both.
- Every agent call degrades to null: a dying agent leaves the run on a named fallback path (the table in `design.md` §9), never crashes it. Every loop carries a named cap read from `config.ts`.
- Claim status and run confidence are COMPUTED from ledger topology; models may lower confidence, never raise it. The engine owns every ledger mutation.
- Output schemas are screened by a platform classifier that kills oversized spawns. The brainer's COORD is built per call (`buildCoord`) to prune unusable clauses, and `test/schemas.test.ts` pins the serialized size ceilings — growing a COORD brick requires shrinking elsewhere first. Keep schema descriptions terse; steering prose lives in the prompt.
- Worker-model reality is part of the contract: optional schema fields are null-tolerant (haiku emitters send `null` for "nothing"; a hard type = a retry burned), the engine null-scrubs at ingest, and prose-tolerant fields (stance targets) are coerced, not rejected.
- Prompts are template consts with snapshot tests. Regenerate deliberately and eyeball the diff — never blind `-u`.
- Secrets and PII never go into code or docs.

## Testing model

`test/setup.ts` sets the ambient harness globals BEFORE any import — `CONFIG` is constructed at module load from `args`, so engine tests that need different args/agents use `vi.resetModules()` + a fresh dynamic import with overridden globals. Agent behavior is driven by canned StructuredOutput stubs keyed off prompt content.

## Docs map (read before structural work)

- `design.md` — how a run actually flows stage by stage: wave order, stop-condition priority, the null-economy fallback table, model-tiering rationale.
- `engine/docs/V3-DESIGN.md` — the canonical naming table and claim-ledger mechanism specs.
- `SKILL.md` — the operator contract (launch args, modes, recovery, midrun). An engine change that alters args or behavior lands its prose here in the same commit; `README.md` mirrors the high-level story.

## Process guards

- Framework/prompt-surface changes — `CLAUDE.md`, `SKILL.md`, anything under `.claude/` — route through `/pfm` (hook-enforced: the pre-edit gate requires `/quality:prompt` loaded this session). Engine source under `engine/src/` is normal dev work gated by the commands above.
