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

## 2026-09-19 — unauthorized chat kill is possible: the fleet has no chat ownership
Observed: the chat MCP server (`/mcp/chat` on loopback, and its stdio twin) answers any caller, and takes the caller's identity from the thread id the client itself sends. Any chat — or any local process — can `chat_kill`, `chat_inject`, `chat_keys` or `chat_name` ANY other chat; nothing records who spawned whom. The testing-foundation Fable review named it (L2-F2); it was left out of that train by the user's ruling.
Amend: a refined spec under `docs/dev/` (own train) — introduce CHAT OWNERSHIP. `chat_new` records the spawning chat as the new chat's owner in the fleet store (the identity the daemon derives, never a tool input — the way `issue_servicedesk` captures its reporter). `chat_kill` is allowed only for (a) the owner, (b) the chat itself (`--self`), (c) the human at a shell or the picker; every other caller gets a named refusal ("chat X is owned by Y"), never a silent no-op. Decide in the spec: whether `chat_inject` / `chat_keys` / `chat_name` follow the same rule or stay open (peer messaging is the fleet's purpose); what owns a chat the human started (no owner = human-only kill); what `chat_unkill` requires; and how ownership survives `/reload` and a resumed session. The enforcement point is the daemon's derived identity, so the spec must first close the forgeable-thread-id door it rests on. Map every kill door closed-world first (CLI, MCP, picker, reap, hooks) — an owner check at N−1 doors is the bug.
Resolved:

## 2026-09-19 — engine parity: Codex and OpenCode trail Claude across the whole surface
Observed: a closed-world map of `develop` (three tracer passes: the pfm engine, hooks + host wiring, the mirror compilers) with Claude as the anchor. pfm registers six core capabilities per engine (`cmd/pfm/engines.go`): Claude 6, Codex 6, OpenCode 2 (index + process matcher). The gaps, per engine:
- OpenCode, engine: no managed launcher (`chat new --engine opencode` refuses), no usage source, no headless planner, no ask runner (so no `chat ask`, no status summary); no transcript reader (`chat last` / `read` / `find` / `save` unsupported); no rename / name sync; no `--model` / `--effort` on its spawn and resume lines; no system-prompt injection at launch; no in-place reload (refused by name); no archive; no statusline, context gauge or cost stats; no doctor engine probe and no platform dependency gate; no `/clear`-hides-the-chat path (Claude: SessionEnd hook; Codex: fleet pane-binding reconcile); `whoami` by socket prefix only (the engine exports no session variable); the inject input-cleared proof cannot see the boxed composer; an idle-pane inject reported "queued, busy".
- OpenCode, hooks and install: no hook mechanism at all (the generator owns only `$schema`, `permission`, `mcp`) — no SessionStart, SessionEnd, UserPromptSubmit, PreToolUse equivalents; the guarded-file and git-write denies exist only per project, the host global config carries no `permission` block; `pfm install` never runs the OpenCode compiler (its one caller is the CLI); global tier is commands only, written as real files, and uninstall never sweeps them; MCP foreign-entry inspection names two clients, not three.
- OpenCode, compiler and blueprint: agent `tools` and `model` frontmatter are dropped (model survives as a prose sentence); commands keep only `description`; no hand-editable config keeper; no `templates/project/opencode/`; zero `refresh-map.json` entries; no SETUP.md generation phase; no placeholder tokens; `scripts/check-opencode-writer.mjs` exists with zero invocation sites.
- Codex: `chat find` searches Claude transcripts only; the MCP search tool is hardcoded to Claude; no booting / sub-agent / split-seat rows and no pane-label capture (Claude-only branches); reload `--new` rename is Claude-only; statusline is a cache refresh nothing renders; no cost stats; hooks are SessionStart only (no SessionEnd, UserPromptSubmit, PreToolUse); guarded-file and git-write denies are prose for writable agents and the main chat; per-agent model is a TOML comment, the tool allowlist collapses to one read-only bit, gitter authority is emergent from its tool list rather than declared; no launcher shim.
- All engines: git-write deny for non-gitter is prose on Claude too; agent-frontmatter `hooks:` are dropped by both compilers; two child `CLAUDE.md` files (`.claude/skills/deep-rr`, `workflows/deep-rr`) have no `AGENTS.md` twin; this repo has no `.mcp.json`, so both project MCP fences are empty and unexercised.
Landed the same day (no longer gaps): a live OpenCode seat (detection, live row, addressability, pane-read status, `/exit` kill, up-front reload refusal); the headless-OpenCode port is in flight.
Amend: a refined spec under `docs/dev/` (own train, after the OpenCode-live and headless-port waves close) — ENGINE PARITY. One wave per capability family, Claude as the reference behaviour, each wave tracer-mapped closed-world first; every cell that stays unsupported answers with a NAMED refusal, never absence. Order by what unblocks the rest: launcher + transcript reader first (they gate `chat new`, `last`, `read`, `find`, `save`, `ask`), then model / effort / prompt injection, then usage + statusline, then the hook-equivalent layer (OpenCode plugins are the candidate mechanism — RND first), then the blueprint side (template dir, refresh-map, SETUP phase, placeholders). Add a parity table to `docs/dev/pfm-surface.md` generated from the code (the `engineCapabilityExceptions` map is the seed) so the matrix cannot rot.
Resolved:

## 2026-09-19 — engine configuration is scattered: no single place, no discoverability
Observed: what configures each harness lives in several unrelated places with no index. The engine global config file (`~/.claude/settings.json` + `~/.claude.json`, `~/.codex/config.toml` + `~/.codex/hooks.json`, `~/.config/opencode/opencode.jsonc`), part pfm-owned inside fences or ownership ledgers and part hand-set (the host Codex `approval_policy` and `writable_roots` are hand-written; pfm writes only the multi-agent timeout keys and the MCP fence); environment variables set at launch (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `PFM_*_ROOT(S)`, the Claude launch env pair); inline launch flags built in Go (`--system-prompt-file`, `--settings`, `--model`, `--effort`, `-c model_reasoning_effort=`, `-c model_instructions_file=`, `--session`); pfm config (`claude.systemPrompt`, account rosters, binaries); per-project generated files (`.codex/config.toml` keeper + fence, `.opencode/opencode.jsonc`); and the user shell functions (`cx`). The same setting can be expressible in two of these at once with no stated precedence. Consequence the user named: asked to "change X for Codex", an agent can edit the wrong layer — the change is overridden by a flag, overwritten by the next install, or lands in a file the engine never reads — and nothing tells it where the effective value comes from.
Amend: a refined spec under `docs/dev/` (own train, RND first) — ONE CONFIGURATION TRUTH PER ENGINE SETTING. Decide: (1) an inventory, generated from the code, of every setting pfm influences per engine — its layer (engine file / env / flag / pfm config / project mirror), its owner (pfm / user), its precedence, and whether install rewrites it; (2) a single declared home for each pfm-owned setting, with the other layers derived from it, never hand-edited; (3) a discoverability verb (`pfm config where <engine> <setting>` or a `pfm doctor` section) that prints the effective value and the layer it came from, so an agent asks before it edits; (4) the agent-facing rule in the fleet prompt / `CLAUDE.md` naming that verb as the first step of any engine-config change; (5) what happens to hand-set values inside pfm-managed files (preserve + report, as the MCP ownership ledger already does). Map every writer and reader of each layer closed-world before the spec.
Resolved:

## 2026-09-19 — a chat keeps the pfm build it was born with: every stdio MCP server outlives every install
Observed: each chat launches `pfm mcp chat serve` once over stdio and holds it for the whole session; `pfm install` replaces the binary underneath and the server keeps answering from the deleted build (nine such processes on one host, the oldest two days stale — a kill by session id answered "killed" from a build that predated the live OpenCode seat and closed nothing). A guard that ended the server on replacement was built, proven in a real chat, and withdrawn the same day: Claude Code does not relaunch a stdio server that exits ("configured MCP server failed to connect: chat"), so the guard trades a stale answer for no chat tools at all in every running chat, self-compact and inject included. Kept from that work: `RunStdio` returns when its context ends even while the client holds stdin open (it used to wait on the blocked reader forever, which is why the guard first looked like it worked in tests and did nothing on a host).
Amend: a refined spec under `docs/dev/` — CHATS REACH pfm THROUGH THE DAEMON. The loopback HTTP daemon (`pfm mcp serve`) already restarts on install and already serves `/mcp/chat`; decide whether installed chats register the chat server as an HTTP MCP entry instead of stdio (identity then has to travel in the request, since the daemon has no chat ancestry — the open design question), or whether the stdio process becomes a thin proxy to the daemon that carries its own identity. Until then `pfm doctor` names every `pfm mcp … serve` process running a replaced executable, with its chat, so a stale server is visible instead of silent.
Resolved:
