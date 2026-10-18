# GPT twins

## Runtime prerequisite

These machine-global agents are optional entry points. Invoke them by registered name without the Agent tool's `model` override. A compatible gateway must accept the Claude harness's Anthropic Messages requests and route the GPT model IDs to an OpenAI-compatible backend. The `[1m]` suffix is a harness context-window hint; the gateway must support the declared IDs and reasoning efforts. [gpt-gateway](https://github.com/rezzminator/gpt-gateway) is that gateway, a separate repository pinned as the `plugins/gpt-gateway` submodule and installed on its own; Professor ships no credentials, endpoint or account configuration.

Installation and mirror compilation are local file operations: they do not contact a model provider or require a gateway. A host without one can install normally and use the ordinary Claude agents; invoking a GPT twin without a matching route fails at the provider boundary.

## Declarations

| Agent | From | Model | Effort | Body |
| --- | --- | --- | --- | --- |
| `gpt-flights-foreman` | `flights-foreman` | `gpt-6.1-sol[1m]` | `high` | the manual, the return law prepended, its own two tiers renamed |
| `gpt-flights-foreman-pro` | `gpt-flights-foreman` | `gpt-6.1-sol[1m]` | `xhigh` | as `gpt-flights-foreman` |
| `gpt-flights-lander` | `flights-lander` | `gpt-6.1-sol[1m]` | `high` | the manual, the return law prepended, reviewer roles renamed |
| `gpt-reviewer` | `reviewer` | `gpt-6.1-sol[1m]` | `high` | the full review manual, Claude tracers unchanged |
| `gpt-reviewer-pro` | `reviewer-pro` | `gpt-6.1-sol[1m]` | `high` | the full deep-review manual, Claude tracers unchanged |
| `gpt-rr` | `rr` | `gpt-6.1-sol[1m]` | `low` | the full research-lead manual, Claude diggers unchanged |
| `gpt-rr-pro` | `rr-pro` | `gpt-6.1-sol[1m]` | `medium` | as `rr-pro`: its budget and repository lane |
| `gpt-rr-pro-max` | `rr-pro-max` | `gpt-6.1-sol[1m]` | `medium` | as `rr-pro-max`: its budget, repository lane and `Read` |
| `sol` | `general-purpose` | `gpt-6.1-sol[1m]` | `high` | the `shared/gpt-worker.md` append text |
| `luna` | `general-purpose` | `gpt-6-luna[1m]` | `medium` | the `shared/gpt-worker.md` append text |
| `astra` | `general-purpose` | `gpt-6-astra[1m]` | `high` | the `shared/gpt-worker.md` append text |

Each twin is a declaration in `templates/global/agents/{name}.md`, with `from:` and its settings in frontmatter; engine-sync renders it into its generated directory and `pfm install` links the result. The shared files live in `templates/global/agents/shared/`, carry no `name`, and are never installed. `shared/gpt.md` gives every twin `requires: [gpt-agents]`, `engines: {codex: false, opencode: false}` and the `autoCompact` block `forceAt: 230k`, `nudgeFrom: 190k`, `nudgeEvery: 20k`. `shared/gpt-flights.md` extends it and prepends the return law to the manual: the parent hears the twin's result only through its final return, and a message to `main` carries only what the manual sends there. `shared/gpt-worker.md` extends it and appends the general-worker text to the empty `general-purpose` body.

A flight twin carries its original's whole manual, so an edit to the original reaches the twin at the next `pfm install`. `gpt-flights-foreman` renames `flights-foreman` and `flights-foreman-pro` to `gpt-flights-foreman` and `gpt-flights-foreman-pro` wherever the manual names them as an agent; a file path such as `flights-foreman.md` is never renamed. Its `flights-worker` and `flights-worker-pro` children stay Claude agents, and `flights-worker`'s `visible-to` lists both GPT foremen beside the three Claude tiers. `gpt-flights-foreman-pro` derives from `gpt-flights-foreman`: the same manual and renames at effort `xhigh`, without `flights-foreman-pro`'s effort-control tools. `gpt-flights-lander` keeps its original's `visibility`, `main` only, and renames `reviewer` and `reviewer-pro` to `gpt-reviewer` and `gpt-reviewer-pro`. Those reviewers are visible to `main` and `gpt-flights-lander`; their `tracer` and `sub-tracer` readers stay Claude agents and are never renamed. `gpt-rr`, `gpt-rr-pro` and `gpt-rr-pro-max` derive from their tier, so `gpt-rr-pro` and `gpt-rr-pro-max` carry that tier's § Budget, repository lane and tools; they rename nothing, keep their original's open visibility, and spawn the Claude `sub-rr` and `clone-distiller`. `sub-rr` lists the three in its `visible-to`, and the `rr-dir` `SubagentStart` matcher names them, so each receives its `RR-DIR:` line. engine-sync stamps `role: {original}` on each flight and research twin; a `general-purpose` worker carries none.

The RR twins keep their original's effort, tools, save law and body; `gpt-rr-pro` and `gpt-rr-pro-max` derive from the rendered `rr-pro` and `rr-pro-max`, so they carry each tier's budget and `shared/repo-lane.md`. They rename nothing: their `sub-rr` diggers and `clone-distiller` stay Claude agents, and `sub-rr`'s visibility lists the three twins beside the three Claude leads. Each is stamped `role: rr`. Their `RR-DIR:` line comes from the same `rr-dir` SubagentStart hook, whose matcher `rr|rr-pro|rr-pro-max|gpt-rr|gpt-rr-pro|gpt-rr-pro-max` names all six (`HookRRDirMatcher`, `pfm/internal/claudelaunch/hooks.go`).

## Opt-in

The twins install only on a host that opts in with `"gptAgents": true` under `claude` in the machine config `pfm.config.json`: pfm then writes `gpt-agents` into the `targets.capabilities` list of the global `~/.claude/engine-sync.json`, which every engine-sync call reads and passes `--capability gpt-agents` on its own install and doctor calls. Without it, check reports each twin `SKIPPED {twin} — requires gpt-agents`, machine doctor prints its `agent SKIPPED all {twin} {path} (requires gpt-agents unmet)` row, and an install retires the twins' links and generated files an earlier opted-in install owned, never a file it does not own.

## Hooks

No twin carries a hook: neither its declaration nor its original's frontmatter attaches one. The GPT Stop-hook scripts and their ownership receipt an earlier install placed are retired: `pfm install` and uninstall remove each `hooks/` link that resolves exactly to its managed copy in the default Claude store, an explicit install store and every account, then the managed copies, their log directories and the receipt; an operator file or a foreign link at the same path is kept and named (`retireGptHooks`, `pfm/internal/installer/retired_hooks.go`).

## Engine mirrors

The twins are Claude-only: `shared/gpt.md` sets `engines.codex` and `engines.opencode` to `false`, so engine-sync writes no Codex role and no OpenCode agent for them in any scope. A Codex or OpenCode session runs the ordinary roles on its own models. The adapter installs no provider credentials and translates no Claude hook into another engine's hook.

## Hand-copy conflicts

A pre-existing regular file at a twin's name in an account's `agents/` directory is not pfm-owned: installation reports the conflict instead of replacing it, and doctor reports it `SHADOWED`. `pfm install --adopt-agents` moves each such file under `{XDG state}/engine-sync/adopted/{UTC timestamp}/` and links the rendered twin in its place. A change to a twin belongs in its own `{name}.md` declaration or its original; recreating a hand copy defeats install ownership.

## Verification

`scripts/check-agent-roster.test.mjs` renders the shipped declaration files through the built engine-sync, with and without `--capability gpt-agents`, and pins on the rendered twins what it pinned on the old source files: no machine-absolute path, the renames applied, model and effort, the compaction block and the size cap — 15 KiB for a worker twin, 2 KiB over the larger of 15 KiB and its original for a role twin. It also proves each twin linked only with the capability, `SKIPPED` without it, and written as no Codex role and no OpenCode agent. Its `templates.mirrors` gate registration runs the cases. The installer and install-health tests check account fan-out, repair and named failures. Run the affected scripts through `.claude/scripts/test.sh` while building, and the full gate, `.claude/scripts/test.sh --full`, once before landing.
