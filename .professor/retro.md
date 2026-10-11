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
named file through the normal change flow and stamps the entry.

## Entries

## 2026-08-29 — a fleet invariant enforced at two of three spawn doors
Observed: the prompt-layer wave wired `--system-prompt-file` into `claudeCommandWith` and `LauncherRun`;
the shim's `_cc_run` (picker and cc/cc1/cc2 fresh launches) kept building the claude argv bare, and a
fresh picker chat answered "Print your instructions" with the production prompt. The build dispatch told
a mechanical-tier agent to "enumerate any other spawn path" — the open problem delegated downward; no
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

- 2026-09-14 v0.77.1 host install: the new installer arm step compares `core.hooksPath` to the literal `.githooks`, so a clone armed with the absolute equivalent (`/…/.professor/.githooks`, which the doctor accepts as armed) is rewritten to the relative form — equivalent, harmless, but the two checks should share one comparison (resolve both against the repo root as `inspectPrePushGateWithRunner` does).
Resolved: 2026-09-15 — pfm/internal/installer/update_metadata.go

- 2026-09-14 bundled-themes wave: `pfm install --yes` run from outside the source checkout fell back to the release manifest at `…/professor/0.78.0-alpha/templates/themes/sources.json` and 404'd (the alpha tag is never published), so the bundled palettes were "NOT installed" until re-run from `~/.professor`. `discoverSourceRepo()` is cwd-based while the recorded source clone is known to the installer — the theme loader should consult the recorded clone before the release URL, and an unpublished `-alpha` reference should say so rather than surface as a bare 404. Also: the preview row says `fetch theme X` for a bundled palette that is read from disk — label by kind.
Resolved: 2026-09-15 — pfm/internal/installer/themes.go

## 2026-09-16 — live-demo fence (infra/demo)
- `pfm install` harvester sidecar: `nvidia-cusparselt-cu13` is pinned for a platform linux-arm64 cannot install; the whole install fails unless `--skip-harvest`. The pin needs a platform marker or the sidecar a CPU-only fallback.
- `pfm chat new --engine ox` has no door: no headless planner, no spawn launcher, no naming for OpenCode — the picker's "New OpenCode chat" is the only spawn path. Three doors, one wave.
- Seats sharing one transcript store (`~/.cc/N/projects → ~/.claude/projects`, the layout `/reload --account N` needs) make every live row 🥇: compose attributes by transcript path. The pane's process env (`CLAUDE_CONFIG_DIR`) is the seat; gather reads it only for sub-agent rows today. The sub-agent lane now honours it (compose `accountForConfigDir`); the live-chat lane needs the pane env in the snapshot.
- Without an init system the MCP HTTP daemon (`pfm mcp serve`) never starts, and Codex rows boot with "1 MCP startup issue" and no chat_* tools; `infra/demo/daemon.sh` is the fence's stand-in. `pfm doctor` should name the daemon as DOWN, not leave it to Codex's banner.
- `pfm install` writes nothing for OpenCode: the host's `~/.config/opencode/opencode.jsonc` chat-MCP registration is hand-written with an absolute pfm path.

## 2026-09-19 — unauthorized chat kill is possible: the fleet has no chat ownership
Observed: the chat MCP server (`/mcp/chat` on loopback, and its stdio twin) answers any caller, and takes the caller's identity from the thread id the client itself sends. Any chat — or any local process — can `chat_kill`, `chat_inject`, `chat_keys` or `chat_name` ANY other chat; nothing records who spawned whom. The testing-foundation Fable review named it (L2-F2); it was left out of that train by the user's ruling.
Amend: a refined spec under `docs/dev/` (own train) — introduce CHAT OWNERSHIP. `chat_new` records the spawning chat as the new chat's owner in the fleet store (the identity the daemon derives, never a tool input — the way `issue_servicedesk` captures its reporter). `chat_kill` is allowed only for (a) the owner, (b) the chat itself (`--self`), (c) the human at a shell or the picker; every other caller gets a named refusal ("chat X is owned by Y"), never a silent no-op. Decide in the spec: whether `chat_inject` / `chat_keys` / `chat_name` follow the same rule or stay open (peer messaging is the fleet's purpose); what owns a chat the human started (no owner = human-only kill); what `chat_unkill` requires; and how ownership survives `/reload` and a resumed session. The enforcement point is the daemon's derived identity, so the spec must first close the forgeable-thread-id door it rests on. Map every kill door closed-world first (CLI, MCP, picker, reap, hooks) — an owner check at N−1 doors is the bug.
Resolved: 2026-09-22 — dropped by ruling (chat ownership not wanted); caller identity itself landed in chat-mcp (6f42261c..38052f15)

## 2026-09-19 — engine parity: Codex and OpenCode trail Claude across the whole surface
Observed: a closed-world map of `develop` (three tracer passes: the pfm engine, hooks + host wiring, the mirror compilers) with Claude as the anchor. pfm registers six core capabilities per engine (`cmd/pfm/engines.go`): Claude 6, Codex 6, OpenCode 2 (index + process matcher). The gaps, per engine:
- OpenCode, engine: no managed launcher (`chat new --engine opencode` refuses), no usage source, no headless planner, no ask runner (so no `chat ask`, no status summary); no transcript reader (`chat last` / `read` / `find` / `save` unsupported); no rename / name sync; no `--model` / `--effort` on its spawn and resume lines; no system-prompt injection at launch; no in-place reload (refused by name); no archive; no statusline, context gauge or cost stats; no doctor engine probe and no platform dependency gate; no `/clear`-hides-the-chat path (Claude: SessionEnd hook; Codex: fleet pane-binding reconcile); `whoami` by socket prefix only (the engine exports no session variable); the inject input-cleared proof cannot see the boxed composer; an idle-pane inject reported "queued, busy".
- OpenCode, hooks and install: no hook mechanism at all (the generator owns only `$schema`, `permission`, `mcp`) — no SessionStart, SessionEnd, UserPromptSubmit, PreToolUse equivalents; the guarded-file and git-write denies exist only per project, the host global config carries no `permission` block; `pfm install` never runs the OpenCode compiler (its one caller is the CLI); global tier is commands only, written as real files, and uninstall never sweeps them; MCP foreign-entry inspection names two clients, not three.
- OpenCode, compiler and blueprint: agent `tools` and `model` frontmatter are dropped (model survives as a prose sentence); commands keep only `description`; no hand-editable config keeper; no `templates/project/opencode/`; zero `refresh-map.json` entries; no SETUP.md generation phase; no placeholder tokens; `scripts/check-opencode-writer.mjs` exists with zero invocation sites.
- Codex: `chat find` searches Claude transcripts only; the MCP search tool is hardcoded to Claude; no booting / sub-agent / split-seat rows and no pane-label capture (Claude-only branches); reload `--new` rename is Claude-only; statusline is a cache refresh nothing renders; no cost stats; hooks are SessionStart only (no SessionEnd, UserPromptSubmit, PreToolUse); guarded-file and git-write denies are prose for writable agents and the main chat; per-agent model is a TOML comment, the tool allowlist collapses to one read-only bit, gitter authority is emergent from its tool list rather than declared; no launcher shim.
- All engines: git-write deny for non-gitter is prose on Claude too; agent-frontmatter `hooks:` are dropped by both compilers; two child `CLAUDE.md` files (`.claude/skills/deep-rr`, `workflows/deep-rr`) have no `AGENTS.md` twin; this repo has no `.mcp.json`, so both project MCP fences are empty and unexercised.
Landed the same day (no longer gaps): a live OpenCode seat (detection, live row, addressability, pane-read status, `/exit` kill, up-front reload refusal); the headless-OpenCode port is in flight.
Amend: a refined spec under `docs/dev/` (own train, after the OpenCode-live and headless-port waves close) — ENGINE PARITY. One wave per capability family, Claude as the reference behaviour, each wave tracer-mapped closed-world first; every cell that stays unsupported answers with a NAMED refusal, never absence. Order by what unblocks the rest: launcher + transcript reader first (they gate `chat new`, `last`, `read`, `find`, `save`, `ask`), then model / effort / prompt injection, then usage + statusline, then the hook-equivalent layer (OpenCode plugins are the candidate mechanism — RND first), then the blueprint side (template dir, refresh-map, SETUP phase, placeholders). Add a parity table to `docs/dev/pfm-surface.md` generated from the code (the `engineCapabilityExceptions` map is the seed) so the matrix cannot rot.
Resolved: 2026-09-22 — partial: compilers emit real model/effort/tools (b030e015), Codex global roles as regular files (7eb55795), `ask` dropped for all engines by ruling; still open: hook adapters, OpenCode launcher/headless slots, guard fixes — queued as the parity flights

## 2026-09-19 — engine configuration is scattered: no single place, no discoverability
Observed: what configures each harness lives in several unrelated places with no index. The engine global config file (`~/.claude/settings.json` + `~/.claude.json`, `~/.codex/config.toml` + `~/.codex/hooks.json`, `~/.config/opencode/opencode.jsonc`), part pfm-owned inside fences or ownership ledgers and part hand-set (the host Codex `approval_policy` and `writable_roots` are hand-written; pfm writes only the multi-agent timeout keys and the MCP fence); environment variables set at launch (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `PFM_*_ROOT(S)`, the Claude launch env pair); inline launch flags built in Go (`--system-prompt-file`, `--settings`, `--model`, `--effort`, `-c model_reasoning_effort=`, `-c model_instructions_file=`, `--session`); pfm config (`claude.systemPrompt`, account rosters, binaries); per-project generated files (`.codex/config.toml` keeper + fence, `.opencode/opencode.jsonc`); and the user shell functions (`cx`). The same setting can be expressible in two of these at once with no stated precedence. Consequence the user named: asked to "change X for Codex", an agent can edit the wrong layer — the change is overridden by a flag, overwritten by the next install, or lands in a file the engine never reads — and nothing tells it where the effective value comes from.
Amend: a refined spec under `docs/dev/` (own train, RND first) — ONE CONFIGURATION TRUTH PER ENGINE SETTING. Decide: (1) an inventory, generated from the code, of every setting pfm influences per engine — its layer (engine file / env / flag / pfm config / project mirror), its owner (pfm / user), its precedence, and whether install rewrites it; (2) a single declared home for each pfm-owned setting, with the other layers derived from it, never hand-edited; (3) a discoverability verb (`pfm config where <engine> <setting>` or a `pfm doctor` section) that prints the effective value and the layer it came from, so an agent asks before it edits; (4) the agent-facing rule in the fleet prompt / `CLAUDE.md` naming that verb as the first step of any engine-config change; (5) what happens to hand-set values inside pfm-managed files (preserve + report, as the MCP ownership ledger already does). Map every writer and reader of each layer closed-world before the spec.
Resolved: 2026-09-22 — judgment: OpenCode model map now lives in pfm config (b030e015); the rest stays as is

## 2026-09-19 — a chat keeps the pfm build it was born with: every stdio MCP server outlives every install
Observed: each chat launches `pfm mcp chat serve` once over stdio and holds it for the whole session; `pfm install` replaces the binary underneath and the server keeps answering from the deleted build (nine such processes on one host, the oldest two days stale — a kill by session id answered "killed" from a build that predated the live OpenCode seat and closed nothing). A guard that ended the server on replacement was built, proven in a real chat, and withdrawn the same day: Claude Code does not relaunch a stdio server that exits ("configured MCP server failed to connect: chat"), so the guard trades a stale answer for no chat tools at all in every running chat, self-compact and inject included. Kept from that work: `RunStdio` returns when its context ends even while the client holds stdin open (it used to wait on the blocked reader forever, which is why the guard first looked like it worked in tests and did nothing on a host).
Amend: a refined spec under `docs/dev/` — CHATS REACH pfm THROUGH THE DAEMON. The loopback HTTP daemon (`pfm mcp serve`) already restarts on install and already serves `/mcp/chat`; decide whether installed chats register the chat server as an HTTP MCP entry instead of stdio (identity then has to travel in the request, since the daemon has no chat ancestry — the open design question), or whether the stdio process becomes a thin proxy to the daemon that carries its own identity. Until then `pfm doctor` names every `pfm mcp … serve` process running a replaced executable, with its chat, so a stale server is visible instead of silent.
Resolved: 2026-09-22 — chat-mcp flight: daemon-backed chat MCP, stdio thin proxy, doctor names stale serve processes (6f42261c..38052f15)

## 2026-09-20 — headless exec: `--engine codex` must run the Codex CLI again (user ruling)
Observed: the headless-OpenCode port (89db9254) made `--engine codex` and `cx` aliases that route through OpenCode, so a caller asking for Codex silently gets a different engine.
Amend: `pfm/internal/headless/run/engine_selector.go` (+ `internal/headless/README.md`, `docs/dev/pfm-surface.md`) — RULED by the user: `--engine codex|cx` runs the Codex CLI. Every common control Codex cannot honour (`--system` replacement, `--json-schema` / `--schema`, `--tools`, `--sealed`, and any other flag the OpenCode plugin alone implements — enumerate them from the code, closed-world) is a named pre-launch refusal that says the flag is not supported for Codex and to use `--engine opencode`; never a silent drop, never a reroute. Tests watched red first.
Resolved: 2026-09-22 — still open; queued in the parity flights (broken guards first)

## 2026-09-20 — headless OpenCode control flags are unverified on a real model (test authorized)
Observed: `--system`, `--json-schema` and `--tools` on `--engine opencode` were never proven end to end: the free model `opencode/big-pickle` refuses a replaced system prompt and the plugin controls ("free tier can only be used from within OpenCode"); pfm surfaces that cleanly (exit 4, no server left behind) but the controls themselves are untested.
Amend: no file — a verification job. The user AUTHORIZED spending paid-model prompts for it without asking again: one run per control on a paid OpenCode model (not the OpenAI quota while it is near its cap), asserting the system prompt is really replaced, the output validates against the schema, and a tool outside the allowlist is refused. A failure becomes its own entry.
Resolved: 2026-09-22 — proven by run in tmp/flights/engine-parity/experiments/opencode.md and tmp/flights/agent-role/experiments/opencode.md

## 2026-09-20 — open defects and parked jobs carried out of the OpenCode close-out
Observed: found during the live tests and the SPEC-C build, none fixed:
- `fleet.ResolveRow` (`fleet/scan.go`) renders its own errors as absence — a failed look reads as "no chat named".
- `pfm chat resolve` answers from a different resolver ladder than every other chat verb.
- `kill/obs.go` `requestShape` cannot tell an id target from a socket target.
- One `chat_inject` into an idle OpenCode pane answered "queued, busy"; never reproduced, cause unknown.
- A fresh Claude chat with 0 prompts is missing from `pfm ls`.
- WARN-level log noise (uv stderr) on ordinary runs.
- `scripts/check-opencode-writer.mjs` exists with zero invocation sites — wire it into a gate or delete it.
- `chat new --engine opencode` is refused: no OpenCode launcher is registered (belongs to the engine-parity train).
Parked, unstarted: the chat-ownership train (entry above); the bind-mount lane test; the 24 unmerged old branches (triage: merge, port, or delete each); the untracked `engines/` directory (track it or remove it); the themes eye-check.
Amend: judgment — each bullet is folded into the train that owns it (resolver bullets and the launcher → engine parity; the rest → a small fixes wave) when that train is refined.
Resolved: 2026-09-22 — chat-verb defects (ResolveRow absence, resolver ladders, kill requestShape, idle inject) landed in chat-mcp; remaining OpenCode items queued in the parity flights; stale bullets: pfm install DOES write OpenCode MCP (installer/mcp.go mcpOpenCodeRegistration), check-opencode-writer.mjs IS invoked (dev.sh:311), .claude/skills/deep-rr twin no longer exists
## 2026-09-17 — Reddit probe (session REDDIT_CRACKER; evidence in .professor/RR/reddit-anonymous-fetch-2026-09-16.md)
- The harvester has no provenance rung. Reddit's "Prove your humanity" reCAPTCHA wall opens on a plain GET carrying ANY `Referer` plus a non-curl UA (verified on 6 threads across 5 subreddits; `example.com` works as well as Google). No rung in the ladder sends a Referer on a direct fetch today, so the ladder never even reaches the cheapest door. Open question for the wave: generic or site-specific? The header itself is generic and harmless — send `Referer: https://www.google.com/` on every direct rung and measure which other walls it opens (a provenance gate is a common cheap bot filter). The FULL-thread read is the browser rung with a stock Chrome UA (headless's own UA says `HeadlessChrome/152` and is walled), the same Referer, and a scroll-until-stable loop — 25 → 82 of 116 comments in ~40 s. Scroll-until-stable is also generic (any lazy-loaded page); the only Reddit-specific piece is the comment-tree extractor. Also: `isChallenge` lacks "blocked by network security" / "blocked due to a network policy" / "prove your humanity", so the wall was cached three times as a 420-byte SUCCESS — a coincidence detector at the ladder's first rung.
- The HTML→Markdown step drops content by design and nobody measures it. trafilatura is a main-content extractor: it keeps the article and discards what it classifies as boilerplate — on Reddit that is every `<shreddit-comment>` (400 KB page → 1.6 KB post, zero comments). Hacker News keeps its comments (plain tables), so the loss is structural, not universal, and it is invisible: the converter reports success and the cache stores the truncated artifact. Root cause (found after filing): Reddit's SSR page streams the whole comment tree inside a `<template>` element — inert by HTML spec, so trafilatura (correctly) skips it; client JS moves it into `<main>` on hydration, which is why the same converter keeps the comments from a rendered DOM. This is a generic class (deferred/hydrated content in `<template>`, `<noscript>`, JSON islands), not a Reddit one — a pre-pass that unwraps inert containers before extraction is the generic fix. Needed on top: a recall gate at the converter — compare extracted text against the DOM's visible text and, below a ratio, fall to a full-DOM converter (defuddle/markitdown) or flag the artifact `partial` at the visible surface; plus per-site tree extractors where the structure is known (Reddit first). Which other sources lose lazily-loaded or custom-element content is unmeasured — the gate is what makes it measurable.

## 2026-09-20 — CLAUDE_PROJECT_DIR leak (fixed in pfm/internal/action/synth.go)
- A chat spawned from inside another chat inherited that session's `CLAUDE_PROJECT_DIR`, and Claude Code keeps an inherited value instead of recomputing it — so a housing chat ran `$CLAUDE_PROJECT_DIR/.claude/scripts/*` out of `~/.professor`: 127s for `notify.sh` / `filter-test-output.sh`, and, worse, the BLUEPRINT's `pfm-guard.sh` and `guard-stamp.sh` silently gating another project's edits. Now stripped by the fleet hygiene list. Open: `internal/doctor/harness_prompt.go:harnessCaptureEnv` keeps a hand-copied near-duplicate of `hygieneNames` (I had to patch both) — one list, derived, or the next entry drifts.
- Host-only test failures, pre-existing and unrelated (clean-tree baseline confirms): `internal/hookentry` tmux-renudge ×5 (`bind: invalid argument` — the macOS temp path exceeds the unix-socket limit) and `internal/harvest` public-namespace ×2 (`/var/folders/…` rejected as unsafe). They pass in the fence and fail for everyone running `dev.sh test pfm` on the host — the jail should build its socket/namespace paths under a short root, or name the host as an unsupported lane instead of a red suite.
- `internal/statusline` `TestDefaultUnknownCacheWindowRendersInfinity` read the ambient environment, so it failed inside any fleet-spawned chat (which carries `FORCE_PROMPT_CACHING_5M=1`) and passed everywhere else — pinned `Env: map[string]string{}`. Worth a sweep: any test reading `os.Getenv` through a `Runtime` that accepts an `Env` map is an ambient-coupling bug of the same shape.

## 2026-09-20 — pfm doctor never checks the machine-global registry links
Observed: `pfm install` links every `templates/global/{agents,commands,skills}` original into `~/.claude/` (and the generated variants, e.g. `super-rr`, from pfm's generated dir), but `pfm doctor` has no check over those links — `grep -n "symlink\|Lstat\|EvalSymlinks" pfm/internal/doctor/*.go` finds only the statusline and overlay checks. A missing link (a new original such as `sub-rr` / `sub-tracer` never linked), a dangling one, a link pointing outside the blueprint clone, or a real file shadowing the original (the hand-symlinked `tracer` case) all read as a healthy install; the first symptom is an `Agent type not found` mid-run.
Amend: `pfm/internal/doctor` — a registry-links check: enumerate the originals plus `variants.json` closed-world, and for each report `OK` / `MISSING` / `DANGLING` / `WRONG-TARGET {where it points}` / `SHADOWED (real file)`; a registry dir that cannot be read reports `UNREAD — {error}`, never a clean pass. Same walk for `~/.codex/agents/` twins.
Resolved: 2026-09-22 — doctor opens each Codex role the way Codex does (installed/SYMLINK/MISMATCH/MISSING/UNREADABLE) and audits role seats' prompt channel (7eb55795, b030e015)

## 2026-09-20 — an agent description with ": " in it sends the model line's comment to the API
Observed: a `description:` holding a colon-space (`… on any code entity: a stored table …`) is invalid as a YAML plain scalar. The harness then falls back to a line parser that keeps inline comments, so `model: sonnet # spec-execution default — retune to your model tier` reaches the API verbatim and every spawn dies with `model_not_found` (HTTP 404). `mapper` died twice this way; the shipped global `tracer.md` carried the same description and would have failed for every adopter. `ruby -ryaml -e 'YAML.safe_load(...)'` over `templates/global/agents/*.md` still fails on `speccer.md` line 3, which also carries a commented `model:` line.
Amend: fix `speccer.md`'s description (no colon-space inside the scalar, or quote it); add a gate that strict-YAML-parses every agent, command and skill frontmatter under `templates/**` and `.claude/**` and reports `FRONTMATTER-INVALID {file}:{line} — {parser error}`, a file it could not read as `UNREAD — {error}`, never a clean pass. `quality:description` gains the rule: a description is a valid YAML plain scalar or it is quoted.
Resolved: 2026-09-22 — codexgen strips trailing inline comments from scalar frontmatter values (b030e015 task 4-a)

## 2026-09-20 — the agent roster keeps a broken parse for the whole session
Observed: the roster parses an agent file once under its name. `mapper`, first read while its frontmatter was broken, kept failing with the commented model name after the file was fixed; the identical file under a fresh name ran on the right model. New names enter the roster only in a turn that a user message or a task notification starts while the chat is idle — a launch retried inside the same turn, or inside a turn a Stop hook continued, fails `Agent type not found` however long it waits; the working recipe is to arm a background wake-up, end the message, and launch in the turn its notification starts. A lead that omits `subagent_type` gets a `general-purpose` agent at the lead's own model and rate, with no error: one mapper run billed its four walkers at three times the walker rate and never exercised the walker prompt.
Amend: `docs/` note for prompt work on agents — benchmark a changed agent under a fresh name, launch it on the turn after the file lands, and meter the walker rows for their agent type before reading a report. The tracer and mapper leads now state the `subagent_type` consequence beside the brief block and name each walker's agent type in TELEMETRY.
Resolved:

## 2026-09-21 — harvester `searchCache` names a match by handle, never by URL, and errors on numeric patterns
Observed: a probe for the rr VERIFY step fetched 12 cited pages with `fetch{size_only:true}` (12/12 cached, about 1K tokens of result for 274K tokens of documents) and ran 34 `searchCache` patterns over them. Every match came back as a `harvest:{hash}` handle plus an `md_path` whose hash differed from the `path` the same session's `fetch` returned for that URL — 0 of 12 claims could be tied to their own page, although the quoted sentence was found verbatim for 8. The tool's description promises `(url, md_path, match count, sample line)`. 7 of 34 patterns failed with `cache search failed; check the expression or retry later` — plain `42` twice — so the failure is not the regex; 14 of 34 hit the `max_results` cap on unrelated pages; `.` in a figure is a wildcard (`86.2` matched `0.8602`). A grep-based citation check was designed around this tool and had to be dropped for a WebFetch closed question (docs/design/RR/rr.md § Not part of the design).
Amend: `pfm/internal/harvestmcp` — a `searchCache` match reports the source URL the page was fetched from, as the description already says; an optional `sources` filter restricts the grep to named URLs or handles; an optional `literal: true` matches the pattern as a fixed string; and the intermittent `cache search failed` names its cause. Same handle for one page from `fetch` and from `searchCache`, or a documented mapping between them.
Resolved: 2026-09-23 — removed with the tool

## 2026-09-21 — benching a prompt: the roster caches an agent file at its first write
Observed: an edited `~/.claude/agents/{name}.md` kept serving its first-written body for the whole session, so a benchmark run on a "fixed" prompt silently ran the old one; and deleting a bench agent file while a run that named it was alive made its lead fall back to `general-purpose` on the main model, voiding that run's cost.
Amend: judgment — when benchmarking a prompt, write each revision under a fresh agent name, launch it in a turn started after the write, and delete bench copies only after every run naming them has returned.
Resolved:

## 2026-09-21 — a wait rule in a lead prompt does not hold; a blocking command does
Observed: across three tracer/mapper designs the lead met "end your message to wait" with `true`/`sleep` calls (33 of 86 tool calls in one run, each re-sending its whole context); replacing the rule with one blocking script call (`ledger wait DIR n…`, Bash timeout 600000) ended it. Same shape for "stay inside your bucket": readers roamed until their search tools were removed.
Amend: judgment — a lead that fans out gets a blocking wait command, and a reader that must not search holds no search tool; neither is asked for in prose.
Resolved:

## 2026-09-21 — gitter refuses Phase SETUP over an untracked directory the flight never touches
Observed: the Codex-side gitter refused twice to create a flight worktree because the live checkout held one unrelated untracked directory (`docs/design/tracing/`); the orchestrator supplied explicit preservation authorization and gitter still refused, so flight `chat-mcp` died at setup with zero tasks started. The previous flight (`module-rename`) hit the same wall and only passed after a speccer revision round. `git worktree add` never reads, stages or moves untracked files; the Claude-side gitter created the same worktree in three calls with the live checkout's status byte-identical before and after.
Amend: `.claude/agents/gitter.md` (+ its `templates/project/**` twin) § SETUP — the clean-base requirement means no tracked modifications and no staged changes; untracked paths outside the flight's files are preserved, reported in the return, and never a reason to refuse. Same reading for the MERGE phase: an untracked path that no commit in the range touches does not block a fast-forward.
Resolved: 2026-09-22 — worked around all session by running SETUP through the Claude gitter; amendment to gitter.md still open → /pcm

## 2026-09-21 — a Codex orchestrator's executor model pin is not honoured
Observed: flight `module-rename`, orchestrated by a Codex chat on `gpt-6-astra`, requested `gpt-5.6-sol` explicitly in every executor spawn; two executors reported running GPT-6 and the third could not tell (only the gitter role reported sol). Round-2 experiments had already shown a Codex role file honours `model_reasoning_effort` and left `model` unproven.
Amend: judgment until proven — before relying on per-executor models under a Codex orchestrator, prove by a run whether a role file's `model` key is honoured; if it is, `pfm/internal/codexgen/compiler.go` emits it (it writes a `# tier:` comment today) and the pin lives in the role, never in the spawn argument.
Resolved: 2026-09-22 — codexgen emits real `model` + `model_reasoning_effort` in role files (b030e015 task 1-a); every agent-role executor reported gpt-5.6-sol

## 2026-09-21 — Codex rejects symlinked role files: every machine-global agent type was unspawnable
Codex reads `agents/<name>.toml` O_NOFOLLOW (exec-server regular_file.rs); pfm linked them; doctor said `linked` over a dead registry. Executor spawns failed with "agent type is currently not available".
Resolved: 2026-09-21 — pfm writes marker-owned regular files into every Codex home, migrates links, retires orphans; doctor opens roles the way Codex does (7eb55795)

## 2026-09-21 — a hunk-split commit shipped another session's half-written tests
Two sessions editing one checkout; gitter split review_test.go by hunk and committed tests whose symbols lived only in the other session's uncommitted review.go; the committer's "verified" claim was false.
Amend: gitter.md — never hunk-split a file another session has modified; leave it out or wait. A commit is verified by exporting the committed tree (git archive) and building it, never by the committer's word.
Resolved: 2026-09-22 — baseline repaired (ce29317c); rule recorded in the RETRO checkpoint; gitter.md amendment open → /pcm


## 2026-09-23 — a model-copied return is not a verbatim return
In the codeprobe benchmarks the script's return file matched the repo on every line, every run. Final messages that re-emitted it did not: haiku retyping a collector return differed from the file on 31/710, 10/449, 11/453 and 61/431 lines, with shifted indentation and inserted lines; sonnet tracer and mapper rewrote the rendered return in 3 of 6 runs, merging rows, summarising lists and adding hedged or false prose ("only caller", "full detail above"). A prompt rule ("copy whole", a wrong/right example, a banner in the tool output) did not hold; a brief demanding the text inline overrode the agent prompt for haiku.
Amend: judgment — verbatim text travels by file the script wrote, named in a short receipt the model can copy; any return a caller must trust line by line is read from that file.

## 2026-09-25 — Claude Code refuses a sub-agent's REPORT.md
Observed: the reviewer benchmark's lead wrote `SANDBOX/REPORT.md` and got `Subagents should return findings as text, not write report files` — Claude Code 2.1.282 refuses any sub-agent Write whose basename matches `^(REPORT|SUMMARY|FINDINGS|ANALYSIS).*\.md$`. `reviewer` now writes `review.md`. `templates/project/commands/rnd.md` still has its `general-purpose` run agent write `REPORT.md` (lines 27, 39, 43, 49), so that deliverable can never land.
Amend: templates/project/commands/rnd.md — rename the run's REPORT.md to a name outside the refused pattern (e.g. `RESULT.md`) end to end; any prompt that names a sub-agent's output file keeps it outside that pattern.

## 2026-10-02 — managed settings shut every user plugin out of the system prompt
Observed: a cache-live-control probe's `prompt.section` hook never ran: the debug log showed 94 of 94 calls `bypassed by cc-plugin-sec-default (tier user)`, and `prompt.context` the same. The trigger is `cc-plugin-sec-default@builtin seated outermost: this machine has managed settings`; the only managed file is pfm's `managed-settings.d/pfm.json` (`cleanupPeriodDays` alone). Every Professor plugin that shapes prompts is limited to `prompt.submit` context on a pfm host. Whether a host without managed settings admits user-tier section hooks is unproven.
Amend: judgment — decide whether pfm keeps a managed-settings file for `cleanupPeriodDays`; until then, a prompt-shaping hook is checked against `--debug-file` for "bypassed by cc-plugin-sec-default" before it is built on.

## 2026-10-02 — test chats left running, launched six at once, watched to completion
Observed: comparing cache TTLs, the main chat launched six Sonnet chats at once, watched them to completion when the first few turns already answered the question, left eight earlier test chats idle, and the plugin's live proof left claude processes and ~26 sessions in the picker. The user ruled: one test chat at a time, read only its first turns, kill it the moment the answer is in, delete what it leaves.
Amend: judgment — a behaviour probe through `chat_new` runs one chat, reads its first turns, kills it, and removes any session it spawned outside the project.

## 2026-10-02 — two pfm surfaces answer "fine" when they failed
Observed: `pfm chat new` returned `the chat drew nothing within 30s` (exit 1) for ttl-n-5m-1 and ttl-s-1h-2, both running normally; `--settle 90` did not change the 30s check. `.claude/scripts/dev.sh iso run bash -c 'cd pfm && go test …'` printed `go: cannot find main module` and then `all steps passed`.
Amend: pfm chat new — the draw check honours `settle` and confirms the pane before reporting a failed launch; .claude/scripts/dev.sh#iso run — exit and report the command's own status, never "all steps passed" after it failed.

## 2026-10-07 — v0.79.0 update: host conflicts survive the rollback

Observed: updating from `0.79.0-alpha` with `pfm update --to v0.79.0` exited 1. The candidate installer exited 4 on account identity entries in the shared Claude store. Source and binary rolled back, but reapplying the previous installer also exited 4 on the same host; the updater correctly printed `rollback residue`. Post-rollback `pfm doctor` exited 3 with six failures and 110 warnings, while `pfm config validate` exited 0. A restored source revision and version string did not mean that host integration had been restored.

Recovery: inspect the actual account/store paths and live-process config directories; compare stable identity fields without exposing their values; quarantine conflicting entries with verified file/tree manifests instead of executing the printed removal commands. Preserve the established account data. Five misplaced store entries and one account cache were preserved intact. The retry exited 0, installed `v0.79.0`, and a subsequent `pfm install --yes` exited 0. All three accounts retained their identity fields. Unrelated untracked work was stashed by explicit path, restored, and checked byte-for-byte on both attempts.

Amend: investigate preflight before a binary/source swap when the current host already has operator-owned conflicts. Keep source rollback, binary rollback and installer-state restoration as separately evidenced results. A failed reapply must retain its exact diagnostics and name the remaining operator action; do not reduce it to "rolled back safely". Prove the failure and recovery with a fixture containing valid account data plus misplaced store identity, without live credentials. These are follow-up requirements, not a claim that the updater repair has shipped.

## 2026-10-07 — an account cache changes ownership across the update

Observed: the installed alpha classified `.last-update-result.json` as shared data and blocked a real copy in an account directory. Stable v0.79.0 classifies it as per-account data and retires the shared copy. Satisfying the old host check with a shared-store link therefore left two new `retired-store-entry` warnings after the successful update. The new installer explicitly skipped retirement because that account had live chats; rerunning installation while those chats remained live still exited 0 with the migration skipped.

Amend: cover this exact old-to-new ownership transition in the adopter rehearsal. Explain the live-chat precondition before updating, and distinguish a successfully installed binary from deferred migration work. Retain and restore the original account cache when appropriate; never overwrite account-owned data or bypass the live-account guard. Verify archival and unlinking after the account becomes idle. Determine whether the old-binary precheck can avoid a temporary repair that the target release immediately retires.

Recovery completed: after explicit authorization to finish the deferred migration, both affected chats were confirmed idle and ended with `pfm chat end`; their histories remained resumable. `pfm install --yes` exited 0, removed the retired account link and archived the shared copy. The original account-owned cache was restored from its verified backup, with no existing file overwritten. A fresh doctor run showed healthy account links, no blocking failures and no retired-store-entry warnings. Other pre-existing host warnings remained visible. The product follow-up above remains open; the host migration is complete.

## 2026-10-07 — the source clone gets inconsistent project-update verdicts

Observed: after the successful v0.79.0 update, the updater printed `NOT-MANAGED` and explained that a missing `.professor/baseline.json` is expected in the Professor source clone. Running the recommended `pfm doctor --project-updates` in that same clone exited 3 with `FAILED` and suggested `pfm update adopt` or `pfm init`. No baseline was created: this was the framework source, not an adopted project.

Amend: make the source-clone distinction consistent across the updater and standalone project report. An adopted project with a missing baseline must still report its real failure; the source clone should not be steered into scaffolding or adoption. Add a paired source-clone/adopter fixture and preserve exit/output parity for both commands.

## 2026-10-07 — successful installation is not a zero-warning health result

Observed: the repaired update and explicit installer both exited 0, but final `pfm doctor` exited 1 with 112 warnings and no failure rows. Account links were healthy, managed plugins were healthy, and the reminder scheduler was armed with no overdue or failed jobs. Two warnings were the deferred account-cache migration above; the remainder included retained operator/plugin files and backups, stale state, an orphaned-kill audit, an unfollowable pane, and a browser worker that reprovisions on its next browser fetch. The release's blanket "doctor exits 0" action was not achieved. No unrelated operator data was deleted or warning filter changed to manufacture a clean result.

Amend: release actions should distinguish blocking failures, actionable migration warnings, explicitly retained operator state and deferred runtime provisioning. Preserve the full private diagnostic record; publish only a sanitized incident summary. Installation success, migration completion and a zero-warning doctor are three different acceptance checks.

## 2026-10-08 — flight chat-exit: host tooling, a moving base, and findings left outside the flight

Observed: the flight shipping MCP `chat_exit` (develop `bcd3f969`) took five lander rounds and hit four process gaps. (1) Host `check-pfm.sh`/`test-pfm.sh` fail under BSD `realpath` (no `-m`); they only ran with GNU coreutils prefixed onto `PATH`. (2) `check-templates.sh` cannot run on the host (no PyYAML) nor through `iso run` (no Linux tools in the read-only mount), so templates are verified by `iso gate` alone. (3) `develop` moved from `69b46e83` to `50aa3877` during the three-hour flight; gitter's MERGE was refused as non-fast-forward and the green gate covered the stale base, costing a rebase and a re-gate. (4) After that rebase the untracked engine mirrors were stale, and `iso gate`'s `templates.mirrors` step failed on the read-only mount until `pfm codex build . && pfm opencode build .` ran on the host.

Left open by the lander, outside the flight: V9 — the `"reload-"` log prefix is a bare literal (`pfm/cmd/pfm/chat_reload_command.go`, `pfm/internal/doctor/crumbs.go`) while the new `SIDKillExitLogPrefix` is a constant. V6 — two simultaneous kills of one chat plus a failed finisher launch can race `Manager.Kill`'s check-then-write rollback; closing it needs a create-if-absent in the kill store. Flakes seen: `templates.lanes.mcp-stdio` "reply after server exit" under host load, and lane M.04-doctor-mcp's "context canceled" record.

Amend: make the host check scripts portable or have their preflight name the missing GNU tool; have the gate's mirrors step name stale mirrors with the host regeneration command instead of a write failure; have the flight close re-check its base against `develop` before the lander runs; route V9 and V6 to their own small flights; and track the two flaky beats until they are deterministic.

## 2026-10-09 — housekeeping session: relayed push refused, a red develop after clean landings, iso run quoting

Observed: (1) The user asked in-turn to delete a stale remote branch; the main chat relayed the ask to gitter quoting the user verbatim, and gitter refused because a relayed quote is not the user's own message — yet the global `gitter.md` says a push runs "when the caller quotes the user asking for it in the current turn". The user had to run `git push origin --delete` from their own prompt. (2) Three flights landed green, each gated before its rebase; re-gating develop's tip afterward (fresh worktree at c867316c) found `pfm/internal/tokens` TestAuditReplaysTheNodeGoldens/suite/014 red — Go prints "--flight … is not readable: stat …" where the Node golden expects "no agents.tsv, and …/run.md is not readable" — introduced by 12f2e454 and merged without a tip re-gate. (3) `dev.sh iso run bash -c '…'` splits the quoted string, so `cd /worktree/pfm && go test …` runs in `/worktree`. (4) The 2026-10-08 entry's item (1), BSD `realpath -m`, is fixed by ddfb551a (`unit_canon` in `.claude/scripts/unit-path.sh`); its PyYAML item stands.

Amend: templates/global/agents/gitter.md#Push — say whether a caller's verbatim quote of the user's in-turn ask suffices, and make this repo's gitter agree; the flight close (flights-lander or the root foreman) re-runs `iso gate` on develop's tip after the landing commit, not only on the pre-rebase tree; `.claude/scripts/dev.sh` `iso run` passes its argv through unsplit, or documents the quoting it needs.

## 2026-10-09 — foreman model benchmark (round 5) and advisor scope

Observed: (1) A bench testing manual that said "run changed packages and their importers" made five parallel arms race-test most of pfm and pegged the host at load 29; a manual for parallel runs must name exact packages. (2) `pfm tokens --out` keeps `usd` buckets and a separate `total`; a grader summing the buckets under-scored a correct arm 0/3 — `total` is the canonical figure. (3) Round 5 (real bug: advisor iterations unpriced by `pricing.UsageBlock.Billed`), one run per arm, blind Opus review: Sonnet high + Opus advisor capped at 2048 scored 8/10 at $7.13; an Opus foreman scored 5.5 at $12.00 and locked a defect in with its own test. (4) The advisor was effectively on for every Sonnet request machine-wide (user `advisorModel: opus` + the live gateway's `advisor_models: [claude-sonnet-5-5]`), mechanical executors and gitter included; a sub-agent request is identifiable at the gateway by the `x-claude-code-agent-id` header and its agent prompt in system block 3.

Amend: flights testing-manual guidance — name exact packages, never "importers", when runs go parallel; advisor-cache-gateway — scope the advisor per agent (`advisor_agents`, handoff 20261009-180119), with a start check that the matched prompt line still exists.

## 2026-10-09 — tokens/transcript into pfm: a landing gated pfm only left develop's templates gate red; close-out cannot be ordered
Observed: (1) Flight transcript-into-pfm landed with its pfm gate green, but it committed 14 symlinks as golden fixtures under pfm/internal/transcript/digest/testdata; scripts/check-codex-markers.mjs (templates gate, step templates.mirrors) rejects every tracked symlink that dangles or points at an untracked target, so develop's templates gate stayed red until the next flight (chatlog-skill) moved them into case.json `links`, created at test time. (2) Host python3 has no PyYAML, so check-templates.sh prints only "FAIL install". (3) `iso gate pfm` and `iso gate templates` run in parallel on one host produce contention failures. (4) check-pfm.sh skips TestNoEngineLiteralOutsideEnginePackage, so a Go fix that passed it still failed the gate on a bare "codex" literal. (5) The user ordered "merge, then /close-out, then self exit" under god speed; close-out carries disable-model-invocation, so the model could not run it and had to hold the exit for the user.
Amend: templates/global/agents/flights-lander.md — every landing runs both gates, sequentially, whatever projects the diff names (the templates gate scans the whole tree); .claude/commands/pfm-testing-manual.md — golden fixtures never commit a symlink or an empty directory, they declare them in the case manifest (`links`, `dirs`); infra/fence/check-templates.sh — name the missing Python module instead of a bare "FAIL install"; .claude/scripts/check-pfm.sh — run the engine-literal sweep or say it skipped it; templates/global/skills/close-out/SKILL.md — judgment: whether a user's in-turn order may invoke it.

## 2026-10-10 — a preview binary handed over beside an installed one reads as "the change does not work"
Observed: the ls-picker flight gave the user `tmp/bin/pfm.dev ls` to preview; the user's second try was plain `pfm`, the old installed binary, and the run looked like the new design had failed (shortcuts still there, no menu). Diagnosis needed shell history plus the launch-memory row.
Amend: templates/global/commands/flights/ (the foreman's preview hand-off) — a preview command for a binary that also exists installed says in the same line that plain `{tool}` is still the old one until the landing installs it.
Resolved:

## 2026-10-10 — a write after an exec-ing open never runs
Observed: the picker's launch memory was written after `chat.OpenRow` returned; in a real terminal OpenRow execs the attach, so the write never ran. Tests passed because the jail's open returns. flights-lander caught it.
Amend: pfm/CLAUDE.md#Code Standards — any state an action must record is written before the call that may exec (OpenRow, action.Dispatch), and rolled back on its error; a test that calls the open through a returning fake proves nothing about the exec path.
Resolved:

## 2026-10-10 — engine-sync-p2: a host probe with the real HOME wrote into ~/.codex; narrowing validate-only roots changed ownership
Observed: (1) The root foreman rebuilt host mirrors with a scratch engine-sync while redirecting only XDG/STATE; HOME stayed real, so `build .` wrote 39 global prompts/SKILL.md into ~/.codex with a newer marker; restored by deleting them and rerunning the installed `pfm codex build .`. (2) Ruling 15 narrowed Codex validate-only roots; reconcile.Claimable reads every root's SourceRoots and falls back to ClaudeTarget on an empty list, so dropping the Claude agents roots made a no-Codex-home `global` overwrite a user's foreign link — flights-lander round 5 caught it. (3) A 200-call flights-lander cap ends re-landing mid-round; a fresh lander per round finished each in under 50 calls. (4) Host `dev.sh iso` needs /opt/homebrew/bin/bash explicitly.
Amend: .claude/commands/pfm-testing-manual.md — any host probe of an engine-sync binary redirects HOME with XDG_*/ENGINE_SYNC_STATE/ENGINE_SYNC_HOME, never XDG alone; plugins/engine-sync CLAUDE.md — grep Claimable before removing a ValidateOnly root; templates/global/commands/flights/ — re-landing rounds spawn a fresh lander.
Resolved:

## 2026-10-10 — a relaying agent summarizes a sub-agent's return unless told to quote it, and the statuses vanish
Observed: in the conflict-resolver lab (rung C, gitter end to end), the v2 gitter received the resolver's `{path} · {status} · …` rows and reported them as prose bullets in 2 of 2 runs. Every status label was lost, so a `dropped` row (one side's change the merge did not keep) would never have reached the caller. One run also reworded its fixed refusal line. The clause "quote the resolver's return verbatim, never a summary" (c33296bd) held in 3 of 4 runs, including both runs that carried a `dropped` row. Even "verbatim" is retyped: one run introduced a typo and then corrected it below.
Amend: templates/global/commands/quality/prompt.md#Correctness & model behavior — an agent that relays a sub-agent's machine-read return quotes it verbatim, and the prompt gives the reason (the caller parses its statuses). A status that must survive every relay goes in a file the caller reads, not in relayed text.
Resolved:

## 2026-10-10 — `visibility.visible-to` is not enforced on this host; GITTER-ONLY held only through the description
Observed: tool-visibility-controller is missing from `enabledPlugins` in both `~/.claude/settings.json` and `~/.cc/2/settings.json`. The main chat's agent list shows conflict-resolver (`visible-to: [gitter]`) and every TRACER-ONLY, RR-ONLY and INVESTIGATOR-ONLY helper. A haiku test relay spawned conflict-resolver directly 9 times once its prompt carried the user's authorization. The only refusal came from the model reading "GITTER-ONLY" in the description.
Amend: judgment — either enable tool-visibility-controller on the host (a fleet-wide change and its own task), or have `pfm doctor` warn when an installed agent declares `visibility` while the plugin is off.
Resolved:

## 2026-10-10 — `pfm doctor`'s repair for a missing plugin cannot work from a live chat
Observed: `pfm doctor` reported `tool-visibility-controller@tool-visibility-controller-dev not enabled … — run pfm install --yes`. Two `pfm install --yes` runs from a chat both exited 0, and both skipped the plugin step ("skip claude plugins: live chats … — close them and rerun"). The chat running the install is itself one of those live chats, so following the repair from inside any chat never closes the gap. The plugin had silently stayed off on every account since it joined the table. Separately, BSD `pgrep` leaves out its own ancestors, so a live-chat count taken from inside a chat is one short.
Amend: pfm/internal/doctor/claude_plugins_checks.go — the plugin-gap repair names the live-chat gate ("close every Claude chat, then run pfm install --yes from a plain terminal"), and `pfm install` reports a skipped plugin step as a warning in its summary line, never an exit-0 skip buried among the rows.
Resolved:

## 2026-10-10 — a context-junk claim rested on a check that exited 2
Observed: the main loop's first context audit read `~/.claude.json` (absent; the active config is `$CLAUDE_CONFIG_DIR/.claude.json`), the chained step exited 2 with no FAILED line, and a row on the Chrome integration was reported as if the check had run. The buddy plugin caught it; the corrected check reversed half the row.
Amend: judgment — the rule exists (fleet prompt § Command execution: a chained step that fails says so on its own line); `/context-audit` step 3 now carries it for this workflow.
Resolved:

## 2026-10-11 — a forced worktree removal judged submodules by HEAD alone
Observed: the shipped `worktree.sh remove` (safety-repairs flight) refused unpushed submodule work by checking only each submodule's HEAD; the landing review proved a commit on a local submodule branch (HEAD back on the recorded commit) and a submodule stash were both deleted by `git worktree remove --force` with rc 0, since a linked worktree's submodule gitdirs live under its private Git directory. The fix judges `git rev-list -n 1 HEAD --branches --not --remotes --tags` plus `refs/stash`.
Amend: templates/global/agents/gitter.md#Phases — the Worktree remove rule proves "HEAD is on its remote"; it should prove no commit reachable from HEAD or a local branch is missing from every remote branch and tag (or from the main checkout's copy), and that no stash exists.
Resolved: 2026-10-11 — templates/global/agents/gitter.md and .claude/agents/gitter.md, Worktree remove: rev-list over HEAD and local branches against remotes and tags, plus the stash check

## 2026-10-11 — GPT role twins redirected the tracer kit off Claude
Observed: `gpt-foreman` spawned `gpt-tracer` (sol), which spawned `gpt-sub-tracer` (luna), because its role line mapped "a tracer, spawn `gpt-tracer`". The twins are generated by the `/iterative-improvement` lab (`gpt-subagent-route`) into `~/.claude/agents/`, not by the repo, so no repo check saw the mapping. The user ruled the tracer kit stays on Claude; both twins and their lab copies were deleted.
Amend: judgment — the lab owns the twins; any future twin generator maps only roles the user named for GPT, never `tracer`/`sub-tracer`.
Resolved: 2026-10-11 — judgment; the twins no longer route the tracer kit and gpt-tracer/gpt-sub-tracer are deleted

## 2026-10-11 — a guarded patch applied by Bash left the flight worktree's engine mirrors stale
Observed: in the price-gap flight the main chat applied `patches/guarded.patch` (`.claude/scripts/check-pfm.sh`, `.claude/commands/pfm-testing-manual.md`) with `git apply`, which the Stop hook's mirror compile never sees, then told the foreman to skip the worktree mirrors; the flight's first `iso gate` failed only on `templates.mirrors` and the lander rebuilt them.
Amend: judgment — the rule exists (root `CLAUDE.md` § Runtime, Claude: after a Bash-driven write, `pfm codex build . && pfm codex check .` then `pfm opencode build . && pfm opencode doctor .`); the main chat applying a guarded patch runs it in the worktree before resuming the foreman.
Resolved: 2026-10-11 — judgment; root CLAUDE.md § Runtime already holds the rule

## 2026-10-11 — the flights roles have no rule for a check's `GAP install`, exit 3
Observed: `check-templates.sh` (safety-repairs) and `check-pfm.sh` (price-gap, `8c663472`) now print only `GAP install ({unit} is missing: …)` and exit 3 when a tool is absent; the role prompts say only "a red fixed and the same command again", so a builder reading exit 3 as a red may chase a tool gap in code, and one reading it as non-FAIL may report a pass.
Amend: templates/global/agents/flights-foreman.md#Tests, templates/global/agents/flights-lander.md steps 2 and 8, templates/global/agents/flights-mechanical-executor.md step 7 — a `GAP install` exit 3 is no red to fix and no pass: the return names it as a gap with the missing tools, and the builder installs nothing.
Resolved: 2026-10-11 — flights-foreman.md § Tests, flights-lander.md step 2, flights-mechanical-executor.md step 7

## 2026-10-11 — an agent's tools allowlist hid the self-compaction tool
Observed: a nudged flights-foreman ran from 143k to 264k without compacting: its `tools:` allowlist left out `mcp__sub-agent-compact__compact`, and its `<compact-now>` markers landed in messages that ended its turn at a wait, where the plugin arms nothing; the plugin's decision log held about 100 skipped asks and no armed row. Every agent but the rr family now lists the tool (`e6865dae`).
Amend: .claude/commands/pcm.md#Special Operations — a new agent with a `tools:` allowlist lists `mcp__sub-agent-compact__compact`, the rr family excepted.
Resolved: 2026-10-11 — .claude/commands/pcm.md and templates/project/commands/pcm.md, New agent

## 2026-10-11 — a brief capped the total number of test chats an agent could launch but not how many could run at once
Observed: the auto-effort experiments brief allowed 40 test chats in total with no concurrency limit. The agent launched its 14-chat pilot at once, an hour after a host crash, and the load average reached 137. The user had every chat killed, and three pilot runs were lost. The same run also showed two side effects of test chats: `/effort` typed into a test chat saved its level into the shared `~/.claude/settings.json`, and a test chat followed `pfm chat ask`'s "to reply" line and messaged the user's main chat.
Amend: CLAUDE.md § Dispatch: a dispatch that lets an agent launch chats or processes caps how many run at once, as well as the total. A test chat changes settings only through launch flags, never through a slash command that saves a default, and is told to answer only in its own chat.

## 2026-10-11 — a builder wrote a guarded template through Bash, and the guard never saw it
Observed: a general-purpose builder changing `templates/global/skills/tracetree/loom_wall.html` (commit `c1e55f14`) made its first edit with a Python script run through Bash. The PreToolUse guard gates Edit and Write only, so that write went through unchecked. When its next Edit was denied, the builder ran the unlock stamp the deny message names, and its later edits were guarded. The next builder's brief said "every template edit through the Edit tool, never Bash", and it complied.
Amend: CLAUDE.md#Process (Guarded files) — a guarded file is written through Edit or Write only, never through a shell command; and `.claude/scripts/pfm-guard.sh` — consider gating Bash commands that write under a guarded path.

## 2026-10-11 — a host reboot wiped every tracer run, and the history `tt book` reads
Observed: a Mac crash and reboot cleared `/tmp`, the tracer runs root `/tmp/professor/tracer`. Every earlier run was gone: the 478-hop run open in the user's browser, the A/B seats' runs, the swatch book's history (46 runs before, 1 after) and every Loom page generated under `/tmp`. Only records copied to `~/.local/state/pfm/` survived.
Amend: templates/global/skills/tracetree/SKILL.md (runs root) — decide whether runs that a caller keeps (a bench, a book, a published page) belong under a durable root such as `~/.local/state/pfm/tracer/`, or whether `/tmp` stays and the caller copies what it keeps; or `judgment`.

## 2026-10-11 — a flight brief ordered "commit the live pfm.config.json", which is gitignored
Observed: the plugin-list flight could not commit its R8 config edit; pfm.config.json is ignored (`.gitignore:2`), and the old pfm binary rejected the new key, so the edit had to wait until after `make host-install`.
Amend: CLAUDE.md#Vocabulary — name pfm.config.json as the host's untracked live config: a flight edits it in place after the host install and never commits it.
Resolved:

## 2026-10-11 — pfm test files near the 1000-line C2 ceiling block new tests
Observed: claude_store_test.go (992) and main_test.go (998) could not grow; the tvc-store-entry flight found this only at the gate.
Amend: .claude/commands/pfm-testing-manual.md — before adding a test, `wc -l` the target; at or near the ceiling, put the test in a sibling file.
Resolved:

## 2026-10-11 — the picker parked forever on one quiet interval, so cosmos drew dead chats after a reboot
Observed: two root causes. `compose/cosmos.go` gave an event-matched node `Dead = Killed` only, so a resumable chat with comms in the 24h window drew live. `picker/pipeline.go` parked after one untouched 5s pass and ran no full pass until a keystroke, so births and deaths never reached the TUI. Fixed in 5fcc59d4 (liveness) and 2d38712b (CPU). Idle Limits under chat churn is still 0.87–1.49% of a core against the old 0.3%, because each presence-triggered pass is a full two-phase pass.
Amend: pfm/internal/picker/pipeline.go — move code out to get under the C1 ceiling, then make a presence-triggered pass lean (full load first, skip the first publish and the reload when the indexer touched nothing; ~40% of its CPU). Close R2 as well: `/tmp/cc-sid` crumb owner is never checked (gather/crumbs.go, statusline, doctor).
Resolved:

## 2026-10-11 — a stat on pfm.db misses WAL commits; PRAGMA data_version is the change gate
Observed: pfm.db runs in WAL mode, and its mtime moves only at checkpoint. The cosmos ledger gate uses `PRAGMA data_version` plus an own-writes counter, because data_version does not move for the committing connection. The pool is one connection, and a second dedicated connection would deadlock it.
Amend: .claude/commands/pfm-testing-manual.md — a cross-process change gate on pfm.db is tested with a second connection and a forced `wal_checkpoint(TRUNCATE)` between inserts.
Resolved:

## 2026-10-11 — an offline analysis opened paths named in transcripts and took the host down
Observed: a throwaway script that sized every file path found in transcript tool calls opened each with an unbounded read(); one path was /dev/zero, the read never ended, two orphan python copies grew past 12 GB each and the host panicked on swap exhaustion. The repair was a helper that stats first, refuses anything but a regular file (S_ISREG), never opens /dev, /proc or /sys, caps every read and runs under an alarm plus a CPU limit. The user's standing rule since: any analysis that opens paths from transcripts reads only S_ISREG files with a bounded read().
Amend: templates/global/skills/chatlog/SKILL.md#rules — any ad-hoc analysis that opens a path found in a transcript stats it first, reads only regular files with a bounded read, never /dev, /proc or /sys, and runs under a timeout; /pcm retro may find a wider home (a global rule) for the same line.
Resolved:

## 2026-10-11 — a flights gate brief leaves out the lander's long run
Observed: the foreman's brief to the lander of a callmeter flight did not name the e2e run docs/testing.md assigns the lander (about 250 s); the lander found it itself, and a foreground call of that length blocks the lander's turn.
Amend: templates/global/agents/flights-foreman.md — the section that spawns the flights-lander: its gate brief names each project's long run the testing manual assigns the lander and says to run it in the background with stdin from /dev/null.
Resolved:
