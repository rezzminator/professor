# GPT twins

## Runtime prerequisite

These machine-global agents are optional entry points. Invoke them by registered name without the Agent tool's `model` override. A compatible gateway must accept the Claude harness's Anthropic Messages requests and route the GPT model IDs to an OpenAI-compatible backend. The `[1m]` suffix is a harness context-window hint; the gateway must support the declared IDs and reasoning efforts. [gpt-gateway](https://github.com/rezzminator/gpt-gateway) is that gateway, a separate repository pinned as the `plugins/gpt-gateway` submodule and installed on its own; Professor ships no credentials, endpoint or account configuration.

Installation and mirror compilation are local file operations: they do not contact a model provider or require a gateway. A host without one can install normally and use the ordinary Claude agents; invoking a GPT twin without a matching route fails at the provider boundary.

## Declarations

| Agent | From | Model | Effort | Body |
| --- | --- | --- | --- | --- |
| `gpt-flights-foreman` | `flights-foreman` | `gpt-6.1-sol[1m]` | `high` | the manual, the return law prepended, its four spawned roles renamed |
| `gpt-foreman-pro` | `gpt-flights-foreman` | `gpt-6.1-sol[1m]` | `xhigh` | as `gpt-flights-foreman` |
| `gpt-flights-lander` | `flights-lander` | `gpt-6.1-sol[1m]` | `high` | the manual, the return law prepended, reviewer roles renamed |
| `gpt-flights-speccer` | `flights-speccer` | `gpt-6.1-sol[1m]` | `xhigh` | the manual, its own return law prepended |
| `gpt-reviewer` | `reviewer` | `gpt-6.1-sol[1m]` | `high` | the full review manual, Claude tracers unchanged |
| `gpt-reviewer-pro` | `reviewer-pro` | `gpt-6.1-sol[1m]` | `high` | the full deep-review manual, Claude tracers unchanged |
| `gpt-rr` | `rr` | `gpt-6.1-sol[1m]` | `low` | the full research-lead manual, Claude diggers unchanged |
| `gpt-rr-pro` | `rr-pro` | `gpt-6.1-sol[1m]` | `medium` | as `rr-pro`: its budget and repository lane |
| `gpt-rr-pro-max` | `rr-pro-max` | `gpt-6.1-sol[1m]` | `medium` | as `rr-pro-max`: its budget, repository lane and `Read` |
| `gpt-flights-mechanical-executor` | `flights-mechanical-executor` | `gpt-6-luna[1m]` | `high` | the manual, the return law prepended |
| `sol` | `general-purpose` | `gpt-6.1-sol[1m]` | `high` | the `shared/gpt-worker.md` append text |
| `luna` | `general-purpose` | `gpt-6-luna[1m]` | `medium` | the `shared/gpt-worker.md` append text |
| `astra` | `general-purpose` | `gpt-6-astra[1m]` | `high` | the `shared/gpt-worker.md` append text |

Each twin is a declaration in `templates/global/agents/{name}.md`, with `from:` and its settings in frontmatter; engine-sync renders it into its generated directory and `pfm install` links the result. The shared files live in `templates/global/agents/shared/`, carry no `name`, and are never installed. `shared/gpt.md` gives every twin `requires: [gpt-agents]`, `engines: {codex: false, opencode: false}` and the `autoCompact` block `forceAt: 230k`, `nudgeFrom: 190k`, `nudgeEvery: 20k`. `shared/gpt-flights.md` extends it and prepends the return law to the manual, naming the builders' files (`run.md`, briefs, returns, `gate.md`, red logs); `gpt-flights-speccer` extends `shared/gpt.md` directly and prepends its own, naming only `spec.md` and the research and calibration files its manual writes in the flight directory, so a literal reading never licenses it to write a builder's file; `shared/gpt-worker.md` extends it and appends the general-worker text to the empty `general-purpose` body.

A flight twin carries its original's whole manual, so an edit to the original reaches the twin at the next `pfm install`. `gpt-flights-foreman` renames `flights-foreman`, `flights-mechanical-executor`, `flights-lander` and `flights-speccer` to `gpt-flights-foreman`, `gpt-flights-mechanical-executor`, `gpt-flights-lander` and `gpt-flights-speccer` wherever the manual names them as an agent; a file path such as `flights-foreman.md` is never renamed, so `gpt-flights-lander` still reads the foreman's § Change inventory. `gpt-foreman-pro` derives from `gpt-flights-foreman`: its xhigh effort belongs to the entry agent only, and its children stay `gpt-flights-foreman`. `gpt-flights-lander` and `gpt-flights-mechanical-executor` replace their original's `visibility` with `main`, `gpt-flights-foreman` and `gpt-foreman-pro`, the agents that spawn them; `gpt-flights-lander` adds `Skill` to its tools and renames `reviewer` and `reviewer-pro` to `gpt-reviewer` and `gpt-reviewer-pro`. Those reviewers remain visible to `main` and `gpt-flights-lander`; their `tracer` and `sub-tracer` readers stay Claude agents and are never renamed. `gpt-flights-speccer` renames nothing and keeps its original's visibility: any chat may spawn it, and its `tracer`, `rr` and `clone-distiller` spawns stay Claude agents. engine-sync stamps `role: {original}` on each flight twin; a `general-purpose` worker carries none.

The RR twins keep their original's effort, tools, save law and body; `gpt-rr-pro` and `gpt-rr-pro-max` derive from the rendered `rr-pro` and `rr-pro-max`, so they carry each tier's budget and `shared/repo-lane.md`. They rename nothing: their `sub-rr` diggers and `clone-distiller` stay Claude agents, and `sub-rr`'s visibility lists the three twins beside the three Claude leads. Each is stamped `role: rr`. Their `RR-DIR:` line comes from the same `rr-dir` SubagentStart hook: its matcher `rr|rr-pro|rr-pro-max` is not a plain name list, so Claude Code tests it as an unanchored regex, which every `gpt-rr*` name matches.

## Opt-in

The twins install only on a host that opts in with `"gptAgents": true` under `claude` in the machine config `pfm.config.json`: pfm then writes `gpt-agents` into the `capabilities` list of engine-sync's `targets.json`, which every engine-sync call reads, passes `--capability gpt-agents` on its own install and doctor calls, and stages the hook assets below. Without it, check reports each twin `SKIPPED {twin} — requires gpt-agents`, machine doctor prints its `agent SKIPPED all {twin} {path} (requires gpt-agents unmet)` row, and an install retires the twins' links and generated files an earlier opted-in install owned, never a file it does not own.

## Owned hook assets

`pfm/internal/installer/assets/gpt-agent-stop.py` is the canonical script. Installation stages its embedded managed copy and creates an exact-source symlink under `hooks/` in the default Claude config directory, an explicit install store and every configured account directory. Subsequent installs repair owned content and missing links; regular operator files and foreign links are visibly refused, never overwritten. `gpt-hook-ownership.json` records successful physical target paths so uninstall retires owned links even after an account is removed or a directory alias moves. Each retirement rechecks the exact source link; the receipt grants no right to delete an operator replacement. Malformed receipts are visible failures. Doctor names missing, unreadable or changed scripts. It is an agent-frontmatter hook, not a machine-wide `settings.json` hook: `gpt-flights-foreman` adds it with `add.hooks` beside `flights-foreman`'s own `pfm internal orchestrator-wait`, and `gpt-foreman-pro` inherits both. An install removes the owned `hooks/gpt-return-watch.py` link, managed copy and log directory an earlier install left; an operator file at that path is kept.

The foreman and pro invoke it through `python3 -I` and `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/hooks/`, so account-local paths and spaces resolve without a machine-specific interpreter path. `python3` is the existing host prerequisite.

- `gpt-agent-stop.py foreman`: Stop, scoped to the foreman agent. It validates the return's first line against explicitly assigned flight/unit names, permits waiting for typed background launches that have not completed, and caps refusals at two per agent using a persistent per-agent counter rather than rescanning audit history. A refusal is emitted only after its counter and audit write succeed; failures are visible and permit stopping. Its retained `sub-tracer` role requires a readable, regular, nonempty stored hops artifact and caps refusals at three. No sub-tracer twin is registered by this family. Decisions are logged under `{fleet home}/.local/state/pfm/gpt-agent-stop/` or `GPT_AGENT_STOP_LOG_DIR`.

`{fleet home}` is `PFM_HOME` when set, otherwise the user's home; the explicit `GPT_*` log-directory overrides take precedence. These assets retain the restored audit format and role contracts with regression-tested safety and jail repairs.

## Engine mirrors

The twins are Claude-only: `shared/gpt.md` sets `engines.codex` and `engines.opencode` to `false`, so engine-sync writes no Codex role and no OpenCode agent for them in any scope. A Codex or OpenCode session runs the ordinary roles on its own models. The adapter installs no provider credentials and translates no Claude hook into another engine's hook.

## Hand-copy conflicts

A pre-existing regular file at a twin's name in an account's `agents/` directory is not pfm-owned: installation reports the conflict instead of replacing it, and doctor reports it `SHADOWED`. `pfm install --adopt-agents` moves each such file under `{XDG state}/engine-sync/adopted/{UTC timestamp}/` and links the rendered twin in its place. A hand-deployed `hooks/gpt-agent-stop.py` that conflicts with the owned link is backed up by hand before `pfm install --yes`. A change to a twin belongs in its own `{name}.md` declaration or its original; recreating a hand copy defeats install ownership.

## Verification

`scripts/check-agent-roster.test.mjs` renders the shipped declaration files through the built engine-sync, with and without `--capability gpt-agents`, and pins on the rendered twins what it pinned on the old source files: no machine-absolute path, the renames applied, the hook commands, model and effort, the compaction block and the size cap — 15 KiB for a worker twin, 2 KiB over the larger of 15 KiB and its original for a role twin. It also proves each twin linked only with the capability, `SKIPPED` without it, and written as no Codex role and no OpenCode agent. Its `templates.mirrors` gate registration runs the cases. The installer and install-health tests check account fan-out, repair and named failures; hook fixtures check runtime behavior without a provider account. Run the affected scripts through the fenced testing runners and both full gates, `iso gate pfm` and `iso gate templates`, before landing.
