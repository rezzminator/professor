# GPT twins

## Runtime prerequisite

These machine-global agents are optional entry points. Invoke them by registered name without the Agent tool's `model` override. A compatible gateway must accept the Claude harness's Anthropic Messages requests and route the GPT model IDs to an OpenAI-compatible backend. The `[1m]` suffix is a harness context-window hint; the gateway must support the declared IDs and reasoning efforts. [gpt-gateway](https://github.com/rezzminator/gpt-gateway) is that gateway, a separate repository pinned as the `plugins/gpt-gateway` submodule and installed on its own; Professor ships no credentials, endpoint or account configuration.

Installation and mirror compilation are local file operations: they do not contact a model provider or require a gateway. A host without one can install normally and use the ordinary Claude agents; invoking a GPT twin without a matching route fails at the provider boundary.

## Agent sources

| Agent | Model | Effort | Role manual |
| --- | --- | --- | --- |
| `gpt-foreman` | `gpt-6.1-sol[1m]` | `high` | `flights-foreman.md` |
| `gpt-foreman-pro` | `gpt-6.1-sol[1m]` | `xhigh` | `flights-foreman.md` |
| `gpt-lander` | `gpt-6.1-sol[1m]` | `high` | `flights-lander.md` |
| `gpt-executor` | `gpt-6-luna[1m]` | `high` | `flights-mechanical-executor.md` |
| `sol` | `gpt-6.1-sol[1m]` | `high` | General-purpose body |
| `luna` | `gpt-6-luna[1m]` | `medium` | General-purpose body |
| `terra` | `gpt-5.6-terra[1m]` | `high` | General-purpose body |
| `astra` | `gpt-6-astra[1m]` | `high` | General-purpose body |

The originals live under `templates/global/agents/`; `pfm install` links them into every configured Claude account's `agents/` directory. `templates/refresh-map.json` marks them curated, so refresh never re-derives them from a host's hand copies. Their `autoCompact` block pins `forceAt: 230k`, `nudgeFrom: 190k`, `nudgeEvery: 20k`.

They are source files, not `variants.json` declarations: engine-sync variants accept single-line scalar overrides of existing keys, replacing any nested continuation block. That cannot replace the foreman's hooks or the original manuals' compaction block faithfully. The thin role bodies resolve `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/agents/{manual}`, read that installed manual and redirect child flight roles to the GPT twins. `gpt-foreman-pro` keeps ordinary `gpt-foreman` children; its xhigh effort belongs to the entry agent only. The bodies retain the explicit artifact-writing and return-only contract.

## Owned hook assets

`pfm/internal/installer/assets/gpt-return-watch.py` and `gpt-agent-stop.py` are the canonical scripts. Installation stages embedded managed copies and creates exact-source symlinks under `hooks/` in the default Claude config directory, an explicit install store and every configured account directory. Subsequent installs repair owned content and missing links; regular operator files and foreign links are visibly refused, never overwritten. `gpt-hook-ownership.json` records successful physical target paths so uninstall retires owned links even after an account is removed or a directory alias moves. Each retirement rechecks the exact source link; the receipt grants no right to delete an operator replacement. Malformed receipts are visible failures. Doctor names missing, unreadable or changed scripts. They are agent-frontmatter hooks, not machine-wide `settings.json` hooks.

The foreman and pro invoke them through `python3 -I` and `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/hooks/`, so account-local paths and spaces resolve without a machine-specific interpreter path. `python3` is the existing host prerequisite.

- `gpt-return-watch.py`: PreToolUse for Bash. A recognized single-return-file background watch whose reader drifted is restored to the manual's `cat "$1"`; only a simple literal path receives automatic permission. Shell operators, expansions and globs pass unchanged through normal permission checks, as do canonical watches and unrelated calls. Decisions are logged under `{fleet home}/.local/state/pfm/gpt-return-watch/` or `GPT_RETURN_WATCH_LOG_DIR`; parsing/log failures are visible on stderr and do not block a call.
- `gpt-agent-stop.py foreman`: Stop, scoped to the foreman agent. It validates the return's first line against explicitly assigned flight/unit names, permits waiting for typed background launches that have not completed, and caps refusals at two per agent using a persistent per-agent counter rather than rescanning audit history. A refusal is emitted only after its counter and audit write succeed; failures are visible and permit stopping. Its retained `sub-tracer` role requires a readable, regular, nonempty stored hops artifact and caps refusals at three. No sub-tracer twin is registered by this family. Decisions are logged under `{fleet home}/.local/state/pfm/gpt-agent-stop/` or `GPT_AGENT_STOP_LOG_DIR`.

For both hooks, `{fleet home}` is `PFM_HOME` when set, otherwise the user's home; the explicit `GPT_*` log-directory overrides take precedence. These assets retain the restored audit format and role contracts with regression-tested safety and jail repairs.

## Engine mirrors

Each source pins `codex-model` to the same GPT ID without `[1m]` and `codex-effort` to its declared effort. Codex honors those pins before `modelMap`, so an adopter does not need entries for the full Claude-side IDs. The machine-global path links Claude sources and writes Codex roles; it does not emit global OpenCode agents. Repository Codex and OpenCode mirrors still compile independently. When a source is compiled in project scope, OpenCode follows its existing `opencode-model`/`modelMap` policy: an unmapped full GPT ID produces a named warning and omits the model, inheriting the session model. An adopter wanting that project-scoped backend configures its provider and maps the full source ID to that provider's model. The adapter does not install provider credentials or translate Claude hooks into another engine's hooks.

## Hand-copy conflicts

A pre-existing regular agent file is not pfm-owned: installation reports the conflict instead of replacing it. Back up the hand-written twins from each configured account's `agents/` directory before `pfm install --yes`, along with any hand-deployed `hooks/gpt-return-watch.py` and `hooks/gpt-agent-stop.py` entries that conflict with the owned links. Once linked, changes belong in the clone's source files; recreating hand copies defeats install ownership. The hooks are owned installer assets, not hand-deployed prerequisites.

## Verification

`scripts/check-agent-roster.test.mjs` includes cases checking the eight GPT source frontmatters, portable manuals, child redirects, hook commands, model/effort pins, compaction, curated registrations and 15 KB cap. Its existing `templates.mirrors` gate registration runs the cases; no standalone unregistered test file is required. The installer and install-health tests check account fan-out, repair and named failures; hook fixtures check runtime behavior without a provider account. Run the affected scripts through the fenced testing runners and both full gates, `iso gate pfm` and `iso gate templates`, before landing.
