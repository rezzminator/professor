# advisor-cache-gateway

Claude Code's advisor rereads your whole conversation at full price on every call. This local gateway turns on the advisor's prompt cache, so a later advisor call in the same turn pays a twentieth of the price (Opus 5.5) for everything an earlier call already read.

The advisor is still Claude Code's own: Anthropic runs it on its servers. It reads what the main model's context holds, which includes the main model's thinking from the current turn but not from earlier turns. The gateway changes one field in the request and nothing else.

## How it works

The Anthropic API's advisor tool (`advisor_20260301`) accepts `caching: {"type": "ephemeral", "ttl": "5m" | "1h"}`, which is off by default. Claude Code 2.1.293 builds the advisor tool as `{type, name, model}` and never adds `caching` (its bundle has no code that does). No setting changes that, and a plugin cannot touch server tools.

So `gateway.py` sits at `ANTHROPIC_BASE_URL=http://127.0.0.1:18787` and forwards every request to `api.anthropic.com` unchanged, with one exception. In `POST /v1/messages`, if the advisor tool has no `caching` field, the gateway adds one:

| Conversation | How the gateway recognises it | Default TTL |
|---|---|---|
| main session | no `x-claude-code-agent-id` header | 5m |
| sub-agent | `x-claude-code-agent-id` header | 5m |

The two kinds are configured separately, so one can move to `1h` or `off` while the other stays. A conversation keeps one TTL, because a 1h request does not read an entry written at 5m (measured). Streaming passes through chunk by chunk. Your login (`Authorization: Bearer`, the subscription's OAuth) passes through and is never logged.

`run.sh` supervises the gateway, under launchd on macOS and systemd on Linux, and owns the setting: it points Claude Code at the gateway only once the gateway answers, and points it at the API itself (`https://api.anthropic.com`) whenever the gateway goes down or is stopped, so a broken gateway never cuts Claude Code off.

It rewrites the URL instead of deleting it, because a running Claude Code session follows a changed `env.ANTHROPIC_BASE_URL` within about a second but keeps a deleted one until it restarts (measured on 2.1.294, see [Known behaviour](#known-behaviour)). An explicit `https://api.anthropic.com` is what Claude Code uses when the variable is unset, and it treats both the same.

## Cost

Caching changes only the price of the advisor's input. Its output costs the same either way. Relative to reading a token uncached:

- **Cache write:** 1.25× at 5m and 2× at 1h. It is paid on the part of the input not yet in the cache: everything on a first call or after a miss, only the new tokens on a hit.
- **Cache read:** 0.05× on Opus 5.5 and Sonnet 5.5, 0.025× on Fable 5.1 and Mythos 5.1, and 0.1× on other models. The Claude Code advisor is Opus 5.5, which is the 0.05× case. Prices from platform.claude.com, 2026-10-08.
- **TTL:** a read refreshes it.

Each call stores a snapshot of the advisor's input from the first token up to that call. A later call reads a snapshot only if it is an exact prefix of the later call's input. Otherwise it reads nothing; partial matches don't count.

At a turn boundary that follows thinking, the earlier snapshot stops matching, because Claude Code sends that thinking back emptied (see the measurements below). So count advisor calls per turn.

Take a turn with `k` advisor calls, a transcript of `T` tokens at the first call, and `δ` new tokens between calls, with Opus 5.5 as the advisor:

| Calls in the turn | Uncached | 5m | 1h |
|---|---|---|---|
| 1 | T | 1.25T | 2T |
| 2 | 2T + δ | 1.30T + 1.25δ | 2.05T + 2δ |
| 3 | 3T + 3δ | 1.35T + 2.55δ | 2.1T + 4.05δ |

Example: `T` = 150k and `δ` = 100k. Opus 5.5 prices per million tokens: input $4, 5m write $5, 1h write $8, cache read $0.20.

| | Call 1 | Call 2 | Two calls |
|---|---|---|---|
| uncached | $0.60 | $1.00 | $1.60 |
| 5m | $0.75 | $0.53 | $1.28 |
| 1h | $1.20 | $0.83 | $2.03 |
| 1h, with a thinking turn between the calls | $1.20 | $2.00 (miss: all 250k written) | $3.20 |

- **5m** costs 25% more on a turn with one call, and wins on a turn with two or more calls less than 5 minutes apart, unless `δ` is above `2.8T`. When a gap passes 5 minutes, that call costs 1.25× instead of 1×, so the loss is bounded at 25%.
- **1h** costs double on a turn with one call, still loses with two, and wins only from three calls in one turn, while `δ` stays below about `0.86T`. It beats 5m only when calls in one turn sit more than 5 minutes apart, or when the turns between calls have no thinking.

Hence both kinds default to 5m. `/usr/bin/python3 ~/.local/share/advisor-cache-gateway/gateway.py stats` prices your real calls from the log, each write at its own TTL and each read at its advisor model's rate, split by kind (`main`, `sub`). If a kind's line shows a loss after a few days, switch it with `./install.sh --ttl …` or `--subagent-ttl …` (`off` stops caching for that kind).

## Measured on 2026-10-08

Setup: Claude Code 2.1.293, Haiku 5.5 as the main model, Opus 5.5 as the advisor.

| Run | advisor call 1 | advisor call 2 |
|---|---|---|
| without the gateway | 99,097 tokens read at full price | 99,413 tokens read at full price |
| with the gateway, 1h | 96,642 written to the cache | 96,642 read from the cache, 200 written |

- **Within a turn, the cache always holds.** Later advisor calls in the same turn read everything the earlier ones wrote, both in main sessions and in sub-agents (73,107 tokens read on a sub-agent's second call at 5m).
- **Across turns, it holds only when the earlier turn had no thinking.**
  - The turn-1 advisor call reads the main model's thinking live.
  - In turn 2, Claude Code sends that thinking back as an empty block with only its signature, and the API leaves earlier-turn thinking out of the context.
  - So the advisor's cached prefix no longer matches, and its first call in the new turn re-writes everything.
  - With no thinking in turn 1, turn 2 read all 98,518 tokens. With thinking, it read 0.
  - This happens on the API side, and the main model's own cache misses at the same point. No gateway can change it.
- **The advisor reads current-turn thinking only.**
  - Adding 2,470 tokens of thinking before an advisor call added exactly 2,470 tokens to the advisor's input.
  - Over 1,000 tokens of thinking in an earlier turn added nothing. The main model's own billed input didn't grow from it either, although Claude Code sends `clear_thinking` with `keep: "all"`.
- **A conversation can't change TTL and keep its cache.** A 1h call after a 5m call read 0 tokens and re-wrote 99,844.
- **The API accepts `max_tokens` on the advisor through the subscription login** (HTTP 200).

## Install, update, remove

```sh
./install.sh                                  # main 5m, sub-agents 5m, port 18787
./install.sh --ttl 1h --subagent-ttl off      # also: --max-tokens 2048, --port N
./uninstall.sh                                # add --purge to delete the logs too
/usr/bin/python3 -I -m unittest discover -s tests
```

It runs on macOS (a launchd agent) and on Linux (a systemd user unit; enable lingering with `loginctl enable-linger` so it runs without a login session). It needs `/usr/bin/python3` 3.9 or later, `jq` and `curl`.

`install.sh` does the following, and is safe to run again:

1. Runs the tests.
2. Points an `ANTHROPIC_BASE_URL` that aims somewhere else at the API, after a backup to `~/.local/state/advisor-cache-gateway/backups/`.
3. Copies `gateway.py`, `run.sh`, `ctl.sh` and this README to `~/.local/share/advisor-cache-gateway/`. It writes the options to `config.env` there and keeps every other line you added to that file.
4. Writes the service, which runs `run.sh` and restarts it within about two seconds if it exits:
   - **macOS:** the launchd agent `local.advisor-cache-gateway` in `~/Library/LaunchAgents/`.
   - **Linux:** the systemd user units `advisor-cache-gateway.socket` and `advisor-cache-gateway.service` in `~/.config/systemd/user/`. The socket unit holds the listening socket; every gateway generation inherits it (see Graceful restart).
5. Applies the change by the least disruptive route:
   - **First install:** turns the gateway on, and waits until `run.sh` has found it healthy and set `env.ANTHROPIC_BASE_URL`. Running Claude Code sessions follow the change within about a second.
   - **Update while on:** a graceful restart (see below). If `run.sh` or the service files changed, it restarts the service instead. Claude Code is pointed at the API while the old gateway drains, so even then no stream is cut.
   - **Update while off:** changes only the files, and the gateway stays off.

`uninstall.sh` stops the service, waiting for in-flight streams to finish, which points running sessions at the API. Then it deletes the setting, which only sessions started afterwards notice, and deletes the service and the installed copy. The backups stay.

A second install beside the first, for a trial run, takes its own `ADVISOR_GATEWAY_LABEL`, `ADVISOR_GATEWAY_DEST`, `ADVISOR_GATEWAY_LOGS`, `CLAUDE_SETTINGS` and `--port`; `uninstall.sh` takes the same `ADVISOR_GATEWAY_DEST`.

`--max-tokens N` caps the advisor's output per call, thinking plus text, with a minimum of 1024. Anthropic's advisor documentation reports that a cap of `2048` cut advisor output about 7× with no detectable loss of quality. It is off by default, because it changes the advice itself and not only its price.

## On, off, restart

Only you turn the gateway on or off, with `~/.local/share/advisor-cache-gateway/ctl.sh`. The state holds across logins. Nothing else connects to it or changes it.

| Command | What happens |
|---|---|
| `ctl.sh on` | starts the service; Claude Code uses the gateway once it answers |
| `ctl.sh off` | Claude Code, running sessions included, goes straight to the API within about a second. The gateway keeps serving for 3 seconds while they follow, finishes the streams already running, then stops. The setting stays at `https://api.anthropic.com` while off, so a session that was suspended follows it too. Stays off after a login. |
| `ctl.sh restart` | graceful restart: re-reads `config.env`; no stream is cut |
| `ctl.sh status` | on or off, the health line, where Claude Code points, where the logs are |

**Graceful restart:**
1. `run.sh` starts a new gateway on the same port.
   - **macOS:** every gateway binds with `SO_REUSEPORT`, and macOS hands new connections to the newest one.
   - **Linux:** every gateway serves the one listening socket the systemd socket unit holds, so old and new share one queue. `SO_REUSEPORT` doesn't work here: Linux spreads new connections over all listeners and resets the ones waiting at a listener that closes (measured: 39 refused connections in 30 restarts; with the shared socket, 0).
2. Once the new gateway answers its health check (which reports its pid), `run.sh` sends the old one `SIGTERM`.
3. The old gateway stops accepting and closes its idle keep-alive connections, so their clients reconnect to the new one. A connection it accepted just before the stop gets 2 seconds to send its request, which it then serves. It finishes every in-flight request and exits once they're done, or after `ADVISOR_CACHE_DRAIN_SECONDS` (600).

A HUP that arrives during a restart starts another one right after. The service manager waits 630 seconds for a stop (launchd `ExitTimeOut`, systemd `TimeoutStopSec`), so a stop also waits for the drain.

## Fail-open

`run.sh` points `env.ANTHROPIC_BASE_URL` at the gateway only while the gateway answers its health check. Otherwise it releases it: it points the URL at the upstream the gateway forwards to (`https://api.anthropic.com`, or `ADVISOR_CACHE_UPSTREAM` in `config.env`). Running sessions follow a release within about a second.

| Event | What `run.sh` does |
|---|---|
| the gateway answers | sets the URL, unless a URL other than the API is already there |
| `/usr/bin/python3` is missing, or the developer tools are gone after a macOS update | releases the URL, waits 60 seconds, exits; the service manager tries again |
| the gateway can't bind its port or doesn't answer within 15 seconds | releases the URL, waits 60 seconds, exits |
| the gateway process dies | releases the URL and exits; the service manager restarts it about two seconds later, and the URL returns once it answers |
| the service is stopped (`ctl.sh off`, logout on macOS, uninstall) | releases the URL first, keeps serving `ADVISOR_GATEWAY_SETTLE_SECONDS` (3) while running sessions follow, then lets the gateways drain, then exits |

It never touches a URL other than the gateway's or the API's. With the URL at the API, Claude Code talks to it directly and the advisor runs uncached. Only a crash cuts requests now: one in flight when a gateway process dies fails with a connection error, and so does one a running session sends in the second before it follows the release. Restarts and stops cut nothing.

## Capture

Every exchange except `/__gateway/health` is written in full to `~/.professor/tmp/gw-logs/`, one folder per request and response:

```
{YYYY-MM-DD}/{HHMMSS.mmm}-{sequence}-{main|sub}-{METHOD}-{path}/
  1-request.json        method, path, headers (Authorization, x-api-key, cookie redacted)
  2-request.body        the body exactly as Claude Code sent it
  3-request.sent.body   the body as forwarded upstream; present only when the gateway rewrote it
  4-response.json       status, reason, response headers (or the gateway's own 502 error)
  5-response.body       the exact bytes streamed back, SSE events included
```

The sequence number orders exchanges by arrival, even when they overlap. A capture write that fails is logged once and never breaks the request.

- **Size and age:** each request carries the full transcript, so expect gigabytes per day of heavy use (measured on devbox: about 120 MB an hour). Day folders older than 7 days are deleted when a gateway starts and on the first capture of each day, on a background thread; `ADVISOR_CACHE_CAPTURE_KEEP_DAYS` in `config.env` changes the 7. Nothing else in the folder is touched, and each deletion is logged.
- **Contents:** the bodies hold whole conversations, including account and device ids and file contents.
- **Configuration:** set `ADVISOR_CACHE_CAPTURE_DIR` in `config.env` to another folder, or to `off` to stop capturing, then run `ctl.sh restart`.

## Operating it

| | |
|---|---|
| Log | `advisor-cache-gateway.log` in `~/Library/Logs/` (macOS) or `~/.local/state/advisor-cache-gateway/logs/` (Linux), rotated at 5 MB. It records each messages request with its status, model, rewrite (`advisor-caching=5m`; `advisor-caching-kept=1h` when the request already carried `caching`; `advisor-tool=none` when its tools held no advisor) and kind (`conv=main` or `conv=sub`); one line per advisor call with its TTL, cache read and cache write; and each error with the request's shape and body. |
| Supervisor log | `advisor-cache-gateway.stderr.log` beside it: each time `run.sh` links or releases the URL, and why. |
| Health | `curl -s http://127.0.0.1:18787/__gateway/health` |
| On, off, restart, status | `~/.local/share/advisor-cache-gateway/ctl.sh on\|off\|restart\|status` |
| Settings | `~/.local/share/advisor-cache-gateway/config.env`, written by `install.sh`; `ctl.sh restart` applies an edit to it |
| Debug | `ADVISOR_CACHE_DEBUG=1` in `config.env` logs, for each advisor request: its `context_management`, its `thinking` config, its non-secret headers and its metadata. |

## Known behaviour

- **An upstream connection is reused for 20 seconds at most** (`ADVISOR_CACHE_UPSTREAM_IDLE`). On devbox, three sub-agent requests through the first install got no answer for 900 seconds and ended in 502; one couldn't even finish writing its body. Each came on a client connection that had sat idle between requests, so the gateway wrote into an upstream connection that idle time had likely killed without a reset. Claude Code gives up after 600 seconds and retries, so each cost a sub-agent a 10-minute stall. A fresh connection costs one TLS handshake, about 100 ms.
- **A running session follows a changed URL but keeps a deleted one** (Claude Code 2.1.294, measured 2026-10-08 with one headless session and two local fake upstreams).
  - Changing `env.ANTHROPIC_BASE_URL` from A to B moved the session's next request to B. Adding it back after a delete moved it too. Deleting it left the session on B.
  - A request sent 1 second after the write always reached the new URL; at 0.5 seconds it sometimes still went to the old one.
  - In the bundle, a settings change whose `env` differs calls `applyConfigEnvironmentVariables`, which copies every settings `env` over `process.env` with `Object.assign` and deletes nothing.
  - So `run.sh` and the scripts release the URL by rewriting it to the API, never by deleting it while a session might use the gateway. End to end: one session went through the gateway, kept working straight to the API after `run.sh` stopped, and went back through the gateway after `run.sh` started again, with no restart.
- **With the gateway on, Claude Code doesn't see a first-party API.** `isFirstPartyAnthropicBaseUrl()` is true only for the host `api.anthropic.com` (or with the internal `_CLAUDE_CODE_ASSUME_FIRST_PARTY_BASE_URL`), so it is false at `http://127.0.0.1:18787`. Which features that flag gates in 2.1.294 was not traced. The advisor and its cache work through the gateway (see the measurements above).
- **One 429 per new session.** Each new session's first request is Claude Code's start-up `quota_check`: Haiku, `max_tokens: 1`, no tools. Through the gateway it answers `429 rate_limit_error "Error"`, and the session carries on normally.
  - Tool-less requests on the same login, sent straight to the API with no gateway, drew the same 429.
  - So the 429 very likely comes from the API, not the gateway. The exact `quota_check` request was not replayed without the gateway.
- **Parallel advisor calls don't share the cache.** When the main model puts two advisor calls in one message, both read the same prefix at the same moment, so both write it and neither reads (measured: two writes of 96,285 tokens). Only a call made after an earlier one has answered reads from the cache.
- **Two advisor calls cached at 1h without the gateway adding it** (2026-10-08 04:06 and 04:21, both in one long main session): the request that ran the advisor showed no rewrite, and the API reported the write as `ephemeral_1h_input_tokens`. Unexplained; the log markers above now tell the next one apart. `stats` prices a row without a TTL at 1h, which matches these two.
- **This install breaks `pfm doctor`'s harness-prompt check when doctor runs from `~`.** A tool that aims a Claude run at its own `ANTHROPIC_BASE_URL` through the process environment loses to this setting whenever that run loads `~/.claude/settings.json`: a settings file's `env` overrides the process environment. `pfm doctor`'s harness-prompt check hits this when run from `~`, where `~/.claude/settings.json` also loads as the folder's project settings, so its probe reaches the gateway with a dummy key and gets 401. Run from any other folder, the check passes.
- **Values already set are kept.** If the advisor tool already carries `caching` or `max_tokens`, the gateway leaves it alone, so it steps aside on its own once Claude Code sets these itself. The feature request is anthropics/claude-code#91110.
