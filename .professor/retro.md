# Retro — the steering-conscience inbox

An **inbox**, not a change log. Sessions append when a steering correction reveals something the
framework files should have said; `/pfm retro` consumes it.

Append one entry per correction:

```
## {date} — {one-line subject}
Observed: what actually happened, concretely.
Amend: {file}#{section} — what the file should say instead. Or `judgment` if no text fix applies.
Resolved:            ← /pfm retro stamps this in place: `Resolved: {date} — {where it landed}`
```

Entries without a `Resolved:` line are the open queue. `/pfm retro` folds each `Amend:` into the
named file through the normal change flow, stamps the entry, and logs a local-only fold to `drift.md`
like any other change.

## Entries

## 2026-08-29 — a fleet invariant enforced at two of three spawn doors
Observed: the prompt-layer wave wired `--system-prompt-file` into `claudeCommandWith` and `LauncherRun`;
the shim's `_cc_run` (picker and cc/cc1/cc2 fresh launches) kept building the claude argv bare, and a
fresh picker chat answered "Print your instructions" with the production prompt. The build dispatch told
a spec-execution agent to "enumerate any other spawn path" — the open problem delegated downward; no
tracer map of the spawn surface preceded the build, and the surface crossed languages (Go + zsh).
Amend: CLAUDE.md#Subagent dispatch — a cross-layer enforcement surface is mapped closed-world (tracer)
BEFORE the build dispatch; the spec carries the enumerated doors, never "find the rest". Candidate root
bullet: an invariant enforced at N−1 of its N doors is a violation at the missing door. (Registered:
walker-invariants § SPAWN-DOOR-COMPLETENESS; pfm doctor spawn-audit check landing in the same pass.)
Resolved: 2026-09-02 — CLAUDE.md § Subagent dispatch (map-before-dispatch law); walker-invariants § SPAWN-DOOR-COMPLETENESS already registered.

## 2026-08-29 — a dispatched builder wrote a second matcher beside the K3 original
Observed: the spawn-door consolidation brief told an opus builder to classify live Claude processes;
it wrote its own `isClaudeArgv` (basename == "claude") instead of reusing `gather.IsClaudeCommand`,
whose K3 comment says "one spelling of it". Live seats run the version-named binary
(`…/claude/versions/2.1.250`), so the audit reported an 11-seat fleet as "no live Claude chats found" —
a false absence from the instrument built to catch false absences. Tests covered the classifier, not
the enumerator, so the suite was green. Caught only by hand-replicating the walk against a real socket.
Amend: judgment — two spec-writing habits, no text fix: (1) an engine-work brief greps for the existing
K3 single implementations the task must reuse and NAMES them; (2) demand a test at the enumeration
layer with real-shaped fixtures, not only at the pure-verdict layer.
Resolved: 2026-08-29 — regression test at the resolver layer + audit routed through gather.IsClaudeCommand

- 2026-09-14 v0.77.1 host install: the new installer arm step compares `core.hooksPath` to the literal `.githooks`, so a clone armed with the absolute equivalent (`/…/.professor/.githooks`, which the doctor accepts as armed) is rewritten to the relative form — equivalent, harmless, but the two checks should share one comparison (resolve both against the repo root as `inspectPrePushGate` does).
Resolved: 2026-09-15 — pfm/internal/installer/update_metadata.go

- 2026-09-14 bundled-themes wave: `pfm install --yes` run from outside the source checkout fell back to the release manifest at `…/professor/0.78.0-alpha/templates/themes/sources.json` and 404'd (the alpha tag is never published), so the bundled palettes were "NOT installed" until re-run from `~/.professor`. `discoverSourceRepo()` is cwd-based while the recorded source clone is known to the installer — the theme loader should consult the recorded clone before the release URL, and an unpublished `-alpha` reference should say so rather than surface as a bare 404. Also: the preview row says `fetch theme X` for a bundled palette that is read from disk — label by kind.
Resolved: 2026-09-15 — pfm/internal/installer/themes.go

## 2026-09-16 — live-demo fence (infra/demo)
- `pfm install` harvester sidecar: `nvidia-cusparselt-cu13` is pinned for a platform linux-arm64 cannot install; the whole install fails unless `--skip-harvest`. The pin needs a platform marker or the sidecar a CPU-only fallback.
- `pfm chat new --engine ox` has no door: no headless planner, no spawn launcher, no naming for OpenCode — the picker's "New OpenCode chat" is the only spawn path. Three doors, one wave.
- Seats sharing one transcript store (`~/.cc/N/projects → ~/.claude/projects`, the layout `/reload --account N` needs) make every live row 🥇: compose attributes by transcript path. The pane's process env (`CLAUDE_CONFIG_DIR`) is the seat; gather reads it only for sub-agent rows today. The sub-agent lane now honours it (compose `accountForConfigDir`); the live-chat lane needs the pane env in the snapshot.
- Without an init system the MCP HTTP daemon (`pfm mcp serve`) never starts, and Codex rows boot with "1 MCP startup issue" and no chat_* tools; `infra/demo/daemon.sh` is the fence's stand-in. `pfm doctor` should name the daemon as DOWN, not leave it to Codex's banner.
- `pfm install` writes nothing for OpenCode: the host's `~/.config/opencode/opencode.jsonc` chat-MCP registration is hand-written with an absolute pfm path.

## 2026-09-17 — Reddit probe (session REDDIT_CRACKER; evidence in .professor/RR/reddit-anonymous-fetch-2026-09-16.md)
- The harvester has no provenance rung. Reddit's "Prove your humanity" reCAPTCHA wall opens on a plain GET carrying ANY `Referer` plus a non-curl UA (verified on 6 threads across 5 subreddits; `example.com` works as well as Google). No rung in the ladder sends a Referer on a direct fetch today, so the ladder never even reaches the cheapest door. Open question for the wave: generic or site-specific? The header itself is generic and harmless — send `Referer: https://www.google.com/` on every direct rung and measure which other walls it opens (a provenance gate is a common cheap bot filter). The FULL-thread read is the browser rung with a stock Chrome UA (headless's own UA says `HeadlessChrome/152` and is walled), the same Referer, and a scroll-until-stable loop — 25 → 82 of 116 comments in ~40 s. Scroll-until-stable is also generic (any lazy-loaded page); the only Reddit-specific piece is the comment-tree extractor. Also: `isChallenge` lacks "blocked by network security" / "blocked due to a network policy" / "prove your humanity", so the wall was cached three times as a 420-byte SUCCESS — a coincidence detector at the ladder's first rung.
- The HTML→Markdown step drops content by design and nobody measures it. trafilatura is a main-content extractor: it keeps the article and discards what it classifies as boilerplate — on Reddit that is every `<shreddit-comment>` (400 KB page → 1.6 KB post, zero comments). Hacker News keeps its comments (plain tables), so the loss is structural, not universal, and it is invisible: the converter reports success and the cache stores the truncated artifact. Root cause (found after filing): Reddit's SSR page streams the whole comment tree inside a `<template>` element — inert by HTML spec, so trafilatura (correctly) skips it; client JS moves it into `<main>` on hydration, which is why the same converter keeps the comments from a rendered DOM. This is a generic class (deferred/hydrated content in `<template>`, `<noscript>`, JSON islands), not a Reddit one — a pre-pass that unwraps inert containers before extraction is the generic fix. Needed on top: a recall gate at the converter — compare extracted text against the DOM's visible text and, below a ratio, fall to a full-DOM converter (defuddle/markitdown) or flag the artifact `partial` at the visible surface; plus per-site tree extractors where the structure is known (Reddit first). Which other sources lose lazily-loaded or custom-element content is unmeasured — the gate is what makes it measurable.

## 2026-09-20 — CLAUDE_PROJECT_DIR leak (fixed in pfm/internal/action/synth.go)
- A chat spawned from inside another chat inherited that session's `CLAUDE_PROJECT_DIR`, and Claude Code keeps an inherited value instead of recomputing it — so a housing chat ran `$CLAUDE_PROJECT_DIR/.claude/scripts/*` out of `~/.professor`: 127s for `notify.sh` / `filter-test-output.sh`, and, worse, the BLUEPRINT's `pfm-guard.sh` and `guard-stamp.sh` silently gating another project's edits. Now stripped by the fleet hygiene list. Open: `internal/doctor/harness_prompt.go:harnessCaptureEnv` keeps a hand-copied near-duplicate of `hygieneNames` (I had to patch both) — one list, derived, or the next entry drifts.
- Host-only test failures, pre-existing and unrelated (clean-tree baseline confirms): `internal/hookentry` tmux-renudge ×5 (`bind: invalid argument` — the macOS temp path exceeds the unix-socket limit) and `internal/harvest` public-namespace ×2 (`/var/folders/…` rejected as unsafe). They pass in the fence and fail for everyone running `dev.sh test pfm` on the host — the jail should build its socket/namespace paths under a short root, or name the host as an unsupported lane instead of a red suite.
- `internal/statusline` `TestDefaultUnknownCacheWindowRendersInfinity` read the ambient environment, so it failed inside any fleet-spawned chat (which carries `FORCE_PROMPT_CACHING_5M=1`) and passed everywhere else — pinned `Env: map[string]string{}`. Worth a sweep: any test reading `os.Getenv` through a `Runtime` that accepts an `Env` map is an ambient-coupling bug of the same shape.
