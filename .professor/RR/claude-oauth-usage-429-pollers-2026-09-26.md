# RR — Which OSS tools poll Claude's /api/oauth/usage, and how do the best avoid 429?

Question: Which open-source projects call Anthropic's Claude OAuth usage endpoint (GET https://api.anthropic.com/api/oauth/usage — the 5-hour / 7-day limit data behind Claude Code's /usage) on a recurring interval — statuslines, menu-bar or tray monitors, TUI dashboards, usage trackers — and, read from their source code, how do the most valuable of them avoid HTTP 429 Too Many Requests? Rank the repositories by value (adoption, maintenance, care in their fetch path) and distill for each: the polling interval, caching (in-memory, on-disk, shared across processes or not), single-flight or locking between concurrent callers, backoff after a 429, whether and how Retry-After is honored, the request headers sent (anthropic-beta, User-Agent, anything else), token refresh, and anything they document about the endpoint's rate limit. End with the patterns that separate the implementations that do not get 429'd from the ones that do.

## Answer

The pollers that stay out of 429 do four things together:

1. They identify as `User-Agent: claude-code/<version>`. The endpoint buckets its rate limit by User-Agent, and requests without one land in a bucket that allows roughly one request an hour.
2. They make at most one request per interval across all processes, using a shared on-disk cache and an exclusive lock.
3. They treat a 429 as a cooldown that is saved and shared, not as something to retry right away: they honor Retry-After or wait 5 minutes by default, and keep showing the last good data meanwhile.
4. They prefer the `rate_limits` data Claude Code already passes to statusline scripts on stdin.

By value, the best-built fetch paths are:

- **steipete/CodexBar**
- **Yeachan-Heo/oh-my-claudecode** (its HUD)
- **sirmalloc/ccstatusline**
- **jarrodwatts/claude-hud**, which once had the most careful fetch path of all and then dropped polling for stdin.

The ones that get 429'd poll fast, retry immediately, share nothing between processes, or send no Claude Code User-Agent.

## The map

### 1. The endpoint's behavior

**Request shape.** Every traced client sends `Authorization: Bearer <accessToken>` and `anthropic-beta: oauth-2025-04-20`; for example, ccstatusline's headers object is exactly `{ 'Authorization': \`Bearer ${token}\`, 'anthropic-beta': 'oauth-2025-04-20' }` ([usage-fetch.ts](https://raw.githubusercontent.com/sirmalloc/ccstatusline/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts)). The token is the Claude Code OAuth token, read from `~/.claude/.credentials.json` or the macOS Keychain item `Claude Code-credentials`.

**The rate limit is bucketed by User-Agent.** Five independent sources say so:
- **oh-my-claudecode commit bc29db0f0f18:** "The endpoint buckets its rate limit by User-Agent" and "Node sends no User-Agent, so every HUD usage poll landed in the bucket for unidentified clients ... which allows roughly one request an hour" ([commit](https://github.com/Yeachan-Heo/oh-my-claudecode/commit/bc29db0f0f18)).
- **Claude-Code-Usage-Monitor #202:** "With correct `User-Agent`: safe at 180-second intervals" / "Without correct `User-Agent`: instant persistent 429s" ([Claude-Code-Usage-Monitor#202](https://github.com/Maciek-roboblog/Claude-Code-Usage-Monitor/issues/202)).
- **usage-pill README:** "A client that does not name itself Claude Code is limited after a handful of calls and then stays limited for hours" ([usage-pill](https://github.com/kcao-gss/usage-pill)).
- **claude-hud commit 46b827a:** "User-Agent: claude-hud → 429 (always) - User-Agent: claude-code/2.1 → 200" (from the code trace, `unquoted` on a fetched page).
- **claude-plugins #329:** a fix that adds `User-Agent: claude-code/1.0.0` ([claude-plugins#329](https://github.com/artem-from-ua/claude-plugins/issues/329)).

**The limit is tied to the token.** "Rate limiting is per-access-token, not per-account" ([#202](https://github.com/Maciek-roboblog/Claude-Code-Usage-Monitor/issues/202)).

**Measured responses by User-Agent** (oh-my-claudecode's table):
- Header omitted: `429 | 348s`.
- `claude-code/2.1.232`: `403 | none - the endpoint's real answer` ([commit](https://github.com/Yeachan-Heo/oh-my-claudecode/commit/bc29db0f0f18)). Why a versioned User-Agent got 403 is unresolved.

**Retry-After is DISPUTED.**
- #30930 says the endpoint "returns HTTP 429 (Rate Limited) **persistently** for Claude Max ($200/month) users, with `retry-after: 0`" and "continues returning 429 indefinitely (tested over 5+ minutes with various intervals)" ([claude-code#30930](https://github.com/anthropics/claude-code/issues/30930)).
- oh-my-claudecode measured Retry-After of about 348 s ([commit](https://github.com/Yeachan-Heo/oh-my-claudecode/commit/bc29db0f0f18)).

**429 body:** `{"error":{"message":"Rate limited. Please try again later.","type":"rate_limit_error"}}` ([#30930](https://github.com/anthropics/claude-code/issues/30930)).

**No official statement.** No Anthropic staff reply appears on #30930. #31637 (backoff 30 s → 300 s still getting 429) was closed as not planned ([claude-code#31637](https://github.com/anthropics/claude-code/issues/31637)).

**Stated throughput.** The figures differ by bucket:
- claude-hud's code comment: `// 5 minutes — matches Anthropic usage API rate limit window` ([usage-api.ts](https://raw.githubusercontent.com/jarrodwatts/claude-hud/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts)).
- #202: 180 s is safe with the right User-Agent.
- ailine's README: "~5 requests per window" (from the inventory digger, `unquoted` in verification).

**Alternatives to polling.**
- Claude Code passes `rate_limits` on the statusline's stdin. claude-hud: "Claude Code must include subscriber `rate_limits` data on stdin for the current session" ([claude-hud](https://github.com/jarrodwatts/claude-hud)).
- #95918 (2026-09-21) reports the field as "consistently `null` / absent" in v2.1.278 ([claude-code#95918](https://github.com/anthropics/claude-code/issues/95918), from a digger, not re-checked).
- Claude-Usage-Tracker gave up on the endpoint: "CLI OAuth usage now fetched via Messages API rate limit headers (`anthropic-ratelimit-unified-*`) instead of the disabled `/api/oauth/usage` endpoint — uses a minimal Haiku request for near-zero cost" ([CHANGELOG](https://raw.githubusercontent.com/hamed-elfayome/Claude-Usage-Tracker/588775e4540757443691fa1bcb33457315e05f86/CHANGELOG.md)).

### 2. Inventory, ranked by value

Star counts are as quoted from each repo's GitHub page by the inventory digger.

| # | Repo | Stars | Type | Polls the endpoint? |
|---|---|---|---|---|
| 1 | [steipete/CodexBar](https://github.com/steipete/CodexBar) | 21.9k | macOS menu bar, many providers | yes, on a timer |
| 2 | [Yeachan-Heo/oh-my-claudecode](https://github.com/Yeachan-Heo/oh-my-claudecode) | 39.4k (whole framework) | HUD statusline | yes, gated by a per-render cache |
| 3 | [sirmalloc/ccstatusline](https://github.com/sirmalloc/ccstatusline) | 13k | statusline | yes, only for fields stdin lacks |
| 4 | [jarrodwatts/claude-hud](https://github.com/jarrodwatts/claude-hud) | 28.2k | statusline | removed in March 2026; now stdin only |
| 5 | [hamed-elfayome/Claude-Usage-Tracker](https://github.com/hamed-elfayome/Claude-Usage-Tracker) | 3.6k | macOS menu bar | only on the multi-profile path; otherwise a Haiku probe |
| 6 | [richhickson/claudecodeusage](https://github.com/richhickson/claudecodeusage) | 339 | macOS menu bar | yes, every 300 s |
| 7 | [ohugonnot/claude-code-statusline](https://github.com/ohugonnot/claude-code-statusline) | 12 | Bash statusline | yes, as a fallback |

Pollers seen only through their READMEs, not code-traced (inventory digger):
- [f-is-h/Usage4Claude](https://github.com/f-is-h/Usage4Claude) (398 stars): adaptive refresh, 1 → 3 → 5 → 10 min.
- [jens-duttke/usage-monitor-for-claude](https://github.com/jens-duttke/usage-monitor-for-claude) (300 stars): adaptive, 15 min when idle, backs off on rate-limit errors.
- [LightspeedDMS/claude-usage](https://github.com/LightspeedDMS/claude-usage) (6 stars): polls every 30 s.
- [lexfrei/ailine](https://github.com/lexfrei/ailine): 10-minute `usage_ttl`.
- [blind0wl/dms-ai-usage](https://github.com/blind0wl/dms-ai-usage): 90 s cache with stale fallback.
- [kcao-gss/usage-pill](https://github.com/kcao-gss/usage-pill): "Refresh every: 1 to 60 minutes, default 5."
- [Dann1y/claude-usage-monitor](https://github.com/Dann1y/claude-usage-monitor): 30 min.

Not pollers:
- Maciek-roboblog/Claude-Code-Usage-Monitor: "ccm currently calculates window boundaries from local JSONL files" ([#202](https://github.com/Maciek-roboblog/Claude-Code-Usage-Monitor/issues/202)).
- claude-powerline (reads stdin `rate_limits`), CCometixLine (transcript analysis) and soulduse/ai-token-monitor (local JSONL), per the inventory digger.

### 3. Fetch paths, from the code

**CodexBar** (@1d09b4d, [trace](https://github.com/steipete/CodexBar/tree/1d09b4d7428c58a177a5a33ba02135457ced2bc5))
- **Headers:** Authorization, `Accept`/`Content-Type: application/json`, `anthropic-beta: oauth-2025-04-20`, and `User-Agent: claude-code/<detected CLI version>`, falling back to `2.1.0`.
- **Interval:** one app-wide timer. Settings are manual or 60/120/300/900/1800 s. The default is adaptive: 2 min when the menu is active, stepping to 5, 15 and 30 min when idle, in Low Power Mode or under thermal pressure.
- **Cache and single-flight:** no response TTL cache, since it is a single process. The last good snapshot stays in memory. `guard !self.isRefreshing` skips overlapping refresh cycles.
- **429:** a per-token cooldown gate ([ClaudeOAuthUsageRateLimitGate.swift](https://raw.githubusercontent.com/steipete/CodexBar/1d09b4d7428c58a177a5a33ba02135457ced2bc5/Sources/CodexBarCore/Providers/Claude/ClaudeOAuth/ClaudeOAuthUsageRateLimitGate.swift)):
  - Retry-After is honored when present; otherwise `static let defaultCooldown: TimeInterval = 60 * 5`.
  - `let blockedUntil = max(existing ?? candidate, candidate)`.
  - It is keyed by `sha256Hex(Data(accessToken.utf8))` and persisted in UserDefaults, so it survives restarts.
  - `guard interaction != .userInitiated else { return nil }` lets a manual refresh through.
  - There is no exponential backoff.
- **Token refresh:** when Claude CLI owns the credentials, the refresh is handed to the `claude` CLI, with single-flight (`return .join(existing)`) and a 5-minute cooldown. CodexBar-owned credentials are refreshed directly with `grant_type=refresh_token` at `platform.claude.com/v1/oauth/token`.
- **Rate-limit docs:** none numeric. The changelog records "treat OAuth usage HTTP 429s as rate limits, preserve cached credentials, and back off background retries while still allowing manual refresh (#1179)".

**oh-my-claudecode HUD** (@9fd35ec, [usage-api.ts](https://raw.githubusercontent.com/Yeachan-Heo/oh-my-claudecode/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/usage-api.ts))
- **Headers:** Authorization, `anthropic-beta`, `Content-Type`. The User-Agent is `claude-code/${version}` only when stdin supplies a valid semantic version; otherwise the header is omitted.
- **Interval:** per render, gated by a 90 s success TTL (`DEFAULT_HUD_USAGE_POLL_INTERVAL_MS = 90 * 1000`, from the code trace) and `const CACHE_TTL_FAILURE_MS = 15 * 1000;`.
- **Cache:** an on-disk JSON file per source under `plugins/oh-my-claudecode/`, shared by every HUD process and keyed by a token hash. Stale data is capped by `const MAX_STALE_DATA_MS = 15 * 60 * 1000;`.
- **Single-flight:** a double-checked `withFileLock(lockPathFor(getCachePath(currentSource)), async () => { const cache = readCache(currentSource);` using an O_EXCL lock file. A process that loses the race serves stale data and does not fetch.
- **429:** exponential backoff `Math.min(normalizedPollIntervalMs * Math.pow(2, Math.max(0, count - 1)), MAX_RATE_LIMITED_BACKOFF_MS)` with `MAX_RATE_LIMITED_BACKOFF_MS = 5 * 60 * 1000`, so 90 → 180 → 300 s. It is tracked separately per User-Agent and cleared on success. **Retry-After is not read.**
- **Token refresh:** the HUD refreshes the token itself (`grant_type: 'refresh_token'` to `platform.claude.com` `/v1/oauth/token`) and writes the new tokens back to the Keychain or file.
- **Rate-limit docs:** a code comment says unidentified polls meant "only the first request of each hour ever reached the API." The file header says "Based on claude-hud implementation by jarrodwatts."
- **History:** [#1398](https://github.com/Yeachan-Heo/oh-my-claudecode/issues/1398) records the earlier failure mode, "Every retry gets 429 → writes error cache (15s TTL) → retries again → 429 spiral". The proposed fix was "1m → 2m → 4m → 8m → 16m → 32m (capped). Resets to 0 on success."

**ccstatusline** (@35440e4, [usage-fetch.ts](https://raw.githubusercontent.com/sirmalloc/ccstatusline/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts))
- **Headers:** only Authorization and `anthropic-beta`. **No User-Agent.**
- **Interval:** stdin `rate_limits` comes first; the API is called only for fields the active widgets need that stdin lacks. The gates are an in-memory cache, then an on-disk cache (`const CACHE_MAX_AGE = 180; // seconds`, by mtime, with an account fingerprint), then the lock deadline.
- **Cache:** `~/.cache/ccstatusline/usage.json`, shared across processes but not written atomically.
- **Single-flight:** advisory only. `fs.writeFileSync(LOCK_FILE, JSON.stringify({ blockedUntil, error }));` with `const LOCK_MAX_AGE = 30;   // rate limit: only try API once per 30 seconds`. It uses no O_EXCL open, so two renders at the same moment can both fetch.
- **429:** Retry-After is parsed as seconds or an HTTP-date, with `const DEFAULT_RATE_LIMIT_BACKOFF = 300; // seconds`, then `writeUsageLock(now + response.retryAfterSeconds, 'rate-limited');`. Every process therefore honors the cooldown. Stale data is served at any age. There is no exponential backoff.
- **Token refresh:** none. It reads the token only.

**claude-hud**
- **Now:** the fetch is gone. "ClaudeHUD is local-only by design. It does not make network requests, scrape credentials, or call undocumented Claude APIs." ([claude-hud](https://github.com/jarrodwatts/claude-hud)).
- **The last fetch path** (@c449c0b, [usage-api.ts](https://raw.githubusercontent.com/jarrodwatts/claude-hud/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts)):
  - `export const USAGE_API_USER_AGENT = 'claude-code/2.1';`
  - A shared cache file (`'.usage-cache.json'`) and an exclusive lock (`fs.openSync(lockPath, 'wx')`, `const CACHE_LOCK_STALE_MS = 30_000;`). The lock holder re-reads the cache before fetching; losers show stale data or wait up to 2 s.
  - `parseRetryAfterSeconds` for Retry-After, and otherwise backoff `Math.min(CACHE_RATE_LIMITED_BASE_MS * Math.pow(2, ...), CACHE_RATE_LIMITED_MAX_MS)`, running 60 s → 5 min. Last-good data is kept during the backoff.
  - The module default TTL is `const CACHE_TTL_MS = 5 * 60_000;`, but the config default `cacheTtlSeconds: 60` overrode it (code trace).
  - It never refreshed the token; an expired token returned null.
- **History** (code trace): the User-Agent was changed from `claude-hud/1.0` to `claude-code/2.1`, accidentally reverted, and restored. Polling was removed in #288 on 2026-03-23.

**Claude-Usage-Tracker** (@588775e, code trace)
- **Headers:** Authorization, `Content-Type`, `User-Agent: claude-code/2.1.5`, `anthropic-beta`.
- **Interval:** `refreshInterval ?? 30.0` s by default, adjustable from 10 to 300 s.
- **Cache and single-flight:** no response cache. `isRefreshing` is set but never checked, so refreshes can overlap.
- **429:** treated as a generic error, with no backoff in the refresh loop and no Retry-After. The endpoint is described as "disabled" in favor of the Haiku `/v1/messages` probe, which reads `anthropic-ratelimit-unified-*` headers even on 429s.
- **Token refresh:** refreshes via `platform.claude.com/v1/oauth/token`, but never while the CLI owns the token.

**claudecodeusage** (@bbbbe01, [UsageManager.swift](https://raw.githubusercontent.com/richhickson/claudecodeusage/bbbbe01ba2fd48de34926467a4cc8533cb0b5e3f/ClaudeUsage/UsageManager.swift))
- **Headers:** `claude-code/\(Self.claudeCodeVersion)` (falling back to `"2.1.0"`) and `anthropic-beta: oauth-2025-04-20`.
- **Interval:** a 300 s timer, plus refreshes at launch, on wake and on manual click.
- **Single-flight:** none.
- **429:** `code == 429 || code == 500 || code == 502 || code == 503` counts as retryable, via `refreshWithRetry(retriesRemaining: 5)` starting at `backoffSeconds: UInt64 = 2` and doubling. That is up to 6 requests in about 62 s. It never reads Retry-After.
- **Cache and token:** memory only; no token refresh.

**claude-code-statusline** (@d45b420, code trace)
- **Headers:** `curl` with Authorization, `anthropic-beta` and `Content-Type`. No User-Agent.
- **Interval:** `REFRESH_INTERVAL` 300 s. The API is a fallback when stdin lacks `rate_limits`, and is always used for the Sonnet weekly quota.
- **Cache:** an atomic `mktemp` + `mv` cache file.
- **Single-flight:** `flock -n`; losers exit and render the cache, but the cache age is not re-checked under the lock.
- **429:** the HTTP status is never read, so there is no backoff and no Retry-After. It retries every render until a call succeeds.
- **Token refresh:** none.

### 4. The failure record

- **Fast failure TTL.** oh-my-claudecode's 15 s failure TTL spiral, described above ([#1398](https://github.com/Yeachan-Heo/oh-my-claudecode/issues/1398)).
- **Missing User-Agent.** claude-plugins' statusline had a 60 s cache but no User-Agent; the fix added `User-Agent: claude-code/1.0.0` ([#329](https://github.com/artem-from-ua/claude-plugins/issues/329), from a digger).
- **Every 429 treated as a probe failure.** hive's ClaudeProber: "In our sampling about 5 of 6 requests got a 429". The proposal: a rate-limited state, a 15-minute last-good cache, Retry-After, and 5 → 10 → 20 min backoff with jitter ([hive#8721](https://github.com/hivecommons/hive/issues/8721), from a digger).
- **Duplicate requests from timer rescheduling.** Usage4Claude's smart refresh made "2–3 requests ... instead of one" per interval; the fix was one fixed 5-minute interval ([Usage4Claude#90](https://github.com/f-is-h/Usage4Claude/issues/90), from a digger).
- **UI re-checks every 5 s.** paperclip logged "80 requests over 6.5 minutes before encountering rate-limiting" ([paperclip#14096](https://github.com/paperclipai/paperclip/issues/14096), from a digger).
- **Token refresh compounding.** In pi, "Every subsequent request to the agent triggers another refresh attempt, which also hits the 429" ([pi#4621](https://github.com/earendil-works/pi/issues/4621), from a digger).
- **Retry-After done right.** dms-ai-usage PR #3 stores a `rate_limited_until` deadline from Retry-After, so later polls skip the live call ([dms-ai-usage#3](https://github.com/blind0wl/dms-ai-usage/pull/3), from a digger).

### 5. What separates the tools that avoid 429 from those that don't

1. **Identify as Claude Code.** A `User-Agent: claude-code/<semver>` header selects the generous bucket. Without it, a tool falls into an unidentified bucket of about one request an hour, and no interval or backoff will save it. This is the largest single factor, corroborated by five sources in section 1. oh-my-claudecode's detail: send a real semantic version taken from stdin, or omit the header rather than send a bare `claude-code`.
2. **Make one network call per interval across all processes.** Statuslines run one process per render, per window. The careful ones pair a shared on-disk cache with an exclusive (`wx`/O_EXCL) lock, then re-read the cache under the lock (claude-hud, oh-my-claudecode). Weaker designs:
   - an advisory lock (ccstatusline), which can double-fetch;
   - `flock` without a re-check (claude-code-statusline);
   - in-app refreshes that can overlap (Claude-Usage-Tracker, claudecodeusage).
3. **Make a 429 a saved, shared cooldown, not a retry.**
   - The good pattern, in CodexBar, ccstatusline and the old claude-hud: honor Retry-After (seconds or HTTP-date), default to about 5 minutes when it is absent, and store the deadline on disk or in UserDefaults, keyed per token. Every caller and every restart then respects it.
   - The anti-patterns: immediate short retries (claudecodeusage's 2 → 32 s), a 15 s failure TTL (early oh-my-claudecode), and never reading the status code (claude-code-statusline).
   - Because Retry-After has been reported as `0` (DISPUTED), always apply a floor.
4. **Serve last-good data during the cooldown.** CodexBar, oh-my-claudecode (up to 15 min), ccstatusline and claude-hud keep showing numbers, so there is no pressure to retry.
5. **Keep the interval long and let it adapt.** 90–300 s is the floor among the careful tools. CodexBar, Usage4Claude and usage-monitor-for-claude slow down when idle. Tools that failed polled at 30 s or 5 s, or fired duplicate requests.
6. **Poll less, or not at all.** claude-hud dropped the endpoint for stdin `rate_limits`, and ccstatusline asks the API only for fields stdin lacks. Claude-Usage-Tracker moved to Messages API rate-limit headers. The stdin field has a reported null regression in v2.1.278.
7. **Don't let token refresh multiply requests.** CodexBar hands CLI-owned refreshes to the CLI with single-flight, and Claude-Usage-Tracker never refreshes a token the CLI owns. pi shows the compounding failure.
8. **Key everything by token.** Limits are per access token (#202). CodexBar, oh-my-claudecode and ccstatusline hash the token into their cache or cooldown keys, so multiple accounts don't poison each other.

## Coverage

- Endpoint behavior: settled. The version that introduced stdin `rate_limits` and the `get_usage` control request are unverified.
- Inventory and ranking: settled.
- Code deep-dives of the top repos: settled. Seven repos were traced.
- Failure record: settled.
- Patterns: settled.

Digging ended: every sub-area settled (after round 2).

tracer-rr result files:
- tracer-rr/steipete-CodexBar-2026-09-26.md
- tracer-rr/jarrodwatts-claude-hud-2026-09-26.md
- tracer-rr/Yeachan-Heo-oh-my-claudecode-2026-09-26.md
- tracer-rr/sirmalloc-ccstatusline-2026-09-26.md
- tracer-rr/hamed-elfayome-Claude-Usage-Tracker-2026-09-26.md
- tracer-rr/richhickson-claudecodeusage-2026-09-26.md
- tracer-rr/ohugonnot-claude-code-statusline-2026-09-26.md

## Verification

54 facts were checked on 12 source pages; 52 were confirmed.

- **NOT ON PAGE:** "oh-my-claudecode #1398 shows stale data with an [API 429] indicator". It was dropped from the map.
- **Returned NO as expected:** "an Anthropic staff member replied on #30930". The absence is kept as a finding.
- **UNCHECKED:** none.

## Open rabbit holes

- Why a versioned `claude-code/2.1.232` User-Agent got 403 in oh-my-claudecode's measurement (token scope? a rejected version?): dug, unsettled.
- Retry-After of 0 versus about 348 s, and whether it has changed over time: dug, unsettled.
- The Claude Code version that introduced stdin `rate_limits` (said to be v2.1.80, from a search snippet only) and the v2.1.278 null regression (#95918): dug, unsettled.
- Claude Code's `get_usage` control request, cited in the claude-hud README: dug, unsettled.
- claude-hud issue #173 and PRs #193 and #288: dug, unsettled (the pages did not yield the content).
- Code traces of Usage4Claude, usage-monitor-for-claude and CodeZeno/Claude-Code-Usage-Monitor (adaptive and backoff claims seen in READMEs only): undug.
- hive PR #8748, whether its proposed fix landed: undug.
- CodexBar issue #1844, where the Keychain `Claude Code-credentials` item holds only MCP OAuth state: undug.
- Headers Claude Code itself sends on its own `/usage` call, beyond reports that its statusline template sends only Authorization and `anthropic-beta`: dug, unsettled.
