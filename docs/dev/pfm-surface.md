# pfm — CLI & MCP Surface

Current operator and integration surface. Legend: **●** established · **◆** enhanced · **✚** added.

## Contents

- [Global](#global)
- [Top-level commands](#top-level-commands)
- [`pfm chat` family](#pfm-chat-family)
- [MCP surface](#mcp-surface)
- [Shared engine `internal/ask`](#shared-engine-internalask)

## Global

- Config: `~/.config/pfm/pfm.config.json` (v2, strict JSON — accounts/emoji/theme/permission posture/mcp/ask; `$XDG_CONFIG_HOME` honored). A machine that still has only the pre-split `config.json` keeps reading it until `pfm install` migrates it (rename, harvester keys moved out, loopback port 8377 → 18377). `claude.compactNudge` `{enabled, start, step}` (global or per-account, defaults on/35/10) governs the milestone self-compact reminder hook; `claude.systemPrompt` (global or per-account): `production` (default — the CLI's own prompt), `lean` (CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1), `professor` (inject the staged `harness-prompts/claude.md` via `--system-prompt-file` on every managed launch; hygiene re-strips any inherited arm each spawn). `claude.maxSubagentSpawnDepth` and `claude.maxConcurrentSubagents` (global or per-account, positive integers) lift Claude Code's sub-agent ceilings: every managed launch carries `CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH` (default 8, over the harness's own 3), and `CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS` only when a value is set — unset leaves the harness's 20. Missing file = defaults. **Malformed file = hard failure for every command** except the three diagnostics (`doctor`, `config show`, `config validate`), which run on defaults and print the exact error. `config show` redacts secrets.
- Harness prompts: `pfm install` composes ONE system prompt per engine at stage time — never at launch time. Shared head (`pfm/harness-prompts/share/head.md`) + engine middle (`claude/`, `codex/`, `opencode/professor.md`) + shared tail (`share/tail.md`), each part's trailing newlines trimmed and the three joined by a single blank line. The three land under `~/.local/share/pfm/install/harness-prompts/`: `claude.md` is the `--system-prompt-file` every managed Claude launch carries when `claude.systemPrompt` is `professor`; `codex.md` is what the Codex SessionStart `additionalContext` hook emits — Codex takes only an appendix to its own prompt, so the composed file IS the appendix; `opencode.md` is named in the `instructions` array of `~/.config/opencode/opencode.jsonc`, the files OpenCode reads into its system prompt, with every other key and every operator entry preserved. The parts themselves and the Claude drift baselines (`harness-prompts/claude/baselines/`) stage beside them. The tree exists ONCE, at `pfm/harness-prompts/`, embedded into the binary at build time by the package beside it (`//go:embed`); there is no second copy to keep in step.
- Harvester config: `~/.config/pfm/harvester.config.json`, beside `pfm.config.json` — the ONLY source of Harvester settings (`enabled`, `external`, `search`, `scholarly`, `fetch`, `convert`, `cache`, `output`); the harvest packages read no process environment and the daemon units carry none. Validated at load (bad URL, negative TTL, external without `publicURL` + auth = load error with the key); a file holding a secret must be `0600`. Every retired `HARVESTER_*` / `SEARXNG_URL` / `BRAVE_API_KEY` variable still exported is a `pfm doctor` warning naming its key.
- One binary, one dispatch. Everything below is `pfm <command> …`.

## Top-level commands

| Cmd | Usage | Does |
| --------------- | -------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| ◆ `ls` | `pfm ls [--plain\|--tsv]` | Fleet picker TUI. `Tab`/`Shift-Tab` switch Chats↔Stats↔Limits; `←/→` moves through the labeled chat-row carousel (`▶ open → ⚡ reboot → 🕐 1h → ✖ hide → ⏸ deactive`, `Enter` runs the boxed action). Hide changes visibility only; deactive stops the tmux server and leaves the chat resumable. Ctrl shortcuts stay as aliases. |
| ◆ `doctor` | `pfm doctor [--verbose]` | Existing health + config provenance, dependency resolve/version/minimum/self-doctor rows, installer-owned Claude/Codex hook presence/path/ledger rows, pre-push gate state, harvest-cache, MCP daemon state, the harness-prompt drift check (re-captures the live CLI's system prompt through a zero-token localhost sink, masks the billing header's `cc_version` build stamp, and hashes it against the staged `harness-prompts/claude/baselines/harness-original.sha256` baseline; match, DRIFT, and CHECK-FAILED are three distinct lines — a failed capture never reads as either verdict), the harness-prompt embed check (hashes the tree this binary embeds against `pfm/harness-prompts/` in the recorded blueprint clone; `embed=ok`, `embed=MISMATCH` naming every differing file with its `(only-in-clone)` / `(only-in-binary)` mark and stating both directions — a hash difference carries no chronology, so a clone holding the newer prompts means rebuild pfm then `pfm install`, while a clone checked out older than the binary means the binary is the newer one and nothing needs rebuilding — and `embed=CHECK FAILED` for a clone that could not be read are three distinct lines, and a clone nobody could read never counts as ok), and the spawn audit (enumerates live `cc-*` panes through the fleet's own tmux probe, resolves each pane's Claude process from `/proc`, and classifies it INJECTED / PREDATES-LAYER / VIOLATION against the configured `claude.systemPrompt`; a `production` policy, an empty fleet, and an audit that could not run are three distinct lines, and an unreadable seat is counted as unaudited, never as clean). Required dependency or hook missing/broken/drift and an existing-but-unwired pre-push gate exit nonzero; `--verbose` writes bounded raw dependency output under `tmp/`. |
| ◆ `install` | `pfm install [--yes]` | Dry-run by default. Reconciles the active pfm hooks, retires automatic Dream/STM injection hooks, applies `cleanupPeriodDays: 36500`, compiles global Codex agents, reconciles marker-owned global Claude-command → Codex mirrors, wires MCP units + clients when enabled, and fans out account config. MCP-backed `chat-*` commands keep prompt cards but no duplicate global skills; `chat-interrogate` remains a skill because it has no MCP tool. |
| ● `uninstall` | `pfm uninstall` | Removes exactly the installer-owned surface (ownership ledger); manual hooks survive. |
| ◆ `chat new` | (see the chat family) | On a proof timeout the launch now presses `Escape` then `Enter` once and RE-PROVES against the engine's transcript before reporting. A rescued launch exits 0 and says what it pressed; an unrescued one still exits `codeUndelivered` and says the retry was tried. |
| ✚ `update` | `pfm update [--to vX.Y.Z] [--repo <path>]` | Ownership-aware, transactional whole-professor update: fetch tags → highest SEMVER tag (parsed, not lexical) → refuse dirty worktree → build twice + hash gate → stage → atomically replace ONLY ledger-owned pfm copies (previous binary preserved for rollback) → `install --yes` → `doctor` → finalize. Unowned PATH copies untouched (drift surfaced loudly instead). Failure → rollback or an exact residue report. Never pushes. |
| ✚ `init` | `pfm init [dir] [--force]` | Per-project scaffold: copies blueprint set (`CLAUDE.md`, `AGENTS.md`, `.claude/{settings,commands,agents,skills}`) with placeholders intact; refuses existing `.claude/` without `--force`; prints the "open Claude, run SETUP" handoff. |
| ✚ `config` | `pfm config init [--force] \| show \| validate` | `init` writes the default v2 file as strict comment-free JSON (0600, atomic; field docs print to stdout). `show` prints resolved config with `(default)/(file)` provenance, secrets redacted. `validate` = load + exact decode error with position. |
| ● `codex` | `pfm codex build\|check\|agents [repo-root]` | Compiler mirror, marker check, global agents md→toml. `agents` also auto-runs inside install. |
| ◆ `harvest` | `pfm harvest [--refresh] [--size-only] [--json] [--header 'Name: value']... <url\|doi\|path>...` | Multi-source fetch → markdown, cache + 24h TTL (`cache.ttlSeconds` in `harvester.config.json`). Stdout clips large sources; full docs live at the printed `path:`. |
| ◆ `harvest download` | `pfm harvest download [--json] [--header 'Name: value']... <url>...` | Downloads 1–50 files as bytes, unparsed; prints `path / kind / content_type / bytes` per item. A failed item is `ERROR: …` and exit 1; a refused header exits 2 before any request. |
| ◆ `harvest ask` | `pfm harvest ask -p "<prompt>" [--engine claude\|codex] [--model MODEL] [--effort EFFORT] [--refresh] <url\|doi\|path>...` | Harvests 1–50 sources into full cache files, preserves failed sources as labeled temporary receipts, then runs one configured `internal/ask` engine pass. `pfm harvest --ask -p "<prompt>" ...` is an equivalent compatibility spelling. The answer is stdout; available token usage is a named stderr receipt. Engine/model/effort default from machine config and explicit flags win. |
| ◆ `mcp` | `pfm mcp serve` · `pfm mcp chat\|harvester serve` | `serve` = ONE daemon process, two ports. Loopback `127.0.0.1:<mcp.http.port>` (default 18377) — header-free, unauthenticated, both servers (`/mcp/chat`, `/mcp/harvester`), never generates or distributes local credentials. External `external.host:external.port` (default 18378, off by default, `harvester.config.json external.*`) — the harvester ONLY at `/mcp`, behind a mandatory passphrase-OAuth and/or static-bearer wall, local reads confined to the cache root; a failed external bind leaves the loopback port serving and reports on `/status`. Single-instance guarded; `GET /status` returns `{pfmVersion, protocolVersion, servers, pid, startTime, endpoint, harvesterExternal}` (doctor consumes it). Per-name stdio remains for clients without HTTP MCP (`pfm mcp harvester serve` is stdio-only; its retired HTTP flags refuse with the config key that replaced them). |
| ● `statusline` | (stdin JSON from Claude Code) | + `7d-fable` segment and `✦` Fable symbol; account badge from config (unknown → no badge, never 🥇). |
| ● `usage-hook` | (UserPromptSubmit hook) | OAuth usage fetch; see [usage-hook](#usage-hook) for shared cache and retry behavior. |
| ● `heal` | `pfm heal` | Codex projection heal. A wedged/midline cursor whose rollout is not canonically ordinalled is reported NONCANONICAL, and one whose rollout could not be read end to end is UNSCANNED — neither is ever deleted, since a rebuild from zero fails on the same record until Codex >= 0.154.0 projects past it. |
| ● `reap` | `pfm reap …` | Unchanged. |
| ✚ `issues` | `pfm issues [--all] [--json]` | Reads the servicedesk complaint ledger that agents file through `issue_servicedesk`. Default view is open issues only; `--all` includes closed ones. The three states stay visibly distinct: an empty ledger prints `no open issues` and exits 0, a store that could not be read prints the cause on stderr and exits 1 (a failed look is never rendered as an empty one), and `--json` always emits an array so a script reading structured output gets `[]` rather than a prose sentence. |
| ✚ `callmeter` | `pfm callmeter report {files\|writes\|commands\|context\|sequences\|faults} [--since D] [--project P] [--agent-type T] [--session S] [--config-dir DIR] [--limit N]` | Reads the call store `$HOME/.local/state/pfm/callmeter.db` that `pfm internal callmeter` writes. `report` prunes rows older than the 30-day window, parses pending Bash commands, then prints one fixed-width table with a note line per named gap; chat names come from pfm's transcript index (`?` and one note when it cannot be read). An absent store prints `callmeter: no store at {path}: nothing recorded yet` and exits 0 without creating it; a store that cannot be opened prints `callmeter: cannot open store {path}: {error}` and exits 1. It covers every Claude config dir the machine config names; `--config-dir` narrows to one and an unconfigured dir is a usage error naming them. A `--since` older than the window is clamped with one stderr note. |
| ● `run` | `pfm run …` | Chat-spawn plumbing (backs `chat new`). Unchanged. |
| ● `agent` | `pfm agent open …` | Headless claude open path. Unchanged. |
| ◆ `internal` | `pfm internal callmeter\|clear-kill\|explore-deny\|rr-dir\|epic-inject\|reload-intercept\|compact-nudge\|exit-intercept\|exit-close` | Hook backends: ✚ `callmeter` (async on six events, records every Claude tool call into the call store `pfm callmeter` reads; section below), ✚ `explore-deny` (ports the shell script; stdin hook JSON → allow/deny), ✚ `rr-dir` (SubagentStart, matcher `rr\|super-rr`; stdin hook JSON `cwd` → one `additionalContext` line: `RR-DIR: {nearest ancestor}/.professor/RR`, the same with `(fallback: …)` for the clone's own ledger, or `RR-DIR-ERROR: {reason}`; exit 0 on every path), ✚ `epic-inject` (see below), ✚ `reload-intercept` (see below), ✚ `compact-nudge` (see below), ✚ `exit-intercept` (see below), ✚ `exit-close` (see below). |
| ● `version` | `pfm version` | Verify identity by hash, not this string. |

### usage-hook

The hook and the `ls` Limits tab share the account cache in `usagehook.DefaultCacheDir`. The hook uses its 180-second default TTL (`CC_USAGE_TTL`); the focused Limits tab uses five-second freshness and polls completed account results every two seconds. Accounts refresh independently, and pending requests are canceled when Limits loses focus or the picker closes. The display labels percentages as quota **used** and advances confirmation ages and reset countdowns every five seconds, including under `--no-sky`.

A cached result retains its provider confirmation time. HTTP 429 responses impose a shared backoff of at least ten minutes, honoring a longer `Retry-After`; other provider failures back off for sixty seconds. Cached values remain visible with the failure reason during a temporary outage. Refreshing an account never blocks the other cards.

### `pfm internal callmeter` ✚

One command registered by `pfm install` in every Claude config dir on `PreToolUse` (matcher `Bash`, for the directory a command starts in), `PostToolUse`, `PostToolUseFailure`, `SubagentStart`, `SubagentStop` (matcher `*`), `PostToolBatch` and `Stop` (no matcher), each entry `async: true`. It upserts the event's rows (calls, requests, agents) into `$HOME/.local/state/pfm/callmeter.db`, never prunes, writes nothing to stdout and exits 0 on every path; a failure to record goes to stderr, pfm's log and a `faults` row when the store is reachable. Design: `docs/design/hooks/callmeter.md`.

### `pfm internal epic-inject` ✚

UserPromptSubmit hook. Resolves the pane's chat name each turn; on `^E_([A-Za-z0-9][A-Za-z0-9-]*)_` finds `docs/epics/{slug}/manifest.md` from cwd upward and emits it as additionalContext prefixed with marker `INJECTED EPIC {slug}/manifest.md` (visible provenance only). Once-only state is STRUCTURED: the shared store records `(session_id, slug)` — same epic never re-injects (not even on manifest edits; no content hashes), a rename into a different epic injects that one once, renaming back stays silent. No hook needed on `/rename` (per-turn re-check). Missing manifest → silent.

### `pfm internal compact-nudge` ✚

UserPromptSubmit hook, Claude only, main chat only. Reads the context used-percentage the statusline persisted for this session (`$PFM_SID_DIR/nudge-ctx-{session}`, written from Claude Code's own `context_window.used_percentage` on every render — the hook never re-derives a context window) and, when it has climbed into a new band of the ladder `start, start+step, …`, emits ONE additionalContext reminder: the milestone is here, write durable state, `chat_self_compact` with one focus line and ONE steer — explicitly "not an order". One reminder per band per climb (`nudge-band-{session}` holds the last band spoken); a compaction that drops the percentage re-arms the ladder. Payloads carrying `agent_id`/`agent_type` (a sub-agent's turn) are skipped. Policy from `claude.compactNudge {enabled, start, step}` (defaults on/35/10, per-account override). No sample yet, or a disabled policy → silent on stdout, the cause on stderr.

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
| `kill` / `unkill` | ● | Kill bookkeeping. `kill` on a target with a live tmux socket+pane also runs the exit choreography (send `/exit`/`/quit`, close the pane, close viewports) — `--exit` remains the explicit form for a caller vouching for a live address the resolver itself cannot see; a target with no live address stays a store write. |
| `reload` | ◆ | `[account] [--then prompt] [--sock socket] [--1h on\|off] [--new [--hide]]` — `--hide` (needs `--new`) records a permanent kill for the conversation left behind once the reboot completes. Slash command installs as `/reload`; a human-typed `/reload …` runs via the `reload-intercept` hook without a model turn (Claude only). The legacy `swap` and `--fresh` names are retired — dispatch refuses each by name and points at its replacement. |
| `end` | ● | Kill the chat's tmux server. |
| `whoami` / `resolve` | ● | Identity / target resolution. |
| `modal` | ● | Send modal keys. |
| `find` / `save` / `branch` / `ls` / `history` | ◆ | Native satellite verbs except `history`, which retains its helper. `branch` creates a real detached Claude or Codex fork and names the Codex fork through Codex's rename command. |

## MCP surface

One daemon process (Task #8), two servers. Every tool is a thin adapter over the SAME function its CLI verb calls — no second implementation, ever.

### Server `chat` — `/mcp/chat` (stdio: `pfm mcp chat serve`)

The Inputs columns below are a reading aid. The servers' own `tools/list` answer is the contract: where the two disagree, the table is the bug.

| Tool | Status | Inputs | Returns |
| --------------------------- | ------ | --------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `chat_ls` | ● | `{all?, killed?, project?, limit?}` | Fleet rows (accounts, engines, sizes, liveness), including chats still booting. `project` filters case-insensitively on project label or directory; rows are capped (200 default, 1000 max) with `matched`/`truncated` reporting the cut. |
| `chat_new` | ✚ | `{name, prompt?, engine?, account?, model?, effort?, cwd?, 1h?, attach?, await?, progress?, settle?, timeout?}` | Spawned chat identity. |
| `chat_open` | ✚ | `{target}` | Open result. |
| `chat_read` | ◆ | `{source, last_n?, max_bytes?}` | Transcript text (converges onto CLI `read`). |
| `chat_last` | ✚ | `{target}` | Newest entry. |
| `chat_status` | ✚ | `{target, summary?, ask?, engine?, model?}` | Liveness/state. `summary` recaps the last exchange (cached); `ask` reports current state from the live pane (never cached). |
| `chat_inject` | ● | `{target, message, then?, force_now?}` | Delivery report (signing rules apply). |
| `chat_self_compact` | ✚ | `{focus, then}` | Request-scoped `/compact <focus>`, scheduled after the caller's active turn settles; exactly ONE post-compact steer (a string, never a list) is mandatory. The only answer to "compact yourself"; the session survives. |
| `chat_capture` | ● | `{target, tail_lines?, max_bytes?}` | Pane text. |
| `chat_keys` | ✚ | `{target, keys: [string], literal?, delay_ms?, capture?}` | Press keys in the chat's pane — the model's hands on a TUI that swallowed a keystroke. Same validation as the CLI verb: an unknown key name is an error, never typed as text. |
| `chat_name` | ✚ | `{target, name}` | Rename result. |
| `chat_kill` / `chat_unkill` | ✚ | `{target, exit?}` / `{target}` | Kill-state result. A live target is really ended (its pane is closed by the exit finisher); the message names which happened — pane closed, or de-listed only. |
| `chat_find` | ◆ | `{excerpt, limit?, include_self?}` | Matching transcripts (converges onto CLI `find`). |
| `chat_save` | ✚ | `{target, transcript?}` | Appends a transcript + environment snapshot to a FILE — `target` is a path, never a chat, and a bare word is refused. |
| `chat_whoami` | ● | `{}` | Caller identity. |
| `chat_resolve` | ● | `{kind, name}` | Resolved target. |
| `issue_servicedesk` | ✚ | `{title, detail, severity?, area?}` | Files a durable complaint about Professor itself for a human to triage later. Reporter identity is CAPTURED the same way `chat_inject` captures a sender, never accepted as tool input, so a model can complain but never forge who is complaining; a reporter that cannot be derived stores the `UNIDENTIFIED` sentinel rather than a blank column. Read the ledger back with `pfm issues`. |

Deliberately NOT exposed: `end`, `modal`, `watch`, `stream`, `recover`, and `history` — interactive/plumbing; the absence is stated in the server docstring.

### Server `harvester` — `/mcp/harvester` (stdio: `pfm mcp harvester serve`; external: `/mcp` on the authenticated port)

Settings come from `harvester.config.json`. `webSearch` trusts exactly the configured `search.searxngURL` origin (a loopback/LAN SearXNG is the normal deployment; redirects are refused); every fetch keeps the SSRF guard. Caller `headers` go only to the target's own origin, never to a reader service, Wayback or a resolver (validation and cache partition: `pfm/internal/harvest/caller_headers.go`). A failed search names each backend's own error.

| Tool | Status | Inputs | Returns |
| ------------- | ------ | -------------------- | ----------------------- |
| `readPage` | ● | `{sources: [string], refresh?, size_only?, headers?}` | Web pages as Markdown (cache + TTL); typed items. |
| `parseLocalDocuments` | ● | `{paths: [string], size_only?}` | Local documents as Markdown; local server only. |
| `download` | ● | `{sources: [string], headers?}` | Files as bytes: path (local) or `resource_link` (remote), kind, type, size, sha256. |
| `findWorks` | ● | `{query, limit?, kind?}` | Scholarly candidates, each with a `handle` for `readWork`, and `sources`: each discovery source's status (answered, partial, failed) with its error, so a failed source never reads as nothing found. |
| `readWork` | ● | `{works: [string], refresh?, size_only?, headers?}` | Works (DOI, arXiv, PMID, PMCID, ISBN, landing URL, handle) as Markdown, with `ids` and `route`. |
| `webSearch` | ● | `{query, count?, lang?, engines?}` | Web search results; served only when SearXNG or Brave is configured. |

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
