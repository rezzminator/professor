---
name: audit:code-hygiene
description: Scans AI-authored code for duplication, ghost fields, dead code, deps, architecture, types, naming, quality, magic numbers — scopes `all`, `dup`, `ghosts`, `dead`, `deps`, `arch`, `types`, `naming`, `quality`, `magic`, `{project}` per roster entry, `diff` (a changed-file set, e.g. a landed flight's), `sweep [{project}|all]` — the one scope that removes dead code, gated by approval; else report-only.
argument-hint: [scope]
---

# Code Hygiene — Audit Sub-Mode

**Trigger:** `code-hygiene`, `code-hygiene <scope>`, or a code-hygiene audit on a scope.

**Scopes:** `all`, `dup`, `ghosts`, `dead`, `deps`, `arch`, `types`, `naming`, `quality`, `magic`, `diff`, `sweep`, plus a per-project scope for each `{project}` in the roster (a single-project repo has just one).

Each category is independent — run only applicable ones based on scope. Scope `diff` restricts every category to a provided changed-file set (e.g., a flight's merged diff) plus the call-sites and imports that touch those files — for reviewing a landed flight's diff.

**This codebase is largely AI-authored — weight the checks accordingly.** Each category marks the LLM-characteristic failure it catches.

Each category names the linter rules that already cover part of its ground so the audit spends its budget elsewhere. Read the severity and the current rule set from the config itself — each project's lint config (e.g. `eslint.config.mjs` / `.eslintrc.json` for a TS project, `[tool.ruff.lint]` in a Python project's `pyproject.toml`) and each `tsconfig.json`'s `noUnusedLocals` / `noUnusedParameters`.

---

## Sweep Mode — report-only by default, remediate on `sweep`

Every run is **report-only by default**: detect, tier, and STOP — it never deletes. The `sweep` scope (`code-hygiene sweep [{project}|all]`) is the one mode that _removes_ confirmed-dead code and unused dependencies. It governs **Category 2 (Dead Code)** and **Category 3 (Stale Dependencies)** only; every other category stays advisory.

**The end-to-end deadness bar.** A symbol is dead only when it has zero consumers across _the whole roster_ and across every surface a static import-grep misses — because this is a {DOMAIN_ADJ} product, and a false "dead" becomes a regression in a live {SESSION_NOUN}:

- {API_PROTOCOL} contract — the {API_PROTOCOL} schema (SDL/types the UI/client queries by name, never imports)
- {QUEUE} message contracts between roster projects (payload fields the consumer reads, never imports)
- a prompt/asset registry — assets loaded by string name at runtime (e.g. a prompt loader resolving a key to a file), never statically imported
- {ORM} migrations, {UI_FRAMEWORK} file-routes, and JSON/(de)serialization mappers — registered by name or convention, not import
- Test-only, config-only (babel/webpack/jest/pytest/ruff/eslint), and reflection consumers

A candidate that can't be proven past this bar is not dead — flag it and keep it.

**Sweep procedure:**

1. **Detect** Category 2 + 3 candidates per their detection steps below.
2. **Prove deadness** adversarially — for each candidate, try to prove it still _alive_ against the bar above before declaring it dead.
3. **Tier** the survivors: **TIER-1** confirmed-dead (cleared the bar) · **TIER-2** uncertain (a consumer surface unresolved — verify first) · **kept** (a real consumer found).
4. **Approval gate** — present the full tiered kill-list and STOP. Cut only the set the user approves; never remediate piecemeal mid-scan.
5. **Remove** the approved set in a worktree (never on `main`), run the full QA gate — green tests are the only empirical proof the cut was truly dead — and have gitter merge. Git is the undo.

---

## Category 1 — Ghost Fields & Dual-Writes

Fields, columns, or properties that exist in multiple places for the same concept, are kept in sync manually, or exist as legacy compatibility shims that nobody dares to remove.

**How to detect:**

- **DB schema dual-writes:** the same logical value written to multiple columns or tables — two UPDATE statements in one function writing similar data, similarly-named fields on different tables for one concept, one roster project writing to another project's owned columns (cross-boundary writes).
- **{API_PROTOCOL}/DB mismatches:** {API_PROTOCOL} fields mapping to no DB column (computed? stale?), DB columns with no {API_PROTOCOL} field (dead storage?), fields present on both the {API_PROTOCOL} type AND as a nested resolver.
- **Client-side fallback chains:** `??` / `||` patterns in UI/client projects reading the same value from multiple sources (`user?.fieldA ?? user?.fieldB`) — a ghost field that should have been consolidated.
- **Enum duplication:** the same enum values defined across DB enum, {API_PROTOCOL} enum, and each language's own enum, and whether they are in sync.

**Files to check (per roster project that applies):**

- the {ORM}/schema definition — all DB columns
- the {API_PROTOCOL} schema — all {API_PROTOCOL} types
- any project's DB-write layer — cross-boundary writes
- UI/client source — fallback chains, dual reads

**Report per finding** — `GHOST: {field_name}` · Where: `{file:line}` + `{file:line}` · What: the duplication · Risk: what breaks if you remove one side · Fix: which side to keep, which to remove.

---

## Category 2 — Dead Code

Code that is never called, never imported, or commented out and left to rot.

Unused imports, unused vars, dead locals/params, and commented-out Python are already linted (see the config pointers above). This category covers what linters cannot: unused exports, orphaned files, unreachable branches, dead call chains, and unused UI state.

**How to detect:**

- **Unused exports:** for each project, list exported functions/classes/constants and grep for their usage — an export with zero imports outside its own file is likely dead. Focus on service methods no resolver calls, utils nothing imports, types/interfaces never referenced, constants never used.
- **Commented-out code blocks (TS projects only):** 3+ consecutive lines starting with `//`. Python projects are covered by Ruff's `ERA` rules.
- **Unreachable branches:** `if (false)` / `if (true)` guards, switch cases that can never match, functions that always return early, error handlers for errors that can't occur.
- **Orphaned files:** `.ts` files in `src/` no import points to, `.py` files with no import and not in `__init__.py`, test files for modules that no longer exist, stale migration or seed-data files.
- **Unused state (UI-project-specific):** component state (e.g. `useState`) whose setter is never called elsewhere in the component, or whose value is never read.
- **TODO/FIXME archaeology:** `TODO`, `FIXME`, `HACK`, `XXX` comments — check whether the referenced work landed elsewhere.
- **Placeholder stubs (LLM artifact):** `throw new Error("not implemented")`, `raise NotImplementedError`, a lone `...` as a TS function body, bare `pass` in a non-empty class method, `// rest of implementation here`. Confirm whether the real implementation landed elsewhere or the stub shipped.

**Scope-specific checks (apply to whichever roster project fits the role):**

- **API/service projects:** walk resolvers → services → repositories. A repo method no service calls, whose service method no resolver calls, is dead.
- **UI/client projects:** a component file never imported by any route, screen, or parent component is dead.
- **AI/pipeline projects (if the roster has one):** a chain function the orchestrator never calls is dead; so is a prompt-registry entry no loader call site names.
- **Contracts/schema hub (if the roster has one):** an authored wire shape (message, frame, payload) that no emit/vendor script references and no consumer imports the vendored copy of is dead; the hub's emitted-artifact directory and consumers' vendored copies are never dead-code candidates — they are emitted and read by hash-pin, not static import (see the deadness bar above).

**Deadness bar:** a `Safe to remove: yes` verdict — and any sweep cut — holds only when the symbol clears the end-to-end deadness bar (§ Sweep Mode); absence from its own project is suspicion, not proof.

**Report per finding** — `DEAD: {symbol_name} in {file:line}` · Type: unused export | commented code | orphaned file | unreachable branch | unused state | stale TODO · Last meaningful use: git blame date if helpful, else "never" · Safe to remove: yes | yes but check X first | no because Y.

---

## Category 3 — Stale Dependencies

Packages installed but never imported, or imported but outdated/deprecated.

**How to detect:**

- **Installed but unused:** for each dependency in a project's manifest, grep that project's `src/` for any import of it; zero imports means stale. Exception: babel plugins, webpack loaders, jest transformers, pytest plugins — used by config, not imports. Check the config files before flagging.
- **DevDependencies in production:** any `devDependencies` imported from `src/` code.
- **Duplicate functionality:** multiple packages doing the same job (both `axios` and `node-fetch` for HTTP).
- **Phantom / hallucinated imports (LLM artifact):** an import naming a package absent from the manifest. AI confidently imports packages that don't exist — a supply-chain (slopsquatting) risk. Cross-check every imported package name against the manifest and flag any that resolve to nothing.

**Manifests:** one per roster project (`package.json` / `pyproject.toml` / etc.) — enumerate them rather than assuming a fixed list.

**Report per finding** — `STALE-DEP: {package_name} in {project}` · Listed in: dependencies | devDependencies | pyproject.toml · Imports found: 0 | N (list files) · Verdict: remove | keep (used by config) | investigate.

---

## Category 4 — Architectural Smells

Patterns that work but are structurally wrong — they'll cause pain as the codebase grows.

Blind excepts and unused arguments are covered by the Python linter (Ruff `BLE`, `ARG`). God files, god functions, deep nesting, and complexity are in no linter configured here — they live in this category because they need semantic context.

**How to detect:**

- **Cross-boundary writes:** each roster project should only write to the tables it owns, and its write boundary belongs in a declared allowlist gated by a test (every raw-SQL write target in `src/` listed; every entry referenced). Read the allowlist for the currently sanctioned exceptions — a write to another project's table that is not declared there is the finding. A project with no such allowlist is itself a finding.
- **Wire-contract drift-gate coverage (if the roster has a contracts/schema hub):** every wire surface (API schema type, queue message, WS frame, REST/SSE payload) needs three links — an author→artifact drift test in the authoring project, an artifact→binding hash pin in the consuming project, and a completeness enumeration test (sorted sets, never counts). A surface missing one of the three, or a comment asserting a gate/protection with no named pinning test backing it, is the finding.
- **God classes/modules:** classes or modules with too many methods (>15) or mixed responsibilities — a repository class accumulating unrelated save paths, or a settings/config model with 30+ flat fields that should be nested sub-models.
- **Circular dependencies:** module A imports from B, B imports from A.
- **Shallow or unsafe error handling (LLM-prone):** AI optimizes for the happy path. Flag silent swallowing (`except.*:\s*pass`, empty `catch {}`); over-broad catches (`except Exception`, bare `except:`, `catch (e)`) that neither re-raise nor log the stack trace; resource acquisition (DB connection, cursor, file) with no `finally`/`with` to release on error paths; retry loops with no backoff. The same problem handled differently across files is also a smell.
- **Missing abstractions / wrong layer:** SQL strings in the service layer (belongs in a repository), business logic in resolvers (belongs in services), API/service resolvers with inline parallel fan-out (e.g. `Promise.all()`) doing parallel DB queries, UI/client components querying {API_PROTOCOL} directly instead of through a custom hook.
- **N+1 query patterns:** {API_PROTOCOL} resolvers that trigger a DB query per item in a list.
- **Over-engineering / speculative generality (LLM-prone):** AI defaults to over-built "enterprise" shapes for simple tasks. Flag an interface/`Protocol`/abstract base class with exactly one implementor, a factory/builder that always returns one concrete type, a wrapper class that only delegates to one member, a config object threaded through layers with most fields never read, or generics parameterized at a single call site. Only flag when no second consumer is foreseeable. (Duplication itself → Category 8.)

**Report per finding** — `SMELL: {pattern_name}` · Where: `{file:line}` · What: the description · Impact: what goes wrong as the codebase grows · Fix: the recommended refactor.

---

## Category 5 — Type Safety Gaps

Places where TypeScript strict mode or Python type hints are bypassed, or where types are structurally weak.

`no-explicit-any`, `consistent-type-assertions`, `no-non-null-assertion`, and `ban-ts-comment` are configured in the TS lint configs; Ruff `PGH` catches `# type: ignore` without an error code. This category covers what they cannot.

**How to detect:**

- **`Any` usage (Python):** `: Any`, `-> Any` in Python source — each needs a justification comment per that project's `CLAUDE.md`.
- **`# type: ignore` without justification (Python):** each needs a comment explaining WHY.
- **Duplicate type definitions:** the same interface/type defined independently in multiple files with different shapes — grep identical interface/type names across files.
- **Overly broad types:** `string` for a known value-set (should be a union/enum), `object` or `{}` for typed data, a loose string-keyed map for structured data that should be a typed schema model.
- **Double `as any` (TS):** `as any) as any` — the developer gave up on typing entirely.
- **Hallucinated API calls (LLM artifact):** AI calls methods/attributes that don't exist or passes wrong argument shapes — confident, plausible, and sometimes type-checking against loose types. Run `tsc --noEmit` and grep its output for "Property … does not exist"; for Python projects run `mypy`/`pyright` and look for missing-attribute errors. Pay special attention to recently changed or low-frequency third-party APIs.

**Report per finding** — `TYPE-GAP: {type} in {file:line}` · Code: the offending line · Risk: what could go wrong at runtime · Fix: the proper type, or "add justification comment".

---

## Category 6 — Naming Inconsistencies

Same concept with different names across projects, or naming that doesn't follow conventions.

**How to detect:**

- **Cross-project naming:** the same domain concept should carry the same name in every roster project — check the key domain terms.
- **Service method prefix inconsistency (API/service projects):** `find*` for read-by-criteria (repo), `get*` for read-by-id (service), `list*` for read-all/paginated, `create*`/`add*` for inserts, `update*` for modifications, `delete*`/`remove*` for deletions.
- **Domain terminology drift (UI/client projects):** "{SESSION_NOUN}" vs "appointment", "{Entity}" vs "Person" vs "People".
- **File naming convention violations:** follow each project's own convention (e.g. TS API projects `kebab-case.ts`; TS UI projects `PascalCase.tsx` for components and `camelCase.ts` for utilities; Python projects `snake_case.py`).
- **Snake_case leaking into TypeScript:** TS types mirroring DB columns drag snake_case field names in.
- **Inconsistent error code naming (API/service projects):** error constants that don't match the actual entity.
- **Boolean parameter naming:** bare `consent: boolean` or `force: boolean` — ambiguous at call sites.
- **Scope-dishonest destructive names:** a delete/erase/clear/reset op named for a broader scope than it performs — `delete{Subject}` that removes only {subject}-level rows (not the {subject} record or {session}-level data), `clearCache` that clears one key. Name it for what it actually touches (`delete{Subject}AnalysisData`). Highest-risk on erasure / {REGULATION} Art. {N} paths: a partial delete behind a total-sounding name is a compliance trap a future caller wires straight to.

**Report per finding** — `NAMING: {the inconsistency}` · Places: `{file:line}`, `{file:line}`, … · Convention: what it should be · Fix: rename A to B, or B to A.

---

## Category 7 — Code Quality & Clean Design

Readability, maintainability, and design patterns. These issues don't cause bugs today — they cause bugs tomorrow.

Nested ternaries, `console.*`, `print()`, and line length are already linted (`no-nested-ternary`, `no-console`, Ruff `T20`, `max-len` / Ruff `E501`). This category covers what they cannot.

**How to detect:**

- **Magic strings & numbers:** literal values used directly in logic instead of named constants.
  - **Domain value-set comparisons:** role/status/type literals compared as raw strings (`=== '{ROLE_USER}'`, `=== "{STATUS_LITERAL}"`) — a fixed value-set must be a typed enum referenced everywhere, never a string retyped per site. A value-set with no enum at all (grep the same literals across many files) is the root finding, not a per-site nit.
  - **Hardcoded hex colors (UI projects):** `#[0-9a-fA-F]{3,8}` in component files — should use theme classes.
  - **Magic numbers:** timeouts, retry counts, buffer sizes without named constants.
- **Hardcoded i18n strings (UI projects):** user-visible string literals in markup that bypass the translation function (e.g. `t()`) — text-element children, ternary copy (`cond ? 'one' : 'other'`), accessibility labels. Grep the changed UI set for these; plurals must use the i18n library's plural keys (e.g. `_one`/`_other`), never an inline ternary. Prose review misses these by recall; the durable backstop is a lint guard (e.g. `react/jsx-no-literals` scoped to text components) — flag its absence rather than relying on the human eye.
- **UI component design violations:** inline sub-components (function/const component declarations inside another component — re-created every render); data fetching in presentation components (queries/mutations inside modals/leaf components); callbacks passed as props without `useCallback` (or the framework equivalent).
- **Python `__init__.py` hygiene (Python projects):** empty when they should export, or stuffed with logic when they should be thin.
- **Overly complex expressions:** chained optional access >3 levels, long boolean conditions that should be extracted.

**Report per finding** — `QUALITY: {issue_type}` · Where: `{file:line}` · What: the description · Impact: readability | maintainability | correctness risk · Fix: the specific improvement.

---

## Category 8 — Duplication & Missed Reuse (DRY)

The top failure mode of AI-authored code: it regenerates logic instead of importing what already exists, so the same function, component, hook, type, or query fragment gets written many times instead of once-and-called. Clones carry their source's bugs to every copy and drift apart over time. Highest-yield category — run it first.

**How to detect:**

- **Reinvented helpers (the core check):** for each new or changed function/util, grep the codebase for an existing export that already does the job. AI writes a fresh `formatDate`/`validateEmail`/`apiClient` because it never searched for the one in `utils/`. Signal: inline logic (date formatting, HTTP calls, validation, DB session creation) appearing outside the project's designated `utils/`/`services/`/`hooks/`/repository location.
- **Near-duplicate components (UI projects):** two component files with near-identical import lists and markup structure (`UserCard` vs `{Role}Card`) — should be one parametric component with a `variant`/`role` prop.
- **Duplicated hooks (UI projects):** the same `[data, loading, error]` async-state body repeated across components instead of one shared `useAsync`/`useResource` hook. Grep for repeated async-state + fetch patterns outside `hooks/`.
- **Repeated query fragments:** the same {ORM} `.where(eq(...))` or {AI_FRAMEWORK} `.filter(...)` clause at ≥3 call sites — extract a shared query helper.
- **Duplicate definitions:** the same typed schema model, {API_PROTOCOL} type, {AI_FRAMEWORK} chain `description=`, tool signature, constant, or `beforeEach` test-setup body defined in multiple files. Grep symbol/description names for cross-file duplicates.
- **Near-duplicate-with-variation:** copies that look identical now but will drift — flag them before one gets fixed and the others don't.
- **Repeated membership/permission predicates:** the same multi-value test (`role === '{ROLE_USER}' || role === '{ROLE_SUPER}'`, status-set checks) written at ≥2 sites — extract one named predicate (`canRecord{Session}(role)`) so the policy lives in one place and call sites read intent, not values. Divergent copies of the "same" check are an authorization-drift bug, not just duplication.

**Detection aids (use what's installed; fall back to grep):** `jscpd --min-lines 5 --pattern "**/*.{ts,tsx}"` for TS/React; PMD CPD for Python. A git add/delete ratio above ~2.5 over recent history signals new code piling up instead of replacing old.

**Report per finding** — `DUP: {what is duplicated}` · Copies: `{file:line}` ↔ `{file:line}` [↔ …] · Existing original: `{file:line}` if one already existed, else "none — N parallel copies" · Drift risk: what breaks when one copy changes and the others don't · Fix: extract to `{location}`, call it from each site.
