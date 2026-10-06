# UPGRADING — Move a host across several pfm versions

A host several releases behind carries old shapes the new `pfm` refuses: a config at a retired path, databases at the pre-layout paths, identity files in the shared Claude store, retired wiring in settings. Each one is a host check row with its own `fix:` line. `pfm install` refuses while any `BLOCK` row stands and does not apply those fixes for you; `pfm install --check --plan` lists all of them in the order to apply them.

For the per-release actions and the project-tier update (`pfm doctor --project-updates`), see [INSTALL.md § Updating](INSTALL.md#updating); this page is the host side.

## Contents

- [The upgrade path](#the-upgrade-path)
- [Reading the plan](#reading-the-plan)
- [Single account and gateway authentication](#single-account-and-gateway-authentication)
- [Claude environment for daemon-spawned chats](#claude-environment-for-daemon-spawned-chats)
- [Going back](#going-back)

---

## The upgrade path

1. **Read the releases you skipped.** Each `releases/vX.Y.Z.md` between `pfm version` and the target lists what it asks of you; [INSTALL.md § Updating](INSTALL.md#updating) explains how to merge them into one list.
2. **Install the new binary.**
   - Binary install: replace `~/.local/bin/pfm` as in [INSTALL.md § 1](INSTALL.md#1-binary-install--pfm-only-2-minutes).
   - Source checkout: check out the target tag, then `make -C pfm build`; its last line, `built: {path}`, names the new binary. Use that path for steps 3 and 4: `make -C pfm host-install` runs `pfm install --check` before it swaps the binary in, and refuses while a host check blocks.
3. **Print the plan:** `pfm install --check --plan`. It runs every host check, prints each finding with its fix in apply order, and writes nothing.
4. **Apply the fixes in order**, top to bottom, then rerun `pfm install --check --plan`. Close every chat first: identity and database moves are unsafe under a running chat or pfm service, and the fix lines that move them say so. Repeat until the plan prints `no fixes`, or only `WARN` rows you have decided to keep.
5. **Install:** `pfm install --yes` (source checkout: `make -C pfm host-install`, which checks, swaps the binary and runs the install).
6. **Verify:** `pfm doctor`. Every required row should be green; a remaining `WARN` row prints its fix the same way.

## Reading the plan

```text
install plan: {N} host checks ran, {F} failed — {verdict}
  FAILED {check} {path} — {problem}
      fix: {fix}
  1. BLOCK {check} {path} — {problem}
      fix: {fix}
  2. WARN {check} {path} — {problem}
      fix: {fix}
  then: pfm install --yes, then pfm doctor
```

- The order follows what each fix reads. The pfm config comes first, because it names the paths, the accounts and the port every later check reads. The databases follow, moved with pfm's services stopped. Then the Claude store and the account directories, which must be real before anything inside them is edited. Then wiring: hooks, plugins, MCP entries, settings `env`, the login shell. Then leftovers nothing reads, and last the fixes that are a run of `pfm install` itself. Within each group, `BLOCK` rows come before `WARN` rows.
- A `FAILED` row is a check that could not look: an unreadable or unparsable file, or a check that cannot run where you ran it. The plan is incomplete until each one is resolved, so it ends with `then: rerun pfm install --check --plan` instead of the install step.
- `no fixes` prints only when every check ran and none failed.
- Exit codes: `0` the plan holds no `BLOCK` row and the remaining pre-change checks passed; `4` a `BLOCK` row, or a running name-sync job; `1` a required dependency is missing, an explicit `--config` file does not exist, or no host check ran; `2` usage (`--plan` needs `--check`).

## Single account and gateway authentication

pfm supports a single-account setup. With one entry in the `accounts` list of `pfm.config.json`, account 1's directory (`~/.cc/1` by default) is the only per-account directory: a fix line that says "each account" or "this account" means that one directory, and the shared store stays `~/.claude`.

A host that authenticates through an LLM gateway instead of a subscription login keeps its auth in Claude's settings: `apiKeyHelper` names the command that prints the key, and the gateway's variables (its base URL and any model or beta switches) sit in the settings `env` block. Two consequences for an upgrade:

- Claude Code caches its GrowthBook feature flags in each account's `.claude.json` (`cachedGrowthBookFeatures`). Behind a gateway the GrowthBook fetch never succeeds, so a cached value never refreshes, and a flag cached `false` stays `false` across every upgrade. `pfm doctor` reports the plugin hook-module flag as a `function-hook-modules` row, with its fix.
- Gateway variables exported only in your login shell do not reach chats the pfm daemon starts; see the next section.

## Claude environment for daemon-spawned chats

`pfm mcp serve` runs as a service: a launchd agent on macOS (`com.professor.pfm.mcp`) and a systemd user service on Linux (`pfm-mcp.service`). A chat it starts, through `chat_new` or any other professor MCP tool, inherits the service's environment, not your login shell's. A `CLAUDE_*` or `ANTHROPIC_*` variable exported in `.zshrc` reaches the chats you open from a terminal and is missing from daemon-spawned ones, so the two diverge.

Put every Claude variable a chat needs in the settings `env` block, never only in a shell export. `pfm doctor` names each variable exported in the login shell but missing from a settings `env` block as a `shell-claude-env` row, with the file to add it to.

## Going back

- Source checkout: `make -C pfm rollback` restores the previous binary that `make -C pfm host-install` kept as `pfm.prev`, reruns its `pfm install --yes`, and restarts the MCP daemon.
- Binary install: install the previous release's binary the same way as the new one.

Neither path undoes the host fixes you applied by hand. A file you moved stays where the fix put it, and an older binary may report it as a finding of its own.
