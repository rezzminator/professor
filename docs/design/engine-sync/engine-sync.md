# engine-sync

engine-sync carries a project's Claude Code configuration to Codex and OpenCode. It is one Go binary and one Claude Code plugin in their own repository, `rezzminator/engine-sync`, cut out of pfm's `codexgen` and `opencodegen`. The binary is the single writer of every Codex and OpenCode artifact derived from Claude sources; the plugin's hooks run it inside Claude sessions; Codex and OpenCode install nothing and run the binary only through a hook entry and a shim the binary writes, for path-scoped rules. pfm keeps the fleet runtime and calls the binary.

**Status:** designed, not built — **Build path:** `flights-foreman`, one flight per phase, P1 first — **Working name:** `engine-sync`

`{codex home}` is one Codex account's home; `{claude config dir}` is one Claude account's `configDir`; `{engine-sync home}` is `~/.local/share/engine-sync`; `{engine-sync state}` is `~/.local/state/engine-sync`; `{engine-sync config}` is `~/.config/engine-sync`.

## Contents

- [Decisions](#decisions)
- [Engine facts](#engine-facts)
- [The split line](#the-split-line)
- [Repository layout](#repository-layout)
- [The binary](#the-binary)
- [The Claude plugin](#the-claude-plugin)
- [Machine-level writes](#machine-level-writes)
- [Agent sources](#agent-sources)
- [pfm after the cut](#pfm-after-the-cut)
- [Doors](#doors)
- [Phases](#phases)
- [Checks](#checks)
- [Rejected alternatives](#rejected-alternatives)
- [Open items](#open-items)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Sources](#sources)

## Decisions

- **One repository, one binary, one Claude plugin.** `rezzminator/engine-sync` holds the Go binary and `plugins/engine-sync/`, the installable Claude plugin, in the house layout of `plugins/callmeter`. It becomes a submodule under `plugins/` and an entry in `.claude-plugin/marketplace.json` at publication.
- **Codex and OpenCode install nothing.** Everything they consume is a file engine-sync writes: compiled mirrors, role TOMLs, an owned hook entry in `{codex home}/hooks.json`, an owned shim in `~/.config/opencode/plugins/`.
- **They run the binary for one job only:** path-scoped rule injection at tool time, which no compiled file can do (`docs/design/rules/rules.md`).
- **The binary is the single writer.** pfm execs it and never imports its packages. A library copy inside pfm next to the plugin's own copy is two writers at two versions.
- **Extract first, byte-identical.** P1 moves the code with its outputs unchanged; behaviour changes start in P2. Rules are built inside engine-sync (P3), never in pfm first.
- **Projects opt in.** The binary touches a project only when it carries engine-sync configuration or an output with engine-sync's generated marker. A user-scope install never writes into a repository that did not ask.
- **Each engine's machine config has one writer.** engine-sync writes only the files it owns, each behind its own marker, fence or receipt; pfm keeps writing only the fleet-runtime files it owns today.
- **An older binary never overwrites a newer one's output.** Several Claude config dirs can hold the plugin at different versions; the binary refuses and names the copy to update.
- **One forwarding release.** `pfm codex build|check|agents` and `pfm opencode build|check|doctor` exec the binary for one release, then go.
- **Publication waits for the user's plain ask:** the GitHub repository, every push, the release binaries, the submodule and the marketplace entry.

## Engine facts

Verified in source against Codex at `openai/codex@64335d1a` and OpenCode at `sst/opencode@38840623`, and in the Claude Code docs through 2.1.295.

| Fact | Claude Code | Codex | OpenCode |
| --- | --- | --- | --- |
| Plugin can carry agent roles | yes | no (issue #36855, open) | through the `config` hook only |
| Plugin can carry instructions | n/a | no | through the `config` hook only |
| Reads `.claude-plugin/plugin.json` | yes | yes, after `.codex-plugin/plugin.json` | no |
| Hook process environment | `CLAUDE_PLUGIN_ROOT`, `CLAUDE_PLUGIN_DATA`, `CLAUDE_PROJECT_DIR` | `PLUGIN_ROOT`, `PLUGIN_DATA` and the `CLAUDE_*` pair | n/a, plugins run in-process in Bun |
| Hook needs trust | folder trust, interactive only | every hook, a `sha256` of event, matcher and handler | no |
| Plugin root path | versioned, changes on every update | versioned | n/a |
| Runtime guaranteed to a hook | none named; `node` not promised | `$SHELL -lc` only | Bun, in-process |
| Hooks fire without a session | no | no | no |

Consequences: hooks call a binary at a path that never changes, `{engine-sync home}/bin/engine-sync`; Codex hook command strings stay constant so trust survives updates; nothing depends on `node` or `python` on a host.

## The split line

| Moves to engine-sync | Stays in pfm |
| --- | --- |
| `pfm/internal/codexgen`, except `fleetprompt.go` | `codexgen.FleetPrompt`, moved to a pfm package |
| `pfm/internal/opencodegen` | Codex `developer_instructions` fence (`codex_defaults.go`, `codex_developer_instructions.go`) |
| `pfm codex build\|check\|agents`, `pfm opencode build\|check\|doctor` | OpenCode `instructions` receipt (`opencode_instructions.go`), the fleet prompt's one door |
| installer Codex role, command and skill fan-out | `resume-unkill`, `codex_hooks.go`, `hook_probe.go`, `codexappendix` |
| workbench mirror compile (`workbench/mirror.go`) | `codexmeta`, the session reader with 11 runtime importers |
| `.claude/scripts/codex-sync.sh` and its template twin | workbench OpenCode seat plugin (`workbench/opencode_seat.go`) |

pfm sheds about 6.4k non-test and 5.5k test lines of Go, about 3% of pfm; the gain is scope: pfm stops compiling configuration.

## Repository layout

```text
engine-sync/
  cmd/engine-sync/                  the binary
  internal/codexgen/                cut from pfm/internal/codexgen, history kept
  internal/opencodegen/             cut from pfm/internal/opencodegen, history kept
  plugins/engine-sync/              the installable Claude plugin, nothing else
    .claude-plugin/plugin.json
    hooks/hooks.json
    libexec/engine-sync             POSIX sh wrapper
    libexec/SHA256SUMS
  .claude-plugin/marketplace.json   the repository is its own marketplace
  CLAUDE.md  CHANGELOG.md  README.md  LICENSE
```

The repository's `CLAUDE.md` follows `plugins/callmeter/CLAUDE.md`: its gates (`go vet`, `go test`, `claude plugin validate --strict`), `main` release-only, tags `engine-sync--vX.Y.Z`, Git writes through gitter, no machine-absolute path in a tracked file.

## The binary

### CLI surface

| Command | Does | Replaces |
| --- | --- | --- |
| `engine-sync build [repo]` | compiles the Codex and the OpenCode mirror | `pfm codex build` + `pfm opencode build` |
| `engine-sync check [repo]` | reports drift, writes nothing, exits 1 on any finding | `pfm codex check` + `pfm opencode check` |
| `engine-sync doctor [repo]` | `check` plus OpenCode surface validation, targets and owned-file state | `pfm opencode doctor` |
| `engine-sync global [--codex-home H]…` | role TOMLs, global prompts, skills and skill links into each Codex home, and commands and agents into OpenCode's global dir | `pfm codex agents` and the installer's Codex steps |
| `engine-sync hook claude {mark\|stop\|start}` | the Claude plugin's hooks | `codex-sync.sh mark\|sync` |
| `engine-sync hook {codex\|opencode}` | path-scoped rule injection (P3) | nothing; `rules.md` |

`build` and `check` keep every flag of `pfm/cmd/pfm/codex_command.go:128-136` and the `--home` of `opencodegen/command.go`, with their meanings.

### Binary resolution

`libexec/engine-sync` resolves the binary in the order `plugins/callmeter/plugins/callmeter/libexec/callmeter` uses: `$ENGINE_SYNC_BIN` when it names an executable, then the cached copy for the wrapper's own version under `{engine-sync home}/versions/{version}/`, then the GitHub release asset for that version, verified against `libexec/SHA256SUMS`. It then points `{engine-sync home}/bin/engine-sync` at the resolved copy, moving the link only forward in version. Every caller outside the plugin (the Codex hook entry, the OpenCode shim, pfm) runs that stable path. Under `hook`, an unavailable binary exits 0 and logs; every other subcommand exits 1 with `engine-sync: binary unavailable: {cause}`.

### Ownership and opt-in

A project is in scope when it has `.claude/codex-build.json` (P1) or `.claude/engine-sync.json` (P2), or when an output it would write already carries an engine-sync marker. P1 keeps pfm's markers, `Generated by pfm codex build` and the legacy line (`pfm/internal/codexgen/reconcile.go:14-15`), so P1 output is byte-identical; P2 introduces engine-sync's marker and claims the old ones, the pattern `legacyGeneratedMarker` already follows.

### Version guard

Every write records the writer's version where it costs no per-file churn on an upgrade. A binary older than the recorded version writes nothing and reports `NEWER-WRITER {version}`, naming the Claude config dir whose plugin to update. A per-project lock serialises concurrent builds; writes stay atomic.

## The Claude plugin

Command hooks, not function-hook modules: modules need `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1`, run without Node, and cannot be called by Codex, OpenCode or CI.

| Event | Command |
| --- | --- |
| `PostToolUse`, matcher `Write\|Edit` | `${CLAUDE_PLUGIN_ROOT}/libexec/engine-sync hook claude mark` |
| `Stop` | `${CLAUDE_PLUGIN_ROOT}/libexec/engine-sync hook claude stop` |
| `SessionStart`, `async: true` | `${CLAUDE_PLUGIN_ROOT}/libexec/engine-sync hook claude start` |

### `hook claude mark`

Sets the project's dirty flag only when the written path is a Claude source; exits 0 silently when the binary is missing.

### `hook claude stop`

When the flag is set: `build`, then `check`. The first failure emits `decision: block` naming the failed stage; under `stop_hook_active` it emits a `systemMessage` instead. A missing binary emits a `systemMessage` warning, exits 0 and keeps the flag.

### `hook claude start`

Links the stable binary, runs `check` and rebuilds when stale (a `git pull` lands here), and refreshes the machine-level targets.

The `mark` and `stop` behaviour is `.claude/scripts/codex-sync.sh` ported; every case `scripts/test-codex-sync.sh` pins becomes a Go test of `hook claude`. The dirty flag moves from `/tmp/{project}/guard/codex_dirty` to `{engine-sync state}`. `$ENGINE_SYNC_BIN` replaces the fence dev-binary preference (`/tmp/{project}/timing/pfm-dev-bin`).

A repository cannot install an external-source plugin for its contributors from its own settings; each Claude account installs it once, and `pfm install` does that for every fleet account.

## Machine-level writes

`{engine-sync config}/targets.json` lists the Codex homes and the host's capabilities: `{"codexHomes": ["{codex home}", …], "capabilities": […]}`. Either key may be absent: `pfm install` writes it from `config.CodexHomes()` (`pfm/internal/config/accounts.go:15`); without the file, or without `codexHomes`, the targets are `$CODEX_HOME`, else `~/.codex`. OpenCode's global dir is `~/.config/opencode`, as `pfm/internal/installer/mcp.go:445` hardcodes.

`engine-sync global` writes, per Codex home: role TOMLs under `agents/` as marker-owned regular files (Codex refuses a symlinked role), global prompts and skills, and skill links. In P3 it adds the two hook entries and the global rules block of `rules.md`. Into OpenCode's global dir it writes `command/`, the machine-global agents unless their `engines.opencode` is `false`, and, in P3, the rules shim and the `instructions` entry, each with an engine-sync receipt beside pfm's.

Codex hook trust (P3): pfm registers it for the fleet through `codexappendix.RegisterHookTrust` (`pfm/internal/codexappendix/trust.go:264`); a standalone user trusts once in `/hooks`. `codex exec` silently skips an untrusted hook, so `engine-sync doctor` reports `UNTRUSTED` per home.

## Agent sources

Each agent's settings live in its own frontmatter, per-engine settings included; engine-sync parses every source kind with one YAML node parser, and a frontmatter that does not parse is `FRONTMATTER-INVALID {file}:{line}`, never body. A header failing strict YAML is retried once with each single-line `key: value` that does not parse on its own read as a literal string, so the single-line values Claude Code accepts (`argument-hint: [pr-number] [priority]`, `description: Review: run the checks`) still parse.

- **`engines:`** — `codex: {model, effort, sandbox}` and `opencode: {model, mode, permission}`; either engine may be `false`, so the agent is not emitted there. A `model` naming a `codex.modelMap` or `opencode.modelMap` key is mapped, any other value is emitted verbatim; `engines.codex.effort` precedes `effort`, and Codex receives `max` as `xhigh`. gitter's OpenCode Git allow is `engines.opencode.permission` in its three `gitter.md` sources. The flat `codex-*` and `opencode-*` keys are refused, naming the `engines.*` path that replaced them.
- **`requires:`** — capability names; an agent is installed or emitted only when the host holds all of them. The capability set is `--capability NAME` on every rendering verb plus `capabilities` in `targets.json`; names mean nothing inside engine-sync. pfm passes `--capability gpt-agents` when `claude.gptAgents` is true ([GPT twins](../gpt-twins/gpt-twins.md)).
- **Keys** — Claude Code's native sub-agent keys, engine-sync's own `engines`, `requires` and `role`, and the host's `agents.passthroughKeys` (Professor's machine policy lists `autoCompact` and `visibility`); any other key warns `UNKNOWN-KEY`.
- **Derived agents** — `templates/global/agents/variants.json`, schema version 2: each variant names `from` (an original, another variant, or `general-purpose`), optional `profile` chains, frontmatter `set`, `add` and `unset`, hash-pinned `## Section` overrides, `rename`, and `prepend`/`append` text, with shared `fragments`. engine-sync stamps `role: {root original}`; a drifted pin fails `SECTION-DRIFT` until `engine-sync variants pin` re-pins it after review. The schema is `plugins/engine-sync/docs/interface.md`.
- **Machine doctor** is closed-world per engine: each declared agent is `INSTALLED`, `MISSING`, `STALE`, `SHADOWED`, `SKIPPED` or `BROKEN` (its source or render fails, a failing row that leaves the healthy agents inspected), and an unowned file at an undeclared name in a managed registry is a `STRAY` warning. `engine-sync global --adopt` (`pfm install --adopt-agents`) moves a shadowing hand file under `{XDG state}/engine-sync/adopted/{UTC timestamp}/` and installs the declared agent.

## pfm after the cut

- **`pfm install`** installs no engine-sync plugin on its own; listing the full id `engine-sync@professor` in `claude.plugins` has it ensured through the primary account like any listed plugin (`pfm/internal/installer/claude_plugins.go`). It writes `targets.json` and runs `engine-sync global --codex-home H` for every home.
- **Workbench:** `EnsureMirror` and `CheckMirror` (`pfm/internal/workbench/mirror.go:21`) exec `engine-sync build|check {bench}`.
- **`pfm doctor`** runs `engine-sync doctor` and renders its rows; a missing binary is a failing row, never an absent one.
- **Forwarders:** for one release `pfm codex build|check|agents` and `pfm opencode build|check|doctor` exec the stable binary with the same arguments, print one stderr line naming the engine-sync command, and pass its exit code through. Without them every adopter's Stop hook calls a `pfm codex` that answers `unknown action`, which `codex-sync.sh` turns into `decision: block` on every Stop.

## Doors

Every pfm path that writes a Codex or OpenCode artifact from Claude sources. P1 is done when each one is gone or execs the binary.

| Door | Location |
| --- | --- |
| `pfm codex build\|check` | `pfm/cmd/pfm/codex_command.go:128` → `codexgen.Run` |
| `pfm codex agents` | `codex_command.go:169` → `codexgen.RunGlobalAgents` |
| `pfm opencode build\|check\|doctor` | `pfm/internal/opencodegen/command.go:65` → `opencodegen.Compile` |
| install `wireCodexAgents` | `pfm/internal/installer/installer.go:219` → `RunGlobalAgents` |
| install `reconcileCodexCommands` | `installer.go:224` → `RunGlobalCommands` |
| install `retireOrphanCodexAgents` | `installer.go:226` |
| install `wireGlobalSkills`, Codex-home links | `installer.go:217`, `skill_sources.go`, `skill_sources_report.go` |
| install role fan-out and retirement | `global_fanout.go`, `global_agent_retire.go` |
| workbench mirror | `pfm/internal/workbench/mirror.go:21`, called from `pfm/internal/hookentry/codex_launch.go:113` and the chat new, satellite, reload and persona paths |
| test jail role staging | `pfm/internal/testjail/registry.go` → `RunGlobalAgents` |
| Stop and PostToolUse hooks | `.claude/scripts/codex-sync.sh` via `.claude/settings.json:52,67`; `templates/project/scripts/codex-sync.sh` via `templates/project/settings.json:58` |
| CI mirror generation | `.github/workflows/verify.yml:46-48`, `.github/workflows/plugin-scan.yml:42-43` |
| fence mirror generation | `infra/fence/checks.sh:216-221` |
| single-writer check | `scripts/check-opencode-writer.mjs:34` |
| dead compiler | `templates/project/scripts/build-codex.mjs`, checked by `scripts/check-codex-markers.mjs` (`infra/fence/checks.sh:235`), claimed by `CLAUDE.md:56` |

The dead compiler has been unwired since v0.58.0 (`releases/v0.58.0.md:14`) and still ships to adopters; it is deleted in P1 whatever else lands.

## Phases

### P1 extract

**Scope:** the repository, the binary, the Claude plugin, every door above, local only.

**Acceptance:**

1. `engine-sync build` output equals `pfm codex build` plus `pfm opencode build` output from the cut commit, byte for byte, for this repository and a fixture adopter, project files and jail-home files both.
2. The tests that move with `codexgen` and `opencodegen` pass in engine-sync's own gate, and `check` reports the same `CONFLICT`, `ORPHAN`, `UNPARSEABLE` and `INVALID` lines for the same broken fixtures.
3. `engine-sync global --codex-home H` writes what `pfm install`'s Codex steps write today into a jail home.
4. Every `scripts/test-codex-sync.sh` case passes as a Go test of `hook claude`; `claude plugin validate --strict` passes.
5. `go list -deps ./...` in `pfm/` lists neither package; the forwarders keep today's exit codes; `pfm doctor` shows a failing row when the binary is absent.
6. `.claude/scripts/dev.sh iso gate pfm` and `iso gate templates` pass; every door in the table is gone or execs the binary.

**Guarded files** (`.claude/**`, `templates/**`, `CLAUDE.md`) land through `/pcm` in the main chat; the flight reports the exact change each needs.

### P2 configuration and policy as data

`.claude/codex-build.json` and the never-created `.claude/opencode-build.json` merge into `.claude/engine-sync.json`. Professor policy hardcoded in Go becomes data in Professor's machine policy, `templates/global/engine-sync/config.json`, which `pfm install` links into the engine-sync config directory (a project's own `.claude/engine-sync.json` holds only its overrides): the OpenCode guarded-file and Git-write denies (`plugins/engine-sync/internal/target/opencode/config.go`). gitter's sandbox exemption (`plugins/engine-sync/internal/target/codex/agents.go`) becomes `engines.codex.sandbox` in gitter's own frontmatter, read by the one Codex role renderer both paths share. Global roles come from `{claude config dir}/agents/`, not from the Professor checkout. engine-sync's own marker replaces pfm's.

### P3 rules

`docs/design/rules/rules.md`, built inside engine-sync, with its Phase 0 probes run first.

### P4 publication

On the user's plain ask: the GitHub repository and push, the first tagged release with its binaries and `SHA256SUMS`, the submodule (`branch = develop`, `ignore = dirty`), the marketplace entry, and the pfm release carrying the forwarders. The release after removes the forwarders.

## Checks

| Check | Broken state it reports |
| --- | --- |
| `engine-sync check` | `CONFLICT`, `ORPHAN`, `UNPARSEABLE`, `INVALID`, `STALE` for a mirror out of date |
| `engine-sync check` | `NEWER-WRITER {version}` when an older binary meets a newer one's output |
| `engine-sync doctor` | `TARGET-MISSING {codex home}` for a listed home that does not exist |
| `engine-sync doctor` | owned role, prompt or skill `MISSING` or `STALE` in a target; a declared agent `MISSING`, `STALE`, `SHADOWED`, `SKIPPED` or `BROKEN`, and a `STRAY` warning |
| `engine-sync check` | `FRONTMATTER-INVALID`, a retired flat key, `SECTION-DRIFT`, `SECTION-MISSING`, `RENAME-UNUSED`, `UNSET-MISSING` in an agent source or a rendered variant |
| `engine-sync doctor` | `UNTRUSTED` Codex hook entry (P3) |
| `pfm doctor` | engine-sync binary `MISSING`, or its doctor exit status, as a failing row |
| Stop hook | `decision: block` naming the failed `build` or `check` stage |

## Rejected alternatives

### A native plugin per engine

A Codex plugin cannot carry roles or instructions, so it would add only hooks; its hooks need trust like any other, the docs say plugin hooks load in "Codex desktop" while the CLI shares the code gate, and three independently updated copies multiply the version-skew surface. A hook entry and a shim the binary writes deliver the same runtime with no install in either engine.

### A function-hook (mods) plugin in TypeScript

The house pattern for the TypeScript plugins, but a module runs with "no DOM, no Node", needs `CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1`, and cannot be run by a Codex hook, OpenCode or CI. It also turns 5.7k Go lines and 5.5k test lines into a rewrite instead of a move.

### pfm importing engine-sync as a Go library

pfm's binary keeps its size, and two copies at two versions write the same files: the two-writer problem of `build-codex.mjs` and `pfm codex build` (`releases/v0.58.0.md:14`).

### Building rules in pfm first

Every rule line would be built twice: once in pfm, once again on the move.

### OpenCode live configuration through the `config` hook

OpenCode's V1 `config` hook can add agents, commands, instructions and MCP servers with no generated file, but the V2 plugin API never calls it and V2 is mid-transition. The compiled mirror stays.

## Open items

- How the fence and CI get the binary before publication: built from the local engine-sync checkout and passed as `$ENGINE_SYNC_BIN`, or another route the P1 flight designs.
- How a pfm test that needs compiled roles (the test jail) gets them: a fake binary or the real one.
- Where the version guard records the writer's version without touching every output on an upgrade.
- `bin/` on a hook's `PATH` is unconfirmed in the Claude docs; hooks call `${CLAUDE_PLUGIN_ROOT}/libexec/engine-sync` by path.

## Surfaces that stay in sync

- `docs/design/rules/rules.md`: built by engine-sync (P3).
- `docs/design/hooks/hooks.md`: the Codex and OpenCode hook rows.
- `docs/BLUEPRINT.md` § Optional: Codex dual-runtime and `docs/SETUP.md` § 7b: the compiler is engine-sync.
- `CLAUDE.md` § Vocabulary (engine mirrors, plugins) and § Runtime (Codex, OpenCode), through `/pcm`.
- `plugins/CLAUDE.md`: the plugin list and alpha copies; `README.md`: the plugin list.
- `.professor/manifest.json`: the `codex-build.json` and `codex-sync.sh` rows; `scripts/arch-check.sh` rows naming the moved packages.

## Sources

- `.professor/RR/codex-opencode-plugin-carry-install-2026-10-09.md`
- `.professor/RR/codex-path-scoped-instruction-injection-2026-10-09.md`
- `.professor/RR/opencode-path-scoped-instruction-injection-2026-10-09.md`
- Claude Code plugin docs: `https://code.claude.com/docs/en/plugins/components`, `https://code.claude.com/docs/en/plugins/loading`, `https://code.claude.com/docs/en/hooks`
