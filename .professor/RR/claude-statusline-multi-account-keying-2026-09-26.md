# RR — How Claude Code usage/statusline tools attribute sessions to accounts across CLAUDE_CONFIG_DIRs and key their caches, locks and credentials

Question: How do Claude Code usage and statusline tools decide which account a statusline session or usage fetch belongs to when several Claude Code config directories (CLAUDE_CONFIG_DIR) run on one machine, and how do they key their per-account usage caches, locks and credentials? Establish it from the code of jarrodwatts/claude-hud, sirmalloc/ccstatusline, steipete/CodexBar, hamed-elfayome/Claude-Usage-Tracker and Yeachan-Heo/oh-my-claudecode, including how each one's keying changed over its commit history, and what a tool does when it cannot tell the accounts apart.

## Answer

The four in-session tools (claude-hud, ccstatusline, CodexBar, oh-my-claudecode) take the account only from the process's `CLAUDE_CONFIG_DIR` (falling back to `~/.claude`) and never from a stdin field. Claude-Usage-Tracker's statusline always shows its active profile's usage from one global cache. Keys differ per tool:

- **claude-hud:** caches under the config-dir path.
- **oh-my-claudecode:** caches under the config-dir path, per provider.
- **ccstatusline:** keeps one global `usage.json`/`usage.lock` guarded by a token-hash field inside it.
- **CodexBar:** keys by a hash of the credentials-file path plus account UUID.
- **Claude-Usage-Tracker:** keys secrets by profile UUID.

When accounts cannot be told apart, the tools typically fall back to a shared cache, the default or newest Keychain item, or an "unavailable" identity rather than failing.

## Map

### claude-hud ([tracer-rr @ 939eb66](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/claude-config-dir.ts#L13-L27))

- **Resolution:** the account comes from `process.env.CLAUDE_CONFIG_DIR?.trim()`, with `~` expanded and the path resolved, else `~/.claude`. All HUD state lives in `{configDir}/plugins/claude-hud` ([claude-config-dir.ts#L13-L27](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/claude-config-dir.ts#L13-L27)). No stdin field or transcript path identifies the account. The account label comes from `oauthAccount` in `${configDir}.json` ([auth.ts#L218-L248](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/auth.ts#L218-L248)).
- **Usage today:** only stdin `rate_limits`, plus an external snapshot as fallback; there is no OAuth fetch ([index.ts#L141-L156](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/index.ts#L141-L156), [CHANGELOG#L227-L228](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/CHANGELOG.md#L227-L228)).
- **Credentials before removal (c449c0b):**
  - Keychain `Claude Code-credentials` for `~/.claude`, else `Claude Code-credentials-{sha256(normalized dir)[:8]}`, also trying a hash of the raw env string and finally the plain name ([usage-api.ts#L544-L582](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts#L544-L582)).
  - Lookups were account-scoped (`-a {os user}`) first ([#L601-L663](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts#L601-L663)).
  - Then `{configDir}/.credentials.json` ([#L798-L832](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts#L798-L832)).
- **Cache and lock keys:** all by config-dir path.
  - Formerly `.usage-cache.json`, `.usage-cache.lock` and `.keychain-backoff` ([usage-api.ts#L98-L104](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts#L98-L104)).
  - Now an auth cache, a daily-cost ledger keyed by session_id ([daily-cost.ts#L55-L57](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/daily-cost.ts#L55-L57)), and a config cache keyed by sha256 of `{cwd, claudeConfigDir}` ([config-reader.ts#L198-L202](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/config-reader.ts#L198-L202)).
  - Per-dir `claude-hud.json` sits outside `plugins/` because the README says: "If you run several Claude config directories via `CLAUDE_CONFIG_DIR` and symlink `plugins/` to a shared location, `plugins/claude-hud/config.json` is the same physical file for all of them. Put per-directory settings in `$CLAUDE_CONFIG_DIR/claude-hud.json` instead." ([README](https://github.com/jarrodwatts/claude-hud), [config.ts#L409-L421](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/src/config.ts#L409-L421))
- **History:**
  - eb18f78 (2026-01-07): usage API, cache hardcoded to `~/.claude/plugins/claude-hud`.
  - c36738d (2026-01-13): fixed Keychain name `Claude Code-credentials`.
  - 9ae17fb (2026-03-03, #160): "consolidate CLAUDE_CONFIG_DIR handling and keychain fallback". Moved cache, backoff and credentials under the config dir and added hashed Keychain names ([CHANGELOG#L278-L291](https://github.com/jarrodwatts/claude-hud/blob/939eb66485832dead1b0a28a954f76f7aa2bdb06/CHANGELOG.md#L278-L291)).
  - 67ddceb (03-06): lock file.
  - e8d6492 (03-13): TTL, last-good data, 429 backoff.
  - d4abb17 (03-14): account-scoped Keychain lookup.
  - 3aebe1b (03-23, #288): deleted `usage-api.ts`; usage now from stdin only.
  - Later: auth cache (08-02/08-04), per-dir `claude-hud.json` (08-18), daily ledger (08-28).
- **When accounts cannot be told apart:**
  - Dirs that symlink a shared `plugins/` share every cache in it. Before #288 that included the usage cache and lock.
  - A custom dir with no Keychain item of its own fell back to the plain default service, so it used the default account's token and cached the result under its own dir. A test forbids that fallback once an account-scoped entry exists but is unusable ([test#L362-L395](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/tests/usage-api.test.js#L362-L395)).
  - On a 429 it kept the last good data with a "syncing" hint ([usage-api.ts#L432-L472](https://github.com/jarrodwatts/claude-hud/blob/c449c0ba29808a8f893f9b0a5ed97064ba54bd2c/src/usage-api.ts#L432-L472)).

### ccstatusline ([tracer-rr @ 35440e4](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/claude-settings.ts#L87-L133))

- **Resolution:** `path.resolve(process.env.CLAUDE_CONFIG_DIR)` when the variable is set and points at a directory or a nonexistent path; otherwise `~/.claude` ([claude-settings.ts#L87-L133](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/claude-settings.ts#L87-L133)). No stdin field selects the account.
- **Credentials:**
  - Non-macOS: `{configDir}/.credentials.json`.
  - macOS with `CLAUDE_CONFIG_DIR` set: Keychain `Claude Code-credentials-{sha256(raw NFC env value)[:8]}`, overridable by `CLAUDE_SECURESTORAGE_CONFIG_DIR`, falling back only to that profile's own file ([usage-fetch.ts#L563-L610](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L563-L610)).
  - Unset: the plain service first, then suffixed `Claude Code-credentials*` items found by `security dump-keychain`, newest first ([#L483-L493](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L483-L493), [#L531-L545](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L531-L545)).
- **Cache and lock keys:**
  - Global per OS user: `~/.cache/ccstatusline/usage.json` and `usage.lock`, with no config-dir component ([usage-fetch.ts#L22-L28](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L22-L28)).
  - Identity is a `tokenHash` field inside the cache: sha256[:16] of the refresh token, else the access token ([#L258-L289](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L258-L289)).
  - The lock holds only `{blockedUntil, error}` ([#L616-L635](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L616-L635)).
  - Only the block-timer cache is keyed by config dir: `block-cache-${sha256(path)[:16]}.json` ([jsonl-cache.ts#L35-L55](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/jsonl-cache.ts#L35-L55)).
  - Stdin `rate_limits` are used first ([usage-prefetch.ts#L215-L235](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-prefetch.ts#L215-L235)).
- **History:**
  - 9f26a07 (2025-10-08, #99): `CLAUDE_CONFIG_DIR` support.
  - a03b7c2 (2026-03-02, #168): global `usage.{json,lock}`. macOS hardcoded the plain Keychain service ([usage.ts@a03b7c2#L157-L180](https://github.com/sirmalloc/ccstatusline/blob/a03b7c223831110ebe90b6dfb2c72abb4aa54a83/src/utils/usage.ts#L157-L180)).
  - dfa009e (03-08, #204): 429 `blockedUntil`.
  - 5b49868 (03-15): suffixed-item scan.
  - 3ff7cb7 (03-20): stdin `rate_limits`.
  - 151521c (06-16, #460): `tokenHash` of the access token.
  - 0551b06 (09-17, #573, refs #521): per-config-dir hashed Keychain service. Before it, macOS ignored the config dir ([@0551b06^#L536-L544](https://github.com/sirmalloc/ccstatusline/blob/06786da2a68be335903f8227bcac758ddf65fc1c/src/utils/usage-fetch.ts#L536-L544)).
  - 282c5a3 (09-18, #536): fingerprint moved to the refresh token, because access-token refreshes were misread as account switches.
  - Cache and lock paths are unchanged from a03b7c2 through HEAD.
- **When accounts cannot be told apart:**
  - With no credential identity, `return true`: whatever the cache holds is served ([#L282-L289](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/usage-fetch.ts#L282-L289)).
  - One account's lock or 429 backoff blocks every account. Tests show a foreign-hash cache yields `timeout` or `rate-limited` rather than the other account's data ([usage-fetch.test.ts#L1023-L1092](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/__tests__/usage-fetch.test.ts#L1023-L1092)).
  - On an unset-dir macOS default profile, the newest-first scan can pick up another profile's suffixed item ([usage-token.test.ts#L279-L302](https://github.com/sirmalloc/ccstatusline/blob/35440e4a93ac8aba7e57973ac004a68adcc51089/src/utils/__tests__/usage-token.test.ts#L279-L302)).
  - Unverified inference: two concurrent profiles overwrite each other's `usage.json` and keep invalidating each other's cache.

### CodexBar ([tracer-rr @ 5890d05](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeConfigPaths.swift#L3-L17))

- **Resolution:**
  - One literal `CLAUDE_CONFIG_DIR` from the fetch environment, else `$HOME/.claude`, with no `~` expansion ([ClaudeConfigPaths.swift#L3-L17](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeConfigPaths.swift#L3-L17), [#L121-L133](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeConfigPaths.swift#L121-L133)).
  - Account UUID from `<root>/.config.json` or `.claude.json` ([#L65-L80](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeConfigPaths.swift#L65-L80)).
  - Extra accounts come from pasted tokens injected as env vars ([ClaudeProviderDescriptor.swift#L50-L59](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeProviderDescriptor.swift#L50-L59)) or from claude-swap (`cswap`) slots ([ClaudeSwapAccountReader.swift#L45-L69](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeSwap/ClaudeSwapAccountReader.swift#L45-L69)).
- **Credentials:**
  - `.credentials.json` under `CLAUDE_SECURESTORAGE_CONFIG_DIR` or the root ([#L82-L96](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeConfigPaths.swift#L82-L96)).
  - Keychain only under the fixed `Claude Code-credentials`, newest item first, with no hashed per-dir variant ([ClaudeOAuthCredentials.swift#L2374-L2410](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeOAuth/ClaudeOAuthCredentials.swift#L2374-L2410)).
  - Reading the Keychain requires opt-in ([#L2977-L2985](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeOAuth/ClaudeOAuthCredentials.swift#L2977-L2985)).
- **Keys:**
  - Profile id = SHA-256 of `codexbar:claude-oauth-cache-profile:v1\0` + the credentials-file path ([#L3093-L3114](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeOAuth/ClaudeOAuthCredentials.swift#L3093-L3114)).
  - Cache key `claude.profile.<id>` ([#L2868-L2872](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeOAuth/ClaudeOAuthCredentials.swift#L2868-L2872)).
  - Active-account hash `claude:active-account:v3:<profileId>:<uuid>` ([UsageStore+ClaudeActiveAccountIdentity.swift#L142-L158](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBar/UsageStore+ClaudeActiveAccountIdentity.swift#L142-L158)).
  - Owner `claude-owner-v1:` + SHA-256(uuid or email, org uuid) ([ClaudeVerifiedAccountOwner.swift#L3-L16](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeVerifiedAccountOwner.swift#L3-L16)).
  - claude-swap retained usage keyed by SHA-256(slot + NUL + email) ([ClaudeSwapRetainedUsageStore.swift#L63-L68](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeSwap/ClaudeSwapRetainedUsageStore.swift#L63-L68)).
- **History:**
  - dfdb15e (2025-12-21): first `CLAUDE_CONFIG_DIR` use (ccusage scanner).
  - 9a00284 (2026-07-05, #1903): history ownership checks and the `claude:active-account` hash.
  - e17ba24 (07-28): "Stop reading Claude-owned credentials".
  - cf16f25 (07-28): "scope Claude OAuth cache by profile". Before it, one global cache key and a hardcoded `.claude/.credentials.json` ([@9622058#L15-L17](https://github.com/steipete/CodexBar/blob/962205858c34733a9326947335c1f1cf7b25b4e4/Sources/CodexBarCore/Providers/Claude/ClaudeOAuth/ClaudeOAuthCredentials.swift#L15-L17)).
  - 5c2c47f (07-29): identity hash v2 (path) to v3 (profile id), with migration ([#L178-L203](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBar/UsageStore+ClaudeActiveAccountIdentity.swift#L178-L203)).
  - 0954a74 (08-08, #2675): Keychain consent; revoking it retires every profile cache.
  - 0f8489d (09-19, #3585): `claude-owner-v1`.
- **When accounts cannot be told apart:**
  - A legacy profile-less cache entry is credited only to the default `~/.claude` profile ([#L2893-L2900](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeOAuth/ClaudeOAuthCredentials.swift#L2893-L2900)). A test asserts "The profile identity, not freshness, must decide ownership" ([ProfileCacheTests#L196-L236](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Tests/CodexBarTests/ClaudeOAuthCredentialsProfileCacheTests.swift#L196-L236)).
  - A missing identity counts as "unavailable" ([#L60-L72](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBar/UsageStore+ClaudeActiveAccountIdentity.swift#L60-L72)). With no UUID, the session scope is a random one-off value ([ClaudeAccountProfile.swift#L67-L83](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeAccountProfile.swift#L67-L83)). With no owner, usage is returned without an identity ([ClaudeUsageFetcher.swift#L967-L986](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeUsageFetcher.swift#L967-L986)).
  - claude-swap rows sharing an email get `· org` or `· Account N` labels ([ClaudeSwapAccountProjection.swift#L72-L122](https://github.com/steipete/CodexBar/blob/5890d052a3be736a0ae77d4a3afc3be154f9284f/Sources/CodexBarCore/Providers/Claude/ClaudeSwap/ClaudeSwapAccountProjection.swift#L72-L122)).
  - Several Keychain items are never mapped to a dir; the newest-modified one wins.

### Claude-Usage-Tracker ([tracer-rr @ 588775e](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/TerminalLauncherService.swift#L113-L135))

- **Resolution:**
  - Launcher `~/.local/bin/claude-<slug>` exports `CLAUDE_CONFIG_DIR` = `~/.claude-<slug>` and carries the profile UUID in a marker line ([TerminalLauncherService.swift#L113-L135](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/TerminalLauncherService.swift#L113-L135)).
  - The slug is stored at install time, so a rename cannot change the path or its hash ([Profile.swift#L37-L57](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Models/Profile.swift#L37-L57)).
  - "Detection" = the expected `Claude Code-credentials-{sha256(path)[:8]}` appears in the Keychain list, and the profile gets `customKeychainServiceName` pinned ([#L80-L90](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/TerminalLauncherService.swift#L80-L90), [#L150-L173](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/TerminalLauncherService.swift#L150-L173)). The check runs only when the settings card appears or after an install ([TerminalLauncherSection.swift#L160-L173](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Views/Settings/Profile/TerminalLauncherSection.swift#L160-L173)).
  - The README says a launcher "opens Claude Code with that profile's own `CLAUDE_CONFIG_DIR`" and "The app detects the new login and links it to the profile automatically" ([README](https://github.com/hamed-elfayome/Claude-Usage-Tracker)).
- **Credentials:**
  - App secrets live under one Keychain service `com.claudeusagetracker.profile-credentials`, account `<profileUUID>.<field>` ([KeychainService.swift#L169-L189](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/KeychainService.swift#L169-L189)).
  - Claude Code entries: `~/.claude` maps to the plain name, any other dir to `-<SHA256(abs path)[:8]>` ([ClaudeCodeSyncService.swift#L448-L501](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/ClaudeCodeSyncService.swift#L448-L501)).
  - The "system" service resolves from memory, then UserDefaults, then the legacy name, then the first `-*` item a dump-keychain scan finds ([#L279-L337](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/ClaudeCodeSyncService.swift#L279-L337)).
- **Statusline cache:**
  - One `.statusline-usage-cache` in the app's own config dir, written without a lock ([Constants.swift#L107-L112](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Utilities/Constants.swift#L107-L112), [StatuslineService.swift#L1095-L1124](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/StatuslineService.swift#L1095-L1124)).
  - It is written only for the active profile ([MenuBarManager.swift#L1065-L1077](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/MenuBar/MenuBarManager.swift#L1065-L1077)).
  - The bash script hardcodes `$HOME/.claude/.statusline-usage-cache` with a 300 s TTL, then falls back to a Swift script with the active profile's session key baked in ([#L423-L446](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/StatuslineService.swift#L423-L446), [#L840-L881](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/StatuslineService.swift#L840-L881)).
  - README: "Claude Code OAuth doesn't work for statusline - it requires direct session key"; "Instant rendering via usage cache (no startup delay)".
- **History:**
  - 037dac2 (2026-03-08): statusline cache and handling of hashed Keychain names.
  - 2992d4e (04-17): refresh-token equality check against cross-profile contamination.
  - a2769fd (05-18, #239): `customKeychainServiceName` pin.
  - eee15cf (07-08): single-writer token model; identity by `accountUuid`, then email.
  - ba99f58 (07-09, GHSA-mfxh-xpwm-23c7): secrets moved from the cleartext plist to the Keychain, keyed `<uuid>.<field>` ([ProfileStore.swift#L86-L117](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Storage/ProfileStore.swift#L86-L117)).
  - 7061028 (08-29, #290): per-profile launchers with auto-pin.
- **When accounts cannot be told apart:**
  - Identity is `accountUuid`, then `emailAddress` ([#L703-L715](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/ClaudeCodeSyncService.swift#L703-L715)).
  - Switching an unpinned profile overwrites the shared system Keychain entry, `.credentials.json` and `oauthAccount` ([#L788-L819](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/ClaudeCodeSyncService.swift#L788-L819)).
  - `resyncBeforeSwitching` skips when identities differ, compares refresh tokens when either identity is missing, and skips setup tokens as "account identity unverifiable" ([#L1209-L1280](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/ClaudeCodeSyncService.swift#L1209-L1280)).
  - `writeSystemCredentials` targets `resolveServiceName()`, which is not tied to a config dir ([#L617-L640](https://github.com/hamed-elfayome/Claude-Usage-Tracker/blob/588775e4540757443691fa1bcb33457315e05f86/Claude%20Usage/Shared/Services/ClaudeCodeSyncService.swift#L617-L640)).
  - Every statusline session shows the active profile's usage, whatever its `CLAUDE_CONFIG_DIR`.

### oh-my-claudecode ([tracer-rr @ 9fd35ec](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/utils/config-dir.ts#L36-L53))

- **Resolution:**
  - `process.env.CLAUDE_CONFIG_DIR`, trimmed, `~` expanded and normalized, else `~/.claude` ([config-dir.ts#L36-L53](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/utils/config-dir.ts#L36-L53)).
  - Stdin supplies only rate limits and `version` ([index.ts#L359-L366](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/index.ts#L359-L366)).
  - The label is `basename(CLAUDE_CONFIG_DIR)` ([#L512-L514](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/index.ts#L512-L514)).
  - An env `CLAUDE_CODE_OAUTH_TOKEN` overrides the Keychain and the file ([usage-api.ts#L777-L795](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/usage-api.ts#L777-L795)).
- **Credentials:**
  - Keychain `Claude Code-credentials-${sha256(raw env value)[:8]}`, or the plain name when unset ([#L652-L667](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/usage-api.ts#L652-L667)).
  - The OS-user account first, then service-only, then an expired entry ([#L710-L741](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/usage-api.ts#L710-L741)).
  - Then `{configDir}/.credentials.json` ([#L746-L749](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/usage-api.ts#L746-L749)).
  - Refreshed tokens are written back to the same entry ([#L1167-L1171](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/usage-api.ts#L1167-L1171)).
- **Keys:**
  - Cache `{configDir}/plugins/oh-my-claudecode/.usage-cache-${source}.json` ([#L312-L323](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/usage-api.ts#L312-L323)); lock = cache path + `.lock` ([file-lock.ts#L86-L88](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/lib/file-lock.ts#L86-L88)).
  - The 429 backoff is stored in the cache, keyed by User-Agent identity ([#L487-L510](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/usage-api.ts#L487-L510)).
  - `credentialIdentity` = sha256(token), only for env-token sessions ([#L534-L544](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/usage-api.ts#L534-L544)).
- **History:**
  - d766030 (2026-02-11, #559): `CLAUDE_CONFIG_DIR` across OMC.
  - 110f37e (02-27, #1125): hashed Keychain service.
  - 5389758 (03-05): 429 backoff.
  - 407eb28 (03-06): `withFileLock`, 15 s stale limit.
  - b268fe2 (03-16): username-first lookup.
  - b8948f6 (04-04): `~` expansion; raw-value hash documented.
  - 0878cea (04-12): per-provider cache and lock split.
  - bc29db0, f1cc749, 06fc4a1 (08-25/26): per-UA backoff.
  - 023d56c (09-10, #4004): `CLAUDE_CODE_OAUTH_TOKEN` and `credentialIdentity`.
  - Header credits "Based on claude-hud implementation by jarrodwatts".
- **When accounts cannot be told apart:**
  - Keychain and file sessions carry `credentialIdentity` undefined, so two logins under one dir share a cache, lock and backoff. Only env-token sessions are separated ([test#L467-L510](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/__tests__/hud/usage-api.test.ts#L467-L510)).
  - It never falls back from the hashed service to the default one.
  - With the lock held, it shows the stale cache ([#L2266-L2276](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/hud/usage-api.ts#L2266-L2276)).
  - Unverified inference: the cache path normalizes the dir but the Keychain hash uses the raw value, so `~/.claude-x` and its absolute form share a cache but read different Keychain entries ([test#L512-L518](https://github.com/Yeachan-Heo/oh-my-claudecode/blob/9fd35ece5d6de65b511bf43b55e42c499e4fc194/src/__tests__/hud/usage-api.test.ts#L512-L518)).

## Coverage

- claude-hud: settled
- ccstatusline: settled
- CodexBar: settled
- Claude-Usage-Tracker: settled
- oh-my-claudecode: settled

Digging ended: every sub-area settled after round 1.

tracer-rr result files:
- tracer-rr/jarrodwatts-claude-hud-2026-09-26-4.md
- tracer-rr/sirmalloc-ccstatusline-2026-09-26-3.md
- tracer-rr/steipete-CodexBar-2026-09-26-2.md
- tracer-rr/hamed-elfayome-Claude-Usage-Tracker-2026-09-26-2.md
- tracer-rr/Yeachan-Heo-oh-my-claudecode-2026-09-26-2.md

Telemetry: 5 diggers dispatched, 5 reports received.

## Verification

5 web facts checked on 2 pages (claude-hud README: 1; Claude-Usage-Tracker README: 4), all confirmed. No fact was NOT ON PAGE or UNCHECKED. Code facts carry commit-pinned tracer-rr links and were not re-fetched. Two claims are tracer inferences, marked unverified inline: ccstatusline's cross-profile cache thrash, and oh-my-claudecode's raw-vs-normalized hash split.

## Open rabbit holes

- ccstatusline issues #521 (macOS profile showing the default account's usage), #459 (stale usage after an account switch) and PR #536 (token lifetimes behind the refresh-token fingerprint): undug
- claude-hud issue #126 (the CLAUDE_CONFIG_DIR report) and PR #288 (why OAuth polling was dropped): undug
- claude-hud's `${configDir}.json` account-file location versus Claude Code's real layout for custom dirs: dug, unsettled
- Claude-Usage-Tracker: no found guard against two profiles pinning the same account or Keychain service; issues #239, #267, #290, #175, #179; the `cux` switcher: dug, unsettled / undug
- CodexBar: claude-swap (`cswap`) internals; the CHANGELOG entries on CLAUDE_CONFIG_DIR: undug
- oh-my-claudecode: diffs of 5389758 and 06fc4a1 not read: dug, unsettled
