# pfmd — one long-running daemon behind every pfm surface

**Status:** DESIGN — decisions below are the owner's and are settled; phases 1-5 are the delivery order. Every citation is `pfm/`-relative (module `github.com/rezzminator/professor/pfm`).

## Contents

- [Problem](#problem)
- [Decisions](#decisions)
- [Architecture](#architecture)
- [API](#api)
- [Process model](#process-model)
- [Failure modes and fallbacks](#failure-modes-and-fallbacks)
- [Security](#security)
- [Migration](#migration)
- [Open questions](#open-questions)
- [What the operator learns](#what-the-operator-learns)

## Problem

**Three uncoordinated processes call the same provider endpoint.** `internal/usagehook/hook.go:181` (`Evaluate`) fetches `https://api.anthropic.com/api/oauth/usage` inline on the `UserPromptSubmit` path — the installer wires it as `pfm usage-hook` at `internal/installer/expected_hooks.go:82`, so every prompt in every chat can become an HTTP call with a 6-second client timeout (`hook.go:304`). `internal/stats/limits.go` runs a second fetcher for the picker's Limits tab, with its own TTL ladder: `defaultLimitsTTL` 3 min (`limits.go:100`), `LiveLimitsTTL` 60 s (`limits.go:110`), `CodexLiveLimitsTTL` 90 s (`limits.go:120`), selected at `cmd/pfm/commands.go:147-151`. `internal/statusline` runs a third path — `SpawnDetached` (`internal/statusline/process.go:23`) forks `pfm statusline --refresh-gpt` behind a lockfile (`render.go:962`, `armRefresh`) to drive `codex app-server`.

They coordinate only through a file: `usagehook.DefaultCacheDir()` = `os.TempDir()/cc-usage-<uid>` (`hook.go:359,369`), one `acct-N.json` per account (`hook.go:388`). The sampler has a single-flight map (`limits.go:665-677`) but it is **in-memory and per-process** — it does not stop a second `pfm ls` from fetching, and it does not see the hook at all.

**The coordination is already known to be insufficient.** `limits.go:107-109` records the incident in the code: *"at 5s both accounts were 429'd (2026-09-11 — a 429 nine seconds after a successful fetch, escalating to a server-sent 1h Retry-After)"*. The sampler answered by writing a shared backoff record into that cache file (`limits.go:408-413`, minimum ten minutes at `limits.go:465`) and raising its TTL to 60 s. The hook never learned the lesson: `refresh` at `hook.go:525-554` returns a bare `fmt.Errorf("usage endpoint returned %s")` on any non-2xx and writes **no** `CacheBackoff`. It *honors* a peer's backoff (`hook.go:203`) but never records its own — so a 429 that a hook fetch earns is invisible, and the next prompt past the 180-second TTL (`hook.go:306`, `CC_USAGE_TTL`) fetches again into the same wall.

**A free authoritative sample is thrown away.** Claude Code hands the statusline its own `rate_limits` payload on stdin (`internal/statusline/render.go:72,94`). `windowsAt` (`render.go:120-160`) reads it, merges a Fable window, and — when the harness omitted the `limits` array entirely — falls back to `usagehook.CachedFableWindow` (`render.go:146`). Nothing writes the harness's numbers **back** into the shared cache. A payload that cost zero requests is rendered once and discarded, while three fetchers spend request budget re-deriving it.

**The store is opened per process, and the MCP layer multiplies those processes.** `internal/mcpserv/backend.go:58,62` opens both `store.Open` and `shared.Open` for every chat MCP server; `cmd/pfm/main.go:191-206` starts one such server per Claude chat over stdio. Each handle is `SetMaxOpenConns(1)` with WAL and `busy_timeout=10000` (`internal/store/store.go:122,162,170`; same in `internal/shared/shared.go:136,152`), so N chats means N single-connection writers queueing on one file.

**Upgrading is a kill.** `Makefile:10-17` states the problem in its own comment — replacing `~/.local/bin/pfm` leaves the running process on the deleted inode, *"and nothing in between says a word."* The remedy at `Makefile:110-112` is `pkill -f 'pfm mcp serve'` whenever the `pfm-mcp.service` unit is absent. A live `pfm mcp serve` (a 5-day-old inode, pid 20576 at the time of writing) is killed mid-request, and every chat MCP client sees its server vanish.

## Decisions

1. **One daemon, `pfmd`, is the same binary.** `pfm daemon run` — the `dockerd`/`docker` shape. It owns: the limits poller (the **only** process that calls the Anthropic usage endpoint), the sqlite store (single writer), fleet/tmux operations, and the comms/cosmos event stream.
2. **Transport is HTTP/1.1 over a unix socket** at `${XDG_STATE_HOME:-~/.local/state}/pfm/run/pfmd.sock`, mode `0600`. Go `net.Listen("unix", …)` + `net/http`; clients use `http.Transport` with a `DialContext` onto the socket. Versioned under `/v1`. **State, not share**: `paths.Resolve` already puts `fleet.db` at `~/.local/state/pfm` (`internal/paths/paths.go:163`), so the runtime socket lives beside it; `~/.local/share/pfm` is the *install* tree (`internal/installer/expected_hooks.go:124`). `PFM_RUN_DIR` overrides the directory, as every other path does through `paths.EnvOr` (`paths.go:96`).
3. **Every existing surface becomes a client.** The TUI subscribes to SSE instead of ticking; the prompt hook does one `GET` with a 200 ms timeout; the statusline reads and *writes back*; `pfm mcp serve` and `pfm mcp chat serve` become thin stdio↔socket JSON-RPC proxies, so `~/.claude.json` and the Codex config are untouched (`internal/installer/mcp.go:117`).
4. **No socket, no failure.** Clients degrade to today's behaviour — shared cache file, direct store. A hook or statusline must never block or fail a prompt. First client that finds no socket spawns `pfm daemon run` detached, the way `statusline.SpawnDetached` (`internal/statusline/process.go:23`) already spawns its refresher behind a lockfile. `pfm daemon install` optionally writes a launchd agent (macOS) or a systemd `--user` unit (devbox, Ubuntu 24.04) with `KeepAlive` / `Restart=always`.
5. **The harvester stays its own service.** It is not absorbed into pfmd's core; pfmd health-checks it and reports it in `pfm doctor`. **Correction to the brief:** the harvester is no longer an external sibling at `127.0.0.1:8377`. `internal/harvestmcp` is compiled into pfm, mounted at `/mcp/harvester` by `cmd/pfm/mcp_serve_command.go:176-185`, and `8377` is `legacyDefaultMCPPort` (`internal/config/harvester.go:30`) — the live loopback port is `18377` (`harvester.go:27`). Its authenticated external gateway runs on its own listener inside the same process (`mcp_serve_command.go:223-260`). pfmd health-checks **that route**, and the Python worker environment behind it (`internal/harvestpy`), and never takes ownership of either.
6. **Upgrade is a handoff, not a kill.** The new pfmd inherits the listening socket fd from the old one; the old drains in-flight requests and exits. `pfm update` and `make install` restart through this path instead of `pkill`. Clients reconnect with backoff; SSE clients resubscribe with a cursor.
7. **Five phases, each behind its own gate, each revertible alone.** Skeleton → limits → store → MCP proxies → handoff.

## Architecture

```
                         ┌──────────────────────── pfmd (pfm daemon run) ────────────────────────┐
  Claude Code prompt     │                                                                        │
  hook  ──GET 200ms──────▶  /v1/limits            ┌──────────────┐                                │
                         │                        │ limits svc   │──┐  ONE fetcher                │
  statusline ──PUT────────▶ /v1/limits/{n}/observed│ TTL 60s      │  ├──▶ api.anthropic.com/…/usage│
             ──GET────────▶ /v1/limits/{n}        │ backoff ≥10m │  └──▶ codex app-server (90s)   │
                         │                        │ singleflight │                                │
  pfm ls (TUI) ──SSE─────▶  /v1/events?cursor=    └──────┬───────┘                                │
                         │        ▲                      │ writes                                 │
  pfm mcp chat serve ────▶  /v1/rpc/chat                 ▼                                        │
    (stdio proxy,        │        │              ┌──────────────────┐   shared cache file          │
     X-PFM-Caller)       │        │              │ acct-N.json      │◀── still written, for        │
                         │        │              │ (compat mirror)  │    socket-absent fallback    │
  pfm mcp serve ─────────▶  /v1/rpc/harvester    └──────────────────┘                             │
    (loopback 18377,     │        │                                                                │
     chat + harvester)   │        │              ┌──────────────┐  ┌──────────────┐               │
                         │        └──────────────│ event bus    │◀─│ store svc    │ SINGLE WRITER │
  pfm doctor ────────────▶  /v1/health           │ SSE + cursor │  │ fleet.db     │               │
                         │                        └──────────────┘  │ ~/.cc/fleet.db│              │
                         │                                          └───────┬──────┘               │
                         │                        ┌──────────────┐          │                      │
                         │                        │ fleet/tmux   │◀─────────┘                      │
                         │                        └──────────────┘                                 │
                         └────────────────────────────────────────────────────────────────────────┘
                                        unix socket, 0600, ~/.local/state/pfm/run/pfmd.sock
   external siblings, health-checked only, never owned:
     harvester route (/mcp/harvester on 18377) · harvester external gateway · internal/harvestpy worker
```

Today's picture for contrast: three arrows into `api.anthropic.com` (hook, sampler, and — via `codex app-server` — the statusline refresher), N chat MCP processes each holding two single-connection sqlite handles, and a `pkill` between any two versions.

## API

All paths are under `/v1`. Request and response bodies are JSON unless noted. Every response carries `X-PFM-Version` so a client can detect skew the way `cmd/pfm/doctor.go:95` already does for the MCP daemon.

| Method | Path | Request | Response | Notes |
| --- | --- | --- | --- | --- |
| GET | `/v1/version` | — | `{pfmVersion, protocol:"v1", pid, startedAt, binary}` | Phase 1. The liveness probe; mirrors `mcpDaemonStatus` (`cmd/pfm/mcp_serve_command.go:31-42`). |
| GET | `/v1/health` | — | `{store:{ok,path,schema}, limits:{lastFetchAt,backoffUntil}, harvester:{route,external}, tmux:{ok}}` | Phase 1 skeleton, filled per phase. `pfm doctor` renders one line per key. |
| GET | `/v1/limits` | `?engine=claude\|codex` | `{accounts:[{id, engine, emoji, windows:{five_hour:{usedPercent,resetsAt},…}, fetchedAt, stale, backoff:{message,retryAfter}\|null, skipReason}]}` | Never fetches. Answers from the daemon's live cache. |
| GET | `/v1/limits/{account}` | — | one `accounts[]` element | The hook's call. Must answer in ≪200 ms. |
| POST | `/v1/limits/{account}/refresh` | `{reason}` | `202` + current record, or `429` + `{retryAfter}` | Bounded by the same backoff rules as `stats.backoffFor` (`limits.go:465`): a 429 sets ≥10 min, honoring a server `Retry-After`. Coalesced by a daemon-wide single-flight per account. |
| PUT | `/v1/limits/{account}/observed` | `{source:"claude-code", windows:{five_hour:{usedPercentage,resetsAt},…}, limits:[…], observedAt}` | `204` | Statusline write-back of the harness's own `rate_limits`. Free, authoritative, and today discarded (`render.go:120-160`). A write-back defers the next poll. |
| GET | `/v1/events` | `?cursor=<atNS>&kinds=comms,limits,fleet` | `text/event-stream`, one event per line: `{cursor, kind, at, payload}` | Cursor is nanoseconds, the same key `shared.CommsSince` pages on (`internal/shared/comms.go:62`). Resubscribe with the last cursor after a reconnect. |
| GET | `/v1/comms` | `?since=<atNS>&limit=` | `{events:[CommsEvent…], truncated}` | The non-streaming form `cosmosSampler` uses today (`cmd/pfm/pipeline.go:184-188`). |
| GET | `/v1/fleet` | `?engine=` | `{rows:[…], scannedAt}` | The picker snapshot, served from the daemon's scan instead of every client scanning. |
| POST | `/v1/rpc/{server}` | one JSON-RPC frame (`server` ∈ `chat`,`harvester`) | one JSON-RPC frame | What the stdio proxies forward. Requires `X-PFM-Caller` — see below. |
| POST | `/v1/daemon/handoff` | `{binary, pid}` | `{socketFdSent:true}` | Phase 5. Control plane for the upgrade handoff. |
| POST | `/v1/daemon/shutdown` | `{drain:"30s"}` | `202` | Graceful stop without a signal; what `pfm daemon stop` calls. |

**`X-PFM-Caller` is the whole reason the chat MCP is stdio today.** `internal/installer/mcp.go:101-116` spells it out: the chat server is registered as `stdio` and every other server as `http` because *"a self-addressed chat_* call carries no thread id from Claude, and the daemon serves every chat on the box from one process, so it can never derive who's calling (mcpserv's callerForRequest, fail-closed by design)"*. `cmd/pfm/main.go:194` sets `AllowAmbientIdentity = true` for the stdio path alone. A thin proxy therefore **must** carry the ambient identity it inherited — tmux socket, pane, session id — as a per-request header, and pfmd must trust that header only over the unix socket with a matching peer uid. Without this, `chat_whoami` and every self-addressed `chat_*` call regress to fail-closed.

## Process model

**Start.** `pfm daemon run [--socket PATH] [--foreground]`. It creates the run dir `0700`, binds the socket, `chmod 0600`, and writes `pfmd.json` beside it (pid, version, binary path, startedAt) — the same status document `probeMCPDaemon` reads today (`mcp_serve_command.go:284`). A stale socket file whose `connect()` is refused is unlinked and rebound; a socket that **answers** makes the second instance exit 1 with `already running (pid N, since T)`, exactly as `runMCPServe` does at `mcp_serve_command.go:151-153`.

**Auto-start.** Any client that dials and gets `ECONNREFUSED`/`ENOENT` takes an exclusive lockfile `pfmd.lock` (`O_CREATE|O_EXCL`, the `armRefresh` pattern at `render.go:962-977`), re-execs `os.Executable()` with `daemon run`, `Setsid: true`, stdio to `/dev/null`, and `Process.Release()` — literally `statusline.SpawnDetached`'s body (`process.go:23-51`) with a different argv. The client does **not** wait: it serves this call from the fallback path and picks the daemon up next time. A prompt hook never pays for a cold start.

**Supervision (optional).** `pfm daemon install` writes `com.professor.pfmd.plist` under `~/Library/LaunchAgents` with `KeepAlive`, or `~/.config/systemd/user/pfmd.service` with `Restart=always`. This is the established shape: `internal/installer/launchd.go:16,83` (`com.professor.pfm.mcp`, `wireMCPLaunchAgent`) and `internal/installer/installer.go:321` (`runSystemctl(ctx, "restart", mcpUnitName)`). The plist is written as a real file, never a symlink — `launchd.go:36-41` records why: launchd refuses symlinked agents *silently*.

**Shutdown.** `SIGTERM` → stop accepting, close the listener, cancel the poller, let in-flight requests finish within a 30 s drain, close the SSE streams with a final `cursor` event, close the store, unlink the socket and `pfmd.json`. `SIGINT` in `--foreground` is the same. A second signal during the drain is immediate exit.

**Handoff (phase 5).** **Decision: re-exec with an inherited fd**, not `SCM_RIGHTS`. The old pfmd clears `FD_CLOEXEC` on the listener, `exec`s the new binary with `PFMD_LISTEN_FD=3` and `PFMD_HANDOFF_FROM=<pid>`, and the new process rebuilds the listener with `net.FileListener`. Why this over passing the fd across a control socket: re-exec needs no second listener, no ordering protocol between two live daemons, and no ambiguity about which process owns the socket file — the pid changes but the inode never does, so no client sees a closed socket. `SCM_RIGHTS` would be the right tool if the old and new daemons had to run **concurrently** (a true zero-downtime blue/green), and they do not: the drain here is measured in the milliseconds a JSON-RPC frame takes. The cost is that a new binary which fails to start takes the socket down; the mitigation is that the old process validates `pfm --version` on the candidate binary before exec'ing it, and clients fall back (§ Failure modes) during the gap.

`make install` and `pfm update` then call `pfm daemon restart`, which is `POST /v1/daemon/handoff`. The `pkill -f 'pfm mcp serve'` line at `Makefile:110-112` is deleted in phase 5, not before.

## Failure modes and fallbacks

| What breaks | What the client does | What the user sees |
| --- | --- | --- |
| Socket file absent (daemon never started) | Dial fails instantly on `ENOENT`; take the lock, spawn `pfm daemon run` detached, serve this call from the shared cache file / direct store | Nothing. The next call is served by the daemon. |
| Socket present, connection refused (daemon died) | Unlink the stale socket, spawn, fall back for this call | Nothing; `pfm doctor` reports `daemon=restarted-by-client` once |
| Daemon slow (> 200 ms for the hook) | Context deadline fires, hook falls back to `acct-N.json` | Prompt submits at normal speed; the warning line may be one TTL stale |
| Daemon returns 5xx | Client logs to its own stderr and falls back | `pfm doctor` shows `daemon=degraded error=…` |
| Version skew (client newer than daemon) | Client compares `X-PFM-Version`, uses the daemon anyway for `/v1` shapes it knows, warns once | `doctor: daemon version-skew daemon=X client=Y` — the line `doctor.go:95` already prints for the MCP daemon |
| Provider 429 | Daemon records the backoff (`≥10 min`, honoring `Retry-After`) and serves cached windows with `backoff` set; `POST …/refresh` answers `429 {retryAfter}` | The Limits tab shows the cached number with a retry time — today's `limits.go:465` message, now with no second process able to re-trigger it |
| Store locked / corrupt | Daemon answers `503` on store endpoints, `/v1/limits` keeps working | `doctor: daemon store=degraded`; limits unaffected |
| Handoff fails (new binary won't start) | Old daemon logs, keeps serving if the exec failed pre-exec; if post-exec, clients auto-start the binary on disk | One dropped request per in-flight client, then normal |
| SSE stream drops | TUI reconnects with backoff and resubscribes at its last cursor | At most one skipped frame in the cosmos tab |
| Chat MCP proxy loses the socket mid-session | Proxy falls back to in-process `mcpserv.NewConfigured` with `AllowAmbientIdentity` (today's exact code path, `main.go:191-206`) | Chat tools keep working; the store gets a second writer until the daemon returns |
| Two daemons race to bind | Loser detects a live answer on the socket and exits 1 with the running pid | `pfm daemon run: already running (pid N, since T)` |

The invariant behind the whole table: **no pfm client ever hard-depends on pfmd.** Every endpoint has the pre-daemon path behind it, and phase gates keep that true (§ Migration).

## Security

- **No TCP, ever.** pfmd binds `unix` only. The existing loopback MCP daemon keeps its `127.0.0.1:18377` bind, and keeps its browser-origin refusal (`mcp_serve_command.go:78-82`) — pfmd adds no new network surface.
- **Socket `0600`, run dir `0700`.** Set with `chmod` after bind (the umask cannot be trusted), the way `usagehook.EnsurePrivateDirectory` (`hook.go:507`) and the SID dir (`render.go:690`) already do.
- **Peer uid check on every connection.** `SO_PEERCRED` on Linux, `LOCAL_PEERCRED`/`getpeereid` on macOS; a connection whose uid ≠ `os.Getuid()` is closed before the first byte is read. File mode is the first lock; the uid check is the second — the house rule that the second lock holds when the first fails silently.
- **Credentials never leave the daemon.** The OAuth access token read by `internal/usagehook/credential.go` (path resolution at `hook.go:381`, macOS keychain at `internal/usagehook/keychain_darwin.go`) is used only inside pfmd. Clients receive **percentages and reset times** — no token, no credential path, no `Authorization` header ever crosses the socket. This is a net reduction: today every chat's hook process reads the keychain on a cadence.
- **`X-PFM-Caller` is trusted only on the socket** with a matching peer uid, and it selects an identity — it never grants one. `mcpserv`'s `callerForRequest` stays fail-closed for anything arriving on the HTTP daemon.
- **Socket path length.** `internal/testjail/testjail.go:20-30` documents the macOS 104-byte cap on the *whole* unix socket path. The real path (~45 bytes) is safe; jails must set `PFM_RUN_DIR` under the short canonical base testjail already installs, or bind fails with `bind: invalid argument` — an error that reads like a code bug.
- **No `docker.sock`-shaped pivot.** pfmd performs tmux and process operations on behalf of clients; it must refuse any request that names an arbitrary binary to exec. Fleet endpoints take *fleet identities* (socket, pane, account), never command lines.

## Migration

Each phase is gated by `daemon.<phase>.enabled` in `~/.config/pfm/pfm.config.json` (the v2 strict config, `internal/config`), default `false` until the phase's tests are green on both hosts. Reverting a phase is flipping its gate — no client loses its fallback path.

### Phase 1 — skeleton

- **New:** `internal/daemon/` (`server.go`, `socket.go`, `status.go`, `signals.go`), `cmd/pfm/daemon_command.go` (`run`, `status`, `stop`, `install`, `uninstall`), route in `cmd/pfm/main.go` beside `case "mcp"` (`main.go:93`).
- **Touched:** `internal/paths/paths.go` — add `RunDir` + `PFM_RUN_DIR` to `Values`/`Resolve` (`paths.go:140-172`); `internal/installer/launchd.go` + `internal/installer/assets.go` — the `com.professor.pfmd.plist` asset beside `mcpLaunchdAsset` (`launchd.go:20`); `internal/installer/installer.go` — the systemd unit beside `mcpUnitName` (`installer.go:321`); `cmd/pfm/doctor.go` — one `doctor: daemon …` line modelled on `doctor.go:89-95`.
- **Tests:** `internal/daemon/socket_jail_test.go` (bind, `0600`, stale-socket rebind, second-instance refusal), `internal/daemon/shutdown_jail_test.go` (SIGTERM drains an in-flight request), `cmd/pfm/daemon_command_test.go` (`run`/`status`/`stop` argv and exit codes), `cmd/pfm/doctor_daemon_test.go`. Each package gets `testmain_test.go` calling `testjail.Run(m)` — the house pattern (`internal/store/testmain_test.go`, `internal/mcpserv/testmain_test.go`).

### Phase 2 — limits service and its clients

- **New:** `internal/daemon/limits.go` (poller, daemon-wide single-flight, backoff ledger, the observed write-back), `internal/daemonclient/` (the `http.Transport` dialer, typed calls, the fallback decision).
- **Touched:** `internal/stats/limits.go` — `LimitsSampler` gains a `Client` seam so the picker asks the daemon before `fetchClaudeCached` (`limits.go:328`); `internal/usagehook/hook.go` — `Evaluate` tries `GET /v1/limits/{n}` with a 200 ms deadline before `refresh` (`hook.go:204`); `internal/statusline/render.go` — `windowsAt` (`render.go:120`) additionally `PUT`s the harness's own `rate_limits`; `cmd/pfm/commands.go:147-151` — TTL wiring moves into the daemon.
- **Also fix here:** the hook's missing backoff write (`hook.go:525-554`). In daemon mode the daemon owns it; in fallback mode the hook writes a `CacheBackoff` like the sampler does (`limits.go:408-413`), so the 2026-09-11 failure cannot recur on either path.
- **Tests:** `internal/daemon/limits_singleflight_jail_test.go` (20 concurrent hook-shaped GETs ⇒ one upstream fetch), `limits_backoff_jail_test.go` (a 429 sets ≥10 min and is served, not re-fetched), `limits_observed_test.go` (a write-back defers the next poll), `internal/usagehook/hook_daemon_fallback_test.go` (socket absent, socket hangs, socket 5xx ⇒ hook still returns within budget), `internal/statusline/writeback_jail_test.go`.

### Phase 3 — store service, comms and cosmos through the daemon

- **New:** `internal/daemon/store.go` (the single writer), `internal/daemon/events.go` (SSE bus, nanosecond cursor).
- **Touched:** `cmd/pfm/pipeline.go:184-188` — `cosmosSampler` reads `/v1/comms` (or the SSE stream) instead of calling `shared.Store.CommsSince` in-process; `internal/shared/comms.go` unchanged as the daemon's own reader; `internal/store/store.go` gains a "daemon owns this file" assertion so a second writer is a loud error, not a `busy_timeout` queue (`store.go:162`).
- **Tests:** `internal/daemon/events_cursor_jail_test.go` (resubscribe at a cursor loses and duplicates nothing), `store_single_writer_jail_test.go` (a client attempting a direct write while the daemon holds the file is refused with a named error), `cmd/pfm/cosmos_daemon_test.go` beside the existing `cosmos_safe_test.go`.

### Phase 4 — MCP proxies

- **New:** `internal/daemon/rpcproxy.go` (socket side), `cmd/pfm/mcp_proxy.go` (stdio side).
- **Touched:** `cmd/pfm/main.go:191-206` — the chat stdio path forwards frames instead of building a service, keeping `mcpserv.NewConfigured` as the fallback; `cmd/pfm/mcp_serve_command.go:134` — `runMCPServe` mounts proxy handlers; `internal/installer/mcp.go:117` — registration shapes stay **byte-identical**, which is the point.
- **Tests:** `cmd/pfm/mcp_proxy_identity_jail_test.go` (a self-addressed `chat_whoami` through the proxy resolves the same caller as `AllowAmbientIdentity` does today — the regression this phase most risks), `mcp_proxy_fallback_test.go` (socket dies mid-session ⇒ in-process service takes over, no dropped frame), `internal/mcpserv/proxy_frame_test.go` (malformed frame handling matches `RunStdio`'s parse-error behaviour, `internal/mcpserv/stdio.go:13-16`).

### Phase 5 — socket handoff on upgrade

- **New:** `internal/daemon/handoff.go` (`PFMD_LISTEN_FD`, `net.FileListener`, candidate validation).
- **Touched:** `Makefile:80,100-116` — `mcp-restart` becomes `daemon-restart`, `pkill` deleted; `internal/update/run.go` — `applyUpdateInstall` calls the handoff; `internal/installer/installer.go:318-322` — the unconditional `systemctl restart` becomes a handoff with the restart as fallback.
- **Tests:** `internal/daemon/handoff_jail_test.go` (a client holding an open connection across a handoff sees no `ECONNRESET`; the socket inode is unchanged), `handoff_badbinary_test.go` (a candidate that fails `--version` is never exec'd), `cmd/pfm/update_handoff_test.go`, and an `e2e/` case in the style of `e2e/install_e2e_test.go`.

## Open questions

1. **Does the daemon poll at all once write-back lands?** If the statusline feeds `PUT /v1/limits/{n}/observed` on every render of every active chat, the 5h/7d windows may never need a fetch — reducing the poller to a slow floor (say 10 min) for idle accounts and for Fable, which the harness sometimes omits (`render.go:140-147`). Measure before choosing a cadence.
2. **Fleet scanning: daemon or client?** `/v1/fleet` above assumes pfmd owns the scan, which would also retire the picker's `refreshCadence` backoff (`cmd/pfm/pipeline.go:60-95`). But a daemon scanning tmux continuously is exactly the fork-churn cost that cadence was built to avoid. It may belong behind an "is anyone watching" gate rather than a timer.
3. **What happens to `pfm mcp serve` as a separate process?** Phase 4 makes it a proxy, which means two long-running daemons plus its external harvester gateway. Fold it into pfmd as a second listener, or keep it separate so an MCP crash cannot take limits down with it?
4. **Per-account or per-daemon backoff on 429?** The current record is per account file (`limits.go:331`), but the 2026-09-11 incident hit *both* accounts. If the limiter is per-IP, one account's 429 should quiet the other.
5. **Does the devbox instance need its own socket namespace?** `pfm` runs on both local-mac and devbox with the same `$HOME` layout; nothing today prevents a future remote client from expecting one. Decide now whether `pfmd.sock` is strictly host-local (it should be) and write that down.

## What the operator learns

- **Phase 1 — the daemon contract.** Unix domain sockets vs TCP and why a filesystem path with a mode is a capability. `SO_REUSEADDR` has no unix analogue, so stale-socket detection is `connect()`-then-unlink. Signal handling and graceful drain: why `SIGTERM` means *stop accepting, finish what you started*. launchd vs systemd `--user`: two answers to "who restarts this", and why launchd refuses symlinked plists.
- **Phase 2 — coordination and rate limits.** Single-flight and request coalescing; why a per-process mutex (`limits.go:665-677`) is not coordination. Backoff as *shared durable state* rather than a local variable. TTL vs push: the write-back turns a polling problem into an event-driven one. Deadline budgets on a latency-critical path (the 200 ms hook).
- **Phase 3 — single-writer discipline.** Why SQLite WAL plus `busy_timeout` is a queue, not concurrency, and what changes when one process owns the file. Cursors and at-least-once delivery: an SSE stream that can be resumed is a log, and a log needs a monotonic key (`shared.CommsSince`'s `at_ns`).
- **Phase 4 — protocol proxying and identity.** Forwarding JSON-RPC without parsing it. Ambient vs explicit identity, and why a shared server must be told who is calling (`installer/mcp.go:101-116`). Fail-closed as a default that survives a refactor.
- **Phase 5 — zero-downtime upgrade.** File descriptors as inheritable resources; `FD_CLOEXEC` and what `exec` does and does not keep. Why an inode outliving a process is the mechanism behind both the stale-binary bug (`Makefile:10-17`) and its fix. `SCM_RIGHTS` as the tool you now know you did not need, and the test that proves it: a connection held open across a restart.
