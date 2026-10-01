# pfm — CLI & MCP Surface

Current operator and integration surface. Legend: **●** established · **◆** enhanced · **✚** added.

## Contents

- [Global](#global)
- [Top-level commands](#top-level-commands)
- [`pfm chat` family](#pfm-chat-family)
- [MCP surface](#mcp-surface)
- [Shared engine `internal/ask`](#shared-engine-internalask)

## Global

- Config: `{clone}/pfm.config.json` (strict JSON — accounts/emoji/theme/permission posture/MCP/ask; `pfm --config PATH` or `PFM_CONFIG` overrides). `pfm install` migrates legacy config and Harvester settings through HostLayout. `claude.systemPrompt` (global or per-account): `production` (default), `lean` (`CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1`), or `professor` (the composed Claude prompt under `{clone}/pfm/harness-prompts/composed/` via `--system-prompt-file`). `claude.maxSubagentSpawnDepth` and `claude.maxConcurrentSubagents` set the corresponding launch environment values. Missing config means defaults; malformed config fails ordinary commands while `doctor`, `config show`, and `config validate` report the exact error. `config show` redacts secrets.
- Harness prompts: `make -C pfm prompts` composes one prompt per engine in `{clone}/pfm/harness-prompts/composed/` from `share/head.md`, the engine middle, and `share/tail.md`. Managed Claude launches read `claude.md` through `--system-prompt-file`; `pfm install` writes `codex.md` into each configured Codex home's `config.toml` as `developer_instructions`, inside its installer-owned fence; OpenCode reads `opencode.md` from its `instructions` array. `pfm install` does not stage prompt copies; the clone is the single source.
- Harvester config: `{clone}/harvester.config.json`, beside `pfm.config.json`, is the only source of Harvester settings (`enabled`, `external`, `search`, `scholarly`, `fetch`, `convert`, `cache`, `output`). Legacy files migrate through HostLayout. Validation names bad values; a file with secrets must be `0600`. Retired `HARVESTER_*`, `SEARXNG_URL`, or `BRAVE_API_KEY` variables still exported produce a `pfm doctor` warning.
- One binary, one dispatch. Everything below is `pfm <command> …`.

## Top-level commands

| Cmd | Usage | Does |
| --------------- | -------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| ◆ `ls` | `pfm ls [--plain\|--tsv]` | Fleet picker TUI. `Tab`/`Shift-Tab` switch Chats↔Stats↔Limits; `←/→` moves through the labeled chat-row carousel (`▶ open → ⚡ reboot → 🕐 1h → ✖ hide → ⏸ deactive`, `Enter` runs the boxed action). Hide changes visibility only; deactive stops the tmux server and leaves the chat resumable. Ctrl shortcuts stay as aliases. |
| ◆ `doctor` | `pfm doctor [--verbose]` | Reports configuration provenance, required dependencies, HostLayout migration state and legacy account-file leftovers, per-account Claude plugin install state, one `skill-source` row per source-fetched global skill (linked, MISSING, CONFLICT, NOT-FETCHED, OFFLINE, SKIPPED, CHECK-FAILED, NO-CLONE, NO-REGISTRY), MCP daemon health, prompt drift and embed checks, and the live spawn audit. Each row distinguishes healthy, absent, broken and could-not-look outcomes; required wiring failures exit nonzero. |
| ◆ `install` | `pfm install [--yes] [--rollback {id} [--force]]` | Dry-run by default. Reconciles the HostLayout through the install journal (strips pfm's legacy hooks and status lines from account `settings.json`, writes the managed `cleanupPeriodDays` drop-in at the configured value), installs the `cache-live-control`, `sub-agent-compact` and `agent-effort` plugins on every Claude account not both enabling it in `settings.json` and recording it in its own `plugins/installed_plugins.json` (`pfm doctor` warns per account on either gap), retires automatic Dream/STM injection hooks, fetches every skill `templates/global/skills/sources.json` registers into `~/.local/share/pfm/install/skills/{name}/` — a fresh shallow clone swapped in whenever `git ls-remote` reports a head other than the one recorded beside the store (`skills/.{name}.commit`) or the store lacks a usable root `SKILL.md`, never a git command inside a store — and links it into every account's `skills/` and `~/.agents/skills/` (a failed fetch keeps the last copy; `PFM_SKILL_SOURCES_OFFLINE=1` skips fetching), compiles global Codex agents, reconciles marker-owned global Claude-command → Codex mirrors, wires MCP units + clients when enabled, and fans out account config. `--rollback {id}` replays one journal. MCP-backed `chat-*` commands keep prompt cards but no duplicate global skills; `chat-interrogate` remains a skill because it has no MCP tool. |
| ● `uninstall` | `pfm uninstall` | Removes exactly the installer-owned surface (ownership ledger); manual hooks survive. |
| ◆ `chat new` | (see the chat family) | On a proof timeout the launch now presses `Escape` then `Enter` once and RE-PROVES against the engine's transcript before reporting. A rescued launch exits 0 and says what it pressed; an unrescued one still exits `codeUndelivered` and says the retry was tried. |
| ✚ `update` | `pfm update [--to vX.Y.Z] [--repo <path>]` | Ownership-aware, transactional whole-professor update: fetch tags → highest SEMVER tag (parsed, not lexical) → refuse dirty worktree → build twice + hash gate → stage → atomically replace ONLY ledger-owned pfm copies (previous binary preserved for rollback) → `install --yes` → `doctor` → finalize. Unowned PATH copies untouched (drift surfaced loudly instead). Failure → rollback or an exact residue report. Never pushes. |
| ✚ `init` | `pfm init [dir] [--force]` | Per-project scaffold: copies blueprint set (`CLAUDE.md`, `AGENTS.md`, `.claude/{settings,commands,agents,skills}`) with placeholders intact; refuses existing `.claude/` without `--force`; prints the "open Claude, run SETUP" handoff. |
| ✚ `config` | `pfm config init [--force] \| show \| validate` | `init` writes the default v2 file as strict comment-free JSON (0600, atomic; field docs print to stdout). `show` prints resolved config with `(default)/(file)` provenance, secrets redacted. `validate` = load + exact decode error with position. |
| ● `codex` | `pfm codex build\|check\|agents [repo-root]` | Compiler mirror, marker check, global agents md→toml. `agents` also auto-runs inside install. |
| ◆ `harvest` | `pfm harvest [--refresh] [--include-content=false] [--ocr-language latin\|zh\|ja\|ar\|ru\|he] [--json] [--header 'Name: value']... <url\|path\|identifier>...` | Multi-source read (each argument routed into `harvester_read`'s `urls`, `files` or `publications`) → markdown, cache + 24h TTL (`cache.ttlSeconds` in `harvester.config.json`). Stdout clips large sources; full docs live at the printed `path:`. |
| ◆ `harvest download-file` | `pfm harvest download-file [--json] [--header 'Name: value']... <url>...` | Downloads 1–50 files as bytes, unparsed; prints `path / kind / content_type / bytes` per item. A failed item is `ERROR: …` and exit 1; a refused header exits 2 before any request. |
| ◆ `harvest search` | `pfm harvest search [--type any\|paper\|book] [--limit N] [--json] <query>...` | Runs `harvester_search_literature`: ranked scholarly candidates, each with a `handle` to pass to `pfm harvest` or `harvester_read`'s `publications`, and each discovery source's status. |
| ◆ `harvest ask` | `pfm harvest ask -p "<prompt>" [--engine claude\|codex] [--model MODEL] [--effort EFFORT] [--refresh] <url\|doi\|path>...` | Harvests 1–50 sources into full cache files, preserves failed sources as labeled temporary receipts, then runs one configured `internal/ask` engine pass. `pfm harvest --ask -p "<prompt>" ...` is an equivalent compatibility spelling. The answer is stdout; available token usage is a named stderr receipt. Engine/model/effort default from machine config and explicit flags win. |
| ◆ `mcp` | `pfm mcp serve` · `pfm mcp serve --stdio` · `pfm mcp ls` · `pfm mcp <server> enable\|disable` | `serve` = ONE daemon process, two ports. Loopback `127.0.0.1:<mcp.http.port>` (default 18377) — header-free, unauthenticated, never generates or distributes local credentials: `/mcp/professor` serves every enabled family's tools as one server (a disabled family's tools are absent), the family views `/mcp/professor/harvester` and `/mcp/professor/chat` serve one family each and answer 503 with the enable command (`pfm mcp: <family> is disabled by config; enable it with: pfm mcp <family> enable`) while that family is disabled, every other path answers 404, and a request carrying an `Origin` header answers 403 on every path. External `external.host:external.port` (default 18378, off by default, `harvester.config.json external.*`) — the harvester family ONLY at `/mcp`, whose `harvester_read` has no `files` field (plus the signed, expiring `/files/{sha256}` download links, whose HMAC signature is their access control), behind a mandatory passphrase-OAuth and/or static-bearer wall, local reads confined to the cache root; a failed external bind leaves the loopback port serving and reports on `/status`. Single-instance guarded; `GET /status` returns `{pfmVersion, protocolVersion, servers, pid, startTime, endpoint, harvesterExternal}`, its `servers` map keyed by family and holding only the enabled families (doctor consumes it). `serve --stdio` is the only stdio server and the command every engine registers as `professor`: it forwards to the daemon's `/mcp/professor` when the daemon is up and compatible, and serves the combined server in process when the daemon is absent or incompatible. `ls` lists each family with its enabled state and where that state comes from; `<server> enable\|disable` toggles a family (`chat` → `mcp.servers.chat.enabled`, `harvester` → `harvester.enabled`). Bare `pfm mcp` and any unknown form print `usage: pfm mcp ls \| pfm mcp serve [--stdio] \| pfm mcp <server> enable\|disable` and exit 2. |
| ● `statusline` | (stdin JSON from Claude Code) | + `7d-fable` segment and `✦` Fable symbol; account badge from config (unknown → no badge, never 🥇). ✚ `--subagents`: one agent-panel row per sub-agent through `subagentStatusLine`, per `docs/design/context/statusline.md`. |
| ● `usage-hook` | (UserPromptSubmit hook) | OAuth usage fetch; see [usage-hook](#usage-hook) for shared cache and retry behavior. |
| ● `heal` | `pfm heal` | Codex projection heal. A wedged/midline cursor whose rollout is not canonically ordinalled is reported NONCANONICAL, and one whose rollout could not be read end to end is UNSCANNED — neither is ever deleted, since a rebuild from zero fails on the same record until Codex >= 0.154.0 projects past it. |
| ● `reap` | `pfm reap …` | Unchanged. |
| ✚ `issues` | `pfm issues [--all] [--json]` | Reads the servicedesk complaint ledger that agents file through `servicedesk`. Default view is open issues only; `--all` includes closed ones. The three states stay visibly distinct: an empty ledger prints `no open issues` and exits 0, a store that could not be read prints the cause on stderr and exits 1 (a failed look is never rendered as an empty one), and `--json` always emits an array so a script reading structured output gets `[]` rather than a prose sentence. |
| ✚ `callmeter` | `pfm callmeter report {files\|writes\|commands\|context\|sequences\|faults} [--since D] [--project P] [--agent-type T] [--session S] [--account N] [--limit N]` | Reads `$HOME/.local/state/pfm/callmeter.db`; an absent store reports nothing recorded, while an unreadable store names the error. `--account N` filters calls and requests by configured account and refuses an unknown id. A `--since` older than the 30-day window is clamped with one stderr note. |
| ✚ `price` | `pfm price [--json\|--check]` | Prints the model price table pfm owns: the embedded `internal/pricing/prices.json` merged by model key with `pfm.prices.json` beside `pfm.config.json`; each row says shipped or override. `--json` feeds token-audit; `--check` validates and exits 1 naming the fault. |
| ● `run` | `pfm run …` | Chat-spawn plumbing (backs `chat new`). Unchanged. |
| ● `agent` | `pfm agent open …` | Headless claude open path. Unchanged. |
| ◆ `internal` | `pfm internal callmeter\|clear-kill\|explore-deny\|git-guard\|rr-dir\|epic-inject\|reload-intercept\|exit-intercept\|exit-close` | Hook backends: ✚ `callmeter` (async on six events, records every Claude tool call into the call store `pfm callmeter` reads; section below), ✚ `explore-deny` (ports the shell script; stdin hook JSON → allow/deny), ✚ `git-guard` (PreToolUse, matcher `Bash`; rides every launch; stdin hook JSON → deny a shared git write to every agent but `gitter`, per `docs/design/hooks/git-guard.md`), ✚ `rr-dir` (SubagentStart, matcher `rr\|super-rr\|heavy-rr`; stdin hook JSON `cwd` → one `additionalContext` line: `RR-DIR: {nearest ancestor}/.professor/RR`, the same with `(fallback: …)` for the clone's own ledger, or `RR-DIR-ERROR: {reason}`; exit 0 on every path), ✚ `epic-inject` (see below), ✚ `reload-intercept` (see below), ✚ `exit-intercept` (see below), ✚ `exit-close` (see below). |
| ● `version` | `pfm version` | Verify identity by hash, not this string. |

### usage-hook

The hook and the `ls` Limits tab share the account cache in `usagehook.DefaultCacheDir` and reach the usage endpoint only through `usagehook.Fetch`. The hook uses its 180-second default TTL (`CC_USAGE_TTL`); the focused Limits tab uses sixty-second freshness (`LiveLimitsTTL`) and polls completed account results every two seconds. Accounts refresh independently, and pending requests are canceled when Limits loses focus or the picker closes. The display labels percentages as quota **used** and advances confirmation ages and reset countdowns every five seconds, including under `--no-sky`.

`usagehook.Fetch` answers, in order, from: a fresh `cc-rate-limits` statusline snapshot of the seat; a cache record fresh within the caller's TTL; an active shared backoff (the cached usage with the backoff's error); then, holding the `O_EXCL` refresh lock `acct-<id>.lock` (stale after 30 seconds; a caller that cannot take it answers from the cache; the holder re-reads the cache first), one request sent with `User-Agent: pfm/{version}`. A cached result retains its provider confirmation time. HTTP 429 responses impose a shared backoff of at least ten minutes, honoring a longer `Retry-After`, and the card reads `rate-limited — retry HH:MM` with the retry time first; other provider failures back off for sixty seconds. Cached values remain visible with the failure reason during a temporary outage. Refreshing an account never blocks the other cards. The hook writes a failed refresh to stderr.

### `pfm internal callmeter` ✚

One command registered by `pfm install` in every Claude config dir on `PreToolUse` (matcher `Bash`, for the directory a command starts in), `PostToolUse`, `PostToolUseFailure`, `SubagentStart`, `SubagentStop` (matcher `*`), `PostToolBatch` and `Stop` (no matcher), each entry `async: true`. It upserts the event's rows (calls, requests, agents) into `$HOME/.local/state/pfm/callmeter.db`, each stamped with the seat it ran from (`CLAUDE_CONFIG_DIR`, else `$HOME/.claude`) and that seat's configured account, never prunes, writes nothing to stdout and exits 0 on every path; a failure to record goes to stderr, pfm's log and a `faults` row when the store is reachable; a machine config it cannot load leaves the account NULL and is one `account` fault per run. Design: `docs/design/hooks/callmeter.md`.

### `pfm internal epic-inject` ✚

UserPromptSubmit hook. Resolves the pane's chat name each turn; on `^E_([A-Za-z0-9][A-Za-z0-9-]*)_` finds `docs/epics/{slug}/manifest.md` from cwd upward and emits it as additionalContext prefixed with marker `INJECTED EPIC {slug}/manifest.md` (visible provenance only). Once-only state is STRUCTURED: the shared store records `(session_id, slug)` — same epic never re-injects (not even on manifest edits; no content hashes), a rename into a different epic injects that one once, renaming back stays silent. No hook needed on `/rename` (per-turn re-check). Missing manifest → silent.

### `pfm internal reload-intercept` ✚

UserPromptSubmit hook. A prompt that is exactly `/reload` or starts `/reload` is split with a quote-aware word splitter and run through the `chat reload` front IN-PROCESS (no re-exec); the front's captured stdout+stderr — its own validation errors included — is written back to the hook's stderr prefixed `reload: `, and the hook exits 2 every time a `/reload …` prompt matched, success or failure, so it never reaches the model. An unmatched prompt or a malformed hook payload exits 0 silently. Claude only — Codex has no UserPromptSubmit hook, so a Codex seat's `/reload` still goes through the model and the `/reload` command body.

### `pfm internal exit-intercept` ✚

UserPromptSubmit hook. A prompt that is exactly `e` or `/e` runs the `chat kill` front IN-PROCESS as `--self --exit`; the front's captured stdout+stderr is written back to the hook's stderr prefixed `exit: `, and the hook exits 2 on a match, success or failure, so the prompt never reaches the model. Any other prompt — `exit`, `E`, `e` with trailing text, a normal sentence — or a malformed payload exits 0 and passes through untouched. Claude only; a Codex seat's `e` still goes through the model. The close itself sends `/exit` to the pane, so the TERMINAL is closed by `exit-close` below: one closer, two entry points, which is why the two spellings cannot drift.

### `pfm internal exit-close` ✚

SessionEnd hook. Closes the terminal a chat was being WATCHED through when the human ends it with `/exit`. The chat pane needs no help — a fleet seat is exec'd, so the engine is the pane root and the pane dies with it; what survives is the shell that SPAWNED `tmux attach` as a child (a VS Code tab's zsh), which returns to a prompt that is the tab left open. The hook reads the chat socket from `$TMUX`, lists the server's clients, and hangs up each client's PARENT when that parent is a terminal shell. Acts ONLY on reason `prompt_input_exit`: `clear` must fall through, because that chat keeps running. Guards, each reporting by name rather than as absence — a parent at pid <= 1, a parent that is not a shell, the hook's own pid, and a socket whose basename is not a fleet socket (the gate that keeps the hook off a human's own tmux, since the same settings file arms it for every Claude session on the host). Fail-open on every path: a hook that can refuse a session's exit is worse than a terminal left open.

## `pfm chat` family

| Verb | Status | Does |
| ------------------------------------------------------ | ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `new` | ● | Spawn a chat (engine/account per picker or flags; `--engine` defaults to the calling chat's own engine, else config). |
| `open` | ● | Open/attach an existing chat. |
| `read` | ● | Read transcript (full/excerpt). |
| `last` | ● | Newest entry by role. |
| `status` | ◆ | Inspect liveness/state. `--summary` adds a ≤40-word summary of the last human exchange through configured `ask`; `--engine`/`--model` override that one call. Complete exchanges cache by transcript path + last-record byte offset; the no-flag form remains token-free and unchanged. `--ask` instead answers what the chat is doing RIGHT NOW from its live pane capture, with the last exchange as background; it NEVER caches, because a pane a second later is a different pane, and it words an absent pane (`TRANSCRIPT-ONLY (chat is not live: ...)`), a failed capture (`TRANSCRIPT-ONLY (pane capture failed: ...)`) and a missing exchange (`PANE-ONLY (...)`) distinctly, so a probe that could not run never reads as an empty one. |
| `stream` | ● | Follow output live. |
| `inject` | ● | Type into a pane (signing rules unchanged). |
| `ask` | ● | Headless ask flow. |
| `watch` | ● | Watch for a condition. |
| `capture` | ● | Capture pane contents. |
| `keys` | ✚ | `pfm chat keys [--delay ms] [--literal] [--capture] <target> <key>...` — press any key in a live chat's pane (`Escape`, `Enter`, `C-c`, `Down`, `F1`, `S-Tab`, …). Unknown names are REFUSED, because tmux types an unrecognised key as text and a caller who writes `Esc` would silently put three letters in the composer; `--literal` types text on purpose. `--delay` defaults to 120ms — a TUI drops keys that arrive in the same millisecond. |
| `recover` | ● | Rollout recovery. |
| `name` | ● | Deliver/set chat name. |
| `kill` / `unkill` | ● | Kill bookkeeping. `kill` on a target with a live tmux socket+pane also runs the exit choreography (send `/exit`/`/quit`, close the pane, close viewports) — `--exit` remains the explicit form for a caller vouching for a live address the resolver itself cannot see; a target with no live address stays a store write. An unkill of a chat carrying no kill exits 1 and names it. |
| `reload` | ◆ | `[--account N] [--then prompt] [--sock socket] [--cache 1h\|5m] [--new [--hide]]` — `--hide` (needs `--new`) records a permanent kill for the conversation left behind once the reboot completes. Slash command installs as `/reload`; a human-typed `/reload …` runs via the `reload-intercept` hook without a model turn (Claude only). |
| `end` | ● | Kill the chat's tmux server. |
| `whoami` / `resolve` | ● | Identity / target resolution. |
| `modal` | ● | Send modal keys. |
| `find` / `save` / `branch` / `ls` / `history` | ◆ | Native satellite verbs except `history`, which retains its helper. `branch` creates a real detached Claude or Codex fork and names the Codex fork through Codex's rename command. |

## MCP surface

One daemon process, one server `professor`, two families. Every tool is a thin adapter over the SAME function its CLI verb calls — no second implementation, ever.

### Server `professor` — `/mcp/professor` (stdio: `pfm mcp serve --stdio`; family views `/mcp/professor/chat`, `/mcp/professor/harvester`; external: `/mcp` on the authenticated port, harvester family only)

A model sees each tool as `mcp__professor__<tool>` in Claude Code and Codex, and as `professor_<tool>` in OpenCode. The Inputs columns below are a reading aid. The server's own `tools/list` answer is the contract: where the two disagree, the table is the bug.

When one enabled family fails to configure at `pfm mcp serve --stdio`'s in-process start, the server still starts with the healthy family, lists every tool of the failed family under its normal name and answers each call with an MCP error result naming the family, the configuration error and the fix (correct that config key, then reconnect with `/mcp`), and writes one stderr line naming the family and its error; only when no enabled family configures does it exit 1, and the daemon (`pfm mcp serve`) still exits 1 on any family failure.

#### Family chat

Toggled by `mcp.servers.chat.enabled`; `servicedesk` belongs to this family and follows its toggle.

| Tool | Status | Inputs | Returns |
| --------------------------- | ------ | --------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `chat_ls` | ● | `{all?, killed?, project?, limit?}` | Fleet rows (accounts, engines, sizes, liveness), including chats still booting. `project` filters case-insensitively on project label or directory; rows are capped (200 default, 1000 max) with `matched`/`truncated` reporting the cut. Without `all`, rows are scoped to the caller's repository (a linked worktree counts as its repo) when `_meta` resolves the caller; `scope` names the repo or `all repos — caller cwd unknown`, `elsewhere` counts the rows left out. `all` also adds killed and background rows, unlike `pfm chat ls --all`, which stays on live chats. |
| `chat_new` | ✚ | `{name, prompt?, engine?, account?, model?, effort?, cwd?, 1h?, attach?, await?, progress?, settle?, timeout?}` | Spawned chat identity. |
| `chat_open` | ✚ | `{target}` | Open result. |
| `chat_read` | ◆ | `{source, last_n?, max_bytes?}` | Transcript text (converges onto CLI `read`). |
| `chat_last` | ✚ | `{target}` | Newest entry. |
| `chat_status` | ✚ | `{target, summary?, ask?, engine?, model?}` | Liveness/state. `summary` recaps the last exchange (cached); `ask` reports current state from the live pane (never cached). |
| `chat_inject` | ● | `{target, message, then?, force_now?}` | Delivery report (signing rules apply). |
| `chat_capture` | ● | `{target, tail_lines?, max_bytes?}` | Pane text. |
| `chat_keys` | ✚ | `{target, keys: [string], literal?, delay_ms?, capture?}` | Press keys in the chat's pane — the model's hands on a TUI that swallowed a keystroke. Same validation as the CLI verb: an unknown key name is an error, never typed as text. |
| `chat_name` | ✚ | `{target, name}` | Rename result. |
| `chat_kill` / `chat_unkill` | ✚ | `{target, exit?}` / `{target}` | Kill-state result. A live target is really ended (its pane is closed by the exit finisher); the message names which happened — pane closed, or de-listed only. |
| `chat_find` | ◆ | `{excerpt, limit?, include_self?}` | Matching transcripts (converges onto CLI `find`). |
| `chat_save` | ✚ | `{target, transcript?}` | Appends a transcript + environment snapshot to a FILE — `target` is a path, never a chat, and a bare word is refused. |
| `chat_whoami` | ● | `{}` | Caller identity. |
| `chat_resolve` | ● | `{kind, name}` | Resolved target. |
| `servicedesk` | ✚ | `{title, detail, severity?, area?}` | Files a durable complaint about Professor itself for a human to triage later. Reporter identity is CAPTURED the same way `chat_inject` captures a sender, never accepted as tool input, so a model can complain but never forge who is complaining; a reporter that cannot be derived stores the `UNIDENTIFIED` sentinel rather than a blank column. Read the ledger back with `pfm issues`. |

Deliberately NOT exposed: `end`, `modal`, `watch`, `stream`, `recover`, and `history` — interactive/plumbing; the absence is stated in the server docstring.

#### Family harvester

Toggled by `harvester.enabled`; the only family the external `/mcp` serves. Settings come from `harvester.config.json`. `harvester_search_web` trusts exactly the configured `search.searxngURL` origin (a loopback/LAN SearXNG is the normal deployment; redirects are refused); every fetch keeps the SSRF guard. Caller `headers` go only to the target's own origin, never to a reader service, Wayback or a resolver (validation and cache partition: `pfm/internal/harvest/caller_headers.go`). A failed search names each backend's own error.

| Tool | Status | Inputs | Returns |
| ------------- | ------ | -------------------- | ----------------------- |
| `harvester_read` | ● | `{urls?: [string], files?: [string], publications?: [string], refresh?, include_content?, ocr_language?, headers?}` | Web pages (`urls`), local documents (`files`, local server only: the remote schema has no such field) and works (`publications`: DOI, arXiv, PMID, PMCID, ISBN, landing URL, handle) as Markdown in one call (cache + TTL); at least one item, at most 50, of which at most 20 publications. Typed items grouped as the input (`source`, `kind`, `title`, `via`, `status`, `gaps`, `cached`, `chars`, `path`, `content`, `error`; publications add `ids`); a misplaced item is a per-item error naming the right field. |
| `harvester_download_file` | ● | `{urls: [string], headers?}` | Files as bytes, unparsed: kind, type, size, sha256, via. Local: `path`, the absolute path of the stored file. Remote: `id` (the sha256), a signed `url` to `curl -fL -o` and `expires` (10 min; a server restart invalidates every url), plus a `resource_link` for `resources/read`; the url is served by `GET|HEAD /files/{sha256}` on the external port, outside the bearer (the HMAC signature is the access control). |
| `harvester_search_literature` | ● | `{query, limit?, type?}` | Scholarly candidates (each with its `type`), each with a `handle` for `harvester_read`'s `publications`, and `sources`: each discovery source's status (answered, partial, failed) with its error, so a failed source never reads as nothing found. |
| `harvester_search_web` | ● | `{query, limit?, lang?, engines?}` | Web search results; served only when SearXNG or Brave is configured. |

## Shared engine `internal/ask`

Content-agnostic one-shot runner behind `pfm harvest ask` — nothing harvester-specific in its interface. **Two engines, selected in config** (`ask.engine`): `codex` (default `gpt-5.6-luna` @ `low`) and `claude` (headless `claude -p`, default `claude-haiku-4-5` @ `low`); per-call override via the CLI's `--engine`, `--model`, and `--effort` flags. The Codex runner uses `codex exec`; both binaries resolve through `internal/deps`, and each process uses the first account home from its configured engine roster. The default timeout is 60 seconds.

```
Engine.Run(ctx, AskInput) (AskResult, error)
  AskInput  { ContentFiles []string; SourceLabels []string; Prompt string;
              Engine, Model, Effort string /* "" → config ask.* */ }
  AskResult { Answer string; Evidence []Evidence; PerFile []FileStatus;
              Usage *TokenUsage; Duration time.Duration }
  Evidence  { File string; Label string; Span SourceSpan /* lines|turns|chunk */; Quote string }
```

The fixed harness prompt requires an `EVIDENCE` section with file numbers, locations, and short quotes. Current process adapters return that section inside `Answer`; they populate `Usage` only when the child emits recognized token counters, and leave `Evidence`/`PerFile` empty. A missing usage line leaves `Usage` nil.

The harvester owns source discovery, keeps each original input in `SourceLabels`, and passes full cache-file paths to the one model process. It does not substitute clipped terminal previews or run a hidden map-reduce pass. Failed sources become temporary JSON receipt files so the same model pass can name unavailable evidence explicitly.
