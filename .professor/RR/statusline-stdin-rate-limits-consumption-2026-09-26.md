# RR — How statusline tools consume Claude Code's stdin `rate_limits` and merge it with /api/oauth/usage

Question: How do Claude Code statusline tools consume the `rate_limits` object Claude Code passes to a statusline command on stdin? Establish from code and Claude Code's own docs or changelog: its exact JSON shape (window keys such as five_hour, seven_day and any model-scoped entries; `used_percentage` vs other names; `resets_at` as epoch seconds or ISO), which Claude Code versions send it, when it is absent or null (the reported v2.1.278 null regression) and how tools detect that; and how tools that ALSO call GET https://api.anthropic.com/api/oauth/usage merge the two sources per account — which source wins, freshness rules, fallback order, and how they avoid calling the endpoint at all. Read it from the code of claude-hud (including when and why it switched to stdin), ccstatusline, claude-powerline and any other repository that parses stdin rate_limits.

Since v2.1.80, Claude Code has sent `rate_limits` with windows `five_hour`, `seven_day` and, behind a gateway, `spend_limit`. Each window carries `used_percentage` (0-100) and `resets_at` in Unix epoch seconds. Claude Code leaves out an empty window rather than sending null, and drops the whole object when no window has data, which is what issue #95918 reports on v2.1.278 (still open). Every tool that also reads the OAuth usage endpoint prefers stdin: it calls the endpoint only for fields stdin lacks, under a cache TTL, a lock and a 429 backoff. claude-hud went further and removed the endpoint path entirely on 2026-03-23, and claude-powerline never had one.

## Map

### 1. The official stdin shape
- Field table: `rate_limits.five_hour.used_percentage` / `seven_day.used_percentage` = "Percentage of the 5-hour or 7-day rate limit consumed, from 0 to 100"; `resets_at` = "Unix epoch seconds when the 5-hour or 7-day rate limit window resets" ([docs](https://code.claude.com/docs/en/statusline), read via [r.jina.ai proxy](https://r.jina.ai/https://code.claude.com/docs/en/statusline)).
- "Behind a Claude apps gateway with spend limits, `rate_limits` carries `spend_limit` with the same two fields for the spend limit that applies to you" ([docs](https://r.jina.ai/https://code.claude.com/docs/en/statusline)).
- Presence: "only present for claude.ai Pro and Max subscribers, or behind a Claude apps gateway with spend limits, and only after the first API response"; "Each window (`five_hour`, `seven_day`, `spend_limit`) may be independently absent, and Claude Code drops a window once its `resets_at` time passes" ([docs](https://r.jina.ai/https://code.claude.com/docs/en/statusline)).
- The docs page does not mention `model_scoped`, `seven_day_sonnet` or `seven_day_opus` (per the digger's read of the page; `unquoted`, since a missing term cannot be quoted).
- How Claude Code builds the object, from the bundled source quoted in [issue #92081](https://github.com/anthropics/claude-code/issues/92081): `...go.five_hour && { five_hour: { used_percentage: go.five_hour.utilization*100, resets_at: go.five_hour.resets_at } }`, the same for `seven_day`, and `...Ie() === "gateway" && go.overage && { spend_limit: ... }`. The internal source therefore uses `utilization` (a fraction) and is renamed to `used_percentage` on the way to stdin.
- Model-scoped entries are not part of the documented stdin contract, but they do occur in adjacent surfaces:
  - The `get_usage` control-request reply on 2.1.280 carries `"five_hour": { "utilization": 9, "resets_at": "2026-09-23T09:49:59.956245+00:00" }`, `"seven_day_opus": null, "seven_day_sonnet": null` and a `limits` array holding a `weekly_scoped` entry scoped to model `"display_name": "Fable"` ([qodeca/xezar#906](https://github.com/qodeca/xezar/issues/906)). That surface uses `utilization` as a whole-number percentage and an ISO `resets_at`.
  - claude-hud types an undocumented stdin `rate_limits.model_scoped: Array<{display_name, utilization, resets_at: string}>` and parses `resets_at` there as ISO ([types.ts#L40-L60](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/types.ts#L40-L60), [stdin.ts#L408-L444](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/stdin.ts#L408-L444)).
  - ccstatusline's zod schema accepts stdin `seven_day_sonnet` / `seven_day_opus`, both nullable ([StatusJSON.ts#L74-L80](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/types/StatusJSON.ts#L74-L80)).
- The OAuth endpoint's shape, seen through consumer code: CCometixLine deserializes `/api/oauth/usage` into `struct UsagePeriod { utilization: f64, resets_at: Option<String>, }` ([usage.rs](https://raw.githubusercontent.com/Haleclipse/CCometixLine/master/src/core/segments/usage.rs)). The endpoint "returns `null` (not `undefined`) for the `five_hour` and `seven_day` rate-limit buckets on pay-as-you-go plans" ([ccstatusline PR #375](https://github.com/sirmalloc/ccstatusline/pull/375)). Tools that merge the two sources therefore convert ISO strings to epoch seconds, or the reverse.

### 2. Versions, absence and the null regression
- v2.1.80: "Added `rate_limits` field to statusline scripts for displaying Claude.ai rate limit usage (5-hour and 7-day windows with `used_percentage` and `resets_at`)", released 19 Mar ([release v2.1.80](https://github.com/anthropics/claude-code/releases/tag/v2.1.80)). ccstatusline's commit 3ff7cb7 (2026-03-20), "use native rate_limits field from Claude Code 2.1.80", independently cites the same version ([commit tree](https://github.com/sirmalloc/ccstatusline/blob/3ff7cb75028f423e34077b598b630f6bb83899f2/src/types/StatusJSON.ts#L17-L68)). The `main` CHANGELOG.md no longer reaches back that far.
- Omission, not null: "each window inside `rate_limits` is emitted through a conditional spread. When a window has no data, the key is omitted from the object rather than emitted with a `null` value." A status line therefore "cannot distinguish" 0% used from not yet known. Observed on CLI 2.1.260: `"rate_limits": { "seven_day": { "used_percentage": 47, "resets_at": 1788606000 } }`, with `five_hour` absent ([#92081](https://github.com/anthropics/claude-code/issues/92081)).
- The v2.1.278 regression: "The `rate_limits` field is consistently `null` / absent in the JSON payload sent to both statusLine commands and hook scripts. The comment in the generated `statusline.sh` template says this works in v2.1.251+, but it does not appear in v2.1.278." The reporter adds: "`rate_limits` is not present in the payload at all (not null — simply absent)." The issue is open with no fix version ([#95918](https://github.com/anthropics/claude-code/issues/95918)).
- Enterprise, 2.1.281: "The statusLine stdin JSON has no `rate_limits`, `rate_limits_available` or `subscription_type` keys, including after a completed assistant response", while `/usage` showed 84% used. Open ([#96543](https://github.com/anthropics/claude-code/issues/96543)).
- How tools detect absence: all of them test the object or window for falsiness, so missing and null are treated alike:
  - claude-hud: `if (!rateLimits) return null`.
  - ccstatusline: `.nullable().optional()` plus `!rateLimits`.
  - claude-powerline: `if (!fiveHour)`.
  - claudia-statusline: typed `Option`.
  - ohugonnot's script: `!= "null"` and non-empty checks.

  The tracer-rr links for each are in sections 3-6.

### 3. claude-hud (jarrodwatts/claude-hud @ 939eb66)
- Parse: `getUsageFromStdin` clamps and rounds `used_percentage`, reads `resets_at` as epoch seconds (`new Date(value * 1000)`) and rejects values that are not numbers or are ≤0 ([stdin.ts#L363-L400](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/stdin.ts#L363-L400)). A partial object keeps the windows that are present; the result is null only when both percentages are null and there is no `model_scoped` array ([stdin.ts#L379-L391](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/stdin.ts#L379-L391)).
- Fallback today: a fresh local snapshot file (`display.externalUsagePath`) or no usage display. When stdin is present, the snapshot only fills `balanceLabel`, a missing `sevenDay` and missing `scopedWindows` ([index.ts#L141-L179](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/index.ts#L141-L179)). HEAD has no `oauth/usage` call, and the README says it "never falls back to credential scraping or undocumented API calls" ([README.md#L345-L358](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/README.md#L345-L358)).
- The switch, all on 2026-03-23:
  - 60cf0c6, "prefer stdin rate limits for usage display".
  - 738a0ce, which refined the stdin fallback.
  - 3aebe1b, "Simplify usage display to stdin only (#288)", which deleted `src/usage-api.ts`.

  CHANGELOG 0.0.10 says it "prefers … stdin `rate_limits` … still falls back to the existing OAuth/cache path", alongside fixes for repeated 429s. 0.0.12 (2026-04-04) says "Background OAuth usage polling, related cache/lock behavior … were removed" ([CHANGELOG.md#L227-L255](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/CHANGELOG.md#L227-L228)). The reason, per that changelog, was the endpoint's repeated 429s; an earlier 429 fix was a089537 (#207, 2026-03-14). The endpoint path itself was added 2026-01-07 in eb18f786 (#35). A stale compiled `dist/usage-api.js` stayed in the repository until 2b816f7 on 2026-07-15.
- The removed merge logic, at c449c0b:
  - Stdin won, and the endpoint was called only when stdin usage was null ([index.ts#L76-L93](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/index.ts#L76-L93)).
  - The request was GET `/api/oauth/usage` with `anthropic-beta: oauth-2025-04-20` ([usage-api.ts#L1026-L1042](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts#L1026-L1042)).
  - Results went to one `.usage-cache.json` plus a lock. The TTL was 5 min on success and 15 s on failure; a 429 triggered exponential backoff from 60 s to 5 min, honouring Retry-After and showing the last good values ([L47-L77](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts#L47-L77), [L376-L472](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts#L376-L472)).
  - It skipped the call on a non-Anthropic `ANTHROPIC_BASE_URL`, missing credentials or no resolvable plan.
  - Per account: only the macOS keychain service name was keyed to the config dir, `-{sha256(configDir)[:8]}`, and the cache was a single file ([L544-L582](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts#L544-L582)).
- No minimum Claude Code version is stated for `rate_limits`.

### 4. ccstatusline (sirmalloc/ccstatusline @ 35440e4)
- Schema: `rate_limits: z.object({ five_hour, seven_day, seven_day_sonnet, seven_day_opus }).nullable().optional()`. Each window is `{ used_percentage, resets_at }`, both nullable and coerced from numeric strings, and `resets_at` is commented `// Unix epoch seconds` ([StatusJSON.ts#L3-L20](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/types/StatusJSON.ts#L3-L20)). `extractUsageDataFromRateLimits` converts epoch seconds to ISO with `new Date(epochSeconds * 1000).toISOString()` to match the API's format. A null Sonnet or Opus bucket is left unset so the API gets asked ([usage-prefetch.ts#L167-L213](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-prefetch.ts#L167-L213)).
- Which source wins: stdin. `mergeUsageData` spreads the API fields first and the stdin fields over them ([usage-prefetch.ts#L159-L165](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-prefetch.ts#L159-L165)).
- Avoiding the call: `prefetchUsageDataIfNeeded` computes which fields the active widgets need and still lack, and fetches only for those. There is no call when no usage widget is configured or stdin already covers everything. When only reset-timer fields were missing, an API error is suppressed ([L55-L61](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-prefetch.ts#L55-L61), [L215-L235](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-prefetch.ts#L215-L235)). The Fable weekly bucket is API-only.
- Freshness:
  - Cache: in memory plus `~/.cache/ccstatusline/usage.json`, 180 s TTL, with a 30 s throttle in `usage.lock` ([usage-fetch.ts#L22-L36](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L22-L36)).
  - Request: 5 s timeout, `anthropic-beta: oauth-2025-04-20`, honours `HTTPS_PROXY` ([L722-L740](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L722-L740)).
  - 429: blocks fetching for `Retry-After`, or 300 s by default, capped at 24 h ([L768-L783](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L768-L783)).
  - Errors: serves the stale cache ([L795-L897](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L795-L897)).
- Per account: the cache is stamped with a 16-character sha256 of the refresh token (or the access token), so switching accounts invalidates it ([L258-L289](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L258-L289)). Credentials come from `$CLAUDE_CONFIG_DIR/.credentials.json`, or on macOS from the keychain item `Claude Code-credentials` with a `-sha256(CLAUDE_CONFIG_DIR)[:8]` suffix for non-default profiles ([L563-L610](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L563-L610)).
- History, from commit messages and partial diffs:
  - 3ff7cb7 (2026-03-20): native `rate_limits`.
  - 1739572 (2026-05-13): per-widget partial merge.
  - #375: null API buckets on pay-as-you-go plans caused "a `parse-error` lock, blocking all subsequent retries for 30 seconds", fixed with `.nullable().optional()` ([PR #375](https://github.com/sirmalloc/ccstatusline/pull/375)).
  - #434: stopped refetching every render on Enterprise accounts with no windows.
  - #460 / #536: the token-hash cache check.
  - #573: reads the `CLAUDE_CONFIG_DIR` keychain item first.
  - Nothing mentions v2.1.278.

### 5. claude-powerline (Owloops/claude-powerline @ 9e9b17f)
- Stdin only. `rate_limits` is typed `five_hour?` / `seven_day?` of `{ used_percentage: number; resets_at: number }`, with no model-scoped keys ([claude.ts#L46-L55](https://github.com/Owloops/claude-powerline/blob/9e9b17f9c05b91899c517b32ee7d5bf6e7f5ce36/src/utils/claude.ts#L46-L55)). It uses `resets_at` as epoch seconds (`epochSeconds * 1000 - Date.now()`, [formatters.ts#L166-L168](https://github.com/Owloops/claude-powerline/blob/9e9b17f9c05b91899c517b32ee7d5bf6e7f5ce36/src/utils/formatters.ts#L166-L168)).
- Absence: `if (!fiveHour)` / `if (!sevenDay) return null` hides the segment ([block.ts#L14-L18](https://github.com/Owloops/claude-powerline/blob/9e9b17f9c05b91899c517b32ee7d5bf6e7f5ce36/src/segments/block.ts#L14-L18)). The README says "Hidden when native data is unavailable" ([README#L342](https://github.com/Owloops/claude-powerline/blob/9e9b17f9c05b91899c517b32ee7d5bf6e7f5ce36/README.md#L342)). A window present with null fields is not guarded.
- There is no HTTP client and no `/api/oauth/usage` call. Commit 03e65d6 (v1.23.0, 2026-04-02) added native `rate_limits` with a transcript-parsing fallback; 6697e5d (v1.24.4, 2026-04-08) removed that fallback, using "native rate limits only" ([CHANGELOG#L140-L145](https://github.com/Owloops/claude-powerline/blob/9e9b17f9c05b91899c517b32ee7d5bf6e7f5ce36/CHANGELOG.md#L140-L145)).

### 6. Other parsers
- ohugonnot/claude-code-statusline (bash, [statusline.sh](https://raw.githubusercontent.com/ohugonnot/claude-code-statusline/main/statusline.sh)):
  - Parse: `(.rate_limits.five_hour.used_percentage // "")`; `HAVE_STDIN_SESSION=1` when the value is non-empty and `!= "null"`.
  - Avoiding the call: `[ "$HAVE_STDIN_SESSION" = 1 ] && [ "$SHOW_WEEKLY" != "1" ] && NEED_API=0`.
  - Otherwise it runs `curl -s --max-time 3 "https://api.anthropic.com/api/oauth/usage" ... -H "anthropic-beta: oauth-2025-04-20"`, with `REFRESH_INTERVAL` defaulting to 300.
  - Stdin epoch values are used directly; cached ISO values go through `iso_to_epoch`.
  - The cache is a single global file with no account key.
- itsPG/claude-code-statusline (bash, [statusline.sh](https://raw.githubusercontent.com/itsPG/claude-code-statusline/main/statusline.sh)):
  - Per-account cache: `USAGE_FILE="${USAGE_FILE%.json}-acct-${ACCOUNT_HASH}.json"`, with the hash built from `.oauthAccount` `accountUuid:organizationUuid`.
  - Avoiding the call: `NEED_API=0` only when the 5h value is present, the 7d value is present (or weekly is not shown), and Extra and Fable display are off.
  - Guard comment: "Leave the epoch empty when no reset is sent — num("") is "0", which would read as a past reset and wrongly zero the live percentage."
  - Token fallback: the macOS keychain item `Claude Code-credentials`.
- hagan/claudia-statusline (Rust, [models.rs](https://raw.githubusercontent.com/hagan/claudia-statusline/main/src/models.rs)):
  - Types: `pub five_hour: Option<RateLimitWindow>, pub seven_day: Option<RateLimitWindow>,`, with `pub used_percentage: Option<f64>, pub resets_at: Option<i64>,`.
  - Doc comment: "Absent for API-key usage and before the first API response in a session."
  - The digger found no `/api/oauth/usage` call in the crate's provider and display files (`unquoted`).
- Haleclipse/CCometixLine (Rust, [usage.rs](https://raw.githubusercontent.com/Haleclipse/CCometixLine/master/src/core/segments/usage.rs)): the opposite pattern, API only. It calls `format!("{}/api/oauth/usage", api_base_url)` with a cache duration defaulting to 300 s (`.unwrap_or(300)`). The verification check found no stdin `rate_limits` read in this file.

## Coverage
- Official stdin shape: settled.
- Versions, absence and null regression: settled.
- claude-hud: settled. Result file: `tracer-rr/jarrodwatts-claude-hud-2026-09-26-3.md`
- ccstatusline: settled. Result file: `tracer-rr/sirmalloc-ccstatusline-2026-09-26-2.md`
- claude-powerline: settled. Result file: `tracer-rr/Owloops-claude-powerline-2026-09-26.md` (its "Not read" note about `git log -S` is stale: the search later finished and found no regression commits).
- Other parsers: settled.

Digging ended: every sub-area settled (after round 2).

## Verification
39 facts checked on 11 source pages. 38 were confirmed by a quoted sentence or code line. One came back NO: CCometixLine usage.rs reading stdin `rate_limits`, which supports the map's claim that it does not. None are NOT ON PAGE, and none are UNCHECKED. The tracer-rr code facts are pinned to commits and were not re-fetched.

## Rabbit holes left open
- Whether Claude Code has ever actually emitted `model_scoped`, `seven_day_sonnet` or `seven_day_opus` on statusline stdin (claude-hud and ccstatusline both type them; the docs do not). Feature requests #91920, #73770 and #88111 were named but not read. Dug, unsettled.
- The fix version for #95918 (v2.1.278) and #96543 (v2.1.281): both are open, and the regression's root cause is not found. Dug, unsettled.
- The Claude Code version that added `spend_limit`: only the statusline.sh template comment's "v2.1.251+", quoted in #95918, speaks to it. Undug.
- The raw `/api/oauth/usage` response body from a primary source: seen here only through consumer types, the `get_usage` reply and #92081's internal object. Issues #31021 and #34346 were named but never located. Dug, unsettled.
- Whether `utilization` is a fraction or a percentage: #92081's code multiplies it by 100, but the `get_usage` reply in xezar#906 shows `9`. The scale may differ per surface. Undug.
- ccusage: whether its statusline reads `rate_limits` is known only from a DeepWiki summary, not its source. Undug.
- claude-hud's stale `dist/usage-api.js`, kept until 2026-07-15: whether any build still called the endpoint after March. Undug.
- The `get_usage` control request: a credential-free official usage source that claude-hud's README names. Its full schema is unread. Undug.
